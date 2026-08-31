import json

import app as service
import cli


def test_devices_endpoint_never_serializes_credential_fields(monkeypatch):
    monkeypatch.setattr(
        service,
        "envoy_systems",
        lambda: [
            {
                "name": "panel",
                "host": "http://envoy",
                "serial": "abc",
                "user": "envoy",
                "password": "fixture-password",
                "token": "fixture-token",
                "enlighten_user": "user@example.test",
                "enlighten_password": "fixture-enlighten-password",
                "session": object(),
            }
        ],
    )

    with service.app.test_client() as client:
        response = client.get("/api/devices?provider=enphase")

    assert response.status_code == 200
    payload = response.get_json()
    encoded = json.dumps(payload)
    assert payload["providers"]["enphase"] == [
        {"name": "panel", "host": "http://envoy", "serial": "abc"}
    ]
    for secret in ("password", "token", "session", "enlighten_password"):
        assert secret not in encoded


def test_cli_url_encodes_provider_filter(monkeypatch):
    captured = []
    monkeypatch.setattr(cli, "request", lambda _base, path: captured.append(path) or {})
    monkeypatch.setattr(
        "sys.argv",
        ["power-monitor", "devices", "--provider", "enphase&other=value#fragment"],
    )

    assert cli.main() == 0
    assert captured == [
        "/api/devices?provider=enphase%26other%3Dvalue%23fragment"
    ]
