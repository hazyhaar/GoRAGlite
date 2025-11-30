// Package assets embeds SQL files for GoRAGlite.
// This package exists to comply with HOROS embed pattern requirements:
// go:embed directives cannot use ".." to reference parent directories.
package assets

import "embed"

// SchemaFS contains embedded SQL schema files.
//
//go:embed schema/*.sql
var SchemaFS embed.FS

// WorkflowsFS contains embedded SQL workflow definitions.
//
//go:embed workflows/*.sql
var WorkflowsFS embed.FS
