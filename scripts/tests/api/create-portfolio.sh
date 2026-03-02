#!/usr/bin/env bash
# Obtain a session via Auth (using ID_TOKEN), then create a portfolio. Echo the portfolio id to stdout.
#
# Usage: ID_TOKEN=<google-id-token> scripts/tests/api/create-portfolio.sh
#
# Requires: grpcurl, jq. Run from repo root with server on localhost:50051.
# Obtain an ID token as part of your flow (e.g. Google Sign-In or test OAuth); set ID_TOKEN.

set -euo pipefail

HOST="${GRPC_HOST:-localhost:50051}"

if [[ -z "${ID_TOKEN:-}" ]]; then
  echo "ID_TOKEN is required. Obtain a Google ID token (e.g. via Google Sign-In) and set ID_TOKEN." >&2
  exit 1
fi

# Call Auth to establish a session; server returns session_id for programmatic clients.
AUTH_RESP=$(grpcurl -plaintext \
  -import-path proto \
  -proto proto/auth/v1/auth.proto \
  -d '{"google_id_token":"'"$ID_TOKEN"'"}' \
  "$HOST" \
  portfoliodb.auth.v1.AuthService/Auth)

SESSION_ID=$(echo "$AUTH_RESP" | jq -r '.sessionId // .session_id // empty')
if [[ -z "$SESSION_ID" ]]; then
  echo "Auth failed or did not return session_id/sessionId. Response:" >&2
  echo "$AUTH_RESP" >&2
  exit 1
fi

RESP=$(grpcurl -plaintext \
  -H "x-session-id: $SESSION_ID" \
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
