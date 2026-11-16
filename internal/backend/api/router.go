package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"mangahub/internal/auth"
	"mangahub/internal/backend/store"
	"mangahub/pkg/protocol"
)

// Deps are the collaborators main wires in. Later phases add
// ChapterNotifier and the cache here.
type Deps struct {
	Store          *store.Store
	Auth           *auth.Manager
	Catalog        Catalog
	CatalogTimeout time.Duration     // one deadline for a whole library page; 0 means 2 s
	Publisher      ProgressPublisher // nil means no-op (the TCP hub arrives in Phase 7)
	BcryptCost     int
	Log            *slog.Logger
}

const maxBody = 1 << 20 // 1 MiB per request body

func NewRouter(d Deps) *gin.Engine {
	if d.Publisher == nil {
		d.Publisher = noopPublisher{}
	}
	if d.CatalogTimeout == 0 {
		d.CatalogTimeout = 2 * time.Second
	}
	h := &handlers{Deps: d}
	// Unknown users still pay one bcrypt compare, so login timing does not reveal who exists.
	h.dummyHash, _ = auth.HashPassword("dummy-password-for-timing", d.BcryptCost)

	r := gin.New()
	r.Use(accessLog(d.Log), gin.CustomRecovery(func(c *gin.Context, err any) {
		d.Log.Error("panic", "err", err, "path", c.FullPath())
		Fail(c, http.StatusInternalServerError, protocol.CodeInternal, "internal error")
	}), func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBody)
		c.Next()
	})

	r.GET("/api/health", h.health)
	r.POST("/api/auth/register", h.register)
	r.POST("/api/auth/login", h.login)
	r.GET("/api/manga", h.searchManga)
	r.GET("/api/manga/:id", h.getManga)

	lib := r.Group("/api/library", auth.Middleware(d.Auth, d.Log))
	lib.POST("", h.addLibrary)
	lib.GET("", h.listLibrary)
	lib.PUT("/:mangaId/progress", h.updateProgress)
	return r
}

// Fail writes the error envelope used by every endpoint.
func Fail(c *gin.Context, status int, code, msg string) {
	c.AbortWithStatusJSON(status, protocol.ErrorResponse{Error: msg, Code: code})
}

// bindJSON maps any decode error (bad JSON, wrong types, body too large) to 400.
func bindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		var tooBig *http.MaxBytesError
		msg := "invalid JSON body"
		if errors.As(err, &tooBig) {
			msg = "request body too large"
		}
		Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, msg)
		return false
	}
	return true
}

// accessLog never logs headers or bodies (they may carry tokens or passwords).
func accessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http", "method", c.Request.Method, "path", c.Request.URL.Path,
			"status", c.Writer.Status(), "dur", time.Since(start))
	}
}

type handlers struct {
	Deps
	dummyHash string
}

func (h *handlers) health(c *gin.Context) {
	// Always 200: the backend itself is up; the catalog state is reported, redis arrives in Phase 12.
	catalog := "down"
	if h.Catalog.Healthy(c.Request.Context()) {
		catalog = "up"
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "catalog": catalog, "redis": "disabled"})
}

func (h *handlers) register(c *gin.Context) {
	var req protocol.RegisterRequest
	if !bindJSON(c, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	for _, err := range []error{protocol.ValidateUsername(req.Username), protocol.ValidatePassword(req.Password)} {
		if err != nil {
			Fail(c, http.StatusBadRequest, protocol.CodeInvalidInput, err.Error())
			return
		}
	}
	hash, err := auth.HashPassword(req.Password, h.BcryptCost)
	if err != nil {
		h.internal(c, err)
		return
	}
	u, err := h.Store.CreateUser(c.Request.Context(), req.Username, hash, protocol.RoleUser)
	if errors.Is(err, store.ErrUsernameTaken) {
		Fail(c, http.StatusConflict, protocol.CodeConflict, "username already taken")
		return
	}
	if err != nil {
		h.internal(c, err)
		return
	}
	c.JSON(http.StatusCreated, protocol.User{ID: u.ID, Username: u.Username, Role: u.Role})
}

func (h *handlers) login(c *gin.Context) {
	var req protocol.LoginRequest
	if !bindJSON(c, &req) {
		return
	}
	u, err := h.Store.GetByUsername(c.Request.Context(), strings.TrimSpace(req.Username))
	switch {
	case errors.Is(err, store.ErrUserNotFound):
		auth.CheckPassword(h.dummyHash, req.Password)
		Fail(c, http.StatusUnauthorized, protocol.CodeUnauthenticated, "invalid username or password")
		return
	case err != nil:
		h.internal(c, err)
		return
	case !auth.CheckPassword(u.PasswordHash, req.Password):
		Fail(c, http.StatusUnauthorized, protocol.CodeUnauthenticated, "invalid username or password")
		return
	}
	tok, exp, err := h.Auth.Issue(u.ID, u.Username, u.Role)
	if err != nil {
		h.internal(c, err)
		return
	}
	c.JSON(http.StatusOK, protocol.LoginResponse{Token: tok, ExpiresAt: exp,
		User: protocol.User{ID: u.ID, Username: u.Username, Role: u.Role}})
}

func (h *handlers) internal(c *gin.Context, err error) {
	h.Log.Error("request failed", "path", c.FullPath(), "err", err)
	Fail(c, http.StatusInternalServerError, protocol.CodeInternal, "internal error")
}
