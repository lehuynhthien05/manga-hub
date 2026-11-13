package database_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/pkg/database"
)

func TestOpen_CreatesParentDirAndEnablesPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "var", "x.db")
	db, err := database.Open(path)
	require.NoError(t, err)
	defer db.Close()

	var mode string
	require.NoError(t, db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	assert.Equal(t, "wal", mode)
	var fk int
	require.NoError(t, db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	assert.Equal(t, 1, fk)
}

func TestOpen_ParentIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	_, err := database.Open(filepath.Join(file, "x.db"))
	assert.Error(t, err)
}
