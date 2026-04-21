package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/file"
)

func setupServerJsonDB(t *testing.T) func() {
	t.Helper()

	oldDb := file.Db
	oldIndexes := file.SnapshotRuntimeIndexes()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("MkdirAll(conf) error = %v", err)
	}

	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(db)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())

	return func() {
		file.ReplaceDb(oldDb)
		file.ReplaceRuntimeIndexes(oldIndexes)
	}
}

func TestServerListsDropInvalidEntriesAndHandleNilPointers(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{Id: 11, VerifyKey: "vk-11", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	validHost := &file.Host{Id: 31, Host: "valid.example.com", Scheme: "all", Location: "/", Client: client, Target: &file.Target{TargetStr: "127.0.0.1:80"}, Flow: &file.Flow{}}
	if err := db.NewHost(validHost); err != nil {
		t.Fatalf("NewHost(validHost) error = %v", err)
	}

	hostNilTarget := &file.Host{Id: 32, Host: "nil-target.example.com", Scheme: "all", Location: "/", Client: client, Target: &file.Target{TargetStr: "127.0.0.1:81"}, Flow: &file.Flow{}}
	if err := db.NewHost(hostNilTarget); err != nil {
		t.Fatalf("NewHost(hostNilTarget) error = %v", err)
	}
	hostNilTarget.Target = nil
	hostNilClient := &file.Host{Id: 33, Host: "nil-client.example.com", Scheme: "all", Location: "/", Client: client, Target: &file.Target{TargetStr: "127.0.0.1:82"}, Flow: &file.Flow{}}
	if err := db.NewHost(hostNilClient); err != nil {
		t.Fatalf("NewHost(hostNilClient) error = %v", err)
	}
	hostNilClient.Client = nil

	db.JsonDb.Clients.Store("bad-client", "invalid")

	clients, totalClients := GetClientList(0, 0, "", "", "", 0)
	if totalClients != 1 || len(clients) != 1 || clients[0].Id != client.Id {
		t.Fatalf("GetClientList() total=%d len=%d ids=%v, want 1 valid client", totalClients, len(clients), clientListIDs(clients))
	}
	if _, ok := db.JsonDb.Clients.Load("bad-client"); ok {
		t.Fatal("GetClientList() should drop invalid client entry")
	}

	hostsByClient, totalHostsByClient := GetHostListByClientIDs(0, 0, []int{client.Id}, "", "Target.TargetStr", "asc")
	if totalHostsByClient != 2 || len(hostsByClient) != 2 {
		t.Fatalf("GetHostListByClientIDs() total=%d len=%d, want 2 valid hosts", totalHostsByClient, len(hostsByClient))
	}
	if hostsByClient[0].Id != hostNilTarget.Id || hostsByClient[1].Id != validHost.Id {
		t.Fatalf("GetHostListByClientIDs() ids=%v, want [%d %d]", hostListIDs(hostsByClient), hostNilTarget.Id, validHost.Id)
	}
	if _, ok := db.JsonDb.Hosts.Load(hostNilClient.Id); ok {
		t.Fatal("GetHostListByClientIDs() should drop indexed host entries whose client reference is now invalid")
	}

	allHosts, totalHosts := GetHostList(0, 0, 0, "", "Client.VerifyKey", "asc")
	if totalHosts != 2 || len(allHosts) != 2 {
		t.Fatalf("GetHostList() total=%d len=%d, want 2 surviving hosts", totalHosts, len(allHosts))
	}
	if ids := hostListIDs(allHosts); len(ids) != 2 || ids[0] != validHost.Id || ids[1] != hostNilTarget.Id {
		t.Fatalf("GetHostList() ids=%v, want [%d %d]", ids, validHost.Id, hostNilTarget.Id)
	}
}

func TestGetClientListReturnsDetachedSnapshots(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{
		Id:             41,
		VerifyKey:      "vk-41",
		Status:         true,
		RateLimit:      32,
		Cnf:            &file.Config{U: "demo-user", P: "demo-pass"},
		Flow:           &file.Flow{InletFlow: 3, ExportFlow: 4},
		ManagerUserIDs: []int{7, 8},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	list, total := GetClientList(0, 0, "", "", "", 0)
	if total != 1 || len(list) != 1 {
		t.Fatalf("GetClientList() total=%d len=%d, want 1/1", total, len(list))
	}

	list[0].Cnf.U = "mutated-user"
	list[0].Flow.InletFlow = 99
	list[0].ManagerUserIDs[0] = 99

	stored, err := db.GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if list[0].Rate == stored.Rate || list[0].BridgeMeter == stored.BridgeMeter || list[0].ServiceMeter == stored.ServiceMeter || list[0].TotalMeter == stored.TotalMeter {
		t.Fatal("GetClientList() should detach runtime limiter and meter pointers")
	}
	list[0].Rate.ResetLimit(0)
	if got := stored.Rate.Limit(); got != 32*1024 {
		t.Fatalf("stored client rate limit after snapshot mutation = %d, want %d", got, 32*1024)
	}
	if stored.Cnf == nil || stored.Cnf.U != "demo-user" {
		t.Fatalf("stored client config username = %q, want %q", stored.Cnf.U, "demo-user")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 3 {
		t.Fatalf("stored client flow inlet = %d, want 3", stored.Flow.InletFlow)
	}
	if len(stored.ManagerUserIDs) != 2 || stored.ManagerUserIDs[0] != 7 {
		t.Fatalf("stored manager user ids = %v, want [7 8]", stored.ManagerUserIDs)
	}
}

func TestGetClientListByIDUsesDirectLookupAndSearchFilter(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{
		Id:        42,
		VerifyKey: "vk-42",
		Remark:    "target-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	list, total := GetClientList(0, 0, "vk-42", "", "", client.Id)
	if total != 1 || len(list) != 1 || list[0].Id != client.Id {
		t.Fatalf("GetClientList(by id + search) total=%d len=%d ids=%v, want 1 target client", total, len(list), clientListIDs(list))
	}
	if list[0] == client {
		t.Fatal("GetClientList(by id) should return a detached snapshot, not the live pointer")
	}

	list, total = GetClientList(0, 0, "missing", "", "", client.Id)
	if total != 0 || len(list) != 0 {
		t.Fatalf("GetClientList(by id + mismatched search) total=%d len=%d, want 0/0", total, len(list))
	}
}

func TestGetClientListKeepsOwnerLifecycleOnSnapshots(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	owner := &file.User{
		Id:        61,
		Username:  "tenant",
		Password:  "secret",
		Kind:      "local",
		Status:    1,
		ExpireAt:  1_900_000_000,
		FlowLimit: 64 * 1024 * 1024,
		TotalFlow: &file.Flow{},
	}
	if err := db.NewUser(owner); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &file.Client{
		Id:          62,
		VerifyKey:   "vk-62",
		Status:      true,
		OwnerUserID: owner.Id,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	list, total := GetClientList(0, 0, "", "ExpireAt", "asc", 0)
	if total != 1 || len(list) != 1 {
		t.Fatalf("GetClientList() total=%d len=%d, want 1/1", total, len(list))
	}
	if got := list[0].EffectiveExpireAt(); got != owner.ExpireAt {
		t.Fatalf("snapshot EffectiveExpireAt() = %d, want owner expire_at %d", got, owner.ExpireAt)
	}
	if got := list[0].EffectiveFlowLimitBytes(); got != owner.FlowLimit {
		t.Fatalf("snapshot EffectiveFlowLimitBytes() = %d, want owner flow_limit %d", got, owner.FlowLimit)
	}
	if list[0].OwnerUser() == nil {
		t.Fatal("snapshot OwnerUser() = nil, want detached owner snapshot")
	}
	if list[0].OwnerUser() == owner {
		t.Fatal("snapshot OwnerUser() should not alias the live owner object")
	}
	if list[0].OwnerUser().Password != "" || list[0].OwnerUser().TOTPSecret != "" {
		t.Fatalf("snapshot OwnerUser() secrets = password:%q totp:%q, want redacted", list[0].OwnerUser().Password, list[0].OwnerUser().TOTPSecret)
	}
	list[0].OwnerUser().ExpireAt = owner.ExpireAt + 1
	storedOwner, err := db.GetUser(owner.Id)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if storedOwner.ExpireAt != owner.ExpireAt {
		t.Fatalf("stored owner expire_at = %d, want %d", storedOwner.ExpireAt, owner.ExpireAt)
	}
}

func TestGetHostListSortsBeforePaginationAndReturnsDetachedSnapshots(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{
		Id:        51,
		VerifyKey: "vk-51",
		Status:    true,
		RateLimit: 32,
		Cnf:       &file.Config{U: "host-user"},
		Flow:      &file.Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	hosts := []*file.Host{
		{Id: 61, Host: "charlie.example.com", Scheme: "all", Location: "/", Client: client, RateLimit: 16, Target: &file.Target{TargetStr: "127.0.0.1:80"}, Flow: &file.Flow{InletFlow: 1}},
		{Id: 62, Host: "alpha.example.com", Scheme: "all", Location: "/", Client: client, RateLimit: 16, Target: &file.Target{TargetStr: "127.0.0.1:81"}, Flow: &file.Flow{InletFlow: 2}},
		{Id: 63, Host: "bravo.example.com", Scheme: "all", Location: "/", Client: client, RateLimit: 16, Target: &file.Target{TargetStr: "127.0.0.1:82"}, Flow: &file.Flow{InletFlow: 3}},
	}
	for _, host := range hosts {
		if err := db.NewHost(host); err != nil {
			t.Fatalf("NewHost(%d) error = %v", host.Id, err)
		}
	}

	list, total := GetHostList(0, 2, 0, "", "Host", "asc")
	if total != 3 || len(list) != 2 {
		t.Fatalf("GetHostList() total=%d len=%d, want 3/2", total, len(list))
	}
	if list[0].Id != 62 || list[1].Id != 63 {
		t.Fatalf("GetHostList() page ids=%v, want [62 63]", hostListIDs(list))
	}

	list[0].Target.TargetStr = "mutated-target"
	list[0].Flow.InletFlow = 99
	list[0].Client.Cnf.U = "mutated-user"

	stored, err := db.GetHostById(62)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if list[0].Rate == stored.Rate || list[0].ServiceMeter == stored.ServiceMeter {
		t.Fatal("GetHostList() should detach host runtime limiter and meter pointers")
	}
	if list[0].Client == nil || list[0].Client.Rate == nil || stored.Client == nil || stored.Client.Rate == nil {
		t.Fatal("GetHostList() client snapshots should keep runtime limiters available")
	}
	if list[0].Client.Rate == stored.Client.Rate {
		t.Fatal("GetHostList() should detach nested client runtime limiter pointers")
	}
	list[0].Rate.ResetLimit(0)
	if got := stored.Rate.Limit(); got != 16*1024 {
		t.Fatalf("stored host rate limit after snapshot mutation = %d, want %d", got, 16*1024)
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:81" {
		t.Fatalf("stored host target = %q, want %q", stored.Target.TargetStr, "127.0.0.1:81")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 2 {
		t.Fatalf("stored host flow inlet = %d, want 2", stored.Flow.InletFlow)
	}
	if stored.Client == nil || stored.Client.Cnf == nil || stored.Client.Cnf.U != "host-user" {
		t.Fatalf("stored host client username = %q, want %q", stored.Client.Cnf.U, "host-user")
	}
}

func TestListSnapshotHelpersPreserveStoredClientConnectionState(t *testing.T) {
	oldBridge := Bridge
	Bridge = nil
	defer func() {
		Bridge = oldBridge
	}()

	client := &file.Client{
		Id:        91,
		VerifyKey: "vk-91",
		IsConnect: true,
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	host := &file.Host{
		Id:       92,
		Host:     "snapshot.example.com",
		Scheme:   "all",
		Location: "/",
		Client:   client,
		Target:   &file.Target{TargetStr: "127.0.0.1:92"},
		Flow:     &file.Flow{},
	}
	tunnel := &file.Tunnel{
		Id:     93,
		Port:   19093,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &file.Target{TargetStr: "127.0.0.1:93"},
		Flow:   &file.Flow{},
	}

	hostSnapshot := snapshotHostForList(host)
	if hostSnapshot == nil || hostSnapshot.Client == nil || !hostSnapshot.Client.IsConnect {
		t.Fatalf("snapshotHostForList() client = %#v, want connected snapshot copied from stored client", hostSnapshot)
	}
	tunnelSnapshot := snapshotTunnelForList(tunnel)
	if tunnelSnapshot == nil || tunnelSnapshot.Client == nil || !tunnelSnapshot.Client.IsConnect {
		t.Fatalf("snapshotTunnelForList() client = %#v, want connected snapshot copied from stored client", tunnelSnapshot)
	}
	if !client.IsConnect {
		t.Fatal("snapshot helpers should not mutate the stored client connection flag")
	}
}

func TestDelTunnelAndHostByClientCleanupDropsInvalidEntries(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{Id: 21, VerifyKey: "vk-21", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &file.Tunnel{Id: 41, Port: 18441, Mode: "tcp", Status: true, NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:80"}, Flow: &file.Flow{}}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.BindRuntimeOwner("node-a", task)

	host := &file.Host{Id: 51, Host: "cleanup.example.com", Scheme: "all", Location: "/", NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:81"}, Flow: &file.Flow{}}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	host.BindRuntimeOwner("node-a", host)

	invalidTask := &file.Tunnel{Id: 141, Port: 18443, Mode: "tcp", Status: true, NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:84"}, Flow: &file.Flow{}}
	if err := db.NewTask(invalidTask); err != nil {
		t.Fatalf("NewTask(invalidTask) error = %v", err)
	}
	invalidHost := &file.Host{Id: 151, Host: "cleanup-invalid.example.com", Scheme: "all", Location: "/", NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:85"}, Flow: &file.Flow{}}
	if err := db.NewHost(invalidHost); err != nil {
		t.Fatalf("NewHost(invalidHost) error = %v", err)
	}
	db.JsonDb.Tasks.Store(invalidTask.Id, "invalid")
	db.JsonDb.Hosts.Store(invalidHost.Id, "invalid")

	DelTunnelAndHostByClientUUID(client.Id, "node-a")
	if _, ok := db.JsonDb.Tasks.Load(task.Id); ok {
		t.Fatal("DelTunnelAndHostByClientUUID() should delete ownerless no-store task")
	}
	if _, ok := db.JsonDb.Hosts.Load(host.Id); ok {
		t.Fatal("DelTunnelAndHostByClientUUID() should delete ownerless no-store host")
	}
	if _, ok := db.JsonDb.Tasks.Load(141); ok {
		t.Fatal("DelTunnelAndHostByClientUUID() should drop invalid indexed task entry")
	}
	if _, ok := db.JsonDb.Hosts.Load(151); ok {
		t.Fatal("DelTunnelAndHostByClientUUID() should drop invalid indexed host entry")
	}

	task2 := &file.Tunnel{Id: 42, Port: 18442, Mode: "tcp", Status: true, NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:82"}, Flow: &file.Flow{}}
	if err := db.NewTask(task2); err != nil {
		t.Fatalf("NewTask(task2) error = %v", err)
	}
	host2 := &file.Host{Id: 52, Host: "cleanup-2.example.com", Scheme: "all", Location: "/", NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:83"}, Flow: &file.Flow{}}
	if err := db.NewHost(host2); err != nil {
		t.Fatalf("NewHost(host2) error = %v", err)
	}

	invalidTask2 := &file.Tunnel{Id: 142, Port: 18444, Mode: "tcp", Status: true, NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:86"}, Flow: &file.Flow{}}
	if err := db.NewTask(invalidTask2); err != nil {
		t.Fatalf("NewTask(invalidTask2) error = %v", err)
	}
	invalidHost2 := &file.Host{Id: 152, Host: "cleanup-invalid-2.example.com", Scheme: "all", Location: "/", NoStore: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:87"}, Flow: &file.Flow{}}
	if err := db.NewHost(invalidHost2); err != nil {
		t.Fatalf("NewHost(invalidHost2) error = %v", err)
	}
	db.JsonDb.Tasks.Store(invalidTask2.Id, "invalid")
	db.JsonDb.Hosts.Store(invalidHost2.Id, "invalid")
	DelTunnelAndHostByClientId(client.Id, true)
	if _, ok := db.JsonDb.Tasks.Load(task2.Id); ok {
		t.Fatal("DelTunnelAndHostByClientId() should delete matching no-store task")
	}
	if _, ok := db.JsonDb.Hosts.Load(host2.Id); ok {
		t.Fatal("DelTunnelAndHostByClientId() should delete matching no-store host")
	}
	if _, ok := db.JsonDb.Tasks.Load(142); ok {
		t.Fatal("DelTunnelAndHostByClientId() should drop invalid indexed task entry")
	}
	if _, ok := db.JsonDb.Hosts.Load(152); ok {
		t.Fatal("DelTunnelAndHostByClientId() should drop invalid indexed host entry")
	}
}

func TestDashboardStatsStoreDropsInvalidEntries(t *testing.T) {
	restore := setupServerJsonDB(t)
	defer restore()

	db := file.GetDb()
	client := &file.Client{Id: 61, VerifyKey: "vk-61", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &file.Host{Id: 71, Host: "stats.example.com", Scheme: "all", Location: "/", Client: client, Target: &file.Target{TargetStr: "127.0.0.1:90"}, Flow: &file.Flow{}}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	task := &file.Tunnel{Id: 81, Port: 18081, Mode: "tcp", Status: true, Client: client, Target: &file.Target{TargetStr: "127.0.0.1:91"}, Flow: &file.Flow{}}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	db.JsonDb.Clients.Store("bad-client", "invalid")
	db.JsonDb.Hosts.Store("bad-host", "invalid")
	db.JsonDb.Tasks.Store("bad-task", "invalid")

	store := dbDashboardStatsStore{}
	clientCount := 0
	hostCount := 0
	taskCount := 0
	store.RangeClients(func(*file.Client) bool { clientCount++; return true })
	store.RangeHosts(func(*file.Host) bool { hostCount++; return true })
	store.RangeTasks(func(*file.Tunnel) bool { taskCount++; return true })

	if clientCount != 1 || hostCount != 1 || taskCount != 1 {
		t.Fatalf("dashboard store counts = %d/%d/%d, want 1/1/1", clientCount, hostCount, taskCount)
	}
	if _, ok := db.JsonDb.Clients.Load("bad-client"); ok {
		t.Fatal("RangeClients() should drop invalid client entry")
	}
	if _, ok := db.JsonDb.Hosts.Load("bad-host"); ok {
		t.Fatal("RangeHosts() should drop invalid host entry")
	}
	if _, ok := db.JsonDb.Tasks.Load("bad-task"); ok {
		t.Fatal("RangeTasks() should drop invalid task entry")
	}
}

func clientListIDs(list []*file.Client) []int {
	ids := make([]int, 0, len(list))
	for _, item := range list {
		if item != nil {
			ids = append(ids, item.Id)
		}
	}
	return ids
}

func hostListIDs(list []*file.Host) []int {
	ids := make([]int, 0, len(list))
	for _, item := range list {
		if item != nil {
			ids = append(ids, item.Id)
		}
	}
	return ids
}
