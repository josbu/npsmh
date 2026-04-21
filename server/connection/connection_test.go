package connection

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/servercfg"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nps-test.conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func getAvailablePort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("alloc port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func cleanupSharedMuxes(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		resetSharedMuxManager()
	})
}

func TestInitConnectionServiceLoadsConfig(t *testing.T) {
	configPath := writeTestConfig(t, `
bridge_ip = 127.0.0.2
bridge_tcp_ip = 127.0.0.3
bridge_kcp_ip = 127.0.0.4
bridge_quic_ip = 127.0.0.5
bridge_tls_ip = 127.0.0.6
bridge_ws_ip = 127.0.0.7
bridge_wss_ip = 127.0.0.8
bridge_port = 18080
bridge_tcp_port = 18081
bridge_kcp_port = 18082
bridge_quic_port = 18083
bridge_tls_port = 18084
bridge_ws_port = 18085
bridge_wss_port = 18086
bridge_path = /bridge
bridge_trusted_ips = 127.0.0.1
bridge_real_ip_header = X-Real-IP
http_proxy_ip = 127.0.0.9
http_proxy_port = 19080
https_proxy_port = 19443
http3_proxy_port = 19444
web_ip = 127.0.0.10
web_port = 18000
p2p_ip = 127.0.0.11
p2p_port = 17000
quic_alpn = nps,test
quic_keep_alive_period = 12
quic_max_idle_timeout = 34
quic_max_incoming_streams = 999
mux_ping_interval = 8
`)

	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("load server config: %v", err)
	}
	resetSharedMuxManager()
	cleanupSharedMuxes(t)

	InitConnectionService()

	if BridgeIp != "127.0.0.2" || BridgeTcpIp != "127.0.0.3" || BridgeTlsIp != "127.0.0.6" {
		t.Fatalf("bridge ip fields not loaded correctly")
	}
	if BridgePath != "/bridge" || BridgeTrustedIps != "127.0.0.1" || BridgeRealIpHeader != "X-Real-IP" {
		t.Fatalf("bridge path/trusted fields not loaded correctly")
	}
	if BridgePort != 18080 || BridgeTcpPort != 18081 || BridgeWssPort != 18086 {
		t.Fatalf("bridge ports not loaded correctly")
	}
	if HttpIp != "127.0.0.9" || HttpPort != 19080 || HttpsPort != 19443 || Http3Port != 19444 {
		t.Fatalf("http settings not loaded correctly")
	}
	if WebIp != "127.0.0.10" || WebPort != 18000 || P2pIp != "127.0.0.11" || P2pPort != 17000 {
		t.Fatalf("web/p2p settings not loaded correctly")
	}
	if len(QuicAlpn) != 2 || QuicAlpn[0] != "nps" || QuicAlpn[1] != "test" {
		t.Fatalf("quic alpn not split correctly: %#v", QuicAlpn)
	}
	if QuicKeepAliveSec != 12 || QuicIdleTimeoutSec != 34 || QuicMaxStreams != 999 {
		t.Fatalf("quic values not loaded correctly")
	}
	if MuxPingIntervalSec != 8 || mux.PingInterval != 8*time.Second {
		t.Fatalf("mux ping interval not loaded correctly")
	}
}

func TestApplySnapshotNormalizesInvalidTransportRuntimeValues(t *testing.T) {
	resetSharedMuxManager()
	cleanupSharedMuxes(t)

	previousPingInterval := mux.PingInterval
	t.Cleanup(func() {
		mux.PingInterval = previousPingInterval
	})

	ApplySnapshot(&servercfg.Snapshot{
		Network: servercfg.NetworkConfig{
			QUICALPNList:           []string{"", "   "},
			QUICKeepAlivePeriod:    0,
			QUICMaxIdleTimeout:     -7,
			QUICMaxIncomingStreams: 0,
			MuxPingInterval:        -3,
		},
	})

	if len(QuicAlpn) != 1 || QuicAlpn[0] != "nps" {
		t.Fatalf("QuicAlpn = %#v, want fallback [\"nps\"]", QuicAlpn)
	}
	if QuicKeepAliveSec != 10 {
		t.Fatalf("QuicKeepAliveSec = %d, want 10", QuicKeepAliveSec)
	}
	if QuicIdleTimeoutSec != 30 {
		t.Fatalf("QuicIdleTimeoutSec = %d, want 30", QuicIdleTimeoutSec)
	}
	if QuicMaxStreams != 100000 {
		t.Fatalf("QuicMaxStreams = %d, want 100000", QuicMaxStreams)
	}
	if MuxPingIntervalSec != 5 || mux.PingInterval != 5*time.Second {
		t.Fatalf("MuxPingIntervalSec/mux.PingInterval = %d/%v, want 5/5s", MuxPingIntervalSec, mux.PingInterval)
	}
}

func TestApplySnapshotTrimsTransportALPNList(t *testing.T) {
	resetSharedMuxManager()
	cleanupSharedMuxes(t)

	ApplySnapshot(&servercfg.Snapshot{
		Network: servercfg.NetworkConfig{
			QUICALPNList:           []string{" nps ", "", "custom "},
			QUICKeepAlivePeriod:    12,
			QUICMaxIdleTimeout:     34,
			QUICMaxIncomingStreams: 999,
			MuxPingInterval:        8,
		},
	})

	if len(QuicAlpn) != 2 || QuicAlpn[0] != "nps" || QuicAlpn[1] != "custom" {
		t.Fatalf("QuicAlpn = %#v, want trimmed values", QuicAlpn)
	}
	if QuicKeepAliveSec != 12 || QuicIdleTimeoutSec != 34 || QuicMaxStreams != 999 || MuxPingIntervalSec != 8 {
		t.Fatalf("transport settings mutated unexpectedly: keepalive=%d idle=%d streams=%d ping=%d", QuicKeepAliveSec, QuicIdleTimeoutSec, QuicMaxStreams, MuxPingIntervalSec)
	}
}

func TestApplySnapshotRefreshesRuntimeConfigAndResetsSharedMux(t *testing.T) {
	cleanupSharedMuxes(t)

	sharedPort := getAvailablePort(t)
	first := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Host: "web.example.com",
		},
		Network: servercfg.NetworkConfig{
			BridgeHost:         "bridge.example.com",
			BridgePath:         "/bridge",
			BridgeTrustedIPs:   "127.0.0.1",
			BridgeRealIPHeader: "X-Real-IP",
			HTTPProxyIP:        "127.0.0.1",
			HTTPProxyPort:      sharedPort,
			WebIP:              "127.0.0.1",
			WebPort:            sharedPort,
		},
	}
	ApplySnapshot(first)

	if BridgePath != "/bridge" || BridgeTrustedIps != "127.0.0.1" || BridgeRealIpHeader != "X-Real-IP" {
		t.Fatalf("initial connection config not applied")
	}
	if sharedMuxManager == nil {
		t.Fatal("expected shared mux manager to be created for initial config")
	}

	second := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Host: "web-v2.example.com",
		},
		Network: servercfg.NetworkConfig{
			BridgeHost:         "bridge-v2.example.com",
			BridgePath:         "/bridge-v2",
			BridgeTrustedIPs:   "10.0.0.0/8",
			BridgeRealIPHeader: "X-Forwarded-For",
			HTTPProxyIP:        "127.0.0.1",
			HTTPProxyPort:      0,
			WebIP:              "127.0.0.1",
			WebPort:            0,
		},
	}
	ApplySnapshot(second)

	if BridgeHost != "bridge-v2.example.com" {
		t.Fatalf("BridgeHost = %q, want %q", BridgeHost, "bridge-v2.example.com")
	}
	if BridgePath != "/bridge-v2" {
		t.Fatalf("BridgePath = %q, want %q", BridgePath, "/bridge-v2")
	}
	if BridgeTrustedIps != "10.0.0.0/8" || BridgeRealIpHeader != "X-Forwarded-For" {
		t.Fatalf("bridge gateway metadata not refreshed")
	}
	if sharedMuxManager != nil {
		t.Fatal("expected shared mux manager to be cleared when no shared listeners remain")
	}
}

func TestApplySnapshotInvalidPrimaryBridgePortFallsBackToZero(t *testing.T) {
	cleanupSharedMuxes(t)

	snapshot := &servercfg.Snapshot{
		Network: servercfg.NetworkConfig{
			BridgePort:    70000,
			BridgeTCPPort: 18081,
		},
	}

	ApplySnapshot(snapshot)

	if BridgePort != 0 {
		t.Fatalf("BridgePort = %d, want 0 after invalid primary port normalization", BridgePort)
	}
	if BridgeTcpPort != 18081 {
		t.Fatalf("BridgeTcpPort = %d, want 18081", BridgeTcpPort)
	}
}

func TestApplySnapshotNormalizesOutOfRangeListenerPortsBeforeSharedMuxBuild(t *testing.T) {
	cleanupSharedMuxes(t)

	snapshot := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Host: "web.example.com",
		},
		Network: servercfg.NetworkConfig{
			BridgeHost:     "bridge.example.com",
			BridgePath:     "/bridge",
			BridgeTCPPort:  70000,
			BridgeTLSPort:  70000,
			BridgeWSPort:   -1,
			BridgeWSSPort:  70000,
			HTTPProxyPort:  70000,
			HTTPSProxyPort: 70000,
			WebPort:        70000,
			P2PPort:        70000,
		},
	}

	ApplySnapshot(snapshot)

	if BridgeTcpPort != 0 || BridgeTlsPort != 0 || BridgeWsPort != 0 || BridgeWssPort != 0 {
		t.Fatalf("bridge ports = tcp:%d tls:%d ws:%d wss:%d, want all zero after normalization", BridgeTcpPort, BridgeTlsPort, BridgeWsPort, BridgeWssPort)
	}
	if HttpPort != 0 || HttpsPort != 0 || WebPort != 0 || P2pPort != 0 {
		t.Fatalf("http/web/p2p ports = http:%d https:%d web:%d p2p:%d, want all zero after normalization", HttpPort, HttpsPort, WebPort, P2pPort)
	}
	if sharedMuxErr != nil {
		t.Fatalf("sharedMuxErr = %v, want nil after invalid ports are normalized out", sharedMuxErr)
	}
	if sharedMuxManager != nil {
		t.Fatal("sharedMuxManager should stay nil when all shared-listener ports normalize to zero")
	}
}

func TestGetBridgeListenersInvalidPort(t *testing.T) {
	tests := []struct {
		name string
		set  func()
		call func() (net.Listener, error)
	}{
		{"tcp", func() { BridgeTcpPort = 0 }, GetBridgeTcpListener},
		{"tls", func() { BridgeTlsPort = 70000 }, GetBridgeTlsListener},
		{"ws", func() { BridgeWsPort = -1 }, GetBridgeWsListener},
		{"wss", func() { BridgeWssPort = 0 }, GetBridgeWssListener},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSharedMuxManager()
			tt.set()
			l, err := tt.call()
			if err == nil || l != nil {
				t.Fatalf("expected invalid port error, got listener=%v err=%v", l, err)
			}
		})
	}
}

func TestGetProxyAndWebListenersInvalidPort(t *testing.T) {
	tests := []struct {
		name string
		set  func()
		call func() (net.Listener, error)
	}{
		{"http", func() {
			HttpIp = "127.0.0.1"
			HttpPort = 0
		}, GetHttpListener},
		{"https", func() {
			HttpIp = "127.0.0.1"
			HttpsPort = 70000
		}, GetHttpsListener},
		{"web", func() {
			WebIp = "127.0.0.1"
			WebPort = -1
			WebOpenSSL = false
		}, GetWebManagerListener},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSharedMuxManager()
			tt.set()
			l, err := tt.call()
			if err == nil || l != nil {
				t.Fatalf("expected invalid port error, got listener=%v err=%v", l, err)
			}
		})
	}
}

func TestGetBridgeTcpListenerValid(t *testing.T) {
	resetSharedMuxManager()
	BridgeTcpIp = "127.0.0.1"
	BridgeTcpPort = getAvailablePort(t)

	l, err := GetBridgeTcpListener()
	if err != nil {
		t.Fatalf("GetBridgeTcpListener() error = %v", err)
	}
	_ = l.Close()
}

func TestGetBridgeTlsWsWssListenersValid(t *testing.T) {
	tests := []struct {
		name string
		set  func()
		call func() (net.Listener, error)
	}{
		{"tls", func() {
			BridgeTlsIp = "127.0.0.1"
			BridgeTlsPort = getAvailablePort(t)
		}, GetBridgeTlsListener},
		{"ws", func() {
			BridgeWsIp = "127.0.0.1"
			BridgeWsPort = getAvailablePort(t)
			BridgePath = "/ws"
		}, GetBridgeWsListener},
		{"wss", func() {
			BridgeWssIp = "127.0.0.1"
			BridgeWssPort = getAvailablePort(t)
			BridgePath = "/wss"
		}, GetBridgeWssListener},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetSharedMuxManager()
			tt.set()
			l, err := tt.call()
			if err != nil {
				t.Fatalf("%s listener error = %v", tt.name, err)
			}
			_ = l.Close()
		})
	}
}
