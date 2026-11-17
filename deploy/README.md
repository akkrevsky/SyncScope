# Deploy

## Build image on a machine with Docker

The build context needs Linux binaries in `bin/` and `lab/bin/syncthing`.

```bash
make build
./lab/fetch-syncthing.sh
tar czf /tmp/syncscope-build.tgz \
  bin/syncscoped bin/syncscope-origin bin/syncscope-client \
  lab/bin/syncthing deploy/Dockerfile deploy/entrypoint.sh
```

On the Docker host:

```bash
mkdir -p ~/syncscope-build && tar xzf syncscope-build.tgz -C ~/syncscope-build
cd ~/syncscope-build
docker build -f deploy/Dockerfile -t syncscope:latest .
```

## Run all-in-one demo

```bash
docker run -d --name syncscope --restart unless-stopped \
  --network citadelmd_internal \
  -p 127.0.0.1:8099:8099 -p 127.0.0.1:8090:8090 \
  -v /home/sp/syncscope-data:/data \
  -e ADMIN_TOKEN="$(openssl rand -hex 24)" \
  -e SHARE_SECRET="$(openssl rand -hex 24)" \
  -e SHARE_PUBLIC_URL="https://example/syncscope-share" \
  syncscope:latest
```

The entrypoint on first start:

1. Generates two Syncthing configs (`origin`, `client`) and starts them.
2. Starts the control plane and provisions a demo library + device + grant.
3. Seeds `Photos/2024`, `Photos/2025`, `Documents`.
4. Starts the origin agent (share server on 8090) and client agent (UI on 8099).

Data lives in `/data`; delete the volume to re-provision.

## Reverse proxy

UI and share pages can live under subpaths:

```nginx
location /syncscope/ {
  auth_basic "SyncScope";
  auth_basic_user_file /etc/nginx/syncscope.htpasswd;
  proxy_pass http://syncscope:8099/;
}

location /syncscope-share/ {
  proxy_pass http://syncscope:8090/;
}
```

The UI uses relative asset/API paths, and the origin share server receives
`-share-base` so its links work under the prefix.
