// Command google-finance mediates price data import from Google Finance via
// Google Sheets. It authenticates to both Google (for Sheets API access) and
// PortfolioDB (for price gap queries and price import), creates a spreadsheet
// with GOOGLEFINANCE formulas, and imports the evaluated results.
//
// Usage:
//
//	google-finance [flags]           # authenticate, query gaps, create/update sheet
//	google-finance --import [flags]  # read evaluated sheet data and import prices
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/api/sheets/v4"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fatalf("cannot determine home directory: %v", err)
	}
	defaultConfigDir := filepath.Join(home, ".portfoliodb", "google-finance")

	var (
		serverFlag    string
		configDirFlag string
		importFlag    bool
		newSheetFlag  bool
	)
	flag.StringVar(&serverFlag, "server", "localhost:50051", "PortfolioDB gRPC server address")
	flag.StringVar(&serverFlag, "s", "localhost:50051", "PortfolioDB gRPC server address (shorthand)")
	flag.StringVar(&configDirFlag, "config-dir", defaultConfigDir, "directory for credentials, tokens, and state")
	flag.StringVar(&configDirFlag, "c", defaultConfigDir, "directory for credentials, tokens, and state (shorthand)")
	flag.BoolVar(&importFlag, "import", false, "import prices from the Output tab of an existing sheet")
	flag.BoolVar(&importFlag, "i", false, "import prices from the Output tab of an existing sheet (shorthand)")
	flag.BoolVar(&newSheetFlag, "new-sheet", false, "create a new spreadsheet instead of updating the existing one")
	flag.BoolVar(&newSheetFlag, "n", false, "create a new spreadsheet instead of updating the existing one (shorthand)")
	flag.BoolVar(&verbose, "verbose", false, "enable verbose/debug output")
	flag.BoolVar(&verbose, "v", false, "enable verbose/debug output (shorthand)")
	flag.Parse()

	ctx := context.Background()

	// 1. Connect to PortfolioDB.
	conn, err := dialGRPC(serverFlag)
	if err != nil {
		fatalf("%v", err)
	}
	defer conn.Close()

	// 2. PortfolioDB auth (uses cached session from ~/.portfoliodb/session;
	//    only fetches a Google ID token if the session has expired).
	sessionID, err := portfolioDBAuth(ctx, conn, configDirFlag)
	if err != nil {
		fatalf("PortfolioDB authentication failed: %v", err)
	}
	rpcCtx := authContext(ctx, sessionID)
	apiClient := apiv1.NewApiServiceClient(conn)

	// 3. Google Sheets client (for creating/reading spreadsheets).
	tokenSource, err := googleTokenSource(ctx, configDirFlag)
	if err != nil {
		fatalf("Google authentication failed: %v", err)
	}
	sheetsSrv, err := sheetsClient(ctx, tokenSource)
	if err != nil {
		fatalf("Sheets client: %v", err)
	}

	if importFlag {
		runImport(ctx, rpcCtx, sheetsSrv, apiClient, configDirFlag)
		return
	}

	runCreateSheet(ctx, rpcCtx, sheetsSrv, apiClient, configDirFlag, newSheetFlag)
}

func runCreateSheet(ctx, rpcCtx context.Context, sheetsSrv *sheets.Service, apiClient apiv1.ApiServiceClient, configDir string, forceNew bool) {
	resp, err := apiClient.ListPriceGaps(rpcCtx, &apiv1.ListPriceGapsRequest{
		AssetClasses: []apiv1.AssetClass{
			apiv1.AssetClass_ASSET_CLASS_STOCK,
			apiv1.AssetClass_ASSET_CLASS_ETF,
			apiv1.AssetClass_ASSET_CLASS_FX,
		},
	})
	if err != nil {
		fatalf("ListPriceGaps: %v", err)
	}

	totalGaps := len(resp.GetPriceGaps()) + len(resp.GetFxGaps())
	if totalGaps == 0 {
		fmt.Fprintf(os.Stderr, "No price gaps found. Nothing to do.\n")
		return
	}
	fmt.Fprintf(os.Stderr, "Found %d instrument(s) with price gaps (%d price, %d FX).\n",
		totalGaps, len(resp.GetPriceGaps()), len(resp.GetFxGaps()))

	res := generateFormulas(resp.GetPriceGaps(), resp.GetFxGaps())
	for _, skip := range res.Skipped {
		fmt.Fprintf(os.Stderr, "  Skipped: %s\n", skip)
	}
	if len(res.Columns) == 0 {
		fmt.Fprintf(os.Stderr, "No mappable instruments. Nothing to do.\n")
		return
	}
	fmt.Fprintf(os.Stderr, "Generated formulas for %d instrument(s).\n", len(res.Columns))

	grid := columnsToGrid(res.Columns)
	url, err := createOrUpdateSheet(ctx, sheetsSrv, configDir, grid, forceNew)
	if err != nil {
		fatalf("Sheet operation failed: %v", err)
	}

	fmt.Fprintf(os.Stderr, "\nSpreadsheet ready:\n  %s\n\n", url)
	fmt.Fprintf(os.Stderr, "Next steps:\n")
	fmt.Fprintf(os.Stderr, "  1. Open the link above in your browser\n")
	fmt.Fprintf(os.Stderr, "  2. Wait for GOOGLEFINANCE formulas to evaluate (may take a minute)\n")
	fmt.Fprintf(os.Stderr, "  3. Select all data on the Input tab (Ctrl+A)\n")
	fmt.Fprintf(os.Stderr, "  4. Copy (Ctrl+C)\n")
	fmt.Fprintf(os.Stderr, "  5. Switch to the Output tab\n")
	fmt.Fprintf(os.Stderr, "  6. Paste as values only (Ctrl+Shift+V)\n")
	fmt.Fprintf(os.Stderr, "  7. Re-run with --import flag: google-finance -i\n")
	// Also print the URL to stdout for scripting.
	fmt.Println(url)
}

func runImport(ctx, rpcCtx context.Context, sheetsSrv *sheets.Service, apiClient apiv1.ApiServiceClient, configDir string) {
	st, err := loadState(configDir)
	if err != nil || st.SpreadsheetID == "" {
		fatalf("no spreadsheet ID found; run without --import first to create a sheet")
	}

	fmt.Fprintf(os.Stderr, "Reading Output tab from spreadsheet %s...\n", st.SpreadsheetID)
	prices, warnings, err := readOutputTab(ctx, sheetsSrv, st.SpreadsheetID)
	if err != nil {
		fatalf("%v", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "  Warning: %s\n", w)
	}
	if len(prices) == 0 {
		fatalf("no valid prices found in Output tab")
	}

	// Count unique instruments.
	instruments := make(map[string]bool)
	for _, p := range prices {
		instruments[p.GetIdentifierType()+"|"+p.GetIdentifierValue()] = true
	}
	fmt.Fprintf(os.Stderr, "Parsed %d prices for %d instrument(s).\n", len(prices), len(instruments))

	fmt.Fprintf(os.Stderr, "Importing prices...\n")
	if err := importPrices(rpcCtx, apiClient, prices); err != nil {
		fatalf("%v", err)
	}
	fmt.Fprintf(os.Stderr, "Done. %d prices submitted for import.\n", len(prices))
}

// verbose is set by --verbose / -v.
var verbose bool

func debugf(format string, args ...interface{}) {
	if verbose {
		fmt.Fprintf(os.Stderr, "debug: "+format+"\n", args...)
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
