// Typed gRPC client for the main API service.
// Uses @connectrpc/connect with gRPC-Web transport, matching the pattern
// in cassette.ts. Session auth is passed per-call via CallOptions.headers.

import { createClient } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-node";
import { ApiService, JobStatus, type GetJobResponse } from "../gen/api/v1/api_pb";
import { ArchiveKind } from "../gen/archive/v1/common_pb";
import { CorporateEventGroupSchema, type CorporateEventGroup } from "../gen/archive/v1/corporate_events_pb";
import { PriceGroupSchema } from "../gen/archive/v1/prices_pb";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { timestampNow } from "@bufbuild/protobuf/wkt";

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

/**
 * Import one archive's worth of price groups and wait for the async job to
 * complete. Returns the final job status.
 *
 * The envelope is built here: exported_at is knowledge time, and a test that had
 * to supply it would be stating something it does not care about.
 */
export async function importPricesAndWait(
  sessionId: string,
  groups: MessageInitShape<typeof PriceGroupSchema>[],
  timeoutMs = 30_000,
): Promise<GetJobResponse> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const resp = await client.importPrices(
    {
      envelope: {
        formatVersion: 1,
        exportedAt: timestampNow(),
        sourceInstance: "e2e",
        kind: ArchiveKind.ADMIN,
      },
      prices: { groups },
    },
    { headers },
  );
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

/**
 * Import one archive's worth of corporate event groups and wait for the async
 * job to complete. Returns the final job status.
 *
 * The envelope is built here for the same reason importPricesAndWait builds
 * one: exported_at is knowledge time, and a test that had to supply it would be
 * stating something it does not care about. A test that does care passes one.
 */
export async function importCorporateEventsAndWait(
  sessionId: string,
  groups: MessageInitShape<typeof CorporateEventGroupSchema>[],
  timeoutMs = 30_000,
): Promise<GetJobResponse> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const resp = await client.importCorporateEvents(
    {
      envelope: {
        formatVersion: 1,
        exportedAt: timestampNow(),
        sourceInstance: "e2e",
        kind: ArchiveKind.ADMIN,
      },
      corporateEvents: { groups },
    },
    { headers },
  );
  const jobId = resp.jobId;
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const job = await client.getJob({ jobId }, { headers });
    if (job.status === JobStatus.SUCCESS || job.status === JobStatus.FAILED) {
      return job;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(
    `corporate event import job ${jobId} did not complete within ${timeoutMs}ms`,
  );
}

/** Drain the ExportCorporateEvents stream into its instrument groups. */
export async function exportCorporateEvents(
  sessionId: string,
): Promise<CorporateEventGroup[]> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const groups: CorporateEventGroup[] = [];
  for await (const resp of client.exportCorporateEvents({}, { headers })) {
    if (resp.item.case === "group") {
      groups.push(resp.item.value);
    }
  }
  return groups;
}

/** Trigger the corporate event fetcher worker to run one cycle. */
export async function triggerCorporateEventFetch(
  sessionId: string,
): Promise<void> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  await client.triggerCorporateEventFetch({}, { headers });
}
