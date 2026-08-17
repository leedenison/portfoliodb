import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { isRecordingSuite } from "../helpers/vcr";
import { waitForWorkersIdle, getWorkerCycles, waitForWorkerCycle } from "../helpers/workers";
import { uploadArchiveAndWait, type ArchiveUpload } from "../helpers/upload";

// The postings are dated relative to the run rather than committed with the
// dates the cassette was recorded on. The fetcher asks for prices up to today,
// and the Massive plugin splits an ask longer than 200 days into a request per
// chunk, so a fixed date drifts into a second request the cassette never
// recorded and the fetch fails on it. Both offsets stay well inside one chunk,
// which keeps the ask to the single interaction that was recorded. The
// cassette matcher normalizes dates in a URL, so the recorded response replays
// whatever window is asked for.
const INITIAL_DAYS_AGO = 150;
const ADDITIONAL_DAYS_AGO = 75;

function daysAgo(n: number): Date {
  const d = new Date();
  d.setUTCHours(0, 0, 0, 0);
  d.setUTCDate(d.getUTCDate() - n);
  return d;
}

// One INTC buy, in a window of its own day. Both dates carry the same instant,
// as a source stating one date does.
function intcBuy(name: string, at: Date, quantity: string, unitPrice: string): ArchiveUpload {
  const from = at.toISOString();
  const before = new Date(at.getTime() + 86_400_000).toISOString();
  return {
    name,
    document: {
      envelope: {
        format_version: 1,
        exported_at: new Date().toISOString(),
        source_instance: "e2e-fixture",
        kind: "USER",
      },
      txs: {
        windows: [
          {
            broker: "FIDELITY",
            period_from: from,
            period_before: before,
            source: "Fidelity:web:archive",
            postings: [
              {
                order_date: from,
                trade_date: from,
                instrument_description: "INTC - Intel Corp.",
                broker_tx_type: ["TRADE_ASSET"],
                asset_class_hint: "STOCK",
                quantity,
                trading_currency: "USD",
                unit_price: unitPrice,
              },
            ],
          },
        ],
      },
    },
  };
}

test.beforeAll(async ({ browser }) => {
  await loadCassette("tx-update-price-fetch");
  await waitForWorkersIdle(browser);
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("price fetch cycle after a transaction update", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("adding a transaction within an already-covered holding period runs a cycle that finds nothing to fetch", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    // Upload initial transaction: buy 10 INTC. This triggers identification and
    // a price fetch for the held period, which runs to today.
    await uploadArchiveAndWait(
      page,
      browser,
      intcBuy("tx-update-initial.json", daysAgo(INITIAL_DAYS_AGO), "10", "22.5"),
      { expectedPostingCount: 1 },
    );

    // Verify the holding appears.
    await page.goto("/holdings");
    const table = page.locator("[data-testid='holdings-table']");
    await expect(table).toBeVisible({ timeout: 10_000 });
    await expect(table).toContainText("INTC");

    // Record where the price fetcher has got to once the initial fetch is done.
    const cyclesAfterFirst = await getWorkerCycles(browser, "price_fetcher");

    // Upload additional transaction: buy 5 more INTC, later but still inside
    // the held period. The period is unchanged -- still the initial buy to
    // today -- just with a larger position from the second buy onwards.
    await uploadArchiveAndWait(
      page,
      browser,
      intcBuy("tx-update-additional.json", daysAgo(ADDITIONAL_DAYS_AGO), "5", "20.8"),
      { expectedPostingCount: 1 },
    );

    // Wait for the price fetcher cycle triggered by the new transaction.
    // The cycle finds no gaps (prices already cover the held period) so the
    // worker never transitions to RUNNING; its completed-cycle count is what
    // confirms the cycle actually executed.
    //
    // That a cycle finding no gaps also puts nothing to a price plugin is not
    // asserted here: nothing the SPA exposes counts provider calls. The
    // price_plugin_call rows in the telemetry schema are where that is read,
    // and a Grafana panel is what reads them.
    await waitForWorkerCycle(browser, "price_fetcher", cyclesAfterFirst);
  });

  // In record mode, ensure all workers finish so the VCR cassette captures
  // every HTTP interaction before the server shuts down.
  if (isRecordingSuite("tx-update-price-fetch")) {
    test("wait for all workers to finish (record mode)", async ({
      browser,
    }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});
