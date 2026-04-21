package server

import (
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/version"
)

type runtimeAddr string

func (a runtimeAddr) Network() string { return "tcp" }
func (a runtimeAddr) String() string  { return string(a) }

type runtimeSignalConn struct {
	remote net.Addr
	local  net.Addr
}

func (runtimeSignalConn) Read([]byte) (int, error)         { return 0, nil }
func (runtimeSignalConn) Write(b []byte) (int, error)      { return len(b), nil }
func (runtimeSignalConn) Close() error                     { return nil }
func (c runtimeSignalConn) LocalAddr() net.Addr            { return c.local }
func (c runtimeSignalConn) RemoteAddr() net.Addr           { return c.remote }
func (runtimeSignalConn) SetDeadline(time.Time) error      { return nil }
func (runtimeSignalConn) SetReadDeadline(time.Time) error  { return nil }
func (runtimeSignalConn) SetWriteDeadline(time.Time) error { return nil }

type stubFlowRuntimeStore struct {
	clients map[int]*file.Client
	hosts   []*file.Host
	tunnels []*file.Tunnel
}

func (s stubFlowRuntimeStore) LoadClient(id int) (*file.Client, bool) {
	client, ok := s.clients[id]
	return client, ok
}

func (s stubFlowRuntimeStore) RangeClients(fn func(*file.Client) bool) {
	for _, client := range s.clients {
		if !fn(client) {
			return
		}
	}
}

func (s stubFlowRuntimeStore) RangeHosts(fn func(*file.Host) bool) {
	for _, host := range s.hosts {
		if !fn(host) {
			return
		}
	}
}

func (s stubFlowRuntimeStore) RangeHostsByClientID(clientID int, fn func(*file.Host) bool) {
	for _, host := range s.hosts {
		if host == nil || host.Client == nil || host.Client.Id != clientID {
			continue
		}
		if !fn(host) {
			return
		}
	}
}

func (s stubFlowRuntimeStore) RangeTunnels(fn func(*file.Tunnel) bool) {
	for _, tunnel := range s.tunnels {
		if !fn(tunnel) {
			return
		}
	}
}

func (s stubFlowRuntimeStore) RangeTunnelsByClientID(clientID int, fn func(*file.Tunnel) bool) {
	for _, tunnel := range s.tunnels {
		if tunnel == nil || tunnel.Client == nil || tunnel.Client.Id != clientID {
			continue
		}
		if !fn(tunnel) {
			return
		}
	}
}

type stubFlowBridgeRuntime struct {
	clients map[int]*bridge.Client
	stored  map[int]*bridge.Client
}

func (s *stubFlowBridgeRuntime) LookupClient(id int) (*bridge.Client, bool) {
	client, ok := s.clients[id]
	return client, ok
}

func (s *stubFlowBridgeRuntime) EnsureClient(id int, client *bridge.Client) {
	if s.clients == nil {
		s.clients = make(map[int]*bridge.Client)
	}
	if s.stored == nil {
		s.stored = make(map[int]*bridge.Client)
	}
	s.clients[id] = client
	s.stored[id] = client
}

func TestRuntimeFlowCoordinatorRefreshClientsAggregatesTrafficAndConnectionState(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	activeNode := bridge.NewNode("node-a", "1.2.3", 0)
	activeNode.AddSignal(conn.NewConn(left))
	activeBridgeClient := bridge.NewClient(1, activeNode)
	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			1: {Id: 1, Status: true, Flow: &file.Flow{}},
			2: {Id: 2, Status: true, Flow: &file.Flow{}},
		},
		hosts: []*file.Host{
			{Client: &file.Client{Id: 1}, Flow: &file.Flow{InletFlow: 11, ExportFlow: 12}},
		},
		tunnels: []*file.Tunnel{
			{Client: &file.Client{Id: 1}, Flow: &file.Flow{InletFlow: 21, ExportFlow: 22}},
		},
	}
	runtime := &stubFlowBridgeRuntime{
		clients: map[int]*bridge.Client{1: activeBridgeClient},
	}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return now },
		outboundIP:      func() string { return "192.0.2.10" },
	}.RefreshClients()

	client1 := store.clients[1]
	client2 := store.clients[2]
	if !client1.IsConnect || client1.Version != "1.2.3" || client1.LastOnlineTime != now.Format("2006-01-02 15:04:05") {
		t.Fatalf("client1 runtime state = %+v", client1)
	}
	if client1.Addr == "" || client1.LocalAddr == "" {
		t.Fatalf("client1 runtime addresses = %q/%q, want populated values", client1.Addr, client1.LocalAddr)
	}
	if client1.InletFlow != 32 || client1.ExportFlow != 34 {
		t.Fatalf("client1 aggregated flow = %d/%d, want 32/34", client1.InletFlow, client1.ExportFlow)
	}
	if client2.IsConnect {
		t.Fatalf("client2 should remain offline: %+v", client2)
	}
}

func TestRuntimeFlowCoordinatorRefreshClientsSkipsNilHostAndTunnelFlow(t *testing.T) {
	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			1: {Id: 1, Status: true, Flow: &file.Flow{}},
		},
		hosts: []*file.Host{
			{Client: &file.Client{Id: 1}},
			{Client: &file.Client{Id: 1}, Flow: &file.Flow{InletFlow: 5, ExportFlow: 7}},
		},
		tunnels: []*file.Tunnel{
			{Client: &file.Client{Id: 1}},
			{Client: &file.Client{Id: 1}, Flow: &file.Flow{InletFlow: 11, ExportFlow: 13}},
		},
	}

	runtimeFlowCoordinator{
		store:           store,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return time.Unix(1_700_001_000, 0) },
	}.RefreshClients()

	client := store.clients[1]
	if client.InletFlow != 16 || client.ExportFlow != 20 {
		t.Fatalf("client aggregated flow = %d/%d, want 16/20", client.InletFlow, client.ExportFlow)
	}
}

func TestRuntimeFlowCoordinatorRefreshClientsKeepsPurePrimaryVersionWithMultipleRuntimeNodes(t *testing.T) {
	leftPrimary, rightPrimary := net.Pipe()
	t.Cleanup(func() { _ = leftPrimary.Close() })
	t.Cleanup(func() { _ = rightPrimary.Close() })
	leftSecondary, rightSecondary := net.Pipe()
	t.Cleanup(func() { _ = leftSecondary.Close() })
	t.Cleanup(func() { _ = rightSecondary.Close() })

	primaryNode := bridge.NewNode("node-a", "1.2.3", 0)
	primaryNode.AddSignal(conn.NewConn(leftPrimary))
	multiNodeClient := bridge.NewClient(9, primaryNode)
	time.Sleep(2 * time.Millisecond)
	secondaryNode := bridge.NewNode("node-b", "1.2.4", 0)
	secondaryNode.AddSignal(conn.NewConn(leftSecondary))
	multiNodeClient.AddNode(secondaryNode)

	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			9: {Id: 9, Status: true, Flow: &file.Flow{}},
		},
	}
	runtime := &stubFlowBridgeRuntime{
		clients: map[int]*bridge.Client{9: multiNodeClient},
	}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return time.Unix(1_700_000_100, 0) },
	}.RefreshClients()

	if got := store.clients[9].Version; got != "1.2.4" {
		t.Fatalf("client.Version = %q, want primary runtime version 1.2.4", got)
	}
}

func TestRuntimeFlowCoordinatorRefreshClientsUsesPrimaryRuntimeAddresses(t *testing.T) {
	primaryNode := bridge.NewNode("node-a", "1.2.3", 4)
	primaryNode.AddSignal(conn.NewConn(runtimeSignalConn{
		remote: runtimeAddr("198.51.100.10:12345"),
		local:  runtimeAddr("10.0.0.8:9001"),
	}))
	multiNodeClient := bridge.NewClient(10, primaryNode)
	time.Sleep(2 * time.Millisecond)
	secondaryNode := bridge.NewNode("node-b", "1.2.4", 4)
	secondaryNode.AddSignal(conn.NewConn(runtimeSignalConn{
		remote: runtimeAddr("203.0.113.7:23456"),
		local:  runtimeAddr("10.0.0.9:9002"),
	}))
	multiNodeClient.AddNode(secondaryNode)

	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			10: {Id: 10, Status: true, Flow: &file.Flow{}},
		},
	}
	runtime := &stubFlowBridgeRuntime{
		clients: map[int]*bridge.Client{10: multiNodeClient},
	}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return time.Unix(1_700_000_200, 0) },
	}.RefreshClients()

	client := store.clients[10]
	if client.Addr != "203.0.113.7" {
		t.Fatalf("client.Addr = %q, want 203.0.113.7", client.Addr)
	}
	if client.LocalAddr != "10.0.0.9" {
		t.Fatalf("client.LocalAddr = %q, want 10.0.0.9", client.LocalAddr)
	}
	if client.Version != "1.2.4" {
		t.Fatalf("client.Version = %q, want 1.2.4", client.Version)
	}
}

func TestRuntimeFlowCoordinatorRefreshClientsUsesLatestOnlineSnapshotNotLastRouteSelection(t *testing.T) {
	primaryNode := bridge.NewNode("node-a", "1.2.3", 4)
	primaryNode.AddSignal(conn.NewConn(runtimeSignalConn{
		remote: runtimeAddr("198.51.100.10:12345"),
		local:  runtimeAddr("10.0.0.8:9001"),
	}))
	multiNodeClient := bridge.NewClient(11, primaryNode)
	time.Sleep(2 * time.Millisecond)
	secondaryNode := bridge.NewNode("node-b", "1.2.4", 4)
	secondaryNode.AddSignal(conn.NewConn(runtimeSignalConn{
		remote: runtimeAddr("203.0.113.7:23456"),
		local:  runtimeAddr("10.0.0.9:9002"),
	}))
	multiNodeClient.AddNode(secondaryNode)
	multiNodeClient.LastUUID = primaryNode.UUID

	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			11: {Id: 11, Status: true, Flow: &file.Flow{}},
		},
	}
	runtime := &stubFlowBridgeRuntime{
		clients: map[int]*bridge.Client{11: multiNodeClient},
	}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return time.Unix(1_700_000_250, 0) },
	}.RefreshClients()

	client := store.clients[11]
	if client.Addr != "203.0.113.7" {
		t.Fatalf("client.Addr = %q, want latest online addr 203.0.113.7", client.Addr)
	}
	if client.LocalAddr != "10.0.0.9" {
		t.Fatalf("client.LocalAddr = %q, want latest online local addr 10.0.0.9", client.LocalAddr)
	}
	if client.Version != "1.2.4" {
		t.Fatalf("client.Version = %q, want latest online version 1.2.4", client.Version)
	}
}

func TestRuntimeFlowCoordinatorRefreshClientsSkipsBridgeClientsWithoutLiveNodes(t *testing.T) {
	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			3: {Id: 3, Status: true, Flow: &file.Flow{}},
		},
	}
	staleBridgeClient := bridge.NewClient(3, bridge.NewNode("node-stale", "1.2.3", 0))
	bridgeRuntime := bridge.NewTunnel(false, &sync.Map{}, 0)
	bridgeRuntime.Client.Store(3, staleBridgeClient)
	runtime := currentFlowBridgeRuntime{
		resolve: func() *bridge.Bridge {
			return bridgeRuntime
		},
	}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return time.Unix(1_700_000_000, 0) },
	}.RefreshClients()

	if store.clients[3].IsConnect {
		t.Fatalf("client should remain offline when bridge runtime has no live node: %+v", store.clients[3])
	}
}

func TestCurrentFlowBridgeRuntimeLookupClientDropsInvalidEntry(t *testing.T) {
	bridgeRuntime := bridge.NewTunnel(false, &sync.Map{}, 0)
	bridgeRuntime.Client.Store(7, "bad-client-entry")

	runtime := currentFlowBridgeRuntime{
		resolve: func() *bridge.Bridge {
			return bridgeRuntime
		},
	}

	if client, ok := runtime.LookupClient(7); ok || client != nil {
		t.Fatalf("LookupClient(7) = %#v, %v, want nil, false", client, ok)
	}
	if _, ok := bridgeRuntime.Client.Load(7); ok {
		t.Fatal("LookupClient should remove invalid bridge client entry")
	}
}

func TestRuntimeFlowCoordinatorRefreshClientsEnsuresVirtualLocalProxyClient(t *testing.T) {
	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			-1: {Id: -1, Status: true, Flow: &file.Flow{}},
		},
	}
	runtime := &stubFlowBridgeRuntime{clients: map[int]*bridge.Client{}}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return true },
		now:             func() time.Time { return time.Unix(1_700_000_000, 0) },
		outboundIP:      func() string { return "198.51.100.77" },
	}.RefreshClients()

	local := store.clients[-1]
	if !local.IsConnect || local.Mode != "local" || local.LocalAddr != "198.51.100.77" || local.Version != version.VERSION {
		t.Fatalf("local proxy runtime state = %+v", local)
	}
	if _, ok := runtime.stored[-1]; !ok {
		t.Fatal("virtual local proxy client should be inserted into bridge runtime")
	}
}

func TestRuntimeFlowCoordinatorRefreshClientSetOnlyTouchesNamedClients(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	node := bridge.NewNode("node-a", "1.2.8", 0)
	node.AddSignal(conn.NewConn(left))
	store := stubFlowRuntimeStore{
		clients: map[int]*file.Client{
			1: {Id: 1, Status: true, Flow: &file.Flow{}, Addr: "stale-a"},
			2: {Id: 2, Status: true, Flow: &file.Flow{}, Addr: "stale-b"},
		},
		hosts: []*file.Host{
			{Client: &file.Client{Id: 1}, Flow: &file.Flow{InletFlow: 3, ExportFlow: 4}},
			{Client: &file.Client{Id: 2}, Flow: &file.Flow{InletFlow: 30, ExportFlow: 40}},
		},
		tunnels: []*file.Tunnel{
			{Client: &file.Client{Id: 1}, Flow: &file.Flow{InletFlow: 5, ExportFlow: 6}},
			{Client: &file.Client{Id: 2}, Flow: &file.Flow{InletFlow: 50, ExportFlow: 60}},
		},
	}
	runtime := &stubFlowBridgeRuntime{
		clients: map[int]*bridge.Client{1: bridge.NewClient(1, node)},
	}

	runtimeFlowCoordinator{
		store:           store,
		bridge:          runtime,
		allowLocalProxy: func() bool { return false },
		now:             func() time.Time { return now },
	}.RefreshClientSet(map[int]struct{}{1: {}})

	if !store.clients[1].IsConnect || store.clients[1].Version != "1.2.8" {
		t.Fatalf("target client runtime state = %+v", store.clients[1])
	}
	if store.clients[1].InletFlow != 8 || store.clients[1].ExportFlow != 10 {
		t.Fatalf("target client aggregated flow = %d/%d, want 8/10", store.clients[1].InletFlow, store.clients[1].ExportFlow)
	}
	if store.clients[2].IsConnect || store.clients[2].Addr != "stale-b" {
		t.Fatalf("untouched client runtime state = %+v, want original offline stale values", store.clients[2])
	}
	if store.clients[2].InletFlow != 0 || store.clients[2].ExportFlow != 0 {
		t.Fatalf("untouched client aggregated flow = %d/%d, want 0/0", store.clients[2].InletFlow, store.clients[2].ExportFlow)
	}
}

func TestFlowPersistenceCoordinatorFlushesAllStores(t *testing.T) {
	steps := make([]string, 0, 5)

	flowPersistenceCoordinator{
		store: stubFlowPersistenceStore{
			storeUsers:   func() { steps = append(steps, "users") },
			storeHosts:   func() { steps = append(steps, "hosts") },
			storeTasks:   func() { steps = append(steps, "tasks") },
			storeClients: func() { steps = append(steps, "clients") },
			storeGlobal:  func() { steps = append(steps, "global") },
		},
	}.Flush()

	want := []string{"users", "hosts", "tasks", "clients", "global"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("flush steps = %v, want %v", steps, want)
	}
}

type stubFlowPersistenceStore struct {
	storeUsers   func()
	storeHosts   func()
	storeTasks   func()
	storeClients func()
	storeGlobal  func()
}

func (s stubFlowPersistenceStore) StoreUsers() {
	if s.storeUsers != nil {
		s.storeUsers()
	}
}

func (s stubFlowPersistenceStore) StoreHosts() {
	if s.storeHosts != nil {
		s.storeHosts()
	}
}

func (s stubFlowPersistenceStore) StoreTasks() {
	if s.storeTasks != nil {
		s.storeTasks()
	}
}

func (s stubFlowPersistenceStore) StoreClients() {
	if s.storeClients != nil {
		s.storeClients()
	}
}

func (s stubFlowPersistenceStore) StoreGlobal() {
	if s.storeGlobal != nil {
		s.storeGlobal()
	}
}

type observingFlowRuntimeStore struct {
	steps *[]string
}

func (observingFlowRuntimeStore) LoadClient(int) (*file.Client, bool) { return nil, false }

func (s observingFlowRuntimeStore) RangeClients(func(*file.Client) bool) {
	if s.steps != nil {
		*s.steps = append(*s.steps, "refreshClients")
	}
}

func (s observingFlowRuntimeStore) RangeHosts(func(*file.Host) bool) {
	if s.steps != nil {
		*s.steps = append(*s.steps, "refreshHosts")
	}
}

func (s observingFlowRuntimeStore) RangeHostsByClientID(int, func(*file.Host) bool) {
	s.RangeHosts(nil)
}

func (s observingFlowRuntimeStore) RangeTunnels(func(*file.Tunnel) bool) {
	if s.steps != nil {
		*s.steps = append(*s.steps, "refreshTunnels")
	}
}

func (s observingFlowRuntimeStore) RangeTunnelsByClientID(int, func(*file.Tunnel) bool) {
	s.RangeTunnels(nil)
}

func TestNewFlowRuntimeServicesWiresSessionFlushToPersistence(t *testing.T) {
	ticker := &stubFlowSessionTicker{ch: make(chan time.Time)}
	var flushes atomic.Int32
	services := newFlowRuntimeServices(
		nil,
		&flowPersistenceCoordinator{
			store: stubFlowPersistenceStore{
				storeUsers:   func() { flushes.Add(1) },
				storeHosts:   func() { flushes.Add(1) },
				storeTasks:   func() { flushes.Add(1) },
				storeClients: func() { flushes.Add(1) },
				storeGlobal:  func() { flushes.Add(1) },
			},
		},
		&flowSessionManager{
			newTicker: func(time.Duration) flowSessionTicker {
				return ticker
			},
		},
	)

	services.UpdateSession(time.Minute)

	if got := flushes.Load(); got != 5 {
		t.Fatalf("immediate session flush calls = %d, want 5", got)
	}
	if services.session.flush == nil {
		t.Fatal("session flush should be wired to flow runtime persistence")
	}

	services.UpdateSession(0)
	time.Sleep(20 * time.Millisecond)
	if !ticker.stopped.Load() {
		t.Fatal("flow runtime session ticker was not stopped after disabling")
	}
}

func TestFlowRuntimeServicesFlushRefreshesBeforePersistence(t *testing.T) {
	steps := make([]string, 0, 8)
	services := newFlowRuntimeServices(
		&runtimeFlowCoordinator{store: observingFlowRuntimeStore{steps: &steps}},
		&flowPersistenceCoordinator{
			store: stubFlowPersistenceStore{
				storeUsers:   func() { steps = append(steps, "users") },
				storeHosts:   func() { steps = append(steps, "hosts") },
				storeTasks:   func() { steps = append(steps, "tasks") },
				storeClients: func() { steps = append(steps, "clients") },
				storeGlobal:  func() { steps = append(steps, "global") },
			},
		},
		nil,
	)
	now := time.Unix(1_700_000_000, 0)
	services.now = func() time.Time { return now }
	services.reuseWindow = time.Minute
	services.lastRefresh = now

	services.Flush()

	want := []string{
		"refreshClients",
		"refreshHosts",
		"refreshTunnels",
		"users",
		"hosts",
		"tasks",
		"clients",
		"global",
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("flow flush steps = %v, want %v", steps, want)
	}
}

type countingFlowRefreshStore struct {
	runs atomic.Int32
}

func (*countingFlowRefreshStore) LoadClient(int) (*file.Client, bool) { return nil, false }

func (s *countingFlowRefreshStore) RangeClients(func(*file.Client) bool) {
	s.runs.Add(1)
}

func (*countingFlowRefreshStore) RangeHosts(func(*file.Host) bool) {}

func (*countingFlowRefreshStore) RangeHostsByClientID(int, func(*file.Host) bool) {}

func (*countingFlowRefreshStore) RangeTunnels(func(*file.Tunnel) bool) {}

func (*countingFlowRefreshStore) RangeTunnelsByClientID(int, func(*file.Tunnel) bool) {}

type stubFlowSessionTicker struct {
	ch      chan time.Time
	stopped atomic.Bool
}

func (t *stubFlowSessionTicker) Chan() <-chan time.Time {
	return t.ch
}

func (t *stubFlowSessionTicker) Stop() {
	t.stopped.Store(true)
}

type blockingFlowRefreshStore struct {
	runs    atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (*blockingFlowRefreshStore) LoadClient(int) (*file.Client, bool) { return nil, false }

func (s *blockingFlowRefreshStore) RangeClients(func(*file.Client) bool) {
	if s.runs.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
}

func (*blockingFlowRefreshStore) RangeHosts(func(*file.Host) bool) {}

func (*blockingFlowRefreshStore) RangeHostsByClientID(int, func(*file.Host) bool) {}

func (*blockingFlowRefreshStore) RangeTunnels(func(*file.Tunnel) bool) {}

func (*blockingFlowRefreshStore) RangeTunnelsByClientID(int, func(*file.Tunnel) bool) {}

func resetFlowSessionRuntime(t *testing.T) {
	t.Helper()
	updateFlowSession(0)
	oldFlush := runtimeFlowSession.flush
	oldTicker := runtimeFlowSession.newTicker
	t.Cleanup(func() {
		updateFlowSession(0)
		runtimeFlowSession.flush = oldFlush
		runtimeFlowSession.newTicker = oldTicker
	})
}

func TestUpdateFlowSessionFlushesOnlyOnceForRepeatedStarts(t *testing.T) {
	resetFlowSessionRuntime(t)

	var flushes atomic.Int32
	runtimeFlowSession.flush = func() {
		flushes.Add(1)
	}

	ticker := &stubFlowSessionTicker{ch: make(chan time.Time)}
	var creations atomic.Int32
	runtimeFlowSession.newTicker = func(time.Duration) flowSessionTicker {
		creations.Add(1)
		return ticker
	}

	updateFlowSession(time.Minute)
	updateFlowSession(time.Minute)

	if got := flushes.Load(); got != 1 {
		t.Fatalf("flush count after repeated starts = %d, want 1", got)
	}
	if got := creations.Load(); got != 1 {
		t.Fatalf("ticker creation count after repeated starts = %d, want 1", got)
	}

	updateFlowSession(0)
	time.Sleep(20 * time.Millisecond)
	if !ticker.stopped.Load() {
		t.Fatal("flow session ticker was not stopped after disabling")
	}
}

func TestUpdateFlowSessionReplacesTickerWhenIntervalChanges(t *testing.T) {
	resetFlowSessionRuntime(t)

	var flushes atomic.Int32
	runtimeFlowSession.flush = func() {
		flushes.Add(1)
	}

	first := &stubFlowSessionTicker{ch: make(chan time.Time)}
	second := &stubFlowSessionTicker{ch: make(chan time.Time)}
	tickers := []*stubFlowSessionTicker{first, second}
	var creations atomic.Int32
	runtimeFlowSession.newTicker = func(time.Duration) flowSessionTicker {
		idx := int(creations.Add(1)) - 1
		return tickers[idx]
	}

	updateFlowSession(time.Minute)
	updateFlowSession(2 * time.Minute)

	if got := flushes.Load(); got != 2 {
		t.Fatalf("flush count after interval change = %d, want 2", got)
	}
	time.Sleep(20 * time.Millisecond)
	if !first.stopped.Load() {
		t.Fatal("first flow session ticker was not stopped after interval change")
	}
	if second.stopped.Load() {
		t.Fatal("second flow session ticker should still be running before disable")
	}

	updateFlowSession(0)
	time.Sleep(20 * time.Millisecond)
	if !second.stopped.Load() {
		t.Fatal("second flow session ticker was not stopped after disabling")
	}
}

func TestFlowRuntimeServicesRefreshClientsCoalescesConcurrentCalls(t *testing.T) {
	store := &blockingFlowRefreshStore{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	services := newFlowRuntimeServices(
		&runtimeFlowCoordinator{store: store},
		nil,
		nil,
	)

	firstDone := make(chan struct{})
	go func() {
		services.RefreshClients()
		close(firstDone)
	}()

	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("first RefreshClients() did not enter coordinator")
	}

	secondDone := make(chan struct{})
	go func() {
		services.RefreshClients()
		close(secondDone)
	}()

	time.Sleep(20 * time.Millisecond)
	if got := store.runs.Load(); got != 1 {
		t.Fatalf("concurrent refresh runs = %d, want 1 shared refresh", got)
	}

	close(store.release)

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("leader RefreshClients() did not finish")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("follower RefreshClients() did not reuse shared refresh result")
	}
	if got := store.runs.Load(); got != 1 {
		t.Fatalf("refresh runs after completion = %d, want 1", got)
	}
}

func TestFlowRuntimeServicesRefreshClientsSkipsBurstRefreshesWithinReuseWindow(t *testing.T) {
	store := &countingFlowRefreshStore{}
	now := time.Unix(1_700_000_000, 0)
	services := newFlowRuntimeServices(
		&runtimeFlowCoordinator{store: store},
		nil,
		nil,
	)
	services.now = func() time.Time { return now }
	services.reuseWindow = time.Second

	services.RefreshClients()
	services.RefreshClients()

	if got := store.runs.Load(); got != 1 {
		t.Fatalf("refresh runs within reuse window = %d, want 1", got)
	}

	now = now.Add(2 * time.Second)
	services.RefreshClients()

	if got := store.runs.Load(); got != 2 {
		t.Fatalf("refresh runs after reuse window = %d, want 2", got)
	}
}

func TestFlowRuntimeServicesForceRefreshClientsBypassesReuseWindow(t *testing.T) {
	store := &countingFlowRefreshStore{}
	now := time.Unix(1_700_000_000, 0)
	services := newFlowRuntimeServices(
		&runtimeFlowCoordinator{store: store},
		nil,
		nil,
	)
	services.now = func() time.Time { return now }
	services.reuseWindow = time.Minute

	services.RefreshClients()
	services.ForceRefreshClients()

	if got := store.runs.Load(); got != 2 {
		t.Fatalf("force refresh runs = %d, want 2", got)
	}
}

func TestFlowRuntimeServicesRefreshClientsBypassesReuseWindowWhenStateRootChanges(t *testing.T) {
	store := &countingFlowRefreshStore{}
	now := time.Unix(1_700_000_000, 0)
	token := flowRuntimeStateToken{bridge: &bridge.Bridge{}}
	services := newFlowRuntimeServices(
		&runtimeFlowCoordinator{store: store},
		nil,
		nil,
	)
	services.now = func() time.Time { return now }
	services.reuseWindow = time.Minute
	services.stateToken = func() flowRuntimeStateToken { return token }

	services.RefreshClients()
	services.RefreshClients()

	if got := store.runs.Load(); got != 1 {
		t.Fatalf("refresh runs before state change = %d, want 1", got)
	}

	token = flowRuntimeStateToken{bridge: &bridge.Bridge{}, db: &file.DbUtils{}}
	services.RefreshClients()

	if got := store.runs.Load(); got != 2 {
		t.Fatalf("refresh runs after state change = %d, want 2", got)
	}
}
