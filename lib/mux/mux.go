package mux

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	muxPingFlag uint8 = iota
	muxNewConnOk
	muxNewConnFail
	muxNewMsg
	muxNewMsgPart
	muxMsgSendOk
	muxNewConn
	muxConnClose
	muxPingReturn
	muxPeerHello
	muxConnCloseWrite
	muxPing            int32 = -1
	maximumSegmentSize       = poolSizeWindow
	maximumWindowSize        = 1 << 27 // 1<<31-1 TCP slide window size is very large,
	// we use 128M, reduce memory usage
)

var (
	PingInterval  = 5 * time.Second
	PingJitter    = 2 * time.Second
	PingMaxPad    = 16
	AcceptBacklog = 1024
)

const (
	capabilityCloseWrite uint32 = 1 << iota
	counterBits                 = 4
	counterMask                 = 1<<counterBits - 1
)

// ConnMap stores mux streams by stream id.
//
// Deprecated: kept for legacy compatibility. Most callers should use Mux
// methods instead of manipulating stream maps directly.
type ConnMap struct {
	cMap map[int32]*Conn
	sync.RWMutex
}

// NewConnMap allocates an empty ConnMap.
func NewConnMap() *ConnMap {
	return &ConnMap{cMap: make(map[int32]*Conn)}
}

func (s *ConnMap) Size() (n int) {
	s.RLock()
	n = len(s.cMap)
	s.RUnlock()
	return
}

func (s *ConnMap) Get(id int32) (*Conn, bool) {
	s.RLock()
	v, ok := s.cMap[id]
	s.RUnlock()
	if ok && v != nil {
		return v, true
	}
	return nil, false
}

func (s *ConnMap) Set(id int32, v *Conn) {
	s.Lock()
	s.cMap[id] = v
	s.Unlock()
}

func (s *ConnMap) Close() {
	s.RLock()
	copyMap := make([]*Conn, 0, len(s.cMap))
	for _, v := range s.cMap {
		copyMap = append(copyMap, v)
	}
	s.RUnlock()

	for _, v := range copyMap {
		_ = v.Close()
	}
}

func (s *ConnMap) Delete(id int32) {
	s.Lock()
	delete(s.cMap, id)
	s.Unlock()
}

func defaultLocalCapabilities() uint32 {
	return capabilityCloseWrite
}

func isMuxStreamDataFlag(flag uint8) bool {
	return flag == muxNewMsg || flag == muxNewMsgPart
}

func isMuxStreamOrderedFlag(flag uint8) bool {
	return isMuxStreamDataFlag(flag) || isMuxOrderedCloseFlag(flag)
}

func isMuxImmediateFlushFlag(flag uint8) bool {
	switch flag {
	case muxPingFlag, muxPingReturn, muxPeerHello, muxNewConn, muxNewConnOk, muxNewConnFail, muxConnClose, muxConnCloseWrite:
		return true
	default:
		return false
	}
}

func isMuxOrderedCloseFlag(flag uint8) bool {
	return flag == muxConnClose || flag == muxConnCloseWrite
}

func (s *Mux) sendPeerHello() {
	if s.localCapabilities == 0 {
		return
	}
	s.sendInfo(muxPeerHello, int32(s.localCapabilities), true, nil)
}

func (s *Mux) setRemoteCapabilities(capabilities uint32) {
	atomic.StoreUint32(&s.remoteCapabilities, capabilities)
}

func (s *Mux) supportsRemoteCapability(capability uint32) bool {
	return atomic.LoadUint32(&s.remoteCapabilities)&capability == capability
}

// Bandwidth tracks inbound throughput estimates for adaptive receive windows.
//
// Deprecated: kept for legacy compatibility with older integration points.
type Bandwidth struct {
	readBandwidth uint64 // store in bits, but it's float64
	readStart     time.Time
	lastReadStart time.Time
	bufLength     uint32
	calcThreshold uint32
}

// NewBandwidth creates a Bandwidth estimator for c.
func NewBandwidth(c net.Conn, connType string) *Bandwidth {
	return &Bandwidth{calcThreshold: bandwidthCalcThreshold(c, connType)}
}

func (Self *Bandwidth) StartRead() {
	if Self.readStart.IsZero() {
		Self.readStart = time.Now()
	}
	if Self.bufLength >= Self.calcThreshold {
		Self.lastReadStart, Self.readStart = Self.readStart, time.Now()
		Self.calcBandWidth()
	}
}

func (Self *Bandwidth) SetCopySize(n uint16) {
	Self.bufLength += uint32(n)
}

func (Self *Bandwidth) calcBandWidth() {
	t := Self.readStart.Sub(Self.lastReadStart)
	if t <= 0 {
		Self.bufLength = 0
		return
	}
	if Self.bufLength >= Self.calcThreshold {
		atomic.StoreUint64(&Self.readBandwidth, math.Float64bits(float64(Self.bufLength)/t.Seconds()))
	}
	Self.bufLength = 0
}

func (Self *Bandwidth) Get() (bw float64) {
	bw = math.Float64frombits(atomic.LoadUint64(&Self.readBandwidth))
	if bw <= 0 {
		bw = 0
	}
	return
}

func (Self *Bandwidth) Close() error {
	return nil
}

func newLatencyCounter() *latencyCounter {
	return &latencyCounter{
		buf:     make([]float64, 1<<counterBits),
		headMin: 0,
	}
}

type latencyCounter struct {
	buf     []float64
	headMin uint8
}

func (Self *latencyCounter) unpack(idx uint8) (head, min uint8) {
	head = (idx >> counterBits) & counterMask
	min = idx & counterMask
	return
}

func (Self *latencyCounter) pack(head, min uint8) uint8 {
	return head<<counterBits | min&counterMask
}

func (Self *latencyCounter) add(value float64) {
	head, minIndex := Self.unpack(Self.headMin)
	Self.buf[head] = value
	if head == minIndex {
		minIndex = Self.minimal()
	}
	if Self.buf[minIndex] <= 0 || Self.buf[minIndex] > value {
		minIndex = head
	}
	head++
	Self.headMin = Self.pack(head, minIndex)
}

func (Self *latencyCounter) minimal() (min uint8) {
	var i uint8
	var found bool
	for i = 0; i <= counterMask; i++ {
		if Self.buf[i] > 0 {
			if !found || Self.buf[i] < Self.buf[min] {
				min = i
				found = true
			}
		}
	}
	return
}

func (Self *latencyCounter) Latency(value float64) (latency float64) {
	Self.add(value)
	return Self.countSuccess()
}

const lossRatio = 3

func (Self *latencyCounter) countSuccess() (successRate float64) {
	var i, success uint8
	_, minIndex := Self.unpack(Self.headMin)
	if Self.buf[minIndex] <= 0 {
		return 0
	}
	for i = 0; i <= counterMask; i++ {
		if Self.buf[i] <= lossRatio*Self.buf[minIndex] && Self.buf[i] > 0 {
			success++
			successRate += Self.buf[i]
		}
	}
	if success == 0 {
		return 0
	}
	return successRate / float64(success)
}

var muxRandState uint64 = seedMuxRand()

func seedMuxRand() uint64 {
	var seed uint64
	if err := binary.Read(cryptorand.Reader, binary.LittleEndian, &seed); err == nil && seed != 0 {
		return seed
	}
	return uint64(time.Now().UnixNano()) | 1
}

func nextMuxRand() uint64 {
	for {
		cur := atomic.LoadUint64(&muxRandState)
		if cur == 0 {
			cur = seedMuxRand()
		}
		next := cur
		next ^= next << 13
		next ^= next >> 7
		next ^= next << 17
		if next == 0 {
			next = 1
		}
		if atomic.CompareAndSwapUint64(&muxRandState, cur, next) {
			return next
		}
	}
}

func randIntn(n int) int {
	if n <= 1 {
		return 0
	}
	return int(nextMuxRand() % uint64(n))
}

func randInt63n(n int64) int64 {
	if n <= 1 {
		return 0
	}
	return int64(nextMuxRand() % uint64(n))
}

func fillRandomBytes(buf []byte) {
	for i := 0; i < len(buf); {
		v := nextMuxRand()
		for j := 0; j < 8 && i < len(buf); j++ {
			buf[i] = byte(v)
			v >>= 8
			i++
		}
	}
}

// Mux multiplexes logical streams over a single ordered byte-stream transport.
type Mux struct {
	latency           uint64 // smoothed latency in float64 bits
	lastLatency       uint64 // last observed RTT in nanoseconds
	peakLatency       uint64 // historical max RTT in nanoseconds
	lastAliveTime     int64
	sessionRecvQueued uint64
	net.Listener
	conn               net.Conn
	connMap            *ConnMap
	newConnCh          chan *Conn
	localCapabilities  uint32
	remoteCapabilities uint32
	id                 int32
	isInitiator        bool
	closeChan          chan struct{}
	counter            *latencyCounter
	bw                 *Bandwidth
	pingCh             chan *muxPackager
	pingTimeout        time.Duration
	openTimeout        time.Duration
	connType           string
	config             MuxConfig
	writeQueue         priorityQueue
	sessionRecvLimit   uint64
	once               sync.Once
	reasonMu           sync.RWMutex
	closeReason        string
}

// NewMux creates a mux using the current default configuration snapshot.
func NewMux(c net.Conn, connType string, pingCheckThreshold int, isInitiator bool) *Mux {
	return NewMuxWithConfig(c, connType, pingCheckThreshold, isInitiator, DefaultMuxConfig())
}

// NewMuxWithConfig creates a mux over c without changing the wire protocol.
func NewMuxWithConfig(c net.Conn, connType string, pingCheckThreshold int, isInitiator bool, cfg MuxConfig) *Mux {
	cfg = normalizeMuxConfig(cfg)
	applySocketOptions(c, cfg)
	pingTimeout := resolvePingTimeout(cfg, connType, pingCheckThreshold)
	cfg.PingTimeout = pingTimeout
	cfg.MinPingTimeout = resolveMinPingTimeout(cfg, pingTimeout)
	cfg.ReadTimeout = resolveIOTimeout(cfg.ReadTimeout, pingTimeout)
	cfg.WriteTimeout = resolveIOTimeout(cfg.WriteTimeout, pingTimeout)
	var startId int32
	if isInitiator {
		startId = -1
	} else {
		startId = 0
	}
	m := &Mux{
		conn:              c,
		connMap:           NewConnMap(),
		id:                startId,
		closeChan:         make(chan struct{}, 1),
		newConnCh:         make(chan *Conn, cfg.AcceptBacklog),
		localCapabilities: defaultLocalCapabilities(),
		bw:                NewBandwidth(c, connType),
		connType:          connType,
		pingCh:            make(chan *muxPackager),
		pingTimeout:       pingTimeout,
		openTimeout:       resolveOpenTimeout(cfg, connType, pingTimeout),
		config:            cfg,
		lastAliveTime:     time.Now().UnixNano(),
		counter:           newLatencyCounter(),
		sessionRecvLimit:  cfg.MaxSessionReceiveWindow,
	}
	m.writeQueue.New()
	m.writeQueue.ConfigureWatermarks(cfg.WriteQueueHighWater, cfg.WriteQueueLowWater)
	//read session by flag
	m.readSession()
	//ping
	m.ping()
	m.writeSession()
	watchConnDone(c, func(reason string) {
		_ = m.closeWithReason(reason)
	})
	m.sendPeerHello()
	return m
}

func (s *Mux) NewConn() (*Conn, error) {
	return s.NewConnTimeout(0)
}

func (s *Mux) NewConnTimeout(timeout time.Duration) (*Conn, error) {
	if s == nil {
		return nil, net.ErrClosed
	}
	if timeout <= 0 {
		timeout = s.openTimeout
	}
	return s.newConnWithTimeout(timeout)
}

func (s *Mux) newConnWithTimeout(timeout time.Duration) (*Conn, error) {
	if s.IsClosed() {
		return nil, s.closedErr("the mux has closed")
	}
	conn := NewConn(s.getId(), s)
	// it must be Set before send
	s.connMap.Set(conn.connId, conn)
	if err := s.sendInfo(muxNewConn, conn.connId, false, nil); err != nil {
		conn.closeLocal()
		return nil, err
	}
	//Set a timer timeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-conn.connStatusOkCh:
		return conn, nil
	case <-conn.connStatusFailCh:
		conn.closeLocal()
		return nil, errors.New("create connection fail, the server refused the connection")

	case <-s.closeChan:
		conn.closeLocal()
		return nil, s.closedErr("create connection fail, the mux has closed")

	case <-timer.C:
		conn.closeLocal()
		return nil, errors.New("create connection timeout")
	}
}

func (s *Mux) Accept() (net.Conn, error) {
	if s == nil {
		return nil, net.ErrClosed
	}
	if s.IsClosed() {
		return nil, s.closedErr("accept error: the mux has closed")
	}
	select {
	case <-s.closeChan:
		return nil, s.closedErr("accept error: the mux has closed")
	case conn, ok := <-s.newConnCh:
		if s.IsClosed() {
			return nil, s.closedErr("accept error: the mux has closed")
		}
		if !ok || conn == nil {
			return nil, errors.New("accept error: the connection has been closed")
		}
		return conn, nil
	}
}

func (s *Mux) Addr() net.Addr {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

func resolvePingTimeout(cfg MuxConfig, connType string, pingCheckThreshold int) time.Duration {
	if pingCheckThreshold > 0 {
		return time.Duration(pingCheckThreshold) * time.Second
	}
	if cfg.PingTimeout > 0 {
		return cfg.PingTimeout
	}
	var timeout time.Duration
	if normalizedConnType(connType) == "kcp" {
		timeout = cfg.PingInterval + 2*cfg.PingJitter
	} else {
		timeout = 2 * (cfg.PingInterval + cfg.PingJitter)
	}
	if maxTimeout := 10 * (cfg.PingInterval + cfg.PingJitter); timeout > maxTimeout {
		timeout = maxTimeout
	}
	if timeout <= cfg.PingInterval+cfg.PingJitter {
		timeout = 30 * time.Second
	}
	return timeout
}

func resolveMinPingTimeout(cfg MuxConfig, maxTimeout time.Duration) time.Duration {
	timeout := cfg.MinPingTimeout
	if timeout <= 0 {
		timeout = cfg.PingInterval + cfg.PingJitter
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	if maxTimeout > 0 && timeout > maxTimeout {
		timeout = maxTimeout
	}
	return timeout
}

func resolveOpenTimeout(cfg MuxConfig, connType string, pingTimeout time.Duration) time.Duration {
	if cfg.OpenTimeout > 0 {
		return cfg.OpenTimeout
	}
	timeout := pingTimeout
	if normalizedConnType(connType) == "kcp" {
		if timeout > 8*time.Second {
			timeout = 8 * time.Second
		}
	} else {
		if timeout > 15*time.Second {
			timeout = 15 * time.Second
		}
	}
	if timeout < 8*time.Second {
		timeout = 8 * time.Second
	}
	return timeout
}

func (s *Mux) markAlive() {
	atomic.StoreInt64(&s.lastAliveTime, time.Now().UnixNano())
}

func (s *Mux) isAliveTimeout(now time.Time) bool {
	last := atomic.LoadInt64(&s.lastAliveTime)
	if last <= 0 {
		return false
	}
	return now.Sub(time.Unix(0, last)) > s.effectivePingTimeout()
}

func absoluteDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (s *Mux) nextPingDelay() time.Duration {
	if s.config.PingInterval <= 0 {
		return time.Second
	}
	jitter := s.config.PingJitter
	if jitter == 0 {
		return s.config.PingInterval
	}
	j := time.Duration(randInt63n(int64(jitter))) - jitter/2
	next := s.config.PingInterval + j
	if next <= 0 {
		return s.config.PingInterval
	}
	return next
}

func (s *Mux) effectivePingTimeout() time.Duration {
	if s == nil {
		return 0
	}
	maxTimeout := s.pingTimeout
	if maxTimeout <= 0 {
		maxTimeout = 30 * time.Second
	}
	if s.config.DisableAdaptivePingTimeout {
		return maxTimeout
	}
	peak := s.PeakLatency()
	if peak <= 0 {
		return maxTimeout
	}

	minTimeout := resolveMinPingTimeout(s.config, maxTimeout)
	adaptive := time.Duration(math.Ceil(float64(peak) * s.config.PingTimeoutMultiplier))
	if adaptive < minTimeout {
		adaptive = minTimeout
	}
	if adaptive > maxTimeout {
		adaptive = maxTimeout
	}
	if adaptive <= 0 {
		return maxTimeout
	}
	return adaptive
}

func (s *Mux) IsClosed() bool {
	if s == nil {
		return true
	}
	select {
	case <-s.closeChan:
		return true
	default:
		return false
	}
}

func (s *Mux) Close() (err error) {
	if s == nil {
		return nil
	}
	return s.closeWithReason("")
}

func (s *Mux) CloseChan() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.closeChan
}

func (s *Mux) Config() MuxConfig {
	if s == nil {
		return MuxConfig{}
	}
	return s.config
}

func (s *Mux) CloseReason() string {
	if s == nil {
		return ""
	}
	s.reasonMu.RLock()
	defer s.reasonMu.RUnlock()
	return s.closeReason
}

func (s *Mux) closeWithReason(reason string) (err error) {
	s.setCloseReason(reason)

	if s.IsClosed() {
		return errors.New("the mux has closed")
	}

	s.once.Do(func() {
		close(s.closeChan)
		s.writeQueue.Stop()
		s.connMap.Close()
		if deadline := closeDeadline(s.config); deadline > 0 {
			_ = s.conn.SetDeadline(time.Now().Add(deadline))
		}
		go func() {
			_ = s.conn.Close()
			_ = s.bw.Close()
		}()
		s.release()
	})
	return
}

func (s *Mux) setCloseReason(reason string) string {
	if reason == "" {
		return s.CloseReason()
	}
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	if s.closeReason == "" {
		s.closeReason = reason
	}
	return s.closeReason
}

func (s *Mux) closedErr(prefix string) error {
	if reason := s.CloseReason(); reason != "" {
		return errors.New(prefix + " (" + reason + ")")
	}
	return errors.New(prefix)
}

func (s *Mux) release() {
	for {
		pack := s.writeQueue.TryPop()
		if pack == nil {
			break
		}
		if pack.content != nil {
			windowBuff.Put(pack.content)
		}
		muxPack.Put(pack)
	}
	s.writeQueue.Stop()
}

func (s *Mux) getId() (id int32) {
	curID := atomic.LoadInt32(&s.id)
	if (math.MaxInt32 - curID) < 10000 {
		if s.isInitiator {
			atomic.StoreInt32(&s.id, -1)
		} else {
			atomic.StoreInt32(&s.id, 0)
		}
	}
	id = atomic.AddInt32(&s.id, 2)
	if _, ok := s.connMap.Get(id); ok {
		return s.getId()
	}
	return
}
