package identifier

// contractCurrencies maps a contract identifier vocabulary to the currency the
// contract's terms are quoted in.
//
// OCC and OPRA are the symbologies of the US options market: a contract wearing
// either is cleared there and struck in dollars. That is a property of the
// vocabulary rather than of any one symbol, which is what makes it safe to read
// off the identifier type alone -- neither spells a currency, and neither is
// used anywhere the answer would be different.
//
// FUT_OPT is deliberately absent. Futures options are listed on exchanges around
// the world and the symbol does not say which, so nothing about the vocabulary
// implies a currency.
var contractCurrencies = map[string]string{
	"OCC":  "USD",
	"OPRA": "USD",
}

// StrikeCurrency is the currency a derivative's strike is quoted in, which is
// the currency of the underlying line it delivers.
//
// A stated currency wins: it is what a source said about this contract, and no
// vocabulary-level rule outranks that. Where nothing states one, a contract
// symbol may still imply it. Where neither answers, the empty string says the
// currency is unknown, and a caller cannot name the line the contract delivers.
//
// See docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
func StrikeCurrency(stated string, identifierTypes []string) string {
	if stated != "" {
		return stated
	}
	for _, t := range identifierTypes {
		if c, ok := contractCurrencies[t]; ok {
			return c
		}
	}
	return ""
}
