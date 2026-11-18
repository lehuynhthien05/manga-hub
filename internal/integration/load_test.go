//go:build load

// Run: go test -tags load -run 'TestLoad|TestSoak' -v ./internal/integration
package integration_test

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

type timings struct {
	mu   sync.Mutex
	by   map[string][]time.Duration
	errs map[string]int
}

func (tm *timings) add(step string, d time.Duration, ok bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.by[step] = append(tm.by[step], d)
	if !ok {
		tm.errs[step]++
	}
}

func pct(ds []time.Duration, p float64) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*p)]
}

// 10.5.1: 50 users in parallel: register → login → search → add → list → update.
func TestLoad(t *testing.T) {
	_, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, _ := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	tm := &timings{by: map[string][]time.Duration{}, errs: map[string]int{}}
	step := func(name, method, url, token, body string, want int) string {
		start := time.Now()
		code, out := call(t, method, url, token, body)
		tm.add(name, time.Since(start), code == want)
		return out
	}

	const users = 50
	begin := time.Now()
	var wg sync.WaitGroup
	for i := range users {
		wg.Go(func() {
			cred := fmt.Sprintf(`{"username":"load%02d","password":"Passw0rdX"}`, i)
			step("register", "POST", srv.URL+client.PathRegister, "", cred, 201)
			out := step("login", "POST", srv.URL+client.PathLogin, "", cred, 200)
			tok := between(out, `"token":"`, `"`)
			step("search", "GET", srv.URL+"/api/manga?q=one", tok, "", 200)
			step("add", "POST", srv.URL+client.PathLibrary, tok, `{"manga_id":"one-piece"}`, 201)
			step("list", "GET", srv.URL+client.PathLibrary, tok, "", 200)
			step("update", "PUT", srv.URL+client.PathLibrary+"/one-piece/progress", tok, `{"current_chapter":25}`, 200)
		})
	}
	wg.Wait()
	t.Logf("%d users in %v (bcrypt at MinCost in tests; production uses DefaultCost)", users, time.Since(begin).Round(time.Millisecond))
	t.Logf("%-9s %5s %9s %9s %9s %6s", "step", "n", "p50", "p90", "max", "errors")
	total := 0
	for _, name := range []string{"register", "login", "search", "add", "list", "update"} {
		ds := tm.by[name]
		t.Logf("%-9s %5d %9v %9v %9v %6d", name, len(ds), pct(ds, .5).Round(time.Microsecond),
			pct(ds, .9).Round(time.Microsecond), pct(ds, 1).Round(time.Microsecond), tm.errs[name])
		total += tm.errs[name]
	}
	assert.Zero(t, total, "every request succeeded (no 'database is locked' reached a user)")
	assert.Less(t, pct(tm.by["search"], .9), 500*time.Millisecond, "reference target: search p90 < 500 ms")
}

func between(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	v, _, _ := strings.Cut(rest, to)
	return v
}

// 10.5.2: 20 TCP + 20 WS clients for 60 s with periodic traffic; goroutines and heap stay flat.
func TestSoak(t *testing.T) {
	_, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, rt := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := syncStack{srv.URL, rt.tcp, rt.udp}
	// Two users with 10 TCP sessions each: 10 is the per-user session limit.
	toks := []string{s.user(t, "soak0", "Passw0rdX"), s.user(t, "soak1", "Passw0rdX")}
	for _, tok := range toks {
		code, _ := call(t, "POST", s.url+client.PathLibrary, tok, `{"manga_id":"one-piece"}`)
		require.Equal(t, http.StatusCreated, code)
	}

	stats := func() (int, uint64) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return runtime.NumGoroutine(), m.HeapAlloc
	}
	g0, h0 := stats()

	var received sync.WaitGroup
	var tcpGot, wsGot int64
	var mu sync.Mutex
	var tcpConns []*nc
	for i := range 20 {
		n := dialSync(t, s.tcp, toks[i%2])
		tcpConns = append(tcpConns, n)
		received.Go(func() {
			r := bufio.NewReader(n.conn)
			for {
				_ = n.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				if _, err := r.ReadBytes('\n'); err != nil {
					return
				}
				mu.Lock()
				tcpGot++
				mu.Unlock()
			}
		})
	}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	var wsConns []*websocket.Conn
	for i := range 20 {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Authorization": {"Bearer " + toks[i%2]}})
		require.NoError(t, err)
		wsConns = append(wsConns, c)
		require.NoError(t, c.WriteJSON(protocol.WSJoin{Type: "join", Room: fmt.Sprintf("room%d", i%3)}))
		received.Go(func() {
			for {
				_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
				mu.Lock()
				wsGot++
				mu.Unlock()
			}
		})
	}
	g1, h1 := stats()

	end := time.Now().Add(60 * time.Second)
	for i := 0; time.Now().Before(end); i++ { // two progress updates and three chat lines per second
		for _, tok := range toks {
			call(t, "PUT", s.url+client.PathLibrary+"/one-piece/progress", tok, fmt.Sprintf(`{"current_chapter":%d}`, i%1100))
		}
		if i%15 == 14 { // like the TUI client: ping every 15 s, or the server drops us after 45 s of silence
			for _, n := range tcpConns {
				_, _ = n.conn.Write([]byte(`{"type":"ping"}` + "\n"))
			}
		}
		for r := range 3 {
			_ = wsConns[r].WriteJSON(protocol.WSChat{Type: "chat", Room: fmt.Sprintf("room%d", r), Text: fmt.Sprintf("tick %d", i)})
		}
		time.Sleep(time.Second) // pacing the load generator, not synchronising
	}
	g2, h2 := stats()
	mu.Lock()
	t.Logf("TCP events received: %d, WS frames received: %d", tcpGot, wsGot)
	mu.Unlock()
	t.Logf("goroutines: idle %d → 40 clients %d → after 60 s %d", g0, g1, g2)
	t.Logf("heap:       idle %d KiB → 40 clients %d KiB → after 60 s %d KiB", h0>>10, h1>>10, h2>>10)
	assert.LessOrEqual(t, g2, g1+5, "goroutines stay flat during the soak")
	for _, c := range wsConns {
		_ = c.Close()
	}
}
