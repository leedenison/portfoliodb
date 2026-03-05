package postgres

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/leedenison/portfoliodb/server/db/migrate"
	"github.com/leedenison/portfoliodb/server/migrations"
	_ "github.com/lib/pq"
)

func TestMain(m *testing.M) {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		conn, err := sql.Open("postgres", url)
		if err != nil {
			log.Fatalf("TestMain open db: %v", err)
		}
		defer conn.Close()
		if err := conn.Ping(); err != nil {
			log.Fatalf("TestMain ping: %v", err)
		}
		if err := migrate.Up(context.Background(), conn, migrations.Files); err != nil {
			log.Fatalf("TestMain migrate: %v", err)
		}
	}
	os.Exit(m.Run())
}

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
