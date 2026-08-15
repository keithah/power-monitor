import pytest

from providers import (
    discovery_hosts,
    envoy_password,
    extract_envoy_readings,
    parse_emporia_devices,
    parse_emporia_usages,
    parse_envoy_serial,
    parse_opower_reads,
    emporia_interval_ts,
)


def test_parse_envoy_serial_and_discovery_hosts():
    assert parse_envoy_serial('<device><device_sn>12345</device_sn></device>') == '12345'
    assert discovery_hosts('192.168.1.0/30,192.168.2.10') == ['192.168.1.1', '192.168.1.2', '192.168.2.10']


def test_envoy_password_matches_plugin_algorithm():
    assert envoy_password("envoy", "enphaseenergy.com", "1234567890") == "2F8dkf83"


def test_extract_envoy_meter_readings_normalizes_power_and_energy():
    payload = {
        "meters": {
            "eim": {"net_consumption": 321, "total_consumption": 654},
            "production": {"power": 987},
        }
    }
    readings = extract_envoy_readings(payload, "gateway-a")
    assert {r["channel"]: r["watts"] for r in readings} == {
        "gateway-a:production": 987,
        "gateway-a:grid_net": 321,
        "gateway-a:consumption": 654,
    }


def test_parse_opower_reads_preserves_timezone_and_kwh():
    reads = [{
        "start_time": "2026-08-13T10:00:00-07:00",
        "end_time": "2026-08-13T10:15:00-07:00",
        "consumption": 1.25,
    }]
    parsed = parse_opower_reads(reads, "pge", "account-1")
    assert parsed == [{
        "source": "pge",
        "channel": "account-1",
        "ts": "2026-08-13T17:15:00+00:00",
        "kwh": 1.25,
        "watts": 5000.0,
    }]


DEVICES_PAYLOAD = {
    "devices": [
        {
            "device_gid": 100001,
            "device_id": "DEV001EXAMPLE00000000",
            "display_name": "Main Panel",
            "model": "VUE003",
            "time_zone": "America/Los_Angeles",
            "channels": [
                {"channel_id": "Mains", "channel_classification": "MAIN", "has_data": True},
                {"channel_id": "Mains_A", "channel_classification": "MAIN", "has_data": True},
                {"channel_id": "Mains_C", "channel_classification": "MAIN", "has_data": False},
                {"channel_id": "Branch_15", "channel_classification": "FIFTY_AMP", "has_data": True},
            ],
        },
        {
            "device_gid": 100001,
            "device_id": "DEV001EXAMPLE00000000",
            "display_name": "Main Panel",
            "model": "VUE003",
            "time_zone": "America/Los_Angeles",
            "channels": [{"channel_id": "Mains", "channel_classification": "MAIN", "has_data": True}],
        },
        {
            "device_gid": 100002,
            "device_id": "DEV002EXAMPLE00000000",
            "display_name": None,
            "model": "VUE003",
            "time_zone": "America/Los_Angeles",
            "channels": [{"channel_id": "Mains", "channel_classification": "MAIN", "has_data": True}],
        },
    ]
}


def test_parse_emporia_devices_dedupes_gids_and_keeps_channels():
    devices = parse_emporia_devices(DEVICES_PAYLOAD)
    assert [d["gid"] for d in devices] == [100001, 100002]
    by_gid = {d["gid"]: d for d in devices}
    assert by_gid[100001]["name"] == "Main Panel"
    assert by_gid[100002]["name"] == "emporia-100002"
    assert by_gid[100001]["channels"] == {
        "Mains": {"classification": "MAIN", "has_data": True},
        "Mains_A": {"classification": "MAIN", "has_data": True},
        "Mains_C": {"classification": "MAIN", "has_data": False},
        "Branch_15": {"classification": "FIFTY_AMP", "has_data": True},
    }


USAGE_PAYLOAD = {
    "instant": "2026-08-15T07:12:06Z",
    "scale": "HOUR",
    "energy_unit": "KILOWATT_HOURS",
    "device_usages": [
        {
            "device_gid": 100001,
            "channel_usages": [
                {"device_gid": 100001, "channel_id": "Mains", "usage": 0.1777, "percentage": 100.0, "nested_devices": []},
                {"device_gid": 100001, "channel_id": "MainsFromGrid", "usage": None, "percentage": 0.0, "nested_devices": []},
                {"device_gid": 100001, "channel_id": "Branch_15", "usage": 0.0666, "percentage": 37.5, "nested_devices": []},
                {"device_gid": 100001, "channel_id": "TotalUsage", "usage": 0.1777, "percentage": 100.0, "nested_devices": []},
                {"device_gid": 100001, "channel_id": "Balance", "usage": 0.0044, "percentage": 2.5, "nested_devices": []},
            ],
        }
    ],
}


def test_parse_emporia_usages_maps_channels_and_floors_ts_to_bucket():
    device = {"gid": 100001, "name": "Main Panel"}
    parsed = parse_emporia_usages(USAGE_PAYLOAD, device)
    assert parsed == [
        {"source": "emporia", "channel": "Main Panel:Mains", "ts": "2026-08-15T07:00:00+00:00", "kwh": 0.1777, "channel_id": "Mains"},
        {"source": "emporia", "channel": "Main Panel:Branch_15", "ts": "2026-08-15T07:00:00+00:00", "kwh": 0.0666, "channel_id": "Branch_15"},
    ]


def test_emporia_usage_skips_computed_and_null_channels():
    parsed = parse_emporia_usages(USAGE_PAYLOAD, {"gid": 100001, "name": "Main Panel"})
    channels = [item["channel_id"] for item in parsed]
    assert "TotalUsage" not in channels
    assert "Balance" not in channels
    assert "MainsFromGrid" not in channels


def test_emporia_interval_ts_floors_to_hour_and_day():
    assert emporia_interval_ts("2026-08-15T07:12:06Z", "HOUR") == "2026-08-15T07:00:00+00:00"
    assert emporia_interval_ts("2026-08-15T07:12:06Z", "DAY") == "2026-08-15T00:00:00+00:00"
