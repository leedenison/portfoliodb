package main

import (
	"fmt"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

const maxChunkDays = 365

// sheetColumn is one column-pair (date + close) for the Input tab.
// Each column contains exactly one GOOGLEFINANCE formula because the formula
// expands vertically to fill multiple rows when evaluated.
// Row 0 is the identifier header; row 1 is the formula.
type sheetColumn struct {
	Header  string // identifier key: "type|domain|value|asset_class"
	Formula string // single GOOGLEFINANCE formula
}

// formulaResult holds the generated sheet data and any skipped instruments.
type formulaResult struct {
	Columns []sheetColumn
	Skipped []string // human-readable skip reasons
}

// generateFormulas converts price gaps into sheet columns with GOOGLEFINANCE formulas.
// Each year-chunk of each gap gets its own column pair because GOOGLEFINANCE
// expands vertically when evaluated.
func generateFormulas(priceGaps, fxGaps []*apiv1.PriceGap) formulaResult {
	var res formulaResult
	for _, pg := range append(priceGaps, fxGaps...) {
		cols, err := gapToColumns(pg)
		if err != nil {
			res.Skipped = append(res.Skipped, err.Error())
			continue
		}
		res.Columns = append(res.Columns, cols...)
	}
	return res
}

func gapToColumns(pg *apiv1.PriceGap) ([]sheetColumn, error) {
	ident := pg.GetIdentifier()
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
		return nil, fmt.Errorf("%s: %w", name, err)
	}

	header := fmt.Sprintf("%s|%s|%s|%s",
		ident.GetType().String(),
		ident.GetDomain(),
		ident.GetValue(),
		db.AssetClassToStr(pg.GetAssetClass()),
	)

	var cols []sheetColumn
	for _, gap := range pg.GetGaps() {
		from, err := time.Parse("2006-01-02", gap.GetFrom())
		if err != nil {
			continue
		}
		to, err := time.Parse("2006-01-02", gap.GetTo())
		if err != nil {
			continue
		}
		for _, c := range chunkRange(from, to) {
			cols = append(cols, sheetColumn{
				Header:  header,
				Formula: googleFinanceFormula(ticker, c.from, c.to),
			})
		}
	}
	return cols, nil
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
// (date + close once the formula expands). Row 0 is the identifier header;
// row 1 is the single GOOGLEFINANCE formula.
func columnsToGrid(cols []sheetColumn) [][]string {
	if len(cols) == 0 {
		return nil
	}

	// 2 rows: header + formula. Each formula expands vertically when evaluated.
	gridCols := len(cols) * 2
	grid := make([][]string, 2)
	for i := range grid {
		grid[i] = make([]string, gridCols)
	}

	for i, col := range cols {
		baseCol := i * 2
		grid[0][baseCol] = col.Header
		grid[1][baseCol] = col.Formula
	}

	return grid
}
