package catalog_test

import (
	"context"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/catalog"
	"mangahub/pkg/database"
)

func TestMigrate_Idempotent(t *testing.T) {
	db := database.OpenTemp(t)
	require.NoError(t, catalog.Migrate(context.Background(), db))
	require.NoError(t, catalog.Migrate(context.Background(), db))
}

func TestSchema_MangaConstraints(t *testing.T) {
	db := database.OpenTemp(t)
	require.NoError(t, catalog.Migrate(context.Background(), db))

	_, err := db.Exec(`INSERT INTO manga(id, title) VALUES ('one-piece','One Piece')`)
	require.NoError(t, err)

	for name, q := range map[string]string{
		"bad status":     `INSERT INTO manga(id, title, status) VALUES ('a','A','cancelled')`,
		"negative total": `INSERT INTO manga(id, title, total_chapters) VALUES ('b','B',-1)`,
	} {
		_, err := db.Exec(q)
		var se sqlite3.Error
		require.ErrorAs(t, err, &se, name)
		assert.Equal(t, sqlite3.ErrConstraintCheck, se.ExtendedCode, name)
	}
	_, err = db.Exec(`INSERT INTO manga(id, title) VALUES ('one-piece','Dup')`)
	var se sqlite3.Error
	require.ErrorAs(t, err, &se)
	assert.Equal(t, sqlite3.ErrConstraintPrimaryKey, se.ExtendedCode)
}
