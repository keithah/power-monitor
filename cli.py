#!/usr/bin/env python3
"""Thin CLI over a running power-monitor HTTP service."""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


def request(base: str, path: str, method: str = "GET") -> object:
    req = urllib.request.Request(base.rstrip("/") + path, method=method)
    try:
        with urllib.request.urlopen(req, timeout=180) as response:
            return json.loads(response.read().decode())
    except urllib.error.HTTPError as exc:
        body = exc.read().decode(errors="replace")
        raise SystemExit(f"{exc.code} {path}: {body[:300]}") from exc
    except urllib.error.URLError as exc:
        raise SystemExit(f"cannot reach {base}: {exc.reason}") from exc


def main() -> int:
    parser = argparse.ArgumentParser(prog="power-monitor", description="Talk to power-monitor")
    parser.add_argument("--url", default=os.getenv("SOLAR_URL", "http://127.0.0.1:8094"))
    sub = parser.add_subparsers(dest="cmd", required=True)
    sub.add_parser("health", help="service health")
    sub.add_parser("status", help="configured providers")
    devices = sub.add_parser("devices", help="configured electricity devices")
    devices.add_argument("--provider", help="filter by provider: enphase, emporia")
    usage = sub.add_parser("usage", help="stored normalized usage")
    usage.add_argument("--provider", help="filter by provider: enphase, emporia")
    usage.add_argument("--limit", type=int, default=500)
    sub.add_parser("collect", help="run one collection cycle")
    report = sub.add_parser("report", help="recent stored readings")
    report.add_argument("--source", help="filter by provider: enphase, emporia, pge")
    report.add_argument("--limit", type=int, default=50)
    args = parser.parse_args()

    if args.cmd == "health":
        print(json.dumps(request(args.url, "/health"), indent=2))
    elif args.cmd == "status":
        print(json.dumps(request(args.url, "/api/status"), indent=2))
    elif args.cmd == "devices":
        query = urllib.parse.urlencode({"provider": args.provider}) if args.provider else ""
        path = "/api/devices" + (("?" + query) if query else "")
        print(json.dumps(request(args.url, path), indent=2))
    elif args.cmd == "usage":
        query = {"limit": args.limit}
        if args.provider: query["provider"] = args.provider
        print(json.dumps(request(args.url, "/api/usage?" + urllib.parse.urlencode(query)), indent=2))
    elif args.cmd == "collect":
        print(json.dumps(request(args.url, "/api/collect", method="POST"), indent=2))
    elif args.cmd == "report":
        payload = request(args.url, "/api/report")
        rows = payload.get("rows", []) if isinstance(payload, dict) else []
        if args.source:
            rows = [row for row in rows if row.get("source") == args.source]
        slim = [
            {k: row.get(k) for k in ("ts", "source", "channel", "watts", "kwh")}
            for row in rows[: args.limit]
        ]
        print(json.dumps({"count": len(slim), "rows": slim}, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
