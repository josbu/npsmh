package connection

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func TestInitConnectionServiceBuildsMultipleSharedMuxGroups(t *testing.T) {
	cleanupSharedMuxes(t)

	httpPort := getAvailablePort(t)
	tlsPort := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
web_host = web.example.com
bridge_tcp_ip = 127.0.0.1
bridge_tls_ip = 127.0.0.1
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
bridge_tcp_port = %d
bridge_tls_port = %d
http_proxy_port = %d
https_proxy_port = %d
web_port = %d
`, httpPort, tlsPort, httpPort, tlsPort, httpPort))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("InitConnectionService() shared mux error = %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	if len(sharedMuxManager.muxByKey) != 2 {
		t.Fatalf("expected 2 shared mux groups, got %d", len(sharedMuxManager.muxByKey))
	}

	httpMux := sharedMuxManager.muxByRoute[routeBridgeTCP]
	if httpMux == nil || httpMux != sharedMuxManager.muxByRoute[routeHTTPProxy] || httpMux != sharedMuxManager.muxByRoute[routeWeb] {
		t.Fatal("bridge tcp/http/web routes should share the same mux")
	}
	tlsMux := sharedMuxManager.muxByRoute[routeBridgeTLS]
	if tlsMux == nil || tlsMux != sharedMuxManager.muxByRoute[routeHTTPS] {
		t.Fatal("bridge tls/https routes should share the same mux")
	}
	if httpMux == tlsMux {
		t.Fatal("expected distinct mux instances for distinct listen groups")
	}
}

func TestInitConnectionServiceRejectsBindIPConflictOnSharedPort(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_tcp_ip = 127.0.0.1
bridge_tcp_port = %d
http_proxy_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected shared mux bind ip conflict")
	}
	if !strings.Contains(sharedMuxErr.Error(), "same bind ip") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceSupportsSharedWebHTTPS(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
web_host = web.example.com
web_open_ssl = true
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
https_proxy_port = %d
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	if sharedMuxManager.muxByRoute[routeWeb] == nil || sharedMuxManager.muxByRoute[routeWeb] != sharedMuxManager.muxByRoute[routeHTTPS] {
		t.Fatal("expected web manager and https proxy to share the same mux")
	}
	if !WebOpenSSL {
		t.Fatal("expected WebOpenSSL to be true")
	}
}

func TestInitConnectionServiceRejectsSharedTLSWebWithoutWebHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
web_open_ssl = true
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
https_proxy_port = %d
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected missing web_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "web_host") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceRejectsSharedTLSWebWithWildcardWebHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
web_host = *.example.com
web_open_ssl = true
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
https_proxy_port = %d
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected wildcard web_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "wildcard") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceRejectsSharedHTTPWebWithoutWebHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
http_proxy_port = %d
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected missing web_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "web_host") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceRejectsSharedHTTPBridgeWSWithoutBridgeHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_ws_ip = 127.0.0.1
bridge_ws_port = %d
bridge_path = /bridge
http_proxy_ip = 127.0.0.1
http_proxy_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected missing bridge_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "bridge_host") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceRejectsSharedHTTPBridgeWSWithWildcardBridgeHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = *.example.com
bridge_ws_ip = 127.0.0.1
bridge_ws_port = %d
bridge_path = /bridge
http_proxy_ip = 127.0.0.1
http_proxy_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected wildcard bridge_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "wildcard") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceRejectsSharedHTTPWebAndBridgeWSWithSameHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = edge.example.com
web_host = edge.example.com
bridge_ws_ip = 127.0.0.1
bridge_ws_port = %d
bridge_path = /bridge
web_ip = 127.0.0.1
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected same host conflict error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "differ") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceSupportsSharedHTTPSBridgeWSS(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
bridge_wss_ip = 127.0.0.1
bridge_wss_port = %d
bridge_path = /wss
http_proxy_ip = 127.0.0.1
https_proxy_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	if sharedMuxManager.muxByRoute[routeBridgeWSS] == nil || sharedMuxManager.muxByRoute[routeBridgeWSS] != sharedMuxManager.muxByRoute[routeHTTPS] {
		t.Fatal("expected bridge wss and https proxy to share the same mux")
	}
}

func TestInitConnectionServiceSupportsSharedHTTPSBridgeTLS(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
bridge_tls_ip = 127.0.0.1
bridge_tls_port = %d
http_proxy_ip = 127.0.0.1
https_proxy_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	if sharedMuxManager.muxByRoute[routeBridgeTLS] == nil || sharedMuxManager.muxByRoute[routeBridgeTLS] != sharedMuxManager.muxByRoute[routeHTTPS] {
		t.Fatal("expected bridge tls and https proxy to share the same mux")
	}
}

func TestInitConnectionServiceSupportsSharedBridgeTLSAndWSS(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
bridge_tls_ip = 127.0.0.1
bridge_wss_ip = 127.0.0.1
bridge_tls_port = %d
bridge_wss_port = %d
bridge_path = /wss
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	if sharedMuxManager.muxByRoute[routeBridgeTLS] == nil || sharedMuxManager.muxByRoute[routeBridgeTLS] != sharedMuxManager.muxByRoute[routeBridgeWSS] {
		t.Fatal("expected bridge tls and bridge wss to share the same mux")
	}
}

func TestInitConnectionServiceSupportsSharedBridgeTLSWSSAndHTTPS(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
bridge_tls_ip = 127.0.0.1
bridge_wss_ip = 127.0.0.1
bridge_tls_port = %d
bridge_wss_port = %d
bridge_path = /wss
http_proxy_ip = 127.0.0.1
https_proxy_port = %d
`, port, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	mux := sharedMuxManager.muxByRoute[routeBridgeTLS]
	if mux == nil || mux != sharedMuxManager.muxByRoute[routeBridgeWSS] || mux != sharedMuxManager.muxByRoute[routeHTTPS] {
		t.Fatal("expected bridge tls, bridge wss and https proxy to share the same mux")
	}
}

func TestInitConnectionServiceRejectsSharedHTTPSBridgeWSSWithoutBridgeHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_wss_ip = 127.0.0.1
bridge_wss_port = %d
bridge_path = /wss
http_proxy_ip = 127.0.0.1
https_proxy_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected missing bridge_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "bridge_host") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceRejectsSharedBridgeTLSAndWSSWithoutBridgeHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_tls_ip = 127.0.0.1
bridge_wss_ip = 127.0.0.1
bridge_tls_port = %d
bridge_wss_port = %d
bridge_path = /wss
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected missing bridge_host error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "bridge_host") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceSupportsSharedTLSWebAndBridgeWSS(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
web_host = web.example.com
web_open_ssl = true
bridge_wss_ip = 127.0.0.1
bridge_wss_port = %d
bridge_path = /wss
web_ip = 127.0.0.1
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	if sharedMuxManager.muxByRoute[routeBridgeWSS] == nil || sharedMuxManager.muxByRoute[routeBridgeWSS] != sharedMuxManager.muxByRoute[routeWeb] {
		t.Fatal("expected bridge wss and web manager to share the same mux")
	}
}

func TestInitConnectionServiceSupportsAllSharedOnSinglePort(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
web_host = web.example.com
web_open_ssl = true
bridge_tcp_ip = 127.0.0.1
bridge_tls_ip = 127.0.0.1
bridge_ws_ip = 127.0.0.1
bridge_wss_ip = 127.0.0.1
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
bridge_tcp_port = %d
bridge_tls_port = %d
bridge_ws_port = %d
bridge_wss_port = %d
http_proxy_port = %d
https_proxy_port = %d
web_port = %d
bridge_path = /wss
`, port, port, port, port, port, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr != nil {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created")
	}
	mux := sharedMuxManager.muxByRoute[routeBridgeTCP]
	if mux == nil {
		t.Fatal("expected shared mux to exist")
	}
	for _, kind := range []sharedRouteKind{routeBridgeTLS, routeBridgeWS, routeBridgeWSS, routeHTTPProxy, routeHTTPS, routeWeb} {
		if sharedMuxManager.muxByRoute[kind] != mux {
			t.Fatalf("expected route %s to share the same mux", kind)
		}
	}
}

func TestInitConnectionServiceRejectsSharedTLSWebAndBridgeWithSameHost(t *testing.T) {
	cleanupSharedMuxes(t)

	port := getAvailablePort(t)
	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = edge.example.com
web_host = edge.example.com
web_open_ssl = true
bridge_wss_ip = 127.0.0.1
bridge_wss_port = %d
bridge_path = /wss
web_ip = 127.0.0.1
web_port = %d
`, port, port))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected same host conflict error")
	}
	if !strings.Contains(sharedMuxErr.Error(), "differ") {
		t.Fatalf("unexpected shared mux error: %v", sharedMuxErr)
	}
}

func TestInitConnectionServiceClosesStartedMuxesWhenLaterGroupFails(t *testing.T) {
	cleanupSharedMuxes(t)

	firstPort := getAvailablePort(t)
	occupied, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer func() { _ = occupied.Close() }()
	secondPort := occupied.Addr().(*net.TCPAddr).Port

	configPath := writeTestConfig(t, fmt.Sprintf(`
bridge_host = bridge.example.com
web_host = web.example.com
web_open_ssl = true
bridge_tcp_ip = 127.0.0.1
bridge_tls_ip = 127.0.0.1
http_proxy_ip = 127.0.0.1
web_ip = 127.0.0.1
bridge_tcp_port = %d
http_proxy_port = %d
bridge_tls_port = %d
https_proxy_port = %d
web_port = %d
`, firstPort, firstPort, secondPort, secondPort, secondPort))

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}

	InitConnectionService()

	if sharedMuxErr == nil {
		t.Fatal("expected shared mux startup error")
	}
	if sharedMuxManager != nil {
		t.Fatal("shared mux manager should not be published on startup failure")
	}

	_ = occupied.Close()
	retry, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: firstPort})
	if err != nil {
		t.Fatalf("expected first shared port to be released after failed init: %v", err)
	}
	_ = retry.Close()
}
