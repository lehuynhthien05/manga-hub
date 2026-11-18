package integration_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

// udpPeer is what `nc -u` does in Gate 8.
func udpPeer(t *testing.T, addr string) *net.UDPConn {
	t.Helper()
	raddr, err := net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)
	c, err := net.DialUDP("udp", nil, raddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func udpRead(c *net.UDPConn, d time.Duration) (string, error) {
	_ = c.SetReadDeadline(time.Now().Add(d))
	b := make([]byte, 2048)
	n, err := c.Read(b)
	return string(b[:n]), err
}

// Gate 8 steps 1–8 with the real router, catalog (gRPC) and UDP notifier.
func TestRelease_UDPToSubscribersOnly(t *testing.T) {
	cat, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, rt := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := syncStack{srv.URL, rt.tcp, rt.udp}
	alice := s.user(t, "alice", "Alice1234")
	_, out := call(t, "POST", s.url+client.PathLogin, "", `{"username":"admin","password":"Admin12345"}`)
	var lr protocol.LoginResponse
	require.NoError(t, json.Unmarshal([]byte(out), &lr))
	admin := lr.Token
	release := s.url + "/api/admin/manga/one-piece/release"

	sub, never := udpPeer(t, rt.udp), udpPeer(t, rt.udp)
	_, _ = sub.Write([]byte(`{"type":"subscribe","token":"` + alice + `"}`))
	got, err := udpRead(sub, time.Second)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"subscribed"}`, got)

	tui := startTUI(t, s.url, "", rt.udp)
	tui.login("alice", "Alice1234")
	tui.waitFor("UDP ●")

	code, body := call(t, "POST", release, alice, "")
	assert.Equal(t, http.StatusForbidden, code, "a user token cannot release")
	code, body = call(t, "POST", release, admin, "")
	require.Equal(t, http.StatusOK, code, body)
	var res protocol.ReleaseResponse
	require.NoError(t, json.Unmarshal([]byte(body), &res))
	assert.Equal(t, 1101, res.Chapter)
	assert.Equal(t, 2, res.UDPSent, "the nc-like peer and the TUI")

	got, err = udpRead(sub, time.Second)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"new_chapter","manga_id":"one-piece","title":"One Piece (ワンピース)","chapter":1101}`, got)
	_, err = udpRead(never, 300*time.Millisecond)
	assert.Error(t, err, "a socket that never subscribed receives nothing")
	tui.waitFor("New chapter: One Piece (ワンピース) — ch. 1101")
	tui.waitFor("1 new alert")
	tui.keys("4")
	tui.waitFor("1101")

	code, body = call(t, "GET", s.url+"/api/manga/one-piece", "", "")
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"total_chapters":1101`, "the catalog really changed (through gRPC)")

	_, _ = sub.Write([]byte(`{"type":"unsubscribe","token":"` + alice + `"}`))
	got, _ = udpRead(sub, time.Second)
	assert.JSONEq(t, `{"type":"unsubscribed"}`, got)

	cat.Stop() // Gate 8.8: a catalog failure sends nothing
	code, _ = call(t, "POST", release, admin, "")
	assert.Equal(t, http.StatusServiceUnavailable, code)
	_, err = udpRead(sub, 300*time.Millisecond)
	assert.Error(t, err)
}
