import json

import pytest


@pytest.fixture()
def client(monkeypatch, tmp_path):
    monkeypatch.setenv("DATA_DIR", str(tmp_path))
    import app as power_app

    power_app.app.config.update(TESTING=True)
    with power_app.app.test_client() as test_client:
        yield test_client


def rpc(client, method, params=None, request_id=1):
    response = client.post(
        "/mcp",
        json={"jsonrpc": "2.0", "id": request_id, "method": method, "params": params or {}},
        headers={"Accept": "application/json, text/event-stream"},
    )
    assert response.status_code == 200
    return response.get_json()


def test_streamable_http_initialize_and_tools_list(client):
    initialized = rpc(
        client,
        "initialize",
        {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "test", "version": "1"}},
    )
    assert initialized["result"]["protocolVersion"] == "2025-03-26"
    assert initialized["result"]["serverInfo"]["name"] == "power-monitor"

    response = client.post(
        "/mcp",
        json={"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}},
        headers={"Accept": "application/json, text/event-stream"},
    )
    assert response.status_code == 202

    listed = rpc(client, "tools/list")
    names = {tool["name"] for tool in listed["result"]["tools"]}
    assert names == {"status", "devices", "usage", "report"}


def test_streamable_http_tool_call_uses_existing_status_route(client, monkeypatch):
    monkeypatch.setenv("EMPORIA_EMAIL", "configured@example.test")
    monkeypatch.setenv("EMPORIA_PASSWORD", "not-a-real-secret")
    called = {"count": 0}

    def fake_status():
        called["count"] += 1
        return {"version": 1, "service": "power-monitor", "providers": {"emporia": True}}

    monkeypatch.setattr("mcp_server.status_payload", fake_status)
    expected = fake_status()
    called["count"] = 0
    payload = rpc(client, "tools/call", {"name": "status", "arguments": {}})
    assert payload["result"]["isError"] is False
    content = payload["result"]["content"]
    assert json.loads(content[0]["text"]) == expected
    assert called["count"] == 1


def test_streamable_http_rejects_malformed_or_unknown_requests(client):
    malformed = client.post("/mcp", json={"jsonrpc": "2.0", "id": 1, "method": "nope"})
    assert malformed.status_code == 200
    assert malformed.get_json()["error"]["code"] == -32601

    invalid = client.post("/mcp", json={"jsonrpc": "2.0", "id": 2})
    assert invalid.status_code == 400
    assert invalid.get_json()["error"]["code"] == -32600

    wrong_method = client.get("/mcp")
    assert wrong_method.status_code == 405
