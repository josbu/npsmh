package bridge

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server/connection"
)

type blockingCloseConn struct {
	closeStarted chan struct{}
	releaseClose chan struct{}
}

func newBlockingCloseConn() *blockingCloseConn {
	return &blockingCloseConn{
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
}

func (c *blockingCloseConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *blockingCloseConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *blockingCloseConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *blockingCloseConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *blockingCloseConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingCloseConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockingCloseConn) SetWriteDeadline(time.Time) error { return nil }
func (c *blockingCloseConn) Close() error {
	select {
	case <-c.closeStarted:
	default:
		close(c.closeStarted)
	}
	<-c.releaseClose
	return nil
}

func TestGetHealthFromClientIgnoresStaleSignalDisconnect(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
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
	node.AddSignal(oldSignal)

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtimeBridge.GetHealthFromClient(client.Id, oldSignal, client, node)
	}()

	node.AddSignal(newSignal)
	<-done

	if got := node.GetSignal(); got != newSignal {
		t.Fatalf("node current signal = %v, want replacement signal", got)
	}
	if newSignal.IsClosed() {
		t.Fatal("replacement signal should remain open after stale health loop exits")
	}
	if client.IsClosed() {
		t.Fatal("client should remain open after stale signal disconnect")
	}
}

func TestGetHealthFromClientRemovesEmptyRuntimeClient(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("solo-health-node", "test", 6)
	client := NewClient(46, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*connectGraceProtectWindow).UnixNano())
	node.joinNano = time.Now().Add(-2 * nodeJoinGraceProtectWindow).UnixNano()
	runtimeBridge.Client.Store(46, client)

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })

	signal := conn.NewConn(serverConn)
	node.AddSignal(signal)

	var closedClientID int
	runtimeBridge.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtimeBridge.GetHealthFromClient(46, signal, client, node)
	}()

	_ = peerConn.Close()
	<-done

	if _, ok := runtimeBridge.loadRuntimeClient(46); ok {
		t.Fatal("empty runtime client should be removed after health reader closes the last node")
	}
	if closedClientID != 46 {
		t.Fatalf("close client hook = %d, want 46", closedClientID)
	}
}

func TestGetHealthFromClientNilConnIsIgnored(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("health-nil-conn", "test", 6)
	client := NewClient(47, node)
	runtimeBridge.Client.Store(47, client)

	runtimeBridge.GetHealthFromClient(47, nil, client, node)

	if got, ok := runtimeBridge.loadRuntimeClient(47); !ok || got != client {
		t.Fatalf("runtime client = %#v, %v, want original client to remain installed", got, ok)
	}
	if got, ok := client.GetNodeByUUID("health-nil-conn"); !ok || got != node {
		t.Fatalf("GetNodeByUUID(health-nil-conn) = %#v, %v, want node to remain attached", got, ok)
	}
}

type bridgeTestListener struct {
	addr       net.Addr
	closeCount atomic.Int32
}

func (l *bridgeTestListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }

func (l *bridgeTestListener) Close() error {
	l.closeCount.Add(1)
	return nil
}

func (l *bridgeTestListener) Addr() net.Addr {
	if l.addr != nil {
		return l.addr
	}
	return conn.LocalTCPAddr
}

func TestBridgeDelClientInvokesCloseClientHook(t *testing.T) {
	resetBridgeConfigTestDB(t)

	dbClient := file.NewClient("bridge-runtime-test", false, false)
	if err := file.GetDb().NewClient(dbClient); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store(dbClient.Id, NewClient(dbClient.Id, NewNode("node-1", "test", 0)))

	var closedClient int
	runtimeBridge.SetCloseClientHook(func(id int) {
		closedClient = id
	})

	runtimeBridge.DelClient(dbClient.Id)

	if closedClient != dbClient.Id {
		t.Fatalf("close client hook = %d, want %d", closedClient, dbClient.Id)
	}
	if _, ok := runtimeBridge.Client.Load(dbClient.Id); ok {
		t.Fatalf("bridge client %d should be removed after DelClient()", dbClient.Id)
	}
}

func TestBridgeDelClientSkipsCloseClientHookForPublicClient(t *testing.T) {
	resetBridgeConfigTestDB(t)

	dbClient := file.NewClient("bridge-runtime-public", false, true)
	if err := file.GetDb().NewClient(dbClient); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store(dbClient.Id, NewClient(dbClient.Id, NewNode("node-1", "test", 0)))

	called := false
	runtimeBridge.SetCloseClientHook(func(int) {
		called = true
	})

	runtimeBridge.DelClient(dbClient.Id)

	if called {
		t.Fatal("close client hook should be skipped for public clients")
	}
}

func TestBridgeDelClientDropsInvalidRuntimeEntryWithoutPanic(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store(15, "bad-client-entry")

	runtimeBridge.DelClient(15)

	if _, ok := runtimeBridge.Client.Load(15); ok {
		t.Fatal("invalid bridge client entry should be removed during DelClient")
	}
}

func TestBridgeLoadRuntimeClientDropsInvalidEntry(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store(16, "bad-client-entry")

	client, ok := runtimeBridge.loadRuntimeClient(16)
	if ok || client != nil {
		t.Fatalf("loadRuntimeClient(16) = %#v, %v, want nil, false", client, ok)
	}
	if _, ok := runtimeBridge.Client.Load(16); ok {
		t.Fatal("loadRuntimeClient should remove invalid runtime entry")
	}
}

func TestBridgeLoadOrStoreRuntimeClientReplacesInvalidEntry(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store(17, "bad-client-entry")
	candidate := NewClient(17, NewNode("node-17", "test", 0))

	client, loaded := runtimeBridge.loadOrStoreRuntimeClient(17, candidate)
	if loaded {
		t.Fatal("loadOrStoreRuntimeClient should reinstall candidate after removing invalid entry")
	}
	if client != candidate {
		t.Fatalf("loadOrStoreRuntimeClient returned %#v, want candidate %#v", client, candidate)
	}
	if current, ok := runtimeBridge.Client.Load(17); !ok || current != candidate {
		t.Fatalf("candidate should be installed after invalid entry replacement, current=%v ok=%v", current, ok)
	}
}

func TestBridgeCollectPingClosedClientsDropsInvalidKey(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store("bad-key", "bad-client-entry")

	closed := runtimeBridge.collectPingClosedClients()
	if len(closed) != 0 {
		t.Fatalf("collectPingClosedClients() = %v, want no closed client ids", closed)
	}
	if _, ok := runtimeBridge.Client.Load("bad-key"); ok {
		t.Fatal("invalid bridge client key should be removed during ping collection")
	}
}

func TestBridgeCollectPingClosedClientsSchedulesInvalidClientValue(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	runtimeBridge.Client.Store(18, "bad-client-entry")

	closed := runtimeBridge.collectPingClosedClients()
	if len(closed) != 1 || closed[0] != 18 {
		t.Fatalf("collectPingClosedClients() = %v, want [18]", closed)
	}
}

func TestBridgeCollectPingClosedClientsRemovesEmptyRuntimeClientImmediately(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("ping-node", "test", 6)
	client := NewClient(19, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*connectGraceProtectWindow).UnixNano())
	node.joinNano = time.Now().Add(-2 * nodeJoinGraceProtectWindow).UnixNano()
	_ = node.Close()
	runtimeBridge.Client.Store(19, client)

	var closedClientID int
	runtimeBridge.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	closed := runtimeBridge.collectPingClosedClients()
	if len(closed) != 0 {
		t.Fatalf("collectPingClosedClients() = %v, want no deferred closed ids", closed)
	}
	if _, ok := runtimeBridge.Client.Load(19); ok {
		t.Fatal("empty runtime client should be removed immediately during ping collection")
	}
	if closedClientID != 19 {
		t.Fatalf("close client hook = %d, want 19", closedClientID)
	}
}

func TestBridgeCollectPingClosedClientsResetsRetryForHealthyClient(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("ping-healthy", "test", 6)
	node.BaseVer = 4
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	node.AddSignal(conn.NewConn(serverConn))
	client := NewClient(20, node)
	client.pingRetryTime = 2
	runtimeBridge.Client.Store(20, client)

	closed := runtimeBridge.collectPingClosedClients()
	if len(closed) != 0 {
		t.Fatalf("collectPingClosedClients() = %v, want no closed clients for healthy runtime", closed)
	}
	if client.pingRetryTime != 0 {
		t.Fatalf("pingRetryTime = %d, want reset to 0 for healthy client", client.pingRetryTime)
	}
	if _, ok := runtimeBridge.Client.Load(20); !ok {
		t.Fatal("healthy runtime client should remain registered after ping collection")
	}
}

func TestBridgeCollectPingClosedClientsClosesDeferredRuntimeAfterRetryBudget(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	client := NewClient(21, NewNode("ping-deferred", "test", 6))
	runtimeBridge.Client.Store(21, client)

	for i := 0; i < retryTimeMax-1; i++ {
		closed := runtimeBridge.collectPingClosedClients()
		if len(closed) != 0 {
			t.Fatalf("attempt %d collectPingClosedClients() = %v, want no closed ids before retry budget is exhausted", i+1, closed)
		}
	}

	closed := runtimeBridge.collectPingClosedClients()
	if len(closed) != 1 || closed[0] != 21 {
		t.Fatalf("collectPingClosedClients() = %v, want [21] after retry budget is exhausted", closed)
	}
	if client.pingRetryTime != retryTimeMax {
		t.Fatalf("pingRetryTime = %d, want %d after deferred runtime reaches retry budget", client.pingRetryTime, retryTimeMax)
	}
	if _, ok := runtimeBridge.Client.Load(21); !ok {
		t.Fatal("collectPingClosedClients should only schedule close; runtime remains until DelClient runs")
	}
}

func TestRemoveCurrentClientIgnoresReplacementRuntime(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	oldClient := NewClient(7, NewNode("node-old", "test", 0))
	newClient := NewClient(7, NewNode("node-new", "test", 0))

	runtimeBridge.Client.Store(7, newClient)

	if runtimeBridge.removeCurrentClient(7, oldClient) {
		t.Fatal("removeCurrentClient should not remove a replacement runtime client")
	}
	if current, ok := runtimeBridge.Client.Load(7); !ok || current != newClient {
		t.Fatalf("runtime client replacement should remain installed, current=%v ok=%v", current, ok)
	}
}

func TestRemoveCurrentClientRemovesCurrentRuntime(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	currentClient := NewClient(7, NewNode("node-current", "test", 0))
	runtimeBridge.Client.Store(7, currentClient)

	if !runtimeBridge.removeCurrentClient(7, currentClient) {
		t.Fatal("removeCurrentClient should remove the current runtime client")
	}
	if _, ok := runtimeBridge.Client.Load(7); ok {
		t.Fatal("runtime client should be removed after removeCurrentClient")
	}
}

func TestStartTunnelReturnsBootstrapErrorAndClosesEarlierListeners(t *testing.T) {
	originalTCPEnabled := ServerTcpEnable
	originalTLSEnabled := ServerTlsEnable
	originalWsEnabled := ServerWsEnable
	originalWssEnabled := ServerWssEnable
	originalKCPEnabled := ServerKcpEnable
	originalQUICEnabled := ServerQuicEnable
	originalReservedTLSGetter := bridgeGetReservedTLSListener
	originalTCPGetter := bridgeGetTCPListener
	originalTLSGetter := bridgeGetTLSListener
	originalWSGetter := bridgeGetWSListener
	originalWSSGetter := bridgeGetWSSListener
	t.Cleanup(func() {
		ServerTcpEnable = originalTCPEnabled
		ServerTlsEnable = originalTLSEnabled
		ServerWsEnable = originalWsEnabled
		ServerWssEnable = originalWssEnabled
		ServerKcpEnable = originalKCPEnabled
		ServerQuicEnable = originalQUICEnabled
		bridgeGetReservedTLSListener = originalReservedTLSGetter
		bridgeGetTCPListener = originalTCPGetter
		bridgeGetTLSListener = originalTLSGetter
		bridgeGetWSListener = originalWSGetter
		bridgeGetWSSListener = originalWSSGetter
	})

	ServerTcpEnable = true
	ServerTlsEnable = true
	ServerWsEnable = false
	ServerWssEnable = false
	ServerKcpEnable = false
	ServerQuicEnable = false

	tcpListener := &bridgeTestListener{addr: conn.LocalTCPAddr}
	wantErr := errors.New("tls bootstrap failed")

	bridgeGetReservedTLSListener = func() (net.Listener, error) {
		t.Fatal("reserved TLS listener should not be used without WSS enabled")
		return nil, nil
	}
	bridgeGetTCPListener = func() (net.Listener, error) {
		return tcpListener, nil
	}
	bridgeGetTLSListener = func() (net.Listener, error) {
		return nil, wantErr
	}
	bridgeGetWSListener = func() (net.Listener, error) {
		t.Fatal("ws listener should not be used when disabled")
		return nil, nil
	}
	bridgeGetWSSListener = func() (net.Listener, error) {
		t.Fatal("wss listener should not be used when disabled")
		return nil, nil
	}

	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	err := runtimeBridge.StartTunnel()
	if !errors.Is(err, wantErr) {
		t.Fatalf("StartTunnel() error = %v, want %v", err, wantErr)
	}
	if got := tcpListener.closeCount.Load(); got != 1 {
		t.Fatalf("tcp bootstrap listener close count = %d, want 1", got)
	}
	if runtimeBridge.VirtualTcpListener != nil || runtimeBridge.VirtualTlsListener != nil ||
		runtimeBridge.VirtualWsListener != nil || runtimeBridge.VirtualWssListener != nil {
		t.Fatal("virtual listeners should not be published when bootstrap fails")
	}
}

func TestBootstrapBridgeListenersUsesReservedTLSGateway(t *testing.T) {
	originalTCPEnabled := ServerTcpEnable
	originalTLSEnabled := ServerTlsEnable
	originalWsEnabled := ServerWsEnable
	originalWssEnabled := ServerWssEnable
	originalReservedTLSGetter := bridgeGetReservedTLSListener
	originalTCPGetter := bridgeGetTCPListener
	originalTLSGetter := bridgeGetTLSListener
	originalWSGetter := bridgeGetWSListener
	originalWSSGetter := bridgeGetWSSListener
	t.Cleanup(func() {
		ServerTcpEnable = originalTCPEnabled
		ServerTlsEnable = originalTLSEnabled
		ServerWsEnable = originalWsEnabled
		ServerWssEnable = originalWssEnabled
		bridgeGetReservedTLSListener = originalReservedTLSGetter
		bridgeGetTCPListener = originalTCPGetter
		bridgeGetTLSListener = originalTLSGetter
		bridgeGetWSListener = originalWSGetter
		bridgeGetWSSListener = originalWSSGetter
	})

	ServerTcpEnable = false
	ServerTlsEnable = true
	ServerWsEnable = false
	ServerWssEnable = true

	reservedListener := &bridgeTestListener{addr: conn.LocalTCPAddr}
	tlsCalled := false
	wssCalled := false

	bridgeGetReservedTLSListener = func() (net.Listener, error) {
		return reservedListener, nil
	}
	bridgeGetTCPListener = func() (net.Listener, error) {
		t.Fatal("tcp listener should not be used when disabled")
		return nil, nil
	}
	bridgeGetTLSListener = func() (net.Listener, error) {
		tlsCalled = true
		return nil, nil
	}
	bridgeGetWSListener = func() (net.Listener, error) {
		t.Fatal("ws listener should not be used when disabled")
		return nil, nil
	}
	bridgeGetWSSListener = func() (net.Listener, error) {
		wssCalled = true
		return nil, nil
	}

	bootstrap, err := bootstrapBridgeListeners()
	if err != nil {
		t.Fatalf("bootstrapBridgeListeners() error = %v", err)
	}
	defer bootstrap.close()

	if !bootstrap.useReservedTLSGateway {
		t.Fatal("bootstrap should mark reserved TLS gateway as active")
	}
	if bootstrap.reservedTLSListener != reservedListener {
		t.Fatalf("reserved TLS listener = %v, want %v", bootstrap.reservedTLSListener, reservedListener)
	}
	if tlsCalled {
		t.Fatal("dedicated TLS listener should be skipped when reserved TLS gateway is active")
	}
	if wssCalled {
		t.Fatal("dedicated WSS listener should be skipped when reserved TLS gateway is active")
	}
}

func TestHandleReservedTLSConnRoutesBridgeTLS(t *testing.T) {
	crypt.InitTls(tls.Certificate{})
	b := NewTunnel(false, nil, 0)
	b.VirtualTlsListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	b.VirtualWssListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	t.Cleanup(func() {
		_ = b.VirtualTlsListener.Close()
		_ = b.VirtualWssListener.Close()
	})

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go b.handleReservedTLSConn(serverConn)

	clientTLS := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true})
	defer func() { _ = clientTLS.Close() }()
	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("TLS handshake failed: %v", err)
	}
	if _, err := clientTLS.Write([]byte(common.CONN_TEST)); err != nil {
		t.Fatalf("TLS write failed: %v", err)
	}

	accepted, err := b.VirtualTlsListener.Accept()
	if err != nil {
		t.Fatalf("VirtualTlsListener.Accept() error = %v", err)
	}
	defer func() { _ = accepted.Close() }()

	buf := make([]byte, len(common.CONN_TEST))
	if _, err := io.ReadFull(accepted, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != common.CONN_TEST {
		t.Fatalf("gateway routed payload %q, want %q", string(buf), common.CONN_TEST)
	}
}

func TestHandleReservedTLSConnRoutesBridgeWSS(t *testing.T) {
	crypt.InitTls(tls.Certificate{})
	b := NewTunnel(false, nil, 0)
	b.VirtualTlsListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	b.VirtualWssListener = conn.NewVirtualListener(conn.LocalTCPAddr)
	t.Cleanup(func() {
		_ = b.VirtualTlsListener.Close()
		_ = b.VirtualWssListener.Close()
	})
	originalConfig := connection.CurrentConfig()
	updatedConfig := originalConfig
	updatedConfig.BridgePath = "/wss"
	connection.ApplySnapshot(updatedConfig.Snapshot())
	t.Cleanup(func() {
		connection.ApplySnapshot(originalConfig.Snapshot())
	})

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	go b.handleReservedTLSConn(serverConn)

	clientTLS := tls.Client(clientConn, &tls.Config{InsecureSkipVerify: true})
	defer func() { _ = clientTLS.Close() }()
	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("TLS handshake failed: %v", err)
	}
	request := "GET /wss HTTP/1.1\r\nHost: bridge.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"
	if _, err := clientTLS.Write([]byte(request)); err != nil {
		t.Fatalf("TLS write failed: %v", err)
	}

	errCh := make(chan error, 1)
	connCh := make(chan net.Conn, 1)
	go func() {
		c, err := b.VirtualWssListener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		connCh <- c
	}()

	select {
	case err := <-errCh:
		t.Fatalf("VirtualWssListener.Accept() error = %v", err)
	case accepted := <-connCh:
		defer func() { _ = accepted.Close() }()
		buf := make([]byte, 3)
		if _, err := io.ReadFull(accepted, buf); err != nil {
			t.Fatalf("ReadFull() error = %v", err)
		}
		if string(buf) != "GET" {
			t.Fatalf("gateway routed payload prefix %q, want GET", string(buf))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WSS gateway routing")
	}
}

func TestBridgeWebsocketGatewayRequestUsesCurrentListenerRuntimeRoot(t *testing.T) {
	oldRuntimeRoot := currentBridgeListenerRuntimeRoot
	defer func() { currentBridgeListenerRuntimeRoot = oldRuntimeRoot }()

	req := &http.Request{
		URL: &url.URL{Path: "/bridge-wss"},
		Header: http.Header{
			"Upgrade":    []string{"websocket"},
			"Connection": []string{"keep-alive, Upgrade"},
		},
	}

	currentBridgeListenerRuntimeRoot = func() connection.BridgeRuntimeConfig {
		return connection.BridgeRuntimeConfig{Path: "/bridge-wss"}
	}
	if !isBridgeGatewayWebsocketRequest(req) {
		t.Fatal("websocket request should match current bridge listener runtime path")
	}

	currentBridgeListenerRuntimeRoot = func() connection.BridgeRuntimeConfig {
		return connection.BridgeRuntimeConfig{Path: "/other"}
	}
	if isBridgeGatewayWebsocketRequest(req) {
		t.Fatal("websocket request should track updated bridge listener runtime path")
	}
}

func TestSetClientSelectMode(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    SelectMode
		wantErr bool
	}{
		{name: "enum", in: RoundRobin, want: RoundRobin},
		{name: "int", in: 2, want: Random},
		{name: "string alias", in: "rr", want: RoundRobin},
		{name: "string number", in: "0", want: Primary},
		{name: "invalid string", in: "bad", want: Primary, wantErr: true},
		{name: "out of range", in: 10, want: Primary, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SetClientSelectMode(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetClientSelectMode(%v) err=%v, wantErr=%v", tt.in, err, tt.wantErr)
			}
			if ClientSelectMode != tt.want {
				t.Fatalf("SetClientSelectMode(%v) mode=%v, want=%v", tt.in, ClientSelectMode, tt.want)
			}
		})
	}
}

func TestClientGetNodeByFileRespectsGraceThenPrunesOfflineNode(t *testing.T) {
	node := NewNode("n1", "", 6)
	client := NewClient(1, node)

	if err := client.AddFile("file-key", "n1"); err != nil {
		t.Fatalf("AddFile returned error: %v", err)
	}

	if got, ok := client.GetNodeByFile("file-key"); ok || got != nil {
		t.Fatalf("expected no node during grace window, got ok=%v node=%v", ok, got)
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("node should be kept during grace window, count=%d", count)
	}

	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
	if got, ok := client.GetNodeByFile("file-key"); ok || got != nil {
		t.Fatalf("expected no node after grace window, got ok=%v node=%v", ok, got)
	}
	if count := client.NodeCount(); count != 0 {
		t.Fatalf("offline node should be pruned after grace window, count=%d", count)
	}
}

func TestClientGetNodeByFileDoesNotHonorJoinGraceWithoutClientGrace(t *testing.T) {
	node := NewNode("n-file-join-grace", "", 6)
	client := NewClient(1, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
	node.joinNano = time.Now().UnixNano()

	if err := client.AddFile("file-join-grace", "n-file-join-grace"); err != nil {
		t.Fatalf("AddFile returned error: %v", err)
	}

	if got, ok := client.GetNodeByFile("file-join-grace"); ok || got != nil {
		t.Fatalf("GetNodeByFile(file-join-grace) = %#v, %v, want nil, false after pruning offline node", got, ok)
	}
	if count := client.NodeCount(); count != 0 {
		t.Fatalf("NodeCount() = %d, want 0 after file route prunes offline node without client grace", count)
	}
}

func TestClientGetNodeByFileReturnsOnlineNodeWithoutExternalDependency(t *testing.T) {
	node := NewNode("n2", "", 6)
	client := NewClient(-1, node)

	if err := client.AddFile("f2", "n2"); err != nil {
		t.Fatalf("AddFile returned error: %v", err)
	}
	got, ok := client.GetNodeByFile("f2")
	if !ok || got != node {
		t.Fatalf("expected online node by file mapping, ok=%v got=%v", ok, got)
	}
}

func TestClientGetNodeByFileFallsBackToOtherOwnerInPool(t *testing.T) {
	node1 := NewNode("n-file-pool-1", "", 6)
	client := NewClient(-1, node1)
	node2 := NewNode("n-file-pool-2", "", 6)
	client.AddNode(node2)

	if err := client.AddFile("file-pool", "n-file-pool-1"); err != nil {
		t.Fatalf("AddFile(first owner) returned error: %v", err)
	}
	if err := client.AddFile("file-pool", "n-file-pool-2"); err != nil {
		t.Fatalf("AddFile(second owner) returned error: %v", err)
	}
	client.nodes.Delete("n-file-pool-1")

	got, ok := client.GetNodeByFile("file-pool")
	if !ok || got != node2 {
		t.Fatalf("GetNodeByFile(file-pool) = %#v, %v, want second owner after first owner disappears", got, ok)
	}

	client.RemoveFileOwner("file-pool", "n-file-pool-2")
	if got, ok := client.GetNodeByFile("file-pool"); ok || got != nil {
		t.Fatalf("GetNodeByFile(file-pool) after removing remaining owner = %#v, %v, want nil, false", got, ok)
	}
}

func TestRuntimeOwnerPoolSelectNextRotatesRoundRobin(t *testing.T) {
	pool := newRuntimeOwnerPool[int]()
	pool.set("owner-a", 1)
	pool.set("owner-b", 2)
	pool.set("owner-c", 3)

	for _, want := range []struct {
		uuid  string
		value int
	}{
		{uuid: "owner-a", value: 1},
		{uuid: "owner-b", value: 2},
		{uuid: "owner-c", value: 3},
		{uuid: "owner-a", value: 1},
	} {
		gotUUID, gotValue, ok := pool.selectNext()
		if !ok {
			t.Fatal("selectNext() = false, want true")
		}
		if gotUUID != want.uuid || gotValue != want.value {
			t.Fatalf("selectNext() = %q/%d, want %q/%d", gotUUID, gotValue, want.uuid, want.value)
		}
	}
}

func TestRuntimeOwnerPoolSelectNextWithCountReturnsCurrentCandidateAndPoolSize(t *testing.T) {
	pool := newRuntimeOwnerPool[int]()
	pool.set("owner-a", 1)
	pool.set("owner-b", 2)
	pool.set("owner-c", 3)

	gotUUID, gotValue, count, ok := pool.selectNextWithCount()
	if !ok {
		t.Fatal("selectNextWithCount() = false, want true")
	}
	if count != 3 {
		t.Fatalf("selectNextWithCount() count = %d, want 3", count)
	}
	if gotUUID != "owner-a" || gotValue != 1 {
		t.Fatalf("selectNextWithCount() = %q/%d, want owner-a/1", gotUUID, gotValue)
	}

	gotUUID, gotValue, ok = pool.selectNext()
	if !ok || gotUUID != "owner-b" || gotValue != 2 {
		t.Fatalf("selectNext() after selectNextWithCount() = %q/%d/%v, want owner-b/2/true", gotUUID, gotValue, ok)
	}
}

func TestClientGetNodeByFileDropsNilOwnerPoolMapping(t *testing.T) {
	node := NewNode("n-file-nil-pool", "", 6)
	client := NewClient(16, node)
	client.files.Store("file-nil-pool", (*runtimeOwnerPool[string])(nil))

	if got, ok := client.GetNodeByFile("file-nil-pool"); ok || got != nil {
		t.Fatalf("GetNodeByFile(file-nil-pool) = %#v, %v, want nil, false", got, ok)
	}
	if _, ok := client.files.Load("file-nil-pool"); ok {
		t.Fatal("nil owner pool mapping should be removed")
	}
}

func TestRuntimeOwnerPoolRemoveKeepsNextAligned(t *testing.T) {
	pool := newRuntimeOwnerPool[string]()
	pool.set("owner-a", "node-a")
	pool.set("owner-b", "node-b")
	pool.set("owner-c", "node-c")

	if _, _, ok := pool.selectNext(); !ok {
		t.Fatal("first selectNext() = false, want true")
	}
	if _, _, ok := pool.selectNext(); !ok {
		t.Fatal("second selectNext() = false, want true")
	}

	if remaining, removed := pool.remove("owner-b"); !removed || remaining != 2 {
		t.Fatalf("remove(owner-b) = %d, %v, want 2, true", remaining, removed)
	}
	if pool.has("owner-b") {
		t.Fatal("has(owner-b) = true, want false after removal")
	}
	if got := pool.count(); got != 2 {
		t.Fatalf("count() = %d, want 2", got)
	}

	gotUUID, gotValue, ok := pool.selectNext()
	if !ok || gotUUID != "owner-c" || gotValue != "node-c" {
		t.Fatalf("selectNext() after removing owner-b = %q/%q/%v, want owner-c/node-c/true", gotUUID, gotValue, ok)
	}
	gotUUID, gotValue, ok = pool.selectNext()
	if !ok || gotUUID != "owner-a" || gotValue != "node-a" {
		t.Fatalf("selectNext() after wrapping = %q/%q/%v, want owner-a/node-a/true", gotUUID, gotValue, ok)
	}

	if remaining, removed := pool.remove("owner-a"); !removed || remaining != 1 {
		t.Fatalf("remove(owner-a) = %d, %v, want 1, true", remaining, removed)
	}
	gotUUID, gotValue, ok = pool.selectNext()
	if !ok || gotUUID != "owner-c" || gotValue != "node-c" {
		t.Fatalf("selectNext() with only owner-c left = %q/%q/%v, want owner-c/node-c/true", gotUUID, gotValue, ok)
	}

	if remaining, removed := pool.remove("owner-c"); !removed || remaining != 0 {
		t.Fatalf("remove(owner-c) = %d, %v, want 0, true", remaining, removed)
	}
	if _, _, ok := pool.selectNext(); ok {
		t.Fatal("selectNext() on empty pool = true, want false")
	}
}

func TestNextFileRouteUUIDUsesOwnerKeyFromPool(t *testing.T) {
	pool := newRuntimeOwnerPool[string]()
	pool.set("owner-a", "node-a")
	pool.set("owner-b", "node-b")

	got, ok := nextFileRouteUUID(pool)
	if !ok || got != "owner-a" {
		t.Fatalf("nextFileRouteUUID(pool) = %q, %v, want owner-a, true", got, ok)
	}
	got, ok = nextFileRouteUUID(pool)
	if !ok || got != "owner-b" {
		t.Fatalf("nextFileRouteUUID(pool) second call = %q, %v, want owner-b, true", got, ok)
	}
}

func TestRemoveNodeLockedOnlyPrunesMatchingOwnerFromFilePool(t *testing.T) {
	node1 := NewNode("n-file-prune-1", "", 6)
	client := NewClient(-1, node1)
	node2 := NewNode("n-file-prune-2", "", 6)
	client.AddNode(node2)

	if err := client.AddFile("file-prune", "n-file-prune-1"); err != nil {
		t.Fatalf("AddFile(first owner) returned error: %v", err)
	}
	if err := client.AddFile("file-prune", "n-file-prune-2"); err != nil {
		t.Fatalf("AddFile(second owner) returned error: %v", err)
	}

	client.mu.Lock()
	client.removeNodeLocked("n-file-prune-1")
	client.mu.Unlock()

	got, ok := client.GetNodeByFile("file-prune")
	if !ok || got != node2 {
		t.Fatalf("GetNodeByFile(file-prune) = %#v, %v, want surviving owner after pruning sibling", got, ok)
	}

	client.RemoveFileOwner("file-prune", "n-file-prune-2")
	if got, ok := client.GetNodeByFile("file-prune"); ok || got != nil {
		t.Fatalf("GetNodeByFile(file-prune) after removing surviving owner = %#v, %v, want nil, false", got, ok)
	}
}

func TestRemoveOfflineNodesRetriesBeforeRemoval(t *testing.T) {
	node := NewNode("n3", "", 6)
	client := NewClient(2, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
	node.joinNano = time.Now().Add(-10 * time.Second).UnixNano()

	for i := 0; i < retryTimeMax; i++ {
		if removed := client.RemoveOfflineNodes(true); removed != 0 {
			t.Fatalf("attempt %d removed=%d, want 0 before retry limit", i+1, removed)
		}
	}

	if removed := client.RemoveOfflineNodes(true); removed != 1 {
		t.Fatalf("expected node removal after retries exhausted, removed=%d", removed)
	}
	if count := client.NodeCount(); count != 0 {
		t.Fatalf("expected no nodes left, count=%d", count)
	}
}

func TestRemoveOfflineNodesRespectsClientGraceUnlessIgnored(t *testing.T) {
	node := NewNode("n3-client-grace", "", 6)
	client := NewClient(22, node)
	node.joinNano = time.Now().Add(-10 * time.Second).UnixNano()

	if removed := client.RemoveOfflineNodes(false); removed != 0 {
		t.Fatalf("RemoveOfflineNodes(false) removed=%d, want 0 during client grace", removed)
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("NodeCount() = %d, want 1 while client grace is active", count)
	}

	for i := 0; i < retryTimeMax; i++ {
		if removed := client.RemoveOfflineNodes(true); removed != 0 {
			t.Fatalf("RemoveOfflineNodes(true) attempt %d removed=%d, want retry budget to keep node", i+1, removed)
		}
	}
	if removed := client.RemoveOfflineNodes(true); removed != 1 {
		t.Fatalf("RemoveOfflineNodes(true) removed=%d, want 1 after retry budget is exhausted", removed)
	}
}

func TestRemoveOfflineNodesKeepsJoinGraceOfflineNode(t *testing.T) {
	node := NewNode("n3-join-grace", "", 6)
	client := NewClient(23, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
	node.joinNano = time.Now().UnixNano()

	if removed := client.RemoveOfflineNodes(true); removed != 0 {
		t.Fatalf("RemoveOfflineNodes(true) removed=%d, want 0 for join-grace node", removed)
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("NodeCount() = %d, want 1 while join grace is active", count)
	}
}

func TestDetachedNodeStateChecksDoNotPanic(t *testing.T) {
	node := NewNode("detached", "", 6)
	if node.IsOnline() {
		t.Fatal("detached node should not be online")
	}
	if !node.IsOffline() {
		t.Fatal("detached node should be offline")
	}
	if !node.IsTunnelClosed() {
		t.Fatal("detached node should report closed tunnel")
	}
}

func TestNilNodeStateChecksAreSafe(t *testing.T) {
	var node *Node
	if node.IsOnline() {
		t.Fatal("nil node should not be online")
	}
	if !node.IsOffline() {
		t.Fatal("nil node should be offline")
	}
	if !node.IsTunnelClosed() {
		t.Fatal("nil node should report closed tunnel")
	}
}

func TestCloseAndRemoveNodeIfCurrentDoesNotRemoveReplacementNode(t *testing.T) {
	oldNode := NewNode("n4", "", 6)
	client := NewClient(4, oldNode)

	replacement := NewNode("n4", "", 6)
	replacement.Client = client
	client.nodes.Store("n4", replacement)

	if removed := client.closeAndRemoveNodeIfCurrent("n4", oldNode); removed {
		t.Fatal("closeAndRemoveNodeIfCurrent should not remove a replacement node")
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("replacement node should remain installed, count=%d", count)
	}
	got, ok := client.GetNodeByUUID("n4")
	if !ok || got != replacement {
		t.Fatalf("replacement node should remain installed, ok=%v got=%v", ok, got)
	}
}

func TestCloseAndRemoveNodeIfCurrentSkipsNodeThatRecoveredBeforeCleanup(t *testing.T) {
	node := NewNode("n5", "", 6)
	client := NewClient(5, node)
	client.Id = -1

	if removed := client.closeAndRemoveNodeIfCurrent("n5", node); removed {
		t.Fatal("closeAndRemoveNodeIfCurrent should not remove a recovered node")
	}
	if count := client.NodeCount(); count != 1 {
		t.Fatalf("recovered node should remain installed, count=%d", count)
	}
	got, ok := client.GetNodeByUUID("n5")
	if !ok || got != node {
		t.Fatalf("recovered node should remain installed, ok=%v got=%v", ok, got)
	}
}

func TestCloseAndRemoveNodeIfCurrentReleasesClientLockBeforeNodeClose(t *testing.T) {
	blockingConn := newBlockingCloseConn()
	node := NewNode("n-close-block", "", 6)
	node.AddSignal(conn.NewConn(blockingConn))
	client := NewClient(18, node)

	removeDone := make(chan bool, 1)
	go func() {
		removeDone <- client.closeAndRemoveNodeIfCurrent("n-close-block", node)
	}()

	select {
	case <-blockingConn.closeStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("node close did not start")
	}

	replacement := NewNode("n-close-block", "", 6)
	addDone := make(chan struct{})
	go func() {
		client.AddNode(replacement)
		close(addDone)
	}()

	select {
	case <-addDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("AddNode() blocked while previous node Close() was in progress")
	}

	close(blockingConn.releaseClose)

	select {
	case removed := <-removeDone:
		if !removed {
			t.Fatal("closeAndRemoveNodeIfCurrent() = false, want true")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("closeAndRemoveNodeIfCurrent() did not finish after releasing blocked Close()")
	}

	got, ok := client.GetNodeByUUID("n-close-block")
	if !ok || got != replacement {
		t.Fatalf("GetNodeByUUID() = %#v, %v, want replacement node after blocked close finishes", got, ok)
	}
}

func TestClientCloseDetachesNodesClearsMappingsAndClosesSignals(t *testing.T) {
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })

	node := NewNode("n-close", "", 6)
	node.AddSignal(conn.NewConn(serverConn))
	client := NewClient(15, node)
	if err := client.AddFile("file-close", "n-close"); err != nil {
		t.Fatalf("AddFile() error = %v", err)
	}
	client.nodes.Store("bad-node", "bad-node-entry")
	client.nodeList.Add("bad-node")

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !client.IsClosed() {
		t.Fatal("client should report closed after Close()")
	}
	if client.NodeCount() != 0 {
		t.Fatalf("NodeCount() = %d, want 0 after Close()", client.NodeCount())
	}
	if _, ok := client.files.Load("file-close"); ok {
		t.Fatal("Close() should clear file mappings")
	}
	if !node.IsOffline() {
		t.Fatal("Close() should close node signal and leave node offline")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
}

func TestClientCheckNodePrunesMissingCurrentUUID(t *testing.T) {
	node := NewNode("n6", "", 6)
	client := NewClient(6, node)
	client.nodes.Delete("n6")

	done := make(chan *Node, 1)
	go func() {
		done <- client.CheckNode()
	}()

	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("CheckNode() = %#v, want nil after pruning missing uuid", got)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("CheckNode should not loop forever when LastUUID is missing from nodes map")
	}

	if client.NodeCount() != 0 {
		t.Fatalf("nodeList should be pruned after missing uuid cleanup, count=%d", client.NodeCount())
	}
	if client.LastUUID != "" {
		t.Fatalf("LastUUID = %q, want empty after pruning missing uuid", client.LastUUID)
	}
}

func TestClientCheckNodeDefersJoinGraceThenFallsBackToBackup(t *testing.T) {
	joining := NewNode("n-join-grace", "", 6)
	backup := NewNode("n-backup", "", 6)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	backup.BaseVer = 4
	backup.AddSignal(conn.NewConn(serverConn))
	client := NewClient(7, joining)
	client.AddNode(backup)
	client.setCurrentNodeUUID("n-join-grace")
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-10*time.Second).UnixNano())
	joining.joinNano = time.Now().UnixNano()

	got := client.CheckNode()
	if got != backup {
		t.Fatalf("CheckNode() = %#v, want backup node after deferring join-grace candidate", got)
	}
	if count := client.NodeCount(); count != 2 {
		t.Fatalf("NodeCount() = %d, want 2 because join-grace node should be kept", count)
	}
	if client.LastUUID != "n-backup" {
		t.Fatalf("LastUUID = %q, want backup node after fallback", client.LastUUID)
	}
}

func TestClientCheckNodeDefersClientGraceThenFallsBackToBackup(t *testing.T) {
	current := NewNode("n-client-grace", "", 6)
	backup := NewNode("n-client-grace-backup", "", 6)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	backup.BaseVer = 4
	backup.AddSignal(conn.NewConn(serverConn))
	client := NewClient(8, current)
	client.AddNode(backup)
	client.setCurrentNodeUUID("n-client-grace")
	current.joinNano = time.Now().Add(-10 * time.Second).UnixNano()

	got := client.CheckNode()
	if got != backup {
		t.Fatalf("CheckNode() = %#v, want backup node during client grace fallback", got)
	}
	if count := client.NodeCount(); count != 2 {
		t.Fatalf("NodeCount() = %d, want 2 because current node should be kept during client grace", count)
	}
	if client.LastUUID != "n-client-grace-backup" {
		t.Fatalf("LastUUID = %q, want backup node after client grace fallback", client.LastUUID)
	}
}

func TestClientCheckNodeReturnsCurrentReadyNode(t *testing.T) {
	current := NewNode("n-current-ready", "", 6)
	current.BaseVer = 4
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	current.AddSignal(conn.NewConn(serverConn))
	client := NewClient(9, current)
	backup := NewNode("n-current-ready-backup", "", 6)
	backup.BaseVer = 4
	backupServer, backupPeer := net.Pipe()
	t.Cleanup(func() { _ = backupServer.Close() })
	t.Cleanup(func() { _ = backupPeer.Close() })
	backup.AddSignal(conn.NewConn(backupServer))
	client.AddNode(backup)
	client.setCurrentNodeUUID("n-current-ready")

	got := client.CheckNode()
	if got != current {
		t.Fatalf("CheckNode() = %#v, want current ready node", got)
	}
	if client.LastUUID != "n-current-ready" {
		t.Fatalf("LastUUID = %q, want n-current-ready", client.LastUUID)
	}
}

func TestClientCurrentNodeIfOnlineReturnsCurrentReadyNode(t *testing.T) {
	current := NewNode("route-current", "", 6)
	client := NewClient(-1, current)
	backup := NewNode("route-backup", "", 6)
	client.AddNode(backup)
	client.setCurrentNodeUUID("route-backup")

	got := client.currentNodeIfOnline()
	if got != backup {
		t.Fatalf("currentNodeIfOnline() = %#v, want current backup node", got)
	}
	if client.LastUUID != "route-backup" {
		t.Fatalf("LastUUID = %q, want route-backup", client.LastUUID)
	}
}

func TestBridgeSelectClientRouteUUIDPrimaryFallsBackWhenCurrentNodeMissing(t *testing.T) {
	oldMode := ClientSelectMode
	ClientSelectMode = Primary
	defer func() {
		ClientSelectMode = oldMode
	}()

	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	current := NewNode("route-current", "", 6)
	client := NewClient(-1, current)
	backup := NewNode("route-backup", "", 6)
	client.AddNode(backup)
	client.setCurrentNodeUUID("route-current")
	client.nodes.Delete("route-current")
	runtimeBridge.Client.Store(77, client)

	if got := runtimeBridge.SelectClientRouteUUID(77); got != "route-backup" {
		t.Fatalf("SelectClientRouteUUID() = %q, want route-backup after current node goes missing", got)
	}
	if client.LastUUID != "route-backup" {
		t.Fatalf("LastUUID = %q, want route-backup after fallback", client.LastUUID)
	}
}

func TestClientGetNodeByFileDropsInvalidMapping(t *testing.T) {
	node := NewNode("n7", "", 6)
	client := NewClient(7, node)
	client.files.Store("bad-file", 123)

	if got, ok := client.GetNodeByFile("bad-file"); ok || got != nil {
		t.Fatalf("GetNodeByFile(bad-file) = %#v, %v, want nil, false", got, ok)
	}
	if _, ok := client.files.Load("bad-file"); ok {
		t.Fatal("invalid file mapping should be removed")
	}
}

func TestClientGetNodeByFileDropsMissingNodeMapping(t *testing.T) {
	node := NewNode("n8", "", 6)
	client := NewClient(8, node)
	if err := client.AddFile("file-key", "n8"); err != nil {
		t.Fatalf("AddFile returned error: %v", err)
	}
	client.nodes.Delete("n8")

	if got, ok := client.GetNodeByFile("file-key"); ok || got != nil {
		t.Fatalf("GetNodeByFile(file-key) = %#v, %v, want nil, false", got, ok)
	}
	if _, ok := client.files.Load("file-key"); ok {
		t.Fatal("stale file mapping should be removed when target node is missing")
	}
	if client.NodeCount() != 0 {
		t.Fatalf("missing node uuid should be pruned from nodeList, count=%d", client.NodeCount())
	}
}

func TestClientAddNodeReplacesInvalidExistingEntry(t *testing.T) {
	node := NewNode("n-invalid", "", 6)
	client := NewClient(9, node)
	client.nodes.Store("n-invalid", "bad-node-entry")

	replacement := NewNode("n-invalid", "replacement", 6)
	client.AddNode(replacement)

	got, ok := client.GetNodeByUUID("n-invalid")
	if !ok || got != replacement {
		t.Fatalf("GetNodeByUUID(n-invalid) = %#v, %v, want replacement node", got, ok)
	}
	if client.NodeCount() != 1 {
		t.Fatalf("NodeCount() = %d, want 1 after replacing invalid node entry", client.NodeCount())
	}
}

func TestClientAddNodeReplacementKeepsFileMappingForSameUUID(t *testing.T) {
	node := NewNode("n-invalid-keep-file", "", 6)
	client := NewClient(-1, node)
	if err := client.AddFile("file-keep", "n-invalid-keep-file"); err != nil {
		t.Fatalf("AddFile() error = %v", err)
	}
	client.nodes.Store("n-invalid-keep-file", "bad-node-entry")

	replacement := NewNode("n-invalid-keep-file", "replacement", 6)
	client.AddNode(replacement)

	got, ok := client.GetNodeByFile("file-keep")
	if !ok || got != replacement {
		t.Fatalf("GetNodeByFile(file-keep) = %#v, %v, want replacement node mapping preserved", got, ok)
	}
}

func TestClientAddFileRejectsInvalidNodeEntry(t *testing.T) {
	node := NewNode("n-file-invalid", "", 6)
	client := NewClient(10, node)
	client.nodes.Store("n-file-invalid", "bad-node-entry")

	if err := client.AddFile("file-invalid", "n-file-invalid"); err == nil {
		t.Fatal("AddFile() error = nil, want invalid node entry rejection")
	}
	if _, ok := client.files.Load("file-invalid"); ok {
		t.Fatal("file mapping should not be created when node entry is invalid")
	}
	if client.NodeCount() != 0 {
		t.Fatalf("NodeCount() = %d, want 0 after invalid node cleanup", client.NodeCount())
	}
}

func TestClientAddFileOwnerKeepsSingleOwnerWithoutPromotingPool(t *testing.T) {
	node := NewNode("n-file-single", "", 6)
	client := NewClient(-1, node)

	added, err := client.AddFileOwner("file-single", "n-file-single")
	if err != nil || !added {
		t.Fatalf("AddFileOwner(first) = %v, %v, want true, nil", added, err)
	}
	added, err = client.AddFileOwner("file-single", "n-file-single")
	if err != nil || added {
		t.Fatalf("AddFileOwner(duplicate) = %v, %v, want false, nil", added, err)
	}

	value, ok := client.files.Load("file-single")
	if !ok {
		t.Fatal("file-single mapping missing after duplicate add")
	}
	if owner, ok := value.(string); !ok || owner != "n-file-single" {
		t.Fatalf("file-single mapping = %#v, want single owner string", value)
	}
}

func TestClientAddFileOwnerReplacesInvalidMapping(t *testing.T) {
	node := NewNode("n-file-replace", "", 6)
	client := NewClient(-1, node)
	client.files.Store("file-replace", 123)

	added, err := client.AddFileOwner("file-replace", "n-file-replace")
	if err != nil || !added {
		t.Fatalf("AddFileOwner(replace invalid) = %v, %v, want true, nil", added, err)
	}
	got, ok := client.GetNodeByFile("file-replace")
	if !ok || got != node {
		t.Fatalf("GetNodeByFile(file-replace) = %#v, %v, want replacement owner node", got, ok)
	}
}

func TestClientRemoveFileUpdatesOwnerIndex(t *testing.T) {
	ownerA := NewNode("n-file-remove-a", "", 6)
	client := NewClient(-1, ownerA)
	ownerB := NewNode("n-file-remove-b", "", 6)
	client.AddNode(ownerB)

	if _, err := client.AddFileOwner("file-remove", "n-file-remove-a"); err != nil {
		t.Fatalf("AddFileOwner(file-remove,a) error = %v", err)
	}
	if _, err := client.AddFileOwner("file-remove", "n-file-remove-b"); err != nil {
		t.Fatalf("AddFileOwner(file-remove,b) error = %v", err)
	}

	client.RemoveFile("file-remove")

	if _, ok := client.files.Load("file-remove"); ok {
		t.Fatal("RemoveFile() should delete file mapping")
	}
	client.mu.RLock()
	_, hasA := client.fileOwnerKeys["n-file-remove-a"]
	_, hasB := client.fileOwnerKeys["n-file-remove-b"]
	client.mu.RUnlock()
	if hasA || hasB {
		t.Fatalf("RemoveFile() should clear owner index, got ownerA=%v ownerB=%v", hasA, hasB)
	}
}

func TestClientPruneFileMappingsTargetsIndexedKeys(t *testing.T) {
	ownerA := NewNode("n-file-prune-index-a", "", 6)
	client := NewClient(-1, ownerA)
	ownerB := NewNode("n-file-prune-index-b", "", 6)
	client.AddNode(ownerB)

	if _, err := client.AddFileOwner("file-shared-index", "n-file-prune-index-a"); err != nil {
		t.Fatalf("AddFileOwner(file-shared-index,a) error = %v", err)
	}
	if _, err := client.AddFileOwner("file-shared-index", "n-file-prune-index-b"); err != nil {
		t.Fatalf("AddFileOwner(file-shared-index,b) error = %v", err)
	}
	if _, err := client.AddFileOwner("file-solo-index-a", "n-file-prune-index-a"); err != nil {
		t.Fatalf("AddFileOwner(file-solo-index-a,a) error = %v", err)
	}
	if _, err := client.AddFileOwner("file-solo-index-b", "n-file-prune-index-b"); err != nil {
		t.Fatalf("AddFileOwner(file-solo-index-b,b) error = %v", err)
	}

	client.pruneFileMappings("n-file-prune-index-a")

	if _, ok := client.files.Load("file-solo-index-a"); ok {
		t.Fatal("pruneFileMappings() should remove file owned only by pruned node")
	}
	if got, ok := client.GetNodeByFile("file-shared-index"); !ok || got != ownerB {
		t.Fatalf("GetNodeByFile(file-shared-index) = %#v, %v, want surviving ownerB", got, ok)
	}
	if got, ok := client.GetNodeByFile("file-solo-index-b"); !ok || got != ownerB {
		t.Fatalf("GetNodeByFile(file-solo-index-b) = %#v, %v, want unaffected ownerB mapping", got, ok)
	}
	client.mu.RLock()
	_, hasA := client.fileOwnerKeys["n-file-prune-index-a"]
	keysB := len(client.fileOwnerKeys["n-file-prune-index-b"])
	client.mu.RUnlock()
	if hasA {
		t.Fatal("pruneFileMappings() should drop removed owner from reverse index")
	}
	if keysB != 2 {
		t.Fatalf("ownerB reverse index size = %d, want 2 for shared+solo mappings", keysB)
	}
}

func TestClientGetNodeByUUIDDropsInvalidNodeEntry(t *testing.T) {
	node := NewNode("n-lookup-invalid", "", 6)
	client := NewClient(10, node)
	client.nodes.Store("n-lookup-invalid", "bad-node-entry")

	got, ok := client.GetNodeByUUID("n-lookup-invalid")
	if ok || got != nil {
		t.Fatalf("GetNodeByUUID(n-lookup-invalid) = %#v, %v, want nil, false", got, ok)
	}
	if _, ok := client.nodes.Load("n-lookup-invalid"); ok {
		t.Fatal("invalid node entry should be removed during GetNodeByUUID")
	}
	if client.NodeCount() != 0 {
		t.Fatalf("NodeCount() = %d, want 0 after invalid lookup cleanup", client.NodeCount())
	}
}

func TestClientSnapshotNodesDropsInvalidNodeEntry(t *testing.T) {
	node := NewNode("n-snapshot", "", 6)
	client := NewClient(11, node)
	client.nodes.Store("bad-node", "bad-node-entry")
	client.nodeList.Add("bad-node")

	snapshots := client.SnapshotNodes()
	if len(snapshots) != 1 || snapshots[0].UUID != "n-snapshot" {
		t.Fatalf("SnapshotNodes() = %#v, want only the valid node snapshot", snapshots)
	}
	if _, ok := client.nodes.Load("bad-node"); ok {
		t.Fatal("invalid node entry should be removed during SnapshotNodes")
	}
	if client.NodeCount() != 1 {
		t.Fatalf("NodeCount() = %d, want 1 after dropping invalid node snapshot entry", client.NodeCount())
	}
}

func TestClientSnapshotNodesOrdersCurrentNodeBeforeOthersDeterministically(t *testing.T) {
	first := NewNode("node-a", "", 6)
	second := NewNode("node-b", "", 6)
	third := NewNode("node-c", "", 6)
	first.joinNano = 100
	second.joinNano = 300
	third.joinNano = 200

	client := NewClient(12, first)
	client.AddNode(second)
	client.AddNode(third)
	client.LastUUID = "node-a"

	snapshots := client.SnapshotNodes()
	if len(snapshots) != 3 {
		t.Fatalf("SnapshotNodes() len = %d, want 3", len(snapshots))
	}
	if snapshots[0].UUID != "node-a" {
		t.Fatalf("SnapshotNodes()[0] = %q, want current node-a first", snapshots[0].UUID)
	}
	if snapshots[1].UUID != "node-b" || snapshots[2].UUID != "node-c" {
		t.Fatalf("SnapshotNodes() order = [%q %q %q], want [node-a node-b node-c]", snapshots[0].UUID, snapshots[1].UUID, snapshots[2].UUID)
	}
}

func TestClientSnapshotNodesBreaksConnectedAtTiesByUUID(t *testing.T) {
	first := NewNode("node-c", "", 6)
	second := NewNode("node-a", "", 6)
	third := NewNode("node-b", "", 6)
	first.joinNano = 200
	second.joinNano = 200
	third.joinNano = 200

	client := NewClient(13, first)
	client.AddNode(second)
	client.AddNode(third)
	client.setCurrentNodeUUID("")

	snapshots := client.SnapshotNodes()
	if len(snapshots) != 3 {
		t.Fatalf("SnapshotNodes() len = %d, want 3", len(snapshots))
	}
	if snapshots[0].UUID != "node-a" || snapshots[1].UUID != "node-b" || snapshots[2].UUID != "node-c" {
		t.Fatalf("SnapshotNodes() order = [%q %q %q], want [node-a node-b node-c]", snapshots[0].UUID, snapshots[1].UUID, snapshots[2].UUID)
	}
}

func TestClientOnlineNodeSnapshotsExcludeOfflineNodesAndKeepCurrentFirst(t *testing.T) {
	currentServer, currentPeer := net.Pipe()
	backupServer, backupPeer := net.Pipe()
	t.Cleanup(func() { _ = currentServer.Close() })
	t.Cleanup(func() { _ = currentPeer.Close() })
	t.Cleanup(func() { _ = backupServer.Close() })
	t.Cleanup(func() { _ = backupPeer.Close() })

	current := NewNode("node-a", "1.2.3", 4)
	current.AddSignal(conn.NewConn(currentServer))
	current.joinNano = 100
	backup := NewNode("node-b", "1.2.4", 4)
	backup.AddSignal(conn.NewConn(backupServer))
	backup.joinNano = 200
	offline := NewNode("node-c", "1.2.5", 4)
	offline.joinNano = 300

	client := NewClient(14, current)
	client.AddNode(backup)
	client.AddNode(offline)
	client.setCurrentNodeUUID(current.UUID)

	snapshots := client.OnlineNodeSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("OnlineNodeSnapshots() len = %d, want 2", len(snapshots))
	}
	if snapshots[0].UUID != "node-a" || snapshots[1].UUID != "node-b" {
		t.Fatalf("OnlineNodeSnapshots() order = [%q %q], want [node-a node-b]", snapshots[0].UUID, snapshots[1].UUID)
	}
	for _, snapshot := range snapshots {
		if !snapshot.Online {
			t.Fatalf("OnlineNodeSnapshots() returned offline snapshot: %#v", snapshot)
		}
		if snapshot.UUID == "node-c" {
			t.Fatalf("OnlineNodeSnapshots() unexpectedly included offline node-c: %#v", snapshots)
		}
	}
}

func TestClientDisplayRuntimeSnapshotUsesLatestOnlineNode(t *testing.T) {
	primaryServer, primaryPeer := net.Pipe()
	latestServer, latestPeer := net.Pipe()
	t.Cleanup(func() { _ = primaryServer.Close() })
	t.Cleanup(func() { _ = primaryPeer.Close() })
	t.Cleanup(func() { _ = latestServer.Close() })
	t.Cleanup(func() { _ = latestPeer.Close() })

	primary := NewNode("node-a", "1.2.3", 4)
	primary.AddSignal(conn.NewConn(primaryServer))
	client := NewClient(25, primary)
	time.Sleep(2 * time.Millisecond)
	latest := NewNode("node-b", "1.2.4", 4)
	latest.AddSignal(conn.NewConn(latestServer))
	client.AddNode(latest)
	client.setCurrentNodeUUID(primary.UUID)

	got, ok := client.DisplayRuntimeSnapshot()
	if !ok {
		t.Fatal("DisplayRuntimeSnapshot() ok = false, want true")
	}
	if got.UUID != latest.UUID {
		t.Fatalf("DisplayRuntimeSnapshot().UUID = %q, want node-b", got.UUID)
	}
	if got.Version != "1.2.4" {
		t.Fatalf("DisplayRuntimeSnapshot().Version = %q, want 1.2.4", got.Version)
	}
}

func TestClientHasMultipleOnlineNodesIgnoresOfflineAndInvalidEntries(t *testing.T) {
	onlineA := NewNode("multi-a", "", 6)
	client := NewClient(-1, onlineA)
	offline := NewNode("multi-offline", "", 6)
	client.AddNode(offline)
	client.nodes.Store("bad-node", "bad-node-entry")
	client.nodeList.Add("bad-node")
	if !client.HasMultipleOnlineNodes() {
		t.Fatal("HasMultipleOnlineNodes() = false, want true with two online nodes after pruning invalid entries")
	}
	client.Id = 20
	if client.HasMultipleOnlineNodes() {
		t.Fatal("HasMultipleOnlineNodes() = true, want false after secondary node becomes offline")
	}
}

func TestClientOnlineNodeCountAndHasOnlineNodeIgnoreInvalidEntries(t *testing.T) {
	online := NewNode("online-a", "", 6)
	client := NewClient(-1, online)
	client.nodes.Store("bad-node", "bad-node-entry")
	client.nodeList.Add("bad-node")

	if count := client.OnlineNodeCount(); count != 1 {
		t.Fatalf("OnlineNodeCount() = %d, want 1 after pruning invalid entry", count)
	}
	if !client.HasOnlineNode() {
		t.Fatal("HasOnlineNode() = false, want true with one online node")
	}
	if _, ok := client.nodes.Load("bad-node"); ok {
		t.Fatal("invalid node entry should be removed during online count")
	}

	client.Id = 21
	if count := client.OnlineNodeCount(); count != 0 {
		t.Fatalf("OnlineNodeCount() = %d, want 0 after node becomes offline", count)
	}
	if client.HasOnlineNode() {
		t.Fatal("HasOnlineNode() = true, want false after node becomes offline")
	}
}

func TestClientHasOnlineNodeAndCountUseCurrentReadyFastPath(t *testing.T) {
	current := NewNode("online-fast", "", 6)
	current.BaseVer = 4
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	current.AddSignal(conn.NewConn(serverConn))
	client := NewClient(24, current)

	if !client.HasOnlineNode() {
		t.Fatal("HasOnlineNode() = false, want true for current ready node")
	}
	if count := client.OnlineNodeCount(); count != 1 {
		t.Fatalf("OnlineNodeCount() = %d, want 1 for single current ready node", count)
	}
}

func TestGetHealthFromClientDropsInvalidTaskAndHostEntries(t *testing.T) {
	resetBridgeConfigTestDB(t)

	dbClient := file.NewClient("bridge-health-runtime", false, false)
	dbClient.Id = 12
	dbClient.Flow = &file.Flow{}
	dbClient.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(dbClient); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &file.Tunnel{
		Mode:   "tcp",
		Port:   12010,
		Remark: "health-task",
		Status: true,
		Flow:   &file.Flow{},
		Client: dbClient,
		Target: &file.Target{TargetStr: "127.0.0.1:9300\n127.0.0.1:9301"},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	host := &file.Host{
		Host:   "health.example.com",
		Scheme: "all",
		Remark: "health-host",
		Client: dbClient,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:9300\n127.0.0.1:9302"},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	file.GetDb().JsonDb.Tasks.Store("bad-task", "bad-task-entry")
	file.GetDb().JsonDb.Hosts.Store("bad-host", "bad-host-entry")

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewTunnel(false, &sync.Map{}, 0).GetHealthFromClient(dbClient.Id, conn.NewConn(serverConn), nil, nil)
	}()

	peer := conn.NewConn(clientConn)
	if _, err := peer.SendHealthInfo("127.0.0.1:9300", common.GetStrByBool(false)); err != nil {
		t.Fatalf("SendHealthInfo() error = %v", err)
	}
	_ = peer.Close()
	<-done

	if _, ok := file.GetDb().JsonDb.Tasks.Load("bad-task"); ok {
		t.Fatal("invalid task entry should be removed during health processing")
	}
	if _, ok := file.GetDb().JsonDb.Hosts.Load("bad-host"); ok {
		t.Fatal("invalid host entry should be removed during health processing")
	}

	storedTask, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if common.IsArrContains(storedTask.Target.TargetArr, "127.0.0.1:9300") {
		t.Fatal("down task target should be removed from runtime target array")
	}
	if !common.IsArrContains(storedTask.HealthRemoveArr, "127.0.0.1:9300") {
		t.Fatal("down task target should be tracked in HealthRemoveArr")
	}

	storedHost, err := file.GetDb().GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if common.IsArrContains(storedHost.Target.TargetArr, "127.0.0.1:9300") {
		t.Fatal("down host target should be removed from runtime target array")
	}
	if !common.IsArrContains(storedHost.HealthRemoveArr, "127.0.0.1:9300") {
		t.Fatal("down host target should be tracked in HealthRemoveArr")
	}
}

func TestNodeSnapshotIncludesRuntimeStats(t *testing.T) {
	node := NewNode("stats-node", "1.0.0", 6)
	node.AddConn()
	node.ObserveBridgeTraffic(3, 5)
	node.ObserveServiceTraffic(7, 11)

	snapshot := node.Snapshot()
	if snapshot.NowConn != 1 {
		t.Fatalf("snapshot.NowConn = %d, want 1", snapshot.NowConn)
	}
	if snapshot.BridgeInBytes != 3 || snapshot.BridgeOutBytes != 5 || snapshot.BridgeTotalBytes != 8 {
		t.Fatalf("bridge bytes = %d/%d/%d, want 3/5/8", snapshot.BridgeInBytes, snapshot.BridgeOutBytes, snapshot.BridgeTotalBytes)
	}
	if snapshot.ServiceInBytes != 7 || snapshot.ServiceOutBytes != 11 || snapshot.ServiceTotalBytes != 18 {
		t.Fatalf("service bytes = %d/%d/%d, want 7/11/18", snapshot.ServiceInBytes, snapshot.ServiceOutBytes, snapshot.ServiceTotalBytes)
	}
	if snapshot.TotalInBytes != 10 || snapshot.TotalOutBytes != 16 || snapshot.TotalBytes != 26 {
		t.Fatalf("total bytes = %d/%d/%d, want 10/16/26", snapshot.TotalInBytes, snapshot.TotalOutBytes, snapshot.TotalBytes)
	}
	node.CutConn()
}

func TestNodeSnapshotMarksLegacySignalOnlyNodeOnline(t *testing.T) {
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = peerConn.Close()
	})

	node := NewNode("legacy-node", "0.26.0", 4)
	node.AddSignal(conn.NewConn(serverConn))
	client := NewClient(88, node)
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}

	snapshot := node.Snapshot()
	if !snapshot.HasSignal {
		t.Fatal("snapshot.HasSignal = false, want true")
	}
	if snapshot.HasTunnel {
		t.Fatal("snapshot.HasTunnel = true, want false")
	}
	if !snapshot.Online {
		t.Fatal("snapshot.Online = false, want true for legacy signal-only node")
	}
}
