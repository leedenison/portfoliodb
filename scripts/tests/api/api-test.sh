#!/usr/bin/env bash
# End-to-end test: bring up stack, apply migrations, load local ref data, ingest transactions,
# then verify resolution (ref symbols resolved, non-ref symbols broker-description-only) and holdings.
#
# Usage: scripts/tests/api/api-test.sh
#
# Steps:
#   1. make docker-clean
#   2. docker compose -f docker/server/docker-compose.yml up -d
#   3. Apply main migration and local plugin migration via docker compose exec
#   4. scripts/tests/api/import-local-identifier-ref.sh
#   5. scripts/tests/api/create-portfolio.sh -> PORTFOLIO_ID
#   6. scripts/tests/api/ingest-txs.sh PORTFOLIO_ID scripts/tests/api/50-transactions.json -> JOB_ID, then poll until success
#   7. Verify resolution and holdings
#
# Requires: grpcurl, jq, psql. Run from repo root.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

COMPOSE_FILE="docker/server/docker-compose.yml"
HOST="${GRPC_HOST:-localhost:50051}"
GRPCURL_OPTS=(
  -plaintext
  -H 'x-auth-sub: smoke-test'
  -H 'x-auth-name: Smoke Test'
  -H 'x-auth-email: smoke@local'
)

echo "=== Step 1: docker-clean ==="
make docker-clean

echo "=== Step 2: docker compose up -d ==="
docker compose -f docker/server/docker-compose.yml up -d

echo "Waiting for Postgres..."
for i in 1 2 3 4 5 6 7 8 9 10; do
  if docker compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U portfoliodb -d portfoliodb 2>/dev/null; then
    break
  fi
  sleep 1
  if [[ $i -eq 10 ]]; then
    echo "Postgres not ready" >&2
    exit 1
  fi
done

echo "Waiting for gRPC server (reflection API)..."
for i in $(seq 1 30); do
  if grpcurl -plaintext "$HOST" list >/dev/null 2>&1; then
    break
  fi
  sleep 1
  if [[ $i -eq 30 ]]; then
    echo "gRPC server not ready" >&2
    exit 1
  fi
done

echo "=== Step 3: Apply migrations ==="
cat server/migrations/001_initial.sql | docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U portfoliodb -d portfoliodb -q
cat server/plugins/local/identifier/migrations/001_instrument_ref.sql | docker compose -f "$COMPOSE_FILE" exec -T postgres psql -U portfoliodb -d portfoliodb -q

echo "=== Step 4: Import local identifier reference data ==="
scripts/tests/api/import-local-identifier-ref.sh

echo "=== Step 5: Create portfolio ==="
PORTFOLIO_ID=$(scripts/tests/api/create-portfolio.sh)
echo "Portfolio ID: $PORTFOLIO_ID"

echo "=== Step 6: Ingest test transactions ==="
JOB_ID=$(scripts/tests/api/ingest-txs.sh "$PORTFOLIO_ID" scripts/tests/api/50-transactions.json)
echo "Job ID: $JOB_ID"

echo "Polling job status (0.5s interval, max 30s)..."
DEADLINE=$(($(date +%s) + 30))
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
    echo "Timed out waiting for job. Last status: $STATUS" >&2
    exit 1
  fi
  sleep 0.5
done

echo "=== Step 7: Verify resolution (ref lookup vs broker-description-only) ==="

RESOLVED_IN_REF=$'Apple Inc. (AAPL)\nAlphabet Inc. Class A (GOOGL)\nMicrosoft Corporation (MSFT)\nVanguard Total Stock Market ETF (VTI)\nAmazon.com Inc. (AMZN)\nNVIDIA Corporation (NVDA)\nMeta Platforms Inc. (META)'
UNRESOLVED_NOT_IN_REF=$'The Home Depot Inc. (HD)\nBerkshire Hathaway Inc. Class B (BRK.B)\nJohnson & Johnson (JNJ)'
IDERR_DESCRIPTIONS=$(echo "$JOB_RESP" | jq -r '
  (.identification_errors // .identificationErrors // [])[] |
  .instrument_description // .instrumentDescription // empty
' | sort -u)
IDERR_BROKER_ONLY=$(echo "$JOB_RESP" | jq -r '
  (.identification_errors // .identificationErrors // [])[] |
  select((.message // .Message) == "broker description only") |
  .instrument_description // .instrumentDescription // empty
' | sort -u)

missing_resolved=""
while IFS= read -r sym; do
  [[ -z "$sym" ]] && continue
  if echo "$IDERR_DESCRIPTIONS" | grep -qFx "$sym"; then
    missing_resolved="${missing_resolved} ${sym}"
  fi
done <<< "$RESOLVED_IN_REF"
if [[ -n "$missing_resolved" ]]; then
  echo "Verification failed: these symbols are in local ref but appeared in identification_errors:$missing_resolved" >&2
  exit 1
fi

found_unresolved=""
while IFS= read -r sym; do
  [[ -z "$sym" ]] && continue
  if echo "$IDERR_BROKER_ONLY" | grep -qFx "$sym"; then
    found_unresolved="${found_unresolved} ${sym}"
  fi
done <<< "$UNRESOLVED_NOT_IN_REF"
if [[ -z "$found_unresolved" ]]; then
  echo "Verification failed: expected at least one of (HD, BRK.B, JNJ) to appear as 'broker description only' in identification_errors. Got: $IDERR_BROKER_ONLY" >&2
  exit 1
fi
echo "Resolution check OK: ref symbols not in errors; at least one non-ref symbol (e.g.$found_unresolved) is broker-description-only."

echo "=== Step 8: Verifying holdings (Apple Inc. (AAPL)) ==="
HOLDINGS_RESP=$(grpcurl "${GRPCURL_OPTS[@]}" \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d "{\"portfolio_id\": \"$PORTFOLIO_ID\"}" \
  "$HOST" \
  portfoliodb.api.v1.ApiService/GetHoldings)
AAPL_QTY=$(echo "$HOLDINGS_RESP" | jq -r '
  (.holdings // [])[] |
  select(.instrumentDescription == "Apple Inc. (AAPL)" or .instrument_description == "Apple Inc. (AAPL)") |
  .quantity // .Quantity
' | head -1)
if [[ -z "$AAPL_QTY" || "$AAPL_QTY" == "null" ]]; then
  echo "Holdings check failed: no Apple Inc. (AAPL) holding found. Response:" >&2
  echo "$HOLDINGS_RESP" >&2
  exit 1
fi
echo "Apple Inc. (AAPL) holding quantity: $AAPL_QTY"

echo "=== E2E test passed ==="
