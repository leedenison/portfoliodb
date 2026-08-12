package postgres

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/migrate"
	"github.com/leedenison/portfoliodb/server/migrations"
	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"
)

func TestMain(m *testing.M) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		conn, err := sql.Open("postgres", url)
		if err != nil {
			log.Fatalf("TestMain open db: %v", err)
		}
		defer conn.Close()
		// Retry ping to handle Postgres still running init scripts.
		for range 10 {
			if err = conn.Ping(); err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			log.Fatalf("TestMain ping: %v", err)
		}
		if err := migrate.Up(context.Background(), conn, migrations.Files); err != nil {
			log.Fatalf("TestMain migrate: %v", err)
		}
	}
	os.Exit(m.Run())
}

// testDBTx returns a Postgres backed by a transaction that is rolled back when the test ends, so each test gets an isolated clean state without maintaining a table list.
func testDBTx(t *testing.T) *Postgres {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set (run via make test-db)")
	}
	conn, err := sqlx.Open("postgres", url)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	tx, err := conn.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return NewWithQueryable(tx)
}

// createTx appends a single posting as its own group. Most tests only need one
// seed row, and CreateTxGroup takes a slice so that a caller can hand over the
// several legs of one event together.
//
// The posting is given a weight of zero, so its group balances on its own and the
// store writes no counterparty for it. That keeps a fixture to the one row it asked
// for: these tests are about what is stored and read back, not about what a group
// owes, and the store settling every group it is handed would otherwise put a
// routed leg beside every seed row. The tests that are about balancing state their
// weights, which is what makes them about it.
func createTx(ctx context.Context, p *Postgres, userID, broker, account, jobID string, tx *apiv1.Tx, instrumentID string, shareCountBasis *time.Time) error {
	return p.CreateTxGroup(ctx, userID, broker, account, jobID, []*apiv1.Tx{tx}, []string{instrumentID}, weightlessFor([]string{instrumentID}), []*time.Time{shareCountBasis})
}

// weightlessFor is a weight per posting that contributes nothing, for a fixture
// whose subject is not what its group owes. See createTx.
func weightlessFor(instrumentIDs []string) []db.Weight {
	out := make([]db.Weight, len(instrumentIDs))
	for i, id := range instrumentIDs {
		out[i] = db.Weight{Amount: decimal.Zero, Commodity: "inst:" + id}
	}
	return out
}

// newTxGroup creates an empty tx group and returns its id, for the fixtures that
// write a posting with raw SQL because the normal path cannot produce it -- an
// account_type outside the vocabulary, or a NULL instrument_id. Every posting
// belongs to a group, so those fixtures need one too.
func newTxGroup(t *testing.T, p *Postgres, userID string) string {
	t.Helper()
	var id string
	err := p.q.QueryRowContext(context.Background(), `
		INSERT INTO tx_groups (user_id, timestamp) VALUES ($1::uuid, now()) RETURNING id
	`, userID).Scan(&id)
	if err != nil {
		t.Fatalf("create tx group: %v", err)
	}
	return id
}

// decf builds a decimal from a float literal, which is what a price test fixture
// is naturally written as. Production code never converts this way -- the
// provider seam in server/pricefetcher does it once, deliberately.
func decf(v float64) decimal.Decimal { return decimal.NewFromFloat(v) }
