#!/usr/bin/env bash
# Export instruments (with at least one canonical identifier) via ExportInstruments RPC.
# Writes JSON to stdout: one JSON object per streamed Instrument (NDJSON), or use jq -s '.' for an array.
#
# Usage: scripts/export-instruments.sh [EXCHANGE] [OUTPUT_FILE]
#   EXCHANGE     Optional. Filter by exchange (e.g. XNAS). Omit for all exchanges.
#   OUTPUT_FILE  Optional. Write here instead of stdout.
#
# Example: scripts/export-instruments.sh > instruments.json
# Example: scripts/export-instruments.sh XNAS /tmp/nasdaq.json
#
# Requires: grpcurl, jq. Run from repo root with server on GRPC_HOST.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOST="${GRPC_HOST:-localhost:50051}"
GRPCURL_OPTS=(
  -plaintext
  -H 'x-auth-sub: smoke-test'
  -H 'x-auth-name: Smoke Test'
  -H 'x-auth-email: smoke@local'
)

EXCHANGE="${1:-}"
OUTPUT_FILE="${2:-}"

if [[ -n "$EXCHANGE" ]]; then
  REQ="{\"exchange\": \"$EXCHANGE\"}"
else
  REQ='{}'
fi

if [[ -n "$OUTPUT_FILE" ]]; then
  grpcurl "${GRPCURL_OPTS[@]}" \
    -import-path proto \
    -proto proto/api/v1/api.proto \
    -d "$REQ" \
    "$HOST" \
    portfoliodb.api.v1.ApiService/ExportInstruments \
    | jq -s '.' > "$OUTPUT_FILE"
  echo "Exported to $OUTPUT_FILE" >&2
else
  grpcurl "${GRPCURL_OPTS[@]}" \
    -import-path proto \
    -proto proto/api/v1/api.proto \
    -d "$REQ" \
    "$HOST" \
    portfoliodb.api.v1.ApiService/ExportInstruments \
    | jq -s '.'
fi
