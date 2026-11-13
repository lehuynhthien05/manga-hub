// Package catalog owns the catalog SQLite schema and, from Phase 2, the gRPC
// service. It must not import internal/backend; the backend reaches it only over gRPC.
package catalog

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
		return fmt.Errorf("migrate catalog schema: %w", err)
	}
	return nil
}
