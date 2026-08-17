package postgres

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/leedenison/portfoliodb/server/db"
)

// The writer holds its own pool precisely so that it does not join a
// transaction, which is what makes the rollback-per-test harness unusable here:
// these tests commit. They clean up by truncating the run table instead, which
// cascades to everything under it.

// testTelemetry returns a writer over the test database, with the telemetry
// tables emptied when the test ends.
func testTelemetry(t *testing.T) *Telemetry {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run via make db-test)")
	}
	conn, err := sqlx.Open("postgres", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(`TRUNCATE telemetry.run CASCADE`); err != nil {
			t.Errorf("truncate: %v", err)
		}
		conn.Close()
	})
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	// Discard the log: the failure paths below are expected to log, and a test
	// that fails loudly on purpose should not fill the output with it.
	return NewTelemetry(conn, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1})))
}

// scanRun reads back the columns a run is stamped with.
func scanRun(t *testing.T, tel *Telemetry, runID string) (outcome sql.NullString, endedAt sql.NullTime, incomplete bool) {
	t.Helper()
	err := tel.db.QueryRow(
		`SELECT outcome, ended_at, telemetry_incomplete FROM telemetry.run WHERE id = $1::uuid`,
		runID,
	).Scan(&outcome, &endedAt, &incomplete)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	return outcome, endedAt, incomplete
}

// TestStartRunThenEndRun pins the run being written twice: created before its
// children, since they reference it, and stamped when the work ends. An unstamped
// run means genuinely in flight, which is what makes a stamped 'incomplete'
// readable as a run that died.
func TestStartRunThenEndRun(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()

	runID := tel.StartRun(ctx, db.TelemetryRun{
		Kind:   db.TelemetryRunTxImport,
		Broker: "FIDELITY",
		Source: "FIDELITY_CSV",
	})
	if runID == "" {
		t.Fatal("StartRun returned no id")
	}
	outcome, endedAt, incomplete := scanRun(t, tel, runID)
	if outcome.Valid {
		t.Errorf("a run in flight has outcome %q, want null", outcome.String)
	}
	if endedAt.Valid {
		t.Error("a run in flight has an ended_at")
	}
	if incomplete {
		t.Error("a run with no failed write is marked telemetry_incomplete")
	}

	tel.EndRun(ctx, runID, db.TelemetryOutcomeSuccess)
	outcome, endedAt, _ = scanRun(t, tel, runID)
	if outcome.String != db.TelemetryOutcomeSuccess {
		t.Errorf("outcome = %q, want %q", outcome.String, db.TelemetryOutcomeSuccess)
	}
	if !endedAt.Valid {
		t.Error("a stamped run has no ended_at")
	}
}

// TestWriteNest walks one resolution through the grains it produces and reads it
// back through the views, which is how a dashboard sees it.
func TestWriteNest(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()

	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport, Source: "FIDELITY_CSV"})
	keyID := tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
		RunID:            runID,
		Source:           "FIDELITY_CSV",
		Description:      "APPLE INC COM",
		TxCount:          300,
		SecurityTypeHint: "Stock",
		InstrumentKind:   "security",
	})
	if keyID == "" {
		t.Fatal("StartResolutionKey returned no id")
	}
	attemptID := tel.WriteIdentificationAttempt(ctx, db.TelemetryIdentificationAttempt{
		RunID:           runID,
		ResolutionKeyID: keyID,
		Purpose:         db.TelemetryPurposePrimary,
		Outcome:         db.TelemetryAttemptIdentified,
		AssetClass:      "STOCK",
	})
	if attemptID == "" {
		t.Fatal("WriteIdentificationAttempt returned no id")
	}
	tel.WriteIdentifierPluginCall(ctx, db.TelemetryIdentifierPluginCall{
		RunID:     runID,
		AttemptID: attemptID,
		PluginID:  "openfigi",
		Outcome:   db.TelemetryPluginCallWon,
		Retries:   1,
		Duration:  1500 * time.Millisecond,
	})
	tel.EndResolutionKey(ctx, keyID, db.TelemetryResolutionKeyOutcome{
		RunID:             runID,
		ExtractionOutcome: db.TelemetryExtractionHintsFound,
		Outcome:           db.TelemetryResolutionIdentified,
	})
	tel.EndRun(ctx, runID, db.TelemetryOutcomeSuccess)

	// The plugin-call view flattens all three parents in, so one row answers what
	// happened, under which attempt, for which description, in which run.
	var (
		pluginID, callOutcome, attemptPurpose, keyDescription, runKind string
		retries, durationMS, txCount                                   int
		reachedPlugins, isImport                                       bool
	)
	err := tel.db.QueryRow(`
		SELECT plugin_id, outcome, retries, duration_ms, attempt_purpose,
		       reached_plugins, key_description, key_tx_count, run_kind, is_import
		FROM telemetry.v_identifier_plugin_call WHERE run_id = $1::uuid
	`, runID).Scan(&pluginID, &callOutcome, &retries, &durationMS, &attemptPurpose,
		&reachedPlugins, &keyDescription, &txCount, &runKind, &isImport)
	if err != nil {
		t.Fatalf("read v_identifier_plugin_call: %v", err)
	}
	if pluginID != "openfigi" || callOutcome != db.TelemetryPluginCallWon {
		t.Errorf("plugin call = (%s, %s), want (openfigi, won)", pluginID, callOutcome)
	}
	if retries != 1 || durationMS != 1500 {
		t.Errorf("retries, duration_ms = (%d, %d), want (1, 1500)", retries, durationMS)
	}
	if attemptPurpose != db.TelemetryPurposePrimary || !reachedPlugins {
		t.Errorf("attempt = (%s, reached=%v), want (primary, reached=true)", attemptPurpose, reachedPlugins)
	}
	// tx_count is the fan-out, and is what tells a failure affecting 300 rows from
	// one affecting 1.
	if keyDescription != "APPLE INC COM" || txCount != 300 {
		t.Errorf("key = (%q, %d), want (APPLE INC COM, 300)", keyDescription, txCount)
	}
	if runKind != db.TelemetryRunTxImport || !isImport {
		t.Errorf("run = (%s, import=%v), want (tx_import, import=true)", runKind, isImport)
	}

	var extraction, resolution string
	if err := tel.db.QueryRow(`
		SELECT extraction_outcome, outcome FROM telemetry.v_resolution_key WHERE id = $1::uuid
	`, keyID).Scan(&extraction, &resolution); err != nil {
		t.Fatalf("read v_resolution_key: %v", err)
	}
	if extraction != db.TelemetryExtractionHintsFound || resolution != db.TelemetryResolutionIdentified {
		t.Errorf("key outcomes = (%s, %s), want (hints_found, identified)", extraction, resolution)
	}
}

// TestWriteDescriptionPluginCallTokens pins tokens being null rather than zero for
// a plugin that costs none. Zero would say a call was free; null says the question
// does not arise, and only one of those can be summed into the cost of an import.
func TestWriteDescriptionPluginCallTokens(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})

	tel.WriteDescriptionPluginCall(ctx, db.TelemetryDescriptionPluginCall{
		RunID:          runID,
		PluginID:       "cash",
		BatchSize:      12,
		ItemsWithHints: 4,
		Outcome:        "hints_returned",
		Duration:       3 * time.Millisecond,
	})
	tel.WriteDescriptionPluginCall(ctx, db.TelemetryDescriptionPluginCall{
		RunID:          runID,
		PluginID:       "openai",
		BatchSize:      8,
		ItemsWithHints: 7,
		Outcome:        "hints_returned",
		Tokens:         &db.TelemetryTokens{Prompt: 900, Completion: 120, Total: 1020},
		Duration:       2 * time.Second,
	})

	cases := []struct {
		plugin    string
		wantTotal sql.NullInt64
	}{
		{"cash", sql.NullInt64{}},
		{"openai", sql.NullInt64{Int64: 1020, Valid: true}},
	}
	for _, c := range cases {
		t.Run(c.plugin, func(t *testing.T) {
			var prompt, completion, total sql.NullInt64
			err := tel.db.QueryRow(`
				SELECT prompt_tokens, completion_tokens, total_tokens
				FROM telemetry.description_plugin_call WHERE run_id = $1::uuid AND plugin_id = $2
			`, runID, c.plugin).Scan(&prompt, &completion, &total)
			if err != nil {
				t.Fatalf("read description_plugin_call: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total_tokens = %+v, want %+v", total, c.wantTotal)
			}
			if prompt.Valid != c.wantTotal.Valid || completion.Valid != c.wantTotal.Valid {
				t.Errorf("token columns disagree about being set: prompt=%+v completion=%+v", prompt, completion)
			}
		})
	}
}

// TestFailedWriteMarksTheRun pins the whole of what a failed write does: it marks
// the run so a panel says the counts understate, and it does not fail the work.
// A value outside a vocabulary is the failure used here because it is the one a
// bug would actually produce.
func TestFailedWriteMarksTheRun(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})
	keyID := tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
		RunID: runID, Source: "FIDELITY_CSV", Description: "APPLE INC COM", TxCount: 1,
	})

	attemptID := tel.WriteIdentificationAttempt(ctx, db.TelemetryIdentificationAttempt{
		RunID:           runID,
		ResolutionKeyID: keyID,
		Purpose:         "cache_hit",
		Outcome:         db.TelemetryAttemptIdentified,
	})
	if attemptID != "" {
		t.Errorf("a rejected write returned an id: %q", attemptID)
	}
	if _, _, incomplete := scanRun(t, tel, runID); !incomplete {
		t.Error("a failed write left the run unmarked")
	}

	// The subtree below a failed write is skipped rather than attempted, so one
	// failure costs its own children and nothing else.
	tel.WriteIdentifierPluginCall(ctx, db.TelemetryIdentifierPluginCall{
		RunID:     runID,
		AttemptID: attemptID,
		PluginID:  "openfigi",
		Outcome:   db.TelemetryPluginCallWon,
	})
	var calls int
	if err := tel.db.QueryRow(
		`SELECT count(*) FROM telemetry.identifier_plugin_call`,
	).Scan(&calls); err != nil {
		t.Fatalf("count identifier_plugin_call: %v", err)
	}
	if calls != 0 {
		t.Errorf("identifier_plugin_call rows = %d, want 0", calls)
	}
}

// TestWriteUnderAMissingParentMarksTheRun covers the other way a parent can be
// unusable: an id that is well formed but names nothing, which the foreign key
// rejects.
func TestWriteUnderAMissingParentMarksTheRun(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})

	id := tel.WriteIdentificationAttempt(ctx, db.TelemetryIdentificationAttempt{
		RunID:           runID,
		ResolutionKeyID: "6f1a3c2e-0000-4000-8000-000000000000",
		Purpose:         db.TelemetryPurposePrimary,
		Outcome:         db.TelemetryAttemptIdentified,
	})
	if id != "" {
		t.Errorf("a write under a missing parent returned an id: %q", id)
	}
	if _, _, incomplete := scanRun(t, tel, runID); !incomplete {
		t.Error("a failed write left the run unmarked")
	}
}

// TestPurgeRunsBefore pins retention being a delete over started_at that cascades,
// so nothing has to know the table list, and a run inside the window keeps
// everything under it.
// TestSweepIncompleteRuns pins what makes a null outcome readable. A run left
// unstamped by a process that died is indistinguishable from one running now
// until the sweep at the next startup stamps it, and a run that already reached a
// terminal outcome must not be restamped by it.
func TestSweepIncompleteRuns(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()

	died := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})
	finished := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunGroupingCycle})
	tel.EndRun(ctx, finished, db.TelemetryOutcomeSuccess)

	swept, err := tel.SweepIncompleteRuns(ctx)
	if err != nil {
		t.Fatalf("SweepIncompleteRuns: %v", err)
	}
	if swept != 1 {
		t.Errorf("swept = %d, want 1", swept)
	}

	outcome, endedAt, _ := scanRun(t, tel, died)
	if outcome.String != db.TelemetryOutcomeIncomplete {
		t.Errorf("outcome of a run that died = %q, want %q", outcome.String, db.TelemetryOutcomeIncomplete)
	}
	// The run ended when its process died, which nothing recorded. Stamping now()
	// would date it to this startup and give the view a duration measuring how
	// long the service was down.
	if endedAt.Valid {
		t.Error("the sweep stamped an ended_at it cannot know")
	}

	outcome, _, _ = scanRun(t, tel, finished)
	if outcome.String != db.TelemetryOutcomeSuccess {
		t.Errorf("the sweep restamped a finished run as %q", outcome.String)
	}
}

func TestPurgeRunsBefore(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-db.TelemetryRetention)

	oldRun := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})
	if _, err := tel.db.Exec(
		`UPDATE telemetry.run SET started_at = $2 WHERE id = $1::uuid`,
		oldRun, cutoff.Add(-24*time.Hour),
	); err != nil {
		t.Fatalf("age the run: %v", err)
	}
	oldKey := tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
		RunID: oldRun, Source: "FIDELITY_CSV", Description: "APPLE INC COM", TxCount: 1,
	})
	tel.WriteIdentificationAttempt(ctx, db.TelemetryIdentificationAttempt{
		RunID:           oldRun,
		ResolutionKeyID: oldKey,
		Purpose:         db.TelemetryPurposePrimary,
		Outcome:         db.TelemetryAttemptIdentified,
	})
	newRun := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})
	tel.StartResolutionKey(ctx, db.TelemetryResolutionKey{
		RunID: newRun, Source: "FIDELITY_CSV", Description: "MSFT", TxCount: 1,
	})

	deleted, err := tel.PurgeRunsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeRunsBefore: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	for _, c := range []struct {
		table string
		want  int
	}{
		{"telemetry.run", 1},
		{"telemetry.resolution_key", 1},
		{"telemetry.identification_attempt", 0},
	} {
		var n int
		if err := tel.db.QueryRow(`SELECT count(*) FROM ` + c.table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", c.table, err)
		}
		if n != c.want {
			t.Errorf("%s rows = %d, want %d", c.table, n, c.want)
		}
	}
}
