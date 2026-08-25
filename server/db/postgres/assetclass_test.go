package postgres

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// sqlLiteral pulls the quoted strings out of a constraint definition.
var sqlLiteral = regexp.MustCompile(`'([^']*)'`)

// The chk_asset_class_vocabulary CHECK in 001_initial.sql restates the proto
// vocabulary, because a CHECK cannot read the generated enum. This is what
// holds the two in step, in the pattern TestCurrencyFamily_matchesGoTable
// follows: it reads the constraint back out of the catalogue and compares the
// values it names against the enum, so it fails in both directions -- a class
// added to the proto and not to the migration, and a value left in the
// migration that the proto no longer declares.
func TestAssetClassCheck_matchesProtoVocabulary(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	var def string
	if err := p.q.QueryRowContext(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname = 'chk_asset_class_vocabulary'
	`).Scan(&def); err != nil {
		t.Fatalf("read chk_asset_class_vocabulary: %v", err)
	}

	var inSQL []string
	for _, m := range sqlLiteral.FindAllStringSubmatch(def, -1) {
		inSQL = append(inSQL, m[1])
	}
	var inProto []string
	for i, name := range typev1.AssetClass_name {
		if typev1.AssetClass(i) != typev1.AssetClass_ASSET_CLASS_UNSPECIFIED {
			inProto = append(inProto, name)
		}
	}
	sort.Strings(inSQL)
	sort.Strings(inProto)
	if strings.Join(inSQL, ",") != strings.Join(inProto, ",") {
		t.Errorf("the CHECK and the proto disagree:\n  SQL:   %s\n  proto: %s",
			strings.Join(inSQL, ","), strings.Join(inProto, ","))
	}

	// And the constraint is live, not merely well spelled. Each value goes in
	// under its own savepoint: OPTION and FUTURE fail chk_underlying_required on
	// a bare row, which is a different constraint and not this test's subject.
	for _, name := range inProto {
		if _, err := p.q.ExecContext(ctx, `SAVEPOINT vocab`); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		_, err := p.q.ExecContext(ctx, `INSERT INTO instruments (asset_class) VALUES ($1)`, name)
		if err != nil && strings.Contains(err.Error(), "chk_asset_class_vocabulary") {
			t.Errorf("asset_class %q rejected by the vocabulary constraint", name)
		}
		if _, err := p.q.ExecContext(ctx, `ROLLBACK TO SAVEPOINT vocab`); err != nil {
			t.Fatalf("rollback to savepoint: %v", err)
		}
	}

	_, err := p.q.ExecContext(ctx, `INSERT INTO instruments (asset_class) VALUES ('NOT_A_CLASS')`)
	if err == nil || !strings.Contains(err.Error(), "chk_asset_class_vocabulary") {
		t.Errorf("a class outside the vocabulary was not refused by the constraint: %v", err)
	}
}

// chk_underlying_required names the classes the schema requires an underlying
// line for, because a CHECK cannot call db.IsDerivative. This holds the two in
// step, in the pattern the test above follows: it reads the constraint back out
// of the catalogue, so it fails in both directions -- a class added under
// DERIVATIVE that the migration does not require a line of, and a class the
// migration still names that IsDerivative no longer accepts.
//
// The query in HeldEventBearingInstruments needs no such test: it binds
// db.DerivativeClasses rather than spelling the classes out, which is available
// to anything that is not a CHECK.
func TestUnderlyingRequiredCheck_matchesIsDerivative(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	var def string
	if err := p.q.QueryRowContext(ctx, `
		SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname = 'chk_underlying_required'
	`).Scan(&def); err != nil {
		t.Fatalf("read chk_underlying_required: %v", err)
	}

	// The constraint states the rule and its negation, so each class it names
	// appears twice.
	seen := make(map[string]bool)
	var inSQL []string
	for _, m := range sqlLiteral.FindAllStringSubmatch(def, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			inSQL = append(inSQL, m[1])
		}
	}
	inGo := db.DerivativeClasses()
	sort.Strings(inSQL)
	sort.Strings(inGo)
	if strings.Join(inSQL, ",") != strings.Join(inGo, ",") {
		t.Errorf("the CHECK and IsDerivative disagree:\n  SQL: %s\n  Go:  %s",
			strings.Join(inSQL, ","), strings.Join(inGo, ","))
	}

	// And the constraint is live, not merely well spelled. The row carries the
	// option fields so that the missing underlying line is the only thing wrong
	// with it -- chk_option_fields would otherwise refuse an OPTION first, and a
	// refusal by the wrong constraint proves nothing about this one. The fields
	// are set for every class rather than for OPTION alone, chk_option_fields
	// asking nothing of a row that is not one, so this test names no class of its
	// own. Each insert goes in under its own savepoint so a refusal does not
	// abort the ones after it.
	const insertNoLine = `
		INSERT INTO instruments (asset_class, strike, expiry, put_call)
		VALUES ($1, 100, '2025-12-19', 'C')
	`
	for _, name := range inGo {
		if _, err := p.q.ExecContext(ctx, `SAVEPOINT underlying`); err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		_, err := p.q.ExecContext(ctx, insertNoLine, name)
		if err == nil || !strings.Contains(err.Error(), "chk_underlying_required") {
			t.Errorf("asset_class %q was accepted with no underlying line: %v", name, err)
		}
		if _, err := p.q.ExecContext(ctx, `ROLLBACK TO SAVEPOINT underlying`); err != nil {
			t.Fatalf("rollback to savepoint: %v", err)
		}
	}

	// A class the constraint does not name needs no line, which is what says the
	// refusals above came from this constraint and not from the row.
	if _, err := p.q.ExecContext(ctx, insertNoLine, db.AssetClassStock); err != nil {
		t.Errorf("a STOCK with no underlying line was refused: %v", err)
	}
}
