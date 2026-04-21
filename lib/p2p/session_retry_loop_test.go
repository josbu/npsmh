package p2p

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func TestRuntimeSessionAcceptLoopReplacesSupersededEpoch(t *testing.T) {
	writeConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(write) error = %v", err)
	}
	defer func() { _ = writeConn.Close() }()

	firstRemote, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(first) error = %v", err)
	}
	defer func() { _ = firstRemote.Close() }()

	secondRemote, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(second) error = %v", err)
	}
	defer func() { _ = secondRemote.Close() }()

	session := &runtimeSession{
		start:  P2PPunchStart{SessionID: "session-1", Token: "token-1", Role: common.WORK_P2P_PROVIDER},
		plan:   PunchPlan{NominationRetryInterval: 250 * time.Millisecond},
		cm:     NewCandidateManager(""),
		accept: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session.startAcceptLoop(ctx, writeConn, firstRemote.LocalAddr(), 1)
	firstKey := nominationLoopKey(candidateKey(writeConn.LocalAddr().String(), firstRemote.LocalAddr().String()), 1)

	session.mu.Lock()
	firstActive := session.accept[firstKey] != nil
	firstCount := len(session.accept)
	session.mu.Unlock()
	if !firstActive || firstCount != 1 {
		t.Fatalf("first accept loop should be tracked once, active=%t count=%d", firstActive, firstCount)
	}

	session.startAcceptLoop(ctx, writeConn, secondRemote.LocalAddr(), 2)
	secondKey := nominationLoopKey(candidateKey(writeConn.LocalAddr().String(), secondRemote.LocalAddr().String()), 2)

	session.mu.Lock()
	_, firstStillActive := session.accept[firstKey]
	secondActive := session.accept[secondKey] != nil
	activeCount := len(session.accept)
	session.mu.Unlock()

	if firstStillActive || !secondActive || activeCount != 1 {
		t.Fatalf("accept loops should keep only the newest epoch, first=%t second=%t count=%d", firstStillActive, secondActive, activeCount)
	}
}

func TestRuntimeSessionNominationLoopReplacesSupersededEpoch(t *testing.T) {
	writeConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(write) error = %v", err)
	}
	defer func() { _ = writeConn.Close() }()

	firstRemote, err := net.ResolveUDPAddr("udp4", "127.0.0.1:41001")
	if err != nil {
		t.Fatalf("ResolveUDPAddr(first) error = %v", err)
	}
	secondRemote, err := net.ResolveUDPAddr("udp4", "127.0.0.1:41002")
	if err != nil {
		t.Fatalf("ResolveUDPAddr(second) error = %v", err)
	}

	session := &runtimeSession{
		start:    P2PPunchStart{SessionID: "session-1", Token: "token-1", Role: common.WORK_P2P_VISITOR},
		plan:     PunchPlan{NominationRetryInterval: 250 * time.Millisecond},
		cm:       NewCandidateManager(""),
		nominate: make(map[string]context.CancelFunc),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session.setOutboundNomination(writeConn.LocalAddr().String(), firstRemote.String(), 1)
	session.startNominationLoopWithEpoch(ctx, writeConn, firstRemote, 1)
	firstKey := nominationLoopKey(candidateKey(writeConn.LocalAddr().String(), firstRemote.String()), 1)

	session.mu.Lock()
	firstActive := session.nominate[firstKey] != nil
	firstCount := len(session.nominate)
	session.mu.Unlock()
	if !firstActive || firstCount != 1 {
		t.Fatalf("first nomination loop should be tracked once, active=%t count=%d", firstActive, firstCount)
	}

	session.setOutboundNomination(writeConn.LocalAddr().String(), secondRemote.String(), 2)
	session.startNominationLoopWithEpoch(ctx, writeConn, secondRemote, 2)
	secondKey := nominationLoopKey(candidateKey(writeConn.LocalAddr().String(), secondRemote.String()), 2)

	session.mu.Lock()
	_, firstStillActive := session.nominate[firstKey]
	secondActive := session.nominate[secondKey] != nil
	activeCount := len(session.nominate)
	session.mu.Unlock()

	if firstStillActive || !secondActive || activeCount != 1 {
		t.Fatalf("nomination loops should keep only the newest epoch, first=%t second=%t count=%d", firstStillActive, secondActive, activeCount)
	}
}

func TestRuntimeSessionSignalConfirmedCancelsRetryLoops(t *testing.T) {
	readConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(read) error = %v", err)
	}
	defer func() { _ = readConn.Close() }()

	nominateCtx, nominateCancel := context.WithCancel(context.Background())
	acceptCtx, acceptCancel := context.WithCancel(context.Background())

	session := &runtimeSession{
		start:     P2PPunchStart{SessionID: "session-1", Token: "token-1", Role: common.WORK_P2P_VISITOR},
		confirmed: make(chan confirmedPair, 1),
		nominate: map[string]context.CancelFunc{
			"nominate|1": nominateCancel,
		},
		accept: map[string]context.CancelFunc{
			"accept|1": acceptCancel,
		},
	}

	session.signalConfirmed(readConn, readConn.LocalAddr().String(), "198.51.100.10:4000")

	select {
	case <-nominateCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("nomination retry loop should be canceled after confirm")
	}
	select {
	case <-acceptCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("accept retry loop should be canceled after confirm")
	}

	session.mu.Lock()
	nominateCount := len(session.nominate)
	acceptCount := len(session.accept)
	session.mu.Unlock()
	if nominateCount != 0 || acceptCount != 0 {
		t.Fatalf("retry loops should be cleared after confirm, nominate=%d accept=%d", nominateCount, acceptCount)
	}
}
