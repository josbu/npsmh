package httpproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/goroutine"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/server/proxy"
)

type ctxKey string

const (
	ctxRequestData  ctxKey = "nps_request_data"
	ctxRemoteAddr   ctxKey = "nps_remote_addr"
	ctxHost         ctxKey = "nps_host"
	ctxSNI          ctxKey = "nps_sni"
	ctxRouteRuntime ctxKey = "nps_route_runtime"
)

type HttpServer struct {
	*HttpProxy
	httpStatus   atomic.Bool
	httpListener net.Listener
	httpServer   *http.Server
}

var (
	errHTTPServerUnavailable    = errors.New("http server unavailable")
	errHTTPServerAlreadyStarted = errors.New("http server already started")
)

type resolvedHTTPProxyServeRequest struct {
	backend resolvedHTTPProxyBackend
	request *http.Request
}

type httpProxyRequestContext struct {
	remoteAddr   string
	host         *file.Host
	sni          string
	routeRuntime *proxy.RouteRuntimeContext
	selection    backendSelection
}

func (d httpProxyRequestContext) normalized() httpProxyRequestContext {
	d.remoteAddr = strings.TrimSpace(d.remoteAddr)
	d.sni = strings.TrimSpace(d.sni)
	d.selection = d.selection.normalized()
	return d
}

func (d httpProxyRequestContext) valid() bool {
	return d.remoteAddr != "" || d.host != nil || d.sni != "" || d.routeRuntime != nil || d.selection.valid()
}

func httpProxyContextData(ctx context.Context) (httpProxyRequestContext, bool) {
	if ctx == nil {
		return httpProxyRequestContext{}, false
	}
	data, ok := ctx.Value(ctxRequestData).(httpProxyRequestContext)
	if !ok {
		return httpProxyRequestContext{}, false
	}
	data = data.normalized()
	return data, data.valid()
}

func withHTTPProxyRequestData(ctx context.Context, data httpProxyRequestContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxRequestData, data.normalized())
}

func httpProxyContextString(ctx context.Context, key ctxKey) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if data, ok := httpProxyContextData(ctx); ok {
		switch key {
		case ctxRemoteAddr:
			if data.remoteAddr != "" {
				return data.remoteAddr, true
			}
		case ctxSNI:
			if data.sni != "" {
				return data.sni, true
			}
		}
	}
	value, ok := ctx.Value(key).(string)
	if !ok {
		return "", false
	}
	return value, true
}

func httpProxyContextHost(ctx context.Context) (*file.Host, bool) {
	if ctx == nil {
		return nil, false
	}
	if data, ok := httpProxyContextData(ctx); ok && data.host != nil {
		return data.host, true
	}
	host, ok := ctx.Value(ctxHost).(*file.Host)
	if !ok || host == nil {
		return nil, false
	}
	return host, true
}

func httpProxyContextRouteRuntime(ctx context.Context) (*proxy.RouteRuntimeContext, bool) {
	if ctx == nil {
		return nil, false
	}
	if data, ok := httpProxyContextData(ctx); ok && data.routeRuntime != nil {
		return data.routeRuntime, true
	}
	routeRuntime, ok := ctx.Value(ctxRouteRuntime).(*proxy.RouteRuntimeContext)
	if !ok || routeRuntime == nil {
		return nil, false
	}
	return routeRuntime, true
}

func NewHttpServer(httpProxy *HttpProxy, l net.Listener) *HttpServer {
	hs := &HttpServer{
		HttpProxy:    httpProxy,
		httpListener: l,
	}
	hs.httpServer = hs.NewServer(hs.HttpPort, "http")
	return hs
}

func (s *HttpServer) Start() error {
	if s == nil || s.httpServer == nil || s.httpListener == nil {
		return errHTTPServerUnavailable
	}
	if !s.httpStatus.CompareAndSwap(false, true) {
		return errHTTPServerAlreadyStarted
	}
	defer s.httpStatus.Store(false)
	if err := s.httpServer.Serve(s.httpListener); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return nil
}

func (s *HttpServer) Close() error {
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	s.httpStatus.Store(false)
	return nil
}

func (s *HttpServer) writeErrorPage(rw http.ResponseWriter, statusCode int, content []byte) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(statusCode)
	_, _ = rw.Write(content)
}

func (s *HttpServer) writeLimitErrorPage(rw http.ResponseWriter, errMsg string) bool {
	var content []byte
	switch {
	case strings.Contains(errMsg, "time limit exceeded"):
		content = s.currentTimeLimitErrorContent()
	case strings.Contains(errMsg, "flow limit exceeded"):
		content = s.currentFlowLimitErrorContent()
	default:
		return false
	}

	if content == nil {
		return false
	}

	s.writeErrorPage(rw, http.StatusTooManyRequests, content)
	return true
}

func (s *HttpServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	host, ok := s.lookupHTTPProxyHost(w, r)
	if !ok {
		return
	}
	if err := validateHTTPBackendHost(host); err != nil {
		logs.Warn("Reject malformed host %q: %v", r.Host, err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	if !s.allowHTTPProxyRequestSource(w, host, r.RemoteAddr) {
		return
	}
	if s.handleHTTPProxyACMEChallenge(w, r, host) {
		return
	}
	httpOnlyPass := s.currentConfig().Auth.HTTPOnlyPass
	isHTTPOnlyRequest := httpOnlyPass != "" && r.Header.Get("X-NPS-Http-Only") == httpOnlyPass
	if isHTTPOnlyRequest {
		r.Header.Del("X-NPS-Http-Only")
	}
	if s.redirectHTTPProxyToHTTPS(w, r, host, isHTTPOnlyRequest) {
		return
	}
	s.applyHTTPProxyPathRewrite(r, host)

	lease, err := s.CheckFlowAndConnNum(host.Client, nil, host)
	if err != nil {
		http.Error(w, "Access denied: "+err.Error(), http.StatusTooManyRequests)
		logs.Warn("Connection limit exceeded, client id %d, host id %d, error %v", host.Client.Id, host.Id, err)
		return
	}
	defer lease.Release()

	if !s.authorizeHTTPProxyRequest(w, r, host) {
		return
	}
	if host.RedirectURL != "" {
		http.Redirect(w, r, s.ChangeRedirectURL(r, host.RedirectURL), http.StatusTemporaryRedirect)
		return
	}

	logs.Debug("%s request, method %s, host %s, url %s, remote address %s", r.URL.Scheme, r.Method, r.Host, r.URL.Path, r.RemoteAddr)
	resolved, err := s.resolveHTTPProxyServeRequest(r, host)
	if err != nil {
		writeHTTPProxyBackendError(w, err)
		return
	}
	r = resolved.request

	// WebSocket
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" || r.Header.Get(":protocol") != "" {
		logs.Trace("Handling websocket from %s", r.RemoteAddr)
		s.handleResolvedWebsocket(w, resolved, isHTTPOnlyRequest)
		return
	}
	serviceLimiter := s.ServiceRateLimiter(resolved.backend.host.Client, nil, resolved.backend.host)
	r, w = applyHTTPProxyServiceAccounting(r, w, resolved.backend.host, resolved.backend.routeRuntime, serviceLimiter)

	tr := s.transportForBackend(resolved.backend.host, resolved.backend.selection)

	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			//req = req.WithContext(context.WithValue(req.Context(), "origReq", r))
			if resolved.backend.host.TargetIsHttps {
				req.URL.Scheme = "https"
			} else {
				req.URL.Scheme = "http"
			}
			//logs.Debug("Director: set req.URL.Scheme=%s, req.URL.Host=%s", req.URL.Scheme, req.URL.Host)
			s.ChangeHostAndHeader(req, resolved.backend.host.HostChange, resolved.backend.host.HeaderChange, isHTTPOnlyRequest)
			req.URL.Host = r.Host
			if isHTTPOnlyRequest {
				req.Header["X-Forwarded-For"] = nil
			}
		},
		Transport: tr,
		//FlushInterval: 100 * time.Millisecond,
		BufferPool: common.BufPoolCopy,
		ModifyResponse: func(resp *http.Response) error {
			// CORS
			if resolved.backend.host.AutoCORS {
				origin := resp.Request.Header.Get("Origin")
				if origin != "" && resp.Header.Get("Access-Control-Allow-Origin") == "" {
					logs.Debug("ModifyResponse: setting CORS headers for origin=%s", origin)
					resp.Header.Set("Access-Control-Allow-Origin", origin)
					resp.Header.Set("Access-Control-Allow-Credentials", "true")
				}
			}
			// H3
			if s.Http3Port > 0 && r.TLS != nil && !resolved.backend.host.HttpsJustProxy && !resolved.backend.host.TlsOffload && !resolved.backend.host.CompatMode {
				resp.Header.Set("Alt-Svc", `h3=":`+s.Http3PortStr+`"; ma=86400`)
			}
			s.ChangeResponseHeader(resp, resolved.backend.host.RespHeaderChange)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			selection, _ := httpProxyContextBackendSelection(req.Context())
			s.removeBackendTransport(resolved.backend.host.Id, selection)
			s.writeProxyError(rw, req, err)
		},
	}
	rp.ServeHTTP(w, r)
}

func applyHTTPProxyServiceAccounting(r *http.Request, w http.ResponseWriter, host *file.Host, routeRuntime *proxy.RouteRuntimeContext, limiter rate.Limiter) (*http.Request, http.ResponseWriter) {
	if r == nil {
		return nil, wrapResponseWriterWithLimiter(w, limiter, nil)
	}
	ingressObserver, egressObserver := httpProxyServiceObservers(routeRuntime, host)
	if r.Body != nil {
		r.Body = wrapReadCloserWithLimiter(r.Body, limiter, ingressObserver)
	}
	w = wrapResponseWriterWithLimiter(w, limiter, egressObserver)
	return r, w
}

func httpProxyServiceObservers(routeRuntime *proxy.RouteRuntimeContext, host *file.Host) (conn.ByteObserver, conn.ByteObserver) {
	if routeRuntime == nil || host == nil || host.Client == nil {
		return nil, nil
	}
	return func(size int64) error {
			return routeRuntime.ObserveServiceTraffic(host.Client, nil, host, size, 0)
		}, func(size int64) error {
			return routeRuntime.ObserveServiceTraffic(host.Client, nil, host, 0, size)
		}
}

func (s *HttpServer) lookupHTTPProxyHost(w http.ResponseWriter, r *http.Request) (*file.Host, bool) {
	cfg := s.currentConfig()
	host, err := s.currentDB().GetInfoByHost(r.Host, r)
	if err == nil && host != nil && !host.IsClose {
		return host, true
	}
	if cfg.Proxy.ErrorAlways {
		s.writeErrorPage(w, http.StatusNotFound, s.currentNotFoundErrorContent())
	} else if hj, ok := w.(http.Hijacker); ok {
		c, _, _ := hj.Hijack()
		_ = c.Close()
	}
	logs.Debug("Host not found: %s %s %s", r.URL.Scheme, r.Host, r.RequestURI)
	return nil, false
}

func (s *HttpServer) allowHTTPProxyRequestSource(w http.ResponseWriter, host *file.Host, remoteAddr string) bool {
	if !s.IsHostSourceAccessDenied(host, remoteAddr) {
		return true
	}
	logs.Warn("Blocked IP: %s", common.GetIpByAddr(remoteAddr))
	if hj, ok := w.(http.Hijacker); ok {
		c, _, _ := hj.Hijack()
		_ = c.Close()
	}
	return false
}

func (s *HttpServer) handleHTTPProxyACMEChallenge(w http.ResponseWriter, r *http.Request, host *file.Host) bool {
	cfg := s.currentConfig()
	if !host.AutoSSL || !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		return false
	}
	if s.HttpPort != 80 && s.HttpsPort != 443 && !cfg.Proxy.ForceAutoSSL {
		return false
	}
	s.Acme.HandleHTTPChallenge(w, r)
	return true
}

func (s *HttpServer) redirectHTTPProxyToHTTPS(w http.ResponseWriter, r *http.Request, host *file.Host, isHTTPOnlyRequest bool) bool {
	if isHTTPOnlyRequest || !host.AutoHttps || r.TLS != nil {
		return false
	}
	redirectHost := common.RemovePortFromHost(r.Host)
	if s.HttpsPort != 443 {
		redirectHost += ":" + s.HttpsPortStr
	}
	http.Redirect(w, r, "https://"+redirectHost+r.RequestURI, http.StatusMovedPermanently)
	return true
}

func (s *HttpServer) applyHTTPProxyPathRewrite(r *http.Request, host *file.Host) {
	if host.PathRewrite == "" || !strings.HasPrefix(r.URL.Path, host.Location) {
		return
	}
	if !host.CompatMode {
		r.Header.Set("X-Original-Path", r.URL.Path)
	}
	r.URL.Path = host.PathRewrite + r.URL.Path[len(host.Location):]
}

func (s *HttpServer) authorizeHTTPProxyRequest(w http.ResponseWriter, r *http.Request, host *file.Host) bool {
	if err := s.Auth(r, nil, host.Client.Cnf.U, host.Client.Cnf.P, host.MultiAccount, host.UserAuth); err == nil {
		return true
	}
	logs.Warn("Unauthorized request from %s", r.RemoteAddr)
	w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
	http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
	return false
}

func (s *HttpServer) resolveHTTPProxyServeRequest(r *http.Request, host *file.Host) (resolvedHTTPProxyServeRequest, error) {
	if r == nil {
		return resolvedHTTPProxyServeRequest{}, errors.New("missing request")
	}
	ctx, resolved, err := s.resolveHTTPProxyBackendState(r.Context(), r, host, backendSelection{})
	if err != nil {
		logs.Warn("No backend found for host: %s Err: %v", r.Host, err)
		return resolvedHTTPProxyServeRequest{}, err
	}
	if s.IsClientDestinationAccessDenied(resolved.host.Client, nil, resolved.selection.targetAddr) {
		return resolvedHTTPProxyServeRequest{}, errHTTPProxyDestinationDenied
	}
	ctx = proxy.WithRouteRuntimeTrace(ctx, resolved.routeRuntime)
	return resolvedHTTPProxyServeRequest{
		backend: resolved,
		request: r.WithContext(ctx),
	}, nil
}

func (s *HttpServer) writeProxyError(rw http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, io.EOF) {
		logs.Info("ErrorHandler: io.EOF encountered, writing 521")
		rw.WriteHeader(521)
		return
	}
	logs.Debug("ErrorHandler: proxy error: method=%s, URL=%s, error=%v", req.Method, req.URL.String(), err)

	errMsg := err.Error()
	idx := strings.Index(errMsg, "Task")
	if idx == -1 {
		idx = strings.Index(errMsg, "Client")
	}
	if idx != -1 {
		if s.writeLimitErrorPage(rw, errMsg) {
			return
		}
		http.Error(rw, errMsg[idx:], http.StatusTooManyRequests)
		return
	}
	s.writeErrorPage(rw, http.StatusBadGateway, s.CurrentErrorContent())
}

func (s *HttpServer) handleResolvedWebsocket(w http.ResponseWriter, resolved resolvedHTTPProxyServeRequest, isHttpOnlyRequest bool) {
	r := resolved.request
	netConn, err := s.dialResolvedBackendContext(r.Context(), resolved.backend)
	if err != nil {
		if !errors.Is(err, errHTTPProxyDestinationDenied) {
			logs.Info("handleWebsocket: connection to target %s failed: %v", resolved.backend.selection.targetAddr, err)
		}
		writeHTTPProxyBackendError(w, err)
		return
	}

	logs.Info("%s websocket request, method %s, host %s, url %s, remote address %s, target %s", r.URL.Scheme, r.Method, r.Host, r.URL.Path, resolved.backend.remoteAddr, resolved.backend.selection.targetAddr)
	if resolved.backend.host.Target.ProxyProtocol != 0 {
		ra, _ := net.ResolveTCPAddr("tcp", resolved.backend.remoteAddr)
		if ra == nil || ra.IP == nil {
			ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
		}
		la, _ := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
		hdr := conn.BuildProxyProtocolHeaderByAddr(ra, la, resolved.backend.host.Target.ProxyProtocol)
		if hdr != nil {
			_ = netConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := netConn.Write(hdr); err != nil {
				_ = netConn.Close()
				return
			}
			_ = netConn.SetWriteDeadline(time.Time{})
		}
	}

	if resolved.backend.selection.targetIsHTTPS {
		sni, _ := httpProxyContextString(r.Context(), ctxSNI)
		tlsConn, err := conn.GetTlsConn(netConn, sni, false)
		if err != nil {
			_ = netConn.Close()
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		netConn = tlsConn
	}

	s.ChangeHostAndHeader(r, resolved.backend.host.HostChange, resolved.backend.host.HeaderChange, isHttpOnlyRequest || resolved.backend.host.CompatMode)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = netConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		logs.Error("handleWebsocket: Hijack not supported")
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		_ = netConn.Close()
		w.WriteHeader(http.StatusInternalServerError)
		logs.Error("handleWebsocket: Hijack failed")
		return
	}
	serviceLimiter := s.ServiceRateLimiter(resolved.backend.host.Client, nil, resolved.backend.host)
	clientConn = conn.WrapNetConnWithTrafficObserver(
		conn.WrapNetConnWithLimiter(clientConn, serviceLimiter),
		resolved.backend.routeRuntime.ServiceObserver(resolved.backend.host.Client, nil, resolved.backend.host),
	)
	r.Close = false
	if err := r.Write(netConn); err != nil {
		logs.Error("handleWebsocket: failed to write handshake to backend: %v", err)
		closeWebsocketProxyConns(netConn, clientConn)
		return
	}

	backendReader := bufio.NewReader(netConn)
	resp, err := http.ReadResponse(backendReader, r)
	if err != nil {
		logs.Error("handleWebsocket: failed to read handshake response from backend: %v", err)
		closeWebsocketProxyConns(netConn, clientConn)
		return
	}
	if resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}

	if err := resp.Write(clientConn); err != nil {
		logs.Error("handleWebsocket: failed to write handshake response to client: %v", err)
		closeWebsocketProxyConns(netConn, clientConn)
		return
	}

	netConn, err = attachBufferedWebsocketBackendData(backendReader, netConn)
	if err != nil {
		logs.Error("handleWebsocket: read backend buffered data failed: %v", err)
		closeWebsocketProxyConns(netConn, clientConn)
		return
	}

	clientConn, err = attachBufferedWebsocketClientIngress(clientBuf.Reader, clientConn, resolved.backend.routeRuntime, resolved.backend.host, serviceLimiter)
	if err != nil {
		logs.Error("handleWebsocket: failed to read buffered data from client: %v", err)
		closeWebsocketProxyConns(netConn, clientConn)
		return
	}

	good := (r.Method == http.MethodConnect && resp.StatusCode == http.StatusOK) ||
		(r.Method != http.MethodConnect && resp.StatusCode == http.StatusSwitchingProtocols)
	if !good {
		logs.Error("handleWebsocket: unexpected status code in handshake: %d", resp.StatusCode)
		closeWebsocketProxyConns(netConn, clientConn)
		return
	}

	goroutine.Join(clientConn, netConn, []*file.Flow{resolved.backend.host.Flow, resolved.backend.host.Client.Flow}, s.CurrentTask(), resolved.backend.remoteAddr)
}

func attachBufferedWebsocketBackendData(backendReader *bufio.Reader, netConn net.Conn) (net.Conn, error) {
	if backendReader == nil || backendReader.Buffered() == 0 {
		return netConn, nil
	}
	pending := make([]byte, backendReader.Buffered())
	if _, err := backendReader.Read(pending); err != nil {
		return netConn, err
	}
	// These bytes were already read through the backend observer while parsing
	// the handshake response, so they must not be re-accounted here.
	return wrapBufferedHTTPProxyConn(netConn, pending), nil
}

func attachBufferedWebsocketClientIngress(bufReader *bufio.Reader, clientConn net.Conn, routeRuntime *proxy.RouteRuntimeContext, host *file.Host, limiter rate.Limiter) (net.Conn, error) {
	if bufReader == nil || bufReader.Buffered() == 0 {
		return clientConn, nil
	}
	pending := make([]byte, bufReader.Buffered())
	if _, err := bufReader.Read(pending); err != nil {
		return clientConn, err
	}
	if err := observeWebsocketBufferedClientIngress(routeRuntime, host, limiter, pending); err != nil {
		return clientConn, err
	}
	return wrapBufferedHTTPProxyConn(clientConn, pending), nil
}

func closeWebsocketProxyConns(conns ...io.Closer) {
	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}
}

func wrapBufferedHTTPProxyConn(target net.Conn, pending []byte) net.Conn {
	if target == nil {
		return nil
	}
	wrapped := conn.NewConn(target).SetRb(pending)
	return proxy.WrapRuntimeRouteConn(wrapped, proxy.RuntimeRouteUUIDFromConn(target))
}

func writeHTTPProxyBackendError(w http.ResponseWriter, err error) {
	if w == nil {
		return
	}
	if errors.Is(err, errHTTPProxyDestinationDenied) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusBadGateway)
}

func observeWebsocketBufferedClientIngress(routeRuntime *proxy.RouteRuntimeContext, host *file.Host, limiter rate.Limiter, pending []byte) error {
	if routeRuntime == nil || host == nil || host.Client == nil {
		return proxy.ObserveBufferedIngress(pending, limiter, nil)
	}
	return proxy.ObserveBufferedIngress(pending, limiter, func(size int64) error {
		return routeRuntime.ObserveServiceTraffic(host.Client, nil, host, size, 0)
	})
}

func (s *HttpServer) NewServer(port int, scheme string) *http.Server {
	return &http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme = scheme
			s.handleProxy(w, r)
		}),
		// Disable HTTP/2.
		//TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
}

func (s *HttpServer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	if _, ok := httpProxyContextString(ctx, ctxRemoteAddr); !ok {
		return nil, fmt.Errorf("missing %s context", "remote address")
	}
	h, ok := httpProxyContextHost(ctx)
	if !ok {
		return nil, errors.New("missing host context")
	}
	ctx, resolved, err := s.resolveHTTPProxyBackendState(ctx, nil, h, backendSelection{})
	if err != nil {
		logs.Warn("No backend found for h: %d Err: %v", h.Id, err)
		return nil, err
	}
	return s.dialResolvedBackendContext(ctx, resolved)
}

func (s *HttpServer) DialTlsContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := s.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	sni, _ := httpProxyContextString(ctx, ctxSNI)

	tlsConn, err := conn.GetTlsConn(c, sni, false)
	if err != nil {
		_ = c.Close()
		return nil, err
	}

	return proxy.WrapRuntimeRouteConn(tlsConn, proxy.RuntimeRouteUUIDFromConn(c)), nil
}

func wrapReadCloserWithLimiter(rc io.ReadCloser, limiter rate.Limiter, observer conn.ByteObserver) io.ReadCloser {
	if rc == nil {
		return rc
	}
	if limiter == nil && observer == nil {
		return rc
	}
	return &limitedReadCloser{
		reader: limitedReader{
			reader:   rc,
			limiter:  limiter,
			observer: observer,
		},
		closer: rc,
	}
}

type limitedReader struct {
	reader   io.Reader
	limiter  rate.Limiter
	observer conn.ByteObserver
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r == nil || r.reader == nil {
		return 0, net.ErrClosed
	}
	n, err := r.reader.Read(p)
	if r.limiter != nil && n > 0 {
		r.limiter.Get(int64(n))
	}
	if r.observer != nil && n > 0 {
		if observeErr := r.observer(int64(n)); observeErr != nil {
			if r.limiter != nil {
				r.limiter.ReturnBucket(int64(n))
			}
			return n, observeErr
		}
	}
	return n, err
}

type limitedReadCloser struct {
	reader limitedReader
	closer io.Closer
}

func (r *limitedReadCloser) Read(p []byte) (int, error) {
	if r == nil {
		return 0, net.ErrClosed
	}
	return r.reader.Read(p)
}

func (r *limitedReadCloser) Close() error {
	if r == nil || r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

func wrapResponseWriterWithLimiter(w http.ResponseWriter, limiter rate.Limiter, observer conn.ByteObserver) http.ResponseWriter {
	if w == nil {
		return w
	}
	if limiter == nil && observer == nil {
		return w
	}
	return &limitedResponseWriter{ResponseWriter: w, limiter: limiter, observer: observer}
}

type limitedResponseWriter struct {
	http.ResponseWriter
	limiter  rate.Limiter
	observer conn.ByteObserver
}

func (w *limitedResponseWriter) Write(p []byte) (int, error) {
	if w == nil || w.ResponseWriter == nil {
		return 0, net.ErrClosed
	}
	if len(p) == 0 {
		return w.ResponseWriter.Write(p)
	}
	if w.limiter != nil {
		w.limiter.Get(int64(len(p)))
	}
	n, err := w.ResponseWriter.Write(p)
	if w.limiter != nil && n < len(p) {
		w.limiter.ReturnBucket(int64(len(p) - n))
	}
	if w.observer != nil && n > 0 {
		if observeErr := w.observer(int64(n)); observeErr != nil {
			return n, observeErr
		}
	}
	return n, err
}

func (w *limitedResponseWriter) Flush() {
	if w == nil || w.ResponseWriter == nil {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *limitedResponseWriter) Push(target string, opts *http.PushOptions) error {
	if w == nil || w.ResponseWriter == nil {
		return http.ErrNotSupported
	}
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (w *limitedResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if w == nil || w.ResponseWriter == nil || r == nil {
		return 0, net.ErrClosed
	}
	buf := common.BufPoolCopy.Get()
	defer common.BufPoolCopy.Put(buf)
	return io.CopyBuffer(responseWriterOnly{writer: w}, r, buf)
}

type responseWriterOnly struct {
	writer io.Writer
}

func (w responseWriterOnly) Write(p []byte) (int, error) {
	if w.writer == nil {
		return 0, net.ErrClosed
	}
	return w.writer.Write(p)
}
