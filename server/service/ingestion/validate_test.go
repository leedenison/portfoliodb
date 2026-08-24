package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
)

func TestValidateTx(t *testing.T) {
	validTs := timestamppb.Now()
	tests := []struct {
		name   string
		tx     *apiv1.Tx
		rowIdx int32
		want   int
	}{
		{"nil tx", nil, 0, 1},
		// Both dates missing is two faults, because a source with one date writes
		// it to both: an absent trade date is a converter that forgot rather than
		// a source that does not distinguish them.
		{"missing both dates", &apiv1.Tx{InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 2},
		{"missing order date", &apiv1.Tx{TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		{"missing trade date", &apiv1.Tx{OrderDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		{"missing instrument_description", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		{"missing broker_tx_type", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", Quantity: "1"}, 0, 1},
		{"unspecified member", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TX_TYPE_UNSPECIFIED}, Quantity: "1"}, 0, 1},
		// AMBIGUOUS is the resolved spelling of an unresolved set; declaring it
		// says less than the set itself.
		{"AMBIGUOUS member", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_AMBIGUOUS}, Quantity: "1"}, 0, 1},
		{"duplicate member", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET, typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		// An ancestor beside its descendant says nothing the ancestor alone
		// does not.
		{"ancestor beside descendant", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER, typev1.TxType_TRANSFER_INTERNAL}, Quantity: "1"}, 0, 1},
		// synthetic_purpose says the server made the posting, so a row claiming one
		// would be thrown away and derived again by the next replace.
		{"claims a synthetic purpose", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1", SyntheticPurpose: db.RoutedPurpose}, 0, 1},
		// group_id is the answer grouping exists to derive, so a row stating one
		// would have it rederived out from under it by the next regroup.
		{"claims a group", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1", GroupId: "18e0b2a8-0000-4000-8000-000000000000"}, 0, 1},
		{"valid", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10"}, 0, 0},
		{"valid antichain set", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, Quantity: "10"}, 0, 0},
		{"line conversion", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER_LISTING}, Quantity: "10"}, 0, 0},
		{"line conversion beside its ancestor", &apiv1.Tx{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER, typev1.TxType_TRANSFER_LISTING}, Quantity: "1"}, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateTx(tc.tx, tc.rowIdx)
			if len(got) != tc.want {
				t.Fatalf("ValidateTx() returned %d errors, want %d", len(got), tc.want)
			}
		})
	}
}

func TestValidateBroker(t *testing.T) {
	tests := []struct {
		name    string
		broker  typev1.Broker
		wantErr bool
	}{
		{"unspecified", typev1.Broker_BROKER_UNSPECIFIED, true},
		{"IBKR", typev1.Broker_IBKR, false},
		{"SCHB", typev1.Broker_SCHB, false},
		{"FIDELITY", typev1.Broker_FIDELITY, false},
		{"unknown broker", typev1.Broker(99), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBroker(tc.broker)
			hasErr := err != nil
			if hasErr != tc.wantErr {
				t.Fatalf("ValidateBroker() error = %v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func containsMessage(errs []*apiv1.ValidationError, msg string) bool {
	for _, e := range errs {
		if e != nil && e.Message == msg {
			return true
		}
	}
	return false
}

func TestValidateBulkRequest(t *testing.T) {
	validTs := timestamppb.Now()
	tests := []struct {
		name         string
		periodFrom   *timestamppb.Timestamp
		periodBefore *timestamppb.Timestamp
		wantCount    int
	}{
		{"both nil", nil, nil, 2},
		{"periodFrom nil", nil, validTs, 1},
		{"periodBefore nil", validTs, nil, 1},
		{"both valid", validTs, validTs, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateBulkRequest(tc.periodFrom, tc.periodBefore)
			if len(got) != tc.wantCount {
				t.Fatalf("ValidateBulkRequest() returned %d errors, want %d", len(got), tc.wantCount)
			}
		})
	}
}

func TestValidateTxs_sameTimestampAndDescriptionAllowed(t *testing.T) {
	// No natural key: same (timestamp, instrument_description) in one batch is allowed.
	ts := timestamppb.Now()
	txs := []*apiv1.Tx{
		{OrderDate: ts,
			TradeDate: ts, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10"},
		{OrderDate: ts,
			TradeDate: ts, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "-5"},
	}
	errs := ValidateTxs(txs)
	if len(errs) != 0 {
		t.Fatalf("ValidateTxs() should allow same timestamp+description in batch, got %v", errs)
	}
}

func TestValidateTxs_empty(t *testing.T) {
	errs := ValidateTxs(nil)
	if len(errs) != 0 {
		t.Fatalf("ValidateTxs(nil) should return no errors, got %d", len(errs))
	}
	errs = ValidateTxs([]*apiv1.Tx{})
	if len(errs) != 0 {
		t.Fatalf("ValidateTxs(empty) should return no errors, got %d", len(errs))
	}
}

func TestValidateTxs_perTxErrors(t *testing.T) {
	validTs := timestamppb.Now()
	txs := []*apiv1.Tx{
		{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10"},
		{OrderDate: validTs,
			TradeDate: validTs, InstrumentDescription: "GOOG", Quantity: "5"}, // missing broker_tx_type
	}
	errs := ValidateTxs(txs)
	if len(errs) == 0 {
		t.Fatal("ValidateTxs() should return errors for missing broker_tx_type")
	}
	if !containsMessage(errs, "required") {
		t.Fatalf("expected a 'required' error, got %v", errs)
	}
}

// TestValidateSettlementAmount pins where the source's own cash total may be
// stated. A posting is money exactly when it cannot be a trade's asset leg, and
// on such a posting the quantity is already that total.
func TestValidateSettlementAmount(t *testing.T) {
	validTs := timestamppb.Now()
	amount := "20514.62"
	tx := func(set []typev1.TxType, settlement *string) *apiv1.Tx {
		return &apiv1.Tx{
			OrderDate:             validTs,
			TradeDate:             validTs,
			InstrumentDescription: "AAPL",
			BrokerTxType:          set,
			Quantity:              "10",
			SettlementAmount:      settlement,
		}
	}
	tests := []struct {
		name string
		tx   *apiv1.Tx
		want int
	}{
		{"stated on a trade's asset leg", tx([]typev1.TxType{typev1.TxType_TRADE_ASSET}, &amount), 0},
		// The source said only TRADE, so the row may yet be an asset leg.
		{"stated on an unnarrowed trade", tx([]typev1.TxType{typev1.TxType_TRADE}, &amount), 0},
		{"stated on a trade's cash leg", tx([]typev1.TxType{typev1.TxType_TRADE_CASH}, &amount), 1},
		{"stated on a transfer", tx([]typev1.TxType{typev1.TxType_TRANSFER}, &amount), 1},
		// Fidelity's Cash In: money under either reading, so the quantity is
		// already the amount.
		{"stated on a declared ambiguity between two money readings", tx([]typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, &amount), 1},
		{"absent on a money row", tx([]typev1.TxType{typev1.TxType_DIVIDEND}, nil), 0},
		{"absent on an asset leg", tx([]typev1.TxType{typev1.TxType_TRADE_ASSET}, nil), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateTx(tc.tx, 0)
			if len(got) != tc.want {
				t.Fatalf("ValidateTx() returned %d errors, want %d: %v", len(got), tc.want, got)
			}
		})
	}
}
