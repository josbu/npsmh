package mux

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionReceiveWindowClampsActiveConnWhenBudgetIsFull(t *testing.T) {
	oldConnWindow := MaxConnReceiveWindow
	oldSessionWindow := MaxSessionReceiveWindow
	MaxConnReceiveWindow = defaultInitialConnWindow * 4
	MaxSessionReceiveWindow = uint64(defaultInitialConnWindow * 4)
	defer func() {
		MaxConnReceiveWindow = oldConnWindow
		MaxSessionReceiveWindow = oldSessionWindow
	}()

	m := &Mux{
		config:           normalizeMuxConfig(DefaultMuxConfig()),
		sessionRecvLimit: normalizedMaxSessionReceiveWindow(),
	}
	rw := &receiveWindow{}
	rw.mux = m
	atomic.StoreUint32(&rw.buffered, uint32(defaultInitialConnWindow*2))
	atomic.StoreUint64(&m.sessionRecvQueued, uint64(defaultInitialConnWindow*4))

	effective := rw.effectiveMaxSize(normalizedMaxConnReceiveWindow())
	if effective != uint32(defaultInitialConnWindow*2) {
		t.Fatalf("effectiveMaxSize() = %d, want %d", effective, defaultInitialConnWindow*2)
	}
	if remain := rw.remainingSize(effective, 0); remain != 0 {
		t.Fatalf("remainingSize() = %d, want 0 when session budget is full", remain)
	}
	if got, want := m.SessionReceiveLimit(), uint64(defaultInitialConnWindow*4); got != want {
		t.Fatalf("SessionReceiveLimit() = %d, want %d", got, want)
	}
}

func TestCloseWriteAllowsHalfCloseAfterCapabilityNegotiation(t *testing.T) {
	left, rightNet := newConnPair(t)
	defer func() { _ = left.Close() }()
	right := rightNet.(*Conn)
	defer func() { _ = right.Close() }()

	waitForCondition(t, 2*time.Second, func() bool {
		return left.receiveWindow.mux.supportsRemoteCapability(capabilityCloseWrite) &&
			right.receiveWindow.mux.supportsRemoteCapability(capabilityCloseWrite)
	})

	if _, err := left.Write([]byte("ping")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := left.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite() error = %v", err)
	}
	if _, err := left.Write([]byte("again")); err == nil {
		t.Fatal("Write() after CloseWrite() error = nil, want closed write side")
	}

	buf := make([]byte, 4)
	if _, err := io.ReadFull(right, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("ReadFull() = %q, want %q", string(buf), "ping")
	}

	eofCh := make(chan error, 1)
	go func() {
		var b [1]byte
		_, err := right.Read(b[:])
		eofCh <- err
	}()

	select {
	case err := <-eofCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Read() error = %v, want EOF after remote CloseWrite()", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for EOF after CloseWrite()")
	}

	if _, err := right.Write([]byte("pong")); err != nil {
		t.Fatalf("Write() on peer after remote CloseWrite() error = %v", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(left, reply); err != nil {
		t.Fatalf("ReadFull() reply error = %v", err)
	}
	if string(reply) != "pong" {
		t.Fatalf("reply = %q, want %q", string(reply), "pong")
	}
}

func TestCloseWriteFallsBackToCloseWithoutNegotiation(t *testing.T) {
	left, rightNet := newConnPair(t)
	defer func() { _ = left.Close() }()
	right := rightNet.(*Conn)
	defer func() { _ = right.Close() }()

	left.receiveWindow.mux.setRemoteCapabilities(0)
	right.receiveWindow.mux.setRemoteCapabilities(0)

	if err := left.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite() error = %v", err)
	}

	waitForCondition(t, 2*time.Second, func() bool {
		return atomic.LoadUint32(&right.closingFlag) == 1
	})

	if _, err := right.Write([]byte("pong")); err == nil {
		t.Fatal("peer Write() error = nil, want full-close fallback")
	}
}
