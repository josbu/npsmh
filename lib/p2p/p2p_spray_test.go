package p2p

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func resetPredictionHistoryForTest() {
	globalPredictionHistory.mu.Lock()
	defer globalPredictionHistory.mu.Unlock()
	globalPredictionHistory.entries = make(map[string]*predictionHistoryEntry)
}

func resetAdaptiveProfileHistoryForTest() {
	globalAdaptiveProfileHistory.mu.Lock()
	defer globalAdaptiveProfileHistory.mu.Unlock()
	globalAdaptiveProfileHistory.entries = make(map[string]*adaptiveProfileEntry)
}

func TestFilterPunchTargetsForLocalAddrKeepsRuntimeFamilyOnly(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	filtered := filterPunchTargetsForLocalAddr(localConn.LocalAddr(), []string{
		"127.0.0.1:4000",
		"[::1]:4000",
		"127.0.0.1:4000",
		"192.168.1.10:4000",
	})
	if len(filtered) != 2 {
		t.Fatalf("len(filtered) = %d, want 2 (%#v)", len(filtered), filtered)
	}
	if filtered[0] != "127.0.0.1:4000" || filtered[1] != "192.168.1.10:4000" {
		t.Fatalf("unexpected filtered targets %#v", filtered)
	}
}

func TestBasePeriodicSprayDelayBackoffAndCap(t *testing.T) {
	if delay := basePeriodicSprayDelay(0); delay != 1500*time.Millisecond {
		t.Fatalf("attempt 0 delay = %s, want 1500ms", delay)
	}
	if delay := basePeriodicSprayDelay(1); delay != 2100*time.Millisecond {
		t.Fatalf("attempt 1 delay = %s, want 2100ms", delay)
	}
	if delay := basePeriodicSprayDelay(2); delay != 2400*time.Millisecond {
		t.Fatalf("attempt 2 delay = %s, want 2400ms", delay)
	}
	if delay := basePeriodicSprayDelay(6); delay != 4*time.Second {
		t.Fatalf("attempt 6 delay = %s, want capped 4s", delay)
	}
}

func TestSleepContextReturnsEarlyOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	if sleepContext(ctx, 5*time.Second) {
		t.Fatal("sleepContext should stop when context is canceled")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("sleepContext returned too slowly after %s", elapsed)
	}
}

func TestSleepContextHandlesNilContext(t *testing.T) {
	if !sleepContext(nil, 0) {
		t.Fatal("sleepContext(nil, 0) = false, want true")
	}
}

func TestRunPeriodicSprayReturnsImmediatelyWhenNominated(t *testing.T) {
	manager := NewCandidateManager("")
	manager.Observe("0.0.0.0:3000", "1.1.1.1:5002")
	manager.MarkSucceeded("0.0.0.0:3000", "1.1.1.1:5002")
	if _, ok := manager.TryNominate("0.0.0.0:3000", "1.1.1.1:5002"); !ok {
		t.Fatal("expected nomination to succeed")
	}
	session := &runtimeSession{
		cm: manager,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := time.Now()
	session.runPeriodicSpray(ctx, nil, nil)
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("runPeriodicSpray() returned too slowly after nomination: %s", elapsed)
	}
}

func TestResolvePunchTargetsCachesResolvedAddrsAndPreservesIndices(t *testing.T) {
	session := &runtimeSession{}
	localAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4000}
	targets := []string{"127.0.0.1:5000", "bad target", "127.0.0.1:5001"}

	first := session.resolvePunchTargets(localAddr, targets)
	if len(first) != 2 {
		t.Fatalf("len(first) = %d, want 2", len(first))
	}
	if first[0].index != 0 || first[1].index != 2 {
		t.Fatalf("resolved indices = %#v, want [0 2]", []int{first[0].index, first[1].index})
	}
	if len(session.resolvedTargetAddrs) != 2 {
		t.Fatalf("cache size = %d, want 2", len(session.resolvedTargetAddrs))
	}

	second := session.resolvePunchTargets(localAddr, targets)
	if len(second) != len(first) {
		t.Fatalf("len(second) = %d, want %d", len(second), len(first))
	}
	if second[0].addr != first[0].addr || second[1].addr != first[1].addr {
		t.Fatal("resolved target cache should reuse parsed UDP addresses within the session")
	}
}

func TestRuntimeSessionStartSprayEnablesBirthdayFallbackAfterTargetSpray(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()
	targetConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(target) error = %v", err)
	}
	defer func() { _ = targetConn.Close() }()

	session := &runtimeSession{
		start:     P2PPunchStart{SessionID: "session-1", Token: "token-1", Role: common.WORK_P2P_VISITOR},
		summary:   P2PProbeSummary{Self: P2PPeerInfo{LocalAddrs: []string{localConn.LocalAddr().String()}}, Peer: P2PPeerInfo{Nat: NatObservation{PublicIP: "127.0.0.1", ObservedBasePort: common.GetPortByAddr(targetConn.LocalAddr().String()), Samples: []ProbeSample{{ObservedAddr: targetConn.LocalAddr().String()}}}}},
		plan:      PunchPlan{UseTargetSpray: true, AllowBirthdayFallback: true, SprayRounds: 1, SprayBurst: 1, SprayPerPacketSleep: 0, SprayBurstGap: 0, SprayPhaseGap: 0, BirthdayListenPorts: 2, BirthdayTargetsPerPort: 1, PredictionIntervals: []int{1}},
		localConn: localConn,
		sockets:   []net.PacketConn{localConn},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}
	defer session.closeSockets(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	session.startSpray(ctx)

	if !session.plan.UseBirthdayAttack {
		t.Fatal("birthday fallback should be enabled after target spray fails")
	}
	if len(session.snapshotSockets()) < 2 {
		t.Fatalf("birthday fallback should open additional sockets, got %d", len(session.snapshotSockets()))
	}
}

func TestDirectPhaseBurstCapsAtFourPackets(t *testing.T) {
	if got := directPhaseBurst(PunchPlan{SprayBurst: 8}); got != 4 {
		t.Fatalf("directPhaseBurst(8) = %d, want 4", got)
	}
	if got := directPhaseBurst(PunchPlan{SprayBurst: 3}); got != 3 {
		t.Fatalf("directPhaseBurst(3) = %d, want 3", got)
	}
}

func TestRuntimeSessionStartSprayDoesNotEnableBirthdayFallbackWhenDisabled(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()
	targetConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(target) error = %v", err)
	}
	defer func() { _ = targetConn.Close() }()

	session := &runtimeSession{
		start:     P2PPunchStart{SessionID: "session-1", Token: "token-1", Role: common.WORK_P2P_VISITOR},
		summary:   P2PProbeSummary{Self: P2PPeerInfo{LocalAddrs: []string{localConn.LocalAddr().String()}}, Peer: P2PPeerInfo{Nat: NatObservation{PublicIP: "127.0.0.1", ObservedBasePort: common.GetPortByAddr(targetConn.LocalAddr().String()), Samples: []ProbeSample{{ObservedAddr: targetConn.LocalAddr().String()}}}}},
		plan:      PunchPlan{UseTargetSpray: true, AllowBirthdayFallback: false, SprayRounds: 1, SprayBurst: 1, SprayPerPacketSleep: 0, SprayBurstGap: 0, SprayPhaseGap: 0, PredictionIntervals: []int{1}},
		localConn: localConn,
		sockets:   []net.PacketConn{localConn},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}
	defer session.closeSockets(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	session.startSpray(ctx)

	if session.plan.UseBirthdayAttack {
		t.Fatal("birthday fallback should stay disabled when AllowBirthdayFallback=false")
	}
	if len(session.snapshotSockets()) != 1 {
		t.Fatalf("disabled birthday fallback should keep one socket, got %d", len(session.snapshotSockets()))
	}
}
