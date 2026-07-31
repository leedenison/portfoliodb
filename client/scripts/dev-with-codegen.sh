#!/bin/sh
# Run from client dev container: repo root is /app. Generate TS bindings once, watch for proto
# changes, then start Next dev. Used by docker-compose.dev.yml.
set -e
cd /app
buf generate --template buf.gen.ts.yaml
# Run the binary by path rather than through npx: the working directory is the
# repo root so that the globs below resolve, but chokidar-cli is a dependency of
# client/, which npx would not find from here.
/app/client/node_modules/.bin/chokidar "proto/**" "buf.gen.ts.yaml" -c "buf generate --template buf.gen.ts.yaml" &
cd /app/client && exec npm run dev
