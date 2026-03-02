package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"

	_ "github.com/lib/pq"
)

func testDB(t *testing.T) *Postgres {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run via make test-db)")
	}
	conn, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return New(conn)
}

// testDBTx returns a Postgres backed by a transaction that is rolled back when the test ends, so each test gets an isolated clean state without maintaining a table list.
func testDBTx(t *testing.T) *Postgres {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run via make test-db)")
	}
	conn, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return NewWithQueryable(tx)
}

func TestGetOrCreateUser(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	id1, err := p.GetOrCreateUser(ctx, "sub|1", "Alice", "a@b.com")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if id1 == "" {
		t.Fatal("expected non-empty user id")
	}
	id2, err := p.GetOrCreateUser(ctx, "sub|1", "Alice Updated", "a2@b.com")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("same auth_sub should return same id: %s != %s", id1, id2)
	}
}

func TestGetUserByAuthSub_ReturnsRole(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	id, err := p.GetOrCreateUser(ctx, "sub|role-test", "U", "u@u.com")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userID, role, err := p.GetUserByAuthSub(ctx, "sub|role-test")
	if err != nil {
		t.Fatalf("GetUserByAuthSub: %v", err)
	}
	if userID != id || role != "user" {
		t.Fatalf("GetUserByAuthSub: got userID=%q role=%q, want userID=%q role=user", userID, role, id)
	}
	// Unknown auth_sub returns empty id and role, no error
	userID2, role2, err := p.GetUserByAuthSub(ctx, "sub|nonexistent")
	if err != nil {
		t.Fatalf("GetUserByAuthSub nonexistent: %v", err)
	}
	if userID2 != "" || role2 != "" {
		t.Fatalf("GetUserByAuthSub nonexistent: got userID=%q role=%q", userID2, role2)
	}
}

func TestPortfolioCRUD(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|p", "U", "u@u.com")
	list, next, err := p.ListPortfolios(ctx, userID, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 || next != "" {
		t.Fatalf("initial list should be empty: %v %s", list, next)
	}
	port, err := p.CreatePortfolio(ctx, userID, "My Portfolio")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if port.GetId() == "" || port.GetName() != "My Portfolio" {
		t.Fatalf("create: %v", port)
	}
	port2, uid, err := p.GetPortfolio(ctx, port.GetId())
	if err != nil || port2 == nil || uid != userID {
		t.Fatalf("get: %v %v %v", err, port2, uid)
	}
	port3, err := p.UpdatePortfolio(ctx, port.GetId(), "Renamed")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if port3.GetName() != "Renamed" {
		t.Fatalf("update name: %s", port3.GetName())
	}
	ok, _ := p.PortfolioBelongsToUser(ctx, port.GetId(), userID)
	if !ok {
		t.Fatal("portfolio should belong to user")
	}
	if err := p.DeletePortfolio(ctx, port.GetId()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	port4, _, _ := p.GetPortfolio(ctx, port.GetId())
	if port4 != nil {
		t.Fatal("portfolio should be gone")
	}
}

func TestReplaceTxsInPeriod_and_ComputeHoldings(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|tx", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	instrumentIDs := []string{instID, instID}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, instrumentIDs)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, asOf, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	if asOf == nil {
		t.Fatal("asOf should be set")
	}
	var aaplQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.Quantity
			break
		}
	}
	if aaplQty != 7 {
		t.Fatalf("expected AAPL quantity 7 (10 + -3), got %v", aaplQty)
	}
}

// TestComputeHoldings_signedQuantity verifies holdings are SUM(quantity) with no type-based sign flip.
// Sells have negative quantity; buys positive. A position that is net short has negative holding.
func TestComputeHoldings_signedQuantity(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|hold", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))
	// Only a sell with negative quantity: no buys. Net position should be -5.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_SELLSTOCK, Quantity: -5, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{instID})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	var googQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" {
			googQty = h.Quantity
			break
		}
	}
	if googQty != -5 {
		t.Fatalf("expected GOOG quantity -5 (signed quantity, no type-based flip), got %v", googQty)
	}
}

func TestCreateTx_AppendOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|up", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	ts := timestamppb.Now()
	tx1 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: ""}
	tx2 := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "GOOG", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "", tx1, instID); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "", tx2, instID); err != nil {
		t.Fatalf("second create: %v", err)
	}
	holdings, _, _ := p.ComputeHoldings(ctx, userID, nil, "", nil)
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" && h.Quantity != 15 {
			t.Fatalf("append-only: expected total quantity 15, got %v", h.Quantity)
		}
	}
}

func TestCreateJob_GetJob(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|j", "U", "u@u.com")
	from := timestamppb.Now()
	to := timestamppb.Now()
	jobID, err := p.CreateJob(ctx, userID, "IBKR", "IBKR:test:statement", from, to)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected job id")
	}
	status, errs, idErrs, jobUserID, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if status != apiv1.JobStatus_PENDING || len(errs) != 0 || len(idErrs) != 0 || jobUserID != userID {
		t.Fatalf("get job: %v %v %v %s", status, errs, idErrs, jobUserID)
	}
	_ = p.SetJobStatus(ctx, jobID, apiv1.JobStatus_SUCCESS)
	_ = p.AppendValidationErrors(ctx, jobID, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	status2, errs2, idErrs2, _, _ := p.GetJob(ctx, jobID)
	if status2 != apiv1.JobStatus_SUCCESS || len(errs2) != 1 || len(idErrs2) != 0 {
		t.Fatalf("after update: %v %v %v", status2, errs2, idErrs2)
	}
}

// TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch verifies that when multiple identifiers
// resolve to different instruments (e.g. A has ISIN 1, B has CUSIP 1), EnsureInstrument merges
// them and returns the survivor; both identifiers end up on the survivor and txs are updated.
func TestEnsureInstrument_mergeWhenMultipleInstrumentsMatch(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create instrument A with (ISIN, 1) and B with (CUSIP, 1).
	idA, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "ISIN", Value: "1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "CUSIP", Value: "1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}
	if idA == idB {
		t.Fatal("A and B should be different instruments")
	}
	// Attach one tx to A and one to B.
	userID, _ := p.GetOrCreateUser(ctx, "sub|merge", "U", "u@u.com")
	_, _ = p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "StockA", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "StockB", Type: apiv1.TxType_BUYSTOCK, Quantity: 5, Account: ""},
	}
	err = p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txs, []string{idA, idB})
	if err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	// Resolve with identifiers that match both A and B; should merge and return survivor.
	brokerDesc := "SomeStock"
	result, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: brokerDesc, Canonical: false},
		{Type: "ISIN", Value: "1", Canonical: true},
		{Type: "CUSIP", Value: "1", Canonical: true},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure merge: %v", err)
	}
	if result != idA && result != idB {
		t.Fatalf("result %s should be either A %s or B %s", result, idA, idB)
	}
	survivor, mergedAway := result, idA
	if result == idA {
		mergedAway = idB
	}
	// Merged-away instrument should be gone.
	gone, _ := p.GetInstrument(ctx, mergedAway)
	if gone != nil {
		t.Fatalf("merged-away instrument %s should be deleted, got %+v", mergedAway, gone)
	}
	// Survivor should have both identifiers.
	row, err := p.GetInstrument(ctx, survivor)
	if err != nil || row == nil {
		t.Fatalf("get survivor: %v %v", err, row)
	}
	hasISIN, hasCUSIP := false, false
	for _, idn := range row.Identifiers {
		if idn.Type == "ISIN" && idn.Value == "1" {
			hasISIN = true
			if !idn.Canonical {
				t.Fatal("ISIN identifier should have Canonical true after merge")
			}
		}
		if idn.Type == "CUSIP" && idn.Value == "1" {
			hasCUSIP = true
			if !idn.Canonical {
				t.Fatal("CUSIP identifier should have Canonical true after merge")
			}
		}
		if idn.Type == "IBKR" && idn.Value == brokerDesc && idn.Canonical {
			t.Fatal("broker description identifier should have Canonical false after merge")
		}
	}
	if !hasISIN || !hasCUSIP {
		t.Fatalf("survivor should have both ISIN 1 and CUSIP 1, got %+v", row.Identifiers)
	}
	// Both txs should now point at survivor (holdings or re-query would show one instrument).
	holdings, _, err := p.ComputeHoldings(ctx, userID, nil, "", nil)
	if err != nil {
		t.Fatalf("holdings: %v", err)
	}
	// We had two txs (10 and 5) on two instruments; after merge both are on survivor, so one holding with quantity 15.
	var totalQty float64
	for _, h := range holdings {
		if h.InstrumentId == survivor {
			totalQty += h.Quantity
		}
	}
	if totalQty != 15 {
		t.Fatalf("expected merged holding quantity 15, got %v (holdings: %+v)", totalQty, holdings)
	}
}

func TestListInstrumentsForExport_ExcludesBrokerDescriptionOnly(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Instrument with only broker description (canonical=false) - should be excluded.
	brokerOnlyID, err := p.EnsureInstrument(ctx, "", "", "", "BrokerOnly", []db.IdentifierInput{{Type: "IBKR", Value: "BRK", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure broker-only: %v", err)
	}
	// Instrument with canonical identifier - should be included.
	withCanonID, err := p.EnsureInstrument(ctx, "EQUITY", "XNAS", "USD", "Apple", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
		{Type: "IBKR", Value: "AAPL", Canonical: false},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure with canonical: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 instrument (exclude broker-only), got %d", len(list))
	}
	if list[0].ID != withCanonID {
		t.Fatalf("expected instrument %s, got %s", withCanonID, list[0].ID)
	}
	if list[0].Name != "Apple" || len(list[0].Identifiers) != 2 {
		t.Fatalf("expected Apple with 2 identifiers, got %+v", list[0])
	}
	_ = brokerOnlyID
}

func TestListInstrumentsForExport_ExchangeFilter(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "EQUITY", "XNAS", "USD", "Nasdaq", []db.IdentifierInput{{Type: "ISIN", Value: "N1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure XNAS: %v", err)
	}
	_, err = p.EnsureInstrument(ctx, "EQUITY", "XNYS", "USD", "NYSE", []db.IdentifierInput{{Type: "ISIN", Value: "Y1", Canonical: true}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure XNYS: %v", err)
	}
	list, err := p.ListInstrumentsForExport(ctx, "XNAS")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport: %v", err)
	}
	if len(list) != 1 || list[0].Exchange != "XNAS" {
		t.Fatalf("expected 1 instrument with exchange XNAS, got %d (first exchange %q)", len(list), list[0].Exchange)
	}
	listAll, err := p.ListInstrumentsForExport(ctx, "")
	if err != nil {
		t.Fatalf("ListInstrumentsForExport all: %v", err)
	}
	if len(listAll) != 2 {
		t.Fatalf("expected 2 instruments with no filter, got %d", len(listAll))
	}
}

func TestEnsureInstrument_WithUnderlyingAndValidDates(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	// Create underlying first (EQUITY).
	underlyingID, err := p.EnsureInstrument(ctx, "EQUITY", "XNAS", "USD", "Apple Inc.", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
	}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure underlying: %v", err)
	}
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	validTo := time.Date(2025, 1, 17, 0, 0, 0, 0, time.UTC)
	// Create option with underlying_id and valid dates.
	optionID, err := p.EnsureInstrument(ctx, "OPTION", "SMART", "USD", "AAPL Call", []db.IdentifierInput{
		{Type: "IBKR", Value: "AAPL 20250117C200", Canonical: false},
	}, underlyingID, &validFrom, &validTo)
	if err != nil {
		t.Fatalf("ensure option: %v", err)
	}
	row, err := p.GetInstrument(ctx, optionID)
	if err != nil || row == nil {
		t.Fatalf("GetInstrument: %v", err)
	}
	if row.UnderlyingID != underlyingID {
		t.Errorf("UnderlyingID = %q, want %q", row.UnderlyingID, underlyingID)
	}
	if row.ValidFrom == nil || !row.ValidFrom.Equal(validFrom) {
		t.Errorf("ValidFrom = %v, want %v", row.ValidFrom, validFrom)
	}
	if row.ValidTo == nil || !row.ValidTo.Equal(validTo) {
		t.Errorf("ValidTo = %v, want %v", row.ValidTo, validTo)
	}
	// ListInstrumentsByIDs returns the option with same fields.
	rows, err := p.ListInstrumentsByIDs(ctx, []string{optionID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListInstrumentsByIDs: %v (len=%d)", err, len(rows))
	}
	if rows[0].UnderlyingID != underlyingID || rows[0].ValidFrom == nil {
		t.Errorf("ListInstrumentsByIDs row: UnderlyingID=%q ValidFrom=%v", rows[0].UnderlyingID, rows[0].ValidFrom)
	}
}

func TestEnsureInstrument_OptionWithoutUnderlying_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "OPTION", "SMART", "USD", "Option", []db.IdentifierInput{
		{Type: "IBKR", Value: "OPT1", Canonical: false},
	}, "", nil, nil)
	if err == nil {
		t.Fatal("expected error when OPTION has no underlying_id")
	}
	if err.Error() != "underlying_id required when asset_class is OPTION" {
		t.Errorf("got error: %v", err)
	}
}

func TestEnsureInstrument_InvalidAssetClass_Rejected(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	_, err := p.EnsureInstrument(ctx, "unknown", "XNAS", "USD", "X", []db.IdentifierInput{
		{Type: "IBKR", Value: "X", Canonical: false},
	}, "", nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid asset_class")
	}
}

func TestListPortfolioFilters_SetPortfolioFilters(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pf", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	list, err := p.ListPortfolioFilters(ctx, port.GetId())
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("initial filters should be empty: %v", list)
	}
	filters := []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "IBKR"},
		{FilterType: "account", FilterValue: "Acc1"},
	}
	if err := p.SetPortfolioFilters(ctx, port.GetId(), filters); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	list, err = p.ListPortfolioFilters(ctx, port.GetId())
	if err != nil {
		t.Fatalf("list after set: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 filters, got %v", list)
	}
	// Replace-all: set different filters
	filters2 := []db.PortfolioFilter{{FilterType: "broker", FilterValue: "SCHB"}}
	if err := p.SetPortfolioFilters(ctx, port.GetId(), filters2); err != nil {
		t.Fatalf("set filters 2: %v", err)
	}
	list, err = p.ListPortfolioFilters(ctx, port.GetId())
	if err != nil {
		t.Fatalf("list after replace: %v", err)
	}
	if len(list) != 1 || list[0].FilterType != "broker" || list[0].FilterValue != "SCHB" {
		t.Fatalf("expected single broker=SCHB filter, got %v", list)
	}
}

func TestListTxsByPortfolio_ComputeHoldingsForPortfolio(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|pv", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	// No filters: portfolio view should return no txs
	txs, tok, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio no filters: %v", err)
	}
	if len(txs) != 0 || tok != "" {
		t.Fatalf("no filters should return 0 txs, got %d %q", len(txs), tok)
	}
	holdings, asOf, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio no filters: %v", err)
	}
	if len(holdings) != 0 || asOf == nil {
		t.Fatalf("no filters holdings: %v asOf %v", holdings, asOf)
	}
	// Add broker=IBKR filter
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{{FilterType: "broker", FilterValue: "IBKR"}}); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txList := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10, Account: ""},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3, Account: ""},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "AAPL", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.ReplaceTxsInPeriod(ctx, userID, "IBKR", from, to, txList, []string{instID, instID}); err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	txs, tok, err = p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 2 || tok != "" {
		t.Fatalf("expected 2 txs, got %d nextToken=%q", len(txs), tok)
	}
	holdings, asOf, err = p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	if asOf == nil {
		t.Fatal("asOf should be set")
	}
	var aaplQty float64
	for _, h := range holdings {
		if h.InstrumentDescription == "AAPL" {
			aaplQty = h.Quantity
			break
		}
	}
	if aaplQty != 7 {
		t.Fatalf("expected AAPL quantity 7 (10-3), got %v", aaplQty)
	}
}

// TestListTxsByPortfolio_OR_dedupe verifies OR semantics and deduplication: a tx matching multiple filters appears once.
func TestListTxsByPortfolio_OR_dedupe(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|dedupe", "U", "u@u.com")
	port, err := p.CreatePortfolio(ctx, userID, "P")
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	// Filters: broker=IBKR OR account=A (so txs matching either appear; tx matching both appears once)
	if err := p.SetPortfolioFilters(ctx, port.GetId(), []db.PortfolioFilter{
		{FilterType: "broker", FilterValue: "IBKR"},
		{FilterType: "account", FilterValue: "A"},
	}); err != nil {
		t.Fatalf("set filters: %v", err)
	}
	now := time.Now()
	ts := timestamppb.New(now.Add(-1 * time.Hour))
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "X", Canonical: false}}, "", nil, nil)
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	// Tx1: IBKR, account "" -> matches broker only
	// Tx2: SCHB, account "A" -> matches account only
	// Tx3: IBKR, account "A" -> matches both, should appear once
	if err := p.CreateTx(ctx, userID, "IBKR", "", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 1, Account: ""}, instID); err != nil {
		t.Fatalf("create tx1: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "SCHB", "A", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 2, Account: "A"}, instID); err != nil {
		t.Fatalf("create tx2: %v", err)
	}
	if err := p.CreateTx(ctx, userID, "IBKR", "A", &apiv1.Tx{Timestamp: ts, InstrumentDescription: "X", Type: apiv1.TxType_BUYSTOCK, Quantity: 3, Account: "A"}, instID); err != nil {
		t.Fatalf("create tx3: %v", err)
	}
	txs, _, err := p.ListTxsByPortfolio(ctx, port.GetId(), nil, nil, 50, "")
	if err != nil {
		t.Fatalf("ListTxsByPortfolio: %v", err)
	}
	if len(txs) != 3 {
		t.Fatalf("expected 3 txs (OR + dedupe), got %d: %+v", len(txs), txs)
	}
	holdings, _, err := p.ComputeHoldingsForPortfolio(ctx, port.GetId(), nil)
	if err != nil {
		t.Fatalf("ComputeHoldingsForPortfolio: %v", err)
	}
	// One instrument, total quantity 1+2+3 = 6 (each tx counted once)
	var totalQty float64
	for _, h := range holdings {
		totalQty += h.Quantity
	}
	if totalQty != 6 {
		t.Fatalf("expected total quantity 6 (deduped txs), got %v", totalQty)
	}
}
