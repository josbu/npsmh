package mux

import "time"

const defaultInitialConnWindow = maximumSegmentSize * 30

var (
	MaxConnReceiveWindow    uint32 = maximumWindowSize
	MaxSessionReceiveWindow uint64 = uint64(maximumWindowSize) * 2
	SocketKeepAlive                = 15 * time.Second
	CloseTimeout                   = 5 * time.Second
	WriteQueueHighWater     uint64 = 2048
	WriteQueueLowWater      uint64 = 1024
	PingTimeoutMultiplier          = 6.0
	MinPingTimeout          time.Duration
)

// MuxConfig controls ping behavior, deadlines, queue backpressure, and optional
// transport-level socket tuning.
type MuxConfig struct {
	PingInterval               time.Duration
	PingJitter                 time.Duration
	PingMaxPad                 int
	AcceptBacklog              int
	MaxConnReceiveWindow       uint32
	MaxSessionReceiveWindow    uint64
	PingTimeout                time.Duration
	MinPingTimeout             time.Duration
	OpenTimeout                time.Duration
	ReadTimeout                time.Duration
	WriteTimeout               time.Duration
	SocketKeepAlive            time.Duration
	DisableSocketNoDelay       bool
	CloseTimeout               time.Duration
	WriteQueueHighWater        uint64
	WriteQueueLowWater         uint64
	DisablePingPadRandom       bool
	DisableTrafficAwarePing    bool
	PingTimeoutMultiplier      float64
	DisableAdaptivePingTimeout bool
}

// DefaultMuxConfig returns the current default runtime configuration snapshot.
func DefaultMuxConfig() MuxConfig {
	return MuxConfig{
		PingInterval:            PingInterval,
		PingJitter:              PingJitter,
		PingMaxPad:              PingMaxPad,
		AcceptBacklog:           AcceptBacklog,
		MaxConnReceiveWindow:    MaxConnReceiveWindow,
		MaxSessionReceiveWindow: MaxSessionReceiveWindow,
		MinPingTimeout:          MinPingTimeout,
		SocketKeepAlive:         SocketKeepAlive,
		CloseTimeout:            CloseTimeout,
		WriteQueueHighWater:     WriteQueueHighWater,
		WriteQueueLowWater:      WriteQueueLowWater,
		PingTimeoutMultiplier:   PingTimeoutMultiplier,
	}
}

func normalizeMuxConfig(cfg MuxConfig) MuxConfig {
	cfg.PingJitter = absoluteDuration(cfg.PingJitter)
	if cfg.PingMaxPad < 0 {
		cfg.PingMaxPad = 0
	} else if cfg.PingMaxPad > maximumSegmentSize-8 {
		cfg.PingMaxPad = maximumSegmentSize - 8
	}
	if cfg.AcceptBacklog <= 0 {
		cfg.AcceptBacklog = 1
	}
	cfg.MaxConnReceiveWindow = cfg.normalizedMaxConnReceiveWindow()
	cfg.MaxSessionReceiveWindow = cfg.normalizedMaxSessionReceiveWindow()
	if cfg.PingTimeout < 0 {
		cfg.PingTimeout = 0
	}
	if cfg.MinPingTimeout < 0 {
		cfg.MinPingTimeout = 0
	}
	if cfg.OpenTimeout < 0 {
		cfg.OpenTimeout = 0
	}
	if cfg.SocketKeepAlive < 0 {
		cfg.SocketKeepAlive = 0
	} else if cfg.SocketKeepAlive == 0 {
		cfg.SocketKeepAlive = SocketKeepAlive
	}
	if cfg.CloseTimeout < 0 {
		cfg.CloseTimeout = 0
	} else if cfg.CloseTimeout == 0 {
		cfg.CloseTimeout = CloseTimeout
	}
	if cfg.PingTimeoutMultiplier <= 0 {
		cfg.PingTimeoutMultiplier = PingTimeoutMultiplier
	}
	cfg.WriteQueueHighWater, cfg.WriteQueueLowWater = normalizeWriteQueueWatermarks(cfg.WriteQueueHighWater, cfg.WriteQueueLowWater)
	return cfg
}

func (cfg MuxConfig) normalizedMaxConnReceiveWindow() uint32 {
	n := cfg.MaxConnReceiveWindow
	if n < defaultInitialConnWindow {
		n = defaultInitialConnWindow
	}
	if n > mask31 {
		n = mask31
	}
	return n
}

func (cfg MuxConfig) normalizedMaxSessionReceiveWindow() uint64 {
	n := cfg.MaxSessionReceiveWindow
	min := uint64(cfg.normalizedMaxConnReceiveWindow())
	if n < min {
		n = min
	}
	if n > uint64(mask31) {
		n = uint64(mask31)
	}
	return n
}

func normalizedMaxConnReceiveWindow() uint32 {
	return normalizeMuxConfig(DefaultMuxConfig()).MaxConnReceiveWindow
}

func normalizedMaxSessionReceiveWindow() uint64 {
	return normalizeMuxConfig(DefaultMuxConfig()).MaxSessionReceiveWindow
}

func normalizeWriteQueueWatermarks(high, low uint64) (uint64, uint64) {
	if high == 0 {
		high = WriteQueueHighWater
	}
	if low == 0 {
		low = WriteQueueLowWater
	}
	if high < 2 {
		high = 2
	}
	if low == 0 {
		low = 1
	}
	if low >= high {
		low = high / 2
		if low == 0 {
			low = 1
		}
	}
	return high, low
}
