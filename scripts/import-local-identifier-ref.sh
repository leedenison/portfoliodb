#!/usr/bin/env bash
# Import local identifier reference data into portfoliodb (local_instruments and
# local_instrument_identifiers). Enables the local plugin and loads ref data.
# Assumes the datamodel (including local plugin tables) is already applied.
# Runs psql inside the postgres container via docker compose exec.
#
# Usage: import-local-identifier-ref.sh [REF_JSON_FILE]
#
#   REF_JSON_FILE  JSON with .instruments[].{asset_class,exchange,currency,name,identifiers[]}
#                  (default: scripts/local-identifier-ref.json)
#
# Requires: jq, docker compose. Run from repo root with stack up (docker compose up -d).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REF_FILE="${1:-$SCRIPT_DIR/local-identifier-ref.json}"
COMPOSE_FILE="${COMPOSE_FILE:-docker/server/docker-compose.yml}"

usage() {
  echo "Usage: $(basename "$0") [REF_JSON_FILE]" >&2
  echo "  REF_JSON_FILE  Default: scripts/local-identifier-ref.json" >&2
  exit 1
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
fi

if [[ ! -f "$REF_FILE" ]]; then
  echo "Reference file not found: $REF_FILE" >&2
  exit 1
fi

psql_exec() {
  docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U portfoliodb -d portfoliodb "$@"
}

# Enable local plugin (idempotent)
echo "Ensuring local plugin is enabled..."
psql_exec -q -c "
  INSERT INTO identifier_plugin_config (plugin_id, enabled, precedence, config)
  VALUES ('local', true, 10, NULL)
  ON CONFLICT (plugin_id) DO UPDATE SET enabled = true, precedence = 10;
"

# Clear existing reference data so re-run replaces it
echo "Clearing existing local reference data..."
psql_exec -q -c "TRUNCATE local_instruments CASCADE;"

# Insert instruments and identifiers
count=$(jq '.instruments | length' "$REF_FILE")
for i in $(seq 0 $((count - 1))); do
  inst=$(jq -c ".instruments[$i]" "$REF_FILE")
  asset_class=$(jq -r '.asset_class // ""' <<< "$inst")
  exchange=$(jq -r '.exchange // ""' <<< "$inst")
  currency=$(jq -r '.currency // ""' <<< "$inst")
  name=$(jq -r '.name // ""' <<< "$inst")
  # Escape single quotes for SQL
  name_sql="${name//\'/\'\'}"

  id=$(psql_exec -t -A -c "
    INSERT INTO local_instruments (asset_class, exchange, currency, name)
    VALUES (
      NULLIF('${asset_class//\'/\'\'}', ''),
      NULLIF('${exchange//\'/\'\'}', ''),
      NULLIF('${currency//\'/\'\'}', ''),
      NULLIF('$name_sql', '')
    )
    RETURNING id;
  " | head -1 | tr -d '\r\n')

  if [[ -z "$id" ]]; then
    echo "Failed to insert instrument at index $i" >&2
    exit 1
  fi

  id_count=$(jq ".instruments[$i].identifiers | length" "$REF_FILE")
  for j in $(seq 0 $((id_count - 1))); do
    id_type=$(jq -r ".instruments[$i].identifiers[$j].identifier_type" "$REF_FILE")
    value=$(jq -r ".instruments[$i].identifiers[$j].value" "$REF_FILE")
    id_type_sql="${id_type//\'/\'\'}"
    value_sql="${value//\'/\'\'}"
    psql_exec -q -c "
      INSERT INTO local_instrument_identifiers (instrument_id, identifier_type, value)
      VALUES ('$id', '$id_type_sql', '$value_sql');
    "
  done
done

echo "Imported $count instruments from $REF_FILE"
