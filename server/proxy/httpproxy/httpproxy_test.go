package httpproxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
	serverproxy "github.com/djylb/nps/server/proxy"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func TestChangeRedirectURL(t *testing.T) {
	s := &HttpProxy{}

	req := &http.Request{
		Host:       "example.com:8080",
		RemoteAddr: "10.0.0.1:52345",
		RequestURI: "/old/path?a=1&b=2",
		URL: &url.URL{
			Path:     "/old/path",
			RawQuery: "a=1&b=2",
		},
		Header: http.Header{
			"X-Forwarded-For": []string{"1.1.1.1", "2.2.2.2"},
		},
	}

	t.Run("replaces template variables", func(t *testing.T) {
		got := s.ChangeRedirectURL(req, "https://${host}/new?from=${request_uri}&xff=${proxy_add_x_forwarded_for}&ip=${remote_ip}")
		want := "https://example.com/new?from=/old/path?a=1&b=2&xff=1.1.1.1, 2.2.2.2, 10.0.0.1&ip=10.0.0.1"
		if got != want {
			t.Fatalf("ChangeRedirectURL() = %q, want %q", got, want)
		}
	})

	t.Run("returns html-unescaped literal when no template", func(t *testing.T) {
		got := s.ChangeRedirectURL(req, " https://static.example.com/a?x=1&amp;y=2 ")
		want := "https://static.example.com/a?x=1&y=2"
		if got != want {
			t.Fatalf("ChangeRedirectURL() = %q, want %q", got, want)
		}
	})
}

func TestHttpProxyCloseAllowsNilCache(t *testing.T) {
	s := &HttpProxy{}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestHttpProxyStartRejectsMissingRuntime(t *testing.T) {
	var nilProxy *HttpProxy
	if err := nilProxy.Start(); !errors.Is(err, errHTTPProxyUnavailable) {
		t.Fatalf("(*HttpProxy)(nil).Start() error = %v, want wrapped %v", err, errHTTPProxyUnavailable)
	}

	if err := (&HttpProxy{}).Start(); !errors.Is(err, errHTTPProxyUnavailable) {
		t.Fatalf("zero HttpProxy.Start() error = %v, want wrapped %v", err, errHTTPProxyUnavailable)
	}
}

func TestHttpProxyCloseHandlesNilReceiver(t *testing.T) {
	var nilProxy *HttpProxy
	if err := nilProxy.Close(); err != nil {
		t.Fatalf("(*HttpProxy)(nil).Close() error = %v, want nil", err)
	}
}

func TestAuthorizeHTTPProxyRequestRequiresAuthForUpgradeRequests(t *testing.T) {
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer: &serverproxy.BaseServer{},
		},
	}
	host := &file.Host{
		Client: &file.Client{
			Cnf: &file.Config{U: "demo", P: "secret"},
		},
	}

	t.Run("rejects unauthorized upgrade request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
		req.Header.Set("Upgrade", "websocket")
		recorder := httptest.NewRecorder()

		if ok := server.authorizeHTTPProxyRequest(recorder, req, host); ok {
			t.Fatal("authorizeHTTPProxyRequest() = true, want false for unauthorized upgrade request")
		}
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("authorizeHTTPProxyRequest() status = %d, want %d", recorder.Code, http.StatusUnauthorized)
		}
		if got := recorder.Header().Get("WWW-Authenticate"); got == "" {
			t.Fatal("authorizeHTTPProxyRequest() missing WWW-Authenticate header")
		}
	})

	t.Run("accepts authorized upgrade request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("demo:secret")))
		recorder := httptest.NewRecorder()

		if ok := server.authorizeHTTPProxyRequest(recorder, req, host); !ok {
			t.Fatal("authorizeHTTPProxyRequest() = false, want true for authorized upgrade request")
		}
	})

	t.Run("allows upgrade request when auth is disabled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/ws", nil)
		req.Header.Set("Upgrade", "websocket")
		recorder := httptest.NewRecorder()

		if ok := server.authorizeHTTPProxyRequest(recorder, req, &file.Host{
			Client: &file.Client{
				Cnf: &file.Config{},
			},
		}); !ok {
			t.Fatal("authorizeHTTPProxyRequest() = false, want true when auth is disabled")
		}
	})
}

func TestHttpsListenerAcceptReturnsClosedErrorAfterClose(t *testing.T) {
	listener := NewHttpsListener(&net.TCPListener{})
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
		t.Fatal("Accept() should not return a connection after close")
	}
}

func TestHttpsListenerDeliverReturnsFalseAfterClose(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })

	listener := NewHttpsListener(&net.TCPListener{})
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	tlsConn := tls.Client(left, &tls.Config{InsecureSkipVerify: true})
	if ok := listener.Deliver(tlsConn); ok {
		_ = tlsConn.Close()
		t.Fatal("Deliver() = true, want false after listener close")
	}
}

func TestChangeHostAndHeader(t *testing.T) {
	s := &HttpProxy{}

	req := &http.Request{
		Host:       "demo.local:8080",
		RemoteAddr: "192.168.1.9:6000",
		RequestURI: "/api/v1?q=ok",
		URL: &url.URL{
			Path:     "/api/v1",
			RawQuery: "q=ok",
		},
		Header: http.Header{
			"Origin":          []string{"http://demo.local:8080"},
			"X-Forwarded-For": []string{"8.8.8.8"},
			"X-Remove-Me":     []string{"to-delete"},
		},
	}

	headerRules := strings.Join([]string{
		"X-Test-IP: ${remote_ip}",
		"X-Test-URI: ${request_uri}",
		"X-Test-XFF: ${proxy_add_x_forwarded_for}",
		"X-Remove-Me: ${unset}",
	}, "\n")

	s.ChangeHostAndHeader(req, "upstream.example.com", headerRules, true)

	if req.Host != "upstream.example.com" {
		t.Fatalf("Host = %q, want %q", req.Host, "upstream.example.com")
	}
	if got := req.Header.Get("Origin"); got != "http://upstream.example.com" {
		t.Fatalf("Origin = %q, want %q", got, "http://upstream.example.com")
	}
	if got := req.Header.Get("X-Test-IP"); got != "192.168.1.9" {
		t.Fatalf("X-Test-IP = %q, want %q", got, "192.168.1.9")
	}
	if got := req.Header.Get("X-Test-URI"); got != "/api/v1?q=ok" {
		t.Fatalf("X-Test-URI = %q, want %q", got, "/api/v1?q=ok")
	}
	if got := req.Header.Get("X-Test-XFF"); got != "8.8.8.8, 192.168.1.9" {
		t.Fatalf("X-Test-XFF = %q, want %q", got, "8.8.8.8, 192.168.1.9")
	}
	if got := req.Header.Get("X-Remove-Me"); got != "" {
		t.Fatalf("X-Remove-Me = %q, want empty", got)
	}
}

func TestChangeResponseHeader(t *testing.T) {
	s := &HttpProxy{}

	t.Run("handles nil response safely", func(t *testing.T) {
		s.ChangeResponseHeader(nil, "X-Test: abc")
	})

	req := &http.Request{
		Method:     http.MethodGet,
		Host:       "resp.example.com:8443",
		RemoteAddr: "127.0.0.1:3456",
		RequestURI: "/hello?foo=bar",
		URL: &url.URL{
			Path:     "/hello",
			RawQuery: "foo=bar",
		},
		TLS: &tls.ConnectionState{},
		Header: http.Header{
			"Origin": []string{"https://origin.example.com"},
		},
	}
	resp := &http.Response{
		Status:        "201 Created",
		StatusCode:    201,
		ContentLength: 99,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Via":          []string{"test-via"},
			"X-Delete-Me":  []string{"bye"},
		},
		Request: req,
	}

	headerRules := strings.Join([]string{
		"X-Resp-Scheme: ${scheme}",
		"X-Resp-Code: ${status_code}",
		"X-Resp-Origin: ${origin}",
		"X-Resp-Date: ${date}",
		"X-Delete-Me: ${unset}",
	}, "\n")
	s.ChangeResponseHeader(resp, headerRules)

	if got := resp.Header.Get("X-Resp-Scheme"); got != "https" {
		t.Fatalf("X-Resp-Scheme = %q, want %q", got, "https")
	}
	if got := resp.Header.Get("X-Resp-Code"); got != "201" {
		t.Fatalf("X-Resp-Code = %q, want %q", got, "201")
	}
	if got := resp.Header.Get("X-Resp-Origin"); got != "https://origin.example.com" {
		t.Fatalf("X-Resp-Origin = %q, want %q", got, "https://origin.example.com")
	}
	if got := resp.Header.Get("X-Resp-Date"); got == "" {
		t.Fatal("X-Resp-Date is empty, want RFC1123 http date")
	}
	if got := resp.Header.Get("X-Delete-Me"); got != "" {
		t.Fatalf("X-Delete-Me = %q, want empty", got)
	}
}

func TestHTTPProxyContextStringHandlesMissingOrInvalidValues(t *testing.T) {
	if got, ok := httpProxyContextString(nil, ctxSNI); ok || got != "" {
		t.Fatalf("httpProxyContextString(nil) = %q, %v, want empty/false", got, ok)
	}

	ctx := context.WithValue(context.Background(), ctxSNI, 123)
	if got, ok := httpProxyContextString(ctx, ctxSNI); ok || got != "" {
		t.Fatalf("httpProxyContextString(invalid) = %q, %v, want empty/false", got, ok)
	}

	ctx = context.WithValue(context.Background(), ctxSNI, "api.example.com")
	if got, ok := httpProxyContextString(ctx, ctxSNI); !ok || got != "api.example.com" {
		t.Fatalf("httpProxyContextString(valid) = %q, %v, want api.example.com/true", got, ok)
	}
}

func TestHTTPProxyRequestDataContextExposesCombinedValues(t *testing.T) {
	host := &file.Host{
		Id:     5,
		Client: &file.Client{Id: 9, Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:   &file.Flow{},
		Target: &file.Target{TargetStr: "127.0.0.1:80"},
	}
	routeRuntime := (&serverproxy.BaseServer{}).NewRouteRuntimeContext(host.Client, "node-a")
	ctx := withHTTPProxyRequestData(context.Background(), httpProxyRequestContext{
		remoteAddr:   " 203.0.113.10:4000 ",
		host:         host,
		sni:          " backend.example.com ",
		routeRuntime: routeRuntime,
		selection: backendSelection{
			routeUUID:  " node-a ",
			targetAddr: " 127.0.0.1:8080 ",
		},
	})

	if got, ok := httpProxyContextString(ctx, ctxRemoteAddr); !ok || got != "203.0.113.10:4000" {
		t.Fatalf("httpProxyContextString(remote) = %q, %v, want 203.0.113.10:4000/true", got, ok)
	}
	if got, ok := httpProxyContextString(ctx, ctxSNI); !ok || got != "backend.example.com" {
		t.Fatalf("httpProxyContextString(sni) = %q, %v, want backend.example.com/true", got, ok)
	}
	if got, ok := httpProxyContextHost(ctx); !ok || got != host {
		t.Fatalf("httpProxyContextHost() = %#v, %v, want original host/true", got, ok)
	}
	if got, ok := httpProxyContextRouteRuntime(ctx); !ok || got != routeRuntime {
		t.Fatalf("httpProxyContextRouteRuntime() = %#v, %v, want original route runtime/true", got, ok)
	}
	if selection, ok := httpProxyContextBackendSelection(ctx); !ok || selection.routeUUID != "node-a" || selection.targetAddr != "127.0.0.1:8080" {
		t.Fatalf("httpProxyContextBackendSelection() = %#v, %v, want normalized payload selection", selection, ok)
	}
}

func TestHttpServerDialContextRejectsMissingContextValues(t *testing.T) {
	server := &HttpServer{
		HttpProxy: &HttpProxy{
			BaseServer: &serverproxy.BaseServer{},
		},
	}

	if _, err := server.DialContext(context.Background(), "tcp", "ignored"); err == nil || !strings.Contains(err.Error(), "missing remote address context") {
		t.Fatalf("DialContext() error = %v, want missing remote address context", err)
	}

	ctx := context.WithValue(context.Background(), ctxRemoteAddr, "127.0.0.1:1234")
	if _, err := server.DialContext(ctx, "tcp", "ignored"); err == nil || !strings.Contains(err.Error(), "missing host context") {
		t.Fatalf("DialContext() error = %v, want missing host context", err)
	}

	ctx = context.WithValue(context.Background(), ctxRemoteAddr, 12345)
	if _, err := server.DialContext(ctx, "tcp", "ignored"); err == nil || !strings.Contains(err.Error(), "missing remote address context") {
		t.Fatalf("DialContext() invalid remote error = %v, want missing remote address context", err)
	}
}

func withHTTPSListenerEnqueueTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	old := httpsListenerEnqueueTimeout
	httpsListenerEnqueueTimeout = timeout
	t.Cleanup(func() {
		httpsListenerEnqueueTimeout = old
	})
}

func newTestTLSConnPair(t *testing.T) (*tls.Conn, net.Conn) {
	t.Helper()
	left, right := net.Pipe()
	return tls.Client(left, &tls.Config{InsecureSkipVerify: true}), right
}

func TestHttpsListenerDeliverWaitsForCapacity(t *testing.T) {
	withHTTPSListenerEnqueueTimeout(t, 500*time.Millisecond)

	listener := newHTTPSListenerWithBuffer(&net.TCPListener{}, 1)
	t.Cleanup(func() { _ = listener.Close() })

	firstConn, firstPeer := newTestTLSConnPair(t)
	secondConn, secondPeer := newTestTLSConnPair(t)
	t.Cleanup(func() { _ = firstPeer.Close() })
	t.Cleanup(func() { _ = secondPeer.Close() })

	if ok := listener.Deliver(firstConn); !ok {
		_ = firstConn.Close()
		t.Fatal("Deliver(first) = false, want true")
	}

	resultCh := make(chan bool, 1)
	go func() {
		resultCh <- listener.Deliver(secondConn)
	}()

	select {
	case ok := <-resultCh:
		if !ok {
			_ = secondConn.Close()
		}
		t.Fatalf("Deliver(second) returned before capacity opened, ok=%v", ok)
	case <-time.After(50 * time.Millisecond):
	}

	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	_ = accepted.Close()

	select {
	case ok := <-resultCh:
		if !ok {
			_ = secondConn.Close()
			t.Fatal("Deliver(second) = false, want true after capacity opened")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver(second) did not complete after capacity opened")
	}

	accepted, err = listener.Accept()
	if err != nil {
		t.Fatalf("second Accept() error = %v", err)
	}
	_ = accepted.Close()
}

func TestHttpsListenerDeliverFailsWhenQueueStaysFull(t *testing.T) {
	withHTTPSListenerEnqueueTimeout(t, 60*time.Millisecond)

	listener := newHTTPSListenerWithBuffer(&net.TCPListener{}, 1)
	t.Cleanup(func() { _ = listener.Close() })

	firstConn, firstPeer := newTestTLSConnPair(t)
	secondConn, secondPeer := newTestTLSConnPair(t)
	t.Cleanup(func() { _ = firstPeer.Close() })
	t.Cleanup(func() { _ = secondPeer.Close() })

	if ok := listener.Deliver(firstConn); !ok {
		_ = firstConn.Close()
		t.Fatal("Deliver(first) = false, want true")
	}

	start := time.Now()
	if ok := listener.Deliver(secondConn); ok {
		t.Fatal("Deliver(second) = true, want false while queue remains full")
	}
	_ = secondConn.Close()

	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("Deliver(second) elapsed = %v, want bounded wait before failure", elapsed)
	}

	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	_ = accepted.Close()
}

type stubPacketConn struct {
	closed bool
}

func (s *stubPacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, net.ErrClosed }
func (s *stubPacketConn) WriteTo([]byte, net.Addr) (int, error)  { return 0, net.ErrClosed }
func (s *stubPacketConn) Close() error {
	s.closed = true
	return nil
}
func (s *stubPacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (s *stubPacketConn) SetDeadline(time.Time) error      { return nil }
func (s *stubPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (s *stubPacketConn) SetWriteDeadline(time.Time) error { return nil }

func TestHttp3ServerCloseHandlesNilRuntime(t *testing.T) {
	pc := &stubPacketConn{}
	s := &Http3Server{
		http3Listener: pc,
	}
	s.http3Status.Store(true)

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !pc.closed {
		t.Fatal("Close() did not close packet listener")
	}
	if s.http3Status.Load() {
		t.Fatal("Close() did not reset http3Status")
	}
}

func TestHttp3ServerCloseClosesTrackedBridgeQUICSessions(t *testing.T) {
	crypt.InitTls(tls.Certificate{})

	listener, err := quic.ListenAddr("127.0.0.1:0", crypt.GetCertCfg(), &quic.Config{})
	if err != nil {
		t.Fatalf("quic.ListenAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	serverSessCh := make(chan *quic.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		sess, err := listener.Accept(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		serverSessCh <- sess
	}()

	clientSess, err := quic.DialAddr(context.Background(), listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic.DialAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientSess.CloseWithError(0, "test cleanup")
	})

	var serverSess *quic.Conn
	select {
	case err := <-errCh:
		t.Fatalf("listener.Accept() error = %v", err)
	case serverSess = <-serverSessCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for accepted quic session")
	}

	s := NewHttp3Server(nil, &stubPacketConn{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serveBridgeQUIC(serverSess)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveBridgeQUIC() should exit after Close()")
	}

	select {
	case <-serverSess.Context().Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server quic session should be closed by Close()")
	}
}

func TestHttp3ServerCloseWaitsForActiveQUICHandlers(t *testing.T) {
	pc := &stubPacketConn{}
	started := make(chan struct{})
	release := make(chan struct{})
	s := &Http3Server{
		http3Listener: pc,
		handleQUICHook: func(*quic.Conn, string, string) {
			close(started)
			<-release
		},
	}
	s.http3Status.Store(true)
	s.dispatchHandleQUIC(nil, "h3", "")

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchHandleQUIC() did not start handler")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- s.Close()
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned before active QUIC handler exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not wait for active QUIC handler")
	}

	if !pc.closed {
		t.Fatal("Close() did not close packet listener")
	}
	if s.http3Status.Load() {
		t.Fatal("Close() did not reset http3Status")
	}
}

func TestHttp3ServerRuntimeContextStaysCanceledAfterClose(t *testing.T) {
	s := NewHttp3Server(nil, &stubPacketConn{})

	ctx := s.runtimeContext()
	select {
	case <-ctx.Done():
		t.Fatal("runtimeContext() should be live before Close()")
	default:
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("Close() should cancel the active runtime context")
	}

	closedCtx := s.runtimeContext()
	select {
	case <-closedCtx.Done():
	default:
		t.Fatal("runtimeContext() should stay canceled after Close()")
	}
}

func TestHttp3ServerPrepareRuntimeReopensContextAfterClose(t *testing.T) {
	s := NewHttp3Server(nil, &stubPacketConn{})

	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	s.prepareRuntime()
	ctx := s.runtimeContext()
	select {
	case <-ctx.Done():
		t.Fatal("prepareRuntime() should reopen runtime context after Close()")
	default:
	}
}

func TestHttp3ServerPrepareQUICWorkerDispatchReopensDispatchAfterClose(t *testing.T) {
	started := make(chan struct{}, 1)
	s := &Http3Server{
		handleQUICHook: func(*quic.Conn, string, string) {
			select {
			case started <- struct{}{}:
			default:
			}
		},
	}

	s.beginQUICWorkerShutdown()
	s.dispatchHandleQUIC(nil, "h3", "")

	select {
	case <-started:
		t.Fatal("dispatchHandleQUIC() should reject workers while shutdown is active")
	case <-time.After(50 * time.Millisecond):
	}

	s.prepareQUICWorkerDispatch()
	s.dispatchHandleQUIC(nil, "h3", "")

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("prepareQUICWorkerDispatch() should reopen worker dispatch")
	}
	s.waitQUICWorkers()
}

func TestHttp3ServerHandleQUICHandlesNilState(t *testing.T) {
	var nilServer *Http3Server
	nilServer.HandleQUIC(nil, http3.NextProtoH3, "")

	server := &Http3Server{}
	server.HandleQUIC(nil, http3.NextProtoH3, "")
	server.serveBridgeQUIC(nil)

	cfg, err := nilServer.GetConfigForClient(nil)
	if err != nil {
		t.Fatalf("nil GetConfigForClient() error = %v, want nil", err)
	}
	if cfg != nil {
		t.Fatalf("nil GetConfigForClient() = %#v, want nil", cfg)
	}

	cfg, err = server.GetConfigForClient(nil)
	if err != nil {
		t.Fatalf("GetConfigForClient(nil) error = %v, want nil", err)
	}
	if cfg != nil {
		t.Fatalf("GetConfigForClient(nil) = %#v, want nil", cfg)
	}
}

func TestHTTPRuntimeServersStartRejectsMissingRuntime(t *testing.T) {
	var nilHTTP *HttpServer
	if err := nilHTTP.Start(); !errors.Is(err, errHTTPServerUnavailable) {
		t.Fatalf("(*HttpServer)(nil).Start() error = %v, want wrapped %v", err, errHTTPServerUnavailable)
	}

	if err := (&HttpServer{}).Start(); !errors.Is(err, errHTTPServerUnavailable) {
		t.Fatalf("zero HttpServer.Start() error = %v, want wrapped %v", err, errHTTPServerUnavailable)
	}

	var nilHTTPS *HttpsServer
	if err := nilHTTPS.Start(); !errors.Is(err, errHTTPSServerUnavailable) {
		t.Fatalf("(*HttpsServer)(nil).Start() error = %v, want wrapped %v", err, errHTTPSServerUnavailable)
	}

	if err := (&HttpsServer{}).Start(); !errors.Is(err, errHTTPSServerUnavailable) {
		t.Fatalf("zero HttpsServer.Start() error = %v, want wrapped %v", err, errHTTPSServerUnavailable)
	}

	var nilHTTP3 *Http3Server
	if err := nilHTTP3.Start(); !errors.Is(err, errHTTP3ServerUnavailable) {
		t.Fatalf("(*Http3Server)(nil).Start() error = %v, want wrapped %v", err, errHTTP3ServerUnavailable)
	}

	if err := (&Http3Server{}).Start(); !errors.Is(err, errHTTP3ServerUnavailable) {
		t.Fatalf("zero Http3Server.Start() error = %v, want wrapped %v", err, errHTTP3ServerUnavailable)
	}
}

func TestHttp3ServerGetConfigForClientHandlesNilHost(t *testing.T) {
	httpProxy := NewHttpProxyWithRoots(nil, 80, 443, 9443, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	}, func() httpProxyDB {
		return &httpProxyDBStub{}
	})
	server := &Http3Server{
		HttpsServer: &HttpsServer{
			HttpServer: &HttpServer{HttpProxy: httpProxy},
		},
	}

	cfg, err := server.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "nil.example.com"})
	if err != nil {
		t.Fatalf("GetConfigForClient() error = %v, want nil", err)
	}
	if cfg != nil {
		t.Fatalf("GetConfigForClient() = %#v, want nil config for nil host lookup", cfg)
	}
}

func TestHttp3ServerGetConfigForClientRejectsTLSOffloadHost(t *testing.T) {
	httpProxy := NewHttpProxyWithRoots(nil, 80, 443, 9443, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	}, func() httpProxyDB {
		return &httpProxyDBStub{
			certHost: &file.Host{
				Id:         3,
				Host:       "tls.example.com",
				TlsOffload: true,
				AutoSSL:    true,
			},
		}
	})
	server := &Http3Server{
		HttpsServer: &HttpsServer{
			HttpServer:   &HttpServer{HttpProxy: httpProxy},
			certMagicTls: &tls.Config{},
		},
	}

	cfg, err := server.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "tls.example.com"})
	if err != nil {
		t.Fatalf("GetConfigForClient() error = %v, want nil", err)
	}
	if cfg != nil {
		t.Fatalf("GetConfigForClient() = %#v, want nil config for tls offload host", cfg)
	}
}

type startErrorListener struct {
	addr net.Addr
	err  error
}

func (l *startErrorListener) Accept() (net.Conn, error) { return nil, l.err }

func (l *startErrorListener) Close() error { return nil }

func (l *startErrorListener) Addr() net.Addr { return l.addr }

func TestHttpProxyStartReturnsHTTPInitError(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	wantErr := errors.New("http listener unavailable")
	oldGetter := httpProxyGetHTTPListener
	httpProxyGetHTTPListener = func() (net.Listener, error) {
		return nil, wantErr
	}
	t.Cleanup(func() {
		httpProxyGetHTTPListener = oldGetter
	})

	s := &HttpProxy{
		BaseServer: &serverproxy.BaseServer{},
		HttpPort:   80,
	}
	err := s.Start()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want wrapped %v", err, wantErr)
	}
}

func TestHttpProxyStartReturnsSubserverError(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	wantErr := errors.New("accept failed")
	oldGetter := httpProxyGetHTTPListener
	httpProxyGetHTTPListener = func() (net.Listener, error) {
		return &startErrorListener{addr: stubAddr("127.0.0.1:80"), err: wantErr}, nil
	}
	t.Cleanup(func() {
		httpProxyGetHTTPListener = oldGetter
	})

	s := &HttpProxy{
		BaseServer: &serverproxy.BaseServer{},
		HttpPort:   80,
	}
	err := s.Start()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start() error = %v, want wrapped %v", err, wantErr)
	}
}

func TestHttpProxyRunRuntimeServersWaitsForAllWorkersAfterClose(t *testing.T) {
	stopRequested := make(chan struct{})
	stopObserved := make(chan struct{})
	allowExit := make(chan struct{})
	s := &HttpProxy{
		closeRuntimeHook: func() error {
			close(stopRequested)
			return nil
		},
	}
	wantErr := errors.New("http runtime failed")
	done := make(chan error, 1)
	go func() {
		done <- s.runRuntimeServers([]httpProxyRuntimeServer{
			{
				name: "http",
				start: func() error {
					return wantErr
				},
			},
			{
				name: "https",
				start: func() error {
					<-stopRequested
					close(stopObserved)
					<-allowExit
					return nil
				},
			},
		})
	}()

	select {
	case <-stopObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("runRuntimeServers() did not trigger runtime close")
	}

	select {
	case err := <-done:
		t.Fatalf("runRuntimeServers() returned before remaining runtime exited: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(allowExit)

	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("runRuntimeServers() error = %v, want wrapped %v", err, wantErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runRuntimeServers() did not wait for remaining runtime exit")
	}
}

func TestHttpProxyRunRuntimeServersPrefersLaterRuntimeErrorOverEarlyNil(t *testing.T) {
	stopRequested := make(chan struct{})
	s := &HttpProxy{
		closeRuntimeHook: func() error {
			close(stopRequested)
			return nil
		},
	}
	wantErr := errors.New("https runtime failed")

	err := s.runRuntimeServers([]httpProxyRuntimeServer{
		{
			name: "http",
			start: func() error {
				return nil
			},
		},
		{
			name: "https",
			start: func() error {
				<-stopRequested
				return wantErr
			},
		},
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("runRuntimeServers() error = %v, want wrapped %v", err, wantErr)
	}
}
