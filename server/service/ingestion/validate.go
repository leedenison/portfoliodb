package ingestion

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ValidateTx checks one tx and returns validation errors (field, message). rowIndex is 0-based.
//
// The broker_tx_type checks repeat what protovalidate enforces at the RPC edge
// -- non-empty, defined, no AMBIGUOUS, no duplicates -- for internal callers,
// and add the antichain rule, which protovalidate cannot express because the
// hierarchy is not in the proto.
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
	errs = append(errs, validateBrokerTxType(tx.GetBrokerTxType(), rowIndex)...)
	return errs
}

// validateBrokerTxType checks the declared candidate set: non-empty, every
// member a real declared value, no duplicates, and an antichain -- a set naming
// an ancestor beside its descendant says nothing the ancestor alone does not.
func validateBrokerTxType(set []typev1.TxType, rowIndex int32) []*apiv1.ValidationError {
	if len(set) == 0 {
		return []*apiv1.ValidationError{{RowIndex: rowIndex, Field: "broker_tx_type", Message: "required"}}
	}
	var errs []*apiv1.ValidationError
	seen := map[typev1.TxType]bool{}
	for _, t := range set {
		switch {
		case t == typev1.TxType_TX_TYPE_UNSPECIFIED:
			errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "broker_tx_type", Message: "member unspecified"})
		case t == typev1.TxType_AMBIGUOUS:
			// AMBIGUOUS is the resolved spelling of an unresolved set; declaring
			// it would say less than the set itself.
			errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "broker_tx_type", Message: "AMBIGUOUS cannot be declared"})
		case seen[t]:
			errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "broker_tx_type", Message: fmt.Sprintf("duplicate member %s", t)})
		}
		seen[t] = true
	}
	if len(errs) == 0 && !txtype.Antichain(set) {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "broker_tx_type", Message: "a member is an ancestor of another; state the ancestor alone"})
	}
	return errs
}

// ValidateBroker returns an error if broker is unspecified or unknown.
func ValidateBroker(b typev1.Broker) *apiv1.ValidationError {
	if b == typev1.Broker_BROKER_UNSPECIFIED {
		return &apiv1.ValidationError{RowIndex: -1, Field: "broker", Message: "required"}
	}
	if b != typev1.Broker_IBKR && b != typev1.Broker_SCHB && b != typev1.Broker_FIDELITY {
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
func ValidateBulkRequest(periodFrom, periodBefore *timestamppb.Timestamp) []*apiv1.ValidationError {
	var errs []*apiv1.ValidationError
	if periodFrom == nil || !periodFrom.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: -1, Field: "period_from", Message: "required"})
	}
	if periodBefore == nil || !periodBefore.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: -1, Field: "period_before", Message: "required"})
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

// instrumentsByID fetches the resolved instruments for a set of ids, keyed by id.
// The rows feed both asset-class validation and group balancing, so they are
// fetched once here rather than once by each consumer.
func instrumentsByID(ctx context.Context, database db.InstrumentDB, instrumentIDs []string) (map[string]*db.InstrumentRow, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, id := range instrumentIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	out := map[string]*db.InstrumentRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := database.ListInstrumentsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	for _, r := range rows {
		if r != nil {
			out[r.ID] = r
		}
	}
	return out, nil
}

// validateAssetClasses checks that each tx's resolved instrument has an
// asset class compatible with the asset class the source stated. A row with no
// stated hint makes no claim and is skipped -- "no claim" is weaker than
// UNKNOWN, which asserts a security of unstated class and still rejects CASH.
// txs and instrumentIDs are parallel slices; originalIndices maps each
// position back to the row index in the user-supplied request (for
// ValidationError.RowIndex). Returns one ValidationError per contradicting row.
func validateAssetClasses(txs []*apiv1.Tx, originalIndices []int, instrumentIDs []string, byID map[string]*db.InstrumentRow) []*apiv1.ValidationError {
	classByID := make(map[string]string, len(byID))
	for id, r := range byID {
		if r.AssetClass != nil {
			classByID[id] = *r.AssetClass
		}
	}
	var errs []*apiv1.ValidationError
	for i, tx := range txs {
		instID := instrumentIDs[i]
		if instID == "" {
			continue
		}
		if tx.GetAssetClassHint() == typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
			continue
		}
		// Missing IDs (e.g. instrument deleted between resolution and
		// validation) default to "" here, which IsAssetClassCompatible
		// treats as compatible -- no signal to contradict.
		resolved := classByID[instID]
		implied := db.AssetClassToStr(tx.GetAssetClassHint())
		if db.IsAssetClassCompatible(implied, resolved) {
			continue
		}
		errs = append(errs, &apiv1.ValidationError{
			RowIndex: int32(originalIndices[i]),
			Field:    "asset_class_hint",
			Message:  fmt.Sprintf("stated asset class %s but resolved instrument has asset class %s", implied, resolved),
		})
	}
	return errs
}
