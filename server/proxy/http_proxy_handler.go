package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

type tunnelHTTPProxyIngress struct {
	targetAddr string
	rawBuffer  []byte
	request    *http.Request
	remoteAddr string
}

type tunnelHTTPProxyResolvedRequest struct {
	selection    tunnelHTTPBackendSelection
	routeRuntime *RouteRuntimeContext
	remoteAddr   string
}

func (r tunnelHTTPProxyResolvedRequest) normalized() tunnelHTTPProxyResolvedRequest {
	r.selection = r.selection.normalized()
	r.remoteAddr = strings.TrimSpace(r.remoteAddr)
	return r
}

func (r tunnelHTTPProxyResolvedRequest) valid() bool {
	return r.selection.valid() && r.routeRuntime != nil
}

const (
	tunnelHTTPProxyBadRequestResponse         = "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"
	tunnelHTTPProxyForbiddenResponse          = "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"
	tunnelHTTPProxyBadGatewayResponse         = "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"
	tunnelHTTPProxyConnectEstablishedResponse = "HTTP/1.1 200 Connection established\r\n\r\n"
)

var errTunnelHTTPProxyDestinationDenied = errors.New("destination denied by dest acl")

// ProcessHttp http proxy
func ProcessHttp(c *conn.Conn, s *TunnelModeServer) error {
	task := s.CurrentTask()
	if err := validateTunnelRuntimeTask(task); err != nil {
		_ = c.Close()
		return err
	}
	ingress, err := readTunnelHTTPProxyIngress(c)
	if err != nil {
		_ = c.Close()
		logs.Info("%v", err)
		return err
	}
	if err := authorizeTunnelHTTPProxyIngress(s, c, task, ingress); err != nil {
		return err
	}
	if ingress.request.Method == http.MethodConnect {
		return s.handleTunnelHTTPConnect(c, task, ingress)
	}
	return s.serveTunnelHTTPProxy(c, task, ingress)
}

func readTunnelHTTPProxyIngress(c *conn.Conn) (tunnelHTTPProxyIngress, error) {
	_, addr, rb, req, err := c.GetHost()
	if err != nil {
		return tunnelHTTPProxyIngress{}, err
	}
	return tunnelHTTPProxyIngress{
		targetAddr: addr,
		rawBuffer:  rb,
		request:    req,
		remoteAddr: proxyConnRemoteAddr(c),
	}, nil
}

func authorizeTunnelHTTPProxyIngress(s *TunnelModeServer, c *conn.Conn, task *file.Tunnel, ingress tunnelHTTPProxyIngress) error {
	if err := s.Auth(ingress.request, nil, task.Client.Cnf.U, task.Client.Cnf.P, task.MultiAccount, task.UserAuth); err != nil {
		return closeTunnelHTTPProxyConn(c, common.ProxyAuthRequiredBytes, err)
	}
	if s.IsClientDestinationAccessDenied(task.Client, task, ingress.targetAddr) {
		return closeTunnelHTTPProxyConn(c, tunnelHTTPProxyForbiddenResponse, errTunnelHTTPProxyDestinationDenied)
	}
	logs.Debug("http proxy request, client=%d method=%s, host=%s, url=%s, remote address=%s, target=%s", task.Client.Id, ingress.request.Method, ingress.request.Host, ingress.request.URL.RequestURI(), ingress.remoteAddr, ingress.targetAddr)
	return nil
}

func (s *TunnelModeServer) handleTunnelHTTPConnect(c *conn.Conn, task *file.Tunnel, ingress tunnelHTTPProxyIngress) error {
	resolved, err := s.resolveAuthorizedTunnelHTTPProxyTarget(task, ingress.targetAddr)
	if err != nil {
		return closeTunnelHTTPProxyConn(c, tunnelHTTPProxyConnResponseForError(err), err)
	}
	writeTunnelHTTPProxyConnResponse(c, tunnelHTTPProxyConnectEstablishedResponse)
	return s.DealClientWithOptions(
		c,
		resolved.selection.task.Client,
		resolved.selection.targetAddr,
		nil,
		common.CONN_TCP,
		nil,
		[]*file.Flow{resolved.selection.task.Flow, resolved.selection.task.Client.Flow},
		0,
		resolved.selection.task.Target.LocalProxy,
		resolved.selection.task,
		conn.WithRouteUUID(resolved.selection.routeUUID),
	)
}

func (s *TunnelModeServer) serveTunnelHTTPProxy(c *conn.Conn, task *file.Tunnel, ingress tunnelHTTPProxyIngress) error {
	var server *http.Server
	serviceLimiter := s.ServiceRateLimiter(task.Client, task, nil)
	c.Conn = conn.WrapNetConnWithLimiter(c.Conn, serviceLimiter)
	listener := conn.NewOneConnListener(c.SetRb(ingress.rawBuffer))
	shutdown := newTunnelHTTPProxyShutdown(30 * time.Second)
	defer func() {
		_ = listener.Close()
		shutdown.stop()
	}()
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		shutdown.begin()
		req, resolved, err := s.resolveAuthorizedTunnelHTTPProxyServeRequest(task, req, ingress.remoteAddr)
		if err != nil {
			shutdown.reset(server)
			writeTunnelHTTPProxyResolveError(w, err)
			return
		}
		// The listener connection already applies the service limiter to the
		// client socket. Keep request/response wrappers focused on service
		// traffic observation so body bytes are not charged twice.
		req, w = applyTunnelHTTPProxyAccounting(req, w, resolved, nil)
		defer shutdown.reset(server)
		newTunnelHTTPReverseProxy(s, resolved.selection).ServeHTTP(w, req)
	})
	server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *TunnelModeServer) resolveTunnelHTTPProxyTarget(task *file.Tunnel, targetAddr string) (tunnelHTTPProxyResolvedRequest, error) {
	selection, err := selectTunnelHTTPBackend(task, s, targetAddr)
	if err != nil {
		return tunnelHTTPProxyResolvedRequest{}, err
	}
	return tunnelHTTPProxyResolvedRequest{
		selection:    selection,
		routeRuntime: s.NewRouteRuntimeContext(selection.task.Client, selection.routeUUID),
	}, nil
}

func (s *TunnelModeServer) prepareTunnelHTTPProxyRequest(task *file.Tunnel, req *http.Request) (tunnelHTTPProxyResolvedRequest, error) {
	return s.resolveTunnelHTTPProxyTarget(task, normalizeTunnelHTTPProxyTarget(req))
}

func (s *TunnelModeServer) resolveTunnelHTTPProxyServeRequest(task *file.Tunnel, req *http.Request, remoteAddr string) (*http.Request, tunnelHTTPProxyResolvedRequest, error) {
	resolved, err := s.prepareTunnelHTTPProxyRequest(task, req)
	if err != nil {
		return req, tunnelHTTPProxyResolvedRequest{}, err
	}
	resolved.remoteAddr = strings.TrimSpace(remoteAddr)
	return prepareTunnelHTTPProxyContext(req, resolved), resolved, nil
}

func (s *TunnelModeServer) resolveAuthorizedTunnelHTTPProxyTarget(task *file.Tunnel, targetAddr string) (tunnelHTTPProxyResolvedRequest, error) {
	resolved, err := s.resolveTunnelHTTPProxyTarget(task, targetAddr)
	if err != nil {
		return tunnelHTTPProxyResolvedRequest{}, err
	}
	if s.tunnelHTTPProxyDestinationDenied(resolved.selection) {
		return tunnelHTTPProxyResolvedRequest{}, errTunnelHTTPProxyDestinationDenied
	}
	return resolved, nil
}

func (s *TunnelModeServer) resolveAuthorizedTunnelHTTPProxyServeRequest(task *file.Tunnel, req *http.Request, remoteAddr string) (*http.Request, tunnelHTTPProxyResolvedRequest, error) {
	req, resolved, err := s.resolveTunnelHTTPProxyServeRequest(task, req, remoteAddr)
	if err != nil {
		return req, tunnelHTTPProxyResolvedRequest{}, err
	}
	if s.tunnelHTTPProxyDestinationDenied(resolved.selection) {
		return req, tunnelHTTPProxyResolvedRequest{}, errTunnelHTTPProxyDestinationDenied
	}
	return req, resolved, nil
}

func (s *TunnelModeServer) tunnelHTTPProxyDestinationDenied(selection tunnelHTTPBackendSelection) bool {
	return s.IsClientDestinationAccessDenied(selection.task.Client, selection.task, selection.targetAddr)
}

func writeTunnelHTTPProxyConnResponse(c *conn.Conn, response string) {
	if c == nil || response == "" {
		return
	}
	_, _ = io.WriteString(c, response)
}

func closeTunnelHTTPProxyConn(c *conn.Conn, response string, err error) error {
	writeTunnelHTTPProxyConnResponse(c, response)
	if c != nil {
		_ = c.Close()
	}
	return err
}

func newTunnelHTTPReverseProxy(s *TunnelModeServer, selection tunnelHTTPBackendSelection) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.Header["X-Forwarded-For"] = nil
		},
		Transport: s.transportForHTTPBackend(selection),
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			s.removeHTTPBackendTransport(selection)
			writeTunnelHTTPProxyError(rw, req, err)
		},
	}
}

func writeTunnelHTTPProxyError(rw http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, io.EOF) {
		rw.WriteHeader(521)
		return
	}
	if status := tunnelHTTPProxyResolvedErrorStatus(err); status != 0 {
		rw.WriteHeader(status)
		return
	}
	logs.Debug("ErrorHandler: proxy error: method=%s, URL=%s, error=%v", req.Method, req.URL.String(), err)
	errMsg := err.Error()
	idx := strings.Index(errMsg, "Task")
	if idx == -1 {
		idx = strings.Index(errMsg, "Client")
	}
	if idx != -1 {
		http.Error(rw, errMsg[idx:], http.StatusTooManyRequests)
		return
	}
	rw.WriteHeader(http.StatusBadGateway)
}

func tunnelHTTPProxyConnResponseForError(err error) string {
	switch {
	case errors.Is(err, errTunnelHTTPProxyMissingTarget):
		return tunnelHTTPProxyBadRequestResponse
	case errors.Is(err, errTunnelHTTPProxyDestinationDenied):
		return tunnelHTTPProxyForbiddenResponse
	default:
		return tunnelHTTPProxyBadGatewayResponse
	}
}

func writeTunnelHTTPProxyResolveError(w http.ResponseWriter, err error) {
	if w == nil {
		return
	}
	if status := tunnelHTTPProxyResolvedErrorStatus(err); status != 0 {
		http.Error(w, http.StatusText(status), status)
		return
	}
	http.Error(w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
}

func tunnelHTTPProxyResolvedErrorStatus(err error) int {
	switch {
	case errors.Is(err, errTunnelHTTPProxyMissingTarget):
		return http.StatusBadRequest
	case errors.Is(err, errTunnelHTTPProxyDestinationDenied):
		return http.StatusForbidden
	default:
		return 0
	}
}

type tunnelHTTPProxyContextKey string

const (
	tunnelHTTPProxyRequestDataKey  tunnelHTTPProxyContextKey = "nps_request_data"
	tunnelHTTPProxyRouteRuntimeKey tunnelHTTPProxyContextKey = "nps_route_runtime"
	tunnelHTTPProxyRemoteAddrKey   tunnelHTTPProxyContextKey = "nps_remote_addr"
	tunnelHTTPProxyBackendKey      tunnelHTTPProxyContextKey = "nps_backend_selection"
)

type tunnelHTTPProxyContextData struct {
	routeRuntime *RouteRuntimeContext
	remoteAddr   string
	selection    tunnelHTTPBackendSelection
}

func (d tunnelHTTPProxyContextData) normalized() tunnelHTTPProxyContextData {
	d.remoteAddr = strings.TrimSpace(d.remoteAddr)
	d.selection = d.selection.normalized()
	return d
}

func (d tunnelHTTPProxyContextData) valid() bool {
	return d.routeRuntime != nil || d.remoteAddr != "" || d.selection.valid()
}

func tunnelHTTPProxyContextValue(ctx context.Context) (tunnelHTTPProxyContextData, bool) {
	if ctx == nil {
		return tunnelHTTPProxyContextData{}, false
	}
	data, ok := ctx.Value(tunnelHTTPProxyRequestDataKey).(tunnelHTTPProxyContextData)
	if !ok {
		return tunnelHTTPProxyContextData{}, false
	}
	data = data.normalized()
	return data, data.valid()
}

func withTunnelHTTPProxyContextData(ctx context.Context, data tunnelHTTPProxyContextData) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tunnelHTTPProxyRequestDataKey, data.normalized())
}

func withTunnelHTTPProxyResolvedRequest(ctx context.Context, resolved tunnelHTTPProxyResolvedRequest) context.Context {
	resolved = resolved.normalized()
	return withTunnelHTTPProxyContextData(ctx, tunnelHTTPProxyContextData{
		routeRuntime: resolved.routeRuntime,
		remoteAddr:   resolved.remoteAddr,
		selection:    resolved.selection,
	})
}

func prepareTunnelHTTPProxyContext(req *http.Request, resolved tunnelHTTPProxyResolvedRequest) *http.Request {
	ctx := withTunnelHTTPProxyResolvedRequest(req.Context(), resolved)
	return req.WithContext(WithRouteRuntimeTrace(ctx, resolved.routeRuntime))
}

func applyTunnelHTTPProxyAccounting(req *http.Request, w http.ResponseWriter, resolved tunnelHTTPProxyResolvedRequest, limiter rate.Limiter) (*http.Request, http.ResponseWriter) {
	ingressObserver, egressObserver := tunnelHTTPProxyServiceObservers(resolved)
	if req.Body != nil {
		req.Body = wrapReadCloserWithAccounting(req.Body, limiter, ingressObserver)
	}
	w = wrapResponseWriterWithAccounting(w, limiter, egressObserver)
	return req, w
}

func tunnelHTTPProxyServiceObservers(resolved tunnelHTTPProxyResolvedRequest) (conn.ByteObserver, conn.ByteObserver) {
	if !resolved.valid() || resolved.selection.task == nil || resolved.selection.task.Client == nil {
		return nil, nil
	}
	return func(size int64) error {
			return resolved.routeRuntime.ObserveServiceTraffic(resolved.selection.task.Client, resolved.selection.task, nil, size, 0)
		}, func(size int64) error {
			return resolved.routeRuntime.ObserveServiceTraffic(resolved.selection.task.Client, resolved.selection.task, nil, 0, size)
		}
}

func wrapReadCloserWithAccounting(rc io.ReadCloser, limiter rate.Limiter, observer conn.ByteObserver) io.ReadCloser {
	if rc == nil {
		return nil
	}
	if limiter == nil && observer == nil {
		return rc
	}
	return &accountedReadCloser{
		reader: accountedReader{
			reader:   rc,
			limiter:  limiter,
			observer: observer,
		},
		closer: rc,
	}
}

type accountedReader struct {
	reader   io.Reader
	limiter  rate.Limiter
	observer conn.ByteObserver
}

func (r *accountedReader) Read(p []byte) (int, error) {
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

type accountedReadCloser struct {
	reader accountedReader
	closer io.Closer
}

func (r *accountedReadCloser) Read(p []byte) (int, error) {
	if r == nil {
		return 0, net.ErrClosed
	}
	return r.reader.Read(p)
}

func (r *accountedReadCloser) Close() error {
	if r == nil {
		return nil
	}
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
}

func wrapResponseWriterWithAccounting(w http.ResponseWriter, limiter rate.Limiter, observer conn.ByteObserver) http.ResponseWriter {
	if w == nil {
		return nil
	}
	if limiter == nil && observer == nil {
		return w
	}
	return &accountedResponseWriter{
		ResponseWriter: w,
		limiter:        limiter,
		observer:       observer,
	}
}

type accountedResponseWriter struct {
	http.ResponseWriter
	limiter  rate.Limiter
	observer conn.ByteObserver
}

func (w *accountedResponseWriter) Write(p []byte) (int, error) {
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

func (w *accountedResponseWriter) Flush() {
	if w == nil || w.ResponseWriter == nil {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *accountedResponseWriter) Push(target string, opts *http.PushOptions) error {
	if w == nil || w.ResponseWriter == nil {
		return http.ErrNotSupported
	}
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (w *accountedResponseWriter) ReadFrom(r io.Reader) (int64, error) {
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

type tunnelHTTPProxyShutdown struct {
	mu         sync.Mutex
	timer      *time.Timer
	delay      time.Duration
	generation uint64
	inFlight   int
	stopped    bool
}

func newTunnelHTTPProxyShutdown(delay time.Duration) *tunnelHTTPProxyShutdown {
	if delay <= 0 {
		delay = 30 * time.Second
	}
	return &tunnelHTTPProxyShutdown{delay: delay}
}

func (s *tunnelHTTPProxyShutdown) begin() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.inFlight++
	s.generation++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *tunnelHTTPProxyShutdown) reset(server interface{ Close() error }) {
	if s == nil || server == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.inFlight > 0 {
		s.inFlight--
	}
	if s.inFlight > 0 {
		return
	}
	s.generation++
	generation := s.generation
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.delay, func() {
		s.fire(generation, server)
	})
}

func (s *tunnelHTTPProxyShutdown) fire(generation uint64, server interface{ Close() error }) {
	if s == nil || server == nil {
		return
	}
	s.mu.Lock()
	if s.stopped || s.inFlight > 0 || generation != s.generation {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	s.mu.Unlock()
	_ = server.Close()
}

func (s *tunnelHTTPProxyShutdown) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	s.inFlight = 0
	s.generation++
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}
