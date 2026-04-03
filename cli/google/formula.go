package main

import (
	"fmt"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

const maxChunkDays = 365

// sheetColumn is one column-pair (date + close) for the Input tab.
// Row 0 is the identifier header; rows 1..N are GOOGLEFINANCE formulas.
type sheetColumn struct {
	Header   string   // identifier key: "type|domain|value|asset_class"
	Formulas []string // one GOOGLEFINANCE formula per year-chunk
}

// formulaResult holds the generated sheet data and any skipped instruments.
type formulaResult struct {
	Columns []sheetColumn
	Skipped []string // human-readable skip reasons
}

// generateFormulas converts price gaps into sheet columns with GOOGLEFINANCE formulas.
func generateFormulas(priceGaps, fxGaps []*apiv1.PriceGap) formulaResult {
	var res formulaResult
	for _, pg := range priceGaps {
		col, err := gapToColumn(pg)
		if err != nil {
			res.Skipped = append(res.Skipped, err.Error())
			continue
		}
		res.Columns = append(res.Columns, col)
	}
	for _, pg := range fxGaps {
		col, err := gapToColumn(pg)
		if err != nil {
			res.Skipped = append(res.Skipped, err.Error())
			continue
		}
		res.Columns = append(res.Columns, col)
	}
	return res
}

func gapToColumn(pg *apiv1.PriceGap) (sheetColumn, error) {
	ident := pg.GetIdentifier()
	// Resolve exchange MIC for gfTicker. For MIC_TICKER the domain is a MIC;
	// for OPENFIGI_TICKER it's a Bloomberg code (not a MIC), so use the gap's
	// exchange field instead.
	exchangeMIC := pg.GetExchange()
	if ident.GetType() == apiv1.IdentifierType_MIC_TICKER && ident.GetDomain() != "" {
		exchangeMIC = ident.GetDomain()
	}
	ticker, err := gfTicker(ident, exchangeMIC)
	if err != nil {
		name := pg.GetName()
		if name == "" {
			name = pg.GetInstrumentId()
		}
		return sheetColumn{}, fmt.Errorf("%s: %w", name, err)
	}

	header := fmt.Sprintf("%s|%s|%s|%s",
		ident.GetType().String(),
		ident.GetDomain(),
		ident.GetValue(),
		db.AssetClassToStr(pg.GetAssetClass()),
	)

	var formulas []string
	for _, gap := range pg.GetGaps() {
		from, err := time.Parse("2006-01-02", gap.GetFrom())
		if err != nil {
			continue
		}
		to, err := time.Parse("2006-01-02", gap.GetTo())
		if err != nil {
			continue
		}
		chunks := chunkRange(from, to)
		for _, c := range chunks {
			formulas = append(formulas, googleFinanceFormula(ticker, c.from, c.to))
		}
	}

	return sheetColumn{Header: header, Formulas: formulas}, nil
}

type dateChunk struct {
	from, to time.Time
}

// chunkRange splits a [from, to) range into segments of at most maxChunkDays.
func chunkRange(from, to time.Time) []dateChunk {
	var chunks []dateChunk
	for from.Before(to) {
		end := from.AddDate(0, 0, maxChunkDays)
		if end.After(to) {
			end = to
		}
		chunks = append(chunks, dateChunk{from: from, to: end})
		from = end
	}
	return chunks
}

// googleFinanceFormula returns a GOOGLEFINANCE formula string.
// to is exclusive; the formula end date is to-1 day.
func googleFinanceFormula(ticker string, from, to time.Time) string {
	// GOOGLEFINANCE end date is inclusive, so subtract 1 day from our exclusive 'to'.
	end := to.AddDate(0, 0, -1)
	return fmt.Sprintf(`=GOOGLEFINANCE("%s","close",DATE(%d,%d,%d),DATE(%d,%d,%d),"DAILY")`,
		ticker,
		from.Year(), from.Month(), from.Day(),
		end.Year(), end.Month(), end.Day(),
	)
}

// columnsToGrid converts sheet columns into a 2D string grid suitable for
// writing to a Google Sheet. Each column-pair occupies two grid columns
// (date + close once the formula expands). Row 0 is the header row.
func columnsToGrid(cols []sheetColumn) [][]string {
	if len(cols) == 0 {
		return nil
	}

	// Find max number of rows needed.
	maxRows := 1 // at least the header row
	for _, c := range cols {
		if n := 1 + len(c.Formulas); n > maxRows {
			maxRows = n
		}
	}

	// Build grid. Each instrument gets 2 columns (formula expands to date + close).
	gridCols := len(cols) * 2
	grid := make([][]string, maxRows)
	for i := range grid {
		grid[i] = make([]string, gridCols)
	}

	for i, col := range cols {
		baseCol := i * 2
		grid[0][baseCol] = col.Header
		for j, f := range col.Formulas {
			grid[1+j][baseCol] = f
		}
	}

	return grid
}
