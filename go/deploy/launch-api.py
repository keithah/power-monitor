import os

root = os.path.dirname(__file__)
with open(os.path.join(root, ".env"), encoding="utf-8") as env:
    for line in env:
        if "=" in line:
            key, value = line.rstrip("\n").split("=", 1)
            os.environ[key] = value
os.environ.update(
    POWER_MONITOR_CONFIG=os.path.join(root, "config.json"),
    POWER_MONITOR_DB=os.path.join(root, "data", "power-monitor.sqlite"),
    POWER_MONITOR_API_ADDR="127.0.0.1:8097",
    POWER_MONITOR_PGE_LOGIN_PATH=os.path.join(root, "pge-login.json"),
)
os.execv(os.path.join(root, "bin", "power-monitor-pp-api"), ["power-monitor-pp-api"])
