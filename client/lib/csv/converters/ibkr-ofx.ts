/**
 * Register IBKR with OFX/QFX tx converter.
 *
 * What it adds to the generic parser is an OCC symbol for the options, which the
 * SECLIST states in its TICKER field. The symbol is constructed from the terms
 * the same record states and emitted only where it matches the one printed, so a
 * TICKER that is not an OCC produces no identifier at all. Where the TICKER is an
 * OCC symbol and the terms name a different one, the file contradicts itself and
 * is refused. See
 * docs/issues/0145-the-ibkr-ofx-converter-states-option-identity-correctly.md
 * and docs/adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
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

    const reading = occFromSec(sec.info);
    if (reading.kind === "none") continue;
    if (reading.kind === "contradiction") {
      // The record states a symbol and states the terms that name a different
      // one, at the one vintage the file carries. Nothing in it says which half
      // is wrong, so the file is refused rather than one half believed. This
      // used to drop the hint and keep the broker description, which left the
      // contract quietly identified by text while the file that named it stayed
      // in circulation. See docs/adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
      result.errors.push({
        rowIndex: result.postings.indexOf(posting),
        field: "identifier_hints",
        message:
          `the option is printed as ${reading.stated} and its terms name ` +
          `${reading.built}. One file states one identity, so nothing says which is right.`,
      });
      continue;
    }

    posting.identifierHints.push(
      create(InstrumentRefSchema, { type: IdentifierType.OCC, value: reading.occ }),
    );
  }

  return result;
}

/**
 * What a security record says about its option symbol.
 *
 * Three answers rather than two, because a record that offers nothing and a
 * record that offers two irreconcilable things are not the same finding. The
 * first is ordinary -- most records are not options, and IBKR prints its own
 * rendering for contracts OCC does not list -- and the second is a faulty file.
 */
type OccReading =
  | { kind: "none" }
  | { kind: "stated"; occ: string }
  | { kind: "contradiction"; stated: string; built: string };

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
 * all fails at the first step, and that is not a disagreement: the record simply
 * names no OCC symbol, and the contract keeps its broker description, which is
 * the identity the source actually gave it.
 *
 * Reaching the last step and failing there is a different thing. The TICKER is a
 * well-formed OCC symbol and the record's own terms name another one, both
 * stated at the one vintage the file carries, so the file contradicts itself and
 * is refused.
 *
 * Terms that build no symbol at all are not counted as a disagreement. The bound
 * they fall outside is this module's rather than the file's -- a strike finer
 * than a thousandth, or above the eight digits the format allows -- and refusing
 * an upload over our own limit would be reading a fault into the file that may
 * not be there.
 */
function occFromSec(info: SecInfo | undefined): OccReading {
  if (info?.opt === undefined) return { kind: "none" };

  const stated = occParts(info.ticker);
  if (stated === undefined) return { kind: "none" };

  // The root is the one part the terms cannot supply -- it is OCC's own name for
  // the underlying, not the broker's -- so it comes from the printed symbol and
  // everything the record does state is rebuilt around it.
  const built = buildOcc(stated.root, info.opt.expiry, info.opt.putCall, info.opt.strike);
  if (built === undefined) return { kind: "none" };
  if (built !== stated.compact) {
    return { kind: "contradiction", stated: stated.compact, built };
  }

  // Padded: the 21-character form is what the file states and what OCC writes.
  // The server normalises a hint to the compact form its own lookups use.
  const occ = occPadded(built);
  return occ === undefined ? { kind: "none" } : { kind: "stated", occ };
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
