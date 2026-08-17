package postgres

import (
	"context"
	"testing"
	"time"
)

// The telemetry schema carries its judgements in its views rather than in the
// panels reading them, so the views are the thing worth pinning: a panel saying
// "where is_import and reached_plugins" is only as good as the definitions behind
// those two columns, and neither is visible in a dashboard once it is written.
//
// These tests write the rows with raw SQL because there is no writer yet -- it
// arrives with the rest of 0115 -- and because what they are about is the schema,
// not the Go that will fill it.

// seedRun inserts a run and returns its id.
func seedRun(t *testing.T, p *Postgres, kind string, startedAt time.Time) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.run (kind, started_at) VALUES ($1, $2) RETURNING id
	`, kind, startedAt).Scan(&id)
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return id
}

// seedResolutionKey inserts a resolution key under a run and returns its id.
func seedResolutionKey(t *testing.T, p *Postgres, runID string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.resolution_key
			(run_id, source, description, tx_count, had_identifier_hints, outcome)
		VALUES ($1::uuid, 'FIDELITY_CSV', 'APPLE INC COM', 3, FALSE, 'identified')
		RETURNING id
	`, runID).Scan(&id)
	if err != nil {
		t.Fatalf("seed resolution key: %v", err)
	}
	return id
}

// seedAttempt inserts an identification attempt under a key and returns its id.
func seedAttempt(t *testing.T, p *Postgres, keyID, purpose, outcome string, depth int) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.identification_attempt
			(resolution_key_id, purpose, depth, outcome, had_identifier_hints)
		VALUES ($1::uuid, $2, $3, $4, FALSE)
		RETURNING id
	`, keyID, purpose, depth, outcome).Scan(&id)
	if err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	return id
}

// TestTelemetryViews_IsImport pins which run kinds a panel counts as an import.
// The three import kinds are the ones a person reads after uploading a file; the
// cycles are the ones that run on their own.
func TestTelemetryViews_IsImport(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	cases := []struct {
		kind     string
		isImport bool
	}{
		{"tx_import", true},
		{"user_archive_import", true},
		{"system_archive_import", true},
		{"grouping_cycle", false},
		{"transfer_match_cycle", false},
		{"corporate_event_cycle", false},
		{"price_fetch_cycle", false},
		{"inflation_cycle", false},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			id := seedRun(t, p, c.kind, time.Now())
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT is_import FROM telemetry.v_run WHERE id = $1::uuid`, id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_run: %v", err)
			}
			if got != c.isImport {
				t.Errorf("is_import for %s = %v, want %v", c.kind, got, c.isImport)
			}
		})
	}
}

// TestTelemetryViews_ReachedPlugins pins the denominator of the identification
// failure rate. An attempt that short-circuited in the database, or that had every
// plugin filtered out, never asked a plugin anything, and counting it would make
// the rate fall as the instrument table fills -- which reads as improving
// identification when nothing has changed.
func TestTelemetryViews_ReachedPlugins(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)

	cases := []struct {
		outcome string
		reached bool
	}{
		{"db_short_circuit", false},
		{"no_eligible_plugins", false},
		{"identified", true},
		{"not_identified", true},
		{"plugin_timeout", true},
		{"plugin_error", true},
	}
	for _, c := range cases {
		t.Run(c.outcome, func(t *testing.T) {
			id := seedAttempt(t, p, keyID, "primary", c.outcome, 0)
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT reached_plugins FROM telemetry.v_identification_attempt WHERE id = $1::uuid`, id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_identification_attempt: %v", err)
			}
			if got != c.reached {
				t.Errorf("reached_plugins for %s = %v, want %v", c.outcome, got, c.reached)
			}
		})
	}
}

// TestTelemetryViews_DoNotFanOut is the property that makes counting a view safe.
// A view spanning two grains would repeat its parent once per child, so a panel
// counting resolution keys would report four for a key that made four attempts.
// Every view here flattens its parents in and stops.
func TestTelemetryViews_DoNotFanOut(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)
	// One primary attempt plus the two the MIC_TICKER against OPENFIGI_SHARE_CLASS
	// mismatch check makes, which is the ordinary shape for a dual-hint description.
	seedAttempt(t, p, keyID, "primary", "identified", 0)
	seedAttempt(t, p, keyID, "mismatch_check", "identified", 0)
	seedAttempt(t, p, keyID, "mismatch_check", "identified", 0)

	var keys, runs int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM telemetry.v_resolution_key WHERE run_id = $1::uuid`, runID,
	).Scan(&keys); err != nil {
		t.Fatalf("count v_resolution_key: %v", err)
	}
	if keys != 1 {
		t.Errorf("v_resolution_key rows = %d, want 1", keys)
	}
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM telemetry.v_run WHERE id = $1::uuid`, runID,
	).Scan(&runs); err != nil {
		t.Fatalf("count v_run: %v", err)
	}
	if runs != 1 {
		t.Errorf("v_run rows = %d, want 1", runs)
	}
}

// TestTelemetryRetentionCascades pins retention being a delete over run.started_at
// and nothing else. Children cascade, so nothing has to know the table list, and a
// run inside the window keeps everything under it.
func TestTelemetryRetentionCascades(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	cutoff := time.Now().Add(-360 * 24 * time.Hour)

	oldRun := seedRun(t, p, "tx_import", cutoff.Add(-24*time.Hour))
	oldKey := seedResolutionKey(t, p, oldRun)
	oldAttempt := seedAttempt(t, p, oldKey, "primary", "identified", 0)
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO telemetry.identifier_plugin_call
			(identification_attempt_id, plugin_id, outcome, retries, duration_ms)
		VALUES ($1::uuid, 'openfigi', 'won', 0, 12)
	`, oldAttempt); err != nil {
		t.Fatalf("seed identifier plugin call: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO telemetry.description_plugin_call
			(run_id, plugin_id, precedence, batch_size, items_with_hints, outcome, duration_ms)
		VALUES ($1::uuid, 'openai', 100, 20, 18, 'hints_returned', 900)
	`, oldRun); err != nil {
		t.Fatalf("seed description plugin call: %v", err)
	}
	newRun := seedRun(t, p, "tx_import", cutoff.Add(24*time.Hour))
	seedResolutionKey(t, p, newRun)

	if _, err := p.q.ExecContext(ctx,
		`DELETE FROM telemetry.run WHERE started_at < $1`, cutoff,
	); err != nil {
		t.Fatalf("delete: %v", err)
	}

	for _, table := range []string{
		"telemetry.run",
		"telemetry.resolution_key",
		"telemetry.identification_attempt",
		"telemetry.identifier_plugin_call",
		"telemetry.description_plugin_call",
	} {
		var n int
		if err := p.q.QueryRowContext(ctx,
			`SELECT count(*) FROM `+table,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		want := 1
		if table == "telemetry.identification_attempt" ||
			table == "telemetry.identifier_plugin_call" ||
			table == "telemetry.description_plugin_call" {
			want = 0
		}
		if n != want {
			t.Errorf("%s rows after delete = %d, want %d", table, n, want)
		}
	}
}

// TestTelemetryReaderRoleIsSelectOnly pins what the dashboard's role can do. It is
// granted through a NOLOGIN group role, so this asks about the privileges rather
// than about a connection: the login that inherits them arrives with the Grafana
// container.
func TestTelemetryReaderRoleIsSelectOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	for _, rel := range []string{
		"telemetry.run",
		"telemetry.resolution_key",
		"telemetry.identification_attempt",
		"telemetry.identifier_plugin_call",
		"telemetry.description_plugin_call",
		"telemetry.v_run",
		"telemetry.v_resolution_key",
		"telemetry.v_identification_attempt",
		"telemetry.v_identifier_plugin_call",
		"telemetry.v_description_plugin_call",
		"telemetry.v_instrument_label",
	} {
		var canSelect, canInsert, canUpdate, canDelete bool
		err := p.q.QueryRowContext(ctx, `
			SELECT has_table_privilege('telemetry_reader', $1, 'SELECT'),
			       has_table_privilege('telemetry_reader', $1, 'INSERT'),
			       has_table_privilege('telemetry_reader', $1, 'UPDATE'),
			       has_table_privilege('telemetry_reader', $1, 'DELETE')
		`, rel).Scan(&canSelect, &canInsert, &canUpdate, &canDelete)
		if err != nil {
			t.Fatalf("privileges on %s: %v", rel, err)
		}
		if !canSelect {
			t.Errorf("telemetry_reader cannot SELECT %s", rel)
		}
		if canInsert || canUpdate || canDelete {
			t.Errorf("telemetry_reader can write %s: insert=%v update=%v delete=%v",
				rel, canInsert, canUpdate, canDelete)
		}
	}
}

// TestTelemetryVocabulariesAreClosed pins the rule the whole schema rests on: one
// row carries exactly one outcome drawn from a closed list. A value outside the
// list is rejected by the database rather than quietly charted as its own bucket.
//
// Each case takes its own transaction because a rejected statement aborts the one
// it ran in, and the next case would then fail for the wrong reason.
func TestTelemetryVocabulariesAreClosed(t *testing.T) {
	t.Run("run kind", func(t *testing.T) {
		p := testDBTx(t)
		// An RPC request is deliberately not a kind of run.
		if _, err := p.q.ExecContext(context.Background(),
			`INSERT INTO telemetry.run (kind) VALUES ('rpc_request')`,
		); err == nil {
			t.Error("run accepted a kind outside its vocabulary")
		}
	})

	t.Run("run outcome", func(t *testing.T) {
		p := testDBTx(t)
		runID := seedRun(t, p, "tx_import", time.Now())
		if _, err := p.q.ExecContext(context.Background(),
			`UPDATE telemetry.run SET outcome = 'cancelled' WHERE id = $1::uuid`, runID,
		); err == nil {
			t.Error("run accepted an outcome outside its vocabulary")
		}
	})

	t.Run("attempt purpose", func(t *testing.T) {
		p := testDBTx(t)
		keyID := seedResolutionKey(t, p, seedRun(t, p, "tx_import", time.Now()))
		// A cache hit is not an outcome anywhere in this schema: it is an artefact
		// of deduplication, and counting it would make the population transactions
		// rather than descriptions.
		if _, err := p.q.ExecContext(context.Background(), `
			INSERT INTO telemetry.identification_attempt
				(resolution_key_id, purpose, depth, outcome, had_identifier_hints)
			VALUES ($1::uuid, 'cache_hit', 0, 'identified', FALSE)
		`, keyID); err == nil {
			t.Error("identification attempt accepted a purpose outside its vocabulary")
		}
	})

	t.Run("plugin call outcome", func(t *testing.T) {
		p := testDBTx(t)
		keyID := seedResolutionKey(t, p, seedRun(t, p, "tx_import", time.Now()))
		attemptID := seedAttempt(t, p, keyID, "primary", "identified", 0)
		if _, err := p.q.ExecContext(context.Background(), `
			INSERT INTO telemetry.identifier_plugin_call
				(identification_attempt_id, plugin_id, outcome, retries, duration_ms)
			VALUES ($1::uuid, 'openfigi', 'cache_hit', 0, 12)
		`, attemptID); err == nil {
			t.Error("identifier plugin call accepted an outcome outside its vocabulary")
		}
	})
}

// seedKeyWithOutcome inserts a resolution key carrying a given stage 2 outcome,
// which may be empty for a key whose run died before it resolved.
func seedKeyWithOutcome(t *testing.T, p *Postgres, runID, outcome string, txCount int) string {
	t.Helper()
	var oc any
	if outcome != "" {
		oc = outcome
	}
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.resolution_key
			(run_id, source, description, tx_count, had_identifier_hints, outcome)
		VALUES ($1::uuid, 'FIDELITY_CSV', 'APPLE INC COM', $2, FALSE, $3)
		RETURNING id
	`, runID, txCount, oc).Scan(&id)
	if err != nil {
		t.Fatalf("seed key with outcome %q: %v", outcome, err)
	}
	return id
}

// seedIdentifierCall inserts an identifier plugin call under an attempt.
func seedIdentifierCall(t *testing.T, p *Postgres, attemptID, outcome string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.identifier_plugin_call
			(identification_attempt_id, plugin_id, outcome, retries, duration_ms)
		VALUES ($1::uuid, 'openfigi', $2, 0, 12)
		RETURNING id
	`, attemptID, outcome).Scan(&id)
	if err != nil {
		t.Fatalf("seed identifier call %q: %v", outcome, err)
	}
	return id
}

// seedDescriptionCall inserts a description plugin call under a run.
func seedDescriptionCall(t *testing.T, p *Postgres, runID, outcome string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.description_plugin_call
			(run_id, plugin_id, precedence, batch_size, items_with_hints, outcome, duration_ms)
		VALUES ($1::uuid, 'openai', 100, 10, 4, $2, 900)
		RETURNING id
	`, runID, outcome).Scan(&id)
	if err != nil {
		t.Fatalf("seed description call %q: %v", outcome, err)
	}
	return id
}

// TestTelemetryViews_Resolved pins which key outcomes count as having ended with
// an identifier. broker_description_only is the member worth staring at: an
// instrument exists, so instrument_id is not null, and nothing identified it.
// A panel listing what went wrong in an import has to include it, which is why
// this judgement cannot be replaced by a null check on instrument_id.
func TestTelemetryViews_Resolved(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())

	cases := []struct {
		outcome  string
		resolved bool
	}{
		{"db_source_description", true},
		{"db_identifier_hints", true},
		{"identified", true},
		{"broker_description_only", false},
		{"extraction_failed", false},
		{"plugin_timeout", false},
		{"plugin_unavailable", false},
		{"conflicting_hints", false},
		// Never stamped: the run died before the key resolved. Not resolved, and
		// not null either, or the row would drop out of both this column and its
		// negation and go unnoticed by every panel.
		{"", false},
	}
	for _, c := range cases {
		name := c.outcome
		if name == "" {
			name = "not_stamped"
		}
		t.Run(name, func(t *testing.T) {
			id := seedKeyWithOutcome(t, p, runID, c.outcome, 1)
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT resolved FROM telemetry.v_resolution_key WHERE id = $1::uuid`, id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_resolution_key: %v", err)
			}
			if got != c.resolved {
				t.Errorf("resolved for %q = %v, want %v", c.outcome, got, c.resolved)
			}
		})
	}
}

// TestTelemetryViews_HadAttempt separates a key that never asked a plugin
// anything from one that asked and was told nothing. Four of the five paths that
// stamp a key return before identification is reached, so no attempt is ordinary
// rather than a fault, and the two are the first thing to tell apart in a key
// that did not resolve.
func TestTelemetryViews_HadAttempt(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())

	without := seedKeyWithOutcome(t, p, runID, "db_source_description", 1)
	with := seedKeyWithOutcome(t, p, runID, "identified", 1)
	seedAttempt(t, p, with, "primary", "identified", 0)

	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"no attempt", without, false},
		{"one attempt", with, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT had_attempt FROM telemetry.v_resolution_key WHERE id = $1::uuid`, c.id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_resolution_key: %v", err)
			}
			if got != c.want {
				t.Errorf("had_attempt = %v, want %v", got, c.want)
			}
		})
	}
}

// TestTelemetryViews_TransportFailed pins the line between the API breaking and
// the API not knowing. not_identified is a completed call with an empty answer
// and must stay out of it, or a plugin that works and finds nothing reads as a
// plugin that is down.
func TestTelemetryViews_TransportFailed(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)
	attemptID := seedAttempt(t, p, keyID, "primary", "identified", 0)

	cases := []struct {
		outcome string
		failed  bool
	}{
		{"won", false},
		{"superseded", false},
		{"discarded_inconsistent", false},
		{"not_identified", false},
		{"skipped_expired", false},
		{"rate_limited", true},
		{"timeout", true},
		{"error", true},
	}
	for _, c := range cases {
		t.Run(c.outcome, func(t *testing.T) {
			id := seedIdentifierCall(t, p, attemptID, c.outcome)
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT transport_failed FROM telemetry.v_identifier_plugin_call WHERE id = $1::uuid`, id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_identifier_plugin_call: %v", err)
			}
			if got != c.failed {
				t.Errorf("transport_failed for %s = %v, want %v", c.outcome, got, c.failed)
			}
		})
	}
}

// TestTelemetryViews_CallFailed keeps no_hints out of the failure bucket. A
// description plugin that ran, answered, and had nothing to offer is not broken,
// and counting it as broken would bury the calls that genuinely were.
func TestTelemetryViews_CallFailed(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())

	cases := []struct {
		outcome string
		failed  bool
	}{
		{"hints_returned", false},
		{"no_hints", false},
		{"error", true},
		{"rate_limited", true},
		{"quota_exceeded", true},
		{"model_not_found", true},
	}
	for _, c := range cases {
		t.Run(c.outcome, func(t *testing.T) {
			id := seedDescriptionCall(t, p, runID, c.outcome)
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT call_failed FROM telemetry.v_description_plugin_call WHERE id = $1::uuid`, id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_description_plugin_call: %v", err)
			}
			if got != c.failed {
				t.Errorf("call_failed for %s = %v, want %v", c.outcome, got, c.failed)
			}
		})
	}
}

// TestTelemetryViews_RunRollupsDoNotMultiply is the test a JOIN would fail. The
// run below has two resolution keys and two description plugin calls, which are
// sibling grains: joining v_run to both would produce four rows and report each
// count as twice what it is. The rollups are scalar subqueries for that reason,
// and the seeded attempts are here so that a join through the key side would be
// wrong by a second factor as well.
func TestTelemetryViews_RunRollupsDoNotMultiply(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())

	first := seedKeyWithOutcome(t, p, runID, "identified", 3)
	seedKeyWithOutcome(t, p, runID, "broker_description_only", 5)
	seedAttempt(t, p, first, "primary", "identified", 0)
	seedAttempt(t, p, first, "mismatch_check", "identified", 0)
	seedDescriptionCall(t, p, runID, "hints_returned")
	seedDescriptionCall(t, p, runID, "no_hints")

	var rows, keyCount, keyTxCount, descCount int
	err := p.q.QueryRowContext(ctx, `
		SELECT count(*), max(key_count), max(key_tx_count), max(description_call_count)
		FROM telemetry.v_run WHERE id = $1::uuid
	`, runID).Scan(&rows, &keyCount, &keyTxCount, &descCount)
	if err != nil {
		t.Fatalf("select v_run: %v", err)
	}
	if rows != 1 {
		t.Errorf("v_run rows = %d, want 1", rows)
	}
	if keyCount != 2 {
		t.Errorf("key_count = %d, want 2", keyCount)
	}
	// The fan-out the keys carry, not the number of keys: 3 + 5.
	if keyTxCount != 8 {
		t.Errorf("key_tx_count = %d, want 8", keyTxCount)
	}
	if descCount != 2 {
		t.Errorf("description_call_count = %d, want 2", descCount)
	}
}

// TestTelemetryViews_RunRollupsAreZeroNotNull keeps a quiet run readable. A cycle
// that found no work still opens a run, and a panel dividing by its key count
// should see 0 rather than a null that silently drops the row.
func TestTelemetryViews_RunRollupsAreZeroNotNull(t *testing.T) {
	p := testDBTx(t)
	runID := seedRun(t, p, "grouping_cycle", time.Now())

	var keyCount, keyTxCount, descCount int
	err := p.q.QueryRowContext(context.Background(), `
		SELECT key_count, key_tx_count, description_call_count
		FROM telemetry.v_run WHERE id = $1::uuid
	`, runID).Scan(&keyCount, &keyTxCount, &descCount)
	if err != nil {
		t.Fatalf("select v_run: %v", err)
	}
	if keyCount != 0 || keyTxCount != 0 || descCount != 0 {
		t.Errorf("rollups for an empty run = (%d, %d, %d), want all 0",
			keyCount, keyTxCount, descCount)
	}
}

// TestTelemetryReaderReadsLabelsWithoutReachingPublic pins the one place the
// telemetry schema reads outside itself, and the mechanism that makes it safe.
// A view runs with its owner's privileges, so the reading role can turn an
// instrument id into a name through telemetry.v_instrument_label while holding
// no privilege on instruments itself. If that ever stops being true, the fix is
// not to grant on instruments.
//
// SET LOCAL ROLE rather than a second connection: the reader is a NOLOGIN group
// role, and the transaction is rolled back, so the role reverts with it.
func TestTelemetryReaderReadsLabelsWithoutReachingPublic(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	if _, err := p.q.ExecContext(ctx, `SET LOCAL ROLE telemetry_reader`); err != nil {
		t.Fatalf("set role: %v", err)
	}

	var n int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM telemetry.v_instrument_label`,
	).Scan(&n); err != nil {
		t.Fatalf("telemetry_reader cannot read v_instrument_label: %v", err)
	}

	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM instruments`,
	).Scan(&n); err == nil {
		t.Error("telemetry_reader can read instruments directly; the view indirection is not what is granting access")
	}
}
