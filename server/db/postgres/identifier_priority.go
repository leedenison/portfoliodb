package postgres

import "fmt"

// Two priority orders, one per grain, for the export-shaped queries that surface
// a single identifier for a thing. What is being named decides which order
// applies: an ISIN names a security and says nothing about which of its lines, a
// ticker names one line and says nothing about the others.
//
// The two lists hold the same types in the same relative order within each grain;
// only the two blocks swap. One order over both grains ranked MIC_TICKER above
// ISIN, so a security-grain export was named by a listing-grain identifier -- and
// which of a security's tickers it got was whichever the planner returned.
// See docs/adr/0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md.
//
// The alias is fixed at ii, because each lateral below builds one candidate set
// from both identifier tables and names it that.
//
// A type absent from a list falls to 99 and is a candidate only when nothing else
// is -- which is how an FX pair, named by FX_PAIR alone, still gets a name.

// securityIdentifierPriorityOrder names a security. The types that name one
// outright lead; the listing-grain types follow, because a security whose only
// names are its lines' tickers still has to be nameable, and a ticker resolves to
// the security above it. BROKER_DESCRIPTION is last everywhere: it is the one
// type that is not canonical.
const securityIdentifierPriorityOrder = `CASE ii.identifier_type
			WHEN 'OCC' THEN 1
			WHEN 'ISIN' THEN 2
			WHEN 'OPENFIGI_SHARE_CLASS' THEN 3
			WHEN 'CUSIP' THEN 4
			WHEN 'OPRA' THEN 5
			WHEN 'MIC_TICKER' THEN 6
			WHEN 'OPENFIGI_TICKER' THEN 7
			WHEN 'OPENFIGI_COMPOSITE' THEN 8
			WHEN 'SEDOL' THEN 9
			WHEN 'BROKER_DESCRIPTION' THEN 10
			ELSE 99
		END`

// listingIdentifierPriorityOrder names one line. A ticker under its venue names
// the line exactly, so the listing-grain types lead; the security-grain types
// follow, and a listing named by one of them is told apart from its siblings by
// the currency the caller carries alongside.
const listingIdentifierPriorityOrder = `CASE ii.identifier_type
			WHEN 'MIC_TICKER' THEN 1
			WHEN 'OPENFIGI_TICKER' THEN 2
			WHEN 'OPENFIGI_COMPOSITE' THEN 3
			WHEN 'SEDOL' THEN 4
			WHEN 'OCC' THEN 5
			WHEN 'ISIN' THEN 6
			WHEN 'OPENFIGI_SHARE_CLASS' THEN 7
			WHEN 'CUSIP' THEN 8
			WHEN 'OPRA' THEN 9
			WHEN 'BROKER_DESCRIPTION' THEN 10
			ELSE 99
		END`

// identifierInForce is the filter both joins share. Only names still in force are
// candidates: an option that has worn two OCC symbols holds both, and a file
// naming it by the one it has given up would name a contract that no longer
// answers to it.
const identifierInForce = `ii.valid_before IS NULL`

// bestSecurityIdentifierJoin is the security join against an outer query that
// already exposes an `instruments i` alias.
//
// Every export that names a security -- stock splits, corporate event coverage,
// unhandled events, corporate event fetch blocks, a posting's identifier hints --
// MUST use this or bestSecurityIdentifierJoinOn, so they all agree on which
// identifier that is.
var bestSecurityIdentifierJoin = bestSecurityIdentifierJoinOn("JOIN", "i.id", "best_id")

// bestSecurityIdentifierJoinOn builds the security join for an arbitrary
// instrument id expression and alias. Pass "LEFT JOIN" where idExpr may be NULL.
//
// Both grains are candidates, because a security whose only identifiers hang off
// its listings still has to be nameable. That is the flattening
// recompute_instrument_name performs to derive a display name, and the tie-break
// is the same one for the same reason: type priority sorts first and the two
// grains contribute disjoint types, so the currency only ever decides between a
// security's own listings. Nulls last within a type is "prefer a name that is on
// a line", so one nobody could place is a last resort rather than a first choice.
//
// Without that tie-break the answer among several tickers is whichever the
// planner returns, and two exports of one security could disagree.
func bestSecurityIdentifierJoinOn(joinType, idExpr, alias string) string {
	return fmt.Sprintf(`
	%[1]s LATERAL (
		SELECT ii.identifier_type, ii.value, ii.domain
		FROM (
			SELECT si.identifier_type, si.value, si.domain, si.valid_before, NULL::text AS currency
			FROM instrument_identifiers si
			WHERE si.instrument_id = %[2]s
			UNION ALL
			SELECT li.identifier_type, li.value, li.domain, li.valid_before, l.currency
			FROM instrument_listing_identifiers li
			LEFT JOIN instrument_listings l ON l.id = li.listing_id
			WHERE li.instrument_id = %[2]s
		) ii
		WHERE %[3]s
		ORDER BY %[4]s, ii.currency IS NULL, ii.currency, ii.domain, ii.value
		LIMIT 1
	) %[5]s ON true
`, joinType, idExpr, identifierInForce, securityIdentifierPriorityOrder, alias)
}

// bestListingIdentifierJoinOn is the listing join: the identifier a file names one
// currency line by. Pass "LEFT JOIN" where idExpr may be NULL.
//
// The candidates are that line's own identifiers and its security's, and never a
// sibling line's. A group named by the GBP line's ticker and stated to be in USD
// names two different things at once, which is the failure the split exists to
// stop.
//
// A listing named by a security-grain identifier -- an ISIN, where the line has no
// ticker of its own -- is told apart from its siblings by the currency the caller
// carries alongside, which is what makes the pair a name.
func bestListingIdentifierJoinOn(joinType, idExpr, alias string) string {
	return fmt.Sprintf(`
	%[1]s LATERAL (
		SELECT ii.identifier_type, ii.value, ii.domain
		FROM (
			SELECT li.identifier_type, li.value, li.domain, li.valid_before
			FROM instrument_listing_identifiers li
			WHERE li.listing_id = %[2]s
			UNION ALL
			SELECT si.identifier_type, si.value, si.domain, si.valid_before
			FROM instrument_identifiers si
			JOIN instrument_listings ls ON ls.instrument_id = si.instrument_id
			WHERE ls.id = %[2]s
		) ii
		WHERE %[3]s
		ORDER BY %[4]s, ii.domain, ii.value
		LIMIT 1
	) %[5]s ON true
`, joinType, idExpr, identifierInForce, listingIdentifierPriorityOrder, alias)
}
