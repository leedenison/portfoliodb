package identifier

import (
	"database/sql"

	"github.com/leedenison/portfoliodb/server/identifier"
)

// RegisterLocal registers the local reference data plugin with the given registry and DB.
// Call this at startup (e.g. from main) so that when plugin config enables "local", the resolver can invoke it.
func RegisterLocal(registry *identifier.Registry, db *sql.DB) {
	registry.Register("local", New(db))
}
