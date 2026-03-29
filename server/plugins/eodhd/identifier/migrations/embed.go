package migrations

import "embed"

// Files is the embedded set of migration SQL files for the EODHD plugin.
//
//go:embed *.sql
var Files embed.FS
