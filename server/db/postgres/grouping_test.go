package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// groupingFixture is one user with an instrument, ready for postings.
type groupingFixture struct {
	p      *Postgres
	userID string
	instID string
	ctx    context.Context
}

func newGroupingFixture(t *testing.T, sub string) *groupingFixture {
	t.Helper()
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|"+sub, "U", sub+"@grouping.test")
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "FIDELITY", Value: "GRP-" + sub, Canonical: false},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	return &groupingFixture{p: p, userID: userID, instID: instID, ctx: ctx}
}

// write stores one posting through the ordinary ingestion path, so the fixture
// exercises what an upload actually produces rather than raw SQL.
func (f *groupingFixture) write(t *testing.T, account string, ts time.Time, qty string, declared []typev1.TxType, cs ...*archivev1.Correlation) {
	t.Helper()
	tx := &apiv1.Tx{
		OrderDate:             timestamppb.New(ts),
		TradeDate:             timestamppb.New(ts),
		InstrumentDescription: "GRP",
		BrokerTxType:          declared,
		ResolvedTxType:        declared[0],
		Quantity:              qty,
		Account:               account,
		Correlations:          cs,
	}
	if err := createTx(f.ctx, f.p, f.userID, "FIDELITY", account, "", tx, f.instID, nil); err != nil {
		t.Fatalf("write posting: %v", err)
	}
}

// writeWithResidual stores a posting the store has to route a counterparty for,
// which is the shape an unmatched transfer actually has in the database: the
// transcribed leg plus a TRANSFER_CLEARING one holding the value in transit.
//
// The counterparty is not written here. The store routes one for whatever a group
// fails to balance to, and a lone leg fails to balance to all of it, so writing the
// leg is the whole of the fixture. residual names the type that produces.
func (f *groupingFixture) writeWithResidual(t *testing.T, account string, ts time.Time, qty string, declared []typev1.TxType, residual typev1.AccountType) {
	t.Helper()
	tx := &apiv1.Tx{
		OrderDate:             timestamppb.New(ts),
		TradeDate:             timestamppb.New(ts),
		InstrumentDescription: "GRP",
		BrokerTxType:          declared,
		ResolvedTxType:        declared[0],
		Quantity:              qty,
		Account:               account,
	}
	// Weighed at its quantity rather than at nothing, which is what leaves the
	// group short and gives the store something to route.
	w := []db.Weight{{Amount: decimal.RequireFromString(qty), Commodity: "inst:" + f.instID}}
	err := f.p.CreateTxGroup(f.ctx, f.userID, "FIDELITY", account, "",
		[]*apiv1.Tx{tx}, []string{f.instID}, w, []*time.Time{nil})
	if err != nil {
		t.Fatalf("write posting with residual: %v", err)
	}
	if got := f.residualTypeOf(t, account, ts); got != residual {
		t.Fatalf("routed a %s counterparty, want %s: the fixture's declared set decides it", got, residual)
	}
}

// residualTypeOf reads the account type the store routed for a posting's group, so
// writeWithResidual can say what shape it built rather than assuming one.
func (f *groupingFixture) residualTypeOf(t *testing.T, account string, ts time.Time) typev1.AccountType {
	t.Helper()
	var s string
	err := f.p.q.QueryRowContext(f.ctx, `
		SELECT r.account_type FROM txs r
		JOIN txs l ON l.group_id = r.group_id AND l.synthetic_purpose IS NULL
		WHERE r.user_id = $1::uuid AND r.synthetic_purpose = $2
		  AND l.account = $3 AND l.order_date = $4
		LIMIT 1`, f.userID, db.RoutedPurpose, account, ts).Scan(&s)
	if err != nil {
		t.Fatalf("read routed counterparty: %v", err)
	}
	return db.StrToAccountType(s)
}

func refCorrelation(token string, ordinal, span int64) *archivev1.Correlation {
	return &archivev1.Correlation{
		Token:       token,
		Ordinal:     &ordinal,
		OrdinalSpan: &span,
		Scope:       typev1.Scope_SCOPE_FILE,
		Match:       []typev1.Match{typev1.Match_MATCH_EXACT, typev1.Match_MATCH_ORDINAL},
	}
}

func idsOf(ps []db.GroupingPosting) map[string]db.GroupingPosting {
	out := map[string]db.GroupingPosting{}
	for _, p := range ps {
		out[p.ID] = p
	}
	return out
}

// A reach on a token is answered from the index over (label, token), which is the
// lookup a grouping pass makes: it starts from an identifier and asks who else holds
// it.
func TestPostingsByToken(t *testing.T) {
	f := newGroupingFixture(t, "token")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.write(t, "A1", ts, "-100", []typev1.TxType{typev1.TxType_TRADE_ASSET}, refCorrelation("shared", 100, 8))
	f.write(t, "A1", ts, "1000", []typev1.TxType{typev1.TxType_TRADE_CASH}, refCorrelation("shared", 101, 8))
	f.write(t, "A1", ts, "5", []typev1.TxType{typev1.TxType_HOLDING_COST}, refCorrelation("other", 900, 8))

	got, err := f.p.PostingsByToken(f.ctx, f.userID, []db.TokenQuery{{
		Broker:  typev1.Broker_FIDELITY,
		Account: "A1",
		Token:   "shared",
	}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d postings, want the 2 sharing the token", len(got))
	}
	// The evidence comes back with them, or a rule would have nothing to compare.
	for _, p := range got {
		if len(p.Correlations) != 1 || p.Correlations[0].Token != "shared" {
			t.Fatalf("posting %s carries %v, want the shared token", p.ID, p.Correlations)
		}
	}
}

// An account-scoped token means nothing outside its account, so the reach that serves
// it must not cross one either.
func TestPostingsByToken_StaysInItsAccount(t *testing.T) {
	f := newGroupingFixture(t, "tokenacct")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.write(t, "A1", ts, "-100", []typev1.TxType{typev1.TxType_TRADE_ASSET}, refCorrelation("shared", 100, 8))
	f.write(t, "A2", ts, "1000", []typev1.TxType{typev1.TxType_TRADE_CASH}, refCorrelation("shared", 101, 8))

	narrow, err := f.p.PostingsByToken(f.ctx, f.userID, []db.TokenQuery{{
		Broker: typev1.Broker_FIDELITY, Account: "A1", Token: "shared",
	}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(narrow) != 1 {
		t.Fatalf("account-scoped reach got %d postings, want 1", len(narrow))
	}

	wide, err := f.p.PostingsByToken(f.ctx, f.userID, []db.TokenQuery{{
		Broker: typev1.Broker_FIDELITY, Account: "A1", Token: "shared", AnyAccount: true,
	}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(wide) != 2 {
		t.Fatalf("broker-scoped reach got %d postings, want 2", len(wide))
	}
}

// The trade rules pair within one account on one day, and the range is half-open as
// every interval in this system is.
func TestPostingsByDates(t *testing.T) {
	f := newGroupingFixture(t, "dates")
	day := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	f.write(t, "A1", day.Add(9*time.Hour), "-100", []typev1.TxType{typev1.TxType_TRADE_ASSET})
	f.write(t, "A1", day.Add(15*time.Hour), "1000", []typev1.TxType{typev1.TxType_TRADE_CASH})
	f.write(t, "A1", day.AddDate(0, 0, 1), "7", []typev1.TxType{typev1.TxType_HOLDING_COST})

	got, err := f.p.PostingsByDates(f.ctx, f.userID, []db.DateQuery{{
		Broker:  typev1.Broker_FIDELITY,
		Account: "A1",
		From:    day,
		Before:  day.AddDate(0, 0, 1),
	}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d postings, want the 2 inside the day", len(got))
	}
}

// A deposit run finds its steps by reference proximity, bounded by the span the
// source declared, and is not bounded by a date at all.
func TestPostingsByOrdinals(t *testing.T) {
	f := newGroupingFixture(t, "ordinals")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.write(t, "A1", ts, "20000", []typev1.TxType{typev1.TxType_TRANSFER}, refCorrelation("700000100", 700000100, 8))
	// Three days later, which a date bucket would exclude and a reference span does
	// not.
	f.write(t, "A1", ts.AddDate(0, 0, 3), "-20000", []typev1.TxType{typev1.TxType_TRADE_CASH}, refCorrelation("700000103", 700000103, 8))
	f.write(t, "A1", ts, "-20000", []typev1.TxType{typev1.TxType_TRADE_CASH}, refCorrelation("700000200", 700000200, 8))

	got, err := f.p.PostingsByOrdinals(f.ctx, f.userID, []db.OrdinalQuery{{
		Broker:  typev1.Broker_FIDELITY,
		Account: "A1",
		Low:     700000100,
		High:    700000108,
	}}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d postings, want the 2 inside the span", len(got))
	}
}

// The closure asks only for what it does not hold, so a posting is never read twice
// and the rounds shrink to nothing.
func TestPostingsByToken_ExcludesHeld(t *testing.T) {
	f := newGroupingFixture(t, "held")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.write(t, "A1", ts, "-100", []typev1.TxType{typev1.TxType_TRADE_ASSET}, refCorrelation("shared", 100, 8))
	f.write(t, "A1", ts, "1000", []typev1.TxType{typev1.TxType_TRADE_CASH}, refCorrelation("shared", 101, 8))

	q := []db.TokenQuery{{Broker: typev1.Broker_FIDELITY, Account: "A1", Token: "shared"}}
	all, err := f.p.PostingsByToken(f.ctx, f.userID, q, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d postings, want 2", len(all))
	}
	rest, err := f.p.PostingsByToken(f.ctx, f.userID, q, []string{all[0].ID})
	if err != nil {
		t.Fatalf("list excluding held: %v", err)
	}
	if len(rest) != 1 || rest[0].ID != all[1].ID {
		t.Fatalf("got %v, want only %s", idsOf(rest), all[1].ID)
	}
}

// A routed residual transcribes nothing, so there is no evidence to say which
// postings it belongs with; it is re-routed after the partition rather than
// partitioned.
func TestListGroupingSeeds_ExcludesRoutedResiduals(t *testing.T) {
	f := newGroupingFixture(t, "residual")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	// A transfer and the TRANSFER_CLEARING leg holding its value in transit: two
	// rows in the database, one of them transcribed.
	f.writeWithResidual(t, "A1", ts, "500", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)

	got, err := f.p.ListGroupingSeeds(f.ctx, db.GroupingSeedOpts{UserID: f.userID})
	if err != nil {
		t.Fatalf("seeds: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d seeds, want only the transcribed posting", len(got))
	}
	if !got[0].Quantity.Equal(decf(500)) {
		t.Fatalf("seed quantity = %s, want 500", got[0].Quantity)
	}
}

// The cadence trigger starts from groups holding something a missing leg would
// explain. A group balanced but for the source's own rounding is not one of those.
func TestListGroupingSeeds_ResidualOnly(t *testing.T) {
	f := newGroupingFixture(t, "seedresidual")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	// An unmatched transfer: something a missing leg would explain.
	f.writeWithResidual(t, "A1", ts, "500", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)
	// A trade whose only residual is the source disagreeing with itself, which is
	// not a reason to go looking. Below the commodity tolerance, since the weight
	// this fixture writes is in the instrument rather than in money.
	f.writeWithResidual(t, "A2", ts, "0.0000001", []typev1.TxType{typev1.TxType_TRADE_ASSET},
		typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING)

	all, err := f.p.ListGroupingSeeds(f.ctx, db.GroupingSeedOpts{UserID: f.userID})
	if err != nil {
		t.Fatalf("seeds: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d unfiltered seeds, want 2", len(all))
	}

	got, err := f.p.ListGroupingSeeds(f.ctx, db.GroupingSeedOpts{UserID: f.userID, Residual: true})
	if err != nil {
		t.Fatalf("seeds: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d seeds, want only the unmatched transfer", len(got))
	}
	if got[0].Account != "A1" {
		t.Fatalf("seeded from %s, want the account holding the unmatched transfer", got[0].Account)
	}
}
