package tool

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type stubDialer struct {
	dialFn func(remote string) (net.Conn, error)
}

func (s *stubDialer) DialVirtual(remote string) (net.Conn, error) {
	if s.dialFn == nil {
		return nil, nil
	}
	return s.dialFn(remote)
}

func (s *stubDialer) ServeVirtual(_ net.Conn) {}

func setLookupForTest(t *testing.T, fn func(int) (Dialer, bool)) {
	t.Helper()
	old := lookup.Load()
	lookup.Store(fn)
	t.Cleanup(func() {
		lookup = atomic.Value{}
		if old != nil {
			lookup.Store(old.(func(int) (Dialer, bool)))
		}
	})
}

func alwaysUsablePort(int) bool {
	return true
}

func withPorts(t *testing.T, p []int) {
	t.Helper()
	original, originalSet := allowPortSnapshot()
	setAllowedPorts(p)
	t.Cleanup(func() {
		portMu.Lock()
		ports = original
		portSet = originalSet
		portMu.Unlock()
	})
}

func resetStatusState() {
	ssMu.Lock()
	defer ssMu.Unlock()
	for i := range statBuf {
		statBuf[i] = nil
	}
	statIdx = 0
	statFilled = false
}

func fillStatusEntries(n int) {
	for i := 0; i < n; i++ {
		statBuf[i] = map[string]interface{}{"id": i}
	}
	statIdx = n
	if n >= statusCap {
		statIdx = n % statusCap
		statFilled = true
	}
}

func TestGetTunnelConnWhenLookupNotSet(t *testing.T) {
	lookup = atomic.Value{}

	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "tunnel lookup not set") {
		t.Fatalf("GetTunnelConn() err=%v, want tunnel lookup not set", err)
	}
}

func TestGetTunnelConnWhenLookupHasInvalidType(t *testing.T) {
	old := lookup.Load()
	lookup = atomic.Value{}
	lookup.Store(17)
	t.Cleanup(func() {
		lookup = atomic.Value{}
		if old != nil {
			if fn, ok := old.(func(int) (Dialer, bool)); ok {
				lookup.Store(fn)
			}
		}
	})

	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "invalid tunnel lookup") {
		t.Fatalf("GetTunnelConn() err=%v, want invalid tunnel lookup", err)
	}
}

func TestGetTunnelConnWhenTunnelNotFound(t *testing.T) {
	setLookupForTest(t, func(id int) (Dialer, bool) {
		return nil, false
	})

	_, err := GetTunnelConn(1, "127.0.0.1:80")
	if err == nil || !strings.Contains(err.Error(), "tunnel not found") {
		t.Fatalf("GetTunnelConn() err=%v, want tunnel not found", err)
	}
}

func TestGetTunnelConnDelegatesToDialer(t *testing.T) {
	called := false
	setLookupForTest(t, func(id int) (Dialer, bool) {
		return &stubDialer{dialFn: func(remote string) (net.Conn, error) {
			called = true
			if remote != "127.0.0.1:8080" {
				t.Fatalf("DialVirtual remote=%q, want 127.0.0.1:8080", remote)
			}
			a, b := net.Pipe()
			_ = b.Close()
			return a, nil
		}}, true
	})

	c, err := GetTunnelConn(7, "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("GetTunnelConn() err=%v, want nil", err)
	}
	if !called {
		t.Fatal("GetTunnelConn() did not call dialer")
	}
	_ = c.Close()
}

func TestGetTunnelConnPropagatesDialError(t *testing.T) {
	wantErr := errors.New("dial failed")
	setLookupForTest(t, func(id int) (Dialer, bool) {
		return &stubDialer{dialFn: func(remote string) (net.Conn, error) {
			return nil, wantErr
		}}, true
	})

	_, err := GetTunnelConn(7, "127.0.0.1:8080")
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetTunnelConn() err=%v, want %v", err, wantErr)
	}
}

func TestGetWebServerConnWhenListenerNotSet(t *testing.T) {
	orig := WebServerListener
	WebServerListener = nil
	t.Cleanup(func() { WebServerListener = orig })

	_, err := GetWebServerConn("127.0.0.1:8080")
	if err == nil || !strings.Contains(err.Error(), "web server not set") {
		t.Fatalf("GetWebServerConn() err=%v, want web server not set", err)
	}
}

func TestGetWebServerConnDialSuccess(t *testing.T) {
	orig := WebServerListener
	l := conn.NewVirtualListener(conn.LocalTCPAddr)
	WebServerListener = l
	t.Cleanup(func() {
		_ = l.Close()
		WebServerListener = orig
	})

	c, err := GetWebServerConn("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("GetWebServerConn() err=%v, want nil", err)
	}
	defer func() { _ = c.Close() }()
	accepted, err := l.Accept()
	if err != nil {
		t.Fatalf("VirtualListener.Accept() err=%v, want nil", err)
	}
	defer func() { _ = accepted.Close() }()
	payload := []byte("ok")
	go func() {
		_, _ = accepted.Write(payload)
	}()

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("ReadFull() err=%v, want nil", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("received %q, want %q", string(buf), string(payload))
	}
}

func TestStatusCountAndSnapshotWithoutWrap(t *testing.T) {
	resetStatusState()
	fillStatusEntries(3)

	if got := statusCount(); got != 3 {
		t.Fatalf("statusCount()=%d, want 3", got)
	}

	snapshot := StatusSnapshot()
	if len(snapshot) != 3 {
		t.Fatalf("len(StatusSnapshot())=%d, want 3", len(snapshot))
	}

	for i := 0; i < 3; i++ {
		if snapshot[i]["id"].(int) != i {
			t.Fatalf("snapshot[%d].id=%v, want %d", i, snapshot[i]["id"], i)
		}
	}
}

func TestStatusSnapshotWithWrap(t *testing.T) {
	resetStatusState()
	ssMu.Lock()
	for i := 0; i < statusCap; i++ {
		statBuf[i] = map[string]interface{}{"id": i}
	}
	statIdx = 100
	statFilled = true
	ssMu.Unlock()

	snapshot := StatusSnapshot()
	if len(snapshot) != statusCap {
		t.Fatalf("len(StatusSnapshot())=%d, want %d", len(snapshot), statusCap)
	}
	if snapshot[0]["id"].(int) != 100 {
		t.Fatalf("snapshot[0].id=%v, want 100", snapshot[0]["id"])
	}
	if snapshot[len(snapshot)-1]["id"].(int) != 99 {
		t.Fatalf("snapshot[last].id=%v, want 99", snapshot[len(snapshot)-1]["id"])
	}
}

func TestStatusSnapshotReturnsDetachedMaps(t *testing.T) {
	resetStatusState()
	ssMu.Lock()
	statBuf[0] = map[string]interface{}{
		"id": 1,
		"nested": map[string]interface{}{
			"value": 2,
		},
	}
	statIdx = 1
	ssMu.Unlock()

	snapshot := StatusSnapshot()
	snapshot[0]["id"] = 99
	snapshot[0]["nested"].(map[string]interface{})["value"] = 77

	if statBuf[0]["id"].(int) != 1 {
		t.Fatalf("statBuf[0][id]=%v, want 1", statBuf[0]["id"])
	}
	if statBuf[0]["nested"].(map[string]interface{})["value"].(int) != 2 {
		t.Fatalf("statBuf[0][nested][value]=%v, want 2", statBuf[0]["nested"].(map[string]interface{})["value"])
	}
}

func TestChartDecilesEdgeCasesAndSampling(t *testing.T) {
	resetStatusState()
	if got := ChartDeciles(); got != nil {
		t.Fatalf("ChartDeciles()=%v, want nil for empty state", got)
	}

	resetStatusState()
	fillStatusEntries(5)
	small := ChartDeciles()
	if len(small) != 5 {
		t.Fatalf("len(ChartDeciles())=%d, want 5", len(small))
	}
	for i := 0; i < 5; i++ {
		if small[i]["id"].(int) != i {
			t.Fatalf("small[%d].id=%v, want %d", i, small[i]["id"], i)
		}
	}

	resetStatusState()
	ssMu.Lock()
	for i := 0; i < statusCap; i++ {
		statBuf[i] = map[string]interface{}{"id": i}
	}
	statIdx = 100
	statFilled = true
	ssMu.Unlock()

	deciles := ChartDeciles()
	if len(deciles) != 10 {
		t.Fatalf("len(ChartDeciles())=%d, want 10", len(deciles))
	}

	for i := 0; i < 10; i++ {
		pos := (i * (statusCap - 1)) / 9
		expected := (100 + pos) % statusCap
		if deciles[i]["id"].(int) != expected {
			t.Fatalf("deciles[%d].id=%v, want %d", i, deciles[i]["id"], expected)
		}
	}
}

func TestChartDecilesReturnsDetachedMaps(t *testing.T) {
	resetStatusState()
	ssMu.Lock()
	for i := 0; i < 11; i++ {
		statBuf[i] = map[string]interface{}{
			"id": i,
			"nested": map[string]interface{}{
				"value": i,
			},
		}
	}
	statIdx = 11
	ssMu.Unlock()

	deciles := ChartDeciles()
	deciles[0]["id"] = 99
	deciles[0]["nested"].(map[string]interface{})["value"] = 77

	if statBuf[0]["id"].(int) != 0 {
		t.Fatalf("statBuf[0][id]=%v, want 0", statBuf[0]["id"])
	}
	if statBuf[0]["nested"].(map[string]interface{})["value"].(int) != 0 {
		t.Fatalf("statBuf[0][nested][value]=%v, want 0", statBuf[0]["nested"].(map[string]interface{})["value"])
	}
}

func TestTestServerPortShortCircuitAndValidation(t *testing.T) {
	withPorts(t, []int{12345})

	if !TestServerPort(-1, "p2p") {
		t.Fatal("TestServerPort() should short-circuit for p2p mode")
	}
	if !TestServerPort(70000, "secret") {
		t.Fatal("TestServerPort() should short-circuit for secret mode")
	}
	if TestServerPort(70000, "tcp") {
		t.Fatal("TestServerPort() should reject ports > 65535")
	}
	if TestServerPort(-1, "udp") {
		t.Fatal("TestServerPort() should reject ports < 0")
	}
	if TestServerPort(54321, "tcp") {
		t.Fatal("TestServerPort() should reject port not in allow list")
	}
}

func TestGenerateServerPortWithAllowList(t *testing.T) {
	withPorts(t, []int{0, 10001, 10002})
	//rand.Seed(1)

	got := GenerateServerPort("p2p")
	if got != 10001 && got != 10002 {
		t.Fatalf("GenerateServerPort()=%d, want one of configured non-zero ports", got)
	}
}

func TestGenerateServerPortWithOnlyZeroAllowList(t *testing.T) {
	withPorts(t, []int{0, 0})
	if got := GenerateServerPort("p2p"); got != 0 {
		t.Fatalf("GenerateServerPort()=%d, want 0 when allow list has no usable ports", got)
	}
}

func TestGenerateServerPortWithoutAllowListUsesDynamicRange(t *testing.T) {
	withPorts(t, nil)
	//rand.Seed(1)

	got := GenerateServerPort("p2p")
	if got < 1024 || got > 65535 {
		t.Fatalf("GenerateServerPort()=%d, want in [1024, 65535]", got)
	}
}

func TestTestTunnelPortChecksUDPForSocks5(t *testing.T) {
	withPorts(t, nil)

	port := 22345
	socksTunnel := &file.Tunnel{Port: port, Mode: "mixProxy", Socks5Proxy: true}
	if testPortUsageWithFns(socksTunnel.Port, socksTunnel.Mode, tunnelNeedsUDP(socksTunnel), alwaysUsablePort, func(candidate int) bool {
		return candidate != port
	}) {
		t.Fatalf("TestTunnelPort() should reject socks5 tunnel when UDP port %d is occupied", port)
	}

	httpTunnel := &file.Tunnel{Port: port, Mode: "mixProxy", Socks5Proxy: false}
	udpChecked := false
	if !testPortUsageWithFns(httpTunnel.Port, httpTunnel.Mode, tunnelNeedsUDP(httpTunnel), alwaysUsablePort, func(int) bool {
		udpChecked = true
		return false
	}) {
		t.Fatalf("TestTunnelPort() should allow HTTP-only mixProxy when only UDP port %d is occupied", port)
	}
	if udpChecked {
		t.Fatalf("TestTunnelPort() should not probe UDP when mixProxy socks5 support is disabled")
	}
}

func TestGenerateTunnelPortUsesTunnelPolicy(t *testing.T) {
	withPorts(t, []int{0, 10001, 10002})

	got := generateTunnelPortWithFns(&file.Tunnel{Mode: "mixProxy", Socks5Proxy: true}, alwaysUsablePort, alwaysUsablePort)
	if got != 10001 && got != 10002 {
		t.Fatalf("GenerateTunnelPort()=%d, want one of configured non-zero ports", got)
	}
}
