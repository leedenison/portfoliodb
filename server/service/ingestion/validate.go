package ingestion

import (
	"context"
	"fmt"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
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
	if tx.OrderDate == nil || !tx.OrderDate.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "order_date", Message: "required"})
	}
	// Both dates are required, and a source with one date writes it to both. So an
	// absent trade date is a converter that forgot rather than a source that did
	// not distinguish them, and defaulting it here would hide that.
	if tx.TradeDate == nil || !tx.TradeDate.IsValid() {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "trade_date", Message: "required"})
	}
	if tx.InstrumentDescription == "" {
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: "instrument_description", Message: "required"})
	}
	errs = append(errs, validateBrokerTxType(tx.GetBrokerTxType(), rowIndex)...)
	errs = append(errs, validateSettlementAmount(tx, rowIndex)...)
	// synthetic_purpose says the server made the posting, so nothing that came from
	// outside may claim it: a source that could stamp RESIDUAL on a row would get it
	// thrown away and derived again by the next replace. The balancer sets it on the
	// counterparties it routes, after this runs.
	if tx.GetSyntheticPurpose() != "" {
		errs = append(errs, &apiv1.ValidationError{
			RowIndex: rowIndex,
			Field:    "synthetic_purpose",
			Message:  "server-derived; not accepted on a posting a source supplied",
		})
	}
	// group_id says which event the posting is a leg of, which is the answer grouping
	// exists to derive. A source that stated one would have it thrown away by the next
	// regroup, so it is refused rather than silently dropped.
	if tx.GetGroupId() != "" {
		errs = append(errs, &apiv1.ValidationError{
			RowIndex: rowIndex,
			Field:    "group_id",
			Message:  "server-derived; not accepted on a posting a source supplied",
		})
	}
	return errs
}

// validateSettlementAmount rejects the source's cash total on a posting whose
// own quantity is already that total.
//
// A posting is money exactly when it cannot be a trade's asset leg, which is the
// same predicate the converters apply to decide whether to read a row's amount as
// its quantity. Carrying the total again on such a row would put the same figure
// on the posting twice and leave two values to disagree, and grouping reads it
// precisely because it is a second, independently transcribed number.
func validateSettlementAmount(tx *apiv1.Tx, rowIndex int32) []*apiv1.ValidationError {
	if tx.SettlementAmount == nil {
		return nil
	}
	if txtype.MayBe(tx.GetBrokerTxType(), typev1.TxType_TRADE_ASSET) {
		return nil
	}
	return []*apiv1.ValidationError{{
		RowIndex: rowIndex,
		Field:    "settlement_amount",
		Message:  "not carried on a money posting, whose quantity is already the amount",
	}}
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

// validateStatedIdentifiers reports the identity claims one upload cannot all
// hold.
//
// Every identifier in one upload is stated as of one vintage -- the export date
// the file carries, which ingestParams holds for the whole batch -- so no reading
// of the validity intervals reconciles two of them that disagree. They are
// offered together, as of one moment. The artefact is faulty, and nothing in it
// says which half is right, so the upload is rejected rather than half of it
// believed. See docs/adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
//
// The check is on the subject -- the type and, where it has one, the domain --
// rather than on the type alone, because a ticker under two domains names two
// listings and a security quoted in two places states both legitimately.
//
// One description is one security: it is the key resolution caches on, so two
// values for one subject under one description are two securities where the file
// names one. The converse is not looked for. Two descriptions naming one
// identifier is a broker writing a security several ways -- a statement, a
// confirmation, a tax document -- and they resolve to one instrument, which is
// the point of storing the mapping.
//
// The converter checks this before upload and this checks it again. Not because
// the converters are doubted, but because what makes an upload acceptable has to
// hold wherever it arrives from: the extension has converters of its own, and the
// ingestion API is reachable without either.
//
// rowIdx maps each posting to the row the source numbered it, so an archive
// window reports against the file rather than against the window.
func validateStatedIdentifiers(txs []*apiv1.Tx, txHints [][]identifier.Identifier, rowIdx []int) []*apiv1.ValidationError {
	type stated struct {
		value string
		row   int
	}
	seen := make(map[string]stated)
	var errs []*apiv1.ValidationError
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		if desc == "" {
			continue
		}
		for _, h := range txHints[i] {
			if h.Value == "" {
				continue
			}
			key := desc + "\x00" + h.Type + "\x00" + h.Domain
			first, ok := seen[key]
			if !ok {
				seen[key] = stated{value: h.Value, row: rowIdx[i]}
				continue
			}
			if first.value == h.Value {
				continue
			}
			// Reported against the first value stated, which is arbitrary and
			// says so: nothing in the file makes one of them the right one, which
			// is the whole reason the upload is refused rather than resolved.
			errs = append(errs, &apiv1.ValidationError{
				RowIndex: int32(rowIdx[i]),
				Field:    "identifier_hints",
				Message: fmt.Sprintf("%s is %s %s here and %s on row %d; one file states one identity, so nothing says which is right",
					desc, h.Type, h.Value, first.value, first.row),
			})
		}
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

// validateAssetClasses checks that no tx's resolved instrument has an asset
// class its source contradicted. A row with no stated hint makes no claim and
// is skipped, and so is one that stated only the root of the vocabulary, which
// rules nothing out; what is refused is a claim and a resolution that cannot
// both be true of one security -- a stated EQUITY against a resolved ETF has
// not contradicted anything.
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
		// Missing IDs (e.g. instrument deleted between resolution and
		// validation) default to "" here, which contradicts nothing -- there
		// is no resolved class to disagree with.
		resolved := classByID[instID]
		implied := db.AssetClassToStr(tx.GetAssetClassHint())
		if !db.AssetClassContradicts(implied, resolved) {
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
