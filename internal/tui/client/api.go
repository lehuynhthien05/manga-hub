package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"mangahub/pkg/protocol"
)

const (
	PathHealth   = "/api/health"
	PathRegister = "/api/auth/register"
	PathLogin    = "/api/auth/login"
	PathManga    = "/api/manga"
	PathLibrary  = "/api/library"
)

// APIError is a non-2xx answer from the backend.
type APIError struct {
	Status        int
	Code, Message string
	Path          string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %d %s", e.Path, e.Status, e.Code) }

// UnreachableError means no HTTP answer at all (refused, timeout, DNS).
type UnreachableError struct {
	Base string
	Err  error
}

func (e *UnreachableError) Error() string { return "cannot reach " + e.Base + ": " + e.Err.Error() }
func (e *UnreachableError) Unwrap() error { return e.Err }

// API is the TUI's HTTP client. The JWT lives only in memory and is never logged.
type API struct {
	base  string
	hc    *http.Client
	token atomic.Pointer[string]
}

func NewAPI(base string) *API {
	return &API{base: strings.TrimRight(base, "/"), hc: &http.Client{Timeout: 5 * time.Second}}
}

func (c *API) Base() string        { return c.base }
func (c *API) SetToken(tok string) { c.token.Store(&tok) }
func (c *API) Clear()              { c.token.Store(nil) }

func (c *API) do(ctx context.Context, method, path string, in, out any, hdr map[string]string) error {
	_, err := c.doH(ctx, method, path, in, out, hdr)
	return err
}

// doH is do that also returns the response headers.
func (c *API) doH(ctx context.Context, method, path string, in, out any, hdr map[string]string) (http.Header, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t := c.token.Load(); t != nil {
		req.Header.Set("Authorization", "Bearer "+*t)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // superseded or shutting down: not a connectivity problem
		}
		return nil, &UnreachableError{Base: c.base, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &UnreachableError{Base: c.base, Err: err}
	}
	if resp.StatusCode >= 300 {
		ae := &APIError{Status: resp.StatusCode, Path: path}
		var env protocol.ErrorResponse
		if json.Unmarshal(raw, &env) == nil {
			ae.Code, ae.Message = env.Code, env.Error
		}
		return resp.Header, ae
	}
	if out == nil {
		return resp.Header, nil
	}
	return resp.Header, json.Unmarshal(raw, out)
}

func (c *API) Register(ctx context.Context, username, password string) (protocol.User, error) {
	var u protocol.User
	err := c.do(ctx, http.MethodPost, PathRegister, protocol.RegisterRequest{Username: username, Password: password}, &u, nil)
	return u, err
}

// Login stores the token on success.
func (c *API) Login(ctx context.Context, username, password string) (protocol.LoginResponse, error) {
	var lr protocol.LoginResponse
	err := c.do(ctx, http.MethodPost, PathLogin, protocol.LoginRequest{Username: username, Password: password}, &lr, nil)
	if err == nil {
		c.SetToken(lr.Token)
	}
	return lr, err
}

func (c *API) Search(ctx context.Context, q string, page, limit int) (protocol.SearchResponse, error) {
	v := url.Values{"q": {q}, "page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(limit)}}
	var res protocol.SearchResponse
	err := c.do(ctx, http.MethodGet, PathManga+"?"+v.Encode(), nil, &res, nil)
	return res, err
}

func (c *API) GetManga(ctx context.Context, id string) (protocol.Manga, error) {
	m, _, err := c.GetMangaCache(ctx, id)
	return m, err
}

// GetMangaCache also returns the X-Cache header ("HIT", "MISS", or "" when caching is off).
func (c *API) GetMangaCache(ctx context.Context, id string) (protocol.Manga, string, error) {
	var m protocol.Manga
	h, err := c.doH(ctx, http.MethodGet, PathManga+"/"+url.PathEscape(id), nil, &m, nil)
	return m, h.Get("X-Cache"), err
}

func (c *API) ListLibrary(ctx context.Context) (protocol.LibraryResponse, error) {
	var res protocol.LibraryResponse
	err := c.do(ctx, http.MethodGet, PathLibrary, nil, &res, nil)
	return res, err
}

func (c *API) AddToLibrary(ctx context.Context, mangaID, status string) (protocol.LibraryItem, error) {
	var it protocol.LibraryItem
	err := c.do(ctx, http.MethodPost, PathLibrary, protocol.AddLibraryRequest{MangaID: mangaID, Status: status}, &it, nil)
	return it, err
}

// UpdateProgress sends sessionID as X-Session-ID so the TCP hub (Phase 7) skips the origin session.
func (c *API) UpdateProgress(ctx context.Context, mangaID string, chapter *int, status *string, sessionID string) (protocol.LibraryItem, error) {
	var hdr map[string]string
	if sessionID != "" {
		hdr = map[string]string{"X-Session-ID": sessionID}
	}
	var it protocol.LibraryItem
	err := c.do(ctx, http.MethodPut, PathLibrary+"/"+url.PathEscape(mangaID)+"/progress",
		protocol.UpdateProgressRequest{CurrentChapter: chapter, Status: status}, &it, hdr)
	return it, err
}

type Health struct {
	Status, Catalog, Redis string
}

func (c *API) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, PathHealth, nil, &h, nil)
	return h, err
}

// IsUnauthorized reports a 401 on anything but login: the session has expired.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized && ae.Path != PathLogin
}

// FriendlyMessage is the single place that turns an error into text for the user.
func FriendlyMessage(err error) string {
	var ue *UnreachableError
	if errors.As(err, &ue) {
		return "Cannot reach the backend at " + ue.Base + " — is it running?"
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return "Unexpected error: " + err.Error()
	}
	switch {
	case ae.Status == http.StatusUnauthorized && ae.Path == PathLogin:
		return "Invalid username or password"
	case ae.Status == http.StatusUnauthorized:
		return "Session expired — please log in again"
	case ae.Status == http.StatusConflict && ae.Path == PathRegister:
		return "Username already taken"
	case ae.Status == http.StatusForbidden:
		return "Not allowed"
	case ae.Code == protocol.CodeCatalogUnavailable:
		return "Catalog service unavailable — try again shortly"
	case ae.Status >= 500 || ae.Message == "":
		return fmt.Sprintf("Server error (%d)", ae.Status)
	default: // 400, 404, 409: the server's own message
		return ae.Message
	}
}
