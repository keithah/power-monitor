"""Emporia and Enphase adapters over the existing upstream parsers."""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, Callable

from power_core import ElectricityReading, ProviderStatus, validate_reading
from providers import extract_envoy_readings, parse_emporia_devices, parse_emporia_usages


class EmporiaProvider:
    name = "emporia"

    def __init__(self, client: Any):
        self.client = client

    def status(self) -> ProviderStatus:
        devices = self.devices()
        return ProviderStatus(self.name, "ok", len(devices))

    def devices(self) -> list[dict[str, Any]]:
        return parse_emporia_devices(self.client.get("/v1/customers/devices"))

    def usage(self) -> list[ElectricityReading]:
        readings: list[ElectricityReading] = []
        for device in self.devices():
            payload = self.client.get(
                "/v1/customers/devices/usages",
                {
                    "device_gids": device["gid"],
                    "instant": self.client.now(),
                    "scale": "HOUR",
                    "energy_unit": "KILOWATT_HOURS",
                },
            )
            for item in parse_emporia_usages(payload, device):
                reading = ElectricityReading(
                    provider=self.name,
                    device_id=str(device["gid"]),
                    channel=item["channel"],
                    timestamp=datetime.fromisoformat(item["ts"]),
                    kwh=float(item["kwh"]),
                )
                validate_reading(reading)
                readings.append(reading)
        return readings


class EnphaseProvider:
    name = "enphase"

    def __init__(self, systems_loader: Callable[[], list[dict[str, Any]]], reader: Callable[[dict[str, Any]], list[dict[str, Any]]]):
        self._systems_loader = systems_loader
        self._reader = reader

    def devices(self) -> list[dict[str, Any]]:
        return [
            {k: v for k, v in system.items() if k not in {"session", "token"}}
            for system in self._systems_loader()
        ]

    def status(self) -> ProviderStatus:
        return ProviderStatus(self.name, "ok", len(self.devices()))

    def usage(self) -> list[ElectricityReading]:
        readings: list[ElectricityReading] = []
        for system in self._systems_loader():
            device_id = str(system.get("serial") or system.get("site_id") or system.get("name"))
            for item in self._reader(system):
                reading = ElectricityReading(
                    provider=self.name,
                    device_id=device_id,
                    channel=str(item["channel"]),
                    timestamp=datetime.now(timezone.utc),
                    watts=float(item["watts"]) if item.get("watts") is not None else None,
                )
                validate_reading(reading)
                readings.append(reading)
        return readings


def envoy_reader(payload: dict[str, Any], name: str) -> list[dict[str, Any]]:
    return extract_envoy_readings(payload, name)
