// A broker file that states an identifier but not a venue.
//
// Such a file used to take Path A straight to the identifier plugins, because
// the candidate stage ran only for a posting that stated nothing at all. What
// this pins is that it now runs when the stated identity is incomplete: the
// resolution key records that it had identifier hints AND that proposals were
// found for it, a combination the old gate made impossible. See
// docs/adr/0058-candidate-plugins-complete-a-partial-identity.md.
//
// It does not yet assert a venue on the instrument. The candidate plugin's
// current prompt returns a bare ticker and no exchange, so what it proposes here
// cannot choose between the listings the ISIN produced. Proposing the exchange
// is docs/issues/0133-candidate-plugin-uses-structured-outputs.md; this suite is
// where that assertion belongs when it lands.

import { test, expect } from "@playwright/test";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB, rawQuery, INSTRUMENT_NAMES } from "../helpers/db";
import { waitForWorkersIdle } from "../helpers/workers";
import { loadCassette, unloadCassette } from "../helpers/cassette";
import { isRecordingSuite } from "../helpers/vcr";

const DESCRIPTION = "BERKSHIRE HATHAWAY INC-CL B";
const ISIN = "US0846707026";

// OFX timestamps are YYYYMMDDHHMMSS with a zone suffix. UTC keeps the rendered
// day equal to the instant's day whatever zone the runner is in.
function ofxStamp(daysAgo: number): string {
  const d = new Date(Date.now() - daysAgo * 86_400_000);
  const p = (n: number) => String(n).padStart(2, "0");
  return (
    `${d.getUTCFullYear()}${p(d.getUTCMonth() + 1)}${p(d.getUTCDate())}` +
    `${p(d.getUTCHours())}${p(d.getUTCMinutes())}${p(d.getUTCSeconds())}.000[0:UTC]`
  );
}

// An IBKR QFX carrying one BUYSTOCK whose SECID is an ISIN and whose SECLIST
// entry names no ticker and no exchange -- a key for the security and nothing
// that says where it trades, which is the shape the format arrives in.
//
// The document is modelled on a real export: the tag order and the nesting are
// copied, the account number and the FITID are invented, and the ISIN is public
// reference data. Do not restore the originals.
//
// The BUYSTOCK deliberately carries no CURRENCY block, which is what makes the
// identity it states partial: a line is a security and a currency, so an ISIN
// with a trading currency beside it is complete and does not reach this stage at
// all. Only quoteCurrency is left unstated by the omission -- figureCurrency
// falls back to the statement's CURDEF, so the cash leg is still USD and the
// group still balances. Adding a CURSYM here would make this suite assert the
// opposite of what it is named for.
//
// It is rendered rather than committed as a fixture because its dates decide the
// window the price fetcher asks providers for. Committed dates make that window
// grow by a day every day, so a cassette recorded once would eventually need a
// request it does not hold. Dates relative to the run keep the window the size
// it was when the cassette was recorded; the dates themselves are normalised out
// by the matcher (see server/testutil/vcr/vcr.go).
function statement(): string {
  return `OFXHEADER:100
DATA:OFXSGML
VERSION:102
SECURITY:NONE
ENCODING:USASCII
CHARSET:1252
COMPRESSION:NONE
OLDFILEUID:NONE
NEWFILEUID:NONE

<OFX>
<SIGNONMSGSRSV1>
<SONRS>
<STATUS>
<CODE>0
<SEVERITY>INFO
</STATUS>
<DTSERVER>${ofxStamp(0)}
<LANGUAGE>ENG
</SONRS>
</SIGNONMSGSRSV1>
<INVSTMTMSGSRSV1>
<INVSTMTTRNRS>
<TRNUID>0
<STATUS>
<CODE>0
<SEVERITY>INFO
</STATUS>
<INVSTMTRS>
<DTASOF>${ofxStamp(0)}
<CURDEF>USD
<INVACCTFROM>
<BROKERID>ibkr.com
<ACCTID>U8000123
</INVACCTFROM>
<INVTRANLIST>
<DTSTART>${ofxStamp(45)}
<DTEND>${ofxStamp(0)}
<BUYSTOCK>
<INVBUY>
<INVTRAN>
<FITID>E1000001
<DTTRADE>${ofxStamp(30)}
</INVTRAN>
<SECID>
<UNIQUEID>${ISIN}
<UNIQUEIDTYPE>ISIN
</SECID>
<UNITS>10
<UNITPRICE>420.00
<TOTAL>-4200.00
</INVBUY>
<BUYTYPE>BUY
</BUYSTOCK>
</INVTRANLIST>
</INVSTMTRS>
</INVSTMTTRNRS>
</INVSTMTMSGSRSV1>
<SECLISTMSGSRSV1>
<SECLIST>
<STOCKINFO>
<SECINFO>
<SECID>
<UNIQUEID>${ISIN}
<UNIQUEIDTYPE>ISIN
</SECID>
<SECNAME>${DESCRIPTION}
</SECINFO>
</STOCKINFO>
</SECLIST>
</SECLISTMSGSRSV1>
</OFX>
`;
}

test.beforeAll(async () => {
  await loadCassette("candidate-completes-identity");
  await resetAndSeedBase("candidate-completes-identity");
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
  await unloadCassette();
});

test.describe("a candidate plugin completes a partial identity", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("an ISIN with no currency reaches the candidate stage", async ({
    context,
    page,
    browser,
  }) => {
    test.setTimeout(TIMEOUT_SLOW);
    await injectSession(context, sessionId);

    await page.goto("/uploads");
    await expect(page.locator("[data-testid='page-uploads']")).toBeVisible();
    await page.locator("[data-testid='btn-upload-transactions']").click();
    await expect(page.locator("[data-testid='upload-modal']")).toBeVisible();

    // Step 1: the QFX converter is registered under IBKR, not the default broker.
    await page
      .locator("[data-testid='select-broker']")
      .selectOption({ label: "IBKR" });
    await page.getByRole("button", { name: "Next" }).click();

    // Step 2: pick the OFX/QFX format, then the file.
    await page.locator("#upload-format").selectOption({ label: "OFX / QFX" });
    await page.locator("#upload-file").setInputFiles({
      name: "statement.qfx",
      mimeType: "application/x-ofx",
      buffer: Buffer.from(statement()),
    });

    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toBeVisible();

    await page.locator("[data-testid='btn-upload-submit']").click();
    await expect(page.locator("[data-testid='upload-modal']")).not.toBeVisible({
      timeout: 30_000,
    });
    await waitForWorkersIdle(browser);

    // Scoped to the run this upload opened. Telemetry rows outlive resetData --
    // they are keyed by run, not by user -- so a query over every tx_import run
    // would also see the ones a previous suite or a previous container left.
    const runs = (await rawQuery(
      `SELECT id FROM telemetry.run
        WHERE kind = 'tx_import' ORDER BY started_at DESC LIMIT 1`
    )) as { id: string }[];
    expect(runs).toHaveLength(1);
    const runId = runs[0].id;

    // The file's one BUYSTOCK becomes a security leg and the cash leg that pays
    // for it, and both carry the security's description, so the description
    // names two keys. They are told apart by what each stated: the cash leg
    // states a CURRENCY, which the database already knows, and only the security
    // leg reached the candidate stage.
    const keys = (await rawQuery(
      `SELECT had_identifier_hints, candidate_outcome, outcome
         FROM telemetry.resolution_key
        WHERE run_id = $1 AND description = $2`,
      [runId, DESCRIPTION]
    )) as {
      had_identifier_hints: boolean;
      candidate_outcome: string;
      outcome: string;
    }[];

    // Both halves of the change on one row: the source did supply identifier
    // hints, and the candidate stage ran for it anyway and found something.
    // Under the old gate a key with hints could only be
    // not_attempted_hints_supplied.
    const completed = keys.filter((k) => k.candidate_outcome === "fields_proposed");
    expect(completed).toHaveLength(1);
    expect(completed[0].had_identifier_hints).toBe(true);
    expect(completed[0].outcome).toBe("identified");

    // And the run paid for it once, over the batch rather than per posting.
    const calls = (await rawQuery(
      `SELECT count(*)::int AS n FROM telemetry.candidate_plugin_call
        WHERE run_id = $1 AND plugin_id = 'openai'`,
      [runId]
    )) as { n: number }[];
    expect(calls[0].n).toBe(1);

    // Every field the plugin proposed is recorded against both the call that
    // produced it and the key it was proposed for. That join is the whole point
    // of the table: the call knows the cost and the key knows the instrument, and
    // only a row naming both can say whether what was paid for was right.
    const fields = (await rawQuery(
      `SELECT field, value, outcome, confidence, plugin_id, key_outcome
         FROM telemetry.v_candidate_field
        WHERE run_id = $1 AND description = $2
        ORDER BY field`,
      [runId, DESCRIPTION]
    )) as {
      field: string;
      value: string;
      outcome: string;
      confidence: number | null;
      plugin_id: string;
      key_outcome: string;
    }[];
    expect(fields.length).toBeGreaterThan(0);
    for (const f of fields) {
      expect(["ticker", "exchange", "currency", "key"]).toContain(f.field);
      expect(["confirmed", "contradicted", "untested", "unused"]).toContain(
        f.outcome
      );
      expect(f.plugin_id).toBe("openai");
      // The key's own outcome travels with the field, because a field confirmed
      // on a key that ended broker-description-only helped nobody.
      expect(f.key_outcome).toBe("identified");
    }

    // The instrument still resolves from what the source stated. A proposal
    // ranks and never resolves, so the ISIN is what found the security.
    const ids = (await rawQuery(
      `SELECT ii.identifier_type
         FROM ${INSTRUMENT_NAMES} ii
        WHERE ii.instrument_id = (
                SELECT instrument_id FROM instrument_identifiers
                 WHERE identifier_type = 'ISIN' AND value = $1)`,
      [ISIN]
    )) as { identifier_type: string }[];
    const types = ids.map((r) => r.identifier_type);
    expect(types).toContain("ISIN");
    expect(types).toContain("MIC_TICKER");
  });

  if (isRecordingSuite("candidate-completes-identity")) {
    test("wait for all workers to finish (record mode)", async ({ browser }) => {
      test.setTimeout(TIMEOUT_SLOW);
      await waitForWorkersIdle(browser, { timeoutMs: TIMEOUT_SLOW });
    });
  }
});
