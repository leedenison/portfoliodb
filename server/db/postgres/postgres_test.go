package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
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
	// Quantity is signed: positive = buy, negative = sell. No type-based sign flip.
	txs := []*apiv1.Tx{
		{Timestamp: ts1, InstrumentDescription: "AAPL", Type: apiv1.TxType_BUYSTOCK, Quantity: 10},
		{Timestamp: ts2, InstrumentDescription: "AAPL", Type: apiv1.TxType_SELLSTOCK, Quantity: -3},
	}
	err := p.ReplaceTxsInPeriod(ctx, port.GetId(), "IBKR", from, to, txs)
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
	err := p.ReplaceTxsInPeriod(ctx, port.GetId(), "IBKR", from, to, txs)
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
	if err := p.UpsertTx(ctx, port.GetId(), "IBKR", tx); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	tx.Quantity = 10
	if err := p.UpsertTx(ctx, port.GetId(), "IBKR", tx); err != nil {
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
	status, errs, portID, err := p.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if status != apiv1.JobStatus_PENDING || len(errs) != 0 || portID != port.GetId() {
		t.Fatalf("get job: %v %v %s", status, errs, portID)
	}
	_ = p.SetJobStatus(ctx, jobID, apiv1.JobStatus_SUCCESS)
	_ = p.AppendValidationErrors(ctx, jobID, []*apiv1.ValidationError{{RowIndex: 0, Field: "x", Message: "y"}})
	status2, errs2, _, _ := p.GetJob(ctx, jobID)
	if status2 != apiv1.JobStatus_SUCCESS || len(errs2) != 1 {
		t.Fatalf("after update: %v %v", status2, errs2)
	}
}
