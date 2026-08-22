package postgres

import "fmt"

// identifierPriorityOrder ranks identifier types for export-shaped queries that
// surface a single identifier per instrument. Adding a new identifier type or
// changing the order requires editing only this expression.
//
// The alias is fixed because the lateral below builds one candidate set from
// both identifier tables and names it ii, which is what keeps one order over
// types that are now stored in two places.
const identifierPriorityOrder = `CASE ii.identifier_type
			WHEN 'MIC_TICKER' THEN 1
			WHEN 'OPENFIGI_TICKER' THEN 2
			WHEN 'OCC' THEN 3
			WHEN 'ISIN' THEN 4
			WHEN 'OPENFIGI_SHARE_CLASS' THEN 5
			WHEN 'OPENFIGI_COMPOSITE' THEN 6
			WHEN 'CUSIP' THEN 7
			WHEN 'SEDOL' THEN 8
			WHEN 'OPRA' THEN 9
			WHEN 'BROKER_DESCRIPTION' THEN 10
			ELSE 99
		END`

// bestIdentifierJoin is a LATERAL JOIN clause that selects the
// highest-priority canonical identifier per instrument for export-shaped
// queries (price export, stock-split export, cash-dividend export). It
// expects an outer query that already exposes an `instruments i` alias.
//
// All export queries that surface a single identifier per instrument MUST
// use this clause, or bestIdentifierJoinOn below, so the priority order stays
// consistent across exports.
var bestIdentifierJoin = bestIdentifierJoinOn("JOIN", "i.id", "best_id")

// bestIdentifierJoinOn builds the same clause for an arbitrary instrument id
// expression and alias, so a query needing a second identifier -- the instrument
// export's underlying reference, which a file names by identifier rather than by
// UUID -- shares the one priority order rather than restating it. Pass
// "LEFT JOIN" where idExpr may be NULL.
//
// Only names still in force are candidates. An option that has worn two OCC
// symbols holds both, and a file naming it by the one it has given up would name
// a contract that no longer answers to it.
//
// Both grains are candidates, because the priority order leads with MIC_TICKER
// and a ticker names a listing. This is the same flattening
// recompute_instrument_name performs to derive a security's display name, and
// for the same reason: naming a security is a question about the security, and
// its listings are where several of its names now live.
//
// It is not the split adr/0069 asks for. That one gives a price group a listing
// join beside the security join, because an archive naming a listing needs its
// currency as well as an identifier; here one row per instrument is exactly what
// is wanted. Issue 0151 is where the ref gains a currency and the two joins part
// company.
func bestIdentifierJoinOn(joinType, idExpr, alias string) string {
	return fmt.Sprintf(`
	%[1]s LATERAL (
		SELECT ii.identifier_type, ii.value, ii.domain
		FROM (
			SELECT si.identifier_type, si.value, si.domain, si.valid_before
			FROM instrument_identifiers si
			WHERE si.instrument_id = %[2]s
			UNION ALL
			SELECT li.identifier_type, li.value, li.domain, li.valid_before
			FROM instrument_listing_identifiers li
			JOIN instrument_listings l ON l.id = li.listing_id
			WHERE l.instrument_id = %[2]s
		) ii
		WHERE ii.valid_before IS NULL
		ORDER BY %[3]s
		LIMIT 1
	) %[4]s ON true
`, joinType, idExpr, identifierPriorityOrder, alias)
}
