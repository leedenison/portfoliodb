// Package migrate runs versioned SQL migrations from an fs.FS (e.g. embedded server/migrations)
// and records applied versions in schema_migrations. Use an advisory lock so only one process applies.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"io/fs"
	"regexp"
	"sort"
	"time"
)

// Advisory lock ID for migration (arbitrary constant; must be same across all server processes).
const advisoryLockID int64 = 0x706f72746f6c6962 // portfoliodb-ish, fits int64

// Up creates schema_migrations if needed, acquires an advisory lock, applies any pending
// migrations from the FS (files matching NNN_name.sql, applied in name order), then releases the lock.
// Pending migrations are run in a transaction each; on success the version is recorded.
func Up(ctx context.Context, db *sql.DB, migrations fs.FS) error {
	if err := createSchemaMigrations(ctx, db); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	acquired, err := tryAdvisoryLock(ctx, db, 30*time.Second, advisoryLockID)
	if err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	if !acquired {
		return fmt.Errorf("advisory lock: timeout waiting for migration lock")
	}
	defer releaseAdvisoryLock(context.Background(), db, advisoryLockID)

	names, err := listMigrations(migrations)
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range names {
		version := versionFromName(name)
		if version == "" {
			continue
		}
		applied, err := isApplied(ctx, db, version)
		if err != nil {
			return fmt.Errorf("check applied %s: %w", version, err)
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, db, migrations, name, version); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

// UpPlugin applies pending migrations from the given fs.FS for a specific plugin.
// It uses plugin_schema_migrations (plugin_id, version) for tracking and a
// per-plugin advisory lock to prevent concurrent application.
func UpPlugin(ctx context.Context, db *sql.DB, pluginID string, migrations fs.FS) error {
	if err := createPluginSchemaMigrations(ctx, db); err != nil {
		return fmt.Errorf("create plugin_schema_migrations: %w", err)
	}
	lockID := pluginAdvisoryLockID(pluginID)
	acquired, err := tryAdvisoryLock(ctx, db, 30*time.Second, lockID)
	if err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	if !acquired {
		return fmt.Errorf("advisory lock: timeout waiting for plugin migration lock")
	}
	defer releaseAdvisoryLock(context.Background(), db, lockID)

	names, err := listMigrations(migrations)
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	for _, name := range names {
		version := versionFromName(name)
		if version == "" {
			continue
		}
		applied, err := isPluginApplied(ctx, db, pluginID, version)
		if err != nil {
			return fmt.Errorf("check applied %s: %w", version, err)
		}
		if applied {
			continue
		}
		if err := applyPluginMigration(ctx, db, migrations, name, pluginID, version); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func createPluginSchemaMigrations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS plugin_schema_migrations (
		plugin_id TEXT NOT NULL,
		version   TEXT NOT NULL,
		PRIMARY KEY (plugin_id, version)
	)`)
	return err
}

func pluginAdvisoryLockID(pluginID string) int64 {
	h := fnv.New64a()
	h.Write([]byte("plugin-migrate:" + pluginID))
	return int64(h.Sum64())
}

func isPluginApplied(ctx context.Context, db *sql.DB, pluginID, version string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM plugin_schema_migrations WHERE plugin_id = $1 AND version = $2`,
		pluginID, version).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func applyPluginMigration(ctx context.Context, db *sql.DB, migrations fs.FS, name, pluginID, version string) error {
	body, err := fs.ReadFile(migrations, name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO plugin_schema_migrations (plugin_id, version) VALUES ($1, $2)`,
		pluginID, version); err != nil {
		return err
	}
	return tx.Commit()
}

func createSchemaMigrations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`)
	return err
}

var migrationNameRe = regexp.MustCompile(`^(\d{3})_.*\.sql$`)

func versionFromName(name string) string {
	m := migrationNameRe.FindStringSubmatch(name)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func listMigrations(migrations fs.FS) ([]string, error) {
	var names []string
	err := fs.WalkDir(migrations, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if migrationNameRe.MatchString(path) {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func isApplied(ctx context.Context, db *sql.DB, version string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = $1`, version).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func applyMigration(ctx context.Context, db *sql.DB, migrations fs.FS, name, version string) error {
	body, err := fs.ReadFile(migrations, name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return err
	}
	return tx.Commit()
}

func tryAdvisoryLock(ctx context.Context, db *sql.DB, timeout time.Duration, lockID int64) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var ok bool
		if err := db.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockID).Scan(&ok); err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return false, nil
}

func releaseAdvisoryLock(ctx context.Context, db *sql.DB, lockID int64) {
	db.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockID)
}
