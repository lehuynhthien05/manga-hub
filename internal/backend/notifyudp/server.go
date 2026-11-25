package notifyudp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"mangahub/internal/auth"
	"mangahub/internal/clock"
	"mangahub/pkg/protocol"
)

const (
	bufSize     = 2048            // a datagram filling the buffer is treated as oversize and dropped
	TTL         = 2 * time.Minute // clients ping every 30 s, so 2 min of silence = gone
	janitorTick = 30 * time.Second
	maxFailures = 3
)

type sub struct {
	addr     net.Addr
	userID   string
	lastSeen time.Time
	failures int
}

// Server keeps soft-state subscriptions: UDP has no connection, so the server
// cannot know a receiver left. The address is learned only from a valid subscribe;
// entries expire after TTL without a ping, and on unsubscribe.
type Server struct {
	auth  auth.Verifier
	clock clock.Clock
	log   *slog.Logger

	mu   sync.Mutex
	subs map[string]*sub // key: addr.String()
	pc   net.PacketConn
}

// NewServer creates a server with no subscriptions. c is the clock used for the TTL.
func NewServer(v auth.Verifier, c clock.Clock, log *slog.Logger) *Server {
	return &Server{auth: v, clock: c, log: log, subs: map[string]*sub{}}
}

// Serve reads datagrams until ctx ends. One reader; Notify may write concurrently.
func (s *Server) Serve(ctx context.Context, pc net.PacketConn) error {
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { _ = pc.Close() })
	defer stop()
	go s.janitor(ctx)

	buf := make([]byte, bufSize)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.log.Warn("udp read", "err", err)
			continue
		}
		if n == len(buf) {
			s.log.Debug("udp oversize datagram dropped", "from", addr.String())
			continue
		}
		s.handle(addr, buf[:n]) // handled synchronously, so reusing buf is safe
	}
}

func (s *Server) janitor(ctx context.Context) {
	t := time.NewTicker(janitorTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if n := s.evictExpired(); n > 0 {
				s.log.Info("udp subscriptions expired", "count", n)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) evictExpired() int {
	cutoff := s.clock.Now().Add(-TTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, sb := range s.subs {
		if sb.lastSeen.Before(cutoff) {
			delete(s.subs, k)
			n++
		}
	}
	return n
}

func (s *Server) reply(addr net.Addr, v any) {
	b, _ := json.Marshal(v)
	_, _ = s.pc.WriteTo(b, addr)
}

// handle ignores garbage silently: no reply, no panic.
func (s *Server) handle(addr net.Addr, data []byte) {
	typ, err := protocol.PeekType(data)
	if err != nil {
		s.log.Debug("udp garbage ignored", "from", addr.String())
		return
	}
	key := addr.String()
	switch typ {
	case protocol.TypeSubscribe, protocol.TypeUnsubscribe:
		var m protocol.UDPSubscribe // same shape as UDPUnsubscribe
		_ = json.Unmarshal(data, &m)
		claims, err := s.auth.Verify(m.Token)
		if err != nil { // the reply is tiny, so a spoofed sender gains no amplification
			s.reply(addr, protocol.UDPError{Type: protocol.TypeError, Error: "invalid token"})
			return
		}
		s.mu.Lock()
		if typ == protocol.TypeSubscribe {
			s.subs[key] = &sub{addr: addr, userID: claims.Subject, lastSeen: s.clock.Now()}
		} else {
			delete(s.subs, key) // only the sender's own address can be removed
		}
		s.mu.Unlock()
		if typ == protocol.TypeSubscribe {
			s.log.Info("udp subscribe", "from", key, "user", claims.Subject)
			s.reply(addr, protocol.UDPSubscribed{Type: protocol.TypeSubscribed})
		} else {
			s.log.Info("udp unsubscribe", "from", key, "user", claims.Subject)
			s.reply(addr, protocol.UDPUnsubscribed{Type: protocol.TypeUnsubscribed})
		}
	case protocol.TypePing:
		s.mu.Lock()
		sb, ok := s.subs[key]
		if ok {
			sb.lastSeen = s.clock.Now()
		}
		s.mu.Unlock()
		if ok { // unknown senders get silence
			s.reply(addr, protocol.UDPPong{Type: protocol.TypePong})
		}
	default:
		s.log.Debug("udp unknown type ignored", "type", typ, "from", key)
	}
}

// Notify sends one datagram per subscriber and returns how many sends were
// attempted successfully by the OS — NOT how many arrived. On this unconnected
// socket a closed receiver port usually produces no error at all (the ICMP
// "port unreachable" is not reported), so dead entries go away through the TTL.
func (s *Server) Notify(ev protocol.NewChapter) (sent int) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return 0
	}
	s.mu.Lock()
	pc := s.pc
	targets := make([]net.Addr, 0, len(s.subs))
	for _, sb := range s.subs {
		targets = append(targets, sb.addr)
	}
	s.mu.Unlock()
	if pc == nil {
		return 0
	}
	for _, addr := range targets { // network I/O outside the lock
		_, err := pc.WriteTo(payload, addr)
		s.mu.Lock()
		if sb, ok := s.subs[addr.String()]; ok {
			if err != nil {
				if sb.failures++; sb.failures >= maxFailures {
					delete(s.subs, addr.String())
				}
			} else {
				sb.failures = 0
			}
		}
		s.mu.Unlock()
		if err != nil {
			s.log.Warn("udp send", "to", addr.String(), "err", err)
			continue
		}
		sent++
	}
	s.log.Info("udp notify", "manga", ev.MangaID, "chapter", ev.Chapter, "sent", sent)
	return sent
}

// Count is the number of current subscriptions.
func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}
