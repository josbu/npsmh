package conn

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type scriptedPacketRead struct {
	n    int
	addr net.Addr
	err  error
}

type scriptedPacketConn struct {
	mu      sync.Mutex
	reads   []scriptedPacketRead
	readPos int
}

type scriptedPacketConnWithLocalAddr struct {
	localAddr  net.Addr
	writeCalls int
}

func (c *scriptedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readPos >= len(c.reads) {
		return 0, nil, net.ErrClosed
	}
	read := c.reads[c.readPos]
	c.readPos++
	if read.n > 0 && len(p) > 0 {
		p[0] = 'x'
	}
	return read.n, read.addr, read.err
}

func (c *scriptedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) { return len(p), nil }
func (c *scriptedPacketConn) Close() error                                 { return nil }
func (c *scriptedPacketConn) LocalAddr() net.Addr                          { return &net.UDPAddr{IP: net.IPv4zero, Port: 0} }
func (c *scriptedPacketConn) SetDeadline(time.Time) error                  { return nil }
func (c *scriptedPacketConn) SetReadDeadline(time.Time) error              { return nil }
func (c *scriptedPacketConn) SetWriteDeadline(time.Time) error             { return nil }

func (c *scriptedPacketConnWithLocalAddr) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, net.ErrClosed
}

func (c *scriptedPacketConnWithLocalAddr) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.writeCalls++
	return len(p), nil
}

func (c *scriptedPacketConnWithLocalAddr) Close() error                    { return nil }
func (c *scriptedPacketConnWithLocalAddr) LocalAddr() net.Addr             { return c.localAddr }
func (c *scriptedPacketConnWithLocalAddr) SetDeadline(time.Time) error     { return nil }
func (c *scriptedPacketConnWithLocalAddr) SetReadDeadline(time.Time) error { return nil }
func (c *scriptedPacketConnWithLocalAddr) SetWriteDeadline(time.Time) error {
	return nil
}

func withSmartUDPReadErrorEnqueueTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := smartUDPReadErrorEnqueueTimeout
	smartUDPReadErrorEnqueueTimeout = timeout
	t.Cleanup(func() {
		smartUDPReadErrorEnqueueTimeout = old
	})
}

func TestSmartUDPConnDeliversReadErrorAfterTransientQueuePressure(t *testing.T) {
	withSmartUDPReadErrorEnqueueTimeout(t, 200*time.Millisecond)

	readErr := errors.New("synthetic read failure")
	pc := &scriptedPacketConn{
		reads: []scriptedPacketRead{
			{n: 1, addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}},
			{err: readErr},
		},
	}
	s := &SmartUdpConn{
		conns:     []net.PacketConn{pc},
		fakeLocal: &net.UDPAddr{IP: net.IPv4zero, Port: 0},
		packetCh:  make(chan packet, 1),
		quit:      make(chan struct{}),
	}
	s.wg.Add(1)
	go s.readLoop(pc)
	t.Cleanup(func() { _ = s.Close() })

	time.Sleep(40 * time.Millisecond)

	buf := make([]byte, 8)
	n, addr, err := s.ReadFrom(buf)
	if err != nil {
		t.Fatalf("first ReadFrom() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("first ReadFrom() n = %d, want 1", n)
	}
	if addr == nil {
		t.Fatal("first ReadFrom() addr = nil")
	}

	errCh := make(chan error, 1)
	go func() {
		_, _, err := s.ReadFrom(buf)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, readErr) {
			t.Fatalf("second ReadFrom() error = %v, want %v", err, readErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second ReadFrom() did not surface queued socket read error")
	}
}

func TestSmartUDPConnWriteToRejectsEmptyConnSet(t *testing.T) {
	s := &SmartUdpConn{}
	_, err := s.WriteTo([]byte("ping"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53})
	if !errors.Is(err, errSmartUDPNoPacketConn) {
		t.Fatalf("WriteTo() error = %v, want %v", err, errSmartUDPNoPacketConn)
	}
}

func TestSmartUDPConnWriteToFallsBackWhenLocalAddrIsNotUDP(t *testing.T) {
	pc := &scriptedPacketConnWithLocalAddr{localAddr: &net.IPAddr{IP: net.IPv4zero}}
	s := &SmartUdpConn{
		conns: []net.PacketConn{pc},
	}

	n, err := s.WriteTo([]byte("ping"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53})
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("WriteTo() n = %d, want 4", n)
	}
	if pc.writeCalls != 1 {
		t.Fatalf("WriteTo() writeCalls = %d, want 1", pc.writeCalls)
	}
}

func TestSmartUDPConnCloseAndDeadlinesIgnoreNilPacketConn(t *testing.T) {
	s := &SmartUdpConn{
		conns:    []net.PacketConn{nil},
		packetCh: make(chan packet, 1),
		quit:     make(chan struct{}),
	}

	if err := s.SetDeadline(time.Now()); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if err := s.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := s.SetWriteDeadline(time.Now()); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestHandleUdp5HandlesNilContext(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	var nilCtx context.Context
	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleUdp5(nilCtx, serverSide, 50*time.Millisecond, "")
	}()

	time.Sleep(20 * time.Millisecond)
	_ = clientSide.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleUdp5(nil, ...) did not exit after tunnel close")
	}
}
