package pmux

import (
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/logs"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port failed: %v", err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type: %T", l.Addr())
	}
	return addr.Port
}

func TestPortMux_ListenersAndClose(t *testing.T) {
	logs.Init("stdout", "trace", "", 0, 0, 0, false, true)

	port := getFreePort(t)
	pMux := NewPortMux("127.0.0.1", port, "Ds", "Cs", "/ws")
	if err := pMux.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if pMux.Listener == nil {
		t.Fatal("port mux listener not initialized")
	}
	if addr, ok := pMux.Addr().(*net.TCPAddr); !ok || !addr.IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("port mux bound to unexpected addr: %#v", pMux.Addr())
	}

	if pMux.GetClientListener() == nil {
		t.Fatal("client listener is nil")
	}
	if pMux.GetClientTlsListener() == nil {
		t.Fatal("client tls listener is nil")
	}
	if pMux.GetHttpListener() == nil {
		t.Fatal("http listener is nil")
	}
	if pMux.GetHttpsListener() == nil {
		t.Fatal("https listener is nil")
	}
	if pMux.GetManagerListener() == nil {
		t.Fatal("manager listener is nil")
	}
	if pMux.GetManagerTLSListener() == nil {
		t.Fatal("manager tls listener is nil")
	}

	if err := pMux.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestPortMux_CloseWithoutListeners(t *testing.T) {
	pMux := &PortMux{}
	if err := pMux.Close(); err != nil {
		t.Fatalf("close without listeners failed: %v", err)
	}
}

func TestPortMux_ProcessNilConn(t *testing.T) {
	pMux := &PortMux{}
	pMux.process(nil)
}

func TestPortListenerAcceptUnblocksOnClose(t *testing.T) {
	route := newPortRoute(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345})
	l := NewPortListener(route)

	errCh := make(chan error, 1)
	go func() {
		_, err := l.Accept()
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := l.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Accept() error = nil, want net.ErrClosed")
		}
	case <-time.After(time.Second):
		t.Fatal("Accept() did not unblock after Close()")
	}
}

func TestClassifyHTTPRequestUsesRegisteredRoutes(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "web.example.com", "bridge.example.com", "/ws")
	managerRoute := pMux.ensureRoute(routeManager)
	httpRoute := pMux.ensureRoute(routeHTTP)
	wsRoute := pMux.ensureRoute(routeClientWS)

	if got := pMux.classifyHTTPRequest(httpRequestMeta{host: "Web.Example.Com:80", path: "/"}); got != managerRoute {
		t.Fatalf("classifyHTTPRequest(web) = %#v, want manager route", got)
	}
	if got := pMux.classifyHTTPRequest(httpRequestMeta{
		host:       "Bridge.Example.Com",
		path:       "/ws",
		connection: "keep-alive, Upgrade",
		upgrade:    "websocket",
	}); got != wsRoute {
		t.Fatalf("classifyHTTPRequest(bridge ws) = %#v, want ws route", got)
	}
	if got := pMux.classifyHTTPRequest(httpRequestMeta{
		host:       "bridge.example.com",
		path:       "/not-ws",
		connection: "keep-alive",
	}); got != httpRoute {
		t.Fatalf("classifyHTTPRequest(non-ws bridge host) = %#v, want http route", got)
	}
	if got := pMux.classifyHTTPRequest(httpRequestMeta{host: "other.example.com", path: "/"}); got != httpRoute {
		t.Fatalf("classifyHTTPRequest(other) = %#v, want http route", got)
	}
}

func TestClassifyHTTPRequestNormalizesBridgePath(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "web.example.com", "bridge.example.com", "ws")
	wsRoute := pMux.ensureRoute(routeClientWS)
	httpRoute := pMux.ensureRoute(routeHTTP)

	if got := pMux.classifyHTTPRequest(httpRequestMeta{
		host:       "bridge.example.com",
		path:       "/ws",
		connection: "Upgrade",
		upgrade:    "websocket",
	}); got != wsRoute {
		t.Fatalf("classifyHTTPRequest(normalized bridge path) = %#v, want websocket route", got)
	}

	if got := pMux.classifyHTTPRequest(httpRequestMeta{
		host:       "bridge.example.com",
		path:       "ws",
		connection: "Upgrade",
		upgrade:    "websocket",
	}); got != httpRoute {
		t.Fatalf("classifyHTTPRequest(invalid request path) = %#v, want http fallback route", got)
	}
}

func TestClassifyHTTPRequestReservesHTTPSOnlyManagerHost(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "web.example.com", "bridge.example.com", "/ws")
	httpsRoute := pMux.ensureRoute(routeHTTPS)
	_ = pMux.ensureRoute(routeHTTP)
	_ = pMux.ensureRoute(routeManagerTLS)

	if got := pMux.classifyHTTPRequest(httpRequestMeta{host: "web.example.com", path: "/"}); got != httpsRoute {
		t.Fatalf("classifyHTTPRequest(https-only manager host) = %#v, want https route", got)
	}
}

func classifyTLSForTest(t *testing.T, pMux *PortMux, serverName string) *portRoute {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()

	errCh := make(chan error, 1)
	go func() {
		defer func() { _ = clientConn.Close() }()
		cfg := &tls.Config{InsecureSkipVerify: true}
		if serverName != "" {
			cfg.ServerName = serverName
		}
		errCh <- tls.Client(clientConn, cfg).Handshake()
	}()

	first := make([]byte, 3)
	if _, err := io.ReadFull(serverConn, first); err != nil {
		t.Fatalf("failed to read TLS prefix: %v", err)
	}
	route, _, _ := pMux.classifyTLS(serverConn, first)
	_ = serverConn.Close()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("TLS client handshake did not finish")
	}
	return route
}

func TestClassifyTLSUsesRegisteredRoutes(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "web.example.com", "bridge.example.com", "/ws")
	managerTLSRoute := pMux.ensureRoute(routeManagerTLS)
	clientWSSRoute := pMux.ensureRoute(routeClientWSS)
	httpsRoute := pMux.ensureRoute(routeHTTPS)

	if got := classifyTLSForTest(t, pMux, "web.example.com"); got != managerTLSRoute {
		t.Fatalf("classifyTLS(web) = %#v, want manager TLS route", got)
	}
	if got := classifyTLSForTest(t, pMux, "bridge.example.com"); got != clientWSSRoute {
		t.Fatalf("classifyTLS(bridge wss) = %#v, want client WSS route", got)
	}
	if got := classifyTLSForTest(t, pMux, "other.example.com"); got != httpsRoute {
		t.Fatalf("classifyTLS(other) = %#v, want https route", got)
	}
}

func TestClassifyTLSUsesReservedBridgeTLSRoute(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "web.example.com", "bridge.example.com", "/ws")
	managerTLSRoute := pMux.ensureRoute(routeManagerTLS)
	reservedRoute := pMux.ensureRoute(routeClientReservedTLS)
	httpsRoute := pMux.ensureRoute(routeHTTPS)

	if got := classifyTLSForTest(t, pMux, "bridge.example.com"); got != reservedRoute {
		t.Fatalf("classifyTLS(bridge reserved) = %#v, want reserved bridge TLS route", got)
	}
	if got := classifyTLSForTest(t, pMux, "web.example.com"); got != managerTLSRoute {
		t.Fatalf("classifyTLS(web reserved setup) = %#v, want manager TLS route", got)
	}
	if got := classifyTLSForTest(t, pMux, "other.example.com"); got != httpsRoute {
		t.Fatalf("classifyTLS(other reserved setup) = %#v, want https route", got)
	}
}

func TestClassifyTLSUsesBridgeTLSRouteOnEmptySNI(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "", "", "")
	clientTLSRoute := pMux.ensureRoute(routeClientTLS)

	if got := classifyTLSForTest(t, pMux, ""); got != clientTLSRoute {
		t.Fatalf("classifyTLS(empty sni) = %#v, want client TLS route", got)
	}
}

func TestClassifyTLSRejectsEmptySNIOnSharedTLSGroup(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "web.example.com", "bridge.example.com", "/ws")
	_ = pMux.ensureRoute(routeClientTLS)
	_ = pMux.ensureRoute(routeHTTPS)

	if got := classifyTLSForTest(t, pMux, ""); got != nil {
		t.Fatalf("classifyTLS(empty sni shared group) = %#v, want nil", got)
	}
}

func TestEnsureRouteReusesExistingRoute(t *testing.T) {
	pMux := NewPortMux("127.0.0.1", 0, "", "", "")
	first := pMux.ensureRoute(routeHTTP)
	second := pMux.ensureRoute(routeHTTP)
	if first == nil || second == nil {
		t.Fatal("ensureRoute() returned nil")
	}
	if first != second {
		t.Fatal("ensureRoute() should reuse the existing route")
	}
}

func TestPortConnReadConsumesPrefixAndSocketInOneCall(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = clientConn.Write([]byte("def"))
		_ = clientConn.Close()
	}()

	conn := newPortConn(serverConn, []byte("abc"))
	buf := make([]byte, 6)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 6 {
		t.Fatalf("Read() bytes = %d, want 6", n)
	}
	if string(buf[:n]) != "abcdef" {
		t.Fatalf("Read() = %q, want %q", string(buf[:n]), "abcdef")
	}

	<-done
}

func TestPortMuxProcessClosesIdleConnAfterClassificationTimeout(t *testing.T) {
	oldTimeout := classifyReadTimeout
	classifyReadTimeout = 50 * time.Millisecond
	defer func() { classifyReadTimeout = oldTimeout }()

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	pMux := NewPortMux("127.0.0.1", 0, "", "", "")
	done := make(chan struct{})
	go func() {
		pMux.process(serverConn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("process() did not return after classification timeout")
	}
}
