package ingestion

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
)

// resolutionKeys owns the telemetry rows for the distinct resolution keys of one
// batch, and for the identifiers of one archive.
//
// A resolution key is not a transaction: many transactions share one and resolve
// once. The fan-out is therefore counted here, over the whole batch, before
// anything resolves. Resolve only ever sees the first transaction of each key --
// the rest are served from the batch cache -- so counting as it went would record
// 1 against every key and lose the difference between a failure affecting 300
// rows and one affecting 1.
//
// A nil *resolutionKeys records nothing and is safe to call, which is what a
// caller with no telemetry writer passes.
type resolutionKeys struct {
	tel   db.TelemetryDB
	runID string
	// ids maps a resolution key -- what cacheKeyWithHints names -- to the row it
	// was written as. An id missing or empty is a write that failed, and the
	// writer already skips the children of one, so nothing here tests for it.
	ids map[string]string
	// candidate is stage 1, decided in the pre-pass before any key resolves and
	// held until the key is stamped, because one row carries both stages.
	candidate map[string]string
	// proposed is what a candidate plugin offered for a key, held for the same
	// reason and written as child rows when the key is stamped. It cannot be
	// written when the proposal is made: the pre-pass runs before this ledger
	// exists, so the key row a field must name is not there yet.
	proposed map[string]keyProposals
	// fieldOutcomes is what the resolution established about each of those, in
	// the order they were proposed.
	fieldOutcomes map[string][]identification.ProposalOutcome
	// mismatched names the probe that disagreed, detected between the two stages
	// and likewise held. A name rather than a flag: more than one probe can
	// disagree about a key, and two findings sharing one boolean cannot be told
	// apart afterwards.
	mismatched map[string]string
	// hintDiffs is what the resolved instrument contradicted about its hints.
	// Recorded by the primary resolution and held until the key is stamped, the
	// same way, because the resolution that decides the key is the one whose
	// contradictions belong on it -- a mismatch-check probe answers a different
	// question and its diffs would overwrite the answer with an aside.
	hintDiffs map[string]string
	stamped   map[string]bool
}

// newResolutionKeys writes a key row per distinct (source, description, hints)
// triple over txs, and returns the ledger that stamps them.
//
// hints is parallel to txs, so the caller's filtered hint lists are reused rather
// than derived a second time -- deriving them again would repeat the discard
// logging that filtering does.
//
// outcome is keyed the same way, by cacheKeyWithHints, because the pre-pass now
// dedupes at the resolution key's own grain. It used to dedupe on (source,
// description) alone and this re-derived that key to read it, which held only
// because the two coincide when no posting carries a hint. They no longer have
// to: a hinted key is answered by the pre-pass too, and says so itself.
func newResolutionKeys(ctx context.Context, tel db.TelemetryDB, runID, source string, txs []*apiv1.Tx, hints [][]identifier.Identifier, pre prePass) *resolutionKeys {
	outcome := pre.outcome
	if tel == nil || runID == "" {
		return nil
	}
	k := &resolutionKeys{
		tel:           tel,
		runID:         runID,
		ids:           make(map[string]string),
		candidate:     make(map[string]string),
		proposed:      make(map[string]keyProposals),
		fieldOutcomes: make(map[string][]identification.ProposalOutcome),
		mismatched:    make(map[string]string),
		hintDiffs:     make(map[string]string),
		stamped:       make(map[string]bool),
	}
	type seed struct {
		desc     string
		hasHints bool
		txHints  identifier.Hints
		txCount  int
	}
	var order []string
	seeds := make(map[string]*seed)
	for i, tx := range txs {
		desc := tx.GetInstrumentDescription()
		key := cacheKeyWithHints(source, desc, hints[i])
		s := seeds[key]
		if s == nil {
			s = &seed{desc: desc, hasHints: len(hints[i]) > 0, txHints: HintsFromTx(tx)}
			seeds[key] = s
			order = append(order, key)
		}
		s.txCount++
	}
	for _, key := range order {
		s := seeds[key]
		k.candidate[key] = outcome[key]
		if kp, ok := pre.proposed[key]; ok {
			k.proposed[key] = kp
		}
		k.ids[key] = tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
			RunID:              runID,
			Source:             source,
			Description:        s.desc,
			TxCount:            s.txCount,
			HadIdentifierHints: s.hasHints,
			SecurityTypeHint:   s.txHints.SecurityTypeHint,
		})
	}
	return k
}

// mismatch records that a probe found two ways of naming this key's instrument
// disagreeing, and which probe it was. It is not an outcome because resolution
// continues and succeeds -- for figi_vs_ticker, using MIC_TICKER.
//
// The first finding stands. A second would be a different probe disagreeing
// about the same key, and overwriting would report whichever ran last rather
// than the one that changed what resolution did.
func (k *resolutionKeys) mismatch(key, which string) {
	if k == nil || which == "" || k.mismatched[key] != "" {
		return
	}
	k.mismatched[key] = which
}

// hintDiff records what the instrument this key resolved to contradicted about
// the hints it was given. Empty summaries are dropped rather than stored, so the
// column reads as "contradicted nothing" rather than "was not looked at".
func (k *resolutionKeys) hintDiff(key, summary string) {
	if k == nil || summary == "" {
		return
	}
	k.hintDiffs[key] = summary
}

// fields records what the resolution established about each identifier proposed
// for this key. Held rather than written, like the hint diffs, because the row
// the child records hang off is stamped once and the first stamp is the one that
// stands.
func (k *resolutionKeys) fields(key string, outcomes []identification.ProposalOutcome) {
	if k == nil || len(outcomes) == 0 || k.fieldOutcomes[key] != nil {
		return
	}
	k.fieldOutcomes[key] = outcomes
}

// writeFields emits one row per field proposed for this key. It runs after the
// key is stamped, because the child names the key.
//
// A proposal whose verdict the resolution never reported is written as unused:
// the field was paid for and no resolution consulted it, which is exactly what
// the member is for and is not the same as having nothing to say about it.
func (k *resolutionKeys) writeFields(ctx context.Context, key string) {
	kp, ok := k.proposed[key]
	if !ok || kp.CallID == "" {
		return
	}
	byIdentifier := make(map[identifier.Identifier]string, len(k.fieldOutcomes[key]))
	for _, o := range k.fieldOutcomes[key] {
		byIdentifier[o.Identifier] = o.Outcome
	}
	for _, p := range kp.Proposals {
		outcome := byIdentifier[p.Identifier]
		if outcome == "" {
			outcome = db.TelemetryCandidateFieldUnused
		}
		var confidence *float64
		if p.Confidence > 0 {
			c := p.Confidence
			confidence = &c
		}
		k.tel.WriteCandidateField(ctx, db.TelemetryCandidateField{
			RunID:           k.runID,
			ResolutionKeyID: k.ids[key],
			CallID:          kp.CallID,
			Field:           p.Field,
			Value:           p.Identifier.Value,
			Confidence:      confidence,
			Outcome:         outcome,
		})
	}
}

// end stamps a key with what became of it, filling stage 1 in from what the
// pre-pass recorded.
//
// The first stamp wins. A key resolves once and every later transaction sharing
// it is answered from the batch cache, so a second call is the same key being
// read rather than a second resolution.
func (k *resolutionKeys) end(ctx context.Context, key, outcome, instrumentID string) {
	if k == nil || k.stamped[key] {
		return
	}
	k.stamped[key] = true
	k.tel.EndResolutionKey(ctx, k.ids[key], db.TelemetryResolutionKeyOutcome{
		RunID:            k.runID,
		CandidateOutcome: k.candidate[key],
		Outcome:          outcome,
		MismatchDetected: k.mismatched[key],
		HintDiffs:        k.hintDiffs[key],
		InstrumentID:     instrumentID,
	})
	k.writeFields(ctx, key)
}

// attempt returns the scope one ResolveWithPlugins call over this key records
// itself under. A caller with no ledger gets the zero Attempt, which records
// nothing.
func (k *resolutionKeys) attempt(key, purpose string) identification.Attempt {
	if k == nil {
		return identification.Attempt{}
	}
	return identification.Attempt{
		DB:      k.tel,
		RunID:   k.runID,
		KeyID:   k.ids[key],
		Purpose: purpose,
	}
}

// resolutionOutcome names what became of a resolution that reached the identifier
// plugins. The three fallback members mirror the messages the resolver already
// records against a row, so a key's outcome and the row's message cannot disagree.
func resolutionOutcome(r identification.ResolveResult) string {
	switch {
	case r.Identified:
		return db.TelemetryResolutionIdentified
	case r.Unconfirmed:
		return db.TelemetryResolutionProposalUnconfirmed
	case r.HadTimeout:
		return db.TelemetryResolutionPluginTimeout
	case r.HadError:
		return db.TelemetryResolutionPluginUnavailable
	}
	return db.TelemetryResolutionBrokerDescriptionOnly
}

// newIdentifierResolutionKeys writes a key row per distinct identifier an archive
// names, and returns the ledger that stamps them.
//
// The price and corporate event parts identify instruments through the same
// resolver the transaction path uses, but from an identifier rather than from a
// broker description. They still need a key, because an identification attempt
// reaches its run through one. So the identifier names the key, tx_count carries
// the archive groups sharing it -- the fan-out this grain exists to record,
// whatever the things sharing it are called -- and source is empty, because an
// archive is not a broker export and names none.
//
// Extraction is recorded as not attempted because every one of these rows already
// names an identifier, which is the same reason a posting carrying one skips it.
func newIdentifierResolutionKeys(ctx context.Context, tel db.TelemetryDB, runID string, refs []identifier.Identifier) *resolutionKeys {
	if tel == nil || runID == "" {
		return nil
	}
	k := &resolutionKeys{
		tel:           tel,
		runID:         runID,
		ids:           make(map[string]string),
		candidate:     make(map[string]string),
		proposed:      make(map[string]keyProposals),
		fieldOutcomes: make(map[string][]identification.ProposalOutcome),
		mismatched:    make(map[string]string),
		hintDiffs:     make(map[string]string),
		stamped:       make(map[string]bool),
	}
	var order []identifier.Identifier
	counts := make(map[string]int)
	for _, r := range refs {
		key := r.Key()
		if counts[key] == 0 {
			order = append(order, r)
		}
		counts[key]++
	}
	for _, r := range order {
		key := r.Key()
		k.candidate[key] = db.TelemetryCandidateNotAttemptedRunKind
		k.ids[key] = tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
			RunID:              runID,
			Description:        r.String(),
			TxCount:            counts[key],
			HadIdentifierHints: true,
		})
	}
	return k
}
