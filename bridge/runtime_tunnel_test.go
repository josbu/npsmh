package bridge

import (
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
)

func TestNodeTunnelHelpersIgnoreTypedNilTunnel(t *testing.T) {
	t.Run("typed nil mux", func(t *testing.T) {
		node := NewNode("mux-node", "test", 6)
		var typedNilMux *mux.Mux
		node.tunnel = typedNilMux

		if got := node.GetTunnel(); got != nil {
			t.Fatalf("GetTunnel() = %#v, want nil for typed nil mux", got)
		}
		if !node.IsTunnelClosed() {
			t.Fatal("IsTunnelClosed() = false, want true for typed nil mux")
		}
		if err := node.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got := node.GetTunnel(); got != nil {
			t.Fatalf("GetTunnel() after Close() = %#v, want nil", got)
		}
	})

	t.Run("typed nil quic", func(t *testing.T) {
		node := NewNode("quic-node", "test", 6)
		var typedNilQUIC *quic.Conn
		node.tunnel = typedNilQUIC

		if got := node.GetTunnel(); got != nil {
			t.Fatalf("GetTunnel() = %#v, want nil for typed nil quic conn", got)
		}
		if !node.IsTunnelClosed() {
			t.Fatal("IsTunnelClosed() = false, want true for typed nil quic conn")
		}
		if err := node.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if got := node.GetTunnel(); got != nil {
			t.Fatalf("GetTunnel() after Close() = %#v, want nil", got)
		}
	})
}

func TestOpenRuntimeTunnelConnRejectsTypedNilTunnel(t *testing.T) {
	var typedNilMux *mux.Mux
	if conn, err := openRuntimeTunnelConn(typedNilMux, 100*time.Millisecond); err == nil || err.Error() != "the tunnel is unavailable" {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("openRuntimeTunnelConn(typed nil mux) error = %v, want tunnel unavailable", err)
	}

	var typedNilQUIC *quic.Conn
	if conn, err := openRuntimeTunnelConn(typedNilQUIC, 100*time.Millisecond); err == nil || err.Error() != "the tunnel is unavailable" {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("openRuntimeTunnelConn(typed nil quic) error = %v, want tunnel unavailable", err)
	}
}

func TestNodeAddTunnelNormalizesTypedNilTunnel(t *testing.T) {
	node := NewNode("normalize-node", "test", 6)

	var typedNilMux *mux.Mux
	node.AddTunnel(typedNilMux)
	if got := node.GetTunnel(); got != nil {
		t.Fatalf("GetTunnel() after AddTunnel(typed nil mux) = %#v, want nil", got)
	}

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = client.Close() })

	cfg := mux.DefaultMuxConfig()
	cfg.OpenTimeout = 50 * time.Millisecond
	liveMux := mux.NewMuxWithConfig(client, "tcp", 30, true, cfg)
	t.Cleanup(func() { _ = liveMux.Close() })

	node.AddTunnel(liveMux)
	if got := node.GetTunnel(); got != liveMux {
		t.Fatalf("GetTunnel() = %#v, want live mux", got)
	}

	var typedNilQUIC *quic.Conn
	node.AddTunnel(typedNilQUIC)
	if got := node.GetTunnel(); got != nil {
		t.Fatalf("GetTunnel() after AddTunnel(typed nil quic) = %#v, want nil", got)
	}
}
