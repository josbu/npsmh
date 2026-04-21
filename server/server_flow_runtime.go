package server

import (
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/lib/version"
)

type flowRuntimeStore interface {
	LoadClient(int) (*file.Client, bool)
	RangeClients(func(*file.Client) bool)
	RangeHosts(func(*file.Host) bool)
	RangeHostsByClientID(int, func(*file.Host) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeTunnelsByClientID(int, func(*file.Tunnel) bool)
}

type flowBridgeRuntime interface {
	LookupClient(id int) (*bridge.Client, bool)
	EnsureClient(id int, client *bridge.Client)
}

type runtimeFlowCoordinator struct {
	store           flowRuntimeStore
	bridge          flowBridgeRuntime
	allowLocalProxy func() bool
	now             func() time.Time
	outboundIP      func() string
}

type flowRefreshContext struct {
	allowLocalProxy bool
	outboundIP      string
	now             time.Time
	clients         map[int]*file.Client
}

func (c runtimeFlowCoordinator) RefreshClients() {
	if c.store == nil {
		return
	}
	refresh := c.refreshRuntimeContext()
	c.refreshClientRuntimeState(&refresh)
	c.aggregateHostTraffic(refresh.clients)
	c.aggregateTunnelTraffic(refresh.clients)
}

func (c runtimeFlowCoordinator) RefreshClientSet(clientIDs map[int]struct{}) {
	if c.store == nil || len(clientIDs) == 0 {
		return
	}
	refresh := c.refreshRuntimeContext()
	for clientID := range clientIDs {
		c.refreshIndexedClientRuntime(&refresh, clientID)
	}
}

func (c runtimeFlowCoordinator) refreshRuntimeContext() flowRefreshContext {
	refresh := flowRefreshContext{}
	if c.allowLocalProxy != nil {
		refresh.allowLocalProxy = c.allowLocalProxy()
	}
	if c.outboundIP != nil {
		refresh.outboundIP = c.outboundIP()
	}
	refresh.now = time.Now()
	if c.now != nil {
		refresh.now = c.now()
	}
	return refresh
}

func (c runtimeFlowCoordinator) refreshClientRuntimeState(refresh *flowRefreshContext) {
	if refresh == nil {
		return
	}
	c.store.RangeClients(func(client *file.Client) bool {
		refresh.rememberClient(client)
		c.refreshClientRuntime(client, refresh.allowLocalProxy, refresh.outboundIP, refresh.now)
		resetClientAggregatedFlow(client)
		return true
	})
}

func (c runtimeFlowCoordinator) refreshIndexedClientRuntime(refresh *flowRefreshContext, clientID int) {
	if refresh == nil || c.store == nil || clientID == 0 {
		return
	}
	client, ok := c.store.LoadClient(clientID)
	if !ok || client == nil {
		return
	}
	refresh.rememberClient(client)
	c.refreshClientRuntime(client, refresh.allowLocalProxy, refresh.outboundIP, refresh.now)
	resetClientAggregatedFlow(client)
	c.store.RangeHostsByClientID(clientID, func(host *file.Host) bool {
		if host == nil {
			return true
		}
		c.addClientTrafficFromFlow(refresh.clients, clientID, host.Flow)
		return true
	})
	c.store.RangeTunnelsByClientID(clientID, func(tunnel *file.Tunnel) bool {
		if tunnel == nil {
			return true
		}
		c.addClientTrafficFromFlow(refresh.clients, clientID, tunnel.Flow)
		return true
	})
}

func (r *flowRefreshContext) rememberClient(client *file.Client) {
	if r == nil || client == nil || client.Id == 0 {
		return
	}
	if r.clients == nil {
		r.clients = make(map[int]*file.Client)
	}
	r.clients[client.Id] = client
}

func (c runtimeFlowCoordinator) aggregateHostTraffic(clients map[int]*file.Client) {
	c.store.RangeHosts(func(host *file.Host) bool {
		if host == nil || host.Client == nil {
			return true
		}
		c.addClientTrafficFromFlow(clients, host.Client.Id, host.Flow)
		return true
	})
}

func (c runtimeFlowCoordinator) aggregateTunnelTraffic(clients map[int]*file.Client) {
	c.store.RangeTunnels(func(tunnel *file.Tunnel) bool {
		if tunnel == nil || tunnel.Client == nil {
			return true
		}
		c.addClientTrafficFromFlow(clients, tunnel.Client.Id, tunnel.Flow)
		return true
	})
}

func (c runtimeFlowCoordinator) addClientTrafficFromFlow(clients map[int]*file.Client, clientID int, flow *file.Flow) {
	if flow == nil {
		return
	}
	c.addClientTraffic(clients, clientID, flow.InletFlow, flow.ExportFlow)
}

func (c runtimeFlowCoordinator) addClientTraffic(clients map[int]*file.Client, clientID int, inlet, export int64) {
	if clientID == 0 {
		return
	}
	client := clients[clientID]
	if client == nil {
		return
	}
	client.InletFlow += inlet
	client.ExportFlow += export
}

func (c runtimeFlowCoordinator) refreshClientRuntime(client *file.Client, allowLocalProxy bool, outboundIP string, now time.Time) {
	if client == nil {
		return
	}
	if activeClient, ok := c.lookupBridgeClient(client.Id); ok {
		applyBridgeClientRuntime(client, activeClient, now)
		return
	}
	if c.applyVirtualLocalRuntime(client, allowLocalProxy, outboundIP) {
		return
	}
	client.IsConnect = false
}

func resetClientAggregatedFlow(client *file.Client) {
	if client == nil {
		return
	}
	client.InletFlow = 0
	client.ExportFlow = 0
}

func applyBridgeClientRuntime(client *file.Client, activeClient *bridge.Client, now time.Time) {
	if client == nil {
		return
	}
	client.IsConnect = true
	client.LastOnlineTime = now.Format("2006-01-02 15:04:05")
	if snapshot, ok := bridgeClientDisplaySnapshot(activeClient); ok {
		applyBridgeClientDisplaySnapshot(client, snapshot)
		return
	}
	client.Version = bridgeClientVersion(activeClient)
}

func applyBridgeClientDisplaySnapshot(client *file.Client, snapshot bridge.NodeRuntimeSnapshot) {
	if client == nil {
		return
	}
	if versionText := strings.TrimSpace(snapshot.Version); versionText != "" {
		client.Version = versionText
	}
	if addr := strings.TrimSpace(common.GetIpByAddr(snapshot.RemoteAddr)); addr != "" {
		client.Addr = addr
	}
	if localAddr := strings.TrimSpace(common.GetIpByAddr(snapshot.LocalAddr)); localAddr != "" {
		client.LocalAddr = localAddr
	}
}

func (c runtimeFlowCoordinator) applyVirtualLocalRuntime(client *file.Client, allowLocalProxy bool, outboundIP string) bool {
	if client == nil || client.Id > 0 || !allowLocalProxy {
		return false
	}
	client.IsConnect = client.Status
	client.Version = version.VERSION
	client.Mode = "local"
	client.LocalAddr = outboundIP
	if client.Status {
		c.ensureVirtualLocalBridgeClient(client.Id)
	}
	return true
}

func (c runtimeFlowCoordinator) lookupBridgeClient(id int) (*bridge.Client, bool) {
	if c.bridge == nil {
		return nil, false
	}
	return c.bridge.LookupClient(id)
}

func (c runtimeFlowCoordinator) ensureVirtualLocalBridgeClient(id int) {
	if id > 0 || c.bridge == nil {
		return
	}
	if _, exists := c.bridge.LookupClient(id); exists {
		return
	}
	c.bridge.EnsureClient(id, bridge.NewClient(id, bridge.NewNode("127.0.0.1", version.VERSION, version.GetLatestIndex())))
	logs.Debug("Inserted virtual client for ID %d", id)
}

func bridgeClientVersion(client *bridge.Client) string {
	if client == nil {
		return ""
	}
	if snapshot, ok := bridgeClientDisplaySnapshot(client); ok {
		return strings.TrimSpace(snapshot.Version)
	}
	fallbackVersion := ""
	for _, snapshot := range client.SnapshotNodes() {
		versionText := strings.TrimSpace(snapshot.Version)
		if fallbackVersion == "" && versionText != "" {
			fallbackVersion = versionText
		}
		if snapshot.Online && versionText != "" {
			return versionText
		}
	}
	return fallbackVersion
}

func bridgeClientDisplaySnapshot(client *bridge.Client) (bridge.NodeRuntimeSnapshot, bool) {
	if client == nil {
		return bridge.NodeRuntimeSnapshot{}, false
	}
	return client.DisplayRuntimeSnapshot()
}

type dbFlowRuntimeStore struct{}

func (dbFlowRuntimeStore) RangeClients(fn func(*file.Client) bool) {
	if fn == nil {
		return
	}
	file.GetDb().RangeClients(fn)
}

func (dbFlowRuntimeStore) LoadClient(id int) (*file.Client, bool) {
	db := file.GetDb()
	if !db.Ready() {
		return nil, false
	}
	client, err := db.GetClient(id)
	if err != nil || client == nil {
		return nil, false
	}
	return client, true
}

func (dbFlowRuntimeStore) RangeHosts(fn func(*file.Host) bool) {
	if fn == nil {
		return
	}
	file.GetDb().RangeHosts(fn)
}

func (dbFlowRuntimeStore) RangeHostsByClientID(clientID int, fn func(*file.Host) bool) {
	if clientID == 0 || fn == nil {
		return
	}
	db := file.GetDb()
	if !db.Ready() {
		return
	}
	db.RangeHostsByClientID(clientID, fn)
}

func (dbFlowRuntimeStore) RangeTunnels(fn func(*file.Tunnel) bool) {
	if fn == nil {
		return
	}
	file.GetDb().RangeTasks(fn)
}

func (dbFlowRuntimeStore) RangeTunnelsByClientID(clientID int, fn func(*file.Tunnel) bool) {
	if clientID == 0 || fn == nil {
		return
	}
	db := file.GetDb()
	if !db.Ready() {
		return
	}
	db.RangeTunnelsByClientID(clientID, fn)
}

type currentFlowBridgeRuntime struct {
	resolve func() *bridge.Bridge
}

func (r currentFlowBridgeRuntime) LookupClient(id int) (*bridge.Client, bool) {
	if r.resolve == nil {
		return nil, false
	}
	bridgeRuntime := r.resolve()
	if bridgeRuntime == nil || bridgeRuntime.Client == nil {
		return nil, false
	}
	return loadOnlineBridgeClientEntry(bridgeRuntime.Client, id)
}

func (r currentFlowBridgeRuntime) EnsureClient(id int, client *bridge.Client) {
	if r.resolve == nil {
		return
	}
	bridgeRuntime := r.resolve()
	if bridgeRuntime == nil || bridgeRuntime.Client == nil || client == nil {
		return
	}
	bridgeRuntime.Client.Store(id, client)
}

type flowPersistenceStore interface {
	StoreUsers()
	StoreHosts()
	StoreTasks()
	StoreClients()
	StoreGlobal()
}

type flowPersistenceCoordinator struct {
	store flowPersistenceStore
}

func (c flowPersistenceCoordinator) Flush() {
	if c.store == nil {
		return
	}
	c.store.StoreUsers()
	c.store.StoreHosts()
	c.store.StoreTasks()
	c.store.StoreClients()
	c.store.StoreGlobal()
}

type dbFlowPersistenceStore struct{}

func (dbFlowPersistenceStore) StoreUsers() {
	file.GetDb().StoreUsers()
}

func (dbFlowPersistenceStore) StoreHosts() {
	file.GetDb().StoreHosts()
}

func (dbFlowPersistenceStore) StoreTasks() {
	file.GetDb().StoreTasks()
}

func (dbFlowPersistenceStore) StoreClients() {
	file.GetDb().StoreClients()
}

func (dbFlowPersistenceStore) StoreGlobal() {
	file.GetDb().StoreGlobal()
}

type flowRuntimeServices struct {
	data        *runtimeFlowCoordinator
	persistence *flowPersistenceCoordinator
	session     *flowSessionManager
	refreshMu   sync.Mutex
	refreshing  chan struct{}
	lastRefresh time.Time
	lastState   flowRuntimeStateToken
	now         func() time.Time
	reuseWindow time.Duration
	stateToken  func() flowRuntimeStateToken
}

var runtimeFlow = newDefaultFlowRuntimeServices()

var runtimeFlowSession = runtimeFlow.session

const flowRefreshReuseWindow = 250 * time.Millisecond

type flowRuntimeStateToken struct {
	bridge *bridge.Bridge
	db     *file.DbUtils
}

func newDefaultFlowRuntimeServices() *flowRuntimeServices {
	return newFlowRuntimeServices(
		&runtimeFlowCoordinator{
			store:           dbFlowRuntimeStore{},
			bridge:          currentFlowBridgeRuntime{resolve: runtimeState.Bridge},
			allowLocalProxy: func() bool { return servercfg.Current().AllowLocalProxyEnabled() },
			now:             time.Now,
			outboundIP: func() string {
				return common.GetOutboundIP().String()
			},
		},
		&flowPersistenceCoordinator{
			store: dbFlowPersistenceStore{},
		},
		&flowSessionManager{
			newTicker: func(d time.Duration) flowSessionTicker {
				return realFlowSessionTicker{Ticker: time.NewTicker(d)}
			},
		},
	)
}

func newFlowRuntimeServices(data *runtimeFlowCoordinator, persistence *flowPersistenceCoordinator, session *flowSessionManager) *flowRuntimeServices {
	services := &flowRuntimeServices{
		data:        data,
		persistence: persistence,
		session:     session,
		now:         time.Now,
		reuseWindow: flowRefreshReuseWindow,
		stateToken: func() flowRuntimeStateToken {
			return flowRuntimeStateToken{
				bridge: runtimeState.Bridge(),
				db:     file.GetDb(),
			}
		},
	}
	if services.session != nil && services.session.flush == nil {
		services.session.flush = services.Flush
	}
	return services
}

func (s *flowRuntimeServices) RefreshClients() {
	s.refreshClients(false)
}

func (s *flowRuntimeServices) ForceRefreshClients() {
	s.refreshClients(true)
}

func (s *flowRuntimeServices) RefreshClient(clientID int) {
	if clientID <= 0 {
		return
	}
	s.refreshClientSet(map[int]struct{}{clientID: {}})
}

func (s *flowRuntimeServices) RefreshClientSet(clientIDs map[int]struct{}) {
	s.refreshClientSet(clientIDs)
}

func (s *flowRuntimeServices) refreshClients(force bool) {
	if s == nil || s.data == nil {
		return
	}
	wait, leader, skipped := s.beginRefreshClients(force)
	if skipped {
		return
	}
	if !leader {
		<-wait
		return
	}
	defer s.finishRefreshClients(true)
	s.data.RefreshClients()
}

func (s *flowRuntimeServices) refreshClientSet(clientIDs map[int]struct{}) {
	if s == nil || s.data == nil || len(clientIDs) == 0 {
		return
	}
	wait, leader, skipped := s.beginRefreshClients(true)
	if skipped {
		return
	}
	if !leader {
		<-wait
		return
	}
	defer s.finishRefreshClients(false)
	s.data.RefreshClientSet(clientIDs)
}

func (s *flowRuntimeServices) beginRefreshClients(force bool) (<-chan struct{}, bool, bool) {
	if s == nil {
		return nil, false, true
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshing != nil {
		return s.refreshing, false, false
	}
	if !force && s.isRefreshFreshLocked() {
		return nil, false, true
	}
	s.refreshing = make(chan struct{})
	return s.refreshing, true, false
}

func (s *flowRuntimeServices) finishRefreshClients(markFresh bool) {
	if s == nil {
		return
	}
	s.refreshMu.Lock()
	refreshing := s.refreshing
	if markFresh {
		s.lastRefresh = s.currentTime()
		s.lastState = s.currentStateToken()
	}
	s.refreshing = nil
	s.refreshMu.Unlock()
	if refreshing != nil {
		close(refreshing)
	}
}

func (s *flowRuntimeServices) Flush() {
	if s == nil || s.persistence == nil {
		return
	}
	s.refreshClients(true)
	s.persistence.Flush()
}

func (s *flowRuntimeServices) isRefreshFreshLocked() bool {
	if s == nil || s.reuseWindow <= 0 || s.lastRefresh.IsZero() {
		return false
	}
	if s.currentStateToken() != s.lastState {
		return false
	}
	return s.currentTime().Sub(s.lastRefresh) < s.reuseWindow
}

func (s *flowRuntimeServices) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

func (s *flowRuntimeServices) currentStateToken() flowRuntimeStateToken {
	if s == nil || s.stateToken == nil {
		return flowRuntimeStateToken{}
	}
	return s.stateToken()
}

func (s *flowRuntimeServices) UpdateSession(interval time.Duration) {
	if s == nil || s.session == nil {
		return
	}
	s.session.Update(interval)
}

func (s *flowRuntimeServices) RunSession(ticker flowSessionTicker, stop <-chan struct{}) {
	if s == nil || s.session == nil {
		return
	}
	s.session.run(ticker, stop)
}

type flowSessionManager struct {
	mu        sync.Mutex
	stop      chan struct{}
	ticker    flowSessionTicker
	interval  time.Duration
	flush     func()
	newTicker func(time.Duration) flowSessionTicker
}

type flowSessionUpdatePlan struct {
	previousStop   chan struct{}
	previousTicker flowSessionTicker
	nextStop       chan struct{}
	nextTicker     flowSessionTicker
	startNew       bool
}

func updateFlowSession(interval time.Duration) {
	runtimeFlow.UpdateSession(interval)
}

func (m *flowSessionManager) Update(interval time.Duration) {
	if m == nil {
		return
	}
	if interval < 0 {
		interval = 0
	}
	plan := m.buildUpdatePlan(interval)
	if plan.previousStop != nil {
		close(plan.previousStop)
	}
	if plan.previousTicker != nil {
		plan.previousTicker.Stop()
	}
	if !plan.startNew {
		return
	}
	if m.flush != nil {
		m.flush()
	}
	go m.run(plan.nextTicker, plan.nextStop)
}

func (m *flowSessionManager) buildUpdatePlan(interval time.Duration) flowSessionUpdatePlan {
	plan := flowSessionUpdatePlan{}
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case interval <= 0:
		plan.previousStop = m.stop
		plan.previousTicker = m.ticker
		m.stop = nil
		m.ticker = nil
		m.interval = 0
	case m.stop != nil && m.interval == interval:
		return plan
	default:
		plan.previousStop = m.stop
		plan.previousTicker = m.ticker
		plan.nextStop = make(chan struct{})
		if m.newTicker != nil {
			plan.nextTicker = m.newTicker(interval)
		}
		m.stop = plan.nextStop
		m.ticker = plan.nextTicker
		m.interval = interval
		plan.startNew = true
	}
	return plan
}

func (m *flowSessionManager) run(ticker flowSessionTicker, stop <-chan struct{}) {
	if ticker == nil {
		return
	}
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case _, ok := <-ticker.Chan():
			if !ok {
				return
			}
			if m.flush != nil {
				m.flush()
			}
		}
	}
}
