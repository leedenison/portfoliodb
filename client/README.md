# PortfolioDB Client

Next.js (TypeScript, Tailwind) SPA for the PortfolioDB web UI. Single place for all web UI and API calls to the backend.

## Run locally

```bash
cp .env.example .env.local
# Edit .env.local: set NEXT_PUBLIC_GOOGLE_CLIENT_ID and NEXT_PUBLIC_GRPC_WEB_BASE (see below).
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000). Sign in with Google; the backend (via Envoy on 8080) verifies the ID token and sets a session cookie.

### GOOGLE_OAUTH_CLIENT_ID (client: NEXT_PUBLIC_GOOGLE_CLIENT_ID)

**NEXT_PUBLIC_GOOGLE_CLIENT_ID** is required for "Sign in with Google". It must be the same OAuth 2.0 Client ID as the backend uses (`GOOGLE_OAUTH_CLIENT_ID`). Create a **Web application** client in [Google Cloud Console](https://console.cloud.google.com/apis/credentials) and add `http://localhost:3000` (and your production origin) to the authorized JavaScript origins. Set it in `.env.local`:

```
NEXT_PUBLIC_GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
```

When the SPA talks to the backend via Envoy, set **NEXT_PUBLIC_GRPC_WEB_BASE** to the Envoy URL, e.g. `http://localhost:8080`.

## Build

```bash
npm run build
npm start
```

## Envoy

In full stack setup, Envoy listens on 8080 for gRPC-Web and can serve the built client so the SPA and API share one origin and session cookies work. Set `NEXT_PUBLIC_GRPC_WEB_BASE` to the Envoy URL (e.g. `http://localhost:8080`) when the SPA runs on a different port.
