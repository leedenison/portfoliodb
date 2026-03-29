package migrate

import "io/fs"

// Migrator is an optional interface that plugins implement to provide
// versioned SQL migrations for plugin-specific reference data.
// The server runs these after core migrations using UpPlugin, with
// plugin-scoped version tracking in plugin_schema_migrations.
type Migrator interface {
	MigrationFS() fs.FS
}
