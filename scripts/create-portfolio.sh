#!/usr/bin/env bash
# Create a user (if needed) and a portfolio via the API; echo the portfolio id to stdout.
#
# Usage: scripts/create-portfolio.sh
#   PORTFOLIO_ID=$(scripts/create-portfolio.sh)
#
# Requires: grpcurl, jq. Run from repo root with server on localhost:50051.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

HOST="${GRPC_HOST:-localhost:50051}"
GRPCURL_OPTS=(
  -plaintext
  -H 'x-auth-sub: smoke-test'
  -H 'x-auth-name: Smoke Test'
  -H 'x-auth-email: smoke@local'
)

grpcurl "${GRPCURL_OPTS[@]}" \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d '{"auth_sub":"smoke-test","name":"Smoke Test","email":"smoke@local"}' \
  "$HOST" \
  portfoliodb.api.v1.ApiService/CreateUser >/dev/null

RESP=$(grpcurl "${GRPCURL_OPTS[@]}" \
  -import-path proto \
  -proto proto/api/v1/api.proto \
  -d '{"name":"grpcurl ingestion test"}' \
  "$HOST" \
  portfoliodb.api.v1.ApiService/CreatePortfolio)

PORTFOLIO_ID=$(echo "$RESP" | jq -r '.portfolio.id // .portfolio.ID // empty')
if [[ -z "$PORTFOLIO_ID" ]]; then
  echo "Failed to get portfolio id from response:" >&2
  echo "$RESP" >&2
  exit 1
fi
echo "$PORTFOLIO_ID"
