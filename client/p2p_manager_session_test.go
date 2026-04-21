package client

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
	"github.com/quic-go/quic-go"
)

func TestP2PManagerSecretTunnelHelpersIgnoreTypedNilTunnel(t *testing.T) {
	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{ctx: mgrCtx, cancel: cancelMgr}

	var typedNilMux *mux.Mux
	mgr.storeSecretConn(typedNilMux)
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("snapshotSecretConn() = %#v, want nil for typed nil mux", got)
	}
	if got := mgr.clearSecretConn(); got != nil {
		t.Fatalf("clearSecretConn() = %#v, want nil for typed nil mux", got)
	}

	var typedNilQUIC *quic.Conn
	mgr.storeSecretConn(typedNilQUIC)
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("snapshotSecretConn() = %#v, want nil for typed nil quic", got)
	}
	if got := mgr.clearSecretConn(); got != nil {
		t.Fatalf("clearSecretConn() = %#v, want nil for typed nil quic", got)
	}
}

func TestOpenRawConnFromSecretTunnelRejectsTypedNilTunnel(t *testing.T) {
	var typedNilMux *mux.Mux
	rawConn, err := openRawConnFromSecretTunnel(context.Background(), typedNilMux)
	if err == nil || err.Error() != "the tunnel is unavailable" {
		if rawConn != nil {
			_ = rawConn.Close()
		}
		t.Fatalf("openRawConnFromSecretTunnel(typed nil mux) error = %v, want tunnel unavailable", err)
	}

	var typedNilQUIC *quic.Conn
	rawConn, err = openRawConnFromSecretTunnel(context.Background(), typedNilQUIC)
	if err == nil || err.Error() != "the tunnel is unavailable" {
		if rawConn != nil {
			_ = rawConn.Close()
		}
		t.Fatalf("openRawConnFromSecretTunnel(typed nil quic) error = %v, want tunnel unavailable", err)
	}
}

func TestOpenVisitorPunchSessionFailsFastOnConnectError(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldRunVisitorSession := p2pRunVisitorSession
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		p2pRunVisitorSession = oldRunVisitorSession
	})

	expectedErr := errors.New("bridge dial failed")
	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return nil, "", expectedErr
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{ctx: mgrCtx, cancel: cancelMgr}

	started := time.Now()
	_, err := mgr.openVisitorPunchSession(context.Background(), "", &config.CommonConfig{}, &config.LocalServer{
		Type:     "p2p",
		Password: "secret",
	}, nil)
	elapsed := time.Since(started)

	if !errors.Is(err, expectedErr) {
		t.Fatalf("openVisitorPunchSession() error = %v, want %v", err, expectedErr)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("openVisitorPunchSession() returned too slowly after %s", elapsed)
	}
}

func TestOpenVisitorPunchSessionUsesAttemptContextForRunVisitorSession(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldRunVisitorSession := p2pRunVisitorSession
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		p2pRunVisitorSession = oldRunVisitorSession
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	go func() {
		_, _ = io.Copy(io.Discard, right)
	}()

	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(left), "visitor-uuid", nil
	}
	p2pRunVisitorSession = func(ctx context.Context, control *conn.Conn, preferredLocalAddr, transportMode, transportData string) (net.PacketConn, string, string, string, string, string, string, time.Duration, error) {
		<-ctx.Done()
		return nil, "", "", "", "", "", "", 0, ctx.Err()
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{ctx: mgrCtx, cancel: cancelMgr}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	done := make(chan error, 1)
	go func() {
		_, err := mgr.openVisitorPunchSession(attemptCtx, "", &config.CommonConfig{}, &config.LocalServer{
			Type:     "p2p",
			Password: "secret",
		}, &p2pRouteBinding{
			associationID: "assoc-1",
			peerUUID:      "provider-uuid",
			routeContext:  buildLocalP2PRouteHint(&config.LocalServer{Type: "p2p", TargetType: common.CONN_TCP}),
		})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("openVisitorPunchSession() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		cancelMgr()
		t.Fatal("openVisitorPunchSession() ignored attempt context cancellation")
	}
}

func TestOpenVisitorPunchSessionUsesAttemptContextForConnectRequestWrite(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pVisitorSendType
	oldSendRequest := p2pVisitorSendConnectRequest
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pVisitorSendType = oldSendType
		p2pVisitorSendConnectRequest = oldSendRequest
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pVisitorSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	p2pVisitorSendConnectRequest = oldSendRequest

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		uuid:   "visitor-uuid",
	}
	mgr.storeSecretConn("cached-secret")

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	done := make(chan error, 1)
	go func() {
		_, err := mgr.openVisitorPunchSession(attemptCtx, "", &config.CommonConfig{}, &config.LocalServer{
			Type:     "p2p",
			Password: "secret",
		}, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("openVisitorPunchSession() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("openVisitorPunchSession() ignored attempt context during connect request write")
	}
}

func TestGetSecretConnContextUsesAttemptContextForInitialDial(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
	})

	p2pClientNewConnContext = func(ctx context.Context, tp string, vkey string, server string, proxyUrl string, localIP string, verifyCertificate bool) (*conn.Conn, string, error) {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		cfg:    &config.CommonConfig{Tp: "tcp"},
	}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	done := make(chan error, 1)
	go func() {
		_, err := mgr.getSecretConnContext(attemptCtx)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("getSecretConnContext() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("getSecretConnContext() ignored attempt context cancellation")
	}
}

func TestOpenBridgeWorkConnUsesAttemptContextForInitialDial(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
	})

	p2pClientNewConnContext = func(ctx context.Context, tp string, vkey string, server string, proxyUrl string, localIP string, verifyCertificate bool) (*conn.Conn, string, error) {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		cfg:    &config.CommonConfig{Tp: "tcp"},
	}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	done := make(chan error, 1)
	go func() {
		_, err := mgr.openBridgeWorkConn(attemptCtx, common.WORK_P2P_RESOLVE)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("openBridgeWorkConn() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("openBridgeWorkConn() ignored attempt context cancellation")
	}
}

func TestGetSecretConnContextClearsCachedTunnelOnSecretHandshakeFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pSecretSendType
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pSecretSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	expectedErr := errors.New("secret handshake failed")
	p2pSecretSendType = func(*conn.Conn, string, string) error {
		return expectedErr
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
	}
	mgr.storeSecretConn("cached-secret")

	_, err := mgr.getSecretConnContext(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("getSecretConnContext() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on handshake failure", got)
	}
}

func TestGetSecretConnContextUsesAttemptContextForCachedSecretMuxOpen(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	cfg := mux.DefaultMuxConfig()
	cfg.OpenTimeout = 2 * time.Second
	tun := mux.NewMuxWithConfig(client, "tcp", 30, true, cfg)
	defer func() { _ = tun.Close() }()
	go func() {
		_, _ = io.Copy(io.Discard, server)
	}()

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
	}
	mgr.storeSecretConn(tun)

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	started := time.Now()
	_, err := mgr.getSecretConnContext(attemptCtx)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("getSecretConnContext() error = %v, want context deadline exceeded", err)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("getSecretConnContext() ignored attempt context cancellation for %s", elapsed)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on mux timeout", got)
	}
}

func TestGetSecretConnContextUsesAttemptContextForInitialSecretMuxOpen(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldVer := Ver
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		Ver = oldVer
	})

	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	go func() {
		_, _ = io.Copy(io.Discard, server)
	}()

	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(client), "secret-uuid", nil
	}
	Ver = 6

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		cfg:    &config.CommonConfig{Tp: "tcp"},
	}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	started := time.Now()
	_, err := mgr.getSecretConnContext(attemptCtx)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("getSecretConnContext() error = %v, want context deadline exceeded", err)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("getSecretConnContext() ignored attempt context cancellation for %s", elapsed)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared after mux timeout", got)
	}
}

func TestOpenBridgeWorkConnClearsCachedTunnelOnWorkFlagHandshakeFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pWorkSendType
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pWorkSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	expectedErr := errors.New("bridge work handshake failed")
	p2pWorkSendType = func(*conn.Conn, string, string) error {
		return expectedErr
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		uuid:   "client-uuid",
	}
	mgr.storeSecretConn("cached-secret")

	_, err := mgr.openBridgeWorkConn(context.Background(), common.WORK_P2P_RESOLVE)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("openBridgeWorkConn() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on work flag handshake failure", got)
	}
}

func TestOpenVisitorPunchSessionClearsCachedTunnelOnP2PHandshakeFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pVisitorSendType
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pVisitorSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	expectedErr := errors.New("p2p visitor handshake failed")
	p2pVisitorSendType = func(*conn.Conn, string, string) error {
		return expectedErr
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		uuid:   "client-uuid",
	}
	mgr.storeSecretConn("cached-secret")

	_, err := mgr.openVisitorPunchSession(context.Background(), "", &config.CommonConfig{}, &config.LocalServer{
		Type:     "p2p",
		Password: "secret",
	}, nil)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("openVisitorPunchSession() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on p2p handshake failure", got)
	}
}

func TestOpenVisitorPunchSessionClearsCachedTunnelOnConnectRequestFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pVisitorSendType
	oldSendRequest := p2pVisitorSendConnectRequest
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pVisitorSendType = oldSendType
		p2pVisitorSendConnectRequest = oldSendRequest
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pVisitorSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	expectedErr := errors.New("p2p connect request failed")
	p2pVisitorSendConnectRequest = func(*conn.Conn, *p2p.P2PConnectRequest) error {
		return expectedErr
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		uuid:   "client-uuid",
	}
	mgr.storeSecretConn("cached-secret")

	_, err := mgr.openVisitorPunchSession(context.Background(), "", &config.CommonConfig{}, &config.LocalServer{
		Type:     "p2p",
		Password: "secret",
	}, nil)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("openVisitorPunchSession() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on connect request failure", got)
	}
}

func TestOpenProviderPunchSessionUsesAttemptContextForInitialDial(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
	})

	p2pClientNewConnContext = func(ctx context.Context, tp string, vkey string, server string, proxyUrl string, localIP string, verifyCertificate bool) (*conn.Conn, string, error) {
		<-ctx.Done()
		return nil, "", ctx.Err()
	}

	client := &TRPClient{
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: common.CONN_TCP,
		vKey:           "demo",
	}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	done := make(chan error, 1)
	go func() {
		_, err := client.openProviderPunchSession(attemptCtx, p2p.P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
		}, "")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("openProviderPunchSession() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("openProviderPunchSession() ignored attempt context cancellation")
	}
}

func TestOpenProviderPunchSessionUsesAttemptContextForSessionJoinWrite(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldSendType := p2pProviderSendType
	oldSendJoin := p2pProviderSendSessionJoin
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		p2pProviderSendType = oldSendType
		p2pProviderSendSessionJoin = oldSendJoin
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(left), "provider-uuid", nil
	}
	p2pProviderSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	p2pProviderSendSessionJoin = oldSendJoin

	client := &TRPClient{
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: common.CONN_TCP,
		vKey:           "demo",
	}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	done := make(chan error, 1)
	go func() {
		_, err := client.openProviderPunchSession(attemptCtx, p2p.P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
		}, "")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("openProviderPunchSession() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("openProviderPunchSession() ignored attempt context during session join write")
	}
}
