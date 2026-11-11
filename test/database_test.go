package test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"mangahub/pkg/database"
)

func TestInitDB(t *testing.T) {
	tempDB := filepath.Join(t.TempDir(), "test.db")

	db, err := database.InitDB(tempDB)
	assert.NoError(t, err)
	assert.NotNil(t, db)
	defer db.Close()

	counts, err := database.CheckDB(db)
	assert.NoError(t, err)
	assert.Contains(t, counts, "manga")
	assert.Contains(t, counts, "users")

	err = database.OptimizeDB(db)
	assert.NoError(t, err)
}
