/**
 * Register IBKR with OFX/QFX tx converter.
 *
 * What it adds to the generic parser is an OCC symbol for the options, which the
 * SECLIST states in its TICKER field. The symbol is constructed from the terms
 * the same record states and emitted only where it matches the one printed, so a
 * TICKER that is not an OCC produces no identifier at all. See
 * docs/issues/0145-the-ibkr-ofx-converter-states-option-identity-correctly.md.
 */

import { create } from "@bufbuild/protobuf";
import { InstrumentRefSchema } from "@/gen/archive/v1/common_pb";
import { Broker, IdentifierType } from "@/gen/type/v1/type_pb";
import type { StandardParseResult } from "@/lib/csv/parse-result";
import { buildOcc, occPadded, occParts } from "@/lib/occ";
import type { SecInfo } from "@/lib/ofx/parser";
import { parseOfxStatement } from "@/lib/ofx/parser";
import { register } from "./registry";

export function convertIbkrOfx(text: string): StandardParseResult {
  const result = parseOfxStatement(text);

  // IBKR identifies a contract by a CONID, which is its own and which no
  // identifier plugin resolves, so an option arrives from the generic parser
  // with no hint at all. Where the SECLIST prints a genuine OCC symbol beside
  // it, that is the identity the file offers and it is stated here.
  for (const [posting, sec] of result.securities) {
    // A SECID the generic parser could already read -- an ISIN, a CUSIP, a
    // SEDOL -- has stated the identity, and this has nothing to add to it.
    if (posting.identifierHints.length > 0) continue;

    const occ = occFromSec(sec.info);
    if (occ === undefined) continue;

    posting.identifierHints.push(
      create(InstrumentRefSchema, { type: IdentifierType.OCC, value: occ }),
    );
  }

  return result;
}

/**
 * The OCC symbol a security record states, or undefined where it states none.
 *
 * The TICKER is checked rather than trusted, because IBKR prints its own
 * rendering there for contracts OCC does not list: `P RHM  20250919 560 M` is a
 * Eurex put, and no OCC symbol exists for it. Emitting one built from the terms
 * anyway would not name that contract -- an OCC root names a US-listed
 * underlying by construction, and the format carries no venue to say otherwise
 * -- so it would either resolve to nothing or bind the contract to an unrelated
 * US option on the same strike ladder.
 *
 * So the terms the record states are built into the symbol they name, and the
 * result has to match the one the file printed. A TICKER that is not an OCC at
 * all fails at the first step; a file disagreeing with itself fails at the last.
 * Either way the contract keeps its broker description, which is the identity
 * the source actually gave it.
 */
function occFromSec(info: SecInfo | undefined): string | undefined {
  if (info?.opt === undefined) return undefined;

  const stated = occParts(info.ticker);
  if (stated === undefined) return undefined;

  // The root is the one part the terms cannot supply -- it is OCC's own name for
  // the underlying, not the broker's -- so it comes from the printed symbol and
  // everything the record does state is rebuilt around it.
  const built = buildOcc(stated.root, info.opt.expiry, info.opt.putCall, info.opt.strike);
  if (built !== stated.compact) return undefined;

  // Padded: the 21-character form is what the file states and what OCC writes.
  // The server normalises a hint to the compact form its own lookups use.
  return occPadded(built);
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
