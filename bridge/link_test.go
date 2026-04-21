package bridge

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/server/tool"
	"github.com/quic-go/quic-go"
)

type bridgeTestDialer struct {
	dialFn func(remote string) (net.Conn, error)
}

func (d *bridgeTestDialer) DialVirtual(remote string) (net.Conn, error) {
	return d.dialFn(remote)
}

func (d *bridgeTestDialer) ServeVirtual(net.Conn) {}

func newLocalProxyBridge() *Bridge {
	b := NewTunnel(false, &sync.Map{}, 0)
	b.VirtualTcpListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	b.VirtualTlsListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	b.VirtualWsListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	b.VirtualWssListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	b.Client.Store(-1, NewClient(-1, NewNode("local", "test", 0)))
	return b
}

func closeBridgeVirtualListeners(b *Bridge) {
	if b == nil {
		return
	}
	if b.VirtualTcpListener != nil {
		_ = b.VirtualTcpListener.Close()
	}
	if b.VirtualTlsListener != nil {
		_ = b.VirtualTlsListener.Close()
	}
	if b.VirtualWsListener != nil {
		_ = b.VirtualWsListener.Close()
	}
	if b.VirtualWssListener != nil {
		_ = b.VirtualWssListener.Close()
	}
}

func assertVirtualPipe(t *testing.T, target net.Conn, listener *conn.VirtualListener) {
	t.Helper()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	defer func() { _ = accepted.Close() }()

	serverPayload := []byte("server-side")
	clientPayload := []byte("virtual-side")

	go func() {
		_, _ = target.Write(serverPayload)
	}()
	buf := make([]byte, len(serverPayload))
	if _, err := io.ReadFull(accepted, buf); err != nil {
		t.Fatalf("ReadFull(listener) error = %v", err)
	}
	if string(buf) != string(serverPayload) {
		t.Fatalf("listener received %q, want %q", string(buf), string(serverPayload))
	}

	go func() {
		_, _ = accepted.Write(clientPayload)
	}()
	buf = make([]byte, len(clientPayload))
	if _, err := io.ReadFull(target, buf); err != nil {
		t.Fatalf("ReadFull(target) error = %v", err)
	}
	if string(buf) != string(clientPayload) {
		t.Fatalf("target received %q, want %q", string(buf), string(clientPayload))
	}
}

func TestSendLinkInfoBridgeSchemeUsesBridgeVirtualListeners(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	tests := []struct {
		name     string
		target   string
		listener *conn.VirtualListener
	}{
		{name: "tcp", target: "bridge://tcp", listener: b.VirtualTcpListener},
		{name: "tls", target: "bridge://tls", listener: b.VirtualTlsListener},
		{name: "ws", target: "bridge://ws", listener: b.VirtualWsListener},
		{name: "wss", target: "bridge://wss", listener: b.VirtualWssListener},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link := conn.NewLink(common.CONN_TCP, tt.target, false, false, "127.0.0.1:9100", true)
			target, err := b.SendLinkInfo(-1, link, nil)
			if err != nil {
				t.Fatalf("SendLinkInfo() error = %v", err)
			}
			defer func() { _ = target.Close() }()

			assertVirtualPipe(t, target, tt.listener)
		})
	}
}

func TestSendLinkInfoBridgeSchemeUsesWebVirtualListener(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	orig := tool.WebServerListener
	webListener := conn.NewVirtualListener(conn.LocalTCPAddr)
	tool.WebServerListener = webListener
	t.Cleanup(func() {
		_ = webListener.Close()
		tool.WebServerListener = orig
	})

	link := conn.NewLink(common.CONN_TCP, "bridge://web", false, false, "127.0.0.1:9200", true)
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()

	assertVirtualPipe(t, target, webListener)
}

func TestSendLinkInfoTunnelSchemeDelegatesToVirtualDialer(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	tool.SetLookup(func(id int) (tool.Dialer, bool) {
		if id != 11 {
			return nil, false
		}
		return &bridgeTestDialer{
			dialFn: func(remote string) (net.Conn, error) {
				if remote != "127.0.0.1:9300" {
					t.Fatalf("DialVirtual remote = %q, want 127.0.0.1:9300", remote)
				}
				a, b := net.Pipe()
				go func() {
					defer func() { _ = b.Close() }()
					_, _ = b.Write([]byte("ok"))
				}()
				return a, nil
			},
		}, true
	})
	t.Cleanup(func() {
		tool.SetLookup(func(int) (tool.Dialer, bool) { return nil, false })
	})

	link := conn.NewLink(common.CONN_TCP, "tunnel://11", false, false, "127.0.0.1:9300", true)
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()

	buf := make([]byte, 2)
	if _, err := io.ReadFull(target, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != "ok" {
		t.Fatalf("received %q, want ok", string(buf))
	}
}

func TestSendLinkInfoTunnelSchemeRejectsSelfLoop(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	link := conn.NewLink(common.CONN_TCP, "tunnel://11", false, false, "127.0.0.1:9400", true)
	if _, err := b.SendLinkInfo(-1, link, &file.Tunnel{Id: 11}); err == nil {
		t.Fatal("SendLinkInfo() should reject tunnel self-loop")
	}
}

func TestSendLinkInfoLocalProxyDirectDialUsesTimeout(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	originalDial := bridgeDirectDial
	bridgeDirectDial = func(timeout time.Duration, network, address string) (net.Conn, error) {
		if timeout != 1500*time.Millisecond {
			t.Fatalf("dial timeout = %v, want %v", timeout, 1500*time.Millisecond)
		}
		if network != "tcp" {
			t.Fatalf("dial network = %q, want tcp", network)
		}
		if address != "127.0.0.1:9601" {
			t.Fatalf("dial address = %q, want 127.0.0.1:9601", address)
		}
		server, client := net.Pipe()
		go func() {
			defer func() { _ = client.Close() }()
			_, _ = client.Write([]byte("ok"))
		}()
		return server, nil
	}
	t.Cleanup(func() {
		bridgeDirectDial = originalDial
	})

	link := conn.NewLink(common.CONN_TCP, "127.0.0.1:9601", false, false, "127.0.0.1:9600", true, conn.LinkTimeout(1500*time.Millisecond), conn.WithConnectResult(true))
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()

	if link.Option.WaitConnectResult {
		t.Fatal("local proxy should disable connect result waiting")
	}

	buf := make([]byte, 2)
	if _, err := io.ReadFull(target, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != "ok" {
		t.Fatalf("received %q, want ok", string(buf))
	}
}

func TestSendLinkInfoLocalProxyDirectDialUsesDefaultTimeoutWhenZero(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	originalDial := bridgeDirectDial
	bridgeDirectDial = func(timeout time.Duration, network, address string) (net.Conn, error) {
		if timeout != 5*time.Second {
			t.Fatalf("dial timeout = %v, want %v", timeout, 5*time.Second)
		}
		server, client := net.Pipe()
		go func() {
			defer func() { _ = client.Close() }()
			_, _ = client.Write([]byte("ok"))
		}()
		return server, nil
	}
	t.Cleanup(func() {
		bridgeDirectDial = originalDial
	})

	link := conn.NewLink(common.CONN_TCP, "127.0.0.1:9602", false, false, "127.0.0.1:9600", true)
	link.Option.Timeout = 0
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()
}

func TestSendLinkInfoLocalProxyDirectDialTrimsHostBeforeFormatting(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	originalDial := bridgeDirectDial
	bridgeDirectDial = func(timeout time.Duration, network, address string) (net.Conn, error) {
		if timeout != 5*time.Second {
			t.Fatalf("dial timeout = %v, want %v", timeout, 5*time.Second)
		}
		if network != "tcp" {
			t.Fatalf("dial network = %q, want tcp", network)
		}
		if address != "127.0.0.1:9603" {
			t.Fatalf("dial address = %q, want trimmed 127.0.0.1:9603", address)
		}
		server, client := net.Pipe()
		go func() {
			defer func() { _ = client.Close() }()
			_, _ = client.Write([]byte("ok"))
		}()
		return server, nil
	}
	t.Cleanup(func() {
		bridgeDirectDial = originalDial
	})

	link := conn.NewLink(common.CONN_TCP, " 9603 ", false, false, "127.0.0.1:9600", true)
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()
}

func TestSendLinkInfoLocalProxyUDP5UsesExplicitTimeout(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	originalHandleUDP5 := bridgeHandleUDP5
	timeoutCh := make(chan time.Duration, 1)
	bridgeHandleUDP5 = func(_ context.Context, serverConn net.Conn, timeout time.Duration, localIP string) {
		timeoutCh <- timeout
		_ = serverConn.Close()
	}
	t.Cleanup(func() {
		bridgeHandleUDP5 = originalHandleUDP5
	})

	link := conn.NewLink("udp5", "unused", false, false, "127.0.0.1:9600", true, conn.LinkTimeout(1500*time.Millisecond))
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()

	select {
	case timeout := <-timeoutCh:
		if timeout != 1500*time.Millisecond {
			t.Fatalf("udp5 timeout = %v, want %v", timeout, 1500*time.Millisecond)
		}
	case <-time.After(time.Second):
		t.Fatal("bridgeHandleUDP5 was not called")
	}
}

func TestSendLinkInfoLocalProxyUDP5UsesDefaultTimeoutWhenZero(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	originalHandleUDP5 := bridgeHandleUDP5
	timeoutCh := make(chan time.Duration, 1)
	bridgeHandleUDP5 = func(_ context.Context, serverConn net.Conn, timeout time.Duration, localIP string) {
		timeoutCh <- timeout
		_ = serverConn.Close()
	}
	t.Cleanup(func() {
		bridgeHandleUDP5 = originalHandleUDP5
	})

	link := conn.NewLink("udp5", "unused", false, false, "127.0.0.1:9600", true)
	link.Option.Timeout = 0
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	defer func() { _ = target.Close() }()

	select {
	case timeout := <-timeoutCh:
		if timeout != 5*time.Second {
			t.Fatalf("udp5 timeout = %v, want %v", timeout, 5*time.Second)
		}
	case <-time.After(time.Second):
		t.Fatal("bridgeHandleUDP5 was not called")
	}
}

func TestSendLinkInfoLocalProxyRejectsUnsupportedScheme(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	link := conn.NewLink(common.CONN_TCP, "unknown://target", false, false, "127.0.0.1:9600", true, conn.WithConnectResult(true))
	if _, err := b.SendLinkInfo(-1, link, nil); err == nil || !strings.Contains(err.Error(), "unsupported scheme") {
		t.Fatalf("SendLinkInfo() error = %v, want unsupported scheme error", err)
	}
	if link.Option.WaitConnectResult {
		t.Fatal("local proxy should disable connect result waiting before local target resolution")
	}
}

func TestSendLinkInfoLocalProxyRejectsMissingSchemeTarget(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)

	link := conn.NewLink(common.CONN_TCP, "bridge://", false, false, "127.0.0.1:9600", true)
	if _, err := b.SendLinkInfo(-1, link, nil); err == nil || !strings.Contains(err.Error(), "missing target") {
		t.Fatalf("SendLinkInfo() error = %v, want missing target error", err)
	}
}

func TestSendLinkInfoLocalProxyRejectsUnavailableBridgeListener(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)
	b.VirtualTcpListener = nil

	link := conn.NewLink(common.CONN_TCP, "bridge://tcp", false, false, "127.0.0.1:9600", true)
	if _, err := b.SendLinkInfo(-1, link, nil); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("SendLinkInfo() error = %v, want unavailable bridge target error", err)
	}
}

func TestOpenRuntimeTunnelConnUsesLinkTimeoutForMux(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	cfg := mux.DefaultMuxConfig()
	cfg.OpenTimeout = 2 * time.Second
	tun := mux.NewMuxWithConfig(client, "tcp", 30, true, cfg)
	defer func() { _ = tun.Close() }()

	start := time.Now()
	_, err := openRuntimeTunnelConn(tun, 60*time.Millisecond)
	if err == nil {
		t.Fatal("openRuntimeTunnelConn() expected mux open timeout")
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("openRuntimeTunnelConn() elapsed %s, want link timeout budget", elapsed)
	}
}

func TestOpenQuicTunnelStreamRejectsNilTunnel(t *testing.T) {
	var typedNilQUIC *quic.Conn
	if target, err := openQuicTunnelStream(typedNilQUIC, 100*time.Millisecond); err == nil || err.Error() != "the tunnel is unavailable" {
		if target != nil {
			_ = target.Close()
		}
		t.Fatalf("openQuicTunnelStream(typed nil quic) error = %v, want tunnel unavailable", err)
	}
}

func TestNormalizeBridgeLinkTimeoutUsesDefaultWhenUnset(t *testing.T) {
	if got := normalizeBridgeLinkTimeout(0); got != 5*time.Second {
		t.Fatalf("normalizeBridgeLinkTimeout(0) = %v, want %v", got, 5*time.Second)
	}
	if got := normalizeBridgeLinkTimeout(-time.Second); got != 5*time.Second {
		t.Fatalf("normalizeBridgeLinkTimeout(-1s) = %v, want %v", got, 5*time.Second)
	}
	if got := normalizeBridgeLinkTimeout(120 * time.Millisecond); got != 120*time.Millisecond {
		t.Fatalf("normalizeBridgeLinkTimeout(120ms) = %v, want %v", got, 120*time.Millisecond)
	}
}

func TestCloseNodeOnLinkAckFailureIgnoresStaleTunnelReplacement(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("node-1", "test", 6)
	client := NewClient(1, node)

	oldServer, oldPeer := net.Pipe()
	newServer, newPeer := net.Pipe()
	t.Cleanup(func() { _ = oldPeer.Close() })
	t.Cleanup(func() { _ = newPeer.Close() })
	t.Cleanup(func() { _ = oldServer.Close() })
	t.Cleanup(func() { _ = newServer.Close() })

	oldSignal := conn.NewConn(oldServer)
	newSignal := conn.NewConn(newServer)
	oldTunnel := &struct{ name string }{"old"}
	newTunnel := &struct{ name string }{"new"}

	node.AddSignal(oldSignal)
	node.AddTunnel(oldTunnel)
	node.AddSignal(newSignal)
	node.AddTunnel(newTunnel)

	b.closeNodeOnLinkAckFailure(client, node, oldTunnel)

	if got := node.GetSignal(); got != newSignal {
		t.Fatalf("node current signal = %v, want replacement signal", got)
	}
	if newSignal.IsClosed() {
		t.Fatal("replacement signal should remain open after stale ACK cleanup")
	}
	if got := node.GetTunnel(); got != newTunnel {
		t.Fatalf("node current tunnel = %v, want replacement tunnel", got)
	}
	if client.IsClosed() {
		t.Fatal("client should remain open after stale ACK cleanup")
	}
}

func TestCloseNodeOnLinkAckFailureRemovesEmptyRuntimeClient(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("ack-node", "test", 6)
	client := NewClient(45, node)
	b.Client.Store(45, client)

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })

	signal := conn.NewConn(serverConn)
	tunnel := &struct{ name string }{"current"}
	node.AddSignal(signal)
	node.AddTunnel(tunnel)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	b.closeNodeOnLinkAckFailure(client, node, tunnel)

	if _, ok := b.loadRuntimeClient(45); ok {
		t.Fatal("empty runtime client should be removed after ACK failure closes the last node")
	}
	if closedClientID != 45 {
		t.Fatalf("close client hook = %d, want 45", closedClientID)
	}
}

func TestCleanupExpiredRegistrationsRemovesExpiredEntries(t *testing.T) {
	b := NewTunnel(true, &sync.Map{}, 0)
	now := time.Now()
	b.Register.Store("192.0.2.10", now.Add(-time.Minute))
	b.Register.Store("192.0.2.11", now.Add(time.Minute))

	removed := b.CleanupExpiredRegistrations(now)
	if removed != 1 {
		t.Fatalf("CleanupExpiredRegistrations() removed = %d, want 1", removed)
	}
	if _, ok := b.Register.Load("192.0.2.10"); ok {
		t.Fatal("expired registration should be removed")
	}
	if _, ok := b.Register.Load("192.0.2.11"); !ok {
		t.Fatal("valid registration should stay")
	}
}

func TestCleanupExpiredRegistrationsIgnoresReplacementForInvalidEntry(t *testing.T) {
	b := NewTunnel(true, &sync.Map{}, 0)
	now := time.Now()
	validExpiry := now.Add(time.Minute)
	b.Register.Store("192.0.2.10", validExpiry)

	if removed := b.removeRegistrationValueIfCurrent("192.0.2.10", "stale-invalid-value"); removed {
		t.Fatal("removeRegistrationValueIfCurrent should not remove a replacement registration")
	}

	removed := b.CleanupExpiredRegistrations(now)
	if removed != 0 {
		t.Fatalf("CleanupExpiredRegistrations() removed = %d, want 0", removed)
	}
	current, ok := b.Register.Load("192.0.2.10")
	if !ok {
		t.Fatal("replacement registration should remain installed")
	}
	if got, ok := current.(time.Time); !ok || !got.Equal(validExpiry) {
		t.Fatalf("replacement registration = %v, want %v", current, validExpiry)
	}
}

func TestCleanupExpiredRegistrationsDropsInvalidKeyWithoutAffectingValidEntries(t *testing.T) {
	b := NewTunnel(true, &sync.Map{}, 0)
	now := time.Now()
	b.Register.Store(12345, "bad-registration-key")
	validExpiry := now.Add(time.Minute)
	b.Register.Store("192.0.2.20", validExpiry)

	removed := b.CleanupExpiredRegistrations(now)
	if removed != 1 {
		t.Fatalf("CleanupExpiredRegistrations() removed = %d, want 1", removed)
	}
	if _, ok := b.Register.Load(12345); ok {
		t.Fatal("invalid registration key should be removed")
	}
	current, ok := b.Register.Load("192.0.2.20")
	if !ok {
		t.Fatal("valid registration should remain installed")
	}
	if got, ok := current.(time.Time); !ok || !got.Equal(validExpiry) {
		t.Fatalf("valid registration = %v, want %v", current, validExpiry)
	}
}

func TestRemoveRegistrationIfCurrentIgnoresReplacement(t *testing.T) {
	b := NewTunnel(true, &sync.Map{}, 0)
	oldExpiry := time.Now().Add(-time.Minute)
	newExpiry := time.Now().Add(time.Minute)
	b.Register.Store("192.0.2.10", newExpiry)

	if b.removeRegistrationIfCurrent("192.0.2.10", oldExpiry) {
		t.Fatal("removeRegistrationIfCurrent should not remove a replacement registration")
	}
	current, ok := b.Register.Load("192.0.2.10")
	if !ok {
		t.Fatal("replacement registration should remain installed")
	}
	if got, ok := current.(time.Time); !ok || !got.Equal(newExpiry) {
		t.Fatalf("replacement registration = %v, want %v", current, newExpiry)
	}
}

func TestSendLinkInfoInvalidRegistrationDoesNotDeleteReplacement(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)
	b.ipVerify.Store(true)
	b.Register.Store("127.0.0.1", time.Now().Add(time.Minute))

	if removed := b.removeRegistrationValueIfCurrent("127.0.0.1", "stale-invalid-value"); removed {
		t.Fatal("removeRegistrationValueIfCurrent should not remove a replacement registration")
	}

	link := conn.NewLink(common.CONN_TCP, "bridge://tcp", false, false, "127.0.0.1:9500", true)
	target, err := b.SendLinkInfo(-1, link, nil)
	if err != nil {
		t.Fatalf("SendLinkInfo() error = %v", err)
	}
	_ = target.Close()

	current, ok := b.Register.Load("127.0.0.1")
	if !ok {
		t.Fatal("replacement registration should remain installed")
	}
	if got, ok := current.(time.Time); !ok || got.Before(time.Now()) {
		t.Fatalf("replacement registration = %v, want live time.Time value", current)
	}
}

func TestSendLinkInfoRemovesExpiredRegistrationOnAccess(t *testing.T) {
	b := newLocalProxyBridge()
	defer closeBridgeVirtualListeners(b)
	b.ipVerify.Store(true)
	b.Register.Store("127.0.0.1", time.Now().Add(-time.Minute))

	link := conn.NewLink(common.CONN_TCP, "bridge://tcp", false, false, "127.0.0.1:9500", true)
	if _, err := b.SendLinkInfo(-1, link, nil); err == nil {
		t.Fatal("SendLinkInfo() should reject expired registration")
	}
	if _, ok := b.Register.Load("127.0.0.1"); ok {
		t.Fatal("expired registration should be deleted after access")
	}
}

func TestClientTunnelUnavailablePrunesOnlyStaleNode(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	primary := NewNode("primary-node", "test", 6)
	backup := NewNode("backup-node", "test", 6)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	backup.AddSignal(conn.NewConn(serverConn))
	backup.AddTunnel(&struct{ name string }{"backup"})

	client := NewClient(41, primary)
	client.AddNode(backup)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	b.Client.Store(41, client)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	err := b.clientTunnelUnavailable(41, client, primary)
	if !errors.Is(err, errBridgeClientTunnelUnavailable) {
		t.Fatalf("clientTunnelUnavailable() error = %v, want wrapped %v", err, errBridgeClientTunnelUnavailable)
	}
	if _, ok := b.loadRuntimeClient(41); !ok {
		t.Fatal("runtime client should remain installed when only one node tunnel is unavailable")
	}
	if _, ok := client.GetNodeByUUID("primary-node"); ok {
		t.Fatal("stale primary node should be pruned after tunnel-unavailable race")
	}
	if got, ok := client.GetNodeByUUID("backup-node"); !ok || got != backup {
		t.Fatalf("backup node should remain installed, ok=%v got=%v", ok, got)
	}
	if closedClientID != 0 {
		t.Fatalf("close client hook = %d, want 0", closedClientID)
	}
}

func TestClientTunnelUnavailableRemovesEmptyRuntimeClient(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("solo-node", "test", 6)
	client := NewClient(42, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	b.Client.Store(42, client)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	err := b.clientTunnelUnavailable(42, client, node)
	if !errors.Is(err, errBridgeClientTunnelUnavailable) {
		t.Fatalf("clientTunnelUnavailable() error = %v, want wrapped %v", err, errBridgeClientTunnelUnavailable)
	}
	if _, ok := b.loadRuntimeClient(42); ok {
		t.Fatal("empty runtime client should be removed after last node tunnel becomes unavailable")
	}
	if closedClientID != 42 {
		t.Fatalf("close client hook = %d, want 42", closedClientID)
	}
}

func TestResolveClientUUIDTunnelRemovesEmptyRuntimeClient(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("solo-node", "test", 6)
	client := NewClient(43, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	node.joinNano = time.Now().Add(-2 * nodeJoinGraceProtectWindow).UnixNano()
	b.Client.Store(43, client)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	gotNode, gotTunnel, err := b.resolveClientUUIDTunnel(client, "solo-node")
	if err == nil || err.Error() != "the client instance solo-node is offline" {
		t.Fatalf("resolveClientUUIDTunnel() error = %v, want offline error", err)
	}
	if gotNode != nil || gotTunnel != nil {
		t.Fatalf("resolveClientUUIDTunnel() = (%v, %v), want nil results", gotNode, gotTunnel)
	}
	if _, ok := b.loadRuntimeClient(43); ok {
		t.Fatal("empty runtime client should be removed after last route uuid becomes offline")
	}
	if closedClientID != 43 {
		t.Fatalf("close client hook = %d, want 43", closedClientID)
	}
}

func TestResolveClientUUIDTunnelPrunesMissingRouteEntryOutsideGrace(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("stale-route-node", "test", 6)
	client := NewClient(45, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	client.nodes.Delete("stale-route-node")

	gotNode, gotTunnel, err := b.resolveClientUUIDTunnel(client, "stale-route-node")
	if err == nil || err.Error() != "the client instance stale-route-node is not connect" {
		t.Fatalf("resolveClientUUIDTunnel() error = %v, want missing route error", err)
	}
	if gotNode != nil || gotTunnel != nil {
		t.Fatalf("resolveClientUUIDTunnel() = (%v, %v), want nil results", gotNode, gotTunnel)
	}
	if count := client.NodeCount(); count != 0 {
		t.Fatalf("NodeCount() = %d, want 0 after missing route uuid is pruned", count)
	}
}

func TestResolveClientUUIDTunnelRejectsMissingRouteUUID(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	client := NewClient(451, NewNode("route-node", "test", 6))

	gotNode, gotTunnel, err := b.resolveClientUUIDTunnel(client, " ")
	if !errors.Is(err, errBridgeClientRouteUUIDRequired) {
		t.Fatalf("resolveClientUUIDTunnel() error = %v, want wrapped %v", err, errBridgeClientRouteUUIDRequired)
	}
	if gotNode != nil || gotTunnel != nil {
		t.Fatalf("resolveClientUUIDTunnel() = (%v, %v), want nil results", gotNode, gotTunnel)
	}
}

func TestResolveClientUUIDTunnelLegacyReconnectKeepsRuntime(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("legacy-route-node", "test", 4)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	node.AddSignal(conn.NewConn(serverConn))
	client := NewClient(46, node)

	gotNode, gotTunnel, err := b.resolveClientUUIDTunnel(client, "legacy-route-node")
	if !errors.Is(err, errBridgeClientTunnelReconnecting) {
		t.Fatalf("resolveClientUUIDTunnel() error = %v, want wrapped %v", err, errBridgeClientTunnelReconnecting)
	}
	if gotNode != nil || gotTunnel != nil {
		t.Fatalf("resolveClientUUIDTunnel() = (%v, %v), want nil results during reconnect grace", gotNode, gotTunnel)
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("NodeCount() = %d, want 1 while legacy route waits for tunnel reconnect", count)
	}
}

func TestClientTunnelUnavailableLegacyTunnelReconnectDuringGrace(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("legacy-solo-node", "test", 4)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	node.AddSignal(conn.NewConn(serverConn))
	client := NewClient(47, node)
	b.Client.Store(47, client)

	err := b.clientTunnelUnavailable(47, client, node)
	if !errors.Is(err, errBridgeClientTunnelReconnecting) {
		t.Fatalf("clientTunnelUnavailable() error = %v, want wrapped %v", err, errBridgeClientTunnelReconnecting)
	}
	if _, ok := b.loadRuntimeClient(47); !ok {
		t.Fatal("runtime client should remain installed while legacy tunnel is reconnecting")
	}
}

func TestClientTunnelUnavailableLegacyTunnelOutsideGraceRemovesRuntimeClient(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("legacy-outside-grace", "test", 4)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	node.AddSignal(conn.NewConn(serverConn))
	client := NewClient(48, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	b.Client.Store(48, client)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	err := b.clientTunnelUnavailable(48, client, node)
	if !errors.Is(err, errBridgeClientTunnelUnavailable) {
		t.Fatalf("clientTunnelUnavailable() error = %v, want wrapped %v", err, errBridgeClientTunnelUnavailable)
	}
	if _, ok := b.loadRuntimeClient(48); ok {
		t.Fatal("runtime client should be removed when legacy tunnel stays unavailable outside grace")
	}
	if closedClientID != 48 {
		t.Fatalf("close client hook = %d, want 48", closedClientID)
	}
}

func TestClientNodeUnavailableOutsideGraceRemovesRuntimeClient(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	client := NewClient(481, NewNode("connect-node", "test", 6))
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	b.Client.Store(481, client)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	err := b.clientNodeUnavailable(481, client)
	if !errors.Is(err, errBridgeClientConnectUnavailable) {
		t.Fatalf("clientNodeUnavailable() error = %v, want wrapped %v", err, errBridgeClientConnectUnavailable)
	}
	if _, ok := b.loadRuntimeClient(481); ok {
		t.Fatal("runtime client should be removed when client connect state stays unavailable outside grace")
	}
	if closedClientID != 481 {
		t.Fatalf("close client hook = %d, want 481", closedClientID)
	}
}

func TestResolveClientFileTunnelRemovesEmptyRuntimeClient(t *testing.T) {
	b := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("file-node", "test", 6)
	client := NewClient(44, node)
	if err := client.AddFile("demo-key", "file-node"); err != nil {
		t.Fatalf("AddFile() error = %v", err)
	}
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*clientConnectGraceWindow).UnixNano())
	b.Client.Store(44, client)

	var closedClientID int
	b.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	link := conn.NewLink(common.CONN_TCP, "file://demo-key", false, false, "127.0.0.1:9500", true)
	gotNode, gotTunnel, err := b.resolveClientFileTunnel(client, link, "file://demo-key")
	if err == nil || err.Error() != "failed to find tunnel for host: file://demo-key" {
		t.Fatalf("resolveClientFileTunnel() error = %v, want missing file tunnel", err)
	}
	if gotNode != nil || gotTunnel != nil {
		t.Fatalf("resolveClientFileTunnel() = (%v, %v), want nil results", gotNode, gotTunnel)
	}
	if _, ok := b.loadRuntimeClient(44); ok {
		t.Fatal("empty runtime client should be removed after last file route becomes offline")
	}
	if closedClientID != 44 {
		t.Fatalf("close client hook = %d, want 44", closedClientID)
	}
}
