FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl jq tini \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY bin/syncscoped bin/syncscope-origin bin/syncscope-client lab/bin/syncthing /app/
COPY deploy/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/syncscoped /app/syncscope-origin /app/syncscope-client /app/syncthing /app/entrypoint.sh

VOLUME ["/data"]
EXPOSE 8099 8090
ENTRYPOINT ["/usr/bin/tini", "--", "/app/entrypoint.sh"]
