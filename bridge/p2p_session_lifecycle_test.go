package bridge

import (
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/p2p"
)

func TestReadSessionControlMessageReadsFlagAndPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	want := p2p.P2PPunchReady{
		SessionID: "session-control-envelope",
		Role:      common.WORK_P2P_VISITOR,
	}
	done := make(chan error, 1)
	go func() {
		done <- p2p.WriteBridgeMessage(conn.NewConn(clientConn), common.P2P_PUNCH_READY, want)
	}()

	msg, err := readSessionControlMessage(conn.NewConn(serverConn))
	if err != nil {
		t.Fatalf("readSessionControlMessage() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("WriteBridgeMessage() error = %v", err)
	}
	if msg.flag != common.P2P_PUNCH_READY {
		t.Fatalf("msg.flag = %q, want %q", msg.flag, common.P2P_PUNCH_READY)
	}

	var got p2p.P2PPunchReady
	if err := json.Unmarshal(msg.payload, &got); err != nil {
		t.Fatalf("json.Unmarshal(msg.payload) error = %v", err)
	}
	if got != want {
		t.Fatalf("decoded payload = %#v, want %#v", got, want)
	}
}

func TestP2PBridgeSessionSuccessClosesControlConns(t *testing.T) {
	visitorServer, visitorClient := net.Pipe()
	providerServer, providerClient := net.Pipe()
	defer func() { _ = visitorClient.Close() }()
	defer func() { _ = providerClient.Close() }()
	mgr := newP2PSessionManager(newP2PAssociationManager())

	session := &p2pBridgeSession{
		mgr:             mgr,
		id:              "session-success-close",
		visitorControl:  conn.NewConn(visitorServer),
		providerControl: conn.NewConn(providerServer),
		summarySent:     true,
		goSent:          true,
		timer:           time.NewTimer(time.Minute),
		telemetry:       newP2PSessionTelemetry(time.Now()),
	}
	mgr.sessions.Store(session.id, session)

	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-success-close",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.handleProgress(common.WORK_P2P_PROVIDER, &p2p.P2PPunchProgress{
		SessionID: "session-success-close",
		Role:      common.WORK_P2P_PROVIDER,
		Stage:     "transport_established",
		Status:    "ok",
	})

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		session.mu.Lock()
		closed := session.closed
		visitorControl := session.visitorControl
		providerControl := session.providerControl
		session.mu.Unlock()
		if closed && visitorControl == nil && providerControl == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successful session should release bridge control connections")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := mgr.get(session.id); ok {
		t.Fatal("successful session should be removed from session manager")
	}
}

func TestP2PBridgeSessionAbortPendingUsesClosingRoleState(t *testing.T) {
	session := &p2pBridgeSession{
		id:          "session-abort-pending",
		summarySent: true,
		goSent:      true,
		timer:       time.NewTimer(time.Minute),
		telemetry:   newP2PSessionTelemetry(time.Now()),
	}
	session.telemetry.recordProgress(p2p.P2PPunchProgress{
		SessionID: "session-abort-pending",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})

	session.abortPending(common.WORK_P2P_VISITOR, "visitor control closed")
	session.mu.Lock()
	closedAfterVisitor := session.closed
	session.mu.Unlock()
	if closedAfterVisitor {
		t.Fatal("session should not abort when the already-established role closes its control connection")
	}

	session.abortPending(common.WORK_P2P_PROVIDER, "provider control closed")
	session.mu.Lock()
	closedAfterProvider := session.closed
	session.mu.Unlock()
	if !closedAfterProvider {
		t.Fatal("session should abort when a not-yet-established role loses its control connection")
	}
}

func TestP2PBridgeSessionDoesNotFinalizeTransportBeforeSummaryAndGo(t *testing.T) {
	session := &p2pBridgeSession{
		id:        "session-early-transport",
		timer:     time.NewTimer(time.Minute),
		telemetry: newP2PSessionTelemetry(time.Now()),
	}

	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-early-transport",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.handleProgress(common.WORK_P2P_PROVIDER, &p2p.P2PPunchProgress{
		SessionID: "session-early-transport",
		Role:      common.WORK_P2P_PROVIDER,
		Stage:     "transport_established",
		Status:    "ok",
	})

	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if closed {
		t.Fatal("session should not finalize transport before summary/go are sent")
	}
}

func TestP2PBridgeSessionFinalizesIfTransportWasEstablishedBeforeGo(t *testing.T) {
	visitorServer, visitorClient := net.Pipe()
	providerServer, providerClient := net.Pipe()
	defer func() { _ = visitorServer.Close() }()
	defer func() { _ = visitorClient.Close() }()
	defer func() { _ = providerServer.Close() }()
	defer func() { _ = providerClient.Close() }()

	session := &p2pBridgeSession{
		id:              "session-finalize-after-go",
		token:           "token-finalize-after-go",
		timeouts:        p2p.P2PTimeouts{ProbeTimeoutMs: 1000, HandshakeTimeoutMs: 1000, TransportTimeoutMs: 1000},
		visitorControl:  conn.NewConn(visitorServer),
		providerControl: conn.NewConn(providerServer),
		visitorReport: &p2p.P2PProbeReport{
			SessionID: "session-finalize-after-go",
			Token:     "token-finalize-after-go",
			Role:      common.WORK_P2P_VISITOR,
			PeerRole:  common.WORK_P2P_PROVIDER,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_VISITOR},
		},
		providerReport: &p2p.P2PProbeReport{
			SessionID: "session-finalize-after-go",
			Token:     "token-finalize-after-go",
			Role:      common.WORK_P2P_PROVIDER,
			PeerRole:  common.WORK_P2P_VISITOR,
			Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
		},
		timer:     time.NewTimer(time.Minute),
		telemetry: newP2PSessionTelemetry(time.Now()),
	}

	type result struct {
		err error
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
			if _, err := p2p.ReadBridgeJSON[p2p.P2PPunchGo](cc, common.P2P_PUNCH_GO); err != nil {
				out <- result{err: err}
				return
			}
			out <- result{}
		}()
		return out
	}

	visitorDone := readSide(visitorClient)
	providerDone := readSide(providerClient)

	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-finalize-after-go",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.handleProgress(common.WORK_P2P_PROVIDER, &p2p.P2PPunchProgress{
		SessionID: "session-finalize-after-go",
		Role:      common.WORK_P2P_PROVIDER,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.sendSummary()
	session.handleReady(common.WORK_P2P_VISITOR, &p2p.P2PPunchReady{SessionID: session.id, Role: common.WORK_P2P_VISITOR})
	session.handleReady(common.WORK_P2P_PROVIDER, &p2p.P2PPunchReady{SessionID: session.id, Role: common.WORK_P2P_PROVIDER})

	if visitorResult := <-visitorDone; visitorResult.err != nil {
		t.Fatalf("visitor side read failed: %v", visitorResult.err)
	}
	if providerResult := <-providerDone; providerResult.err != nil {
		t.Fatalf("provider side read failed: %v", providerResult.err)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		session.mu.Lock()
		closed := session.closed
		session.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session should finalize after go once both transports were already established")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestP2PBridgeSessionServeAbortsOnPartialUnknownPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	session := &p2pBridgeSession{
		id:             "session-partial-payload",
		visitorControl: conn.NewConn(serverConn),
		timer:          time.NewTimer(time.Minute),
		telemetry:      newP2PSessionTelemetry(time.Now()),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		session.serve(common.WORK_P2P_VISITOR, session.visitorControl)
	}()

	if _, err := clientConn.Write([]byte("junk")); err != nil {
		t.Fatalf("Write(junk flag) error = %v", err)
	}
	_ = clientConn.Close()

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		session.mu.Lock()
		closed := session.closed
		session.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial control payload should abort the pending session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("serve loop should exit after partial payload abort")
	}
}

func TestP2PBridgeSessionServeAbortsOnPartialProgressPayload(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()

	session := &p2pBridgeSession{
		id:             "session-partial-progress",
		visitorControl: conn.NewConn(serverConn),
		timer:          time.NewTimer(time.Minute),
		telemetry:      newP2PSessionTelemetry(time.Now()),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		session.serve(common.WORK_P2P_VISITOR, session.visitorControl)
	}()

	if _, err := clientConn.Write([]byte(common.P2P_PUNCH_PROGRESS)); err != nil {
		t.Fatalf("Write(progress flag) error = %v", err)
	}
	_ = clientConn.Close()

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		session.mu.Lock()
		closed := session.closed
		session.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial progress payload should abort the pending session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("serve loop should exit after partial progress abort")
	}
}

func TestP2PSessionManagerGetRejectsClosedSession(t *testing.T) {
	mgr := newP2PSessionManager(newP2PAssociationManager())
	session := &p2pBridgeSession{id: "session-closed", closed: true}
	mgr.sessions.Store(session.id, session)

	if _, ok := mgr.get(session.id); ok {
		t.Fatal("get() should reject closed sessions")
	}
	if _, exists := mgr.sessions.Load(session.id); exists {
		t.Fatal("closed session should be evicted from the session manager")
	}
}

func TestP2PBridgeSessionAbortDoesNotRemoveReplacementSession(t *testing.T) {
	mgr := newP2PSessionManager(newP2PAssociationManager())
	stale := &p2pBridgeSession{
		mgr:       mgr,
		id:        "session-replacement-abort",
		timer:     time.NewTimer(time.Minute),
		telemetry: newP2PSessionTelemetry(time.Now()),
	}
	replacement := &p2pBridgeSession{
		mgr: mgr,
		id:  stale.id,
	}
	mgr.sessions.Store(stale.id, replacement)

	stale.abort("stale abort")

	current, ok := mgr.sessions.Load(stale.id)
	if !ok || current != replacement {
		t.Fatal("stale abort should not remove replacement session")
	}
}

func TestP2PBridgeSessionFinishTransportEstablishedDoesNotRemoveReplacementSession(t *testing.T) {
	mgr := newP2PSessionManager(newP2PAssociationManager())
	stale := &p2pBridgeSession{
		mgr:       mgr,
		id:        "session-replacement-finish",
		timer:     time.NewTimer(time.Minute),
		telemetry: newP2PSessionTelemetry(time.Now()),
	}
	replacement := &p2pBridgeSession{
		mgr: mgr,
		id:  stale.id,
	}
	mgr.sessions.Store(stale.id, replacement)

	stale.finishTransportEstablished()

	current, ok := mgr.sessions.Load(stale.id)
	if !ok || current != replacement {
		t.Fatal("stale finish should not remove replacement session")
	}
}

func TestP2PBridgeSessionAttachProviderRejectsDuplicateOrClosed(t *testing.T) {
	session := &p2pBridgeSession{}
	first := &conn.Conn{}
	second := &conn.Conn{}
	if !session.attachProvider(first) {
		t.Fatal("first provider attach should succeed")
	}
	if session.attachProvider(second) {
		t.Fatal("duplicate provider attach should be rejected")
	}
	session.closed = true
	if session.attachProvider(first) {
		t.Fatal("closed session should reject provider attach")
	}
}

func TestP2PSessionManagerCreateRejectsMissingTaskOrVisitorControl(t *testing.T) {
	mgr := newP2PSessionManager(newP2PAssociationManager())
	probe := p2p.P2PProbeConfig{
		Endpoints: []p2p.P2PProbeEndpoint{{
			Provider: p2p.ProbeProviderNPS,
			Mode:     p2p.ProbeModeUDP,
			Network:  p2p.ProbeNetworkUDP,
			Address:  "127.0.0.1:23001",
		}},
	}

	if _, err := mgr.create(1, 2, nil, &conn.Conn{}, probe, probe, p2p.P2PAssociation{AssociationID: "assoc-1"}, p2p.P2PAccessPolicy{}, p2p.P2PRouteContext{}); err == nil {
		t.Fatal("create() should reject missing task")
	}
	if _, err := mgr.create(1, 2, &file.Tunnel{Password: "demo"}, nil, probe, probe, p2p.P2PAssociation{AssociationID: "assoc-1"}, p2p.P2PAccessPolicy{}, p2p.P2PRouteContext{}); err == nil {
		t.Fatal("create() should reject missing visitor control")
	}
}

func TestP2PBridgeSessionAttachProviderRejectsNilControl(t *testing.T) {
	session := &p2pBridgeSession{}
	if session.attachProvider(nil) {
		t.Fatal("attachProvider() should reject nil control")
	}
}

func TestP2PBridgeSessionPrepareGoDispatchRequiresBothControls(t *testing.T) {
	visitorServer, visitorClient := net.Pipe()
	defer func() { _ = visitorServer.Close() }()
	defer func() { _ = visitorClient.Close() }()

	session := &p2pBridgeSession{
		id:             "session-go-controls",
		token:          "token-go-controls",
		visitorControl: conn.NewConn(visitorServer),
		summarySent:    true,
		visitorReady:   true,
		providerReady:  true,
		timeouts:       p2p.P2PTimeouts{HandshakeTimeoutMs: 1000, TransportTimeoutMs: 1000},
		timer:          time.NewTimer(time.Minute),
		telemetry:      newP2PSessionTelemetry(time.Now()),
	}
	defer session.timer.Stop()

	if _, ok := session.prepareGoDispatch(); ok {
		t.Fatal("prepareGoDispatch() should reject missing provider control")
	}
	if session.goSent {
		t.Fatal("prepareGoDispatch() should not mark goSent when provider control is missing")
	}
}

func TestPunchGoDelayMsAdaptsToSummaryHints(t *testing.T) {
	easyDelay := punchGoDelayMs(p2p.P2PTimeouts{HandshakeTimeoutMs: 2000}, &p2p.P2PSummaryHints{
		SharedFamilyCount:      2,
		SelfFilteringTested:    true,
		PeerFilteringTested:    true,
		SelfProbeEndpointCount: 2,
		PeerProbeEndpointCount: 2,
	})
	hardDelay := punchGoDelayMs(p2p.P2PTimeouts{HandshakeTimeoutMs: 2000}, &p2p.P2PSummaryHints{
		MappingConfidenceLow:   true,
		ProbePortRestricted:    true,
		SelfFilteringTested:    false,
		PeerFilteringTested:    true,
		SelfProbeEndpointCount: 1,
		PeerProbeEndpointCount: 1,
	})
	if easyDelay >= hardDelay {
		t.Fatalf("easy delay %d should be lower than hard delay %d", easyDelay, hardDelay)
	}
	if easyDelay < 40 || hardDelay < easyDelay {
		t.Fatalf("unexpected delays easy=%d hard=%d", easyDelay, hardDelay)
	}
}

func TestPostSummaryTTLUsesHandshakeAndTransportBudget(t *testing.T) {
	if got := postSummaryTTL(p2p.P2PTimeouts{}); got != 20*time.Second {
		t.Fatalf("postSummaryTTL(zero) = %s, want 20s", got)
	}
	if got := postSummaryTTL(p2p.P2PTimeouts{HandshakeTimeoutMs: 50, TransportTimeoutMs: 50}); got != 1100*time.Millisecond {
		t.Fatalf("postSummaryTTL(50ms,50ms) = %s, want 1100ms", got)
	}
	if got := postSummaryTTL(p2p.P2PTimeouts{HandshakeTimeoutMs: 0, TransportTimeoutMs: -800}); got != 20*time.Second {
		t.Fatalf("postSummaryTTL(invalid) = %s, want 20s", got)
	}
}

func TestP2PBridgeSessionHandleProgressIgnoresMismatchedSessionOrRole(t *testing.T) {
	session := &p2pBridgeSession{
		id:          "session-progress-guard",
		summarySent: true,
		goSent:      true,
		timer:       time.NewTimer(time.Minute),
		telemetry:   newP2PSessionTelemetry(time.Now()),
	}

	session.handleProgress(common.WORK_P2P_PROVIDER, &p2p.P2PPunchProgress{
		SessionID: "session-progress-guard",
		Role:      common.WORK_P2P_PROVIDER,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "foreign-session",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})
	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-progress-guard",
		Role:      common.WORK_P2P_PROVIDER,
		Stage:     "transport_established",
		Status:    "ok",
	})

	session.mu.Lock()
	closedBeforeMatch := session.closed
	visitorEstablished := session.telemetry.roleTransportEstablished(common.WORK_P2P_VISITOR)
	session.mu.Unlock()
	if closedBeforeMatch {
		t.Fatal("mismatched progress should not finalize the session")
	}
	if visitorEstablished {
		t.Fatal("mismatched progress should not record visitor transport success")
	}

	session.handleProgress(common.WORK_P2P_VISITOR, &p2p.P2PPunchProgress{
		SessionID: "session-progress-guard",
		Role:      common.WORK_P2P_VISITOR,
		Stage:     "transport_established",
		Status:    "ok",
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.closed {
		t.Fatal("matching progress should finalize once both roles are established")
	}
	if !session.telemetry.roleTransportEstablished(common.WORK_P2P_VISITOR) {
		t.Fatal("matching progress should record visitor transport success")
	}
}

func TestP2PBridgeSessionHandleReportIgnoresMismatchedRole(t *testing.T) {
	session := &p2pBridgeSession{
		id:        "session-report-guard",
		token:     "token-report-guard",
		timeouts:  p2p.P2PTimeouts{HandshakeTimeoutMs: 1000, TransportTimeoutMs: 1000},
		timer:     time.NewTimer(time.Minute),
		telemetry: newP2PSessionTelemetry(time.Now()),
	}

	session.handleReport(common.WORK_P2P_PROVIDER, &p2p.P2PProbeReport{
		SessionID: "session-report-guard",
		Token:     "token-report-guard",
		Role:      common.WORK_P2P_PROVIDER,
		PeerRole:  common.WORK_P2P_VISITOR,
		Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
	})
	session.handleReport(common.WORK_P2P_VISITOR, &p2p.P2PProbeReport{
		SessionID: "session-report-guard",
		Token:     "token-report-guard",
		Role:      common.WORK_P2P_PROVIDER,
		PeerRole:  common.WORK_P2P_VISITOR,
		Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_PROVIDER},
	})

	session.mu.Lock()
	visitorReport := session.visitorReport
	summarySentBeforeMatch := session.summarySent
	session.mu.Unlock()
	if visitorReport != nil {
		t.Fatal("mismatched report should not populate the visitor report")
	}
	if summarySentBeforeMatch {
		t.Fatal("mismatched report should not trigger summary send")
	}

	session.handleReport(common.WORK_P2P_VISITOR, &p2p.P2PProbeReport{
		SessionID: "session-report-guard",
		Token:     "token-report-guard",
		Role:      common.WORK_P2P_VISITOR,
		PeerRole:  common.WORK_P2P_PROVIDER,
		Self:      p2p.P2PPeerInfo{Role: common.WORK_P2P_VISITOR},
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.visitorReport == nil {
		t.Fatal("matching report should populate the visitor report")
	}
	if !session.summarySent {
		t.Fatal("matching visitor/provider reports should trigger summary send")
	}
}

func TestP2PBridgeSessionHandleReadyIgnoresMismatchedRole(t *testing.T) {
	visitorServer, visitorClient := net.Pipe()
	defer func() { _ = visitorServer.Close() }()
	defer func() { _ = visitorClient.Close() }()
	providerServer, providerClient := net.Pipe()
	defer func() { _ = providerServer.Close() }()
	defer func() { _ = providerClient.Close() }()

	go func() { _, _ = io.Copy(io.Discard, visitorClient) }()
	go func() { _, _ = io.Copy(io.Discard, providerClient) }()

	session := &p2pBridgeSession{
		visitorControl:  conn.NewConn(visitorServer),
		providerControl: conn.NewConn(providerServer),
		id:              "session-ready-guard",
		summarySent:     true,
		timer:           time.NewTimer(time.Minute),
		telemetry:       newP2PSessionTelemetry(time.Now()),
	}

	session.handleReady(common.WORK_P2P_VISITOR, &p2p.P2PPunchReady{
		SessionID: "session-ready-guard",
		Role:      common.WORK_P2P_PROVIDER,
	})
	session.handleReady(common.WORK_P2P_PROVIDER, &p2p.P2PPunchReady{
		SessionID: "session-ready-guard",
		Role:      common.WORK_P2P_PROVIDER,
	})

	session.mu.Lock()
	visitorReadyBeforeMatch := session.visitorReady
	goSentBeforeMatch := session.goSent
	session.mu.Unlock()
	if visitorReadyBeforeMatch {
		t.Fatal("mismatched ready should not mark the visitor as ready")
	}
	if goSentBeforeMatch {
		t.Fatal("mismatched ready should not trigger punch go")
	}

	session.handleReady(common.WORK_P2P_VISITOR, &p2p.P2PPunchReady{
		SessionID: "session-ready-guard",
		Role:      common.WORK_P2P_VISITOR,
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.visitorReady {
		t.Fatal("matching ready should mark the visitor as ready")
	}
	if !session.providerReady {
		t.Fatal("matching provider ready should be preserved")
	}
	if !session.goSent {
		t.Fatal("matching ready should trigger punch go once both roles are ready")
	}
}

func TestP2PBridgeSessionHandleAbortIgnoresMismatchedSessionOrRole(t *testing.T) {
	session := &p2pBridgeSession{
		id:        "session-abort-guard",
		timer:     time.NewTimer(time.Minute),
		telemetry: newP2PSessionTelemetry(time.Now()),
	}

	session.handleAbort(common.WORK_P2P_VISITOR, &p2p.P2PPunchAbort{
		SessionID: "foreign-session",
		Role:      common.WORK_P2P_VISITOR,
		Reason:    "foreign abort",
	})
	session.handleAbort(common.WORK_P2P_VISITOR, &p2p.P2PPunchAbort{
		SessionID: "session-abort-guard",
		Role:      common.WORK_P2P_PROVIDER,
		Reason:    "wrong role abort",
	})

	session.mu.Lock()
	closedBeforeMatch := session.closed
	session.mu.Unlock()
	if closedBeforeMatch {
		t.Fatal("mismatched abort should not close the session")
	}

	session.handleAbort(common.WORK_P2P_VISITOR, &p2p.P2PPunchAbort{
		SessionID: "session-abort-guard",
		Role:      common.WORK_P2P_VISITOR,
		Reason:    "expected abort",
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.closed {
		t.Fatal("matching abort should close the session")
	}
}
