// Package util pins UV0006: a leaf with an internal edge is not a leaf.
// Note it never imports ultrastack itself — the platform gate is
// transitive, which is exactly how the violating import gets it there.
package util

import (
	"strings"

	"example.com/prod/internal/db" // want `warning\[UV0006\]: internal/util imports internal/db — util/ is a leaf`
)

func Slug(s string) string { return strings.ToLower(s) }

func Name(d *db.DB) string { return "db" }
