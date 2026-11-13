package store_test

import (
	"context"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/backend/store"
	"mangahub/pkg/database"
)

func sqliteCode(t *testing.T, err error) sqlite3.ErrNoExtended {
	t.Helper()
	var se sqlite3.Error
	require.ErrorAs(t, err, &se)
	return se.ExtendedCode
}

func TestMigrate_Idempotent(t *testing.T) {
	db := database.OpenTemp(t)
	require.NoError(t, store.Migrate(context.Background(), db))
	require.NoError(t, store.Migrate(context.Background(), db))
}

func TestSchema_LibraryConstraints(t *testing.T) {
	db := database.OpenTemp(t)
	require.NoError(t, store.Migrate(context.Background(), db))
	_, err := db.Exec(`INSERT INTO users(id, username, password_hash) VALUES ('u1','alice','h')`)
	require.NoError(t, err)

	const ins = `INSERT INTO user_library(user_id, manga_id, status, current_chapter) VALUES (?,?,?,?)`
	tests := []struct {
		name  string
		args  []any
		code  sqlite3.ErrNoExtended
		valid bool
	}{
		{"ok", []any{"u1", "one-piece", "reading", 5}, 0, true},
		{"bad status", []any{"u1", "naruto", "bogus", 0}, sqlite3.ErrConstraintCheck, false},
		{"old status spelling", []any{"u1", "naruto", "plan_to_read", 0}, sqlite3.ErrConstraintCheck, false},
		{"negative chapter", []any{"u1", "naruto", "reading", -1}, sqlite3.ErrConstraintCheck, false},
		{"duplicate pair", []any{"u1", "one-piece", "reading", 6}, sqlite3.ErrConstraintPrimaryKey, false},
		{"unknown user", []any{"nobody", "naruto", "reading", 0}, sqlite3.ErrConstraintForeignKey, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(ins, tc.args...)
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.code, sqliteCode(t, err))
		})
	}
}

func TestSchema_UserConstraintsAndCascade(t *testing.T) {
	db := database.OpenTemp(t)
	require.NoError(t, store.Migrate(context.Background(), db))
	_, err := db.Exec(`INSERT INTO users(id, username, password_hash) VALUES ('u1','Alice','h')`)
	require.NoError(t, err)

	// Usernames are unique case-insensitively.
	_, err = db.Exec(`INSERT INTO users(id, username, password_hash) VALUES ('u2','alice','h')`)
	require.Error(t, err)
	assert.Equal(t, sqlite3.ErrConstraintUnique, sqliteCode(t, err))

	_, err = db.Exec(`INSERT INTO users(id, username, password_hash, role) VALUES ('u3','bob','h','root')`)
	require.Error(t, err)
	assert.Equal(t, sqlite3.ErrConstraintCheck, sqliteCode(t, err))

	_, err = db.Exec(`INSERT INTO user_library(user_id, manga_id, status) VALUES ('u1','m','reading')`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM users WHERE id='u1'`)
	require.NoError(t, err)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_library`).Scan(&n))
	assert.Zero(t, n)
}
