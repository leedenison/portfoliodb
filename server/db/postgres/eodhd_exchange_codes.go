package postgres

import (
	"context"
	"fmt"
	"strings"
)

// LookupMICsByEODHDCode implements db.EODHDExchangeCodeDB.
func (p *Postgres) LookupMICsByEODHDCode(ctx context.Context, code string) ([]string, error) {
	var raw *string
	err := p.q.QueryRowContext(ctx,
		`SELECT operating_mic FROM eodhd_exchange_codes WHERE code = $1`, code).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("lookup eodhd exchange code %q: %w", code, err)
	}
	if raw == nil || *raw == "" {
		return nil, nil
	}
	parts := strings.Split(*raw, ",")
	mics := make([]string, 0, len(parts))
	for _, s := range parts {
		if t := strings.TrimSpace(s); t != "" {
			mics = append(mics, t)
		}
	}
	return mics, nil
}
