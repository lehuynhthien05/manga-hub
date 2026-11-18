package chatws

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"mangahub/pkg/protocol"
)

// Limits bound one backend's chat. Tests shrink the timings.
type Limits struct {
	MaxClients, MaxRooms, HistorySize, MaxText, SendBuffer int
	ReadLimit                                              int64
	MsgRate, MsgBurst                                      float64 // per client, messages per second
	PongWait, PingPeriod, WriteWait                        time.Duration
}

func DefaultLimits() Limits {
	return Limits{MaxClients: 200, MaxRooms: 100, HistorySize: 50, MaxText: 500, SendBuffer: 32, ReadLimit: 4096,
		MsgRate: 5, MsgBurst: 10, PongWait: 60 * time.Second, PingPeriod: 54 * time.Second, WriteWait: 10 * time.Second}
}

var (
	ErrFull       = errors.New("server full")
	ErrNotMember  = errors.New("not a member of this room")
	ErrBadRoom    = errors.New("invalid room name: use 1-32 of a-z 0-9 _ -")
	ErrTooMany    = errors.New("too many rooms")
	ErrBadText    = errors.New("text must be 1-500 characters")
	ErrRateLimit  = errors.New("slow down")
	ErrBadMessage = errors.New("invalid message")
)

// Hub owns all chat state. Locking: h.mu guards clients, rooms, history and
// every Client.rooms set. Methods enqueue frames while holding the lock (never
// blocking), and close slow clients only after unlocking. Sockets are written
// only by each client's own writePump.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	rooms   map[string]map[*Client]struct{}
	history map[string][]protocol.WSHistoryItem // last HistorySize per room
	lim     Limits
	log     *slog.Logger
	wg      sync.WaitGroup // client pumps; Add happens in register, under mu
	closed  bool
}

func NewHub(lim Limits, log *slog.Logger) *Hub {
	return &Hub{clients: map[*Client]struct{}{}, rooms: map[string]map[*Client]struct{}{},
		history: map[string][]protocol.WSHistoryItem{}, lim: lim, log: log}
}

func encode(v any) []byte { b, _ := json.Marshal(v); return b }

// enqueue sends b to every client in to without blocking; full buffers are returned.
func enqueue(to map[*Client]struct{}, skip *Client, b []byte) (slow []*Client) {
	for c := range to {
		if c != skip && !c.enqueue(b) {
			slow = append(slow, c)
		}
	}
	return slow
}

func closeSlow(slow []*Client) {
	for _, c := range slow {
		c.close("slow consumer")
	}
}

func (h *Hub) register(c *Client) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || len(h.clients) >= h.lim.MaxClients {
		return ErrFull
	}
	h.clients[c] = struct{}{}
	h.wg.Add(2) // readPump + writePump; counted before Close can start waiting
	return nil
}

// unregister leaves every room, announces it, and deletes empty rooms. Idempotent.
func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	var slow []*Client
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		for room := range c.rooms {
			slow = append(slow, h.leaveLocked(c, room)...)
		}
	}
	h.mu.Unlock()
	closeSlow(slow)
}

func (h *Hub) leaveLocked(c *Client, room string) []*Client {
	delete(c.rooms, room)
	members := h.rooms[room]
	delete(members, c)
	if len(members) == 0 {
		delete(h.rooms, room)
		delete(h.history, room)
		return nil
	}
	return enqueue(members, nil, encode(protocol.WSSystem{Type: protocol.TypeSystem, Room: room, Text: c.username + " left"}))
}

// join sends the joiner its history before any live message, then tells the others.
func (h *Hub) join(c *Client, room string) error {
	if !protocol.ValidRoom(room) {
		return ErrBadRoom
	}
	h.mu.Lock()
	if _, ok := c.rooms[room]; ok {
		h.mu.Unlock()
		return nil
	}
	if h.rooms[room] == nil {
		if len(h.rooms) >= h.lim.MaxRooms {
			h.mu.Unlock()
			return ErrTooMany
		}
		h.rooms[room] = map[*Client]struct{}{}
	}
	hist := append([]protocol.WSHistoryItem{}, h.history[room]...)
	c.enqueue(encode(protocol.WSHistory{Type: protocol.TypeHistory, Room: room, Messages: hist}))
	slow := enqueue(h.rooms[room], nil, encode(protocol.WSSystem{Type: protocol.TypeSystem, Room: room, Text: c.username + " joined"}))
	h.rooms[room][c] = struct{}{}
	c.rooms[room] = struct{}{}
	h.mu.Unlock()
	closeSlow(slow)
	return nil
}

func (h *Hub) leave(c *Client, room string) error {
	h.mu.Lock()
	if _, ok := c.rooms[room]; !ok {
		h.mu.Unlock()
		return ErrNotMember
	}
	slow := h.leaveLocked(c, room)
	h.mu.Unlock()
	closeSlow(slow)
	return nil
}

// cleanText trims and drops control characters (including ESC of ANSI sequences).
func cleanText(s string, max int) (string, error) {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
	if n := utf8.RuneCountInString(s); n == 0 || n > max {
		return "", ErrBadText
	}
	return s, nil
}

// chat broadcasts to every member, the sender included (one render path, server order).
func (h *Hub) chat(c *Client, room, text string) error {
	text, err := cleanText(text, h.lim.MaxText)
	if err != nil {
		return err
	}
	msg := protocol.WSChat{Type: protocol.TypeChat, Room: room, User: c.username, Text: text, TS: time.Now().Unix()}
	h.mu.Lock()
	if _, ok := c.rooms[room]; !ok { // membership is enforced here, on the server
		h.mu.Unlock()
		return fmt.Errorf("%w %s", ErrNotMember, room)
	}
	hist := append(h.history[room], protocol.WSHistoryItem{User: msg.User, Text: msg.Text, TS: msg.TS})
	if len(hist) > h.lim.HistorySize {
		hist = hist[len(hist)-h.lim.HistorySize:]
	}
	h.history[room] = hist
	slow := enqueue(h.rooms[room], nil, encode(msg))
	h.mu.Unlock()
	closeSlow(slow)
	return nil
}

// Close disconnects every client and waits for their goroutines (backend shutdown).
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true // no new clients: every wg.Add happened before this
	h.mu.Unlock()
	h.mu.RLock()
	all := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		all = append(all, c)
	}
	h.mu.RUnlock()
	for _, c := range all {
		c.close("shutdown")
	}
	h.wg.Wait()
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}
