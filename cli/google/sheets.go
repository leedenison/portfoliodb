package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

const (
	inputTab  = "Input"
	outputTab = "Output"
)

// stateCache persists the spreadsheet ID between runs.
type stateCache struct {
	SpreadsheetID string `json:"spreadsheet_id"`
}

func loadState(configDir string) (*stateCache, error) {
	data, err := os.ReadFile(filepath.Join(configDir, "state.json"))
	if err != nil {
		return nil, err
	}
	var s stateCache
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveState(configDir string, s *stateCache) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, "state.json"), data, 0o600)
}

// sheetsClient creates a Google Sheets API client from an OAuth2 token source.
func sheetsClient(ctx context.Context, ts oauth2.TokenSource) (*sheets.Service, error) {
	return sheets.NewService(ctx, option.WithTokenSource(ts))
}

// createOrUpdateSheet creates (or updates) a spreadsheet with Input/Output tabs
// and writes the formula grid to the Input tab. Returns the spreadsheet URL.
func createOrUpdateSheet(ctx context.Context, srv *sheets.Service, configDir string, grid [][]string, forceNew bool) (string, error) {
	var ssID string

	if !forceNew {
		if st, err := loadState(configDir); err == nil && st.SpreadsheetID != "" {
			ssID = st.SpreadsheetID
		}
	}

	if ssID == "" {
		id, err := createSpreadsheet(ctx, srv)
		if err != nil {
			return "", err
		}
		ssID = id
		if err := saveState(configDir, &stateCache{SpreadsheetID: ssID}); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to cache spreadsheet ID: %v\n", err)
		}
	}

	if err := writeInputTab(ctx, srv, ssID, grid); err != nil {
		return "", fmt.Errorf("write Input tab: %w", err)
	}
	if err := clearOutputTab(ctx, srv, ssID); err != nil {
		return "", fmt.Errorf("clear Output tab: %w", err)
	}

	return fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", ssID), nil
}

func createSpreadsheet(ctx context.Context, srv *sheets.Service) (string, error) {
	title := fmt.Sprintf("PortfolioDB Prices - %s", time.Now().Format("2006-01-02"))
	ss := &sheets.Spreadsheet{
		Properties: &sheets.SpreadsheetProperties{Title: title},
		Sheets: []*sheets.Sheet{
			{Properties: &sheets.SheetProperties{Title: inputTab}},
			{Properties: &sheets.SheetProperties{Title: outputTab}},
		},
	}
	created, err := srv.Spreadsheets.Create(ss).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create spreadsheet: %w", err)
	}
	return created.SpreadsheetId, nil
}

func writeInputTab(ctx context.Context, srv *sheets.Service, ssID string, grid [][]string) error {
	// Clear existing data first.
	_, err := srv.Spreadsheets.Values.Clear(ssID, inputTab, &sheets.ClearValuesRequest{}).Context(ctx).Do()
	if err != nil {
		return err
	}

	if len(grid) == 0 {
		return nil
	}

	rows := make([]*sheets.RowData, 0, len(grid))
	for _, row := range grid {
		cells := make([]*sheets.CellData, 0, len(row))
		for _, cell := range row {
			cd := &sheets.CellData{}
			if strings.HasPrefix(cell, "=") {
				cd.UserEnteredValue = &sheets.ExtendedValue{FormulaValue: &cell}
			} else if cell != "" {
				cd.UserEnteredValue = &sheets.ExtendedValue{StringValue: &cell}
			}
			cells = append(cells, cd)
		}
		rows = append(rows, &sheets.RowData{Values: cells})
	}

	// Find the Input sheet ID.
	ss, err := srv.Spreadsheets.Get(ssID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("get spreadsheet: %w", err)
	}
	var sheetID int64
	for _, s := range ss.Sheets {
		if s.Properties.Title == inputTab {
			sheetID = s.Properties.SheetId
			break
		}
	}

	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				UpdateCells: &sheets.UpdateCellsRequest{
					Start: &sheets.GridCoordinate{SheetId: sheetID, RowIndex: 0, ColumnIndex: 0},
					Rows:  rows,
					Fields: "userEnteredValue",
				},
			},
		},
	}
	_, err = srv.Spreadsheets.BatchUpdate(ssID, req).Context(ctx).Do()
	return err
}

func clearOutputTab(ctx context.Context, srv *sheets.Service, ssID string) error {
	_, err := srv.Spreadsheets.Values.Clear(ssID, outputTab, &sheets.ClearValuesRequest{}).Context(ctx).Do()
	return err
}

// readOutputTab reads all values from the Output tab and parses them into
// ImportPriceRow messages.
func readOutputTab(ctx context.Context, srv *sheets.Service, ssID string) ([]*apiv1.ImportPriceRow, []string, error) {
	resp, err := srv.Spreadsheets.Values.Get(ssID, outputTab).Context(ctx).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("read Output tab: %w", err)
	}
	return parseOutputValues(resp.Values)
}

// parseOutputValues extracts prices from a 2D cell grid (as returned by the
// Sheets API). Each column pair has a header in row 0 encoding
// "type|domain|value|asset_class", followed by rows of (date, close).
func parseOutputValues(values [][]any) ([]*apiv1.ImportPriceRow, []string, error) {
	if len(values) < 2 {
		return nil, nil, fmt.Errorf("Output tab is empty; open the spreadsheet, wait for formulas to evaluate, then copy-paste the Input tab values to the Output tab (Ctrl+Shift+V to paste as values)")
	}
	prices, warnings := parseOutputData(values)
	return prices, warnings, nil
}

// parseOutputData does the actual cell-by-cell parsing. Returned warnings are
// collected via the second return value of the caller; here we just skip bad rows.
func parseOutputData(values [][]any) ([]*apiv1.ImportPriceRow, []string) {
	var prices []*apiv1.ImportPriceRow
	var warnings []string

	headers := values[0]
	for col := 0; col < len(headers); col += 2 {
		hdr, ok := cellString(headers, col)
		if !ok || hdr == "" {
			continue
		}
		idType, domain, value, assetClass, err := parseHeader(hdr)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("column %d: %v", col, err))
			continue
		}

		for row := 1; row < len(values); row++ {
			rowData := values[row]
			dateStr, ok := cellString(rowData, col)
			if !ok || dateStr == "" {
				continue
			}
			closeStr, ok := cellString(rowData, col+1)
			if !ok || closeStr == "" {
				continue
			}
			// Skip GOOGLEFINANCE header row ("Date", "Close").
			if dateStr == "Date" {
				continue
			}
			// Skip error values (#N/A, #ERROR!, etc.) — may occur when the
			// entire range is non-trading days or the ticker is unrecognised.
			if strings.HasPrefix(dateStr, "#") || strings.HasPrefix(closeStr, "#") {
				continue
			}
			priceDate, err := parseDate(dateStr)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("row %d col %d: %v", row+1, col, err))
				continue
			}
			closeVal, err := strconv.ParseFloat(closeStr, 64)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("row %d col %d: invalid close price %q", row+1, col+1, closeStr))
				continue
			}

			prices = append(prices, &apiv1.ImportPriceRow{
				IdentifierType:   idType,
				IdentifierValue:  value,
				IdentifierDomain: domain,
				PriceDate:        priceDate,
				Close:            closeVal,
				AssetClass:       db.StrToAssetClass(assetClass),
			})
		}
	}

	return prices, warnings
}

// parseHeader splits a "TYPE|domain|value|ASSET_CLASS" header string.
func parseHeader(hdr string) (idType, domain, value, assetClass string, err error) {
	parts := strings.Split(hdr, "|")
	if len(parts) != 4 {
		return "", "", "", "", fmt.Errorf("invalid header %q: expected 4 pipe-separated fields", hdr)
	}
	return parts[0], parts[1], parts[2], parts[3], nil
}

// parseDate handles various date formats Google Sheets may produce,
// including dates with time components from GOOGLEFINANCE.
func parseDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	// Try common formats (with and without time components).
	for _, layout := range []string{
		"2006-01-02",
		"1/2/2006",
		"01/02/2006",
		"1/2/2006 15:04:05",
		"01/02/2006 15:04:05",
		"2006-01-02 15:04:05",
		"2-Jan-2006",
		"2-Jan-06",
		"Jan 2, 2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	// Try parsing as a serial date number (days since 1899-12-30).
	if n, err := strconv.ParseFloat(s, 64); err == nil && n > 1 {
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		t := base.AddDate(0, 0, int(n))
		return t.Format("2006-01-02"), nil
	}
	return "", fmt.Errorf("unparseable date %q", s)
}

// cellString extracts a string value from a row at the given column index.
func cellString(row []any, col int) (string, bool) {
	if col >= len(row) {
		return "", false
	}
	switch v := row[col].(type) {
	case string:
		return v, true
	case float64:
		// Numeric values from Sheets API.
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

// importPrices sends prices to PortfolioDB in batches.
func importPrices(ctx context.Context, client apiv1.ApiServiceClient, prices []*apiv1.ImportPriceRow) error {
	const batchSize = 1000
	for i := 0; i < len(prices); i += batchSize {
		end := i + batchSize
		if end > len(prices) {
			end = len(prices)
		}
		batch := prices[i:end]
		resp, err := client.ImportPrices(ctx, &apiv1.ImportPricesRequest{Prices: batch})
		if err != nil {
			return fmt.Errorf("ImportPrices batch %d-%d: %w", i, end-1, err)
		}
		fmt.Fprintf(os.Stderr, "  Submitted batch %d-%d (job %s)\n", i+1, end, resp.GetJobId())
	}
	return nil
}
