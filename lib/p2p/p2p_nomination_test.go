package p2p

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func TestRuntimeSessionProviderSwitchesToHigherEpochProposal(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	firstConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(first) error = %v", err)
	}
	defer func() { _ = firstConn.Close() }()
	secondConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(second) error = %v", err)
	}
	defer func() { _ = secondConn.Close() }()

	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_PROVIDER,
		},
		plan:      PunchPlan{NominationRetryInterval: 40 * time.Millisecond},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
		accept:    make(map[string]context.CancelFunc),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	end1 := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeEnd)
	end1.NominationEpoch = 1
	raw1, err := EncodeUDPPacket(end1)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(end1) error = %v", err)
	}
	if _, err := firstConn.WriteTo(raw1, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(end1) error = %v", err)
	}
	time.Sleep(80 * time.Millisecond)

	end2 := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeEnd)
	end2.NominationEpoch = 2
	raw2, err := EncodeUDPPacket(end2)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(end2) error = %v", err)
	}
	if _, err := secondConn.WriteTo(raw2, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(end2) error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	if pair := session.cm.NominatedPair(); pair == nil || pair.RemoteAddr != secondConn.LocalAddr().String() {
		t.Fatalf("provider should follow higher epoch proposal, got %#v", pair)
	}

	ready1 := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeReady)
	ready1.NominationEpoch = 1
	ready1Raw, err := EncodeUDPPacket(ready1)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(ready1) error = %v", err)
	}
	if _, err := firstConn.WriteTo(ready1Raw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(ready1) error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	select {
	case pair := <-session.confirmed:
		t.Fatalf("stale READY should not confirm, got %#v", pair)
	default:
	}

	ready2 := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeReady)
	ready2.NominationEpoch = 2
	ready2Raw, err := EncodeUDPPacket(ready2)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(ready2) error = %v", err)
	}
	if _, err := secondConn.WriteTo(ready2Raw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(ready2) error = %v", err)
	}

	select {
	case pair := <-session.confirmed:
		if pair.remoteAddr != secondConn.LocalAddr().String() {
			t.Fatalf("confirmed remote = %q, want %q", pair.remoteAddr, secondConn.LocalAddr().String())
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("provider should confirm after READY for the higher epoch proposal")
	}
}

func TestRuntimeSessionVisitorIgnoresStaleAcceptAfterRenomination(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	firstConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(first) error = %v", err)
	}
	defer func() { _ = firstConn.Close() }()
	secondConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(second) error = %v", err)
	}
	defer func() { _ = secondConn.Close() }()

	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_VISITOR,
		},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}
	session.cm.MarkSucceeded(readConn.LocalAddr().String(), firstConn.LocalAddr().String())
	session.cm.MarkSucceeded(readConn.LocalAddr().String(), secondConn.LocalAddr().String())
	if _, ok := session.cm.AdoptNomination(readConn.LocalAddr().String(), firstConn.LocalAddr().String()); !ok {
		t.Fatal("expected first nomination to be adopted")
	}
	session.setOutboundNomination(readConn.LocalAddr().String(), firstConn.LocalAddr().String(), 1)
	if session.cm.BackoffNomination(readConn.LocalAddr().String(), firstConn.LocalAddr().String(), time.Second) == nil {
		t.Fatal("expected first nomination to back off")
	}
	if _, ok := session.cm.AdoptNomination(readConn.LocalAddr().String(), secondConn.LocalAddr().String()); !ok {
		t.Fatal("expected second nomination to be adopted")
	}
	session.setOutboundNomination(readConn.LocalAddr().String(), secondConn.LocalAddr().String(), 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	staleAccept := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeAccept)
	staleAccept.NominationEpoch = 1
	staleRaw, err := EncodeUDPPacket(staleAccept)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(staleAccept) error = %v", err)
	}
	if _, err := firstConn.WriteTo(staleRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(staleAccept) error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	select {
	case pair := <-session.confirmed:
		t.Fatalf("stale ACCEPT should not confirm, got %#v", pair)
	default:
	}

	accept := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeAccept)
	accept.NominationEpoch = 2
	acceptRaw, err := EncodeUDPPacket(accept)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(accept) error = %v", err)
	}
	if _, err := secondConn.WriteTo(acceptRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(accept) error = %v", err)
	}

	select {
	case pair := <-session.confirmed:
		if pair.remoteAddr != secondConn.LocalAddr().String() {
			t.Fatalf("confirmed remote = %q, want %q", pair.remoteAddr, secondConn.LocalAddr().String())
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("visitor should confirm only the latest nomination epoch")
	}
}

func TestRuntimeSessionDelayedNominationPrefersHigherRankedSuccess(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	betterConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(better) error = %v", err)
	}
	defer func() { _ = betterConn.Close() }()
	worseConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(worse) error = %v", err)
	}
	defer func() { _ = worseConn.Close() }()

	summary := P2PProbeSummary{
		Self: P2PPeerInfo{
			LocalAddrs: []string{readConn.LocalAddr().String()},
		},
		Peer: P2PPeerInfo{
			Nat: NatObservation{
				PublicIP:         "127.0.0.1",
				ObservedBasePort: common.GetPortByAddr(betterConn.LocalAddr().String()),
				Samples: []ProbeSample{
					{ObservedAddr: betterConn.LocalAddr().String()},
					{ObservedAddr: worseConn.LocalAddr().String()},
				},
			},
		},
	}
	plan := PunchPlan{
		NominationDelay:         120 * time.Millisecond,
		NominationRetryInterval: 40 * time.Millisecond,
	}
	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_VISITOR,
		},
		summary:   summary,
		plan:      plan,
		localConn: readConn,
		sockets:   []net.PacketConn{readConn},
		cm:        NewCandidateManager(""),
		ranker:    NewCandidateRanker(summary.Self, summary.Peer, plan),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	worse := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeSucc)
	worseRaw, err := EncodeUDPPacket(worse)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(worse) error = %v", err)
	}
	if _, err := worseConn.WriteTo(worseRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(worse) error = %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	better := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeSucc)
	betterRaw, err := EncodeUDPPacket(better)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(better) error = %v", err)
	}
	if _, err := betterConn.WriteTo(betterRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(better) error = %v", err)
	}
	time.Sleep(220 * time.Millisecond)

	pair := session.cm.NominatedPair()
	if pair == nil {
		t.Fatal("expected delayed nomination to select a candidate")
	}
	if pair.RemoteAddr != betterConn.LocalAddr().String() {
		t.Fatalf("nominated remote = %q, want %q", pair.RemoteAddr, betterConn.LocalAddr().String())
	}
	if !strings.HasPrefix(pair.ScoreReason, "responsive_public(") {
		t.Fatalf("nominated pair score reason = %q, want responsive_public(...)", pair.ScoreReason)
	}
}

func TestCrosstalkPacketDoesNotAffectOtherSession(t *testing.T) {
	sessionA := &runtimeSession{
		start:     P2PPunchStart{SessionID: "session-a", Token: "token-a", Role: common.WORK_P2P_VISITOR},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}
	sessionB := &runtimeSession{
		start:     P2PPunchStart{SessionID: "session-b", Token: "token-b", Role: common.WORK_P2P_VISITOR},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}

	readA, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(readA) error = %v", err)
	}
	defer func() { _ = readA.Close() }()
	readB, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(readB) error = %v", err)
	}
	defer func() { _ = readB.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sessionA.readLoopOnConn(ctx, readA)
	go sessionB.readLoopOnConn(ctx, readB)

	packetA := newUDPPacket("session-a", "token-a", common.WORK_P2P_PROVIDER, packetTypeSucc)
	rawA, err := EncodeUDPPacket(packetA)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(packetA) error = %v", err)
	}
	if _, err := sendConn.WriteTo(rawA, readA.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(readA) error = %v", err)
	}
	if _, err := sendConn.WriteTo(rawA, readB.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(readB) error = %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if !sessionA.cm.HasNominated() {
		t.Fatal("session A should accept its own packet")
	}
	if sessionB.cm.HasNominated() || sessionB.cm.HasConfirmed() || sessionB.endpoints.CandidateRemote != "" {
		t.Fatal("session B should ignore crosstalk packet from session A")
	}
	if stats := sessionB.snapshotStats(); stats.tokenMismatchDropped == 0 {
		t.Fatal("session B should count mismatched crosstalk packet")
	}
}
