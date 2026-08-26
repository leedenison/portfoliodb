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
		INSERT INTO telemetry.candidate_plugin_call
			(run_id, plugin_id, precedence, batch_size, items_completed, fields_proposed, outcome, duration_ms)
		VALUES ($1::uuid, 'openai', 100, 20, 18, 26, 'hints_returned', 900)
	`, oldRun); err != nil {
		t.Fatalf("seed candidate plugin call: %v", err)
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
		"telemetry.candidate_plugin_call",
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
			table == "telemetry.candidate_plugin_call" {
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
		"telemetry.candidate_plugin_call",
		"telemetry.v_run",
		"telemetry.v_resolution_key",
		"telemetry.v_identification_attempt",
		"telemetry.v_identifier_plugin_call",
		"telemetry.v_candidate_plugin_call",
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

	t.Run("price gap outcome", func(t *testing.T) {
		p := testDBTx(t)
		runID := seedRun(t, p, "price_fetch_cycle", time.Now())
		// 'skipped' is the tempting member and is deliberately absent: a gap no
		// plugin was eligible for and one every plugin failed on need different
		// fixes, so the vocabulary names which.
		if _, err := p.q.ExecContext(context.Background(), `
			INSERT INTO telemetry.price_gap
				(run_id, listing_id, instrument_id, is_fx, days_outstanding, outcome)
			VALUES ($1::uuid, gen_random_uuid(), gen_random_uuid(), FALSE, 10, 'skipped')
		`, runID); err == nil {
			t.Error("price gap accepted an outcome outside its vocabulary")
		}
	})

	t.Run("price plugin call outcome", func(t *testing.T) {
		p := testDBTx(t)
		gapID := seedPriceGap(t, p, seedRun(t, p, "price_fetch_cycle", time.Now()), "filled", 10)
		if _, err := p.q.ExecContext(context.Background(), `
			INSERT INTO telemetry.price_plugin_call
				(price_gap_id, plugin_id, precedence, range_from, range_before,
				 bars, outcome, duration_ms)
			VALUES ($1::uuid, 'eodhd', 100, DATE '2026-01-01', DATE '2026-01-11',
			        0, 'already_covered', 12)
		`, gapID); err == nil {
			t.Error("price plugin call accepted an outcome outside its vocabulary")
		}
	})
}

// seedPriceGap inserts a price gap under a run and returns its id. An empty
// outcome is a gap the cycle never reached.
func seedPriceGap(t *testing.T, p *Postgres, runID, outcome string, days int) string {
	t.Helper()
	var oc any
	if outcome != "" {
		oc = outcome
	}
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.price_gap
			(run_id, listing_id, instrument_id, is_fx, asset_class, currency, exchange,
			 days_outstanding, outcome)
		VALUES ($1::uuid, gen_random_uuid(), gen_random_uuid(), FALSE, 'STOCK', 'USD', 'XNAS', $2, $3)
		RETURNING id
	`, runID, days, oc).Scan(&id)
	if err != nil {
		t.Fatalf("seed price gap %q: %v", outcome, err)
	}
	return id
}

// seedPriceCall inserts a price plugin call under a gap.
func seedPriceCall(t *testing.T, p *Postgres, gapID, pluginID, outcome string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.price_plugin_call
			(price_gap_id, plugin_id, precedence, range_from, range_before,
			 bars, outcome, duration_ms)
		VALUES ($1::uuid, $2, 100, DATE '2026-01-01', DATE '2026-01-11', 0, $3, 40)
		RETURNING id
	`, gapID, pluginID, outcome).Scan(&id)
	if err != nil {
		t.Fatalf("seed price call %q: %v", outcome, err)
	}
	return id
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
	// The mismatch columns are the detail of one outcome, and the schema's CHECK
	// requires them exactly where that outcome is, so the fixture supplies them
	// for discarded_inconsistent and for nothing else.
	var subject, winner, other, winnerPlugin any
	if outcome == "discarded_inconsistent" {
		subject, winner, other, winnerPlugin = "Currency", "USD", "EUR", "eodhd"
	}
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.identifier_plugin_call
			(identification_attempt_id, plugin_id, outcome, retries, duration_ms,
			 mismatch_subject, mismatch_winner, mismatch_other, mismatch_winner_plugin)
		VALUES ($1::uuid, 'openfigi', $2, 0, 12, $3, $4, $5, $6)
		RETURNING id
	`, attemptID, outcome, subject, winner, other, winnerPlugin).Scan(&id)
	if err != nil {
		t.Fatalf("seed identifier call %q: %v", outcome, err)
	}
	return id
}

// seedCandidateCall inserts a candidate plugin call under a run.
func seedCandidateCall(t *testing.T, p *Postgres, runID, outcome string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO telemetry.candidate_plugin_call
			(run_id, plugin_id, precedence, batch_size, items_completed, fields_proposed, outcome, duration_ms)
		VALUES ($1::uuid, 'openai', 100, 10, 4, 7, $2, 900)
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
// candidate plugin that ran, answered, and had nothing to offer is not broken,
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
			id := seedCandidateCall(t, p, runID, c.outcome)
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT call_failed FROM telemetry.v_candidate_plugin_call WHERE id = $1::uuid`, id,
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
// run below has two resolution keys and two candidate plugin calls, which are
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
	seedCandidateCall(t, p, runID, "hints_returned")
	seedCandidateCall(t, p, runID, "no_hints")
	// A fourth child grain under the same run. Two gaps, one carrying two calls,
	// so a join rather than a scalar subquery would multiply the key counts by
	// three and the gap count by two.
	gapID := seedPriceGap(t, p, runID, "filled", 30)
	seedPriceGap(t, p, runID, "settled_empty", 5)
	seedPriceCall(t, p, gapID, "eodhd", "bars_returned")
	seedPriceCall(t, p, gapID, "massive", "no_data")

	var rows, keyCount, keyTxCount, descCount, gapCount int
	err := p.q.QueryRowContext(ctx, `
		SELECT count(*), max(key_count), max(key_tx_count), max(candidate_call_count),
		       max(gap_count)
		FROM telemetry.v_run WHERE id = $1::uuid
	`, runID).Scan(&rows, &keyCount, &keyTxCount, &descCount, &gapCount)
	if err != nil {
		t.Fatalf("select v_run: %v", err)
	}
	if rows != 1 {
		t.Errorf("v_run rows = %d, want 1", rows)
	}
	if gapCount != 2 {
		t.Errorf("gap_count = %d, want 2", gapCount)
	}
	if keyCount != 2 {
		t.Errorf("key_count = %d, want 2", keyCount)
	}
	// The fan-out the keys carry, not the number of keys: 3 + 5.
	if keyTxCount != 8 {
		t.Errorf("key_tx_count = %d, want 8", keyTxCount)
	}
	if descCount != 2 {
		t.Errorf("candidate_call_count = %d, want 2", descCount)
	}
}

// TestTelemetryViews_RunRollupsAreZeroNotNull keeps a quiet run readable. A cycle
// that found no work still opens a run, and a panel dividing by its key count
// should see 0 rather than a null that silently drops the row.
func TestTelemetryViews_RunRollupsAreZeroNotNull(t *testing.T) {
	p := testDBTx(t)
	runID := seedRun(t, p, "grouping_cycle", time.Now())

	var keyCount, keyTxCount, descCount, gapCount int
	err := p.q.QueryRowContext(context.Background(), `
		SELECT key_count, key_tx_count, candidate_call_count, gap_count
		FROM telemetry.v_run WHERE id = $1::uuid
	`, runID).Scan(&keyCount, &keyTxCount, &descCount, &gapCount)
	if err != nil {
		t.Fatalf("select v_run: %v", err)
	}
	if keyCount != 0 || keyTxCount != 0 || descCount != 0 || gapCount != 0 {
		t.Errorf("rollups for an empty run = (%d, %d, %d, %d), want all 0",
			keyCount, keyTxCount, descCount, gapCount)
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

// TestTelemetryViews_Settled pins which gap outcomes mean the gap is dealt with.
// settled_empty is the member worth staring at, and is the mirror image of
// broker_description_only above: no prices were stored, and the gap is
// nonetheless finished with. Reading it as a failure would put every untraded
// week and every pre-IPO range into a panel meant to show outages.
func TestTelemetryViews_Settled(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "price_fetch_cycle", time.Now())

	cases := []struct {
		outcome string
		settled bool
	}{
		{"filled", true},
		{"settled_empty", true},
		{"no_eligible_plugin", false},
		{"all_plugins_failed", false},
		{"listing_missing", false},
		// A gap the cycle died before reaching. Not settled, and it must not
		// vanish from both the column and its negation.
		{"", false},
	}
	for _, c := range cases {
		name := c.outcome
		if name == "" {
			name = "unstamped"
		}
		t.Run(name, func(t *testing.T) {
			id := seedPriceGap(t, p, runID, c.outcome, 10)
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT settled FROM telemetry.v_price_gap WHERE id = $1::uuid`, id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_price_gap: %v", err)
			}
			if got != c.settled {
				t.Errorf("settled for %q = %v, want %v", c.outcome, got, c.settled)
			}
		})
	}
}

// TestTelemetryViews_HadCall separates a gap that never asked a plugin from one
// that asked and got nothing. Four paths -- the plugin filter, a fetch block, no
// supported identifier, and a range already covered -- return before FetchPrices,
// so no call is the ordinary case and not a fault.
func TestTelemetryViews_HadCall(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "price_fetch_cycle", time.Now())

	asked := seedPriceGap(t, p, runID, "all_plugins_failed", 10)
	seedPriceCall(t, p, asked, "eodhd", "error")
	never := seedPriceGap(t, p, runID, "no_eligible_plugin", 10)

	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"asked", asked, true},
		{"never asked", never, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got bool
			if err := p.q.QueryRowContext(ctx,
				`SELECT had_call FROM telemetry.v_price_gap WHERE id = $1::uuid`, c.id,
			).Scan(&got); err != nil {
				t.Fatalf("select v_price_gap: %v", err)
			}
			if got != c.want {
				t.Errorf("had_call = %v, want %v", got, c.want)
			}
		})
	}
}

// TestTelemetryViews_PriceTransportFailed pins where the line falls between the
// provider being unreachable and the provider having nothing. no_data is the
// member a panel most easily gets wrong: it is the expected answer for a range an
// instrument did not trade in, and counting it as a transport failure would make
// every delisted holding look like an outage.
//
// upsert_failed sits outside transport_failed and alone in write_failed, because
// it is our database and not the provider's API.
func TestTelemetryViews_PriceTransportFailed(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	gapID := seedPriceGap(t, p, seedRun(t, p, "price_fetch_cycle", time.Now()), "filled", 10)

	cases := []struct {
		outcome   string
		transport bool
		write     bool
	}{
		{"timeout", true, false},
		{"error", true, false},
		{"bars_returned", false, false},
		{"no_data", false, false},
		{"history_limit", false, false},
		{"permanent_block", false, false},
		{"upsert_failed", false, true},
	}
	for _, c := range cases {
		t.Run(c.outcome, func(t *testing.T) {
			id := seedPriceCall(t, p, gapID, "eodhd", c.outcome)
			var transport, write bool
			if err := p.q.QueryRowContext(ctx, `
				SELECT transport_failed, write_failed
				FROM telemetry.v_price_plugin_call WHERE id = $1::uuid
			`, id).Scan(&transport, &write); err != nil {
				t.Fatalf("select v_price_plugin_call: %v", err)
			}
			if transport != c.transport {
				t.Errorf("transport_failed for %q = %v, want %v", c.outcome, transport, c.transport)
			}
			if write != c.write {
				t.Errorf("write_failed for %q = %v, want %v", c.outcome, write, c.write)
			}
		})
	}
}

// TestTelemetryViews_PriceGapDoesNotFanOut keeps the gap view one row per gap
// however many plugins were put to it. A gap asked of three plugins is one gap,
// and a view that returned it three times would make every count over
// days_outstanding silently treble.
func TestTelemetryViews_PriceGapDoesNotFanOut(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	gapID := seedPriceGap(t, p, seedRun(t, p, "price_fetch_cycle", time.Now()), "filled", 30)
	seedPriceCall(t, p, gapID, "eodhd", "no_data")
	seedPriceCall(t, p, gapID, "massive", "bars_returned")
	seedPriceCall(t, p, gapID, "massive", "history_limit")

	var rows, days int
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(days_outstanding), 0)
		FROM telemetry.v_price_gap WHERE id = $1::uuid
	`, gapID).Scan(&rows, &days); err != nil {
		t.Fatalf("select v_price_gap: %v", err)
	}
	if rows != 1 {
		t.Errorf("v_price_gap rows = %d, want 1", rows)
	}
	if days != 30 {
		t.Errorf("summed days_outstanding = %d, want 30", days)
	}
}

// TestTelemetryPriceGapCascades pins that a gap and its calls go with their run,
// so retention stays a delete from one table.
func TestTelemetryPriceGapCascades(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "price_fetch_cycle", time.Now())
	gapID := seedPriceGap(t, p, runID, "filled", 10)
	seedPriceCall(t, p, gapID, "eodhd", "bars_returned")

	if _, err := p.q.ExecContext(ctx,
		`DELETE FROM telemetry.run WHERE id = $1::uuid`, runID); err != nil {
		t.Fatalf("delete run: %v", err)
	}

	for _, tbl := range []string{"price_gap", "price_plugin_call"} {
		var n int
		if err := p.q.QueryRowContext(ctx,
			`SELECT count(*) FROM telemetry.`+tbl,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows after its run went", tbl, n)
		}
	}
}

// TestTelemetryPriceCallDurationIsNullable pins that history_limit can record no
// duration. It made no call, and a zero would average into the latency panel as a
// plugin that answered instantly.
func TestTelemetryPriceCallDurationIsNullable(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	gapID := seedPriceGap(t, p, seedRun(t, p, "price_fetch_cycle", time.Now()), "settled_empty", 10)

	var id string
	if err := p.q.QueryRowContext(ctx, `
		INSERT INTO telemetry.price_plugin_call
			(price_gap_id, plugin_id, precedence, range_from, range_before,
			 bars, outcome, duration_ms)
		VALUES ($1::uuid, 'eodhd', 100, DATE '2026-01-01', DATE '2026-01-11',
		        0, 'history_limit', NULL)
		RETURNING id
	`, gapID).Scan(&id); err != nil {
		t.Fatalf("insert history_limit call with a null duration: %v", err)
	}

	var duration *int
	if err := p.q.QueryRowContext(ctx,
		`SELECT duration_ms FROM telemetry.v_price_plugin_call WHERE id = $1::uuid`, id,
	).Scan(&duration); err != nil {
		t.Fatalf("select v_price_plugin_call: %v", err)
	}
	if duration != nil {
		t.Errorf("duration_ms = %d, want null", *duration)
	}
}

// TestTelemetryViews_PriceCallDaysAreDerived pins that the span of a fetch range
// is the subtraction of its own two dates rather than a stored number that could
// drift from them.
func TestTelemetryViews_PriceCallDaysAreDerived(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	gapID := seedPriceGap(t, p, seedRun(t, p, "price_fetch_cycle", time.Now()), "filled", 10)
	id := seedPriceCall(t, p, gapID, "eodhd", "bars_returned")

	var days int
	if err := p.q.QueryRowContext(ctx,
		`SELECT days FROM telemetry.v_price_plugin_call WHERE id = $1::uuid`, id,
	).Scan(&days); err != nil {
		t.Fatalf("select v_price_plugin_call: %v", err)
	}
	// The seed's half-open [2026-01-01, 2026-01-11).
	if days != 10 {
		t.Errorf("days = %d, want 10", days)
	}
}

// A proposed field names two parents, and the view that joins them is what makes
// "did completion help" a query. Neither parent alone can answer it: the call
// covers many keys, and the key does not know which call spoke for it.
func TestTelemetryViews_CandidateField(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)
	callID := seedCandidateCall(t, p, runID, "hints_returned")

	for _, f := range []struct {
		field, value, outcome string
		confidence            any
	}{
		{"exchange", "AAPL", "confirmed", 0.7},
		{"currency", "USD", "untested", nil},
	} {
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO telemetry.candidate_field
				(resolution_key_id, call_id, field, value, confidence, outcome)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
		`, keyID, callID, f.field, f.value, f.confidence, f.outcome); err != nil {
			t.Fatalf("seed candidate field %q: %v", f.field, err)
		}
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT field, outcome, was_tested, plugin_id, key_outcome, run_kind, is_import
		FROM telemetry.v_candidate_field WHERE run_id = $1::uuid ORDER BY field
	`, runID)
	if err != nil {
		t.Fatalf("read v_candidate_field: %v", err)
	}
	defer rows.Close()
	type row struct {
		field, outcome, pluginID, keyOutcome, runKind string
		wasTested, isImport                           bool
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.field, &r.outcome, &r.wasTested, &r.pluginID, &r.keyOutcome, &r.runKind, &r.isImport); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("read %d rows, want 2", len(got))
	}
	// The key's own outcome travels with the field: a field confirmed on a key
	// that ended broker-description-only helped nobody.
	if got[0].field != "currency" || got[0].wasTested {
		t.Errorf("currency row = %+v, want untested and not counted as tested", got[0])
	}
	if got[1].field != "exchange" || !got[1].wasTested || got[1].pluginID != "openai" {
		t.Errorf("exchange row = %+v, want a tested openai proposal", got[1])
	}
	if got[1].keyOutcome != "identified" || got[1].runKind != "tx_import" || !got[1].isImport {
		t.Errorf("exchange row context = %+v, want the key and run it hangs off", got[1])
	}
}

// Both parents are required, and a field outlives neither: deleting a run takes
// the call, the key and the fields beneath them.
func TestTelemetryCandidateField_CascadesFromItsParents(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)
	callID := seedCandidateCall(t, p, runID, "hints_returned")
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO telemetry.candidate_field (resolution_key_id, call_id, field, value, outcome)
		VALUES ($1::uuid, $2::uuid, 'ticker', 'AAPL', 'confirmed')
	`, keyID, callID); err != nil {
		t.Fatalf("seed candidate field: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `DELETE FROM telemetry.run WHERE id = $1::uuid`, runID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	var n int
	if err := p.q.QueryRowContext(ctx, `SELECT count(*) FROM telemetry.candidate_field`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("candidate_field rows = %d, want 0 after the run was deleted", n)
	}
}

// The mismatch columns are the detail of one outcome, so the schema requires
// them exactly where that outcome is. A discarded_inconsistent row with nothing
// saying what was argued about is the log-line-only state adr/0080 exists to end,
// and a mismatch on any other outcome is a column meaning a different thing per
// row.
func TestTelemetrySchema_MismatchBelongsToOneOutcome(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)
	attemptID := seedAttempt(t, p, keyID, "primary", "identified", 0)

	insert := func(outcome string, subject any) error {
		_, err := p.q.ExecContext(ctx, `
			INSERT INTO telemetry.identifier_plugin_call
				(identification_attempt_id, plugin_id, outcome, retries, duration_ms, mismatch_subject)
			VALUES ($1::uuid, 'openfigi', $2, 0, 1, $3)
		`, attemptID, outcome, subject)
		return err
	}
	if err := insert("discarded_inconsistent", nil); err == nil {
		t.Error("a result discarded as inconsistent was stored without saying what it argued about")
	}
}

// The same rule from the other side, in its own transaction: the first insert
// above aborts the one it runs in.
func TestTelemetrySchema_MismatchOnAnotherOutcomeIsRefused(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)
	attemptID := seedAttempt(t, p, keyID, "primary", "identified", 0)

	_, err := p.q.ExecContext(ctx, `
		INSERT INTO telemetry.identifier_plugin_call
			(identification_attempt_id, plugin_id, outcome, retries, duration_ms, mismatch_subject)
		VALUES ($1::uuid, 'openfigi', 'won', 0, 1, 'Currency')
	`, attemptID)
	if err == nil {
		t.Error("a winning call carries a mismatch, which is a column meaning a different thing per row")
	}
}

// A conflicting hint hangs off the key whose names disagreed, and carries the
// whole triple and the instrument it reached. One row per instrument, so a panel
// groups by the key for the disagreement and counts keys for how often it
// happens.
func TestTelemetryViews_ConflictingHint(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	runID := seedRun(t, p, "tx_import", time.Now())
	keyID := seedResolutionKey(t, p, runID)

	inst := usdInstrument(t, p)
	for _, h := range []struct{ typ, value string }{
		{"ISIN", "US0000000001"},
		{"CUSIP", "000000001"},
	} {
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO telemetry.conflicting_hint
				(resolution_key_id, identifier_type, value, instrument_id)
			VALUES ($1::uuid, $2, $3, $4::uuid)
		`, keyID, h.typ, h.value, inst); err != nil {
			t.Fatalf("seed conflicting hint %s: %v", h.typ, err)
		}
	}

	var n int
	var runKind, keySource string
	if err := p.q.QueryRowContext(ctx, `
		SELECT count(*), min(run_kind), min(key_source)
		FROM telemetry.v_conflicting_hint WHERE resolution_key_id = $1::uuid
	`, keyID).Scan(&n, &runKind, &keySource); err != nil {
		t.Fatalf("select v_conflicting_hint: %v", err)
	}
	if n != 2 {
		t.Errorf("rows = %d, want one per instrument the names reached", n)
	}
	// The run and the key are flattened in, so a panel reads the disagreement
	// without joining back out of the schema.
	if runKind != "tx_import" || keySource != "FIDELITY_CSV" {
		t.Errorf("run_kind = %q, key_source = %q; want the parents flattened in", runKind, keySource)
	}
}

// An unhandled event is deduped within a run and not across them. The same event
// failing on every cycle is how long it has been failing, and folding the rows
// together would take that away -- the reason price_gap writes a row per cycle
// for a gap that keeps coming back.
func TestTelemetrySchema_UnhandledEventRecursAcrossRuns(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst := usdInstrument(t, p)

	insert := func(runID string) error {
		_, err := p.q.ExecContext(ctx, `
			INSERT INTO telemetry.unhandled_corporate_event
				(run_id, instrument_id, event_type, ex_date, detail)
			VALUES ($1::uuid, $2::uuid, 'REVERSE_SPLIT', '2025-04-11', '1:10')
			ON CONFLICT (run_id, instrument_id, event_type, ex_date) DO NOTHING
		`, runID, inst)
		return err
	}

	first := seedRun(t, p, "corporate_event_cycle", time.Now())
	if err := insert(first); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Reached twice in one run -- a split is examined for the underlying and
	// again per option -- and recorded once.
	if err := insert(first); err != nil {
		t.Fatalf("second insert in the same run: %v", err)
	}
	second := seedRun(t, p, "corporate_event_cycle", time.Now())
	if err := insert(second); err != nil {
		t.Fatalf("insert under a second run: %v", err)
	}

	var n int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM telemetry.unhandled_corporate_event WHERE instrument_id = $1::uuid`,
		inst).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("rows = %d, want one per run", n)
	}

	// The run is flattened into the view, so a panel reads what kind of run
	// could not apply it without joining out of the schema.
	var kind string
	if err := p.q.QueryRowContext(ctx, `
		SELECT min(run_kind) FROM telemetry.v_unhandled_corporate_event
		WHERE instrument_id = $1::uuid
	`, inst).Scan(&kind); err != nil {
		t.Fatalf("select v_unhandled_corporate_event: %v", err)
	}
	if kind != "corporate_event_cycle" {
		t.Errorf("run_kind = %q, want the cycle's", kind)
	}
}

// The instrument is not a foreign key: telemetry outlives the work it describes,
// and a merge deleting an instrument must not take the record of what could not
// be applied to it.
func TestTelemetrySchema_UnhandledEventOutlivesItsInstrument(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	inst := usdInstrument(t, p)
	runID := seedRun(t, p, "corporate_event_cycle", time.Now())

	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO telemetry.unhandled_corporate_event
			(run_id, instrument_id, event_type, detail)
		VALUES ($1::uuid, $2::uuid, 'MERGER', 'unsupported')
	`, runID, inst); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.q.ExecContext(ctx, `DELETE FROM instruments WHERE id = $1::uuid`, inst); err != nil {
		t.Fatalf("delete instrument: %v", err)
	}

	var n int
	if err := p.q.QueryRowContext(ctx,
		`SELECT count(*) FROM telemetry.unhandled_corporate_event WHERE instrument_id = $1::uuid`,
		inst).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("rows = %d; the event went with the instrument it names", n)
	}
}
