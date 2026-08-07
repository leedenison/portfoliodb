import { test, expect } from "@playwright/test";
import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { seedSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, seedFixture, rawQuery, closeDB } from "../helpers/db";
import {
  exportCorporateEvents,
  importCorporateEventsAndWait,
} from "../helpers/api";
import { ImportCorporateEventRowSchema, JobStatus, SplitRowSchema } from "../gen/api/v1/api_pb";
import { AssetClass } from "../gen/type/v1/type_pb";

// ---------------------------------------------------------------------------
// A PortfolioDB-to-PortfolioDB round trip must not restamp knowledge time.
//
// stock_splits.first_known_at decides whether an option on the underlying
// still needs retroactive OCC adjustment, so a re-import that stamps the
// import time makes every existing option look identified-before-we-knew and
// re-adjusts symbols that were already correct.
//
// The instruments are seeded pre-identified so the import resolves through the
// DB and never reaches an identifier plugin -- no cassette needed.
// ---------------------------------------------------------------------------
test.describe("corporate event export/import round trip", () => {
  let adminSession: string;

  const KNOWN_AT = new Date("2015-03-04T09:30:00.000Z");

  test.beforeAll(async () => {
    await resetAndSeedBase();
    await seedFixture("instruments.sql");
    adminSession = await seedSession("admin");
  });

  test.afterAll(async () => {
    await closeRedis();
    await closeDB();
  });

  test("knowledge time survives export and re-import", async () => {
    // Import a split we learned of in 2015.
    const first = await importCorporateEventsAndWait(adminSession, [
      create(ImportCorporateEventRowSchema, {
        identifierType: "MIC_TICKER",
        identifierValue: "AMZN",
        identifierDomain: "",
        assetClass: AssetClass.STOCK,
        event: {
          case: "split",
          value: create(SplitRowSchema, {
            exDate: "2022-06-06",
            splitFrom: "1",
            splitTo: "20",
            firstKnownAt: timestampFromDate(KNOWN_AT),
          }),
        },
      }),
    ]);
    expect(first.status).toBe(JobStatus.SUCCESS);

    const stored = await rawQuery(
      `SELECT s.first_known_at, s.split_to::text AS split_to
         FROM stock_splits s
         WHERE s.instrument_id = 'e2e00000-0000-0000-0000-000000000101'`,
    );
    expect(stored).toHaveLength(1);
    expect(new Date((stored[0] as { first_known_at: Date }).first_known_at).toISOString()).toBe(
      KNOWN_AT.toISOString(),
    );

    // Export, and confirm knowledge time reaches the wire.
    const exported = await exportCorporateEvents(adminSession);
    const splitRows = exported.filter((r) => r.event.case === "split");
    expect(splitRows).toHaveLength(1);
    const exportedSplit = splitRows[0].event.value as {
      exDate: string;
      splitFrom: string;
      splitTo: string;
      firstKnownAt?: Parameters<typeof timestampDate>[0];
    };
    expect(exportedSplit.firstKnownAt).toBeDefined();
    expect(timestampDate(exportedSplit.firstKnownAt!).toISOString()).toBe(
      KNOWN_AT.toISOString(),
    );

    // Re-import the exported rows verbatim, as a restore would.
    const second = await importCorporateEventsAndWait(
      adminSession,
      splitRows.map((r) =>
        create(ImportCorporateEventRowSchema, {
          identifierType: r.identifierType,
          identifierValue: r.identifierValue,
          identifierDomain: r.identifierDomain,
          assetClass: r.assetClass,
          event: { case: "split", value: r.event.value as never },
        }),
      ),
    );
    expect(second.status).toBe(JobStatus.SUCCESS);

    const after = await rawQuery(
      `SELECT first_known_at
         FROM stock_splits
         WHERE instrument_id = 'e2e00000-0000-0000-0000-000000000101'`,
    );
    expect(after).toHaveLength(1);
    expect(new Date((after[0] as { first_known_at: Date }).first_known_at).toISOString()).toBe(
      KNOWN_AT.toISOString(),
    );
  });

  test("a split with no declared knowledge time is stamped on arrival", async () => {
    const before = Date.now();
    const job = await importCorporateEventsAndWait(adminSession, [
      create(ImportCorporateEventRowSchema, {
        identifierType: "MIC_TICKER",
        identifierValue: "NVDA",
        identifierDomain: "",
        assetClass: AssetClass.STOCK,
        event: {
          case: "split",
          value: create(SplitRowSchema, {
            exDate: "2024-06-10",
            splitFrom: "1",
            splitTo: "10",
          }),
        },
      }),
    ]);
    expect(job.status).toBe(JobStatus.SUCCESS);

    const rows = await rawQuery(
      `SELECT first_known_at
         FROM stock_splits
         WHERE instrument_id = 'e2e00000-0000-0000-0000-000000000102'`,
    );
    expect(rows).toHaveLength(1);
    const stamped = new Date(
      (rows[0] as { first_known_at: Date }).first_known_at,
    ).getTime();
    expect(stamped).toBeGreaterThanOrEqual(before - 60_000);
    expect(stamped).toBeLessThanOrEqual(Date.now() + 60_000);
  });
});
