#!/usr/bin/env bash
# Run the DB initialise SQL script against the Postgres container.
# No-op when script path is empty or the file does not exist.
# Usage: scripts/init-db.sh <compose-cmd> <script-path>
#   e.g. scripts/init-db.sh "docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml --env-file .env" local/dev-init.sql
set -e
COMPOSE_CMD="$1"
SCRIPT_PATH="$2"
if [ -z "$SCRIPT_PATH" ]; then
	echo "init-db: skipped (DB_INITIALISE_SCRIPT not set)"
	exit 0
fi
if [ ! -f "$SCRIPT_PATH" ]; then
	echo "init-db: skipped (file does not exist: $SCRIPT_PATH)"
	exit 0
fi
cat "$SCRIPT_PATH" | $COMPOSE_CMD exec -T postgres psql -U portfoliodb -d portfoliodb -f - >/dev/null
echo "init-db: ran $SCRIPT_PATH"
