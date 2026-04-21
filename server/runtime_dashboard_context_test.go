package server

import (
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
	psnet "github.com/shirou/gopsutil/v4/net"
)

func TestNewDashboardRuntimeContextWiresSharedDefaults(t *testing.T) {
	ctx := newDashboardRuntimeContext()
	if ctx == nil || ctx.cache == nil || ctx.sampler == nil {
		t.Fatalf("dashboard runtime context = %+v", ctx)
	}
	cache, ok := ctx.service.cache.(*memoryDashboardCacheStore)
	if !ok || cache != ctx.cache {
		t.Fatalf("dashboard service cache wiring = %#v", ctx.service.cache)
	}
	if ctx.service.now == nil {
		t.Fatal("dashboard service now func should be wired")
	}
	if ctx.builder.attachSocks == nil {
		t.Fatal("dashboard builder socks attachment should be wired")
	}
}

func TestDashboardBuilderBuildCombinesProviders(t *testing.T) {
	refreshes := 0
	builder := dashboardBuilder{
		static: dashboardStaticProvider{
			version:     func() string { return "v1" },
			minVersion:  func() string { return "v0" },
			hostCount:   func() int { return 2 },
			clientCount: func(*servercfg.Snapshot) int { return 3 },
			runTime:     func() string { return "10s" },
			runSecs:     func() int64 { return 10 },
			startTime:   func() int64 { return 100 },
			portString:  intStringOrEmpty,
		},
		stats: dashboardStatsCoordinator{
			store: stubDashboardStatsStore{
				clients: []*file.Client{
					{IsConnect: true, NowConn: 2, InletFlow: 5, ExportFlow: 7, Flow: &file.Flow{InletFlow: 25, ExportFlow: 47}},
				},
				tasks: []*file.Tunnel{
					{Mode: "tcp"},
					{Mode: "httpProxy"},
				},
			},
			refreshClients: func() { refreshes++ },
		},
		runtime: dashboardRuntimeProvider{
			p2pConfig: func() connection.P2PRuntimeConfig {
				return connection.P2PRuntimeConfig{IP: "198.51.100.10", Port: 1234}
			},
			serverIP:     func(ip string) string { return "203.0.113.10" },
			outboundIPv4: func() string { return "192.0.2.10" },
			outboundIPv6: func() string { return "2001:db8::10" },
			buildAddr:    func(host string, port int) string { return "203.0.113.10:1234" },
		},
		system: dashboardSystemMetricsProvider{
			utilization:   func() (interface{}, interface{}, interface{}, interface{}) { return 1.0, "load", 2.0, 3.0 },
			protoCounters: func() map[string]int64 { return map[string]int64{"tcp": 5} },
			ioSpeeds:      func() (interface{}, interface{}) { return 8.0, 9.0 },
		},
		chart: dashboardChartProvider{
			deciles: func() []map[string]interface{} { return []map[string]interface{}{{"cpu": 1.0}} },
		},
		attachSocks: func(dst map[string]interface{}) { dst["socks5Metrics"] = "attached" },
	}

	data := builder.Build(&servercfg.Snapshot{
		Bridge:  servercfg.BridgeConfig{PrimaryType: "ws"},
		Log:     servercfg.LogConfig{Level: "info"},
		Runtime: servercfg.RuntimeConfig{IPLimit: "limit"},
	})

	if refreshes != 1 {
		t.Fatalf("refresh count = %d, want 1", refreshes)
	}
	if data["version"] != "v1" || data["minVersion"] != "v0" || data["hostCount"] != 2 || data["clientCount"] != 3 {
		t.Fatalf("builder static fields = %+v", data)
	}
	if data["clientOnlineCount"] != 1 || data["inletFlowCount"] != 25 || data["exportFlowCount"] != 47 || data["tcpCount"] != 2 {
		t.Fatalf("builder client fields = %+v", data)
	}
	if data["tcpC"] != 1 || data["httpProxyCount"] != 1 {
		t.Fatalf("builder task fields = %+v", data)
	}
	if data["serverIp"] != "203.0.113.10" || data["p2pAddr"] != "203.0.113.10:1234" {
		t.Fatalf("builder runtime fields = %+v", data)
	}
	if data["cpu"] != 1.0 || data["load"] != "load" || data["io_send"] != 8.0 || data["tcp"] != int64(5) {
		t.Fatalf("builder system fields = %+v", data)
	}
	if _, ok := data["sys1"].(map[string]interface{}); !ok {
		t.Fatalf("builder chart field = %+v", data["sys1"])
	}
	if data["socks5Metrics"] != "attached" {
		t.Fatalf("builder socks5 attachment = %+v", data["socks5Metrics"])
	}
}

func TestDashboardBuilderBuildUsesSharedClientCountOverride(t *testing.T) {
	refreshes := 0
	builder := dashboardBuilder{
		static: dashboardStaticProvider{
			clientCount:       func(*servercfg.Snapshot) int { return 99 },
			sharedClientCount: true,
		},
		stats: dashboardStatsCoordinator{
			store: stubDashboardStatsStore{
				clients: []*file.Client{
					{VerifyKey: "shared", NoDisplay: true, Flow: &file.Flow{}},
					{VerifyKey: "shared", NoDisplay: true, Flow: &file.Flow{}},
					{VerifyKey: "user", Flow: &file.Flow{}},
				},
			},
			refreshClients: func() { refreshes++ },
		},
	}

	data := builder.Build(&servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{PublicVKey: "shared"},
	})

	if refreshes != 1 {
		t.Fatalf("refresh count = %d, want 1", refreshes)
	}
	if data["clientCount"] != 2 {
		t.Fatalf("shared clientCount = %+v, want 2", data["clientCount"])
	}
}

func TestDashboardBuilderRefreshCachedUpdatesCachedFields(t *testing.T) {
	builder := dashboardBuilder{
		stats: dashboardStatsCoordinator{
			store: stubDashboardStatsStore{
				clients: []*file.Client{
					{NowConn: 7, Flow: &file.Flow{}},
				},
			},
		},
		system: dashboardSystemMetricsProvider{
			upTime:      func() string { return "1m" },
			utilization: func() (interface{}, interface{}, interface{}, interface{}) { return 4.0, "load", nil, nil },
		},
		attachSocks: func(dst map[string]interface{}) { dst["socks5Metrics"] = "attached" },
	}

	dst := builder.RefreshCached(map[string]interface{}{"existing": 1})

	if dst["existing"] != 1 || dst["tcpCount"] != 7 || dst["upTime"] != "1m" || dst["cpu"] != 4.0 {
		t.Fatalf("builder cached refresh = %+v", dst)
	}
	if dst["socks5Metrics"] != "attached" {
		t.Fatalf("builder cached socks5 attachment = %+v", dst["socks5Metrics"])
	}
}

type stubDashboardCacheStore struct {
	snapshot      dashboardCacheSnapshot
	storeCount    int
	lastStoreFull bool
	lastStoreTime time.Time
}

func (s *stubDashboardCacheStore) Load() dashboardCacheSnapshot {
	if s == nil {
		return dashboardCacheSnapshot{}
	}
	return s.snapshot
}

func (s *stubDashboardCacheStore) Store(data map[string]interface{}, now time.Time, fullRefresh bool) {
	if s == nil {
		return
	}
	s.storeCount++
	s.lastStoreFull = fullRefresh
	s.lastStoreTime = now
	s.snapshot.data = data
	s.snapshot.lastRefresh = now
	if fullRefresh {
		s.snapshot.lastFullRefresh = now
	}
}

func TestDashboardServiceBuildsAndStoresOnForce(t *testing.T) {
	cache := &stubDashboardCacheStore{}
	builds := 0
	now := time.Unix(1_700_000_000, 0)
	service := dashboardService{
		builder: dashboardBuilder{
			static: dashboardStaticProvider{
				version:     func() string { builds++; return "v1" },
				minVersion:  func() string { return "v0" },
				hostCount:   func() int { return 1 },
				clientCount: func(*servercfg.Snapshot) int { return 2 },
				runTime:     func() string { return "1s" },
				runSecs:     func() int64 { return 1 },
				startTime:   func() int64 { return 10 },
				portString:  intStringOrEmpty,
			},
		},
		cache: cache,
		now:   func() time.Time { return now },
	}

	got := service.Get(true, &servercfg.Snapshot{})

	if builds != 1 {
		t.Fatalf("build count = %d, want 1", builds)
	}
	if cache.storeCount != 1 || !cache.lastStoreFull || !cache.lastStoreTime.Equal(now) {
		t.Fatalf("cache store state = %+v", cache)
	}
	if got["version"] != "v1" {
		t.Fatalf("forced build snapshot = %+v", got)
	}
}

func TestDashboardServiceUsesFreshCachedSnapshot(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cache := &stubDashboardCacheStore{
		snapshot: dashboardCacheSnapshot{
			data:            map[string]interface{}{"count": 1, "nested": map[string]interface{}{"value": 2}},
			lastRefresh:     base.Add(-500 * time.Millisecond),
			lastFullRefresh: base.Add(-2 * time.Second),
		},
	}
	service := dashboardService{
		cache: cache,
		now:   func() time.Time { return base },
	}

	got := service.Get(false, &servercfg.Snapshot{})
	got["count"] = 99
	gotNested := got["nested"].(map[string]interface{})
	gotNested["value"] = 77

	again := service.Get(false, &servercfg.Snapshot{})
	if again["count"] == 99 {
		t.Fatalf("service reused cached top-level map: %+v", again)
	}
	againNested := again["nested"].(map[string]interface{})
	if againNested["value"] == 77 {
		t.Fatalf("service reused cached nested map: %+v", againNested)
	}
	if cache.storeCount != 0 {
		t.Fatalf("service unexpectedly stored fresh cached snapshot: %+v", cache)
	}
}

func TestDashboardServiceRefreshesStaleCacheWithoutFullRebuild(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cache := &stubDashboardCacheStore{
		snapshot: dashboardCacheSnapshot{
			data:            map[string]interface{}{"existing": 1},
			lastRefresh:     base.Add(-2 * time.Second),
			lastFullRefresh: base.Add(-2 * time.Second),
		},
	}
	service := dashboardService{
		builder: dashboardBuilder{
			stats: dashboardStatsCoordinator{
				store: stubDashboardStatsStore{},
			},
			system: dashboardSystemMetricsProvider{
				upTime:      func() string { return "1m" },
				utilization: func() (interface{}, interface{}, interface{}, interface{}) { return 4.0, "load", nil, nil },
			},
			attachSocks: func(dst map[string]interface{}) { dst["socks5Metrics"] = "attached" },
		},
		cache: cache,
		now:   func() time.Time { return base },
	}

	got := service.Get(false, &servercfg.Snapshot{})

	if cache.storeCount != 1 || cache.lastStoreFull {
		t.Fatalf("stale cache store state = %+v", cache)
	}
	if got["existing"] != 1 || got["upTime"] != "1m" || got["cpu"] != 4.0 || got["socks5Metrics"] != "attached" {
		t.Fatalf("stale cache refresh result = %+v", got)
	}
}

func TestDashboardServiceRebuildsAfterFullRefreshWindowExpires(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cache := &stubDashboardCacheStore{
		snapshot: dashboardCacheSnapshot{
			data:            map[string]interface{}{"stale": 1},
			lastRefresh:     base.Add(-2 * time.Second),
			lastFullRefresh: base.Add(-6 * time.Second),
		},
	}
	builds := 0
	service := dashboardService{
		builder: dashboardBuilder{
			static: dashboardStaticProvider{
				version:     func() string { builds++; return "v2" },
				minVersion:  func() string { return "v1" },
				hostCount:   func() int { return 1 },
				clientCount: func(*servercfg.Snapshot) int { return 2 },
				runTime:     func() string { return "2s" },
				runSecs:     func() int64 { return 2 },
				startTime:   func() int64 { return 20 },
				portString:  intStringOrEmpty,
			},
		},
		cache: cache,
		now:   func() time.Time { return base },
	}

	got := service.Get(false, &servercfg.Snapshot{})

	if builds != 1 {
		t.Fatalf("build count = %d, want 1", builds)
	}
	if cache.storeCount != 1 || !cache.lastStoreFull {
		t.Fatalf("full refresh store state = %+v", cache)
	}
	if got["version"] != "v2" || got["clientCount"] != 2 {
		t.Fatalf("full refresh rebuild result = %+v", got)
	}
}

func TestDashboardStaticProviderSnapshotApply(t *testing.T) {
	snapshot := dashboardStaticProvider{
		version:    func() string { return "v1.2.3" },
		minVersion: func() string { return "v0.9.0" },
		hostCount:  func() int { return 5 },
		clientCount: func(cfg *servercfg.Snapshot) int {
			if cfg.Runtime.IPLimit != "limit-value" {
				t.Fatalf("unexpected cfg for clientCount: %+v", cfg.Runtime)
			}
			return 7
		},
		runTime:   func() string { return "3m2s" },
		runSecs:   func() int64 { return 182 },
		startTime: func() int64 { return 1700000000 },
		portString: func(v int) string {
			return "port:" + intStringOrEmpty(v)
		},
	}.Snapshot(&servercfg.Snapshot{
		Bridge: servercfg.BridgeConfig{PrimaryType: "ws"},
		Log:    servercfg.LogConfig{Level: "debug"},
		Runtime: servercfg.RuntimeConfig{
			IPLimit:           "limit-value",
			FlowStoreInterval: 9,
		},
		Network: servercfg.NetworkConfig{
			HTTPProxyPort:  80,
			HTTPSProxyPort: 443,
		},
	})

	dst := map[string]interface{}{}
	snapshot.Apply(dst)

	if dst["version"] != "v1.2.3" || dst["minVersion"] != "v0.9.0" {
		t.Fatalf("static version fields = %+v", dst)
	}
	if dst["hostCount"] != 5 || dst["clientCount"] != 7 || dst["bridgeType"] != "ws" {
		t.Fatalf("static count/runtime fields = %+v", dst)
	}
	if dst["httpProxyPort"] != "port:80" || dst["httpsProxyPort"] != "port:443" || dst["flowStoreInterval"] != "port:9" {
		t.Fatalf("static port fields = %+v", dst)
	}
	if dst["ipLimit"] != "limit-value" || dst["logLevel"] != "debug" {
		t.Fatalf("static config fields = %+v", dst)
	}
	if dst["upTime"] != "3m2s" || dst["upSecs"] != int64(182) || dst["startTime"] != int64(1700000000) {
		t.Fatalf("static time fields = %+v", dst)
	}
}

func TestDashboardStaticProviderSnapshotWithNilFuncs(t *testing.T) {
	dst := map[string]interface{}{"existing": 1}

	dashboardStaticProvider{}.Snapshot(nil).Apply(dst)

	if dst["existing"] != 1 {
		t.Fatalf("static snapshot unexpectedly modified existing data: %+v", dst)
	}
	if dst["version"] != "" || dst["hostCount"] != 0 || dst["upSecs"] != int64(0) {
		t.Fatalf("static snapshot default values = %+v", dst)
	}
}

func TestDashboardRuntimeProviderSnapshotApply(t *testing.T) {
	snapshot := dashboardRuntimeProvider{
		p2pConfig: func() connection.P2PRuntimeConfig {
			return connection.P2PRuntimeConfig{IP: "198.51.100.10", Port: 1234}
		},
		serverIP: func(ip string) string {
			if ip != "198.51.100.10" {
				t.Fatalf("unexpected p2p ip input: %s", ip)
			}
			return "203.0.113.7"
		},
		outboundIPv4: func() string { return "192.0.2.10" },
		outboundIPv6: func() string { return "2001:db8::10" },
		buildAddr: func(host string, port int) string {
			return host + ":" + "1234"
		},
	}.Snapshot()

	dst := map[string]interface{}{}
	snapshot.Apply(dst)

	if dst["serverIp"] != "203.0.113.7" || dst["serverIpv4"] != "192.0.2.10" || dst["serverIpv6"] != "2001:db8::10" {
		t.Fatalf("runtime snapshot server fields = %+v", dst)
	}
	if dst["p2pIp"] != "198.51.100.10" || dst["p2pPort"] != 1234 || dst["p2pAddr"] != "203.0.113.7:1234" {
		t.Fatalf("runtime snapshot p2p fields = %+v", dst)
	}
}

func TestDashboardRuntimeProviderSnapshotWithNilInputs(t *testing.T) {
	dst := map[string]interface{}{"existing": 1}

	dashboardRuntimeProvider{}.Snapshot().Apply(dst)

	if dst["existing"] != 1 {
		t.Fatalf("runtime snapshot unexpectedly modified existing data: %+v", dst)
	}
	if dst["p2pPort"] != 0 || dst["p2pIp"] != "" {
		t.Fatalf("runtime snapshot nil inputs = %+v", dst)
	}
}

func TestDashboardIOSamplerSampleUpdatesRates(t *testing.T) {
	call := 0
	sampler := &dashboardIOSampler{
		ioCounters: func(bool) ([]psnet.IOCountersStat, error) {
			call++
			switch call {
			case 1:
				return []psnet.IOCountersStat{{BytesSent: 100, BytesRecv: 200}}, nil
			default:
				return []psnet.IOCountersStat{{BytesSent: 160, BytesRecv: 260}}, nil
			}
		},
		now: func() time.Time { return time.Unix(100, 0) },
	}

	sampler.initialize()
	sampler.sample(time.Unix(102, 0))
	send, recv := sampler.CurrentSpeeds()

	if send != 30.0 || recv != 30.0 {
		t.Fatalf("sampler rates = %v/%v, want 30/30", send, recv)
	}
}

func TestDashboardIOSamplerSampleSkipsNonPositiveElapsed(t *testing.T) {
	call := 0
	sampler := &dashboardIOSampler{
		ioCounters: func(bool) ([]psnet.IOCountersStat, error) {
			call++
			switch call {
			case 1:
				return []psnet.IOCountersStat{{BytesSent: 100, BytesRecv: 200}}, nil
			default:
				return []psnet.IOCountersStat{{BytesSent: 130, BytesRecv: 260}}, nil
			}
		},
		now: func() time.Time { return time.Unix(100, 0) },
	}

	sampler.initialize()
	sampler.sample(time.Unix(100, 0))
	send, recv := sampler.CurrentSpeeds()

	if send != nil || recv != nil {
		t.Fatalf("sampler rates after zero elapsed = %v/%v, want nil/nil", send, recv)
	}
	if sampler.lastBytesSent != 130 || sampler.lastBytesRecv != 260 {
		t.Fatalf("sampler baseline not updated after zero elapsed: %+v", sampler)
	}
}

func TestDashboardIOSamplerSampleResetsBaselineWhenCountersRollback(t *testing.T) {
	call := 0
	sampler := &dashboardIOSampler{
		ioCounters: func(bool) ([]psnet.IOCountersStat, error) {
			call++
			switch call {
			case 1:
				return []psnet.IOCountersStat{{BytesSent: 200, BytesRecv: 300}}, nil
			default:
				return []psnet.IOCountersStat{{BytesSent: 150, BytesRecv: 250}}, nil
			}
		},
		now: func() time.Time { return time.Unix(100, 0) },
	}

	sampler.initialize()
	sampler.sample(time.Unix(102, 0))
	send, recv := sampler.CurrentSpeeds()

	if send != nil || recv != nil {
		t.Fatalf("sampler rates after counter rollback = %v/%v, want nil/nil", send, recv)
	}
	if sampler.lastBytesSent != 150 || sampler.lastBytesRecv != 250 {
		t.Fatalf("sampler baseline after counter rollback = %+v, want sent/recv 150/250", sampler)
	}
}

type stubDashboardStatsStore struct {
	clients []*file.Client
	hosts   []*file.Host
	tasks   []*file.Tunnel
}

func (s stubDashboardStatsStore) RangeClients(fn func(*file.Client) bool) {
	for _, client := range s.clients {
		if !fn(client) {
			return
		}
	}
}

func (s stubDashboardStatsStore) RangeHosts(fn func(*file.Host) bool) {
	for _, host := range s.hosts {
		if !fn(host) {
			return
		}
	}
}

func (s stubDashboardStatsStore) RangeTasks(fn func(*file.Tunnel) bool) {
	for _, task := range s.tasks {
		if !fn(task) {
			return
		}
	}
}

func TestDashboardStatsCoordinatorClientCountExcludesRuntimeVKeys(t *testing.T) {
	stats := dashboardStatsCoordinator{
		store: stubDashboardStatsStore{
			clients: []*file.Client{
				{Id: 1, Flow: &file.Flow{}},
				{Id: 2, NoDisplay: true, VerifyKey: "public-key", Flow: &file.Flow{}},
				{Id: 3, NoDisplay: true, VerifyKey: "public-key", Flow: &file.Flow{}},
				{Id: 4, NoDisplay: true, VerifyKey: "visitor-key", Flow: &file.Flow{}},
				{Id: 5, NoDisplay: true, VerifyKey: "custom-key", Flow: &file.Flow{}},
			},
		},
	}

	got := stats.ClientCount(&servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			PublicVKey:  "public-key",
			VisitorVKey: "visitor-key",
		},
	})

	if got != 3 {
		t.Fatalf("client count = %d, want 3", got)
	}
}

func TestDashboardStatsCoordinatorRefreshClientFlowTotalsAndTaskModes(t *testing.T) {
	refreshed := 0
	stats := dashboardStatsCoordinator{
		store: stubDashboardStatsStore{
			clients: []*file.Client{
				{Id: 1, IsConnect: true, NowConn: 2, InletFlow: 10, ExportFlow: 20, Flow: &file.Flow{InletFlow: 50, ExportFlow: 80}},
				{Id: 2, IsConnect: false, NowConn: 3, InletFlow: 5, ExportFlow: 15, Flow: &file.Flow{InletFlow: 40, ExportFlow: 30}},
			},
			tasks: []*file.Tunnel{
				{Mode: "tcp"},
				{Mode: "udp"},
				{Mode: "secret"},
				{Mode: "socks5"},
				{Mode: "p2p"},
				{Mode: "httpProxy"},
				{Mode: "mixProxy", HttpProxy: true, Socks5Proxy: true},
			},
		},
		refreshClients: func() {
			refreshed++
		},
	}

	online, inlet, export, tcpCount := stats.RefreshClientFlowTotals()
	if refreshed != 1 {
		t.Fatalf("refresh count = %d, want 1", refreshed)
	}
	if online != 1 || inlet != 90 || export != 110 || tcpCount != 5 {
		t.Fatalf("flow totals = online:%d inlet:%d export:%d tcp:%d, want 1/90/110/5", online, inlet, export, tcpCount)
	}
	if got := stats.TCPConnectionCount(); got != 5 {
		t.Fatalf("TCPConnectionCount() = %d, want 5", got)
	}

	tcpN, udpN, secretN, socks5N, p2pN, httpN := stats.TaskModeCounts()
	if tcpN != 1 || udpN != 1 || secretN != 1 || socks5N != 2 || p2pN != 1 || httpN != 2 {
		t.Fatalf("task mode counts = %d/%d/%d/%d/%d/%d, want 1/1/1/2/1/2", tcpN, udpN, secretN, socks5N, p2pN, httpN)
	}
}

func TestDashboardSystemMetricsProviderSnapshotApply(t *testing.T) {
	snapshot := dashboardSystemMetricsProvider{
		upTime: func() string { return "1h2m3s" },
		utilization: func() (interface{}, interface{}, interface{}, interface{}) {
			return 10.0, "load-1", 20.0, 30.0
		},
		protoCounters: func() map[string]int64 {
			return map[string]int64{"tcp": 7, "udp": 9}
		},
		ioSpeeds: func() (interface{}, interface{}) {
			return 123.0, 456.0
		},
	}.Snapshot()

	withUptime := map[string]interface{}{}
	snapshot.Apply(withUptime, true)
	if withUptime["upTime"] != "1h2m3s" || withUptime["cpu"] != 10.0 || withUptime["load"] != "load-1" {
		t.Fatalf("snapshot apply with uptime = %+v", withUptime)
	}
	if withUptime["swap_mem"] != 20.0 || withUptime["virtual_mem"] != 30.0 {
		t.Fatalf("snapshot memory fields = %+v", withUptime)
	}
	if withUptime["tcp"] != int64(7) || withUptime["udp"] != int64(9) {
		t.Fatalf("snapshot proto counters = %+v", withUptime)
	}
	if withUptime["io_send"] != 123.0 || withUptime["io_recv"] != 456.0 {
		t.Fatalf("snapshot io fields = %+v", withUptime)
	}

	withoutUptime := map[string]interface{}{}
	snapshot.Apply(withoutUptime, false)
	if _, ok := withoutUptime["upTime"]; ok {
		t.Fatalf("snapshot apply without uptime unexpectedly set upTime: %+v", withoutUptime)
	}
}

func TestDashboardSystemSnapshotApplySkipsNilValues(t *testing.T) {
	dst := map[string]interface{}{"existing": 1}

	dashboardSystemSnapshot{
		protoCounters: map[string]int64{"tcp": 3},
	}.Apply(dst, false)

	if dst["existing"] != 1 || dst["tcp"] != int64(3) {
		t.Fatalf("snapshot apply with nil values = %+v", dst)
	}
	if _, ok := dst["cpu"]; ok {
		t.Fatalf("snapshot apply unexpectedly set cpu: %+v", dst)
	}
	if _, ok := dst["io_send"]; ok {
		t.Fatalf("snapshot apply unexpectedly set io_send: %+v", dst)
	}
}

func TestDashboardChartProviderSnapshotApply(t *testing.T) {
	snapshot := dashboardChartProvider{
		deciles: func() []map[string]interface{} {
			return []map[string]interface{}{
				{"cpu": 1.0},
				{"cpu": 2.0},
			}
		},
	}.Snapshot()

	dst := map[string]interface{}{}
	snapshot.Apply(dst)

	sys1, ok := dst["sys1"].(map[string]interface{})
	if !ok || sys1["cpu"] != 1.0 {
		t.Fatalf("chart snapshot sys1 = %+v", dst["sys1"])
	}
	sys2, ok := dst["sys2"].(map[string]interface{})
	if !ok || sys2["cpu"] != 2.0 {
		t.Fatalf("chart snapshot sys2 = %+v", dst["sys2"])
	}

	sys1["cpu"] = 99.0
	again, ok := dst["sys1"].(map[string]interface{})
	if !ok || again["cpu"] != 99.0 {
		t.Fatalf("chart snapshot apply result = %+v", dst["sys1"])
	}
}

func TestGetDashboardDataReturnsDetachedSnapshot(t *testing.T) {
	oldCache := replaceDashboardCacheSnapshot(runtimeDashboardCache, dashboardCacheSnapshot{
		data: map[string]interface{}{
			"count": 1,
			"nested": map[string]interface{}{
				"value": 2,
			},
		},
		lastRefresh:     time.Now(),
		lastFullRefresh: time.Now(),
	})
	defer replaceDashboardCacheSnapshot(runtimeDashboardCache, oldCache)

	snapshot := GetDashboardData(false)
	snapshot["count"] = 99

	nested, ok := snapshot["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("snapshot nested type = %T, want map[string]interface{}", snapshot["nested"])
	}
	nested["value"] = 77

	again := GetDashboardData(false)
	if again["count"] == 99 {
		t.Fatalf("GetDashboardData() returned shared top-level cache: %+v", again)
	}
	againNested, ok := again["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("again nested type = %T, want map[string]interface{}", again["nested"])
	}
	if againNested["value"] == 77 {
		t.Fatalf("GetDashboardData() returned shared nested cache: %+v", againNested)
	}
}

func replaceDashboardCacheSnapshot(store *memoryDashboardCacheStore, next dashboardCacheSnapshot) dashboardCacheSnapshot {
	if store == nil {
		return dashboardCacheSnapshot{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	previous := dashboardCacheSnapshot{
		data:            store.data,
		lastRefresh:     store.lastRefresh,
		lastFullRefresh: store.lastFullRefresh,
	}
	store.data = next.data
	store.lastRefresh = next.lastRefresh
	store.lastFullRefresh = next.lastFullRefresh
	return previous
}

func TestAttachSocks5MetricsAddsDetachedSnapshot(t *testing.T) {
	dst := map[string]interface{}{"existing": 1}

	attachSocks5Metrics(dst)

	metrics, ok := dst["socks5Metrics"].(map[string]interface{})
	if !ok {
		t.Fatalf("dst[socks5Metrics] type = %T, want map[string]interface{}", dst["socks5Metrics"])
	}
	connect, ok := metrics["connect"].(map[string]interface{})
	if !ok {
		t.Fatalf("metrics[connect] type = %T, want map[string]interface{}", metrics["connect"])
	}
	if _, ok := connect["remote_result_timeout"].(uint64); !ok {
		t.Fatalf("metrics[connect][remote_result_timeout] type = %T, want uint64", connect["remote_result_timeout"])
	}

	metrics["connect"] = map[string]interface{}{"remote_result_timeout": uint64(99)}

	fresh := proxy.Socks5MetricsSnapshot()
	freshConnect, ok := fresh["connect"].(map[string]interface{})
	if !ok {
		t.Fatalf("fresh[connect] type = %T, want map[string]interface{}", fresh["connect"])
	}
	if freshConnect["remote_result_timeout"] == uint64(99) {
		t.Fatalf("attachSocks5Metrics reused proxy metrics snapshot: %+v", freshConnect)
	}
}
