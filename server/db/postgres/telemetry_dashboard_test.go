package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The dashboards are SQL against the telemetry views, held in files no compiler
// reads and no test would otherwise touch. Rename a column in 005_telemetry.sql
// and every panel selecting it keeps working until somebody opens a browser --
// which, for a dashboard read a few times a month, could be a long time.
//
// So each panel's query is pulled out of the JSON and planned against the real
// schema. EXPLAIN resolves every column and function without running anything,
// which is the whole of what is being asked: does this query still make sense
// against these views.
//
// It cannot check that a panel means what its title says. It can check that a
// panel which no longer parses fails here rather than in front of a person
// trying to diagnose an import.

// dashboardDir is docker/grafana/dashboards relative to this package.
const dashboardDir = "../../../docker/grafana/dashboards"

var (
	// $__timeGroupAlias(col,$__interval) -> col AS "time". Grafana's own
	// expansion buckets the column; for planning, the column alone type checks
	// the same way and keeps the alias the panel's ORDER BY refers to.
	reTimeGroup = regexp.MustCompile(`\$__timeGroupAlias\(\s*([A-Za-z_][A-Za-z0-9_.]*)\s*,\s*\$__interval\s*\)`)
	// $__timeFilter(col) is a BETWEEN over the dashboard's time range.
	reTimeFilter = regexp.MustCompile(`\$__timeFilter\(\s*[A-Za-z_][A-Za-z0-9_.]*\s*\)`)
)

// expand turns a panel's rawSql into something the planner will accept, standing
// in for what Grafana substitutes at query time. Every replacement keeps the
// column references and the result types the panel depends on, because those are
// what this test exists to check.
func expand(sql string) string {
	sql = reTimeGroup.ReplaceAllString(sql, `$1 AS "time"`)
	sql = reTimeFilter.ReplaceAllString(sql, "true")
	// A single-value variable arrives already quoted by the panel: '${run}'::uuid.
	sql = strings.ReplaceAll(sql, "${run}", "00000000-0000-0000-0000-000000000000")
	sql = strings.ReplaceAll(sql, "${scope}", "all")
	// :sqlstring quotes and comma-joins a multi-value variable. One empty string
	// keeps the IN list valid and matches nothing.
	sql = strings.ReplaceAll(sql, "${broker:sqlstring}", "''")
	return sql
}

// query is one rawSql found in a dashboard, with enough context to name it in a
// failure.
type query struct {
	dashboard string
	panel     string
	sql       string
}

// collect walks a dashboard file for every rawSql it holds: panel targets, the
// targets of panels nested inside a row, and the template variable queries,
// which are as capable of naming a dropped column as any panel.
func collect(t *testing.T, path string) []query {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var d struct {
		Title  string `json:"title"`
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				RawSQL string `json:"rawSql"`
			} `json:"targets"`
			Panels []struct {
				Title   string `json:"title"`
				Targets []struct {
					RawSQL string `json:"rawSql"`
				} `json:"targets"`
			} `json:"panels"`
		} `json:"panels"`
		Templating struct {
			List []struct {
				Name string `json:"name"`
				Type string `json:"type"`
				// A bare string for either kind of variable: a custom variable
				// holds its comma-joined options, a query variable its SQL.
				// Decoding late keeps both readable by one struct.
				Query json.RawMessage `json:"query"`
			} `json:"list"`
		} `json:"templating"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var out []query
	for _, p := range d.Panels {
		for _, tg := range p.Targets {
			if tg.RawSQL != "" {
				out = append(out, query{d.Title, p.Title, tg.RawSQL})
			}
		}
		for _, n := range p.Panels {
			for _, tg := range n.Targets {
				if tg.RawSQL != "" {
					out = append(out, query{d.Title, n.Title, tg.RawSQL})
				}
			}
		}
	}
	for _, v := range d.Templating.List {
		if v.Type != "query" {
			continue
		}
		// A string, not the {rawSql, format} object a panel target uses.
		// Grafana hands a query variable's query to metricFindQuery, which
		// assigns it to rawSql itself; an object there reaches postgres as an
		// object and the picker silently offers nothing. So the shape is part
		// of what this test checks, not just the SQL inside it.
		var sql string
		if err := json.Unmarshal(v.Query, &sql); err != nil {
			t.Fatalf("variable %s in %s: query must be a SQL string, not an object: %v",
				v.Name, path, err)
		}
		if sql != "" {
			out = append(out, query{d.Title, "variable " + v.Name, sql})
		}
	}
	return out
}

// TestDashboardQueriesStillPlan is the test that fails when a view moves under a
// dashboard.
func TestDashboardQueriesStillPlan(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	files, err := filepath.Glob(filepath.Join(dashboardDir, "*.json"))
	if err != nil {
		t.Fatalf("glob dashboards: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no dashboards found under %s", dashboardDir)
	}

	var queries []query
	for _, f := range files {
		queries = append(queries, collect(t, f)...)
	}
	// A dashboard whose panels stopped being extracted would otherwise pass this
	// test by checking nothing at all.
	if len(queries) < 20 {
		t.Fatalf("found only %d dashboard queries, which is too few to be right", len(queries))
	}

	for _, q := range queries {
		t.Run(q.dashboard+"/"+q.panel, func(t *testing.T) {
			if _, err := p.q.ExecContext(ctx, "EXPLAIN "+expand(q.sql)); err != nil {
				t.Errorf("panel query no longer plans against the schema: %v\n\n%s",
					err, expand(q.sql))
			}
		})
	}
}

// TestDashboardsSelectOnlyFromViews pins the rule the issue is built on: a panel
// reads a view, so a judgement it needs is stated once in reviewed SQL rather
// than retyped into a dashboard nobody diffs. Reading a table directly would let
// a panel recompute is_import or resolved for itself and drift from the
// definition every other panel uses.
func TestDashboardsSelectOnlyFromViews(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(dashboardDir, "*.json"))
	if err != nil {
		t.Fatalf("glob dashboards: %v", err)
	}
	// Every relation in the telemetry schema that is not a view.
	tables := []string{
		"telemetry.run",
		"telemetry.resolution_key",
		"telemetry.identification_attempt",
		"telemetry.identifier_plugin_call",
		"telemetry.candidate_plugin_call",
	}
	for _, f := range files {
		for _, q := range collect(t, f) {
			for _, tbl := range tables {
				// The view names share these prefixes, so match the table only
				// where a view name cannot also match.
				for _, kw := range []string{"FROM " + tbl, "JOIN " + tbl} {
					if strings.Contains(q.sql, kw) {
						t.Errorf("%s / %s reads %s directly; panels read the views",
							q.dashboard, q.panel, tbl)
					}
				}
			}
		}
	}
}
