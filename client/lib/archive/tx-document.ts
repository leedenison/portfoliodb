/**
 * Reading an uploaded archive document down to the one transaction window it
 * states.
 *
 * The archive is the single format PortfolioDB defines for transactions in
 * either direction, so a hand-written or script-written upload is an archive
 * document rather than a format of its own. See docs/spec/archive-format.md.
 *
 * A document holding more than one window, or holding parts other than
 * transactions, belongs on the archive page: this path replaces one broker's
 * period and has nowhere to put the rest.
 */

import type { TxWindow } from "@/gen/archive/v1/txs_pb";
import type { ParseError } from "@/lib/csv/parse-result";
import { archiveErrorMessage, unmarshalUser } from "@/lib/archive/codec";

export interface TxDocumentResult {
  window?: TxWindow;
  errors: ParseError[];
}

function fail(message: string): TxDocumentResult {
  return { errors: [{ rowIndex: 0, field: "file", message }] };
}

export function readTxDocument(json: string): TxDocumentResult {
  let doc;
  try {
    doc = unmarshalUser(json);
  } catch (e) {
    return fail(archiveErrorMessage(e));
  }
  if (!doc.txs) {
    return fail("This archive carries no transactions");
  }
  const windows = doc.txs.windows;
  if (windows.length === 0) {
    return fail("This archive's transaction part is empty");
  }
  if (windows.length > 1) {
    return fail(
      "This archive covers more than one broker or period. Import it from the archive page instead",
    );
  }
  // Preferences and declarations have nowhere to go on this path, and dropping
  // them silently would import less than the file states.
  if (doc.preferences || doc.declarations) {
    return fail(
      "This archive carries more than transactions. Import it from the archive page instead",
    );
  }
  return { window: windows[0], errors: [] };
}
