// Package schemas contains the installed offline schema catalog and its resources.
package schemas

import "embed"

// Files is the checked-in schema source, not the complete release contract package.
//
//go:embed catalog.json *.schema.json
var Files embed.FS
