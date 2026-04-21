package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/quic-go/quic-go"
)

func TestQuicLinkForP2PSendAddsAckWithoutMutatingOriginal(t *testing.T) {
	original := &conn.Link{
		ConnType: "tcp",
		Host:     "127.0.0.1:8080",
		Option: conn.Options{
			Timeout: 50 * time.Millisecond,
			NeedAck: false,
		},
	}

	sendLink := quicLinkForP2PSend(original, true)
	if sendLink == nil {
		t.Fatal("quicLinkForP2PSend() = nil")
	}
	if sendLink == original {
		t.Fatal("quicLinkForP2PSend() returned original pointer")
	}
	if !sendLink.Option.NeedAck {
		t.Fatal("sendLink.Option.NeedAck = false, want true")
	}
	if original.Option.NeedAck {
		t.Fatal("original.Option.NeedAck mutated to true")
	}
}

func TestKCPLinkForP2PSendClearsAckWithoutMutatingOriginal(t *testing.T) {
	original := &conn.Link{
		ConnType: "tcp",
		Host:     "127.0.0.1:8080",
		Option: conn.Options{
			Timeout: 50 * time.Millisecond,
			NeedAck: true,
		},
	}

	sendLink := kcpLinkForP2PSend(original)
	if sendLink == nil {
		t.Fatal("kcpLinkForP2PSend() = nil")
	}
	if sendLink == original {
		t.Fatal("kcpLinkForP2PSend() returned original pointer")
	}
	if sendLink.Option.NeedAck {
		t.Fatal("sendLink.Option.NeedAck = true, want false")
	}
	if !original.Option.NeedAck {
		t.Fatal("original.Option.NeedAck mutated to false")
	}
}

func TestCloneLinkForP2PSendCopiesLinkOptions(t *testing.T) {
	original := &conn.Link{
		ConnType: "udp",
		Host:     "127.0.0.1:9000",
		Option: conn.Options{
			Timeout:           3 * time.Second,
			NeedAck:           true,
			WaitConnectResult: true,
		},
	}

	cloned := cloneLinkForP2PSend(original)
	if cloned == nil {
		t.Fatal("cloneLinkForP2PSend() = nil")
	}
	if cloned.Option != original.Option {
		t.Fatalf("cloned.Option = %+v, want %+v", cloned.Option, original.Option)
	}

	cloned.Option.Timeout = time.Second
	cloned.Option.WaitConnectResult = false
	if original.Option.Timeout != 3*time.Second || !original.Option.WaitConnectResult {
		t.Fatalf("original.Option mutated = %+v", original.Option)
	}
}

func TestP2pBridgeSendLinkInfoRejectsMissingManager(t *testing.T) {
	link := &conn.Link{}

	var nilBridge *P2pBridge
	if _, err := nilBridge.SendLinkInfo(0, link, nil); !errors.Is(err, errP2PBridgeUnavailable) {
		t.Fatalf("nil bridge SendLinkInfo() error = %v, want %v", err, errP2PBridgeUnavailable)
	}

	bridge := &P2pBridge{}
	if _, err := bridge.SendLinkInfo(0, link, nil); !errors.Is(err, errP2PManagerUnavailable) {
		t.Fatalf("bridge without manager SendLinkInfo() error = %v, want %v", err, errP2PManagerUnavailable)
	}
}

func TestP2pBridgeSendLinkInfoHandlesNilManagerContext(t *testing.T) {
	bridge := &P2pBridge{
		mgr:     &P2PManager{},
		timeout: 20 * time.Millisecond,
	}

	start := time.Now()
	_, err := bridge.SendLinkInfo(0, &conn.Link{}, nil)
	if err == nil || !strings.Contains(err.Error(), "timeout waiting P2P tunnel") {
		t.Fatalf("SendLinkInfo() error = %v, want timeout waiting P2P tunnel", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("SendLinkInfo() took %v with nil manager context, want <500ms", elapsed)
	}
}

func TestP2pBridgeSendLinkInfoSkipsMalformedActiveQUICTransport(t *testing.T) {
	oldOpenStream := p2pOpenQUICStreamSync
	t.Cleanup(func() {
		p2pOpenQUICStreamSync = oldOpenStream
	})

	called := false
	p2pOpenQUICStreamSync = func(context.Context, *quic.Conn) (*quic.Stream, error) {
		called = true
		return nil, errors.New("should not use malformed quic transport")
	}

	bridge := &P2pBridge{
		mgr: &P2PManager{
			ctx:      context.Background(),
			quicConn: &quic.Conn{},
			statusCh: make(chan struct{}, 1),
		},
		p2p:     true,
		timeout: 20 * time.Millisecond,
	}

	_, err := bridge.SendLinkInfo(0, &conn.Link{}, nil)
	if err == nil || !strings.Contains(err.Error(), "timeout waiting P2P tunnel") {
		t.Fatalf("SendLinkInfo() error = %v, want timeout waiting P2P tunnel", err)
	}
	if called {
		t.Fatal("SendLinkInfo() should skip malformed zero-value quic transport")
	}
}

func TestSendViaQUICUsesRequestContext(t *testing.T) {
	oldOpenStream := p2pOpenQUICStreamSync
	t.Cleanup(func() {
		p2pOpenQUICStreamSync = oldOpenStream
	})

	p2pOpenQUICStreamSync = func(ctx context.Context, _ *quic.Conn) (*quic.Stream, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	bridge := &P2pBridge{
		mgr: &P2PManager{
			ctx:      mgrCtx,
			cancel:   cancelMgr,
			statusCh: make(chan struct{}, 1),
		},
		timeout: 50 * time.Millisecond,
	}
	qConn := &quic.Conn{}

	attemptCtx, cancelAttempt := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelAttempt()

	_, err := bridge.sendViaQUIC(attemptCtx, &conn.Link{}, qConn, 0, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sendViaQUIC() error = %v, want context deadline exceeded", err)
	}
}

func TestSendViaQUICRejectsNilTransport(t *testing.T) {
	bridge := &P2pBridge{
		mgr:     &P2PManager{},
		timeout: 50 * time.Millisecond,
	}

	if _, err := bridge.sendViaQUIC(context.Background(), &conn.Link{}, nil, 0, nil); !errors.Is(err, errP2PQUICTransportUnavailable) {
		t.Fatalf("sendViaQUIC(nil transport) error = %v, want %v", err, errP2PQUICTransportUnavailable)
	}
}

func TestSendViaKCPRejectsNilTransport(t *testing.T) {
	bridge := &P2pBridge{mgr: &P2PManager{}}

	if _, err := bridge.sendViaKCP(&conn.Link{}, nil, nil); !errors.Is(err, errP2PKCPTransportUnavailable) {
		t.Fatalf("sendViaKCP(nil transport) error = %v, want %v", err, errP2PKCPTransportUnavailable)
	}
}

func TestSendViaSecretClearsCachedTunnelOnLinkSendFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pSecretSendType
	oldSendLink := p2pSecretSendLinkInfo
	oldReadACK := p2pSecretReadACK
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pSecretSendType = oldSendType
		p2pSecretSendLinkInfo = oldSendLink
		p2pSecretReadACK = oldReadACK
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	go func() {
		buf := make([]byte, 32)
		_, _ = io.ReadFull(right, buf)
	}()

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pSecretSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	expectedErr := errors.New("secret link send failed")
	p2pSecretSendLinkInfo = func(net.Conn, *conn.Link) error {
		return expectedErr
	}
	p2pSecretReadACK = func(net.Conn, time.Duration) error {
		return nil
	}

	mgrCtx, cancelMgr := context.WithCancel(context.Background())
	defer cancelMgr()
	mgr := &P2PManager{
		ctx:    mgrCtx,
		cancel: cancelMgr,
		uuid:   "client-uuid",
	}
	mgr.storeSecretConn("cached-secret")
	bridge := &P2pBridge{
		mgr:   mgr,
		local: &config.LocalServer{Password: "secret"},
	}

	_, err := bridge.sendViaSecret(context.Background(), &conn.Link{})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("sendViaSecret() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on secret link send failure", got)
	}
}

func TestSendViaSecretClearsCachedTunnelOnAckFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pSecretSendType
	oldSendLink := p2pSecretSendLinkInfo
	oldReadACK := p2pSecretReadACK
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pSecretSendType = oldSendType
		p2pSecretSendLinkInfo = oldSendLink
		p2pSecretReadACK = oldReadACK
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})
	go func() {
		buf := make([]byte, 32)
		_, _ = io.ReadFull(right, buf)
	}()

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pSecretSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	p2pSecretSendLinkInfo = func(net.Conn, *conn.Link) error {
		return nil
	}
	expectedErr := errors.New("secret ack failed")
	p2pSecretReadACK = func(net.Conn, time.Duration) error {
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
	bridge := &P2pBridge{
		mgr:   mgr,
		local: &config.LocalServer{Password: "secret"},
	}

	_, err := bridge.sendViaSecret(context.Background(), &conn.Link{
		Option: conn.Options{NeedAck: true},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("sendViaSecret() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on secret ack failure", got)
	}
}

func TestSendViaSecretRejectsMissingManager(t *testing.T) {
	var nilBridge *P2pBridge
	if _, err := nilBridge.sendViaSecret(context.Background(), &conn.Link{}); !errors.Is(err, errP2PBridgeUnavailable) {
		t.Fatalf("nil bridge sendViaSecret() error = %v, want %v", err, errP2PBridgeUnavailable)
	}

	bridge := &P2pBridge{}
	if _, err := bridge.sendViaSecret(context.Background(), &conn.Link{}); !errors.Is(err, errP2PManagerUnavailable) {
		t.Fatalf("bridge without manager sendViaSecret() error = %v, want %v", err, errP2PManagerUnavailable)
	}
}
