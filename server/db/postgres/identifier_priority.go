package postgres

// bestIdentifierJoin is a LATERAL JOIN clause that selects the
// highest-priority canonical identifier per instrument for export-shaped
// queries (price export, stock-split export, cash-dividend export). It
// expects an outer query that already exposes an `instruments i` alias.
//
// All export queries that surface a single identifier per instrument MUST
// use this constant so the priority order stays consistent across exports.
// Adding a new identifier type or changing the order requires editing only
// this constant.
const bestIdentifierJoin = `
	JOIN LATERAL (
		SELECT ii.identifier_type, ii.value, ii.domain
		FROM instrument_identifiers ii
		WHERE ii.instrument_id = i.id
		ORDER BY CASE ii.identifier_type
			WHEN 'MIC_TICKER' THEN 1
			WHEN 'OPENFIGI_TICKER' THEN 2
			WHEN 'OCC' THEN 3
			WHEN 'ISIN' THEN 4
			WHEN 'OPENFIGI_GLOBAL' THEN 5
			WHEN 'OPENFIGI_SHARE_CLASS' THEN 6
			WHEN 'OPENFIGI_COMPOSITE' THEN 7
			WHEN 'CUSIP' THEN 8
			WHEN 'SEDOL' THEN 9
			WHEN 'OPRA' THEN 10
			WHEN 'BROKER_DESCRIPTION' THEN 11
			ELSE 99
		END
		LIMIT 1
	) best_id ON true
`
