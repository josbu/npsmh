package client

import (
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/p2p"
)

type typedNilAssociationConnStub struct{}

func (s *typedNilAssociationConnStub) Read([]byte) (int, error) {
	panic("unexpected Read call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) Write([]byte) (int, error) {
	panic("unexpected Write call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) Close() error {
	panic("unexpected Close call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) LocalAddr() net.Addr {
	panic("unexpected LocalAddr call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) RemoteAddr() net.Addr {
	panic("unexpected RemoteAddr call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) SetDeadline(time.Time) error {
	panic("unexpected SetDeadline call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) SetReadDeadline(time.Time) error {
	panic("unexpected SetReadDeadline call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) SetWriteDeadline(time.Time) error {
	panic("unexpected SetWriteDeadline call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) P2PAssociationID() string {
	panic("unexpected P2PAssociationID call on typed nil association conn")
}

func (s *typedNilAssociationConnStub) Unwrap() net.Conn {
	panic("unexpected Unwrap call on typed nil association conn")
}

func TestP2PAssociationConnHelpersIgnoreTypedNilConn(t *testing.T) {
	var typedNil *typedNilAssociationConnStub

	if got := wrapP2PAssociationConn(typedNil, "assoc-1"); got != nil {
		t.Fatalf("wrapP2PAssociationConn() = %#v, want nil", got)
	}
	if got := p2pAssociationIDFromConn(typedNil); got != "" {
		t.Fatalf("p2pAssociationIDFromConn() = %q, want empty", got)
	}
}

func TestClientP2PStateStoreInitializesAndReusesRuntimeState(t *testing.T) {
	client := &TRPClient{}

	assoc := runtimeClientP2PStateRoot.EnsureAssociation(client, "assoc-1")
	if assoc == nil {
		t.Fatal("EnsureAssociation() = nil")
	}
	peer := runtimeClientP2PStateRoot.EnsurePeerPolicy(client, "peer-1")
	if peer == nil {
		t.Fatal("EnsurePeerPolicy() = nil")
	}
	if client.p2pState == nil || client.p2pState.associations == nil || client.p2pState.peerPolicies == nil {
		t.Fatal("state store did not initialize state holder")
	}
	if again := runtimeClientP2PStateRoot.EnsureAssociation(client, "assoc-1"); again != assoc {
		t.Fatal("EnsureAssociation() did not reuse runtime entry")
	}
	if again := runtimeClientP2PStateRoot.EnsurePeerPolicy(client, "peer-1"); again != peer {
		t.Fatal("EnsurePeerPolicy() did not reuse runtime entry")
	}
}

func TestNewClientP2PStateRootBuildsDefaultWiring(t *testing.T) {
	ctx := newClientP2PStateRoot()
	if ctx == nil {
		t.Fatal("newClientP2PStateRoot() = nil")
	}
	client := &TRPClient{uuid: "provider-runtime"}
	if ctx.EnsureAssociation(client, "assoc-root") == nil {
		t.Fatal("state root methods are not fully wired")
	}
	if ctx.EnsurePeerPolicy(client, "peer-root") == nil {
		t.Fatal("state root peer policy methods are not fully wired")
	}
	ctx.RecordBind(client, p2p.P2PAssociationBind{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-bind",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-runtime"},
			Visitor:       p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		},
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode: p2p.P2PAccessModeOpen,
		},
		Route: p2p.P2PRouteContext{TunnelID: 1},
		Phase: "binding",
	})
	if _, ok := ctx.AssociationPolicy(client, "assoc-bind"); !ok {
		t.Fatal("state root association policy methods are not fully wired")
	}
}

func TestClientP2PAssociationStorePolicyReturnsEffectiveGrant(t *testing.T) {
	client := &TRPClient{uuid: "provider-runtime"}

	runtimeClientP2PStateRoot.RecordBind(client, p2p.P2PAssociationBind{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-1",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-runtime"},
			Visitor:       p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		},
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.8:8443"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 101},
		Phase: "binding",
	})

	policy, ok := runtimeClientP2PStateRoot.AssociationPolicy(client, "assoc-1")
	if !ok {
		t.Fatal("Policy() = false, want true")
	}
	if policy.Mode != p2p.P2PAccessModeWhitelist {
		t.Fatalf("Policy() mode = %q, want %q", policy.Mode, p2p.P2PAccessModeWhitelist)
	}
	if len(policy.Targets) != 1 || policy.Targets[0] != "10.0.0.8:8443" {
		t.Fatalf("Policy() targets = %#v, want [10.0.0.8:8443]", policy.Targets)
	}
}

func TestClientP2PAssociationStoreRecordPunchStartSetsPhase(t *testing.T) {
	client := &TRPClient{uuid: "provider-runtime"}

	runtimeClientP2PStateRoot.RecordPunchStart(client, p2p.P2PPunchStart{
		AssociationID: "assoc-2",
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode: p2p.P2PAccessModeOpen,
		},
		Role:  common.WORK_P2P_PROVIDER,
		Self:  p2p.P2PPeerRuntime{UUID: "provider-runtime"},
		Peer:  p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		Route: p2p.P2PRouteContext{TunnelID: 202},
	})

	runtime, ok := runtimeClientP2PStateRoot.Association(client, "assoc-2")
	if !ok || runtime == nil {
		t.Fatal("RecordPunchStart() should create runtime entry")
	}
	if runtime.Phase != "punching" {
		t.Fatalf("RecordPunchStart() phase = %q, want punching", runtime.Phase)
	}
	if runtime.PeerUUID != "visitor-runtime" {
		t.Fatalf("RecordPunchStart() peer uuid = %q, want visitor-runtime", runtime.PeerUUID)
	}
}

func TestClientP2PAssociationStoreRecordBindIgnoresMismatchedProviderRuntime(t *testing.T) {
	client := &TRPClient{uuid: "provider-runtime"}

	runtimeClientP2PStateRoot.RecordBind(client, p2p.P2PAssociationBind{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-ignore-bind",
			Provider:      p2p.P2PPeerRuntime{UUID: "other-provider"},
			Visitor:       p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		},
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode: p2p.P2PAccessModeOpen,
		},
		Route: p2p.P2PRouteContext{TunnelID: 303},
		Phase: "binding",
	})

	if _, ok := runtimeClientP2PStateRoot.Association(client, "assoc-ignore-bind"); ok {
		t.Fatal("RecordBind() should ignore bind for a different provider runtime")
	}
	if _, ok := runtimeClientP2PStateRoot.PeerPolicy(client, "visitor-runtime"); ok {
		t.Fatal("RecordBind() should not create peer policy for a different provider runtime")
	}
}

func TestClientP2PAssociationStoreRecordPunchStartIgnoresMismatchedRuntime(t *testing.T) {
	client := &TRPClient{uuid: "provider-runtime"}

	runtimeClientP2PStateRoot.RecordPunchStart(client, p2p.P2PPunchStart{
		AssociationID: "assoc-ignore-start-role",
		Role:          common.WORK_P2P_VISITOR,
		Self:          p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		Peer:          p2p.P2PPeerRuntime{UUID: "provider-runtime"},
		Route:         p2p.P2PRouteContext{TunnelID: 404},
	})
	runtimeClientP2PStateRoot.RecordPunchStart(client, p2p.P2PPunchStart{
		AssociationID: "assoc-ignore-start-self",
		Role:          common.WORK_P2P_PROVIDER,
		Self:          p2p.P2PPeerRuntime{UUID: "other-provider"},
		Peer:          p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		Route:         p2p.P2PRouteContext{TunnelID: 405},
	})

	if _, ok := runtimeClientP2PStateRoot.Association(client, "assoc-ignore-start-role"); ok {
		t.Fatal("RecordPunchStart() should ignore visitor-side punch starts on provider runtime")
	}
	if _, ok := runtimeClientP2PStateRoot.Association(client, "assoc-ignore-start-self"); ok {
		t.Fatal("RecordPunchStart() should ignore punch starts for a different provider runtime")
	}
}

func TestMergeP2PAccessGrantKeepsNormalizedCurrentWhenGrantMissing(t *testing.T) {
	current := p2p.P2PAccessPolicy{
		Mode:    p2p.P2PAccessModeWhitelist,
		Targets: []string{"10.0.0.1:22"},
	}

	merged := mergeP2PAccessGrant(current, p2p.P2PAccessPolicy{})
	if merged.Mode != p2p.P2PAccessModeWhitelist {
		t.Fatalf("mergeP2PAccessGrant() mode = %q, want %q", merged.Mode, p2p.P2PAccessModeWhitelist)
	}
	if len(merged.Targets) != 1 || merged.Targets[0] != "10.0.0.1:22" {
		t.Fatalf("mergeP2PAccessGrant() targets = %#v, want current targets", merged.Targets)
	}
}

func TestApplyP2PAssociationGrantUpdatesRuntimeAndPeerPolicy(t *testing.T) {
	now := time.Unix(123, 0)
	runtime := &p2pAssociationRuntime{}
	peer := &p2pPeerPolicyRuntime{PeerUUID: "visitor-runtime"}
	route := p2p.P2PRouteContext{TunnelID: 301}
	grant := p2p.P2PAccessPolicy{
		Mode:       p2p.P2PAccessModeOpen,
		OpenReason: p2p.P2PAccessReasonProxyMode,
	}

	applyP2PAssociationGrant(runtime, peer, route, grant, now)

	if runtime.UpdatedAt != now {
		t.Fatalf("runtime.UpdatedAt = %v, want %v", runtime.UpdatedAt, now)
	}
	if runtime.PeerUUID != "visitor-runtime" {
		t.Fatalf("runtime.PeerUUID = %q, want %q", runtime.PeerUUID, "visitor-runtime")
	}
	if runtime.RouteRefs[301].TunnelID != 301 {
		t.Fatalf("runtime.RouteRefs[301] = %#v, want tunnel 301", runtime.RouteRefs[301])
	}
	if peer.UpdatedAt != now {
		t.Fatalf("peer.UpdatedAt = %v, want %v", peer.UpdatedAt, now)
	}
	if peer.EffectivePolicy.Mode != p2p.P2PAccessModeOpen {
		t.Fatalf("peer.EffectivePolicy.Mode = %q, want %q", peer.EffectivePolicy.Mode, p2p.P2PAccessModeOpen)
	}
	if peer.RouteGrants[301].Mode != p2p.P2PAccessModeOpen {
		t.Fatalf("peer.RouteGrants[301] = %#v, want open", peer.RouteGrants[301])
	}
}

func TestP2PAccessPolicyAllows(t *testing.T) {
	policy := p2p.P2PAccessPolicy{
		Mode:    p2p.P2PAccessModeWhitelist,
		Targets: []string{"EXAMPLE.com:443", "8080"},
	}
	if !p2pAccessPolicyAllows(policy, "example.com:443") {
		t.Fatal("expected case-insensitive domain target to be allowed")
	}
	if !p2pAccessPolicyAllows(policy, "127.0.0.1:8080") {
		t.Fatal("expected bare-port whitelist target to normalize to loopback")
	}
	if p2pAccessPolicyAllows(policy, "example.com:80") {
		t.Fatal("unexpected allow for non-whitelisted target")
	}
	if !p2pAccessPolicyAllows(p2p.P2PAccessPolicy{Mode: p2p.P2PAccessModeOpen}, "198.51.100.10:22") {
		t.Fatal("open policy should allow any target")
	}
}

func TestHandleChanDeniesP2PAssociationWhitelistMiss(t *testing.T) {
	clientRuntime := &TRPClient{
		p2pState: &clientP2PRuntimeStateHolder{
			associations: map[string]*p2pAssociationRuntime{
				"assoc-1": {
					PeerUUID: "visitor-runtime",
				},
			},
			peerPolicies: map[string]*p2pPeerPolicyRuntime{
				"visitor-runtime": {
					PeerUUID: "visitor-runtime",
					EffectivePolicy: p2p.P2PAccessPolicy{
						Mode:    p2p.P2PAccessModeWhitelist,
						Targets: []string{"127.0.0.1:18080"},
					},
				},
			},
		},
	}

	serverSide, visitorSide := net.Pipe()
	defer func() { _ = visitorSide.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		clientRuntime.handleChan(wrapP2PAssociationConn(serverSide, "assoc-1"))
	}()

	link := conn.NewLink("tcp", "127.0.0.1:19999", false, false, "", false,
		conn.LinkTimeout(time.Second),
		conn.WithConnectResult(true),
	)
	if _, err := conn.NewConn(visitorSide).SendInfo(link, ""); err != nil {
		t.Fatalf("send link info failed: %v", err)
	}

	status, err := conn.ReadConnectResult(visitorSide, time.Second)
	if err != nil {
		t.Fatalf("read connect result failed: %v", err)
	}
	if status != conn.ConnectResultNotAllowed {
		t.Fatalf("connect result = %v, want %v", status, conn.ConnectResultNotAllowed)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleChan did not return after denying target")
	}
}

func TestHandleChanDeniesWhenP2PAssociationACLRuntimeIsMissing(t *testing.T) {
	clientRuntime := &TRPClient{
		p2pState: &clientP2PRuntimeStateHolder{
			associations: map[string]*p2pAssociationRuntime{
				"assoc-1": {
					PeerUUID: "visitor-runtime",
				},
			},
		},
	}

	serverSide, visitorSide := net.Pipe()
	defer func() { _ = visitorSide.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		clientRuntime.handleChan(wrapP2PAssociationConn(serverSide, "assoc-1"))
	}()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false,
		conn.LinkTimeout(time.Second),
		conn.WithConnectResult(true),
	)
	if _, err := conn.NewConn(visitorSide).SendInfo(link, ""); err != nil {
		t.Fatalf("send link info failed: %v", err)
	}

	status, err := conn.ReadConnectResult(visitorSide, time.Second)
	if err != nil {
		t.Fatalf("read connect result failed: %v", err)
	}
	if status != conn.ConnectResultNotAllowed {
		t.Fatalf("connect result = %v, want %v", status, conn.ConnectResultNotAllowed)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleChan did not return after denying missing acl runtime")
	}
}

func TestRecordP2PAssociationRuntimeTracksBindAndPunchStart(t *testing.T) {
	c := &TRPClient{uuid: "provider-runtime"}
	association := p2p.P2PAssociation{
		AssociationID: "assoc-1",
		Visitor:       p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-runtime"},
		Provider:      p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-runtime"},
	}

	runtimeClientP2PStateRoot.RecordBind(c, p2p.P2PAssociationBind{
		Association: association,
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.1:22"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 101},
		Phase: "binding",
	})
	runtimeClientP2PStateRoot.RecordPunchStart(c, p2p.P2PPunchStart{
		AssociationID: association.AssociationID,
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode:       p2p.P2PAccessModeOpen,
			OpenReason: p2p.P2PAccessReasonDynamicTarget,
		},
		Role:  common.WORK_P2P_PROVIDER,
		Self:  association.Provider,
		Peer:  association.Visitor,
		Route: p2p.P2PRouteContext{TunnelID: 102},
	})

	runtime, ok := runtimeClientP2PStateRoot.Association(c, association.AssociationID)
	if !ok || runtime == nil {
		t.Fatal("p2p association runtime missing")
	}
	if runtime.Phase != "punching" {
		t.Fatalf("runtime phase = %q, want punching", runtime.Phase)
	}
	if runtime.PeerUUID != association.Visitor.UUID {
		t.Fatalf("peer uuid = %q, want %q", runtime.PeerUUID, association.Visitor.UUID)
	}
	if runtime.Association.Provider.UUID != association.Provider.UUID {
		t.Fatalf("provider uuid = %q, want %q", runtime.Association.Provider.UUID, association.Provider.UUID)
	}
	if runtime.Association.Visitor.UUID != association.Visitor.UUID {
		t.Fatalf("visitor uuid = %q, want %q", runtime.Association.Visitor.UUID, association.Visitor.UUID)
	}
	if len(runtime.RouteRefs) != 2 {
		t.Fatalf("routeRefs len = %d, want 2", len(runtime.RouteRefs))
	}
	if _, ok := runtime.RouteRefs[101]; !ok {
		t.Fatal("missing bind route context")
	}
	if _, ok := runtime.RouteRefs[102]; !ok {
		t.Fatal("missing punch-start route context")
	}
	peerPolicy, ok := runtimeClientP2PStateRoot.PeerPolicy(c, association.Visitor.UUID)
	if !ok || peerPolicy == nil {
		t.Fatal("peer policy runtime missing")
	}
	if peerPolicy.EffectivePolicy.Mode != p2p.P2PAccessModeOpen {
		t.Fatalf("effective policy mode = %q, want open", peerPolicy.EffectivePolicy.Mode)
	}
	if len(peerPolicy.RouteGrants) != 2 {
		t.Fatalf("route grants len = %d, want 2", len(peerPolicy.RouteGrants))
	}
}

func TestRecordP2PAssociationRuntimeKeepsPeerPolicyWidenOnly(t *testing.T) {
	c := &TRPClient{uuid: "provider-runtime"}
	association := p2p.P2PAssociation{
		AssociationID: "assoc-2",
		Visitor:       p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-runtime"},
		Provider:      p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-runtime"},
	}

	runtimeClientP2PStateRoot.RecordBind(c, p2p.P2PAssociationBind{
		Association: association,
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode:       p2p.P2PAccessModeOpen,
			OpenReason: p2p.P2PAccessReasonProxyMode,
		},
		Route: p2p.P2PRouteContext{TunnelID: 201},
		Phase: "binding",
	})
	runtimeClientP2PStateRoot.RecordBind(c, p2p.P2PAssociationBind{
		Association: association,
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.8:8443"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 201},
		Phase: "binding",
	})

	peerPolicy, ok := runtimeClientP2PStateRoot.PeerPolicy(c, association.Visitor.UUID)
	if !ok || peerPolicy == nil {
		t.Fatal("peer policy runtime missing")
	}
	if peerPolicy.EffectivePolicy.Mode != p2p.P2PAccessModeOpen {
		t.Fatalf("effective policy mode = %q, want open after narrower update", peerPolicy.EffectivePolicy.Mode)
	}
	if grant := peerPolicy.RouteGrants[201]; grant.Mode != p2p.P2PAccessModeWhitelist || len(grant.Targets) != 1 || grant.Targets[0] != "10.0.0.8:8443" {
		t.Fatalf("latest route grant = %#v, want narrowed route snapshot", grant)
	}
}
