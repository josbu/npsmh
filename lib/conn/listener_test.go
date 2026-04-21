package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

func withVirtualListenerEnqueueTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := virtualListenerEnqueueTimeout
	virtualListenerEnqueueTimeout = timeout
	t.Cleanup(func() {
		virtualListenerEnqueueTimeout = old
	})
}

func TestVirtualListenerDialVirtualReturnsErrorWhenQueueIsFull(t *testing.T) {
	withVirtualListenerEnqueueTimeout(t, 25*time.Millisecond)
	listener := newVirtualListenerWithBuffer(LocalTCPAddr, 1)
	t.Cleanup(func() { _ = listener.Close() })

	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	listener.ServeVirtual(right)

	conn, err := listener.DialVirtual("127.0.0.1:9100")
	if !errors.Is(err, ErrVirtualListenerFull) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("DialVirtual() error = %v, want %v", err, ErrVirtualListenerFull)
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatal("DialVirtual() should not return a connection when the queue is full")
	}
}

func TestVirtualListenerDialVirtualWaitsForCapacity(t *testing.T) {
	withVirtualListenerEnqueueTimeout(t, 200*time.Millisecond)
	listener := newVirtualListenerWithBuffer(LocalTCPAddr, 1)
	t.Cleanup(func() { _ = listener.Close() })

	firstClient, firstServer := net.Pipe()
	t.Cleanup(func() { _ = firstClient.Close() })
	listener.ServeVirtual(firstServer)

	type dialResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan dialResult, 1)
	go func() {
		conn, err := listener.DialVirtual("127.0.0.1:9102")
		resultCh <- dialResult{conn: conn, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.conn != nil {
			_ = result.conn.Close()
		}
		t.Fatalf("DialVirtual() returned early while listener queue was still full: err=%v", result.err)
	case <-time.After(40 * time.Millisecond):
	}

	firstAccepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}
	_ = firstAccepted.Close()

	var dialed net.Conn
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("DialVirtual() error = %v", result.err)
		}
		dialed = result.conn
	case <-time.After(2 * time.Second):
		t.Fatal("DialVirtual() did not succeed after queue capacity became available")
	}
	if dialed == nil {
		t.Fatal("DialVirtual() returned nil conn after queue capacity became available")
	}
	defer func() { _ = dialed.Close() }()

	secondAccepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("second Accept() error = %v", err)
	}
	_ = secondAccepted.Close()
}

func TestVirtualListenerDialVirtualReturnsClosedErrorForClosedListener(t *testing.T) {
	listener := newVirtualListenerWithBuffer(LocalTCPAddr, 1)
	_ = listener.Close()

	conn, err := listener.DialVirtual("127.0.0.1:9101")
	if !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("DialVirtual() error = %v, want %v", err, net.ErrClosed)
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatal("DialVirtual() should not return a connection after close")
	}
}

func TestVirtualListenerAcceptReturnsQueuedConnection(t *testing.T) {
	listener := newVirtualListenerWithBuffer(LocalTCPAddr, 1)
	t.Cleanup(func() { _ = listener.Close() })

	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close() })
	listener.ServeVirtual(right)

	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if conn == nil {
		t.Fatal("Accept() returned nil connection")
	}
	_ = conn.Close()
}

func TestVirtualListenerDialVirtualReturnsErrorForInvalidRemoteAddr(t *testing.T) {
	listener := newVirtualListenerWithBuffer(LocalTCPAddr, 1)
	t.Cleanup(func() { _ = listener.Close() })

	conn, err := listener.DialVirtual("not-a-tcp-addr")
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("DialVirtual() error = nil, want invalid remote addr error")
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatal("DialVirtual() should not return a connection for invalid remote addr")
	}
}

func TestOneConnListenerAcceptThenClose(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })

	listener := NewOneConnListener(left)

	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("first Accept() error = %v", err)
	}
	if conn != left {
		t.Fatal("first Accept() should return the original connection")
	}
	if listener.Addr() == nil {
		t.Fatal("Addr() returned nil for one-conn listener")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := listener.Accept()
		errCh <- err
	}()

	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := <-errCh; !errors.Is(err, io.EOF) {
		t.Fatalf("second Accept() error = %v, want %v", err, io.EOF)
	}
}

func TestOneConnListenerCloseBeforeAcceptReturnsClosedError(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })

	listener := NewOneConnListener(left)
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	conn, err := listener.Accept()
	if !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("Accept() error = %v, want %v", err, net.ErrClosed)
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatal("Accept() should not return a conn after Close()")
	}
}

func TestOneConnListenerHandlesNilConn(t *testing.T) {
	listener := NewOneConnListener(nil)

	if got := listener.Addr(); got != nil {
		t.Fatalf("Addr() = %#v, want nil", got)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	conn, err := listener.Accept()
	if !errors.Is(err, net.ErrClosed) {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("Accept() error = %v, want %v", err, net.ErrClosed)
	}
	if conn != nil {
		_ = conn.Close()
		t.Fatal("Accept() should not return a conn for nil one-conn listener")
	}
}

func TestServeQUICListenerAcceptsNewSessionWhileEarlierSessionWaitsForStream(t *testing.T) {
	crypt.InitTls(tls.Certificate{})

	listener, err := quic.ListenAddr("127.0.0.1:0", crypt.GetCertCfg(), &quic.Config{})
	if err != nil {
		t.Fatalf("quic.ListenAddr() error = %v", err)
	}

	acceptedConnCh := make(chan net.Conn, 1)
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- serveQUICListener(listener, func(c net.Conn) {
			acceptedConnCh <- c
		})
	}()

	clientTLS := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
	}

	firstSess, err := quic.DialAddr(context.Background(), listener.Addr().String(), clientTLS, &quic.Config{})
	if err != nil {
		t.Fatalf("first quic.DialAddr() error = %v", err)
	}
	defer func() {
		_ = firstSess.CloseWithError(0, "test cleanup")
	}()

	secondSess, err := quic.DialAddr(context.Background(), listener.Addr().String(), clientTLS, &quic.Config{})
	if err != nil {
		t.Fatalf("second quic.DialAddr() error = %v", err)
	}
	defer func() {
		_ = secondSess.CloseWithError(0, "test cleanup")
	}()

	secondStream, err := secondSess.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync() error = %v", err)
	}
	defer func() {
		_ = secondStream.Close()
	}()
	if _, err := secondStream.Write([]byte("init")); err != nil {
		t.Fatalf("secondStream.Write() error = %v", err)
	}

	select {
	case acceptedConn := <-acceptedConnCh:
		_ = acceptedConn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("second session stream should be processed even if the first session never opens a stream")
	}

	_ = listener.Close()

	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("serveQUICListener() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveQUICListener() should exit after listener close")
	}

	select {
	case <-firstSess.Context().Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("listener close should also close earlier accepted QUIC sessions that never opened a stream")
	}
}

type closeErrorListener struct {
	acceptCalls int
}

func (l *closeErrorListener) Accept() (net.Conn, error) {
	l.acceptCalls++
	return nil, fmt.Errorf("wrapped close: %w", net.ErrClosed)
}

func (l *closeErrorListener) Close() error { return nil }

func (l *closeErrorListener) Addr() net.Addr { return LocalTCPAddr }

func TestAcceptStopsOnWrappedNetErrClosed(t *testing.T) {
	listener := &closeErrorListener{}

	Accept(listener, func(net.Conn) {
		t.Fatal("Accept() callback should not run on closed listener")
	})

	if listener.acceptCalls != 1 {
		t.Fatalf("Accept() calls = %d, want 1", listener.acceptCalls)
	}
}

type eofErrorListener struct {
	acceptCalls int
}

func (l *eofErrorListener) Accept() (net.Conn, error) {
	l.acceptCalls++
	return nil, io.EOF
}

func (l *eofErrorListener) Close() error { return nil }

func (l *eofErrorListener) Addr() net.Addr { return LocalTCPAddr }

func TestAcceptStopsOnEOF(t *testing.T) {
	listener := &eofErrorListener{}

	Accept(listener, func(net.Conn) {
		t.Fatal("Accept() callback should not run on EOF listener")
	})

	if listener.acceptCalls != 1 {
		t.Fatalf("Accept() calls = %d, want 1", listener.acceptCalls)
	}
}

type closeKCPListener struct {
	acceptCalls int
}

func (l *closeKCPListener) AcceptKCP() (*kcp.UDPSession, error) {
	l.acceptCalls++
	return nil, fmt.Errorf("wrapped close: %w", net.ErrClosed)
}

func TestServeKCPListenerStopsOnWrappedNetErrClosed(t *testing.T) {
	listener := &closeKCPListener{}

	if err := serveKCPListener(listener, func(net.Conn) {
		t.Fatal("serveKCPListener() callback should not run on closed listener")
	}); err != nil {
		t.Fatalf("serveKCPListener() error = %v, want nil", err)
	}

	if listener.acceptCalls != 1 {
		t.Fatalf("AcceptKCP() calls = %d, want 1", listener.acceptCalls)
	}
}

type retryThenCloseKCPListener struct {
	acceptCalls int
}

func (l *retryThenCloseKCPListener) AcceptKCP() (*kcp.UDPSession, error) {
	l.acceptCalls++
	if l.acceptCalls == 1 {
		return nil, errors.New("temporary accept failure")
	}
	return nil, fmt.Errorf("closed accept: %w", net.ErrClosed)
}

func TestServeKCPListenerRetriesTransientErrorThenStopsOnClose(t *testing.T) {
	listener := &retryThenCloseKCPListener{}

	if err := serveKCPListener(listener, func(net.Conn) {
		t.Fatal("serveKCPListener() callback should not run without accepted sessions")
	}); err != nil {
		t.Fatalf("serveKCPListener() error = %v, want nil", err)
	}

	if listener.acceptCalls != 2 {
		t.Fatalf("AcceptKCP() calls = %d, want 2", listener.acceptCalls)
	}
}
