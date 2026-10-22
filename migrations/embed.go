// Package migrations embarque les fichiers SQL appliqués par `agentic-os migrate`.
package migrations

import "embed"

// FS contient les migrations NNNN_nom.sql.
//
//go:embed *.sql
var FS embed.FS
