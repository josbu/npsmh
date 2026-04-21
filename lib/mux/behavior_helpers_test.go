package mux

import (
	"io"
	"net"
	"testing"
	"time"
)

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

func newMuxPair(t *testing.T) (*Mux, *Mux) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = client.Close() })

	left := NewMux(client, "tcp", 30, true)
	right := NewMux(server, "tcp", 30, false)
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	return left, right
}

func newConnPair(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	leftMux, rightMux := newMuxPair(t)

	acceptCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := rightMux.Accept()
		if err != nil {
			errCh <- err
			return
		}
		acceptCh <- conn
	}()

	leftConn, err := leftMux.NewConn()
	if err != nil {
		t.Fatalf("NewConn() error = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Accept() error = %v", err)
	case rightConn := <-acceptCh:
		return leftConn, rightConn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote Accept")
		return nil, nil
	}
	return nil, nil
}

func newQueueOnlyMuxForTest() *Mux {
	cfg := normalizeMuxConfig(DefaultMuxConfig())
	m := &Mux{
		config:           cfg,
		connMap:          NewConnMap(),
		closeChan:        make(chan struct{}),
		sessionRecvLimit: cfg.MaxSessionReceiveWindow,
	}
	m.writeQueue.New()
	return m
}

func newQueueOnlyConnForTest(m *Mux, id int32) *Conn {
	conn := NewConn(id, m)
	m.connMap.Set(id, conn)
	return conn
}

func popQueuedPackOrFatal(t *testing.T, m *Mux) *muxPackager {
	t.Helper()
	pack := m.writeQueue.TryPop()
	if pack == nil {
		t.Fatal("writeQueue.TryPop() returned nil")
	}
	return pack
}

func recycleMuxPackForTest(pack *muxPackager) {
	if pack == nil {
		return
	}
	if pack.content != nil {
		windowBuff.Put(pack.content)
		pack.content = nil
	}
	muxPack.Put(pack)
}

func drainWriteQueueForTest(m *Mux) {
	for {
		pack := m.writeQueue.TryPop()
		if pack == nil {
			return
		}
		recycleMuxPackForTest(pack)
	}
}

type flushWriterTestConn struct {
	writeErr          error
	lastWriteDeadline time.Time
	writeDeadlineHits int
}

func (c *flushWriterTestConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *flushWriterTestConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}
func (c *flushWriterTestConn) Close() error                    { return nil }
func (c *flushWriterTestConn) LocalAddr() net.Addr             { return dummyAddr("local") }
func (c *flushWriterTestConn) RemoteAddr() net.Addr            { return dummyAddr("remote") }
func (c *flushWriterTestConn) SetDeadline(time.Time) error     { return nil }
func (c *flushWriterTestConn) SetReadDeadline(time.Time) error { return nil }
func (c *flushWriterTestConn) SetWriteDeadline(t time.Time) error {
	c.lastWriteDeadline = t
	c.writeDeadlineHits++
	return nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }
