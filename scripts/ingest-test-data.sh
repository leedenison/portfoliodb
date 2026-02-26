#!/usr/bin/env bash
# Create a portfolio via grpcurl, then ingest a transactions JSON file into it.
# Usage: ingest-test-data.sh [TX_JSON_FILE]
#
#   $ make docker-clean
#   $ docker compose -f docker/server/docker-compose.yml up -d
#   $ cat server/migrations/001_initial.sql | docker exec -i portfoliodb-postgres psql -U portfoliodb -d portfoliodb
#   $ scripts/ingest-test-data.sh scripts/50-transactions.json
# 
# Requires: grpcurl, jq. Run from repo root with server on localhost:50051.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  echo "Usage: $(basename "$0") [TX_JSON_FILE]" >&2
  echo "  TX_JSON_FILE  UpsertTxsRequest JSON (default: testdata/ingestion_50_txs.json)" >&2
  exit 1
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
fi

if [[ -n "${1:-}" ]]; then
  TX_FILE="$1"
else
  usage
fi

TEST_DATA="$TX_FILE"
HOST="${GRPC_HOST:-localhost:50051}"

GRPCURL_OPTS=(
  -plaintext
  -H 'x-auth-sub: smoke-test'
  -H 'x-auth-name: Smoke Test'
  -H 'x-auth-email: smoke@local'
)

if [[ ! -f "$TEST_DATA" ]]; then
  echo "Transactions file not found: $TEST_DATA" >&2
  exit 1
fi
echo "Using transactions file: $TEST_DATA"

echo "Ensuring user exists..."
grpcurl "${GRPCURL_OPTS[@]}" \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d '{"auth_sub":"smoke-test","name":"Smoke Test","email":"smoke@local"}' \
  "$HOST" \
  portfoliodb.api.v1.ApiService/CreateUser >/dev/null

echo "Creating portfolio..."
RESP=$(grpcurl "${GRPCURL_OPTS[@]}" \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d '{"name":"grpcurl ingestion test"}' \
  "$HOST" \
  portfoliodb.api.v1.ApiService/CreatePortfolio)

# grpcurl JSON may use .portfolio.id (camelCase id) or .portfolio.id (same)
PORTFOLIO_ID=$(echo "$RESP" | jq -r '.portfolio.id // .portfolio.ID // empty')
if [[ -z "$PORTFOLIO_ID" ]]; then
  echo "Failed to get portfolio id from response:" >&2
  echo "$RESP" >&2
  exit 1
fi
echo "Portfolio ID: $PORTFOLIO_ID"

echo "Ingesting test transactions..."
INGEST_RESP=$(jq --arg id "$PORTFOLIO_ID" '.portfolio_id = $id' "$TEST_DATA" | \
  grpcurl "${GRPCURL_OPTS[@]}" \
    -import-path proto \
    -proto proto/ingestion/v1/ingestion.proto \
    -d @ \
    "$HOST" \
    portfoliodb.ingestion.v1.IngestionService/UpsertTxs)

JOB_ID=$(echo "$INGEST_RESP" | jq -r '.jobId // .job_id // empty')
if [[ -z "$JOB_ID" ]]; then
  echo "Ingestion response did not include job_id; cannot poll. Response: $INGEST_RESP" >&2
  exit 1
fi
echo "Job ID: $JOB_ID"

echo "Polling job status (0.5s interval, max 2s)..."
DEADLINE=$(($(date +%s) + 2))
while true; do
  JOB_RESP=$(grpcurl "${GRPCURL_OPTS[@]}" \
    -import-path proto \
    -proto proto/api/v1/api.proto \
    -d "{\"job_id\": \"$JOB_ID\"}" \
    "$HOST" \
    portfoliodb.api.v1.ApiService/GetJob)
  STATUS=$(echo "$JOB_RESP" | jq -r '.status // .Status // empty')
  if [[ "$STATUS" == "SUCCESS" ]]; then
    echo "Job completed successfully."
    break
  fi
  if [[ "$STATUS" == "FAILED" ]]; then
    echo "Job failed. Response:" >&2
    echo "$JOB_RESP" | jq '.' >&2
    exit 1
  fi
  if [[ $(date +%s) -ge "$DEADLINE" ]]; then
    echo "Timed out waiting for job (2s). Last status: $STATUS" >&2
    exit 1
  fi
  sleep 0.5
done

echo "Fetching holdings (checking for AAPL)..."
HOLDINGS_RESP=$(grpcurl "${GRPCURL_OPTS[@]}" \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d "{\"portfolio_id\": \"$PORTFOLIO_ID\"}" \
  "$HOST" \
  portfoliodb.api.v1.ApiService/GetHoldings)

# grpcurl may use camelCase (instrumentDescription) or snake_case (instrument_description)
AAPL_QTY=$(echo "$HOLDINGS_RESP" | jq -r '
  (.holdings // [])[] |
  select(.instrumentDescription == "AAPL" or .instrument_description == "AAPL") |
  .quantity // .Quantity
' | head -1)
if [[ -z "$AAPL_QTY" || "$AAPL_QTY" == "null" ]]; then
  echo "Holdings check failed: no AAPL holding found. Response:" >&2
  echo "$HOLDINGS_RESP" >&2
  exit 1
fi
echo "AAPL holding quantity: $AAPL_QTY"
