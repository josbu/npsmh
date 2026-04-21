package client

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/p2p"
)

func TestStoreResolvedRouteBindingLockedClosesOrphanedPeerOnRebind(t *testing.T) {
	mgr := newTestP2PManagerRouteState()
	local := &config.LocalServer{Type: "p2p", Port: 18080, Password: "secret"}
	localKey := buildLocalBindingKey(local)
	oldPeerKey := buildPeerKey("provider-old", "quic")

	oldBinding := &p2pRouteBinding{
		localKey:      localKey,
		passwordMD5:   "old-password",
		peerUUID:      "provider-old",
		associationID: "assoc-old",
		peerKey:       oldPeerKey,
	}
	mgr.localBindings[localKey] = oldBinding
	mgr.associations["assoc-old"] = &p2pAssociationState{
		association: p2p.P2PAssociation{
			AssociationID: "assoc-old",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-old"},
		},
		phase:     "established",
		peerKey:   oldPeerKey,
		routeRefs: map[string]*p2pRouteBinding{localKey: oldBinding},
	}
	mgr.peerIndex["provider-old"] = "assoc-old"
	closed := make(chan struct{}, 1)
	mgr.peers[oldPeerKey] = &p2pPeerState{
		key:           oldPeerKey,
		associationID: "assoc-old",
		transport: p2pTransportState{
			udpConn: &reentrantCloseConn{
				closeFn: func() {
					select {
					case closed <- struct{}{}:
					default:
					}
				},
			},
		},
		statusOK: true,
	}

	resp := p2p.P2PResolveResult{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-new",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-new"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 201},
		Phase: "binding",
	}

	mgr.mu.Lock()
	binding, assoc, staleTransport, err := mgr.storeResolvedRouteBindingLocked(local, "new-password", resp)
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("storeResolvedRouteBindingLocked() error = %v", err)
	}
	closeP2PTransport(staleTransport, "test rebind cleanup")

	if binding.associationID != "assoc-new" {
		t.Fatalf("binding associationID = %q, want assoc-new", binding.associationID)
	}
	if assoc == nil || assoc.association.AssociationID != "assoc-new" {
		t.Fatalf("association = %#v, want assoc-new", assoc)
	}
	if _, ok := mgr.associations["assoc-old"]; ok {
		t.Fatal("old association should be removed after local rebind")
	}
	if _, ok := mgr.peers[oldPeerKey]; ok {
		t.Fatal("old peer should be removed after local rebind")
	}
	if got := mgr.peerIndex["provider-new"]; got != "assoc-new" {
		t.Fatalf("peerIndex[provider-new] = %q, want assoc-new", got)
	}
	if _, ok := mgr.peerIndex["provider-old"]; ok {
		t.Fatal("old provider peer index should be removed after local rebind")
	}
	select {
	case <-closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("orphaned peer transport should be closed after local rebind")
	}
}

func TestStoreResolvedRouteBindingLockedKeepsPeerOnSameAssociationRefresh(t *testing.T) {
	mgr := newTestP2PManagerRouteState()
	local := &config.LocalServer{Type: "p2p", Port: 18081, Password: "secret"}
	localKey := buildLocalBindingKey(local)
	peerKey := buildPeerKey("provider-same", "quic")

	oldBinding := &p2pRouteBinding{
		localKey:      localKey,
		passwordMD5:   "old-password",
		peerUUID:      "provider-same",
		associationID: "assoc-same",
		peerKey:       peerKey,
	}
	mgr.localBindings[localKey] = oldBinding
	mgr.associations["assoc-same"] = &p2pAssociationState{
		association: p2p.P2PAssociation{
			AssociationID: "assoc-same",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-same"},
		},
		phase:     "established",
		peerKey:   peerKey,
		routeRefs: map[string]*p2pRouteBinding{localKey: oldBinding},
	}
	mgr.peerIndex["provider-same"] = "assoc-same"
	closed := make(chan struct{}, 1)
	mgr.peers[peerKey] = &p2pPeerState{
		key:           peerKey,
		associationID: "assoc-same",
		transport: p2pTransportState{
			udpConn: &reentrantCloseConn{
				closeFn: func() {
					select {
					case closed <- struct{}{}:
					default:
					}
				},
			},
		},
		statusOK: true,
	}

	resp := p2p.P2PResolveResult{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-same",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-same"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 202},
		Phase: "binding",
	}

	mgr.mu.Lock()
	binding, assoc, staleTransport, err := mgr.storeResolvedRouteBindingLocked(local, "new-password", resp)
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("storeResolvedRouteBindingLocked() error = %v", err)
	}
	closeP2PTransport(staleTransport, "test same association refresh")

	if binding.associationID != "assoc-same" {
		t.Fatalf("binding associationID = %q, want assoc-same", binding.associationID)
	}
	if assoc == nil || assoc.association.AssociationID != "assoc-same" {
		t.Fatalf("association = %#v, want assoc-same", assoc)
	}
	if _, ok := mgr.peers[peerKey]; !ok {
		t.Fatal("peer should stay registered when local refreshes the same association")
	}
	select {
	case <-closed:
		t.Fatal("same-association refresh should not close the live peer transport")
	default:
	}
}

func TestStoreResolvedRouteBindingLockedKeepsSharedAssociationPeer(t *testing.T) {
	mgr := newTestP2PManagerRouteState()
	localA := &config.LocalServer{Type: "p2p", Port: 18082, Password: "secret-a"}
	localB := &config.LocalServer{Type: "p2p", Port: 18083, Password: "secret-b"}
	localAKey := buildLocalBindingKey(localA)
	localBKey := buildLocalBindingKey(localB)
	peerKey := buildPeerKey("provider-shared", "quic")

	bindingA := &p2pRouteBinding{
		localKey:      localAKey,
		passwordMD5:   "password-a",
		peerUUID:      "provider-shared",
		associationID: "assoc-shared",
		peerKey:       peerKey,
	}
	bindingB := &p2pRouteBinding{
		localKey:      localBKey,
		passwordMD5:   "password-b",
		peerUUID:      "provider-shared",
		associationID: "assoc-shared",
		peerKey:       peerKey,
	}
	mgr.localBindings[localAKey] = bindingA
	mgr.localBindings[localBKey] = bindingB
	mgr.associations["assoc-shared"] = &p2pAssociationState{
		association: p2p.P2PAssociation{
			AssociationID: "assoc-shared",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-shared"},
		},
		phase:   "established",
		peerKey: peerKey,
		routeRefs: map[string]*p2pRouteBinding{
			localAKey: bindingA,
			localBKey: bindingB,
		},
	}
	mgr.peerIndex["provider-shared"] = "assoc-shared"
	closed := make(chan struct{}, 1)
	mgr.peers[peerKey] = &p2pPeerState{
		key:           peerKey,
		associationID: "assoc-shared",
		transport: p2pTransportState{
			udpConn: &reentrantCloseConn{
				closeFn: func() {
					select {
					case closed <- struct{}{}:
					default:
					}
				},
			},
		},
		statusOK: true,
	}

	resp := p2p.P2PResolveResult{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-new-shared-test",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-new-shared-test"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 203},
		Phase: "binding",
	}

	mgr.mu.Lock()
	_, _, staleTransport, err := mgr.storeResolvedRouteBindingLocked(localA, "new-password", resp)
	mgr.mu.Unlock()
	if err != nil {
		t.Fatalf("storeResolvedRouteBindingLocked() error = %v", err)
	}
	closeP2PTransport(staleTransport, "test shared association")

	if _, ok := mgr.associations["assoc-shared"]; !ok {
		t.Fatal("shared association should stay while another local still references it")
	}
	if _, ok := mgr.peers[peerKey]; !ok {
		t.Fatal("shared peer should stay while another local still references it")
	}
	select {
	case <-closed:
		t.Fatal("shared association rebind should not close a peer still used by another local")
	default:
	}
}

func newTestP2PManagerRouteState() *P2PManager {
	return &P2PManager{
		associations:  make(map[string]*p2pAssociationState),
		peerIndex:     make(map[string]string),
		peers:         make(map[string]*p2pPeerState),
		localBindings: make(map[string]*p2pRouteBinding),
		statusCh:      make(chan struct{}, 1),
	}
}

func TestResolveRouteBindingClearsCachedTunnelOnResolveRequestFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pWorkSendType
	oldSendRequest := p2pSendResolveRequest
	oldReadResponse := p2pReadResolveResponse
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pWorkSendType = oldSendType
		p2pSendResolveRequest = oldSendRequest
		p2pReadResolveResponse = oldReadResponse
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pWorkSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	expectedErr := errors.New("resolve request failed")
	p2pSendResolveRequest = func(*conn.Conn, *p2p.P2PResolveRequest) error {
		return expectedErr
	}
	p2pReadResolveResponse = oldReadResponse

	mgr := newTestP2PManagerRouteState()
	mgr.uuid = "client-uuid"
	mgr.storeSecretConn("cached-secret")

	_, _, err := mgr.resolveRouteBinding(context.Background(), &config.LocalServer{
		Type:     "p2p",
		Port:     18090,
		Password: "secret",
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("resolveRouteBinding() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on resolve request failure", got)
	}
}

func TestResolveRouteBindingClearsCachedTunnelOnResolveResponseFailure(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pWorkSendType
	oldSendRequest := p2pSendResolveRequest
	oldReadResponse := p2pReadResolveResponse
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pWorkSendType = oldSendType
		p2pSendResolveRequest = oldSendRequest
		p2pReadResolveResponse = oldReadResponse
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pWorkSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	p2pSendResolveRequest = func(*conn.Conn, *p2p.P2PResolveRequest) error {
		return nil
	}
	expectedErr := errors.New("resolve response failed")
	p2pReadResolveResponse = func(*conn.Conn) (p2p.P2PResolveResult, error) {
		return p2p.P2PResolveResult{}, expectedErr
	}

	mgr := newTestP2PManagerRouteState()
	mgr.uuid = "client-uuid"
	mgr.storeSecretConn("cached-secret")

	_, _, err := mgr.resolveRouteBinding(context.Background(), &config.LocalServer{
		Type:     "p2p",
		Port:     18091,
		Password: "secret",
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("resolveRouteBinding() error = %v, want %v", err, expectedErr)
	}
	if got := mgr.snapshotSecretConn(); got != nil {
		t.Fatalf("cached secret tunnel = %#v, want cleared on resolve response failure", got)
	}
}

func TestResolveRouteBindingUsesAttemptContextForResponseRead(t *testing.T) {
	oldOpenRaw := p2pOpenRawConnFromSecretTunnel
	oldSendType := p2pWorkSendType
	oldSendRequest := p2pSendResolveRequest
	oldReadResponse := p2pReadResolveResponse
	t.Cleanup(func() {
		p2pOpenRawConnFromSecretTunnel = oldOpenRaw
		p2pWorkSendType = oldSendType
		p2pSendResolveRequest = oldSendRequest
		p2pReadResolveResponse = oldReadResponse
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pOpenRawConnFromSecretTunnel = func(context.Context, any) (net.Conn, error) {
		return left, nil
	}
	p2pWorkSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	p2pSendResolveRequest = func(*conn.Conn, *p2p.P2PResolveRequest) error {
		return nil
	}
	p2pReadResolveResponse = oldReadResponse

	mgr := newTestP2PManagerRouteState()
	mgr.uuid = "client-uuid"
	mgr.storeSecretConn("cached-secret")

	attemptCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, _, err := mgr.resolveRouteBinding(attemptCtx, &config.LocalServer{
		Type:       "p2p",
		Port:       18092,
		Password:   "secret",
		TargetType: common.CONN_TCP,
	})
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolveRouteBinding() error = %v, want context deadline exceeded", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("resolveRouteBinding() ignored attempt context cancellation for %s", elapsed)
	}
}

func TestWaitOrEstablishPeerHandlesNilContextWithHealthyPeer(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldSendType := p2pWorkSendType
	oldSendRequest := p2pSendResolveRequest
	oldReadResponse := p2pReadResolveResponse
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		p2pWorkSendType = oldSendType
		p2pSendResolveRequest = oldSendRequest
		p2pReadResolveResponse = oldReadResponse
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(left), "client-uuid", nil
	}
	p2pWorkSendType = func(*conn.Conn, string, string) error {
		return nil
	}
	p2pSendResolveRequest = func(*conn.Conn, *p2p.P2PResolveRequest) error {
		return nil
	}
	p2pReadResolveResponse = func(*conn.Conn) (p2p.P2PResolveResult, error) {
		return p2p.P2PResolveResult{
			Association: p2p.P2PAssociation{
				AssociationID: "assoc-ready",
				Provider:      p2p.P2PPeerRuntime{UUID: "provider-ready"},
			},
			Route: p2p.P2PRouteContext{TunnelID: 204},
			Phase: "established",
		}, nil
	}

	mgr := newTestP2PManagerRouteState()
	peerKey := buildPeerKey("provider-ready", P2PMode)
	liveMux, _ := newLiveP2PMuxPair(t)
	mgr.peers[peerKey] = &p2pPeerState{
		key:           peerKey,
		associationID: "assoc-ready",
		transport: p2pTransportState{
			muxSession: liveMux,
		},
		statusOK:   true,
		lastActive: time.Now(),
		mode:       common.CONN_KCP,
	}

	local := &config.LocalServer{
		Type:     "p2p",
		Port:     18093,
		Password: "secret",
	}

	binding, peer, err := mgr.waitOrEstablishPeer(nil, local)
	if err != nil {
		t.Fatalf("waitOrEstablishPeer(nil) error = %v", err)
	}
	if binding == nil || binding.associationID != "assoc-ready" {
		t.Fatalf("binding = %#v, want assoc-ready binding", binding)
	}
	if peer == nil || peer.key != peerKey {
		t.Fatalf("peer = %#v, want healthy peer %q", peer, peerKey)
	}
}
