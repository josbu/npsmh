package client

import (
	"context"
	"errors"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server/proxy"
)

type stubManagedService struct {
	started    atomic.Bool
	startErr   error
	closeCalls atomic.Int32
	waitCh     chan struct{}
}

func (s *stubManagedService) Start() error {
	s.started.Store(true)
	if s.waitCh != nil {
		<-s.waitCh
	}
	return s.startErr
}

func (s *stubManagedService) Close() error {
	s.closeCalls.Add(1)
	return nil
}

type closeNotifyConn struct {
	closed chan struct{}
	once   sync.Once
}

func newCloseNotifyConn() *closeNotifyConn {
	return &closeNotifyConn{closed: make(chan struct{})}
}

func (c *closeNotifyConn) Read([]byte) (int, error)    { return 0, net.ErrClosed }
func (c *closeNotifyConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *closeNotifyConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}
func (c *closeNotifyConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *closeNotifyConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *closeNotifyConn) SetDeadline(time.Time) error      { return nil }
func (c *closeNotifyConn) SetReadDeadline(time.Time) error  { return nil }
func (c *closeNotifyConn) SetWriteDeadline(time.Time) error { return nil }

func TestStartManagedServiceAppendsAndStarts(t *testing.T) {
	mgr := &P2PManager{}
	srv := &stubManagedService{waitCh: make(chan struct{})}
	defer close(srv.waitCh)

	mgr.startManagedService(srv)
	deadline := time.Now().Add(time.Second)
	for !srv.started.Load() {
		if time.Now().After(deadline) {
			t.Fatal("managed service did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.proxyServers) != 1 {
		t.Fatalf("proxyServers len = %d, want 1", len(mgr.proxyServers))
	}
	if mgr.proxyServers[0] != srv {
		t.Fatalf("proxyServers[0] = %#v, want started service", mgr.proxyServers[0])
	}
}

func TestStartManagedServiceRemovesFailedServiceAndCleansUp(t *testing.T) {
	mgr := &P2PManager{}
	srv := &stubManagedService{startErr: errors.New("listen failed")}

	mgr.startManagedService(srv)

	deadline := time.Now().Add(time.Second)
	for {
		mgr.mu.Lock()
		remaining := len(mgr.proxyServers)
		mgr.mu.Unlock()
		if srv.started.Load() && remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service cleanup did not complete, started=%v remaining=%d", srv.started.Load(), remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srv.closeCalls.Load() != 1 {
		t.Fatalf("Close() call count = %d, want 1", srv.closeCalls.Load())
	}
}

func TestNewP2PManagerParentCancelClosesTransport(t *testing.T) {
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	mgr := NewP2PManager(parentCtx, func() {}, nil)
	conn := newCloseNotifyConn()
	mgr.mu.Lock()
	mgr.udpConn = conn
	mgr.mu.Unlock()

	parentCancel()

	select {
	case <-conn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("parent context cancel should close active p2p transport")
	}
}

func TestNewP2PManagerSkipsWatcherForBackgroundContext(t *testing.T) {
	baseline := runtime.NumGoroutine()
	managers := make([]*P2PManager, 0, 32)
	for i := 0; i < 32; i++ {
		managers = append(managers, NewP2PManager(context.Background(), func() {}, nil))
	}
	for _, mgr := range managers {
		mgr.Close()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		if delta := runtime.NumGoroutine() - baseline; delta <= 4 {
			return
		}
	}

	if delta := runtime.NumGoroutine() - baseline; delta > 4 {
		t.Fatalf("goroutine delta = %d, want <= 4 after closing background-context managers", delta)
	}
}

func TestNewP2PManagerDoesNotLeakWatcherAfterManualCloseWithCancelableParent(t *testing.T) {
	baseline := runtime.NumGoroutine()
	managers := make([]*P2PManager, 0, 32)
	cancels := make([]context.CancelFunc, 0, 32)
	for i := 0; i < 32; i++ {
		parentCtx, cancel := context.WithCancel(context.Background())
		managers = append(managers, NewP2PManager(parentCtx, func() {}, nil))
		cancels = append(cancels, cancel)
	}
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})

	for _, mgr := range managers {
		mgr.Close()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		if delta := runtime.NumGoroutine() - baseline; delta <= 4 {
			return
		}
	}

	if delta := runtime.NumGoroutine() - baseline; delta > 4 {
		t.Fatalf("goroutine delta = %d, want <= 4 after manual close with cancelable parents", delta)
	}
}

func TestClearActiveTransportSnapshotsAndResetsState(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	mgr := &P2PManager{
		udpConn:  left,
		statusOK: true,
	}

	state := mgr.clearActiveTransport()
	if state.udpConn != left {
		t.Fatalf("captured udpConn = %#v, want original conn", state.udpConn)
	}
	if mgr.udpConn != nil || mgr.muxSession != nil || mgr.quicConn != nil || mgr.quicPacket != nil {
		t.Fatalf("transport state should be cleared, got %+v", mgr)
	}
	if mgr.statusOK {
		t.Fatal("statusOK should be reset when clearing active transport")
	}
}

func TestInstallActiveTransportQuicClearsUdpState(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	mgr := &P2PManager{
		udpConn:  left,
		statusOK: false,
	}

	previous := mgr.installActiveTransport(common.CONN_QUIC, nil, nil, nil, 0)
	if previous.udpConn != left {
		t.Fatalf("previous udpConn = %#v, want original conn", previous.udpConn)
	}
	if mgr.udpConn != nil || mgr.muxSession != nil || mgr.quicConn != nil || mgr.quicPacket != nil {
		t.Fatalf("quic install should clear non-quic transport state, got %+v", mgr)
	}
	if !mgr.statusOK {
		t.Fatal("installActiveTransport should mark statusOK")
	}
}

func TestNewLocalTunnelTaskUsesMixProxyModeForP2PS(t *testing.T) {
	mgr := &P2PManager{
		cfg: &config.CommonConfig{
			Client: &file.Client{
				Cnf: &file.Config{},
			},
		},
	}
	local := &config.LocalServer{
		Type:       "p2ps",
		Port:       2000,
		Target:     "127.0.0.1:8080",
		TargetType: common.CONN_TCP,
	}

	task := mgr.newLocalTunnelTask(local)
	if task.Mode != "mixProxy" {
		t.Fatalf("task.Mode = %q, want mixProxy", task.Mode)
	}
	if !task.HttpProxy || !task.Socks5Proxy {
		t.Fatalf("proxy flags = http:%v socks:%v, want both enabled", task.HttpProxy, task.Socks5Proxy)
	}
}

func TestNewLocalTunnelTaskUsesP2PTModeForTransparentProxy(t *testing.T) {
	mgr := &P2PManager{
		cfg: &config.CommonConfig{
			Client: &file.Client{
				Cnf: &file.Config{},
			},
		},
	}
	local := &config.LocalServer{
		Type:       "p2pt",
		Port:       2001,
		Target:     "127.0.0.1:8081",
		TargetType: common.CONN_TCP,
	}

	task := mgr.newLocalTunnelTask(local)
	if task.Mode != "p2pt" {
		t.Fatalf("task.Mode = %q, want p2pt", task.Mode)
	}
}

func TestResolveP2PTransportTimeoutFallsBackToDefault(t *testing.T) {
	if got := resolveP2PTransportTimeout(0); got != 10*time.Second {
		t.Fatalf("resolveP2PTransportTimeout(0) = %s, want %s", got, 10*time.Second)
	}
	if got := resolveP2PTransportTimeout(3 * time.Second); got != 3*time.Second {
		t.Fatalf("resolveP2PTransportTimeout(3s) = %s, want 3s", got)
	}
}

func TestNormalizeP2PTransportModeKeepsNegotiatedMode(t *testing.T) {
	if got := normalizeP2PTransportMode(common.CONN_QUIC, common.CONN_QUIC); got != common.CONN_QUIC {
		t.Fatalf("normalizeP2PTransportMode(quic, quic) = %q, want quic", got)
	}
	if got := normalizeP2PTransportMode(common.CONN_QUIC, common.CONN_KCP); got != common.CONN_KCP {
		t.Fatalf("normalizeP2PTransportMode(quic, kcp) = %q, want kcp", got)
	}
	if got := normalizeP2PTransportMode("", ""); got != common.CONN_KCP {
		t.Fatalf("normalizeP2PTransportMode(empty, empty) = %q, want kcp", got)
	}
}

func TestBuildLocalP2PRouteHintUsesActualLocalShape(t *testing.T) {
	hint := buildLocalP2PRouteHint(&config.LocalServer{
		Type:       "p2ps",
		Target:     "",
		TargetType: common.CONN_ALL,
	})
	if hint.TunnelMode != "p2ps" {
		t.Fatalf("TunnelMode = %q, want p2ps", hint.TunnelMode)
	}
	if hint.AccessPolicy.Mode != "open" {
		t.Fatalf("AccessPolicy.Mode = %q, want open", hint.AccessPolicy.Mode)
	}

	hint = buildLocalP2PRouteHint(&config.LocalServer{
		Type:       "p2p",
		Target:     "10.0.0.1:22",
		TargetType: common.CONN_TCP,
	})
	if hint.TunnelMode != "p2p" || hint.TargetType != common.CONN_TCP {
		t.Fatalf("unexpected hint = %#v", hint)
	}
	if hint.AccessPolicy.Mode != "whitelist" || len(hint.AccessPolicy.Targets) != 1 || hint.AccessPolicy.Targets[0] != "10.0.0.1:22" {
		t.Fatalf("unexpected access policy = %#v", hint.AccessPolicy)
	}
}

func TestMonitorStateConsumeRoundResultTracksSoftRetries(t *testing.T) {
	state := p2pMonitorState{}
	shouldStop := state.consumeRoundResult(p2pMonitorRoundResult{softRetries: 2, established: false}, 300)
	if shouldStop {
		t.Fatal("consumeRoundResult should not stop on small soft retry count")
	}
	if state.notReadyRetry != 2 {
		t.Fatalf("notReadyRetry = %d, want 2", state.notReadyRetry)
	}
}

func TestMonitorStateConsumeRoundResultSchedulesHardNATBackoff(t *testing.T) {
	state := p2pMonitorState{natHardFailCount: 5}
	shouldStop := state.consumeRoundResult(p2pMonitorRoundResult{hardNATHits: 1}, 300)
	if shouldStop {
		t.Fatal("consumeRoundResult should back off, not stop")
	}
	if state.natHardBackoff != 1 {
		t.Fatalf("natHardBackoff = %d, want 1", state.natHardBackoff)
	}
	if state.nextHardNATRetryAt.IsZero() {
		t.Fatal("nextHardNATRetryAt should be set after hard NAT backoff")
	}
	if state.notReadyRetry != 0 {
		t.Fatalf("notReadyRetry = %d, want 0 after hard NAT backoff", state.notReadyRetry)
	}
}

var _ proxy.Service = (*stubManagedService)(nil)

func TestOpenP2PControlConnReusesCachedSecretConnWithExistingUUID(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pClientNewConnContext = oldNewConn
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	dialCalled := false
	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		dialCalled = true
		return conn.NewConn(right), "new-uuid", nil
	}

	mgr := &P2PManager{uuid: "client-uuid"}
	mgr.storeSecretConn("cached-secret")

	controlConn, err := mgr.openP2PControlConn(context.Background(), nil)
	if err != nil {
		t.Fatalf("openP2PControlConn() error = %v", err)
	}
	if dialCalled {
		t.Fatal("openP2PControlConn() should reuse cached secret tunnel before dialing")
	}
	if controlConn.Conn == nil || controlConn.Conn.Conn != left {
		t.Fatalf("controlConn.Conn = %#v, want cached raw conn", controlConn.Conn)
	}
	if controlConn.SecretConn != "cached-secret" {
		t.Fatalf("controlConn.SecretConn = %#v, want cached-secret", controlConn.SecretConn)
	}
	if controlConn.UUID != "client-uuid" {
		t.Fatalf("controlConn.UUID = %q, want client-uuid", controlConn.UUID)
	}
}

func TestOpenP2PControlConnFallsBackToDialWhenCachedSecretConnHasNoUUIDForWorkPath(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pClientNewConnContext = oldNewConn
	})

	secretLeft, secretRight := net.Pipe()
	dialLeft, dialRight := net.Pipe()
	t.Cleanup(func() {
		_ = secretLeft.Close()
		_ = secretRight.Close()
		_ = dialLeft.Close()
		_ = dialRight.Close()
	})

	dialCalled := false
	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return secretLeft, nil
	}
	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		dialCalled = true
		return conn.NewConn(dialLeft), "dial-uuid", nil
	}

	mgr := &P2PManager{}
	mgr.storeSecretConn("cached-secret")

	controlConn, err := mgr.openP2PControlConnWithPolicy(context.Background(), &config.CommonConfig{Tp: "tcp"}, true)
	if err != nil {
		t.Fatalf("openP2PControlConnWithPolicy() error = %v", err)
	}
	if !dialCalled {
		t.Fatal("openP2PControlConn() should dial when cached secret tunnel has no uuid")
	}
	if controlConn.Conn == nil || controlConn.Conn.Conn != dialLeft {
		t.Fatalf("controlConn.Conn = %#v, want dialed conn", controlConn.Conn)
	}
	if controlConn.SecretConn != nil {
		t.Fatalf("controlConn.SecretConn = %#v, want nil after dial fallback", controlConn.SecretConn)
	}
	if controlConn.UUID != "dial-uuid" {
		t.Fatalf("controlConn.UUID = %q, want dial-uuid", controlConn.UUID)
	}
	if got := mgr.currentP2PUUID(); got != "dial-uuid" {
		t.Fatalf("mgr.currentP2PUUID() = %q, want dial-uuid", got)
	}
}

func TestP2pBridgeSendLinkInfoUsesConfiguredTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := &P2PManager{
		ctx:      ctx,
		cancel:   cancel,
		statusCh: make(chan struct{}, 1),
	}
	bridge := &P2pBridge{
		mgr:     mgr,
		p2p:     true,
		secret:  false,
		timeout: 50 * time.Millisecond,
	}

	started := time.Now()
	_, err := bridge.SendLinkInfo(0, &conn.Link{}, nil)
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "timeout waiting P2P tunnel") {
		t.Fatalf("SendLinkInfo() error = %v, want timeout", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("SendLinkInfo() returned too early after %s", elapsed)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("SendLinkInfo() ignored configured timeout, elapsed=%s", elapsed)
	}
}

func TestPreferredP2PLocalAddrNormalizesWildcardAndIPv6(t *testing.T) {
	if got := preferredP2PLocalAddr(""); got != "" {
		t.Fatalf("preferredP2PLocalAddr(\"\") = %q, want empty", got)
	}
	if got := preferredP2PLocalAddr("0.0.0.0"); got != "" {
		t.Fatalf("preferredP2PLocalAddr(0.0.0.0) = %q, want empty", got)
	}
	if got := preferredP2PLocalAddr("[::]"); got != "" {
		t.Fatalf("preferredP2PLocalAddr([::]) = %q, want empty", got)
	}
	if got := preferredP2PLocalAddr("[2001:db8::10]"); got != "2001:db8::10" {
		t.Fatalf("preferredP2PLocalAddr([2001:db8::10]) = %q", got)
	}
}

func TestP2PHardNATRetryDelayBackoffAndCap(t *testing.T) {
	tests := []struct {
		level int
		want  time.Duration
	}{
		{level: 0, want: 15 * time.Second},
		{level: 1, want: 15 * time.Second},
		{level: 2, want: 30 * time.Second},
		{level: 3, want: time.Minute},
		{level: 4, want: 2 * time.Minute},
		{level: 8, want: 2 * time.Minute},
	}
	for _, tt := range tests {
		if got := p2pHardNATRetryDelay(tt.level); got != tt.want {
			t.Fatalf("p2pHardNATRetryDelay(%d) = %s, want %s", tt.level, got, tt.want)
		}
	}
}

func TestP2PManagerCloseDoesNotHoldLockDuringTransportClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := &P2PManager{
		ctx:      ctx,
		cancel:   cancel,
		statusCh: make(chan struct{}, 1),
	}
	closed := make(chan struct{})
	mgr.mu.Lock()
	mgr.udpConn = &reentrantCloseConn{
		closeFn: func() {
			mgr.resetStatus(false)
			close(closed)
		},
	}
	mgr.statusOK = true
	mgr.mu.Unlock()

	done := make(chan struct{})
	go func() {
		mgr.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close() should not deadlock while closing active transport")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("active transport Close() was not reached")
	}
}

func TestCloseP2PTransportClosesQuicPacketConn(t *testing.T) {
	closed := make(chan struct{}, 1)
	closeP2PTransport(p2pTransportState{
		quicPacket: &closeSpyPacketConn{
			closeFn: func() {
				select {
				case closed <- struct{}{}:
				default:
				}
			},
		},
	}, "test")

	select {
	case <-closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("closeP2PTransport() should close the retained QUIC packet conn")
	}
}

type reentrantCloseConn struct {
	closeFn func()
}

func (c *reentrantCloseConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *reentrantCloseConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *reentrantCloseConn) Close() error {
	if c.closeFn != nil {
		c.closeFn()
	}
	return nil
}
func (c *reentrantCloseConn) LocalAddr() net.Addr              { return &net.IPAddr{} }
func (c *reentrantCloseConn) RemoteAddr() net.Addr             { return &net.IPAddr{} }
func (c *reentrantCloseConn) SetDeadline(time.Time) error      { return nil }
func (c *reentrantCloseConn) SetReadDeadline(time.Time) error  { return nil }
func (c *reentrantCloseConn) SetWriteDeadline(time.Time) error { return nil }

type closeSpyPacketConn struct {
	closeFn func()
}

func (c *closeSpyPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, io.EOF
}
func (c *closeSpyPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (c *closeSpyPacketConn) Close() error {
	if c.closeFn != nil {
		c.closeFn()
	}
	return nil
}
func (c *closeSpyPacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *closeSpyPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *closeSpyPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *closeSpyPacketConn) SetWriteDeadline(time.Time) error { return nil }
