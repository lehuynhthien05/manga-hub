package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"mangahub/pkg/protocol"
)

// Connection states reported through OnState.
const (
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateDisconnected = "disconnected"
)

// SyncClient keeps one long-lived TCP session for progress sync and reconnects
// with backoff. Callbacks run on its own goroutine: the TUI must Post, not draw.
type SyncClient struct {
	Addr    string
	Token   string
	OnEvent func(protocol.ProgressUpdated)
	OnState func(state, sessionID string, err error)

	// Tunables; zero values mean the defaults. Tests shorten them.
	MinBackoff, MaxBackoff, PingEvery, ReadTimeout time.Duration

	sessionID atomic.Pointer[string]
}

// SessionID is the current server session id, empty while disconnected. It is
// sent as X-Session-ID so the hub does not echo our own updates back to us.
func (c *SyncClient) SessionID() string {
	if p := c.sessionID.Load(); p != nil {
		return *p
	}
	return ""
}

func or(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// Run connects until ctx ends; backoff doubles (with jitter) up to MaxBackoff and
// resets after every successful auth.
func (c *SyncClient) Run(ctx context.Context) {
	minB, maxB := or(c.MinBackoff, time.Second), or(c.MaxBackoff, 30*time.Second)
	backoff := minB
	for ctx.Err() == nil {
		c.OnState(StateConnecting, "", nil)
		authed, err := c.connectOnce(ctx)
		c.sessionID.Store(nil)
		if ctx.Err() != nil {
			return
		}
		c.OnState(StateDisconnected, "", err)
		if authed {
			backoff = minB
		}
		wait := backoff/2 + rand.N(backoff/2+1) // jitter: many clients do not reconnect in lockstep
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, maxB)
	}
}

func (c *SyncClient) connectOnce(ctx context.Context) (authed bool, err error) {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	var wmu sync.Mutex // one writer at a time: the auth line here, then the pinger
	write := func(v any) error {
		line, err := protocol.EncodeLine(v)
		if err != nil {
			return err
		}
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Write(line)
		return err
	}
	if err := write(protocol.TCPAuth{Type: protocol.TypeAuth, Token: c.Token}); err != nil {
		return false, err
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := r.ReadSlice('\n')
	if err != nil {
		return false, err
	}
	var ok protocol.TCPAuthOK
	if t, _ := protocol.PeekType(line); t != protocol.TypeAuthOK || json.Unmarshal(line, &ok) != nil {
		var e protocol.TCPError
		_ = json.Unmarshal(line, &e)
		return false, fmt.Errorf("auth rejected: %s", e.Error)
	}
	c.sessionID.Store(&ok.SessionID)
	c.OnState(StateConnected, ok.SessionID, nil)

	pingDone := make(chan struct{})
	defer close(pingDone)
	go func() { // keep-alive; the server closes sessions silent for 45 s
		t := time.NewTicker(or(c.PingEvery, 15*time.Second))
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if write(protocol.TCPPing{Type: protocol.TypePing}) != nil {
					_ = conn.Close()
					return
				}
			case <-pingDone:
				return
			}
		}
	}()

	readTimeout := or(c.ReadTimeout, 45*time.Second) // pongs arrive every 15 s, so 45 s of silence = dead link
	for {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
		line, err := r.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, bufio.ErrBufferFull) {
				err = errors.New("line too long")
			}
			return true, err
		}
		if t, _ := protocol.PeekType(line); t == protocol.TypeProgressUpdated {
			var ev protocol.ProgressUpdated
			if json.Unmarshal(line, &ev) == nil {
				c.OnEvent(ev)
			}
		} // pong and unknown types: nothing to do
	}
}
