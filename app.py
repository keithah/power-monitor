from __future__ import annotations

import asyncio, json, os, sqlite3, threading, time
from datetime import datetime, timezone, timedelta
from flask import Flask, jsonify, request
import requests
from requests.auth import HTTPDigestAuth
from providers import (
    discovery_hosts,
    envoy_password,
    extract_envoy_readings,
    parse_emporia_devices,
    parse_emporia_usages,
    parse_envoy_serial,
    parse_opower_reads,
)
from provider_adapters import EmporiaProvider

def _select_data_dir():
    """Use the container data path, with a writable local-dev fallback."""
    configured = os.getenv('DATA_DIR')
    if configured:
        os.makedirs(configured, exist_ok=True)
        return configured
    try:
        os.makedirs('/data', exist_ok=True)
        return '/data'
    except OSError:
        local = os.path.abspath('data')
        os.makedirs(local, exist_ok=True)
        return local


DATA_DIR = _select_data_dir()
DB = os.path.join(DATA_DIR, 'solar.sqlite3')
PORT = int(os.getenv('PORT', '8080'))
app = Flask(__name__)
lock = threading.Lock()
_DISCOVERY_CACHE = {'expires': 0.0, 'systems': []}
_PGE_MFA = {'loop': None, 'thread': None, 'session': None, 'api': None, 'handler': None}
_EMPORIA_CLIENT = None
_EMPORIA_API = 'https://api.emporiaenergy.com'


def _public_envoy_system(system):
    """Return only non-credential Envoy metadata for API responses."""
    return {
        key: system[key]
        for key in ('name', 'host', 'serial', 'cloud', 'site_id')
        if key in system and system[key] is not None
    }


def _jwt_exp(token: str) -> float | None:
    """Return the exp claim of a JWT without verifying it."""
    try:
        import base64
        payload = token.split('.')[1]
        payload += '=' * (-len(payload) % 4)
        return float(json.loads(base64.urlsafe_b64decode(payload))['exp'])
    except Exception:
        return None


class _EmporiaClient:
    """Thin client for the Emporia v1 API using Cognito tokens from pyemvue."""

    def __init__(self, email: str, password: str):
        self.email = email
        self.password = password
        self._vue = None

    def _ensure_tokens(self):
        from pyemvue.pyemvue import PyEmVue
        if self._vue is None:
            self._vue = PyEmVue(connect_timeout=6, read_timeout=15)
            self._vue.login(self.email, self.password)
            return
        exp = _jwt_exp(self._vue.auth.tokens.get('id_token', ''))
        if exp is None or exp - time.time() < 60:
            try:
                self._vue.auth.refresh_tokens()
            except Exception:
                self._vue = PyEmVue(connect_timeout=6, read_timeout=15)
                self._vue.login(self.email, self.password)

    def now(self) -> str:
        return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace('+00:00', 'Z')

    def get(self, path: str, params: dict | None = None) -> dict:
        self._ensure_tokens()
        response = requests.get(_EMPORIA_API + path, params=params, timeout=60,
                                headers={'Authorization': 'Bearer ' + self._vue.auth.tokens['id_token'],
                                         'accept': 'application/json'})
        response.raise_for_status()
        return response.json()


def _pge_loop():
    if _PGE_MFA['loop'] is None:
        loop = asyncio.new_event_loop()
        _PGE_MFA['loop'] = loop
        _PGE_MFA['thread'] = threading.Thread(target=loop.run_forever, daemon=True)
        _PGE_MFA['thread'].start()
    return _PGE_MFA['loop']


def _pge_run(coro):
    future = asyncio.run_coroutine_threadsafe(coro, _pge_loop())
    return future.result(timeout=45)


def db():
    c = sqlite3.connect(DB)
    c.row_factory = sqlite3.Row
    c.execute('''CREATE TABLE IF NOT EXISTS readings (
      ts TEXT NOT NULL, source TEXT NOT NULL, channel TEXT NOT NULL,
      watts REAL, kwh REAL, raw TEXT, PRIMARY KEY(ts,source,channel))''')
    c.commit(); return c


def put(ts, source, channel, watts=None, kwh=None, raw=None):
    with lock, db() as c:
        c.execute('INSERT OR REPLACE INTO readings VALUES (?,?,?,?,?,?)',
                  (ts, source, channel, watts, kwh, json.dumps(raw) if raw else None))
        c.commit()


def iso(dt): return dt.astimezone(timezone.utc).replace(microsecond=0).isoformat()


def _enlighten_login():
    user = os.getenv('ENPHASE_EMAIL') or os.getenv('ENPHASE_USERNAME')
    password = os.getenv('ENPHASE_PASSWORD')
    if not user or not password:
        return None
    session = requests.Session()
    response = session.post(
        'https://enlighten.enphaseenergy.com/login/login.json',
        data={'user[email]': user, 'user[password]': password}, timeout=20)
    response.raise_for_status()
    body = response.json() if response.content else {}
    session_id = body.get('session_id') or session.cookies.get('_enlighten_4_session')
    if not session_id:
        raise RuntimeError('Enphase login succeeded but returned no session')
    return session, user, session_id


def _gateway_token(login, serial):
    if not login:
        return None
    session, user, session_id = login
    response = session.post(
        'https://entrez.enphaseenergy.com/tokens',
        json={'session_id': session_id, 'serial_num': serial, 'username': user}, timeout=20)
    if response.ok:
        # The current endpoint returns a bare JWT; older versions returned JSON.
        try:
            body = response.json()
            return body.get('token') if isinstance(body, dict) else str(body)
        except ValueError:
            return response.text.strip() or None
    response = session.get('https://enlighten.enphaseenergy.com/entrez-auth-token',
                           params={'serial_num': serial}, timeout=20)
    if not response.ok:
        return None
    try:
        body = response.json()
        return body.get('token') if isinstance(body, dict) else None
    except ValueError:
        return response.text.strip() or None


def _discover_envoys():
    """Discover all account systems from Enlighten, with LAN fallback."""
    now = time.time()
    if _DISCOVERY_CACHE['expires'] > now:
        return _DISCOVERY_CACHE['systems']
    login = _enlighten_login()
    if not login:
        return []
    session, user, session_id = login
    try:
        sites = session.get('https://enlighten.enphaseenergy.com/app-api/user_sites.json', timeout=20).json().get('sites', [])
    except (requests.RequestException, ValueError):
        sites = []
    systems = []
    for site in sites:
        site_id = str(site.get('id', '')).strip()
        if not site_id:
            continue
        try:
            payload = session.get(f'https://enlighten.enphaseenergy.com/app-api/{site_id}/data.json', timeout=20).json()
            devices = payload.get('state', {}).get('devices', [])
        except (requests.RequestException, ValueError):
            devices = []
        serial = next((str(d.get('serialNumber')) for d in devices if d.get('serialNumber')), None)
        systems.append({'name': site.get('name') or f'enphase_{site_id}', 'cloud': True,
                        'site_id': site_id, 'serial': serial, 'session': session})
    if systems:
        _DISCOVERY_CACHE.update(expires=now + 300, systems=systems)
        return systems
    # Fallback for installations where account site listing is unavailable.
    cidrs = os.getenv('ENPHASE_DISCOVERY_CIDRS', '192.168.1.0/24')
    timeout = float(os.getenv('ENPHASE_DISCOVERY_TIMEOUT', '0.7'))
    found = []
    from concurrent.futures import ThreadPoolExecutor, as_completed
    def probe(host):
        for scheme in ('http', 'https'):
            try:
                response = requests.get(f'{scheme}://{host}/info.xml', timeout=timeout, verify=False)
                if response.ok:
                    serial = parse_envoy_serial(response.text)
                    if serial:
                        return {'name': f'enphase_{serial}', 'host': f'{scheme}://{host}',
                                'serial': serial, 'token': _gateway_token(login, serial)}
            except requests.RequestException:
                pass
        return None
    with ThreadPoolExecutor(max_workers=32) as pool:
        for future in as_completed([pool.submit(probe, h) for h in discovery_hosts(cidrs)]):
            item = future.result()
            if item: found.append(item)
    systems = sorted(found, key=lambda item: item['serial'])
    _DISCOVERY_CACHE.update(expires=now + 300, systems=systems)
    return systems


def envoy_systems():
    raw = os.getenv('ENVOY_SYSTEMS', '').strip()
    if raw:
        return json.loads(raw)
    systems = []
    for i in range(1, 10):
        host = os.getenv(f'ENVOY_SYSTEM_{i}_HOST')
        if host:
            systems.append({'name': os.getenv(f'ENVOY_SYSTEM_{i}_NAME', f'envoy_{i}'), 'host': host,
                'token': os.getenv(f'ENVOY_SYSTEM_{i}_TOKEN'), 'serial': os.getenv(f'ENVOY_SYSTEM_{i}_SERIAL'),
                'user': os.getenv(f'ENVOY_SYSTEM_{i}_USER', 'envoy'), 'password': os.getenv(f'ENVOY_SYSTEM_{i}_PASSWORD'),
                'enlighten_user': os.getenv(f'ENVOY_SYSTEM_{i}_ENLIGHTEN_USER'), 'enlighten_password': os.getenv(f'ENVOY_SYSTEM_{i}_ENLIGHTEN_PASSWORD')})
    return systems or _discover_envoys()


def _envoy_session(system):
    session = requests.Session(); session.verify = os.getenv('ENVOY_VERIFY_TLS', '0') == '1'
    token = system.get('token')
    if token:
        session.headers['Authorization'] = f'Bearer {token}'; return session
    password = system.get('password')
    if not password and system.get('serial'):
        password = envoy_password('envoy', 'enphaseenergy.com', system['serial'])
    if password: session.auth = HTTPDigestAuth(system.get('user', 'envoy'), password)
    return session


def enphase():
    systems = envoy_systems()
    if not systems: return {'status': 'not_configured'}
    count = 0
    for system in systems:
        name = system.get('name', 'envoy')
        if system.get('cloud'):
            site = system['site_id']; client = system.get('session') or _enlighten_login()[0]
            payload = client.get(f'https://enlighten.enphaseenergy.com/pv/systems/{site}/today', timeout=30).json()
            stat = (payload.get('stats') or [{}])[0]
            values = stat.get('production') or []
            now = iso(datetime.now(timezone.utc))
            watts = float(values[-1]) if values else None
            total = (stat.get('totals') or {}).get('production')
            put(now, 'enphase', f'{name}:production', watts=watts, kwh=float(total) if total is not None else None, raw=payload)
            count += 1; continue
        host = system['host'].rstrip('/'); client = _envoy_session(system); responses = []
        for path in ('/production.json?details=1', '/ivp/meters/readings', '/api/v1/production'):
            response = client.get(host + path, timeout=20)
            if response.ok: responses.append(response.json())
        if not responses: raise RuntimeError(f'Envoy {name} returned no readable data')
        for payload in responses:
            readings = extract_envoy_readings(payload, name)
            now = iso(datetime.now(timezone.utc))
            for reading in readings: put(now, reading['source'], reading['channel'], watts=reading['watts'], raw=payload)
        count += 1
    return {'status': 'ok', 'systems': count}


def emporia():
    email=os.getenv('EMPORIA_EMAIL'); password=os.getenv('EMPORIA_PASSWORD')
    if not email or not password: return {'status':'not_configured'}
    global _EMPORIA_CLIENT
    if _EMPORIA_CLIENT is None:
        _EMPORIA_CLIENT = _EmporiaClient(email, password)
    provider = EmporiaProvider(_EMPORIA_CLIENT)
    readings = provider.usage()
    for reading in readings:
        put(reading.timestamp.isoformat(), reading.provider, reading.channel,
            kwh=reading.kwh, raw={'device_id': reading.device_id, 'unit': 'kWh', 'scale': 'HOUR'})
    return {'status':'ok','devices':len(provider.devices()),'readings':len(readings)}


def pge():
    username = os.getenv('PGE_USERNAME') or os.getenv('PGE_EMAIL'); password = os.getenv('PGE_PASSWORD')
    if not username or not password: return {'status': 'not_configured'}
    import aiohttp
    from opower import AggregateType, Opower, create_cookie_jar
    async def collect_opower():
        async with aiohttp.ClientSession(cookie_jar=create_cookie_jar()) as session:
            login_data = {}
            login_path = os.path.join(DATA_DIR, 'pge-login.json')
            try:
                with open(login_path, encoding='utf-8') as fh: login_data = json.load(fh)
            except (FileNotFoundError, json.JSONDecodeError): pass
            api = Opower(session, os.getenv('PGE_UTILITY', 'Pacific Gas and Electric Company (PG&E)'), username, password, login_data=login_data)
            await api.async_login(); accounts = await api.async_get_accounts(); total = 0
            from opower import AggregateType
            for account in accounts:
                resolution = getattr(account.read_resolution, 'value', str(account.read_resolution or 'DAY')).upper()
                aggregate = {'QUARTER_HOUR': AggregateType.QUARTER_HOUR, 'FIVE_MINUTE': AggregateType.QUARTER_HOUR,
                             'HALF_HOUR': AggregateType.HALF_HOUR, 'HOUR': AggregateType.HOUR,
                             'DAY': AggregateType.DAY, 'BILLING': AggregateType.BILL}.get(resolution, AggregateType.DAY)
                start = None if aggregate == AggregateType.BILL else datetime.now(timezone.utc)-timedelta(days=2)
                end = None if aggregate == AggregateType.BILL else datetime.now(timezone.utc)
                reads = await api.async_get_usage_reads(account, aggregate, start, end)
                for item in parse_opower_reads(reads, 'pge', account.utility_account_id):
                    put(item['ts'], item['source'], item['channel'], watts=item['watts'], kwh=item['kwh'], raw={'account': account.utility_account_id, 'start': item['ts']}); total += 1
            return {'status':'ok','accounts':len(accounts),'readings':total}
    return asyncio.run(collect_opower())


def collect():
    results={}
    try: results['enphase']=enphase()
    except Exception as e: results['enphase']={'status':'error','error':str(e)[:300]}
    try: results['emporia']=emporia()
    except Exception as e: results['emporia']={'status':'error','error':str(e)[:300]}
    try: results['pge']=pge()
    except Exception as e: results['pge']={'status':'error','error':str(e)[:300]}
    return results


@app.get('/health')
def health(): return jsonify(ok=True, service='power-monitor')

@app.get('/api/status')
def status():
    enphase_configured = bool(os.getenv('ENPHASE_' + 'PASSWORD') and (os.getenv('ENPHASE_' + 'USERNAME') or os.getenv('ENPHASE_' + 'EMAIL')))
    emporia_configured = bool(os.getenv('EMPORIA_' + 'EMAIL') and os.getenv('EMPORIA_' + 'PASSWORD'))
    pge_configured = bool(os.getenv('PGE_' + 'USERNAME') and os.getenv('PGE_' + 'PASSWORD'))
    return jsonify(version=1, service='power-monitor', providers={'enphase': enphase_configured, 'emporia': emporia_configured, 'pge_opower': pge_configured})

@app.get('/api/enphase/systems')
def enphase_systems():
    try:
        systems = envoy_systems()
        return jsonify(status='ok' if systems else 'not_configured', systems=[_public_envoy_system(s) for s in systems]), (200 if systems else 503)
    except Exception as e: return jsonify(status='error', error=str(e)[:300]), 502


@app.get('/api/devices')
def devices():
    """Return configured electricity devices without credentials or raw payloads."""
    requested = request.args.get('provider')
    if requested and requested not in ('enphase', 'emporia'):
        return jsonify(status='error', error=f'unknown provider {requested!r}'), 400
    result = {'version': 1, 'providers': {}}
    try:
        systems = envoy_systems()
        if not requested or requested == 'enphase':
            result['providers']['enphase'] = [_public_envoy_system(s) for s in systems]
    except Exception as exc:
        result['providers']['enphase'] = {'status': 'error', 'error': str(exc)[:300]}
    if (not requested or requested == 'emporia') and os.getenv('EMPORIA_EMAIL') and os.getenv('EMPORIA_PASSWORD'):
        try:
            email = os.getenv('EMPORIA_EMAIL')
            password = os.getenv('EMPORIA_PASSWORD')
            assert email and password
            client = _EMPORIA_CLIENT or _EmporiaClient(email, password)
            result['providers']['emporia'] = EmporiaProvider(client).devices()
        except Exception as exc:
            result['providers']['emporia'] = {'status': 'error', 'error': str(exc)[:300]}
    return jsonify(result)


@app.get('/api/usage')
def usage():
    """Return stored normalized usage rows, filtered by provider when requested."""
    source = request.args.get('provider')
    try:
        limit = min(max(int(request.args.get('limit', '500')), 1), 5000)
    except ValueError:
        return jsonify(status='error', error='limit must be an integer'), 400
    query = 'SELECT ts, source, channel, watts, kwh FROM readings'
    params = []
    if source:
        query += ' WHERE source=?'
        params.append(source)
    query += ' ORDER BY ts DESC LIMIT ?'
    params.append(limit)
    with db() as c:
        rows = [dict(r) for r in c.execute(query, params)]
    return jsonify(version=1, provider=source, rows=rows)


@app.post('/api/pge/mfa/start')
def pge_mfa_start():
    """Start PG&E login and return masked email/phone delivery choices."""
    username = os.getenv('PGE_USERNAME') or os.getenv('PGE_EMAIL'); password = os.getenv('PGE_PASSWORD')
    if not username or not password: return jsonify(status='not_configured'), 503
    async def start():
        import aiohttp
        from opower import Opower
        from opower.exceptions import MfaChallenge
        session = aiohttp.ClientSession(cookie_jar=aiohttp.CookieJar())
        api = Opower(session, os.getenv('PGE_UTILITY', 'Pacific Gas and Electric Company (PG&E)'), username, password)
        try:
            await api.async_login()
            await session.close()
            return {'status': 'ok', 'message': 'PG&E login did not require MFA'}
        except MfaChallenge as exc:
            _PGE_MFA.update(session=session, api=api, handler=exc.handler)
            return {'status': 'mfa_required', 'options': await exc.handler.async_get_mfa_options()}
    try: return jsonify(_pge_run(start()))
    except Exception as exc: return jsonify(status='error', error=str(exc)[:300]), 502


@app.post('/api/pge/mfa/select')
def pge_mfa_select():
    option = (request.get_json(silent=True) or {}).get('option')
    handler = _PGE_MFA.get('handler')
    if not handler: return jsonify(status='error', error='Start MFA first'), 409
    if option not in ('Email', 'Phone'): return jsonify(status='error', error='option must be Email or Phone'), 400
    try:
        _pge_run(handler.async_select_mfa_option(option))
        return jsonify(status='code_sent', option=option)
    except Exception as exc: return jsonify(status='error', error=str(exc)[:300]), 502


@app.post('/api/pge/mfa/verify')
def pge_mfa_verify():
    code = str((request.get_json(silent=True) or {}).get('code', '')).strip()
    handler = _PGE_MFA.get('handler')
    if not handler: return jsonify(status='error', error='Start MFA first'), 409
    if not code or len(code) > 32: return jsonify(status='error', error='A valid MFA code is required'), 400
    try:
        login_data = _pge_run(handler.async_submit_mfa_code(code))
        os.makedirs(DATA_DIR, exist_ok=True)
        path = os.path.join(DATA_DIR, 'pge-login.json')
        with open(path, 'w', encoding='utf-8') as fh: json.dump(login_data, fh)
        os.chmod(path, 0o600)
        _pge_run(_PGE_MFA['session'].close())
        _PGE_MFA.update(session=None, api=None, handler=None)
        return jsonify(status='ok', message='PG&E MFA verified and login session saved')
    except Exception as exc: return jsonify(status='error', error=str(exc)[:300]), 502

@app.post('/api/collect')
def api_collect(): return jsonify(collect())

@app.get('/api/report')
def report():
    with db() as c: rows=[dict(r) for r in c.execute('SELECT * FROM readings ORDER BY ts DESC LIMIT 500')]
    return jsonify(rows=rows)


from mcp_server import register_mcp
register_mcp(app)


def _collector_loop(interval: int):
    while True:
        time.sleep(interval)
        try:
            collect()
        except Exception:
            pass


if __name__ == '__main__':
    interval = int(os.getenv('COLLECT_INTERVAL_SECONDS', '0') or '0')
    if interval > 0:
        threading.Thread(target=_collector_loop, args=(interval,), daemon=True).start()
    app.run(host='0.0.0.0', port=PORT)
