package client

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"mangahub/pkg/protocol"
)

// ChatClient keeps one WebSocket to the chat hub, reconnects with backoff and
// re-joins the rooms the user had open. OnFrame gets every server frame raw (the
// TUI decodes by "type"); callbacks run on the client's goroutines: Post, don't draw.
type ChatClient struct {
	URL, Token string
	OnFrame    func(raw []byte)
	OnState    func(state string, err error) // StateConnected / StateDisconnected / StateConnecting

	MinBackoff, MaxBackoff time.Duration // zero means 1 s / 30 s

	mu        sync.Mutex
	rooms     map[string]bool // rooms to (re)join
	out       chan []byte     // the current connection's writer queue; nil while down
	connected atomic.Bool
}

// Send queues a frame for the writer goroutine; false while disconnected or full.
func (c *ChatClient) Send(v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.out == nil {
		return false
	}
	select {
	case c.out <- b:
		return true
	default:
		return false
	}
}

// Join remembers the room (for rejoin after a reconnect) and sends the join.
func (c *ChatClient) Join(room string) bool {
	c.mu.Lock()
	if c.rooms == nil {
		c.rooms = map[string]bool{}
	}
	c.rooms[room] = true
	c.mu.Unlock()
	return c.Send(protocol.WSJoin{Type: protocol.TypeJoin, Room: room})
}

// Leave forgets the room (no rejoin after a reconnect) and sends the leave.
func (c *ChatClient) Leave(room string) bool {
	c.mu.Lock()
	delete(c.rooms, room)
	c.mu.Unlock()
	return c.Send(protocol.WSLeave{Type: protocol.TypeLeave, Room: room})
}

// Connected reports whether a WebSocket connection is currently open.
func (c *ChatClient) Connected() bool { return c.connected.Load() }

// Run connects until ctx ends, rejoining the remembered rooms after every
// reconnect; the backoff doubles from MinBackoff up to MaxBackoff.
func (c *ChatClient) Run(ctx context.Context) {
	minB, maxB := or(c.MinBackoff, time.Second), or(c.MaxBackoff, 30*time.Second)
	backoff := minB
	for ctx.Err() == nil {
		c.OnState(StateConnecting, nil)
		up, err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.OnState(StateDisconnected, err)
		if up {
			backoff = minB
		}
		select {
		case <-time.After(backoff/2 + rand.N(backoff/2+1)): // jitter: no reconnect storm after a restart
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, maxB)
	}
}

func (c *ChatClient) session(ctx context.Context) (bool, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.URL, http.Header{"Authorization": {"Bearer " + c.Token}})
	if err != nil {
		return false, err
	}
	defer conn.Close()

	out := make(chan []byte, 64)
	c.mu.Lock()
	c.out = out
	for room := range c.rooms { // re-join what the user had open (queued before anything else)
		b, _ := json.Marshal(protocol.WSJoin{Type: protocol.TypeJoin, Room: room})
		out <- b
	}
	c.mu.Unlock()
	c.connected.Store(true)
	c.OnState(StateConnected, nil)
	defer func() {
		c.connected.Store(false)
		c.mu.Lock()
		c.out = nil
		c.mu.Unlock()
	}()

	readDone := make(chan error, 1)
	go func() { // the only reader
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				readDone <- err
				return
			}
			c.OnFrame(data)
		}
	}()
	for { // this goroutine is the only writer
		select {
		case b := <-out:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
				return true, err
			}
		case err := <-readDone:
			return true, err
		case <-ctx.Done(): // say goodbye properly; the server then unregisters us at once
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			return true, nil
		}
	}
}
