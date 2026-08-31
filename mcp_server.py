"""Native stateless MCP Streamable HTTP adapter for power-monitor."""

from __future__ import annotations

import json
import os
from typing import Any, Callable

from flask import jsonify, request

PROTOCOL_VERSION = "2025-03-26"
TOOLS = {
    "status": {
        "description": "Return configured provider status without secrets.",
        "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
    },
    "devices": {
        "description": "Return configured Enphase systems and Emporia devices.",
        "inputSchema": {
            "type": "object",
            "properties": {"provider": {"type": "string", "enum": ["enphase", "emporia"]}},
            "additionalProperties": False,
        },
    },
    "usage": {
        "description": "Return stored normalized electricity usage readings.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "provider": {"type": "string", "enum": ["enphase", "emporia", "pge"]},
                "limit": {"type": "integer", "minimum": 1, "maximum": 5000, "default": 500},
            },
            "additionalProperties": False,
        },
    },
    "report": {
        "description": "Return recent stored readings without raw provider payloads.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "source": {"type": "string", "enum": ["enphase", "emporia", "pge"]},
                "limit": {"type": "integer", "minimum": 1, "maximum": 500, "default": 50},
            },
            "additionalProperties": False,
        },
    },
}


def _error(request_id: Any, code: int, message: str) -> dict[str, Any]:
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}}


def _result(request_id: Any, result: Any) -> dict[str, Any]:
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def status_payload() -> dict[str, Any]:
    return {
        "version": 1,
        "service": "power-monitor",
        "providers": {
            "enphase": bool(os.getenv("ENPHASE_PASSWORD") and (os.getenv("ENPHASE_USERNAME") or os.getenv("ENPHASE_EMAIL"))),
            "emporia": bool(os.getenv("EMPORIA_EMAIL") and os.getenv("EMPORIA_PASSWORD")),
            "pge_opower": bool(os.getenv("PGE_USERNAME") and os.getenv("PGE_PASSWORD")),
        },
    }


def devices_payload(arguments: dict[str, Any]) -> dict[str, Any]:
    from app import EmporiaProvider, _EMPORIA_CLIENT, _EmporiaClient, _public_envoy_system, envoy_systems

    provider = arguments.get("provider")
    if provider and provider not in ("enphase", "emporia"):
        raise ValueError("unknown provider")
    result: dict[str, Any] = {"version": 1, "providers": {}}
    if not provider or provider == "enphase":
        result["providers"]["enphase"] = [_public_envoy_system(s) for s in envoy_systems()]
    if not provider or provider == "emporia":
        email, password = os.getenv("EMPORIA_EMAIL"), os.getenv("EMPORIA_PASSWORD")
        if email and password:
            client = _EMPORIA_CLIENT or _EmporiaClient(email, password)
            result["providers"]["emporia"] = EmporiaProvider(client).devices()
    return result


def usage_payload(arguments: dict[str, Any]) -> dict[str, Any]:
    from app import db

    source = arguments.get("provider")
    limit = min(max(int(arguments.get("limit", 500)), 1), 5000)
    query = "SELECT ts, source, channel, watts, kwh FROM readings"
    params: list[Any] = []
    if source:
        query += " WHERE source=?"
        params.append(source)
    query += " ORDER BY ts DESC LIMIT ?"
    params.append(limit)
    with db() as connection:
        rows = [dict(row) for row in connection.execute(query, params)]
    return {"version": 1, "provider": source, "rows": rows}


def report_payload(arguments: dict[str, Any]) -> dict[str, Any]:
    from app import db

    source, limit = arguments.get("source"), min(max(int(arguments.get("limit", 50)), 1), 500)
    with db() as connection:
        rows = [dict(row) for row in connection.execute("SELECT ts, source, channel, watts, kwh FROM readings ORDER BY ts DESC LIMIT 500")]
    if source:
        rows = [row for row in rows if row["source"] == source]
    return {"version": 1, "count": len(rows[:limit]), "rows": rows[:limit]}


def _call_tool(name: str, arguments: dict[str, Any]) -> Any:
    handlers: dict[str, Callable[[dict[str, Any]], Any]] = {
        "status": lambda _: status_payload(),
        "devices": devices_payload,
        "usage": usage_payload,
        "report": report_payload,
    }
    if name not in handlers:
        raise KeyError(name)
    return handlers[name](arguments)


def register_mcp(flask_app) -> None:
    @flask_app.route("/mcp", methods=["POST"])
    def mcp_endpoint():
        expected_token = os.getenv("MCP_AUTH_TOKEN")
        if expected_token and request.headers.get("Authorization") != f"Bearer {expected_token}":
            return jsonify(_error(None, -32001, "unauthorized")), 401
        if not request.is_json:
            return jsonify(_error(None, -32700, "request must be JSON")), 400
        message = request.get_json(silent=True)
        if not isinstance(message, dict) or message.get("jsonrpc") != "2.0" or "method" not in message:
            return jsonify(_error(message.get("id") if isinstance(message, dict) else None, -32600, "invalid JSON-RPC request")), 400
        request_id = message.get("id")
        method = message["method"]
        if request_id is None and method.startswith("notifications/"):
            return ("", 202)
        if method == "initialize":
            return jsonify(_result(request_id, {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "power-monitor", "version": "1"},
            }))
        if method == "notifications/initialized":
            return ("", 202)
        if method == "tools/list":
            return jsonify(_result(request_id, {"tools": [{"name": name, **spec} for name, spec in TOOLS.items()]}))
        if method == "tools/call":
            params = message.get("params") or {}
            name, arguments = params.get("name"), params.get("arguments") or {}
            if not isinstance(name, str) or not isinstance(arguments, dict):
                return jsonify(_error(request_id, -32602, "tools/call requires name and object arguments"))
            try:
                value = _call_tool(name, arguments)
            except KeyError:
                return jsonify(_error(request_id, -32602, f"unknown tool: {name}"))
            except Exception as exc:
                return jsonify(_result(request_id, {"isError": True, "content": [{"type": "text", "text": str(exc)[:300]}]}))
            return jsonify(_result(request_id, {"isError": False, "content": [{"type": "text", "text": json.dumps(value)}]}))
        return jsonify(_error(request_id, -32601, f"method not found: {method}"))

    @flask_app.route("/mcp", methods=["GET", "DELETE", "PUT", "PATCH"])
    def mcp_method_not_allowed():
        return jsonify(_error(None, -32600, "only POST is supported for this stateless Streamable HTTP endpoint")), 405
