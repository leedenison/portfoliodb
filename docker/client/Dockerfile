# Build Next.js standalone (same-origin API when served behind Envoy at :8080).
FROM golang:1.25-alpine AS tools
RUN go install github.com/bufbuild/buf/cmd/buf@latest

FROM node:25-alpine AS base
RUN apk add --no-cache libc6-compat
ENV NEXT_TELEMETRY_DISABLED=1

FROM base AS deps
WORKDIR /app
COPY client/package.json client/package-lock.json* ./
# Install all deps including dev -- tailwindcss, postcss, protoc-gen-es
# are needed at build time. The runner stage sets NODE_ENV=production.
RUN npm ci

FROM base AS builder
WORKDIR /app
COPY --from=tools /go/bin/buf /usr/local/bin/buf
COPY --from=deps /app/node_modules ./node_modules
COPY client/ .
COPY proto/ /proto/
COPY buf.yaml /
# Generate TS protobuf bindings (protoc-gen-es from node_modules).
RUN printf 'version: v2\nplugins:\n  - local: /app/node_modules/.bin/protoc-gen-es\n    out: /app/gen\n    opt: target=ts\n' > /tmp/buf.gen.ts.yaml \
    && cd / && buf generate --template /tmp/buf.gen.ts.yaml
RUN mkdir -p public
# Same-origin: SPA and API both at localhost:8080 via Envoy
ENV NEXT_PUBLIC_GRPC_WEB_BASE=
# Google client ID for Sign in with Google (passed from compose; inlined at build time)
ARG GOOGLE_OAUTH_CLIENT_ID
ENV NEXT_PUBLIC_GOOGLE_CLIENT_ID=$GOOGLE_OAUTH_CLIENT_ID
RUN npm run build

FROM base AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
RUN addgroup --system --gid 1001 nodejs
RUN adduser --system --uid 1001 nextjs

COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs
EXPOSE 3000
ENV PORT=3000
ENV HOSTNAME=0.0.0.0
CMD ["node", "server.js"]
