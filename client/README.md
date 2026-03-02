# PortfolioDB Client

Next.js (TypeScript, Tailwind) SPA for the PortfolioDB web UI. Single place for all web UI and API calls to the backend.

## Run locally

```bash
cp .env.example .env.local
# Edit .env.local: set NEXT_PUBLIC_GOOGLE_CLIENT_ID (from Google Cloud Console) and
# NEXT_PUBLIC_GRPC_WEB_BASE=http://localhost:8080 when using Envoy.
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000). Sign in with Google; the backend (via Envoy on 8080) verifies the ID token and sets a session cookie.

## Build

```bash
npm run build
npm start
```

## Envoy

In full stack setup, Envoy listens on 8080 for gRPC-Web and can serve the built client so the SPA and API share one origin and session cookies work. Set `NEXT_PUBLIC_GRPC_WEB_BASE` to the Envoy URL (e.g. `http://localhost:8080`) when the SPA runs on a different port.
