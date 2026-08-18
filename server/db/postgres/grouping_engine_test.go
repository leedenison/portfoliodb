// The real engine wired into the store, which is the arrangement the process runs
// and the one every other fixture in this package substitutes away.
//
// oneGroupSettler stands in wherever a test needs a multi-leg group without caring
// how it was decided, and server/grouping's own tests call Partition and
// Neighbourhood directly over postings they built in memory. Neither reaches
// Engine.Settle, and neither reaches the seam it sits in: the seed read back out of
// the write, the neighbourhood grown over the transaction in progress, and the
// changes applied before it commits. An upload's partition is decided there and
// nowhere else, so these are the tests that say it works.

package postgres

import (
	"context"
	"sort"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/grouping"
)

// engineFixture is a store with the production settler wired in, plus a user and an
// instrument to hang postings on.
func engineFixture(t *testing.T, sub string) (*Postgres, string, string) {
	t.Helper()
	p := testDBTx(t).WithSettler(grouping.NewEngine())
	ctx := context.Background()
	userID, err := p.GetOrCreateUser(ctx, "sub|"+sub, "U", sub+"@engine.test")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "ENGINE", Value: "ENG-" + sub, Canonical: false},
	}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	return p, userID, instID
}

// leg is one posting as a source would state it. The postings here are weightless,
// so every partition balances and the store routes nothing: what these tests are
// about is which postings end up together, not what their group owes.
func leg(ts time.Time, account, qty string, declared typev1.TxType, cs ...*archivev1.Correlation) *apiv1.Tx {
	return &apiv1.Tx{
		OrderDate:             timestamppb.New(ts),
		TradeDate:             timestamppb.New(ts),
		InstrumentDescription: "ENG",
		BrokerTxType:          []typev1.TxType{declared},
		ResolvedTxType:        declared,
		Quantity:              qty,
		Account:               account,
		Correlations:          cs,
	}
}

// fileRef is the shape a Fidelity reference number arrives in: comparable within the
// file that supplied it, which once the rows are stored is the job that ingested them.
func fileRef(token string) *archivev1.Correlation {
	return &archivev1.Correlation{
		Token: token,
		Scope: typev1.Scope_SCOPE_FILE,
		Match: []typev1.Match{typev1.Match_MATCH_EXACT},
	}
}

// fitID is the shape an OFX FITID arrives in: the record's own identifier, stamped
// on every leg the parser reads out of it, and comparable across the whole account
// rather than only within one file.
func fitID(token string) *archivev1.Correlation {
	return &archivev1.Correlation{
		Token: token,
		Scope: typev1.Scope_SCOPE_ACCOUNT,
		Match: []typev1.Match{typev1.Match_MATCH_EXACT},
	}
}

// partition returns the groups a user's transcribed postings ended up in, each as its
// members' quantities, sorted so the result does not depend on the ids postgres
// happened to issue.
func partition(t *testing.T, p *Postgres, userID string) [][]string {
	t.Helper()
	rows, err := p.q.QueryContext(context.Background(), `
		SELECT group_id::text, quantity::text FROM txs
		WHERE user_id = $1::uuid AND synthetic_purpose IS NULL
		ORDER BY group_id, quantity`, userID)
	if err != nil {
		t.Fatalf("read partition: %v", err)
	}
	defer rows.Close()
	byGroup := map[string][]string{}
	var order []string
	for rows.Next() {
		var group, qty string
		if err := rows.Scan(&group, &qty); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if _, seen := byGroup[group]; !seen {
			order = append(order, group)
		}
		byGroup[group] = append(byGroup[group], qty)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	out := make([][]string, 0, len(order))
	for _, g := range order {
		out = append(out, byGroup[g])
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func equalPartition(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// The ordinary import: a source states its own grouping as a shared reference, and
// the legs are together by the time the write that carried them commits. Nothing
// waits for the cadence, which is the whole reason the store holds a settler at all.
func TestGroupWritten_JoinsTheLegsOfOneUpload(t *testing.T) {
	p, userID, instID := engineFixture(t, "engine-upload")
	ctx := context.Background()
	base := time.Date(2025, 4, 15, 12, 0, 0, 0, time.UTC)
	from, to := timestamppb.New(base.Add(-time.Hour)), timestamppb.New(base.Add(time.Hour))

	// The job matters: a FILE-scoped reference is comparable within the file that
	// supplied it, and the job is what a file becomes once its rows are postings.
	jobID, err := p.CreateJob(ctx, db.CreateJobParams{
		UserID: userID, JobType: "tx", Broker: "FIDELITY", Source: "Fidelity:web:fidelity-csv",
		Filename: "export.csv", PeriodFrom: from, PeriodBefore: to,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	txs := []*apiv1.Tx{
		leg(base, "A", "10", typev1.TxType_TRADE_ASSET, fileRef("971613414")),
		leg(base, "A", "-1855", typev1.TxType_TRADE_CASH, fileRef("971613414")),
	}
	ids := []string{instID, instID}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", jobID, from, to, txs, ids, weightlessFor(ids), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	want := [][]string{{"-1855", "10"}}
	if got := partition(t, p, userID); !equalPartition(got, want) {
		t.Errorf("partition: got %v, want %v", got, want)
	}
}

// The seam a settler that only sees the seed cannot reach: the leg that arrives
// second joins the one already stored, because Settle grows a neighbourhood over the
// transaction in progress rather than partitioning what it was handed.
//
// A FITID would not do this. It is comparable across the account, so the two legs are
// one record however many uploads they arrived in -- which is exactly the case a
// broker's incremental export produces.
func TestGroupWritten_JoinsALegToOneAlreadyStored(t *testing.T) {
	p, userID, instID := engineFixture(t, "engine-later")
	ctx := context.Background()
	base := time.Date(2025, 4, 15, 12, 0, 0, 0, time.UTC)
	token := "20250415U10000011234567890"

	first := leg(base, "U1000001", "10", typev1.TxType_TRADE_ASSET, fitID(token))
	if err := createTx(ctx, p, userID, "IBKR", "U1000001", "", first, instID, nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if got := countGroups(t, p, userID); got != 1 {
		t.Fatalf("groups after the first leg: got %d, want 1", got)
	}

	second := leg(base, "U1000001", "-1855", typev1.TxType_TRADE_CASH, fitID(token))
	if err := createTx(ctx, p, userID, "IBKR", "U1000001", "", second, instID, nil); err != nil {
		t.Fatalf("second write: %v", err)
	}

	want := [][]string{{"-1855", "10"}}
	if got := partition(t, p, userID); !equalPartition(got, want) {
		t.Errorf("partition: got %v, want %v", got, want)
	}
	if got := countGroups(t, p, userID); got != 1 {
		t.Errorf("groups: got %d, want the one the legs were joined into", got)
	}
}

// The other half of the same claim, and the half a stand-in settler gets wrong: two
// records that arrived together are still two events. A settler that puts everything
// a write stored into one group passes the tests above and fails this one.
func TestGroupWritten_LeavesUnrelatedRecordsApart(t *testing.T) {
	p, userID, instID := engineFixture(t, "engine-apart")
	ctx := context.Background()
	base := time.Date(2025, 4, 15, 12, 0, 0, 0, time.UTC)
	from, to := timestamppb.New(base.Add(-time.Hour)), timestamppb.New(base.Add(time.Hour))

	jobID, err := p.CreateJob(ctx, db.CreateJobParams{
		UserID: userID, JobType: "tx", Broker: "FIDELITY", Source: "Fidelity:web:fidelity-csv",
		Filename: "export.csv", PeriodFrom: from, PeriodBefore: to,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	// Two trades in one file, each stating its own reference. The amounts are far
	// apart and the references are not adjacent, so no rule below Exact has anything
	// to say about them either.
	txs := []*apiv1.Tx{
		leg(base, "A", "10", typev1.TxType_TRADE_ASSET, fileRef("971613414")),
		leg(base, "A", "-1855", typev1.TxType_TRADE_CASH, fileRef("971613414")),
		leg(base, "A", "20", typev1.TxType_TRADE_ASSET, fileRef("880000001")),
		leg(base, "A", "-9400", typev1.TxType_TRADE_CASH, fileRef("880000001")),
	}
	ids := []string{instID, instID, instID, instID}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "FIDELITY", jobID, from, to, txs, ids, weightlessFor(ids), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	want := [][]string{
		{"-1855", "10"},
		{"-9400", "20"},
	}
	if got := partition(t, p, userID); !equalPartition(got, want) {
		t.Errorf("partition: got %v, want %v", got, want)
	}
}
