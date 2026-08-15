"""Provider adapters extracted from the OPower and Envoy integrations."""

from __future__ import annotations

import hashlib
import ipaddress
import re
from datetime import datetime, timezone
from typing import Any


def envoy_password(user: str, realm: str, serial_number: str) -> str:
    digest = hashlib.md5(f"[e]{user}@{realm}#{serial_number} EnPhAsE eNeRgY ".encode()).hexdigest()
    zeros, ones, result = digest.count("0"), digest.count("1"), []
    for char in digest[-1 : len(digest) - 9 : -1]:
        if zeros in (3, 6, 9): zeros -= 1
        zeros = max(0, min(20, zeros))
        if ones in (9, 15): ones -= 1
        ones = max(0, min(26, ones))
        if char == "0": result.append(chr(ord("f") + zeros)); zeros -= 1
        elif char == "1": result.append(chr(ord("@") + ones)); ones -= 1
        else: result.append(char)
    return "".join(result)


def parse_envoy_serial(payload: str) -> str | None:
    """Read device serial from the Envoy info XML without retaining the XML."""
    match = re.search(r"<device_sn>\s*([^<\s]+)", payload, re.I)
    if match:
        return match.group(1)
    match = re.search(r"<device[^>]+sn=[\"']([^\"']+)", payload, re.I)
    return match.group(1) if match else None


def discovery_hosts(cidrs: str) -> list[str]:
    """Expand comma-separated CIDRs/IPs into deterministic host addresses."""
    hosts: list[str] = []
    for item in cidrs.split(","):
        network = ipaddress.ip_network(item.strip(), strict=False)
        hosts.extend(str(ip) for ip in network.hosts())
    return hosts


def _number(value: Any) -> float | None:
    try: return None if value is None else float(value)
    except (TypeError, ValueError): return None


def extract_envoy_readings(payload: dict[str, Any], channel_prefix: str) -> list[dict[str, Any]]:
    meters = payload.get("meters", payload)
    eim = meters.get("eim", {}) if isinstance(meters, dict) else {}
    production = meters.get("production", {}) if isinstance(meters, dict) else {}
    candidates = {
        "production": production.get("power", production.get("w_now")),
        "grid_net": eim.get("net_consumption", eim.get("net_consumption_w")),
        "consumption": eim.get("total_consumption", eim.get("total_consumption_w")),
    }
    return [
        {"source": "enphase", "channel": f"{channel_prefix}:{name}", "watts": value}
        for name, raw in candidates.items()
        if (value := _number(raw)) is not None
    ]


def parse_opower_reads(reads: list[Any], source: str, channel: str) -> list[dict[str, Any]]:
    result = []
    for read in reads:
        start = read["start_time"] if isinstance(read, dict) else read.start_time
        end = read["end_time"] if isinstance(read, dict) else read.end_time
        kwh = read["consumption"] if isinstance(read, dict) else read.consumption
        if isinstance(start, str): start = datetime.fromisoformat(start)
        if isinstance(end, str): end = datetime.fromisoformat(end)
        if start.tzinfo is None: start = start.replace(tzinfo=timezone.utc)
        if end.tzinfo is None: end = end.replace(tzinfo=timezone.utc)
        seconds = (end - start).total_seconds()
        result.append({"source": source, "channel": channel, "ts": end.astimezone(timezone.utc).isoformat(), "kwh": float(kwh), "watts": float(kwh) * 3600 / seconds * 1000 if seconds > 0 else None})
    return result


# Channels the usage API can report per monitor. Computed totals (TotalUsage,
# Balance) duplicate Mains and are excluded; everything else is a real sensor.
_EMPORIA_KEEP_CHANNEL = re.compile(r"^(Mains|Mains_[ABC]|MainsFromGrid|MainsToGrid|Branch_\d+)$")
_EMPORIA_COMPUTED = {"TotalUsage", "Balance"}


def emporia_interval_ts(instant: str, scale: str) -> str:
    """Floor an ISO instant to the start of its HOUR/DAY bucket in UTC."""
    dt = datetime.fromisoformat(instant.replace("Z", "+00:00")).astimezone(timezone.utc)
    if scale == "DAY":
        dt = dt.replace(hour=0, minute=0, second=0, microsecond=0)
    else:
        dt = dt.replace(minute=0, second=0, microsecond=0)
    return dt.isoformat()


def parse_emporia_devices(payload: dict[str, Any]) -> list[dict[str, Any]]:
    """Normalize /v1/customers/devices into deduplicated devices with channels."""
    seen: dict[int, dict[str, Any]] = {}
    for dev in payload.get("devices", payload if isinstance(payload, list) else []):
        gid = dev.get("device_gid")
        if gid is None or gid in seen:
            continue
        seen[gid] = {
            "gid": gid,
            "device_id": dev.get("device_id"),
            "name": dev.get("display_name") or f"emporia-{gid}",
            "model": dev.get("model"),
            "timezone": dev.get("time_zone"),
            "channels": {
                ch["channel_id"]: {
                    "classification": ch.get("channel_classification"),
                    "has_data": bool(ch.get("has_data")),
                }
                for ch in dev.get("channels", [])
                if ch.get("channel_id")
            },
        }
    return list(seen.values())


def parse_emporia_usages(payload: dict[str, Any], device: dict[str, Any]) -> list[dict[str, Any]]:
    """Flatten /v1/customers/devices/usages into storable hourly readings.

    Usage values are cumulative within the interval containing ``instant``;
    the returned ts is the interval start, so later polls overwrite the same
    row until the bucket completes.
    """
    instant = payload.get("instant")
    scale = payload.get("scale", "HOUR")
    if not instant:
        return []
    ts = emporia_interval_ts(instant, scale)
    result: list[dict[str, Any]] = []
    for device_usage in payload.get("device_usages", []):
        for channel in device_usage.get("channel_usages", []):
            channel_id = channel.get("channel_id")
            usage = channel.get("usage")
            if not channel_id or channel_id in _EMPORIA_COMPUTED:
                continue
            if not _EMPORIA_KEEP_CHANNEL.match(channel_id) or usage is None:
                continue
            result.append({
                "source": "emporia",
                "channel": f"{device['name']}:{channel_id}",
                "ts": ts,
                "kwh": float(usage),
                "channel_id": channel_id,
            })
    return result
