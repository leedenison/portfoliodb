import { test, expect } from "@playwright/test";
import { create } from "@bufbuild/protobuf";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, queryTxSplitAdjustments, queryInstrumentByIdentifier } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { uploadCSVAndWait } from "../helpers/upload";
import { triggerCorporateEventFetch, importCorporateEventsAndWait } from "../helpers/api";
import { getCounter, closeCountersRedis } from "../helpers/counters";
import {
  AssetClass,
  ImportCorporateEventRowSchema,
  JobStatus,
  SplitRowSchema,
} from "../gen/api/v1/api_pb";

// ---------------------------------------------------------------------------
// Case 1: Transactions uploaded BEFORE the split is discovered.
//
// Upload stock + option txs in a single CSV (pre/post split dates). Then
// trigger the corporate event fetcher which discovers the 4:1 split from
// EODHD. Verify split-adjusted quantities and option OCC/strike update.
// ---------------------------------------------------------------------------
test.describe("stock split: tx uploaded before split", () => {
  let userSession: string;
  let adminSession: string;

  test.beforeAll(async () => {
    await loadCassette("stock-split-tx-first");
    await resetAndSeedBase();
    userSession = await seedSession("user");
    adminSession = await seedSession("admin");
  });

  test.afterAll(async () => {
    await closeCountersRedis();
    await closeRedis();
    await closeDB();
    await unloadCassette();
  });

  test("upload txs, discover split, verify adjustments", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSession);

    // Upload all txs in one CSV to avoid ReplaceTxsInPeriod conflicts
    // between overlapping date ranges on different instruments.
    await uploadCSVAndWait(page, browser, "split-txs.csv", {
      expectedTxCount: 3,
    });

    // Before the split: split_adjusted_quantity == raw quantity.
    const preSplitTxs = await queryTxSplitAdjustments("MIC_TICKER", "AAPL");
    expect(preSplitTxs).toHaveLength(2);
    expect(preSplitTxs[0].split_adjusted_quantity).toBe(25); // no split yet
    expect(preSplitTxs[1].split_adjusted_quantity).toBe(50);

    // Trigger the corporate event fetcher — EODHD returns a 4:1 split.
    // Poll the cycle counter to confirm the fetch actually executed before
    // checking worker idle state.
    const cyclesBefore = await getCounter("corporate_event_fetcher.cycles");
    await triggerCorporateEventFetch(adminSession);
    await expect(async () => {
      const cycles = await getCounter("corporate_event_fetcher.cycles");
      expect(cycles).toBeGreaterThan(cyclesBefore);
    }).toPass({ timeout: 30_000 });
    await waitForWorkersIdle(browser);

    // After the split: tx1 (pre-split) adjusted by factor 4.
    const postSplitTxs = await queryTxSplitAdjustments("MIC_TICKER", "AAPL");
    expect(postSplitTxs).toHaveLength(2);
    expect(postSplitTxs[0].split_adjusted_quantity).toBe(100); // 25 * 4
    expect(postSplitTxs[1].split_adjusted_quantity).toBe(50); // post-split, factor 1

    // Option: OCC should be updated from pre-split to post-split.
    const option = await queryInstrumentByIdentifier(
      "OCC",
      "AAPL250117C00190000",
    );
    expect(option).not.toBeNull();
    expect(option!.strike).toBe(190); // 760 / 4
    expect(option!.asset_class).toBe("OPTION");

    // The option tx split_adjusted_quantity should reflect the split.
    const optTxs = await queryTxSplitAdjustments(
      "OCC",
      "AAPL250117C00190000",
    );
    expect(optTxs).toHaveLength(1);
    expect(optTxs[0].split_adjusted_quantity).toBe(4); // 1 * 4
  });
});

// ---------------------------------------------------------------------------
// Case 2: Split imported BEFORE transactions are uploaded.
//
// Import the AAPL 4:1 split via ImportCorporateEvents (no coverage).
// Then upload stock + option txs. AdjustOCCForKnownSplits adjusts the
// option OCC during identification. Trigger the fetcher so it records
// coverage and runs processOptionSplits.
// ---------------------------------------------------------------------------
test.describe("stock split: split uploaded before tx", () => {
  let userSession: string;
  let adminSession: string;

  test.beforeAll(async () => {
    await loadCassette("stock-split-split-first");
    await resetAndSeedBase();
    userSession = await seedSession("user");
    adminSession = await seedSession("admin");
  });

  test.afterAll(async () => {
    await closeCountersRedis();
    await closeRedis();
    await closeDB();
    await unloadCassette();
  });

  test("import split, upload txs, verify adjustments", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, userSession);

    // Import the 4:1 split (no coverage so the fetcher will still query EODHD).
    const splitEvent = create(ImportCorporateEventRowSchema, {
      identifierType: "MIC_TICKER",
      identifierDomain: "XNAS",
      identifierValue: "AAPL",
      assetClass: AssetClass.ASSET_CLASS_STOCK,
      event: {
        case: "split",
        value: create(SplitRowSchema, {
          exDate: "2024-08-01",
          splitFrom: "1",
          splitTo: "4",
        }),
      },
    });
    const importResult = await importCorporateEventsAndWait(adminSession, [
      splitEvent,
    ]);
    expect(importResult.status).toBe(JobStatus.SUCCESS);

    // Upload all txs in one CSV.
    await uploadCSVAndWait(page, browser, "split-txs.csv", {
      expectedTxCount: 3,
    });

    // Trigger the fetcher to record coverage and process option splits.
    const cyclesBefore = await getCounter("corporate_event_fetcher.cycles");
    await triggerCorporateEventFetch(adminSession);
    await expect(async () => {
      const cycles = await getCounter("corporate_event_fetcher.cycles");
      expect(cycles).toBeGreaterThan(cyclesBefore);
    }).toPass({ timeout: 30_000 });
    await waitForWorkersIdle(browser);

    // Stock: same final state as case 1.
    const stockTxs = await queryTxSplitAdjustments("MIC_TICKER", "AAPL");
    expect(stockTxs).toHaveLength(2);
    expect(stockTxs[0].split_adjusted_quantity).toBe(100); // 25 * 4
    expect(stockTxs[1].split_adjusted_quantity).toBe(50);

    // Option: should be identified directly with post-split OCC.
    const option = await queryInstrumentByIdentifier(
      "OCC",
      "AAPL250117C00190000",
    );
    expect(option).not.toBeNull();
    expect(option!.strike).toBe(190);
    expect(option!.asset_class).toBe("OPTION");

    // Option tx split_adjusted_quantity should reflect the split.
    const optTxs = await queryTxSplitAdjustments(
      "OCC",
      "AAPL250117C00190000",
    );
    expect(optTxs).toHaveLength(1);
    expect(optTxs[0].split_adjusted_quantity).toBe(4); // 1 * 4
  });
});
