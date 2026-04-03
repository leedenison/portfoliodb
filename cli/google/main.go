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
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fatalf("cannot determine home directory: %v", err)
	}
	defaultConfigDir := filepath.Join(home, ".portfoliodb", "google-finance")

	server := flag.String("server", "localhost:50051", "PortfolioDB gRPC server address")
	configDir := flag.String("config-dir", defaultConfigDir, "directory for credentials, tokens, and state")
	doImport := flag.Bool("import", false, "import prices from the Output tab of an existing sheet")
	newSheet := flag.Bool("new-sheet", false, "create a new spreadsheet instead of updating the existing one")
	flag.Parse()

	_ = newSheet // used in PR 4 (sheets integration)

	ctx := context.Background()

	// 1. Google OAuth.
	tokenSource, idToken, err := googleAuth(ctx, *configDir)
	if err != nil {
		fatalf("Google authentication failed: %v", err)
	}
	_ = tokenSource // used in PR 4 (sheets integration)

	// 2. Connect to PortfolioDB.
	conn, err := dialGRPC(*server)
	if err != nil {
		fatalf("%v", err)
	}
	defer conn.Close()

	// 3. PortfolioDB auth.
	sessionID, err := portfolioDBAuth(ctx, conn, *configDir, idToken)
	if err != nil {
		fatalf("PortfolioDB authentication failed: %v", err)
	}
	rpcCtx := authContext(ctx, sessionID)

	if *doImport {
		// Phase 2: read output tab and import prices.
		// TODO(PR4): implement import flow
		fmt.Fprintf(os.Stderr, "Import mode not yet implemented.\n")
		os.Exit(1)
	}

	// Phase 1: query price gaps and create/update sheet.
	apiClient := apiv1.NewApiServiceClient(conn)
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

	// TODO(PR3): generate GOOGLEFINANCE formulas
	// TODO(PR4): create/update Google Sheet and print link
	fmt.Fprintf(os.Stderr, "Sheet creation not yet implemented.\n")
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
