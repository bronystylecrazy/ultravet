// Package db is clean infra: it knows nothing about the product above it.
package db

import "github.com/bronystylecrazy/di"

type DB struct{}

func New() *DB { return &DB{} }

var _ = di.Provide
