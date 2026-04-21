package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
)

func TestSocks5UDPRegistryCloseClosesSessionsConcurrently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstConn := newBlockingCloseConn()
	secondConn := newBlockingCloseConn()
	registry := &socks5UDPRegistry{
		ctx:            ctx,
		cancel:         cancel,
		sessions:       make(map[*socks5UDPSession]struct{}),
		sessionsByAddr: map[string]*socks5UDPSession{},
		sessionsByIP:   make(map[string]map[*socks5UDPSession]struct{}),
		pendingByIP:    make(map[string][]*socks5UDPSession),
	}

	first := &socks5UDPSession{
		registry:    registry,
		framed:      conn.WrapFramed(firstConn),
		clientIPKey: "127.0.0.1",
		packetCh:    make(chan socks5UDPPacket, 1),
		done:        make(chan struct{}),
	}
	second := &socks5UDPSession{
		registry:    registry,
		framed:      conn.WrapFramed(secondConn),
		clientIPKey: "127.0.0.1",
		packetCh:    make(chan socks5UDPPacket, 1),
		done:        make(chan struct{}),
	}
	registry.sessions[first] = struct{}{}
	registry.sessions[second] = struct{}{}

	closeDone := make(chan struct{})
	go func() {
		_ = registry.Close()
		close(closeDone)
	}()

	select {
	case <-firstConn.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first socks5 udp session close did not start")
	}

	select {
	case <-secondConn.closeStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("second socks5 udp session close should start before the first close is released")
	}

	close(firstConn.releaseClose)
	close(secondConn.releaseClose)

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("registry Close() did not finish after session closes were released")
	}

	if got := registry.sessionCount(); got != 0 {
		t.Fatalf("sessionCount() = %d, want 0 after Close()", got)
	}
}
