package api_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"mangahub/internal/auth"
	"mangahub/internal/backend/api"
	"mangahub/internal/backend/store"
	"mangahub/pkg/database"
)

// 10.4.3: a panicking handler answers with the generic 500 envelope; details go to the log only.
func TestRouter_PanicGives500Envelope(t *testing.T) {
	var logs bytes.Buffer
	db, err := database.Open(filepath.Join(t.TempDir(), "b.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mgr, _ := auth.NewManager(secret, time.Hour)
	r := api.NewRouter(api.Deps{Store: store.New(db), Auth: mgr, Catalog: &fakeCatalog{}, BcryptCost: bcrypt.MinCost,
		Log: slog.New(slog.NewTextHandler(&logs, nil))})
	r.GET("/boom", func(*gin.Context) { panic("secret internal detail") })

	w := do(r, "GET", "/boom", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.JSONEq(t, `{"error":"internal error","code":"internal"}`, w.Body.String())
	assert.Contains(t, logs.String(), "secret internal detail")
}
