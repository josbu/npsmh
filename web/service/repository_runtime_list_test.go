package service

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/server"
)

type backendRuntimeAddr string

func (a backendRuntimeAddr) Network() string { return "tcp" }
func (a backendRuntimeAddr) String() string  { return string(a) }

type backendRuntimeSignalConn struct {
	remote net.Addr
	local  net.Addr
}

type backendRuntimeIdleConnectionCloser struct {
	called bool
}

func (backendRuntimeSignalConn) Read([]byte) (int, error)         { return 0, nil }
func (backendRuntimeSignalConn) Write(b []byte) (int, error)      { return len(b), nil }
func (backendRuntimeSignalConn) Close() error                     { return nil }
func (c backendRuntimeSignalConn) LocalAddr() net.Addr            { return c.local }
func (c backendRuntimeSignalConn) RemoteAddr() net.Addr           { return c.remote }
func (backendRuntimeSignalConn) SetDeadline(time.Time) error      { return nil }
func (backendRuntimeSignalConn) SetReadDeadline(time.Time) error  { return nil }
func (backendRuntimeSignalConn) SetWriteDeadline(time.Time) error { return nil }

func (c *backendRuntimeIdleConnectionCloser) CloseIdleConnections() {
	c.called = true
}

func TestDefaultRepositoryListVisibleClientsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:            11,
		VerifyKey:     "demo-vkey",
		Remark:        "original-client",
		Status:        true,
		Cnf:           &file.Config{U: "demo"},
		Flow:          &file.Flow{},
		EntryAclMode:  file.AclBlacklist,
		EntryAclRules: "127.0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	rows, count := defaultRepository{}.ListVisibleClients(ListClientsInput{
		Visibility: ClientVisibility{IsAdmin: true},
	})
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListVisibleClients() count=%d rows=%d, want 1/1", count, len(rows))
	}

	stored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListVisibleClients() should return cloned clients, not live pointers")
	}

	rows[0].Remark = "mutated-client"
	rows[0].Cnf.U = "mutated"
	rows[0].Flow.Add(5, 6)
	rows[0].EntryAclRules = "8.8.8.8"

	if stored.Remark != "original-client" {
		t.Fatalf("stored client remark = %q, want %q", stored.Remark, "original-client")
	}
	if stored.Cnf == nil || stored.Cnf.U != "demo" {
		t.Fatalf("stored client config mutated = %+v", stored.Cnf)
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored client flow mutated = %+v", stored.Flow)
	}
	if stored.EntryAclRules != "127.0.0.1" {
		t.Fatalf("stored client entry acl mutated = %q", stored.EntryAclRules)
	}
	if legacy := stored.LegacyBlackIPImport(); len(legacy) != 0 {
		t.Fatalf("stored client legacy blacklist should stay cleared, got %v", legacy)
	}
}

func TestDefaultRepositoryListVisibleClientsReturnsWorkingCopiesForScopedQuery(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Remark:    "client-a",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(11) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:        12,
		VerifyKey: "demo-vkey-2",
		Remark:    "client-b",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(12) error = %v", err)
	}

	rows, count := defaultRepository{}.ListVisibleClients(ListClientsInput{
		Visibility: ClientVisibility{ClientIDs: []int{11, 12}},
	})
	if count != 2 || len(rows) != 2 {
		t.Fatalf("ListVisibleClients() count=%d rows=%d, want 2/2", count, len(rows))
	}

	stored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListVisibleClients() scoped query should return cloned clients, not live pointers")
	}

	rows[0].Remark = "mutated-client"
	if stored.Remark != "client-a" {
		t.Fatalf("stored scoped client remark = %q, want %q", stored.Remark, "client-a")
	}
}

func TestDefaultRepositoryListVisibleClientsRefreshesOnlyVisibleRuntimeSet(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	for _, client := range []*file.Client{
		{
			Id:        11,
			VerifyKey: "demo-vkey-11",
			Remark:    "visible-client",
			Status:    true,
			Cnf:       &file.Config{},
			Flow:      &file.Flow{},
			Addr:      "stale-visible-addr",
			LocalAddr: "stale-visible-local",
			Version:   "0.0.1",
		},
		{
			Id:        12,
			VerifyKey: "demo-vkey-12",
			Remark:    "hidden-client",
			Status:    true,
			Cnf:       &file.Config{},
			Flow:      &file.Flow{},
			Addr:      "stale-hidden-addr",
			LocalAddr: "stale-hidden-local",
			Version:   "0.0.2",
		},
	} {
		if err := file.GetDb().NewClient(client); err != nil {
			t.Fatalf("NewClient(%d) error = %v", client.Id, err)
		}
	}

	visibleNode := bridge.NewNode("visible-node", "1.2.4", 4)
	visibleNode.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.21:21000"),
		local:  backendRuntimeAddr("10.0.0.21:9100"),
	}))
	server.Bridge.Client.Store(11, bridge.NewClient(11, visibleNode))

	hiddenNode := bridge.NewNode("hidden-node", "1.2.5", 4)
	hiddenNode.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.22:22000"),
		local:  backendRuntimeAddr("10.0.0.22:9200"),
	}))
	server.Bridge.Client.Store(12, bridge.NewClient(12, hiddenNode))

	rows, count := defaultRepository{}.ListVisibleClients(ListClientsInput{
		Visibility: ClientVisibility{ClientIDs: []int{11}},
	})
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListVisibleClients() count=%d rows=%d, want 1/1", count, len(rows))
	}
	if !rows[0].IsConnect {
		t.Fatal("ListVisibleClients() should refresh runtime fields for visible clients")
	}
	if rows[0].Version != "1.2.4" {
		t.Fatalf("ListVisibleClients() visible client version = %q, want 1.2.4", rows[0].Version)
	}
	if rows[0].Addr != "203.0.113.21" {
		t.Fatalf("ListVisibleClients() visible client addr = %q, want 203.0.113.21", rows[0].Addr)
	}
	if rows[0].LocalAddr != "10.0.0.21" {
		t.Fatalf("ListVisibleClients() visible client local addr = %q, want 10.0.0.21", rows[0].LocalAddr)
	}

	visibleStored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient(11) error = %v", err)
	}
	if !visibleStored.IsConnect || visibleStored.Version != "1.2.4" {
		t.Fatalf("stored visible client runtime state = %+v, want refreshed online snapshot", visibleStored)
	}

	hiddenStored, err := file.GetDb().GetClient(12)
	if err != nil {
		t.Fatalf("GetClient(12) error = %v", err)
	}
	if hiddenStored.IsConnect {
		t.Fatal("ListVisibleClients() should not refresh hidden clients outside the visible set")
	}
	if hiddenStored.Version != "0.0.2" {
		t.Fatalf("hidden client version = %q, want stale value 0.0.2", hiddenStored.Version)
	}
	if hiddenStored.Addr != "stale-hidden-addr" {
		t.Fatalf("hidden client addr = %q, want stale value", hiddenStored.Addr)
	}
	if hiddenStored.LocalAddr != "stale-hidden-local" {
		t.Fatalf("hidden client local addr = %q, want stale value", hiddenStored.LocalAddr)
	}
}

func TestDefaultRepositoryListVisibleClientsDropsInvalidEntryForScopedQuery(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Remark:    "client-a",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(11) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:        12,
		VerifyKey: "demo-vkey-2",
		Remark:    "client-b",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(12) error = %v", err)
	}
	file.GetDb().JsonDb.Clients.Store(999, "bad-client-entry")

	rows, count := defaultRepository{}.ListVisibleClients(ListClientsInput{
		Visibility: ClientVisibility{ClientIDs: []int{11, 12, 999}},
	})

	if count != 2 || len(rows) != 2 {
		t.Fatalf("ListVisibleClients() count=%d rows=%d, want 2/2", count, len(rows))
	}
	if _, ok := file.GetDb().JsonDb.Clients.Load(999); ok {
		t.Fatal("ListVisibleClients() should remove invalid visible client entry")
	}
}

func TestDefaultRepositoryListVisibleClientsAppliesOffsetAndLimitAfterSort(t *testing.T) {
	resetBackendTestDB(t)

	for _, client := range []*file.Client{
		{Id: 11, VerifyKey: "demo-vkey-11", Remark: "client-c", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}},
		{Id: 12, VerifyKey: "demo-vkey-12", Remark: "client-a", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}},
		{Id: 13, VerifyKey: "demo-vkey-13", Remark: "client-b", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}},
	} {
		if err := file.GetDb().NewClient(client); err != nil {
			t.Fatalf("NewClient(%d) error = %v", client.Id, err)
		}
	}

	rows, count := defaultRepository{}.ListVisibleClients(ListClientsInput{
		Offset:     1,
		Limit:      1,
		Sort:       "Remark",
		Order:      "asc",
		Visibility: ClientVisibility{ClientIDs: []int{11, 12, 13}},
	})
	if count != 3 || len(rows) != 1 {
		t.Fatalf("ListVisibleClients() count=%d rows=%d, want 3/1", count, len(rows))
	}
	if rows[0].Id != 13 || rows[0].Remark != "client-b" {
		t.Fatalf("ListVisibleClients() row = %+v, want id=13 remark=client-b after sorted pagination", rows[0])
	}

	stored, err := file.GetDb().GetClient(13)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	rows[0].Remark = "mutated-client"
	if stored.Remark != "client-b" {
		t.Fatalf("stored paginated client remark = %q, want %q", stored.Remark, "client-b")
	}
}

func TestDefaultRuntimeListClientsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:            11,
		VerifyKey:     "demo-vkey",
		Remark:        "original-client",
		Status:        true,
		Cnf:           &file.Config{U: "demo"},
		Flow:          &file.Flow{},
		EntryAclMode:  file.AclBlacklist,
		EntryAclRules: "127.0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	rows, count := defaultRuntime{}.ListClients(0, 0, "", "", "", 0)
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListClients() count=%d rows=%d, want 1/1", count, len(rows))
	}

	stored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListClients() should return cloned clients, not live pointers")
	}

	rows[0].Remark = "mutated-client"
	rows[0].Flow.Add(1, 2)
	if stored.Remark != "original-client" {
		t.Fatalf("stored client remark = %q, want %q", stored.Remark, "original-client")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored client flow mutated = %+v", stored.Flow)
	}
}

func TestDefaultRuntimeListTunnelsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Addr:      "stale-addr",
		LocalAddr: "stale-local",
		Version:   "0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Port:   8080,
		Mode:   "tcp",
		Remark: "original-tunnel",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	node := bridge.NewNode("node-b", "1.2.4", 4)
	node.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.11:21000"),
		local:  backendRuntimeAddr("10.0.0.11:9100"),
	}))
	server.Bridge.Client.Store(11, bridge.NewClient(11, node))

	rows, count := defaultRuntime{}.ListTunnels(0, 0, "", 0, "", "", "")
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListTunnels() count=%d rows=%d, want 1/1", count, len(rows))
	}

	stored, err := file.GetDb().GetTask(21)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListTunnels() should return cloned tunnels, not live pointers")
	}

	rows[0].Remark = "mutated-tunnel"
	rows[0].Flow.Add(3, 4)
	if stored.Remark != "original-tunnel" {
		t.Fatalf("stored tunnel remark = %q, want %q", stored.Remark, "original-tunnel")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored tunnel flow mutated = %+v", stored.Flow)
	}
	if rows[0].Client == nil || !rows[0].Client.IsConnect {
		t.Fatalf("ListTunnels() client runtime state = %#v, want connected client", rows[0].Client)
	}
	if rows[0].Client.Version != "1.2.4" {
		t.Fatalf("ListTunnels() client version = %q, want 1.2.4", rows[0].Client.Version)
	}
	if rows[0].Client.Addr != "203.0.113.11" {
		t.Fatalf("ListTunnels() client addr = %q, want 203.0.113.11", rows[0].Client.Addr)
	}
	if rows[0].Client.LocalAddr != "10.0.0.11" {
		t.Fatalf("ListTunnels() client local addr = %q, want 10.0.0.11", rows[0].Client.LocalAddr)
	}
}

func TestDefaultRuntimeListVisibleTunnelsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewTask(&file.Tunnel{
		Id:     21,
		Port:   8080,
		Mode:   "tcp",
		Remark: "original-visible-tunnel",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:8080"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	rows, count := defaultRuntime{}.ListVisibleTunnels(0, 0, "tcp", []int{11}, "", "", "")
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListVisibleTunnels() count=%d rows=%d, want 1/1", count, len(rows))
	}

	stored, err := file.GetDb().GetTask(21)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListVisibleTunnels() should return detached tunnel snapshots, not live pointers")
	}

	rows[0].Remark = "mutated-tunnel"
	rows[0].Target.TargetStr = "127.0.0.1:8181"
	if stored.Remark != "original-visible-tunnel" {
		t.Fatalf("stored visible tunnel remark = %q, want %q", stored.Remark, "original-visible-tunnel")
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:8080" {
		t.Fatalf("stored visible tunnel target = %+v, want original target", stored.Target)
	}
}

func TestDefaultRuntimeListHostsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Addr:      "stale-addr",
		LocalAddr: "stale-local",
		Version:   "0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:     31,
		Host:   "demo.example.com",
		Scheme: "http",
		Remark: "original-host",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	node := bridge.NewNode("node-c", "1.2.5", 4)
	node.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.12:22000"),
		local:  backendRuntimeAddr("10.0.0.12:9200"),
	}))
	server.Bridge.Client.Store(11, bridge.NewClient(11, node))

	rows, count := defaultRuntime{}.ListHosts(0, 0, 0, "", "", "")
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListHosts() count=%d rows=%d, want 1/1", count, len(rows))
	}

	stored, err := file.GetDb().GetHostById(31)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListHosts() should return cloned hosts, not live pointers")
	}

	rows[0].Remark = "mutated-host"
	rows[0].Flow.Add(5, 6)
	if stored.Remark != "original-host" {
		t.Fatalf("stored host remark = %q, want %q", stored.Remark, "original-host")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored host flow mutated = %+v", stored.Flow)
	}
	if rows[0].Client == nil || !rows[0].Client.IsConnect {
		t.Fatalf("ListHosts() client runtime state = %#v, want connected client", rows[0].Client)
	}
	if rows[0].Client.Version != "1.2.5" {
		t.Fatalf("ListHosts() client version = %q, want 1.2.5", rows[0].Client.Version)
	}
	if rows[0].Client.Addr != "203.0.113.12" {
		t.Fatalf("ListHosts() client addr = %q, want 203.0.113.12", rows[0].Client.Addr)
	}
	if rows[0].Client.LocalAddr != "10.0.0.12" {
		t.Fatalf("ListHosts() client local addr = %q, want 10.0.0.12", rows[0].Client.LocalAddr)
	}
}

func TestDefaultRuntimeListVisibleHostsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:        11,
		VerifyKey: "demo-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := file.GetDb().NewHost(&file.Host{
		Id:     31,
		Host:   "visible.example.com",
		Scheme: "http",
		Remark: "original-visible-host",
		Client: &file.Client{Id: 11},
		Target: &file.Target{TargetStr: "127.0.0.1:9090"},
		Flow:   &file.Flow{},
	}); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	rows, count := defaultRuntime{}.ListVisibleHosts(0, 0, []int{11}, "", "", "")
	if count != 1 || len(rows) != 1 {
		t.Fatalf("ListVisibleHosts() count=%d rows=%d, want 1/1", count, len(rows))
	}

	stored, err := file.GetDb().GetHostById(31)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if rows[0] == stored {
		t.Fatal("ListVisibleHosts() should return detached host snapshots, not live pointers")
	}

	rows[0].Remark = "mutated-host"
	rows[0].Target.TargetStr = "127.0.0.1:9191"
	if stored.Remark != "original-visible-host" {
		t.Fatalf("stored visible host remark = %q, want %q", stored.Remark, "original-visible-host")
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:9090" {
		t.Fatalf("stored visible host target = %+v, want original target", stored.Target)
	}
}

func TestDefaultRuntimeRemoveHostCacheClosesIdleConnections(t *testing.T) {
	oldCache := server.HttpProxyCache
	server.HttpProxyCache = index.NewAnyIntIndex()
	t.Cleanup(func() {
		server.HttpProxyCache = oldCache
	})

	closer := &backendRuntimeIdleConnectionCloser{}
	server.HttpProxyCache.Add(51, closer)

	defaultRuntime{}.RemoveHostCache(51)

	if !closer.called {
		t.Fatal("RemoveHostCache() should close idle connections before removing the cache entry")
	}
	if _, ok := server.HttpProxyCache.Get(51); ok {
		t.Fatal("RemoveHostCache() should remove the cache entry")
	}
}

func TestDefaultRepositoryGetClientRefreshesRuntimeFields(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	if err := file.GetDb().NewClient(&file.Client{
		Id:        21,
		VerifyKey: "demo-vkey",
		Remark:    "runtime-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Addr:      "stale-addr",
		LocalAddr: "stale-local",
		Version:   "0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	node := bridge.NewNode("node-b", "1.2.4", 4)
	node.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.7:23456"),
		local:  backendRuntimeAddr("10.0.0.9:9002"),
	}))
	server.Bridge.Client.Store(21, bridge.NewClient(21, node))

	client, err := defaultRepository{}.GetClient(21)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if !client.IsConnect {
		t.Fatal("GetClient() should reflect online runtime state")
	}
	if client.Version != "1.2.4" {
		t.Fatalf("client.Version = %q, want 1.2.4", client.Version)
	}
	if client.Addr != "203.0.113.7" {
		t.Fatalf("client.Addr = %q, want 203.0.113.7", client.Addr)
	}
	if client.LocalAddr != "10.0.0.9" {
		t.Fatalf("client.LocalAddr = %q, want 10.0.0.9", client.LocalAddr)
	}
}

func TestDefaultRepositoryGetClientByVerifyKeyRefreshesRuntimeFields(t *testing.T) {
	resetBackendTestDB(t)

	oldBridge := server.Bridge
	server.Bridge = bridge.NewTunnel(false, &sync.Map{}, 0)
	t.Cleanup(func() {
		server.Bridge = oldBridge
	})

	if err := file.GetDb().NewClient(&file.Client{
		Id:        22,
		VerifyKey: "vk-runtime",
		Remark:    "runtime-client-by-vkey",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Addr:      "stale-addr",
		LocalAddr: "stale-local",
		Version:   "0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	node := bridge.NewNode("node-c", "1.2.6", 4)
	node.AddSignal(conn.NewConn(backendRuntimeSignalConn{
		remote: backendRuntimeAddr("203.0.113.8:23457"),
		local:  backendRuntimeAddr("10.0.0.10:9003"),
	}))
	server.Bridge.Client.Store(22, bridge.NewClient(22, node))

	client, err := defaultRepository{}.GetClientByVerifyKey("vk-runtime")
	if err != nil {
		t.Fatalf("GetClientByVerifyKey() error = %v", err)
	}
	if !client.IsConnect {
		t.Fatal("GetClientByVerifyKey() should reflect online runtime state")
	}
	if client.Version != "1.2.6" {
		t.Fatalf("client.Version = %q, want 1.2.6", client.Version)
	}
	if client.Addr != "203.0.113.8" {
		t.Fatalf("client.Addr = %q, want 203.0.113.8", client.Addr)
	}
	if client.LocalAddr != "10.0.0.10" {
		t.Fatalf("client.LocalAddr = %q, want 10.0.0.10", client.LocalAddr)
	}
}
