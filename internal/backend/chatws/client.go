package chatws

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"mangahub/internal/auth"
	"mangahub/pkg/protocol"
)

// Client is one WebSocket connection: exactly one reader (readPump) and one
// writer (writePump) goroutine, as gorilla/websocket requires.
type Client struct {
	hub              *Hub
	conn             *websocket.Conn
	userID, username string
	send             chan []byte   // never closed: a broadcaster may still be sending
	done             chan struct{} // closed once by close(); stops writePump
	once             sync.Once
	rooms            map[string]struct{} // guarded by hub.mu
	bucket           bucket              // used only by readPump
}

func (c *Client) enqueue(b []byte) bool {
	select {
	case c.send <- b:
		return true
	default:
		return false
	}
}

func (c *Client) close(reason string) {
	c.once.Do(func() {
		close(c.done)
		c.hub.log.Info("ws close", "user", c.username, "reason", reason)
	})
}

// bucket is a token bucket: MsgRate tokens per second, at most MsgBurst saved up.
type bucket struct {
	tokens, rate, burst float64
	last                time.Time
}

func (b *bucket) allow(now time.Time) bool {
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister(c)
		c.close("read ended")
	}()
	lim := c.hub.lim
	c.conn.SetReadLimit(lim.ReadLimit) // an oversize frame ends the connection
	_ = c.conn.SetReadDeadline(time.Now().Add(lim.PongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(lim.PongWait)) })
	for {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if err := c.handle(mt, data); err != nil {
			c.enqueue(encode(protocol.WSError{Type: protocol.TypeError, Error: err.Error()}))
		}
	}
}

// handle errors go back to the sender only; the connection stays open.
func (c *Client) handle(mt int, data []byte) error {
	if mt != websocket.TextMessage {
		return errors.New("binary frames are not supported")
	}
	typ, err := protocol.PeekType(data)
	if err != nil {
		return ErrBadMessage
	}
	var m protocol.WSChat // join, leave and chat share {type, room, text}
	if json.Unmarshal(data, &m) != nil {
		return ErrBadMessage
	}
	switch typ {
	case protocol.TypeJoin:
		return c.hub.join(c, m.Room)
	case protocol.TypeLeave:
		return c.hub.leave(c, m.Room)
	case protocol.TypeChat:
		if !c.bucket.allow(time.Now()) {
			return ErrRateLimit // the message is dropped, the connection stays
		}
		return c.hub.chat(c, m.Room, m.Text)
	}
	return errors.New("unknown message type " + typ)
}

// writePump is the only goroutine that writes to conn.
func (c *Client) writePump() {
	lim := c.hub.lim
	t := time.NewTicker(lim.PingPeriod)
	defer func() {
		t.Stop()
		_ = c.conn.Close() // also ends readPump
	}()
	for {
		select {
		case b := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(lim.WriteWait))
			if c.conn.WriteMessage(websocket.TextMessage, b) != nil {
				c.close("write error")
				return
			}
		case <-t.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(lim.WriteWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				c.close("ping error")
				return
			}
		case <-c.done:
			_ = c.conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			return
		}
	}
}

// The default CheckOrigin accepts handshakes without an Origin header (our Go
// client) and rejects cross-origin browser pages.
var upgrader = websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}

// Serve is GET /ws. It must run behind auth.Middleware, so a missing or bad JWT
// gets a 401 JSON answer before any upgrade happens.
func (h *Hub) Serve(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade already answered with an HTTP error
	}
	cl := &Client{hub: h, conn: conn, userID: auth.UserID(c), username: auth.Username(c),
		send: make(chan []byte, h.lim.SendBuffer), done: make(chan struct{}), rooms: map[string]struct{}{},
		bucket: bucket{tokens: h.lim.MsgBurst, rate: h.lim.MsgRate, burst: h.lim.MsgBurst, last: time.Now()}}
	if err := h.register(cl); err != nil {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, err.Error()), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	h.log.Info("ws open", "user", cl.username, "remote", c.Request.RemoteAddr)
	go func() { defer h.wg.Done(); cl.writePump() }()
	go func() { defer h.wg.Done(); cl.readPump() }()
}
