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
	callID := tel.WriteIdentifierPluginCall(ctx, db.TelemetryIdentifierPluginCall{
		RunID:     runID,
		AttemptID: attemptID,
		PluginID:  "openfigi",
		Outcome:   db.TelemetryPluginCallWon,
		Retries:   1,
		Duration:  1500 * time.Millisecond,
	})
	if callID == "" {
		t.Fatal("WriteIdentifierPluginCall returned no id")
	}
	// What this call claimed: a FIGI it returned, and the ISIN it was filtered
	// on and deliberately did not echo back.
	tel.WriteIdentifierClaim(ctx, db.TelemetryIdentifierClaim{
		RunID: runID, CallID: callID,
		Type: "OPENFIGI_SHARE_CLASS", Value: "BBG001S5N8V8", Role: db.ClaimRoleReturned,
	})
	tel.WriteIdentifierClaim(ctx, db.TelemetryIdentifierClaim{
		RunID: runID, CallID: callID,
		Type: "ISIN", Value: "US0378331005", Role: db.ClaimRoleFiltered,
	})
	tel.EndResolutionKey(ctx, keyID, db.TelemetryResolutionKeyOutcome{
		RunID:            runID,
		CandidateOutcome: db.TelemetryCandidateFieldsProposed,
		Outcome:          db.TelemetryResolutionIdentified,
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

	// The claims under one call are what that call said in one answer, so the
	// view has to keep them reachable by call_id rather than only by run. A
	// filtered row sits beside a returned one and is graded with it: the ISIN
	// never came back in the payload, and the association still holds.
	rows, err := tel.db.Query(`
		SELECT identifier_type, value, role, plugin_id, key_description, is_import
		FROM telemetry.v_identifier_claim WHERE call_id = $1::uuid
		ORDER BY role
	`, callID)
	if err != nil {
		t.Fatalf("read v_identifier_claim: %v", err)
	}
	defer rows.Close()
	type claimRow struct {
		typ, value, role, plugin, description string
		isImport                              bool
	}
	var claims []claimRow
	for rows.Next() {
		var c claimRow
		if err := rows.Scan(&c.typ, &c.value, &c.role, &c.plugin, &c.description, &c.isImport); err != nil {
			t.Fatalf("scan claim: %v", err)
		}
		claims = append(claims, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("claim rows: %v", err)
	}
	want := []claimRow{
		{"ISIN", "US0378331005", db.ClaimRoleFiltered, "openfigi", "APPLE INC COM", true},
		{"OPENFIGI_SHARE_CLASS", "BBG001S5N8V8", db.ClaimRoleReturned, "openfigi", "APPLE INC COM", true},
	}
	if len(claims) != len(want) {
		t.Fatalf("claims = %+v, want %+v", claims, want)
	}
	for i := range want {
		if claims[i] != want[i] {
			t.Errorf("claim %d = %+v, want %+v", i, claims[i], want[i])
		}
	}

	var extraction, resolution string
	if err := tel.db.QueryRow(`
		SELECT candidate_outcome, outcome FROM telemetry.v_resolution_key WHERE id = $1::uuid
	`, keyID).Scan(&extraction, &resolution); err != nil {
		t.Fatalf("read v_resolution_key: %v", err)
	}
	if extraction != db.TelemetryCandidateFieldsProposed || resolution != db.TelemetryResolutionIdentified {
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

	tel.WriteCandidatePluginCall(ctx, db.TelemetryCandidatePluginCall{
		RunID:          runID,
		PluginID:       "cash",
		BatchSize:      12,
		ItemsCompleted: 4,
		Outcome:        "hints_returned",
		Duration:       3 * time.Millisecond,
	})
	tel.WriteCandidatePluginCall(ctx, db.TelemetryCandidatePluginCall{
		RunID:          runID,
		PluginID:       "openai",
		BatchSize:      8,
		ItemsCompleted: 7,
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
				FROM telemetry.candidate_plugin_call WHERE run_id = $1::uuid AND plugin_id = $2
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

// TestWriteDescriptionPluginCallPrecedence pins the order the chain ran in
// surviving the write. Without it the rows carry batch sizes whose populations
// cannot be put back in sequence, and batch_size descending is only a guess at
// it -- two plugins handed equal batches order arbitrarily.
func TestWriteDescriptionPluginCallPrecedence(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunTxImport})

	// The narrowing a chain produces: the second plugin sees only what the first
	// failed on. Both are written with the precedence they ran at.
	tel.WriteCandidatePluginCall(ctx, db.TelemetryCandidatePluginCall{
		RunID:          runID,
		PluginID:       "cash",
		Precedence:     100,
		BatchSize:      40,
		ItemsCompleted: 28,
		Outcome:        "hints_returned",
		Duration:       time.Millisecond,
	})
	tel.WriteCandidatePluginCall(ctx, db.TelemetryCandidatePluginCall{
		RunID:          runID,
		PluginID:       "openai",
		Precedence:     50,
		BatchSize:      12,
		ItemsCompleted: 9,
		Outcome:        "hints_returned",
		Duration:       time.Second,
	})

	rows, err := tel.db.Query(`
		SELECT plugin_id, precedence, batch_size
		FROM telemetry.v_candidate_plugin_call
		WHERE run_id = $1::uuid
		ORDER BY precedence DESC
	`, runID)
	if err != nil {
		t.Fatalf("read v_description_plugin_call: %v", err)
	}
	defer rows.Close()

	type call struct {
		plugin     string
		precedence int
		batchSize  int
	}
	var got []call
	for rows.Next() {
		var c call
		if err := rows.Scan(&c.plugin, &c.precedence, &c.batchSize); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := []call{{"cash", 100, 40}, {"openai", 50, 12}}
	if len(got) != len(want) {
		t.Fatalf("rows = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestWritePriceGapNest walks the price grains end to end, so that a column
// renamed under the writer fails here rather than in front of someone reading a
// dashboard. The call view flattens both parents in, which is what lets one row
// answer what a plugin was asked, about which instrument, in which run.
func TestWritePriceGapNest(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()

	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunPriceFetchCycle})
	instID := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	gapID := tel.StartPriceGap(ctx, db.TelemetryPriceGap{
		RunID:           runID,
		InstrumentID:    instID,
		AssetClass:      "STOCK",
		Currency:        "USD",
		Exchange:        "XNAS",
		DaysOutstanding: 420,
	})
	if gapID == "" {
		t.Fatal("StartPriceGap returned no id")
	}
	took := 1500 * time.Millisecond
	tel.WritePricePluginCall(ctx, db.TelemetryPricePluginCall{
		RunID:      runID,
		GapID:      gapID,
		PluginID:   "eodhd",
		Precedence: 70,
		From:       time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Before:     time.Date(2024, 1, 11, 0, 0, 0, 0, time.UTC),
		Bars:       7,
		Outcome:    db.TelemetryPriceCallBarsReturned,
		Duration:   &took,
	})
	tel.EndPriceGap(ctx, runID, gapID, db.TelemetryGapFilled)
	tel.EndRun(ctx, runID, db.TelemetryOutcomeSuccess)

	var (
		pluginID, callOutcome, gapOutcome, runKind, assetClass, currency, exchange string
		precedence, days, bars, durationMS, daysOutstanding                        int
		transportFailed, writeFailed, gapSettled, isFX                             bool
	)
	err := tel.db.QueryRow(`
		SELECT plugin_id, precedence, days, bars, outcome, duration_ms,
		       transport_failed, write_failed,
		       gap_outcome, gap_settled, gap_is_fx, gap_asset_class, gap_currency,
		       gap_exchange, gap_days_outstanding, run_kind
		FROM telemetry.v_price_plugin_call WHERE run_id = $1::uuid
	`, runID).Scan(&pluginID, &precedence, &days, &bars, &callOutcome, &durationMS,
		&transportFailed, &writeFailed, &gapOutcome, &gapSettled, &isFX,
		&assetClass, &currency, &exchange, &daysOutstanding, &runKind)
	if err != nil {
		t.Fatalf("read v_price_plugin_call: %v", err)
	}
	if pluginID != "eodhd" || callOutcome != db.TelemetryPriceCallBarsReturned {
		t.Errorf("call = (%s, %s), want (eodhd, bars_returned)", pluginID, callOutcome)
	}
	if precedence != 70 || bars != 7 || durationMS != 1500 {
		t.Errorf("precedence, bars, duration_ms = (%d, %d, %d), want (70, 7, 1500)",
			precedence, bars, durationMS)
	}
	// Derived from the range rather than stored, so it cannot disagree with it.
	if days != 10 {
		t.Errorf("days = %d, want the 10 the range spans", days)
	}
	if transportFailed || writeFailed {
		t.Errorf("a call that returned bars is marked failed (transport=%v, write=%v)",
			transportFailed, writeFailed)
	}
	if gapOutcome != db.TelemetryGapFilled || !gapSettled || isFX {
		t.Errorf("gap = (%s, settled=%v, fx=%v), want (filled, true, false)",
			gapOutcome, gapSettled, isFX)
	}
	if assetClass != "STOCK" || currency != "USD" || exchange != "XNAS" {
		t.Errorf("gap attributes = (%s, %s, %s), want (STOCK, USD, XNAS)",
			assetClass, currency, exchange)
	}
	if daysOutstanding != 420 || runKind != db.TelemetryRunPriceFetchCycle {
		t.Errorf("gap days_outstanding, run kind = (%d, %s), want (420, price_fetch_cycle)",
			daysOutstanding, runKind)
	}

	// The instrument is readable through the label view rather than joined into
	// the grain views, and the gap holds the id that reaches it.
	var storedInst string
	if err := tel.db.QueryRow(
		`SELECT instrument_id::text FROM telemetry.v_price_gap WHERE id = $1::uuid`, gapID,
	).Scan(&storedInst); err != nil {
		t.Fatalf("read v_price_gap: %v", err)
	}
	if storedInst != instID {
		t.Errorf("instrument_id = %s, want %s", storedInst, instID)
	}
}

// TestWritePricePluginCallNullDuration pins the one outcome that made no call.
// Zero would average into a latency panel as a plugin answering instantly, which
// is the same reason a plugin costing no tokens writes null rather than zero.
func TestWritePricePluginCallNullDuration(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunPriceFetchCycle})
	gapID := tel.StartPriceGap(ctx, db.TelemetryPriceGap{
		RunID:           runID,
		InstrumentID:    "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		DaysOutstanding: 30,
	})
	tel.WritePricePluginCall(ctx, db.TelemetryPricePluginCall{
		RunID:    runID,
		GapID:    gapID,
		PluginID: "eodhd",
		From:     time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
		Before:   time.Date(2019, 2, 1, 0, 0, 0, 0, time.UTC),
		Outcome:  db.TelemetryPriceCallHistoryLimit,
	})

	var duration sql.NullInt64
	if err := tel.db.QueryRow(
		`SELECT duration_ms FROM telemetry.v_price_plugin_call WHERE run_id = $1::uuid`, runID,
	).Scan(&duration); err != nil {
		t.Fatalf("read v_price_plugin_call: %v", err)
	}
	if duration.Valid {
		t.Errorf("duration_ms = %d, want null for a call that never happened", duration.Int64)
	}
}

// TestUnstampedPriceGapIsNotSettled pins that a gap the cycle never reached does
// not read as dealt with. It must appear in the complement of settled, or a cycle
// killed part way would look like one that finished.
func TestUnstampedPriceGapIsNotSettled(t *testing.T) {
	tel := testTelemetry(t)
	ctx := context.Background()
	runID := tel.StartRun(ctx, db.TelemetryRun{Kind: db.TelemetryRunPriceFetchCycle})
	gapID := tel.StartPriceGap(ctx, db.TelemetryPriceGap{
		RunID:           runID,
		InstrumentID:    "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		DaysOutstanding: 30,
	})

	var settled, hadCall bool
	var outcome sql.NullString
	if err := tel.db.QueryRow(
		`SELECT outcome, settled, had_call FROM telemetry.v_price_gap WHERE id = $1::uuid`, gapID,
	).Scan(&outcome, &settled, &hadCall); err != nil {
		t.Fatalf("read v_price_gap: %v", err)
	}
	if outcome.Valid {
		t.Errorf("outcome = %q, want null on a gap that was never stamped", outcome.String)
	}
	if settled || hadCall {
		t.Errorf("settled, had_call = (%v, %v), want both false", settled, hadCall)
	}
}
