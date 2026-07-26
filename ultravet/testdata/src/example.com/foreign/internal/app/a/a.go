// Package a is a foreign codebase that happens to have an internal/app tree
// and imports a sibling. Nothing here touches ultrastack, so the doctrine is
// none of our business: no diagnostics.
package a

import "example.com/foreign/internal/app/b"

func Use() int { return b.N }
