from datetime import datetime, timezone

from provider_adapters import EmporiaProvider, EnphaseProvider


class FakeEmporia:
    def now(self):
        return "2026-08-15T07:12:06Z"

    def get(self, path, params=None):
        if path.endswith("/devices"):
            return {"devices": [{"device_gid": 1, "display_name": "Panel", "channels": [{"channel_id": "Mains", "has_data": True}]}]}
        return {"instant": self.now(), "scale": "HOUR", "device_usages": [{"device_gid": 1, "channel_usages": [{"channel_id": "Mains", "usage": 0.5}]}]}


def test_emporia_adapter_normalizes_existing_payloads():
    readings = EmporiaProvider(FakeEmporia()).usage()
    assert readings[0].provider == "emporia"
    assert readings[0].device_id == "1"
    assert readings[0].kwh == 0.5
    assert readings[0].timestamp.tzinfo is not None


def test_enphase_adapter_normalizes_reader_output():
    provider = EnphaseProvider(
        lambda: [{"name": "site", "serial": "abc", "session": object()}],
        lambda _: [{"channel": "site:production", "watts": 123}],
    )
    assert provider.devices() == [{"name": "site", "serial": "abc"}]
    reading = provider.usage()[0]
    assert reading.provider == "enphase"
    assert reading.watts == 123
    assert reading.timestamp.tzinfo == timezone.utc
