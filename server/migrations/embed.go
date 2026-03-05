package migrations

import "embed"

// Files is the embedded set of migration SQL files (NNN_name.sql). Used by server/db/migrate and tests.
//go:embed *.sql
var Files embed.FS
