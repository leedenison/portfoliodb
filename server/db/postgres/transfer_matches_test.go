package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"slices"
	"strconv"
	"testing"
	"time"
)

// transferFixture stands up the two sides of one transfer hop as ingestion would
// leave them: a journal posting in each account, each balanced by a
// TRANSFER_CLEARING counterparty holding the value in transit. It returns the group
// ids of the departure and the arrival.
//
// The rows are the 2025-04-15 lump sum from the Fidelity master, whose references
// differ by 3.
// fidelityCorrelations is the evidence a Fidelity export supplies for one row: a
// reference number, comparable by equality and by distance, and the account the
// source names as the other side where it names one.
func fidelityCorrelations(t *testing.T, ref, counterparty string) []*archivev1.Correlation {
	t.Helper()
	ordinal, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		t.Fatalf("reference %q: %v", ref, err)
	}
	out := []*archivev1.Correlation{{
		Token: ref, Ordinal: &ordinal,
		Scope: typev1.Scope_SCOPE_FILE,
		Match: []typev1.Match{typev1.Match_MATCH_EXACT, typev1.Match_MATCH_ORDINAL},
	}}
	if counterparty != "" {
		out = append(out, &archivev1.Correlation{
			Label: "counterparty", Token: counterparty,
			Scope: typev1.Scope_SCOPE_BROKER,
			Match: []typev1.Match{typev1.Match_MATCH_ACCOUNT},
		})
	}
	return out
}

func transferFixture(t *testing.T, p *Postgres, userID, instID string) (from, to string) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2025, 4, 15, 0, 0, 0, 0, time.UTC)
	period := func() (*timestamppb.Timestamp, *timestamppb.Timestamp) {
		return timestamppb.New(base), timestamppb.New(base.Add(24 * time.Hour))
	}
	side := func(account, qty, ref, counterparty string) []*apiv1.Tx {
		return []*apiv1.Tx{
			{Timestamp: timestamppb.New(base), InstrumentDescription: "GBP", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER,
				Quantity: qty, Account: account, GroupRef: ref,
				Correlations: fidelityCorrelations(t, ref, counterparty)},
			// The clearing counterparty, equal and opposite, as routing writes it:
			// no reference of its own, because it was transcribed from no row.
			{Timestamp: timestamppb.New(base), InstrumentDescription: "GBP", BrokerTxType: []typev1.TxType{typev1.TxType_TRANSFER}, ResolvedTxType: typev1.TxType_TRANSFER,
				Quantity: negate(t, qty), Account: account, GroupRef: ref,
				AccountType: typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING},
		}
	}
	f, b := period()
	txs := append(side("AG10000001", "-20000", "971613411", ""),
		side("AW10000001", "20000", "971613414", "AG10000001")...)
	ids := []string{instID, instID, instID, instID}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", "", f, b, txs, ids, nil, nil); err != nil {
		t.Fatalf("replace: %v", err)
	}
	// The departure's clearing leg is positive: the account's own leg is negative
	// and the clearing leg holds what is owed out.
	if err := p.q.QueryRowContext(ctx, `
		SELECT group_id FROM txs
		WHERE user_id = $1::uuid AND account_type = 'TRANSFER_CLEARING' AND quantity > 0
	`, userID).Scan(&from); err != nil {
		t.Fatalf("departure group: %v", err)
	}
	if err := p.q.QueryRowContext(ctx, `
		SELECT group_id FROM txs
		WHERE user_id = $1::uuid AND account_type = 'TRANSFER_CLEARING' AND quantity < 0
	`, userID).Scan(&to); err != nil {
		t.Fatalf("arrival group: %v", err)
	}
	return from, to
}

func negate(t *testing.T, qty string) string {
	t.Helper()
	if qty[0] == '-' {
		return qty[1:]
	}
	return "-" + qty
}

func transferUser(t *testing.T, p *Postgres, sub string) (userID, instID string) {
	t.Helper()
	ctx := context.Background()
	userID, _ = p.GetOrCreateUser(ctx, sub, "U", sub+"@t.com")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "BROKER_DESCRIPTION", Domain: sub, Value: "GBP", Canonical: false}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	return userID, instID
}

// TestListUnmatchedTransferSides_ReadsBothSidesWithTheirEvidence verifies the shape
// the matcher works on: one row per clearing residual, signed by direction, carrying
// the references and counterparties of its whole group rather than of the residual,
// which was transcribed from no row and carries neither.
func TestListUnmatchedTransferSides_ReadsBothSidesWithTheirEvidence(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := transferUser(t, p, "sub|tm-sides")
	transferFixture(t, p, userID, instID)

	sides, err := p.ListUnmatchedTransferSides(ctx, db.TransferSideOpts{UserID: userID})
	if err != nil {
		t.Fatalf("list sides: %v", err)
	}
	if len(sides) != 2 {
		t.Fatalf("got %d unmatched sides, want 2", len(sides))
	}
	byAccount := map[string]db.TransferSide{}
	for _, s := range sides {
		byAccount[s.Account] = s
	}
	departure, arrival := byAccount["AG10000001"], byAccount["AW10000001"]
	if !departure.Amount.Add(arrival.Amount).IsZero() {
		t.Errorf("sides sum to %v, want a pair summing to exactly zero",
			departure.Amount.Add(arrival.Amount))
	}
	if !departure.Amount.IsPositive() {
		t.Errorf("departure amount = %v, want positive: the value left this account", departure.Amount)
	}
	// The evidence comes from the journal leg, not from the residual beside it,
	// which was transcribed from no row and correlates with nothing.
	tokens := func(s db.TransferSide, match string) []string {
		var out []string
		for _, c := range s.Correlations {
			if slices.Contains(c.Match, match) {
				out = append(out, c.Token)
			}
		}
		return out
	}
	if got := tokens(departure, db.MatchOrdinal); !slices.Equal(got, []string{"971613411"}) {
		t.Errorf("departure references = %v, want [971613411]", got)
	}
	if got := tokens(departure, db.MatchAccount); len(got) != 0 {
		t.Errorf("departure pointers = %v, want none: only the receiving side names one", got)
	}
	if got := tokens(arrival, db.MatchAccount); !slices.Equal(got, []string{"AG10000001"}) {
		t.Errorf("arrival pointers = %v, want [AG10000001]", got)
	}
	// The ordinal is the number the converter took out of the reference, not
	// something the matcher parses back out of the token.
	for _, c := range arrival.Correlations {
		if slices.Contains(c.Match, db.MatchOrdinal) && (c.Ordinal == nil || *c.Ordinal != 971613414) {
			t.Errorf("arrival ordinal = %v, want 971613414", c.Ordinal)
		}
	}
}

// TestCreateTransferMatches_ClearsBothSides verifies a match takes both sides out of
// the unmatched set, which is what makes a residual TRANSFER_CLEARING balance mean
// an unmatched transfer rather than any transfer.
func TestCreateTransferMatches_ClearsBothSides(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := transferUser(t, p, "sub|tm-clear")
	from, to := transferFixture(t, p, userID, instID)

	n, err := p.CreateTransferMatches(ctx, []db.TransferMatch{{
		UserID: userID, FromGroupID: from, ToGroupID: to,
		InstrumentID: instID, Method: db.TransferMatchReference,
	}})
	if err != nil {
		t.Fatalf("create match: %v", err)
	}
	if n != 1 {
		t.Errorf("wrote %d matches, want 1", n)
	}
	sides, err := p.ListUnmatchedTransferSides(ctx, db.TransferSideOpts{UserID: userID})
	if err != nil {
		t.Fatalf("list sides: %v", err)
	}
	if len(sides) != 0 {
		t.Errorf("got %d unmatched sides after matching, want none", len(sides))
	}
	matches, err := p.ListTransferMatches(ctx, userID)
	if err != nil {
		t.Fatalf("list matches: %v", err)
	}
	if len(matches) != 1 || matches[0].FromGroupID != from || matches[0].ToGroupID != to {
		t.Errorf("stored match = %+v, want %s -> %s", matches, from, to)
	}
}

// TestCreateTransferMatches_IsIdempotent verifies a second pass over unchanged data
// writes nothing. The matcher is re-runnable because the second side of a transfer
// can arrive in a later import, so every cycle re-proposes what it already decided.
func TestCreateTransferMatches_IsIdempotent(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := transferUser(t, p, "sub|tm-idem")
	from, to := transferFixture(t, p, userID, instID)
	m := db.TransferMatch{UserID: userID, FromGroupID: from, ToGroupID: to,
		InstrumentID: instID, Method: db.TransferMatchReference}

	if _, err := p.CreateTransferMatches(ctx, []db.TransferMatch{m}); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	n, err := p.CreateTransferMatches(ctx, []db.TransferMatch{m})
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n != 0 {
		t.Errorf("second pass wrote %d matches, want 0", n)
	}

	// The same guard rejects a side being matched again against a different group,
	// which is what stops one arrival satisfying two departures.
	other := newTxGroup(t, p, userID)
	n, err = p.CreateTransferMatches(ctx, []db.TransferMatch{{
		UserID: userID, FromGroupID: other, ToGroupID: to,
		InstrumentID: instID, Method: db.TransferMatchReference,
	}})
	if err != nil {
		t.Fatalf("second claim on the arrival: %v", err)
	}
	if n != 0 {
		t.Errorf("a second claim on an already-matched side wrote %d, want 0", n)
	}
}

// TestTransferMatches_CascadeOnReupload verifies the link is disposable. Re-uploading
// a period replaces one side's groups; the link goes with them and the surviving side
// reappears as unmatched, so the matcher can pair it again without anything having to
// know a re-upload happened.
func TestTransferMatches_CascadeOnReupload(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, instID := transferUser(t, p, "sub|tm-cascade")
	from, to := transferFixture(t, p, userID, instID)
	if _, err := p.CreateTransferMatches(ctx, []db.TransferMatch{{
		UserID: userID, FromGroupID: from, ToGroupID: to,
		InstrumentID: instID, Method: db.TransferMatchReference,
	}}); err != nil {
		t.Fatalf("create match: %v", err)
	}

	// Replacing the period deletes both sides' groups, so both links and postings go.
	base := time.Date(2025, 4, 15, 0, 0, 0, 0, time.UTC)
	f, b := timestamppb.New(base), timestamppb.New(base.Add(24*time.Hour))
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", "", f, b, nil, nil, nil, nil); err != nil {
		t.Fatalf("replace with nothing: %v", err)
	}
	matches, err := p.ListTransferMatches(ctx, userID)
	if err != nil {
		t.Fatalf("list matches: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("got %d matches after the groups were replaced, want none", len(matches))
	}
}

// TestTransferMatches_SurviveAnInstrumentMerge verifies the link moves with the
// postings it names. A match is keyed on the commodity in flight, so one left behind
// would point at the instrument the merge deletes and the settled pair would surface
// as unmatched again.
func TestTransferMatches_SurviveAnInstrumentMerge(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|tm-merge", "U", "u@merge.com")
	// Two instruments that turn out to be one security. The transfer is posted
	// against the one that loses the merge -- the survivor is whichever carries more
	// identifiers -- so the rewrite is the thing under test rather than a no-op.
	mergedAway, err := p.EnsureInstrument(ctx, "", "", "", "", "", "",
		[]db.IdentifierInput{{Type: "ISIN", Value: "TM1", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure merged-away: %v", err)
	}
	if _, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "CUSIP", Value: "TM1", Canonical: true},
		{Type: "SEDOL", Value: "TM1", Canonical: true},
	}, "", nil, nil, nil); err != nil {
		t.Fatalf("ensure survivor: %v", err)
	}
	from, to := transferFixture(t, p, userID, mergedAway)
	if _, err := p.CreateTransferMatches(ctx, []db.TransferMatch{{
		UserID: userID, FromGroupID: from, ToGroupID: to,
		InstrumentID: mergedAway, Method: db.TransferMatchReference,
	}}); err != nil {
		t.Fatalf("create match: %v", err)
	}

	// Naming both identifiers at once is what merges them.
	survivor, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "ISIN", Value: "TM1", Canonical: true},
		{Type: "CUSIP", Value: "TM1", Canonical: true},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if survivor == mergedAway {
		t.Fatal("fixture did not merge away the instrument the match was keyed on")
	}

	matches, err := p.ListTransferMatches(ctx, userID)
	if err != nil {
		t.Fatalf("list matches: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches after the merge, want the pair to survive it", len(matches))
	}
	if matches[0].InstrumentID != survivor {
		t.Errorf("match instrument = %s, want the survivor %s", matches[0].InstrumentID, survivor)
	}
	// And the pair is still settled, which is the thing the report reads.
	sides, err := p.ListUnmatchedTransferSides(ctx, db.TransferSideOpts{UserID: userID})
	if err != nil {
		t.Fatalf("list sides: %v", err)
	}
	if len(sides) != 0 {
		t.Errorf("got %d unmatched sides after the merge, want none", len(sides))
	}
}
