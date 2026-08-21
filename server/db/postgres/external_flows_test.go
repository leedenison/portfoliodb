package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/db"
)

// flow is the shape these tests assert on: the date, the commodity's description
// rather than its id, and the amount. The id is a UUID a fixture does not know, and
// the description is what says which commodity moved.
type flow struct {
	Date        string
	Description string
	Amount      decimal.Decimal
}

// flowsOf resolves each flow's commodity back to a description so a want literal can
// be written by hand.
func flowsOf(t *testing.T, p *Postgres, got []db.ExternalFlow) []flow {
	t.Helper()
	out := make([]flow, len(got))
	for i, f := range got {
		desc := f.InstrumentDescription
		if f.InstrumentID != "" {
			inst, err := p.GetInstrument(context.Background(), f.InstrumentID)
			if err != nil {
				t.Fatalf("get instrument %s: %v", f.InstrumentID, err)
			}
			if inst.Name == nil {
				t.Fatalf("instrument %s has no name", f.InstrumentID)
			}
			desc = *inst.Name
		}
		out[i] = flow{
			Date:        f.Date.Format("2006-01-02"),
			Description: desc,
			Amount:      f.Amount,
		}
	}
	return out
}

// decEq compares flows, whose amounts are decimals: 1.0 and 1.00 hold different
// big.Ints and are the same number.
var decEq = cmp.Comparer(func(a, b decimal.Decimal) bool { return a.Equal(b) })

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

// flowWindow is the window these tests read, wide enough to hold the whole April
// fixture and the days either side of it.
func flowWindow() (time.Time, time.Time) { return april(1), april(30) }

// postGroup writes one balanced group directly, for the leg shapes ingestion will
// not produce on demand: an EQUITY counterparty, an IMBALANCE residual, a USER leg
// in a second account. Each leg is (account, account_type, quantity), and the caller
// is responsible for their weights summing to zero.
func postGroup(t *testing.T, p *Postgres, userID, desc, instID string, at time.Time, legs ...[3]string) string {
	t.Helper()
	ctx := context.Background()
	var groupID string
	if err := p.q.QueryRowContext(ctx, `
		INSERT INTO tx_groups (user_id, timestamp) VALUES ($1::uuid, $2::timestamptz)
		RETURNING id`, userID, at).Scan(&groupID); err != nil {
		t.Fatalf("create group: %v", err)
	}
	for _, leg := range legs {
		if _, err := p.q.ExecContext(ctx, `
			INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
				instrument_id, broker_tx_type, resolved_tx_type, quantity, account_type,
				group_id, weight, weight_commodity, share_count_basis, split_adjusted_quantity)
			VALUES ($1::uuid, 'FIDELITY', $2, $3::timestamptz, $3::timestamptz, $4, $5::uuid,
				ARRAY['TRANSFER'], 'TRANSFER', $6::numeric, $7, $8::uuid, $6::numeric,
				'inst:'||$5, $3::timestamptz::date, $6::numeric)
		`, userID, leg[0], at, desc, instID, leg[2], leg[1], groupID); err != nil {
			t.Fatalf("insert %s leg: %v", leg[1], err)
		}
	}
	return groupID
}

// usdCash is the seeded US dollar cash instrument, the commodity these transfers
// move.
func usdCash(t *testing.T, p *Postgres) string {
	t.Helper()
	id, err := p.FindInstrumentByIdentifier(context.Background(), "CURRENCY", "", "USD")
	if err != nil || id == "" {
		t.Fatalf("USD cash instrument not found: %v", err)
	}
	return id
}

// TestGetPortfolioExternalFlows_MatchedPairBothMembersNets verifies the correctness
// consequence adr/0022 names: a transfer between two accounts of one portfolio is
// not a flow at all, so money-weighted return does not read it as a withdrawal and an
// unrelated deposit.
func TestGetPortfolioExternalFlows_MatchedPairBothMembersNets(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-nets", "U", "u@flow-nets.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no flows, got %v", flowsOf(t, p, got))
	}
}

// TestGetPortfolioExternalFlows_MatchedPairOneMemberIsExternal verifies that a
// matched pair still leaves a portfolio that holds only one of the two accounts.
// From that portfolio's point of view the money really did go.
func TestGetPortfolioExternalFlows_MatchedPairOneMemberIsExternal(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-onemember", "U", "u@flow-onemember.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	port := accountPortfolio(t, p, userID, "Departure", spec.fromAcct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{{Date: "2025-04-15", Description: "USD", Amount: dec("-20000")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_UnmatchedPairIsTwoFlows verifies what an unmatched
// transfer reads as: the withdrawal followed by an unrelated deposit that
// adr/0022 says makes money-weighted return wrong for a multi-account portfolio.
func TestGetPortfolioExternalFlows_UnmatchedPairIsTwoFlows(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-unmatched", "U", "u@flow-unmatched.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	transferFixtureAt(t, p, userID, spec)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{
		{Date: "2025-04-15", Description: "USD", Amount: dec("-20000")},
		{Date: "2025-04-20", Description: "USD", Amount: dec("20000")},
	}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_UnmatchedPairOnOneDateCancels verifies that two
// unmatched sides landing on the same day report nothing: no value crossed the
// boundary that day, whatever the matcher does or does not know about the pair.
func TestGetPortfolioExternalFlows_UnmatchedPairOnOneDateCancels(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-sameday", "U", "u@flow-sameday.com")
	spec := inFlightSpec(t, p, april(15), april(15))
	transferFixtureAt(t, p, userID, spec)
	port := accountPortfolio(t, p, userID, "Both", spec.fromAcct, spec.toAcct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no flows, got %v", flowsOf(t, p, got))
	}
}

// TestGetPortfolioExternalFlows_SecurityTransferFlowsInShares verifies that a
// journal moving shares reports a flow in shares. A contribution in specie is not a
// cash amount, and converting it would need a price.
func TestGetPortfolioExternalFlows_SecurityTransferFlowsInShares(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-shares", "U", "u@flow-shares.com")
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "FIDELITY", Value: "AAPL Corp", Canonical: false},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	spec := transferSpec{
		instID: instID, desc: "AAPL Corp", qty: "-10",
		depart: april(15), arrive: april(20),
		fromAcct: "AG10000001", toAcct: "AW10000001",
	}
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, instID)
	port := accountPortfolio(t, p, userID, "Departure", spec.fromAcct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{{Date: "2025-04-15", Description: "AAPL Corp", Amount: dec("-10")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_IncomeAndCostAreNotFlows verifies the bullet list in
// docs/spec/postings.md: income, charges and residuals are return and cost rather
// than contribution. Treating them as external would strip dividends out of the
// return and report it gross of fees.
func TestGetPortfolioExternalFlows_IncomeAndCostAreNotFlows(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-income", "U", "u@flow-income.com")
	cashID := usdCash(t, p)
	acct := "AG10000001"
	postGroup(t, p, userID, "USD CASH", cashID, april(5),
		[3]string{acct, "USER", "100"}, [3]string{acct, "INCOME", "-100"})
	postGroup(t, p, userID, "USD CASH", cashID, april(6),
		[3]string{acct, "USER", "-9"}, [3]string{acct, "EXPENSE", "9"})
	postGroup(t, p, userID, "USD CASH", cashID, april(7),
		[3]string{acct, "USER", "3"}, [3]string{acct, "IMBALANCE", "-3"})
	postGroup(t, p, userID, "USD CASH", cashID, april(8),
		[3]string{acct, "USER", "1"}, [3]string{acct, "SOURCE_ROUNDING", "-1"})
	port := accountPortfolio(t, p, userID, "One", acct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no flows, got %v", flowsOf(t, p, got))
	}
}

// TestGetPortfolioExternalFlows_NonMemberGroupProducesNothing verifies the guard
// that keeps a group with no member leg out. Without it a dividend in an account the
// portfolio does not hold has exactly one leg that is outside and external -- its
// own USER leg -- and reports as a withdrawal from a portfolio the group never
// touched.
func TestGetPortfolioExternalFlows_NonMemberGroupProducesNothing(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-nonmember", "U", "u@flow-nonmember.com")
	cashID := usdCash(t, p)
	postGroup(t, p, userID, "USD CASH", cashID, april(5),
		[3]string{"AG10000001", "USER", "100"}, [3]string{"AG10000001", "INCOME", "-100"})
	// The portfolio is a different account entirely, with an event of its own so it
	// is not merely empty.
	postGroup(t, p, userID, "USD CASH", cashID, april(6),
		[3]string{"AX10000001", "USER", "50"}, [3]string{"AX10000001", "INCOME", "-50"})
	port := accountPortfolio(t, p, userID, "Other", "AX10000001")

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no flows, got %v", flowsOf(t, p, got))
	}
}

// TestGetPortfolioExternalFlows_OpeningBalanceIsAContribution verifies that a pad's
// EQUITY counterparty reports as value entering. Without it a declared opening
// position has no flow behind it and money-weighted return is infinite.
func TestGetPortfolioExternalFlows_OpeningBalanceIsAContribution(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-opening", "U", "u@flow-opening.com")
	cashID := usdCash(t, p)
	acct := "AG10000001"
	postGroup(t, p, userID, "USD CASH", cashID, april(5),
		[3]string{acct, "USER", "20000"}, [3]string{acct, "EQUITY", "-20000"})
	port := accountPortfolio(t, p, userID, "One", acct)

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{{Date: "2025-04-05", Description: "USD", Amount: dec("20000")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_UserToUserNetsWhenBothAreMembers verifies the case
// the boundary states without a clearing account in it: a group whose two USER legs
// are in different accounts is external to each of them, and internal to a portfolio
// that holds both.
func TestGetPortfolioExternalFlows_UserToUserNetsWhenBothAreMembers(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-usertouser", "U", "u@flow-usertouser.com")
	cashID := usdCash(t, p)
	postGroup(t, p, userID, "USD CASH", cashID, april(5),
		[3]string{"AG10000001", "USER", "-100"}, [3]string{"AW10000001", "USER", "100"})

	f, b := flowWindow()
	both := accountPortfolio(t, p, userID, "Both", "AG10000001", "AW10000001")
	got, err := p.GetPortfolioExternalFlows(ctx, both, f, b)
	if err != nil {
		t.Fatalf("external flows, both members: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no flows for a portfolio holding both, got %v", flowsOf(t, p, got))
	}

	one := accountPortfolio(t, p, userID, "One", "AG10000001")
	got, err = p.GetPortfolioExternalFlows(ctx, one, f, b)
	if err != nil {
		t.Fatalf("external flows, one member: %v", err)
	}
	want := []flow{{Date: "2025-04-05", Description: "USD", Amount: dec("-100")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_WindowExcludesItsUpperBound verifies the half-open
// range. Both bounds are applied here, where valuation applies only the upper one: a
// flow is an event and belongs to its date, where a valuation is a state and needs
// the history before the window.
func TestGetPortfolioExternalFlows_WindowExcludesItsUpperBound(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-window", "U", "u@flow-window.com")
	cashID := usdCash(t, p)
	acct := "AG10000001"
	for _, day := range []int{9, 10, 14, 15} {
		postGroup(t, p, userID, "USD CASH", cashID, april(day),
			[3]string{acct, "USER", "10"}, [3]string{acct, "EQUITY", "-10"})
	}
	port := accountPortfolio(t, p, userID, "One", acct)

	got, err := p.GetPortfolioExternalFlows(ctx, port, april(10), april(15))
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{
		{Date: "2025-04-10", Description: "USD", Amount: dec("10")},
		{Date: "2025-04-14", Description: "USD", Amount: dec("10")},
	}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_GroupStraddlingTheWindowStillReports verifies that
// the window bounds the flows rather than the groups. A group's legs need not share a
// date, so the member leg that brings a group into scope can fall outside the window
// the external leg falls inside.
func TestGetPortfolioExternalFlows_GroupStraddlingTheWindowStillReports(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-straddle", "U", "u@flow-straddle.com")
	cashID := usdCash(t, p)
	acct := "AG10000001"
	groupID := postGroup(t, p, userID, "USD CASH", cashID, april(5),
		[3]string{acct, "USER", "500"})
	// The EQUITY counterparty settles five days later, inside the window the member
	// leg is outside.
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
			instrument_id, broker_tx_type, resolved_tx_type, quantity, account_type,
			group_id, weight, weight_commodity, share_count_basis, split_adjusted_quantity)
		VALUES ($1::uuid, 'FIDELITY', $2, $3::timestamptz, $3::timestamptz, 'USD CASH', $4::uuid,
			ARRAY['TRANSFER'], 'TRANSFER', -500, 'EQUITY', $5::uuid, -500,
			'inst:'||$4, $3::timestamptz::date, -500)
	`, userID, acct, april(12), cashID, groupID); err != nil {
		t.Fatalf("insert equity leg: %v", err)
	}
	port := accountPortfolio(t, p, userID, "One", acct)

	got, err := p.GetPortfolioExternalFlows(ctx, port, april(10), april(15))
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{{Date: "2025-04-12", Description: "USD", Amount: dec("500")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetPortfolioExternalFlows_InstrumentFilterSeesTheCashLeg verifies that
// membership is per posting rather than per account, which an instrument filter makes
// visible: a buy's cash leg is not a member of an instrument-scoped portfolio, so the
// cash reads as a flow into it. That is the right answer for such a portfolio -- the
// cash bought in -- and it agrees with valuation, which does not value that cash
// either.
func TestGetPortfolioExternalFlows_InstrumentFilterSeesTheCashLeg(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-instfilter", "U", "u@flow-instfilter.com")
	cashID := usdCash(t, p)
	instID, err := p.EnsureInstrument(ctx, "STOCK", "", "USD", "AAPL", "", "", []db.IdentifierInput{
		{Type: "BROKER_DESCRIPTION", Domain: "FIDELITY", Value: "AAPL Corp", Canonical: false},
	}, nil, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	acct := "AG10000001"
	groupID := postGroup(t, p, userID, "AAPL Corp", instID, april(5),
		[3]string{acct, "USER", "10"})
	if _, err := p.q.ExecContext(ctx, `
		INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description,
			instrument_id, broker_tx_type, resolved_tx_type, quantity, account_type,
			group_id, weight, weight_commodity, share_count_basis, split_adjusted_quantity)
		VALUES ($1::uuid, 'FIDELITY', $2, $3::timestamptz, $3::timestamptz, 'USD CASH', $4::uuid,
			ARRAY['TRADE_CASH'], 'TRADE_CASH', -1855, 'USER', $5::uuid, -10,
			'inst:'||$6, $3::timestamptz::date, -1855)
	`, userID, acct, april(5), cashID, groupID, instID); err != nil {
		t.Fatalf("insert cash leg: %v", err)
	}

	port, err := p.CreatePortfolio(ctx, userID, "AAPL only")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if err := p.SetPortfolioFilters(ctx, port.Id, []db.PortfolioFilter{
		{FilterType: "instrument", FilterValue: instID},
	}); err != nil {
		t.Fatalf("set filters: %v", err)
	}

	f, b := flowWindow()
	got, err := p.GetPortfolioExternalFlows(ctx, port.Id, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{{Date: "2025-04-05", Description: "USD", Amount: dec("1855")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetUserExternalFlows_MatchedPairNetsAndEquityDoesNot verifies the user-scoped
// query, where every account is in scope: a matched transfer is internal wherever it
// went, and an EQUITY leg is still value entering or leaving the holdings entirely.
func TestGetUserExternalFlows_MatchedPairNetsAndEquityDoesNot(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-user", "U", "u@flow-user.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	from, to := transferFixtureAt(t, p, userID, spec)
	matchTransfer(t, p, userID, from, to, spec.instID)
	postGroup(t, p, userID, "USD CASH", spec.instID, april(5),
		[3]string{spec.fromAcct, "USER", "20000"}, [3]string{spec.fromAcct, "EQUITY", "-20000"})

	f, b := flowWindow()
	got, err := p.GetUserExternalFlows(ctx, userID, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{{Date: "2025-04-05", Description: "USD", Amount: dec("20000")}}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetUserExternalFlows_UnmatchedTransferIsExternal verifies that an unmatched
// side is a flow even for a user, because one half is all that is known: the money
// left for somewhere, and asserting otherwise would launder it out of the return.
func TestGetUserExternalFlows_UnmatchedTransferIsExternal(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()

	userID, _ := p.GetOrCreateUser(ctx, "sub|flow-user-unmatched", "U", "u@flow-user-unmatched.com")
	spec := inFlightSpec(t, p, april(15), april(20))
	transferFixtureAt(t, p, userID, spec)

	f, b := flowWindow()
	got, err := p.GetUserExternalFlows(ctx, userID, f, b)
	if err != nil {
		t.Fatalf("external flows: %v", err)
	}
	want := []flow{
		{Date: "2025-04-15", Description: "USD", Amount: dec("-20000")},
		{Date: "2025-04-20", Description: "USD", Amount: dec("20000")},
	}
	if diff := cmp.Diff(want, flowsOf(t, p, got), decEq); diff != "" {
		t.Errorf("flows (-want +got):\n%s", diff)
	}
}

// TestGetExternalFlows_RejectsAMalformedScopeID pins the error the service layer
// relies on rather than a driver-level failure deeper in.
func TestGetExternalFlows_RejectsAMalformedScopeID(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	f, b := flowWindow()
	if _, err := p.GetPortfolioExternalFlows(ctx, "not-a-uuid", f, b); err == nil {
		t.Error("expected an error for a malformed portfolio id")
	}
	if _, err := p.GetUserExternalFlows(ctx, "not-a-uuid", f, b); err == nil {
		t.Error("expected an error for a malformed user id")
	}
}
