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
	var underlyingID sql.NullString
	var validFrom, validTo sql.NullTime
	err := p.db.QueryRowContext(ctx, `
		SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
		FROM local_instruments i
		JOIN local_instrument_identifiers ii ON ii.instrument_id = i.id
		WHERE ii.identifier_type = $1 AND ii.value = $2
	`, source, instrumentDescription).Scan(&instID, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo)
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
	if validFrom.Valid {
		t := validFrom.Time
		inst.ValidFrom = &t
	}
	if validTo.Valid {
		t := validTo.Time
		inst.ValidTo = &t
	}
	if underlyingID.Valid && underlyingID.String != "" {
		underlying, underlyingIds, err := p.loadLocalInstrument(ctx, underlyingID.String)
		if err != nil {
			return nil, nil, err
		}
		if underlying != nil {
			inst.Underlying = underlying
			inst.UnderlyingIdentifiers = underlyingIds
		}
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

// loadLocalInstrument loads one instrument from local_instruments by ID and returns it plus its identifiers.
func (p *Plugin) loadLocalInstrument(ctx context.Context, instrumentID string) (*identifier.Instrument, []identifier.Identifier, error) {
	var assetClass, exchange, currency, name sql.NullString
	var validFrom, validTo sql.NullTime
	err := p.db.QueryRowContext(ctx, `
		SELECT asset_class, exchange, currency, name, valid_from, valid_to
		FROM local_instruments WHERE id = $1
	`, instrumentID).Scan(&assetClass, &exchange, &currency, &name, &validFrom, &validTo)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	inst := &identifier.Instrument{ID: instrumentID}
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
	if validFrom.Valid {
		t := validFrom.Time
		inst.ValidFrom = &t
	}
	if validTo.Valid {
		t := validTo.Time
		inst.ValidTo = &t
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT identifier_type, value FROM local_instrument_identifiers WHERE instrument_id = $1
	`, instrumentID)
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
