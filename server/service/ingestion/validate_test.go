package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
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
		{"missing timestamp", &apiv1.Tx{InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		{"missing instrument_description", &apiv1.Tx{Timestamp: validTs, BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		{"missing broker_tx_type", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", Quantity: "1"}, 0, 1},
		{"unspecified member", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TX_TYPE_UNSPECIFIED}, Quantity: "1"}, 0, 1},
		// AMBIGUOUS is the resolved spelling of an unresolved set; declaring it
		// says less than the set itself.
		{"AMBIGUOUS member", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_AMBIGUOUS}, Quantity: "1"}, 0, 1},
		{"duplicate member", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET, typev1.TxType_TRADE_ASSET}, Quantity: "1"}, 0, 1},
		// An ancestor beside its descendant says nothing the ancestor alone
		// does not.
		{"ancestor beside descendant", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER, typev1.TxType_TRANSFER_INTERNAL}, Quantity: "1"}, 0, 1},
		{"valid", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10"}, 0, 0},
		{"valid antichain set", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER}, Quantity: "10"}, 0, 0},
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
		{Timestamp: ts, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10"},
		{Timestamp: ts, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "-5"},
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
		{Timestamp: validTs, InstrumentDescription: "AAPL", BrokerTxType: []typev1.TxType{typev1.TxType_TRADE_ASSET}, Quantity: "10"},
		{Timestamp: validTs, InstrumentDescription: "GOOG", Quantity: "5"}, // missing broker_tx_type
	}
	errs := ValidateTxs(txs)
	if len(errs) == 0 {
		t.Fatal("ValidateTxs() should return errors for missing broker_tx_type")
	}
	if !containsMessage(errs, "required") {
		t.Fatalf("expected a 'required' error, got %v", errs)
	}
}
