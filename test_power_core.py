import json
from datetime import datetime, timezone

import pytest

from power_core import (
    ElectricityReading,
    ProviderRegistry,
    ProviderStatus,
    selection_error,
    validate_reading,
)


def test_normalized_reading_has_explicit_units_and_timezone():
    reading = ElectricityReading(
        provider="emporia",
        device_id="100001",
        channel="Main Panel:Mains",
        timestamp=datetime(2026, 8, 15, 7, tzinfo=timezone.utc),
        watts=321.0,
        kwh=0.1777,
    )
    assert reading.to_dict() == {
        "provider": "emporia",
        "device_id": "100001",
        "channel": "Main Panel:Mains",
        "timestamp": "2026-08-15T07:00:00+00:00",
        "power": {"value": 321.0, "unit": "W"},
        "energy": {"value": 0.1777, "unit": "kWh"},
    }


def test_invalid_reading_is_rejected_before_storage():
    reading = ElectricityReading(
        provider="enphase",
        device_id="site-1",
        channel="production",
        timestamp=datetime.now(timezone.utc),
        watts=-1,
    )
    with pytest.raises(ValueError, match="power"):
        validate_reading(reading)


def test_registry_requires_provider_when_multiple_are_configured():
    registry = ProviderRegistry({"emporia": object(), "enphase": object()})
    with pytest.raises(ValueError, match="--provider"):
        registry.select(None)


def test_registry_selects_named_provider_and_rejects_unknown():
    emporia = object()
    registry = ProviderRegistry({"emporia": emporia, "enphase": object()})
    assert registry.select("emporia") is emporia
    with pytest.raises(ValueError, match="unknown provider"):
        registry.select("pge")


def test_status_json_is_versioned_and_secret_free():
    payload = ProviderStatus("emporia", "ok", devices=1).to_dict()
    encoded = json.dumps(payload)
    assert payload == {"version": 1, "provider": "emporia", "status": "ok", "devices": 1}
    assert "token" not in encoded
    assert "password" not in encoded


def test_selection_error_is_actionable():
    assert "emporia" in selection_error(["emporia", "enphase"])
    assert "--provider" in selection_error(["emporia", "enphase"])
