package ingestion

import (
	"context"
	"fmt"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
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
	if b != apiv1.Broker_IBKR && b != apiv1.Broker_SCHB && b != apiv1.Broker_FIDELITY {
		return &apiv1.ValidationError{RowIndex: -1, Field: "broker", Message: "unknown broker"}
	}
	return nil
}

// ValidateSource returns an error if source is empty.
func ValidateSource(source string) *apiv1.ValidationError {
	if source == "" {
		return &apiv1.ValidationError{RowIndex: -1, Field: "source", Message: "required"}
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

// ValidateTxs runs ValidateTx on each tx.
func ValidateTxs(txs []*apiv1.Tx) []*apiv1.ValidationError {
	var errs []*apiv1.ValidationError
	for i, tx := range txs {
		errs = append(errs, ValidateTx(tx, int32(i))...)
	}
	return errs
}

// validateAssetClasses checks that each tx's resolved instrument has an
// asset class compatible with the asset class implied by the tx type.
// txs and instrumentIDs are parallel slices; originalIndices maps each
// position back to the row index in the user-supplied request (for
// ValidationError.RowIndex). Returns one ValidationError per contradicting
// row. The second return value is a non-nil error only when the DB lookup
// itself fails.
func validateAssetClasses(ctx context.Context, database db.InstrumentDB, txs []*apiv1.Tx, originalIndices []int, instrumentIDs []string) ([]*apiv1.ValidationError, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, id := range instrumentIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := database.ListInstrumentsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list instruments for asset class validation: %w", err)
	}
	classByID := make(map[string]string, len(rows))
	for _, r := range rows {
		if r == nil {
			continue
		}
		var ac string
		if r.AssetClass != nil {
			ac = *r.AssetClass
		}
		classByID[r.ID] = ac
	}
	var errs []*apiv1.ValidationError
	for i, tx := range txs {
		instID := instrumentIDs[i]
		if instID == "" {
			continue
		}
		resolved := classByID[instID]
		implied := db.TxTypeToAssetClass(tx.GetType())
		if db.IsAssetClassCompatible(implied, resolved) {
			continue
		}
		errs = append(errs, &apiv1.ValidationError{
			RowIndex: int32(originalIndices[i]),
			Field:    "type",
			Message:  fmt.Sprintf("transaction type %s implies asset class %s but resolved instrument has asset class %s", tx.GetType(), implied, resolved),
		})
	}
	return errs, nil
}
