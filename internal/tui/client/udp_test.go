package client_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mangahub/internal/testutil"
	"mangahub/internal/tui/client"
	"mangahub/pkg/protocol"
)

// fakeNotifier answers subscribe and (when pongs is true) ping, and records what it got.
type fakeNotifier struct {
	pc    net.PacketConn
	mu    sync.Mutex
	got   []string
	peer  net.Addr
	pongs bool
}

func newFakeNotifier(t *testing.T) *fakeNotifier {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	f := &fakeNotifier{pc: pc, pongs: true}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			msg := string(buf[:n])
			f.mu.Lock()
			f.got, f.peer = append(f.got, msg), addr
			pongs := f.pongs
			f.mu.Unlock()
			switch {
			case strings.Contains(msg, `"subscribe"`) && strings.Contains(msg, `"token":"tok"`):
				_, _ = pc.WriteTo([]byte(`{"type":"subscribed"}`), addr)
			case strings.Contains(msg, `"subscribe"`):
				_, _ = pc.WriteTo([]byte(`{"type":"error","error":"invalid token"}`), addr)
			case strings.Contains(msg, `"ping"`) && pongs:
				_, _ = pc.WriteTo([]byte(`{"type":"pong"}`), addr)
			}
		}
	}()
	return f
}

func (f *fakeNotifier) count(typ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.got {
		if strings.Contains(m, `"type":"`+typ+`"`) {
			n++
		}
	}
	return n
}

func (f *fakeNotifier) send(t *testing.T, msg string) {
	f.mu.Lock()
	peer := f.peer
	f.mu.Unlock()
	_, err := f.pc.WriteTo([]byte(msg), peer)
	require.NoError(t, err)
}

type udpRec struct {
	mu     sync.Mutex
	states []string
	events []protocol.NewChapter
}

func (r *udpRec) state(s string, _ error) { r.mu.Lock(); r.states = append(r.states, s); r.mu.Unlock() }
func (r *udpRec) event(e protocol.NewChapter) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}
func (r *udpRec) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.states) == 0 {
		return ""
	}
	return r.states[len(r.states)-1]
}

func runNotify(t *testing.T, c *client.NotifyClient) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return func() { cancel(); <-done }
}

func TestNotifyClient_SubscribeReceiveUnsubscribe(t *testing.T) {
	f := newFakeNotifier(t)
	rec := &udpRec{}
	stop := runNotify(t, &client.NotifyClient{Addr: f.pc.LocalAddr().String(), Token: "tok", OnState: rec.state, OnEvent: rec.event})
	testutil.Eventually(t, func() bool { return rec.last() == client.UDPSubscribed }, 2*time.Second, "subscribed")

	f.send(t, `{"type":"new_chapter","manga_id":"one-piece","title":"One Piece","chapter":1101}`)
	testutil.Eventually(t, func() bool { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.events) == 1 }, 2*time.Second, "alert")
	assert.Equal(t, 1101, rec.events[0].Chapter)

	stop()
	testutil.Eventually(t, func() bool { return f.count("unsubscribe") == 1 }, time.Second, "unsubscribe on exit")
}

func TestNotifyClient_ResubscribesWhenPongsStop(t *testing.T) {
	f := newFakeNotifier(t)
	rec := &udpRec{}
	runNotify(t, &client.NotifyClient{Addr: f.pc.LocalAddr().String(), Token: "tok", OnState: rec.state, OnEvent: rec.event,
		PingEvery: 40 * time.Millisecond, MinBackoff: 10 * time.Millisecond})
	testutil.Eventually(t, func() bool { return f.count("ping") >= 2 }, 2*time.Second, "pings while answered")
	assert.Equal(t, 1, f.count("subscribe"), "answered pings keep the subscription")

	f.mu.Lock()
	f.pongs = false // e.g. the backend restarted and forgot us
	f.mu.Unlock()
	testutil.Eventually(t, func() bool { return f.count("subscribe") >= 2 }, 2*time.Second, "resubscribed after two missed pongs")
}

func TestNotifyClient_UnreachableAndRejected(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := pc.LocalAddr().String()
	require.NoError(t, pc.Close()) // nobody listens: ICMP port unreachable → ECONNREFUSED on a connected socket
	rec := &udpRec{}
	runNotify(t, &client.NotifyClient{Addr: addr, Token: "tok", OnState: rec.state, OnEvent: rec.event,
		AckTimeout: 100 * time.Millisecond, MinBackoff: 10 * time.Millisecond})
	testutil.Eventually(t, func() bool { return rec.last() == client.UDPUnreachable }, 2*time.Second, "unreachable")

	f := newFakeNotifier(t)
	bad := &udpRec{}
	runNotify(t, &client.NotifyClient{Addr: f.pc.LocalAddr().String(), Token: "bad", OnState: bad.state, OnEvent: bad.event,
		MinBackoff: 10 * time.Millisecond})
	testutil.Eventually(t, func() bool { return bad.last() == client.UDPUnconfirmed }, 2*time.Second, "rejected")
}
