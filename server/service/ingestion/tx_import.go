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
func importTxPart(ctx context.Context, deps ingestDeps, userID, jobID string, part *archivev1.TxPart, rep *archiveimport.PartReporter) (bool, error) {
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
		txs, basis, rowIdx, err := windowTxs(w, offset, rep)
		if err != nil {
			return stored, err
		}
		offset += len(txs)

		res, err := ingestBatch(ctx, deps, ingestParams{
			UserID:          userID,
			Broker:          broker,
			Source:          w.GetSource(),
			JobID:           jobID,
			Txs:             txs,
			ShareCountBasis: basis,
			PeriodFrom:      w.GetPeriodFrom(),
			PeriodBefore:    w.GetPeriodBefore(),
			RowIndices:      rowIdx,
			TotalDeclared:   true,
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
// The store rebuilds groups from a shared key, and the file states one: a
// posting's group_ref travels through unchanged, and a posting carrying none is
// its own group, which groupPostings gives a non-colliding ref of its own.
func windowTxs(w *archivev1.TxWindow, offset int, rep *archiveimport.PartReporter) ([]*apiv1.Tx, []*time.Time, []int, error) {
	var txs []*apiv1.Tx
	var basis []*time.Time
	var rowIdx []int
	anyBasis := false
	for _, pos := range w.GetPostings() {
		row := offset + len(txs)
		b, err := postingBasis(pos)
		if err != nil {
			rep.Errf(row, "share_count_basis", err.Error())
			return nil, nil, nil, fmt.Errorf("posting %d: %w", row, err)
		}
		if b != nil {
			anyBasis = true
		}
		txs = append(txs, archiveTx(pos))
		basis = append(basis, b)
		rowIdx = append(rowIdx, row)
	}
	// A window that restates nothing carries no basis slice at all, which is
	// what leaves every posting to the insert trigger and its own date.
	if !anyBasis {
		basis = nil
	}
	return txs, basis, rowIdx, nil
}

// postingBasis parses a restated share count basis. Absent is nil, which the
// store reads as the posting's own timestamp date.
func postingBasis(p *archivev1.Posting) (*time.Time, error) {
	b := p.GetShareCountBasis()
	if b == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", b)
	if err != nil {
		return nil, fmt.Errorf("invalid date %q: want YYYY-MM-DD", b)
	}
	return &parsed, nil
}

// archiveTx converts one archive posting into the ingestion form.
//
// account_type travels as it was stored, routed residuals included. A group
// exported with its residual already sums to zero, and the balancer routes
// nothing for a commodity that does, so re-importing one is idempotent rather
// than doubling.
func archiveTx(p *archivev1.Posting) *apiv1.Tx {
	tx := &apiv1.Tx{
		Timestamp:             p.GetTimestamp(),
		InstrumentDescription: p.GetInstrumentDescription(),
		Type:                  p.GetType(),
		Quantity:              p.GetQuantity(),
		Account:               p.GetAccount(),
		AccountType:           p.GetAccountType(),
		GroupRef:              p.GetGroupRef(),
		BrokerRef:             p.GetBrokerRef(),
		CounterpartyAccount:   p.GetCounterpartyAccount(),
		// The archive message, so the evidence travels by reference rather than
		// being copied field by field into a second declaration of itself.
		Correlations:       p.GetCorrelations(),
		TradingCurrency:    p.GetTradingCurrency(),
		SettlementCurrency: p.GetSettlementCurrency(),
	}
	if p.UnitPrice != nil {
		tx.UnitPrice = proto.String(p.GetUnitPrice())
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
