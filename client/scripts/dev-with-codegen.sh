#!/bin/sh
# Run from client dev container: repo root is /app. Generate TS bindings once, watch for proto
# changes, then start Next dev. Used by docker-compose.dev.yml.
set -e
cd /app
buf generate --template buf.gen.ts.yaml
npx chokidar "proto/**" "buf.gen.ts.yaml" -c "buf generate --template buf.gen.ts.yaml" &
cd /app/client && exec npm run dev
