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
// seed row, and CreateTxGroup takes a slice so that the append path can carry a
// routed counterparty alongside the posting it balances.
func createTx(ctx context.Context, p *Postgres, userID, broker, account, jobID string, tx *apiv1.Tx, instrumentID string, shareCountBasis *time.Time) error {
	return p.CreateTxGroup(ctx, userID, broker, account, jobID, []*apiv1.Tx{tx}, []string{instrumentID}, shareCountBasis)
}

// decf builds a decimal from a float literal, which is what a price test fixture
// is naturally written as. Production code never converts this way -- the
// provider seam in server/pricefetcher does it once, deliberately.
func decf(v float64) decimal.Decimal { return decimal.NewFromFloat(v) }
