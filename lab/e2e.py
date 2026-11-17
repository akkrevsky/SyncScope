#!/usr/bin/env python3
"""Local end-to-end lab: control plane + two Syncthing instances.

It verifies the core hybrid behaviour:
  * a library is shared as one Syncthing folder;
  * a client-side whitelist downloads only the selected directory;
  * a single file can be selected;
  * a revoked device is removed from the origin folder.

Requires ./bin/syncthing (v2.x) and the syncscope binaries.
"""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.abspath(__file__))
RUN = os.path.join(ROOT, "e2e-run")
BIN = os.path.join(ROOT, "bin")
APP = os.path.join(ROOT, "..", "bin")
CONTROL = "http://127.0.0.1:18088"
ADMIN = "test-admin"
PORTS = {"origin": 8384, "client": 8385}
SYNC = {"origin": 22000, "client": 22001}

procs = []


def log(msg):
    print(f"[e2e] {msg}", flush=True)


def run(cmd, **kw):
    return subprocess.run(cmd, check=True, **kw)


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


def api(method, path, body=None, token=None, admin=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    if admin:
        headers["X-Admin-Token"] = admin
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(CONTROL + path, method=method, data=data, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def st(port, key, path, method="GET", body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}{path}",
        method=method,
        data=data,
        headers={"X-API-Key": key, "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        raw = r.read()
        return json.loads(raw) if raw else {}


def wait_http(url, timeout=30):
    end = time.time() + timeout
    while time.time() < end:
        try:
            urllib.request.urlopen(url, timeout=1).read()
            return
        except Exception:
            time.sleep(0.2)
    raise TimeoutError(url)


def ui_json(path, method="GET", body=None, timeout=30):
    req = urllib.request.Request(
        "http://127.0.0.1:18099" + path,
        method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.loads(r.read() or b"{}")


def wait_st(port, key, timeout=30):
    end = time.time() + timeout
    while time.time() < end:
        try:
            st(port, key, "/rest/system/status")
            return
        except Exception:
            time.sleep(0.2)
    raise TimeoutError(f"syncthing {port}")


def patch_config(name):
    home = os.path.join(RUN, name)
    cfg = os.path.join(home, "config.xml")
    tree = ET.parse(cfg)
    root = tree.getroot()
    root.find("gui/address").text = f"127.0.0.1:{PORTS[name]}"
    opts = root.find("options")
    opts.find("listenAddress").text = f"tcp://127.0.0.1:{SYNC[name]}"
    for tag in ("globalAnnounceEnabled", "localAnnounceEnabled", "relaysEnabled",
                "startBrowser", "natEnabled", "crashReportingEnabled"):
        el = opts.find(tag)
        if el is not None:
            el.text = "false"
    tree.write(cfg, encoding="utf-8", xml_declaration=True)
    return tree.findtext("gui/apikey")


def setup_syncthing():
    for name in ("origin", "client"):
        home = os.path.join(RUN, name)
        os.makedirs(home, exist_ok=True)
        run([os.path.join(BIN, "syncthing"), "generate", "--home", home],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        patch_config(name)
        start(
            [os.path.join(BIN, "syncthing"), "serve", "--home", home,
             "--no-browser", "--no-restart", "--log-level=WARN"],
            os.path.join(RUN, name + ".log"),
        )
    keys = {}
    for name in ("origin", "client"):
        tree = ET.parse(os.path.join(RUN, name, "config.xml"))
        keys[name] = tree.findtext("gui/apikey")
        wait_st(PORTS[name], keys[name])
    return keys


def wait_sync(port, key, folder, predicate, timeout=45):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        try:
            s = st(port, key, f"/rest/db/status?folder={folder}")
            last = s
            if predicate(s):
                return s
        except Exception as e:
            last = {"error": str(e)}
        time.sleep(0.5)
    raise AssertionError(f"sync timeout: {last}")


def exists(*parts):
    return os.path.exists(os.path.join(RUN, *parts))


def wait_catalog(lib_id, parent, name, token, timeout=15):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        code, body = api("GET", f"/v1/catalog/{lib_id}?parent={parent}", token=token)
        last = body
        if code == 200:
            for n in body.get("nodes", []):
                if n.get("name") == name:
                    return n
        time.sleep(0.5)
    raise AssertionError(f"catalog did not update: {last}")


def wait_file(rel, timeout=45):
    end = time.time() + timeout
    while time.time() < end:
        if exists(*rel.split("/")):
            return
        time.sleep(0.5)
    raise AssertionError("file did not arrive: " + rel)


def main():
    if not os.path.exists(os.path.join(BIN, "syncthing")):
        sys.exit("missing lab/bin/syncthing")
    shutil.rmtree(RUN, ignore_errors=True)
    os.makedirs(RUN, exist_ok=True)

    keys = setup_syncthing()
    origin_id = st(PORTS["origin"], keys["origin"], "/rest/system/status")["myID"]
    client_id = st(PORTS["client"], keys["client"], "/rest/system/status")["myID"]
    log(f"origin={origin_id} client={client_id}")

    start([os.path.join(APP, "syncscoped"), "-addr", "127.0.0.1:18088",
           "-data", os.path.join(RUN, "state.json"), "-admin-token", ADMIN,
           "-share-secret", "test-share"],
          os.path.join(RUN, "control.log"))
    wait_http(CONTROL + "/v1/health")

    status, body = api("POST", "/v1/devices", {
        "name": "client",
        "syncthing_id": client_id,
        "address": f"tcp://127.0.0.1:{SYNC['client']}",
        "platform": "linux",
    }, admin=ADMIN)
    assert status == 201, (status, body)
    client_token = body["token"]
    dev_id = body["device"]["id"]

    libroot = os.path.join(RUN, "library")
    os.makedirs(os.path.join(libroot, "2024"), exist_ok=True)
    os.makedirs(os.path.join(libroot, "2025"), exist_ok=True)
    with open(os.path.join(libroot, "2024", "a.txt"), "w") as f:
        f.write("selected\n")
    with open(os.path.join(libroot, "2025", "report.pdf"), "w") as f:
        f.write("selected file\n")
    with open(os.path.join(libroot, "2025", "other.txt"), "w") as f:
        f.write("other\n")

    status, body = api("POST", "/v1/libraries", {
        "name": "Photo", "root_path": libroot, "origin_url": "http://127.0.0.1:18090",
    }, admin=ADMIN)
    assert status == 201, (status, body)
    lib = body["library"]
    status, body = api("POST", f"/v1/libraries/{lib['id']}/grants", {"device_id": dev_id}, admin=ADMIN)
    assert status == 201, (status, body)

    status, body = api("GET", f"/v1/catalog/{lib['id']}", token=client_token)
    assert status == 200 and len(body["nodes"]) == 2, (status, body)

    log("provision origin folder")
    run([os.path.join(APP, "syncscope-origin"),
         "-control", CONTROL, "-admin-token", ADMIN,
         "-syncthing", f"http://127.0.0.1:{PORTS['origin']}",
         "-syncthing-key", keys["origin"],
         "-http", "127.0.0.1:18090", "-share-secret", "test-share", "-once"])

    state_file = os.path.join(RUN, "selection.json")
    log("select /2024")
    run([os.path.join(APP, "syncscope-client"),
         "-state", state_file,
         "-control", CONTROL, "-token", client_token,
         "-syncthing", f"http://127.0.0.1:{PORTS['client']}",
         "-syncthing-key", keys["client"],
         "-library", lib["id"], "-folder", lib["id"],
         "-local", os.path.join(RUN, "client-data"),
         "-origin-device", origin_id,
         "-origin-address", f"tcp://127.0.0.1:{SYNC['origin']}",
         "-select", "2024", "-wait"])

    wait_sync(PORTS["client"], keys["client"], lib["id"],
              lambda s: s.get("state") == "idle" and s.get("needFiles") == 0 and s.get("localFiles", 0) > 0)
    assert exists("client-data", "2024", "a.txt"), "selected dir not synced"
    assert not exists("client-data", "2025", "other.txt"), "unselected file leaked"
    log("directory selection OK")

    # A new file inside the selected tree must arrive; outside must not.
    with open(os.path.join(libroot, "2024", "b.txt"), "w") as f:
        f.write("new selected\n")
    with open(os.path.join(libroot, "2025", "new-other.txt"), "w") as f:
        f.write("new other\n")
    st(PORTS["origin"], keys["origin"], "/rest/db/scan?folder=" + lib["id"], method="POST")
    wait_file("client-data/2024/b.txt")
    wait_sync(PORTS["client"], keys["client"], lib["id"],
              lambda s: s.get("state") == "idle" and s.get("needFiles") == 0)
    time.sleep(1.5)
    assert not exists("client-data", "2025", "new-other.txt"), "new unselected file leaked"
    log("incremental selection OK")

    log("exclude a file inside the selected directory")
    run([os.path.join(APP, "syncscope-client"),
         "-state", state_file,
         "-control", CONTROL, "-token", client_token,
         "-syncthing", f"http://127.0.0.1:{PORTS['client']}",
         "-syncthing-key", keys["client"],
         "-library", lib["id"], "-folder", lib["id"],
         "-local", os.path.join(RUN, "client-data"),
         "-origin-device", origin_id,
         "-origin-address", f"tcp://127.0.0.1:{SYNC['origin']}",
         "-select", "2024", "-exclude", "2024/private.txt", "-wait"])
    with open(os.path.join(libroot, "2024", "private.txt"), "w") as f:
        f.write("must not sync\n")
    st(PORTS["origin"], keys["origin"], "/rest/db/scan?folder=" + lib["id"], method="POST")
    time.sleep(3)
    assert exists("client-data", "2024", "a.txt"), "selected file disappeared"
    assert not exists("client-data", "2024", "private.txt"), "excluded file leaked"
    log("exclude OK")

    log("second library")
    lib2root = os.path.join(RUN, "library2")
    os.makedirs(os.path.join(lib2root, "docs"), exist_ok=True)
    with open(os.path.join(lib2root, "docs", "readme.md"), "w") as f:
        f.write("# second mount\n")
    status, body = api("POST", "/v1/libraries", {
        "name": "Docs", "root_path": lib2root, "origin_url": "http://127.0.0.1:18090",
    }, admin=ADMIN)
    assert status == 201, (status, body)
    lib2 = body["library"]
    status, body = api("POST", f"/v1/libraries/{lib2['id']}/grants", {"device_id": dev_id}, admin=ADMIN)
    assert status == 201, (status, body)
    run([os.path.join(APP, "syncscope-origin"),
         "-control", CONTROL, "-admin-token", ADMIN,
         "-syncthing", f"http://127.0.0.1:{PORTS['origin']}",
         "-syncthing-key", keys["origin"],
         "-http", "127.0.0.1:18090", "-share-secret", "test-share", "-once"])
    os.makedirs(os.path.join(RUN, "client-data2"), exist_ok=True)

    log("navigator UI (multi-mount)")
    cfg_path = os.path.join(RUN, "client.json")
    with open(cfg_path, "w") as f:
        json.dump({
            "control": CONTROL,
            "token": client_token,
            "syncthing": f"http://127.0.0.1:{PORTS['client']}",
            "syncthing_key": keys["client"],
            "state": state_file,
            "ui": "127.0.0.1:18099",
            "mounts": [
                {"library": lib["id"], "local": os.path.join(RUN, "client-data"),
                 "origin_device": origin_id, "origin_address": f"tcp://127.0.0.1:{SYNC['origin']}"},
                {"library": lib2["id"], "local": os.path.join(RUN, "client-data2"),
                 "origin_device": origin_id, "origin_address": f"tcp://127.0.0.1:{SYNC['origin']}"},
            ],
        }, f, indent=2)
    start([os.path.join(APP, "syncscope-client"), "-config", cfg_path],
          os.path.join(RUN, "ui.log"))
    wait_http("http://127.0.0.1:18099/api/health")
    html = urllib.request.urlopen("http://127.0.0.1:18099/", timeout=5).read().decode()
    assert "SyncScope" in html, "UI index missing"
    mounts = ui_json("/api/mounts")
    assert len(mounts["mounts"]) == 2, mounts

    cat = ui_json(f"/api/catalog?mount={lib['id']}&parent=")
    assert len(cat["nodes"]) == 2, cat
    preview = urllib.request.urlopen(
        f"http://127.0.0.1:18099/api/preview?mount={lib['id']}&path=2024/a.txt", timeout=5).read()
    assert preview == b"selected\n", preview
    download = urllib.request.urlopen(
        f"http://127.0.0.1:18099/api/download?mount={lib['id']}&path=2024/a.txt", timeout=5)
    assert download.status == 200
    try:
        urllib.request.urlopen(
            f"http://127.0.0.1:18099/api/preview?mount={lib['id']}&path=../etc/passwd", timeout=5)
        raise AssertionError("path traversal was not rejected")
    except urllib.error.HTTPError as e:
        assert e.code in (400, 404), e.code

    log("select second mount")
    ui_json("/api/selection", "POST", {"mount": lib2["id"], "included": ["docs"], "excluded": []})
    wait_file("client-data2/docs/readme.md")
    cat2 = ui_json(f"/api/catalog?mount={lib2['id']}&parent=docs")
    assert any(n["name"] == "readme.md" for n in cat2["nodes"]), cat2
    log("multi-mount OK")

    ui_json("/api/selection", "POST", {"mount": lib["id"], "included": ["2025"], "excluded": []})
    wait_file("client-data/2025/report.pdf")
    status = ui_json(f"/api/status?mount={lib['id']}")
    assert "state" in status, status
    ui_json("/api/free-space", "POST", {"mount": lib["id"], "path": "2025"}, timeout=30)
    time.sleep(0.5)
    assert not exists("client-data", "2025"), "free space did not remove local copy"
    assert os.path.exists(os.path.join(libroot, "2025", "report.pdf")), "free space touched origin"
    log("UI and free space OK")

    log("public share")
    start([os.path.join(APP, "syncscope-origin"),
           "-control", CONTROL, "-admin-token", ADMIN,
           "-syncthing", f"http://127.0.0.1:{PORTS['origin']}",
           "-syncthing-key", keys["origin"],
           "-http", "127.0.0.1:18090", "-share-secret", "test-share",
           "-interval", "2s"], os.path.join(RUN, "origin-share.log"))
    wait_http("http://127.0.0.1:18090/healthz")
    status, body = api("POST", "/v1/shares", {
        "library": lib["id"], "path": "2024", "is_dir": True, "expires_in": 3600,
    }, token=client_token)
    assert status == 201, (status, body)
    share_url = body["url"]
    page = urllib.request.urlopen(share_url, timeout=5).read().decode()
    assert "a.txt" in page, page[:300]
    token = share_url.rsplit("/", 1)[-1]
    shared = urllib.request.urlopen(
        f"http://127.0.0.1:18090/s/{token}/download?path=a.txt", timeout=5).read()
    assert shared == b"selected\n", shared
    log("public share OK")

    log("live catalog")
    os.makedirs(os.path.join(libroot, "2026"), exist_ok=True)
    with open(os.path.join(libroot, "2026", "live.txt"), "w") as f:
        f.write("live\n")
    root_node = wait_catalog(lib["id"], "", "2026", client_token)
    assert root_node["is_dir"], root_node
    wait_catalog(lib["id"], "2026", "live.txt", client_token)
    log("live catalog OK")

    log("revoke device")
    status, _ = api("POST", f"/v1/devices/{dev_id}/revoke", admin=ADMIN)
    assert status == 200, status
    time.sleep(4)
    for lid in (lib["id"], lib2["id"]):
        folder = st(PORTS["origin"], keys["origin"], f"/rest/config/folders/{lid}")
        ids = [d["deviceID"] for d in folder["devices"]]
        assert client_id not in ids, (lid, ids)
    log("revocation OK")

    print("E2E PASS")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print(f"E2E FAIL: {e}", file=sys.stderr)
        raise
    finally:
        stop_all()
