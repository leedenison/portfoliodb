/**
 * Register IBKR with OFX/QFX tx converter.
 * Maps CONID identifiers to OCC option symbols using the SECLIST TICKER field.
 */

import { create } from "@bufbuild/protobuf";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { Broker, IdentifierType } from "@/gen/type/v1/type_pb";
import type { StandardParseResult } from "@/lib/csv/parse-result";
import { parseOfxStatement } from "@/lib/ofx/parser";
import { register } from "./registry";

export function convertIbkrOfx(text: string): StandardParseResult {
  const result = parseOfxStatement(text);

  // IBKR uses CONID (internal contract ID) for options. The SECLIST
  // carries the OCC-format TICKER for these contracts. Add OCC hints
  // for any transaction whose SECID has no standard identifier hints
  // but can be resolved via SECLIST.
  for (const posting of result.postings) {
    const hasStandardHint = posting.identifierHints.length > 0;
    if (hasStandardHint) continue;

    // Look up the instrument description in the SECLIST by matching
    // on secName (which is what we set as instrumentDescription).
    for (const [, sec] of result.secList) {
      if (sec.secName === posting.instrumentDescription && sec.uniqueIdType === "CONID" && sec.ticker) {
        posting.identifierHints.push(
          create(InstrumentRefSchema, {
            type: IdentifierType.OCC,
            value: sec.ticker,
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
