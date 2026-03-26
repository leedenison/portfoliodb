// Typed gRPC client for the main API service.
// Uses @connectrpc/connect with gRPC-Web transport, matching the pattern
// in cassette.ts. Session auth is passed per-call via CallOptions.headers.

import { createClient } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-node";
import { ApiService } from "../gen/api/v1/api_pb";

const COOKIE_NAME = "portfoliodb_session";

const transport = createGrpcWebTransport({
  baseUrl: process.env.E2E_BASE_URL ?? "http://envoy:8080",
});

const client = createClient(ApiService, transport);

export async function setDisplayCurrency(
  sessionId: string,
  currency: string,
): Promise<void> {
  await client.setDisplayCurrency(
    { displayCurrency: currency },
    { headers: { Cookie: `${COOKIE_NAME}=${sessionId}` } },
  );
}
