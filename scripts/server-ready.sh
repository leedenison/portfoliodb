#!/usr/bin/env bash
# Wait for portfoliodb gRPC server to report SERVING via grpc_health_probe. Exits 0 when ready, 1 after max tries.
# Probes from inside the portfoliodb container so no host-side grpc tooling is required.
# Usage: scripts/server-ready.sh <compose-cmd> [max_tries]
#   e.g. scripts/server-ready.sh "docker compose -p portfoliodb-dev -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --env-file .env"
set -e
COMPOSE_CMD="$1"
MAX_TRIES="${2:-20}"
for i in $(seq 1 "$MAX_TRIES"); do
	if $COMPOSE_CMD exec -T portfoliodb grpc-health-probe -addr=localhost:50051 -connect-timeout=2s >/dev/null 2>&1; then
		exit 0
	fi
	sleep 1
done
echo "portfoliodb gRPC not ready" >&2
exit 1
