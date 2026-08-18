package ingestion

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
)

// importTxPart applies an archive's transaction part, one window at a time.
//
// A window is a replacement scope, so each one is stored with the period it
// states rather than with a period inferred from its postings: a window holding
// no postings is a valid instruction to clear that period, and an inferred one
// could never say that. See docs/adr/0002-transaction-ingestion-model.md.
//
// Split adjustments are recomputed once for the whole part rather than once per
// window. The pass rescans every posting of each instrument it is given, so
// running it per window would rescan the same instruments repeatedly for a
// result that only the last run decides.
//
// asOf is the document's envelope vintage, and every window is resolved against
// it: knowledge time that differs between one file's own parts is not knowledge
// time, and the same holds between one part's own windows.
func importTxPart(ctx context.Context, deps ingestDeps, userID, jobID string, part *archivev1.TxPart, asOf *time.Time, rep *archiveimport.PartReporter) (bool, error) {
	windows := part.GetWindows()
	// The part's unit is a posting, which is what a window's own progress counts
	// and what a reader watching the job expects to see move.
	total := 0
	for _, w := range windows {
		total += len(w.GetPostings())
	}
	rep.Total(ctx, total)

	var touched []string
	stored := false
	// Row indices run across the whole part rather than restarting per window,
	// so a problem points at a posting in the document.
	offset := 0
	for _, w := range windows {
		broker := db.BrokerToStr(w.GetBroker())
		if broker == "" {
			rep.Errf(offset, "broker", fmt.Sprintf("unknown broker %q", w.GetBroker()))
			return stored, fmt.Errorf("window names broker %q", w.GetBroker())
		}
		txs, rowIdx, err := windowTxs(w, offset, rep)
		if err != nil {
			return stored, err
		}
		offset += len(txs)

		res, err := ingestBatch(ctx, deps, ingestParams{
			UserID:        userID,
			Broker:        broker,
			Source:        w.GetSource(),
			JobID:         jobID,
			Txs:           txs,
			PeriodFrom:    w.GetPeriodFrom(),
			PeriodBefore:  w.GetPeriodBefore(),
			ExportedAt:    asOf,
			RowIndices:    rowIdx,
			TotalDeclared: true,
		}, rep)
		if err != nil {
			return stored, err
		}
		if res.Stored > 0 {
			stored = true
		}
		touched = append(touched, res.InstrumentIDs...)
	}

	recomputeSplitAdjustedTxs(ctx, deps.DB, touched)
	return stored, nil
}

// windowTxs turns one window's postings into the form the ingestion pipeline
// takes.
//
// Nothing about grouping travels: the postings go over flat and the store
// partitions them from the evidence they carry, which is the same answer a fresh
// upload of the same records would get.
func windowTxs(w *archivev1.TxWindow, offset int, rep *archiveimport.PartReporter) ([]*apiv1.Tx, []int, error) {
	var txs []*apiv1.Tx
	var rowIdx []int
	for _, pos := range w.GetPostings() {
		row := offset + len(txs)
		txs = append(txs, archiveTx(pos))
		rowIdx = append(rowIdx, row)
	}
	return txs, rowIdx, nil
}

// archiveTx converts one archive posting into the ingestion form.
//
// account_type travels as it was stored, routed residuals included. A group
// exported with its residual already sums to zero, and the balancer routes
// nothing for a commodity that does, so re-importing one is idempotent rather
// than doubling.
func archiveTx(p *archivev1.Posting) *apiv1.Tx {
	tx := &apiv1.Tx{
		OrderDate:             p.GetOrderDate(),
		TradeDate:             p.GetTradeDate(),
		InstrumentDescription: p.GetInstrumentDescription(),
		// The declaration travels; the resolved value does not, and is
		// re-derived by the ingest pipeline like any upload's.
		BrokerTxType:   p.GetBrokerTxType(),
		AssetClassHint: p.GetAssetClassHint(),
		Quantity:       p.GetQuantity(),
		Account:        p.GetAccount(),
		AccountType:    p.GetAccountType(),
		// The archive message, so the evidence travels by reference rather than
		// being copied field by field into a second declaration of itself.
		Correlations:       p.GetCorrelations(),
		TradingCurrency:    p.GetTradingCurrency(),
		SettlementCurrency: p.GetSettlementCurrency(),
	}
	if p.UnitPrice != nil {
		tx.UnitPrice = proto.String(p.GetUnitPrice())
	}
	if p.SettlementAmount != nil {
		tx.SettlementAmount = proto.String(p.GetSettlementAmount())
	}
	// The archive can name several identifiers per posting, which the flat CSV
	// could not. They are hints rather than an assertion: resolution still runs,
	// and an instrument the importing instance already holds wins.
	for _, h := range p.GetIdentifierHints() {
		tx.IdentifierHints = append(tx.IdentifierHints, &apiv1.InstrumentIdentifier{
			Type:   h.GetType(),
			Value:  h.GetValue(),
			Domain: h.GetDomain(),
		})
	}
	return tx
}
