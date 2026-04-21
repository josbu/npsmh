package server

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/proxy"
)

type stubServerStore struct {
	userErr error
	users   map[int]*file.User
}

func (s stubServerStore) GetUser(id int) (*file.User, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	user, ok := s.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (stubServerStore) GetUserByUsername(string) (*file.User, error) {
	return nil, errors.New("not implemented")
}

func (stubServerStore) CreateUser(*file.User) error { return errors.New("not implemented") }

func (stubServerStore) UpdateUser(*file.User) error { return errors.New("not implemented") }

func (stubServerStore) GetClient(string) (*file.Client, error) {
	return nil, errors.New("not implemented")
}

func (stubServerStore) GetClientByID(int) (*file.Client, error) {
	return nil, errors.New("not implemented")
}

func (stubServerStore) UpdateClient(*file.Client) error { return errors.New("not implemented") }

func (stubServerStore) GetAllClients() []*file.Client { return nil }

func (stubServerStore) GetClientsByUserId(int) []*file.Client { return nil }

func (stubServerStore) GetTunnelsByUserId(int) int { return 0 }

func (stubServerStore) GetHostsByUserId(int) int { return 0 }

func (stubServerStore) AddTraffic(int, int64, int64) {}

func (stubServerStore) Flush() error { return nil }

type stubServerService struct {
	closeCalls int
}

func (s *stubServerService) Start() error { return nil }

func (s *stubServerService) Close() error {
	s.closeCalls++
	return nil
}

func withGlobalStore(t *testing.T, value file.Store) {
	t.Helper()
	oldStore := file.GlobalStore
	file.GlobalStore = value
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})
}

func resetServerTestDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	oldDb := file.Db
	oldIndexes := file.SnapshotRuntimeIndexes()
	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(db)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())
	t.Cleanup(func() {
		file.ReplaceDb(oldDb)
		file.ReplaceRuntimeIndexes(oldIndexes)
	})
	return root
}

func resetServerRuntimeGlobals(t *testing.T) {
	t.Helper()
	oldBridge := Bridge
	Bridge = nil
	RunList = sync.Map{}
	t.Cleanup(func() {
		Bridge = oldBridge
		RunList = sync.Map{}
	})
}

func TestShouldDisconnectClient(t *testing.T) {
	now := time.Now().Unix()

	t.Run("nil client stays disconnected false", func(t *testing.T) {
		withGlobalStore(t, nil)
		if shouldDisconnectClient(now, nil) {
			t.Fatal("shouldDisconnectClient(nil) = true, want false")
		}
	})

	t.Run("disabled client disconnects", func(t *testing.T) {
		withGlobalStore(t, nil)
		if !shouldDisconnectClient(now, &file.Client{Status: false}) {
			t.Fatal("shouldDisconnectClient(disabled) = false, want true")
		}
	})

	t.Run("missing user disconnects", func(t *testing.T) {
		withGlobalStore(t, stubServerStore{userErr: errors.New("missing user")})
		if !shouldDisconnectClient(now, &file.Client{Status: true, UserId: 7, Flow: &file.Flow{}}) {
			t.Fatal("shouldDisconnectClient(missing user) = false, want true")
		}
	})

	t.Run("disabled user disconnects", func(t *testing.T) {
		withGlobalStore(t, stubServerStore{users: map[int]*file.User{
			7: {Id: 7, Status: 0, TotalFlow: &file.Flow{}},
		}})
		if !shouldDisconnectClient(now, &file.Client{Status: true, UserId: 7, Flow: &file.Flow{}}) {
			t.Fatal("shouldDisconnectClient(disabled user) = false, want true")
		}
	})

	t.Run("expired user disconnects", func(t *testing.T) {
		withGlobalStore(t, stubServerStore{users: map[int]*file.User{
			7: {Id: 7, Status: 1, ExpireAt: now, TotalFlow: &file.Flow{}},
		}})
		if !shouldDisconnectClient(now, &file.Client{Status: true, UserId: 7, Flow: &file.Flow{}}) {
			t.Fatal("shouldDisconnectClient(expired user) = false, want true")
		}
	})

	t.Run("user flow limit disconnects", func(t *testing.T) {
		withGlobalStore(t, stubServerStore{users: map[int]*file.User{
			7: {Id: 7, Status: 1, FlowLimit: 10, TotalFlow: &file.Flow{InletFlow: 4, ExportFlow: 6}},
		}})
		if !shouldDisconnectClient(now, &file.Client{Status: true, UserId: 7, Flow: &file.Flow{}}) {
			t.Fatal("shouldDisconnectClient(user flow limit) = false, want true")
		}
	})

	t.Run("healthy user stays connected", func(t *testing.T) {
		withGlobalStore(t, stubServerStore{users: map[int]*file.User{
			7: {Id: 7, Status: 1, ExpireAt: now + 60, FlowLimit: 100, TotalFlow: &file.Flow{InletFlow: 1, ExportFlow: 2}},
		}})
		if shouldDisconnectClient(now, &file.Client{Status: true, UserId: 7, Flow: &file.Flow{}}) {
			t.Fatal("shouldDisconnectClient(healthy user) = true, want false")
		}
	})

	t.Run("owner user id fallback disconnects", func(t *testing.T) {
		withGlobalStore(t, stubServerStore{users: map[int]*file.User{
			7: {Id: 7, Status: 0, TotalFlow: &file.Flow{}},
		}})
		if !shouldDisconnectClient(now, &file.Client{Status: true, OwnerUserID: 7, Flow: &file.Flow{}}) {
			t.Fatal("shouldDisconnectClient(owner user id fallback) = false, want true")
		}
	})

	t.Run("user scoped client falls back to standalone lifecycle when store is unavailable", func(t *testing.T) {
		withGlobalStore(t, nil)
		client := &file.Client{Status: true, UserId: 7, Flow: &file.Flow{}}
		client.SetExpireAt(now)
		if !shouldDisconnectClient(now, client) {
			t.Fatal("shouldDisconnectClient(user client without store) = false, want standalone lifecycle fallback")
		}
	})

	t.Run("expired client disconnects", func(t *testing.T) {
		withGlobalStore(t, nil)
		client := &file.Client{Status: true, Flow: &file.Flow{}}
		client.SetExpireAt(now)
		if !shouldDisconnectClient(now, client) {
			t.Fatal("shouldDisconnectClient(expired client) = false, want true")
		}
	})

	t.Run("client flow limit disconnects", func(t *testing.T) {
		withGlobalStore(t, nil)
		client := &file.Client{Status: true, Flow: &file.Flow{InletFlow: 6, ExportFlow: 4}}
		client.SetFlowLimitBytes(10)
		if !shouldDisconnectClient(now, client) {
			t.Fatal("shouldDisconnectClient(client flow limit) = false, want true")
		}
	})

	t.Run("healthy standalone client stays connected", func(t *testing.T) {
		withGlobalStore(t, nil)
		client := &file.Client{Status: true, Flow: &file.Flow{InletFlow: 1, ExportFlow: 2}}
		client.SetExpireAt(now + 60)
		client.SetFlowLimitBytes(100)
		if shouldDisconnectClient(now, client) {
			t.Fatal("shouldDisconnectClient(healthy standalone client) = true, want false")
		}
	})
}

func TestDisconnectOrphanClients(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)

	client := &file.Client{
		Id:        1,
		VerifyKey: "kept-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	Bridge.Client.Store(1, bridge.NewClient(1, bridge.NewNode("keep", "test", 0)))
	Bridge.Client.Store(2, bridge.NewClient(2, bridge.NewNode("drop", "test", 0)))
	Bridge.Client.Store(-1, bridge.NewClient(-1, bridge.NewNode("local", "test", 0)))
	Bridge.Client.Store("ignored", bridge.NewClient(99, bridge.NewNode("ignored", "test", 0)))
	Bridge.Client.Store(3, "bad-client-entry")

	DisconnectOrphanClients()

	if _, ok := Bridge.Client.Load(1); !ok {
		t.Fatal("existing client should remain in Bridge.Client")
	}
	if _, ok := Bridge.Client.Load(2); ok {
		t.Fatal("orphan client should be removed from Bridge.Client")
	}
	if _, ok := Bridge.Client.Load(-1); !ok {
		t.Fatal("non-positive client id should be ignored by orphan cleanup")
	}
	if _, ok := Bridge.Client.Load("ignored"); ok {
		t.Fatal("non-int bridge client key should be dropped by orphan cleanup")
	}
	if _, ok := Bridge.Client.Load(3); ok {
		t.Fatal("invalid positive-id bridge client entry should be dropped by orphan cleanup")
	}
}

func TestRuntimeRegistryLookupDialerGuardsMalformedTunnelModeServer(t *testing.T) {
	tests := []struct {
		name string
		task *file.Tunnel
	}{
		{
			name: "nil task",
			task: nil,
		},
		{
			name: "nil target",
			task: &file.Tunnel{Id: 201},
		},
		{
			name: "tunnel target",
			task: &file.Tunnel{
				Id:     202,
				Target: &file.Target{TargetStr: "tunnel://next-hop"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := 900 + len(tt.name)
			svr := proxy.NewTunnelModeServer(proxy.ProcessTunnel, nil, tt.task)
			runtimeTasks.Store(id, svr)
			t.Cleanup(func() {
				runtimeTasks.Delete(id)
			})

			dialer, ok := runtimeTasks.LookupDialer(id)
			if ok || dialer != nil {
				t.Fatalf("LookupDialer() = (%v, %v), want (nil, false)", dialer, ok)
			}
		})
	}
}

func TestRuntimeRegistryLookupDialerReturnsTunnelModeServerForNormalTarget(t *testing.T) {
	id := 950
	svr := proxy.NewTunnelModeServer(proxy.ProcessTunnel, nil, &file.Tunnel{
		Id:     203,
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
	})
	runtimeTasks.Store(id, svr)
	t.Cleanup(func() {
		runtimeTasks.Delete(id)
	})

	dialer, ok := runtimeTasks.LookupDialer(id)
	if !ok || dialer == nil {
		t.Fatalf("LookupDialer() = (%v, %v), want non-nil dialer", dialer, ok)
	}
	if dialer != svr {
		t.Fatalf("LookupDialer() dialer = %v, want %v", dialer, svr)
	}
}

func TestStartManagedTasksFromDBRegistersSecretTasks(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	client := &file.Client{
		Id:        1,
		VerifyKey: "managed-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runnable := &file.Tunnel{Id: 11, Mode: "secret", Status: true, Remark: "secret-enabled", Client: client}
	disabled := &file.Tunnel{Id: 12, Mode: "secret", Status: false, Remark: "secret-disabled", Client: client}
	preloaded := &file.Tunnel{Id: 13, Mode: "secret", Status: true, Remark: "secret-preloaded", Client: client}
	for _, task := range []*file.Tunnel{runnable, disabled, preloaded} {
		if err := file.GetDb().NewTask(task); err != nil {
			t.Fatalf("NewTask(%d) error = %v", task.Id, err)
		}
	}

	marker := &struct{ name string }{"already-running"}
	RunList.Store(preloaded.Id, marker)

	StartManagedTasksFromDB()

	if value, ok := RunList.Load(runnable.Id); !ok || value != nil {
		t.Fatalf("runnable secret task not registered correctly, value=%v ok=%v", value, ok)
	}
	if _, ok := RunList.Load(disabled.Id); ok {
		t.Fatal("disabled task should not be started")
	}
	if value, ok := RunList.Load(preloaded.Id); !ok || value != marker {
		t.Fatalf("preloaded task should be preserved, value=%v ok=%v", value, ok)
	}
}

func TestStopManagedTasksPreserveStatusStopsRuntimeOnly(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	client := &file.Client{
		Id:        1,
		VerifyKey: "runtime-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &file.Tunnel{Id: 21, Mode: "secret", Status: true, Remark: "runtime-stop", Client: client}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	service := &stubServerService{}
	RunList.Store(task.Id, service)

	StopManagedTasksPreserveStatus()

	if service.closeCalls != 1 {
		t.Fatalf("Close() call count = %d, want 1", service.closeCalls)
	}
	if _, ok := RunList.Load(task.Id); ok {
		t.Fatal("runtime entry should be removed after StopManagedTasksPreserveStatus")
	}
	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !stored.Status {
		t.Fatal("StopManagedTasksPreserveStatus should not mutate persisted task status")
	}
}

func TestCloneDashboardDataCreatesIndependentNestedCopies(t *testing.T) {
	source := map[string]interface{}{
		"nested": map[string]interface{}{
			"count": 1,
		},
		"items": []interface{}{
			map[string]interface{}{"name": "alpha"},
			"beta",
		},
	}

	cloned := cloneDashboardData(source)
	nested := cloned["nested"].(map[string]interface{})
	nested["count"] = 2
	items := cloned["items"].([]interface{})
	items[0].(map[string]interface{})["name"] = "changed"

	if got := source["nested"].(map[string]interface{})["count"]; got != 1 {
		t.Fatalf("source nested map mutated = %v, want 1", got)
	}
	if got := source["items"].([]interface{})[0].(map[string]interface{})["name"]; got != "alpha" {
		t.Fatalf("source slice map mutated = %v, want alpha", got)
	}
}

func TestIntStringOrEmpty(t *testing.T) {
	if got := intStringOrEmpty(0); got != "" {
		t.Fatalf("intStringOrEmpty(0) = %q, want empty string", got)
	}
	if got := intStringOrEmpty(42); got != "42" {
		t.Fatalf("intStringOrEmpty(42) = %q, want 42", got)
	}
}

func TestApplyRuntimeConfigUpdatesBridgeAndCreatesRuntimeClients(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	cfg := &servercfg.Snapshot{
		Feature: servercfg.FeatureConfig{
			AllowLocalProxy: true,
		},
		Runtime: servercfg.RuntimeConfig{
			IPLimit:           "true",
			DisconnectTimeout: 17,
			PublicVKey:        "public-access",
			VisitorVKey:       "visitor-access",
		},
	}

	ApplyRuntimeConfig(cfg)

	if !Bridge.IPVerifyEnabled() {
		t.Fatal("ApplyRuntimeConfig() should enable bridge ip verification")
	}
	if got := Bridge.DisconnectTimeout(); got != 17 {
		t.Fatalf("ApplyRuntimeConfig() disconnect timeout = %d, want 17", got)
	}

	localClient, err := file.GetDb().GetClient(-1)
	if err != nil {
		t.Fatalf("GetClient(-1) error = %v", err)
	}
	if localClient == nil || localClient.VerifyKey != "localproxy" || !localClient.ConfigConnAllow || !localClient.NoStore {
		t.Fatalf("local proxy client = %+v", localClient)
	}
	if localByVerifyKey, err := file.GetDb().GetClientByVerifyKey("localproxy"); err != nil || localByVerifyKey == nil || localByVerifyKey.Id != localClient.Id {
		t.Fatalf("GetClientByVerifyKey(localproxy) = %+v, %v, want local client id %d", localByVerifyKey, err, localClient.Id)
	}

	publicClient, err := file.GetDb().GetClientByVerifyKey("public-access")
	if err != nil {
		t.Fatalf("GetClientByVerifyKey(public-access) error = %v", err)
	}
	if publicClient.Remark != "public_vkey" || !publicClient.NoStore || !publicClient.NoDisplay {
		t.Fatalf("public runtime client = %+v", publicClient)
	}
	if _, ok := RunList.Load(publicClient.Id); !ok {
		t.Fatalf("public runtime client id %d should be published in RunList", publicClient.Id)
	}

	visitorClient, err := file.GetDb().GetClientByVerifyKey("visitor-access")
	if err != nil {
		t.Fatalf("GetClientByVerifyKey(visitor-access) error = %v", err)
	}
	if visitorClient.Remark != "visitor_vkey" || !visitorClient.NoStore || !visitorClient.NoDisplay {
		t.Fatalf("visitor runtime client = %+v", visitorClient)
	}
	if _, ok := RunList.Load(visitorClient.Id); ok {
		t.Fatalf("visitor runtime client id %d should not be published in RunList", visitorClient.Id)
	}
}

func TestApplyRuntimeConfigNormalizesNonPositiveDisconnectTimeout(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	cfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			IPLimit:           "false",
			DisconnectTimeout: 0,
		},
	}

	ApplyRuntimeConfig(cfg)

	if got := Bridge.DisconnectTimeout(); got != 30 {
		t.Fatalf("ApplyRuntimeConfig() disconnect timeout = %d, want normalized 30", got)
	}
}

func TestSyncRuntimeVKeyClientsPrunesRemovedRuntimeClients(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)

	stale := file.NewClient("stale-public", true, true)
	stale.Remark = "public_vkey"
	if err := file.GetDb().NewClient(stale); err != nil {
		t.Fatalf("NewClient(stale) error = %v", err)
	}
	runtimeClient := bridge.NewClient(stale.Id, bridge.NewNode("stale-node", "test", 0))
	Bridge.Client.Store(stale.Id, runtimeClient)
	RunList.Store(stale.Id, nil)

	runtimeSpecialClients.SyncVKeys(&servercfg.Snapshot{})

	if _, err := file.GetDb().GetClient(stale.Id); err == nil {
		t.Fatalf("stale runtime client %d should be deleted", stale.Id)
	}
	if _, ok := Bridge.Client.Load(stale.Id); ok {
		t.Fatalf("stale runtime client %d should be removed from Bridge.Client", stale.Id)
	}
	if _, ok := RunList.Load(stale.Id); ok {
		t.Fatalf("stale runtime client %d should be removed from RunList", stale.Id)
	}
	if !runtimeClient.IsClosed() {
		t.Fatalf("stale runtime client %d should be closed before removal", stale.Id)
	}
}

func TestEnsureLocalProxyClientDisablesAndRemovesRuntimeState(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)

	runtimeSpecialClients.EnsureLocalProxy(true)
	localClient, err := file.GetDb().GetClient(-1)
	if err != nil {
		t.Fatalf("GetClient(-1) after enable error = %v", err)
	}
	runtimeClient := bridge.NewClient(-1, bridge.NewNode("local", "test", 0))
	Bridge.Client.Store(-1, runtimeClient)
	RunList.Store(-1, nil)

	runtimeSpecialClients.EnsureLocalProxy(false)

	if _, err := file.GetDb().GetClient(-1); err == nil {
		t.Fatalf("local proxy client should be deleted after disable, stale=%+v", localClient)
	}
	if _, err := file.GetDb().GetClientByVerifyKey("localproxy"); err == nil {
		t.Fatal("local proxy verify-key lookup should be removed after disable")
	}
	if _, ok := Bridge.Client.Load(-1); ok {
		t.Fatal("local proxy runtime client should be removed from Bridge.Client")
	}
	if _, ok := RunList.Load(-1); ok {
		t.Fatal("local proxy runtime client should be removed from RunList")
	}
	if !runtimeClient.IsClosed() {
		t.Fatal("local proxy runtime client should be closed before removal")
	}
}

type idleConnectionCloserStub struct {
	called bool
}

func (s *idleConnectionCloserStub) CloseIdleConnections() {
	s.called = true
}

func TestServerRuntimeStateTracksBridgeAndRunListGlobals(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)
	if runtimeState.Bridge() != runtimeBridge {
		t.Fatalf("runtime state bridge = %+v, want %+v", runtimeState.Bridge(), runtimeBridge)
	}

	first := struct{}{}
	entries := runtimeState.taskEntries()
	if entries == nil {
		t.Fatal("runtime state task entries should not be nil")
	}
	entries.Store(1, first)
	if got, ok := RunList.Load(1); !ok || got != first {
		t.Fatal("runtime state task entries should write through to current RunList")
	}

	RunList = sync.Map{}
	second := struct{}{}
	runtimeState.taskEntries().Store(2, second)
	if got, ok := RunList.Load(2); !ok || got != second {
		t.Fatal("runtime state task entries should follow reset RunList")
	}
}

func TestServerRuntimeStateRemovesHostCache(t *testing.T) {
	oldCache := HttpProxyCache
	HttpProxyCache = index.NewAnyIntIndex()
	t.Cleanup(func() {
		HttpProxyCache = oldCache
	})

	HttpProxyCache.Add(7, "cached")
	runtimeState.RemoveHostCache(7)
	if _, ok := HttpProxyCache.Get(7); ok {
		t.Fatal("runtime state should remove host cache entry")
	}
}

func TestServerRuntimeStateClosesIdleHostCacheConnections(t *testing.T) {
	oldCache := HttpProxyCache
	HttpProxyCache = index.NewAnyIntIndex()
	t.Cleanup(func() {
		HttpProxyCache = oldCache
	})

	closer := &idleConnectionCloserStub{}
	HttpProxyCache.Add(9, closer)
	runtimeState.RemoveHostCache(9)
	if !closer.called {
		t.Fatal("runtime state should close idle host cache connections before removal")
	}
}

func TestHttpProxyCacheClearClosesIdleConnections(t *testing.T) {
	oldCache := HttpProxyCache
	HttpProxyCache = index.NewAnyIntIndex()
	t.Cleanup(func() {
		HttpProxyCache = oldCache
	})

	closer := &idleConnectionCloserStub{}
	HttpProxyCache.Add(11, closer)
	HttpProxyCache.Clear()
	if !closer.called {
		t.Fatal("proxy cache clear should close idle host cache connections")
	}
}

func TestServerRuntimeStateAppliesBridgeConfigAndTracksClients(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)
	left, right := net.Pipe()
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	node := bridge.NewNode("runtime", "test", 0)
	node.AddSignal(conn.NewConn(left))
	runtimeBridge.Client.Store(9, bridge.NewClient(9, node))

	runtimeState.ApplyBridgeConfig(true, 23)

	if !runtimeBridge.IPVerifyEnabled() {
		t.Fatal("runtime state should apply bridge ip verification")
	}
	if got := runtimeBridge.DisconnectTimeout(); got != 23 {
		t.Fatalf("runtime state disconnect timeout = %d, want 23", got)
	}
	if !runtimeState.HasBridgeClient(9) {
		t.Fatal("runtime state should detect bridge client presence")
	}

	runtimeState.DeleteBridgeClient(9)
	if runtimeState.HasBridgeClient(9) {
		t.Fatal("runtime state should delete bridge client")
	}
}

func TestServerRuntimeStateSkipsBridgeClientsWithoutOnlineNodes(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)
	runtimeBridge.Client.Store(13, bridge.NewClient(13, bridge.NewNode("offline", "test", 0)))

	if runtimeState.HasBridgeClient(13) {
		t.Fatal("runtime state should not treat bridge client without online nodes as present")
	}
	if connections := GetClientConnections(13); connections != nil {
		t.Fatalf("GetClientConnections(13) = %#v, want nil for offline runtime client", connections)
	}
	if count := GetClientConnectionCount(13); count != 0 {
		t.Fatalf("GetClientConnectionCount(13) = %d, want 0 for offline runtime client", count)
	}
}

func TestServerRuntimeStateBridgeClientLookupsDropInvalidEntries(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)
	runtimeBridge.Client.Store(11, "bad-client-entry")

	if runtimeState.HasBridgeClient(11) {
		t.Fatal("invalid bridge client entry should not be treated as present")
	}
	if _, ok := runtimeBridge.Client.Load(11); ok {
		t.Fatal("invalid bridge client entry should be removed during typed lookup")
	}

	runtimeBridge.Client.Store(12, "bad-client-entry")
	if connections := GetClientConnections(12); connections != nil {
		t.Fatalf("GetClientConnections(12) = %#v, want nil", connections)
	}
	if _, ok := runtimeBridge.Client.Load(12); ok {
		t.Fatal("GetClientConnections should remove invalid bridge client entry")
	}
}

func TestServerRuntimeStateCleansExpiredRegistrations(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)
	now := time.Now()
	runtimeBridge.Register.Store("expired", now.Add(-time.Minute))
	runtimeBridge.Register.Store("fresh", now.Add(time.Minute))

	removed := runtimeState.CleanupExpiredRegistrations(now)
	if removed != 1 {
		t.Fatalf("runtime state removed %d registrations, want 1", removed)
	}
	if _, ok := runtimeBridge.Register.Load("expired"); ok {
		t.Fatal("runtime state should remove expired registrations")
	}
	if _, ok := runtimeBridge.Register.Load("fresh"); !ok {
		t.Fatal("runtime state should keep fresh registrations")
	}
}

func TestGetClientConnectionsIncludesRuntimeStats(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)

	signalServer, signalPeer := net.Pipe()
	t.Cleanup(func() {
		_ = signalServer.Close()
		_ = signalPeer.Close()
	})
	tunnelServer, tunnelPeer := net.Pipe()
	t.Cleanup(func() {
		_ = tunnelServer.Close()
		_ = tunnelPeer.Close()
	})

	node := bridge.NewNode("node-1", "1.2.3", 6)
	node.AddSignal(conn.NewConn(signalServer))
	node.AddTunnel(mux.NewMux(tunnelServer, "tcp", 0, false))
	node.AddConn()
	node.ObserveBridgeTraffic(10, 20)
	node.ObserveServiceTraffic(30, 40)
	client := bridge.NewClient(21, node)
	offline := bridge.NewNode("node-2", "1.2.3", 6)
	offline.ObserveBridgeTraffic(1, 1)
	client.AddNode(offline)
	runtimeBridge.Client.Store(21, client)

	connections := GetClientConnections(21)
	if len(connections) != 1 {
		t.Fatalf("GetClientConnections(21) len = %d, want 1", len(connections))
	}
	if count := GetClientConnectionCount(21); count != 1 {
		t.Fatalf("GetClientConnectionCount(21) = %d, want 1", count)
	}
	connection := connections[0]
	if connection.NowConn != 1 {
		t.Fatalf("connection.NowConn = %d, want 1", connection.NowConn)
	}
	if connection.BridgeTotalBytes != 30 {
		t.Fatalf("connection.BridgeTotalBytes = %d, want 30", connection.BridgeTotalBytes)
	}
	if connection.ServiceTotalBytes != 70 {
		t.Fatalf("connection.ServiceTotalBytes = %d, want 70", connection.ServiceTotalBytes)
	}
	if connection.TotalBytes != 100 {
		t.Fatalf("connection.TotalBytes = %d, want 100", connection.TotalBytes)
	}
	if count := GetClientConnectionCount(999); count != 0 {
		t.Fatalf("GetClientConnectionCount(999) = %d, want 0", count)
	}
}

func TestGetClientConnectionsIncludesLegacySignalOnlyRuntimeNode(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)

	signalServer, signalPeer := net.Pipe()
	t.Cleanup(func() {
		_ = signalServer.Close()
		_ = signalPeer.Close()
	})

	node := bridge.NewNode("legacy-node", "0.26.0", 4)
	node.AddSignal(conn.NewConn(signalServer))
	client := bridge.NewClient(22, node)
	runtimeBridge.Client.Store(22, client)

	connections := GetClientConnections(22)
	if len(connections) != 1 {
		t.Fatalf("GetClientConnections(22) len = %d, want 1", len(connections))
	}
	if count := GetClientConnectionCount(22); count != 1 {
		t.Fatalf("GetClientConnectionCount(22) = %d, want 1", count)
	}
	if !connections[0].Online {
		t.Fatal("legacy signal-only runtime node should be reported online")
	}
	if !connections[0].HasSignal {
		t.Fatal("legacy signal-only runtime node should report signal availability")
	}
	if connections[0].HasTunnel {
		t.Fatal("legacy signal-only runtime node should not report tunnel availability")
	}
}

func TestGetClientConnectionsIncludesMultipleOnlineRuntimeNodes(t *testing.T) {
	resetServerRuntimeGlobals(t)

	runtimeBridge := bridge.NewTunnel(false, &sync.Map{}, 0)
	runtimeState.AssignBridge(runtimeBridge)

	signalServerA, signalPeerA := net.Pipe()
	signalServerB, signalPeerB := net.Pipe()
	tunnelServerA, tunnelPeerA := net.Pipe()
	tunnelServerB, tunnelPeerB := net.Pipe()
	t.Cleanup(func() {
		_ = signalServerA.Close()
		_ = signalPeerA.Close()
		_ = signalServerB.Close()
		_ = signalPeerB.Close()
		_ = tunnelServerA.Close()
		_ = tunnelPeerA.Close()
		_ = tunnelServerB.Close()
		_ = tunnelPeerB.Close()
	})

	nodeA := bridge.NewNode("node-a", "1.2.3", 6)
	nodeA.AddSignal(conn.NewConn(signalServerA))
	nodeA.AddTunnel(mux.NewMux(tunnelServerA, "tcp", 0, false))
	nodeA.AddConn()
	nodeA.ObserveBridgeTraffic(10, 20)
	nodeA.ObserveServiceTraffic(30, 40)

	nodeB := bridge.NewNode("node-b", "1.2.4", 6)
	nodeB.AddSignal(conn.NewConn(signalServerB))
	nodeB.AddTunnel(mux.NewMux(tunnelServerB, "tcp", 0, false))
	nodeB.AddConn()
	nodeB.AddConn()
	nodeB.ObserveBridgeTraffic(1, 2)
	nodeB.ObserveServiceTraffic(3, 4)

	client := bridge.NewClient(23, nodeA)
	client.AddNode(nodeB)
	runtimeBridge.Client.Store(23, client)

	connections := GetClientConnections(23)
	if len(connections) != 2 {
		t.Fatalf("GetClientConnections(23) len = %d, want 2", len(connections))
	}
	if count := GetClientConnectionCount(23); count != 2 {
		t.Fatalf("GetClientConnectionCount(23) = %d, want 2", count)
	}

	seen := map[string]ClientConnectionInfo{}
	for _, connection := range connections {
		seen[connection.UUID] = connection
	}

	connectionA, ok := seen["node-a"]
	if !ok {
		t.Fatalf("GetClientConnections(23) missing node-a snapshot: %#v", connections)
	}
	if connectionA.NowConn != 1 || connectionA.TotalBytes != 100 {
		t.Fatalf("node-a snapshot = %#v, want now_conn=1 total_bytes=100", connectionA)
	}

	connectionB, ok := seen["node-b"]
	if !ok {
		t.Fatalf("GetClientConnections(23) missing node-b snapshot: %#v", connections)
	}
	if connectionB.NowConn != 2 || connectionB.TotalBytes != 10 {
		t.Fatalf("node-b snapshot = %#v, want now_conn=2 total_bytes=10", connectionB)
	}
}

func TestServerRuntimeStateOpensBridgeLinkAndExposesEvents(t *testing.T) {
	oldState := runtimeState
	t.Cleanup(func() {
		runtimeState = oldState
	})

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	openHost := make(chan *file.Host, 1)
	openTask := make(chan *file.Tunnel, 1)
	secret := make(chan *conn.Secret, 1)
	runtimeState = serverRuntimeState{
		currentBridge:  oldState.currentBridge,
		assignBridge:   oldState.assignBridge,
		taskEntries:    oldState.taskEntries,
		httpProxyCache: oldState.httpProxyCache,
		sendLinkInfo: func(id int, link *conn.Link, tunnel *file.Tunnel) (net.Conn, error) {
			if id != 12 {
				t.Fatalf("OpenBridgeLink client id = %d, want 12", id)
			}
			if tunnel != nil {
				t.Fatalf("OpenBridgeLink tunnel = %+v, want nil", tunnel)
			}
			if link.ConnType != "ping" || !link.Option.NeedAck || link.Option.Timeout != pingTimeout {
				t.Fatalf("OpenBridgeLink link = %+v", link)
			}
			return serverConn, nil
		},
	}
	runtimeState.currentBridge = func() *bridge.Bridge {
		return &bridge.Bridge{
			OpenHost:   openHost,
			OpenTask:   openTask,
			SecretChan: secret,
		}
	}

	link := conn.NewLink("ping", "", false, false, "127.0.0.1", false)
	link.Option.NeedAck = true
	link.Option.Timeout = pingTimeout
	gotConn, err := runtimeState.OpenBridgeLink(12, link, nil)
	if err != nil {
		t.Fatalf("OpenBridgeLink() error = %v", err)
	}
	if gotConn != serverConn {
		t.Fatalf("OpenBridgeLink() conn = %+v, want %+v", gotConn, serverConn)
	}

	events := runtimeState.BridgeEvents()
	if events.openHost != openHost || events.openTask != openTask || events.secret != secret {
		t.Fatal("BridgeEvents() should expose current bridge channels")
	}
}
