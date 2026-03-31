// Typed gRPC client for the main API service.
// Uses @connectrpc/connect with gRPC-Web transport, matching the pattern
// in cassette.ts. Session auth is passed per-call via CallOptions.headers.

import { createClient } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-node";
import {
  ApiService,
  AssetClass,
  JobStatus,
  type GetJobResponse,
} from "../gen/api/v1/api_pb";

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

/** Import prices and wait for the async job to complete. Returns the final job status. */
export async function importPricesAndWait(
  sessionId: string,
  prices: Array<{
    identifierType: string;
    identifierValue: string;
    identifierDomain?: string;
    priceDate: string;
    close: number;
    open?: number;
    high?: number;
    low?: number;
    assetClass?: AssetClass;
  }>,
  timeoutMs = 30_000,
): Promise<GetJobResponse> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const resp = await client.importPrices({ prices }, { headers });
  const jobId = resp.jobId;
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const job = await client.getJob({ jobId }, { headers });
    if (job.status === JobStatus.SUCCESS || job.status === JobStatus.FAILED) {
      return job;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`price import job ${jobId} did not complete within ${timeoutMs}ms`);
}
