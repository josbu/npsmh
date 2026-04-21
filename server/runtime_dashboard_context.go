package server

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
	"github.com/djylb/nps/server/tool"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	psnet "github.com/shirou/gopsutil/v4/net"
)

type dashboardRuntimeContext struct {
	cache   *memoryDashboardCacheStore
	sampler *dashboardIOSampler
	builder dashboardBuilder
	service dashboardService
}

var runtimeDashboard = newDashboardRuntimeContext()

func newDashboardRuntimeContext() *dashboardRuntimeContext {
	ctx := &dashboardRuntimeContext{
		cache:   runtimeDashboardCache,
		sampler: runtimeDashboardIOSampler,
	}
	ctx.builder = dashboardBuilder{
		static:      runtimeDashboardStatic,
		stats:       runtimeDashboardStats,
		runtime:     runtimeDashboardRuntime,
		system:      runtimeDashboardSystemMetrics,
		chart:       runtimeDashboardChart,
		attachSocks: attachSocks5Metrics,
	}
	ctx.service = dashboardService{
		builder: ctx.builder,
		cache:   ctx.cache,
		now:     time.Now,
	}
	return ctx
}

func GetDashboardData(force bool) map[string]interface{} {
	if runtimeDashboard == nil {
		return nil
	}
	return runtimeDashboard.service.Get(force, servercfg.Current())
}

func InitDashboardData() {
	if runtimeDashboard == nil {
		return
	}
	if runtimeDashboard.sampler != nil {
		runtimeDashboard.sampler.Start()
	}
	runtimeDashboard.service.Get(true, servercfg.Current())
}

type dashboardCacheSnapshot struct {
	data            map[string]interface{}
	lastRefresh     time.Time
	lastFullRefresh time.Time
}

type dashboardCacheStore interface {
	Load() dashboardCacheSnapshot
	Store(data map[string]interface{}, now time.Time, fullRefresh bool)
}

type memoryDashboardCacheStore struct {
	mu              sync.RWMutex
	data            map[string]interface{}
	lastRefresh     time.Time
	lastFullRefresh time.Time
}

var runtimeDashboardCache = &memoryDashboardCacheStore{}

func (s *memoryDashboardCacheStore) Load() dashboardCacheSnapshot {
	if s == nil {
		return dashboardCacheSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return dashboardCacheSnapshot{
		data:            s.data,
		lastRefresh:     s.lastRefresh,
		lastFullRefresh: s.lastFullRefresh,
	}
}

func (s *memoryDashboardCacheStore) Store(data map[string]interface{}, now time.Time, fullRefresh bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = data
	s.lastRefresh = now
	if fullRefresh {
		s.lastFullRefresh = now
	}
}

type dashboardService struct {
	builder dashboardBuilder
	cache   dashboardCacheStore
	now     func() time.Time
}

type dashboardRefreshPlan struct {
	now    time.Time
	cached dashboardCacheSnapshot
	mode   dashboardRefreshMode
}

const (
	dashboardFullRefreshInterval = 5 * time.Second
	dashboardCacheReuseWindow    = time.Second
)

type dashboardRefreshMode uint8

const (
	dashboardRefreshUseCached dashboardRefreshMode = iota
	dashboardRefreshLight
	dashboardRefreshFull
)

func (s dashboardService) Get(force bool, cfg *servercfg.Snapshot) map[string]interface{} {
	cfg = servercfg.Resolve(cfg)
	plan := s.buildRefreshPlan(force)
	data, fullRefresh := s.resolveRefreshData(plan, cfg)
	if plan.mode != dashboardRefreshUseCached && s.cache != nil {
		s.cache.Store(data, plan.now, fullRefresh)
	}
	return cloneDashboardData(data)
}

func selectDashboardRefreshMode(force bool, now time.Time, cached dashboardCacheSnapshot) dashboardRefreshMode {
	if cached.data == nil || force || now.Sub(cached.lastFullRefresh) >= dashboardFullRefreshInterval {
		return dashboardRefreshFull
	}
	if now.Sub(cached.lastRefresh) < dashboardCacheReuseWindow {
		return dashboardRefreshUseCached
	}
	return dashboardRefreshLight
}

func (s dashboardService) buildRefreshPlan(force bool) dashboardRefreshPlan {
	plan := dashboardRefreshPlan{now: time.Now()}
	if s.now != nil {
		plan.now = s.now()
	}
	if s.cache != nil {
		plan.cached = s.cache.Load()
	}
	plan.mode = selectDashboardRefreshMode(force, plan.now, plan.cached)
	return plan
}

func (s dashboardService) resolveRefreshData(plan dashboardRefreshPlan, cfg *servercfg.Snapshot) (map[string]interface{}, bool) {
	switch plan.mode {
	case dashboardRefreshFull:
		return s.builder.Build(cfg), true
	case dashboardRefreshUseCached:
		return plan.cached.data, false
	default:
		return s.builder.RefreshCached(plan.cached.data), false
	}
}

type dashboardBuilder struct {
	static      dashboardStaticProvider
	stats       dashboardStatsCoordinator
	runtime     dashboardRuntimeProvider
	system      dashboardSystemMetricsProvider
	chart       dashboardChartProvider
	attachSocks func(map[string]interface{})
}

func (b dashboardBuilder) Build(cfg *servercfg.Snapshot) map[string]interface{} {
	data := make(map[string]interface{})
	clientStats := b.stats.RefreshClientStatsSnapshot(cfg)
	staticProvider := b.static
	if staticProvider.sharedClientCount {
		staticProvider.clientCount = nil
	}
	staticProvider.Snapshot(cfg).Apply(data)
	if staticProvider.sharedClientCount {
		data["clientCount"] = clientStats.count.totalClients - clientStats.count.runtimeVKeyClients
	}
	clientStats.traffic.Apply(data)
	b.stats.TaskModeSnapshot().Apply(data)
	b.runtime.Snapshot().Apply(data)
	b.system.Snapshot().Apply(data, false)
	b.chart.Snapshot().Apply(data)
	if b.attachSocks != nil {
		b.attachSocks(data)
	}
	return data
}

func (b dashboardBuilder) RefreshCached(cached map[string]interface{}) map[string]interface{} {
	dst := cloneDashboardData(cached)
	if dst == nil {
		dst = make(map[string]interface{})
	}
	dst["tcpCount"] = b.stats.TCPConnectionCount()
	b.system.Snapshot().Apply(dst, true)
	if b.attachSocks != nil {
		b.attachSocks(dst)
	}
	return dst
}

type dashboardChartSnapshot struct {
	deciles []map[string]interface{}
}

type dashboardChartProvider struct {
	deciles func() []map[string]interface{}
}

var runtimeDashboardChart = dashboardChartProvider{
	deciles: tool.ChartDeciles,
}

func (p dashboardChartProvider) Snapshot() dashboardChartSnapshot {
	snapshot := dashboardChartSnapshot{}
	if p.deciles != nil {
		snapshot.deciles = p.deciles()
	}
	return snapshot
}

func (s dashboardChartSnapshot) Apply(dst map[string]interface{}) {
	if dst == nil {
		return
	}
	for i, value := range s.deciles {
		dst["sys"+strconv.Itoa(i+1)] = cloneDashboardValue(value)
	}
}

func attachSocks5Metrics(dst map[string]interface{}) {
	if dst == nil {
		return
	}
	dst["socks5Metrics"] = proxy.Socks5MetricsSnapshot()
}

func GetVersion() string {
	return version.VERSION
}

func GetMinVersion() string {
	return version.GetMinVersion(bridge.ServerSecureMode)
}

func GetCurrentYear() int {
	return time.Now().Year()
}

func intStringOrEmpty(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func cloneDashboardData(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneDashboardValue(value)
	}
	return dst
}

func cloneDashboardValue(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return cloneDashboardData(v)
	case []interface{}:
		items := make([]interface{}, len(v))
		for i, item := range v {
			items[i] = cloneDashboardValue(item)
		}
		return items
	default:
		return value
	}
}

type dashboardRuntimeSnapshot struct {
	serverIP   string
	serverIPv4 string
	serverIPv6 string
	p2pIP      string
	p2pPort    int
	p2pAddr    string
}

type dashboardRuntimeProvider struct {
	p2pConfig    func() connection.P2PRuntimeConfig
	serverIP     func(string) string
	outboundIPv4 func() string
	outboundIPv6 func() string
	buildAddr    func(string, int) string
}

var runtimeDashboardRuntime = dashboardRuntimeProvider{
	p2pConfig: func() connection.P2PRuntimeConfig {
		return connection.CurrentP2PRuntime()
	},
	serverIP: func(ip string) string {
		return common.GetServerIp(ip)
	},
	outboundIPv4: func() string {
		return common.GetOutboundIPv4().String()
	},
	outboundIPv6: func() string {
		return common.GetOutboundIPv6().String()
	},
	buildAddr: func(host string, port int) string {
		return common.BuildAddress(host, strconv.Itoa(port))
	},
}

func (p dashboardRuntimeProvider) Snapshot() dashboardRuntimeSnapshot {
	snapshot := dashboardRuntimeSnapshot{}
	if p.p2pConfig == nil {
		return snapshot
	}
	p2pCfg := p.p2pConfig()
	snapshot.p2pIP = p2pCfg.IP
	snapshot.p2pPort = p2pCfg.Port
	if p.serverIP != nil {
		snapshot.serverIP = p.serverIP(p2pCfg.IP)
	}
	if p.outboundIPv4 != nil {
		snapshot.serverIPv4 = p.outboundIPv4()
	}
	if p.outboundIPv6 != nil {
		snapshot.serverIPv6 = p.outboundIPv6()
	}
	if p.buildAddr != nil {
		snapshot.p2pAddr = p.buildAddr(snapshot.serverIP, p2pCfg.Port)
	}
	return snapshot
}

func (s dashboardRuntimeSnapshot) Apply(dst map[string]interface{}) {
	if dst == nil {
		return
	}
	dst["serverIp"] = s.serverIP
	dst["serverIpv4"] = s.serverIPv4
	dst["serverIpv6"] = s.serverIPv6
	dst["p2pIp"] = s.p2pIP
	dst["p2pPort"] = s.p2pPort
	dst["p2pAddr"] = s.p2pAddr
}

type dashboardStaticSnapshot struct {
	version           string
	minVersion        string
	hostCount         int
	clientCount       int
	bridgeType        string
	httpProxyPort     string
	httpsProxyPort    string
	ipLimit           string
	flowStoreInterval string
	logLevel          string
	upTime            string
	upSecs            int64
	startTime         int64
}

type dashboardStaticProvider struct {
	version           func() string
	minVersion        func() string
	hostCount         func() int
	clientCount       func(*servercfg.Snapshot) int
	runTime           func() string
	runSecs           func() int64
	startTime         func() int64
	portString        func(int) string
	sharedClientCount bool
}

var runtimeDashboardStatic = dashboardStaticProvider{
	version:           GetVersion,
	minVersion:        GetMinVersion,
	hostCount:         runtimeDashboardStats.HostCount,
	clientCount:       runtimeDashboardStats.ClientCount,
	runTime:           common.GetRunTime,
	runSecs:           common.GetRunSecs,
	startTime:         common.GetStartTime,
	portString:        intStringOrEmpty,
	sharedClientCount: true,
}

func (p dashboardStaticProvider) Snapshot(cfg *servercfg.Snapshot) dashboardStaticSnapshot {
	cfg = servercfg.Resolve(cfg)
	snapshot := dashboardStaticSnapshot{
		bridgeType: cfg.Bridge.PrimaryType,
		ipLimit:    cfg.Runtime.IPLimit,
		logLevel:   cfg.Log.Level,
	}
	if p.portString != nil {
		snapshot.httpProxyPort = p.portString(cfg.Network.HTTPProxyPort)
		snapshot.httpsProxyPort = p.portString(cfg.Network.HTTPSProxyPort)
		snapshot.flowStoreInterval = p.portString(cfg.Runtime.FlowStoreInterval)
	}
	if p.version != nil {
		snapshot.version = p.version()
	}
	if p.minVersion != nil {
		snapshot.minVersion = p.minVersion()
	}
	if p.hostCount != nil {
		snapshot.hostCount = p.hostCount()
	}
	if p.clientCount != nil {
		snapshot.clientCount = p.clientCount(cfg)
	}
	if p.runTime != nil {
		snapshot.upTime = p.runTime()
	}
	if p.runSecs != nil {
		snapshot.upSecs = p.runSecs()
	}
	if p.startTime != nil {
		snapshot.startTime = p.startTime()
	}
	return snapshot
}

func (s dashboardStaticSnapshot) Apply(dst map[string]interface{}) {
	if dst == nil {
		return
	}
	dst["version"] = s.version
	dst["minVersion"] = s.minVersion
	dst["hostCount"] = s.hostCount
	dst["clientCount"] = s.clientCount
	dst["bridgeType"] = s.bridgeType
	dst["httpProxyPort"] = s.httpProxyPort
	dst["httpsProxyPort"] = s.httpsProxyPort
	dst["ipLimit"] = s.ipLimit
	dst["flowStoreInterval"] = s.flowStoreInterval
	dst["logLevel"] = s.logLevel
	dst["upTime"] = s.upTime
	dst["upSecs"] = s.upSecs
	dst["startTime"] = s.startTime
}

type dashboardIOTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type realDashboardIOTicker struct {
	*time.Ticker
}

func (t realDashboardIOTicker) Chan() <-chan time.Time {
	return t.C
}

type dashboardIOSampler struct {
	startOnce sync.Once
	stateMu   sync.Mutex

	ioCounters func(bool) ([]psnet.IOCountersStat, error)
	newTicker  func(time.Duration) dashboardIOTicker
	now        func() time.Time

	lastBytesSent  uint64
	lastBytesRecv  uint64
	lastSampleTime time.Time
	ioSendRate     atomic.Value
	ioRecvRate     atomic.Value
}

var runtimeDashboardIOSampler = newDashboardIOSampler()

func newDashboardIOSampler() *dashboardIOSampler {
	return &dashboardIOSampler{
		ioCounters: psnet.IOCounters,
		newTicker: func(d time.Duration) dashboardIOTicker {
			return realDashboardIOTicker{Ticker: time.NewTicker(d)}
		},
		now: time.Now,
	}
}

func (s *dashboardIOSampler) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		s.initialize()
		if s.newTicker == nil {
			return
		}
		ticker := s.newTicker(time.Second)
		if ticker == nil {
			return
		}
		go s.run(ticker)
	})
}

func (s *dashboardIOSampler) CurrentSpeeds() (interface{}, interface{}) {
	if s == nil {
		return nil, nil
	}
	var ioSend, ioRecv interface{}
	if v, ok := s.ioSendRate.Load().(float64); ok {
		ioSend = v
	}
	if v, ok := s.ioRecvRate.Load().(float64); ok {
		ioRecv = v
	}
	return ioSend, ioRecv
}

func (s *dashboardIOSampler) initialize() {
	now := time.Now()
	if s != nil && s.now != nil {
		now = s.now()
	}
	sent, recv, _ := s.readCurrentTotals()

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.storeBaselineLocked(sent, recv, now)
}

func (s *dashboardIOSampler) run(ticker dashboardIOTicker) {
	defer ticker.Stop()
	for now := range ticker.Chan() {
		s.sample(now)
	}
}

func (s *dashboardIOSampler) sample(now time.Time) {
	sent, recv, ok := s.readCurrentTotals()
	if !ok {
		return
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	elapsed := now.Sub(s.lastSampleTime).Seconds()
	if elapsed <= 0 {
		s.storeBaselineLocked(sent, recv, now)
		return
	}
	if sent < s.lastBytesSent || recv < s.lastBytesRecv {
		s.storeBaselineLocked(sent, recv, now)
		return
	}

	rateSent := float64(sent-s.lastBytesSent) / elapsed
	rateRecv := float64(recv-s.lastBytesRecv) / elapsed

	s.ioSendRate.Store(rateSent)
	s.ioRecvRate.Store(rateRecv)
	s.storeBaselineLocked(sent, recv, now)
}

func (s *dashboardIOSampler) readCurrentTotals() (uint64, uint64, bool) {
	if s == nil || s.ioCounters == nil {
		return 0, 0, false
	}
	stats, _ := s.ioCounters(false)
	if len(stats) == 0 {
		return 0, 0, false
	}
	return stats[0].BytesSent, stats[0].BytesRecv, true
}

func (s *dashboardIOSampler) storeBaselineLocked(sent, recv uint64, now time.Time) {
	s.lastBytesSent = sent
	s.lastBytesRecv = recv
	s.lastSampleTime = now
}

type dashboardSystemSnapshot struct {
	upTime        string
	cpuVal        interface{}
	loadVal       interface{}
	swapVal       interface{}
	virtVal       interface{}
	protoCounters map[string]int64
	ioSend        interface{}
	ioRecv        interface{}
}

type dashboardSystemMetricsProvider struct {
	upTime        func() string
	utilization   func() (cpuVal, loadVal, swapVal, virtVal interface{})
	protoCounters func() map[string]int64
	ioSpeeds      func() (ioSend, ioRecv interface{})
}

var runtimeDashboardSystemMetrics = dashboardSystemMetricsProvider{
	upTime:        common.GetRunTime,
	utilization:   currentSystemUtilization,
	protoCounters: currentProtoCounters,
	ioSpeeds:      runtimeDashboardIOSampler.CurrentSpeeds,
}

func (p dashboardSystemMetricsProvider) Snapshot() dashboardSystemSnapshot {
	snapshot := dashboardSystemSnapshot{}
	if p.upTime != nil {
		snapshot.upTime = p.upTime()
	}
	if p.utilization != nil {
		snapshot.cpuVal, snapshot.loadVal, snapshot.swapVal, snapshot.virtVal = p.utilization()
	}
	if p.protoCounters != nil {
		snapshot.protoCounters = p.protoCounters()
	}
	if p.ioSpeeds != nil {
		snapshot.ioSend, snapshot.ioRecv = p.ioSpeeds()
	}
	return snapshot
}

func (s dashboardSystemSnapshot) Apply(dst map[string]interface{}, includeUptime bool) {
	if dst == nil {
		return
	}
	if includeUptime {
		dst["upTime"] = s.upTime
	}
	if s.cpuVal != nil {
		dst["cpu"] = s.cpuVal
	}
	if s.loadVal != nil {
		dst["load"] = s.loadVal
	}
	if s.swapVal != nil {
		dst["swap_mem"] = s.swapVal
	}
	if s.virtVal != nil {
		dst["virtual_mem"] = s.virtVal
	}
	for k, v := range s.protoCounters {
		dst[k] = v
	}
	if s.ioSend != nil {
		dst["io_send"] = s.ioSend
	}
	if s.ioRecv != nil {
		dst["io_recv"] = s.ioRecv
	}
}

type dashboardStatsStore interface {
	RangeClients(func(*file.Client) bool)
	RangeHosts(func(*file.Host) bool)
	RangeTasks(func(*file.Tunnel) bool)
}

type dashboardStatsCoordinator struct {
	store          dashboardStatsStore
	refreshClients func()
}

type dashboardClientCountSnapshot struct {
	totalClients       int
	runtimeVKeyClients int
}

type dashboardClientStatsSnapshot struct {
	count   dashboardClientCountSnapshot
	traffic dashboardTrafficSnapshot
}

type dashboardTrafficSnapshot struct {
	clientOnlineCount int
	inletFlowCount    int64
	exportFlowCount   int64
	tcpCount          int
}

type dashboardTaskModeSnapshot struct {
	tcpCount       int
	udpCount       int
	secretCount    int
	socks5Count    int
	p2pCount       int
	httpProxyCount int
}

var runtimeDashboardStats = dashboardStatsCoordinator{
	store:          dbDashboardStatsStore{},
	refreshClients: runtimeFlow.RefreshClients,
}

func (c dashboardStatsCoordinator) HostCount() int {
	count := 0
	if c.store == nil {
		return count
	}
	c.store.RangeHosts(func(*file.Host) bool {
		count++
		return true
	})
	return count
}

func (c dashboardStatsCoordinator) ClientCount(cfg *servercfg.Snapshot) int {
	snapshot := c.ClientCountSnapshot(cfg)
	return snapshot.totalClients - snapshot.runtimeVKeyClients
}

func (c dashboardStatsCoordinator) ClientCountSnapshot(cfg *servercfg.Snapshot) dashboardClientCountSnapshot {
	return c.ClientStatsSnapshot(cfg).count
}

func (c dashboardStatsCoordinator) RefreshClientStatsSnapshot(cfg *servercfg.Snapshot) dashboardClientStatsSnapshot {
	if c.refreshClients != nil {
		c.refreshClients()
	}
	return c.ClientStatsSnapshot(cfg)
}

func (c dashboardStatsCoordinator) ClientStatsSnapshot(cfg *servercfg.Snapshot) dashboardClientStatsSnapshot {
	snapshot := dashboardClientStatsSnapshot{}
	if c.store == nil {
		return snapshot
	}
	publicVKey := ""
	visitorVKey := ""
	if cfg != nil {
		publicVKey = strings.TrimSpace(cfg.Runtime.PublicVKey)
		visitorVKey = strings.TrimSpace(cfg.Runtime.VisitorVKey)
	}
	seenRuntimeVKeys := make(map[string]struct{}, 2)
	c.store.RangeClients(func(client *file.Client) bool {
		if client == nil {
			return true
		}
		snapshot.count.totalClients++
		if client.IsConnect {
			snapshot.traffic.clientOnlineCount++
		}
		snapshot.traffic.tcpCount += int(client.NowConn)
		clientIn, clientOut, _ := client.TotalTrafficTotals()
		snapshot.traffic.inletFlowCount += clientIn
		snapshot.traffic.exportFlowCount += clientOut
		if !client.NoDisplay {
			return true
		}
		verifyKey := strings.TrimSpace(client.VerifyKey)
		if verifyKey == "" || (verifyKey != publicVKey && verifyKey != visitorVKey) {
			return true
		}
		if _, ok := seenRuntimeVKeys[verifyKey]; ok {
			return true
		}
		seenRuntimeVKeys[verifyKey] = struct{}{}
		snapshot.count.runtimeVKeyClients++
		return true
	})
	return snapshot
}

func (c dashboardStatsCoordinator) RefreshClientFlowTotals() (onlineCount int, inletFlow int64, exportFlow int64, tcpCount int) {
	snapshot := c.RefreshTrafficSnapshot()
	return snapshot.clientOnlineCount, snapshot.inletFlowCount, snapshot.exportFlowCount, snapshot.tcpCount
}

func (c dashboardStatsCoordinator) ClientFlowTotals() (onlineCount int, inletFlow int64, exportFlow int64, tcpCount int) {
	snapshot := c.TrafficSnapshot()
	return snapshot.clientOnlineCount, snapshot.inletFlowCount, snapshot.exportFlowCount, snapshot.tcpCount
}

func (c dashboardStatsCoordinator) RefreshTrafficSnapshot() dashboardTrafficSnapshot {
	return c.RefreshClientStatsSnapshot(nil).traffic
}

func (c dashboardStatsCoordinator) TrafficSnapshot() dashboardTrafficSnapshot {
	return c.ClientStatsSnapshot(nil).traffic
}

func (c dashboardStatsCoordinator) TaskModeCounts() (tcpN, udpN, secretN, socks5N, p2pN, httpN int) {
	snapshot := c.TaskModeSnapshot()
	return snapshot.tcpCount, snapshot.udpCount, snapshot.secretCount, snapshot.socks5Count, snapshot.p2pCount, snapshot.httpProxyCount
}

func (c dashboardStatsCoordinator) TaskModeSnapshot() dashboardTaskModeSnapshot {
	snapshot := dashboardTaskModeSnapshot{}
	if c.store == nil {
		return snapshot
	}
	c.store.RangeTasks(func(task *file.Tunnel) bool {
		if task == nil {
			return true
		}
		switch task.Mode {
		case "tcp":
			snapshot.tcpCount++
		case "socks5":
			snapshot.socks5Count++
		case "httpProxy":
			snapshot.httpProxyCount++
		case "mixProxy":
			if task.HttpProxy {
				snapshot.httpProxyCount++
			}
			if task.Socks5Proxy {
				snapshot.socks5Count++
			}
		case "udp":
			snapshot.udpCount++
		case "p2p":
			snapshot.p2pCount++
		case "secret":
			snapshot.secretCount++
		}
		return true
	})
	return snapshot
}

func (c dashboardStatsCoordinator) TCPConnectionCount() int {
	if c.store == nil {
		return 0
	}
	tcpCount := 0
	c.store.RangeClients(func(client *file.Client) bool {
		if client == nil {
			return true
		}
		tcpCount += int(client.NowConn)
		return true
	})
	return tcpCount
}

func (s dashboardTrafficSnapshot) Apply(dst map[string]interface{}) {
	if dst == nil {
		return
	}
	dst["clientOnlineCount"] = s.clientOnlineCount
	dst["inletFlowCount"] = int(s.inletFlowCount)
	dst["exportFlowCount"] = int(s.exportFlowCount)
	dst["tcpCount"] = s.tcpCount
}

func (s dashboardTaskModeSnapshot) Apply(dst map[string]interface{}) {
	if dst == nil {
		return
	}
	dst["tcpC"] = s.tcpCount
	dst["udpCount"] = s.udpCount
	dst["socks5Count"] = s.socks5Count
	dst["httpProxyCount"] = s.httpProxyCount
	dst["secretCount"] = s.secretCount
	dst["p2pCount"] = s.p2pCount
}

type dbDashboardStatsStore struct{}

func (dbDashboardStatsStore) RangeClients(fn func(*file.Client) bool) {
	if fn == nil {
		return
	}
	file.GetDb().RangeClients(fn)
}

func (dbDashboardStatsStore) RangeHosts(fn func(*file.Host) bool) {
	if fn == nil {
		return
	}
	file.GetDb().RangeHosts(fn)
}

func (dbDashboardStatsStore) RangeTasks(fn func(*file.Tunnel) bool) {
	if fn == nil {
		return
	}
	file.GetDb().RangeTasks(fn)
}

func currentSystemUtilization() (cpuVal, loadVal, swapVal, virtVal interface{}) {
	if cpuPercent, err := cpu.Percent(0, true); err == nil {
		var sum float64
		for _, v := range cpuPercent {
			sum += v
		}
		if n := len(cpuPercent); n > 0 {
			cpuVal = math.Round(sum / float64(n))
		}
	}
	if loads, err := load.Avg(); err == nil {
		loadVal = loads.String()
	}
	if swap, err := mem.SwapMemory(); err == nil {
		swapVal = math.Round(swap.UsedPercent)
	}
	if vir, err := mem.VirtualMemory(); err == nil {
		virtVal = math.Round(vir.UsedPercent)
	}
	return cpuVal, loadVal, swapVal, virtVal
}

func currentProtoCounters() map[string]int64 {
	protoVals := map[string]int64{}
	if pcounters, err := net.ProtoCounters(nil); err == nil {
		for _, v := range pcounters {
			if val, ok := v.Stats["CurrEstab"]; ok {
				protoVals[v.Protocol] = val
			}
		}
	}
	if _, ok := protoVals["tcp"]; !ok {
		if conns, err := net.Connections("tcp"); err == nil {
			protoVals["tcp"] = int64(len(conns))
		}
	}
	if _, ok := protoVals["udp"]; !ok {
		if conns, err := net.Connections("udp"); err == nil {
			protoVals["udp"] = int64(len(conns))
		}
	}
	return protoVals
}
