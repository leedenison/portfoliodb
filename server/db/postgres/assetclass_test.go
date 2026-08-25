package postgres

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
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
