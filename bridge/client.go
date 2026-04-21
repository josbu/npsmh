package bridge

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/pool"
	"github.com/djylb/nps/lib/rate"
)

type SelectMode int32

const (
	Primary SelectMode = iota
	RoundRobin
	Random
)

var selectModeNames = [...]string{
	"Primary",
	"RoundRobin",
	"Random",
}

func nameOf(m SelectMode) string {
	if i := int(m); i >= 0 && i < len(selectModeNames) {
		return selectModeNames[i]
	}
	return fmt.Sprintf("%d", m)
}

var ClientSelectMode = Primary

func SetClientSelectMode(v any) error {
	const (
		minMode = int32(Primary)
		maxMode = int32(Random)
	)

	var mode SelectMode
	var bad bool

	switch x := v.(type) {
	case SelectMode:
		mode = x
	case int:
		mode = SelectMode(x)
	case int32:
		mode = SelectMode(x)
	case int64:
		mode = SelectMode(int(x))
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		switch s {
		case "", "primary", "p":
			mode = Primary
		case "roundrobin", "round", "rr":
			mode = RoundRobin
		case "random", "rand":
			mode = Random
		default:
			n, err := strconv.Atoi(s)
			if err != nil {
				bad = true
			} else {
				mode = SelectMode(n)
			}
		}
	default:
		bad = true
	}

	if int32(mode) < minMode || int32(mode) > maxMode {
		bad = true
	}

	if bad {
		ClientSelectMode = Primary
		logs.Warn("Invalid client select mode %v; fallback to %s(%d)", v, nameOf(Primary), Primary)
		return fmt.Errorf("invalid select mode %v: fallback to Primary", v)
	}
	ClientSelectMode = mode
	logs.Info("Client select mode set to %s(%d)", nameOf(mode), mode)
	return nil
}

const retryTimeMax = 3
const connectGraceProtectWindow = 3 * time.Second
const nodeJoinGraceProtectWindow = 3 * time.Second

type Node struct {
	mu        sync.RWMutex
	Client    *Client
	UUID      string
	Version   string
	BaseVer   int
	signal    *conn.Conn
	tunnel    any //*mux.Mux or *quic.Conn
	retryTime int
	joinNano  int64
	stats     *nodeRuntimeStats
	statsRef  atomic.Pointer[nodeRuntimeStats]
}

type NodeRuntimeSnapshot struct {
	UUID                   string
	Version                string
	BaseVer                int
	RemoteAddr             string
	LocalAddr              string
	HasSignal              bool
	HasTunnel              bool
	Online                 bool
	ConnectedAt            int64
	NowConn                int32
	BridgeInBytes          int64
	BridgeOutBytes         int64
	BridgeTotalBytes       int64
	ServiceInBytes         int64
	ServiceOutBytes        int64
	ServiceTotalBytes      int64
	TotalInBytes           int64
	TotalOutBytes          int64
	TotalBytes             int64
	BridgeNowRateInBps     int64
	BridgeNowRateOutBps    int64
	BridgeNowRateTotalBps  int64
	ServiceNowRateInBps    int64
	ServiceNowRateOutBps   int64
	ServiceNowRateTotalBps int64
	TotalNowRateInBps      int64
	TotalNowRateOutBps     int64
	TotalNowRateTotalBps   int64
}

type nodeSnapshotState struct {
	uuid        string
	version     string
	baseVer     int
	connectedAt int64
	signal      *conn.Conn
	hasTunnel   bool
	online      bool
	stats       *nodeRuntimeStats
}

func NewNode(uuid, vs string, bv int) *Node {
	node := &Node{
		UUID:     uuid,
		Version:  vs,
		BaseVer:  bv,
		joinNano: time.Now().UnixNano(),
		stats:    newNodeRuntimeStats(),
	}
	node.statsRef.Store(node.stats)
	return node
}

type nodeTrafficStats struct {
	ExportBytes int64
	InletBytes  int64
}

func (s *nodeTrafficStats) Add(in, out int64) {
	if s == nil {
		return
	}
	if in != 0 {
		atomic.AddInt64(&s.InletBytes, in)
	}
	if out != 0 {
		atomic.AddInt64(&s.ExportBytes, out)
	}
}

func (s *nodeTrafficStats) Snapshot() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	in := atomic.LoadInt64(&s.InletBytes)
	out := atomic.LoadInt64(&s.ExportBytes)
	return in, out, in + out
}

func (s *nodeTrafficStats) Reset() {
	if s == nil {
		return
	}
	atomic.StoreInt64(&s.InletBytes, 0)
	atomic.StoreInt64(&s.ExportBytes, 0)
}

type nodeRuntimeStats struct {
	nowConn        int32
	bridgeTraffic  nodeTrafficStats
	serviceTraffic nodeTrafficStats
	bridgeMeter    *rate.Meter
	serviceMeter   *rate.Meter
}

func newNodeRuntimeStats() *nodeRuntimeStats {
	return &nodeRuntimeStats{
		bridgeMeter:  rate.NewMeter(),
		serviceMeter: rate.NewMeter(),
	}
}

func (s *nodeRuntimeStats) ensure() {
	if s == nil {
		return
	}
	if s.bridgeMeter == nil {
		s.bridgeMeter = rate.NewMeter()
	}
	if s.serviceMeter == nil {
		s.serviceMeter = rate.NewMeter()
	}
}

func (s *nodeRuntimeStats) AddConn() {
	if s == nil {
		return
	}
	atomic.AddInt32(&s.nowConn, 1)
}

func (s *nodeRuntimeStats) CutConn() {
	if s == nil {
		return
	}
	for {
		current := atomic.LoadInt32(&s.nowConn)
		if current <= 0 {
			if atomic.CompareAndSwapInt32(&s.nowConn, current, 0) {
				return
			}
			continue
		}
		if atomic.CompareAndSwapInt32(&s.nowConn, current, current-1) {
			return
		}
	}
}

func (s *nodeRuntimeStats) ConnCount() int32 {
	if s == nil {
		return 0
	}
	return atomic.LoadInt32(&s.nowConn)
}

func (s *nodeRuntimeStats) ObserveBridgeTraffic(in, out int64) {
	if s == nil || (in == 0 && out == 0) {
		return
	}
	s.ensure()
	s.bridgeTraffic.Add(in, out)
	s.bridgeMeter.Add(in, out)
}

func (s *nodeRuntimeStats) ObserveServiceTraffic(in, out int64) {
	if s == nil || (in == 0 && out == 0) {
		return
	}
	s.ensure()
	s.serviceTraffic.Add(in, out)
	s.serviceMeter.Add(in, out)
}

func (s *nodeRuntimeStats) BridgeTrafficTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.bridgeTraffic.Snapshot()
}

func (s *nodeRuntimeStats) ServiceTrafficTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.serviceTraffic.Snapshot()
}

func (s *nodeRuntimeStats) TotalTrafficTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	bridgeIn, bridgeOut, _ := s.BridgeTrafficTotals()
	serviceIn, serviceOut, _ := s.ServiceTrafficTotals()
	totalIn := bridgeIn + serviceIn
	totalOut := bridgeOut + serviceOut
	return totalIn, totalOut, totalIn + totalOut
}

func (s *nodeRuntimeStats) BridgeRateTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.ensure()
	return s.bridgeMeter.Snapshot()
}

func (s *nodeRuntimeStats) ServiceRateTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.ensure()
	return s.serviceMeter.Snapshot()
}

func (s *nodeRuntimeStats) TotalRateTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	bridgeIn, bridgeOut, _ := s.BridgeRateTotals()
	serviceIn, serviceOut, _ := s.ServiceRateTotals()
	totalIn := bridgeIn + serviceIn
	totalOut := bridgeOut + serviceOut
	return totalIn, totalOut, totalIn + totalOut
}

func (s *nodeRuntimeStats) Reset() {
	if s == nil {
		return
	}
	atomic.StoreInt32(&s.nowConn, 0)
	s.bridgeTraffic.Reset()
	s.serviceTraffic.Reset()
	if s.bridgeMeter != nil {
		s.bridgeMeter.Reset()
	}
	if s.serviceMeter != nil {
		s.serviceMeter.Reset()
	}
}

func (n *Node) touchJoinTime() {
	n.joinNano = time.Now().UnixNano()
}

func (n *Node) ensureStatsLocked() *nodeRuntimeStats {
	if n == nil {
		return nil
	}
	if n.stats == nil {
		n.stats = newNodeRuntimeStats()
	}
	n.stats.ensure()
	n.statsRef.Store(n.stats)
	return n.stats
}

func (n *Node) runtimeStats() *nodeRuntimeStats {
	if n == nil {
		return nil
	}
	if stats := n.statsRef.Load(); stats != nil && stats.bridgeMeter != nil && stats.serviceMeter != nil {
		return stats
	}
	n.mu.Lock()
	stats := n.ensureStatsLocked()
	n.mu.Unlock()
	return stats
}

func (n *Node) resetRuntimeStatsLocked() {
	if stats := n.ensureStatsLocked(); stats != nil {
		stats.Reset()
	}
}

func (n *Node) isOfflineLocked() bool {
	if n == nil {
		return true
	}
	if n.BaseVer < 5 {
		return n.isTunnelClosed() && (n.signal == nil || n.signal.IsClosed()) && (n.Client == nil || n.Client.Id > 0)
	}
	return !n.isOnline()
}

func (n *Node) AddNode(node *Node) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.isOfflineLocked() {
		n.resetRuntimeStatsLocked()
	}
	if node.Version != "" {
		n.Version = node.Version
	}
	if node.BaseVer != 0 {
		n.BaseVer = node.BaseVer
	}
	n.touchJoinTime()
	if node.signal != nil {
		n.addSignal(node.signal)
	}
	if node.tunnel != nil {
		n.addTunnel(node.tunnel)
	}
}

func (n *Node) AddSignal(signal *conn.Conn) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addSignal(signal)
}

func (n *Node) addSignal(signal *conn.Conn) {
	if n.signal != nil && n.signal != signal {
		_ = n.signal.Close()
	}
	n.signal = signal
	n.touchJoinTime()
}

func (n *Node) AddTunnel(tunnel any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.addTunnel(tunnel)
}

func (n *Node) addTunnel(tunnel any) {
	tunnel = normalizeBridgeRuntimeTunnel(tunnel)
	if n.tunnel != tunnel {
		_ = n.closeTunnel("override")
		n.tunnel = tunnel
		n.touchJoinTime()
	}
}

func (n *Node) InJoinGraceWindow(window time.Duration) bool {
	if window <= 0 {
		return false
	}
	n.mu.RLock()
	v := n.joinNano
	n.mu.RUnlock()
	if v <= 0 {
		return false
	}
	return time.Since(time.Unix(0, v)) < window
}

func (n *Node) GetSignal() *conn.Conn {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.signal
}

func (n *Node) GetTunnel() any {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return normalizeBridgeRuntimeTunnel(n.tunnel)
}

func (n *Node) IsOnline() bool {
	if n == nil {
		return false
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.isOnline()
}

func (n *Node) isOnline() bool {
	if n == nil {
		return false
	}
	if n.Client != nil && n.Client.Id < 0 {
		return true
	}
	return !n.isTunnelClosed() && n.signal != nil && !n.signal.IsClosed()
}

func (n *Node) IsTunnelClosed() bool {
	if n == nil {
		return true
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.isTunnelClosed()
}

func (n *Node) isTunnelClosed() bool {
	return bridgeRuntimeTunnelClosed(n.tunnel)
}

func (n *Node) IsOffline() bool {
	if n == nil {
		return true
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.isOfflineLocked()
}

func (n *Node) shouldRemoveOffline(joinWindow time.Duration, ignoreRetry bool) bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if joinWindow > 0 && n.joinNano > 0 && time.Since(time.Unix(0, n.joinNano)) < joinWindow {
		return false
	}
	if !n.isOfflineLocked() {
		return false
	}
	if !ignoreRetry && n.retryTime < retryTimeMax {
		n.retryTime++
		return false
	}
	return true
}

func (n *Node) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeLocked("node close")
	return nil
}

func (n *Node) CloseIfSignalCurrent(signal *conn.Conn) bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.signal != signal {
		return false
	}
	n.closeLocked("node close")
	return true
}

func (n *Node) CloseIfTunnelCurrent(tunnel any) bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.tunnel != tunnel {
		return false
	}
	n.closeLocked("node close")
	return true
}

func (n *Node) CloseIfOffline() bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.isOfflineLocked() {
		return false
	}
	n.closeLocked("node close")
	return true
}

func (n *Node) closeLocked(err string) {
	_ = n.closeTunnel(err)
	if n.signal != nil {
		_ = n.signal.Close()
		n.signal = nil
	}
	n.retryTime = retryTimeMax
}

func (n *Node) Snapshot() NodeRuntimeSnapshot {
	if n == nil {
		return NodeRuntimeSnapshot{}
	}
	state := n.snapshotState()
	snapshot := NodeRuntimeSnapshot{
		UUID:        state.uuid,
		Version:     state.version,
		BaseVer:     state.baseVer,
		ConnectedAt: state.connectedAt,
	}
	if state.signal != nil && !state.signal.IsClosed() {
		snapshot.HasSignal = true
		if remote := state.signal.RemoteAddr(); remote != nil {
			snapshot.RemoteAddr = remote.String()
		}
		if local := state.signal.LocalAddr(); local != nil {
			snapshot.LocalAddr = local.String()
		}
	}
	snapshot.HasTunnel = state.hasTunnel
	snapshot.Online = state.online
	populateNodeSnapshotTraffic(&snapshot, state.stats)
	return snapshot
}

func (n *Node) snapshotState() nodeSnapshotState {
	if n == nil {
		return nodeSnapshotState{}
	}
	n.mu.RLock()
	defer n.mu.RUnlock()

	state := nodeSnapshotState{
		uuid:        n.UUID,
		version:     n.Version,
		baseVer:     n.BaseVer,
		connectedAt: n.joinNano,
		signal:      n.signal,
		hasTunnel:   n.tunnel != nil && !n.isTunnelClosed(),
		online:      !n.isOfflineLocked(),
		stats:       n.statsRef.Load(),
	}
	if state.stats == nil {
		state.stats = n.stats
	}
	return state
}

func populateNodeSnapshotTraffic(snapshot *NodeRuntimeSnapshot, stats *nodeRuntimeStats) {
	if snapshot == nil || stats == nil {
		return
	}
	snapshot.NowConn = stats.ConnCount()
	snapshot.BridgeInBytes, snapshot.BridgeOutBytes, snapshot.BridgeTotalBytes = stats.BridgeTrafficTotals()
	snapshot.ServiceInBytes, snapshot.ServiceOutBytes, snapshot.ServiceTotalBytes = stats.ServiceTrafficTotals()
	snapshot.TotalInBytes, snapshot.TotalOutBytes, snapshot.TotalBytes = stats.TotalTrafficTotals()
	snapshot.BridgeNowRateInBps, snapshot.BridgeNowRateOutBps, snapshot.BridgeNowRateTotalBps = stats.BridgeRateTotals()
	snapshot.ServiceNowRateInBps, snapshot.ServiceNowRateOutBps, snapshot.ServiceNowRateTotalBps = stats.ServiceRateTotals()
	snapshot.TotalNowRateInBps, snapshot.TotalNowRateOutBps, snapshot.TotalNowRateTotalBps = stats.TotalRateTotals()
}

func (n *Node) AddConn() {
	if n == nil {
		return
	}
	stats := n.runtimeStats()
	if stats != nil {
		stats.AddConn()
	}
}

func (n *Node) CutConn() {
	if n == nil {
		return
	}
	stats := n.runtimeStats()
	if stats != nil {
		stats.CutConn()
	}
}

func (n *Node) ObserveBridgeTraffic(in, out int64) {
	if n == nil || (in == 0 && out == 0) {
		return
	}
	stats := n.runtimeStats()
	if stats != nil {
		stats.ObserveBridgeTraffic(in, out)
	}
}

func (n *Node) ObserveServiceTraffic(in, out int64) {
	if n == nil || (in == 0 && out == 0) {
		return
	}
	stats := n.runtimeStats()
	if stats != nil {
		stats.ObserveServiceTraffic(in, out)
	}
}

func (n *Node) closeTunnel(err string) error {
	if n.tunnel != nil {
		_ = closeBridgeRuntimeTunnel(n.tunnel, err)
		n.tunnel = nil
	}
	return nil
}

type Client struct {
	mu              sync.RWMutex
	Id              int
	LastUUID        string
	lastUUIDRef     atomic.Pointer[clientLastUUIDState]
	nodeList        *pool.Pool[string] // nodeUUID
	nodes           sync.Map           // map[nodeUUID]*Node
	files           sync.Map           // map[fileUUID]string or *runtimeOwnerPool[string]
	fileOwnerKeys   map[string]map[string]struct{}
	pingRetryTime   int // bridge ping failure budget before closing the runtime client
	closed          uint32
	lastConnectNano int64
	closeNodeHook   func(int, string)
}

type clientLastUUIDState struct {
	value string
}

type clientNodeLookupState uint8

const (
	clientNodeLookupMissing clientNodeLookupState = iota
	clientNodeLookupReady
	clientNodeLookupInvalid
)

type clientNodeLookupResult struct {
	node    *Node
	state   clientNodeLookupState
	cleaned bool
}

type clientNodeCandidateAction uint8

const (
	clientNodeCandidateMissing clientNodeCandidateAction = iota
	clientNodeCandidateInvalid
	clientNodeCandidateReady
	clientNodeCandidateDefer
	clientNodeCandidatePrune
)

type clientNodeCandidate struct {
	uuid    string
	node    *Node
	action  clientNodeCandidateAction
	cleaned bool
}

type clientNodeRef struct {
	uuid string
	node *Node
}

type clientPingHealthState uint8

const (
	clientPingHealthUnavailable clientPingHealthState = iota
	clientPingHealthHealthy
	clientPingHealthEmpty
)

type clientPingHealth struct {
	state clientPingHealthState
}

func NewClient(id int, n *Node) *Client {
	c := &Client{
		Id:            id,
		nodeList:      pool.New[string](),
		fileOwnerKeys: make(map[string]map[string]struct{}),
	}
	c.assignCurrentNodeUUID(n.UUID)
	c.MarkConnectedNow()
	n.Client = c
	c.nodes.Store(n.UUID, n)
	c.nodeList.Add(n.UUID)
	return c
}

func (c *Client) AddNode(n *Node) {
	if n == nil {
		return
	}
	c.mu.Lock()
	if v, ok := c.nodes.Load(n.UUID); ok {
		existing, ok := v.(*Node)
		if !ok || existing == nil {
			c.dropNodeRuntimeLocked(n.UUID, true)
		} else {
			if existing.IsOnline() && n.BaseVer < 6 {
				c.mu.Unlock()
				_ = n.Close()
				return
			}
			existing.AddNode(n)
			c.assignCurrentNodeUUID(n.UUID)
			c.mu.Unlock()
			return
		}
	}
	n.Client = c
	c.nodes.Store(n.UUID, n)
	c.nodeList.Add(n.UUID)
	c.assignCurrentNodeUUID(n.UUID)
	c.mu.Unlock()
}

func (c *Client) MarkConnectedNow() {
	atomic.StoreInt64(&c.lastConnectNano, time.Now().UnixNano())
}

func (c *Client) SetCloseNodeHook(hook func(int, string)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closeNodeHook = hook
	c.mu.Unlock()
}

func (c *Client) InConnectGraceWindow(window time.Duration) bool {
	if window <= 0 {
		return false
	}
	v := atomic.LoadInt64(&c.lastConnectNano)
	if v <= 0 {
		return false
	}
	return time.Since(time.Unix(0, v)) < window
}

func (c *Client) currentNodeUUID() string {
	if c == nil {
		return ""
	}
	if state := c.lastUUIDRef.Load(); state != nil {
		return state.value
	}
	c.mu.RLock()
	current := c.LastUUID
	c.mu.RUnlock()
	if current != "" {
		c.lastUUIDRef.CompareAndSwap(nil, &clientLastUUIDState{value: current})
	}
	return current
}

func (c *Client) currentNodeIfOnline() *Node {
	if c == nil {
		return nil
	}
	current := strings.TrimSpace(c.currentNodeUUID())
	if current == "" {
		return nil
	}
	result := c.lookupNodeByUUID(current)
	if result.state != clientNodeLookupReady || result.node == nil || result.node.IsOffline() {
		return nil
	}
	return result.node
}

func (c *Client) assignCurrentNodeUUID(uuid string) {
	if c == nil {
		return
	}
	c.LastUUID = uuid
	if uuid == "" {
		c.lastUUIDRef.Store(nil)
		return
	}
	c.lastUUIDRef.Store(&clientLastUUIDState{value: uuid})
}

func (c *Client) setCurrentNodeUUID(uuid string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.assignCurrentNodeUUID(uuid)
	c.mu.Unlock()
}

func (c *Client) nextNodeUUIDByMode() string {
	if c == nil || c.nodeList == nil {
		return ""
	}
	switch ClientSelectMode {
	case Random:
		uuid, _ := c.nodeList.Random()
		return uuid
	case Primary, RoundRobin:
		fallthrough
	default:
		uuid, _ := c.nodeList.Next()
		return uuid
	}
}

func (c *Client) nextDistinctNodeUUID(exclude string) string {
	if c == nil || c.nodeList == nil {
		return ""
	}
	exclude = strings.TrimSpace(exclude)
	attempts := c.nodeList.Size()
	if attempts == 0 {
		return ""
	}
	for i := 0; i < attempts; i++ {
		next := strings.TrimSpace(c.nextNodeUUIDByMode())
		if next != "" && next != exclude {
			return next
		}
	}
	var fallback string
	c.nodeList.Range(func(uuid string) bool {
		uuid = strings.TrimSpace(uuid)
		if uuid == "" || uuid == exclude {
			return true
		}
		fallback = uuid
		return false
	})
	return fallback
}

func (c *Client) currentOrNextNodeUUID() string {
	if current := c.currentNodeUUID(); current != "" {
		return current
	}
	nextUUID := c.nextNodeUUIDByMode()
	if nextUUID == "" {
		return ""
	}
	c.mu.Lock()
	if c.LastUUID == "" {
		c.assignCurrentNodeUUID(nextUUID)
	}
	current := c.LastUUID
	c.mu.Unlock()
	return current
}

func (c *Client) AddFile(key, uuid string) error {
	_, err := c.AddFileOwner(key, uuid)
	return err
}

func (c *Client) AddFileOwner(key, uuid string) (bool, error) {
	uuid = strings.TrimSpace(uuid)
	if result := c.lookupNodeByUUID(uuid); result.state != clientNodeLookupReady {
		return false, fmt.Errorf("uuid %q not found", uuid)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	nextValue, added := addFileOwnerToRoute(c.loadFileRouteLocked(key), uuid)
	if !added {
		return false, nil
	}
	c.files.Store(key, nextValue)
	c.trackFileRouteOwnersLocked(key, nextValue)
	return true, nil
}

func addFileOwnerToRoute(current interface{}, uuid string) (interface{}, bool) {
	switch current := current.(type) {
	case nil:
		return uuid, true
	case string:
		current = strings.TrimSpace(current)
		switch {
		case current == "":
			return uuid, true
		case current == uuid:
			return current, false
		default:
			pool := newRuntimeOwnerPool[string]()
			pool.set(current, current)
			pool.set(uuid, uuid)
			return pool, true
		}
	case *runtimeOwnerPool[string]:
		if current == nil {
			return uuid, true
		}
		if current.has(uuid) {
			return current, false
		}
		current.set(uuid, uuid)
		return current, true
	default:
		return uuid, true
	}
}

func removeFileOwnerFromRoute(current interface{}, uuid string) (keep bool, removed bool) {
	switch current := current.(type) {
	case nil:
		return false, false
	case string:
		current = strings.TrimSpace(current)
		if current == "" {
			return false, true
		}
		return current != uuid, current == uuid
	case *runtimeOwnerPool[string]:
		if current == nil {
			return false, true
		}
		remaining, removed := current.remove(uuid)
		if !removed {
			return true, false
		}
		return remaining > 0, true
	default:
		return false, true
	}
}

func (c *Client) RemoveFile(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeFileRouteLocked(key)
}

func (c *Client) RemoveFileOwner(key, uuid string) bool {
	if c == nil || key == "" || uuid == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeFileOwnerLocked(key, uuid)
}

func (c *Client) deleteFileMappingIfCurrent(key string, value interface{}) bool {
	if c == nil || key == "" || value == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.files.CompareAndDelete(key, value) {
		return false
	}
	c.untrackFileRouteOwnersLocked(key, value)
	return true
}

func (c *Client) GetNodeByFile(key string) (*Node, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	v, ok := c.files.Load(key)
	if !ok {
		return nil, false
	}
	switch current := v.(type) {
	case string:
		uuid := strings.TrimSpace(current)
		if uuid == "" {
			c.deleteFileMappingIfCurrent(key, v)
			return nil, false
		}
		return c.resolveFileRouteNode(key, uuid)
	case *runtimeOwnerPool[string]:
		return c.resolveFileRouteOwnerPool(key, v, current)
	default:
		c.deleteFileMappingIfCurrent(key, v)
		return nil, false
	}
}

func (c *Client) resolveFileRouteOwnerPool(key string, mapping interface{}, owners *runtimeOwnerPool[string]) (*Node, bool) {
	if owners == nil {
		c.deleteFileMappingIfCurrent(key, mapping)
		return nil, false
	}
	uuid, _, attempts, ok := owners.selectNextWithCount()
	if !ok {
		c.deleteFileMappingIfCurrent(key, mapping)
		return nil, false
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if node, ok := c.resolveFileRouteNode(key, strings.TrimSpace(uuid)); ok {
			return node, true
		}
		if attempt+1 >= attempts {
			break
		}
		nextUUID, _, nextOK := owners.selectNext()
		if !nextOK {
			break
		}
		uuid = nextUUID
	}
	return nil, false
}

func (c *Client) resolveFileRouteNode(key, uuid string) (*Node, bool) {
	candidate := c.evaluateNodeCandidate(uuid, false)
	switch candidate.action {
	case clientNodeCandidateReady:
		return candidate.node, true
	case clientNodeCandidateDefer:
		return nil, false
	case clientNodeCandidateMissing:
		c.removeFileOwner(key, uuid)
		c.pruneMissingNodeUUID(uuid)
		return nil, false
	case clientNodeCandidateInvalid:
		c.removeFileOwner(key, uuid)
		return nil, false
	case clientNodeCandidatePrune:
		c.removeFileOwner(key, uuid)
		c.closeAndRemoveNodeIfCurrent(uuid, candidate.node)
		return nil, false
	default:
		return nil, false
	}
}

func (c *Client) evaluateNodeCandidate(uuid string, allowJoinGrace bool) clientNodeCandidate {
	result := c.lookupNodeByUUID(uuid)
	candidate := clientNodeCandidate{
		uuid:    strings.TrimSpace(uuid),
		node:    result.node,
		cleaned: result.cleaned,
	}
	switch result.state {
	case clientNodeLookupMissing:
		candidate.action = clientNodeCandidateMissing
	case clientNodeLookupInvalid:
		candidate.action = clientNodeCandidateInvalid
	case clientNodeLookupReady:
		if result.node != nil && !result.node.IsOffline() {
			candidate.action = clientNodeCandidateReady
			return candidate
		}
		if c.InConnectGraceWindow(connectGraceProtectWindow) {
			candidate.action = clientNodeCandidateDefer
			return candidate
		}
		if allowJoinGrace && result.node != nil && result.node.InJoinGraceWindow(nodeJoinGraceProtectWindow) {
			candidate.action = clientNodeCandidateDefer
			return candidate
		}
		candidate.action = clientNodeCandidatePrune
	default:
		candidate.action = clientNodeCandidateMissing
	}
	return candidate
}

func (c *Client) CheckNode() *Node {
	size := c.nodeList.Size()
	if size == 0 {
		logs.Warn("Client %d has no nodes to switch to", c.Id)
		return nil
	}
	if node := c.currentNodeIfOnline(); node != nil {
		return node
	}
	first := true
	graceChecksLeft := size
	for {
		lastUUID := c.currentOrNextNodeUUID()
		if lastUUID == "" {
			logs.Warn("Client %d has no nodes to switch to", c.Id)
			return nil
		}
		candidate := c.evaluateNodeCandidate(lastUUID, true)
		switch candidate.action {
		case clientNodeCandidateReady:
			if !first {
				logs.Info("Client %d switched to backup node %s", c.Id, lastUUID)
			}
			return candidate.node
		case clientNodeCandidateDefer:
			first = false
			graceChecksLeft--
			if graceChecksLeft <= 0 {
				return nil
			}
			c.setCurrentNodeUUID(c.nextDistinctNodeUUID(lastUUID))
			continue
		case clientNodeCandidateInvalid:
			if candidate.cleaned {
				logs.Info("Client %d removed invalid node entry %s", c.Id, lastUUID)
			}
		case clientNodeCandidateMissing:
			if c.pruneMissingNodeUUID(lastUUID) {
				logs.Info("Client %d pruned missing node uuid %s", c.Id, lastUUID)
			}
		case clientNodeCandidatePrune:
		default:
		}
		first = false
		if candidate.action == clientNodeCandidatePrune && c.closeAndRemoveNodeIfCurrent(lastUUID, candidate.node) {
			logs.Info("Client %d removed node %s", c.Id, lastUUID)
		}
	}
}

func (c *Client) GetNode() *Node {
	if ClientSelectMode != Primary {
		c.setCurrentNodeUUID(c.nextNodeUUIDByMode())
	}
	return c.CheckNode()
}

func (c *Client) GetNodeByUUID(uuid string) (*Node, bool) {
	result := c.lookupNodeByUUID(uuid)
	return result.node, result.state == clientNodeLookupReady
}

func (c *Client) HasOnlineNode() bool {
	if c != nil && c.currentNodeIfOnline() != nil {
		return true
	}
	return c.onlineNodeCount(1) > 0
}

func (c *Client) SnapshotNodes() []NodeRuntimeSnapshot {
	return c.snapshotNodes(false)
}

func (c *Client) OnlineNodeSnapshots() []NodeRuntimeSnapshot {
	return c.snapshotNodes(true)
}

func (c *Client) snapshotNodes(onlineOnly bool) []NodeRuntimeSnapshot {
	if c == nil {
		return nil
	}
	snapshots := c.collectNodeSnapshots(onlineOnly)
	sortNodeSnapshots(snapshots, c.snapshotCurrentNodeUUID())
	return snapshots
}

func (c *Client) DisplayRuntimeSnapshot() (NodeRuntimeSnapshot, bool) {
	if c == nil {
		return NodeRuntimeSnapshot{}, false
	}
	best := NodeRuntimeSnapshot{}
	found := false
	c.forEachValidNode(func(uuid string, node *Node) bool {
		snapshot := node.Snapshot()
		if !snapshot.Online {
			return true
		}
		if !found ||
			snapshot.ConnectedAt > best.ConnectedAt ||
			(snapshot.ConnectedAt == best.ConnectedAt && snapshot.UUID < best.UUID) {
			best = snapshot
			found = true
		}
		return true
	})
	return best, found
}

func (c *Client) NodeCount() int {
	return c.nodeList.Size()
}

func (c *Client) OnlineNodeCount() int {
	if c != nil && c.nodeList != nil && c.nodeList.Size() == 1 && c.currentNodeIfOnline() != nil {
		return 1
	}
	return c.onlineNodeCount(0)
}

func (c *Client) HasMultipleOnlineNodes() bool {
	if c == nil || c.nodeList == nil || c.nodeList.Size() <= 1 {
		return false
	}
	return c.onlineNodeCount(2) > 1
}

func (c *Client) collectNodeSnapshots(onlineOnly bool) []NodeRuntimeSnapshot {
	if c == nil {
		return nil
	}
	snapshots := make([]NodeRuntimeSnapshot, 0, c.NodeCount())
	c.forEachValidNode(func(uuid string, node *Node) bool {
		snapshot := node.Snapshot()
		if onlineOnly && !snapshot.Online {
			return true
		}
		snapshots = append(snapshots, snapshot)
		return true
	})
	return snapshots
}

func (c *Client) snapshotCurrentNodeUUID() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	current := c.LastUUID
	c.mu.RUnlock()
	if current == "" {
		c.lastUUIDRef.Store(nil)
		return ""
	}
	if state := c.lastUUIDRef.Load(); state == nil || state.value != current {
		c.lastUUIDRef.Store(&clientLastUUIDState{value: current})
	}
	return current
}

func sortNodeSnapshots(snapshots []NodeRuntimeSnapshot, currentUUID string) {
	sort.SliceStable(snapshots, func(i, j int) bool {
		return lessNodeSnapshot(currentUUID, snapshots[i], snapshots[j])
	})
}

func lessNodeSnapshot(currentUUID string, left, right NodeRuntimeSnapshot) bool {
	leftCurrent := left.UUID == currentUUID
	rightCurrent := right.UUID == currentUUID
	if leftCurrent != rightCurrent {
		return leftCurrent && !rightCurrent
	}
	if left.ConnectedAt != right.ConnectedAt {
		return left.ConnectedAt > right.ConnectedAt
	}
	return left.UUID < right.UUID
}

func (c *Client) onlineNodeCount(limit int) int {
	if c == nil {
		return 0
	}
	count := 0
	c.forEachValidNode(func(uuid string, node *Node) bool {
		if node.IsOffline() {
			return true
		}
		count++
		if limit > 0 && count >= limit {
			return false
		}
		return true
	})
	return count
}

func (c *Client) collectPingHealth() clientPingHealth {
	if c == nil {
		return clientPingHealth{state: clientPingHealthUnavailable}
	}
	c.RemoveOfflineNodes(false)
	if c.NodeCount() == 0 {
		return clientPingHealth{state: clientPingHealthEmpty}
	}
	if c.CheckNode() == nil {
		return clientPingHealth{state: clientPingHealthUnavailable}
	}
	return clientPingHealth{state: clientPingHealthHealthy}
}

func (c *Client) notePingUnavailable() (retries int, shouldClose bool) {
	if c == nil {
		return 0, false
	}
	c.mu.Lock()
	c.pingRetryTime++
	retries = c.pingRetryTime
	c.mu.Unlock()
	return retries, retries >= retryTimeMax
}

func (c *Client) resetPingRetry() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.pingRetryTime = 0
	c.mu.Unlock()
}

func (c *Client) RemoveOfflineNodes(ignoreClientGrace bool) (removed int) {
	return c.removeOfflineNodes("", false, ignoreClientGrace)
}

func (c *Client) RemoveOfflineNodesExcept(keepUUID string, ignoreClientGrace bool) (removed int) {
	return c.removeOfflineNodes(keepUUID, false, ignoreClientGrace)
}

func (c *Client) removeOfflineNodes(keepUUID string, ignoreNodeRetry bool, ignoreClientGrace bool) (removed int) {
	if c.nodeList.Size() == 0 {
		return 0
	}
	if c.shouldSkipOfflineNodePrune(ignoreNodeRetry, ignoreClientGrace) {
		return 0
	}
	toRemove := c.collectOfflineNodesToRemove(keepUUID, ignoreNodeRetry)
	if len(toRemove) == 0 {
		return 0
	}
	for _, it := range toRemove {
		if c.closeAndRemoveNodeIfCurrent(it.uuid, it.node) {
			removed++
			logs.Info("Client %d removed offline node %s", c.Id, it.uuid)
		}
	}
	if removed > 0 {
		logs.Info("Client %d pruned %d offline node(s)", c.Id, removed)
	}
	return removed
}

func (c *Client) shouldSkipOfflineNodePrune(ignoreNodeRetry bool, ignoreClientGrace bool) bool {
	if c == nil {
		return true
	}
	return !ignoreNodeRetry && !ignoreClientGrace && c.InConnectGraceWindow(connectGraceProtectWindow)
}

func (c *Client) shouldRemoveOfflineNode(uuid, keepUUID string, node *Node, ignoreNodeRetry bool) bool {
	if c == nil || uuid == "" || node == nil || uuid == keepUUID {
		return false
	}
	return node.shouldRemoveOffline(nodeJoinGraceProtectWindow, ignoreNodeRetry)
}

func (c *Client) collectOfflineNodesToRemove(keepUUID string, ignoreNodeRetry bool) []clientNodeRef {
	if c == nil {
		return nil
	}
	toRemove := make([]clientNodeRef, 0)
	c.forEachValidNode(func(uuid string, node *Node) bool {
		if c.shouldRemoveOfflineNode(uuid, keepUUID, node, ignoreNodeRetry) {
			toRemove = append(toRemove, clientNodeRef{uuid: uuid, node: node})
		}
		return true
	})
	return toRemove
}

func (c *Client) lookupNodeByUUID(uuid string) clientNodeLookupResult {
	uuid = strings.TrimSpace(uuid)
	if c == nil || uuid == "" {
		return clientNodeLookupResult{state: clientNodeLookupMissing}
	}
	raw, ok := c.nodes.Load(uuid)
	if !ok {
		return clientNodeLookupResult{state: clientNodeLookupMissing}
	}
	node, ok := raw.(*Node)
	if ok && node != nil {
		return clientNodeLookupResult{node: node, state: clientNodeLookupReady}
	}
	return clientNodeLookupResult{
		state:   clientNodeLookupInvalid,
		cleaned: c.removeNodeEntryIfCurrent(uuid, raw),
	}
}

func (c *Client) forEachValidNode(fn func(uuid string, node *Node) bool) {
	if c == nil || fn == nil {
		return
	}
	c.nodes.Range(func(key, value interface{}) bool {
		uuid, ok := key.(string)
		if !ok {
			c.nodes.CompareAndDelete(key, value)
			return true
		}
		node, ok := value.(*Node)
		if !ok || node == nil {
			c.removeNodeEntryIfCurrent(uuid, value)
			return true
		}
		return fn(uuid, node)
	})
}

func (c *Client) closeAndRemoveNodeIfCurrent(uuid string, node *Node) bool {
	if c == nil || node == nil || uuid == "" {
		return false
	}
	var hook func(int, string)
	var clientID int
	c.mu.Lock()
	current, ok := c.nodes.Load(uuid)
	if !ok || current != node || !node.IsOffline() {
		c.mu.Unlock()
		return false
	}
	c.dropNodeRuntimeLocked(uuid, true)
	hook = c.closeNodeHook
	clientID = c.Id
	c.mu.Unlock()
	_ = node.Close()
	c.pruneFileMappings(uuid)
	if hook != nil {
		hook(clientID, uuid)
	}
	return true
}

func (c *Client) removeNodeEntryIfCurrent(uuid string, value interface{}) bool {
	if c == nil || uuid == "" || value == nil {
		return false
	}
	c.mu.Lock()
	current, ok := c.nodes.Load(uuid)
	if !ok || current != value {
		c.mu.Unlock()
		return false
	}
	c.dropNodeRuntimeLocked(uuid, true)
	c.mu.Unlock()
	c.pruneFileMappings(uuid)
	return true
}

func (c *Client) pruneMissingNodeUUID(uuid string) bool {
	if c == nil || uuid == "" {
		return false
	}
	c.mu.Lock()
	if _, ok := c.nodes.Load(uuid); ok {
		c.mu.Unlock()
		return false
	}
	if c.nodeList == nil || !c.nodeList.Has(uuid) {
		c.mu.Unlock()
		return false
	}
	c.dropNodeRuntimeLocked(uuid, false)
	c.mu.Unlock()
	c.pruneFileMappings(uuid)
	return true
}

func (c *Client) removeNodeLocked(uuid string) {
	c.dropNodeRuntimeLocked(uuid, true)
}

func (c *Client) dropNodeRuntimeLocked(uuid string, removeNodeEntry bool) {
	if c == nil || uuid == "" {
		return
	}
	if removeNodeEntry {
		c.nodes.Delete(uuid)
	}
	c.nodeList.Remove(uuid)
	c.advanceCurrentNodeLocked(uuid)
}

func (c *Client) advanceCurrentNodeLocked(uuid string) {
	if c == nil || uuid == "" || c.LastUUID != uuid {
		return
	}
	if next, ok := c.nodeList.Next(); ok {
		c.assignCurrentNodeUUID(next)
		return
	}
	c.assignCurrentNodeUUID("")
}

func (c *Client) IsClosed() bool {
	return atomic.LoadUint32(&c.closed) == 1
}

func (c *Client) Close() error {
	if !atomic.CompareAndSwapUint32(&c.closed, 0, 1) {
		return nil
	}
	closeDetachedNodes(c.detachNodesForClose())
	return nil
}

func (c *Client) detachNodesForClose() []*Node {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	size := 0
	if c.nodeList != nil {
		size = c.nodeList.Size()
	}
	nodes := make([]*Node, 0, size)
	c.nodes.Range(func(key, value interface{}) bool {
		uuid, okKey := key.(string)
		n, ok := value.(*Node)
		if !okKey || !ok || n == nil {
			if okKey {
				c.dropNodeRuntimeLocked(uuid, true)
			} else {
				c.nodes.CompareAndDelete(key, value)
			}
			return true
		}
		nodes = append(nodes, n)
		return true
	})
	c.assignCurrentNodeUUID("")
	c.nodeList.Clear(nil)
	c.nodes = sync.Map{}
	c.files = sync.Map{}
	c.fileOwnerKeys = nil
	return nodes
}

func closeDetachedNodes(nodes []*Node) {
	for _, node := range nodes {
		if node != nil {
			_ = node.Close()
		}
	}
}

func (c *Client) loadFileRouteLocked(key string) interface{} {
	if c == nil || key == "" {
		return nil
	}
	value, ok := c.files.Load(key)
	if !ok {
		return nil
	}
	return value
}

func (c *Client) removeFileOwner(key, uuid string) bool {
	if c == nil || key == "" || uuid == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeFileOwnerLocked(key, uuid)
}

func (c *Client) removeFileOwnerLocked(key, uuid string) bool {
	value, ok := c.files.Load(key)
	if !ok {
		return false
	}
	keep, removed := removeFileOwnerFromRoute(value, uuid)
	if !removed {
		return false
	}
	c.untrackFileOwnerLocked(key, uuid)
	if !keep {
		c.files.Delete(key)
	}
	return true
}

func (c *Client) pruneFileMappings(uuid string) {
	if c == nil || uuid == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range c.collectFileMappingKeysByOwnerLocked(uuid) {
		c.removeFileOwnerLocked(key, uuid)
	}
}

func nextFileRouteUUID(value interface{}) (string, bool) {
	switch current := value.(type) {
	case string:
		uuid := strings.TrimSpace(current)
		return uuid, uuid != ""
	case *runtimeOwnerPool[string]:
		if current == nil {
			return "", false
		}
		uuid, _, ok := current.selectNext()
		uuid = strings.TrimSpace(uuid)
		return uuid, ok && uuid != ""
	default:
		return "", false
	}
}

func (c *Client) removeFileRouteLocked(key string) bool {
	if c == nil || key == "" {
		return false
	}
	value, ok := c.files.Load(key)
	if !ok {
		return false
	}
	c.files.Delete(key)
	c.untrackFileRouteOwnersLocked(key, value)
	return true
}

func (c *Client) ensureFileOwnerKeysLocked() {
	if c == nil {
		return
	}
	if c.fileOwnerKeys == nil {
		c.fileOwnerKeys = make(map[string]map[string]struct{})
	}
}

func (c *Client) trackFileRouteOwnersLocked(key string, value interface{}) {
	if c == nil || key == "" {
		return
	}
	for _, uuid := range fileRouteOwnerUUIDs(value) {
		c.trackFileOwnerLocked(key, uuid)
	}
}

func (c *Client) untrackFileRouteOwnersLocked(key string, value interface{}) {
	if c == nil || key == "" {
		return
	}
	for _, uuid := range fileRouteOwnerUUIDs(value) {
		c.untrackFileOwnerLocked(key, uuid)
	}
}

func (c *Client) trackFileOwnerLocked(key, uuid string) {
	if c == nil {
		return
	}
	key = strings.TrimSpace(key)
	uuid = strings.TrimSpace(uuid)
	if key == "" || uuid == "" {
		return
	}
	c.ensureFileOwnerKeysLocked()
	keys := c.fileOwnerKeys[uuid]
	if keys == nil {
		keys = make(map[string]struct{})
		c.fileOwnerKeys[uuid] = keys
	}
	keys[key] = struct{}{}
}

func (c *Client) untrackFileOwnerLocked(key, uuid string) {
	if c == nil || c.fileOwnerKeys == nil {
		return
	}
	key = strings.TrimSpace(key)
	uuid = strings.TrimSpace(uuid)
	if key == "" || uuid == "" {
		return
	}
	keys := c.fileOwnerKeys[uuid]
	if len(keys) == 0 {
		delete(c.fileOwnerKeys, uuid)
		return
	}
	delete(keys, key)
	if len(keys) == 0 {
		delete(c.fileOwnerKeys, uuid)
	}
}

func (c *Client) collectFileMappingKeysByOwnerLocked(uuid string) []string {
	if c == nil || c.fileOwnerKeys == nil {
		return nil
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil
	}
	keys := c.fileOwnerKeys[uuid]
	if len(keys) == 0 {
		delete(c.fileOwnerKeys, uuid)
		return nil
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		if strings.TrimSpace(key) == "" {
			delete(keys, key)
			continue
		}
		result = append(result, key)
	}
	if len(keys) == 0 {
		delete(c.fileOwnerKeys, uuid)
	}
	return result
}

func fileRouteOwnerUUIDs(value interface{}) []string {
	switch current := value.(type) {
	case string:
		uuid := strings.TrimSpace(current)
		if uuid == "" {
			return nil
		}
		return []string{uuid}
	case *runtimeOwnerPool[string]:
		if current == nil {
			return nil
		}
		keys := current.snapshotKeys()
		if len(keys) == 0 {
			return nil
		}
		result := make([]string, 0, len(keys))
		for _, uuid := range keys {
			uuid = strings.TrimSpace(uuid)
			if uuid == "" {
				continue
			}
			result = append(result, uuid)
		}
		return result
	default:
		return nil
	}
}

type runtimeOwnerPool[T any] struct {
	mu      sync.RWMutex
	order   []string
	entries map[string]T
	next    int
}

func newRuntimeOwnerPool[T any]() *runtimeOwnerPool[T] {
	return &runtimeOwnerPool[T]{
		entries: make(map[string]T),
	}
}

func (p *runtimeOwnerPool[T]) count() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.order)
}

func (p *runtimeOwnerPool[T]) set(uuid string, value T) {
	if p == nil || uuid == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = make(map[string]T)
	}
	if _, exists := p.entries[uuid]; !exists {
		p.order = append(p.order, uuid)
	}
	p.entries[uuid] = value
	p.normalizeNextLocked()
}

func (p *runtimeOwnerPool[T]) has(uuid string) bool {
	if p == nil || uuid == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.entries == nil {
		return false
	}
	_, ok := p.entries[uuid]
	return ok
}

func (p *runtimeOwnerPool[T]) remove(uuid string) (int, bool) {
	if p == nil || uuid == "" {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.entries[uuid]; !exists {
		return len(p.order), false
	}
	delete(p.entries, uuid)
	index := p.indexOfLocked(uuid)
	if index >= 0 {
		p.removeOrderIndexLocked(index)
	}
	p.normalizeNextLocked()
	return len(p.order), true
}

func (p *runtimeOwnerPool[T]) selectNext() (string, T, bool) {
	var zero T
	if p == nil {
		return "", zero, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	uuid, value, _, ok := p.selectNextLocked()
	return uuid, value, ok
}

func (p *runtimeOwnerPool[T]) selectNextWithCount() (string, T, int, bool) {
	var zero T
	if p == nil {
		return "", zero, 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selectNextLocked()
}

func (p *runtimeOwnerPool[T]) selectNextLocked() (string, T, int, bool) {
	var zero T
	if p == nil {
		return "", zero, 0, false
	}
	count := len(p.order)
	if count == 0 {
		return "", zero, 0, false
	}
	p.normalizeNextLocked()
	start := p.next
	for i := 0; i < count; i++ {
		index := (start + i) % count
		uuid := p.order[index]
		value, ok := p.entries[uuid]
		if !ok {
			continue
		}
		p.next = (index + 1) % count
		return uuid, value, count, true
	}
	return "", zero, count, false
}

func (p *runtimeOwnerPool[T]) normalizeNextLocked() {
	if p == nil || len(p.order) == 0 {
		if p != nil {
			p.next = 0
		}
		return
	}
	if p.next < 0 || p.next >= len(p.order) {
		p.next = 0
	}
}

func (p *runtimeOwnerPool[T]) indexOfLocked(uuid string) int {
	if p == nil || uuid == "" {
		return -1
	}
	for index, current := range p.order {
		if current == uuid {
			return index
		}
	}
	return -1
}

func (p *runtimeOwnerPool[T]) removeOrderIndexLocked(index int) {
	if p == nil || index < 0 || index >= len(p.order) {
		return
	}
	last := len(p.order) - 1
	copy(p.order[index:], p.order[index+1:])
	p.order[last] = ""
	p.order = p.order[:last]
	if len(p.order) == 0 {
		p.next = 0
		return
	}
	if p.next > index {
		p.next--
		return
	}
	if p.next >= len(p.order) {
		p.next = 0
	}
}

func (p *runtimeOwnerPool[T]) snapshotKeys() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.order) == 0 {
		return nil
	}
	keys := make([]string, 0, len(p.order))
	for _, uuid := range p.order {
		if uuid == "" {
			continue
		}
		if _, ok := p.entries[uuid]; !ok {
			continue
		}
		keys = append(keys, uuid)
	}
	return keys
}
