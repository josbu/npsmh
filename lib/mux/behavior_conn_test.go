package mux

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNewConnDoesNotWaitForAccept(t *testing.T) {
	leftMux, rightMux := newMuxPair(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		conn, err := rightMux.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	start := time.Now()
	conn, err := leftMux.NewConn()
	if err != nil {
		t.Fatalf("NewConn() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	if elapsed := time.Since(start); elapsed >= 250*time.Millisecond {
		t.Fatalf("NewConn() took %s, want it to complete before delayed Accept", elapsed)
	}

	select {
	case remote := <-accepted:
		_ = remote.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delayed Accept")
	}
}

func TestNewConnFailsWhenAcceptBacklogFull(t *testing.T) {
	oldBacklog := AcceptBacklog
	AcceptBacklog = 1
	defer func() { AcceptBacklog = oldBacklog }()

	leftMux, _ := newMuxPair(t)

	first, err := leftMux.NewConn()
	if err != nil {
		t.Fatalf("first NewConn() error = %v", err)
	}
	defer func() { _ = first.Close() }()

	_, err = leftMux.NewConn()
	if err == nil {
		t.Fatal("second NewConn() error = nil, want backlog failure")
	}
	if !strings.Contains(err.Error(), "server refused") {
		t.Fatalf("second NewConn() error = %q, want backlog refusal", err.Error())
	}
}

func TestNewMuxUsesGlobalDefaultsSnapshot(t *testing.T) {
	oldPingInterval := PingInterval
	oldPingJitter := PingJitter
	oldPingMaxPad := PingMaxPad
	oldBacklog := AcceptBacklog
	oldConnWindow := MaxConnReceiveWindow
	oldSessionWindow := MaxSessionReceiveWindow
	oldSocketKeepAlive := SocketKeepAlive
	oldCloseTimeout := CloseTimeout
	oldHighWater := WriteQueueHighWater
	oldLowWater := WriteQueueLowWater
	oldMinPingTimeout := MinPingTimeout
	PingInterval = 3 * time.Second
	PingJitter = 1 * time.Second
	PingMaxPad = 7
	AcceptBacklog = 5
	MaxConnReceiveWindow = defaultInitialConnWindow * 2
	MaxSessionReceiveWindow = uint64(defaultInitialConnWindow * 3)
	SocketKeepAlive = 17 * time.Second
	CloseTimeout = 9 * time.Second
	WriteQueueHighWater = 99
	WriteQueueLowWater = 33
	MinPingTimeout = 6 * time.Second
	defer func() {
		PingInterval = oldPingInterval
		PingJitter = oldPingJitter
		PingMaxPad = oldPingMaxPad
		AcceptBacklog = oldBacklog
		MaxConnReceiveWindow = oldConnWindow
		MaxSessionReceiveWindow = oldSessionWindow
		SocketKeepAlive = oldSocketKeepAlive
		CloseTimeout = oldCloseTimeout
		WriteQueueHighWater = oldHighWater
		WriteQueueLowWater = oldLowWater
		MinPingTimeout = oldMinPingTimeout
	}()

	server, client := net.Pipe()
	left := NewMux(client, "tcp", 0, true)
	right := NewMux(server, "tcp", 0, false)
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	cfg := left.Config()
	if cfg.PingInterval != 3*time.Second || cfg.PingJitter != time.Second || cfg.PingMaxPad != 7 {
		t.Fatalf("Config() ping snapshot = %+v, want interval=3s jitter=1s pad=7", cfg)
	}
	if cfg.AcceptBacklog != 5 {
		t.Fatalf("Config().AcceptBacklog = %d, want 5", cfg.AcceptBacklog)
	}
	if cfg.MaxConnReceiveWindow != defaultInitialConnWindow*2 {
		t.Fatalf("Config().MaxConnReceiveWindow = %d, want %d", cfg.MaxConnReceiveWindow, defaultInitialConnWindow*2)
	}
	if cfg.MaxSessionReceiveWindow != uint64(defaultInitialConnWindow*3) {
		t.Fatalf("Config().MaxSessionReceiveWindow = %d, want %d", cfg.MaxSessionReceiveWindow, uint64(defaultInitialConnWindow*3))
	}
	if cfg.SocketKeepAlive != 17*time.Second || cfg.DisableSocketNoDelay || cfg.CloseTimeout != 9*time.Second {
		t.Fatalf("Config() socket snapshot = %+v, want keepalive=17s disable_nodelay=false close_timeout=9s", cfg)
	}
	if cfg.WriteQueueHighWater != 99 || cfg.WriteQueueLowWater != 33 {
		t.Fatalf("Config() write queue watermarks = %d/%d, want 99/33", cfg.WriteQueueHighWater, cfg.WriteQueueLowWater)
	}
	if cfg.MinPingTimeout != 6*time.Second || cfg.PingTimeout == 0 {
		t.Fatalf("Config() ping timeout snapshot = %+v, want min=6s and derived max timeout", cfg)
	}
	if got := left.AcceptBacklogCap(); got != 5 {
		t.Fatalf("AcceptBacklogCap() = %d, want 5", got)
	}

	AcceptBacklog = 11
	if got := left.AcceptBacklogCap(); got != 5 {
		t.Fatalf("AcceptBacklogCap() after global mutation = %d, want frozen value 5", got)
	}
	if cfg2 := left.Config(); cfg2.AcceptBacklog != 5 {
		t.Fatalf("Config().AcceptBacklog after global mutation = %d, want frozen value 5", cfg2.AcceptBacklog)
	}
}

func TestNewMuxWithConfigOverridesGlobals(t *testing.T) {
	oldBacklog := AcceptBacklog
	oldConnWindow := MaxConnReceiveWindow
	oldSessionWindow := MaxSessionReceiveWindow
	AcceptBacklog = 2
	MaxConnReceiveWindow = defaultInitialConnWindow * 2
	MaxSessionReceiveWindow = uint64(defaultInitialConnWindow * 3)
	defer func() {
		AcceptBacklog = oldBacklog
		MaxConnReceiveWindow = oldConnWindow
		MaxSessionReceiveWindow = oldSessionWindow
	}()

	server, client := net.Pipe()
	cfg := MuxConfig{
		PingInterval:            9 * time.Second,
		PingJitter:              4 * time.Second,
		PingMaxPad:              3,
		AcceptBacklog:           6,
		MaxConnReceiveWindow:    defaultInitialConnWindow * 4,
		MaxSessionReceiveWindow: uint64(defaultInitialConnWindow * 5),
		PingTimeout:             18 * time.Second,
		MinPingTimeout:          7 * time.Second,
		OpenTimeout:             12 * time.Second,
		ReadTimeout:             14 * time.Second,
		WriteTimeout:            13 * time.Second,
		SocketKeepAlive:         21 * time.Second,
		DisableSocketNoDelay:    true,
		CloseTimeout:            11 * time.Second,
		WriteQueueHighWater:     120,
		WriteQueueLowWater:      48,
		DisablePingPadRandom:    true,
	}
	left := NewMuxWithConfig(client, "tcp", 0, true, cfg)
	right := NewMux(server, "tcp", 0, false)
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	got := left.Config()
	if got.AcceptBacklog != 6 {
		t.Fatalf("left Config().AcceptBacklog = %d, want 6", got.AcceptBacklog)
	}
	if got.MaxConnReceiveWindow != defaultInitialConnWindow*4 {
		t.Fatalf("left Config().MaxConnReceiveWindow = %d, want %d", got.MaxConnReceiveWindow, defaultInitialConnWindow*4)
	}
	if got.MaxSessionReceiveWindow != uint64(defaultInitialConnWindow*5) {
		t.Fatalf("left Config().MaxSessionReceiveWindow = %d, want %d", got.MaxSessionReceiveWindow, uint64(defaultInitialConnWindow*5))
	}
	if got.OpenTimeout != 12*time.Second {
		t.Fatalf("left Config().OpenTimeout = %s, want 12s", got.OpenTimeout)
	}
	if got.ReadTimeout != 14*time.Second || got.WriteTimeout != 13*time.Second {
		t.Fatalf("left Config() io timeouts = %s/%s, want 14s/13s", got.ReadTimeout, got.WriteTimeout)
	}
	if got.SocketKeepAlive != 21*time.Second || !got.DisableSocketNoDelay || got.CloseTimeout != 11*time.Second {
		t.Fatalf("left Config() socket options = %+v, want keepalive=21s disable_nodelay=true close_timeout=11s", got)
	}
	if got.WriteQueueHighWater != 120 || got.WriteQueueLowWater != 48 || !got.DisablePingPadRandom {
		t.Fatalf("left Config() queue/random options = %+v, want high=120 low=48 disable_ping_pad_random=true", got)
	}
	if got.PingTimeout != 18*time.Second || got.MinPingTimeout != 7*time.Second {
		t.Fatalf("left Config() ping timeout options = %+v, want max=18s min=7s", got)
	}
	if cap := left.AcceptBacklogCap(); cap != 6 {
		t.Fatalf("left AcceptBacklogCap() = %d, want 6", cap)
	}
	if got, want := left.SessionReceiveLimit(), uint64(defaultInitialConnWindow*5); got != want {
		t.Fatalf("left SessionReceiveLimit() = %d, want %d", got, want)
	}

	rightCfg := right.Config()
	if rightCfg.AcceptBacklog != 2 {
		t.Fatalf("right Config().AcceptBacklog = %d, want legacy global value 2", rightCfg.AcceptBacklog)
	}
	if cap := right.AcceptBacklogCap(); cap != 2 {
		t.Fatalf("right AcceptBacklogCap() = %d, want 2", cap)
	}
}

func TestNewMuxWithConfigPreservesDefaultsForOmittedNewFields(t *testing.T) {
	server, client := net.Pipe()
	cfg := MuxConfig{
		AcceptBacklog:           4,
		MaxConnReceiveWindow:    defaultInitialConnWindow * 2,
		MaxSessionReceiveWindow: uint64(defaultInitialConnWindow * 3),
	}
	left := NewMuxWithConfig(client, "tcp", 0, true, cfg)
	right := NewMux(server, "tcp", 0, false)
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	got := left.Config()
	if got.SocketKeepAlive != SocketKeepAlive || got.DisableSocketNoDelay || got.CloseTimeout != CloseTimeout {
		t.Fatalf("omitted socket fields = %+v, want keepalive default / nodelay enabled / close timeout default", got)
	}
	if got.WriteQueueHighWater != WriteQueueHighWater || got.WriteQueueLowWater != WriteQueueLowWater {
		t.Fatalf("omitted queue watermarks = %d/%d, want defaults %d/%d", got.WriteQueueHighWater, got.WriteQueueLowWater, WriteQueueHighWater, WriteQueueLowWater)
	}
	if got.ReadTimeout == 0 || got.WriteTimeout == 0 {
		t.Fatalf("omitted io timeouts = %s/%s, want derived non-zero defaults", got.ReadTimeout, got.WriteTimeout)
	}
	if got.PingTimeout == 0 || got.MinPingTimeout == 0 {
		t.Fatalf("omitted ping timeout fields = %+v, want derived non-zero defaults", got)
	}
}

func TestNewMuxDerivesIOTimeoutsFromPingTimeoutByDefault(t *testing.T) {
	server, client := net.Pipe()
	left := NewMux(client, "tcp", 0, true)
	right := NewMux(server, "tcp", 0, false)
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	cfg := left.Config()
	want := resolvePingTimeout(cfg, "tcp", 0)
	if cfg.ReadTimeout != want || cfg.WriteTimeout != want {
		t.Fatalf("Config() io timeouts = %s/%s, want derived ping timeout %s", cfg.ReadTimeout, cfg.WriteTimeout, want)
	}
}

func TestSetReadDeadlineWakesBlockedRead(t *testing.T) {
	left, right := newConnPair(t)
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := left.Read(buf)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := left.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	select {
	case err := <-done:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("Read() error = %v, want timeout net.Error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read() did not unblock after read deadline")
	}
}

func TestMuxCloseChanAndNumStreams(t *testing.T) {
	leftMux, rightMux := newMuxPair(t)

	select {
	case <-leftMux.CloseChan():
		t.Fatal("CloseChan() should not be closed before Close()")
	default:
	}

	leftConn, err := leftMux.NewConn()
	if err != nil {
		t.Fatalf("NewConn() error = %v", err)
	}
	defer func() { _ = leftConn.Close() }()

	rightConn, err := rightMux.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	defer func() { _ = rightConn.Close() }()

	waitForCondition(t, time.Second, func() bool {
		return leftMux.NumStreams() == 1 && rightMux.NumStreams() == 1
	})

	if err := leftMux.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-leftMux.CloseChan():
	case <-time.After(time.Second):
		t.Fatal("CloseChan() did not close after Close()")
	}
}
