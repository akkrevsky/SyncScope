#!/usr/bin/env python3
"""Force-relay lab: two Syncthing instances connect only through a local relay.

This verifies the relay transport path (the public pool uses the same protocol),
not the pool's discovery/latency selection.
"""
import json
import os
import re
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(ROOT, "relay-run")
BIN = os.path.join(ROOT, "bin")
RELAY = os.path.join(ROOT, "relay-bin", "strelaysrv")
GUI = {"origin": 8391, "client": 8392}
procs = []


def log(msg):
    print(f"[relay] {msg}", flush=True)


def start(cmd, logfile):
    f = open(logfile, "ab")
    p = subprocess.Popen(cmd, stdout=f, stderr=subprocess.STDOUT)
    procs.append(p)
    return p


def stop_all():
    for p in procs:
        if p.poll() is None:
            p.terminate()
    for p in procs:
        try:
            p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            p.kill()


def api(port, key, path, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}{path}",
        method=method,
        data=data,
        headers={"X-API-Key": key, "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            raw = r.read()
            return r.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def wait_st(port, key, timeout=30):
    end = time.time() + timeout
    while time.time() < end:
        try:
            if api(port, key, "/rest/system/status")[0] == 200:
                return
        except Exception:
            pass
        time.sleep(0.2)
    raise TimeoutError(f"syncthing {port}")


def patch(home, gui, relay_uri):
    cfg = os.path.join(home, "config.xml")
    tree = ET.parse(cfg)
    root = tree.getroot()
    root.find("gui/address").text = f"127.0.0.1:{gui}"
    opts = root.find("options")
    opts.find("listenAddress").text = relay_uri
    for tag in ("globalAnnounceEnabled", "localAnnounceEnabled", "startBrowser",
                "natEnabled", "crashReportingEnabled"):
        el = opts.find(tag)
        if el is not None:
            el.text = "false"
    opts.find("relaysEnabled").text = "true"
    tree.write(cfg, encoding="utf-8", xml_declaration=True)
    return tree.findtext("gui/apikey")


def main():
    if not os.path.exists(RELAY):
        sys.exit("missing relay-bin/strelaysrv; run ./lab/fetch-strelaysrv.sh")
    shutil.rmtree(RUN, ignore_errors=True)
    os.makedirs(RUN)
    os.makedirs(os.path.join(RUN, "keys"))
    os.makedirs(os.path.join(RUN, "library", "2024"))
    os.makedirs(os.path.join(RUN, "client-data"))
    with open(os.path.join(RUN, "library", "2024", "relay.txt"), "w") as f:
        f.write("relayed\n")

    relay_log = os.path.join(RUN, "relay.log")
    start([RELAY, "-keys", os.path.join(RUN, "keys"),
           "-listen", "127.0.0.1:22067", "-status-srv", "127.0.0.1:22070",
           "-pools", "", "-provided-by", "lab"], relay_log)

    relay_id = None
    for _ in range(50):
        time.sleep(0.2)
        try:
            text = open(relay_log).read()
        except OSError:
            continue
        m = re.search(r"id=([A-Z0-9-]+)", text)
        if m:
            relay_id = m.group(1)
            break
    if not relay_id:
        raise RuntimeError("relay did not print device id")
    relay_uri = f"relay://127.0.0.1:22067/?id={relay_id}"
    log(f"relay={relay_id}")

    keys = {}
    for name in ("origin", "client"):
        home = os.path.join(RUN, name)
        os.makedirs(home)
        subprocess.run([os.path.join(BIN, "syncthing"), "generate", "--home", home],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        keys[name] = patch(home, GUI[name], relay_uri)
        start([os.path.join(BIN, "syncthing"), "serve", "--home", home,
               "--no-browser", "--no-restart", "--log-level=WARN"],
              os.path.join(RUN, name + ".log"))
        wait_st(GUI[name], keys[name])

    o = api(GUI["origin"], keys["origin"], "/rest/system/status")[1]["myID"]
    c = api(GUI["client"], keys["client"], "/rest/system/status")[1]["myID"]
    for port, key, peer in ((GUI["origin"], keys["origin"], c),
                            (GUI["client"], keys["client"], o)):
        code, body = api(port, key, f"/rest/config/devices/{peer}", "PUT", {
            "deviceID": peer, "name": "peer", "addresses": [relay_uri],
        })
        if code != 200:
            raise RuntimeError(f"add device: {code} {body}")

    code, body = api(GUI["client"], keys["client"], "/rest/config/folders/lib-relay", "PUT", {
        "id": "lib-relay", "label": "Relay", "path": os.path.join(RUN, "client-data"),
        "type": "receiveonly", "fsWatcherEnabled": True,
        "devices": [{"deviceID": c}, {"deviceID": o}],
    })
    if code != 200:
        raise RuntimeError(f"client folder: {code} {body}")
    code, body = api(GUI["origin"], keys["origin"], "/rest/config/folders/lib-relay", "PUT", {
        "id": "lib-relay", "label": "Relay", "path": os.path.join(RUN, "library"),
        "type": "sendonly", "fsWatcherEnabled": True,
        "devices": [{"deviceID": o}, {"deviceID": c}],
    })
    if code != 200:
        raise RuntimeError(f"origin folder: {code} {body}")

    target = os.path.join(RUN, "client-data", "2024", "relay.txt")
    end = time.time() + 60
    conn_type = ""
    while time.time() < end:
        time.sleep(0.5)
        conns = api(GUI["origin"], keys["origin"], "/rest/system/connections")[1]["connections"]
        conn = conns.get(c, {})
        st = api(GUI["client"], keys["client"], "/rest/db/status?folder=lib-relay")[1]
        conn_type = conn.get("type", "")
        if (conn.get("connected") and st.get("state") == "idle"
                and st.get("needFiles") == 0 and os.path.exists(target)):
            break
    if not conn_type.startswith("relay"):
        raise AssertionError(f"connection is not relay: {conn_type!r}")
    if not os.path.exists(target):
        raise AssertionError("relayed file did not arrive")
    print(f"RELAY E2E PASS ({conn_type})")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print(f"RELAY E2E FAIL: {e}", file=sys.stderr)
        raise
    finally:
        stop_all()
