/**
 * Register IBKR with OFX/QFX format support.
 * Adds IBKR-specific post-processing: maps CONID identifiers to OCC
 * option symbols using the SECLIST TICKER field.
 */

import { create } from "@bufbuild/protobuf";
import {
  Broker,
  IdentifierType,
  InstrumentIdentifierSchema,
} from "@/gen/api/v1/api_pb";
import type { StandardParseResult } from "@/lib/csv/standard";
import { parseOfxStatement } from "@/lib/ofx/parser";
import { register } from "./registry";

function convertIbkrOfx(text: string): StandardParseResult {
  const result = parseOfxStatement(text);

  // IBKR uses CONID (internal contract ID) for options. The SECLIST
  // carries the OCC-format TICKER for these contracts. Add OCC hints
  // for any transaction whose SECID has no standard identifier hints
  // but can be resolved via SECLIST.
  for (const tx of result.txs) {
    const hasStandardHint = tx.identifierHints.length > 0;
    if (hasStandardHint) continue;

    // Look up the instrument description in the SECLIST by matching
    // on secName (which is what we set as instrumentDescription).
    for (const [, sec] of result.secList) {
      if (sec.secName === tx.instrumentDescription && sec.uniqueIdType === "CONID" && sec.ticker) {
        tx.identifierHints.push(
          create(InstrumentIdentifierSchema, {
            type: IdentifierType.OCC,
            value: sec.ticker,
            canonical: false,
          }),
        );
        break;
      }
    }
  }

  return result;
}

register({
  broker: Broker.IBKR,
  label: "IBKR",
  sourcePrefix: "IBKR",
  formats: [
    {
      id: "ibkr-ofx",
      label: "OFX / QFX",
      accept: ".ofx,.qfx",
      convert: convertIbkrOfx,
    },
  ],
});
