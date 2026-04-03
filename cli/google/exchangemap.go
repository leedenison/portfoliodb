package main

import (
	"encoding/csv"
	_ "embed"
	"fmt"
	"strings"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

//go:embed gf_exchanges.csv
var exchangeCSV string

// micToGF maps ISO 10383 MIC codes to Google Finance exchange prefixes.
var micToGF map[string]string

func init() {
	micToGF = parseExchangeCSV(exchangeCSV)
}

// parseExchangeCSV parses the embedded CSV into a MIC -> GF code map.
func parseExchangeCSV(data string) map[string]string {
	m := make(map[string]string)
	r := csv.NewReader(strings.NewReader(data))
	rows, err := r.ReadAll()
	if err != nil {
		panic(fmt.Sprintf("parse exchange CSV: %v", err))
	}
	for i, row := range rows {
		if i == 0 || len(row) < 4 {
			continue // skip header
		}
		gfCode, mic := row[0], row[3]
		if mic != "" {
			m[mic] = gfCode
		}
	}
	return m
}

// gfTicker converts a PortfolioDB instrument identifier to a Google Finance
// ticker string suitable for use in GOOGLEFINANCE formulas.
//
// For MIC_TICKER identifiers, it maps the MIC domain to the GF exchange prefix.
// For OPENFIGI_TICKER, it attempts MIC lookup from the instrument's exchange field.
// For FX_PAIR, it uses the "CURRENCY:" prefix.
func gfTicker(ident *apiv1.InstrumentIdentifier, exchangeMIC string) (string, error) {
	switch ident.GetType() {
	case apiv1.IdentifierType_FX_PAIR:
		return "CURRENCY:" + ident.GetValue(), nil

	case apiv1.IdentifierType_MIC_TICKER:
		mic := ident.GetDomain()
		if mic == "" {
			mic = exchangeMIC
		}
		gf, ok := micToGF[mic]
		if !ok {
			return "", fmt.Errorf("no Google Finance mapping for MIC %q (ticker %s)", mic, ident.GetValue())
		}
		return gf + ":" + ident.GetValue(), nil

	case apiv1.IdentifierType_OPENFIGI_TICKER:
		// OPENFIGI_TICKER domain is a Bloomberg exchange code, not a MIC.
		// Fall back to the instrument's exchange MIC.
		if exchangeMIC == "" {
			return "", fmt.Errorf("OPENFIGI_TICKER %s has no exchange MIC for mapping", ident.GetValue())
		}
		gf, ok := micToGF[exchangeMIC]
		if !ok {
			return "", fmt.Errorf("no Google Finance mapping for MIC %q (OPENFIGI_TICKER %s)", exchangeMIC, ident.GetValue())
		}
		return gf + ":" + ident.GetValue(), nil

	default:
		return "", fmt.Errorf("unsupported identifier type %s for Google Finance", ident.GetType())
	}
}

// MICToGF returns the Google Finance exchange code for a MIC, if known.
func MICToGF(mic string) (string, bool) {
	gf, ok := micToGF[mic]
	return gf, ok
}
