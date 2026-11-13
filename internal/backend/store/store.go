// Package store owns the backend SQLite schema (users, user_library) and the
// queries on it. It must not import internal/catalog.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed schema.sql
var schemaSQL string

// Migrate applies the schema; every statement is IF NOT EXISTS, so it is idempotent.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate backend schema: %w", err)
	}
	return nil
}
