package identifier

import (
	"context"
	"database/sql"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// Plugin implements identifier.Plugin by looking up (source, instrument_description) in
// local_instrument_identifiers and resolving to canonical data in local_instruments.
// Broker is passed but not used for lookup.
type Plugin struct {
	db *sql.DB
}

// New returns a new local reference data plugin that uses the given DB to query
// local_instruments and local_instrument_identifiers.
func New(db *sql.DB) *Plugin {
	return &Plugin{db: db}
}

// Identify implements identifier.Plugin.
func (p *Plugin) Identify(ctx context.Context, broker, source, instrumentDescription string) (*identifier.Instrument, []identifier.Identifier, error) {
	var instID string
	var assetClass, exchange, currency, name sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT i.id, i.asset_class, i.exchange, i.currency, i.name
		FROM local_instruments i
		JOIN local_instrument_identifiers ii ON ii.instrument_id = i.id
		WHERE ii.identifier_type = $1 AND ii.value = $2
	`, source, instrumentDescription).Scan(&instID, &assetClass, &exchange, &currency, &name)
	if err == sql.ErrNoRows {
		return nil, nil, identifier.ErrNotIdentified
	}
	if err != nil {
		return nil, nil, err
	}
	inst := &identifier.Instrument{ID: instID}
	if assetClass.Valid {
		inst.AssetClass = assetClass.String
	}
	if exchange.Valid {
		inst.Exchange = exchange.String
	}
	if currency.Valid {
		inst.Currency = currency.String
	}
	if name.Valid {
		inst.Name = name.String
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT identifier_type, value FROM local_instrument_identifiers WHERE instrument_id = $1
	`, instID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var identifiers []identifier.Identifier
	for rows.Next() {
		var idType, value string
		if err := rows.Scan(&idType, &value); err != nil {
			return nil, nil, err
		}
		identifiers = append(identifiers, identifier.Identifier{Type: idType, Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return inst, identifiers, nil
}

// Ensure Plugin implements identifier.Plugin.
var _ identifier.Plugin = (*Plugin)(nil)
