import { beforeEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { JobStatus, GetJobResponseSchema, ListTxsResponseSchema } from "@/gen/api/v1/api_pb";
import { Broker } from "@/gen/type/v1/type_pb";
import { UpsertTxsResponseSchema } from "@/gen/ingestion/v1/ingestion_pb";
import type { RunLogEntry } from "../lib/run-log";

const loadConfig = vi.fn();
const getSessionId = vi.fn();
const listTxs = vi.fn();
const upsertTxs = vi.fn();
const getJob = vi.fn();
const captureExport = vi.fn();
const recordRun = vi.fn();

vi.mock("../config", () => ({ loadConfig: () => loadConfig() }));
vi.mock("../lib/session", () => ({ getSessionId: () => getSessionId() }));
vi.mock("../lib/api", () => ({
  listTxs: (...a: unknown[]) => listTxs(...a),
  upsertTxs: (...a: unknown[]) => upsertTxs(...a),
  getJob: (...a: unknown[]) => getJob(...a),
}));
vi.mock("./export", () => ({ captureExport: (...a: unknown[]) => captureExport(...a) }));
vi.mock("../lib/run-log", () => ({ recordRun: (e: RunLogEntry) => recordRun(e) }));

const { sync } = await import("./sync");

const CONFIG = {
  portfoliodbOrigin: "http://localhost:8080",
  currency: "GBP",
  historyStartDate: "",
  lookbackDays: 14,
  timeZone: "Europe/London",
};

/** A settled Fidelity cash row, in the shape the JSON endpoint returns. */
function row(overrides: Record<string, unknown> = {}) {
  return {
    accountNumber: "ACC-1",
    transactionType: "Cash Interest",
    assetName: "Cash",
    isin: "AA00S0000000",
    sedol: "S000000",
    units: 1.26,
    valuation: 1.26,
    pricePerUnit: 1,
    currency: "GBP",
    status: "Completed",
    debitCreditIndicator: "CREDIT",
    dealDate: "15/07/2026",
    settlementDate: "21/07/2026",
    ...overrides,
  };
}

function withLatest(date: Date | null) {
  listTxs.mockResolvedValue(
    create(ListTxsResponseSchema, {
      txs: date ? [{ broker: Broker.FIDELITY, tx: { orderDate: timestampFromDate(date) } }] : [],
    })
  );
}

const run = () =>
  sync({ broker: Broker.FIDELITY, now: new Date("2026-07-27T09:00:00Z"), sleep: async () => {} });

describe("sync", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    loadConfig.mockResolvedValue({ ...CONFIG });
    getSessionId.mockResolvedValue("session-abc");
    withLatest(new Date(2026, 6, 20));
    captureExport.mockResolvedValue({ status: 200, body: JSON.stringify([row()]) });
    upsertTxs.mockResolvedValue(create(UpsertTxsResponseSchema, { jobId: "job-1" }));
    getJob.mockResolvedValue(create(GetJobResponseSchema, { status: JobStatus.SUCCESS }));
  });

  it("uploads the requested window, not the range of the rows returned", async () => {
    const { entry } = await run();

    expect(entry.status).toBe("success");
    const req = upsertTxs.mock.calls[0]![2] as {
      window: { periodFrom: { seconds: bigint }; periodBefore: { seconds: bigint } };
    };
    // Window is 14 days before the last known transaction (20 Jul) up to the
    // start of today (27 Jul) -- wider than the single 21 Jul row that came
    // back, which is what lets the replace delete anything the broker
    // cancelled.
    expect(Number(req.window.periodFrom.seconds)).toBe(Math.floor(new Date(2026, 6, 6).getTime() / 1000));
    expect(Number(req.window.periodBefore.seconds)).toBe(
      Math.floor(new Date(2026, 6, 27).getTime() / 1000)
    );
  });

  // The extension drove the broker's export moments ago, so the identifiers in
  // it are the ones the broker lists now. Without a stated vintage the server
  // would stamp the upload anyway, but the run's own clock is what this path
  // actually knows.
  it("states the run's own time as the vintage of the export", async () => {
    await run();
    const req = upsertTxs.mock.calls[0]![2] as { exportedAt: { seconds: bigint } };
    expect(Number(req.exportedAt.seconds)).toBe(
      Math.floor(new Date("2026-07-27T09:00:00Z").getTime() / 1000)
    );
  });

  it("sends the source string the web client already uses", async () => {
    await run();
    const req = upsertTxs.mock.calls[0]![2] as { window: { source: string } };
    expect(req.window.source).toBe("Fidelity:web:fidelity-csv");
  });

  it("leaves the share count undeclared when the converter does not declare one", async () => {
    await run();
    const req = upsertTxs.mock.calls[0]![2] as {
      window: { postings: { shareCountBasis?: string }[] };
    };
    // Declaring a basis on an as-traded export would tell the server the
    // quantities are already post-split, and historical rows spanning a split
    // would be left unadjusted. The archive states it per posting, so an
    // as-traded export states it nowhere.
    for (const p of req.window.postings) {
      expect(p.shareCountBasis).toBeUndefined();
    }
  });

  it("asks only for the most recent transaction of that broker", async () => {
    await run();
    expect(listTxs.mock.calls[0]![2]).toMatchObject({
      broker: Broker.FIDELITY,
      descending: true,
      pageSize: 1,
    });
  });

  it("uploads despite dropped rows, but records them and warns", async () => {
    captureExport.mockResolvedValue({
      status: 200,
      body: JSON.stringify([row(), row({ transactionType: "Corporate Action Reinvestment" })]),
    });

    const { entry } = await run();

    expect(upsertTxs).toHaveBeenCalled();
    expect(entry.status).toBe("warning");
    expect(entry.droppedCount).toBe(1);
    expect(entry.droppedTypes).toEqual(["Corporate Action Reinvestment"]);
    // The surviving Cash Interest row. The income leg that balances it is the
    // server's, so it is not in what was uploaded.
    expect(entry.txCount).toBe(1);
    expect(entry.rowCount).toBe(2);
    // The row itself, not just a count: a replace deleted whatever it stored, so
    // the run has to be able to say which row and why.
    expect(entry.droppedRows).toEqual([
      { rowIndex: 2, field: "type", message: "Unknown transaction type: Corporate Action Reinvestment" },
    ]);
  });

  it("records a row dropped for a reason that names no type", async () => {
    // A malformed date contributes nothing to droppedTypes, so without the
    // per-row detail the run would show a bare count and no way to find it.
    captureExport.mockResolvedValue({
      status: 200,
      body: JSON.stringify([row(), row({ dealDate: "not a date", settlementDate: null })]),
    });

    const { entry } = await run();

    expect(entry.status).toBe("warning");
    expect(entry.droppedCount).toBe(1);
    expect(entry.droppedTypes).toBeUndefined();
    expect(entry.droppedRows).toEqual([
      { rowIndex: 2, field: "dealDate", message: "Invalid or missing date" },
    ]);
  });

  it("refuses to upload when nothing converted", async () => {
    // The server marks a bulk upload with no storable rows successful without
    // performing the replace, so uploading would report success and delete
    // nothing.
    captureExport.mockResolvedValue({ status: 200, body: "[]" });

    const { entry } = await run();

    expect(upsertTxs).not.toHaveBeenCalled();
    expect(entry.status).toBe("failed");
    expect(entry.error).toContain("no transactions");
  });

  it("reports up to date without fetching anything", async () => {
    withLatest(new Date(2026, 6, 27));
    loadConfig.mockResolvedValue({ ...CONFIG, lookbackDays: 0 });

    const { entry } = await run();

    expect(entry.status).toBe("up-to-date");
    expect(captureExport).not.toHaveBeenCalled();
  });

  it("explains what to configure on a first run with no history start date", async () => {
    withLatest(null);

    const { entry } = await run();

    expect(entry.status).toBe("failed");
    expect(entry.error).toContain("history start date");
    expect(captureExport).not.toHaveBeenCalled();
  });

  it("uses the history start date on a first run when it is set", async () => {
    withLatest(null);
    loadConfig.mockResolvedValue({ ...CONFIG, historyStartDate: "2025-01-06" });

    await run();

    const req = upsertTxs.mock.calls[0]![2] as { window: { periodFrom: { seconds: bigint } } };
    expect(Number(req.window.periodFrom.seconds)).toBe(Math.floor(new Date(2025, 0, 6).getTime() / 1000));
  });

  it("stops before fetching when there is no session", async () => {
    getSessionId.mockResolvedValue("");

    const { entry } = await run();

    expect(entry.error).toContain("Connect");
    expect(captureExport).not.toHaveBeenCalled();
  });

  it("records the job's own errors when ingestion fails", async () => {
    getJob.mockResolvedValue(
      create(GetJobResponseSchema, {
        status: JobStatus.FAILED,
        validationErrors: [{ rowIndex: 3, field: "quantity", message: "must be a number" }],
      })
    );

    const { entry } = await run();

    expect(entry.status).toBe("failed");
    expect(entry.jobId).toBe("job-1");
    expect(entry.jobErrors?.[0]).toContain("must be a number");
  });

  it("waits for a running job to finish", async () => {
    getJob
      .mockResolvedValueOnce(create(GetJobResponseSchema, { status: JobStatus.RUNNING }))
      .mockResolvedValueOnce(create(GetJobResponseSchema, { status: JobStatus.SUCCESS }));

    const { entry } = await run();

    expect(getJob).toHaveBeenCalledTimes(2);
    expect(entry.status).toBe("success");
  });

  it("records every outcome, including the failures", async () => {
    captureExport.mockRejectedValue(new Error("HTTP 302"));

    const { entry } = await run();

    expect(recordRun).toHaveBeenCalledWith(entry);
    expect(entry.error).toContain("HTTP 302");
    // The window is recorded even though the export failed, so a failed run still
    // says what it was trying to fetch.
    expect(entry.window).toEqual({ from: "06/07/2026", to: "26/07/2026" });
  });
});
