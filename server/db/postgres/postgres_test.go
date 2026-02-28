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
	port, _ := p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "AAPL", Canonical: false}})
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	instrumentIDs := []string{instID, instID}
	err = p.ReplaceTxsInPeriod(ctx, port.GetId(), "IBKR", from, to, txs, instrumentIDs)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, asOf, err := p.ComputeHoldings(ctx, port.GetId(), nil)
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
	port, _ := p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-1 * time.Hour))
	to := timestamppb.New(now)
	ts := timestamppb.New(now.Add(-30 * time.Minute))
	// Only a sell with negative quantity: no buys. Net position should be -5.
	txs := []*apiv1.Tx{
		{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_SELLSTOCK, Quantity: -5},
	}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "GOOG", Canonical: false}})
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	err = p.ReplaceTxsInPeriod(ctx, port.GetId(), "IBKR", from, to, txs, []string{instID})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	holdings, _, err := p.ComputeHoldings(ctx, port.GetId(), nil)
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

func TestUpsertTx_Idempotent(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|up", "U", "u@u.com")
	port, _ := p.CreatePortfolio(ctx, userID, "P")
	ts := timestamppb.Now()
	tx := &apiv1.Tx{Timestamp: ts, InstrumentDescription: "GOOG", Type: apiv1.TxType_BUYSTOCK, Quantity: 5}
	instID, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "IBKR", Value: "GOOG", Canonical: false}})
	if err != nil {
		t.Fatalf("ensure instrument: %v", err)
	}
	if err := p.UpsertTx(ctx, port.GetId(), "IBKR", tx, instID); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	tx.Quantity = 10
	if err := p.UpsertTx(ctx, port.GetId(), "IBKR", tx, instID); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	holdings, _, _ := p.ComputeHoldings(ctx, port.GetId(), nil)
	for _, h := range holdings {
		if h.InstrumentDescription == "GOOG" && h.Quantity != 10 {
			t.Fatalf("idempotent upsert should update quantity to 10, got %v", h.Quantity)
		}
	}
}

func TestCreateJob_GetJob(t *testing.T) {
	p := testDBTx(t)
	ctx := context.Background()
	userID, _ := p.GetOrCreateUser(ctx, "sub|j", "U", "u@u.com")
	port, _ := p.CreatePortfolio(ctx, userID, "P")
	from := timestamppb.Now()
	to := timestamppb.Now()
	jobID, err := p.CreateJob(ctx, port.GetId(), "IBKR", from, to)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected job id")
	}
	status, errs, idErrs, portID, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if status != apiv1.JobStatus_PENDING || len(errs) != 0 || len(idErrs) != 0 || portID != port.GetId() {
		t.Fatalf("get job: %v %v %v %s", status, errs, idErrs, portID)
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
	idA, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "ISIN", Value: "1", Canonical: true}})
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	idB, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{{Type: "CUSIP", Value: "1", Canonical: true}})
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}
	if idA == idB {
		t.Fatal("A and B should be different instruments")
	}
	// Attach one tx to A and one to B.
	userID, _ := p.GetOrCreateUser(ctx, "sub|merge", "U", "u@u.com")
	port, _ := p.CreatePortfolio(ctx, userID, "P")
	now := time.Now()
	from := timestamppb.New(now.Add(-2 * time.Hour))
	to := timestamppb.New(now)
	ts1 := timestamppb.New(now.Add(-90 * time.Minute))
	ts2 := timestamppb.New(now.Add(-30 * time.Minute))
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "StockA", Type: apiv1.TxType_BUYSTOCK, Quantity: 10},
		{Timestamp: ts2, InstrumentDescription: "StockB", Type: apiv1.TxType_BUYSTOCK, Quantity: 5},
	}
	err = p.ReplaceTxsInPeriod(ctx, port.GetId(), "IBKR", from, to, txs, []string{idA, idB})
	if err != nil {
		t.Fatalf("replace txs: %v", err)
	}
	// Resolve with identifiers that match both A and B; should merge and return survivor.
	brokerDesc := "SomeStock"
	result, err := p.EnsureInstrument(ctx, "", "", "", "", []db.IdentifierInput{
		{Type: "IBKR", Value: brokerDesc, Canonical: false},
		{Type: "ISIN", Value: "1", Canonical: true},
		{Type: "CUSIP", Value: "1", Canonical: true},
	})
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
	holdings, _, err := p.ComputeHoldings(ctx, port.GetId(), nil)
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
	brokerOnlyID, err := p.EnsureInstrument(ctx, "", "", "", "BrokerOnly", []db.IdentifierInput{{Type: "IBKR", Value: "BRK", Canonical: false}})
	if err != nil {
		t.Fatalf("ensure broker-only: %v", err)
	}
	// Instrument with canonical identifier - should be included.
	withCanonID, err := p.EnsureInstrument(ctx, "equity", "XNAS", "USD", "Apple", []db.IdentifierInput{
		{Type: "ISIN", Value: "US0378331005", Canonical: true},
		{Type: "IBKR", Value: "AAPL", Canonical: false},
	})
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
	_, err := p.EnsureInstrument(ctx, "equity", "XNAS", "USD", "Nasdaq", []db.IdentifierInput{{Type: "ISIN", Value: "N1", Canonical: true}})
	if err != nil {
		t.Fatalf("ensure XNAS: %v", err)
	}
	_, err = p.EnsureInstrument(ctx, "equity", "XNYS", "USD", "NYSE", []db.IdentifierInput{{Type: "ISIN", Value: "Y1", Canonical: true}})
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
