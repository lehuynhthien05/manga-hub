// Package database opens SQLite files with the settings every MangaHub
// program needs. Schemas live with their owners (backend store, catalog).
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DSN options: WAL lets readers run beside one writer; busy_timeout makes a
// blocked writer wait instead of failing; _txlock=immediate makes BeginTx run
// BEGIN IMMEDIATE so read-then-write transactions cannot fail on lock upgrade.
const dsnOptions = "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_txlock=immediate"

// Open creates the parent directory if needed and opens the database file.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite3", path+dsnOptions)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	return db, nil
}

// OpenTemp opens a database file in the test's temp dir. It is a file, not
// ":memory:", because every pooled connection to ":memory:" is its own database.
func OpenTemp(tb testing.TB) *sql.DB {
	tb.Helper()
	db, err := Open(filepath.Join(tb.TempDir(), "test.db"))
	if err != nil {
		tb.Fatalf("OpenTemp: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return db
}
