package ingestion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/archiveimport"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/identifier/candidate"
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
	DB                db.DB
	Registry          *identifier.Registry
	CandidateRegistry *candidate.Registry
	// Telemetry and RunID are the run this batch is part of. Either left unset
	// records nothing: a batch outside a run has nothing to hang its event rows
	// off, and the writer would reject them.
	Telemetry db.TelemetryDB
	RunID     string
	// RunKind is what kind of import this batch belongs to, and it decides more
	// than how the run is filed: see completesPartialIdentity.
	RunKind string
}

// completesPartialIdentity reports whether this batch may pay a candidate plugin
// to fill a gap in an identity its source only partly stated.
//
// Only a broker upload may. An archive names one identifier per posting, chosen
// out of an identity the exporting instance had already resolved -- a pointer to
// that instrument rather than everything the source knew about it. Judging that
// pointer incomplete and paying a plugin to complete it mistakes a reference for
// a description, and what came back would be tested against a stated identifier
// that was never partial in the first place.
//
// This bounds completion, not the stage: a posting an archive names no
// identifier for is one the exporting instance never resolved either, and it
// reaches the candidate plugins on its description exactly as it always has.
func (d ingestDeps) completesPartialIdentity() bool {
	return d.RunKind == db.TelemetryRunTxImport
}

// writeCandidatePluginCall records one ProposeBatch invocation against the run,
// and does nothing when the batch is not part of one.
func (d ingestDeps) writeCandidatePluginCall(ctx context.Context, c db.TelemetryCandidatePluginCall) string {
	if d.Telemetry == nil || d.RunID == "" {
		return ""
	}
	return d.Telemetry.WriteCandidatePluginCall(ctx, c)
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
	// ExportedAt is the point in market time this batch's identifiers are stated
	// as of: one value for the whole batch, because a file has one export rather
	// than one per row. nil leaves resolution to treat the hints as current, which
	// is what a vintage of now computes to anyway.
	ExportedAt *time.Time
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
	// InstrumentIDs is every instrument the batch stored a posting against, so a
	// caller can recompute split adjustments once for several batches rather than
	// once per batch.
	//
	// The instruments of the counterparties the store routes are not among them
	// and need not be: a money residual resolves to a currency, which has no
	// splits, and one in a security carries the instrument of a leg already here.
	InstrumentIDs []string
}

// vintage reads a declared knowledge time into the form the resolver takes.
// Absent stays absent: a document or an upload that states nothing has its hints
// treated as current, and there is no date to invent for it.
func vintage(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
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
	txs := p.Txs
	replacing := p.PeriodFrom != nil && p.PeriodBefore != nil
	// A batch handed no postings at all, over a period, is an instruction to
	// clear that period -- which is what an archive window holding no groups
	// says, and the reason a window states its period rather than having one
	// inferred from its groups.
	clearing := replacing && len(txs) == 0
	if len(txs) == 0 && !clearing {
		return out, nil
	}
	if !p.TotalDeclared {
		rep.Total(ctx, len(txs))
	}
	rowIdx := make([]int, len(txs))
	for i := range txs {
		rowIdx[i] = i
		if p.RowIndices != nil && i < len(p.RowIndices) {
			rowIdx[i] = p.RowIndices[i]
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

	// Filtered once and shared by both passes, because filtering logs what it
	// discards and deriving the same list twice would say it twice.
	txHints := make([][]identifier.Identifier, len(txs))
	for i, tx := range txs {
		txHints[i] = identifierHintsFromTx(ctx, tx)
	}
	pre, err := proposeCandidates(ctx, deps, p.Source, p.Broker, txs, txHints)
	if err != nil {
		rep.Errf(-1, "txs", err.Error())
		return out, fmt.Errorf("propose candidates: %w", err)
	}
	instrumentIDs, idErrs, err := resolveInstruments(ctx, deps, p.Broker, p.Source, txs, rowIdx, txHints, pre, p.ExportedAt, rep)
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
	// explicit counterparty. This runs after resolution, because telling a
	// currency commodity from a security one is a property of the instrument.
	balanceInsts := balanceInstruments(instByID)
	// The neutrality check needs the contract size, which is why it runs here
	// rather than with the shape checks in ValidateTxs.
	if wErrs := validateWeightNeutrality(txs, rowIdx, instrumentIDs, balanceInsts); len(wErrs) > 0 {
		rep.Errs(wErrs)
		return out, errBatchRejected
	}
	// What each posting contributes to its group's balance, stored beside it. The
	// counterparties that make the group sum to zero are the store's: it settles
	// each group from these weights once the postings are in, inside the same
	// transaction, so a group is never observed unbalanced and an upload cannot
	// disagree with a replace about what a group owes.
	txWeights := weights(txs, instrumentIDs, balanceInsts)

	if replacing {
		err = deps.DB.ReplaceTxsInPeriod(ctx, p.UserID, p.Broker, p.JobID, p.PeriodFrom, p.PeriodBefore, txs, instrumentIDs, txWeights, p.ShareCountBasis)
	} else {
		err = deps.DB.CreateTxGroup(ctx, p.UserID, p.Broker, txs[0].GetAccount(), p.JobID, txs, instrumentIDs, txWeights, p.ShareCountBasis)
	}
	if err != nil {
		rep.Errf(-1, "txs", err.Error())
		return out, fmt.Errorf("store postings: %w", err)
	}
	out.Stored = len(txs)
	out.InstrumentIDs = instrumentIDs
	return out, nil
}
