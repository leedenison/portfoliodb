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
	// extraction is stage 1, decided in the pre-pass before any key resolves and
	// held until the key is stamped, because one row carries both stages.
	extraction map[string]string
	// mismatched is the MIC_TICKER against OPENFIGI_SHARE_CLASS disagreement,
	// detected between the two stages and likewise held.
	mismatched map[string]bool
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
// extraction is keyed by cacheKey(source, description) -- no hints -- because that
// is the grain the extraction pre-pass dedupes on. The two notions of distinct
// coincide exactly when no posting carries a client hint, which is the only case
// extraction runs in: a key that carries hints is never extracted, and is recorded
// as not attempted for that reason.
func newResolutionKeys(ctx context.Context, tel db.TelemetryDB, runID, source string, txs []*apiv1.Tx, hints [][]identifier.Identifier, extraction map[string]string) *resolutionKeys {
	if tel == nil || runID == "" {
		return nil
	}
	k := &resolutionKeys{
		tel:        tel,
		runID:      runID,
		ids:        make(map[string]string),
		extraction: make(map[string]string),
		mismatched: make(map[string]bool),
		hintDiffs:  make(map[string]string),
		stamped:    make(map[string]bool),
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
		ex := db.TelemetryExtractionNotAttemptedHintsSupplied
		if !s.hasHints {
			ex = extraction[cacheKey(source, s.desc)]
		}
		k.extraction[key] = ex
		k.ids[key] = tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
			RunID:              runID,
			Source:             source,
			Description:        s.desc,
			TxCount:            s.txCount,
			HadIdentifierHints: s.hasHints,
			SecurityTypeHint:   s.txHints.SecurityTypeHint,
			InstrumentKind:     s.txHints.InstrumentKind,
		})
	}
	return k
}

// mismatch records that MIC_TICKER and OPENFIGI_SHARE_CLASS resolved differently
// for this key. It is a flag rather than an outcome because resolution continues
// and succeeds using MIC_TICKER.
func (k *resolutionKeys) mismatch(key string) {
	if k == nil {
		return
	}
	k.mismatched[key] = true
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
		RunID:             k.runID,
		ExtractionOutcome: k.extraction[key],
		Outcome:           outcome,
		MismatchDetected:  k.mismatched[key],
		HintDiffs:         k.hintDiffs[key],
		InstrumentID:      instrumentID,
	})
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
	case r.HadTimeout:
		return db.TelemetryResolutionPluginTimeout
	case r.HadError:
		return db.TelemetryResolutionPluginUnavailable
	}
	return db.TelemetryResolutionBrokerDescriptionOnly
}

// identifierRef is an instrument as an archive names it: an identifier and
// nothing else. It is the grain the price and corporate event parts resolve and
// cache on, and the grain their resolution keys are written at.
type identifierRef struct {
	Type   string
	Domain string
	Value  string
}

// cacheKey is what the per-archive resolve cache keys on, so the cache and the
// telemetry ledger cannot disagree about what counts as the same instrument.
func (r identifierRef) cacheKey() string {
	return r.Type + "\x00" + r.Domain + "\x00" + r.Value
}

// description names the key in the absence of a broker description, which is what
// these parts resolve without.
func (r identifierRef) description() string {
	return r.Type + ":" + r.Domain + ":" + r.Value
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
func newIdentifierResolutionKeys(ctx context.Context, tel db.TelemetryDB, runID string, refs []identifierRef) *resolutionKeys {
	if tel == nil || runID == "" {
		return nil
	}
	k := &resolutionKeys{
		tel:        tel,
		runID:      runID,
		ids:        make(map[string]string),
		extraction: make(map[string]string),
		mismatched: make(map[string]bool),
		hintDiffs:  make(map[string]string),
		stamped:    make(map[string]bool),
	}
	var order []identifierRef
	counts := make(map[string]int)
	for _, r := range refs {
		key := r.cacheKey()
		if counts[key] == 0 {
			order = append(order, r)
		}
		counts[key]++
	}
	for _, r := range order {
		key := r.cacheKey()
		k.extraction[key] = db.TelemetryExtractionNotAttemptedHintsSupplied
		k.ids[key] = tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
			RunID:              runID,
			Description:        r.description(),
			TxCount:            counts[key],
			HadIdentifierHints: true,
		})
	}
	return k
}
