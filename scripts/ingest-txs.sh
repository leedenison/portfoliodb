#!/usr/bin/env bash
# Import transactions from a JSON file into the given portfolio; echo the job id to stdout.
#
# Usage: scripts/ingest-txs.sh PORTFOLIO_ID TX_JSON_FILE
#   JOB_ID=$(scripts/ingest-txs.sh "$PORTFOLIO_ID" scripts/50-transactions.json)
#
# Requires: grpcurl, jq. Run from repo root with server on localhost:50051.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: $(basename "$0") PORTFOLIO_ID TX_JSON_FILE" >&2
  exit 1
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  echo "Usage: $(basename "$0") PORTFOLIO_ID TX_JSON_FILE" >&2
  echo "  PORTFOLIO_ID  UUID of the portfolio to ingest into" >&2
  echo "  TX_JSON_FILE  UpsertTxsRequest JSON (portfolio_id in file is overwritten)" >&2
  exit 0
fi

if [[ $# -lt 2 ]]; then
  usage
fi

PORTFOLIO_ID="$1"
TX_FILE="$2"
HOST="${GRPC_HOST:-localhost:50051}"
GRPCURL_OPTS=(
  -plaintext
  -H 'x-auth-sub: smoke-test'
  -H 'x-auth-name: Smoke Test'
  -H 'x-auth-email: smoke@local'
)

if [[ ! -f "$TX_FILE" ]]; then
  echo "Transactions file not found: $TX_FILE" >&2
  exit 1
fi

INGEST_RESP=$(jq --arg id "$PORTFOLIO_ID" '.portfolio_id = $id' "$TX_FILE" | \
  grpcurl "${GRPCURL_OPTS[@]}" \
    -import-path proto \
    -proto proto/ingestion/v1/ingestion.proto \
    -d @ \
    "$HOST" \
    portfoliodb.ingestion.v1.IngestionService/UpsertTxs)

JOB_ID=$(echo "$INGEST_RESP" | jq -r '.jobId // .job_id // empty')
if [[ -z "$JOB_ID" ]]; then
  echo "Ingestion response did not include job_id. Response: $INGEST_RESP" >&2
  exit 1
fi
echo "$JOB_ID"
