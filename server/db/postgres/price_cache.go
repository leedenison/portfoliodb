package postgres

import (
	"context"
	"fmt"

	"github.com/leedenison/portfoliodb/server/db"
)

// HeldRanges implements db.PriceCacheDB.
func (p *Postgres) HeldRanges(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	return nil, fmt.Errorf("not implemented")
}

// PriceCoverage implements db.PriceCacheDB.
func (p *Postgres) PriceCoverage(ctx context.Context, instrumentIDs []string) ([]db.InstrumentDateRanges, error) {
	return nil, fmt.Errorf("not implemented")
}

// PriceGaps implements db.PriceCacheDB.
func (p *Postgres) PriceGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	return nil, fmt.Errorf("not implemented")
}
