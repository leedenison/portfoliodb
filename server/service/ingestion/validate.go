package ingestion

import (
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ValidateTx checks one tx and returns validation errors (field, message). rowIndex is 0-based.
func ValidateTx(tx *apiv1.Tx, rowIndex int32) []*apiv1.ValidationError {
	var errs []*apiv1.ValidationError
	if tx == nil {
		return []*apiv1.ValidationError{{RowIndex: rowIndex, Field: "tx", Message: "transaction is required"}}
	}
	if tx.Timestamp == nil || !tx.Timestamp.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "timestamp", Message: "required"})
	}
	if tx.InstrumentDescription == "" {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "instrument_description", Message: "required"})
	}
	if tx.Type == apiv1.TxType_TX_TYPE_UNSPECIFIED {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "type", Message: "required"})
	}
	return errs
}

// ValidateBroker returns an error if broker is unspecified or unknown.
func ValidateBroker(b apiv1.Broker) *apiv1.ValidationError {
	if b == apiv1.Broker_BROKER_UNSPECIFIED {
		return &apiv1.ValidationError{RowIndex: -1, Field: "broker", Message: "required"}
	}
	if b != apiv1.Broker_IBKR && b != apiv1.Broker_SCHB {
		return &apiv1.ValidationError{RowIndex: -1, Field: "broker", Message: "unknown broker"}
	}
	return nil
}

// ValidateBulkRequest validates UpsertTxsRequest (period and broker).
func ValidateBulkRequest(periodFrom, periodTo *timestamppb.Timestamp) []*apiv1.ValidationError {
	var errs []*apiv1.ValidationError
	if periodFrom == nil || !periodFrom.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: -1, Field: "period_from", Message: "required"})
	}
	if periodTo == nil || !periodTo.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: -1, Field: "period_to", Message: "required"})
	}
	return errs
}

// ValidateTxs runs ValidateTx on each tx and checks for duplicate natural keys within the batch.
func ValidateTxs(txs []*apiv1.Tx) []*apiv1.ValidationError {
	var errs []*apiv1.ValidationError
	seen := make(map[string]bool)
	for i, tx := range txs {
		rowIndex := int32(i)
		errs = append(errs, ValidateTx(tx, rowIndex)...)
		if tx != nil && tx.Timestamp != nil && tx.Timestamp.IsValid() && tx.InstrumentDescription != "" {
			key := tx.Timestamp.AsTime().Format(time.RFC3339Nano) + "\x00" + tx.InstrumentDescription
			if seen[key] {
				errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "timestamp,instrument_description", Message: "duplicate in batch"})
			}
			seen[key] = true
		}
	}
	return errs
}
