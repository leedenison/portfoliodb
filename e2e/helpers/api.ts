// Typed gRPC client for the main API service.
// Uses @connectrpc/connect with gRPC-Web transport, matching the pattern
// in cassette.ts. Session auth is passed per-call via CallOptions.headers.

import { createClient } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-node";
import { ApiService, JobStatus, type GetJobResponse } from "../gen/api/v1/api_pb";
import { ArchiveKind, ArchivePart } from "../gen/archive/v1/common_pb";
import { AssetClass } from "../gen/type/v1/type_pb";
import { CorporateEventGroupSchema, type CorporateEventGroup } from "../gen/archive/v1/corporate_events_pb";
import { PriceGroupSchema } from "../gen/archive/v1/prices_pb";
import type { TxWindow } from "../gen/archive/v1/txs_pb";
import { SystemArchiveSchema, UserArchiveSchema } from "../gen/archive/v1/archive_pb";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate, timestampNow } from "@bufbuild/protobuf/wkt";

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
 * Import a whole system archive document and wait for the job to complete.
 *
 * The document is passed as it would be read from a file, so a test can hand it
 * a generated or hand-written archive rather than only the parts these helpers
 * know how to build.
 */
export async function importSystemArchiveAndWait(
  sessionId: string,
  archive: MessageInitShape<typeof SystemArchiveSchema>,
  timeoutMs = 60_000,
): Promise<GetJobResponse> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const resp = await client.importSystemArchive({ archive }, { headers });
  return waitForJob(resp.jobId, headers, timeoutMs);
}

/**
 * Queue a system archive import and return the job id without waiting, for a
 * test that wants to do something while the import is still running.
 */
export async function importSystemArchive(
  sessionId: string,
  archive: MessageInitShape<typeof SystemArchiveSchema>,
): Promise<string> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const resp = await client.importSystemArchive({ archive }, { headers });
  return resp.jobId;
}

/**
 * Replace the caller's ignore rules.
 *
 * Written through the RPC rather than through SQL, so what a later export reads
 * is the shape the API stores rather than one this test invented.
 */
export async function setIgnoredAssetClasses(
  sessionId: string,
  rules: { broker: string; account: string; assetClass: AssetClass }[],
): Promise<void> {
  await client.setIgnoredAssetClasses(
    { rules },
    { headers: { Cookie: `${COOKIE_NAME}=${sessionId}` } },
  );
}

/** Import a whole user archive document and wait for the job to complete. */
export async function importUserArchiveAndWait(
  sessionId: string,
  archive: MessageInitShape<typeof UserArchiveSchema>,
  timeoutMs = 60_000,
): Promise<GetJobResponse> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const resp = await client.importUserArchive({ archive }, { headers });
  return waitForJob(resp.jobId, headers, timeoutMs);
}

/** Read one job's status without waiting for it to finish. */
export async function getJobStatus(sessionId: string, jobId: string): Promise<GetJobResponse> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  return client.getJob({ jobId }, { headers });
}

/** Poll one job until it reaches a terminal status. */
async function waitForJob(
  jobId: string,
  headers: Record<string, string>,
  timeoutMs: number,
): Promise<GetJobResponse> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const job = await client.getJob({ jobId }, { headers });
    if (job.status === JobStatus.SUCCESS || job.status === JobStatus.FAILED) {
      return job;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`job ${jobId} did not complete within ${timeoutMs}ms`);
}

/**
 * Import one archive carrying only a price part and wait for the job to
 * complete. Returns the final job status.
 *
 * Importing one kind of data means supplying a document carrying only that
 * part; there is no per-entity endpoint.
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
  const resp = await client.importSystemArchive(
    {
      archive: {
        envelope: {
          formatVersion: 1,
          exportedAt: timestampNow(),
          sourceInstance: "e2e",
          kind: ArchiveKind.SYSTEM,
        },
        prices: { groups },
      },
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
 * Import one archive carrying only a corporate event part and wait for the job
 * to complete. Returns the final job status.
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
  const resp = await client.importSystemArchive(
    {
      archive: {
        envelope: {
          formatVersion: 1,
          exportedAt: timestampNow(),
          sourceInstance: "e2e",
          kind: ArchiveKind.SYSTEM,
        },
        corporateEvents: { groups },
      },
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

/**
 * Drain a corporate-event-only export into its instrument groups.
 *
 * Only that part is asked for, so the stream carries the envelope, the part
 * marker and the groups.
 */
export async function exportCorporateEvents(
  sessionId: string,
): Promise<CorporateEventGroup[]> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const groups: CorporateEventGroup[] = [];
  const req = { parts: [ArchivePart.CORPORATE_EVENTS] };
  for await (const resp of client.exportSystemArchive(req, { headers })) {
    if (resp.item.case === "corporateEventGroup") {
      groups.push(resp.item.value);
    }
  }
  return groups;
}

/**
 * Export the signed-in user's transaction windows, optionally over a period.
 *
 * The period is half-open and scopes the transaction part alone. The export
 * adheres strictly to it, so a group straddling a bound contributes only its
 * in-period legs. The archive page has no period control, so this drives the RPC
 * the way a caller asking for one would.
 */
export async function exportUserTxWindows(
  sessionId: string,
  period?: { from?: Date; before?: Date },
): Promise<TxWindow[]> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  const windows: TxWindow[] = [];
  const req = {
    parts: [ArchivePart.TXS],
    periodFrom: period?.from ? timestampFromDate(period.from) : undefined,
    periodBefore: period?.before ? timestampFromDate(period.before) : undefined,
  };
  for await (const resp of client.exportUserArchive(req, { headers })) {
    if (resp.item.case === "txWindow") {
      windows.push(resp.item.value);
    }
  }
  return windows;
}

/** Trigger the corporate event fetcher worker to run one cycle. */
export async function triggerCorporateEventFetch(
  sessionId: string,
): Promise<void> {
  const headers = { Cookie: `${COOKIE_NAME}=${sessionId}` };
  await client.triggerCorporateEventFetch({}, { headers });
}
