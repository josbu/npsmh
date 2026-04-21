package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

func TestResolveSecretRouteFixedTargetIgnoresVisitorOverride(t *testing.T) {
	task := &file.Tunnel{
		TargetType: common.CONN_TCP,
		Target: &file.Target{
			TargetStr:  "10.0.0.1:22",
			LocalProxy: false,
		},
	}
	lk := conn.NewLink(common.CONN_UDP, "198.51.100.10:53", false, false, "", true)

	connType, host, localProxy, err := resolveSecretRoute(task, lk, true)
	if err != nil {
		t.Fatalf("resolveSecretRoute() error = %v", err)
	}
	if connType != common.CONN_TCP {
		t.Fatalf("connType = %q, want %q", connType, common.CONN_TCP)
	}
	if host != "10.0.0.1:22" {
		t.Fatalf("host = %q, want %q", host, "10.0.0.1:22")
	}
	if localProxy {
		t.Fatal("fixed secret target should not inherit visitor local-proxy override")
	}
}

func TestResolveSecretRouteFixedTargetAllowsUDPOnlyWhenConfiguredAll(t *testing.T) {
	task := &file.Tunnel{
		TargetType: common.CONN_ALL,
		Target: &file.Target{
			TargetStr: "10.0.0.2:53",
		},
	}
	lk := conn.NewLink(common.CONN_UDP, "198.51.100.10:53", false, false, "", false)

	connType, host, _, err := resolveSecretRoute(task, lk, false)
	if err != nil {
		t.Fatalf("resolveSecretRoute() error = %v", err)
	}
	if connType != common.CONN_UDP {
		t.Fatalf("connType = %q, want %q", connType, common.CONN_UDP)
	}
	if host != "10.0.0.2:53" {
		t.Fatalf("host = %q, want %q", host, "10.0.0.2:53")
	}
}

func TestResolveSecretRouteDynamicUsesVisitorTarget(t *testing.T) {
	task := &file.Tunnel{
		TargetType: common.CONN_TCP,
		Target:     &file.Target{},
	}
	lk := conn.NewLink(common.CONN_TCP, "api.internal.example:443", false, false, "", true)

	connType, host, localProxy, err := resolveSecretRoute(task, lk, false)
	if err != nil {
		t.Fatalf("resolveSecretRoute() error = %v", err)
	}
	if connType != common.CONN_TCP {
		t.Fatalf("connType = %q, want %q", connType, common.CONN_TCP)
	}
	if host != "api.internal.example:443" {
		t.Fatalf("host = %q, want %q", host, "api.internal.example:443")
	}
	if localProxy {
		t.Fatal("dynamic secret should gate server-local access behind allowSecretLocal")
	}

	_, _, localProxy, err = resolveSecretRoute(task, lk, true)
	if err != nil {
		t.Fatalf("resolveSecretRoute() with allowSecretLocal error = %v", err)
	}
	if !localProxy {
		t.Fatal("dynamic secret should allow server-local access when allowSecretLocal is enabled")
	}
}

func TestResolveSecretRouteDynamicRequiresVisitorTarget(t *testing.T) {
	task := &file.Tunnel{Target: &file.Target{}}

	if _, _, _, err := resolveSecretRoute(task, nil, false); err == nil {
		t.Fatal("resolveSecretRoute() should reject dynamic secret without link info")
	}

	lk := conn.NewLink(common.CONN_TCP, "", false, false, "", false)
	if _, _, _, err := resolveSecretRoute(task, lk, false); err == nil {
		t.Fatal("resolveSecretRoute() should reject dynamic secret without target host")
	}
}

type tunnelServerBridgeStub struct{}

func (tunnelServerBridgeStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, nil
}
func (tunnelServerBridgeStub) IsServer() bool                { return false }
func (tunnelServerBridgeStub) CliProcess(*conn.Conn, string) {}

type tunnelHTTPRouteBridgeStub struct {
	tunnelServerBridgeStub
	selectedRoute string
}

func (s tunnelHTTPRouteBridgeStub) SelectClientRouteUUID(int) string {
	return s.selectedRoute
}

type tunnelHTTPBackendConnBridgeStub struct {
	tunnelServerBridgeStub
	lastClientID int
	lastLink     *conn.Link
}

func bindMalformedTunnelRuntimeTarget(task *file.Tunnel) *file.Tunnel {
	if task == nil {
		return nil
	}
	task.BindRuntimeOwner("node-a", &file.Tunnel{})
	return task
}

func (s *tunnelHTTPBackendConnBridgeStub) SendLinkInfo(clientID int, link *conn.Link, task *file.Tunnel) (net.Conn, error) {
	s.lastClientID = clientID
	s.lastLink = link
	serverSide, peerSide := net.Pipe()
	go func() {
		_ = peerSide.Close()
	}()
	return serverSide, nil
}

type blockingCloseConn struct {
	closeStarted chan struct{}
	releaseClose chan struct{}
	remoteAddr   net.Addr
	localAddr    net.Addr
	closeOnce    sync.Once
}

func newBlockingCloseConn() *blockingCloseConn {
	return &blockingCloseConn{
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
		remoteAddr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 32001},
		localAddr:    &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 32002},
	}
}

func (c *blockingCloseConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *blockingCloseConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *blockingCloseConn) LocalAddr() net.Addr              { return c.localAddr }
func (c *blockingCloseConn) RemoteAddr() net.Addr             { return c.remoteAddr }
func (c *blockingCloseConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingCloseConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockingCloseConn) SetWriteDeadline(time.Time) error { return nil }

func (c *blockingCloseConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closeStarted)
		<-c.releaseClose
	})
	return nil
}

type tunnelRuntimeListenerStub struct {
	acceptStarted chan struct{}
	acceptReady   chan struct{}
	closed        chan struct{}
	lateConn      net.Conn
	addr          net.Addr
	closeOnce     sync.Once
}

func newTunnelRuntimeListenerStub(lateConn net.Conn) *tunnelRuntimeListenerStub {
	return &tunnelRuntimeListenerStub{
		acceptStarted: make(chan struct{}),
		acceptReady:   make(chan struct{}),
		closed:        make(chan struct{}),
		lateConn:      lateConn,
		addr:          &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 33001},
	}
}

func (l *tunnelRuntimeListenerStub) Accept() (net.Conn, error) {
	select {
	case <-l.acceptStarted:
	default:
		close(l.acceptStarted)
	}
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case <-l.acceptReady:
		return l.lateConn, nil
	}
}

func (l *tunnelRuntimeListenerStub) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *tunnelRuntimeListenerStub) Addr() net.Addr { return l.addr }

func TestTunnelModeServerCloseStopsListenerBeforeSweepingActiveConnections(t *testing.T) {
	task := &file.Tunnel{
		Id:   9,
		Port: 8080,
		Client: &file.Client{
			Id:   7,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	existingConn := newBlockingCloseConn()
	lateServerConn, lateClientConn := net.Pipe()
	defer func() { _ = lateClientConn.Close() }()

	listener := newTunnelRuntimeListenerStub(lateServerConn)
	handlerEntered := make(chan struct{}, 1)
	releaseHandler := make(chan struct{})
	server := NewTunnelModeServer(func(c *conn.Conn, s *TunnelModeServer) error {
		select {
		case handlerEntered <- struct{}{}:
		default:
		}
		<-releaseHandler
		return nil
	}, tunnelServerBridgeStub{}, task)
	server.listener = listener
	server.activeConnections.Store(existingConn, struct{}{})

	acceptDone := make(chan struct{})
	go func() {
		conn.Accept(listener, server.handleConn)
		close(acceptDone)
	}()

	select {
	case <-listener.acceptStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop did not start")
	}

	closeDone := make(chan struct{})
	go func() {
		_ = server.Close()
		close(closeDone)
	}()

	select {
	case <-existingConn.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("existing connection was not swept during Close()")
	}

	close(listener.acceptReady)

	select {
	case <-handlerEntered:
		t.Fatal("listener accepted a new connection after Close() started")
	case <-time.After(100 * time.Millisecond):
	}

	close(existingConn.releaseClose)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not finish")
	}

	select {
	case <-acceptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("accept loop did not stop after Close()")
	}

	close(releaseHandler)
	_ = lateServerConn.Close()
}

func TestTunnelModeServerCloseDropsInvalidActiveConnectionEntry(t *testing.T) {
	server := NewTunnelModeServer(func(c *conn.Conn, s *TunnelModeServer) error { return nil }, tunnelServerBridgeStub{}, &file.Tunnel{})
	spyConn := newUDPCloseSpyConn()
	server.activeConnections.Store(spyConn, struct{}{})
	server.activeConnections.Store("bad-conn-entry", "bad-value")

	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, ok := server.activeConnections.Load("bad-conn-entry"); ok {
		t.Fatal("Close() should drop invalid active connection entry")
	}
	if _, ok := server.activeConnections.Load(spyConn); ok {
		t.Fatal("Close() should remove closed active connection entry")
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not close active tunnel connection")
	}
}

func TestTunnelModeServerCloseClosesActiveConnectionsConcurrently(t *testing.T) {
	server := NewTunnelModeServer(func(c *conn.Conn, s *TunnelModeServer) error { return nil }, tunnelServerBridgeStub{}, &file.Tunnel{})
	first := newBlockingCloseConn()
	second := newBlockingCloseConn()
	server.activeConnections.Store(first, struct{}{})
	server.activeConnections.Store(second, struct{}{})

	closeDone := make(chan struct{})
	go func() {
		_ = server.Close()
		close(closeDone)
	}()

	select {
	case <-first.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first active connection close did not start")
	}

	select {
	case <-second.closeStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second active connection close should start before the first close is released")
	}

	close(first.releaseClose)
	close(second.releaseClose)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not finish after active connections were released")
	}

	if _, ok := server.activeConnections.Load(first); ok {
		t.Fatal("Close() should remove the first active connection entry")
	}
	if _, ok := server.activeConnections.Load(second); ok {
		t.Fatal("Close() should remove the second active connection entry")
	}
}

func TestTunnelModeServerHandleConnRejectsMalformedRuntimeTask(t *testing.T) {
	handlerCalled := false
	server := NewTunnelModeServer(func(c *conn.Conn, s *TunnelModeServer) error {
		handlerCalled = true
		return nil
	}, tunnelServerBridgeStub{}, &file.Tunnel{})
	spyConn := newUDPCloseSpyConn()

	server.handleConn(spyConn)

	if handlerCalled {
		t.Fatal("handleConn() should not invoke handler for malformed runtime task")
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn() should close malformed tunnel connection")
	}
	if _, ok := server.activeConnections.Load(spyConn); ok {
		t.Fatal("handleConn() should remove malformed tunnel connection from active set")
	}
}

func TestPrepareTunnelRouteTargetRejectsMalformedSelectedRuntimeTask(t *testing.T) {
	task := bindMalformedTunnelRuntimeTarget(newTestSocks5Task(1080, "tcp"))
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	spyConn := newUDPCloseSpyConn()

	selected, targetAddr, err := server.prepareTunnelRouteTarget(conn.NewConn(spyConn))
	if err == nil {
		t.Fatal("prepareTunnelRouteTarget() should reject malformed selected runtime task")
	}
	if selected != nil {
		t.Fatalf("prepareTunnelRouteTarget() selected = %#v, want nil", selected)
	}
	if targetAddr != "" {
		t.Fatalf("prepareTunnelRouteTarget() targetAddr = %q, want empty", targetAddr)
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("prepareTunnelRouteTarget() should close malformed tunnel conn")
	}
}

func TestTunnelModeServerSocks5ConnectRejectsMalformedSelectedRuntimeTask(t *testing.T) {
	bridge := &noCallServerBridge{}
	task := bindMalformedTunnelRuntimeTarget(newTestSocks5Task(1080, "mixProxy"))
	server := NewTunnelModeServer(ProcessMix, bridge, task)
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	done := make(chan struct{})
	go func() {
		server.handleSocks5Connect(serverSide, socks5Address{Type: ipV4, Host: "8.8.8.8", Port: 53})
		close(done)
	}()

	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}
	if bridge.sendCalled {
		t.Fatal("SendLinkInfo() should not run for malformed selected runtime task")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSocks5Connect() did not finish in time")
	}
}

func TestTunnelModeServerSocks5AssociateRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, &file.Tunnel{})
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	done := make(chan struct{})
	go func() {
		server.handleSocks5Associate(serverSide, socks5Address{Type: ipV4, Host: "8.8.8.8", Port: 53})
		close(done)
	}()

	if replyCode := readSocks5ReplyCode(t, clientSide); replyCode != serverFailure {
		t.Fatalf("reply code = %d, want %d", replyCode, serverFailure)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSocks5Associate() did not finish in time")
	}
}

func TestSecretServerHandleSecretRejectsMalformedSelectedRuntimeTask(t *testing.T) {
	bridge := &noCallServerBridge{}
	task := bindMalformedTunnelRuntimeTarget(newTestSocks5Task(1080, "tcp"))
	server := NewSecretServer(bridge, task, false)
	spyConn := newUDPCloseSpyConn()

	if err := server.HandleSecret(spyConn); err == nil {
		t.Fatal("HandleSecret() should reject malformed selected runtime task")
	}
	if bridge.sendCalled {
		t.Fatal("SendLinkInfo() should not run for malformed selected runtime task")
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleSecret() should close malformed secret conn")
	}
}

func TestProcessHttpRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewTunnelModeServer(ProcessHttp, &noCallServerBridge{}, &file.Tunnel{})
	spyConn := newUDPCloseSpyConn()

	if err := ProcessHttp(conn.NewConn(spyConn), server); err == nil {
		t.Fatal("ProcessHttp() should reject malformed runtime task")
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessHttp() should close malformed http proxy conn")
	}
}

func TestProcessMixRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, &file.Tunnel{})
	spyConn := newUDPCloseSpyConn()

	if err := ProcessMix(conn.NewConn(spyConn), server); err == nil {
		t.Fatal("ProcessMix() should reject malformed mixed proxy conn")
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessMix() should close malformed mixed proxy conn")
	}
}

func TestProcessMixRejectsUnknownProtocol(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, newTestSocks5Task(1080, "mixProxy"))
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ProcessMix(conn.NewConn(serverSide), server)
	}()

	if _, err := clientSide.Write([]byte("zz")); err != nil {
		t.Fatalf("write mixed protocol header: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, errMixedProxyUnknownProtocol) {
			t.Fatalf("ProcessMix() error = %v, want %v", err, errMixedProxyUnknownProtocol)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessMix() did not finish in time")
	}
}

func TestTunnelModeServerHandleMixedHTTPProxyRejectsDisabled(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, newTestSocks5Task(1080, "mixProxy"))
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.handleMixedHTTPProxy(conn.NewConn(serverSide), server.CurrentTask(), []byte("GE"), false)
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, errMixedHTTPProxyDisabled) {
			t.Fatalf("handleMixedHTTPProxy() error = %v, want %v", err, errMixedHTTPProxyDisabled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleMixedHTTPProxy() did not finish in time")
	}
}

func TestTunnelModeServerHandleMixedSocks5RejectsDisabled(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, newTestSocks5Task(1080, "mixProxy"))
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.handleMixedSocks5(conn.NewConn(serverSide), server.CurrentTask(), 1, false)
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, errMixedSocks5ProxyDisabled) {
			t.Fatalf("handleMixedSocks5() error = %v, want %v", err, errMixedSocks5ProxyDisabled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleMixedSocks5() did not finish in time")
	}
}

func TestTunnelModeServerHandleMixedSocks5RejectsInvalidMethodList(t *testing.T) {
	server := NewTunnelModeServer(ProcessMix, &noCallServerBridge{}, newTestSocks5Task(1080, "mixProxy"))
	serverSide, clientSide := net.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.handleMixedSocks5(conn.NewConn(serverSide), server.CurrentTask(), 1, true)
	}()

	_ = clientSide.Close()

	select {
	case err := <-errCh:
		if !errors.Is(err, errMixedSocks5InvalidMethodList) {
			t.Fatalf("handleMixedSocks5() error = %v, want %v", err, errMixedSocks5InvalidMethodList)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleMixedSocks5() did not finish in time")
	}
}

func TestTunnelModeServerUsesTransparentTCPForTransparentModes(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want bool
	}{
		{name: "p2pt mode", mode: "p2pt", want: true},
		{name: "tcpTrans mode", mode: "tcpTrans", want: true},
		{name: "tcp mode", mode: "tcp", want: false},
		{name: "empty mode", mode: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &TunnelModeServer{
				BaseServer: &BaseServer{
					Task: &file.Tunnel{Mode: tt.mode},
				},
			}
			if got := s.usesTransparentTCP(); got != tt.want {
				t.Fatalf("usesTransparentTCP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteTunnelHTTPProxyErrorTreatsWrappedEOFAs521(t *testing.T) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)

	writeTunnelHTTPProxyError(recorder, req, fmt.Errorf("wrapped eof: %w", io.EOF))

	if recorder.Code != 521 {
		t.Fatalf("status = %d, want 521", recorder.Code)
	}
}

func TestTunnelHTTPProxyConnResponseForErrorClassifiesFailures(t *testing.T) {
	if got := tunnelHTTPProxyConnResponseForError(errTunnelHTTPProxyMissingTarget); got != tunnelHTTPProxyBadRequestResponse {
		t.Fatalf("tunnelHTTPProxyConnResponseForError(missing target) = %q, want %q", got, tunnelHTTPProxyBadRequestResponse)
	}
	if got := tunnelHTTPProxyConnResponseForError(errTunnelHTTPProxyDestinationDenied); got != tunnelHTTPProxyForbiddenResponse {
		t.Fatalf("tunnelHTTPProxyConnResponseForError(destination denied) = %q, want %q", got, tunnelHTTPProxyForbiddenResponse)
	}
	if got := tunnelHTTPProxyConnResponseForError(errors.New("backend unavailable")); got != tunnelHTTPProxyBadGatewayResponse {
		t.Fatalf("tunnelHTTPProxyConnResponseForError(default) = %q, want %q", got, tunnelHTTPProxyBadGatewayResponse)
	}
}

func TestWriteTunnelHTTPProxyResolveErrorClassifiesFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "missing target", err: errTunnelHTTPProxyMissingTarget, want: http.StatusBadRequest},
		{name: "destination denied", err: errTunnelHTTPProxyDestinationDenied, want: http.StatusForbidden},
		{name: "backend failure", err: errors.New("backend unavailable"), want: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeTunnelHTTPProxyResolveError(recorder, tt.err)
			if recorder.Code != tt.want {
				t.Fatalf("writeTunnelHTTPProxyResolveError() status = %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}

func TestWriteTunnelHTTPProxyErrorClassifiesResolvedFailures(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)

	t.Run("missing target", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writeTunnelHTTPProxyError(recorder, req, errTunnelHTTPProxyMissingTarget)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("writeTunnelHTTPProxyError() status = %d, want %d", recorder.Code, http.StatusBadRequest)
		}
	})

	t.Run("destination denied", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		writeTunnelHTTPProxyError(recorder, req, errTunnelHTTPProxyDestinationDenied)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("writeTunnelHTTPProxyError() status = %d, want %d", recorder.Code, http.StatusForbidden)
		}
	})
}

func TestCloseTunnelHTTPProxyConnWritesPayloadAndCloses(t *testing.T) {
	spy := &tunnelHTTPProxyRawResponseConn{}
	errSentinel := errors.New("boom")

	if err := closeTunnelHTTPProxyConn(conn.NewConn(spy), tunnelHTTPProxyBadGatewayResponse, errSentinel); !errors.Is(err, errSentinel) {
		t.Fatalf("closeTunnelHTTPProxyConn() error = %v, want %v", err, errSentinel)
	}
	if got := spy.payload.String(); got != tunnelHTTPProxyBadGatewayResponse {
		t.Fatalf("raw payload = %q, want %q", got, tunnelHTTPProxyBadGatewayResponse)
	}
	if !spy.closed.Load() {
		t.Fatal("closeTunnelHTTPProxyConn() should close the underlying conn")
	}
}

func TestNormalizeTunnelHTTPProxyTargetAddsDefaultPorts(t *testing.T) {
	getReq := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	getReq.Host = "example.com"
	if got := normalizeTunnelHTTPProxyTarget(getReq); got != "example.com:80" {
		t.Fatalf("normalizeTunnelHTTPProxyTarget(GET) = %q, want %q", got, "example.com:80")
	}

	connectReq := httptest.NewRequest(http.MethodConnect, "http://ignored", nil)
	connectReq.Host = "secure.example.com"
	if got := normalizeTunnelHTTPProxyTarget(connectReq); got != "secure.example.com:443" {
		t.Fatalf("normalizeTunnelHTTPProxyTarget(CONNECT) = %q, want %q", got, "secure.example.com:443")
	}
}

func TestTunnelHTTPBackendTransportPoolCachesPerBackendKey(t *testing.T) {
	pool := newTunnelHTTPBackendTransportPool()
	created := 0
	create := func() *http.Transport {
		created++
		return &http.Transport{}
	}
	task := &file.Tunnel{Id: 1, Flow: &file.Flow{}, Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}}}

	first := pool.GetOrCreate(tunnelHTTPBackendSelection{routeUUID: "node-a", targetAddr: "example.com:80", task: task}, create)
	second := pool.GetOrCreate(tunnelHTTPBackendSelection{routeUUID: "node-a", targetAddr: "example.com:80", task: task}, create)
	third := pool.GetOrCreate(tunnelHTTPBackendSelection{routeUUID: "node-b", targetAddr: "example.com:80", task: task}, create)

	if first == nil || second == nil || third == nil {
		t.Fatal("GetOrCreate() returned nil transport")
	}
	if first != second {
		t.Fatal("same backend key should reuse the same transport")
	}
	if first == third {
		t.Fatal("different backend key should use a different transport")
	}
	if created != 2 {
		t.Fatalf("transport create count = %d, want 2", created)
	}
	if got := pool.Size(); got != 2 {
		t.Fatalf("pool size = %d, want 2", got)
	}
}

func TestTunnelHTTPBackendTransportPoolDefersExpiredSweepUntilScheduled(t *testing.T) {
	pool := newTunnelHTTPBackendTransportPool()
	pool.idleTTL = time.Second
	task := &file.Tunnel{Id: 1, Flow: &file.Flow{}, Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}}}
	staleSelection := tunnelHTTPBackendSelection{routeUUID: "stale-node", targetAddr: "example.com:81", task: task}
	staleKey := staleSelection.key()
	now := time.Now()
	pool.items[staleKey] = newTunnelHTTPBackendEntry(&http.Transport{}, now.Add(-2*time.Second))
	pool.nextPruneAt = now.Add(time.Minute)

	freshSelection := tunnelHTTPBackendSelection{routeUUID: "fresh-node", targetAddr: "example.com:82", task: task}
	pool.GetOrCreate(freshSelection, func() *http.Transport { return &http.Transport{} })

	if _, ok := pool.items[staleKey]; !ok {
		t.Fatal("stale entry should be retained until the scheduled prune time")
	}

	pool.nextPruneAt = time.Time{}
	nextSelection := tunnelHTTPBackendSelection{routeUUID: "next-node", targetAddr: "example.com:83", task: task}
	pool.GetOrCreate(nextSelection, func() *http.Transport { return &http.Transport{} })

	if _, ok := pool.items[staleKey]; ok {
		t.Fatal("stale entry should be pruned once the scheduled prune time is reached")
	}
}

func TestTunnelHTTPBackendTransportPoolCoalescesConcurrentCreateAfterUnlockedBuild(t *testing.T) {
	pool := newTunnelHTTPBackendTransportPool()
	task := &file.Tunnel{Id: 1, Flow: &file.Flow{}, Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}}}
	selection := tunnelHTTPBackendSelection{routeUUID: "node-a", targetAddr: "example.com:80", task: task}

	var created atomic.Int32
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	create := func() *http.Transport {
		created.Add(1)
		ready <- struct{}{}
		<-release
		return &http.Transport{}
	}

	results := make([]*http.Transport, 2)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait()
			results[idx] = pool.GetOrCreate(selection, create)
		}(i)
	}

	start.Done()
	<-ready
	<-ready
	close(release)
	wg.Wait()

	if created.Load() != 2 {
		t.Fatalf("transport create count = %d, want 2 concurrent unlocked builds", created.Load())
	}
	if results[0] == nil || results[1] == nil {
		t.Fatalf("GetOrCreate() results = %#v, want non-nil shared transport", results)
	}
	if results[0] != results[1] {
		t.Fatal("concurrent GetOrCreate() should converge on one cached transport")
	}
	if got := pool.Size(); got != 1 {
		t.Fatalf("pool size = %d, want 1 after concurrent coalescing", got)
	}
}

func TestTunnelModeServerHTTPProxyBackendPoolConvergesConcurrently(t *testing.T) {
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, &file.Tunnel{})

	const callers = 32
	results := make([]*tunnelHTTPBackendTransportPool, callers)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			start.Wait()
			results[idx] = server.httpProxyBackendPool()
		}(i)
	}

	start.Done()
	wg.Wait()

	if results[0] == nil {
		t.Fatal("httpProxyBackendPool() returned nil pool")
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatal("concurrent httpProxyBackendPool() calls should converge on one shared pool")
		}
	}
}

func TestTunnelModeServerCloseDetachesAndClearsHTTPProxyBackendPool(t *testing.T) {
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, &file.Tunnel{})
	pool := server.httpProxyBackendPool()
	pool.items["node-a|example.com:80"] = newTunnelHTTPBackendEntry(&http.Transport{}, time.Now())

	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if server.httpProxyPool != nil {
		t.Fatal("Close() should detach cached tunnel http backend pool")
	}
	if got := pool.Size(); got != 0 {
		t.Fatalf("detached pool size = %d, want 0 after Close()", got)
	}
}

func TestSelectTunnelHTTPBackendUsesBridgeRouteSelector(t *testing.T) {
	task := &file.Tunnel{
		Id:     3,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   9,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, tunnelHTTPRouteBridgeStub{selectedRoute: "node-b"}, task)

	selection, err := selectTunnelHTTPBackend(task, server, "example.com:80")
	if err != nil {
		t.Fatalf("selectTunnelHTTPBackend() error = %v", err)
	}
	if selection.routeUUID != "node-b" {
		t.Fatalf("selection.routeUUID = %q, want %q", selection.routeUUID, "node-b")
	}
	if selection.targetAddr != "example.com:80" {
		t.Fatalf("selection.targetAddr = %q, want %q", selection.targetAddr, "example.com:80")
	}
	if selection.task == nil {
		t.Fatal("selection.task = nil, want selected task")
	}
}

func TestSelectTunnelHTTPBackendRejectsMalformedRuntimeTask(t *testing.T) {
	tests := []struct {
		name string
		task *file.Tunnel
	}{
		{
			name: "missing client",
			task: &file.Tunnel{
				Id:     1,
				Flow:   &file.Flow{},
				Target: &file.Target{TargetStr: "example.com:80"},
			},
		},
		{
			name: "missing client config",
			task: &file.Tunnel{
				Id:     2,
				Flow:   &file.Flow{},
				Target: &file.Target{TargetStr: "example.com:80"},
				Client: &file.Client{Id: 1, Flow: &file.Flow{}},
			},
		},
		{
			name: "missing client flow",
			task: &file.Tunnel{
				Id:     3,
				Flow:   &file.Flow{},
				Target: &file.Target{TargetStr: "example.com:80"},
				Client: &file.Client{Id: 1, Cnf: &file.Config{}},
			},
		},
		{
			name: "missing task flow",
			task: &file.Tunnel{
				Id:     4,
				Target: &file.Target{TargetStr: "example.com:80"},
				Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}},
			},
		},
		{
			name: "missing target",
			task: &file.Tunnel{
				Id:     5,
				Flow:   &file.Flow{},
				Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewTunnelModeServer(ProcessTunnel, tunnelHTTPRouteBridgeStub{selectedRoute: "node-b"}, tt.task)
			_, err := selectTunnelHTTPBackend(tt.task, server, "example.com:80")
			if !errors.Is(err, errTunnelHTTPProxyInvalidBackend) {
				t.Fatalf("selectTunnelHTTPBackend() error = %v, want %v", err, errTunnelHTTPProxyInvalidBackend)
			}
		})
	}
}

func TestTunnelModeServerDialSelectedHTTPBackendRejectsMalformedSelectionTask(t *testing.T) {
	bridge := &noCallServerBridge{}
	server := NewTunnelModeServer(ProcessTunnel, bridge, &file.Tunnel{Flow: &file.Flow{}})

	selection := tunnelHTTPBackendSelection{
		targetAddr: "example.com:80",
		task: &file.Tunnel{
			Id:   6,
			Flow: &file.Flow{},
			Client: &file.Client{
				Id:   1,
				Cnf:  &file.Config{},
				Flow: &file.Flow{},
			},
		},
	}

	target, err := server.dialSelectedHTTPBackend(context.Background(), selection, "127.0.0.1:12345")
	if !errors.Is(err, errTunnelHTTPProxyInvalidBackend) {
		t.Fatalf("dialSelectedHTTPBackend() error = %v, want %v", err, errTunnelHTTPProxyInvalidBackend)
	}
	if target != nil {
		t.Fatalf("dialSelectedHTTPBackend() target = %#v, want nil", target)
	}
	if bridge.sendCalled {
		t.Fatal("SendLinkInfo() should not be called for malformed backend selection")
	}
}

func TestTunnelModeServerDialSelectedHTTPBackendHandlesNilContext(t *testing.T) {
	bridge := &tunnelHTTPBackendConnBridgeStub{}
	task := &file.Tunnel{
		Id:     7,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   2,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, bridge, task)
	selection := tunnelHTTPBackendSelection{
		routeUUID:  "node-a",
		targetAddr: "example.com:80",
		task:       task,
	}

	var parent context.Context
	target, err := server.dialSelectedHTTPBackend(parent, selection, "127.0.0.1:12345")
	if err != nil {
		t.Fatalf("dialSelectedHTTPBackend() error = %v", err)
	}
	if target == nil {
		t.Fatal("dialSelectedHTTPBackend() returned nil conn")
	}
	_ = target.Close()

	if bridge.lastClientID != task.Client.Id {
		t.Fatalf("SendLinkInfo() client id = %d, want %d", bridge.lastClientID, task.Client.Id)
	}
	if bridge.lastLink == nil {
		t.Fatal("SendLinkInfo() did not receive a link")
	}
	if bridge.lastLink.Host != "example.com:80" {
		t.Fatalf("SendLinkInfo() target = %q, want %q", bridge.lastLink.Host, "example.com:80")
	}
	if bridge.lastLink.Option.RouteUUID != "node-a" {
		t.Fatalf("SendLinkInfo() route uuid = %q, want %q", bridge.lastLink.Option.RouteUUID, "node-a")
	}
}

func TestTunnelModeServerTransportForHTTPBackendUsesPerRequestRemoteAddr(t *testing.T) {
	bridge := &tunnelHTTPBackendConnBridgeStub{}
	task := &file.Tunnel{
		Id:     8,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   3,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, bridge, task)
	selection := tunnelHTTPBackendSelection{
		routeUUID:  "node-a",
		targetAddr: "example.com:80",
		task:       task,
	}

	firstTransport := server.transportForHTTPBackend(selection)
	secondTransport := server.transportForHTTPBackend(selection)
	if firstTransport == nil || secondTransport == nil {
		t.Fatal("transportForHTTPBackend() returned nil transport")
	}
	if firstTransport != secondTransport {
		t.Fatal("same backend selection should reuse cached transport")
	}

	firstCtx := context.WithValue(context.Background(), tunnelHTTPProxyRouteRuntimeKey, server.NewRouteRuntimeContext(task.Client, selection.routeUUID))
	firstCtx = context.WithValue(firstCtx, tunnelHTTPProxyRemoteAddrKey, "198.51.100.10:1000")
	firstConn, err := firstTransport.DialContext(firstCtx, "tcp", "")
	if err != nil {
		t.Fatalf("first DialContext() error = %v", err)
	}
	_ = firstConn.Close()
	if bridge.lastLink == nil {
		t.Fatal("first DialContext() did not open a bridge link")
	}
	if bridge.lastLink.RemoteAddr != "198.51.100.10:1000" {
		t.Fatalf("first bridge remote addr = %q, want %q", bridge.lastLink.RemoteAddr, "198.51.100.10:1000")
	}

	secondCtx := context.WithValue(context.Background(), tunnelHTTPProxyRouteRuntimeKey, server.NewRouteRuntimeContext(task.Client, selection.routeUUID))
	secondCtx = context.WithValue(secondCtx, tunnelHTTPProxyRemoteAddrKey, "203.0.113.20:2000")
	secondConn, err := secondTransport.DialContext(secondCtx, "tcp", "")
	if err != nil {
		t.Fatalf("second DialContext() error = %v", err)
	}
	_ = secondConn.Close()
	if bridge.lastLink == nil {
		t.Fatal("second DialContext() did not open a bridge link")
	}
	if bridge.lastLink.RemoteAddr != "203.0.113.20:2000" {
		t.Fatalf("second bridge remote addr = %q, want %q", bridge.lastLink.RemoteAddr, "203.0.113.20:2000")
	}
}

func TestTunnelModeServerTransportForHTTPBackendUsesPerRequestSelectionState(t *testing.T) {
	bridge := &tunnelHTTPBackendConnBridgeStub{}
	baseTask := &file.Tunnel{
		Id:     9,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   4,
			Cnf:  &file.Config{Crypt: false},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, bridge, baseTask)
	selection := tunnelHTTPBackendSelection{
		routeUUID:  "node-a",
		targetAddr: "example.com:80",
		task:       baseTask,
	}

	transport := server.transportForHTTPBackend(selection)
	if transport == nil {
		t.Fatal("transportForHTTPBackend() returned nil transport")
	}

	firstCtx := context.WithValue(context.Background(), tunnelHTTPProxyRouteRuntimeKey, server.NewRouteRuntimeContext(baseTask.Client, selection.routeUUID))
	firstCtx = context.WithValue(firstCtx, tunnelHTTPProxyRemoteAddrKey, "198.51.100.30:3000")
	firstCtx = context.WithValue(firstCtx, tunnelHTTPProxyBackendKey, selection)
	firstConn, err := transport.DialContext(firstCtx, "tcp", "")
	if err != nil {
		t.Fatalf("first DialContext() error = %v", err)
	}
	_ = firstConn.Close()
	if bridge.lastLink == nil {
		t.Fatal("first DialContext() did not open a bridge link")
	}
	if bridge.lastLink.Crypt {
		t.Fatal("first bridge link should preserve the initial non-crypt task state")
	}

	updatedTask := &file.Tunnel{
		Id:     baseTask.Id,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   baseTask.Client.Id,
			Cnf:  &file.Config{Crypt: true},
			Flow: &file.Flow{},
		},
	}
	updatedSelection := tunnelHTTPBackendSelection{
		routeUUID:  selection.routeUUID,
		targetAddr: selection.targetAddr,
		task:       updatedTask,
	}

	secondCtx := context.WithValue(context.Background(), tunnelHTTPProxyRouteRuntimeKey, server.NewRouteRuntimeContext(updatedTask.Client, updatedSelection.routeUUID))
	secondCtx = context.WithValue(secondCtx, tunnelHTTPProxyRemoteAddrKey, "203.0.113.30:4000")
	secondCtx = context.WithValue(secondCtx, tunnelHTTPProxyBackendKey, updatedSelection)
	secondConn, err := transport.DialContext(secondCtx, "tcp", "")
	if err != nil {
		t.Fatalf("second DialContext() error = %v", err)
	}
	_ = secondConn.Close()
	if bridge.lastLink == nil {
		t.Fatal("second DialContext() did not open a bridge link")
	}
	if !bridge.lastLink.Crypt {
		t.Fatal("second bridge link should use the per-request backend selection instead of the cached task snapshot")
	}
}

func TestTunnelModeServerResolveTunnelHTTPProxyTargetBuildsRouteRuntime(t *testing.T) {
	task := &file.Tunnel{
		Id:     10,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   5,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, tunnelHTTPRouteBridgeStub{selectedRoute: "node-b"}, task)

	resolved, err := server.resolveTunnelHTTPProxyTarget(task, " example.com:80 ")
	if err != nil {
		t.Fatalf("resolveTunnelHTTPProxyTarget() error = %v", err)
	}
	if resolved.selection.routeUUID != "node-b" {
		t.Fatalf("selection.routeUUID = %q, want %q", resolved.selection.routeUUID, "node-b")
	}
	if resolved.selection.targetAddr != "example.com:80" {
		t.Fatalf("selection.targetAddr = %q, want %q", resolved.selection.targetAddr, "example.com:80")
	}
	if resolved.routeRuntime == nil {
		t.Fatal("resolveTunnelHTTPProxyTarget() returned nil route runtime")
	}
	if got := resolved.routeRuntime.RouteUUID(); got != "node-b" {
		t.Fatalf("routeRuntime.RouteUUID() = %q, want %q", got, "node-b")
	}
}

func TestTunnelModeServerResolveAuthorizedTunnelHTTPProxyTargetRejectsDestinationDenied(t *testing.T) {
	task := &file.Tunnel{
		Id:           10,
		Mode:         "mixProxy",
		Flow:         &file.Flow{},
		Target:       &file.Target{TargetStr: "blocked.example:443"},
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "full:blocked.example",
		Client: &file.Client{
			Id:   5,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	task.CompileDestACL()
	server := NewTunnelModeServer(ProcessTunnel, tunnelHTTPRouteBridgeStub{selectedRoute: "node-b"}, task)

	resolved, err := server.resolveAuthorizedTunnelHTTPProxyTarget(task, " blocked.example:443 ")
	if !errors.Is(err, errTunnelHTTPProxyDestinationDenied) {
		t.Fatalf("resolveAuthorizedTunnelHTTPProxyTarget() error = %v, want %v", err, errTunnelHTTPProxyDestinationDenied)
	}
	if resolved != (tunnelHTTPProxyResolvedRequest{}) {
		t.Fatalf("resolveAuthorizedTunnelHTTPProxyTarget() resolved = %#v, want zero value on denial", resolved)
	}
}

func TestTunnelModeServerResolveTunnelHTTPBackendStateBackfillsCombinedPayload(t *testing.T) {
	task := &file.Tunnel{
		Id:     11,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   6,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	routeRuntime := server.NewRouteRuntimeContext(task.Client, "")
	ctx := context.WithValue(context.Background(), tunnelHTTPProxyRouteRuntimeKey, routeRuntime)
	ctx = context.WithValue(ctx, tunnelHTTPProxyRemoteAddrKey, " 203.0.113.70:7070 ")
	ctx = context.WithValue(ctx, tunnelHTTPProxyBackendKey, tunnelHTTPBackendSelection{
		routeUUID:  " node-z ",
		targetAddr: " example.com:80 ",
		task:       task,
	})

	resolvedCtx, resolved, err := server.resolveTunnelHTTPBackendState(ctx, tunnelHTTPBackendSelection{}, "")
	if err != nil {
		t.Fatalf("resolveTunnelHTTPBackendState() error = %v", err)
	}
	if resolved.selection.routeUUID != "node-z" {
		t.Fatalf("selection.routeUUID = %q, want %q", resolved.selection.routeUUID, "node-z")
	}
	if resolved.selection.targetAddr != "example.com:80" {
		t.Fatalf("selection.targetAddr = %q, want %q", resolved.selection.targetAddr, "example.com:80")
	}
	if resolved.remoteAddr != "203.0.113.70:7070" {
		t.Fatalf("remoteAddr = %q, want %q", resolved.remoteAddr, "203.0.113.70:7070")
	}
	if resolved.routeRuntime != routeRuntime {
		t.Fatalf("routeRuntime = %#v, want original route runtime %#v", resolved.routeRuntime, routeRuntime)
	}
	if got := routeRuntime.RouteUUID(); got != "node-z" {
		t.Fatalf("routeRuntime.RouteUUID() = %q, want %q", got, "node-z")
	}
	data, ok := tunnelHTTPProxyContextValue(resolvedCtx)
	if !ok {
		t.Fatal("resolveTunnelHTTPBackendState() should backfill combined payload")
	}
	if data.routeRuntime != routeRuntime {
		t.Fatalf("combined routeRuntime = %#v, want %#v", data.routeRuntime, routeRuntime)
	}
	if data.remoteAddr != "203.0.113.70:7070" {
		t.Fatalf("combined remoteAddr = %q, want %q", data.remoteAddr, "203.0.113.70:7070")
	}
	if data.selection != resolved.selection {
		t.Fatalf("combined selection = %#v, want %#v", data.selection, resolved.selection)
	}
}

func TestTunnelModeServerDialResolvedTunnelHTTPBackendReturnsDestinationDenied(t *testing.T) {
	task := &file.Tunnel{
		Id:           11,
		Mode:         "mixProxy",
		Flow:         &file.Flow{},
		Target:       &file.Target{TargetStr: "blocked.example:443"},
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "full:blocked.example",
		Client: &file.Client{
			Id:   6,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	task.CompileDestACL()
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	resolved := tunnelHTTPProxyResolvedRequest{
		selection: tunnelHTTPBackendSelection{
			routeUUID:  "node-z",
			targetAddr: "blocked.example:443",
			task:       task,
		},
		routeRuntime: server.NewRouteRuntimeContext(task.Client, "node-z"),
		remoteAddr:   "203.0.113.70:7070",
	}

	target, err := server.dialResolvedTunnelHTTPBackend(context.Background(), resolved)
	if !errors.Is(err, errTunnelHTTPProxyDestinationDenied) {
		t.Fatalf("dialResolvedTunnelHTTPBackend() error = %v, want %v", err, errTunnelHTTPProxyDestinationDenied)
	}
	if target != nil {
		t.Fatalf("dialResolvedTunnelHTTPBackend() conn = %#v, want nil", target)
	}
}

func TestTunnelModeServerResolveTunnelHTTPProxyServeRequestBackfillsRemoteAddr(t *testing.T) {
	task := &file.Tunnel{
		Id:     12,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   7,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, tunnelHTTPRouteBridgeStub{selectedRoute: "node-c"}, task)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)

	resolvedReq, resolved, err := server.resolveTunnelHTTPProxyServeRequest(task, req, " 198.51.100.88:8088 ")
	if err != nil {
		t.Fatalf("resolveTunnelHTTPProxyServeRequest() error = %v", err)
	}
	if resolved.remoteAddr != "198.51.100.88:8088" {
		t.Fatalf("resolved.remoteAddr = %q, want %q", resolved.remoteAddr, "198.51.100.88:8088")
	}
	data, ok := tunnelHTTPProxyContextValue(resolvedReq.Context())
	if !ok {
		t.Fatal("resolveTunnelHTTPProxyServeRequest() should install combined payload")
	}
	if data.remoteAddr != "198.51.100.88:8088" {
		t.Fatalf("combined remoteAddr = %q, want %q", data.remoteAddr, "198.51.100.88:8088")
	}
	if data.selection != resolved.selection {
		t.Fatalf("combined selection = %#v, want %#v", data.selection, resolved.selection)
	}
	if data.routeRuntime != resolved.routeRuntime {
		t.Fatalf("combined routeRuntime = %#v, want %#v", data.routeRuntime, resolved.routeRuntime)
	}
	if trace := httptrace.ContextClientTrace(resolvedReq.Context()); trace == nil || trace.GotConn == nil {
		t.Fatal("resolveTunnelHTTPProxyServeRequest() should install route runtime httptrace")
	}
}

func TestTunnelHTTPProxyContextDataResolvesCombinedPayload(t *testing.T) {
	task := &file.Tunnel{
		Id:     10,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   5,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	selection := tunnelHTTPBackendSelection{
		routeUUID:  "node-a",
		targetAddr: "example.com:80",
		task:       task,
	}
	routeRuntime := server.NewRouteRuntimeContext(task.Client, "")
	ctx := withTunnelHTTPProxyContextData(context.Background(), tunnelHTTPProxyContextData{
		routeRuntime: routeRuntime,
		remoteAddr:   " 203.0.113.55:5050 ",
		selection: tunnelHTTPBackendSelection{
			routeUUID:  " node-a ",
			targetAddr: " example.com:80 ",
			task:       task,
		},
	})

	resolvedSelection, err := resolveTunnelHTTPBackendSelection(ctx, tunnelHTTPBackendSelection{})
	if err != nil {
		t.Fatalf("resolveTunnelHTTPBackendSelection() error = %v", err)
	}
	if resolvedSelection != selection {
		t.Fatalf("resolveTunnelHTTPBackendSelection() = %#v, want %#v", resolvedSelection, selection)
	}
	remoteAddr, err := resolveTunnelHTTPBackendRemoteAddr(ctx, "")
	if err != nil {
		t.Fatalf("resolveTunnelHTTPBackendRemoteAddr() error = %v", err)
	}
	if remoteAddr != "203.0.113.55:5050" {
		t.Fatalf("resolveTunnelHTTPBackendRemoteAddr() = %q, want %q", remoteAddr, "203.0.113.55:5050")
	}
	resolvedRuntime := server.resolveTunnelHTTPRouteRuntime(ctx, selection)
	if resolvedRuntime != routeRuntime {
		t.Fatalf("resolveTunnelHTTPRouteRuntime() = %#v, want original route runtime %#v", resolvedRuntime, routeRuntime)
	}
	if routeRuntime.RouteUUID() != "node-a" {
		t.Fatalf("routeRuntime.RouteUUID() = %q, want %q", routeRuntime.RouteUUID(), "node-a")
	}
}

func TestPrepareTunnelHTTPProxyContextStoresCombinedPayload(t *testing.T) {
	task := &file.Tunnel{
		Id:     11,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   6,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	selection := tunnelHTTPBackendSelection{
		routeUUID:  "node-b",
		targetAddr: "example.com:80",
		task:       task,
	}
	routeRuntime := server.NewRouteRuntimeContext(task.Client, "node-b")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)

	req = prepareTunnelHTTPProxyContext(req, tunnelHTTPProxyResolvedRequest{
		selection:    selection,
		routeRuntime: routeRuntime,
		remoteAddr:   "198.51.100.10:8080",
	})

	resolvedSelection, err := resolveTunnelHTTPBackendSelection(req.Context(), tunnelHTTPBackendSelection{})
	if err != nil {
		t.Fatalf("resolveTunnelHTTPBackendSelection() error = %v", err)
	}
	if resolvedSelection != selection {
		t.Fatalf("resolveTunnelHTTPBackendSelection() = %#v, want %#v", resolvedSelection, selection)
	}
	remoteAddr, err := resolveTunnelHTTPBackendRemoteAddr(req.Context(), "")
	if err != nil {
		t.Fatalf("resolveTunnelHTTPBackendRemoteAddr() error = %v", err)
	}
	if remoteAddr != "198.51.100.10:8080" {
		t.Fatalf("resolveTunnelHTTPBackendRemoteAddr() = %q, want %q", remoteAddr, "198.51.100.10:8080")
	}
	resolvedRuntime := server.resolveTunnelHTTPRouteRuntime(req.Context(), selection)
	if resolvedRuntime != routeRuntime {
		t.Fatalf("resolveTunnelHTTPRouteRuntime() = %#v, want original route runtime %#v", resolvedRuntime, routeRuntime)
	}
	if trace := httptrace.ContextClientTrace(req.Context()); trace == nil || trace.GotConn == nil {
		t.Fatal("prepareTunnelHTTPProxyContext() should install route runtime httptrace")
	}
}

func TestPrepareTunnelHTTPProxyContextPreservesExistingTrace(t *testing.T) {
	task := &file.Tunnel{
		Id:     12,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "example.com:80"},
		Client: &file.Client{
			Id:   7,
			Cnf:  &file.Config{},
			Flow: &file.Flow{},
		},
	}
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, task)
	routeRuntime := server.NewRouteRuntimeContext(task.Client, "")
	selection := tunnelHTTPBackendSelection{
		routeUUID:  "node-c",
		targetAddr: "example.com:80",
		task:       task,
	}

	parentTraceCalls := 0
	parentCtx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			parentTraceCalls++
		},
	})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil).WithContext(parentCtx)
	req = prepareTunnelHTTPProxyContext(req, tunnelHTTPProxyResolvedRequest{
		selection:    selection,
		routeRuntime: routeRuntime,
		remoteAddr:   "198.51.100.20:8080",
	})

	trace := httptrace.ContextClientTrace(req.Context())
	if trace == nil || trace.GotConn == nil {
		t.Fatal("prepareTunnelHTTPProxyContext() should keep a usable route runtime httptrace")
	}

	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	trace.GotConn(httptrace.GotConnInfo{Conn: WrapRuntimeRouteConn(left, "node-c")})

	if parentTraceCalls != 1 {
		t.Fatalf("existing parent trace calls = %d, want 1", parentTraceCalls)
	}
	if routeRuntime.RouteUUID() != "node-c" {
		t.Fatalf("routeRuntime.RouteUUID() = %q, want %q", routeRuntime.RouteUUID(), "node-c")
	}
}

func TestTunnelHTTPProxyShutdownResetAndStop(t *testing.T) {
	closer := &tunnelHTTPProxyShutdownCloserStub{closed: make(chan struct{}, 1)}
	shutdown := newTunnelHTTPProxyShutdown(20 * time.Millisecond)

	shutdown.reset(closer)
	time.Sleep(5 * time.Millisecond)
	shutdown.reset(closer)
	select {
	case <-closer.closed:
		t.Fatal("shutdown timer should be replaced before the first deadline fires")
	case <-time.After(10 * time.Millisecond):
	}
	select {
	case <-closer.closed:
	case <-time.After(80 * time.Millisecond):
		t.Fatal("shutdown timer did not close server after reset")
	}
	if closer.calls.Load() != 1 {
		t.Fatalf("shutdown close calls = %d, want 1", closer.calls.Load())
	}

	closer2 := &tunnelHTTPProxyShutdownCloserStub{closed: make(chan struct{}, 1)}
	shutdown2 := newTunnelHTTPProxyShutdown(20 * time.Millisecond)
	shutdown2.reset(closer2)
	shutdown2.stop()
	select {
	case <-closer2.closed:
		t.Fatal("shutdown stop should cancel scheduled close")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTunnelHTTPProxyShutdownWaitsForActiveRequestBeforeClosing(t *testing.T) {
	closer := &tunnelHTTPProxyShutdownCloserStub{closed: make(chan struct{}, 1)}
	shutdown := newTunnelHTTPProxyShutdown(20 * time.Millisecond)

	shutdown.reset(closer)
	time.Sleep(5 * time.Millisecond)
	shutdown.begin()

	select {
	case <-closer.closed:
		t.Fatal("shutdown should not close server while request is active")
	case <-time.After(40 * time.Millisecond):
	}

	shutdown.reset(closer)

	select {
	case <-closer.closed:
	case <-time.After(80 * time.Millisecond):
		t.Fatal("shutdown did not close server after active request completed")
	}
	if closer.calls.Load() != 1 {
		t.Fatalf("shutdown close calls = %d, want 1", closer.calls.Load())
	}
}

type udpCloseSpyConn struct {
	closed chan struct{}
}

type tunnelHTTPProxyRawResponseConn struct {
	payload bytes.Buffer
	closed  atomic.Bool
}

type tunnelHTTPProxyShutdownCloserStub struct {
	calls  atomic.Int32
	closed chan struct{}
}

func (s *tunnelHTTPProxyShutdownCloserStub) Close() error {
	if s.calls.Add(1) == 1 && s.closed != nil {
		s.closed <- struct{}{}
	}
	return nil
}

type udpPacketWriterStub struct {
	written int
	err     error
	calls   int
}

func newUDPCloseSpyConn() *udpCloseSpyConn {
	return &udpCloseSpyConn{closed: make(chan struct{})}
}

func (c *udpCloseSpyConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *udpCloseSpyConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *udpCloseSpyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *udpCloseSpyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *udpCloseSpyConn) SetDeadline(time.Time) error      { return nil }
func (c *udpCloseSpyConn) SetReadDeadline(time.Time) error  { return nil }
func (c *udpCloseSpyConn) SetWriteDeadline(time.Time) error { return nil }

func (c *udpCloseSpyConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *tunnelHTTPProxyRawResponseConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *tunnelHTTPProxyRawResponseConn) Write(b []byte) (int, error)      { return c.payload.Write(b) }
func (c *tunnelHTTPProxyRawResponseConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *tunnelHTTPProxyRawResponseConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *tunnelHTTPProxyRawResponseConn) SetDeadline(time.Time) error      { return nil }
func (c *tunnelHTTPProxyRawResponseConn) SetReadDeadline(time.Time) error  { return nil }
func (c *tunnelHTTPProxyRawResponseConn) SetWriteDeadline(time.Time) error { return nil }
func (c *tunnelHTTPProxyRawResponseConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (s *udpPacketWriterStub) WriteTo([]byte, net.Addr) (int, error) {
	s.calls++
	return s.written, s.err
}

type noCallServerBridge struct {
	sendCalled bool
}

func (b *noCallServerBridge) SendLinkInfo(clientID int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	b.sendCalled = true
	return nil, nil
}

func (b *noCallServerBridge) IsServer() bool { return true }

func (b *noCallServerBridge) CliProcess(*conn.Conn, string) {}

func TestNewTunnelModeServerWithRuntimeUsesInjectedRuntimeContext(t *testing.T) {
	user := &file.User{
		Id:        19,
		Status:    1,
		RateLimit: 64,
		TotalFlow: &file.Flow{},
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{Id: 9, UserId: user.Id, Flow: &file.Flow{}, Cnf: &file.Config{}}
	users := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{user: user, clients: []*file.Client{client}}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{}
		},
	}
	runtime := proxyRuntimeContext{
		users: users,
		accessPolicy: proxyAccessPolicyRuntime{
			users: users,
			currentGlobal: func() proxyGlobalAccessSource {
				return proxyDeniedGlobalSourceStub{}
			},
		},
		userQuota:       proxyUserQuotaRuntime{users: users},
		clientLifecycle: proxyClientLifecycleRuntime{},
		rateLimiters: proxyRateLimitRuntime{
			userQuota: proxyUserQuotaRuntime{users: users},
		},
	}

	server := NewTunnelModeServerWithRuntime(ProcessTunnel, runtime, &noCallServerBridge{}, &file.Tunnel{Flow: &file.Flow{}})
	if !server.IsClientSourceAccessDenied(nil, nil, "127.0.0.1:4000") {
		t.Fatal("IsClientSourceAccessDenied() should use injected runtime policy")
	}
	if limiter := server.ServiceRateLimiter(client, nil, nil); limiter == nil {
		t.Fatal("ServiceRateLimiter() should use injected runtime context")
	}
}

func TestNewTunnelModeServerUsesCurrentGlobalRuntimeContext(t *testing.T) {
	oldRuntime := runtimeProxy
	runtimeProxy = newProxyRuntimeContext()
	t.Cleanup(func() {
		runtimeProxy = oldRuntime
	})

	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, &file.Tunnel{Flow: &file.Flow{}})

	runtimeProxy = proxyRuntimeContext{
		accessPolicy: proxyAccessPolicyRuntime{
			currentGlobal: func() proxyGlobalAccessSource {
				return proxyDeniedGlobalSourceStub{}
			},
		},
	}

	if !server.IsClientSourceAccessDenied(nil, nil, "127.0.0.1:4000") {
		t.Fatal("default TunnelModeServer should keep following current global runtime context")
	}
}

func TestUdpModeServerClientWorkerCutsClientConnOnTaskFlowLimitFailure(t *testing.T) {
	bridge := &noCallServerBridge{}
	task := &file.Tunnel{
		Id: 10,
		Client: &file.Client{
			Id:      1,
			MaxConn: 1,
			Cnf:     &file.Config{},
			Flow:    &file.Flow{},
		},
		Flow: &file.Flow{
			FlowLimit:  1,
			ExportFlow: 2 << 20,
		},
		Target: &file.Target{TargetStr: "127.0.0.1:9000"},
	}
	server := NewUdpModeServer(bridge, task)
	ctx, cancel := context.WithCancel(context.Background())
	session := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    ctx,
		cancel: cancel,
	}

	server.clientWorker(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7000}, session)

	if bridge.sendCalled {
		t.Fatal("SendLinkInfo() should not be called after task flow limit failure")
	}
	if got := task.Client.NowConn; got != 0 {
		t.Fatalf("client.NowConn = %d, want 0", got)
	}
	if got := task.NowConn; got != 0 {
		t.Fatalf("task.NowConn = %d, want 0", got)
	}
}

func TestUdpModeServerStartRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})

	if err := server.Start(); err == nil {
		t.Fatal("Start() should reject malformed udp runtime task")
	}
	if server.listener != nil {
		t.Fatal("Start() should not open listener for malformed udp runtime task")
	}
}

func TestModeServersStartRejectMissingRuntime(t *testing.T) {
	var nilUDP *UdpModeServer
	if err := nilUDP.Start(); !errors.Is(err, errUDPServerUnavailable) {
		t.Fatalf("(*UdpModeServer)(nil).Start() error = %v, want wrapped %v", err, errUDPServerUnavailable)
	}
	if err := (&UdpModeServer{}).Start(); !errors.Is(err, errUDPServerUnavailable) {
		t.Fatalf("zero UdpModeServer.Start() error = %v, want wrapped %v", err, errUDPServerUnavailable)
	}

	var nilTunnel *TunnelModeServer
	if err := nilTunnel.Start(); !errors.Is(err, errTunnelServerUnavailable) {
		t.Fatalf("(*TunnelModeServer)(nil).Start() error = %v, want wrapped %v", err, errTunnelServerUnavailable)
	}
	if err := (&TunnelModeServer{}).Start(); !errors.Is(err, errTunnelServerUnavailable) {
		t.Fatalf("zero TunnelModeServer.Start() error = %v, want wrapped %v", err, errTunnelServerUnavailable)
	}
}

func TestTunnelModeServerStartRejectsMalformedRuntimeTask(t *testing.T) {
	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, &file.Tunnel{})

	if err := server.Start(); err == nil {
		t.Fatal("Start() should reject malformed tunnel runtime task")
	}
	if server.listener != nil {
		t.Fatal("Start() should not open listener for malformed tunnel runtime task")
	}
}

func TestModeServersCloseHandleNilReceiver(t *testing.T) {
	var nilUDP *UdpModeServer
	if err := nilUDP.Close(); err != nil {
		t.Fatalf("(*UdpModeServer)(nil).Close() error = %v, want nil", err)
	}

	var nilTunnel *TunnelModeServer
	if err := nilTunnel.Close(); err != nil {
		t.Fatalf("(*TunnelModeServer)(nil).Close() error = %v, want nil", err)
	}
}

func TestUdpModeServerClientWorkerRejectsMalformedRuntimeTask(t *testing.T) {
	bridge := &noCallServerBridge{}
	server := NewUdpModeServer(bridge, &file.Tunnel{})
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    sessionCtx,
		cancel: cancel,
	}

	server.clientWorker(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7000}, session)

	if bridge.sendCalled {
		t.Fatal("clientWorker() should not open bridge link for malformed udp runtime task")
	}
	select {
	case <-session.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("clientWorker() should close malformed udp session")
	}
}

func TestShouldStopUDPServerReadLoopOnWrappedClosedError(t *testing.T) {
	err := fmt.Errorf("wrapped udp close: %w", net.ErrClosed)
	if !shouldStopUDPServerReadLoop(err) {
		t.Fatalf("shouldStopUDPServerReadLoop(%v) = false, want true", err)
	}
	if shouldStopUDPServerReadLoop(errors.New("other")) {
		t.Fatal("shouldStopUDPServerReadLoop(other) = true, want false")
	}
}

func TestUdpModeServerCloseWaitsForWorkersAndClosesSessionConn(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	sessionCtx, cancel := context.WithCancel(context.Background())
	spyConn := newUDPCloseSpyConn()
	session := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    sessionCtx,
		cancel: cancel,
		conn:   spyConn,
	}
	server.sessions.Store("127.0.0.1:7000", session)

	release := make(chan struct{})
	server.workerWG.Add(1)
	go func() {
		defer server.workerWG.Done()
		<-session.ctx.Done()
		<-release
	}()

	closeDone := make(chan struct{})
	go func() {
		_ = server.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
		t.Fatal("Close() returned before waiting for udp worker")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not close active udp session conn")
	}

	close(release)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not finish after udp worker exit")
	}
}

func TestUdpModeServerCloseDropsInvalidSessionEntry(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	server.sessions.Store("127.0.0.1:7009", "bad-session-entry")

	sessionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spyConn := newUDPCloseSpyConn()
	validSession := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    sessionCtx,
		cancel: cancel,
		conn:   spyConn,
	}
	server.sessions.Store("127.0.0.1:7010", validSession)

	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, ok := server.sessions.Load("127.0.0.1:7009"); ok {
		t.Fatal("Close() should drop invalid session entry")
	}
	if current, ok := server.sessions.Load("127.0.0.1:7010"); !ok || current != validSession {
		t.Fatalf("valid session entry should remain until worker cleanup, current=%v ok=%v", current, ok)
	}
	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not close valid session conn")
	}
}

func TestWriteObservedUDPBackchannelTracksActualWrittenBytes(t *testing.T) {
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:9000"},
	}
	file.InitializeTunnelRuntime(task)

	collector := &proxyRouteRuntimeCollectorStub{}
	writer := &udpPacketWriterStub{written: 2}

	if err := writeObservedUDPBackchannel(writer, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7000}, nil, []byte("hello"), task, collector, "node-a", nil); err != nil {
		t.Fatalf("writeObservedUDPBackchannel() error = %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("udp writer calls = %d, want 1", writer.calls)
	}

	in, out, total := task.ServiceTrafficTotals()
	if in != 0 || out != 2 || total != 2 {
		t.Fatalf("task service traffic = (%d, %d, %d), want (0, 2, 2)", in, out, total)
	}
	clientIn, clientOut, clientTotal := client.ServiceTrafficTotals()
	if clientIn != 0 || clientOut != 2 || clientTotal != 2 {
		t.Fatalf("client service traffic = (%d, %d, %d), want (0, 2, 2)", clientIn, clientOut, clientTotal)
	}
	if collector.serviceTotals[0] != 0 || collector.serviceTotals[1] != 2 {
		t.Fatalf("route service totals = (%d, %d), want (0, 2)", collector.serviceTotals[0], collector.serviceTotals[1])
	}
}

func TestWriteObservedUDPBackchannelUsesBoundNodeRuntimeWhenAvailable(t *testing.T) {
	client := &file.Client{Id: 7, Flow: &file.Flow{}}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:9000"},
	}
	file.InitializeTunnelRuntime(task)

	collector := &proxyRouteRuntimeCollectorStub{}
	boundNode := &proxyBoundNodeRuntimeStub{}
	writer := &udpPacketWriterStub{written: 3}

	if err := writeObservedUDPBackchannel(writer, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7000}, nil, []byte("hello"), task, collector, "node-a", boundNode); err != nil {
		t.Fatalf("writeObservedUDPBackchannel() error = %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("udp writer calls = %d, want 1", writer.calls)
	}
	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when bound node runtime exists", collector.serviceTotals)
	}
	if boundNode.serviceTotals != [2]int64{0, 3} {
		t.Fatalf("bound node service totals = %#v, want %#v", boundNode.serviceTotals, [2]int64{0, 3})
	}
}

func TestUdpModeServerWriteUDPBridgePacketUsesBoundNodeRuntimeWhenAvailable(t *testing.T) {
	client := &file.Client{Id: 7, Flow: &file.Flow{}, Cnf: &file.Config{}}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:9000"},
	}
	file.InitializeTunnelRuntime(task)

	collector := &proxyRouteRuntimeCollectorStub{}
	boundNode := &proxyBoundNodeRuntimeStub{}
	target := &recordingConn{}
	runtime := &udpWorkerRuntime{
		task:        task,
		addr:        &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7000},
		link:        conn.NewLink(common.CONN_UDP, task.Target.TargetStr, false, false, "127.0.0.1:7000", false, conn.WithRouteUUID("node-a")),
		routeStats:  collector,
		boundNode:   boundNode,
		sessionConn: target,
	}
	session := &udpClientSession{}
	packet := udpPacket{buf: []byte("hello"), n: len("hello")}

	if err := (&UdpModeServer{}).writeUDPBridgePacket(session, runtime, packet); err != nil {
		t.Fatalf("writeUDPBridgePacket() error = %v", err)
	}
	if got := target.writes.String(); got != "hello" {
		t.Fatalf("bridge payload write = %q, want %q", got, "hello")
	}
	if collector.serviceTotals != [2]int64{} {
		t.Fatalf("collector service totals = %#v, want zero when bound node runtime exists", collector.serviceTotals)
	}
	if boundNode.serviceTotals != [2]int64{5, 0} {
		t.Fatalf("bound node service totals = %#v, want %#v", boundNode.serviceTotals, [2]int64{5, 0})
	}
}

func TestSecretServerCopySecretTrafficLocalProxyReplaysBufferedIngressToBackendAndCountsServiceTraffic(t *testing.T) {
	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:     9,
		Client: client,
		Flow:   &file.Flow{},
		Target: &file.Target{LocalProxy: true},
	}
	file.InitializeTunnelRuntime(task)

	server := NewSecretServer(&noCallServerBridge{}, task, true)
	collector := &proxyRouteRuntimeCollectorStub{}
	backendTarget := &recordingConn{}
	serviceSide, peerSide := net.Pipe()
	_ = peerSide.Close()

	server.copySecretTraffic(task, secretIngress{
		conn:   conn.NewConn(serviceSide),
		buffer: []byte("secret-prefetch"),
	}, &secretBackend{
		target:       backendTarget,
		routeRuntime: newRouteRuntimeContext(collector, client, "node-secret"),
		localProxy:   true,
		connType:     common.CONN_TCP,
	})

	if got := backendTarget.writes.String(); got != "secret-prefetch" {
		t.Fatalf("backend buffered write = %q, want %q", got, "secret-prefetch")
	}
	in, out, total := task.ServiceTrafficTotals()
	if in != int64(len("secret-prefetch")) || out != 0 || total != int64(len("secret-prefetch")) {
		t.Fatalf("task service traffic = (%d, %d, %d), want (%d, 0, %d)", in, out, total, len("secret-prefetch"), len("secret-prefetch"))
	}
	clientIn, clientOut, clientTotal := client.ServiceTrafficTotals()
	if clientIn != int64(len("secret-prefetch")) || clientOut != 0 || clientTotal != int64(len("secret-prefetch")) {
		t.Fatalf("client service traffic = (%d, %d, %d), want (%d, 0, %d)", clientIn, clientOut, clientTotal, len("secret-prefetch"), len("secret-prefetch"))
	}
	if collector.serviceTotals != [2]int64{int64(len("secret-prefetch")), 0} {
		t.Fatalf("route service totals = %#v, want %#v", collector.serviceTotals, [2]int64{int64(len("secret-prefetch")), 0})
	}
}

func TestSecretServerOpenSecretBackendUsesIngressRemoteAddr(t *testing.T) {
	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	file.InitializeClientRuntime(client)
	task := &file.Tunnel{
		Id:         9,
		Client:     client,
		Flow:       &file.Flow{},
		Target:     &file.Target{TargetStr: "10.0.0.1:22"},
		TargetType: common.CONN_TCP,
	}
	file.InitializeTunnelRuntime(task)

	targetSide, targetPeer := net.Pipe()
	defer func() { _ = targetSide.Close() }()
	defer func() { _ = targetPeer.Close() }()

	bridge := &baseBridgeRuntimeStub{conn: targetSide}
	server := NewSecretServer(bridge, task, false)
	serviceSide, peerSide := net.Pipe()
	defer func() { _ = serviceSide.Close() }()
	defer func() { _ = peerSide.Close() }()

	ingress := secretIngress{
		conn:       conn.NewConn(serviceSide),
		remoteAddr: "203.0.113.8:9000",
	}

	backend, err := server.openSecretBackend(task, ingress)
	if err != nil {
		t.Fatalf("openSecretBackend() error = %v", err)
	}
	if backend == nil {
		t.Fatal("openSecretBackend() returned nil backend")
	}
	if bridge.gotLink == nil {
		t.Fatal("openSecretBackend() did not send a link")
	}
	if bridge.gotLink.RemoteAddr != ingress.remoteAddr {
		t.Fatalf("openSecretBackend() remote addr = %q, want %q", bridge.gotLink.RemoteAddr, ingress.remoteAddr)
	}
}

func TestUDPClientSessionSetConnClosesConnAfterSessionClose(t *testing.T) {
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    sessionCtx,
		cancel: cancel,
	}

	session.close()
	spyConn := newUDPCloseSpyConn()
	session.setConn(spyConn)

	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("setConn() should close conn immediately when session is already closed")
	}
}

func TestUdpModeServerDeleteSessionIfCurrentIgnoresReplacement(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	oldSessionCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	oldSession := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    oldSessionCtx,
		cancel: oldCancel,
	}
	newSessionCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	newSession := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    newSessionCtx,
		cancel: newCancel,
	}

	server.sessions.Store("127.0.0.1:7001", newSession)

	if server.deleteSessionIfCurrent("127.0.0.1:7001", oldSession) {
		t.Fatal("deleteSessionIfCurrent should not remove a replacement session")
	}
	if current, ok := server.sessions.Load("127.0.0.1:7001"); !ok || current != newSession {
		t.Fatalf("replacement session should remain installed, current=%v ok=%v", current, ok)
	}
}

func TestUdpModeServerLoadActiveSessionDropsClosedStaleEntry(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	sessionCtx, cancel := context.WithCancel(context.Background())
	session := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    sessionCtx,
		cancel: cancel,
	}
	server.sessions.Store("127.0.0.1:7002", session)
	cancel()

	if got := server.loadActiveSession("127.0.0.1:7002"); got != nil {
		t.Fatalf("loadActiveSession() = %#v, want nil for closed session", got)
	}
	if _, ok := server.sessions.Load("127.0.0.1:7002"); ok {
		t.Fatal("closed stale session should be removed from session map")
	}
}

func TestUdpModeServerLoadActiveSessionInvalidEntryDoesNotDeleteReplacement(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	sessionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacement := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    sessionCtx,
		cancel: cancel,
	}
	server.sessions.Store("127.0.0.1:7003", replacement)

	if deleted := server.deleteSessionValueIfCurrent("127.0.0.1:7003", "stale-invalid-entry"); deleted {
		t.Fatal("deleteSessionValueIfCurrent should not remove a replacement session")
	}
	if got := server.loadActiveSession("127.0.0.1:7003"); got != replacement {
		t.Fatalf("loadActiveSession() = %#v, want replacement session", got)
	}
	if current, ok := server.sessions.Load("127.0.0.1:7003"); !ok || current != replacement {
		t.Fatalf("replacement session should remain installed, current=%v ok=%v", current, ok)
	}
}

func TestP2PServerAcquireWorkerUnblocksOnClose(t *testing.T) {
	s := NewP2PServer(0, false)
	s.workers = make(chan struct{}, 1)
	s.workers <- struct{}{}

	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- s.acquireWorker()
	}()

	select {
	case ok := <-resultCh:
		t.Fatalf("acquireWorker() returned early = %v, want blocked until Close()", ok)
	case <-time.After(50 * time.Millisecond):
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case ok := <-resultCh:
		if ok {
			t.Fatal("acquireWorker() = true after Close(), want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquireWorker() should unblock after Close()")
	}
}

func TestP2PServerStartRejectsMissingRuntime(t *testing.T) {
	var nilServer *P2PServer
	if err := nilServer.Start(); !errors.Is(err, errP2PServerUnavailable) {
		t.Fatalf("(*P2PServer)(nil).Start() error = %v, want wrapped %v", err, errP2PServerUnavailable)
	}
	if err := nilServer.StartBackground(); !errors.Is(err, errP2PServerUnavailable) {
		t.Fatalf("(*P2PServer)(nil).StartBackground() error = %v, want wrapped %v", err, errP2PServerUnavailable)
	}

	zeroServer := &P2PServer{}
	if err := zeroServer.Start(); !errors.Is(err, errP2PServerUnavailable) {
		t.Fatalf("zero P2PServer.Start() error = %v, want wrapped %v", err, errP2PServerUnavailable)
	}
	if err := zeroServer.StartBackground(); !errors.Is(err, errP2PServerUnavailable) {
		t.Fatalf("zero P2PServer.StartBackground() error = %v, want wrapped %v", err, errP2PServerUnavailable)
	}
	if zeroServer.acquireWorker() {
		t.Fatal("zero P2PServer.acquireWorker() = true, want false")
	}
}

func TestP2PServerStartBackgroundReturnsBootstrapError(t *testing.T) {
	wantErr := errors.New("listen failed")
	oldListen := p2pProbeListenPorts
	p2pProbeListenPorts = func(int) ([]*net.UDPConn, error) {
		return nil, wantErr
	}
	defer func() {
		p2pProbeListenPorts = oldListen
	}()

	server := NewP2PServer(9000, false)
	err := server.StartBackground()
	if !errors.Is(err, wantErr) {
		t.Fatalf("StartBackground() error = %v, want wrapped %v", err, wantErr)
	}
}

func TestP2PServerRunListenersWaitsForAllClosedListeners(t *testing.T) {
	listenerA, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(listenerA) error = %v", err)
	}
	listenerB, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		_ = listenerA.Close()
		t.Fatalf("ListenUDP(listenerB) error = %v", err)
	}
	t.Cleanup(func() {
		_ = listenerA.Close()
		_ = listenerB.Close()
	})

	server := NewP2PServer(0, false)
	server.listeners = []*net.UDPConn{listenerA, listenerB}
	server.ports = []int{
		listenerA.LocalAddr().(*net.UDPAddr).Port,
		listenerB.LocalAddr().(*net.UDPAddr).Port,
	}

	done := make(chan error, 1)
	go func() {
		done <- server.runListeners()
	}()

	if err := listenerA.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listenerA.Close() error = %v", err)
	}

	select {
	case err := <-done:
		t.Fatalf("runListeners() returned early after first closed listener: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := listenerB.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listenerB.Close() error = %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runListeners() error = %v, want nil after all listeners close", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runListeners() did not return after all listeners closed")
	}
}
