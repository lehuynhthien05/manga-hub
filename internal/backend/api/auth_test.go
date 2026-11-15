package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	"mangahub/pkg/protocol"
)

const secret = "test-secret-0123456789"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func init() { gin.SetMode(gin.TestMode) }

func newRouter(t *testing.T, dbPath string) (*gin.Engine, *store.Store) {
	t.Helper()
	db, err := database.Open(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, store.Migrate(t.Context(), db))
	st := store.New(db)
	mgr, err := auth.NewManager(secret, time.Hour)
	require.NoError(t, err)
	return api.NewRouter(api.Deps{Store: st, Auth: mgr, BcryptCost: bcrypt.MinCost, Log: quiet}), st
}

func do(r http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func code(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e protocol.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), w.Body.String())
	return e.Code
}

func TestRegister(t *testing.T) {
	r, st := newRouter(t, filepath.Join(t.TempDir(), "b.db"))

	w := do(r, "POST", "/api/auth/register", `{"username":"Alice","password":"Alice1234"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var u protocol.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &u))
	assert.Equal(t, "Alice", u.Username)
	assert.Equal(t, protocol.RoleUser, u.Role)

	// The password is stored as a bcrypt hash, never in clear text.
	stored, err := st.GetByUsername(t.Context(), "alice")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stored.PasswordHash, "$2"))
	assert.NotContains(t, stored.PasswordHash, "Alice1234")

	w = do(r, "POST", "/api/auth/register", `{"username":"alice","password":"Other1234"}`)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, protocol.CodeConflict, code(t, w))

	for name, body := range map[string]string{
		"short username": `{"username":"al","password":"Alice1234"}`,
		"bad chars":      `{"username":"al ice!","password":"Alice1234"}`,
		"weak password":  `{"username":"bob","password":"password"}`,
		"73 bytes":       `{"username":"bob","password":"Aa1` + strings.Repeat("x", 70) + `"}`,
		"empty":          `{}`,
		"bad json":       `{"username":`,
		"wrong type":     `{"username":5,"password":"Alice1234"}`,
	} {
		w := do(r, "POST", "/api/auth/register", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, name)
		assert.Equal(t, protocol.CodeInvalidInput, code(t, w), name)
	}

	big := `{"username":"bob","password":"` + strings.Repeat("x", 2<<20) + `"}`
	assert.Equal(t, http.StatusBadRequest, do(r, "POST", "/api/auth/register", big).Code)
}

func TestLogin(t *testing.T) {
	r, _ := newRouter(t, filepath.Join(t.TempDir(), "b.db"))
	require.Equal(t, http.StatusCreated, do(r, "POST", "/api/auth/register", `{"username":"alice","password":"Alice1234"}`).Code)

	w := do(r, "POST", "/api/auth/login", `{"username":"ALICE","password":"Alice1234"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var lr protocol.LoginResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &lr))
	mgr, _ := auth.NewManager(secret, time.Hour)
	c, err := mgr.Verify(lr.Token)
	require.NoError(t, err)
	assert.Equal(t, lr.User.ID, c.Subject)
	assert.Equal(t, "alice", c.Username)

	wrong := do(r, "POST", "/api/auth/login", `{"username":"alice","password":"Wrong1234"}`)
	unknown := do(r, "POST", "/api/auth/login", `{"username":"nobody","password":"Wrong1234"}`)
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)
	assert.Equal(t, http.StatusUnauthorized, unknown.Code)
	assert.Equal(t, wrong.Body.String(), unknown.Body.String(), "no user enumeration")
}

func TestMiddleware(t *testing.T) {
	mgr, _ := auth.NewManager(secret, time.Hour)
	r := gin.New()
	ok := func(c *gin.Context) { c.String(http.StatusOK, auth.UserID(c)) }
	r.GET("/p", auth.Middleware(mgr, quiet), ok)
	r.GET("/a", auth.Middleware(mgr, quiet), auth.RequireAdmin(), ok)

	user, _, _ := mgr.Issue("usr_u", "u", protocol.RoleUser)
	admin, _, _ := mgr.Issue("usr_a", "a", protocol.RoleAdmin)
	expMgr, _ := auth.NewManager(secret, -time.Minute)
	expired, _, _ := expMgr.Issue("usr_u", "u", protocol.RoleUser)
	bearer := func(t string) []string { return []string{"Authorization", "Bearer " + t} }

	assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/p", "").Code)
	assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/p", "", bearer("forged.token.x")...).Code)
	assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/p", "", bearer(expired)...).Code)
	assert.Equal(t, http.StatusUnauthorized, do(r, "GET", "/p", "", "Authorization", user).Code) // no "Bearer "

	w := do(r, "GET", "/p", "", bearer(user)...)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "usr_u", w.Body.String())

	w = do(r, "GET", "/a", "", bearer(user)...)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, protocol.CodeForbidden, code(t, w))
	assert.Equal(t, http.StatusOK, do(r, "GET", "/a", "", bearer(admin)...).Code)
}

func TestPersistence_LoginAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.db")
	r, _ := newRouter(t, path)
	require.Equal(t, http.StatusCreated, do(r, "POST", "/api/auth/register", `{"username":"alice","password":"Alice1234"}`).Code)

	r2, _ := newRouter(t, path) // a new connection pool on the same file = a restarted backend
	assert.Equal(t, http.StatusOK, do(r2, "POST", "/api/auth/login", `{"username":"alice","password":"Alice1234"}`).Code)
}

func TestHealth(t *testing.T) {
	r, _ := newRouter(t, filepath.Join(t.TempDir(), "b.db"))
	w := do(r, "GET", "/api/health", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, bytes.Contains(w.Body.Bytes(), []byte(`"status":"ok"`)))
}
