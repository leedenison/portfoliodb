/**
 * Converts the sample Fidelity exports and dumps what the grouping engine reads,
 * so the Go side can be run in shadow against them.
 *
 * Not a test: it asserts nothing and writes files. It is here because vitest is the
 * only thing that can run the converters, and it is opt-in so an ordinary run does
 * neither. See server/grouping/shadow_masters_test.go for what consumes the dump.
 *
 *   SHADOW_EXTRACT=1 make client-test
 */
import { describe, it } from "vitest";
import { readFileSync, writeFileSync, mkdirSync, existsSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { AccountType, Match, Scope } from "@/gen/type/v1/type_pb";
import { convertFidelityToStandard } from "./fidelity-csv";

const ROOT = resolve(process.cwd(), "..");
const MASTERS = resolve(ROOT, "local/masters");
const OUT = resolve(ROOT, "local/shadow");

/**
 * Whatever Fidelity exports are sitting in local/masters, found rather than named:
 * an export is named after whoever it belongs to, and no such name goes in the
 * repository.
 */
const sources = () =>
  readdirSync(MASTERS).filter((f) => /fidelity.*\.csv$/i.test(f));

/** The currency these exports are denominated in; the converter requires one. */
const CURRENCY = "GBP";

const scopeName = (s: number) => Scope[s] ?? "";
const matchName = (m: number) => Match[m] ?? "";

describe("shadow extract", () => {
  it.skipIf(!process.env.SHADOW_EXTRACT)("dumps the postings the engine reads", () => {
    if (!existsSync(MASTERS)) {
      console.log("no local/masters; nothing to extract");
      return;
    }
    mkdirSync(OUT, { recursive: true });
    for (const file of sources()) {
      const path = resolve(MASTERS, file);
      const result = convertFidelityToStandard(readFileSync(path, "utf8"), {
        currency: CURRENCY,
      });
      const rows = result.postings.map((p, i) => ({
        id: `p${i}`,
        account: p.account,
        timestamp: p.timestamp ? new Date(Number(p.timestamp.seconds) * 1000).toISOString() : "",
        quantity: p.quantity,
        unit_price: p.unitPrice,
        settlement_amount: p.settlementAmount,
        broker_tx_type: p.brokerTxType.map((t) => t),
        account_type: p.accountType,
        is_user: p.accountType === AccountType.UNSPECIFIED || p.accountType === AccountType.USER,
        group_ref: p.groupRef ?? "",
        correlations: p.correlations.map((c) => ({
          label: c.label,
          token: c.token,
          ordinal: c.ordinal === undefined ? null : Number(c.ordinal),
          ordinal_span: c.ordinalSpan === undefined ? null : Number(c.ordinalSpan),
          scope: scopeName(c.scope),
          match: c.match.map(matchName),
        })),
      }));
      const out = resolve(OUT, file.replace(/\.csv$/, ".json"));
      writeFileSync(out, JSON.stringify(rows, null, 1));
      console.log(
        `${file}: ${rows.length} postings, ${rows.filter((r) => r.is_user).length} transcribed, ` +
          `${new Set(rows.filter((r) => r.group_ref).map((r) => r.group_ref)).size} named groups, ` +
          `${result.errors.length} errors -> ${out}`
      );
    }
  });
});
