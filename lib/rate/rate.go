package rate

import (
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxI64 = int64(^uint64(0) >> 1)
	minI64 = -maxI64 - 1

	sampleIntervalNs = int64(time.Second)

	// throughput-first upper bound
	coalesceWaitNs = int64(200 * time.Microsecond)

	// very short waits: use Sleep (no timer alloc)
	shortWaitNs = int64(2 * time.Millisecond)

	burstWindowNs = int64(2 * time.Second)
)

type stopSignal struct {
	ch chan struct{}
}

type Meter struct {
	lastSampleNs int64
	inAcc        int64
	outAcc       int64
	inBps        int64
	outBps       int64
}

type rateConn struct {
	conn io.ReadWriteCloser
	rate Limiter
}

type Rate struct {
	rate    int64 // bytes/s, <=0 => unlimited
	burstNs int64 // burst window in ns
	tat     int64 // theoretical arrival time (ns since t0), can be negative

	enabled int32
	t0      time.Time

	mu      sync.Mutex
	stopped bool
	stop    atomic.Pointer[stopSignal]

	// approx realtime rate (bytes/s)
	bytesAcc     int64
	lastSampleNs int64
	nowBps       int64
}

type rateJSON struct {
	NowRate int64 `json:"NowRate"` // bytes/s
	Limit   int64 `json:"Limit"`   // bytes/s, 0 => unlimited
}

var timerPool = sync.Pool{
	New: func() any {
		t := time.NewTimer(0)
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		return t
	},
}

func getTimer(d time.Duration) *time.Timer {
	t := timerPool.Get().(*time.Timer)
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
	return t
}

func putTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	timerPool.Put(t)
}

func NewRate(limitBps int64) *Rate {
	if limitBps <= 0 {
		limitBps = 0
	}
	r := &Rate{
		enabled: 1,
		t0:      time.Now(),
		burstNs: burstWindowNs,
	}
	atomic.StoreInt64(&r.rate, limitBps)
	r.stop.Store(&stopSignal{ch: make(chan struct{})})

	if limitBps > 0 {
		atomic.StoreInt64(&r.tat, -r.burstNs) // full burst initially
	} else {
		atomic.StoreInt64(&r.tat, 0)
	}

	now := r.nowNs()
	atomic.StoreInt64(&r.lastSampleNs, now)
	atomic.StoreInt64(&r.nowBps, 0)
	return r
}

func NewMeter() *Meter {
	now := time.Now().UnixNano()
	return &Meter{lastSampleNs: now}
}

func (m *Meter) Clone() *Meter {
	if m == nil {
		return nil
	}
	cloned := &Meter{}
	atomic.StoreInt64(&cloned.lastSampleNs, atomic.LoadInt64(&m.lastSampleNs))
	atomic.StoreInt64(&cloned.inAcc, atomic.LoadInt64(&m.inAcc))
	atomic.StoreInt64(&cloned.outAcc, atomic.LoadInt64(&m.outAcc))
	atomic.StoreInt64(&cloned.inBps, atomic.LoadInt64(&m.inBps))
	atomic.StoreInt64(&cloned.outBps, atomic.LoadInt64(&m.outBps))
	return cloned
}

func (m *Meter) Add(in, out int64) {
	if m == nil {
		return
	}
	if in != 0 {
		atomic.AddInt64(&m.inAcc, in)
	}
	if out != 0 {
		atomic.AddInt64(&m.outAcc, out)
	}
	m.roll(time.Now().UnixNano())
}

func (m *Meter) Snapshot() (int64, int64, int64) {
	if m == nil {
		return 0, 0, 0
	}
	m.roll(time.Now().UnixNano())
	inBps := atomic.LoadInt64(&m.inBps)
	outBps := atomic.LoadInt64(&m.outBps)
	return inBps, outBps, inBps + outBps
}

func (m *Meter) Reset() {
	if m == nil {
		return
	}
	now := time.Now().UnixNano()
	atomic.StoreInt64(&m.lastSampleNs, now)
	atomic.StoreInt64(&m.inAcc, 0)
	atomic.StoreInt64(&m.outAcc, 0)
	atomic.StoreInt64(&m.inBps, 0)
	atomic.StoreInt64(&m.outBps, 0)
}

func (m *Meter) roll(nowNs int64) {
	if m == nil {
		return
	}
	last := atomic.LoadInt64(&m.lastSampleNs)
	if nowNs <= last || nowNs-last < sampleIntervalNs {
		return
	}
	if !atomic.CompareAndSwapInt64(&m.lastSampleNs, last, nowNs) {
		return
	}
	delta := nowNs - last
	if delta <= 0 {
		return
	}
	inBytes := atomic.SwapInt64(&m.inAcc, 0)
	outBytes := atomic.SwapInt64(&m.outAcc, 0)
	atomic.StoreInt64(&m.inBps, inBytes*sampleIntervalNs/delta)
	atomic.StoreInt64(&m.outBps, outBytes*sampleIntervalNs/delta)
}

func NewRateConn(conn io.ReadWriteCloser, rate Limiter) io.ReadWriteCloser {
	return &rateConn{
		conn: conn,
		rate: rate,
	}
}

func (s *rateConn) Read(b []byte) (n int, err error) {
	n, err = s.conn.Read(b)
	if s.rate != nil && n > 0 {
		s.rate.Get(int64(n))
	}
	return
}

func (s *rateConn) Write(b []byte) (n int, err error) {
	if s.rate != nil && len(b) > 0 {
		s.rate.Get(int64(len(b)))
	}
	n, err = s.conn.Write(b)
	if s.rate != nil && len(b) > 0 && n < len(b) {
		s.rate.ReturnBucket(int64(len(b) - n))
	}
	return
}

func (s *rateConn) Close() error {
	return s.conn.Close()
}

func (r *Rate) Clone() *Rate {
	if r == nil {
		return nil
	}
	enabled := atomic.LoadInt32(&r.enabled)
	if r.t0.IsZero() {
		cloned := NewRate(atomic.LoadInt64(&r.rate))
		if enabled == 0 {
			cloned.Stop()
		}
		return cloned
	}

	r.mu.Lock()
	stopped := r.stopped
	burstNs := r.burstNs
	t0 := r.t0
	r.mu.Unlock()

	cloned := &Rate{
		burstNs: burstNs,
		t0:      t0,
		stopped: stopped,
	}
	atomic.StoreInt64(&cloned.rate, atomic.LoadInt64(&r.rate))
	atomic.StoreInt64(&cloned.tat, atomic.LoadInt64(&r.tat))
	atomic.StoreInt32(&cloned.enabled, enabled)
	atomic.StoreInt64(&cloned.bytesAcc, atomic.LoadInt64(&r.bytesAcc))
	atomic.StoreInt64(&cloned.lastSampleNs, atomic.LoadInt64(&r.lastSampleNs))
	atomic.StoreInt64(&cloned.nowBps, atomic.LoadInt64(&r.nowBps))

	signal := &stopSignal{ch: make(chan struct{})}
	if stopped {
		close(signal.ch)
	}
	cloned.stop.Store(signal)
	return cloned
}

func (r *Rate) SetLimit(limitBps int64) {
	if r == nil {
		return
	}
	if limitBps <= 0 {
		limitBps = 0
	}
	atomic.StoreInt64(&r.rate, limitBps)
}

// ResetLimit is a helper: Stop -> SetLimit -> Start.
func (r *Rate) ResetLimit(limitBps int64) {
	if r == nil {
		return
	}
	r.Stop()
	r.SetLimit(limitBps)
	r.Start()
}

func (r *Rate) Limit() int64 {
	if r == nil {
		return 0
	}
	return atomic.LoadInt64(&r.rate)
}

func (r *Rate) Now() int64 {
	if r == nil {
		return 0
	}
	r.updateRateWithNow(r.nowNs())
	return atomic.LoadInt64(&r.nowBps)
}

func (r *Rate) Start() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	prevEnabled := atomic.LoadInt32(&r.enabled) != 0
	needReset := r.stopped || !prevEnabled || r.stop.Load() == nil

	if r.stopped || r.stop.Load() == nil {
		r.stop.Store(&stopSignal{ch: make(chan struct{})})
		r.stopped = false
	}

	if needReset {
		rate := atomic.LoadInt64(&r.rate)
		if rate > 0 {
			now := r.nowNs()
			atomic.StoreInt64(&r.tat, now-r.burstNs) // full burst on (re)enable
		} else {
			atomic.StoreInt64(&r.tat, 0)
		}
		atomic.StoreInt64(&r.bytesAcc, 0)
		now := r.nowNs()
		atomic.StoreInt64(&r.lastSampleNs, now)
		atomic.StoreInt64(&r.nowBps, 0)
	}

	atomic.StoreInt32(&r.enabled, 1)
}

func (r *Rate) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	atomic.StoreInt32(&r.enabled, 0)
	if !r.stopped {
		if s := r.stop.Load(); s != nil && s.ch != nil {
			close(s.ch) // wake sleepers immediately
		}
		r.stopped = true
	}
	atomic.StoreInt64(&r.bytesAcc, 0)
	atomic.StoreInt64(&r.nowBps, 0)
}

func (r *Rate) ReturnBucket(size int64) {
	if r == nil || size <= 0 || atomic.LoadInt32(&r.enabled) == 0 {
		return
	}

	atomic.AddInt64(&r.bytesAcc, -size)

	rate := atomic.LoadInt64(&r.rate)
	if rate <= 0 {
		return
	}

	refund := bytesToNsCeil(size, rate)
	now := r.nowNs()
	minTat := now - r.burstNs

	for {
		prev := atomic.LoadInt64(&r.tat)
		next := clampSub(prev, refund)
		if next < minTat {
			next = minTat
		}
		if atomic.CompareAndSwapInt64(&r.tat, prev, next) {
			return
		}
		if atomic.LoadInt32(&r.enabled) == 0 {
			return
		}
	}
}

func (r *Rate) Get(size int64) {
	wait := r.reserve(size)
	if wait <= coalesceWaitNs {
		return
	}
	sleepNs(wait, r.stopCh())
}

func (r *Rate) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return json.Marshal(rateJSON{
		NowRate: r.Now(),
		Limit:   r.Limit(),
	})
}

func sleepNs(waitNs int64, stopCh <-chan struct{}) {
	if waitNs <= 0 {
		return
	}
	if waitNs <= shortWaitNs || stopCh == nil {
		time.Sleep(time.Duration(waitNs))
		return
	}

	t := getTimer(time.Duration(waitNs))
	select {
	case <-t.C:
	case <-stopCh:
	}
	putTimer(t)
}

func (r *Rate) reserve(size int64) int64 {
	if r == nil || size <= 0 || atomic.LoadInt32(&r.enabled) == 0 {
		return 0
	}

	atomic.AddInt64(&r.bytesAcc, size)

	now := r.nowNs()
	r.updateRateWithNow(now)

	currentRate := atomic.LoadInt64(&r.rate)
	if currentRate <= 0 {
		return 0
	}

	cost := bytesToNsCeil(size, currentRate)

	for {
		minTat := now - r.burstNs

		prev := atomic.LoadInt64(&r.tat)
		base := prev
		if base < minTat {
			base = minTat
		}
		next := clampAdd(base, cost)

		if atomic.CompareAndSwapInt64(&r.tat, prev, next) {
			wait := next - now
			if wait < 0 {
				return 0
			}
			return wait
		}

		if atomic.LoadInt32(&r.enabled) == 0 {
			return 0
		}
		now = r.nowNs()
	}
}

func (r *Rate) stopCh() <-chan struct{} {
	if r == nil {
		return nil
	}
	if s := r.stop.Load(); s != nil {
		return s.ch
	}
	return nil
}

func (r *Rate) updateRateWithNow(now int64) {
	last := atomic.LoadInt64(&r.lastSampleNs)
	if now-last < sampleIntervalNs {
		return
	}
	if !atomic.CompareAndSwapInt64(&r.lastSampleNs, last, now) {
		return
	}

	bytes := atomic.SwapInt64(&r.bytesAcc, 0)
	if bytes < 0 {
		bytes = 0
	}
	dt := now - last
	if dt <= 0 {
		return
	}
	atomic.StoreInt64(&r.nowBps, bytesPerSec(bytes, dt))
}

func (r *Rate) nowNs() int64 {
	return int64(time.Since(r.t0))
}

func bytesToNsCeil(bytes, rate int64) int64 {
	if bytes <= 0 || rate <= 0 {
		return 0
	}
	if bytes > maxI64/1e9 {
		return maxI64
	}
	num := bytes * 1e9
	return (num + rate - 1) / rate
}

func bytesPerSec(bytes, dtNs int64) int64 {
	if bytes <= 0 || dtNs <= 0 {
		return 0
	}
	q := bytes / dtNs
	rem := bytes % dtNs

	if q > maxI64/1e9 {
		return maxI64
	}
	res := q * 1e9

	if rem > 0 {
		if rem > maxI64/1e9 {
			add := maxI64 / dtNs
			if add > maxI64-res {
				return maxI64
			}
			res += add
		} else {
			add := (rem * 1e9) / dtNs
			if add > maxI64-res {
				return maxI64
			}
			res += add
		}
	}
	if res < 0 {
		return maxI64
	}
	return res
}

func clampAdd(a, b int64) int64 {
	if b <= 0 {
		return a
	}
	if a > maxI64-b {
		return maxI64
	}
	return a + b
}

func clampSub(a, b int64) int64 {
	if b <= 0 {
		return a
	}
	if a < minI64+b {
		return minI64
	}
	return a - b
}
