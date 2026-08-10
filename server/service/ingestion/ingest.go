package ingestion

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/description"
	"github.com/leedenison/portfoliodb/server/telemetry"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// errBatchRejected reports that a batch was not stored because its contents
// were rejected. What was wrong is on the reporter, a problem at a time; this
// only says that the caller should mark its job or its part failed.
//
// A rejected posting fails its whole batch rather than being dropped, which is
// what the archive format allows a part to do for a row. Postings are not
// independent: a group balances or it does not, so storing the survivors of a
// group would store something that never happened.
var errBatchRejected = errors.New("batch rejected")

// ingestDeps are the collaborators a batch needs, distinct from what it is
// ingesting.
type ingestDeps struct {
	DB           db.DB
	Registry     *identifier.Registry
	DescRegistry *description.Registry
	Counter      telemetry.CounterIncrementer
}

// ingestParams is one batch of postings and the scope they are stored under.
type ingestParams struct {
	UserID string
	// Broker in its stored form, not the enum.
	Broker string
	Source string
	// JobID stamps the groups this batch creates, so a group can be traced back
	// to the upload or import that wrote it.
	JobID string
	Txs   []*apiv1.Tx
	// ShareCountBasis is parallel to Txs, or nil when every row is as-traded.
	ShareCountBasis []*time.Time
	// PeriodFrom and PeriodBefore make this a replacement rather than an append.
	// Both nil appends the batch as one group, which is the manual-entry path.
	PeriodFrom, PeriodBefore *timestamppb.Timestamp
	// RowIndices maps each tx back to the index the caller wants a problem
	// reported against, so an error points at the file rather than at the
	// filtered slice. nil means the tx's own position.
	RowIndices []int
	// TotalDeclared says the caller has already told the reporter how much work
	// there is, counting the postings handed to this batch. A caller running
	// several batches -- one per archive window -- declares one total across all
	// of them, and the batch then advances past what it filters out so the
	// progress still reaches that total. Left false, the batch declares its own
	// total, which is what an upload of one file wants: the count is what will
	// be stored.
	TotalDeclared bool
}

// ingestResult reports what a batch did, for a caller deciding what to nudge.

type ingestResult struct {
	Stored int
	// InstrumentIDs is every instrument the batch touched, routed residuals
	// included, so a caller can recompute split adjustments once for several
	// batches rather than once per batch.
	InstrumentIDs []string
}

// ingestBatch resolves, balances and stores one batch of postings.
//
// It is the whole of the ingestion pipeline below the payload: shared by the
// broker upload, which is one batch per job, and by the archive transaction
// part, which is one batch per window. Problems go to the reporter and a
// rejection comes back as errBatchRejected, so the caller decides whether a
// rejection fails a job or only a part.
func ingestBatch(ctx context.Context, deps ingestDeps, p ingestParams, rep *archiveimport.PartReporter) (ingestResult, error) {
	var out ingestResult

	if errs := ValidateTxs(p.Txs); len(errs) > 0 {
		rep.Errs(errs)
		return out, errBatchRejected
	}
	// Resolve every declared set to its common ancestor, overwriting whatever the
	// client sent: the resolved value is derived state, and this is the one place
	// it is derived. Server-side grouping will refine this by letting the pass
	// that claims a row resolve it; until then the common ancestor is what keeps
	// whatever was known.
	for _, tx := range p.Txs {
		tx.ResolvedTxType = txtype.Resolve(tx.GetBrokerTxType())
	}
	// A rule this reader cannot load is not a reason to reject the batch: the
	// filter narrows what is stored, and storing more than asked is better than
	// storing nothing.
	ignoredClasses, err := deps.DB.ListIgnoredAssetClasses(ctx, p.UserID)
	if err != nil {
		log.Printf("ingest: load ignored asset classes: %v", err)
		ignoredClasses = nil
	}
	// Drop whatever the user's ignore rules cover. Where the caller declared the
	// total, a dropped posting still counts as processed: its total is what the
	// file carried, and a job that ended below its own total would read as
	// unfinished.
	txs, filteredIdx := filterIgnoredTxs(p.Txs, p.Broker, ignoredClasses)
	if p.TotalDeclared {
		rep.Advance(ctx, len(p.Txs)-len(txs))
	}
	replacing := p.PeriodFrom != nil && p.PeriodBefore != nil
	// A batch handed no postings at all, over a period, is an instruction to
	// clear that period -- which is what an archive window holding no groups
	// says, and the reason a window states its period rather than having one
	// inferred from its groups.
	//
	// A batch whose postings were all filtered out is not the same statement.
	// Nothing asked for those rows to be removed; a filter decided this instance
	// does not store them, and acting on that by emptying the period would
	// delete rows the file never mentioned.
	clearing := replacing && len(p.Txs) == 0
	if len(txs) == 0 && !clearing {
		return out, nil
	}
	if !p.TotalDeclared {
		rep.Total(ctx, len(txs))
	}
	rowIdx := make([]int, len(filteredIdx))
	for i, f := range filteredIdx {
		rowIdx[i] = f
		if p.RowIndices != nil && f < len(p.RowIndices) {
			rowIdx[i] = p.RowIndices[f]
		}
	}

	if clearing {
		// Nothing to resolve or balance: the delete is the whole instruction.
		if err := deps.DB.ReplaceTxsInPeriod(ctx, p.UserID, p.Broker, p.JobID, p.PeriodFrom, p.PeriodBefore, nil, nil, nil, nil); err != nil {
			rep.Errf(-1, "txs", err.Error())
			return out, fmt.Errorf("clear period: %w", err)
		}
		return out, nil
	}

	cache, extractedHints, err := extractDescHints(ctx, deps.DB, deps.DescRegistry, deps.Counter, p.Source, p.Broker, txs)
	if err != nil {
		rep.Errf(-1, "txs", err.Error())
		return out, fmt.Errorf("extract description hints: %w", err)
	}
	instrumentIDs, idErrs, err := resolveInstruments(ctx, deps.DB, deps.Registry, p.Broker, p.Source, deps.Counter, txs, rowIdx, cache, extractedHints, rep)
	if err != nil {
		rep.Errf(-1, "instrument_description", err.Error())
		return out, fmt.Errorf("resolve instruments: %w", err)
	}
	if len(idErrs) > 0 {
		_ = deps.DB.AppendIdentificationErrors(ctx, p.JobID, idErrs)
	}
	// The resolved instruments feed both the asset-class check and balancing.
	instByID, err := instrumentsByID(ctx, deps.DB, instrumentIDs)
	if err != nil {
		rep.Errf(-1, "txs", err.Error())
		return out, fmt.Errorf("load instruments: %w", err)
	}
	// Catches contradictions that arise when two txs share (source, description)
	// but state different asset classes (e.g. a trade stating STOCK beside
	// income stating CASH), as well as any other path where resolution lands on
	// the wrong class.
	if classErrs := validateAssetClasses(txs, rowIdx, instrumentIDs, instByID); len(classErrs) > 0 {
		rep.Errs(classErrs)
		return out, errBatchRejected
	}
	// Balance every group by routing whatever its postings leave over to an
	// explicit counterparty. This runs after filtering, so a dropped leg cannot
	// contribute a residual, and after resolution, because telling a currency
	// commodity from a security one is a property of the instrument.
	balanceInsts := balanceInstruments(instByID)
	// The neutrality check needs the contract size, which is why it runs here
	// rather than with the shape checks in ValidateTxs.
	if wErrs := validateWeightNeutrality(txs, rowIdx, instrumentIDs, balanceInsts); len(wErrs) > 0 {
		rep.Errs(wErrs)
		return out, errBatchRejected
	}
	routed := routeResiduals(txs, instrumentIDs, balanceInsts)
	routedTxs, routedIDs, routedWeights, unresolved := resolveRouted(ctx, deps.DB, routed)
	// A residual with no instrument to post it against leaves its group
	// unbalanced, which the balance constraint rejects at COMMIT and which would
	// surface as a constraint violation naming nothing. Naming the commodity
	// here is what makes it legible. Currencies are seeded, so this is a safety
	// net rather than a live path.
	if len(unresolved) > 0 {
		for _, cur := range unresolved {
			log.Printf("ingest: no instrument for residual commodity %q", cur)
			rep.Errf(-1, "instrument_description",
				fmt.Sprintf("no instrument for residual commodity %q; the group it balances cannot be stored", cur))
		}
		return out, errBatchRejected
	}
	// Weighed after routing, which is what assigns a synthetic group_ref to a
	// posting that had none, and before the routed postings are appended, since
	// those carry the weight of the residual they negate rather than one derived
	// from the tx.
	txWeights := weights(txs, instrumentIDs, balanceInsts)
	basis := filterBasis(p.ShareCountBasis, filteredIdx)
	txs = append(txs, routedTxs...)
	instrumentIDs = append(instrumentIDs, routedIDs...)
	txWeights = append(txWeights, routedWeights...)
	// A routed posting is denominated the way the leg it negates is, which is
	// what a nil entry says: the store leaves it to the insert trigger, which
	// takes the posting's own date, and a routed leg clones the first leg's
	// timestamp.
	for len(basis) > 0 && len(basis) < len(txs) {
		basis = append(basis, nil)
	}

	if replacing {
		err = deps.DB.ReplaceTxsInPeriod(ctx, p.UserID, p.Broker, p.JobID, p.PeriodFrom, p.PeriodBefore, txs, instrumentIDs, txWeights, basis)
	} else {
		err = deps.DB.CreateTxGroup(ctx, p.UserID, p.Broker, txs[0].GetAccount(), p.JobID, txs, instrumentIDs, txWeights, basis)
	}
	if err != nil {
		rep.Errf(-1, "txs", err.Error())
		return out, fmt.Errorf("store postings: %w", err)
	}
	out.Stored = len(txs)
	out.InstrumentIDs = instrumentIDs
	return out, nil
}

// filterBasis narrows a per-row basis slice to the rows that survived
// filtering, so it stays parallel to the postings actually being stored.
func filterBasis(basis []*time.Time, filteredIdx []int) []*time.Time {
	if len(basis) == 0 {
		return nil
	}
	out := make([]*time.Time, len(filteredIdx))
	for i, f := range filteredIdx {
		if f < len(basis) {
			out[i] = basis[f]
		}
	}
	return out
}
