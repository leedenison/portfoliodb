package postgres

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/leedenison/portfoliodb/server/db"
)

// Telemetry writes the telemetry schema.
//
// It takes *sqlx.DB rather than the queryable the rest of this package uses,
// because joining the work's transaction is the one thing it must not do: a failed
// import rolls back, and telemetry riding along would erase the diagnostics for
// the run most worth inspecting. The pool is its own for the same reason -- a
// connection already inside a transaction cannot write outside it.
type Telemetry struct {
	db  *sqlx.DB
	log *slog.Logger
}

// NewTelemetry returns a writer over its own pool.
func NewTelemetry(conn *sqlx.DB, log *slog.Logger) *Telemetry {
	if log == nil {
		log = slog.Default()
	}
	return &Telemetry{db: conn, log: log}
}

// Ensure Telemetry implements db.TelemetryDB.
var _ db.TelemetryDB = (*Telemetry)(nil)

// fail records that a telemetry write was lost. It logs, and marks the run so a
// panel can say its counts understate rather than reading them as a fall in
// traffic. The mark is itself best-effort: if the database is what failed, there
// is nowhere left to record that it did, and telemetry never fails the work.
func (t *Telemetry) fail(ctx context.Context, runID, op string, err error) {
	t.log.Error("telemetry write failed", "op", op, "run_id", runID, "error", err)
	id, parseErr := uuid.Parse(runID)
	if parseErr != nil {
		return
	}
	if _, markErr := t.db.ExecContext(ctx,
		`UPDATE telemetry.run SET telemetry_incomplete = TRUE WHERE id = $1`, id,
	); markErr != nil {
		t.log.Error("telemetry mark incomplete failed", "run_id", runID, "error", markErr)
	}
}

// parent returns the parsed id of a row's parent, and false when there is none to
// write under. An empty id is the previous write having failed, which is already
// logged and already marked, so the child is skipped silently rather than
// reported a second time.
func (t *Telemetry) parent(ctx context.Context, runID, parentID, op string) (uuid.UUID, bool) {
	if parentID == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parentID)
	if err != nil {
		t.fail(ctx, runID, op, err)
		return uuid.Nil, false
	}
	return id, true
}

// optUUID renders an optional id for storage, reusing parseNullUUID so that an
// absent one becomes NULL. An id that will not parse is a bug in the caller rather
// than a fact about the work, so it is logged and dropped to NULL: losing the
// pointer is better than losing the row that says what happened.
func (t *Telemetry) optUUID(s, op string) any {
	v, err := parseNullUUID(s)
	if err != nil {
		t.log.Error("telemetry ignored an unparseable id", "op", op, "value", s, "error", err)
		return nil
	}
	return v
}

// ms rounds a duration to whole milliseconds, the resolution the columns carry.
func ms(d time.Duration) int64 {
	return d.Milliseconds()
}

// StartRun implements db.TelemetryDB.
func (t *Telemetry) StartRun(ctx context.Context, r db.TelemetryRun) string {
	var id uuid.UUID
	err := t.db.QueryRowContext(ctx, `
		INSERT INTO telemetry.run (kind, job_id, user_id, broker, source)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, r.Kind, t.optUUID(r.JobID, "start run"), t.optUUID(r.UserID, "start run"),
		nullStr(r.Broker), nullStr(r.Source)).Scan(&id)
	if err != nil {
		// No run id to mark: the run is what failed to exist.
		t.log.Error("telemetry write failed", "op", "start run", "kind", r.Kind, "error", err)
		return ""
	}
	return id.String()
}

// EndRun implements db.TelemetryDB.
func (t *Telemetry) EndRun(ctx context.Context, runID, outcome string) {
	id, ok := t.parent(ctx, runID, runID, "end run")
	if !ok {
		return
	}
	if _, err := t.db.ExecContext(ctx, `
		UPDATE telemetry.run SET ended_at = now(), outcome = $2 WHERE id = $1
	`, id, outcome); err != nil {
		t.fail(ctx, runID, "end run", err)
	}
}

// StartResolutionKey implements db.TelemetryDB.
func (t *Telemetry) StartResolutionKey(ctx context.Context, k db.TelemetryResolutionKey) string {
	runID, ok := t.parent(ctx, k.RunID, k.RunID, "start resolution key")
	if !ok {
		return ""
	}
	var id uuid.UUID
	err := t.db.QueryRowContext(ctx, `
		INSERT INTO telemetry.resolution_key
			(run_id, source, description, tx_count, had_identifier_hints,
			 security_type_hint, instrument_kind)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, runID, k.Source, k.Description, k.TxCount, k.HadIdentifierHints,
		nullStr(k.SecurityTypeHint), nullStr(k.InstrumentKind)).Scan(&id)
	if err != nil {
		t.fail(ctx, k.RunID, "start resolution key", err)
		return ""
	}
	return id.String()
}

// EndResolutionKey implements db.TelemetryDB.
func (t *Telemetry) EndResolutionKey(ctx context.Context, keyID string, o db.TelemetryResolutionKeyOutcome) {
	id, ok := t.parent(ctx, o.RunID, keyID, "end resolution key")
	if !ok {
		return
	}
	if _, err := t.db.ExecContext(ctx, `
		UPDATE telemetry.resolution_key
		SET candidate_outcome = $2, outcome = $3, mismatch_detected = $4,
		    hint_diffs = $5, instrument_id = $6
		WHERE id = $1
	`, id, nullStr(o.CandidateOutcome), nullStr(o.Outcome), nullStr(o.MismatchDetected),
		nullStr(o.HintDiffs), t.optUUID(o.InstrumentID, "end resolution key")); err != nil {
		t.fail(ctx, o.RunID, "end resolution key", err)
	}
}

// WriteIdentificationAttempt implements db.TelemetryDB.
func (t *Telemetry) WriteIdentificationAttempt(ctx context.Context, a db.TelemetryIdentificationAttempt) string {
	keyID, ok := t.parent(ctx, a.RunID, a.ResolutionKeyID, "write identification attempt")
	if !ok {
		return ""
	}
	var id uuid.UUID
	err := t.db.QueryRowContext(ctx, `
		INSERT INTO telemetry.identification_attempt
			(resolution_key_id, purpose, depth, outcome, security_type_hint,
			 asset_class, had_identifier_hints)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, keyID, a.Purpose, a.Depth, a.Outcome, nullStr(a.SecurityTypeHint),
		nullStr(a.AssetClass), a.HadIdentifierHints).Scan(&id)
	if err != nil {
		t.fail(ctx, a.RunID, "write identification attempt", err)
		return ""
	}
	return id.String()
}

// WriteIdentifierPluginCall implements db.TelemetryDB.
func (t *Telemetry) WriteIdentifierPluginCall(ctx context.Context, c db.TelemetryIdentifierPluginCall) string {
	attemptID, ok := t.parent(ctx, c.RunID, c.AttemptID, "write identifier plugin call")
	if !ok {
		return ""
	}
	var id string
	if err := t.db.QueryRowContext(ctx, `
		INSERT INTO telemetry.identifier_plugin_call
			(identification_attempt_id, plugin_id, outcome, retries, duration_ms)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, attemptID, c.PluginID, c.Outcome, c.Retries, ms(c.Duration)).Scan(&id); err != nil {
		t.fail(ctx, c.RunID, "write identifier plugin call", err)
		return ""
	}
	return id
}

// WriteIdentifierClaim implements db.TelemetryDB. A claim whose call failed to
// write has nothing to hang off and is dropped rather than written against a
// null, exactly as a candidate field is.
func (t *Telemetry) WriteIdentifierClaim(ctx context.Context, c db.TelemetryIdentifierClaim) {
	callID, ok := t.parent(ctx, c.RunID, c.CallID, "write identifier claim")
	if !ok {
		return
	}
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO telemetry.identifier_claim
			(call_id, identifier_type, domain, value, role)
		VALUES ($1, $2, $3, $4, $5)
	`, callID, c.Type, nullStr(c.Domain), c.Value, c.Role); err != nil {
		t.fail(ctx, c.RunID, "write identifier claim", err)
	}
}

// WriteCandidatePluginCall implements db.TelemetryDB.
func (t *Telemetry) WriteCandidatePluginCall(ctx context.Context, c db.TelemetryCandidatePluginCall) string {
	runID, ok := t.parent(ctx, c.RunID, c.RunID, "write candidate plugin call")
	if !ok {
		return ""
	}
	var prompt, completion, total any
	if c.Tokens != nil {
		prompt, completion, total = c.Tokens.Prompt, c.Tokens.Completion, c.Tokens.Total
	}
	var id string
	if err := t.db.QueryRowContext(ctx, `
		INSERT INTO telemetry.candidate_plugin_call
			(run_id, plugin_id, precedence, batch_size, items_completed, fields_proposed,
			 outcome, prompt_tokens, completion_tokens, total_tokens, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`, runID, c.PluginID, c.Precedence, c.BatchSize, c.ItemsCompleted, c.FieldsProposed,
		c.Outcome, prompt, completion, total, ms(c.Duration)).Scan(&id); err != nil {
		t.fail(ctx, c.RunID, "write candidate plugin call", err)
		return ""
	}
	return id
}

// WriteCandidateField implements db.TelemetryDB. Both parents must exist: a field
// whose call or key failed to write has nothing to hang off, and is dropped rather
// than written against a null.
func (t *Telemetry) WriteCandidateField(ctx context.Context, f db.TelemetryCandidateField) {
	keyID, ok := t.parent(ctx, f.RunID, f.ResolutionKeyID, "write candidate field")
	if !ok {
		return
	}
	callID, ok := t.parent(ctx, f.RunID, f.CallID, "write candidate field")
	if !ok {
		return
	}
	var confidence any
	if f.Confidence != nil {
		confidence = *f.Confidence
	}
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO telemetry.candidate_field
			(resolution_key_id, call_id, field, value, confidence, outcome)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, keyID, callID, f.Field, f.Value, confidence, f.Outcome); err != nil {
		t.fail(ctx, f.RunID, "write candidate field", err)
	}
}

// SweepIncompleteRuns implements db.TelemetryDB.
//
// ended_at is deliberately left null. The run ended when its process died, which
// is a time nobody recorded; stamping now() would date it to this startup and
// give the view a duration measuring how long the service was down.
func (t *Telemetry) SweepIncompleteRuns(ctx context.Context) (int64, error) {
	res, err := t.db.ExecContext(ctx, `
		UPDATE telemetry.run SET outcome = $1 WHERE outcome IS NULL
	`, db.TelemetryOutcomeIncomplete)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeRunsBefore implements db.TelemetryDB.
func (t *Telemetry) PurgeRunsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := t.db.ExecContext(ctx,
		`DELETE FROM telemetry.run WHERE started_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StartPriceGap implements db.TelemetryDB.
func (t *Telemetry) StartPriceGap(ctx context.Context, g db.TelemetryPriceGap) string {
	runID, ok := t.parent(ctx, g.RunID, g.RunID, "start price gap")
	if !ok {
		return ""
	}
	var id uuid.UUID
	err := t.db.QueryRowContext(ctx, `
		INSERT INTO telemetry.price_gap
			(run_id, instrument_id, is_fx, asset_class, currency, exchange,
			 days_outstanding)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, runID, t.optUUID(g.InstrumentID, "start price gap"), g.IsFX,
		nullStr(g.AssetClass), nullStr(g.Currency), nullStr(g.Exchange),
		g.DaysOutstanding).Scan(&id)
	if err != nil {
		t.fail(ctx, g.RunID, "start price gap", err)
		return ""
	}
	return id.String()
}

// EndPriceGap implements db.TelemetryDB.
func (t *Telemetry) EndPriceGap(ctx context.Context, runID, gapID, outcome string) {
	id, ok := t.parent(ctx, runID, gapID, "end price gap")
	if !ok {
		return
	}
	if _, err := t.db.ExecContext(ctx, `
		UPDATE telemetry.price_gap SET outcome = $2 WHERE id = $1
	`, id, outcome); err != nil {
		t.fail(ctx, runID, "end price gap", err)
	}
}

// WritePricePluginCall implements db.TelemetryDB.
func (t *Telemetry) WritePricePluginCall(ctx context.Context, c db.TelemetryPricePluginCall) {
	gapID, ok := t.parent(ctx, c.RunID, c.GapID, "write price plugin call")
	if !ok {
		return
	}
	// Null rather than zero for a call that never happened, so a history-limited
	// range does not average into the latency panel as an instant answer.
	var duration any
	if c.Duration != nil {
		duration = ms(*c.Duration)
	}
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO telemetry.price_plugin_call
			(price_gap_id, plugin_id, precedence, range_from, range_before, bars,
			 outcome, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, gapID, c.PluginID, c.Precedence, c.From, c.Before, c.Bars, c.Outcome,
		duration); err != nil {
		t.fail(ctx, c.RunID, "write price plugin call", err)
	}
}
