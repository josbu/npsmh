package mux

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Deprecated: Rate is kept for legacy compatibility. Prefer transport-level
// shaping outside of mux when possible.
type Rate struct {
	bucketSize        int64
	bucketSurplusSize int64
	bucketAddSize     int64
	stopChan          chan struct{}
	startOnce         sync.Once
	stopOnce          sync.Once
	// Deprecated: use CurrentRate instead.
	NowRate int64
}

// Deprecated: NewRate is kept for legacy compatibility.
func NewRate(addSize int64) *Rate {
	if addSize < 0 {
		addSize = 0
	}
	return &Rate{
		bucketSize:        addSize * 2,
		bucketSurplusSize: 0,
		bucketAddSize:     addSize,
		stopChan:          make(chan struct{}),
	}
}

func (s *Rate) Start() {
	if s == nil || s.bucketAddSize <= 0 {
		return
	}
	s.startOnce.Do(func() {
		go s.session()
	})
}

func (s *Rate) add(size int64) {
	if s == nil || size <= 0 || s.bucketSize <= 0 {
		return
	}
	for {
		current := atomic.LoadInt64(&s.bucketSurplusSize)
		room := s.bucketSize - current
		if room <= 0 {
			return
		}
		if size > room {
			size = room
		}
		if atomic.CompareAndSwapInt64(&s.bucketSurplusSize, current, current+size) {
			return
		}
	}
}

func (s *Rate) ReturnBucket(size int64) {
	s.add(size)
}

func (s *Rate) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopChan)
	})
}

func (s *Rate) Get(size int64) {
	if s == nil || size <= 0 || s.bucketAddSize <= 0 {
		return
	}
	for size > 0 {
		chunk := size
		if chunk > s.bucketSize {
			chunk = s.bucketSize
		}
		if chunk <= 0 || !s.waitTake(chunk) {
			return
		}
		size -= chunk
	}
}

func (s *Rate) waitTake(size int64) bool {
	if s.tryTake(size) {
		return true
	}
	ticker := time.NewTicker(time.Millisecond * 100)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if s.tryTake(size) {
				return true
			}
		case <-s.stopChan:
			return false
		}
	}
}

func (s *Rate) tryTake(size int64) bool {
	for {
		current := atomic.LoadInt64(&s.bucketSurplusSize)
		if current < size {
			return false
		}
		if atomic.CompareAndSwapInt64(&s.bucketSurplusSize, current, current-size) {
			return true
		}
	}
}

// CurrentRate returns the last rate sample produced by the internal token
// bucket loop.
func (s *Rate) CurrentRate() int64 {
	if s == nil {
		return 0
	}
	return atomic.LoadInt64(&s.NowRate)
}

func (s *Rate) session() {
	ticker := time.NewTicker(time.Second * 1)
	for {
		select {
		case <-ticker.C:
			current := atomic.LoadInt64(&s.bucketSurplusSize)
			var rate int64
			if rs := s.bucketAddSize - current; rs > 0 {
				rate = rs
			} else {
				rate = s.bucketSize - current
			}
			if rate < 0 {
				rate = 0
			}
			atomic.StoreInt64(&s.NowRate, rate)
			s.add(s.bucketAddSize)
		case <-s.stopChan:
			ticker.Stop()
			return
		}
	}
}

// Deprecated: RateConn is kept for legacy compatibility.
type RateConn struct {
	conn net.Conn
	rate *Rate
}

// Deprecated: NewRateConn is kept for legacy compatibility.
func NewRateConn(rate *Rate, conn net.Conn) *RateConn {
	return &RateConn{
		conn: conn,
		rate: rate,
	}
}

func (conn *RateConn) Read(b []byte) (n int, err error) {
	if conn == nil || conn.conn == nil {
		return 0, net.ErrClosed
	}
	n, err = conn.conn.Read(b)
	if conn.rate != nil {
		conn.rate.Get(int64(n))
	}
	return n, err
}

func (conn *RateConn) Write(b []byte) (n int, err error) {
	if conn == nil || conn.conn == nil {
		return 0, net.ErrClosed
	}
	n, err = conn.conn.Write(b)
	if conn.rate != nil {
		conn.rate.Get(int64(n))
	}
	return n, err
}

func (conn *RateConn) LocalAddr() net.Addr {
	if conn == nil || conn.conn == nil {
		return nil
	}
	return conn.conn.LocalAddr()
}

func (conn *RateConn) RemoteAddr() net.Addr {
	if conn == nil || conn.conn == nil {
		return nil
	}
	return conn.conn.RemoteAddr()
}

func (conn *RateConn) SetDeadline(t time.Time) error {
	if conn == nil || conn.conn == nil {
		return net.ErrClosed
	}
	return conn.conn.SetDeadline(t)
}

func (conn *RateConn) SetWriteDeadline(t time.Time) error {
	if conn == nil || conn.conn == nil {
		return net.ErrClosed
	}
	return conn.conn.SetWriteDeadline(t)
}

func (conn *RateConn) SetReadDeadline(t time.Time) error {
	if conn == nil || conn.conn == nil {
		return net.ErrClosed
	}
	return conn.conn.SetReadDeadline(t)
}

func (conn *RateConn) Close() error {
	if conn == nil || conn.conn == nil {
		return net.ErrClosed
	}
	return conn.conn.Close()
}

func (conn *RateConn) WrappedConn() net.Conn {
	if conn == nil {
		return nil
	}
	return conn.conn
}

func (conn *RateConn) GetRawConn() net.Conn {
	if conn == nil {
		return nil
	}
	return conn.conn
}

func (conn *RateConn) CloseChan() <-chan struct{} {
	if conn == nil || conn.conn == nil {
		return nil
	}
	if c, ok := conn.conn.(interface{ CloseChan() <-chan struct{} }); ok {
		return c.CloseChan()
	}
	return nil
}

func (conn *RateConn) Done() <-chan struct{} {
	if conn == nil || conn.conn == nil {
		return nil
	}
	if c, ok := conn.conn.(interface{ Done() <-chan struct{} }); ok {
		return c.Done()
	}
	return nil
}

func (conn *RateConn) Context() context.Context {
	if conn == nil || conn.conn == nil {
		return nil
	}
	if c, ok := conn.conn.(interface{ Context() context.Context }); ok {
		return c.Context()
	}
	return nil
}
