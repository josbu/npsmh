package mux

import (
	"math"
	"sync/atomic"
	"time"
)

func (s *Mux) addSessionReceive(n uint32) {
	if n == 0 {
		return
	}
	atomic.AddUint64(&s.sessionRecvQueued, uint64(n))
}

func (s *Mux) releaseSessionReceive(n uint32) {
	if n == 0 {
		return
	}
	atomic.AddUint64(&s.sessionRecvQueued, ^(uint64(n) - 1))
}

func (s *Mux) sessionReceiveWindowForConn(buffered uint32) uint32 {
	limit := s.sessionRecvLimit
	queued := atomic.LoadUint64(&s.sessionRecvQueued)
	if queued >= limit {
		if buffered == 0 {
			return defaultInitialConnWindow
		}
		if buffered > mask31 {
			return mask31
		}
		return buffered
	}
	allowed := uint64(buffered) + (limit - queued)
	if buffered == 0 && allowed < uint64(defaultInitialConnWindow) {
		allowed = uint64(defaultInitialConnWindow)
	}
	if allowed > uint64(mask31) {
		allowed = uint64(mask31)
	}
	return uint32(allowed)
}

func (s *Mux) maxConnReceiveWindow() uint32 {
	if s == nil {
		return normalizedMaxConnReceiveWindow()
	}
	if s.config.MaxConnReceiveWindow == 0 {
		return normalizedMaxConnReceiveWindow()
	}
	return s.config.MaxConnReceiveWindow
}

func (s *Mux) AcceptBacklogLen() int {
	return len(s.newConnCh)
}

func (s *Mux) AcceptBacklogCap() int {
	return cap(s.newConnCh)
}

func (s *Mux) WriteQueueLen() uint64 {
	return s.writeQueue.Len()
}

func (s *Mux) NumStreams() int {
	if s == nil || s.connMap == nil {
		return 0
	}
	return s.connMap.Size()
}

func (s *Mux) SessionReceiveQueued() uint64 {
	return atomic.LoadUint64(&s.sessionRecvQueued)
}

func (s *Mux) SessionReceiveLimit() uint64 {
	return s.sessionRecvLimit
}

func (s *Mux) observeLatency(rtt time.Duration) {
	if s == nil || rtt <= 0 {
		return
	}
	atomic.StoreUint64(&s.lastLatency, uint64(rtt))
	for {
		prev := atomic.LoadUint64(&s.peakLatency)
		if uint64(rtt) <= prev {
			break
		}
		if atomic.CompareAndSwapUint64(&s.peakLatency, prev, uint64(rtt)) {
			break
		}
	}
	if s.counter == nil {
		return
	}
	sec := float64(rtt) / float64(time.Second)
	atomic.StoreUint64(&s.latency, math.Float64bits(s.counter.Latency(sec)))
}

func (s *Mux) Latency() time.Duration {
	if s == nil {
		return 0
	}
	sec := math.Float64frombits(atomic.LoadUint64(&s.latency))
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

func (s *Mux) LastLatency() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(atomic.LoadUint64(&s.lastLatency))
}

func (s *Mux) PeakLatency() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(atomic.LoadUint64(&s.peakLatency))
}

func (s *Mux) ResetPeakLatency() time.Duration {
	if s == nil {
		return 0
	}
	last := atomic.LoadUint64(&s.lastLatency)
	prev := atomic.SwapUint64(&s.peakLatency, last)
	return time.Duration(prev)
}

func (s *Mux) EffectivePingTimeout() time.Duration {
	return s.effectivePingTimeout()
}
