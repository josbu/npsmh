package p2p

import (
	"net"
	"testing"
)

func TestNewRuntimeSessionStartsInCreatedPhase(t *testing.T) {
	session := newRuntimeSession(
		P2PPunchStart{SessionID: "session-a", Role: "visitor"},
		P2PProbeSummary{},
		PunchPlan{},
		nil,
		&testPacketConn{localAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4000}},
		make(chan confirmedPair, 1),
	)

	if got := session.currentPhase(); got != sessionPhaseCreated {
		t.Fatalf("currentPhase() = %q, want %q", got, sessionPhaseCreated)
	}
	transitions := session.snapshotPhaseTransitions()
	if len(transitions) != 1 {
		t.Fatalf("len(snapshotPhaseTransitions()) = %d, want 1", len(transitions))
	}
	if transitions[0].To != sessionPhaseCreated || transitions[0].Event != sessionEventSessionCreated {
		t.Fatalf("unexpected initial transition %#v", transitions[0])
	}
}

func TestRuntimeSessionPhaseTransitionsStayMonotonic(t *testing.T) {
	session := &runtimeSession{}
	if !session.transitionPhase(sessionEventSessionCreated, sessionPhaseCreated) {
		t.Fatal("created transition should succeed")
	}
	if !session.transitionPhase(sessionEventReadySent, sessionPhaseReadySent) {
		t.Fatal("ready transition should succeed")
	}
	if !session.transitionPhase(sessionEventPunchGo, sessionPhasePunching) {
		t.Fatal("punching transition should succeed")
	}
	if session.transitionPhase(sessionEventReadySent, sessionPhaseReadySent) {
		t.Fatal("duplicate transition should be ignored")
	}
	if session.transitionPhase(sessionEventSessionCreated, sessionPhaseCreated) {
		t.Fatal("backward transition should be rejected")
	}
	if got := session.currentPhase(); got != sessionPhasePunching {
		t.Fatalf("currentPhase() = %q, want %q", got, sessionPhasePunching)
	}
	transitions := session.snapshotPhaseTransitions()
	if len(transitions) != 3 {
		t.Fatalf("len(snapshotPhaseTransitions()) = %d, want 3", len(transitions))
	}
}

func TestRuntimeSessionFailurePhaseIsTerminal(t *testing.T) {
	session := &runtimeSession{}
	session.transitionPhase(sessionEventSessionCreated, sessionPhaseCreated)
	if !session.transitionPhase(sessionEventHandshakeTimeout, sessionPhaseFailed) {
		t.Fatal("failed transition should succeed")
	}
	if session.transitionPhase(sessionEventHandoverBegin, sessionPhaseHandover) {
		t.Fatal("handover should not be allowed after failure")
	}
	if got := session.currentPhase(); got != sessionPhaseFailed {
		t.Fatalf("currentPhase() = %q, want %q", got, sessionPhaseFailed)
	}
}

func TestRuntimeSessionCanRecordSamePhaseEvents(t *testing.T) {
	session := &runtimeSession{}
	session.transitionPhase(sessionEventSessionCreated, sessionPhaseCreated)
	session.transitionPhase(sessionEventPunchGo, sessionPhasePunching)
	session.transitionPhase(sessionEventCandidateHit, sessionPhaseNegotiating)

	if !session.recordPhaseEvent(sessionEventAcceptReceived) {
		t.Fatal("same-phase event should be recorded")
	}
	if got := session.currentPhase(); got != sessionPhaseNegotiating {
		t.Fatalf("currentPhase() = %q, want %q", got, sessionPhaseNegotiating)
	}
	phase, event := session.phaseMeta()
	if phase != string(sessionPhaseNegotiating) {
		t.Fatalf("phaseMeta phase = %q, want %q", phase, sessionPhaseNegotiating)
	}
	if event != string(sessionEventAcceptReceived) {
		t.Fatalf("phaseMeta event = %q, want %q", event, sessionEventAcceptReceived)
	}
	transitions := session.snapshotPhaseTransitions()
	last := transitions[len(transitions)-1]
	if last.From != sessionPhaseNegotiating || last.To != sessionPhaseNegotiating || last.Event != sessionEventAcceptReceived {
		t.Fatalf("unexpected same-phase transition %#v", last)
	}
}

func TestRuntimeSessionDispatchSessionEventUsesCentralSpec(t *testing.T) {
	session := &runtimeSession{}
	if !session.dispatchSessionEvent(sessionEventSessionCreated) {
		t.Fatal("session_created dispatch should succeed")
	}
	if !session.dispatchSessionEvent(sessionEventReadySent) {
		t.Fatal("ready_sent dispatch should succeed")
	}
	if !session.dispatchSessionEvent(sessionEventPunchGo) {
		t.Fatal("punch_go dispatch should succeed")
	}
	if !session.dispatchSessionEvent(sessionEventCandidateHit) {
		t.Fatal("candidate_hit dispatch should succeed")
	}
	if !session.dispatchSessionEvent(sessionEventAcceptReceived) {
		t.Fatal("accept_received dispatch should succeed as same-phase event")
	}
	if got := session.currentPhase(); got != sessionPhaseNegotiating {
		t.Fatalf("currentPhase() = %q, want %q", got, sessionPhaseNegotiating)
	}
	phase, event := session.phaseMeta()
	if phase != string(sessionPhaseNegotiating) || event != string(sessionEventAcceptReceived) {
		t.Fatalf("phaseMeta() = (%q, %q), want (%q, %q)", phase, event, sessionPhaseNegotiating, sessionEventAcceptReceived)
	}
}

func TestRuntimeSessionDroppedEventsAreCounted(t *testing.T) {
	session := &runtimeSession{
		cm: NewCandidateManager(""),
	}
	if !session.dispatchSessionEvent(sessionEventSessionCreated) {
		t.Fatal("session_created dispatch should succeed")
	}
	if !session.dispatchSessionEvent(sessionEventHandshakeTimeout) {
		t.Fatal("handshake_timeout dispatch should succeed")
	}
	if session.dispatchSessionEvent(sessionEventReadyReceived) {
		t.Fatal("ready_received should be rejected after failure")
	}
	if session.dispatchSessionEvent(sessionEvent("unknown")) {
		t.Fatal("unknown session event should be rejected")
	}
	stats := session.snapshotStats()
	if stats.sessionEventDropped != 2 {
		t.Fatalf("sessionEventDropped = %d, want 2", stats.sessionEventDropped)
	}
	if stats.lastDroppedEvent != sessionEvent("unknown") {
		t.Fatalf("lastDroppedEvent = %q, want %q", stats.lastDroppedEvent, sessionEvent("unknown"))
	}
	counters := session.progressCounters()
	if counters["session_event_dropped"] != 2 {
		t.Fatalf("session_event_dropped counter = %d, want 2", counters["session_event_dropped"])
	}
}

func TestSignalConfirmedAdvancesSessionPhase(t *testing.T) {
	session := &runtimeSession{
		confirmed: make(chan confirmedPair, 1),
	}
	session.transitionPhase(sessionEventSessionCreated, sessionPhaseCreated)
	session.transitionPhase(sessionEventPunchGo, sessionPhasePunching)
	session.signalConfirmed(nil, "127.0.0.1:4000", "198.51.100.10:5000")

	if got := session.currentPhase(); got != sessionPhaseConfirmed {
		t.Fatalf("currentPhase() = %q, want %q", got, sessionPhaseConfirmed)
	}
	select {
	case pair := <-session.confirmed:
		if pair.localAddr != "127.0.0.1:4000" || pair.remoteAddr != "198.51.100.10:5000" {
			t.Fatalf("confirmed pair = %#v", pair)
		}
	default:
		t.Fatal("signalConfirmed should publish the confirmed pair")
	}
}

func TestEnrichProgressMetaIncludesSessionPhaseAndEvent(t *testing.T) {
	session := &runtimeSession{
		localConn: &testPacketConn{localAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4000}},
		cm:        NewCandidateManager("198.51.100.10:5000"),
	}
	session.markSessionCreated()
	session.markPunching()

	meta := session.enrichProgressMeta("127.0.0.1:4000", "198.51.100.10:5000", map[string]string{"custom": "1"})
	if meta["session_phase"] != string(sessionPhasePunching) {
		t.Fatalf("session_phase = %q, want %q", meta["session_phase"], sessionPhasePunching)
	}
	if meta["session_event"] != string(sessionEventPunchGo) {
		t.Fatalf("session_event = %q, want %q", meta["session_event"], sessionEventPunchGo)
	}
}
