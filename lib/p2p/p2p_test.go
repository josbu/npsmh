package p2p

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func waitForProviderNominationBeforeReady(t *testing.T, session *runtimeSession, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case pair := <-session.confirmed:
			t.Fatalf("provider should not handover before READY, got %#v", pair)
		case <-deadline:
			t.Fatal("provider should keep a nominated pair after END")
		case <-ticker.C:
			if session.cm.HasConfirmed() {
				t.Fatal("provider should not mark pair confirmed before READY")
			}
			if session.cm.HasNominated() {
				return
			}
		}
	}
}

func TestWorkerForConfirmedPairMatchesAdditionalSocketOwner(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()
	extraConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(extra) error = %v", err)
	}
	defer func() { _ = extraConn.Close() }()

	rs := &runtimeSession{
		localConn: localConn,
		sockets:   []net.PacketConn{localConn, extraConn},
	}
	worker := &runtimeFamilyWorker{family: udpFamilyV4, rs: rs}
	pair := confirmedPair{
		owner:      rs,
		conn:       extraConn,
		localAddr:  extraConn.LocalAddr().String(),
		remoteAddr: "1.1.1.1:5000",
	}
	if got := workerForConfirmedPair([]*runtimeFamilyWorker{worker}, pair); got != worker {
		t.Fatalf("workerForConfirmedPair() = %#v, want owner worker", got)
	}
}

func TestRuntimeSessionKeepsConfirmedRemoteStable(t *testing.T) {
	session := &runtimeSession{}
	session.updateCandidateRemote("1.1.1.1:5002")
	session.updateConfirmedRemote("1.1.1.1:5002")
	session.updateCandidateRemote("1.1.1.1:5010")
	if session.endpoints.CandidateRemote != "1.1.1.1:5002" {
		t.Fatalf("CandidateRemote changed to %q", session.endpoints.CandidateRemote)
	}
	if session.endpoints.ConfirmedRemote != "1.1.1.1:5002" {
		t.Fatalf("ConfirmedRemote changed to %q", session.endpoints.ConfirmedRemote)
	}
}

func TestRuntimeSessionDropsWrongTokenSilently(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	wrong := newUDPPacket("session-1", "wrong-token", common.WORK_P2P_PROVIDER, packetTypeSucc)
	wrongRaw, err := EncodeUDPPacket(wrong)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(wrong) error = %v", err)
	}
	if _, err := sendConn.WriteTo(wrongRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(wrong) error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if session.cm.HasNominated() || session.cm.HasConfirmed() || session.endpoints.CandidateRemote != "" {
		t.Fatal("wrong-token packet should be silently dropped")
	}
	if stats := session.snapshotStats(); stats.tokenMismatchDropped == 0 {
		t.Fatal("wrong-token packet should increase mismatch counter")
	}

	right := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeSucc)
	rightRaw, err := EncodeUDPPacket(right)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(right) error = %v", err)
	}
	if _, err := sendConn.WriteTo(rightRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(right) error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	pair := session.cm.NominatedPair()
	if pair == nil {
		t.Fatal("valid packet should still progress handshake after wrong-token packet")
	}
	if pair.RemoteAddr != sendConn.LocalAddr().String() {
		t.Fatalf("nominated remote = %q, want %q", pair.RemoteAddr, sendConn.LocalAddr().String())
	}
	if stats := session.snapshotStats(); !stats.tokenVerified {
		t.Fatal("valid packet should mark token verified")
	}
}

func TestRuntimeSessionDropsWrongWireRouteSilently(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Wire:      P2PWireSpec{RouteID: "route-a"},
			Role:      common.WORK_P2P_VISITOR,
		},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	wrongRoute := newUDPPacketWithWire("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeSucc, P2PWireSpec{RouteID: "route-b"})
	wrongRouteRaw, err := EncodeUDPPacket(wrongRoute)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(wrongRoute) error = %v", err)
	}
	if _, err := sendConn.WriteTo(wrongRouteRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(wrongRoute) error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	if session.cm.HasNominated() || session.cm.HasConfirmed() || session.endpoints.CandidateRemote != "" {
		t.Fatal("wrong-route packet should be silently dropped")
	}
	if stats := session.snapshotStats(); stats.tokenMismatchDropped == 0 {
		t.Fatal("wrong-route packet should increase mismatch counter")
	}
}

func TestRuntimeSessionDropsReplaySilently(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_PROVIDER,
		},
		cm:        NewCandidateManager(""),
		replay:    NewReplayWindow(30 * time.Second),
		confirmed: make(chan confirmedPair, 1),
		nominate:  make(map[string]context.CancelFunc),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	packet := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeProbe)
	raw, err := EncodeUDPPacket(packet)
	if err != nil {
		t.Fatalf("EncodeUDPPacket() error = %v", err)
	}
	if _, err := sendConn.WriteTo(raw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(first) error = %v", err)
	}
	if _, err := sendConn.WriteTo(raw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(replay) error = %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if stats := session.snapshotStats(); stats.replayDropped == 0 {
		t.Fatal("replayed packet should increase replay counter")
	}
}

func TestRuntimeSessionDoesNotHandoverBeforeConfirm(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	success := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeSucc)
	raw, err := EncodeUDPPacket(success)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(success) error = %v", err)
	}
	if _, err := sendConn.WriteTo(raw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(success) error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	select {
	case pair := <-session.confirmed:
		t.Fatalf("session should not handover before confirm, got %#v", pair)
	default:
	}
	if session.cm.HasConfirmed() {
		t.Fatal("confirmed pair should stay empty before ACCEPT")
	}
}

func TestRuntimeSessionProviderWaitsForReadyBeforeHandover(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

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

	end := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeEnd)
	end.NominationEpoch = 1
	endRaw, err := EncodeUDPPacket(end)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(end) error = %v", err)
	}
	if _, err := sendConn.WriteTo(endRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(end) error = %v", err)
	}
	waitForProviderNominationBeforeReady(t, session, 600*time.Millisecond)

	ready := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeReady)
	ready.NominationEpoch = 1
	readyRaw, err := EncodeUDPPacket(ready)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(ready) error = %v", err)
	}
	if _, err := sendConn.WriteTo(readyRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(ready) error = %v", err)
	}

	select {
	case pair := <-session.confirmed:
		if pair.remoteAddr != sendConn.LocalAddr().String() {
			t.Fatalf("confirmed remote = %q, want %q", pair.remoteAddr, sendConn.LocalAddr().String())
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("provider should handover after READY")
	}
}

func TestRuntimeSessionProviderIgnoresDuplicateReadyAfterConfirm(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_PROVIDER,
		},
		plan:      PunchPlan{NominationRetryInterval: 40 * time.Millisecond},
		cm:        NewCandidateManager(""),
		confirmed: make(chan confirmedPair, 2),
		nominate:  make(map[string]context.CancelFunc),
		accept:    make(map[string]context.CancelFunc),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	end := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeEnd)
	end.NominationEpoch = 1
	endRaw, err := EncodeUDPPacket(end)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(end) error = %v", err)
	}
	if _, err := sendConn.WriteTo(endRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(end) error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	ready := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeReady)
	ready.NominationEpoch = 1
	readyRaw, err := EncodeUDPPacket(ready)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(ready) error = %v", err)
	}
	if _, err := sendConn.WriteTo(readyRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(first ready) error = %v", err)
	}

	select {
	case <-session.confirmed:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("provider should confirm after the first READY")
	}

	if _, err := sendConn.WriteTo(readyRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(duplicate ready) error = %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	select {
	case pair := <-session.confirmed:
		t.Fatalf("duplicate READY should not signal confirmed twice, got %#v", pair)
	default:
	}
}

func TestRuntimeSessionVisitorSendsReadyAfterAccept(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()
	sendConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(send) error = %v", err)
	}
	defer func() { _ = sendConn.Close() }()

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
	session.cm.Observe(readConn.LocalAddr().String(), sendConn.LocalAddr().String())
	session.cm.MarkSucceeded(readConn.LocalAddr().String(), sendConn.LocalAddr().String())
	if _, ok := session.cm.TryNominate(readConn.LocalAddr().String(), sendConn.LocalAddr().String()); !ok {
		t.Fatal("visitor should nominate pair before ACCEPT")
	}
	session.setOutboundNomination(readConn.LocalAddr().String(), sendConn.LocalAddr().String(), 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.readLoopOnConn(ctx, readConn)

	accept := newUDPPacket("session-1", "token-1", common.WORK_P2P_PROVIDER, packetTypeAccept)
	accept.NominationEpoch = 1
	acceptRaw, err := EncodeUDPPacket(accept)
	if err != nil {
		t.Fatalf("EncodeUDPPacket(accept) error = %v", err)
	}
	if _, err := sendConn.WriteTo(acceptRaw, readConn.LocalAddr()); err != nil {
		t.Fatalf("WriteTo(accept) error = %v", err)
	}

	buf := make([]byte, 2048)
	_ = sendConn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	defer func() { _ = sendConn.SetReadDeadline(time.Time{}) }()
	receivedReady := false
	for !receivedReady {
		n, _, err := sendConn.ReadFrom(buf)
		if err != nil {
			t.Fatalf("ReadFrom() error = %v", err)
		}
		packet, err := DecodeUDPPacket(buf[:n], "token-1")
		if err != nil {
			t.Fatalf("DecodeUDPPacket(ready) error = %v", err)
		}
		if packet.Type == packetTypeReady {
			receivedReady = true
		}
	}

	select {
	case pair := <-session.confirmed:
		if pair.remoteAddr != sendConn.LocalAddr().String() {
			t.Fatalf("confirmed remote = %q, want %q", pair.remoteAddr, sendConn.LocalAddr().String())
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("visitor should handover after ACCEPT")
	}
}
