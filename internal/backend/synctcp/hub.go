package synctcp

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"mangahub/pkg/protocol"
)

// Limits bound what one backend accepts. Tests shrink them.
type Limits struct {
	MaxTotal, MaxPerUser, SendBuffer int
	AuthTimeout, IdleTimeout         time.Duration
	WriteTimeout                     time.Duration
	MaxLine                          int
}

// DefaultLimits: the client pings every 15 s, so 45 s of silence means a dead peer.
func DefaultLimits() Limits {
	return Limits{MaxTotal: 100, MaxPerUser: 10, SendBuffer: 16,
		AuthTimeout: 5 * time.Second, IdleTimeout: 45 * time.Second, WriteTimeout: 5 * time.Second, MaxLine: 64 << 10}
}

// ErrTooMany is returned when MaxTotal or MaxPerUser sessions are already open.
var ErrTooMany = errors.New("too many connections")

type session struct {
	id, userID string
	conn       net.Conn
	send       chan []byte   // never closed: publishers may still be sending
	done       chan struct{} // closed once by close(); stops the writer
	once       sync.Once
	log        *slog.Logger
}

func newSession(c net.Conn, userID string, buf int, log *slog.Logger) *session {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return &session{id: "s_" + hex.EncodeToString(b), userID: userID, conn: c,
		send: make(chan []byte, buf), done: make(chan struct{}), log: log}
}

// close is idempotent and safe from any goroutine.
func (s *session) close(reason string) {
	s.once.Do(func() {
		close(s.done)
		_ = s.conn.Close()
		s.log.Info("tcp session close", "id", s.id, "user", s.userID, "reason", reason)
	})
}

// enqueue never blocks; false means the buffer is full.
func (s *session) enqueue(line []byte) bool {
	select {
	case s.send <- line:
		return true
	default:
		return false
	}
}

// writeLoop is the only goroutine that writes to the connection.
func (s *session) writeLoop(timeout time.Duration) {
	for {
		select {
		case line := <-s.send:
			_ = s.conn.SetWriteDeadline(time.Now().Add(timeout))
			if _, err := s.conn.Write(line); err != nil {
				s.close("write failed")
				return
			}
		case <-s.done:
			return
		}
	}
}

// Hub routes progress events to the sessions of one user. It implements api.ProgressPublisher.
type Hub struct {
	mu     sync.RWMutex
	byUser map[string]map[string]*session
	total  int
	lim    Limits
	log    *slog.Logger
}

// NewHub creates an empty hub. The Server registers sessions after auth.
func NewHub(lim Limits, log *slog.Logger) *Hub {
	return &Hub{byUser: map[string]map[string]*session{}, lim: lim, log: log}
}

func (h *Hub) add(s *session) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total >= h.lim.MaxTotal || len(h.byUser[s.userID]) >= h.lim.MaxPerUser {
		return ErrTooMany
	}
	if h.byUser[s.userID] == nil {
		h.byUser[s.userID] = map[string]*session{}
	}
	h.byUser[s.userID][s.id] = s
	h.total++
	return nil
}

// remove is idempotent and deletes empty user buckets.
func (h *Hub) remove(s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.byUser[s.userID][s.id]; !ok {
		return
	}
	delete(h.byUser[s.userID], s.id)
	if len(h.byUser[s.userID]) == 0 {
		delete(h.byUser, s.userID)
	}
	h.total--
}

// Publish sends ev to userID's sessions except exclude (the origin, from X-Session-ID).
// It snapshots under RLock, never blocks, and closes slow sessions only after unlocking.
func (h *Hub) Publish(userID string, ev protocol.ProgressUpdated, exclude string) {
	line, err := protocol.EncodeLine(ev)
	if err != nil {
		h.log.Error("encode progress event", "err", err)
		return
	}
	var slow []*session
	sent := 0
	h.mu.RLock()
	for id, s := range h.byUser[userID] {
		if id == exclude {
			continue
		}
		if s.enqueue(line) {
			sent++
		} else {
			slow = append(slow, s)
		}
	}
	h.mu.RUnlock()
	for _, s := range slow {
		h.log.Warn("tcp slow consumer", "id", s.id, "user", s.userID)
		s.close("slow consumer")
	}
	h.log.Debug("tcp publish", "user", userID, "manga", ev.MangaID, "version", ev.Version, "sent", sent)
}

// Count is the number of registered sessions.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}
