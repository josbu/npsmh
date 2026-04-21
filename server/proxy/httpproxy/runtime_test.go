package httpproxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/cache"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	serverproxy "github.com/djylb/nps/server/proxy"
)

func loadHTTPProxyTestConfig(t *testing.T, cfg map[string]any) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "nps.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := servercfg.Load(path); err != nil {
		t.Fatalf("load config: %v", err)
	}

	t.Cleanup(func() {
		restorePath := filepath.Join(dir, "restore.json")
		if err := os.WriteFile(restorePath, []byte("{}"), 0o600); err != nil {
			t.Errorf("write restore config: %v", err)
			return
		}
		if err := servercfg.Load(restorePath); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})
}

func resetHTTPProxyTestDB(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	confDir := filepath.Join(root, "conf")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}

	oldDb := file.GetDb()
	oldIndexes := file.SnapshotRuntimeIndexes()

	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(db)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())

	t.Cleanup(func() {
		file.ReplaceDb(oldDb)
		file.ReplaceRuntimeIndexes(oldIndexes)
	})

	return root
}

func newHTTPProxyTestClient(id int) *file.Client {
	return &file.Client{
		Id:        id,
		VerifyKey: "client-key",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
}

func TestNewHttpProxyWithRootsUsesInjectedProviders(t *testing.T) {
	db := &httpProxyDBStub{}
	cfg := &servercfg.Snapshot{
		Auth:  servercfg.AuthConfig{HTTPOnlyPass: "injected-http-only"},
		Proxy: servercfg.ProxyConfig{ErrorAlways: true},
	}

	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, func() *servercfg.Snapshot {
		return cfg
	}, func() httpProxyDB {
		return db
	})

	if got := httpProxy.currentConfig().Auth.HTTPOnlyPass; got != "injected-http-only" {
		t.Fatalf("currentConfig().Auth.HTTPOnlyPass = %q, want %q", got, "injected-http-only")
	}
	if !httpProxy.currentConfig().Proxy.ErrorAlways {
		t.Fatal("currentConfig().Proxy.ErrorAlways = false, want true from injected config")
	}
	if got := httpProxy.currentDB(); got != db {
		t.Fatalf("currentDB() = %+v, want injected db %+v", got, db)
	}
}

func TestResolveManualCertSourceUsesInjectedDBRoot(t *testing.T) {
	reusable := &file.Host{Id: 9, Host: "*.example.com", CertFile: "shared-cert.pem", KeyFile: "shared-key.pem"}
	db := &httpProxyDBStub{reusableCert: reusable}
	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, servercfg.Current, func() httpProxyDB {
		return db
	})
	server := &HttpsServer{HttpServer: &HttpServer{HttpProxy: httpProxy}}
	host := &file.Host{Id: 3, Host: "api.example.com"}

	if got := server.resolveManualCertSource("api.example.com", host); got != reusable {
		t.Fatalf("resolveManualCertSource() = %+v, want injected reusable host %+v", got, reusable)
	}
	if db.lastServer != "api.example.com" || db.lastHostID != host.Id {
		t.Fatalf("db lookup = (%q, %d), want (%q, %d)", db.lastServer, db.lastHostID, "api.example.com", host.Id)
	}
}

func TestNewHttpProxyUsesCurrentGlobalRoots(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{
		"x_nps_http_only": "global-first",
	})
	resetHTTPProxyTestDB(t)
	initialDB := file.GetDb()

	httpProxy := NewHttpProxy(httpProxyNoCallBridge{}, &file.Tunnel{Flow: &file.Flow{}}, 80, 443, 0, nil)
	if got := httpProxy.currentConfig().Auth.HTTPOnlyPass; got != "global-first" {
		t.Fatalf("currentConfig().Auth.HTTPOnlyPass = %q, want %q", got, "global-first")
	}
	if got := httpProxy.currentDB(); got != initialDB {
		t.Fatalf("currentDB() = %+v, want %+v", got, initialDB)
	}

	loadHTTPProxyTestConfig(t, map[string]any{
		"x_nps_http_only": "global-second",
	})
	if got := httpProxy.currentConfig().Auth.HTTPOnlyPass; got != "global-second" {
		t.Fatalf("currentConfig().Auth.HTTPOnlyPass after global config change = %q, want %q", got, "global-second")
	}

	nextRoot := t.TempDir()
	nextDB := &file.DbUtils{JsonDb: file.NewJsonDb(nextRoot)}
	nextDB.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(nextDB)
	t.Cleanup(func() {
		file.ReplaceDb(initialDB)
	})
	if got := httpProxy.currentDB(); got != nextDB {
		t.Fatalf("currentDB() after global db change = %+v, want %+v", got, nextDB)
	}
}

type httpProxyDBStub struct {
	reusableCert *file.Host
	certHost     *file.Host
	certErr      error
	lastServer   string
	lastHostID   int
	host         *file.Host
	hostErr      error
}

func (s *httpProxyDBStub) FindCertByHost(string) (*file.Host, error) {
	if s.certHost != nil || s.certErr != nil {
		return s.certHost, s.certErr
	}
	return nil, fmt.Errorf("not found")
}

func (s *httpProxyDBStub) FindReusableCertHost(serverName string, hostID int) (*file.Host, error) {
	s.lastServer = serverName
	s.lastHostID = hostID
	if s.reusableCert == nil {
		return nil, fmt.Errorf("not found")
	}
	return s.reusableCert, nil
}

func (s *httpProxyDBStub) GetInfoByHost(string, *http.Request) (*file.Host, error) {
	if s.host != nil || s.hostErr != nil {
		return s.host, s.hostErr
	}
	return nil, fmt.Errorf("not found")
}

type httpProxyNoCallBridge struct{}

func (httpProxyNoCallBridge) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	return nil, fmt.Errorf("unexpected SendLinkInfo call")
}

func (httpProxyNoCallBridge) IsServer() bool { return false }

func (httpProxyNoCallBridge) CliProcess(*conn.Conn, string) {}

func TestHttpServerHandleProxyRejectsMalformedHost(t *testing.T) {
	db := &httpProxyDBStub{
		host: &file.Host{
			Id:   17,
			Flow: &file.Flow{},
			Client: &file.Client{
				Id:   29,
				Cnf:  &file.Config{},
				Flow: &file.Flow{},
			},
		},
	}
	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	}, func() httpProxyDB {
		return db
	})
	server := &HttpServer{HttpProxy: httpProxy}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	recorder := httptest.NewRecorder()

	server.handleProxy(recorder, req)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("handleProxy() status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
}

func TestHttpServerHandleProxyReturnsForbiddenForDestinationDenied(t *testing.T) {
	user := &file.User{
		Id:           6,
		Status:       1,
		TotalFlow:    &file.Flow{},
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "10.0.0.0/8",
	}
	file.InitializeUserRuntime(user)

	client := newHTTPProxyTestClient(30)
	client.SetOwnerUserID(user.Id)
	client.BindOwnerUser(user)

	db := &httpProxyDBStub{
		host: &file.Host{
			Id:     18,
			Client: client,
			Flow:   &file.Flow{},
			Target: &file.Target{TargetStr: "10.0.0.8:80"},
		},
	}
	httpProxy := NewHttpProxyWithRoots(serverproxy.NewBaseServer(httpProxyNoCallBridge{}, nil), 80, 443, 0, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	}, func() httpProxyDB {
		return db
	})
	server := &HttpServer{HttpProxy: httpProxy}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	recorder := httptest.NewRecorder()

	server.handleProxy(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("handleProxy() status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestHttpServerHandleProxyTreatsNilHostAsNotFound(t *testing.T) {
	db := &httpProxyDBStub{}
	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Proxy: servercfg.ProxyConfig{ErrorAlways: true},
		}
	}, func() httpProxyDB {
		return db
	})
	server := &HttpServer{HttpProxy: httpProxy}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	recorder := httptest.NewRecorder()

	server.handleProxy(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("handleProxy() status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestHttpProxyAutoSSLDecisionRejectsNilHost(t *testing.T) {
	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	}, func() httpProxyDB {
		return &httpProxyDBStub{}
	})

	err := httpProxy.decideAutoSSLHost("nil.example.com")
	if err == nil || !strings.Contains(err.Error(), `unknown host "nil.example.com"`) {
		t.Fatalf("DecisionFunc() err = %v, want unknown host error", err)
	}
}

func TestHttpProxyCurrentConfigAndErrorPages(t *testing.T) {
	dir := t.TempDir()
	notFoundPath := filepath.Join(dir, "notfound.html")
	timeLimitPath := filepath.Join(dir, "time.html")
	flowLimitPath := filepath.Join(dir, "flow.html")

	if err := os.WriteFile(notFoundPath, []byte("not-found-page"), 0o600); err != nil {
		t.Fatalf("write not found page: %v", err)
	}
	if err := os.WriteFile(timeLimitPath, []byte("time-limit-page"), 0o600); err != nil {
		t.Fatalf("write time limit page: %v", err)
	}
	if err := os.WriteFile(flowLimitPath, []byte("flow-limit-page"), 0o600); err != nil {
		t.Fatalf("write flow limit page: %v", err)
	}

	loadHTTPProxyTestConfig(t, map[string]any{
		"x_nps_http_only":             "http-only-token",
		"http_proxy_port":             8080,
		"https_proxy_port":            8443,
		"http3_proxy_port":            9443,
		"error_page":                  notFoundPath,
		"error_page_time_limit":       timeLimitPath,
		"error_page_flow_limit":       flowLimitPath,
		"error_always":                true,
		"force_auto_ssl":              true,
		"http_proxy_response_timeout": 17,
	})

	proxy := &HttpProxy{
		BaseServer:            &serverproxy.BaseServer{},
		TimeLimitErrorContent: []byte("inline-time"),
		FlowLimitErrorContent: []byte("inline-flow"),
	}
	proxy.ErrorContent = []byte("inline-not-found")

	if got := proxy.currentConfig().Auth.HTTPOnlyPass; got != "http-only-token" {
		t.Fatalf("currentConfig().Auth.HTTPOnlyPass = %q, want %q", got, "http-only-token")
	}
	if !proxy.currentConfig().Proxy.ErrorAlways {
		t.Fatal("currentConfig().Proxy.ErrorAlways = false, want true")
	}
	if !proxy.currentConfig().Proxy.ForceAutoSSL {
		t.Fatal("currentConfig().Proxy.ForceAutoSSL = false, want true")
	}
	if got := proxy.currentConfig().ProxyResponseHeaderTimeout(); got != 17*time.Second {
		t.Fatalf("currentConfig().ProxyResponseHeaderTimeout() = %v, want %v", got, 17*time.Second)
	}
	if got := string(proxy.currentNotFoundErrorContent()); got != "not-found-page" {
		t.Fatalf("currentNotFoundErrorContent() = %q, want %q", got, "not-found-page")
	}
	if got := string(proxy.currentTimeLimitErrorContent()); got != "time-limit-page" {
		t.Fatalf("currentTimeLimitErrorContent() = %q, want %q", got, "time-limit-page")
	}
	if got := string(proxy.currentFlowLimitErrorContent()); got != "flow-limit-page" {
		t.Fatalf("currentFlowLimitErrorContent() = %q, want %q", got, "flow-limit-page")
	}
	if got := proxy.loadOptionalErrorPage("   "); got != nil {
		t.Fatalf("loadOptionalErrorPage(blank) = %q, want nil", string(got))
	}
	if got := proxy.loadOptionalErrorPage(filepath.Join(dir, "missing.html")); got != nil {
		t.Fatalf("loadOptionalErrorPage(missing) = %q, want nil", string(got))
	}
	if got := proxyPortString(0, "80"); got != "80" {
		t.Fatalf("proxyPortString(0) = %q, want %q", got, "80")
	}
	if got := proxyPortString(18080, "80"); got != "18080" {
		t.Fatalf("proxyPortString(18080) = %q, want %q", got, "18080")
	}
}

func TestHttpProxyCurrentErrorPagesFallbackToInlineContent(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	proxy := &HttpProxy{
		BaseServer:            &serverproxy.BaseServer{},
		TimeLimitErrorContent: []byte("inline-time"),
		FlowLimitErrorContent: []byte("inline-flow"),
	}
	proxy.ErrorContent = []byte("inline-not-found")

	if got := string(proxy.currentNotFoundErrorContent()); got != "inline-not-found" {
		t.Fatalf("currentNotFoundErrorContent() = %q, want %q", got, "inline-not-found")
	}
	if got := string(proxy.currentTimeLimitErrorContent()); got != "inline-time" {
		t.Fatalf("currentTimeLimitErrorContent() = %q, want %q", got, "inline-time")
	}
	if got := string(proxy.currentFlowLimitErrorContent()); got != "inline-flow" {
		t.Fatalf("currentFlowLimitErrorContent() = %q, want %q", got, "inline-flow")
	}
}

func TestHttpProxyCurrentResponseHeaderTimeoutNormalizesNonPositiveValues(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{
		"http_proxy_response_timeout": 0,
	})

	proxy := &HttpProxy{
		BaseServer: &serverproxy.BaseServer{},
	}
	if got := proxy.currentConfig().ProxyResponseHeaderTimeout(); got != 100*time.Second {
		t.Fatalf("currentConfig().ProxyResponseHeaderTimeout() with zero = %v, want %v", got, 100*time.Second)
	}

	loadHTTPProxyTestConfig(t, map[string]any{
		"http_proxy_response_timeout": -15,
	})
	if got := proxy.currentConfig().ProxyResponseHeaderTimeout(); got != 100*time.Second {
		t.Fatalf("currentConfig().ProxyResponseHeaderTimeout() with negative = %v, want %v", got, 100*time.Second)
	}
}

func TestResolveProxySSLCacheConfigNormalizesNegativeValues(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Proxy: servercfg.ProxyConfig{
			SSL: servercfg.SSLConfig{
				CacheMax:    -1,
				CacheReload: -2,
				CacheIdle:   -3,
			},
		},
	}

	cacheCfg := resolveProxySSLCacheConfig(cfg)
	if cacheCfg.maxEntries != 0 {
		t.Fatalf("resolveProxySSLCacheConfig().maxEntries = %d, want 0", cacheCfg.maxEntries)
	}
	if cacheCfg.reloadInterval != 0 {
		t.Fatalf("resolveProxySSLCacheConfig().reloadInterval = %v, want 0", cacheCfg.reloadInterval)
	}
	if cacheCfg.idleInterval != 60*time.Minute {
		t.Fatalf("resolveProxySSLCacheConfig().idleInterval = %v, want %v", cacheCfg.idleInterval, 60*time.Minute)
	}
}

func TestHttpServerWriteLimitErrorPage(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	server := &HttpServer{
		HttpProxy: &HttpProxy{
			TimeLimitErrorContent: []byte("time-limit-page"),
			FlowLimitErrorContent: []byte("flow-limit-page"),
		},
	}

	t.Run("time limit", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		if ok := server.writeLimitErrorPage(recorder, "Task time limit exceeded for client 1"); !ok {
			t.Fatal("writeLimitErrorPage() = false, want true for time limit error")
		}
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
		}
		if got := recorder.Body.String(); got != "time-limit-page" {
			t.Fatalf("body = %q, want %q", got, "time-limit-page")
		}
	})

	t.Run("flow limit", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		if ok := server.writeLimitErrorPage(recorder, "Client flow limit exceeded"); !ok {
			t.Fatal("writeLimitErrorPage() = false, want true for flow limit error")
		}
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTooManyRequests)
		}
		if got := recorder.Body.String(); got != "flow-limit-page" {
			t.Fatalf("body = %q, want %q", got, "flow-limit-page")
		}
	})

	t.Run("unrecognized error", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		if ok := server.writeLimitErrorPage(recorder, "backend unavailable"); ok {
			t.Fatal("writeLimitErrorPage() = true, want false for unrelated error")
		}
		if recorder.Body.Len() != 0 {
			t.Fatalf("body = %q, want empty", recorder.Body.String())
		}
	})
}

func TestHttpServerWriteProxyErrorTreatsWrappedEOFAs521(t *testing.T) {
	loadHTTPProxyTestConfig(t, map[string]any{})

	server := &HttpServer{
		HttpProxy: &HttpProxy{},
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	recorder := httptest.NewRecorder()

	server.writeProxyError(recorder, req, fmt.Errorf("wrapped eof: %w", io.EOF))

	if recorder.Code != 521 {
		t.Fatalf("status = %d, want 521", recorder.Code)
	}
}

func TestHostHasManualCertificate(t *testing.T) {
	if hostHasManualCertificate(nil) {
		t.Fatal("hostHasManualCertificate(nil) = true, want false")
	}
	if hostHasManualCertificate(&file.Host{CertFile: "  ", KeyFile: "key.pem"}) {
		t.Fatal("hostHasManualCertificate() = true, want false when cert path is blank")
	}
	if !hostHasManualCertificate(&file.Host{CertFile: "cert.pem", KeyFile: "key.pem"}) {
		t.Fatal("hostHasManualCertificate() = false, want true")
	}
}

func TestResolveManualCertSource(t *testing.T) {
	resetHTTPProxyTestDB(t)

	client := newHTTPProxyTestClient(1)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	hostWithoutCert := &file.Host{
		Id:       1,
		Host:     "api.example.com",
		Location: "/",
		Scheme:   "all",
		Client:   client,
		Flow:     &file.Flow{},
		Target:   &file.Target{TargetStr: "127.0.0.1:80"},
	}
	if err := file.GetDb().NewHost(hostWithoutCert); err != nil {
		t.Fatalf("NewHost(hostWithoutCert) error = %v", err)
	}

	reusable := &file.Host{
		Id:       2,
		Host:     "*.example.com",
		Location: "/",
		Scheme:   "all",
		CertFile: "shared-cert.pem",
		KeyFile:  "shared-key.pem",
		Client:   client,
		Flow:     &file.Flow{},
		Target:   &file.Target{TargetStr: "127.0.0.1:80"},
	}
	if err := file.GetDb().NewHost(reusable); err != nil {
		t.Fatalf("NewHost(reusable) error = %v", err)
	}

	server := &HttpsServer{
		HttpServer: &HttpServer{
			HttpProxy: NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, servercfg.Current, func() httpProxyDB {
				return file.GetDb()
			}),
		},
	}

	if got := server.resolveManualCertSource("api.example.com", nil); got != nil {
		t.Fatalf("resolveManualCertSource(nil) = %+v, want nil", got)
	}

	autoSSLHost := &file.Host{Id: 3, Host: "auto.example.com", AutoSSL: true}
	if got := server.resolveManualCertSource("auto.example.com", autoSSLHost); got != autoSSLHost {
		t.Fatalf("resolveManualCertSource(autoSSL) = %+v, want original host", got)
	}

	manualHost := &file.Host{Id: 4, Host: "manual.example.com", CertFile: "cert.pem", KeyFile: "key.pem"}
	if got := server.resolveManualCertSource("manual.example.com", manualHost); got != manualHost {
		t.Fatalf("resolveManualCertSource(manual) = %+v, want original host", got)
	}

	if got := server.resolveManualCertSource("api.example.com", hostWithoutCert); got != reusable {
		t.Fatalf("resolveManualCertSource(reuse) = %+v, want reusable host %+v", got, reusable)
	}

	if got := server.resolveManualCertSource("api.other.test", hostWithoutCert); got != hostWithoutCert {
		t.Fatalf("resolveManualCertSource(no match) = %+v, want original host %+v", got, hostWithoutCert)
	}
}

func TestResolveManualCertSourceTreatsNilReusableLookupAsOriginalHost(t *testing.T) {
	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, servercfg.Current, func() httpProxyDB {
		return &httpProxyDBStub{}
	})
	server := &HttpsServer{HttpServer: &HttpServer{HttpProxy: httpProxy}}
	hostWithoutCert := &file.Host{Id: 8, Host: "api.example.com"}

	if got := server.resolveManualCertSource("api.example.com", hostWithoutCert); got != hostWithoutCert {
		t.Fatalf("resolveManualCertSource(nil reusable) = %+v, want original host %+v", got, hostWithoutCert)
	}
}

func TestLoadHostedCertificateFallsBackToDefaultCert(t *testing.T) {
	certPEM, keyPEM := generateHTTPSProxyTestPEM(t)
	dir := t.TempDir()
	defaultCert := filepath.Join(dir, "default-cert.pem")
	defaultKey := filepath.Join(dir, "default-key.pem")
	if err := os.WriteFile(defaultCert, []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write default cert: %v", err)
	}
	if err := os.WriteFile(defaultKey, []byte(keyPEM), 0o600); err != nil {
		t.Fatalf("write default key: %v", err)
	}

	server := &HttpsServer{
		cert:            cache.NewCertManager(8, 0, 0),
		hasDefaultCert:  true,
		defaultCertFile: defaultCert,
		defaultKeyFile:  defaultKey,
		defaultCertHash: crypt.FNV1a64("file", defaultCert, defaultKey),
	}
	t.Cleanup(server.cert.Stop)

	host := &file.Host{
		Id:       10,
		CertFile: filepath.Join(dir, "missing-cert.pem"),
		KeyFile:  filepath.Join(dir, "missing-key.pem"),
		CertType: "file",
		CertHash: "missing-host-cert",
	}

	cert, err := server.loadHostedCertificate("fallback.example.com", host)
	if err != nil {
		t.Fatalf("loadHostedCertificate() error = %v", err)
	}
	if cert == nil {
		t.Fatal("loadHostedCertificate() returned nil certificate")
	}
}

func generateHTTPSProxyTestPEM(t *testing.T) (string, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key failed: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate failed: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return string(certPEM), string(keyPEM)
}

func TestCheckHTTPAndRedirectWritesRedirectResponse(t *testing.T) {
	resetHTTPProxyTestDB(t)

	client := newHTTPProxyTestClient(1)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:       1,
		Host:     "demo.example.com",
		Location: "/",
		Scheme:   "all",
		Client:   client,
		Flow:     &file.Flow{},
		Target:   &file.Target{TargetStr: "127.0.0.1:80"},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	server := &HttpsServer{
		HttpServer: &HttpServer{
			HttpProxy: &HttpProxy{
				BaseServer: &serverproxy.BaseServer{},
			},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.checkHTTPAndRedirect(serverConn, []byte("GET /hello?q=1 HTTP/1.1\r\nHost: demo.example.com\r\n\r\n"))
	}()

	data, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	<-done

	text := string(data)
	if !strings.Contains(text, "HTTP/1.1 302 Found\r\n") {
		t.Fatalf("response = %q, want 302 status line", text)
	}
	if !strings.Contains(text, "Location: https://demo.example.com/hello?q=1\r\n") {
		t.Fatalf("response = %q, want redirect location", text)
	}
}

func TestCheckHTTPAndRedirectIgnoresUnknownHost(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	server := &HttpsServer{
		HttpServer: &HttpServer{
			HttpProxy: &HttpProxy{
				BaseServer: &serverproxy.BaseServer{},
			},
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.checkHTTPAndRedirect(serverConn, []byte("GET /missing HTTP/1.1\r\nHost: missing.example.com\r\n\r\n"))
	}()

	data, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	<-done

	if len(data) != 0 {
		t.Fatalf("response = %q, want empty response for unknown host", string(data))
	}
}

func TestCheckHTTPAndRedirectIgnoresNilHostLookup(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	httpProxy := NewHttpProxyWithRoots(&serverproxy.BaseServer{}, 80, 443, 0, nil, func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	}, func() httpProxyDB {
		return &httpProxyDBStub{}
	})
	server := &HttpsServer{
		HttpServer: &HttpServer{HttpProxy: httpProxy},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.checkHTTPAndRedirect(serverConn, []byte("GET /nil HTTP/1.1\r\nHost: nil.example.com\r\n\r\n"))
	}()

	data, err := io.ReadAll(clientConn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	<-done

	if len(data) != 0 {
		t.Fatalf("response = %q, want empty response for nil host lookup", string(data))
	}
}

func TestHttpServerStartReturnsNilOnClose(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	server := &HttpServer{
		HttpProxy:    &HttpProxy{},
		httpListener: listener,
	}
	server.httpServer = server.NewServer(0, "http")

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	_ = conn.Close()

	time.Sleep(50 * time.Millisecond)
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Start() error = %v, want nil after normal close", err)
	}
}

type stubAddr string

func (a stubAddr) Network() string { return "tcp" }

func (a stubAddr) String() string { return string(a) }

type stubListener struct {
	addr net.Addr
}

func (l *stubListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }

func (l *stubListener) Close() error { return nil }

func (l *stubListener) Addr() net.Addr { return l.addr }

func TestHttpsServerCloseAllowsNilFields(t *testing.T) {
	var server HttpsServer
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestHttpsListenerAddr(t *testing.T) {
	if got := (*HttpsListener)(nil).Addr(); got != nil {
		t.Fatalf("Addr(nil listener) = %v, want nil", got)
	}

	listener := NewHttpsListener(nil)
	if got := listener.Addr(); got != nil {
		t.Fatalf("Addr(nil parent) = %v, want nil", got)
	}

	parent := &stubListener{addr: stubAddr("127.0.0.1:443")}
	listener = NewHttpsListener(parent)
	if got := listener.Addr(); got == nil || got.String() != "127.0.0.1:443" {
		t.Fatalf("Addr() = %v, want parent addr 127.0.0.1:443", got)
	}
}
