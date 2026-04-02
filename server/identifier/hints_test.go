package identifier

import "testing"

func TestShouldAttemptPlugin(t *testing.T) {
	cashKinds := map[string]bool{InstrumentKindCash: true}
	secKinds := map[string]bool{InstrumentKindSecurity: true}
	cashTypes := map[string]bool{SecurityTypeHintCash: true}
	stockTypes := map[string]bool{SecurityTypeHintStock: true, SecurityTypeHintFixedIncome: true}

	tests := []struct {
		name            string
		acceptableKinds map[string]bool
		acceptableTypes map[string]bool
		kind            string
		secType         string
		want            bool
	}{
		// TRANSFER: kind=SECURITY, type=UNKNOWN
		{"cash plugin rejects SECURITY kind", cashKinds, cashTypes, InstrumentKindSecurity, SecurityTypeHintUnknown, false},
		{"security plugin accepts SECURITY+UNKNOWN", secKinds, stockTypes, InstrumentKindSecurity, SecurityTypeHintUnknown, true},
		// BUYSTOCK: kind=SECURITY, type=STOCK
		{"security plugin accepts STOCK", secKinds, stockTypes, InstrumentKindSecurity, SecurityTypeHintStock, true},
		{"cash plugin rejects STOCK", cashKinds, cashTypes, InstrumentKindSecurity, SecurityTypeHintStock, false},
		// INCOME: kind=CASH, type=CASH
		{"cash plugin accepts CASH", cashKinds, cashTypes, InstrumentKindCash, SecurityTypeHintCash, true},
		{"security plugin rejects CASH kind", secKinds, stockTypes, InstrumentKindCash, SecurityTypeHintCash, false},
		// BUYOPT: kind=SECURITY, type=OPTION -- plugin only accepts STOCK/FIXED_INCOME
		{"stock plugin rejects OPTION type", secKinds, stockTypes, InstrumentKindSecurity, SecurityTypeHintOption, false},
		// Empty maps (accept all)
		{"nil kinds accepts any kind", nil, stockTypes, InstrumentKindCash, SecurityTypeHintStock, true},
		{"nil types accepts any type", secKinds, nil, InstrumentKindSecurity, SecurityTypeHintOption, true},
		{"both nil accepts everything", nil, nil, InstrumentKindSecurity, SecurityTypeHintStock, true},
		// Empty hint kind
		{"empty hint kind passes kind gate", secKinds, stockTypes, "", SecurityTypeHintStock, true},
		// Empty hint type
		{"empty hint type passes type gate", secKinds, stockTypes, InstrumentKindSecurity, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldAttemptPlugin(tt.acceptableKinds, tt.acceptableTypes, tt.kind, tt.secType)
			if got != tt.want {
				t.Errorf("ShouldAttemptPlugin() = %v, want %v", got, tt.want)
			}
		})
	}
}
