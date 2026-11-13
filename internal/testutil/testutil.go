// Package testutil holds helpers for tests: polling instead of sleeping,
// loopback listeners and bounded reads.
package testutil

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// Eventually polls cond every few ms until it is true or timeout passes.
func Eventually(t testing.TB, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v: %s", timeout, msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ListenLoopback listens on 127.0.0.1:0 and closes the listener on cleanup.
func ListenLoopback(t testing.TB) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// ReadLine reads one "\n"-terminated line (without the newline) or fails after timeout.
func ReadLine(t testing.TB, conn net.Conn, timeout time.Duration) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read line: %v", err)
	}
	return line[:len(line)-1]
}

// ExpectNoData fails if anything arrives on conn within d.
func ExpectNoData(t testing.TB, conn net.Conn, d time.Duration) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	if n > 0 {
		t.Fatalf("expected no data, got %q", buf[:n])
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected a read timeout, got %v", err)
	}
}
