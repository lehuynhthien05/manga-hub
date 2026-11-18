package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: msg, Code: code})
}

// fakeBackend answers like the real one for user alice/Alice1234; every other call fails.
func fakeBackend(t *testing.T) (*client.API, *http.Request) {
	t.Helper()
	var last http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = *r
		authed := r.Header.Get("Authorization") == "Bearer tok"
		switch {
		case r.URL.Path == client.PathLogin:
			var req protocol.LoginRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Password != "Alice1234" {
				fail(w, 401, protocol.CodeUnauthenticated, "invalid username or password")
				return
			}
			_ = json.NewEncoder(w).Encode(protocol.LoginResponse{Token: "tok", User: protocol.User{Username: req.Username}})
		case r.URL.Path == client.PathRegister:
			fail(w, 409, protocol.CodeConflict, "username already taken")
		case r.URL.Path == client.PathManga:
			if r.URL.Query().Get("q") == "down" {
				fail(w, 503, protocol.CodeCatalogUnavailable, "catalog service unavailable")
				return
			}
			_ = json.NewEncoder(w).Encode(protocol.SearchResponse{Items: []protocol.Manga{{ID: "one-piece"}}, Total: 1, Page: 1, Limit: 20})
		case r.URL.Path == client.PathManga+"/nope":
			fail(w, 404, protocol.CodeNotFound, "manga not found")
		case strings.HasPrefix(r.URL.Path, client.PathLibrary) && !authed:
			fail(w, 401, protocol.CodeUnauthenticated, "missing bearer token")
		case r.URL.Path == client.PathLibrary+"/one-piece/progress":
			_ = json.NewEncoder(w).Encode(protocol.LibraryItem{MangaID: "one-piece", CurrentChapter: 25, Version: 2})
		case r.URL.Path == client.PathLibrary:
			_ = json.NewEncoder(w).Encode(protocol.LibraryResponse{Items: []protocol.LibraryItem{}, Catalog: "ok"})
		default:
			w.WriteHeader(500)
			_, _ = w.Write([]byte("<html>boom</html>"))
		}
	}))
	t.Cleanup(srv.Close)
	return client.NewAPI(srv.URL + "/"), &last
}

func TestAPI_LoginStoresTokenAndAuthorizes(t *testing.T) {
	c, last := fakeBackend(t)
	ctx := context.Background()

	_, err := c.ListLibrary(ctx)
	assert.True(t, client.IsUnauthorized(err), "no token yet")

	_, err = c.Login(ctx, "alice", "Wrong1234")
	assert.Equal(t, "Invalid username or password", client.FriendlyMessage(err))
	assert.False(t, client.IsUnauthorized(err), "a failed login is not an expired session")

	lr, err := c.Login(ctx, "alice", "Alice1234")
	require.NoError(t, err)
	assert.Equal(t, "alice", lr.User.Username)
	res, err := c.ListLibrary(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Catalog)

	ch := 25
	it, err := c.UpdateProgress(ctx, "one-piece", &ch, nil, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, 2, it.Version)
	assert.Equal(t, "sess-1", last.Header.Get("X-Session-ID"))

	c.Clear()
	_, err = c.ListLibrary(ctx)
	assert.True(t, client.IsUnauthorized(err))
}

func TestAPI_SearchAndErrors(t *testing.T) {
	c, last := fakeBackend(t)
	ctx := context.Background()

	res, err := c.Search(ctx, "one piece", 2, 10)
	require.NoError(t, err)
	assert.Len(t, res.Items, 1)
	assert.Equal(t, "one piece", last.URL.Query().Get("q"))
	assert.Equal(t, "2", last.URL.Query().Get("page"))

	_, err = c.Search(ctx, "down", 1, 10)
	assert.Equal(t, "Catalog service unavailable — try again shortly", client.FriendlyMessage(err))
	_, err = c.GetManga(ctx, "nope")
	assert.Equal(t, "manga not found", client.FriendlyMessage(err))
	_, err = c.Register(ctx, "alice", "Alice1234")
	assert.Equal(t, "Username already taken", client.FriendlyMessage(err))
	_, err = c.AddToLibrary(ctx, "x", "reading") // 401 without token
	assert.Equal(t, "Session expired — please log in again", client.FriendlyMessage(err))
	_, err = c.Health(ctx) // the fake has no health route: a non-JSON 500
	assert.Equal(t, "Server error (500)", client.FriendlyMessage(err))
}

func TestFriendlyMessage_Table(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"403":       {&client.APIError{Status: 403, Path: "/x"}, "Not allowed"},
		"400":       {&client.APIError{Status: 400, Message: "limit: bad", Path: "/x"}, "limit: bad"},
		"502":       {&client.APIError{Status: 502, Code: "bad_gateway", Message: "catalog error"}, "Server error (502)"},
		"409 other": {&client.APIError{Status: 409, Message: "manga already in library", Path: client.PathLibrary}, "manga already in library"},
		"other":     {errors.New("x"), "Unexpected error: x"},
	}
	for name, tc := range cases {
		assert.Equal(t, tc.want, client.FriendlyMessage(tc.err), name)
	}
}

func TestAPI_UnreachableAndCanceled(t *testing.T) {
	ln := testutil.ListenLoopback(t)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close()) // nothing listens there now: connection refused
	c := client.NewAPI("http://" + addr)

	_, err := c.Health(context.Background())
	assert.Equal(t, "Cannot reach the backend at http://"+addr+" — is it running?", client.FriendlyMessage(err))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Health(ctx)
	assert.ErrorIs(t, err, context.Canceled, "a canceled request is not reported as unreachable")
}
