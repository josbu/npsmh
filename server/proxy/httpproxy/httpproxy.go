package httpproxy

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
)

type HttpProxy struct {
	*proxy.BaseServer
	HttpServer            *HttpServer
	HttpsServer           *HttpsServer
	Http3Server           *Http3Server
	HttpPort              int
	HttpsPort             int
	Http3Port             int
	HttpProxyCache        *index.AnyIntIndex
	HttpPortStr           string
	HttpsPortStr          string
	Http3PortStr          string
	Http3Bridge           bool
	TimeLimitErrorContent []byte
	FlowLimitErrorContent []byte
	Magic                 *certmagic.Config
	Acme                  *certmagic.ACMEIssuer
	configRoot            func() *servercfg.Snapshot
	dbRoot                func() httpProxyDB
	closeRuntimeHook      func() error
}

type httpProxyDB interface {
	FindCertByHost(string) (*file.Host, error)
	FindReusableCertHost(string, int) (*file.Host, error)
	GetInfoByHost(string, *http.Request) (*file.Host, error)
}

var currentHTTPProxyConfigRoot = servercfg.Current
var errHTTPProxyUnavailable = errors.New("http proxy unavailable")

var currentHTTPProxyDBRoot = func() httpProxyDB {
	return file.GetDb()
}

var httpProxyGetHTTPListener = connection.GetHttpListener
var httpProxyGetHTTPSListener = connection.GetHttpsListener

var httpProxyListenHTTP3 = func(ip string, port int) (net.PacketConn, error) {
	return net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip), Port: port})
}

func (s *HttpProxy) currentConfig() *servercfg.Snapshot {
	if s == nil {
		return servercfg.Current()
	}
	return servercfg.ResolveProvider(s.configRoot)
}

func (s *HttpProxy) currentDB() httpProxyDB {
	if s != nil && s.dbRoot != nil {
		if db := s.dbRoot(); db != nil {
			return db
		}
	}
	return file.GetDb()
}

func (s *HttpProxy) currentNotFoundErrorContent() []byte {
	cfg := s.currentConfig()
	content, err := common.ReadAllFromFile(common.ResolvePath(cfg.Proxy.ErrorPage))
	if err == nil && len(content) > 0 {
		return content
	}
	if content := s.CurrentErrorContent(); len(content) > 0 {
		return content
	}
	return []byte("nps 404")
}

func (s *HttpProxy) currentTimeLimitErrorContent() []byte {
	cfg := s.currentConfig()
	if content := s.loadOptionalErrorPage(cfg.Proxy.ErrorPageTimeLimit); len(content) > 0 {
		return content
	}
	return s.TimeLimitErrorContent
}

func (s *HttpProxy) currentFlowLimitErrorContent() []byte {
	cfg := s.currentConfig()
	if content := s.loadOptionalErrorPage(cfg.Proxy.ErrorPageFlowLimit); len(content) > 0 {
		return content
	}
	return s.FlowLimitErrorContent
}

func NewHttpProxy(bridge proxy.NetBridge, task *file.Tunnel, httpPort, httpsPort, http3Port int, httpProxyCache *index.AnyIntIndex) *HttpProxy {
	return NewHttpProxyWithRoots(
		proxy.NewBaseServer(bridge, task),
		httpPort,
		httpsPort,
		http3Port,
		httpProxyCache,
		currentHTTPProxyConfigRoot,
		currentHTTPProxyDBRoot,
	)
}

func NewHttpProxyWithRoots(baseServer *proxy.BaseServer, httpPort, httpsPort, http3Port int, httpProxyCache *index.AnyIntIndex, configRoot func() *servercfg.Snapshot, dbRoot func() httpProxyDB) *HttpProxy {
	if baseServer == nil {
		baseServer = &proxy.BaseServer{}
	}
	httpProxy := &HttpProxy{
		BaseServer:     baseServer,
		HttpPort:       httpPort,
		HttpsPort:      httpsPort,
		Http3Port:      http3Port,
		HttpProxyCache: httpProxyCache,
		HttpPortStr:    strconv.Itoa(httpPort),
		HttpsPortStr:   strconv.Itoa(httpsPort),
		Http3PortStr:   strconv.Itoa(http3Port),
		configRoot:     configRoot,
		dbRoot:         dbRoot,
	}
	return httpProxy
}

func (s *HttpProxy) decideAutoSSLHost(name string) error {
	h, err := s.currentDB().FindCertByHost(name)
	if err != nil || h == nil {
		return fmt.Errorf("unknown host %q", name)
	}
	if !h.AutoSSL {
		return fmt.Errorf("AutoSSL disabled for %q", name)
	}
	return nil
}

type httpProxyRuntimeServer struct {
	name  string
	start func() error
}

func (s *HttpProxy) Start() error {
	if s == nil || s.BaseServer == nil {
		return errHTTPProxyUnavailable
	}
	cfg := s.currentConfig()
	content, err := common.ReadAllFromFile(common.ResolvePath(cfg.Proxy.ErrorPage))
	if err != nil {
		content = []byte("nps 404")
	}
	s.SetErrorContent(content)
	s.TimeLimitErrorContent = s.loadOptionalErrorPage(cfg.Proxy.ErrorPageTimeLimit)
	s.FlowLimitErrorContent = s.loadOptionalErrorPage(cfg.Proxy.ErrorPageFlowLimit)

	if s.BridgeIsServer() {
		s.Http3Bridge = cfg.Proxy.BridgeHTTP3
	}

	certmagic.Default.Logger = logs.ZapLogger
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = cfg.Proxy.SSL.Email
	switch strings.ToLower(cfg.Proxy.SSL.CA) {
	case "letsencrypt", "le", "prod", "production":
		certmagic.DefaultACME.CA = certmagic.LetsEncryptProductionCA
	case "zerossl", "zero", "zs":
		certmagic.DefaultACME.CA = certmagic.ZeroSSLProductionCA
	case "googletrust", "google", "goog":
		certmagic.DefaultACME.CA = certmagic.GoogleTrustProductionCA
	default:
		certmagic.DefaultACME.CA = certmagic.LetsEncryptStagingCA
	}
	certmagic.Default.Storage = &certmagic.FileStorage{
		Path: common.ResolvePath(cfg.Proxy.SSL.Path),
	}
	s.Magic = certmagic.NewDefault()
	if certmagic.DefaultACME.CA == certmagic.ZeroSSLProductionCA {
		s.Magic.Issuers = []certmagic.Issuer{
			&certmagic.ZeroSSLIssuer{
				APIKey: cfg.Proxy.SSL.ZeroSSLAPI,
			},
		}
	}
	s.Magic.OnDemand = &certmagic.OnDemandConfig{
		DecisionFunc: func(ctx context.Context, name string) error {
			return s.decideAutoSSLHost(name)
		},
	}
	s.Acme = certmagic.NewACMEIssuer(s.Magic, certmagic.DefaultACME)

	if err := s.initHTTPServer(); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.initHTTPSServer(); err != nil {
		_ = s.Close()
		return err
	}
	if err := s.initHTTP3Server(); err != nil {
		_ = s.Close()
		return err
	}
	return s.runRuntimeServers(s.runtimeServers())
}

func (s *HttpProxy) initHTTPServer() error {
	if s == nil || s.HttpPort <= 0 {
		return nil
	}
	httpListener, err := httpProxyGetHTTPListener()
	if err != nil {
		return fmt.Errorf("start http listener: %w", err)
	}
	s.HttpServer = NewHttpServer(s, httpListener)
	logs.Info("HTTP server listening on port %d", s.HttpPort)
	return nil
}

func (s *HttpProxy) initHTTPSServer() error {
	if s == nil || s.HttpsPort <= 0 {
		return nil
	}
	httpsListener, err := httpProxyGetHTTPSListener()
	if err != nil {
		return fmt.Errorf("start https listener: %w", err)
	}
	if s.HttpServer == nil {
		s.HttpServer = NewHttpServer(s, nil)
	}
	s.HttpsServer = NewHttpsServer(s.HttpServer, httpsListener)
	logs.Info("HTTPS server listening on port %d", s.HttpsPort)
	return nil
}

func (s *HttpProxy) initHTTP3Server() error {
	if s == nil || s.HttpsServer == nil || s.Http3Port <= 0 {
		return nil
	}
	httpCfg := connection.CurrentHTTPRuntime()
	http3PacketConn, err := httpProxyListenHTTP3(httpCfg.IP, s.Http3Port)
	if err != nil {
		return fmt.Errorf("start http3 listener: %w", err)
	}
	logs.Info("HTTP/3 server listening on port %d", s.Http3Port)
	s.Http3Server = NewHttp3Server(s.HttpsServer, http3PacketConn)
	return nil
}

func (s *HttpProxy) runtimeServers() []httpProxyRuntimeServer {
	servers := make([]httpProxyRuntimeServer, 0, 3)
	if s.HttpServer != nil && s.HttpServer.httpListener != nil {
		servers = append(servers, httpProxyRuntimeServer{
			name: "http",
			start: func() error {
				return s.HttpServer.Start()
			},
		})
	}
	if s.HttpsServer != nil {
		servers = append(servers, httpProxyRuntimeServer{
			name: "https",
			start: func() error {
				return s.HttpsServer.Start()
			},
		})
	}
	if s.Http3Server != nil {
		servers = append(servers, httpProxyRuntimeServer{
			name: "http3",
			start: func() error {
				return s.Http3Server.Start()
			},
		})
	}
	return servers
}

func (s *HttpProxy) runRuntimeServers(servers []httpProxyRuntimeServer) error {
	if len(servers) == 0 {
		return nil
	}

	errCh := make(chan error, len(servers))
	var wg sync.WaitGroup
	for _, server := range servers {
		server := server
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := server.start(); err != nil {
				errCh <- fmt.Errorf("%s runtime stopped: %w", server.name, err)
				return
			}
			errCh <- nil
		}()
	}

	result := <-errCh
	closeErr := s.closeRuntimeServers()
	wg.Wait()
	for i := 1; i < len(servers); i++ {
		if err := <-errCh; result == nil && err != nil {
			result = err
		}
	}
	if result == nil {
		return closeErr
	}
	return result
}

func (s *HttpProxy) closeRuntimeServers() error {
	if s != nil && s.closeRuntimeHook != nil {
		return s.closeRuntimeHook()
	}
	if s == nil {
		return nil
	}
	return s.Close()
}

func (s *HttpProxy) loadOptionalErrorPage(path string) []byte {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	content, err := common.ReadAllFromFile(common.ResolvePath(path))
	if err != nil {
		logs.Warn("Failed to load error page from %s: %v", path, err)
		return nil
	}
	return content
}

func (s *HttpProxy) Close() error {
	if s == nil {
		return nil
	}
	if s.HttpServer != nil {
		_ = s.HttpServer.Close()
	}
	if s.HttpsServer != nil {
		_ = s.HttpsServer.Close()
	}
	if s.Http3Server != nil {
		_ = s.Http3Server.Close()
	}
	if s.HttpProxyCache != nil {
		s.HttpProxyCache.Clear()
	}
	return nil
}

func (s *HttpProxy) ChangeHostAndHeader(r *http.Request, host string, header string, httpOnly bool) {
	cfg := s.currentConfig()
	scheme := "http"
	ssl := "off"
	serverPort := proxyPortString(cfg.Network.HTTPProxyPort, "80")
	if r.TLS != nil {
		scheme = "https"
		ssl = "on"
		serverPort = proxyPortString(cfg.Network.HTTPSProxyPort, "443")
	}
	origHost := r.Host
	hostOnly := common.RemovePortFromHost(origHost)

	if host != "" {
		r.Host = host
		if orig := r.Header.Get("Origin"); orig != "" {
			r.Header.Set("Origin", scheme+"://"+host)
		}
	}

	remoteAddr := r.RemoteAddr
	clientIP := common.GetIpByAddr(remoteAddr)
	clientPort := common.GetPortStrByAddr(remoteAddr)

	proxyAddXFF := clientIP
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		proxyAddXFF = strings.Join(prior, ", ") + ", " + clientIP
	}

	var addOrigin bool
	if !httpOnly {
		addOrigin = cfg.Proxy.AddOriginHeader
	} else {
		addOrigin = false
	}

	if addOrigin {
		if r.Header.Get("X-Forwarded-Proto") == "" {
			r.Header.Set("X-Forwarded-Proto", scheme)
		}
		if r.Header.Get("X-Forwarded-Host") == "" && host == "" {
			r.Header.Set("X-Forwarded-Host", origHost)
		}
		r.Header.Set("X-Real-IP", clientIP)
	}

	if header == "" {
		return
	}

	expandVars := func(val string) string {
		rep := strings.NewReplacer(
			"${scheme}", scheme,
			"${ssl}", ssl,
			"${forwarded_ssl}", ssl,

			"${host}", hostOnly,
			"${http_host}", origHost,

			"${remote_addr}", remoteAddr,
			"${remote_ip}", clientIP,
			"${remote_port}", clientPort,
			"${proxy_add_x_forwarded_for}", proxyAddXFF,

			"${request_uri}", r.RequestURI,
			"${uri}", r.URL.Path,
			"${args}", r.URL.RawQuery,
			"${query_string}", r.URL.RawQuery,
			"${scheme_host}", scheme+"://"+origHost,

			"${http_upgrade}", r.Header.Get("Upgrade"),
			"${http_connection}", r.Header.Get("Connection"),

			"${server_port}", serverPort,

			"${http_range}", r.Header.Get("Range"),
			"${http_if_range}", r.Header.Get("If-Range"),
		)
		return rep.Replace(val)
	}

	h := strings.Split(strings.ReplaceAll(header, "\r\n", "\n"), "\n")
	for _, v := range h {
		hd := strings.SplitN(v, ":", 2)
		if len(hd) == 2 {
			key := strings.TrimSpace(hd[0])
			if key == "" {
				continue
			}
			val := strings.TrimSpace(hd[1])
			val = html.UnescapeString(val)
			if val == "${unset}" {
				if !strings.EqualFold(key, "Host") {
					r.Header.Del(key)
				}
				continue
			}
			val = expandVars(val)
			r.Header.Set(key, val)
		}
	}
}

func (s *HttpProxy) ChangeResponseHeader(resp *http.Response, header string) {
	cfg := s.currentConfig()
	if header == "" {
		return
	}

	if resp == nil || resp.Request == nil {
		return
	}

	httpPort := proxyPortString(cfg.Network.HTTPProxyPort, "80")
	httpsPort := proxyPortString(cfg.Network.HTTPSProxyPort, "443")
	http3Port := proxyPortString(cfg.Network.HTTP3ProxyPort, httpsPort)

	scheme := "http"
	ssl := "off"
	serverPort := httpPort
	if resp.Request.TLS != nil {
		scheme = "https"
		ssl = "on"
		serverPort = httpsPort
	}

	origHost := resp.Request.Host
	hostOnly := common.RemovePortFromHost(origHost)

	remoteAddr := resp.Request.RemoteAddr
	clientIP := common.GetIpByAddr(remoteAddr)
	clientPort := common.GetPortStrByAddr(remoteAddr)

	timeNow := time.Now()

	expandVars := func(val string) string {
		rep := strings.NewReplacer(
			// Protocol/SSL
			"${scheme}", scheme,
			"${ssl}", ssl,

			// Ports
			"${server_port}", serverPort,
			"${server_port_http}", httpPort,
			"${server_port_https}", httpsPort,
			"${server_port_http3}", http3Port,

			// Host info
			"${host}", hostOnly,
			"${http_host}", origHost,

			// Client info
			"${remote_addr}", remoteAddr,
			"${remote_ip}", clientIP,
			"${remote_port}", clientPort,

			// Request info
			"${request_method}", resp.Request.Method,
			"${request_host}", resp.Request.Host,
			"${request_uri}", resp.Request.RequestURI,
			"${request_path}", resp.Request.URL.Path,
			"${uri}", resp.Request.URL.Path,
			"${query_string}", resp.Request.URL.RawQuery,
			"${args}", resp.Request.URL.RawQuery,
			"${origin}", resp.Request.Header.Get("Origin"),
			"${user_agent}", resp.Request.Header.Get("User-Agent"),
			"${http_referer}", resp.Request.Header.Get("Referer"),
			"${scheme_host}", scheme+"://"+origHost,

			// Response info
			"${status}", resp.Status,
			"${status_code}", strconv.Itoa(resp.StatusCode),
			"${content_length}", strconv.FormatInt(resp.ContentLength, 10),
			"${content_type}", resp.Header.Get("Content-Type"),
			"${via}", resp.Header.Get("Via"),

			// Time variables
			"${date}", timeNow.UTC().Format(http.TimeFormat),
			"${timestamp}", strconv.FormatInt(timeNow.UTC().Unix(), 10),
			"${timestamp_ms}", strconv.FormatInt(timeNow.UTC().UnixNano()/1e6, 10),
		)
		return rep.Replace(val)
	}

	h := strings.Split(strings.ReplaceAll(header, "\r\n", "\n"), "\n")
	for _, v := range h {
		hd := strings.SplitN(v, ":", 2)
		if len(hd) == 2 {
			key := strings.TrimSpace(hd[0])
			if key == "" {
				continue
			}
			val := strings.TrimSpace(hd[1])
			val = html.UnescapeString(val)
			if val == "${unset}" {
				resp.Header.Del(key)
				continue
			}
			val = expandVars(val)
			resp.Header.Set(key, val)
		}
	}
}

func (s *HttpProxy) ChangeRedirectURL(r *http.Request, url string) string {
	cfg := s.currentConfig()
	val := strings.TrimSpace(url)
	val = html.UnescapeString(val)

	if !strings.Contains(val, "${") {
		return val
	}

	scheme := "http"
	ssl := "off"
	serverPort := proxyPortString(cfg.Network.HTTPProxyPort, "80")
	if r.TLS != nil {
		scheme = "https"
		ssl = "on"
		serverPort = proxyPortString(cfg.Network.HTTPSProxyPort, "443")
	}

	origHost := r.Host
	hostOnly := common.RemovePortFromHost(origHost)

	remoteAddr := r.RemoteAddr
	clientIP := common.GetIpByAddr(remoteAddr)
	clientPort := common.GetPortStrByAddr(remoteAddr)

	proxyAddXFF := clientIP
	if prior, ok := r.Header["X-Forwarded-For"]; ok {
		proxyAddXFF = strings.Join(prior, ", ") + ", " + clientIP
	}

	rep := strings.NewReplacer(
		"${scheme}", scheme,
		"${ssl}", ssl,
		"${forwarded_ssl}", ssl,

		"${host}", hostOnly,
		"${http_host}", origHost,

		"${remote_addr}", remoteAddr,
		"${remote_ip}", clientIP,
		"${remote_port}", clientPort,
		"${proxy_add_x_forwarded_for}", proxyAddXFF,

		"${request_uri}", r.RequestURI,
		"${uri}", r.URL.Path,
		"${args}", r.URL.RawQuery,
		"${query_string}", r.URL.RawQuery,
		"${scheme_host}", scheme+"://"+origHost,

		"${server_port}", serverPort,
	)

	return rep.Replace(val)
}

func proxyPortString(port int, fallback string) string {
	if port == 0 {
		return fallback
	}
	return strconv.Itoa(port)
}
