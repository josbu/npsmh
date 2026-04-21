package proxy

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func TestUdpModeServerSessionForPacketCoalescesConcurrentCreates(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	var started atomic.Int32
	server.sessionWorker = func(addr *net.UDPAddr, session *udpClientSession) {
		started.Add(1)
		<-session.ctx.Done()
	}

	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7100}
	const callers = 32

	results := make(chan *udpClientSession, callers)
	var start sync.WaitGroup
	start.Add(1)
	var callersWG sync.WaitGroup
	for i := 0; i < callers; i++ {
		callersWG.Add(1)
		go func() {
			defer callersWG.Done()
			start.Wait()
			results <- server.sessionForPacket(addr)
		}()
	}
	start.Done()
	callersWG.Wait()
	close(results)

	var first *udpClientSession
	for session := range results {
		if session == nil {
			t.Fatal("sessionForPacket() returned nil session")
		}
		if first == nil {
			first = session
			continue
		}
		if session != first {
			t.Fatalf("sessionForPacket() returned multiple sessions: first=%p current=%p", first, session)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for started.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if started.Load() != 1 {
		t.Fatalf("session worker starts = %d, want 1", started.Load())
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestUdpModeServerLoadActiveSessionDropsMalformedLifecycleEntry(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	server.sessions.Store("127.0.0.1:7101", &udpClientSession{
		ch: make(chan udpPacket, 1),
	})

	if got := server.loadActiveSession("127.0.0.1:7101"); got != nil {
		t.Fatalf("loadActiveSession() = %#v, want nil for malformed session", got)
	}
	if _, ok := server.sessions.Load("127.0.0.1:7101"); ok {
		t.Fatal("malformed session should be removed from session map")
	}
}

func TestUDPClientSessionCloseAllowsNilCancel(t *testing.T) {
	session := &udpClientSession{
		ch:  make(chan udpPacket, 1),
		ctx: context.Background(),
	}
	spyConn := newUDPCloseSpyConn()
	session.conn = spyConn

	session.close()

	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close() should still close the current conn when cancel is nil")
	}
}

func TestUDPClientSessionSetConnClosesConnWhenContextMissing(t *testing.T) {
	session := &udpClientSession{
		ch:     make(chan udpPacket, 1),
		cancel: func() {},
	}
	spyConn := newUDPCloseSpyConn()

	session.setConn(spyConn)

	select {
	case <-spyConn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("setConn() should close conn when session context is missing")
	}
}

func TestUdpModeServerCloseClosesSessionsConcurrently(t *testing.T) {
	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()

	firstConn := newBlockingCloseConn()
	secondConn := newBlockingCloseConn()
	server.sessions.Store("127.0.0.1:7102", &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    firstCtx,
		cancel: firstCancel,
		conn:   firstConn,
	})
	server.sessions.Store("127.0.0.1:7103", &udpClientSession{
		ch:     make(chan udpPacket, 1),
		ctx:    secondCtx,
		cancel: secondCancel,
		conn:   secondConn,
	})

	closeDone := make(chan struct{})
	go func() {
		_ = server.Close()
		close(closeDone)
	}()

	select {
	case <-firstConn.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first udp session close did not start")
	}

	select {
	case <-secondConn.closeStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second udp session close should start before the first close is released")
	}

	close(firstConn.releaseClose)
	close(secondConn.releaseClose)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not finish after udp session closes were released")
	}
}
