package integration_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

func realStack(t *testing.T) (syncStack, string) {
	t.Helper()
	_, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, rt := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return syncStack{srv.URL, rt.tcp, rt.udp}, "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func dialWS(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Authorization": {"Bearer " + token}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// collect reads n lines (TCP) in the background and returns them through a channel.
func collectTCP(n *nc, count int) <-chan protocol.ProgressUpdated {
	out := make(chan protocol.ProgressUpdated, count)
	go func() {
		defer close(out)
		for range count {
			_ = n.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			l, err := n.r.ReadBytes('\n')
			var ev protocol.ProgressUpdated
			if err != nil || json.Unmarshal(l, &ev) != nil {
				return
			}
			out <- ev
		}
	}()
	return out
}

// v3.0 §8 "one concurrent two-client test": alice has two TCP sessions and two WS
// clients, bob one TCP session; 20 progress updates and 20 chat lines run concurrently.
func TestConcurrent_SameUserTCPAndTwoWSClients(t *testing.T) {
	s, wsURL := realStack(t)
	alice, bob := s.user(t, "alice", "Alice1234"), s.user(t, "bob", "Bob12345x")
	code, _ := call(t, "POST", s.url+client.PathLibrary, alice, `{"manga_id":"one-piece","status":"reading"}`)
	require.Equal(t, http.StatusCreated, code)

	a1, a2, b1 := dialSync(t, s.tcp, alice), dialSync(t, s.tcp, alice), dialSync(t, s.tcp, bob)
	w1, w2 := dialWS(t, wsURL, alice), dialWS(t, wsURL, alice)
	for _, w := range []*websocket.Conn{w1, w2} { // the history frame proves the join was processed
		require.NoError(t, w.WriteJSON(protocol.WSJoin{Type: "join", Room: "general"}))
		for {
			var m protocol.WSHistory
			_ = w.SetReadDeadline(time.Now().Add(2 * time.Second))
			require.NoError(t, w.ReadJSON(&m))
			if m.Type == protocol.TypeHistory {
				break
			}
		}
	}
	wsGot := func(w *websocket.Conn) <-chan string {
		out := make(chan string, 64)
		go func() {
			defer close(out)
			for {
				_ = w.SetReadDeadline(time.Now().Add(5 * time.Second))
				var m protocol.WSChat
				if w.ReadJSON(&m) != nil {
					return
				}
				if m.Type == protocol.TypeChat {
					out <- m.Text
				}
			}
		}()
		return out
	}
	g1, g2 := wsGot(w1), wsGot(w2)
	e1, e2 := collectTCP(a1, 20), collectTCP(a2, 20)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			code, _ := call(t, "PUT", s.url+client.PathLibrary+"/one-piece/progress", alice, fmt.Sprintf(`{"current_chapter":%d}`, i+1))
			assert.Equal(t, http.StatusOK, code)
		})
	}
	for n, w := range []*websocket.Conn{w1, w2} { // one sender per connection: gorilla allows a single writer
		wg.Go(func() {
			for i := range 10 {
				assert.NoError(t, w.WriteJSON(protocol.WSChat{Type: "chat", Room: "general", Text: fmt.Sprintf("w%d msg %d", n+1, i)}))
			}
		})
	}
	wg.Wait()

	for name, ch := range map[string]<-chan protocol.ProgressUpdated{"A1": e1, "A2": e2} {
		var versions []int
		for ev := range ch {
			versions = append(versions, ev.Version)
		}
		slices.Sort(versions) // concurrent handlers may publish slightly out of order; the TUI keeps the newest
		want := make([]int, 20)
		for i := range want {
			want[i] = i + 2
		}
		assert.Equal(t, want, versions, "%s receives every version exactly once (2..21, gap-free)", name)
	}
	testutil.ExpectNoData(t, b1.conn, 300*time.Millisecond) // bob gets nothing of alice's
	for name, ch := range map[string]<-chan string{"W1": g1, "W2": g2} {
		var texts []string
		for txt := range ch {
			if texts = append(texts, txt); len(texts) == 20 {
				break
			}
		}
		assert.Len(t, texts, 20, "%s receives all 20 chat lines", name)
	}
}

// v3.0 §8 "one restart / persistence test": both databases are reopened by a second
// catalog and backend (process-level restarts: scripts/restart-check.sh).
func TestRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	catDB, backDB := filepath.Join(dir, "catalog.db"), filepath.Join(dir, "backend.db")
	_, catAddr := serveCatalogAt(t, "127.0.0.1:0", catDB)
	h, _ := newBackendAt(t, catAddr, 2*time.Second, false, backDB)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := syncStack{url: srv.URL}
	alice := s.user(t, "alice", "Alice1234")
	call(t, "POST", s.url+client.PathLibrary, alice, `{"manga_id":"one-piece","status":"reading"}`)
	code, _ := call(t, "PUT", s.url+client.PathLibrary+"/one-piece/progress", alice, `{"current_chapter":25}`)
	require.Equal(t, http.StatusOK, code)
	_, out := call(t, "POST", s.url+client.PathLogin, "", `{"username":"admin","password":"Admin12345"}`)
	admin := between(out, `"token":"`, `"`)
	code, _ = call(t, "POST", s.url+"/api/admin/manga/one-piece/release", admin, "")
	require.Equal(t, http.StatusOK, code)

	// "Restart": a fresh catalog and backend on the same two files.
	_, catAddr2 := serveCatalogAt(t, "127.0.0.1:0", catDB)
	h2, _ := newBackendAt(t, catAddr2, 2*time.Second, false, backDB)
	srv2 := httptest.NewServer(h2)
	t.Cleanup(srv2.Close)
	code, out = call(t, "POST", srv2.URL+client.PathLogin, "", `{"username":"alice","password":"Alice1234"}`)
	require.Equal(t, http.StatusOK, code, "the account survived")
	tok := between(out, `"token":"`, `"`)
	_, out = call(t, "GET", srv2.URL+client.PathLibrary, tok, "")
	var lr protocol.LibraryResponse
	require.NoError(t, json.Unmarshal([]byte(out), &lr))
	require.Len(t, lr.Items, 1)
	assert.Equal(t, 25, lr.Items[0].CurrentChapter)
	assert.Equal(t, 2, lr.Items[0].Version)
	require.NotNil(t, lr.Items[0].Manga)
	assert.Equal(t, 1101, lr.Items[0].Manga.TotalChapters, "the release survived in the catalog DB")
	_, out = call(t, "GET", srv2.URL+"/api/manga?limit=1", "", "")
	assert.Contains(t, out, `"total":35`, "the seed was not applied twice")
}

// between is shared with the load test (build tag), so it lives here too.
func between(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	v, _, _ := strings.Cut(rest, to)
	return v
}

// v3.0 §8 "one unauthorized-user test", on every protocol.
func TestUnauthorized_AllProtocols(t *testing.T) {
	s, wsURL := realStack(t)
	alice, bob := s.user(t, "alice", "Alice1234"), s.user(t, "bob", "Bob12345x")
	call(t, "POST", s.url+client.PathLibrary, alice, `{"manga_id":"one-piece","status":"reading"}`)
	forged := alice[:len(alice)-1] + map[bool]string{true: "A", false: "B"}[!strings.HasSuffix(alice, "A")]

	// HTTP: no token, forged token, and another user's entry.
	for _, tok := range []string{"", forged} {
		code, body := call(t, "GET", s.url+client.PathLibrary, tok, "")
		assert.Equal(t, http.StatusUnauthorized, code)
		assert.Contains(t, body, "unauthenticated")
	}
	code, body := call(t, "GET", s.url+client.PathLibrary, bob, "")
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"items":[],"catalog":"ok"}`, body, "bob cannot see alice's rows")
	code, _ = call(t, "PUT", s.url+client.PathLibrary+"/one-piece/progress", bob, `{"current_chapter":9}`)
	assert.Equal(t, http.StatusNotFound, code, "bob cannot edit alice's entry")

	// TCP: forged token → error line, connection closed, nothing registered.
	conn, err := net.Dial("tcp", s.tcp)
	require.NoError(t, err)
	defer conn.Close()
	_, _ = conn.Write([]byte(`{"type":"auth","token":"` + forged + `"}` + "\n"))
	assert.JSONEq(t, `{"type":"error","error":"invalid token"}`, string(testutil.ReadLine(t, conn, 2*time.Second)))

	// UDP: forged token → error, not registered (a release reaches nobody).
	u := udpPeer(t, s.udp)
	_, _ = u.Write([]byte(`{"type":"subscribe","token":"` + forged + `"}`))
	got, err := udpRead(u, time.Second)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"error","error":"invalid token"}`, got)
	_, out := call(t, "POST", s.url+client.PathLogin, "", `{"username":"admin","password":"Admin12345"}`)
	_, out = call(t, "POST", s.url+"/api/admin/manga/one-piece/release", between(out, `"token":"`, `"`), "")
	assert.Contains(t, out, `"udp_sent":0`)
	_, err = udpRead(u, 300*time.Millisecond)
	assert.Error(t, err, "the rejected socket receives no alert")

	// WebSocket: the upgrade itself is refused with 401.
	for _, tok := range []string{"", forged} {
		_, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": {"Bearer " + tok}})
		require.ErrorIs(t, err, websocket.ErrBadHandshake)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}
}
