package postgres

import (
	"context"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// residualSeed is one residual posting to seed. Each is appended as its own group,
// which is what the routing path produces for a one-sided source row.
type residualSeed struct {
	broker      string
	account     string
	instID      string
	txType      apiv1.TxType
	accountType apiv1.AccountType
	qty         float64
	daysAgo     int
}

func seedResiduals(t *testing.T, p *Postgres, userID string, seeds ...residualSeed) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, s := range seeds {
		tx := &apiv1.Tx{
			Timestamp:             timestamppb.New(now.AddDate(0, 0, -s.daysAgo)),
			InstrumentDescription: "residual",
			Type:                  s.txType,
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
	id, err := p.EnsureInstrument(context.Background(), "CASH", "", "USD", "USD", "", "",
		[]db.IdentifierInput{{Type: "CURRENCY", Value: "USD", Canonical: true}}, "", nil, nil, nil)
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

// findBalance returns the single balance matching broker, account and tx type.
func findBalance(t *testing.T, rows []db.ResidualBalance, broker apiv1.Broker, account string, txType apiv1.TxType) db.ResidualBalance {
	t.Helper()
	var found []db.ResidualBalance
	for _, r := range rows {
		if r.Broker == broker && r.Account == account && r.TxType == txType {
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
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -100, 1},
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -50, 2},
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_BUYSTOCK, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, 3.5, 3},
	)

	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	income := findBalance(t, rows, apiv1.Broker_IBKR, "A1", apiv1.TxType_INCOME)
	if income.Balance != -150 {
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
	if income.AccountType != apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Errorf("income account type = %v, want IMBALANCE", income.AccountType)
	}
	buy := findBalance(t, rows, apiv1.Broker_IBKR, "A1", apiv1.TxType_BUYSTOCK)
	if buy.Balance != 3.5 {
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
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, 100, 1},
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -100, 2},
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
	stockID, err := p.EnsureInstrument(context.Background(), "STOCK", "", "USD", "", "", "",
		[]db.IdentifierInput{{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL", Canonical: true}}, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure stock: %v", err)
	}

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", stockID, apiv1.TxType_JRNLSEC, apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, 50, 1},
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

// TestListResidualBalances_SettledTransferIsStillReported pins the limitation this
// report has until transfers are matched (0068): both sides of a completed journal
// are TRANSFER_CLEARING postings in different accounts, nothing pairs them, and the
// report cannot tell a settled transfer from one whose second side never arrived.
// Both sides are reported. Update this test when 0068 makes them distinguishable.
func TestListResidualBalances_SettledTransferIsStillReported(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|settled")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_JRNLFUND, apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, 500, 40},
		residualSeed{"IBKR", "A2", usd, apiv1.TxType_JRNLFUND, apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, -500, 38},
	)

	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected both sides reported, got %+v", rows)
	}
	out := findBalance(t, rows, apiv1.Broker_IBKR, "A1", apiv1.TxType_JRNLFUND)
	if out.Balance != 500 {
		t.Errorf("A1 balance = %v, want 500", out.Balance)
	}
	if out.Oldest == nil || out.Newest == nil {
		t.Error("expected the contributing postings to be dated")
	}
}

// TestListResidualBalances_ImbalancesDoNotNetAcrossAccounts verifies imbalances are
// not netted the way transfers are. Two offsetting converter defects are two
// defects, and netting them would report neither.
func TestListResidualBalances_ImbalancesDoNotNetAcrossAccounts(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|noimbnet")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, 500, 5},
		residualSeed{"IBKR", "A2", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -500, 5},
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
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -10, 30},
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_BUYSTOCK, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -20, 2},
	)

	from := time.Now().UTC().AddDate(0, 0, -10)
	rows, err := p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{From: &from})
	if err != nil {
		t.Fatalf("list from: %v", err)
	}
	if len(rows) != 1 || rows[0].TxType != apiv1.TxType_BUYSTOCK {
		t.Fatalf("expected only the recent residual, got %+v", rows)
	}

	before := time.Now().UTC().AddDate(0, 0, -10)
	rows, err = p.ListResidualBalances(context.Background(), db.ResidualBalanceOpts{Before: &before})
	if err != nil {
		t.Fatalf("list before: %v", err)
	}
	if len(rows) != 1 || rows[0].TxType != apiv1.TxType_INCOME {
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
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -10, 2},
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_JRNLFUND, apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, 500, 2},
	)

	ctx := context.Background()
	rows, err := p.ListResidualBalances(ctx, db.ResidualBalanceOpts{AccountType: apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE})
	if err != nil {
		t.Fatalf("list imbalance: %v", err)
	}
	if len(rows) != 1 || rows[0].AccountType != apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE {
		t.Fatalf("expected only the imbalance, got %+v", rows)
	}
	rows, err = p.ListResidualBalances(ctx, db.ResidualBalanceOpts{AccountType: apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING})
	if err != nil {
		t.Fatalf("list transfers: %v", err)
	}
	if len(rows) != 1 || rows[0].AccountType != apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING {
		t.Fatalf("expected only the transfer, got %+v", rows)
	}
}

// TestCountResidualBalances_StalenessBoundary verifies a recently imported transfer
// is quiet and an old one is loud. Every imbalance counts whatever its age.
func TestCountResidualBalances_StalenessBoundary(t *testing.T) {
	p := testDBTx(t)
	userID := newUser(t, p, "sub|stale")
	usd := usdInstrument(t, p)

	seedResiduals(t, p, userID,
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_INCOME, apiv1.AccountType_ACCOUNT_TYPE_IMBALANCE, -10, 1},
		residualSeed{"IBKR", "A1", usd, apiv1.TxType_JRNLFUND, apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, 100, 6},
		residualSeed{"IBKR", "A2", usd, apiv1.TxType_JRNLFUND, apiv1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING, 200, 8},
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
