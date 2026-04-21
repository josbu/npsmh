package bridge

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/p2p"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/p2pstate"
)

type typedNilBridgeConnStub struct{}

func (s *typedNilBridgeConnStub) Read([]byte) (int, error) {
	panic("unexpected Read call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) Write([]byte) (int, error) {
	panic("unexpected Write call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) Close() error {
	panic("unexpected Close call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) LocalAddr() net.Addr {
	panic("unexpected LocalAddr call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) RemoteAddr() net.Addr {
	panic("unexpected RemoteAddr call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) SetDeadline(time.Time) error {
	panic("unexpected SetDeadline call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) SetReadDeadline(time.Time) error {
	panic("unexpected SetReadDeadline call on typed nil bridge conn")
}

func (s *typedNilBridgeConnStub) SetWriteDeadline(time.Time) error {
	panic("unexpected SetWriteDeadline call on typed nil bridge conn")
}

type nilAddrBridgeConnStub struct{}

func (s *nilAddrBridgeConnStub) Read([]byte) (int, error) {
	panic("unexpected Read call on nil-addr bridge conn")
}

func (s *nilAddrBridgeConnStub) Write([]byte) (int, error) {
	panic("unexpected Write call on nil-addr bridge conn")
}

func (s *nilAddrBridgeConnStub) Close() error {
	return nil
}

func (s *nilAddrBridgeConnStub) LocalAddr() net.Addr {
	return nil
}

func (s *nilAddrBridgeConnStub) RemoteAddr() net.Addr {
	return nil
}

func (s *nilAddrBridgeConnStub) SetDeadline(time.Time) error {
	return nil
}

func (s *nilAddrBridgeConnStub) SetReadDeadline(time.Time) error {
	return nil
}

func (s *nilAddrBridgeConnStub) SetWriteDeadline(time.Time) error {
	return nil
}

func newBridgeIsolatedDB(t *testing.T) *file.DbUtils {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	return db
}

func TestP2PBridgeSessionAbortStillRelaysAfterSummary(t *testing.T) {
	visitorServer, visitorClient := net.Pipe()
	providerServer, providerClient := net.Pipe()
	defer func() { _ = visitorServer.Close() }()
	defer func() { _ = visitorClient.Close() }()
	defer func() { _ = providerServer.Close() }()
	defer func() { _ = providerClient.Close() }()

	session := &p2pBridgeSession{
		mgr:             newP2PSessionManager(newP2PAssociationManager()),
		id:              "session-1",
		token:           "token-1",
		timeouts:        p2p.P2PTimeouts{ProbeTimeoutMs: 1000, HandshakeTimeoutMs: 1000, TransportTimeoutMs: 1000},
		visitorControl:  conn.NewConn(visitorServer),
		providerControl: conn.NewConn(providerServer),
		visitorReport: &p2p.P2PProbeReport{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_VISITOR,
			PeerRole:  common.WORK_P2P_PROVIDER,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_VISITOR},
		},
		providerReport: &p2p.P2PProbeReport{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_PROVIDER,
			PeerRole:  common.WORK_P2P_VISITOR,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
		},
		timer: time.NewTimer(time.Minute),
	}
	session.mgr.sessions.Store(session.id, session)
	p2pstate.Register(session.id, p2p.P2PWireSpec{RouteID: session.id}, session.token, time.Minute)
	defer p2pstate.Unregister(session.id)

	type result struct {
		flag  string
		abort p2p.P2PPunchAbort
		err   error
	}
	readSide := func(c net.Conn) <-chan result {
		out := make(chan result, 1)
		go func() {
			defer close(out)
			cc := conn.NewConn(c)
			if _, err := p2p.ReadBridgeJSON[p2p.P2PProbeSummary](cc, common.P2P_PROBE_SUMMARY); err != nil {
				out <- result{err: err}
				return
			}
			flag, err := cc.ReadFlag()
			if err != nil {
				out <- result{err: err}
				return
			}
			raw, err := cc.GetShortLenContent()
			if err != nil {
				out <- result{err: err}
				return
			}
			var abort p2p.P2PPunchAbort
			if err := json.Unmarshal(raw, &abort); err != nil {
				out <- result{err: err}
				return
			}
			out <- result{flag: flag, abort: abort}
		}()
		return out
	}

	visitorDone := readSide(visitorClient)
	providerDone := readSide(providerClient)

	session.sendSummary()
	session.abort("late failure")

	visitorResult := <-visitorDone
	providerResult := <-providerDone
	if visitorResult.err != nil {
		t.Fatalf("visitor side read failed: %v", visitorResult.err)
	}
	if providerResult.err != nil {
		t.Fatalf("provider side read failed: %v", providerResult.err)
	}
	if visitorResult.flag != common.P2P_PUNCH_ABORT || providerResult.flag != common.P2P_PUNCH_ABORT {
		t.Fatalf("unexpected abort flags visitor=%q provider=%q", visitorResult.flag, providerResult.flag)
	}
	if visitorResult.abort.Reason != "late failure" || providerResult.abort.Reason != "late failure" {
		t.Fatalf("unexpected abort payload visitor=%#v provider=%#v", visitorResult.abort, providerResult.abort)
	}
}

func TestCollectProbeBaseHostsIncludesBridgeAndConfiguredFamilies(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	serverConnCh := make(chan net.Conn, 1)
	go func() {
		serverConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		serverConnCh <- serverConn
	}()

	clientConn, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = clientConn.Close() }()
	serverConn := <-serverConnCh
	defer func() { _ = serverConn.Close() }()

	originalConfig := connection.CurrentConfig()
	updatedConfig := originalConfig
	updatedConfig.P2pIp = "::1"
	connection.ApplySnapshot(updatedConfig.Snapshot())
	defer func() { connection.ApplySnapshot(originalConfig.Snapshot()) }()

	hosts := collectProbeBaseHosts(conn.NewConn(serverConn), currentBridgeP2PRuntime())
	if len(hosts) < 2 {
		t.Fatalf("expected dual-family probe hosts, got %#v", hosts)
	}
	if hosts[0] != "127.0.0.1" {
		t.Fatalf("hosts[0] = %q, want bridge local IPv4", hosts[0])
	}
	foundIPv6 := false
	for _, host := range hosts {
		if host == "::1" {
			foundIPv6 = true
			break
		}
	}
	if !foundIPv6 {
		t.Fatalf("expected configured IPv6 probe host in %#v", hosts)
	}
}

func TestBridgeConnLocalAddrStringHandlesMalformedConn(t *testing.T) {
	var typedNil *typedNilBridgeConnStub

	if got := bridgeConnLocalAddrString(nil); got != "" {
		t.Fatalf("bridgeConnLocalAddrString(nil) = %q, want empty", got)
	}
	if got := bridgeConnLocalAddrString(&conn.Conn{}); got != "" {
		t.Fatalf("bridgeConnLocalAddrString(zero) = %q, want empty", got)
	}
	if got := bridgeConnLocalAddrString(&conn.Conn{Conn: typedNil}); got != "" {
		t.Fatalf("bridgeConnLocalAddrString(typed nil) = %q, want empty", got)
	}
	if got := bridgeConnLocalAddrString(&conn.Conn{Conn: &nilAddrBridgeConnStub{}}); got != "" {
		t.Fatalf("bridgeConnLocalAddrString(nil addr) = %q, want empty", got)
	}
}

func TestCollectProbeBaseHostsHandlesMalformedConn(t *testing.T) {
	var typedNil *typedNilBridgeConnStub

	hosts := collectProbeBaseHosts(&conn.Conn{Conn: typedNil}, connection.P2PRuntimeConfig{IP: "127.0.0.1"})
	if len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("collectProbeBaseHosts(typed nil) = %#v, want only configured host", hosts)
	}

	hosts = collectProbeBaseHosts(&conn.Conn{Conn: &nilAddrBridgeConnStub{}}, connection.P2PRuntimeConfig{IP: "127.0.0.1"})
	if len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("collectProbeBaseHosts(nil addr) = %#v, want only configured host", hosts)
	}
}

func TestBuildProbePolicyIncludesPCPFlag(t *testing.T) {
	policy := buildProbePolicy(12345)
	if policy == nil {
		t.Fatal("buildProbePolicy should return a typed policy model")
	}
	if policy.BasePort != 12345 {
		t.Fatalf("typed policy should preserve base_port, got %d", policy.BasePort)
	}
	if policy.Layout != "triple-port" {
		t.Fatalf("unexpected probe layout %q", policy.Layout)
	}
	if policy.PortMapping.EnablePCPPortmap != servercfg.Current().P2P.EnablePCPPortmap {
		t.Fatalf("typed policy should mirror current PCP setting, policy=%v config=%v", policy.PortMapping.EnablePCPPortmap, servercfg.Current().P2P.EnablePCPPortmap)
	}
}

func TestBridgeProbeConfigUsesCurrentP2PRuntimeRoot(t *testing.T) {
	oldRuntimeRoot := currentBridgeP2PRuntimeRoot
	defer func() { currentBridgeP2PRuntimeRoot = oldRuntimeRoot }()

	currentBridgeP2PRuntimeRoot = func() connection.P2PRuntimeConfig {
		return connection.P2PRuntimeConfig{IP: "127.0.0.1", Port: 23001}
	}
	first := buildProbeConfig(nil)
	if len(first.Endpoints) != 3 {
		t.Fatalf("len(first.Endpoints) = %d, want 3", len(first.Endpoints))
	}
	if !strings.Contains(first.Endpoints[0].Address, "23001") {
		t.Fatalf("first endpoint address = %q, want port 23001", first.Endpoints[0].Address)
	}

	currentBridgeP2PRuntimeRoot = func() connection.P2PRuntimeConfig {
		return connection.P2PRuntimeConfig{IP: "127.0.0.1", Port: 24001}
	}
	second := buildProbeConfig(nil)
	if len(second.Endpoints) != 3 {
		t.Fatalf("len(second.Endpoints) = %d, want 3", len(second.Endpoints))
	}
	if !strings.Contains(second.Endpoints[0].Address, "24001") {
		t.Fatalf("second endpoint address = %q, want port 24001", second.Endpoints[0].Address)
	}
	if first.Endpoints[0].Address == second.Endpoints[0].Address {
		t.Fatalf("probe config should track current runtime root, first=%q second=%q", first.Endpoints[0].Address, second.Endpoints[0].Address)
	}
}

func TestBuildBridgeP2PProbeEndpointsUsesTriplePortSpan(t *testing.T) {
	endpoints := buildBridgeP2PProbeEndpoints([]string{"127.0.0.1", "::1"}, 23001)
	if len(endpoints) != 6 {
		t.Fatalf("len(endpoints) = %d, want 6", len(endpoints))
	}
	if endpoints[0].Address != "127.0.0.1:23001" {
		t.Fatalf("endpoints[0].Address = %q, want 127.0.0.1:23001", endpoints[0].Address)
	}
	if endpoints[2].Address != "127.0.0.1:23003" {
		t.Fatalf("endpoints[2].Address = %q, want 127.0.0.1:23003", endpoints[2].Address)
	}
	if endpoints[3].Address != "[::1]:23001" {
		t.Fatalf("endpoints[3].Address = %q, want [::1]:23001", endpoints[3].Address)
	}
	if endpoints[5].Address != "[::1]:23003" {
		t.Fatalf("endpoints[5].Address = %q, want [::1]:23003", endpoints[5].Address)
	}
}

func TestBridgeProbePolicyAndTimeoutsUseCurrentConfigRoot(t *testing.T) {
	oldConfigRoot := currentBridgeConfigRoot
	defer func() { currentBridgeConfigRoot = oldConfigRoot }()

	first := &servercfg.Snapshot{}
	first.P2P.EnablePCPPortmap = true
	first.P2P.ProbeTimeoutMs = 1201
	first.P2P.HandshakeTimeoutMs = 2202
	first.P2P.TransportTimeoutMs = 3203

	second := &servercfg.Snapshot{}
	second.P2P.EnablePCPPortmap = false
	second.P2P.ProbeTimeoutMs = 4204
	second.P2P.HandshakeTimeoutMs = 5205
	second.P2P.TransportTimeoutMs = 6206

	current := first
	currentBridgeConfigRoot = func() *servercfg.Snapshot {
		return current
	}

	firstPolicy := buildProbePolicy(12345)
	firstTimeouts := loadP2PTimeouts()
	if !firstPolicy.PortMapping.EnablePCPPortmap {
		t.Fatalf("first policy should reflect current config root: %#v", firstPolicy.PortMapping)
	}
	if firstTimeouts != (p2p.P2PTimeouts{ProbeTimeoutMs: 1201, HandshakeTimeoutMs: 2202, TransportTimeoutMs: 3203}) {
		t.Fatalf("first timeouts = %#v", firstTimeouts)
	}

	current = second
	secondPolicy := buildProbePolicy(12345)
	secondTimeouts := loadP2PTimeouts()
	if secondPolicy.PortMapping.EnablePCPPortmap {
		t.Fatalf("second policy should reflect updated config root: %#v", secondPolicy.PortMapping)
	}
	if secondTimeouts != (p2p.P2PTimeouts{ProbeTimeoutMs: 4204, HandshakeTimeoutMs: 5205, TransportTimeoutMs: 6206}) {
		t.Fatalf("second timeouts = %#v", secondTimeouts)
	}
}

func TestResolveP2PRouteByPasswordUsesCurrentBridgeDBRoot(t *testing.T) {
	oldDBRoot := currentBridgeDBRoot
	oldIndexes := file.SnapshotRuntimeIndexes()
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())
	defer func() {
		currentBridgeDBRoot = oldDBRoot
		file.ReplaceRuntimeIndexes(oldIndexes)
	}()

	first := newBridgeIsolatedDB(t)
	second := newBridgeIsolatedDB(t)

	providerClient := file.NewClient("provider-vkey", false, false)
	if err := second.NewClient(providerClient); err != nil {
		t.Fatalf("NewClient(provider) error = %v", err)
	}
	task := &file.Tunnel{
		Mode:     "p2p",
		Password: "demo-secret",
		Client:   providerClient,
	}
	if err := second.NewTask(task); err != nil {
		t.Fatalf("NewTask(p2p) error = %v", err)
	}

	serverConn, peerConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = peerConn.Close() }()

	runtimeBridge := NewTunnel(false, nil, 0)
	node := NewNode("provider-node", "test", 6)
	node.AddSignal(conn.NewConn(serverConn))
	runtimeBridge.Client.Store(providerClient.Id, NewClient(providerClient.Id, node))

	currentDB := first
	currentBridgeDBRoot = func() *file.DbUtils {
		return currentDB
	}

	if _, _, _, _, _, _, _, _, err := runtimeBridge.resolveP2PRouteByPassword(77, "visitor-node", 6, crypt.Md5("demo-secret"), "provider-node", p2p.P2PRouteContext{}); err == nil || !strings.Contains(err.Error(), "p2p task not found") {
		t.Fatalf("resolveP2PRouteByPassword(first db) err = %v, want p2p task not found", err)
	}

	currentDB = second
	gotTask, gotClient, gotNode, association, _, _, _, needPunch, err := runtimeBridge.resolveP2PRouteByPassword(77, "visitor-node", 6, crypt.Md5("demo-secret"), "provider-node", p2p.P2PRouteContext{})
	if err != nil {
		t.Fatalf("resolveP2PRouteByPassword(second db) error = %v", err)
	}
	if gotTask == nil || gotTask.Id != task.Id {
		t.Fatalf("resolved task = %+v, want task id %d", gotTask, task.Id)
	}
	if gotClient == nil || gotClient.Id != providerClient.Id {
		t.Fatalf("resolved client = %+v, want provider client id %d", gotClient, providerClient.Id)
	}
	if gotNode == nil || gotNode.UUID != "provider-node" {
		t.Fatalf("resolved node = %+v, want provider-node", gotNode)
	}
	if association.AssociationID == "" || !needPunch {
		t.Fatalf("association = %#v, needPunch=%v, want non-empty association and needPunch", association, needPunch)
	}
}

func TestP2PBridgeSessionSendsGoAfterBothSidesReady(t *testing.T) {
	visitorServer, visitorClient := net.Pipe()
	providerServer, providerClient := net.Pipe()
	defer func() { _ = visitorServer.Close() }()
	defer func() { _ = visitorClient.Close() }()
	defer func() { _ = providerServer.Close() }()
	defer func() { _ = providerClient.Close() }()

	session := &p2pBridgeSession{
		mgr:             newP2PSessionManager(newP2PAssociationManager()),
		id:              "session-go",
		token:           "token-go",
		timeouts:        p2p.P2PTimeouts{ProbeTimeoutMs: 1000, HandshakeTimeoutMs: 1000, TransportTimeoutMs: 1000},
		visitorControl:  conn.NewConn(visitorServer),
		providerControl: conn.NewConn(providerServer),
		visitorReport: &p2p.P2PProbeReport{
			SessionID: "session-go",
			Token:     "token-go",
			Role:      common.WORK_P2P_VISITOR,
			PeerRole:  common.WORK_P2P_PROVIDER,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_VISITOR},
		},
		providerReport: &p2p.P2PProbeReport{
			SessionID: "session-go",
			Token:     "token-go",
			Role:      common.WORK_P2P_PROVIDER,
			PeerRole:  common.WORK_P2P_VISITOR,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
		},
		timer: time.NewTimer(time.Minute),
	}

	type result struct {
		goMsg p2p.P2PPunchGo
		err   error
	}
	readSide := func(c net.Conn) <-chan result {
		out := make(chan result, 1)
		go func() {
			defer close(out)
			cc := conn.NewConn(c)
			if _, err := p2p.ReadBridgeJSON[p2p.P2PProbeSummary](cc, common.P2P_PROBE_SUMMARY); err != nil {
				out <- result{err: err}
				return
			}
			goMsg, err := p2p.ReadBridgeJSON[p2p.P2PPunchGo](cc, common.P2P_PUNCH_GO)
			out <- result{goMsg: goMsg, err: err}
		}()
		return out
	}

	visitorDone := readSide(visitorClient)
	providerDone := readSide(providerClient)
	session.sendSummary()
	session.handleReady(common.WORK_P2P_VISITOR, &p2p.P2PPunchReady{SessionID: session.id, Role: common.WORK_P2P_VISITOR})
	session.handleReady(common.WORK_P2P_PROVIDER, &p2p.P2PPunchReady{SessionID: session.id, Role: common.WORK_P2P_PROVIDER})

	visitorResult := <-visitorDone
	providerResult := <-providerDone
	if visitorResult.err != nil {
		t.Fatalf("visitor side read failed: %v", visitorResult.err)
	}
	if providerResult.err != nil {
		t.Fatalf("provider side read failed: %v", providerResult.err)
	}
	if visitorResult.goMsg.SessionID != session.id || providerResult.goMsg.SessionID != session.id {
		t.Fatalf("unexpected go payload visitor=%#v provider=%#v", visitorResult.goMsg, providerResult.goMsg)
	}
	if visitorResult.goMsg.DelayMs <= 0 || providerResult.goMsg.DelayMs <= 0 {
		t.Fatalf("go delay must be positive visitor=%#v provider=%#v", visitorResult.goMsg, providerResult.goMsg)
	}
}

func TestP2PBridgeSessionSummaryRetainsPostSummaryTimeout(t *testing.T) {
	mgr := newP2PSessionManager(newP2PAssociationManager())
	session := &p2pBridgeSession{
		mgr:      mgr,
		id:       "session-post-summary-timeout",
		token:    "token-post-summary-timeout",
		timeouts: p2p.P2PTimeouts{HandshakeTimeoutMs: 20, TransportTimeoutMs: 20},
		visitorReport: &p2p.P2PProbeReport{
			SessionID: "session-post-summary-timeout",
			Token:     "token-post-summary-timeout",
			Role:      common.WORK_P2P_VISITOR,
			PeerRole:  common.WORK_P2P_PROVIDER,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_VISITOR},
		},
		providerReport: &p2p.P2PProbeReport{
			SessionID: "session-post-summary-timeout",
			Token:     "token-post-summary-timeout",
			Role:      common.WORK_P2P_PROVIDER,
			PeerRole:  common.WORK_P2P_VISITOR,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
		},
		telemetry: newP2PSessionTelemetry(time.Now()),
	}
	mgr.sessions.Store(session.id, session)

	session.sendSummary()

	if _, ok := mgr.get(session.id); !ok {
		t.Fatal("session should remain managed after summary until transport completes or times out")
	}
	if session.telemetrySnapshot().SummaryAtMs == 0 {
		t.Fatal("summary telemetry should be recorded")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		session.mu.Lock()
		closed := session.closed
		timer := session.timer
		session.mu.Unlock()
		if closed {
			break
		}
		if timer == nil {
			t.Fatal("post-summary timeout timer should remain active")
		}
		if time.Now().After(deadline) {
			t.Fatal("post-summary timeout should abort stalled session")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := mgr.get(session.id); ok {
		t.Fatal("timed-out post-summary session should be removed from manager")
	}
}

func TestP2PBridgeSessionSendSummaryUnregistersPendingState(t *testing.T) {
	mgr := newP2PSessionManager(newP2PAssociationManager())
	session := &p2pBridgeSession{
		mgr:      mgr,
		id:       "session-summary-unregister",
		token:    "token-summary-unregister",
		timeouts: p2p.P2PTimeouts{HandshakeTimeoutMs: 1000, TransportTimeoutMs: 1000},
		visitorReport: &p2p.P2PProbeReport{
			SessionID: "session-summary-unregister",
			Token:     "token-summary-unregister",
			Role:      common.WORK_P2P_VISITOR,
			PeerRole:  common.WORK_P2P_PROVIDER,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_VISITOR},
		},
		providerReport: &p2p.P2PProbeReport{
			SessionID: "session-summary-unregister",
			Token:     "token-summary-unregister",
			Role:      common.WORK_P2P_PROVIDER,
			PeerRole:  common.WORK_P2P_VISITOR,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
		},
		telemetry: newP2PSessionTelemetry(time.Now()),
	}
	mgr.sessions.Store(session.id, session)
	p2pstate.Register(session.id, p2p.P2PWireSpec{RouteID: session.id}, session.token, time.Minute)

	if _, ok := p2pstate.LookupSession(p2p.WireRouteKey(session.id)); !ok {
		t.Fatal("expected session to be registered before summary")
	}

	session.sendSummary()

	if _, ok := p2pstate.LookupSession(p2p.WireRouteKey(session.id)); ok {
		t.Fatal("summary dispatch should unregister pending p2p state")
	}
}

func TestBuildSummaryHintsIncludesFamilyBreakdown(t *testing.T) {
	self := p2p.BuildPeerInfo(common.WORK_P2P_VISITOR, common.CONN_KCP, "", []p2p.P2PFamilyInfo{
		{
			Family: "udp4",
			Nat: p2p.NatObservation{
				NATType:             p2p.NATTypeRestrictedCone,
				MappingBehavior:     p2p.NATMappingEndpointIndependent,
				FilteringBehavior:   p2p.NATFilteringPortRestricted,
				ClassificationLevel: p2p.ClassificationConfidenceMed,
				ProbeEndpointCount:  3,
				Samples:             []p2p.ProbeSample{{Provider: p2p.ProbeProviderNPS}},
			},
		},
		{
			Family: "udp6",
			Nat: p2p.NatObservation{
				NATType:             p2p.NATTypeCone,
				MappingBehavior:     p2p.NATMappingEndpointIndependent,
				ClassificationLevel: p2p.ClassificationConfidenceHigh,
				ProbeEndpointCount:  2,
				Samples:             []p2p.ProbeSample{{Provider: p2p.ProbeProviderNPS}},
			},
		},
	})
	peer := p2p.BuildPeerInfo(common.WORK_P2P_PROVIDER, common.CONN_KCP, "", []p2p.P2PFamilyInfo{
		{
			Family: "udp4",
			Nat:    p2p.NatObservation{NATType: p2p.NATTypePortRestricted},
		},
	})

	model := buildSummaryHintsModel(self, peer)
	if model == nil || model.SharedFamilyCount != 1 || model.DualStackParallel {
		t.Fatalf("unexpected typed summary hints %#v", model)
	}
	if _, ok := model.SelfFamilyDetails["udp4"]; !ok {
		t.Fatalf("typed self_family_details missing udp4: %#v", model.SelfFamilyDetails)
	}
	if _, ok := model.SelfFamilyDetails["udp6"]; !ok {
		t.Fatalf("typed self_family_details missing udp6: %#v", model.SelfFamilyDetails)
	}
}

func TestP2PBridgeSessionTelemetryAggregatesSuccess(t *testing.T) {
	session := &p2pBridgeSession{
		id:          "session-telemetry",
		summarySent: true,
		goSent:      true,
		telemetry:   newP2PSessionTelemetry(time.Now()),
	}
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-telemetry",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "family_ready",
		Status:    "ok",
		LocalAddr: "127.0.0.1:1000",
		Meta: map[string]string{
			"family":                     "udp4",
			"transport_mode":             common.CONN_KCP,
			"candidate_score":            "42",
			"probe_public_ip":            "198.51.100.10",
			"nat_type":                   p2p.NATTypeSymmetric,
			"mapping_behavior":           p2p.NATMappingEndpointDependent,
			"filtering_behavior":         p2p.NATFilteringPortRestricted,
			"classification_level":       p2p.ClassificationConfidenceHigh,
			"probe_endpoint_count":       "3",
			"probe_provider_count":       "2",
			"probe_observed_base_port":   "4000",
			"probe_observed_interval":    "4",
			"probe_port_restricted":      "true",
			"probe_filtering_tested":     "true",
			"mapping_confidence_low":     "true",
			"port_mapping_method":        "pcp",
			"port_mapping_external_addr": "198.51.100.10:45000",
			"port_mapping_internal_addr": "192.168.1.10:4000",
			"port_mapping_lease_seconds": "3600",
		},
		Counters: map[string]int{"total": 2},
	})
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID:  "session-telemetry",
		Role:       common.WORK_P2P_VISITOR,
		Stage:      "transport_established",
		Status:     "ok",
		Detail:     common.CONN_KCP,
		LocalAddr:  "127.0.0.1:1000",
		RemoteAddr: "198.51.100.10:4000",
		Meta:       map[string]string{"family": "udp4"},
	})
	session.handleProgress(common.WORK_P2P_PROVIDER, &p2p.P2PPunchProgress{
		SessionID:  "session-telemetry",
		Role:       common.WORK_P2P_PROVIDER,
		Stage:      "transport_established",
		Status:     "ok",
		Detail:     common.CONN_KCP,
		LocalAddr:  "127.0.0.1:2000",
		RemoteAddr: "198.51.100.20:5000",
		Meta:       map[string]string{"family": "udp4"},
	})

	snapshot := session.telemetrySnapshot()
	if snapshot.Outcome != "transport_established" {
		t.Fatalf("telemetry outcome = %q, want transport_established", snapshot.Outcome)
	}
	visitor := snapshot.Roles[common.WORK_P2P_VISITOR]
	if !visitor.TransportEstablished || visitor.LastFamily != "udp4" {
		t.Fatalf("unexpected visitor telemetry %#v", visitor)
	}
	if visitor.Counters["total"] != 2 {
		t.Fatalf("visitor counters = %#v, want total=2", visitor.Counters)
	}
	if visitor.Stages["family_ready"].Count != 1 {
		t.Fatalf("family_ready stage = %#v", visitor.Stages["family_ready"])
	}
	if visitor.ProbeFamilies["udp4"].PublicIP != "198.51.100.10" || visitor.ProbeFamilies["udp4"].ObservedInterval != 4 {
		t.Fatalf("unexpected probe family telemetry %#v", visitor.ProbeFamilies["udp4"])
	}
	if visitor.PortMappings["udp4"].Method != "pcp" || visitor.PortMappings["udp4"].LeaseSeconds != 3600 {
		t.Fatalf("unexpected port mapping telemetry %#v", visitor.PortMappings["udp4"])
	}
}

func TestP2PBridgeSessionTelemetryKeepsProbeFamiliesSeparate(t *testing.T) {
	session := &p2pBridgeSession{
		id:        "session-probe-families",
		telemetry: newP2PSessionTelemetry(time.Now()),
	}
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-probe-families",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "family_ready",
		Status:    "ok",
		Meta: map[string]string{
			"family":               "udp4",
			"probe_public_ip":      "198.51.100.10",
			"nat_type":             p2p.NATTypeCone,
			"probe_endpoint_count": "2",
		},
	})
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-probe-families",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "family_ready",
		Status:    "ok",
		Meta: map[string]string{
			"family":               "udp6",
			"probe_public_ip":      "2001:db8::10",
			"nat_type":             p2p.NATTypeRestrictedCone,
			"probe_endpoint_count": "1",
		},
	})

	snapshot := session.telemetrySnapshot()
	visitor := snapshot.Roles[common.WORK_P2P_VISITOR]
	if len(visitor.ProbeFamilies) != 2 {
		t.Fatalf("probe families = %#v", visitor.ProbeFamilies)
	}
	if visitor.ProbeFamilies["udp4"].PublicIP != "198.51.100.10" {
		t.Fatalf("udp4 probe telemetry = %#v", visitor.ProbeFamilies["udp4"])
	}
	if visitor.ProbeFamilies["udp6"].PublicIP != "2001:db8::10" {
		t.Fatalf("udp6 probe telemetry = %#v", visitor.ProbeFamilies["udp6"])
	}
}

type recordingP2PSessionTelemetrySink struct {
	records []p2p.P2PSessionTelemetryRecord
}

func (s *recordingP2PSessionTelemetrySink) EmitSessionTelemetry(record p2p.P2PSessionTelemetryRecord) {
	s.records = append(s.records, record)
}

func TestP2PBridgeSessionTelemetrySinkEmitsFinalSnapshot(t *testing.T) {
	sink := &recordingP2PSessionTelemetrySink{}
	restore := setP2PSessionTelemetrySinkForTest(sink)
	t.Cleanup(restore)

	session := &p2pBridgeSession{
		id:          "session-telemetry-sink",
		summarySent: true,
		goSent:      true,
		telemetry:   newP2PSessionTelemetry(time.Now()),
	}
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-telemetry-sink",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.handleProgress(common.WORK_P2P_PROVIDER, &p2p.P2PPunchProgress{
		SessionID: "session-telemetry-sink",
		Role:      common.WORK_P2P_PROVIDER,
		Stage:     "transport_established",
		Status:    "ok",
	})

	if len(sink.records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(sink.records))
	}
	if sink.records[0].SessionID != "session-telemetry-sink" {
		t.Fatalf("sessionID = %q, want session-telemetry-sink", sink.records[0].SessionID)
	}
	if sink.records[0].SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", sink.records[0].SchemaVersion)
	}
	if sink.records[0].Snapshot.Outcome != "transport_established" {
		t.Fatalf("snapshot outcome = %q, want transport_established", sink.records[0].Snapshot.Outcome)
	}
}
