# Runs as root on a sandbox through Run Command and prints one line: the marker, then JSON with the boot time, the
# number of T3 Code runs going on now, and when the latest run started or ended.
import json
import socket
import sqlite3
import time
from datetime import datetime, timezone

DB = "/home/agent/.t3/userdata/statev2.sqlite"


def server_up():
    try:
        socket.create_connection(("127.0.0.1", 3773), timeout=2).close()
        return True
    except OSError:
        return False


def parse(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")) if value else None


with open("/proc/uptime") as f:
    booted = datetime.fromtimestamp(time.time() - float(f.read().split()[0]), timezone.utc)

running, latest = 0, None
try:
    db = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
except sqlite3.OperationalError:
    db = None
if db is not None:
    # Every agent's thread, delegated ones included, keeps its runs in this table. A run that started before this
    # boot died with the machine, whatever status it was left in.
    up = server_up()
    for status, requested, completed in db.execute(
        "select status, requested_at, completed_at from orchestration_v2_projection_runs"
    ):
        start, end = parse(requested), parse(completed)
        if status == "running" and up and start and start >= booted:
            running += 1
        for t in (start, end):
            if t and (latest is None or t > latest):
                latest = t

print(
    "skybox-activity "
    + json.dumps(
        {
            "booted": booted.isoformat(),
            "running": running,
            "latestRun": latest.isoformat() if latest else None,
        }
    )
)
