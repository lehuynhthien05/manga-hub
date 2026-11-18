package integration_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"mangahub/internal/auth"
	"mangahub/internal/backend/notifyudp"
	"mangahub/internal/clock"
	"mangahub/internal/testutil"
	"mangahub/pkg/protocol"
	"mangahub/proto/mangaproto"
)

// lossyProxy sits between one UDP client and the server and drops each datagram,
// in both directions, with probability drop (seeded, so the pattern is reproducible).
// The server sees the proxy's back socket as the subscriber address.
type lossyProxy struct {
	mu      sync.Mutex
	rng     *rand.Rand
	drop    float64
	client  net.Addr
	dropped int
}

func (p *lossyProxy) lose() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rng.Float64() < p.drop {
		p.dropped++
		return true
	}
	return false
}

func startLossyProxy(t *testing.T, server string, drop float64, seed uint64) (string, *lossyProxy) {
	t.Helper()
	front, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	raddr, err := net.ResolveUDPAddr("udp", server)
	require.NoError(t, err)
	back, err := net.DialUDP("udp", nil, raddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = front.Close(); _ = back.Close() })
	p := &lossyProxy{rng: rand.New(rand.NewPCG(seed, 1)), drop: drop}
	go func() { // client → server
		buf := make([]byte, 2048)
		for {
			n, from, err := front.ReadFrom(buf)
			if err != nil {
				return
			}
			p.mu.Lock()
			p.client = from
			p.mu.Unlock()
			if !p.lose() {
				_, _ = back.Write(buf[:n])
			}
		}
	}()
	go func() { // server → client
		buf := make([]byte, 2048)
		for {
			n, err := back.Read(buf)
			if err != nil {
				return
			}
			p.mu.Lock()
			to := p.client
			p.mu.Unlock()
			if to != nil && !p.lose() {
				_, _ = front.WriteTo(buf[:n], to)
			}
		}
	}()
	return front.LocalAddr().String(), p
}

// 13.1.1: with 50% loss each way, Notify reports 100 sent but only about half arrive.
func TestUDP_LossyNetwork(t *testing.T) {
	mgr, err := auth.NewManager("test-secret-0123456789", time.Hour)
	require.NoError(t, err)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := notifyudp.NewServer(mgr, clock.System{}, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, pc); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	addr, proxy := startLossyProxy(t, pc.LocalAddr().String(), 0.5, 42)
	c := udpPeer(t, addr)
	tok, _, _ := mgr.Issue("usr_a", "alice", "user")
	tries := 0
	for subscribed := false; !subscribed; tries++ { // the client's retry logic: resend until acked
		require.Less(t, tries, 40, "never got an ack through the lossy link")
		_, _ = c.Write([]byte(`{"type":"subscribe","token":"` + tok + `"}`))
		got, err := udpRead(c, 150*time.Millisecond)
		subscribed = err == nil && strings.Contains(got, "subscribed")
	}

	sent := 0
	for i := range 100 {
		sent += srv.Notify(protocol.NewChapter{Type: protocol.TypeNewChapter, MangaID: "one-piece", Chapter: 1101 + i})
	}
	received := 0
	for {
		if _, err := udpRead(c, 300*time.Millisecond); err != nil { // waiting for the absence of more datagrams
			break
		}
		received++
	}
	proxy.mu.Lock()
	dropped := proxy.dropped // the proxy goroutines still run: read under the lock
	proxy.mu.Unlock()
	t.Logf("subscribe needed %d tries; Notify: sent=%d, received=%d; proxy dropped %d datagrams in total",
		tries, sent, received, dropped)
	assert.Equal(t, 100, sent, "sent counts datagrams handed to the OS")
	assert.GreaterOrEqual(t, received, 30)
	assert.LessOrEqual(t, received, 70, "best-effort: no retransmission, so about half is lost")
}

// 13.1.3: a catalog that never answers in time: 100 concurrent requests all get a
// bounded 503 and no goroutine is left behind.
func TestCatalog_FrozenStub100Concurrent(t *testing.T) {
	lis := testutil.ListenLoopback(t)
	g := grpc.NewServer()
	mangaproto.RegisterMangaServiceServer(g, slowCatalog{})
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	srv := httptest.NewServer(backend(t, lis.Addr().String(), 200*time.Millisecond))
	t.Cleanup(srv.Close)
	call(t, "GET", srv.URL+"/api/manga?q=warm", "", "") // establish the gRPC connection first
	before := runtime.NumGoroutine()

	var mu sync.Mutex
	var worst time.Duration
	codes := map[int]int{}
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			begin := time.Now()
			code, _ := call(t, "GET", fmt.Sprintf("%s/api/manga?q=q%d", srv.URL, i), "", "")
			d := time.Since(begin)
			mu.Lock()
			codes[code]++
			worst = max(worst, d)
			mu.Unlock()
		})
	}
	wg.Wait()
	t.Logf("100 concurrent searches against a frozen catalog: %v, worst %v", codes, worst.Round(time.Millisecond))
	assert.Equal(t, map[int]int{http.StatusServiceUnavailable: 100}, codes)
	assert.Less(t, worst, 400*time.Millisecond, "every answer within the 200 ms deadline + slack")
	srv.CloseClientConnections()
	testutil.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+5 }, time.Second,
		"goroutines back to baseline")
}
