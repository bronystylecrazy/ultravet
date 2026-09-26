// Package kind pins UV0010's leaf rule: the shared vocabulary imports ONLY
// the standard library. strings is fine; an internal import is not.
package kind

import (
	"strings"

	"example.com/prod/internal/db" // want `error\[UV0010\]: internal/kind imports example.com/prod/internal/db`
)

// WireID is the vocabulary a kind package exists to hold: a value type.
type WireID string

func (w WireID) Clean() WireID { return WireID(strings.TrimSpace(string(w))) }

// Illegal is the behavior that does not belong here.
func Illegal() *db.DB { return db.New() }
