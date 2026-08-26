/**
 * What a file says about identity, checked against itself.
 *
 * Every identifier in one file is stated as of one vintage -- the export date the
 * file carries -- so no reading of the validity intervals reconciles two of them
 * that disagree. There is no "one was true before the other": they are offered
 * together, as of one moment. A file that does that is faulty, and the answer is
 * to refuse it rather than to pick one and store it, because nothing in the file
 * says which one is right.
 *
 * That is the first of the three cases in
 * docs/adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md. The other
 * two -- a file disagreeing with the database, and a transaction fault -- are the
 * server's, and are decided there.
 *
 * The server checks this again rather than trusting the converter. That is not
 * defence against a converter this repository owns so much as against the
 * extension and any other client: what makes an upload acceptable has to hold
 * wherever it arrives from.
 *
 * Kept free of React so the import extension can use it.
 */

import type { Posting } from "@/gen/archive/v1/txs_pb";
import { IdentifierType } from "@/gen/type/v1/type_pb";
import type { ParseError } from "@/lib/csv/parse-result";

/**
 * The subject an identifier names: its type and, where it has one, its domain.
 *
 * The subject rather than the type alone, because a ticker under two domains
 * names two listings. A security quoted in New York and in London states two
 * MIC_TICKER values legitimately, and comparing them on the type would call that
 * a contradiction.
 *
 * Domains are compared as the file spells them. A segment MIC and the operating
 * MIC it normalises to are two subjects here where the server makes them one,
 * which errs towards accepting -- this refuses a whole upload, so it may only
 * fire where the disagreement is plain in the file itself.
 */
function subject(type: IdentifierType, domain: string): string {
  return `${type} ${domain}`;
}

/** The vocabulary name of a type, for the message. */
function typeName(type: IdentifierType): string {
  return IdentifierType[type] ?? String(type);
}

/**
 * The identity claims a file cannot all hold, as parse errors.
 *
 * One description is one security -- it is the key the server resolves on -- so
 * two values for one subject under one description are two securities where the
 * file names one.
 *
 * Two descriptions naming one identifier are not a contradiction and are
 * deliberately not looked for: a broker may write a security several ways, in a
 * statement and a confirmation and a tax document, and they resolve to one
 * instrument, which is the point of storing the mapping.
 *
 * The first value stated wins the comparison, so a description stating three
 * values reports two errors and each names what it disagreed with. Reporting
 * against the first is arbitrary and says so in the message: nothing in the file
 * makes one of them the right one, which is the whole reason the upload is
 * refused rather than resolved.
 */
export function statedIdentityErrors(postings: Posting[]): ParseError[] {
  const errors: ParseError[] = [];
  const seen = new Map<string, { value: string; row: number }>();

  postings.forEach((posting, row) => {
    const desc = posting.instrumentDescription;
    if (desc === "") return;
    for (const hint of posting.identifierHints) {
      if (hint.value === "") continue;
      const key = `${desc} ${subject(hint.type, hint.domain)}`;
      const first = seen.get(key);
      if (first === undefined) {
        seen.set(key, { value: hint.value, row });
        continue;
      }
      if (first.value === hint.value) continue;
      errors.push({
        rowIndex: row,
        field: "identifier_hints",
        message:
          `${desc} is ${typeName(hint.type)} ${hint.value} here and ${first.value} ` +
          `on row ${first.row}. One file states one identity, so nothing says ` +
          `which is right.`,
      });
    }
  });

  return errors;
}
