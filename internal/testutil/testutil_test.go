package testutil_test

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"mangahub/internal/testutil"
)

func TestEventually(t *testing.T) {
	var n atomic.Int32
	go func() { n.Store(1) }()
	testutil.Eventually(t, func() bool { return n.Load() == 1 }, time.Second, "flag set")
}

func TestReadLineAndExpectNoData(t *testing.T) {
	ln := testutil.ListenLoopback(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("hello\n"))
		<-done2(c)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	assert.NoError(t, err)
	defer conn.Close()
	assert.Equal(t, "hello", string(testutil.ReadLine(t, conn, time.Second)))
	testutil.ExpectNoData(t, conn, 50*time.Millisecond)
}

// done2 returns a channel that closes when the peer closes the connection.
func done2(c net.Conn) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = c.Read(buf)
		close(ch)
	}()
	return ch
}
