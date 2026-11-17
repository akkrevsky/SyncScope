#!/usr/bin/env bash
set -Eeuo pipefail

APP=/app
DATA="${DATA_DIR:-/data}"
ADMIN="${ADMIN_TOKEN:-change-me-admin}"
SHARE_SECRET="${SHARE_SECRET:-change-me-share}"
SHARE_PUBLIC_URL="${SHARE_PUBLIC_URL:-https://akkrevsky.ru/syncscope-share}"
UI_ADDR="${UI_ADDR:-0.0.0.0:8099}"
ORIGIN_KEY="${ORIGIN_KEY:-origin-local-key}"
CLIENT_KEY="${CLIENT_KEY:-client-local-key}"
PIDS=()

log() { echo "[syncscope] $*" >&2; }
cleanup() {
  for pid in "${PIDS[@]:-}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

mkdir -p "$DATA"/{origin,client,library,client-data}
mkdir -p "$DATA/library/Photos/2024" "$DATA/library/Photos/2025" "$DATA/library/Documents"

gen_st() {
  local home="$1" port="$2"
  if [[ ! -f "$home/config.xml" ]]; then
    log "generating Syncthing config: $home"
    /app/syncthing generate --home "$home" >/dev/null 2>&1
    sed -i "s#<listenAddress>default</listenAddress>#<listenAddress>tcp://127.0.0.1:${port}</listenAddress>#" "$home/config.xml"
    sed -i 's#<globalAnnounceEnabled>true#<globalAnnounceEnabled>false#; s#<localAnnounceEnabled>true#<localAnnounceEnabled>false#; s#<relaysEnabled>true#<relaysEnabled>false#; s#<startBrowser>true#<startBrowser>false#; s#<natEnabled>true#<natEnabled>false#' "$home/config.xml"
  fi
}

wait_st() {
  local port="$1" key="$2" tries="${3:-60}"
  for _ in $(seq 1 "$tries"); do
    if curl -fsS --max-time 2 -H "X-API-Key: $key" "http://127.0.0.1:${port}/rest/system/status" >/dev/null 2>&1; then return 0; fi
    sleep 0.5
  done
  log "timeout waiting for Syncthing :$port"
  return 1
}

wait_http() {
  local url="$1" tries="${2:-60}"
  for _ in $(seq 1 "$tries"); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then return 0; fi
    sleep 0.5
  done
  log "timeout waiting for $url"
  return 1
}

start_st() {
  local home="$1" gui="$2" key="$3" name="$4"
  /app/syncthing serve --home "$home" --gui-address="127.0.0.1:${gui}" --gui-apikey="$key" \
    --no-browser --no-restart --log-level=WARN >"$DATA/${name}.log" 2>&1 &
  PIDS+=("$!")
}

st() {
  local port="$1" key="$2" path="$3"
  curl -fsS -H "X-API-Key: $key" "http://127.0.0.1:${port}${path}"
}

gen_st "$DATA/origin" 22000
gen_st "$DATA/client" 22001

start_st "$DATA/origin" 8384 "$ORIGIN_KEY" origin
start_st "$DATA/client" 8385 "$CLIENT_KEY" client
wait_st 8384 "$ORIGIN_KEY"
wait_st 8385 "$CLIENT_KEY"

ORIGIN_ID=$(st 8384 "$ORIGIN_KEY" /rest/system/status | jq -r .myID)
CLIENT_ID=$(st 8385 "$CLIENT_KEY" /rest/system/status | jq -r .myID)

/app/syncscoped -addr 127.0.0.1:8088 -data "$DATA/state.json" \
  -admin-token "$ADMIN" -share-secret "$SHARE_SECRET" >"$DATA/control.log" 2>&1 &
PIDS+=("$!")
wait_http "http://127.0.0.1:8088/v1/health"

if [[ ! -f "$DATA/client.json" ]]; then
  log "first start: provisioning demo library"
  cat > "$DATA/library/Documents/readme.md" <<'TXT'
# SyncScope demo

Это демонстрационная библиотека. Выберите папку справа/слева — файлы придут по P2P.
TXT
  printf 'hello from 2024\n' > "$DATA/library/Photos/2024/hello.txt"
  printf 'another photo\n' > "$DATA/library/Photos/2024/notes.md"
  printf 'old photo\n' > "$DATA/library/Photos/2025/old.txt"

  CLIENT_TOKEN=$(curl -fsS -X POST http://127.0.0.1:8088/v1/devices \
    -H "X-Admin-Token: $ADMIN" -H 'Content-Type: application/json' \
    -d "{\"name\":\"web\",\"syncthing_id\":\"$CLIENT_ID\",\"address\":\"tcp://127.0.0.1:22001\",\"platform\":\"linux\"}" | jq -r .token)
  DEVICE_ID=$(curl -fsS http://127.0.0.1:8088/v1/devices -H "X-Admin-Token: $ADMIN" | jq -r '.devices[0].id')
  LIB_ID=$(curl -fsS -X POST http://127.0.0.1:8088/v1/libraries \
    -H "X-Admin-Token: $ADMIN" -H 'Content-Type: application/json' \
    -d "{\"name\":\"Demo\",\"root_path\":\"$DATA/library\",\"origin_url\":\"$SHARE_PUBLIC_URL\"}" | jq -r .library.id)
  curl -fsS -X POST "http://127.0.0.1:8088/v1/libraries/${LIB_ID}/grants" \
    -H "X-Admin-Token: $ADMIN" -H 'Content-Type: application/json' \
    -d "{\"device_id\":\"$DEVICE_ID\"}" >/dev/null

  cat > "$DATA/client.json" <<JSON
{
  "control": "http://127.0.0.1:8088",
  "token": "$CLIENT_TOKEN",
  "syncthing": "http://127.0.0.1:8385",
  "syncthing_key": "$CLIENT_KEY",
  "state": "$DATA/selection.json",
  "ui": "$UI_ADDR",
  "mounts": [
    {"library": "$LIB_ID", "local": "$DATA/client-data",
     "origin_device": "$ORIGIN_ID", "origin_address": "tcp://127.0.0.1:22000"}
  ]
}
JSON
  cat > "$DATA/selection.json" <<JSON
{"$LIB_ID":{"library":"$LIB_ID","included":["Photos/2024"],"excluded":[]}}
JSON
else
  log "existing state found: starting without provisioning"
fi

/app/syncscope-origin -control http://127.0.0.1:8088 -admin-token "$ADMIN" \
  -syncthing http://127.0.0.1:8384 -syncthing-key "$ORIGIN_KEY" \
  -http 0.0.0.0:8090 -share-secret "$SHARE_SECRET" -share-base /syncscope-share/ -interval 5s \
  >"$DATA/origin-agent.log" 2>&1 &
PIDS+=("$!")

/app/syncscope-client -config "$DATA/client.json" >"$DATA/client-agent.log" 2>&1 &
PIDS+=("$!")

log "ready: UI http://127.0.0.1:8099, share origin http://127.0.0.1:8090"
wait -n "${PIDS[@]}"
