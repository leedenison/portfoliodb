package postgres

import (
	"context"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
	"time"
)

// residualSeed is one residual posting to seed. Each is appended as its own group,
// which is what the routing path produces for a one-sided source row.
type residualSeed struct {
	broker      string
	account     string
	instID      string
	txType      typev1.TxType
	accountType typev1.AccountType
	qty         string
	daysAgo     int
}

func seedResiduals(t *testing.T, p *Postgres, userID string, seeds ...residualSeed) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, s := range seeds {
		tx := &apiv1.Tx{
			OrderDate:             timestamppb.New(now.AddDate(0, 0, -s.daysAgo)),
			TradeDate:             timestamppb.New(now.AddDate(0, 0, -s.daysAgo)),
			InstrumentDescription: "residual",
			BrokerTxType:          []typev1.TxType{s.txType},
			ResolvedTxType:        s.txType,
			Quantity:              s.qty,
			AccountType:           s.accountType,
		}
		if err := createTx(ctx, p, userID, s.broker, s.account, "", tx, s.instID, nil); err != nil {
			t.Fatalf("seed residual %s/%s: %v", s.broker, s.account, err)
		}
	}
}

func usdInstrument(t *testing.T, p *Postgres) string {
	t.Helper()
	id, _, err := p.EnsureInstrument(context.Background(), "CASH", "USD", "USD", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "CURRENCY", Value: "USD"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure USD: %v", err)
	}
	return id
}

func newUser(t *testing.T, p *Postgres, sub string) string {
	t.Helper()
	id, err := p.GetOrCreateUser(context.Background(), sub, "U", sub+"@u.com")
	if err != nil {
		t.Fatalf("create user %s: %v", sub, err)
	}
	return id
}

// findBalance returns the single balance matching broker, account and resolved
// tx type.
func findBalance(t *testing.T, rows []db.ResidualBalance, broker typev1.Broker, account string, txType typev1.TxType) db.ResidualBalance {
	t.Helper()
	var found []db.ResidualBalance
	for _, r := range rows {
		if r.Broker == broker && r.Account == account && r.ResolvedTxType == txType {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 balance for %v/%s/%v, got %d (rows: %+v)", broker, account, txType, len(found), rows)
	}
	return found[0]
}

// TestListResidualBalances_GroupsByTxType verifies the breakdown the report exists
// for: an uncategorised dividend and a missing fee under the same broker account
// are separate rows, because they lead to different converter work.
func TestListResidualBalances_GroupsByTxType(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|grouping")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-100", 1},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-50", 2},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRADE_ASSET, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "3.5", 3},
	)

	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	income := findBalance(t, rows, typev1.Broker_IBKR, "A1", typev1.TxType_INCOME)
	if income.Balance.String() != "-150" {
		t.Errorf("income balance = %v, want -150", income.Balance)
	}
	if income.PostingCount != 2 {
		t.Errorf("income posting count = %d, want 2", income.PostingCount)
	}
	if income.Commodity != "USD" {
		t.Errorf("income commodity = %q, want USD", income.Commodity)
	}
	if income.AssetClass != "CASH" {
		t.Errorf("income asset class = %q, want CASH", income.AssetClass)
	}
	if income.AccountType != typev1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Errorf("income account type = %v, want IMBALANCE", income.AccountType)
	}
	buy := findBalance(t, rows, typev1.Broker_IBKR, "A1", typev1.TxType_TRADE_ASSET)
	if buy.Balance.String() != "3.5" {
		t.Errorf("buy balance = %v, want 3.5", buy.Balance)
	}
}

// TestListResidualBalances_OffsettingImbalancesDrop verifies that a key whose
// residuals cancel is not reported: there is nothing left to fix.
func TestListResidualBalances_OffsettingImbalancesDrop(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|offset")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "100", 1},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-100", 2},
	)

	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %+v", rows)
	}
}

// TestListResidualBalances_SecurityCommodity verifies a residual in shares reports
// the ticker and its asset class, not the currency the instrument trades in. The
// report would otherwise render a share count as money.
func TestListResidualBalances_SecurityCommodity(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|security")
	stockID, _, err := p.EnsureInstrument(context.Background(), "STOCK", "USD", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "MIC_TICKER", Value: "AAPL", Domain: "XNAS"},
			Canonical: true,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure stock: %v", err)
	}

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", stockID, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "50", 1},
	)

	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %+v", rows)
	}
	if rows[0].Commodity != "AAPL" {
		t.Errorf("commodity = %q, want AAPL", rows[0].Commodity)
	}
	if rows[0].AssetClass != "STOCK" {
		t.Errorf("asset class = %q, want STOCK (a quantity, not money)", rows[0].AssetClass)
	}
}

// TestListResidualBalances_MatchedTransferIsSettled verifies a paired journal drops
// out of the report. Both sides are TRANSFER_CLEARING postings in different accounts,
// so before matching a settled transfer and one whose second side never arrived were
// the same shape and both were listed -- which is why the page carried a caveat
// saying it listed every imported transfer. A matched side is settled: the value is
// in the other account and the balance is an artefact of the two statements.
func TestListResidualBalances_MatchedTransferIsSettled(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := newUser(t, p, "sub|settled")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "500", 40},
		residualSeed{"IBKR", "A2", usd, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "-500", 38},
	)

	// Unmatched, both sides are reported: nothing yet says they belong together.
	rows, err := p.ListResidualBalances(ctx, db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("before matching: expected both sides reported, got %+v", rows)
	}
	out := findBalance(t, rows, typev1.Broker_IBKR, "A1", typev1.TxType_TRANSFER)
	if out.Balance.String() != "500" {
		t.Errorf("A1 balance = %v, want 500", out.Balance)
	}
	if out.Oldest == nil || out.Newest == nil {
		t.Error("expected the contributing postings to be dated")
	}

	from, to := groupOf(t, p, userID, "A1"), groupOf(t, p, userID, "A2")
	if _, err := p.CreateTransferMatches(ctx, []db.TransferMatch{{
		UserID: userID, FromGroupID: from, ToGroupID: to, InstrumentID: usd,
		Method: db.TransferMatchReference,
	}}); err != nil {
		t.Fatalf("create match: %v", err)
	}

	rows, err = p.ListResidualBalances(ctx, db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list after matching: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("after matching: expected the settled pair to drop out, got %+v", rows)
	}

	// And the dashboard stops counting it, so a number above zero is something to
	// act on rather than a restatement of how many transfers were imported.
	_, stale, err := p.CountResidualBalances(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if stale != 0 {
		t.Errorf("stale transfer count = %d, want 0: the pair is settled", stale)
	}
}

// groupOf returns the tx group of the residual posting seeded into one account.
func groupOf(t *testing.T, p *Postgres, userID, account string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(),
		`SELECT group_id FROM txs WHERE user_id = $1::uuid AND account = $2`, userID, account).Scan(&id)
	if err != nil {
		t.Fatalf("group of %s: %v", account, err)
	}
	return id
}

// TestListResidualBalances_ImbalancesDoNotNetAcrossAccounts verifies imbalances are
// not netted the way transfers are. Two offsetting converter defects are two
// defects, and netting them would report neither.
func TestListResidualBalances_ImbalancesDoNotNetAcrossAccounts(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|noimbnet")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "500", 5},
		residualSeed{"IBKR", "A2", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-500", 5},
	)

	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected both imbalances, got %+v", rows)
	}
}

// TestListResidualBalances_PeriodBounds verifies the window is half-open.
func TestListResidualBalances_PeriodBounds(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|period")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-10", 30},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRADE_ASSET, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-20", 2},
	)

	from := time.Now().UTC().AddDate(0, 0, -10)
	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{From: &from})
	if err != nil {
		t.Fatalf("list from: %v", err)
	}
	if len(rows) != 1 || rows[0].ResolvedTxType != typev1.TxType_TRADE_ASSET {
		t.Fatalf("expected only the recent residual, got %+v", rows)
	}

	before := time.Now().UTC().AddDate(0, 0, -10)
	rows, err = p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{Before: &before})
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(rows) != 1 || rows[0].ResolvedTxType != typev1.TxType_INCOME {
		t.Fatalf("expected only the old residual, got %+v", rows)
	}
}

// TestListResidualBalances_AccountTypeFilter verifies each tab can ask for its own
// rows.
func TestListResidualBalances_AccountTypeFilter(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|filter")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-10", 2},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "500", 2},
	)

	ctx := context.Background()
	rows, err := p.ListResidualBalances(ctx, db.ResidualBalanceOpts{AccountType: typev1.AccountType_ACCOUNT_TYPE_IMBALANCE})
	if err != nil {
		t.Fatalf("list imbalance: %v", err)
	}
	if len(rows) != 1 || rows[0].AccountType != typev1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Fatalf("expected only the imbalance, got %+v", rows)
	}
	rows, err = p.ListResidualBalances(ctx, db.ResidualBalanceOpts{AccountType: typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING})
	if err != nil {
		t.Fatalf("list transfers: %v", err)
	}
	if len(rows) != 1 || rows[0].AccountType != typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING {
		t.Fatalf("expected only the transfer, got %+v", rows)
	}
}

// TestResidualBalances_SourceRoundingIsReportedNotCounted verifies where a rounding
// residual lands: aggregated in the report, where the per-broker total and posting
// count are the only place its cost is visible, and absent from the dashboard counts,
// which ask whether something is wrong. A source rounding its own cash figures is not.
func TestResidualBalances_SourceRoundingIsReportedNotCounted(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|rounding")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRADE_ASSET, typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING, "0.0028", 1},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRADE_ASSET, typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING, "-0.0011", 2},
	)

	ctx := context.Background()
	rows, err := p.ListResidualBalances(ctx, db.ResidualBalanceOpts{AccountType: typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING})
	if err != nil {
		t.Fatalf("list rounding: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected the two postings to aggregate to 1 row, got %+v", rows)
	}
	if rows[0].Balance.String() != "0.0017" {
		t.Errorf("balance = %v, want 0.0017", rows[0].Balance)
	}
	if rows[0].PostingCount != 2 {
		t.Errorf("posting count = %d, want 2", rows[0].PostingCount)
	}

	imbalances, stale, err := p.CountResidualBalances(ctx, time.Now().UTC().AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if imbalances != 0 || stale != 0 {
		t.Errorf("counts = (%d, %d), want (0, 0): rounding is not a fault", imbalances, stale)
	}
}

// TestCountResidualBalances_StalenessBoundary verifies a recently imported transfer
// is quiet and an old one is loud. Every imbalance counts whatever its age.
func TestCountResidualBalances_StalenessBoundary(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|stale")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, typev1.TxType_INCOME, typev1.AccountType_ACCOUNT_TYPE_IMBALANCE, "-10", 1},
		residualSeed{"IBKR", "A1", usd, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "100", 6},
		residualSeed{"IBKR", "A2", usd, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "200", 8},
	)

	staleBefore := time.Now().UTC().AddDate(0, 0, -7)
	imbalances, stale, err := p.CountResidualBalances(context.Background(), staleBefore)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if imbalances != 1 {
		t.Errorf("imbalances = %d, want 1", imbalances)
	}
	if stale != 1 {
		t.Errorf("stale transfers = %d, want 1 (the 8-day-old side, not the 6-day-old one)", stale)
	}
}

// TestListResidualBalances_ShareResidualAcrossSplit is why the aggregate reads the
// split-adjusted column. A residual left by an unpaired share transfer is a quantity of
// shares, and the two postings below straddle a 2:1 split, so their raw quantities
// are in different share counts. Added raw they net to -100 and the report claims a
// share residual nobody left behind; converted, the transfer balances and there is
// nothing to report.
func TestListResidualBalances_ShareResidualAcrossSplit(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := newUser(t, p, "sub|residual-split")
	instID, _, err := p.EnsureInstrument(ctx, "", "", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "JRN", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	// 400 days ago is before the split, 10 days ago after it.
	addSplit(t, p, instID, time.Now().UTC().AddDate(0, 0, -200), 1, 2)
	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", instID, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "100", 400},
		residualSeed{"IBKR", "A1", instID, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "-200", 10},
	)
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute split adjustments: %v", err)
	}

	rows, err := p.ListResidualBalances(ctx, db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no residual balances, got %+v", rows)
	}
}

// TestListResidualBalances_ShareResidualReportedInTodaysShareCount is the other
// half: a real residual is reported, and in the share count the rest of the system
// uses. Half the transferred position came back, which is 100 of today's shares.
func TestListResidualBalances_ShareResidualReportedInTodaysShareCount(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID := newUser(t, p, "sub|residual-split-open")
	instID, _, err := p.EnsureInstrument(ctx, "", "", "", "", "",
		[]db.IdentifierInput{{
			Ref:       db.InstrumentRef{Type: "BROKER_DESCRIPTION", Value: "JRN2", Domain: "IBKR"},
			Canonical: false,
		}}, nil, "", nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	addSplit(t, p, instID, time.Now().UTC().AddDate(0, 0, -200), 1, 2)
	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", instID, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "100", 400},
		residualSeed{"IBKR", "A1", instID, typev1.TxType_TRANSFER, typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, "-100", 10},
	)
	if err := p.RecomputeSplitAdjustments(ctx, instID); err != nil {
		t.Fatalf("recompute split adjustments: %v", err)
	}

	rows, err := p.ListResidualBalances(ctx, db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := findBalance(t, rows, typev1.Broker_IBKR, "A1", typev1.TxType_TRANSFER)
	if got.Balance.String() != "100" {
		t.Errorf("balance = %v, want 100 (200 post-split shares in, 100 out)", got.Balance)
	}
}
