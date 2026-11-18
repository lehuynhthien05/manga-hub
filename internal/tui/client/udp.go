package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"mangahub/pkg/protocol"
)

// NotifyClient states.
const (
	UDPSubscribing = "subscribing"
	UDPSubscribed  = "subscribed"
	UDPUnconfirmed = "not confirmed (UDP is best-effort) — retrying"
	UDPUnreachable = "server unreachable"
)

// NotifyClient subscribes to new-chapter alerts over UDP. It uses one *connected*
// socket for subscribe, pings and alerts: the server replies to its port, and the OS
// reports an ICMP "port unreachable" as ECONNREFUSED on it. Callbacks run on the
// client's goroutine: the TUI must Post.
type NotifyClient struct {
	Addr, Token string
	OnEvent     func(protocol.NewChapter)
	OnState     func(state string, err error)

	// Tunables; zero means the default. Tests shorten them.
	PingEvery, AckTimeout, MinBackoff, MaxBackoff time.Duration
}

// Run keeps a subscription alive until ctx ends, then sends a best-effort unsubscribe.
func (c *NotifyClient) Run(ctx context.Context) {
	minB, maxB := or(c.MinBackoff, time.Second), or(c.MaxBackoff, 30*time.Second)
	backoff := minB
	for ctx.Err() == nil {
		state, err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.OnState(state, err)
		if state == UDPSubscribed { // the subscription worked; it just went quiet
			backoff = minB
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, maxB)
	}
}

func unreachable(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

// session subscribes (3 tries) and then reads alerts until the link looks dead.
// It returns the state to report and why it ended.
func (c *NotifyClient) session(ctx context.Context) (string, error) {
	raddr, err := net.ResolveUDPAddr("udp", c.Addr)
	if err != nil {
		return UDPUnreachable, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return UDPUnreachable, err
	}
	defer conn.Close()
	// Wake a blocked Read on cancel without closing, so we can still say goodbye.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	c.OnState(UDPSubscribing, nil)
	sub, _ := json.Marshal(protocol.UDPSubscribe{Type: protocol.TypeSubscribe, Token: c.Token})
	buf := make([]byte, 2048)
	ack := or(c.AckTimeout, 2*time.Second)
	confirmed := false
	for try := 0; try < 3 && !confirmed && ctx.Err() == nil; try++ {
		if _, err := conn.Write(sub); err != nil {
			if unreachable(err) {
				return UDPUnreachable, err
			}
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(ack))
		n, err := conn.Read(buf)
		switch {
		case unreachable(err):
			return UDPUnreachable, err
		case err != nil: // timeout: the datagram or its reply was lost; try again
			continue
		}
		switch t, _ := protocol.PeekType(buf[:n]); t {
		case protocol.TypeSubscribed:
			confirmed = true
		case protocol.TypeError:
			var e protocol.UDPError
			_ = json.Unmarshal(buf[:n], &e)
			return UDPUnconfirmed, fmt.Errorf("subscribe rejected: %s", e.Error)
		}
	}
	if !confirmed {
		return UDPUnconfirmed, ctx.Err()
	}
	c.OnState(UDPSubscribed, nil)
	defer c.unsubscribe(conn, ctx)

	// Keep-alive: after every quiet period we ping; any datagram resets the count.
	// Two pings without a reply ⇒ the server forgot us or is gone ⇒ resubscribe.
	every := or(c.PingEvery, 30*time.Second)
	ping, _ := json.Marshal(protocol.UDPPing{Type: protocol.TypePing})
	unanswered := 0
	for {
		_ = conn.SetReadDeadline(time.Now().Add(every))
		n, err := conn.Read(buf)
		if ctx.Err() != nil {
			return UDPSubscribed, nil
		}
		var ne net.Error
		switch {
		case unreachable(err):
			return UDPUnreachable, err
		case errors.As(err, &ne) && ne.Timeout():
			if unanswered == 2 {
				return UDPSubscribed, errors.New("no pong: resubscribing")
			}
			unanswered++
			if _, err := conn.Write(ping); unreachable(err) {
				return UDPUnreachable, err
			}
			continue
		case err != nil:
			return UDPUnreachable, err
		}
		unanswered = 0
		if t, _ := protocol.PeekType(buf[:n]); t == protocol.TypeNewChapter {
			var ev protocol.NewChapter
			if json.Unmarshal(buf[:n], &ev) == nil {
				c.OnEvent(ev)
			}
		}
	}
}

// unsubscribe is best effort: one datagram, no waiting for the reply.
func (c *NotifyClient) unsubscribe(conn *net.UDPConn, ctx context.Context) {
	if ctx.Err() == nil {
		return // the link died; the server's TTL will drop us
	}
	b, _ := json.Marshal(protocol.UDPUnsubscribe{Type: protocol.TypeUnsubscribe, Token: c.Token})
	_ = conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	_, _ = conn.Write(b)
}
