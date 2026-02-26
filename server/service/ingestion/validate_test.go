package ingestion

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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
		{"missing timestamp", &apiv1.Tx{InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 1}, 0, 1},
		{"missing instrument_description", &apiv1.Tx{Timestamp: validTs, Type: apiv1.TxType_BUYSTOCK, Quantity: 1}, 0, 1},
		{"missing type", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", Quantity: 1}, 0, 1},
		{"valid", &apiv1.Tx{Timestamp: validTs, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10}, 0, 0},
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
		name  string
		broker apiv1.Broker
		want  bool
	}{
		{"unspecified", apiv1.Broker_BROKER_UNSPECIFIED, true},
		{"IBKR", apiv1.Broker_IBKR, false},
		{"SCHB", apiv1.Broker_SCHB, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBroker(tc.broker)
			hasErr := err != nil
			if hasErr != tc.want {
				t.Fatalf("ValidateBroker() error = %v, want err=%v", err, tc.want)
			}
		})
	}
}

func TestValidateTxs_duplicate(t *testing.T) {
	ts := timestamppb.Now()
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10},
		{Timestamp: ts, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: 5},
	}
	errs := ValidateTxs(txs)
	var dup bool
	for _, e := range errs {
		if e.Message == "duplicate in batch" {
			dup = true
			break
		}
	}
	if !dup {
		t.Fatalf("ValidateTxs() should report duplicate in batch, got %v", errs)
	}
}
