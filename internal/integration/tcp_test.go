package integration_test

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/app"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

type syncStack struct {
	url, tcp, udp string
}

func newSyncStack(t *testing.T) syncStack {
	t.Helper()
	_, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, rt := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return syncStack{srv.URL, rt.tcp, rt.udp}
}

// call is a tiny curl: it returns the status code and the body.
func call(t *testing.T, method, url, token, body string, hdr ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var b strings.Builder
	_, _ = bufio.NewReader(resp.Body).WriteTo(&b)
	return resp.StatusCode, b.String()
}

func (s syncStack) user(t *testing.T, name, pw string) string {
	t.Helper()
	body := `{"username":"` + name + `","password":"` + pw + `"}`
	code, _ := call(t, "POST", s.url+client.PathRegister, "", body)
	require.Equal(t, http.StatusCreated, code)
	code, out := call(t, "POST", s.url+client.PathLogin, "", body)
	require.Equal(t, http.StatusOK, code)
	var lr protocol.LoginResponse
	require.NoError(t, json.Unmarshal([]byte(out), &lr))
	return lr.Token
}

// nc is what `nc localhost 9090` does in Gate 7: auth line, then read lines.
type nc struct {
	conn net.Conn
	r    *bufio.Reader
	id   string
}

func dialSync(t *testing.T, addr, token string) *nc {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, _ = conn.Write([]byte(`{"type":"auth","token":"` + token + `"}` + "\n"))
	n := &nc{conn: conn, r: bufio.NewReader(conn)}
	var ok protocol.TCPAuthOK
	require.NoError(t, json.Unmarshal(n.line(t), &ok))
	require.Equal(t, protocol.TypeAuthOK, ok.Type)
	n.id = ok.SessionID
	return n
}

func (n *nc) line(t *testing.T) []byte {
	t.Helper()
	_ = n.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	l, err := n.r.ReadBytes('\n')
	require.NoError(t, err)
	return l
}

// Gate 7 steps 1–5 with the real router, hub and catalog.
func TestSync_HTTPUpdateReachesOnlyOtherSessionsOfSameUser(t *testing.T) {
	s := newSyncStack(t)
	alice, bob := s.user(t, "alice", "Alice1234"), s.user(t, "bob", "Bob12345x")
	code, _ := call(t, "POST", s.url+client.PathLibrary, alice, `{"manga_id":"one-piece","status":"reading"}`)
	require.Equal(t, http.StatusCreated, code)

	a1, a2, b1 := dialSync(t, s.tcp, alice), dialSync(t, s.tcp, alice), dialSync(t, s.tcp, bob)
	put := s.url + client.PathLibrary + "/one-piece/progress"

	code, _ = call(t, "PUT", put, alice, `{"current_chapter":30}`, "X-Session-ID", a1.id) // a1 is the origin
	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"type":"progress_updated","manga_id":"one-piece","chapter":30,"version":2,"status":"reading"}`, string(a2.line(t)))
	testutil.ExpectNoData(t, a1.conn, 300*time.Millisecond) // no echo to the origin
	testutil.ExpectNoData(t, b1.conn, 300*time.Millisecond) // another user gets nothing

	code, body := call(t, "PUT", put, alice, `{"current_chapter":5000}`)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "1100")
	testutil.ExpectNoData(t, a2.conn, 300*time.Millisecond) // rejected update: no event

	_, _ = a2.conn.Write([]byte(`{"type":"ping"}` + "\n"))
	assert.JSONEq(t, `{"type":"pong"}`, string(a2.line(t)))

	code, _ = call(t, "PUT", put, alice, `{"status":"completed"}`) // no X-Session-ID: every alice session gets it
	require.Equal(t, http.StatusOK, code)
	for _, n := range []*nc{a1, a2} {
		assert.JSONEq(t, `{"type":"progress_updated","manga_id":"one-piece","chapter":30,"version":3,"status":"completed"}`, string(n.line(t)))
	}
}

// Gate 7 step 8: two TUIs of alice; A updates, B's row changes with no key press.
func TestSync_TwoTUIs(t *testing.T) {
	s := newSyncStack(t)
	alice := s.user(t, "alice", "Alice1234")
	code, _ := call(t, "POST", s.url+client.PathLibrary, alice, `{"manga_id":"one-piece","status":"reading"}`)
	require.Equal(t, http.StatusCreated, code)

	a, b := startTUI(t, s.url, app.Addrs{TCP: s.tcp}), startTUI(t, s.url, app.Addrs{TCP: s.tcp})
	for _, x := range []*screen{a, b} {
		x.login("alice", "Alice1234")
		x.waitFor("TCP ●")
		x.keys("2")
		x.waitFor("0/1100")
	}

	a.keys("u")
	a.key(tcell.KeyCtrlU, tcell.ModCtrl)
	a.keys("40")
	a.key(tcell.KeyEnter, 0)
	a.waitFor("40/1100")

	b.waitFor("40/1100") // pushed over TCP; B pressed nothing
	b.waitFor("Synced from another session: One Piece (ワンピース) → ch. 40")
	assert.NotContains(t, a.text(), "Synced from another session", "A's own change is not echoed back")
}
