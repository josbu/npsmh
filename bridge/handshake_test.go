package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/p2p"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/quic-go/quic-go"
)

func TestIdleTimerClosesAfterIdleWithoutActivity(t *testing.T) {
	closed := make(chan struct{}, 1)
	timer := NewIdleTimer(20*time.Millisecond, func() {
		closed <- struct{}{}
	})
	defer timer.Stop()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle timer did not close after idle timeout")
	}
}

func TestIdleTimerIncDecDefersCloseUntilIdleAgain(t *testing.T) {
	closed := make(chan struct{}, 1)
	timer := NewIdleTimer(25*time.Millisecond, func() {
		closed <- struct{}{}
	})
	defer timer.Stop()

	timer.Inc()
	time.Sleep(60 * time.Millisecond)

	select {
	case <-closed:
		t.Fatal("idle timer closed while activity count was non-zero")
	default:
	}

	timer.Dec()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle timer did not close after activity returned to zero")
	}
}

func TestIdleTimerStopPreventsClose(t *testing.T) {
	var called atomic.Int32
	timer := NewIdleTimer(20*time.Millisecond, func() {
		called.Add(1)
	})
	timer.Stop()

	time.Sleep(80 * time.Millisecond)

	if got := called.Load(); got != 0 {
		t.Fatalf("close callback invoked %d times after Stop, want 0", got)
	}
}

func TestIdleTimerIgnoresIncDecAfterClosed(t *testing.T) {
	var called atomic.Int32
	timer := NewIdleTimer(20*time.Millisecond, func() {
		called.Add(1)
	})

	timer.Stop()
	timer.Inc()
	timer.Dec()
	time.Sleep(80 * time.Millisecond)

	if got := called.Load(); got != 0 {
		t.Fatalf("close callback invoked %d times after Stop+Inc/Dec, want 0", got)
	}
}

func TestIsReplay(t *testing.T) {
	rep.mu.Lock()
	rep.items = map[string]int64{}
	rep.ttl = 300
	rep.mu.Unlock()

	if got := IsReplay("token-1"); got {
		t.Fatal("first key observation should not be replay")
	}
	if got := IsReplay("token-1"); !got {
		t.Fatal("second key observation should be replay")
	}
}

func TestIsReplayEvictsExpiredItems(t *testing.T) {
	now := time.Now().Unix()
	rep.mu.Lock()
	rep.items = map[string]int64{
		"expired": now - 10,
	}
	rep.ttl = 1
	rep.mu.Unlock()

	if got := IsReplay("new-key"); got {
		t.Fatal("new key should not be replay")
	}

	rep.mu.Lock()
	_, hasExpired := rep.items["expired"]
	rep.mu.Unlock()
	if hasExpired {
		t.Fatal("expired entry should be evicted on IsReplay call")
	}
}

func TestBridgeAuthKindForClient(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Runtime.PublicVKey = "public-demo"
	cfg.Runtime.VisitorVKey = "visitor-demo"

	tests := []struct {
		name   string
		client *file.Client
		want   bridgeAuthKind
	}{
		{name: "nil client defaults to control", client: nil, want: bridgeAuthKindControl},
		{name: "regular visible client is control", client: &file.Client{VerifyKey: "client-demo"}, want: bridgeAuthKindControl},
		{name: "visible client matching public key stays control", client: &file.Client{VerifyKey: "public-demo"}, want: bridgeAuthKindControl},
		{name: "public runtime client becomes public auth", client: &file.Client{VerifyKey: "public-demo", NoStore: true, NoDisplay: true}, want: bridgeAuthKindPublic},
		{name: "visitor runtime client becomes visitor auth", client: &file.Client{VerifyKey: "visitor-demo", NoStore: true, NoDisplay: true}, want: bridgeAuthKindVisitor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bridgeAuthKindForRuntimeClient(tt.client, cfg); got != tt.want {
				t.Fatalf("bridgeAuthKindForClient() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBridgeAuthKindForClientAllowsSharedAccessOnlyKey(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	cfg.Runtime.PublicVKey = "shared-access"
	cfg.Runtime.VisitorVKey = "shared-access"

	client := &file.Client{
		VerifyKey: "shared-access",
		NoStore:   true,
		NoDisplay: true,
	}
	if got := bridgeAuthKindForRuntimeClient(client, cfg); got != bridgeAuthKindAccess {
		t.Fatalf("bridgeAuthKindForRuntimeClient(shared access) = %v, want %v", got, bridgeAuthKindAccess)
	}
}

func TestBridgeAuthKindForClientUsesCurrentBridgeConfigRoot(t *testing.T) {
	oldConfigRoot := currentBridgeConfigRoot
	defer func() { currentBridgeConfigRoot = oldConfigRoot }()

	client := &file.Client{
		VerifyKey: "runtime-access",
		NoStore:   true,
		NoDisplay: true,
	}
	first := &servercfg.Snapshot{}
	first.Runtime.PublicVKey = "runtime-access"
	second := &servercfg.Snapshot{}

	current := first
	currentBridgeConfigRoot = func() *servercfg.Snapshot {
		return current
	}

	if got := bridgeAuthKindForClient(client); got != bridgeAuthKindPublic {
		t.Fatalf("bridgeAuthKindForClient(first) = %v, want %v", got, bridgeAuthKindPublic)
	}

	current = second
	if got := bridgeAuthKindForClient(client); got != bridgeAuthKindControl {
		t.Fatalf("bridgeAuthKindForClient(second) = %v, want %v", got, bridgeAuthKindControl)
	}
}

func TestBridgeAuthKindAllowsExpectedFlags(t *testing.T) {
	tests := []struct {
		name string
		kind bridgeAuthKind
		yes  []string
		no   []string
	}{
		{
			name: "control",
			kind: bridgeAuthKindControl,
			yes:  []string{common.WORK_MAIN, common.WORK_CHAN, common.WORK_CONFIG, common.WORK_REGISTER, common.WORK_FILE, common.WORK_P2P_SESSION, common.WORK_VISITOR, common.WORK_SECRET, common.WORK_P2P, common.WORK_P2P_RESOLVE},
			no:   nil,
		},
		{
			name: "public",
			kind: bridgeAuthKindPublic,
			yes:  []string{common.WORK_CONFIG, common.WORK_REGISTER},
			no:   []string{common.WORK_MAIN, common.WORK_CHAN, common.WORK_VISITOR, common.WORK_SECRET, common.WORK_P2P, common.WORK_P2P_SESSION, common.WORK_P2P_RESOLVE},
		},
		{
			name: "visitor",
			kind: bridgeAuthKindVisitor,
			yes:  []string{common.WORK_REGISTER, common.WORK_VISITOR, common.WORK_SECRET, common.WORK_P2P, common.WORK_P2P_RESOLVE},
			no:   []string{common.WORK_MAIN, common.WORK_CHAN, common.WORK_CONFIG, common.WORK_FILE, common.WORK_P2P_SESSION},
		},
		{
			name: "access",
			kind: bridgeAuthKindAccess,
			yes:  []string{common.WORK_CONFIG, common.WORK_REGISTER, common.WORK_VISITOR, common.WORK_SECRET, common.WORK_P2P, common.WORK_P2P_RESOLVE},
			no:   []string{common.WORK_MAIN, common.WORK_CHAN, common.WORK_FILE, common.WORK_P2P_SESSION},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, flag := range tt.yes {
				if !tt.kind.allowsFlag(flag) {
					t.Fatalf("%v should allow %s", tt.kind, flag)
				}
			}
			for _, flag := range tt.no {
				if tt.kind.allowsFlag(flag) {
					t.Fatalf("%v should reject %s", tt.kind, flag)
				}
			}
		})
	}
}

func TestDecodeBridgeRuntimeClientInfoLegacy(t *testing.T) {
	localAddr, mode, err := decodeBridgeRuntimeClientInfo("kcp", 2, common.EncodeIP(net.ParseIP("127.0.0.1")))
	if err != nil {
		t.Fatalf("decodeBridgeRuntimeClientInfo(legacy) error = %v", err)
	}
	if localAddr != "127.0.0.1" {
		t.Fatalf("localAddr = %q, want 127.0.0.1", localAddr)
	}
	if mode != "kcp" {
		t.Fatalf("mode = %q, want kcp", mode)
	}
}

func TestDecodeBridgeRuntimeClientInfoModern(t *testing.T) {
	payload := append(common.EncodeIP(net.ParseIP("127.0.0.1")), byte(len("tls")))
	payload = append(payload, []byte("tls")...)

	localAddr, mode, err := decodeBridgeRuntimeClientInfo("quic", 6, payload)
	if err != nil {
		t.Fatalf("decodeBridgeRuntimeClientInfo(modern) error = %v", err)
	}
	if localAddr != "127.0.0.1" {
		t.Fatalf("localAddr = %q, want 127.0.0.1", localAddr)
	}
	if mode != "quic,tls" {
		t.Fatalf("mode = %q, want quic,tls", mode)
	}
}

func TestDecodeBridgeRuntimeClientInfoRejectsShortModernPayload(t *testing.T) {
	if _, _, err := decodeBridgeRuntimeClientInfo("quic", 6, []byte{1, 2, 3}); err == nil {
		t.Fatal("decodeBridgeRuntimeClientInfo(short modern payload) error = nil, want rejection")
	}
}

func TestReadBridgeRuntimeAuthEnvelopeReadsOrderedBuffers(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	ts := time.Now().Unix()
	payload := append([]byte{}, common.TimestampToBytes(ts)...)
	payload = append(payload, bytes.Repeat([]byte{'k'}, 64)...)
	infoPacket, err := conn.GetLenBytes([]byte("info-payload"))
	if err != nil {
		t.Fatalf("GetLenBytes(info) error = %v", err)
	}
	randPacket, err := conn.GetLenBytes([]byte("rand-payload"))
	if err != nil {
		t.Fatalf("GetLenBytes(rand) error = %v", err)
	}
	payload = append(payload, infoPacket...)
	payload = append(payload, randPacket...)
	payload = append(payload, bytes.Repeat([]byte{'h'}, 32)...)

	done := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(payload)
		done <- err
	}()

	env, err := readBridgeRuntimeAuthEnvelope(conn.NewConn(serverConn))
	if err != nil {
		t.Fatalf("readBridgeRuntimeAuthEnvelope() error = %v", err)
	}
	if env.ts != ts {
		t.Fatalf("env.ts = %d, want %d", env.ts, ts)
	}
	if got := string(env.keyBuf); got != strings.Repeat("k", 64) {
		t.Fatalf("env.keyBuf = %q, want 64 k chars", got)
	}
	if got := string(env.infoBuf); got != "info-payload" {
		t.Fatalf("env.infoBuf = %q, want info-payload", got)
	}
	if got := string(env.randBuf); got != "rand-payload" {
		t.Fatalf("env.randBuf = %q, want rand-payload", got)
	}
	if got := string(env.hmacBuf); got != strings.Repeat("h", 32) {
		t.Fatalf("env.hmacBuf = %q, want 32 h chars", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("clientConn.Write() error = %v", err)
	}
}

func TestValidateBridgeRuntimeTimestampRespectsSecureMode(t *testing.T) {
	oldSecureMode := ServerSecureMode
	oldTTL := rep.ttl
	t.Cleanup(func() {
		ServerSecureMode = oldSecureMode
		rep.ttl = oldTTL
	})

	rep.ttl = 1
	expired := common.TimeNow().Unix() - 10

	ServerSecureMode = false
	if err := validateBridgeRuntimeTimestamp(expired); err != nil {
		t.Fatalf("validateBridgeRuntimeTimestamp(insecure) error = %v, want nil", err)
	}

	ServerSecureMode = true
	if err := validateBridgeRuntimeTimestamp(expired); err == nil {
		t.Fatal("validateBridgeRuntimeTimestamp(secure expired) error = nil, want rejection")
	}
}

func TestVerifyBridgeRuntimeAuthEnvelopeDetectsReplayWhenSecure(t *testing.T) {
	oldSecureMode := ServerSecureMode
	oldItems := rep.items
	oldTTL := rep.ttl
	t.Cleanup(func() {
		ServerSecureMode = oldSecureMode
		rep.items = oldItems
		rep.ttl = oldTTL
	})

	ServerSecureMode = true
	rep.items = map[string]int64{}
	rep.ttl = 300

	client := &file.Client{VerifyKey: "demo-runtime-key"}
	hs := bridgeHandshakeVersion{
		ver:          6,
		minVerRaw:    []byte("0.27.0"),
		clientVerRaw: []byte("v-test"),
	}
	env := bridgeRuntimeAuthEnvelope{
		ts:      common.TimeNow().Unix(),
		infoBuf: []byte("info"),
		randBuf: []byte("rand"),
	}
	env.hmacBuf = crypt.ComputeHMAC(client.VerifyKey, env.ts, hs.minVerRaw, hs.clientVerRaw, env.infoBuf, env.randBuf)

	if err := verifyBridgeRuntimeAuthEnvelope(client, hs, env); err != nil {
		t.Fatalf("verifyBridgeRuntimeAuthEnvelope(first) error = %v", err)
	}
	if err := verifyBridgeRuntimeAuthEnvelope(client, hs, env); err == nil {
		t.Fatal("verifyBridgeRuntimeAuthEnvelope(replay) error = nil, want rejection")
	}
}

func TestResolveBridgeRuntimeUUIDUsesExplicitUUID(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
	if got := resolveBridgeRuntimeUUID(addr, 6, "runtime-node"); got != "runtime-node" {
		t.Fatalf("resolveBridgeRuntimeUUID(explicit) = %q, want runtime-node", got)
	}
}

func TestResolveBridgeRuntimeUUIDFallsBackToRemoteIPForLegacyRuntime(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
	want := crypt.GenerateUUID("127.0.0.1").String()
	if got := resolveBridgeRuntimeUUID(addr, 4, ""); got != want {
		t.Fatalf("resolveBridgeRuntimeUUID(legacy fallback) = %q, want %q", got, want)
	}
}

func TestReadBridgeRuntimeWorkEnvelopeReadsExplicitUUID(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	payload := []byte(common.WORK_P2P)
	uuidPacket, err := conn.GetLenBytes([]byte("provider-node"))
	if err != nil {
		t.Fatalf("GetLenBytes(uuid) error = %v", err)
	}
	randomPacket, err := conn.GetLenBytes([]byte("random-buffer"))
	if err != nil {
		t.Fatalf("GetLenBytes(random) error = %v", err)
	}
	payload = append(payload, uuidPacket...)
	payload = append(payload, randomPacket...)

	done := make(chan error, 1)
	go func() {
		_, err := clientConn.Write(payload)
		done <- err
	}()

	work, ok := readBridgeRuntimeWorkEnvelope(conn.NewConn(serverConn), 6)
	if !ok {
		t.Fatal("readBridgeRuntimeWorkEnvelope() ok = false, want true")
	}
	if work.flag != common.WORK_P2P {
		t.Fatalf("work.flag = %q, want %q", work.flag, common.WORK_P2P)
	}
	if work.uuid != "provider-node" {
		t.Fatalf("work.uuid = %q, want provider-node", work.uuid)
	}
	if err := <-done; err != nil {
		t.Fatalf("clientConn.Write() error = %v", err)
	}
}

func TestCurrentBridgeP2PProviderSignalPrunesNodeWhenSignalMissing(t *testing.T) {
	runtimeBridge := NewTunnel(false, nil, 0)
	node := NewNode("provider-node", "test", 6)
	dbClient := &file.Client{Id: 21}
	task := &file.Tunnel{Client: dbClient}
	runtimeClient := NewClient(dbClient.Id, node)
	runtimeBridge.Client.Store(dbClient.Id, runtimeClient)

	if got := runtimeBridge.currentBridgeP2PProviderSignal(task, node); got != nil {
		t.Fatalf("currentBridgeP2PProviderSignal() = %#v, want nil", got)
	}
	if _, ok := runtimeBridge.loadRuntimeClient(dbClient.Id); ok {
		t.Fatal("runtime client should be removed when the last provider node becomes stale")
	}
}

func TestDispatchBridgeP2PResolveResultWritesBindAndVisitorResult(t *testing.T) {
	oldWriter := bridgeWriteP2PMessage
	t.Cleanup(func() {
		bridgeWriteP2PMessage = oldWriter
	})

	visitorServer, visitorClient := net.Pipe()
	providerServer, providerClient := net.Pipe()
	t.Cleanup(func() { _ = visitorServer.Close() })
	t.Cleanup(func() { _ = visitorClient.Close() })
	t.Cleanup(func() { _ = providerServer.Close() })
	t.Cleanup(func() { _ = providerClient.Close() })

	var gotBind p2p.P2PAssociationBind
	bridgeWriteP2PMessage = func(c *conn.Conn, flag string, payload any) error {
		if flag != common.P2P_ASSOCIATION_BIND {
			t.Fatalf("flag = %q, want %q", flag, common.P2P_ASSOCIATION_BIND)
		}
		bind, ok := payload.(p2p.P2PAssociationBind)
		if !ok {
			t.Fatalf("payload type = %T, want P2PAssociationBind", payload)
		}
		gotBind = bind
		return nil
	}

	target := bridgeP2PResolvedRoute{
		node:   NewNode("provider-node", "test", 6),
		signal: conn.NewConn(providerServer),
		association: p2p.P2PAssociation{
			AssociationID: "assoc-1",
			Visitor:       p2p.P2PPeerRuntime{ClientID: 7, UUID: "visitor-node"},
			Provider:      p2p.P2PPeerRuntime{ClientID: 8, UUID: "provider-node"},
		},
		accessGrant: p2p.P2PAccessPolicy{Mode: p2p.P2PAccessModeWhitelist, Targets: []string{"10.0.0.1:22"}},
		route:       p2p.P2PRouteContext{TunnelID: 33, TunnelMode: "p2p", TargetType: common.CONN_TCP},
		phase:       p2pAssociationPhaseBinding,
		needPunch:   true,
	}

	done := make(chan struct {
		result p2p.P2PResolveResult
		err    error
	}, 1)
	go func() {
		result, err := decodeJSONPayload[p2p.P2PResolveResult](conn.NewConn(visitorClient))
		done <- struct {
			result p2p.P2PResolveResult
			err    error
		}{result: result, err: err}
	}()

	runtimeBridge := NewTunnel(false, nil, 0)
	logMessage, err := runtimeBridge.dispatchBridgeP2PResolveResult(conn.NewConn(visitorServer), target)
	if err != nil {
		t.Fatalf("dispatchBridgeP2PResolveResult() error = %v", err)
	}
	if logMessage != "" {
		t.Fatalf("dispatchBridgeP2PResolveResult() logMessage = %q, want empty", logMessage)
	}

	out := <-done
	if out.err != nil {
		t.Fatalf("decodeJSONPayload(P2PResolveResult) error = %v", out.err)
	}
	if gotBind.Association.AssociationID != target.association.AssociationID {
		t.Fatalf("provider bind association = %q, want %q", gotBind.Association.AssociationID, target.association.AssociationID)
	}
	if out.result.Association.AssociationID != target.association.AssociationID {
		t.Fatalf("visitor resolve association = %q, want %q", out.result.Association.AssociationID, target.association.AssociationID)
	}
	if out.result.Phase != target.phase || !out.result.NeedPunch {
		t.Fatalf("visitor resolve result = %#v, want phase %q needPunch true", out.result, target.phase)
	}
}

func TestDecodeBridgeP2PSessionJoinReadsJSONPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := conn.NewConn(clientConn).SendInfo(p2p.P2PSessionJoin{
			SessionID: "session-join",
			Token:     "token-join",
		}, "")
		done <- err
	}()

	join, err := decodeBridgeP2PSessionJoin(conn.NewConn(serverConn))
	if err != nil {
		t.Fatalf("decodeBridgeP2PSessionJoin() error = %v", err)
	}
	if join.SessionID != "session-join" || join.Token != "token-join" {
		t.Fatalf("join = %#v, want session-join/token-join", join)
	}
	if err := <-done; err != nil {
		t.Fatalf("SendInfo(P2PSessionJoin) error = %v", err)
	}
}

func TestResolveBridgeP2PSessionJoinValidatesProviderAndToken(t *testing.T) {
	runtimeBridge := NewTunnel(false, nil, 0)
	session := &p2pBridgeSession{
		id:         "session-1",
		token:      "token-1",
		providerID: 22,
	}
	runtimeBridge.p2pSessions.sessions.Store(session.id, session)

	got, err := runtimeBridge.resolveBridgeP2PSessionJoin(p2p.P2PSessionJoin{
		SessionID: "session-1",
		Token:     "token-1",
	}, 22)
	if err != nil {
		t.Fatalf("resolveBridgeP2PSessionJoin(valid) error = %v", err)
	}
	if got != session {
		t.Fatalf("resolveBridgeP2PSessionJoin(valid) = %#v, want %#v", got, session)
	}
	if _, err := runtimeBridge.resolveBridgeP2PSessionJoin(p2p.P2PSessionJoin{
		SessionID: "session-1",
		Token:     "wrong",
	}, 22); err == nil {
		t.Fatal("resolveBridgeP2PSessionJoin(wrong token) error = nil, want rejection")
	}
	if _, err := runtimeBridge.resolveBridgeP2PSessionJoin(p2p.P2PSessionJoin{
		SessionID: "session-1",
		Token:     "token-1",
	}, 23); err == nil {
		t.Fatal("resolveBridgeP2PSessionJoin(wrong provider) error = nil, want rejection")
	}
	if _, err := runtimeBridge.resolveBridgeP2PSessionJoin(p2p.P2PSessionJoin{
		SessionID: "missing",
		Token:     "token-1",
	}, 22); err == nil {
		t.Fatal("resolveBridgeP2PSessionJoin(missing session) error = nil, want rejection")
	}
}

func TestCleanupTunnelRuntimeNodeIgnoresStaleTunnelReplacement(t *testing.T) {
	node := NewNode("node-1", "test", 6)
	client := NewClient(1, node)

	oldServer, oldPeer := net.Pipe()
	newServer, newPeer := net.Pipe()
	t.Cleanup(func() { _ = oldPeer.Close() })
	t.Cleanup(func() { _ = newPeer.Close() })
	t.Cleanup(func() { _ = oldServer.Close() })
	t.Cleanup(func() { _ = newServer.Close() })

	oldSignal := conn.NewConn(oldServer)
	newSignal := conn.NewConn(newServer)
	oldTunnel := &struct{ name string }{"old"}
	newTunnel := &struct{ name string }{"new"}

	node.AddSignal(oldSignal)
	node.AddTunnel(oldTunnel)
	node.AddSignal(newSignal)
	node.AddTunnel(newTunnel)

	if removed := NewTunnel(false, nil, 0).cleanupTunnelRuntimeNode(node, client, oldTunnel); removed != 0 {
		t.Fatalf("cleanupTunnelRuntimeNode(stale) removed=%d, want 0", removed)
	}
	if got := node.GetSignal(); got != newSignal {
		t.Fatalf("node current signal = %v, want replacement signal", got)
	}
	if newSignal.IsClosed() {
		t.Fatal("replacement signal should remain open after stale tunnel cleanup")
	}
	if got := node.GetTunnel(); got != newTunnel {
		t.Fatalf("node current tunnel = %v, want replacement tunnel", got)
	}
}

func TestCleanupTunnelRuntimeNodeRemovesEmptyRuntimeClient(t *testing.T) {
	runtimeBridge := NewTunnel(false, &sync.Map{}, 0)
	node := NewNode("solo-node", "test", 6)
	client := NewClient(47, node)
	atomic.StoreInt64(&client.lastConnectNano, time.Now().Add(-2*connectGraceProtectWindow).UnixNano())
	node.joinNano = time.Now().Add(-2 * nodeJoinGraceProtectWindow).UnixNano()
	runtimeBridge.Client.Store(47, client)

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })

	signal := conn.NewConn(serverConn)
	tunnel := &struct{ name string }{"only"}
	node.AddSignal(signal)
	node.AddTunnel(tunnel)

	var closedClientID int
	runtimeBridge.SetCloseClientHook(func(id int) {
		closedClientID = id
	})

	if removed := runtimeBridge.cleanupTunnelRuntimeNode(node, client, tunnel); removed != 1 {
		t.Fatalf("cleanupTunnelRuntimeNode(current) removed=%d, want 1", removed)
	}
	if _, ok := runtimeBridge.loadRuntimeClient(47); ok {
		t.Fatal("empty runtime client should be removed after last tunnel runtime node is cleaned up")
	}
	if closedClientID != 47 {
		t.Fatalf("close client hook = %d, want 47", closedClientID)
	}
}

func TestServeVisitorRuntimeQUICClosesSessionAfterIdle(t *testing.T) {
	crypt.InitTls(tls.Certificate{})

	originalIdle := bridgeVisitorRuntimeIdleTimeout
	bridgeVisitorRuntimeIdleTimeout = 40 * time.Millisecond
	t.Cleanup(func() {
		bridgeVisitorRuntimeIdleTimeout = originalIdle
	})

	listener, err := quic.ListenAddr("127.0.0.1:0", crypt.GetCertCfg(), &quic.Config{})
	if err != nil {
		t.Fatalf("quic.ListenAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	acceptCtx, cancelAccept := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancelAccept)

	serverSessCh := make(chan *quic.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		sess, err := listener.Accept(acceptCtx)
		if err != nil {
			errCh <- err
			return
		}
		serverSessCh <- sess
	}()

	clientSess, err := quic.DialAddr(acceptCtx, listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic.DialAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientSess.CloseWithError(0, "test cleanup")
	})

	clientStream, err := clientSess.OpenStreamSync(acceptCtx)
	if err != nil {
		t.Fatalf("OpenStreamSync() error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientStream.Close()
	})
	if _, err := clientStream.Write([]byte("init")); err != nil {
		t.Fatalf("clientStream.Write() error = %v", err)
	}

	var serverSess *quic.Conn
	select {
	case err := <-errCh:
		t.Fatalf("listener.Accept() error = %v", err)
	case serverSess = <-serverSessCh:
	case <-acceptCtx.Done():
		t.Fatal("timed out waiting for accepted quic session")
	}

	serverStream, err := serverSess.AcceptStream(acceptCtx)
	if err != nil {
		t.Fatalf("AcceptStream() error = %v", err)
	}

	bridgeConn := conn.NewConn(conn.NewQuicAutoCloseConn(serverStream, serverSess))
	done := make(chan struct{})
	b := NewTunnel(false, nil, 0)
	go func() {
		defer close(done)
		b.serveVisitorRuntime(serverSess, bridgeConn, 1, 6, "test", "quic", bridgeAuthKindVisitor, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveVisitorRuntime() should exit after QUIC idle timeout")
	}

	select {
	case <-serverSess.Context().Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server quic session should be closed by idle timeout")
	}
}

func TestHandleSecretWorkEnqueuesSecretWithoutClosingConn(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	b := NewTunnel(false, nil, 0)
	c := conn.NewConn(serverConn)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handleSecretWork(c)
	}()

	if _, err := clientConn.Write([]byte("12345678901234567890123456789012")); err != nil {
		t.Fatalf("clientConn.Write() error = %v", err)
	}

	select {
	case secret := <-b.SecretChan:
		if secret == nil || secret.Password != "12345678901234567890123456789012" {
			t.Fatalf("secret = %+v, want password dispatch", secret)
		}
		if secret.Conn != c {
			t.Fatalf("secret.Conn = %+v, want %+v", secret.Conn, c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleSecretWork() did not enqueue secret")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSecretWork() did not return after enqueue")
	}
}

func TestHandleSecretWorkClosesConnWhenSecretQueueIsFull(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	oldTimeout := bridgeSecretDispatchTimeout
	bridgeSecretDispatchTimeout = 40 * time.Millisecond
	t.Cleanup(func() {
		bridgeSecretDispatchTimeout = oldTimeout
	})

	b := NewTunnel(false, nil, 0)
	b.SecretChan = make(chan *conn.Secret, 1)
	b.SecretChan <- conn.NewSecret("occupied", nil)
	c := conn.NewConn(serverConn)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handleSecretWork(c)
	}()

	if _, err := clientConn.Write([]byte("12345678901234567890123456789012")); err != nil {
		t.Fatalf("clientConn.Write() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSecretWork() should return when secret queue is full")
	}

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := clientConn.Read(buf)
		readDone <- err
	}()

	select {
	case err := <-readDone:
		if err == nil || err == io.ErrNoProgress {
			t.Fatalf("clientConn.Read() error = %v, want closed pipe/EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clientConn should observe closed connection after queue-full drop")
	}

	select {
	case secret := <-b.SecretChan:
		if secret == nil || secret.Password != "occupied" {
			t.Fatalf("queue entry = %+v, want original occupied secret", secret)
		}
	default:
		t.Fatal("full secret queue entry should remain untouched")
	}
}

func TestHandleSecretWorkWaitsForSecretQueueCapacity(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	oldTimeout := bridgeSecretDispatchTimeout
	bridgeSecretDispatchTimeout = 200 * time.Millisecond
	t.Cleanup(func() {
		bridgeSecretDispatchTimeout = oldTimeout
	})

	b := NewTunnel(false, nil, 0)
	b.SecretChan = make(chan *conn.Secret, 1)
	b.SecretChan <- conn.NewSecret("occupied", nil)
	c := conn.NewConn(serverConn)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handleSecretWork(c)
	}()

	if _, err := clientConn.Write([]byte("12345678901234567890123456789012")); err != nil {
		t.Fatalf("clientConn.Write() error = %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("handleSecretWork() returned before queue capacity was released")
	default:
	}

	occupied := <-b.SecretChan
	if occupied == nil || occupied.Password != "occupied" {
		t.Fatalf("occupied secret = %+v, want original entry", occupied)
	}

	select {
	case secret := <-b.SecretChan:
		if secret == nil || secret.Password != "12345678901234567890123456789012" {
			t.Fatalf("secret = %+v, want deferred enqueue", secret)
		}
		if secret.Conn != c {
			t.Fatalf("secret.Conn = %+v, want %+v", secret.Conn, c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleSecretWork() did not enqueue secret after capacity became available")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSecretWork() did not return after deferred enqueue")
	}
}
