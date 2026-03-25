#!/usr/bin/env bash
# Wait for Postgres to accept connections. Exits 0 when ready, 1 after max tries.
# Usage: scripts/postgres-ready.sh <compose-cmd> [max_tries]
#   e.g. scripts/postgres-ready.sh "docker compose -f docker/docker-compose.yml --env-file .env"
set -e
COMPOSE_CMD="$1"
MAX_TRIES="${2:-10}"
for i in $(seq 1 "$MAX_TRIES"); do
	if $COMPOSE_CMD exec -T postgres pg_isready -U portfoliodb 2>/dev/null; then
		exit 0
	fi
	sleep 1
done
echo "Postgres not ready" >&2
exit 1
