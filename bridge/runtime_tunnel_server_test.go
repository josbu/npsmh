package bridge

import (
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
)

func waitForPipeCloseError(t *testing.T, peer net.Conn) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		var buf [1]byte
		_, err := peer.Read(buf[:])
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("peer.Read() error = nil, want closed connection")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for peer side close")
	}
}

func TestTunnelCloseReasonIgnoresTypedNilRuntimeTunnel(t *testing.T) {
	var typedNilMux *mux.Mux
	if reason := tunnelCloseReason(typedNilMux); reason != "" {
		t.Fatalf("tunnelCloseReason(typed nil mux) = %q, want empty", reason)
	}

	var typedNilQUIC *quic.Conn
	if reason := tunnelCloseReason(typedNilQUIC); reason != "" {
		t.Fatalf("tunnelCloseReason(typed nil quic) = %q, want empty", reason)
	}
}

func TestServeTunnelRuntimeRejectsTypedNilTunnel(t *testing.T) {
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })

	node := NewNode("typed-nil-runtime", "test", 6)
	client := NewClient(1, node)
	runtimeBridge := NewTunnel(false, nil, 0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		var typedNilMux *mux.Mux
		runtimeBridge.serveTunnelRuntime(typedNilMux, conn.NewConn(serverConn), 1, 6, "test", "tcp", bridgeAuthKindControl, "typed-nil-runtime", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}, client, node)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveTunnelRuntime() did not return for typed nil tunnel")
	}
	waitForPipeCloseError(t, peerConn)
}

func TestServeVisitorRuntimeRejectsTypedNilTunnel(t *testing.T) {
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })

	runtimeBridge := NewTunnel(false, nil, 0)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var typedNilQUIC *quic.Conn
		runtimeBridge.serveVisitorRuntime(typedNilQUIC, conn.NewConn(serverConn), 1, 6, "test", "quic", bridgeAuthKindVisitor, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveVisitorRuntime() did not return for typed nil tunnel")
	}
	waitForPipeCloseError(t, peerConn)
}
