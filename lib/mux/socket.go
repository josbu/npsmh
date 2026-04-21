package mux

import (
	"context"
	"net"
	"strings"
	"time"
)

const (
	minBandwidthCalcThreshold     uint32 = 128 << 10
	maxBandwidthCalcThreshold     uint32 = 4 << 20
	defaultBandwidthCalcThreshold uint32 = 1 << 20
	kcpBandwidthCalcThreshold     uint32 = 512 << 10
)

type rawConnGetter interface {
	GetRawConn() net.Conn
}

type netConnGetter interface {
	NetConn() net.Conn
}

type underlyingConnGetter interface {
	UnderlyingConn() net.Conn
}

type wrappedConnGetter interface {
	WrappedConn() net.Conn
}

type closeChanGetter interface {
	CloseChan() <-chan struct{}
}

type doneChanGetter interface {
	Done() <-chan struct{}
}

type contextGetter interface {
	Context() context.Context
}

func bandwidthCalcThreshold(c net.Conn, connType string) uint32 {
	if size, ok := socketReceiveBufferSize(c); ok {
		return normalizeBandwidthCalcThreshold(size)
	}
	return fallbackBandwidthCalcThreshold(connType)
}

func fallbackBandwidthCalcThreshold(connType string) uint32 {
	switch normalizedConnType(connType) {
	case "kcp":
		return kcpBandwidthCalcThreshold
	default:
		return defaultBandwidthCalcThreshold
	}
}

func normalizeBandwidthCalcThreshold(size uint32) uint32 {
	if size < minBandwidthCalcThreshold {
		return minBandwidthCalcThreshold
	}
	if size > maxBandwidthCalcThreshold {
		return maxBandwidthCalcThreshold
	}
	return size
}

func normalizedConnType(connType string) string {
	return strings.ToLower(strings.TrimSpace(connType))
}

func unwrapSocketConn(c net.Conn) net.Conn {
	for depth := 0; depth < 8 && c != nil; depth++ {
		switch v := c.(type) {
		case rawConnGetter:
			next := v.GetRawConn()
			if next == nil || next == c {
				return c
			}
			c = next
		case netConnGetter:
			next := v.NetConn()
			if next == nil || next == c {
				return c
			}
			c = next
		case underlyingConnGetter:
			next := v.UnderlyingConn()
			if next == nil || next == c {
				return c
			}
			c = next
		case wrappedConnGetter:
			next := v.WrappedConn()
			if next == nil || next == c {
				return c
			}
			c = next
		default:
			return c
		}
	}
	return c
}

func applySocketOptions(c net.Conn, cfg MuxConfig) {
	base := unwrapSocketConn(c)
	if base == nil {
		return
	}
	if !cfg.DisableSocketNoDelay {
		if conn, ok := base.(interface{ SetNoDelay(bool) error }); ok {
			_ = conn.SetNoDelay(true)
		}
	}
	if cfg.SocketKeepAlive > 0 {
		if conn, ok := base.(interface {
			SetKeepAlive(bool) error
			SetKeepAlivePeriod(time.Duration) error
		}); ok {
			_ = conn.SetKeepAlive(true)
			_ = conn.SetKeepAlivePeriod(cfg.SocketKeepAlive)
		}
	}
}

func closeDeadline(cfg MuxConfig) time.Duration {
	if cfg.CloseTimeout <= 0 {
		return 0
	}
	return cfg.CloseTimeout
}

func watchConnDone(c net.Conn, closeFn func(string)) {
	if c == nil || closeFn == nil {
		return
	}
	var closeCh, doneCh, contextDone <-chan struct{}
	if v, ok := c.(closeChanGetter); ok {
		closeCh = v.CloseChan()
	}
	if v, ok := c.(doneChanGetter); ok {
		doneCh = v.Done()
	}
	if v, ok := c.(contextGetter); ok {
		if ctx := v.Context(); ctx != nil {
			contextDone = ctx.Done()
		}
	}
	if closeCh == nil && doneCh == nil && contextDone == nil {
		return
	}
	go func() {
		select {
		case <-closeCh:
			closeFn("underlying connection closed")
		case <-doneCh:
			closeFn("underlying connection done")
		case <-contextDone:
			closeFn("underlying connection context done")
		}
	}()
}

func resolveIOTimeout(timeout, fallback time.Duration) time.Duration {
	if timeout < 0 {
		return 0
	}
	if timeout > 0 {
		return timeout
	}
	if fallback < 0 {
		return 0
	}
	return fallback
}

func pingIdleThreshold(cfg MuxConfig) time.Duration {
	if cfg.PingInterval <= 0 {
		return 0
	}
	threshold := cfg.PingInterval / 2
	if threshold <= 0 {
		return cfg.PingInterval
	}
	if threshold < 500*time.Millisecond {
		threshold = 500 * time.Millisecond
	}
	return threshold
}
