package bridge

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/p2p"
)

func TestP2PAssociationManagerReusesAssociationForSamePeerPair(t *testing.T) {
	mgr := newP2PAssociationManager()
	visitor := p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-runtime"}
	provider := p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-runtime"}

	assoc1, policy1, phase1, needPunch1 := mgr.attach(visitor, provider, p2p.P2PRouteContext{
		TunnelID: 101,
		AccessPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.1:22"},
		},
	})
	if assoc1.AssociationID == "" {
		t.Fatal("first attach should allocate association id")
	}
	if policy1.Mode != p2p.P2PAccessModeWhitelist || len(policy1.Targets) != 1 {
		t.Fatalf("first attach policy = %#v, want whitelist with one target", policy1)
	}
	if phase1 != p2pAssociationPhaseBinding || !needPunch1 {
		t.Fatalf("first attach = phase %q needPunch %v, want binding/true", phase1, needPunch1)
	}

	mgr.markPunching(assoc1.AssociationID)
	assoc2, policy2, phase2, needPunch2 := mgr.attach(visitor, provider, p2p.P2PRouteContext{
		TunnelID: 102,
		AccessPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.2:53"},
		},
	})
	if assoc2.AssociationID != assoc1.AssociationID {
		t.Fatalf("second attach association = %q, want %q", assoc2.AssociationID, assoc1.AssociationID)
	}
	if policy2.Mode != p2p.P2PAccessModeWhitelist || len(policy2.Targets) != 1 || policy2.Targets[0] != "10.0.0.2:53" {
		t.Fatalf("second attach policy = %#v, want current route grant", policy2)
	}
	if phase2 != p2pAssociationPhasePunching || needPunch2 {
		t.Fatalf("second attach = phase %q needPunch %v, want punching/false", phase2, needPunch2)
	}

	mgr.markEstablished(assoc1.AssociationID)
	if _, ok := mgr.byID[assoc1.AssociationID]; ok {
		t.Fatal("markEstablished should drop established association state")
	}
	assoc3, policy3, phase3, needPunch3 := mgr.attach(visitor, provider, p2p.P2PRouteContext{
		TunnelID:     103,
		AccessPolicy: p2p.BuildP2PAccessPolicy("", false),
	})
	if assoc3.AssociationID != assoc1.AssociationID {
		t.Fatalf("third attach association = %q, want %q", assoc3.AssociationID, assoc1.AssociationID)
	}
	if policy3.Mode != p2p.P2PAccessModeOpen {
		t.Fatalf("third attach policy = %#v, want open-dominant policy", policy3)
	}
	if phase3 != p2pAssociationPhaseBinding || !needPunch3 {
		t.Fatalf("third attach = phase %q needPunch %v, want binding/true after established cleanup", phase3, needPunch3)
	}

	state := mgr.byID[assoc1.AssociationID]
	if state == nil {
		t.Fatal("association state missing after attach")
	}
	if len(mgr.byID) != 1 {
		t.Fatalf("association state count = %d, want 1", len(mgr.byID))
	}
	if state.phase != p2pAssociationPhaseBinding {
		t.Fatalf("stored phase = %q, want binding after re-attach", state.phase)
	}
}

func TestP2PAssociationManagerResetsFailedAssociationToBinding(t *testing.T) {
	mgr := newP2PAssociationManager()
	visitor := p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-runtime"}
	provider := p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-runtime"}

	assoc1, _, _, _ := mgr.attach(visitor, provider, p2p.P2PRouteContext{TunnelID: 201})
	mgr.markFailed(assoc1.AssociationID)
	if _, ok := mgr.byID[assoc1.AssociationID]; ok {
		t.Fatal("markFailed should drop failed association state")
	}

	assoc2, _, phase2, needPunch2 := mgr.attach(visitor, provider, p2p.P2PRouteContext{TunnelID: 202})
	if assoc2.AssociationID != assoc1.AssociationID {
		t.Fatalf("re-attach association = %q, want %q", assoc2.AssociationID, assoc1.AssociationID)
	}
	if phase2 != p2pAssociationPhaseBinding || !needPunch2 {
		t.Fatalf("re-attach = phase %q needPunch %v, want binding/true", phase2, needPunch2)
	}
}

func TestP2PAssociationManagerReopensExpiredPendingAssociation(t *testing.T) {
	mgr := newP2PAssociationManager()
	visitor := p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-runtime"}
	provider := p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-runtime"}
	now := time.Now()

	assoc1, _, phase1, needPunch1 := mgr.attachAt(visitor, provider, p2p.P2PRouteContext{TunnelID: 301}, now)
	if phase1 != p2pAssociationPhaseBinding || !needPunch1 {
		t.Fatalf("first attach = phase %q needPunch %v, want binding/true", phase1, needPunch1)
	}

	assoc2, _, phase2, needPunch2 := mgr.attachAt(visitor, provider, p2p.P2PRouteContext{TunnelID: 302}, now.Add(p2pAssociationPendingTTL+time.Second))
	if assoc2.AssociationID != assoc1.AssociationID {
		t.Fatalf("expired binding attach association = %q, want %q", assoc2.AssociationID, assoc1.AssociationID)
	}
	if phase2 != p2pAssociationPhaseBinding || !needPunch2 {
		t.Fatalf("expired binding attach = phase %q needPunch %v, want binding/true", phase2, needPunch2)
	}

	mgr.markPunching(assoc1.AssociationID)
	state := mgr.byID[assoc1.AssociationID]
	state.updatedAt = now

	assoc3, _, phase3, needPunch3 := mgr.attachAt(visitor, provider, p2p.P2PRouteContext{TunnelID: 303}, now.Add(p2pAssociationPendingTTL+2*time.Second))
	if assoc3.AssociationID != assoc1.AssociationID {
		t.Fatalf("expired punching attach association = %q, want %q", assoc3.AssociationID, assoc1.AssociationID)
	}
	if phase3 != p2pAssociationPhaseBinding || !needPunch3 {
		t.Fatalf("expired punching attach = phase %q needPunch %v, want binding/true", phase3, needPunch3)
	}
}

func TestP2PAssociationManagerTracksLatestRouteForSameTunnel(t *testing.T) {
	mgr := newP2PAssociationManager()
	visitor := p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-runtime"}
	provider := p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-runtime"}

	_, _, _, _ = mgr.attach(visitor, provider, p2p.P2PRouteContext{
		TunnelID: 401,
		AccessPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.1:22"},
		},
	})
	_, policy, _, _ := mgr.attach(visitor, provider, p2p.P2PRouteContext{
		TunnelID: 401,
		AccessPolicy: p2p.P2PAccessPolicy{
			Mode:    p2p.P2PAccessModeWhitelist,
			Targets: []string{"10.0.0.3:443"},
		},
	})

	if policy.Mode != p2p.P2PAccessModeWhitelist {
		t.Fatalf("policy mode = %q, want whitelist", policy.Mode)
	}
	if len(policy.Targets) != 1 || policy.Targets[0] != "10.0.0.3:443" {
		t.Fatalf("policy targets = %#v, want current tunnel grant", policy.Targets)
	}
	state := mgr.byID[buildP2PAssociationID(visitor, provider)]
	if state == nil {
		t.Fatal("association state missing after tunnel replacement")
	}
	if len(mgr.byID) != 1 {
		t.Fatalf("association state count = %d, want 1 after replacement", len(mgr.byID))
	}
	if state.phase != p2pAssociationPhaseBinding {
		t.Fatalf("stored phase = %q, want binding after re-attach", state.phase)
	}
}

func TestP2PAssociationManagerSweepsExpiredPendingState(t *testing.T) {
	mgr := newP2PAssociationManager()
	now := time.Now()
	expiredVisitor := p2p.P2PPeerRuntime{ClientID: 11, UUID: "visitor-expired"}
	expiredProvider := p2p.P2PPeerRuntime{ClientID: 22, UUID: "provider-expired"}
	freshVisitor := p2p.P2PPeerRuntime{ClientID: 33, UUID: "visitor-fresh"}
	freshProvider := p2p.P2PPeerRuntime{ClientID: 44, UUID: "provider-fresh"}

	expiredAssoc, _, _, _ := mgr.attachAt(expiredVisitor, expiredProvider, p2p.P2PRouteContext{TunnelID: 501}, now.Add(-p2pAssociationPendingTTL-time.Second))
	if len(mgr.byID) != 1 {
		t.Fatalf("association state count = %d, want 1 after expired attach", len(mgr.byID))
	}

	mgr.lastSweep = now.Add(-p2pAssociationSweepInterval - time.Second)
	freshAssoc, _, phase, needPunch := mgr.attachAt(freshVisitor, freshProvider, p2p.P2PRouteContext{TunnelID: 502}, now)
	if freshAssoc.AssociationID == expiredAssoc.AssociationID {
		t.Fatalf("fresh association id = %q, want different from expired %q", freshAssoc.AssociationID, expiredAssoc.AssociationID)
	}
	if phase != p2pAssociationPhaseBinding || !needPunch {
		t.Fatalf("fresh attach = phase %q needPunch %v, want binding/true", phase, needPunch)
	}
	if _, ok := mgr.byID[expiredAssoc.AssociationID]; ok {
		t.Fatal("expired pending association should be swept on fresh attach")
	}
	if _, ok := mgr.byID[freshAssoc.AssociationID]; !ok {
		t.Fatal("fresh association should remain after sweep")
	}
}

func TestBuildP2PRouteContextPrefersVisitorRouteHint(t *testing.T) {
	task := &file.Tunnel{
		Id:         7,
		Mode:       "p2p",
		TargetType: common.CONN_TCP,
		Target: &file.Target{
			TargetStr: "server-record-target:80",
		},
	}
	hint := p2p.P2PRouteContext{
		TunnelMode: "p2ps",
		TargetType: common.CONN_ALL,
		AccessPolicy: p2p.P2PAccessPolicy{
			Mode:       p2p.P2PAccessModeOpen,
			OpenReason: p2p.P2PAccessReasonProxyMode,
		},
	}

	route := buildP2PRouteContext(task, hint)
	if route.TunnelID != 7 {
		t.Fatalf("TunnelID = %d, want 7", route.TunnelID)
	}
	if route.TunnelMode != "p2ps" {
		t.Fatalf("TunnelMode = %q, want p2ps", route.TunnelMode)
	}
	if route.TargetType != common.CONN_ALL {
		t.Fatalf("TargetType = %q, want %q", route.TargetType, common.CONN_ALL)
	}
	if route.AccessPolicy.Mode != p2p.P2PAccessModeOpen || route.AccessPolicy.OpenReason != p2p.P2PAccessReasonProxyMode {
		t.Fatalf("AccessPolicy = %#v, want open proxy policy", route.AccessPolicy)
	}
}

func TestSendBridgeMessageToNodeSignalRetriesReplacementSignal(t *testing.T) {
	oldWriter := bridgeWriteP2PMessage
	t.Cleanup(func() {
		bridgeWriteP2PMessage = oldWriter
	})

	oldServer, oldPeer := net.Pipe()
	newServer, newPeer := net.Pipe()
	t.Cleanup(func() { _ = oldPeer.Close() })
	t.Cleanup(func() { _ = newPeer.Close() })
	t.Cleanup(func() { _ = oldServer.Close() })
	t.Cleanup(func() { _ = newServer.Close() })

	oldSignal := conn.NewConn(oldServer)
	newSignal := conn.NewConn(newServer)
	node := NewNode("node-1", "test", 6)
	node.AddSignal(oldSignal)
	signal := node.GetSignal()
	node.AddSignal(newSignal)

	calls := make([]*conn.Conn, 0, 2)
	bridgeWriteP2PMessage = func(c *conn.Conn, flag string, payload any) error {
		calls = append(calls, c)
		if flag != common.P2P_ASSOCIATION_BIND {
			t.Fatalf("flag = %q, want %q", flag, common.P2P_ASSOCIATION_BIND)
		}
		if _, ok := payload.(p2p.P2PAssociationBind); !ok {
			t.Fatalf("payload type = %T, want P2PAssociationBind", payload)
		}
		if c == signal {
			return errors.New("stale signal")
		}
		if c != newSignal {
			t.Fatalf("retry conn = %v, want replacement signal", c)
		}
		return nil
	}

	err := sendBridgeMessageToNodeSignal(node, signal, common.P2P_ASSOCIATION_BIND, p2p.P2PAssociationBind{})
	if err != nil {
		t.Fatalf("sendBridgeMessageToNodeSignal() error = %v", err)
	}
	if len(calls) != 2 || calls[0] != signal || calls[1] != newSignal {
		t.Fatalf("calls = %v, want stale then replacement signal", calls)
	}
}

func TestResolveBridgeP2PProviderUsesHintedNodeSignal(t *testing.T) {
	runtimeBridge := NewTunnel(false, nil, 0)
	task := &file.Tunnel{Client: &file.Client{Id: 42}}
	client := NewClient(task.Client.Id, NewNode("primary-node", "test", 6))
	hinted := NewNode("hinted-node", "test", 6)
	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })
	hinted.AddSignal(conn.NewConn(serverConn))
	client.AddNode(hinted)
	runtimeBridge.Client.Store(task.Client.Id, client)

	gotClient, gotNode, gotSignal, err := runtimeBridge.resolveBridgeP2PProvider(task, "hinted-node")
	if err != nil {
		t.Fatalf("resolveBridgeP2PProvider() error = %v", err)
	}
	if gotClient != client {
		t.Fatalf("provider client = %#v, want %#v", gotClient, client)
	}
	if gotNode != hinted {
		t.Fatalf("provider node = %#v, want hinted node %#v", gotNode, hinted)
	}
	if gotSignal == nil || gotSignal != hinted.GetSignal() {
		t.Fatalf("provider signal = %#v, want hinted signal %#v", gotSignal, hinted.GetSignal())
	}
}

func TestResolveBridgeP2PProviderPrunesHintedStaleNode(t *testing.T) {
	runtimeBridge := NewTunnel(false, nil, 0)
	task := &file.Tunnel{Client: &file.Client{Id: 44}}
	healthy := NewNode("healthy-node", "test", 6)
	healthyServer, healthyPeer := net.Pipe()
	t.Cleanup(func() { _ = healthyServer.Close() })
	t.Cleanup(func() { _ = healthyPeer.Close() })
	healthy.AddSignal(conn.NewConn(healthyServer))
	stale := NewNode("stale-node", "test", 6)
	client := NewClient(task.Client.Id, healthy)
	client.AddNode(stale)
	runtimeBridge.Client.Store(task.Client.Id, client)

	var closedClientID int
	runtimeBridge.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	gotClient, gotNode, gotSignal, err := runtimeBridge.resolveBridgeP2PProvider(task, "stale-node")
	if err == nil {
		t.Fatal("resolveBridgeP2PProvider() error = nil, want hinted stale-node signal failure")
	}
	if !strings.Contains(err.Error(), "provider node stale-node signal unavailable") {
		t.Fatalf("resolveBridgeP2PProvider() error = %v, want hinted stale-node signal failure", err)
	}
	if gotClient != client {
		t.Fatalf("provider client = %#v, want %#v", gotClient, client)
	}
	if gotNode != stale {
		t.Fatalf("provider node = %#v, want stale hinted node %#v", gotNode, stale)
	}
	if gotSignal != nil {
		t.Fatalf("provider signal = %#v, want nil for pruned stale node", gotSignal)
	}
	if _, ok := runtimeBridge.Client.Load(task.Client.Id); !ok {
		t.Fatal("stale provider cleanup should not remove the whole runtime client")
	}
	if _, ok := client.GetNodeByUUID("stale-node"); ok {
		t.Fatal("stale hinted provider node should be pruned from runtime client")
	}
	if got, ok := client.GetNodeByUUID("healthy-node"); !ok || got != healthy {
		t.Fatalf("healthy provider node should remain installed, ok=%v got=%v", ok, got)
	}
	if closedClientID != 0 {
		t.Fatalf("close client hook = %d, want 0; stale hinted provider cleanup should not drop the whole client", closedClientID)
	}
}

func TestCurrentBridgeP2PProviderSignalPrunesOnlyStaleNode(t *testing.T) {
	runtimeBridge := NewTunnel(false, nil, 0)
	task := &file.Tunnel{Client: &file.Client{Id: 43}}
	stale := NewNode("stale-node", "test", 6)
	healthy := NewNode("healthy-node", "test", 6)
	runtimeClient := NewClient(task.Client.Id, stale)
	runtimeClient.AddNode(healthy)
	runtimeBridge.Client.Store(task.Client.Id, runtimeClient)

	var closedClientID int
	runtimeBridge.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	if signal := runtimeBridge.currentBridgeP2PProviderSignal(task, stale); signal != nil {
		t.Fatalf("currentBridgeP2PProviderSignal() = %#v, want nil for stale provider", signal)
	}
	if _, ok := runtimeBridge.Client.Load(task.Client.Id); !ok {
		t.Fatal("stale provider cleanup should not remove the whole runtime client")
	}
	if _, ok := runtimeClient.GetNodeByUUID("stale-node"); ok {
		t.Fatal("stale provider node should be pruned from runtime client")
	}
	if got, ok := runtimeClient.GetNodeByUUID("healthy-node"); !ok || got != healthy {
		t.Fatalf("healthy provider node should remain installed, ok=%v got=%v", ok, got)
	}
	if closedClientID != 0 {
		t.Fatalf("close client hook = %d, want 0; stale provider cleanup should not drop the whole client", closedClientID)
	}
}

func setupP2PResolveIsolatedDB(t *testing.T) *file.DbUtils {
	t.Helper()

	oldDBRoot := currentBridgeDBRoot
	oldIndexes := file.SnapshotRuntimeIndexes()
	db := newBridgeIsolatedDB(t)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())
	currentBridgeDBRoot = func() *file.DbUtils {
		return db
	}
	t.Cleanup(func() {
		currentBridgeDBRoot = oldDBRoot
		file.ReplaceRuntimeIndexes(oldIndexes)
	})
	return db
}

func TestResolveP2PRouteByPasswordUsesRuntimeOwnerWhenHintMissing(t *testing.T) {
	db := setupP2PResolveIsolatedDB(t)
	oldSelectMode := ClientSelectMode
	ClientSelectMode = Primary
	t.Cleanup(func() {
		ClientSelectMode = oldSelectMode
	})

	providerClient := file.NewClient("provider-vkey-no-hint", false, false)
	if err := db.NewClient(providerClient); err != nil {
		t.Fatalf("NewClient(provider) error = %v", err)
	}
	task := &file.Tunnel{
		Mode:       "p2p",
		Password:   "runtime-owner-secret",
		TargetType: common.CONN_TCP,
		Target:     &file.Target{TargetStr: "10.0.0.10:8080"},
		Client:     providerClient,
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask(p2p) error = %v", err)
	}
	passwordHash := crypt.Md5(task.Password)
	task.BindRuntimeOwner("owner-node", &file.Tunnel{
		Mode:       "p2p",
		Password:   task.Password,
		TargetType: common.CONN_TCP,
		Target:     &file.Target{TargetStr: "10.0.0.20:8080"},
	})

	runtimeBridge := NewTunnel(false, nil, 0)
	primaryNode := NewNode("primary-node", "test", 6)
	ownerNode := NewNode("owner-node", "test", 6)
	primaryServer, primaryPeer := net.Pipe()
	ownerServer, ownerPeer := net.Pipe()
	t.Cleanup(func() { _ = primaryServer.Close() })
	t.Cleanup(func() { _ = primaryPeer.Close() })
	t.Cleanup(func() { _ = ownerServer.Close() })
	t.Cleanup(func() { _ = ownerPeer.Close() })
	primaryNode.AddSignal(conn.NewConn(primaryServer))
	ownerNode.AddSignal(conn.NewConn(ownerServer))
	runtimeClient := NewClient(providerClient.Id, primaryNode)
	runtimeClient.AddNode(ownerNode)
	runtimeBridge.Client.Store(providerClient.Id, runtimeClient)

	gotTask, _, gotNode, _, routeGrant, route, _, _, err := runtimeBridge.resolveP2PRouteByPassword(77, "visitor-node", 6, passwordHash, "", p2p.P2PRouteContext{})
	if err != nil {
		t.Fatalf("resolveP2PRouteByPassword() error = %v", err)
	}
	if gotNode == nil || gotNode.UUID != "owner-node" {
		t.Fatalf("provider node = %+v, want owner-node", gotNode)
	}
	if gotTask == nil || gotTask.RuntimeRouteUUID() != "owner-node" {
		t.Fatalf("resolved task route uuid = %q, want owner-node", gotTask.RuntimeRouteUUID())
	}
	if route.TargetType != common.CONN_TCP {
		t.Fatalf("route.TargetType = %q, want %q", route.TargetType, common.CONN_TCP)
	}
	if routeGrant.Mode != p2p.P2PAccessModeWhitelist || len(routeGrant.Targets) != 1 || routeGrant.Targets[0] != "10.0.0.20:8080" {
		t.Fatalf("route grant = %#v, want owner-node backend target", routeGrant)
	}
}

func TestResolveP2PRouteByPasswordUsesHintedOwnerView(t *testing.T) {
	db := setupP2PResolveIsolatedDB(t)
	oldSelectMode := ClientSelectMode
	ClientSelectMode = Primary
	t.Cleanup(func() {
		ClientSelectMode = oldSelectMode
	})

	providerClient := file.NewClient("provider-vkey-hint", false, false)
	if err := db.NewClient(providerClient); err != nil {
		t.Fatalf("NewClient(provider) error = %v", err)
	}
	task := &file.Tunnel{
		Mode:       "p2p",
		Password:   "hinted-owner-secret",
		TargetType: common.CONN_TCP,
		Target:     &file.Target{TargetStr: "10.0.0.30:8080"},
		Client:     providerClient,
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask(p2p) error = %v", err)
	}
	passwordHash := crypt.Md5(task.Password)
	task.BindRuntimeOwner("owner-a", &file.Tunnel{
		Mode:       "p2p",
		Password:   task.Password,
		TargetType: common.CONN_TCP,
		Target:     &file.Target{TargetStr: "10.0.0.31:8080"},
	})
	task.BindRuntimeOwner("owner-b", &file.Tunnel{
		Mode:       "tcp",
		Password:   task.Password,
		TargetType: common.CONN_UDP,
		Target:     &file.Target{TargetStr: "10.0.0.32:8080"},
	})

	runtimeBridge := NewTunnel(false, nil, 0)
	primaryNode := NewNode("owner-a", "test", 6)
	hintedNode := NewNode("owner-b", "test", 6)
	primaryServer, primaryPeer := net.Pipe()
	hintedServer, hintedPeer := net.Pipe()
	t.Cleanup(func() { _ = primaryServer.Close() })
	t.Cleanup(func() { _ = primaryPeer.Close() })
	t.Cleanup(func() { _ = hintedServer.Close() })
	t.Cleanup(func() { _ = hintedPeer.Close() })
	primaryNode.AddSignal(conn.NewConn(primaryServer))
	hintedNode.AddSignal(conn.NewConn(hintedServer))
	runtimeClient := NewClient(providerClient.Id, primaryNode)
	runtimeClient.AddNode(hintedNode)
	runtimeBridge.Client.Store(providerClient.Id, runtimeClient)

	gotTask, _, gotNode, _, routeGrant, route, _, _, err := runtimeBridge.resolveP2PRouteByPassword(88, "visitor-node", 6, passwordHash, "owner-b", p2p.P2PRouteContext{})
	if err != nil {
		t.Fatalf("resolveP2PRouteByPassword() error = %v", err)
	}
	if gotNode == nil || gotNode.UUID != "owner-b" {
		t.Fatalf("provider node = %+v, want owner-b", gotNode)
	}
	if gotTask == nil || gotTask.RuntimeRouteUUID() != "owner-b" {
		t.Fatalf("resolved task route uuid = %q, want owner-b", gotTask.RuntimeRouteUUID())
	}
	if gotTask.Target == nil || gotTask.Target.TargetStr != "10.0.0.32:8080" {
		t.Fatalf("resolved task target = %+v, want owner-b backend target", gotTask.Target)
	}
	if route.TargetType != common.CONN_TCP {
		t.Fatalf("route.TargetType = %q, want canonical target type %q", route.TargetType, common.CONN_TCP)
	}
	if routeGrant.Mode != p2p.P2PAccessModeWhitelist || len(routeGrant.Targets) != 1 || routeGrant.Targets[0] != "10.0.0.32:8080" {
		t.Fatalf("route grant = %#v, want owner-b backend target", routeGrant)
	}
}
