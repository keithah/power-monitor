"""Provider-neutral electricity models and selection logic."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Mapping


@dataclass(frozen=True)
class ElectricityReading:
    provider: str
    device_id: str
    channel: str
    timestamp: datetime
    watts: float | None = None
    kwh: float | None = None

    def to_dict(self) -> dict[str, Any]:
        value: dict[str, Any] = {
            "provider": self.provider,
            "device_id": self.device_id,
            "channel": self.channel,
            "timestamp": self.timestamp.astimezone(timezone.utc).isoformat(),
        }
        if self.watts is not None:
            value["power"] = {"value": self.watts, "unit": "W"}
        if self.kwh is not None:
            value["energy"] = {"value": self.kwh, "unit": "kWh"}
        return value


@dataclass(frozen=True)
class ProviderStatus:
    provider: str
    status: str
    devices: int = 0

    def to_dict(self) -> dict[str, Any]:
        return {
            "version": 1,
            "provider": self.provider,
            "status": self.status,
            "devices": self.devices,
        }


def validate_reading(reading: ElectricityReading) -> None:
    if not reading.provider or not reading.device_id or not reading.channel:
        raise ValueError("reading identity is required")
    if reading.timestamp.tzinfo is None:
        raise ValueError("timestamp must include timezone")
    if reading.watts is not None and reading.watts < 0:
        raise ValueError("power cannot be negative")
    if reading.kwh is not None and reading.kwh < 0:
        raise ValueError("energy cannot be negative")


def selection_error(providers: list[str]) -> str:
    names = ", ".join(sorted(providers))
    return f"multiple providers configured ({names}); specify --provider <name>"


class ProviderRegistry:
    def __init__(self, providers: Mapping[str, object]):
        self._providers = dict(providers)

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(sorted(self._providers))

    def select(self, name: str | None) -> object:
        if name:
            try:
                return self._providers[name]
            except KeyError as exc:
                choices = ", ".join(self.names)
                raise ValueError(f"unknown provider {name!r}; choose from {choices}") from exc
        if len(self._providers) != 1:
            raise ValueError(selection_error(list(self._providers)))
        return next(iter(self._providers.values()))
