#!/usr/bin/env bash
# Wait for portfoliodb gRPC server to be ready (grpcurl list). Exits 0 when ready, 1 after max tries.
# Usage: scripts/server-ready.sh [target] [max_tries]
#   target defaults to localhost:50051
set -e
TARGET="${1:-localhost:50051}"
MAX_TRIES="${2:-20}"
if ! command -v grpcurl >/dev/null 2>&1; then
	echo "grpcurl not found (install from https://github.com/fullstorydev/grpcurl)" >&2
	exit 1
fi
for i in $(seq 1 "$MAX_TRIES"); do
	if grpcurl -plaintext -connect-timeout 2 "$TARGET" list >/dev/null 2>&1; then
		exit 0
	fi
	sleep 1
done
echo "portfoliodb gRPC not ready" >&2
exit 1
