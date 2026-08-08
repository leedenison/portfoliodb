import { test, expect } from "@playwright/test";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { seedSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, seedFixture, rawQuery, closeDB } from "../helpers/db";
import {
  exportCorporateEvents,
  importCorporateEventsAndWait,
} from "../helpers/api";
import { JobStatus } from "../gen/api/v1/api_pb";
import { AssetClass, IdentifierType } from "../gen/type/v1/type_pb";

// ---------------------------------------------------------------------------
// A PortfolioDB-to-PortfolioDB round trip must not restamp knowledge time.
//
// stock_splits.first_known_at decides whether an option on the underlying
// still needs retroactive OCC adjustment, so a re-import that stamps the
// import time makes every existing option look identified-before-we-knew and
// re-adjusts symbols that were already correct. cash_dividends.first_known_at
// is stored and restored the same way.
//
// The instruments are seeded pre-identified so the import resolves through the
// DB and never reaches an identifier plugin -- no cassette needed.
// ---------------------------------------------------------------------------
test.describe("corporate event export/import round trip", () => {
  let adminSession: string;

  const KNOWN_AT = new Date("2015-03-04T09:30:00.000Z");
  const DIV_KNOWN_AT = new Date("2018-11-02T14:00:00.000Z");
  const AMZN = "e2e00000-0000-0000-0000-000000000101";
  const NVDA = "e2e00000-0000-0000-0000-000000000102";

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
    // One group carrying a split and a dividend we learned of years apart,
    // plus the coverage span saying which dates were asked about.
    const first = await importCorporateEventsAndWait(adminSession, [
      {
        instrument: { type: IdentifierType.MIC_TICKER, value: "AMZN" },
        assetClass: AssetClass.STOCK,
        coverage: [{ from: "2015-01-01", before: "2025-01-01" }],
        events: [
          {
            event: {
              case: "split",
              value: {
                exDate: "2022-06-06",
                splitFrom: "1",
                splitTo: "20",
                firstKnownAt: timestampFromDate(KNOWN_AT),
              },
            },
          },
          {
            event: {
              case: "dividend",
              value: {
                exDate: "2018-11-08",
                amount: "0.25",
                currency: "USD",
                firstKnownAt: timestampFromDate(DIV_KNOWN_AT),
              },
            },
          },
        ],
      },
    ]);
    expect(first.status).toBe(JobStatus.SUCCESS);

    const stored = await rawQuery(
      `SELECT s.first_known_at, s.split_to::text AS split_to
         FROM stock_splits s
         WHERE s.instrument_id = '${AMZN}'`,
    );
    expect(stored).toHaveLength(1);
    expect(new Date((stored[0] as { first_known_at: Date }).first_known_at).toISOString()).toBe(
      KNOWN_AT.toISOString(),
    );

    // Export, and confirm both knowledge times and the coverage reach the wire.
    const exported = await exportCorporateEvents(adminSession);
    const group = exported.find((g) => g.instrument?.value === "AMZN");
    expect(group).toBeDefined();
    expect(group!.coverage).toHaveLength(1);
    expect(group!.coverage[0].before).toBe("2025-01-01");

    const split = group!.events.find((e) => e.event.case === "split");
    expect(split?.event.case === "split" && split.event.value.firstKnownAt).toBeDefined();
    expect(
      timestampDate(
        (split!.event.value as { firstKnownAt: Parameters<typeof timestampDate>[0] }).firstKnownAt,
      ).toISOString(),
    ).toBe(KNOWN_AT.toISOString());

    // The dividend is carried at all, which the JSON format it replaces never
    // did: a rebuild used to lose every dividend it had ever fetched.
    const dividend = group!.events.find((e) => e.event.case === "dividend");
    expect(dividend).toBeDefined();
    expect(
      timestampDate(
        (dividend!.event.value as { firstKnownAt: Parameters<typeof timestampDate>[0] }).firstKnownAt,
      ).toISOString(),
    ).toBe(DIV_KNOWN_AT.toISOString());

    // Re-import the exported groups verbatim, as a restore would.
    const second = await importCorporateEventsAndWait(adminSession, exported);
    expect(second.status).toBe(JobStatus.SUCCESS);

    const after = await rawQuery(
      `SELECT first_known_at FROM stock_splits WHERE instrument_id = '${AMZN}'`,
    );
    expect(after).toHaveLength(1);
    expect(new Date((after[0] as { first_known_at: Date }).first_known_at).toISOString()).toBe(
      KNOWN_AT.toISOString(),
    );

    const divAfter = await rawQuery(
      `SELECT first_known_at, amount::text AS amount
         FROM cash_dividends WHERE instrument_id = '${AMZN}'`,
    );
    expect(divAfter).toHaveLength(1);
    expect(new Date((divAfter[0] as { first_known_at: Date }).first_known_at).toISOString()).toBe(
      DIV_KNOWN_AT.toISOString(),
    );
  });

  test("a split with no declared knowledge time falls back to the envelope", async () => {
    const before = Date.now();
    const job = await importCorporateEventsAndWait(adminSession, [
      {
        instrument: { type: IdentifierType.MIC_TICKER, value: "NVDA" },
        assetClass: AssetClass.STOCK,
        events: [
          {
            event: {
              case: "split",
              value: { exDate: "2024-06-10", splitFrom: "1", splitTo: "10" },
            },
          },
        ],
      },
    ]);
    expect(job.status).toBe(JobStatus.SUCCESS);

    const rows = await rawQuery(
      `SELECT first_known_at FROM stock_splits WHERE instrument_id = '${NVDA}'`,
    );
    expect(rows).toHaveLength(1);
    const stamped = new Date(
      (rows[0] as { first_known_at: Date }).first_known_at,
    ).getTime();
    expect(stamped).toBeGreaterThanOrEqual(before - 60_000);
    expect(stamped).toBeLessThanOrEqual(Date.now() + 60_000);
  });
});
