package httpproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/cache"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type HttpsServer struct {
	*HttpServer
	httpsStatus        atomic.Bool
	httpsListener      net.Listener
	httpsServeListener *HttpsListener
	httpsServer        *http.Server
	cert               *cache.CertManager
	certMagicTls       *tls.Config
	hasDefaultCert     bool
	defaultCertHash    string
	defaultCertFile    string
	defaultKeyFile     string
	ticketKeys         [][32]byte
	tlsNextProtos      []string
}

type proxySSLCacheConfig struct {
	maxEntries     int
	reloadInterval time.Duration
	idleInterval   time.Duration
}

type resolvedHTTPSProxyBackend struct {
	host       *file.Host
	targetAddr string
	task       *file.Tunnel
	release    func()
}

var (
	errHTTPSServerUnavailable    = errors.New("https server unavailable")
	errHTTPSServerAlreadyStarted = errors.New("https server already started")
	errHTTP3ServerUnavailable    = errors.New("http3 server unavailable")
	errHTTP3ServerAlreadyRunning = errors.New("http3 server is already running")
)

func (r resolvedHTTPSProxyBackend) releaseLease() {
	if r.release != nil {
		r.release()
	}
}

func proxyConnRemoteAddr(c net.Conn) string {
	if c == nil || c.RemoteAddr() == nil {
		return ""
	}
	return c.RemoteAddr().String()
}

func hostHasManualCertificate(host *file.Host) bool {
	if host == nil {
		return false
	}
	return strings.TrimSpace(host.CertFile) != "" && strings.TrimSpace(host.KeyFile) != ""
}

func (s *HttpsServer) resolveManualCertSource(serverName string, host *file.Host) *file.Host {
	if host == nil || host.AutoSSL || hostHasManualCertificate(host) {
		return host
	}
	reusable, err := s.currentDB().FindReusableCertHost(serverName, host.Id)
	if err != nil || reusable == nil {
		return host
	}
	return reusable
}

func (s *HttpsServer) loadHostedCertificate(serverName string, host *file.Host) (*tls.Certificate, error) {
	if s == nil || s.cert == nil {
		return nil, errors.New("certificate manager unavailable")
	}
	certSource := s.resolveManualCertSource(serverName, host)
	if certSource == nil {
		return nil, errors.New("certificate host unavailable")
	}
	cert, err := s.cert.Get(certSource.CertFile, certSource.KeyFile, certSource.CertType, certSource.CertHash)
	if err == nil {
		return cert, nil
	}
	if !s.hasDefaultCert {
		return nil, err
	}
	cert, err = s.cert.Get(s.defaultCertFile, s.defaultKeyFile, "file", s.defaultCertHash)
	if err != nil {
		logs.Error("Failed to load certificate: %v", err)
		return nil, err
	}
	return cert, nil
}

func (s *HttpsServer) tlsConfigForCertificate(cert *tls.Certificate, nextProtos []string) *tls.Config {
	if cert == nil {
		return nil
	}
	config := &tls.Config{
		Certificates: []tls.Certificate{*cert},
	}
	config.NextProtos = nextProtos
	config.SetSessionTicketKeys(s.ticketKeys)
	return config
}

func resolveProxySSLCacheConfig(cfg *servercfg.Snapshot) proxySSLCacheConfig {
	return proxySSLCacheConfig{
		maxEntries:     cfg.ProxySSLCacheMaxEntries(),
		reloadInterval: cfg.ProxySSLCacheReloadInterval(),
		idleInterval:   cfg.ProxySSLCacheIdleInterval(),
	}
}

func NewHttpsServer(httpServer *HttpServer, l net.Listener) *HttpsServer {
	cfg := httpServer.currentConfig()
	cacheCfg := resolveProxySSLCacheConfig(cfg)
	https := &HttpsServer{
		HttpServer:      httpServer,
		httpsListener:   l,
		defaultCertFile: cfg.Proxy.SSL.DefaultCertFile,
		defaultKeyFile:  cfg.Proxy.SSL.DefaultKeyFile,
	}

	_, https.hasDefaultCert = common.LoadCert(https.defaultCertFile, https.defaultKeyFile)
	https.defaultCertHash = crypt.FNV1a64("file", https.defaultCertFile, https.defaultKeyFile)

	https.cert = cache.NewCertManager(
		cacheCfg.maxEntries,
		cacheCfg.reloadInterval,
		cacheCfg.idleInterval,
	)
	https.httpsServeListener = NewHttpsListener(l)
	https.httpsServer = https.NewServer(https.HttpsPort, "https")

	https.tlsNextProtos = []string{"h2", "http/1.1"}
	if https.Http3Port != 0 {
		https.tlsNextProtos = append([]string{"h3"}, https.tlsNextProtos...)
	}

	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		logs.Error("failed to generate session ticket key: %v", err)
		s := crypt.GetRandomString(len(key))
		copy(key[:], s)
	}
	https.ticketKeys = append(https.ticketKeys, key)

	https.certMagicTls = https.Magic.TLSConfig()
	https.certMagicTls.NextProtos = append(https.tlsNextProtos, https.certMagicTls.NextProtos...)
	https.certMagicTls.SetSessionTicketKeys(https.ticketKeys)

	go func() {
		if err := https.httpsServer.Serve(https.httpsServeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logs.Error("HTTPS server exit: %v", err)
		}
	}()

	return https
}

func (s *HttpsServer) Start() error {
	if s == nil || s.HttpServer == nil || s.httpsServer == nil || s.httpsListener == nil || s.httpsServeListener == nil {
		return errHTTPSServerUnavailable
	}
	if !s.httpsStatus.CompareAndSwap(false, true) {
		return errHTTPSServerAlreadyStarted
	}
	defer s.httpsStatus.Store(false)
	conn.Accept(s.httpsListener, func(c net.Conn) {
		helloInfo, rb, err := crypt.ReadClientHello(c, nil)
		if err != nil || helloInfo == nil {
			logs.Debug("Failed to read clientHello from %v, err=%v", c.RemoteAddr(), err)
			// Check if the request is an HTTP request.
			s.checkHTTPAndRedirect(c, rb)
			return
		}

		serverName := helloInfo.ServerName
		if serverName == "" {
			logs.Debug("IP access to HTTPS port is not allowed. Remote address: %v", c.RemoteAddr())
			_ = c.Close()
			return
		}

		host, err := s.currentDB().FindCertByHost(serverName)
		if err != nil || host == nil || host.IsClose {
			_ = c.Close()
			logs.Debug("The URL %s cannot be parsed! Remote address: %v", serverName, c.RemoteAddr())
			return
		}
		if s.IsHostSourceAccessDenied(host, proxyConnRemoteAddr(c)) {
			_ = c.Close()
			return
		}

		if host.HttpsJustProxy {
			logs.Debug("Certificate handled by backend")
			s.handleHttpsProxy(host, c, rb, serverName)
			return
		}

		var tlsConfig *tls.Config
		if host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.currentConfig().Proxy.ForceAutoSSL) {
			logs.Debug("Auto SSL is enabled")
			tlsConfig = s.certMagicTls
		} else {
			cert, err := s.loadHostedCertificate(serverName, host)
			if err != nil {
				logs.Debug("Certificate handled by backend")
				s.handleHttpsProxy(host, c, rb, serverName)
				return
			}
			tlsConfig = s.tlsConfigForCertificate(cert, s.tlsNextProtos)
		}

		acceptConn := conn.NewConn(c).SetRb(rb)
		tlsConn := tls.Server(acceptConn, tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			return
		}
		if host.TlsOffload {
			s.handleTlsProxy(host, tlsConn, serverName)
			return
		}
		if !s.httpsServeListener.Deliver(tlsConn) {
			_ = tlsConn.Close()
			return
		}
	})
	return nil
}

func (s *HttpsServer) checkHTTPAndRedirect(c net.Conn, rb []byte) {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = c.Close() }()

	req, ok := s.readHTTPSRedirectRequest(c, rb)
	if !ok {
		return
	}
	logs.Debug("HTTP Request Sent to HTTPS Port")
	req.URL.Scheme = "https"
	_ = c.SetDeadline(time.Time{})

	if !s.allowHTTPSRedirectRequest(req, proxyConnRemoteAddr(c)) {
		return
	}
	redirectURL := "https://" + req.Host + req.RequestURI
	s.writeHTTPSRedirectResponse(c, redirectURL)
}

func (s *HttpsServer) readHTTPSRedirectRequest(c net.Conn, rb []byte) (*http.Request, bool) {
	logs.Debug("Pre-read rb content: %q", string(rb))
	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(rb), c))
	req, err := http.ReadRequest(reader)
	if err != nil {
		logs.Debug("Failed to parse HTTP request from %v, err=%v", c.RemoteAddr(), err)
		return nil, false
	}
	return req, true
}

func (s *HttpsServer) allowHTTPSRedirectRequest(req *http.Request, remoteAddr string) bool {
	host, err := s.currentDB().GetInfoByHost(req.Host, req)
	if err != nil || host == nil {
		logs.Debug("Host not found: %s %s %s", req.URL.Scheme, req.Host, req.RequestURI)
		return false
	}
	if s.IsHostSourceAccessDenied(host, remoteAddr) {
		logs.Debug("HTTP request to HTTPS port blocked by entry ACL: host=%s remote=%v", req.Host, remoteAddr)
		return false
	}
	return true
}

func (s *HttpsServer) writeHTTPSRedirectResponse(c net.Conn, redirectURL string) {
	response := "HTTP/1.1 302 Found\r\n" +
		"Location: " + redirectURL + "\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: close\r\n\r\n"
	if _, writeErr := c.Write([]byte(response)); writeErr != nil {
		logs.Error("Failed to write redirect response to %v, err=%v", c.RemoteAddr(), writeErr)
		return
	}
	logs.Info("Redirected HTTP request from %v to %s", c.RemoteAddr(), redirectURL)
}

func (s *HttpsServer) handleHttpsProxy(host *file.Host, c net.Conn, rb []byte, sni string) {
	resolved, err := s.resolveHTTPSProxyBackend(host)
	if errors.Is(err, errHTTPProxyInvalidBackend) {
		logs.Warn("Reject malformed HTTPS backend for %q: %v", sni, err)
		_ = c.Close()
		return
	}
	if err != nil {
		clientID, hostID := 0, 0
		if resolved.host != nil {
			hostID = resolved.host.Id
			if resolved.host.Client != nil {
				clientID = resolved.host.Client.Id
			}
		}
		logs.Debug("Client id %d, host id %d, error %v during https connection", clientID, hostID, err)
		_ = c.Close()
		return
	}
	defer resolved.releaseLease()

	logs.Info("New HTTPS connection, clientId %d, host %s, remote address %v", resolved.host.Client.Id, sni, c.RemoteAddr())
	_ = s.DealClient(conn.NewConn(c), resolved.host.Client, resolved.targetAddr, rb, common.CONN_TCP, nil, []*file.Flow{resolved.host.Flow, resolved.host.Client.Flow}, resolved.host.Target.ProxyProtocol, resolved.host.Target.LocalProxy, resolved.task)
}

func (s *HttpsServer) handleTlsProxy(host *file.Host, tlsConn net.Conn, sni string) {
	resolved, err := s.resolveHTTPSProxyBackend(host)
	if errors.Is(err, errHTTPProxyInvalidBackend) {
		logs.Warn("Reject malformed TLS backend for %q: %v", sni, err)
		_ = tlsConn.Close()
		return
	}
	if err != nil {
		clientID, hostID := 0, 0
		if resolved.host != nil {
			hostID = resolved.host.Id
			if resolved.host.Client != nil {
				clientID = resolved.host.Client.Id
			}
		}
		logs.Debug("Client id %d, host id %d, TLS-only check failed: %v", clientID, hostID, err)
		_ = tlsConn.Close()
		return
	}
	defer resolved.releaseLease()

	logs.Info("New TLS offload connection, clientId %d, host %s, remote %v -> %s", resolved.host.Client.Id, sni, tlsConn.RemoteAddr(), resolved.targetAddr)
	_ = s.DealClient(conn.NewConn(tlsConn), resolved.host.Client, resolved.targetAddr, nil, common.CONN_TCP, nil, []*file.Flow{resolved.host.Flow, resolved.host.Client.Flow}, resolved.host.Target.ProxyProtocol, resolved.host.Target.LocalProxy, resolved.task)
}

func (s *HttpsServer) resolveHTTPSProxyBackend(host *file.Host) (resolvedHTTPSProxyBackend, error) {
	resolved := resolvedHTTPSProxyBackend{
		host: host.SelectRuntimeRoute(),
	}
	if err := validateHTTPBackendHost(resolved.host); err != nil {
		return resolved, err
	}

	lease, err := s.CheckFlowAndConnNum(resolved.host.Client, nil, resolved.host)
	if err != nil {
		return resolved, err
	}

	resolved.targetAddr, err = resolved.host.Target.GetRandomTarget()
	if err != nil {
		lease.Release()
		return resolved, err
	}
	resolved.task = file.NewTunnelByHost(resolved.host, s.HttpsPort)
	resolved.release = lease.Release
	return resolved, nil
}

func (s *HttpsServer) Close() error {
	if s == nil {
		return nil
	}
	if s.httpsServer != nil {
		_ = s.httpsServer.Close()
	}
	if s.httpsServeListener != nil {
		_ = s.httpsServeListener.Close()
	}
	if s.cert != nil {
		s.cert.Stop()
	}
	s.httpsStatus.Store(false)
	if s.httpsListener != nil {
		return s.httpsListener.Close()
	}
	return nil
}

// HttpsListener wraps a parent listener.
type HttpsListener struct {
	acceptConn     chan net.Conn
	done           chan struct{}
	parentListener net.Listener
	closeOnce      sync.Once
}

func NewHttpsListener(l net.Listener) *HttpsListener {
	return newHTTPSListenerWithBuffer(l, 1024)
}

var httpsListenerEnqueueTimeout = 250 * time.Millisecond

func normalizedHTTPSListenerEnqueueTimeout() time.Duration {
	if httpsListenerEnqueueTimeout <= 0 {
		return 250 * time.Millisecond
	}
	return httpsListenerEnqueueTimeout
}

func newHTTPSListenerWithBuffer(l net.Listener, buffer int) *HttpsListener {
	if buffer <= 0 {
		buffer = 1
	}
	return &HttpsListener{
		parentListener: l,
		acceptConn:     make(chan net.Conn, buffer),
		done:           make(chan struct{}),
	}
}

func (l *HttpsListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	case httpsConn := <-l.acceptConn:
		if httpsConn == nil {
			return nil, net.ErrClosed
		}
		return httpsConn, nil
	}
}

func (l *HttpsListener) Deliver(c *tls.Conn) bool {
	if l == nil || c == nil {
		return false
	}
	timer := time.NewTimer(normalizedHTTPSListenerEnqueueTimeout())
	defer timer.Stop()

	select {
	case <-l.done:
		return false
	default:
	}
	select {
	case <-l.done:
		return false
	case l.acceptConn <- c:
		return true
	default:
	}
	select {
	case <-l.done:
		return false
	case l.acceptConn <- c:
		return true
	case <-timer.C:
		return false
	}
}

func (l *HttpsListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
		for {
			select {
			case c := <-l.acceptConn:
				if c != nil {
					_ = c.Close()
				}
			default:
				return
			}
		}
	})
	return nil
}

func (l *HttpsListener) Addr() net.Addr {
	if l == nil || l.parentListener == nil {
		return nil
	}
	return l.parentListener.Addr()
}

type Http3Server struct {
	*HttpsServer
	http3Status     atomic.Bool
	http3Listener   net.PacketConn
	http3Server     *http3.Server
	http3NextProtos []string
	closeMu         sync.Mutex
	closeCtx        context.Context
	closeCancel     context.CancelFunc
	runtimeClosed   bool
	bridgeConnMu    sync.Mutex
	bridgeConns     map[*quic.Conn]struct{}
	workerMu        sync.Mutex
	workerClosing   bool
	workerWG        sync.WaitGroup
	handleQUICHook  func(*quic.Conn, string, string)
}

var closedHTTP3RuntimeContext = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}()

func NewHttp3Server(httpsSrv *HttpsServer, udpConn net.PacketConn) *Http3Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Http3Server{
		HttpsServer:   httpsSrv,
		http3Listener: udpConn,
		closeCtx:      ctx,
		closeCancel:   cancel,
		bridgeConns:   make(map[*quic.Conn]struct{}),
	}
}

func (s *Http3Server) prepareRuntime() {
	if s == nil {
		return
	}

	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closeCtx == nil || s.runtimeClosed {
		s.closeCtx, s.closeCancel = context.WithCancel(context.Background())
	}
	s.runtimeClosed = false
}

func (s *Http3Server) runtimeContext() context.Context {
	if s == nil {
		return context.Background()
	}

	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.runtimeClosed {
		return closedHTTP3RuntimeContext
	}
	if s.closeCtx == nil {
		s.closeCtx, s.closeCancel = context.WithCancel(context.Background())
	}
	return s.closeCtx
}

func (s *Http3Server) cancelRuntime() {
	if s == nil {
		return
	}

	s.closeMu.Lock()
	cancel := s.closeCancel
	s.closeCtx = nil
	s.closeCancel = nil
	s.runtimeClosed = true
	s.closeMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Http3Server) trackBridgeConn(qc *quic.Conn) func() {
	if s == nil || qc == nil {
		return func() {}
	}

	s.bridgeConnMu.Lock()
	if s.bridgeConns == nil {
		s.bridgeConns = make(map[*quic.Conn]struct{})
	}
	s.bridgeConns[qc] = struct{}{}
	s.bridgeConnMu.Unlock()

	return func() {
		s.bridgeConnMu.Lock()
		delete(s.bridgeConns, qc)
		s.bridgeConnMu.Unlock()
	}
}

func (s *Http3Server) closeBridgeConns() {
	if s == nil {
		return
	}

	s.bridgeConnMu.Lock()
	conns := make([]*quic.Conn, 0, len(s.bridgeConns))
	for qc := range s.bridgeConns {
		conns = append(conns, qc)
	}
	s.bridgeConnMu.Unlock()

	for _, qc := range conns {
		_ = qc.CloseWithError(0, "server closed")
	}
}

func (s *Http3Server) dispatchHandleQUIC(qc *quic.Conn, alpn, sni string) {
	if s == nil {
		closeAcceptedQUIC(qc, "server closed")
		return
	}

	s.workerMu.Lock()
	if s.workerClosing {
		s.workerMu.Unlock()
		closeAcceptedQUIC(qc, "server closed")
		return
	}
	s.workerWG.Add(1)
	hook := s.handleQUICHook
	s.workerMu.Unlock()

	go func() {
		defer s.workerWG.Done()
		if hook != nil {
			hook(qc, alpn, sni)
			return
		}
		s.HandleQUIC(qc, alpn, sni)
	}()
}

func (s *Http3Server) beginQUICWorkerShutdown() {
	if s == nil {
		return
	}

	s.workerMu.Lock()
	s.workerClosing = true
	s.workerMu.Unlock()
}

func (s *Http3Server) prepareQUICWorkerDispatch() {
	if s == nil {
		return
	}

	s.workerMu.Lock()
	s.workerClosing = false
	s.workerMu.Unlock()
}

func (s *Http3Server) waitQUICWorkers() {
	if s == nil {
		return
	}
	s.workerWG.Wait()
}

func closeAcceptedQUIC(qc *quic.Conn, msg string) {
	if qc == nil {
		return
	}
	_ = qc.CloseWithError(0, msg)
}

func (s *Http3Server) Start() error {
	if s == nil || s.HttpsServer == nil || s.httpsServer == nil || s.http3Listener == nil {
		return errHTTP3ServerUnavailable
	}
	if !s.http3Status.CompareAndSwap(false, true) {
		return errHTTP3ServerAlreadyRunning
	}
	defer func() {
		s.http3Status.Store(false)
	}()
	s.prepareRuntime()
	s.prepareQUICWorkerDispatch()
	s.http3NextProtos = []string{http3.NextProtoH3}
	tlsConfig := &tls.Config{
		NextProtos:         s.http3NextProtos,
		GetConfigForClient: s.GetConfigForClient,
	}
	tlsConfig.SetSessionTicketKeys(s.ticketKeys)

	if !s.Http3Bridge {
		s.http3Server = &http3.Server{
			Handler:   s.httpsServer.Handler,
			TLSConfig: tlsConfig,
		}

		if err := s.http3Server.Serve(s.http3Listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logs.Error("HTTP/3 Serve error: %v", err)
			return err
		}
		return nil
	}

	quicCfg := connection.CurrentQUICRuntime()
	s.http3NextProtos = append([]string{http3.NextProtoH3}, quicCfg.ALPN...)
	quicConfig := &quic.Config{
		KeepAlivePeriod:    time.Duration(quicCfg.KeepAliveSec) * time.Second,
		MaxIdleTimeout:     time.Duration(quicCfg.IdleTimeoutSec) * time.Second,
		MaxIncomingStreams: quicCfg.MaxStreams,
		Allow0RTT:          true,
	}

	tr := &quic.Transport{Conn: s.http3Listener}
	ln, err := tr.ListenEarly(tlsConfig, quicConfig)
	if err != nil {
		return err
	}
	s.http3Server = &http3.Server{
		Handler: s.httpsServer.Handler,
	}

	for {
		qc, err := ln.Accept(s.runtimeContext())
		if err != nil {
			logs.Trace("HTTP/3 Accept transient error: %v", err)
			if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, quic.ErrServerClosed) {
				break
			}
			continue
		}
		state := qc.ConnectionState()
		alpn := state.TLS.NegotiatedProtocol
		sni := state.TLS.ServerName
		s.dispatchHandleQUIC(qc, alpn, sni)
	}
	_ = ln.Close()
	return nil
}

func (s *Http3Server) Close() error {
	if s == nil {
		return nil
	}
	s.cancelRuntime()
	s.beginQUICWorkerShutdown()
	s.closeBridgeConns()
	if s.http3Server != nil {
		_ = s.http3Server.Close()
	}
	s.http3Status.Store(false)
	var err error
	if s.http3Listener != nil {
		err = s.http3Listener.Close()
	}
	s.waitQUICWorkers()
	return err
}

func (s *Http3Server) HandleQUIC(qc *quic.Conn, alpn, sni string) {
	if s == nil || qc == nil {
		closeAcceptedQUIC(qc, "server unavailable")
		return
	}
	bridgeCfg := connection.CurrentBridgeRuntime()
	if alpn == http3.NextProtoH3 && !strings.EqualFold(sni, bridgeCfg.Host) {
		if s.http3Server == nil {
			closeAcceptedQUIC(qc, "server unavailable")
			return
		}
		_ = s.http3Server.ServeQUICConn(qc)
		return
	}
	s.serveBridgeQUIC(qc)
}

func (s *Http3Server) serveBridgeQUIC(qc *quic.Conn) {
	if s == nil || qc == nil {
		closeAcceptedQUIC(qc, "server unavailable")
		return
	}
	release := s.trackBridgeConn(qc)
	defer release()

	stream, err := qc.AcceptStream(s.runtimeContext())
	if err != nil {
		logs.Trace("QUIC accept stream error: %v", err)
		_ = qc.CloseWithError(0, "closed")
		return
	}
	c := conn.NewQuicAutoCloseConn(stream, qc)
	s.ProcessBridgeClient(conn.NewConn(c), "quic")
}

func (s *Http3Server) GetConfigForClient(info *tls.ClientHelloInfo) (*tls.Config, error) {
	if s == nil || info == nil {
		return nil, nil
	}
	host, err := s.currentDB().FindCertByHost(info.ServerName)
	if err != nil || host == nil || host.HttpsJustProxy || host.TlsOffload || host.IsClose {
		return nil, nil
	}

	if host.AutoSSL && (s.HttpPort == 80 || s.HttpsPort == 443 || s.currentConfig().Proxy.ForceAutoSSL) {
		return s.certMagicTls, nil
	}

	cert, err := s.loadHostedCertificate(info.ServerName, host)
	if err != nil {
		return nil, nil
	}

	nextProtos := s.tlsNextProtos
	if s.Http3Bridge {
		nextProtos = s.http3NextProtos
	}
	config := s.tlsConfigForCertificate(cert, nextProtos)
	return config, nil
}
