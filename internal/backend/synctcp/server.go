package synctcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"mangahub/internal/auth"
	"mangahub/pkg/protocol"
)

type Server struct {
	hub  *Hub
	auth auth.Verifier
	lim  Limits
	log  *slog.Logger
	wg   sync.WaitGroup // connection handlers and session writers
}

func NewServer(v auth.Verifier, lim Limits, log *slog.Logger) *Server {
	return &Server{hub: NewHub(lim, log), auth: v, lim: lim, log: log}
}

func (s *Server) Hub() *Hub { return s.hub }

// Serve accepts until ctx is cancelled, then closes every connection and waits for
// all handlers and writers to finish. It returns nil on a ctx-driven stop.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	defer s.wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond) // temporary accept error: back off, keep serving
				continue
			}
			return err
		}
		s.wg.Go(func() { s.handle(ctx, c) })
	}
}

func writeErr(c net.Conn, msg string) {
	line, _ := protocol.EncodeLine(protocol.TCPError{Type: protocol.TypeError, Error: msg})
	_ = c.SetWriteDeadline(time.Now().Add(time.Second))
	_, _ = c.Write(line)
}

// readLine returns one "\n"-terminated line. TCP is a byte stream: one Read may hold
// half a message or two, so we frame on "\n". A line longer than MaxLine fails with
// bufio.ErrBufferFull instead of growing without bound (bufio.Scanner has the same
// 64 KiB trap, but fails with a less obvious ErrTooLong).
var errLineTooLong = errors.New("line too long")

func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, errLineTooLong
	}
	return line, err
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() }) // shutdown also frees unauthenticated connections
	defer stop()

	r := bufio.NewReaderSize(c, s.lim.MaxLine)
	_ = c.SetReadDeadline(time.Now().Add(s.lim.AuthTimeout))
	line, err := readLine(r)
	if errors.Is(err, errLineTooLong) {
		writeErr(c, err.Error())
		return
	}
	if err != nil {
		writeErr(c, "expected auth message") // includes the auth timeout
		return
	}
	var a protocol.TCPAuth
	if t, _ := protocol.PeekType(line); t != protocol.TypeAuth || json.Unmarshal(line, &a) != nil {
		writeErr(c, "expected auth message")
		return
	}
	claims, err := s.auth.Verify(a.Token)
	if err != nil {
		writeErr(c, "invalid token")
		return
	}

	sess := newSession(c, claims.Subject, s.lim.SendBuffer, s.log)
	ok, _ := protocol.EncodeLine(protocol.TCPAuthOK{Type: protocol.TypeAuthOK, SessionID: sess.id})
	sess.enqueue(ok) // queued BEFORE registering, so auth_ok is always the first line the client gets
	if err := s.hub.add(sess); err != nil {
		writeErr(c, err.Error())
		return
	}
	s.log.Info("tcp session open", "id", sess.id, "user", sess.userID, "remote", c.RemoteAddr().String())
	defer s.hub.remove(sess)
	defer sess.close("disconnected")
	s.wg.Go(func() { sess.writeLoop(s.lim.WriteTimeout) })

	pong, _ := protocol.EncodeLine(protocol.TCPPong{Type: protocol.TypePong})
	for {
		_ = c.SetReadDeadline(time.Now().Add(s.lim.IdleTimeout)) // refreshed per line: silence = dead peer
		line, err := readLine(r)
		if err != nil { // EOF, idle timeout, or an over-long line: just close (only the writer may write now)
			return
		}
		if t, _ := protocol.PeekType(line); t == protocol.TypePing && !sess.enqueue(pong) {
			sess.close("slow consumer")
			return
		}
	}
}
