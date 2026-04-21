package file

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/crypt"
)

func TestClientTunnelAndHostHelpersDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "helper-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &Tunnel{
		Id:     11,
		Client: client,
		Mode:   "tcp",
		Port:   18001,
		Flow:   &Flow{},
		Target: &Target{TargetStr: "127.0.0.1:80"},
	}
	host := &Host{
		Id:       13,
		Client:   client,
		Host:     "helper.example.com",
		Location: "/",
		Scheme:   "all",
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:81"},
	}
	db.JsonDb.Tasks.Store(task.Id, task)
	db.JsonDb.Hosts.Store(host.Id, host)
	db.JsonDb.Tasks.Store(101, "bad-task")
	db.JsonDb.Hosts.Store(102, "bad-host")
	db.JsonDb.Tasks.Store(103, &Tunnel{Id: 103, Mode: "tcp", Port: 18005, Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:90"}})
	db.JsonDb.Hosts.Store(104, &Host{Id: 104, Host: "nil-client.example.com", Location: "/", Scheme: "all", Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:91"}})

	gotTask, ok := client.HasTunnel(&Tunnel{Client: client, Port: task.Port})
	if !ok || gotTask != task {
		t.Fatalf("HasTunnel() = %#v, %v, want %#v, true", gotTask, ok, task)
	}
	if count := client.GetTunnelNum(); count != 2 {
		t.Fatalf("GetTunnelNum() = %d, want 2", count)
	}
	gotHost, ok := client.HasHost(&Host{Client: client, Host: host.Host, Location: host.Location})
	if !ok || gotHost != host {
		t.Fatalf("HasHost() = %#v, %v, want %#v, true", gotHost, ok, host)
	}

	for _, key := range []int{101, 103} {
		if _, ok := db.JsonDb.Tasks.Load(key); ok {
			t.Fatalf("invalid task entry %d should be dropped by helper scans", key)
		}
	}
	for _, key := range []int{102, 104} {
		if _, ok := db.JsonDb.Hosts.Load(key); ok {
			t.Fatalf("invalid host entry %d should be dropped by helper scans", key)
		}
	}
}

func TestReloadAndAccessPolicyHelpersDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	owner := &User{Id: 3, Username: "owner", Password: "pw", Status: 1, TotalFlow: &Flow{}}
	if err := db.NewUser(owner); err != nil {
		t.Fatalf("NewUser(owner) error = %v", err)
	}
	client := &Client{
		Id:          8,
		VerifyKey:   "reload-client",
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
		OwnerUserID: owner.Id,
		UserId:      owner.Id,
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient(client) error = %v", err)
	}

	tunnel := &Tunnel{
		Id:     21,
		Client: &Client{Id: client.Id},
		Mode:   "tcp",
		Port:   18002,
		Flow:   &Flow{},
		Target: &Target{TargetStr: "127.0.0.1:82"},
	}
	host := &Host{
		Id:       22,
		Client:   &Client{Id: client.Id},
		Host:     "reload.example.com",
		Location: "/",
		Scheme:   "all",
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:83"},
	}
	orphanTunnel := &Tunnel{
		Id:     23,
		Client: &Client{Id: 999},
		Mode:   "tcp",
		Port:   18003,
		Flow:   &Flow{},
		Target: &Target{TargetStr: "127.0.0.1:84"},
	}
	orphanHost := &Host{
		Id:       24,
		Client:   &Client{Id: 999},
		Host:     "orphan.example.com",
		Location: "/",
		Scheme:   "all",
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:85"},
	}
	db.JsonDb.Tasks.Store(tunnel.Id, tunnel)
	db.JsonDb.Hosts.Store(host.Id, host)
	db.JsonDb.Tasks.Store(orphanTunnel.Id, orphanTunnel)
	db.JsonDb.Hosts.Store(orphanHost.Id, orphanHost)

	db.JsonDb.Users.Store(301, "bad-user")
	db.JsonDb.Clients.Store(302, "bad-client")
	db.JsonDb.Tasks.Store(303, "bad-task")
	db.JsonDb.Hosts.Store(304, "bad-host")

	db.JsonDb.rebindLoadedClientOwners()
	if client.OwnerUser() != owner {
		t.Fatalf("OwnerUser() = %#v, want %#v", client.OwnerUser(), owner)
	}

	db.JsonDb.relinkLoadedClientReferences()
	if tunnel.Client != client {
		t.Fatalf("tunnel.Client = %p, want rebound client %p", tunnel.Client, client)
	}
	if host.Client != client {
		t.Fatalf("host.Client = %p, want rebound client %p", host.Client, client)
	}
	if _, err := db.GetTask(orphanTunnel.Id); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("GetTask(orphan) error = %v, want ErrTaskNotFound", err)
	}
	if _, err := db.GetHostById(orphanHost.Id); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("GetHostById(orphan) error = %v, want ErrHostNotFound", err)
	}

	db.JsonDb.Hosts.Store(305, "bad-route-host")
	if !db.JsonDb.loadedHostRouteExists(&Host{Id: 99, Host: host.Host, Location: host.Location, Scheme: "http"}) {
		t.Fatal("loadedHostRouteExists() = false, want true for matching route")
	}

	db.JsonDb.Users.Store(311, "bad-user-policy")
	db.JsonDb.Clients.Store(312, "bad-client-policy")
	db.JsonDb.Tasks.Store(313, "bad-task-policy")
	db.JsonDb.Hosts.Store(314, "bad-host-policy")
	RecompileAccessPoliciesIfLoaded()

	for _, key := range []int{301, 311} {
		if _, ok := db.JsonDb.Users.Load(key); ok {
			t.Fatalf("invalid user entry %d should be dropped", key)
		}
	}
	for _, key := range []int{302, 312} {
		if _, ok := db.JsonDb.Clients.Load(key); ok {
			t.Fatalf("invalid client entry %d should be dropped", key)
		}
	}
	for _, key := range []int{303, 313} {
		if _, ok := db.JsonDb.Tasks.Load(key); ok {
			t.Fatalf("invalid task entry %d should be dropped", key)
		}
	}
	for _, key := range []int{304, 305, 314} {
		if _, ok := db.JsonDb.Hosts.Load(key); ok {
			t.Fatalf("invalid host entry %d should be dropped", key)
		}
	}
}

func TestUserTaskAndMigrationHelpersDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	platformUser := &User{
		Id:                 41,
		Username:           "__platform_a",
		Kind:               "platform_service",
		ExternalPlatformID: "platform-a",
		Hidden:             true,
		Status:             1,
		TotalFlow:          &Flow{},
	}
	if err := db.NewUser(platformUser); err != nil {
		t.Fatalf("NewUser(platformUser) error = %v", err)
	}
	db.JsonDb.Users.Store(401, "bad-platform-user")

	gotUser, err := db.GetUserByExternalPlatformID("platform-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID() error = %v", err)
	}
	if gotUser != platformUser {
		t.Fatalf("GetUserByExternalPlatformID() = %#v, want %#v", gotUser, platformUser)
	}
	if _, err := db.GetUserByExternalPlatformID("missing-platform"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetUserByExternalPlatformID(missing-platform) error = %v, want ErrUserNotFound", err)
	}

	db.JsonDb.Users.Store(402, "bad-platform-user-2")
	if !EnsureManagementPlatformUsers([]ManagementPlatformBinding{{PlatformID: "platform-b", Enabled: true}}) {
		t.Fatal("EnsureManagementPlatformUsers() should report changes when adding a platform service user")
	}
	if _, ok := db.JsonDb.Users.Load(402); ok {
		t.Fatal("invalid user entry should be dropped by EnsureManagementPlatformUsers")
	}
	if _, err := db.GetUserByExternalPlatformID("platform-b"); err != nil {
		t.Fatalf("GetUserByExternalPlatformID(platform-b) error = %v", err)
	}

	client := &Client{
		Id:        42,
		VerifyKey: "task-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient(client) error = %v", err)
	}

	staleHash := crypt.Md5("stale-secret")
	CurrentTaskPasswordIndex().Add(staleHash, 501)
	db.JsonDb.Tasks.Store(501, "bad-task-index")
	if got := db.GetTaskByMd5Password(staleHash); got != nil {
		t.Fatalf("GetTaskByMd5Password() = %#v, want nil for invalid indexed task", got)
	}
	if _, ok := CurrentTaskPasswordIndex().Get(staleHash); ok {
		t.Fatal("stale task password index should be removed")
	}
	if err := db.DelTask(501); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("DelTask(invalid) error = %v, want ErrTaskNotFound", err)
	}
	if _, ok := db.JsonDb.Tasks.Load(501); ok {
		t.Fatal("invalid task entry should be dropped by DelTask")
	}

	db.JsonDb.Tasks.Store(502, "bad-task-update")
	update := &Tunnel{
		Id:     502,
		Client: client,
		Mode:   "tcp",
		Port:   18004,
		Flow:   &Flow{},
		Target: &Target{TargetStr: "127.0.0.1:86"},
	}
	if err := db.UpdateTask(update); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("UpdateTask(invalid) error = %v, want ErrTaskNotFound", err)
	}
	if _, ok := db.JsonDb.Tasks.Load(502); ok {
		t.Fatal("invalid task entry should be dropped by UpdateTask")
	}

	db.JsonDb.Users.Store(503, "bad-user-update")
	if err := db.UpdateUser(&User{Id: 503, Username: "user-503", Status: 1, TotalFlow: &Flow{}}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("UpdateUser(invalid) error = %v, want ErrUserNotFound", err)
	}
	if _, ok := db.JsonDb.Users.Load(503); ok {
		t.Fatal("invalid user entry should be dropped by UpdateUser")
	}

	db.JsonDb.Users.Store(504, "bad-user-delete")
	if err := db.DelUser(504); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("DelUser(invalid) error = %v, want ErrUserNotFound", err)
	}
	if _, ok := db.JsonDb.Users.Load(504); ok {
		t.Fatal("invalid user entry should be dropped by DelUser")
	}

	db.JsonDb.Users.Store(511, "bad-migration-user")
	db.JsonDb.Clients.Store(512, "bad-migration-client")
	db.JsonDb.Tasks.Store(513, "bad-migration-task")
	db.JsonDb.Hosts.Store(514, "bad-migration-host")
	MigrateLegacyData()

	if _, ok := db.JsonDb.Users.Load(511); ok {
		t.Fatal("invalid user entry should be dropped by MigrateLegacyData")
	}
	if _, ok := db.JsonDb.Clients.Load(512); ok {
		t.Fatal("invalid client entry should be dropped by MigrateLegacyData")
	}
	if _, ok := db.JsonDb.Tasks.Load(513); ok {
		t.Fatal("invalid task entry should be dropped by MigrateLegacyData")
	}
	if _, ok := db.JsonDb.Hosts.Load(514); ok {
		t.Fatal("invalid host entry should be dropped by MigrateLegacyData")
	}
}
