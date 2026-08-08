package archiveimport

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// InflationPart writes an archive's inflation index rows and reports through
// rep. It returns how many rows were written.
//
// asOf is the envelope's knowledge time and is stamped on every row as
// last_fetched_at, because an imported value is only as fresh as the file it
// came from. Stamping it now instead would tell the fetcher a stale series had
// just been confirmed.
//
// A row the file describes badly is a validation error rather than a hard
// failure, and a currency whose rows are all bad still leaves the rest of the
// part to land.
func InflationPart(ctx context.Context, database db.DB, part *archivev1.InflationPart, asOf *time.Time, rep *PartReporter) (int32, error) {
	groups := part.GetGroups()
	rep.Total(ctx, len(groups))
	if len(groups) == 0 {
		return 0, nil
	}

	var written int32
	for i, g := range groups {
		rows, ok := inflationRows(g, i, rep)
		rep.Advance(ctx, 1)
		if !ok || len(rows) == 0 {
			continue
		}
		if err := database.UpsertInflationIndices(ctx, rows, asOf); err != nil {
			return written, fmt.Errorf("upsert inflation indices for %s: %w", g.GetCurrency(), err)
		}
		written += int32(len(rows))
	}
	return written, nil
}

// inflationRows converts one group's rows, reporting each problem against the
// group's index. It returns false when the group itself is unusable, which is
// a different thing from a group whose rows were individually rejected.
func inflationRows(g *archivev1.InflationGroup, idx int, rep *PartReporter) ([]db.InflationIndex, bool) {
	if g.GetCurrency() == "" {
		rep.Errf(idx, "currency", "inflation group has no currency")
		return nil, false
	}
	out := make([]db.InflationIndex, 0, len(g.GetRows()))
	for _, r := range g.GetRows() {
		month, err := time.Parse("2006-01-02", r.GetMonth())
		if err != nil {
			rep.Errf(idx, "month", fmt.Sprintf("%s: %v", g.GetCurrency(), err))
			continue
		}
		value, err := decimal.NewFromString(r.GetIndexValue())
		if err != nil {
			rep.Errf(idx, "index_value", fmt.Sprintf("%s %s: %v", g.GetCurrency(), r.GetMonth(), err))
			continue
		}
		out = append(out, db.InflationIndex{
			Currency:     g.GetCurrency(),
			Month:        month,
			IndexValue:   value,
			BaseYear:     int(r.GetBaseYear()),
			DataProvider: db.InflationProviderImport,
		})
	}
	return out, true
}
