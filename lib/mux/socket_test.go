package mux

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type rawGetterTestConn struct {
	net.Conn
	raw net.Conn
}

func (c rawGetterTestConn) GetRawConn() net.Conn {
	return c.raw
}

type netGetterTestConn struct {
	net.Conn
	raw net.Conn
}

func (c netGetterTestConn) NetConn() net.Conn {
	return c.raw
}

type closeChanTestConn struct {
	net.Conn
	closeCh chan struct{}
}

func (c closeChanTestConn) CloseChan() <-chan struct{} {
	return c.closeCh
}

type closeSpyConn struct {
	net.Conn
	closeCh chan struct{}
	once    sync.Once
}

func (c *closeSpyConn) Close() error {
	c.once.Do(func() {
		close(c.closeCh)
	})
	return c.Conn.Close()
}

func TestUnwrapSocketConnFollowsRawAndNetConnWrappers(t *testing.T) {
	baseLeft, baseRight := net.Pipe()
	defer func() { _ = baseLeft.Close() }()
	defer func() { _ = baseRight.Close() }()

	wrapped := netGetterTestConn{
		raw: rawGetterTestConn{raw: baseLeft},
	}

	if got := unwrapSocketConn(wrapped); got != baseLeft {
		t.Fatalf("unwrapSocketConn() = %T %p, want base conn %p", got, got, baseLeft)
	}
}

func TestNormalizeBandwidthCalcThresholdClampsRange(t *testing.T) {
	if got := normalizeBandwidthCalcThreshold(1); got != minBandwidthCalcThreshold {
		t.Fatalf("normalizeBandwidthCalcThreshold(1) = %d, want %d", got, minBandwidthCalcThreshold)
	}
	if got := normalizeBandwidthCalcThreshold(maxBandwidthCalcThreshold + 1); got != maxBandwidthCalcThreshold {
		t.Fatalf("normalizeBandwidthCalcThreshold(max+1) = %d, want %d", got, maxBandwidthCalcThreshold)
	}
	mid := uint32(768 << 10)
	if got := normalizeBandwidthCalcThreshold(mid); got != mid {
		t.Fatalf("normalizeBandwidthCalcThreshold(%d) = %d, want unchanged", mid, got)
	}
}

func TestFallbackBandwidthCalcThresholdRespectsTransport(t *testing.T) {
	if got := fallbackBandwidthCalcThreshold("kcp"); got != kcpBandwidthCalcThreshold {
		t.Fatalf("fallbackBandwidthCalcThreshold(kcp) = %d, want %d", got, kcpBandwidthCalcThreshold)
	}
	for _, connType := range []string{"tcp", "tls", "ws", "wss", ""} {
		if got := fallbackBandwidthCalcThreshold(connType); got != defaultBandwidthCalcThreshold {
			t.Fatalf("fallbackBandwidthCalcThreshold(%q) = %d, want %d", connType, got, defaultBandwidthCalcThreshold)
		}
	}
}

func TestNewBandwidthUsesFallbackThresholdWhenSocketUnavailable(t *testing.T) {
	if got := NewBandwidth(nil, "kcp").calcThreshold; got != kcpBandwidthCalcThreshold {
		t.Fatalf("NewBandwidth(nil, kcp).calcThreshold = %d, want %d", got, kcpBandwidthCalcThreshold)
	}
	if got := NewBandwidth(nil, "ws").calcThreshold; got != defaultBandwidthCalcThreshold {
		t.Fatalf("NewBandwidth(nil, ws).calcThreshold = %d, want %d", got, defaultBandwidthCalcThreshold)
	}
}

func TestNormalizeWriteQueueWatermarksClampsAndRestoresHysteresis(t *testing.T) {
	high, low := normalizeWriteQueueWatermarks(1, 9)
	if high != 2 || low != 1 {
		t.Fatalf("normalizeWriteQueueWatermarks(1, 9) = %d/%d, want 2/1", high, low)
	}

	high, low = normalizeWriteQueueWatermarks(64, 64)
	if high != 64 || low != 32 {
		t.Fatalf("normalizeWriteQueueWatermarks(64, 64) = %d/%d, want 64/32", high, low)
	}
}

func TestResolveIOTimeoutUsesFallbackUnlessExplicitlyDisabled(t *testing.T) {
	if got := resolveIOTimeout(0, 9*time.Second); got != 9*time.Second {
		t.Fatalf("resolveIOTimeout(0, fallback) = %s, want 9s", got)
	}
	if got := resolveIOTimeout(5*time.Second, 9*time.Second); got != 5*time.Second {
		t.Fatalf("resolveIOTimeout(5s, fallback) = %s, want 5s", got)
	}
	if got := resolveIOTimeout(-time.Second, 9*time.Second); got != 0 {
		t.Fatalf("resolveIOTimeout(-1s, fallback) = %s, want 0", got)
	}
}

func TestFillPingPadHonorsDisableRandomization(t *testing.T) {
	m := &Mux{config: normalizeMuxConfig(DefaultMuxConfig())}
	m.config.DisablePingPadRandom = true

	buf := []byte{1, 2, 3, 4}
	m.fillPingPad(buf)
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("fillPingPad()[%d] = %d, want 0 when randomization disabled", i, b)
		}
	}
}

func TestWatchConnDoneUsesCloseChanWhenAvailable(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	closeCh := make(chan struct{})
	done := make(chan string, 1)
	watchConnDone(closeChanTestConn{Conn: left, closeCh: closeCh}, func(reason string) {
		done <- reason
	})

	close(closeCh)

	select {
	case reason := <-done:
		if reason != "underlying connection closed" {
			t.Fatalf("watchConnDone() reason = %q, want %q", reason, "underlying connection closed")
		}
	case <-time.After(time.Second):
		t.Fatal("watchConnDone() did not react to CloseChan")
	}
}

func TestAdaptConnExposesCapabilitiesToMuxHelpers(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closeCh := make(chan struct{})

	adapted := AdaptConn(right, ConnCapabilities{
		RawConn:   left,
		CloseChan: closeCh,
		Context:   ctx,
	})

	if got := unwrapSocketConn(adapted); got != left {
		t.Fatalf("unwrapSocketConn(adapted) = %p, want raw conn %p", got, left)
	}

	triggered := make(chan string, 1)
	watchConnDone(adapted, func(reason string) {
		select {
		case triggered <- reason:
		default:
		}
	})

	close(closeCh)

	select {
	case reason := <-triggered:
		if reason != "underlying connection closed" {
			t.Fatalf("watchConnDone(adapted) reason = %q, want close-chan reason", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("watchConnDone(adapted) did not react")
	}
}

func TestPingIdleThresholdAndTrafficAwarePing(t *testing.T) {
	m := &Mux{config: normalizeMuxConfig(DefaultMuxConfig())}
	m.config.PingInterval = 4 * time.Second
	now := time.Now()
	atomic.StoreInt64(&m.lastAliveTime, now.Add(-time.Second).UnixNano())

	if m.shouldSendPing(now) {
		t.Fatal("shouldSendPing() = true, want false when session is recently active")
	}

	atomic.StoreInt64(&m.lastAliveTime, now.Add(-3*time.Second).UnixNano())
	if !m.shouldSendPing(now) {
		t.Fatal("shouldSendPing() = false, want true after idle threshold")
	}

	m.config.DisableTrafficAwarePing = true
	atomic.StoreInt64(&m.lastAliveTime, now.UnixNano())
	if !m.shouldSendPing(now) {
		t.Fatal("shouldSendPing() = false, want true when traffic-aware ping suppression disabled")
	}
}

func TestObserveLatencyTracksLastPeakAndAdaptiveTimeout(t *testing.T) {
	m := &Mux{
		config: normalizeMuxConfig(MuxConfig{
			PingInterval:          5 * time.Second,
			PingJitter:            2 * time.Second,
			PingTimeout:           20 * time.Second,
			MinPingTimeout:        9 * time.Second,
			PingTimeoutMultiplier: 4,
		}),
		counter:     newLatencyCounter(),
		pingTimeout: 20 * time.Second,
	}

	m.observeLatency(80 * time.Millisecond)
	if got := m.LastLatency(); got != 80*time.Millisecond {
		t.Fatalf("LastLatency() = %s, want 80ms", got)
	}
	if got := m.PeakLatency(); got != 80*time.Millisecond {
		t.Fatalf("PeakLatency() = %s, want 80ms", got)
	}
	if got := m.EffectivePingTimeout(); got != 9*time.Second {
		t.Fatalf("EffectivePingTimeout() = %s, want min clamp 9s", got)
	}
	if got := m.Latency(); got <= 0 {
		t.Fatalf("Latency() = %s, want positive smoothed RTT", got)
	}

	m.observeLatency(3 * time.Second)
	if got := m.PeakLatency(); got != 3*time.Second {
		t.Fatalf("PeakLatency() after spike = %s, want 3s", got)
	}
	if got := m.EffectivePingTimeout(); got != 12*time.Second {
		t.Fatalf("EffectivePingTimeout() after 3s spike = %s, want 12s", got)
	}

	m.observeLatency(500 * time.Millisecond)
	if got := m.PeakLatency(); got != 3*time.Second {
		t.Fatalf("PeakLatency() after recovery = %s, want sticky 3s", got)
	}

	m.observeLatency(10 * time.Second)
	if got := m.EffectivePingTimeout(); got != 20*time.Second {
		t.Fatalf("EffectivePingTimeout() = %s, want capped max timeout 20s", got)
	}
}

func TestResetPeakLatencyFallsBackToLastLatency(t *testing.T) {
	m := &Mux{
		config: normalizeMuxConfig(MuxConfig{
			PingTimeout:           20 * time.Second,
			MinPingTimeout:        5 * time.Second,
			PingTimeoutMultiplier: 4,
		}),
		counter:     newLatencyCounter(),
		pingTimeout: 20 * time.Second,
	}

	m.observeLatency(3 * time.Second)
	m.observeLatency(700 * time.Millisecond)

	if got := m.PeakLatency(); got != 3*time.Second {
		t.Fatalf("PeakLatency() before reset = %s, want 3s", got)
	}
	if prev := m.ResetPeakLatency(); prev != 3*time.Second {
		t.Fatalf("ResetPeakLatency() = %s, want previous peak 3s", prev)
	}
	if got := m.PeakLatency(); got != 700*time.Millisecond {
		t.Fatalf("PeakLatency() after reset = %s, want last latency 700ms", got)
	}
	if got := m.EffectivePingTimeout(); got != 5*time.Second {
		t.Fatalf("EffectivePingTimeout() after reset = %s, want min clamp 5s", got)
	}
}

func TestPreparePingPayloadCanReachConfiguredMaxPad(t *testing.T) {
	m := &Mux{config: normalizeMuxConfig(DefaultMuxConfig())}
	m.config.PingMaxPad = 1
	buf := make([]byte, 9)

	for i := 0; i < 128; i++ {
		payload := m.preparePingPayload(buf, time.Now())
		if len(payload) == 9 {
			return
		}
	}

	t.Fatal("preparePingPayload() never used the configured maximum pad length")
}

func TestPreparePingReplyCanReachConfiguredMaxPad(t *testing.T) {
	m := &Mux{config: normalizeMuxConfig(DefaultMuxConfig())}
	m.config.PingMaxPad = 1
	buf := make([]byte, 9)
	buf[0] = 1

	for i := 0; i < 128; i++ {
		reply := m.preparePingReply(buf)
		if len(reply) == 9 {
			return
		}
	}

	t.Fatal("preparePingReply() never used the configured maximum pad length")
}

func TestMuxCloseClosesUnderlyingConn(t *testing.T) {
	server, client := net.Pipe()
	leftConn := &closeSpyConn{Conn: client, closeCh: make(chan struct{})}
	left := NewMux(leftConn, "tcp", 30, true)
	right := NewMux(server, "tcp", 30, false)
	defer func() { _ = right.Close() }()

	if err := left.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-leftConn.closeCh:
	case <-time.After(time.Second):
		t.Fatal("underlying conn Close() was not called")
	}
}
