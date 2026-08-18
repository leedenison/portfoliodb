package postgres

import "fmt"

// identifierPriorityOrder ranks identifier types for export-shaped queries that
// surface a single identifier per instrument. Adding a new identifier type or
// changing the order requires editing only this expression.
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
func bestIdentifierJoinOn(joinType, idExpr, alias string) string {
	return fmt.Sprintf(`
	%s LATERAL (
		SELECT ii.identifier_type, ii.value, ii.domain
		FROM instrument_identifiers ii
		WHERE ii.instrument_id = %s AND ii.valid_before IS NULL
		ORDER BY %s
		LIMIT 1
	) %s ON true
`, joinType, idExpr, identifierPriorityOrder, alias)
}
