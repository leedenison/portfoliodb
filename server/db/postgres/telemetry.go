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
		SET extraction_outcome = $2, outcome = $3, mismatch_detected = $4, instrument_id = $5
		WHERE id = $1
	`, id, nullStr(o.ExtractionOutcome), nullStr(o.Outcome), o.MismatchDetected,
		t.optUUID(o.InstrumentID, "end resolution key")); err != nil {
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
func (t *Telemetry) WriteIdentifierPluginCall(ctx context.Context, c db.TelemetryIdentifierPluginCall) {
	attemptID, ok := t.parent(ctx, c.RunID, c.AttemptID, "write identifier plugin call")
	if !ok {
		return
	}
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO telemetry.identifier_plugin_call
			(identification_attempt_id, plugin_id, outcome, retries, duration_ms)
		VALUES ($1, $2, $3, $4, $5)
	`, attemptID, c.PluginID, c.Outcome, c.Retries, ms(c.Duration)); err != nil {
		t.fail(ctx, c.RunID, "write identifier plugin call", err)
	}
}

// WriteDescriptionPluginCall implements db.TelemetryDB.
func (t *Telemetry) WriteDescriptionPluginCall(ctx context.Context, c db.TelemetryDescriptionPluginCall) {
	runID, ok := t.parent(ctx, c.RunID, c.RunID, "write description plugin call")
	if !ok {
		return
	}
	var prompt, completion, total any
	if c.Tokens != nil {
		prompt, completion, total = c.Tokens.Prompt, c.Tokens.Completion, c.Tokens.Total
	}
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO telemetry.description_plugin_call
			(run_id, plugin_id, batch_size, items_with_hints, outcome,
			 prompt_tokens, completion_tokens, total_tokens, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, runID, c.PluginID, c.BatchSize, c.ItemsWithHints, c.Outcome,
		prompt, completion, total, ms(c.Duration)); err != nil {
		t.fail(ctx, c.RunID, "write description plugin call", err)
	}
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
