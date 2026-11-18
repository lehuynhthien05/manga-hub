package integration_test

import (
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/app"
	"mangahub/internal/tui/client"
)

// 10.2.3: a TUI with all real-time clients up quits without leaking goroutines.
func TestTUI_QuitLeavesNoGoroutines(t *testing.T) {
	_, catAddr := serveCatalog(t, "127.0.0.1:0")
	h, rt := newBackend(t, catAddr, 2*time.Second, true)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	syncStack{url: srv.URL}.user(t, "alice", "Alice1234")
	before := runtime.NumGoroutine()

	sim := tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, sim.Init())
	sim.SetSize(80, 24)
	a := app.New(client.NewAPI(srv.URL), app.Addrs{TCP: rt.tcp, UDP: rt.udp, WS: "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"}, quiet)
	a.SetScreen(sim)
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	s := &screen{t, a, sim}
	s.login("alice", "Alice1234")
	s.waitFor("TCP ● UDP ● WS ●")

	a.Quit()
	require.NoError(t, <-done)
	srv.CloseClientConnections() // the HTTP keep-alive conns are the test server's, not the TUI's
	testutil.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+2 }, 3*time.Second,
		fmt.Sprintf("TUI goroutines leaked (before=%d)", before))
}
