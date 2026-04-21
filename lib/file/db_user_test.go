package file

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/djylb/nps/lib/common"
)

func TestDelUserClearsClientUserReferencesAndPersists(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	for _, user := range []*User{
		{Id: 1, Username: "owner-a", Status: 1},
		{Id: 2, Username: "manager-a", Status: 1},
		{Id: 3, Username: "owner-b", Status: 1},
	} {
		if err := db.NewUser(user); err != nil {
			t.Fatalf("NewUser(%s) error = %v", user.Username, err)
		}
	}

	owned := &Client{
		Id:             11,
		VerifyKey:      "vk-owned",
		OwnerUserID:    1,
		UserId:         1,
		ManagerUserIDs: []int{1, 2, 2},
		Status:         true,
		Cnf:            &Config{},
		Flow:           &Flow{},
	}
	managerOnly := &Client{
		Id:             12,
		VerifyKey:      "vk-manager",
		OwnerUserID:    3,
		UserId:         3,
		ManagerUserIDs: []int{1, 2},
		Status:         true,
		Cnf:            &Config{},
		Flow:           &Flow{},
	}
	if err := db.NewClient(owned); err != nil {
		t.Fatalf("NewClient(owned) error = %v", err)
	}
	if err := db.NewClient(managerOnly); err != nil {
		t.Fatalf("NewClient(managerOnly) error = %v", err)
	}

	ownedBeforeRevision := owned.Revision
	managerBeforeRevision := managerOnly.Revision

	if err := db.DelUser(1); err != nil {
		t.Fatalf("DelUser() error = %v", err)
	}

	currentOwned, err := db.GetClient(owned.Id)
	if err != nil {
		t.Fatalf("GetClient(owned) error = %v", err)
	}
	if currentOwned.OwnerID() != 0 || currentOwned.OwnerUser() != nil {
		t.Fatalf("owned client ownership = (%d, %v), want cleared", currentOwned.OwnerID(), currentOwned.OwnerUser())
	}
	if len(currentOwned.ManagerUserIDs) != 1 || currentOwned.ManagerUserIDs[0] != 2 {
		t.Fatalf("owned client manager ids = %v, want [2]", currentOwned.ManagerUserIDs)
	}
	if currentOwned.Revision <= ownedBeforeRevision {
		t.Fatalf("owned client revision = %d, want > %d after user reference cleanup", currentOwned.Revision, ownedBeforeRevision)
	}

	currentManagerOnly, err := db.GetClient(managerOnly.Id)
	if err != nil {
		t.Fatalf("GetClient(managerOnly) error = %v", err)
	}
	if currentManagerOnly.OwnerID() != 3 {
		t.Fatalf("manager-only client owner = %d, want 3", currentManagerOnly.OwnerID())
	}
	if got := db.GetClientIDsByUserId(3); len(got) != 1 || got[0] != managerOnly.Id {
		t.Fatalf("GetClientIDsByUserId(3) after DelUser(1) = %v, want [%d]", got, managerOnly.Id)
	}
	if len(currentManagerOnly.ManagerUserIDs) != 1 || currentManagerOnly.ManagerUserIDs[0] != 2 {
		t.Fatalf("manager-only client manager ids = %v, want [2]", currentManagerOnly.ManagerUserIDs)
	}
	if currentManagerOnly.Revision <= managerBeforeRevision {
		t.Fatalf("manager-only client revision = %d, want > %d after user reference cleanup", currentManagerOnly.Revision, managerBeforeRevision)
	}

	if got := db.GetClientsByUserId(1); len(got) != 0 {
		t.Fatalf("GetClientsByUserId(1) length = %d, want 0 after user deletion", len(got))
	}
	if got := db.GetVisibleManagedClientIDsByUserId(1); len(got) != 0 {
		t.Fatalf("GetVisibleManagedClientIDsByUserId(1) = %v, want empty after user deletion", got)
	}
	if got := db.GetVisibleManagedClientIDsByUserId(2); len(got) != 2 || got[0] != owned.Id || got[1] != managerOnly.Id {
		t.Fatalf("GetVisibleManagedClientIDsByUserId(2) = %v, want [%d %d]", got, owned.Id, managerOnly.Id)
	}
	if indexed := db.JsonDb.managerClientIndex.snapshot(1); len(indexed) != 0 {
		t.Fatalf("managerClientIndex[1] = %v, want empty after user deletion", indexed)
	}

	payload, err := common.ReadAllFromFile(db.JsonDb.ClientFilePath)
	if err != nil {
		t.Fatalf("ReadAllFromFile(clients.json) error = %v", err)
	}
	var stored []Client
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatalf("json.Unmarshal(clients.json) error = %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("len(stored clients) = %d, want 2", len(stored))
	}
	storedByID := make(map[int]*Client, len(stored))
	for i := range stored {
		client := &stored[i]
		storedByID[client.Id] = client
	}
	if storedByID[11].OwnerUserID != 0 || storedByID[11].UserId != 0 {
		t.Fatalf("stored owned client owner fields = (%d,%d), want cleared", storedByID[11].OwnerUserID, storedByID[11].UserId)
	}
	if len(storedByID[11].ManagerUserIDs) != 1 || storedByID[11].ManagerUserIDs[0] != 2 {
		t.Fatalf("stored owned client manager ids = %v, want [2]", storedByID[11].ManagerUserIDs)
	}
	if storedByID[12].OwnerUserID != 3 || storedByID[12].UserId != 3 {
		t.Fatalf("stored manager-only client owner fields = (%d,%d), want 3", storedByID[12].OwnerUserID, storedByID[12].UserId)
	}
	if len(storedByID[12].ManagerUserIDs) != 1 || storedByID[12].ManagerUserIDs[0] != 2 {
		t.Fatalf("stored manager-only client manager ids = %v, want [2]", storedByID[12].ManagerUserIDs)
	}
}

func TestUpdateUserRejectsMissingUser(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.UpdateUser(&User{Id: 42, Username: "missing", Status: 1}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("UpdateUser() error = %v, want %v", err, ErrUserNotFound)
	}
	if _, err := db.GetUser(42); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("GetUser(42) error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestUserScopedLookupsUseOwnerUserIDFallback(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &Client{
		Id:          11,
		VerifyKey:   "tenant-client",
		OwnerUserID: user.Id,
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	host := &Host{
		Id:      31,
		Host:    "tenant.example.com",
		Scheme:  "all",
		Client:  client,
		Target:  &Target{TargetStr: "127.0.0.1:8080"},
		Flow:    &Flow{},
		IsClose: false,
		Remark:  "demo",
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	if got := db.GetClientsByUserId(user.Id); len(got) != 1 || got[0].Id != client.Id {
		t.Fatalf("GetClientsByUserId(%d) = %+v, want client %d", user.Id, got, client.Id)
	}
	if got := db.GetClientIDsByUserId(user.Id); len(got) != 1 || got[0] != client.Id {
		t.Fatalf("GetClientIDsByUserId(%d) = %+v, want client id %d", user.Id, got, client.Id)
	}
	if got := db.GetTunnelsByUserId(user.Id); got != 1 {
		t.Fatalf("GetTunnelsByUserId(%d) = %d, want 1", user.Id, got)
	}
	if got := db.GetHostsByUserId(user.Id); got != 1 {
		t.Fatalf("GetHostsByUserId(%d) = %d, want 1", user.Id, got)
	}
}

func TestGetClientIDsByUserIdPrunesStaleOwnerIndexEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &Client{
		Id:          11,
		VerifyKey:   "tenant-client",
		OwnerUserID: user.Id,
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	db.JsonDb.ownerClientIndex.add(user.Id, 404)

	got := db.GetClientIDsByUserId(user.Id)
	if len(got) != 1 || got[0] != client.Id {
		t.Fatalf("GetClientIDsByUserId(%d) = %v, want [%d]", user.Id, got, client.Id)
	}
	if indexed := db.JsonDb.ownerClientIndex.snapshot(user.Id); len(indexed) != 1 || indexed[0] != client.Id {
		t.Fatalf("ownerClientIndex snapshot = %v, want [%d] after stale prune", indexed, client.Id)
	}
}

func TestCollectOwnedResourceCountsUsesIndexesAndPrunesStaleEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	userA := &User{Id: 7, Username: "tenant-a", Password: "secret", Status: 1}
	userB := &User{Id: 8, Username: "tenant-b", Password: "secret", Status: 1}
	if err := db.NewUser(userA); err != nil {
		t.Fatalf("NewUser(userA) error = %v", err)
	}
	if err := db.NewUser(userB); err != nil {
		t.Fatalf("NewUser(userB) error = %v", err)
	}

	clientA := &Client{
		Id:          11,
		VerifyKey:   "tenant-a-client",
		OwnerUserID: userA.Id,
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
	}
	clientB := &Client{
		Id:          12,
		VerifyKey:   "tenant-b-client",
		OwnerUserID: userB.Id,
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
	}
	if err := db.NewClient(clientA); err != nil {
		t.Fatalf("NewClient(clientA) error = %v", err)
	}
	if err := db.NewClient(clientB); err != nil {
		t.Fatalf("NewClient(clientB) error = %v", err)
	}

	if err := db.NewTask(&Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: clientA,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}); err != nil {
		t.Fatalf("NewTask(clientA) error = %v", err)
	}
	if err := db.NewHost(&Host{
		Id:       31,
		Host:     "tenant-a.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   clientA,
		Target:   &Target{TargetStr: "127.0.0.1:81"},
		Flow:     &Flow{},
	}); err != nil {
		t.Fatalf("NewHost(clientA) error = %v", err)
	}
	if err := db.NewTask(&Tunnel{
		Id:     22,
		Mode:   "tcp",
		Status: true,
		Client: clientB,
		Target: &Target{TargetStr: "127.0.0.1:82"},
		Flow:   &Flow{},
	}); err != nil {
		t.Fatalf("NewTask(clientB) error = %v", err)
	}

	db.JsonDb.Clients.Store(999, "bad-client")
	db.JsonDb.ownerClientIndex.add(userA.Id, 404)
	db.JsonDb.taskClientIndex.add(clientA.Id, 405)
	db.JsonDb.hostClientIndex.add(clientA.Id, 406)

	clientCounts, tunnelCounts, hostCounts := db.CollectOwnedResourceCounts()
	if clientCounts[userA.Id] != 1 || clientCounts[userB.Id] != 1 {
		t.Fatalf("clientCounts = %v, want userA/userB => 1/1", clientCounts)
	}
	if tunnelCounts[userA.Id] != 1 || tunnelCounts[userB.Id] != 1 {
		t.Fatalf("tunnelCounts = %v, want userA/userB => 1/1", tunnelCounts)
	}
	if hostCounts[userA.Id] != 1 || hostCounts[userB.Id] != 0 {
		t.Fatalf("hostCounts = %v, want userA/userB => 1/0", hostCounts)
	}

	if _, ok := db.JsonDb.Clients.Load(999); ok {
		t.Fatal("CollectOwnedResourceCounts() should remove invalid client entry")
	}
	if indexed := db.JsonDb.ownerClientIndex.snapshot(userA.Id); len(indexed) != 1 || indexed[0] != clientA.Id {
		t.Fatalf("ownerClientIndex snapshot = %v, want [%d]", indexed, clientA.Id)
	}
	if indexed := db.JsonDb.managerClientIndex.snapshot(userA.Id); len(indexed) != 0 {
		t.Fatalf("managerClientIndex snapshot = %v, want empty for owner-only clients", indexed)
	}
	if indexed := db.JsonDb.taskClientIndex.snapshot(clientA.Id); len(indexed) != 1 || indexed[0] != 21 {
		t.Fatalf("taskClientIndex snapshot = %v, want [21]", indexed)
	}
	if indexed := db.JsonDb.hostClientIndex.snapshot(clientA.Id); len(indexed) != 1 || indexed[0] != 31 {
		t.Fatalf("hostClientIndex snapshot = %v, want [31]", indexed)
	}
}

func TestDelUserRejectsMissingUser(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.DelUser(404); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("DelUser(404) error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestUserLookupAndRangesDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &Client{
		Id:        11,
		VerifyKey: "tenant-client",
		UserId:    user.Id,
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &Tunnel{
		Id:     21,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	host := &Host{
		Id:       31,
		Host:     "tenant.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Target:   &Target{TargetStr: "127.0.0.1:81"},
		Flow:     &Flow{},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	db.JsonDb.Users.Store("bad-user", "invalid")
	db.JsonDb.Clients.Store("bad-client", "invalid")
	db.JsonDb.Tasks.Store("bad-task", "invalid")
	db.JsonDb.Hosts.Store("bad-host", "invalid")

	lookedUp, err := db.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	}
	if lookedUp.Id != user.Id {
		t.Fatalf("GetUserByUsername(tenant) id = %d, want %d", lookedUp.Id, user.Id)
	}
	if got := db.GetClientsByUserId(user.Id); len(got) != 1 || got[0].Id != client.Id {
		t.Fatalf("GetClientsByUserId(%d) = %v, want [%d]", user.Id, got, client.Id)
	}
	if got := db.GetTunnelsByUserId(user.Id); got != 1 {
		t.Fatalf("GetTunnelsByUserId(%d) = %d, want 1", user.Id, got)
	}
	if got := db.GetHostsByUserId(user.Id); got != 1 {
		t.Fatalf("GetHostsByUserId(%d) = %d, want 1", user.Id, got)
	}

	if _, ok := db.JsonDb.Users.Load("bad-user"); ok {
		t.Fatal("invalid user entry should be dropped")
	}
	if _, ok := db.JsonDb.Clients.Load("bad-client"); ok {
		t.Fatal("invalid client entry should be dropped")
	}
}

func TestGetUserByExternalPlatformIDDropsInvalidUserEntriesOnIndexHit(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	serviceUser := &User{
		Id:                 7,
		Username:           "__platform_a",
		Kind:               "platform_service",
		ExternalPlatformID: "platform-a",
		Hidden:             true,
		Status:             1,
		TotalFlow:          &Flow{},
	}
	if err := db.NewUser(serviceUser); err != nil {
		t.Fatalf("NewUser(serviceUser) error = %v", err)
	}

	db.JsonDb.Users.Store("bad-user", "invalid")

	got, err := db.GetUserByExternalPlatformID("platform-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID() error = %v", err)
	}
	if got != serviceUser {
		t.Fatalf("GetUserByExternalPlatformID() = %#v, want %#v", got, serviceUser)
	}
	if _, ok := db.JsonDb.Users.Load("bad-user"); ok {
		t.Fatal("invalid user entry should be dropped on external platform lookup")
	}
}

func TestGetUserByUsernameRepairsStaleIndex(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	first := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &Flow{}}
	second := &User{Id: 8, Username: "other", Password: "secret", Status: 1, TotalFlow: &Flow{}}
	if err := db.NewUser(first); err != nil {
		t.Fatalf("NewUser(first) error = %v", err)
	}
	if err := db.NewUser(second); err != nil {
		t.Fatalf("NewUser(second) error = %v", err)
	}

	CurrentUsernameIndex().Add("tenant", second.Id)

	got, err := db.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	}
	if got != first {
		t.Fatalf("GetUserByUsername(tenant) = %#v, want %#v", got, first)
	}
	if id, ok := CurrentUsernameIndex().Get("tenant"); !ok || id != first.Id {
		t.Fatalf("UsernameIndex[tenant] = %d, %v, want %d, true", id, ok, first.Id)
	}
}

func TestLoadUserFromJsonFileRebuildsUsernameIndex(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &Flow{}}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	CurrentUsernameIndex().Add("stale-user", 404)
	CurrentUsernameIndex().Remove("tenant")

	db.JsonDb.LoadUserFromJsonFile()

	got, err := db.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	}
	if got == nil || got.Id != user.Id {
		t.Fatalf("GetUserByUsername(tenant) = %#v, want id %d", got, user.Id)
	}
	if _, ok := CurrentUsernameIndex().Get("stale-user"); ok {
		t.Fatal("LoadUserFromJsonFile() should clear stale username index entries")
	}
	if id, ok := CurrentUsernameIndex().Get("tenant"); !ok || id != user.Id {
		t.Fatalf("UsernameIndex[tenant] = %d, %v, want %d, true", id, ok, user.Id)
	}
}

func TestGetUserByExternalPlatformIDRepairsStaleIndex(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	serviceUser := &User{
		Id:                 7,
		Username:           "__platform_a",
		Kind:               "platform_service",
		ExternalPlatformID: "platform-a",
		Hidden:             true,
		Status:             1,
		TotalFlow:          &Flow{},
	}
	localUser := &User{Id: 8, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &Flow{}}
	if err := db.NewUser(serviceUser); err != nil {
		t.Fatalf("NewUser(serviceUser) error = %v", err)
	}
	if err := db.NewUser(localUser); err != nil {
		t.Fatalf("NewUser(localUser) error = %v", err)
	}

	CurrentPlatformUserIndex().Add("platform-a", localUser.Id)

	got, err := db.GetUserByExternalPlatformID("platform-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID() error = %v", err)
	}
	if got != serviceUser {
		t.Fatalf("GetUserByExternalPlatformID() = %#v, want %#v", got, serviceUser)
	}
	if id, ok := CurrentPlatformUserIndex().Get("platform-a"); !ok || id != serviceUser.Id {
		t.Fatalf("PlatformUserIndex[platform-a] = %d, %v, want %d, true", id, ok, serviceUser.Id)
	}
}

func TestLoadUserFromJsonFileRebuildsPlatformUserIndex(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	serviceUser := &User{
		Id:                 7,
		Username:           "__platform_a",
		Kind:               "platform_service",
		ExternalPlatformID: "platform-a",
		Hidden:             true,
		Status:             1,
		TotalFlow:          &Flow{},
	}
	if err := db.NewUser(serviceUser); err != nil {
		t.Fatalf("NewUser(serviceUser) error = %v", err)
	}

	CurrentPlatformUserIndex().Add("stale-platform", 404)
	CurrentPlatformUserIndex().Remove("platform-a")

	db.JsonDb.LoadUserFromJsonFile()

	got, err := db.GetUserByExternalPlatformID("platform-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(platform-a) error = %v", err)
	}
	if got == nil || got.Id != serviceUser.Id {
		t.Fatalf("GetUserByExternalPlatformID(platform-a) = %#v, want id %d", got, serviceUser.Id)
	}
	if _, ok := CurrentPlatformUserIndex().Get("stale-platform"); ok {
		t.Fatal("LoadUserFromJsonFile() should clear stale platform user index entries")
	}
	if id, ok := CurrentPlatformUserIndex().Get("platform-a"); !ok || id != serviceUser.Id {
		t.Fatalf("PlatformUserIndex[platform-a] = %d, %v, want %d, true", id, ok, serviceUser.Id)
	}
}

func TestNewUserTrimsUsernameForPersistenceAndLookup(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{
		Id:       7,
		Username: "  tenant  ",
		Password: "secret",
		Status:   1,
	}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if user.Username != "tenant" {
		t.Fatalf("user.Username = %q, want %q", user.Username, "tenant")
	}

	stored, err := db.GetUser(7)
	if err != nil {
		t.Fatalf("GetUser(7) error = %v", err)
	}
	if stored.Username != "tenant" {
		t.Fatalf("stored.Username = %q, want %q", stored.Username, "tenant")
	}
	lookedUp, err := db.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	}
	if lookedUp.Id != 7 {
		t.Fatalf("GetUserByUsername(tenant) id = %d, want 7", lookedUp.Id)
	}
}

func TestUpdateUserTrimsUsernameAndRejectsTrimmedDuplicate(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	first := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1}
	second := &User{Id: 8, Username: "other", Password: "secret", Status: 1}
	if err := db.NewUser(first); err != nil {
		t.Fatalf("NewUser(first) error = %v", err)
	}
	if err := db.NewUser(second); err != nil {
		t.Fatalf("NewUser(second) error = %v", err)
	}

	duplicate := &User{
		Id:       second.Id,
		Username: "  tenant  ",
		Password: second.Password,
		Status:   second.Status,
	}
	if err := db.UpdateUser(duplicate); err == nil {
		t.Fatal("UpdateUser(trimmed duplicate) error = nil, want rejection")
	}

	updated := &User{
		Id:       second.Id,
		Username: "  other-updated  ",
		Password: second.Password,
		Status:   second.Status,
	}
	if err := db.UpdateUser(updated); err != nil {
		t.Fatalf("UpdateUser(trimmed) error = %v", err)
	}

	stored, err := db.GetUser(second.Id)
	if err != nil {
		t.Fatalf("GetUser(%d) error = %v", second.Id, err)
	}
	if stored.Username != "other-updated" {
		t.Fatalf("stored.Username = %q, want %q", stored.Username, "other-updated")
	}
	lookedUp, err := db.GetUserByUsername("other-updated")
	if err != nil {
		t.Fatalf("GetUserByUsername(other-updated) error = %v", err)
	}
	if lookedUp.Id != second.Id {
		t.Fatalf("GetUserByUsername(other-updated) id = %d, want %d", lookedUp.Id, second.Id)
	}
}

func TestLoadUserFromJsonFileTrimsUsernameForLookup(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	payload := []byte(`[
  {"Id":7,"Username":"  tenant  ","Password":"secret","Status":1}
]`)
	if err := os.WriteFile(db.JsonDb.UserFilePath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(users.json) error = %v", err)
	}

	db.JsonDb.LoadUserFromJsonFile()

	stored, err := db.GetUser(7)
	if err != nil {
		t.Fatalf("GetUser(7) error = %v", err)
	}
	if stored.Username != "tenant" {
		t.Fatalf("stored.Username = %q, want %q", stored.Username, "tenant")
	}
	lookedUp, err := db.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	}
	if lookedUp.Id != 7 {
		t.Fatalf("GetUserByUsername(tenant) id = %d, want 7", lookedUp.Id)
	}
}

func TestLoadUserFromJsonFileClearsRemovedUsersOnReload(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	payload := []byte(`[
  {"Id":7,"Username":"tenant","Password":"secret","Status":1}
]`)
	if err := os.WriteFile(db.JsonDb.UserFilePath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(users.json) error = %v", err)
	}

	db.JsonDb.LoadUserFromJsonFile()
	if _, err := db.GetUser(7); err != nil {
		t.Fatalf("GetUser(7) error = %v", err)
	}

	if err := os.WriteFile(db.JsonDb.UserFilePath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFile(empty users.json) error = %v", err)
	}
	db.JsonDb.LoadUserFromJsonFile()

	if _, err := db.GetUser(7); err == nil {
		t.Fatal("GetUser(7) error = nil, want removed user after reload")
	}
	if _, err := db.GetUserByUsername("tenant"); err == nil {
		t.Fatal("GetUserByUsername(tenant) error = nil, want stale lookup removed after reload")
	}
	if db.JsonDb.UserIncreaseId != 0 {
		t.Fatalf("UserIncreaseId = %d, want 0 after reloading an empty user file", db.JsonDb.UserIncreaseId)
	}
}

func TestLoadUserFromJsonFileSkipsDuplicateUsernamesAcrossDifferentIDs(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	payload := []byte(`[
  {"Id":7,"Username":"tenant","Password":"secret","Status":1},
  {"Id":8,"Username":"tenant","Password":"other","Status":1}
]`)
	if err := os.WriteFile(db.JsonDb.UserFilePath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile(users.json) error = %v", err)
	}

	db.JsonDb.LoadUserFromJsonFile()

	first, err := db.GetUser(7)
	if err != nil {
		t.Fatalf("GetUser(7) error = %v", err)
	}
	if first.Username != "tenant" {
		t.Fatalf("first.Username = %q, want tenant", first.Username)
	}
	if _, err := db.GetUser(8); err == nil {
		t.Fatal("GetUser(8) error = nil, want duplicate-username user skipped on reload")
	}
	lookedUp, err := db.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	}
	if lookedUp.Id != 7 {
		t.Fatalf("GetUserByUsername(tenant) id = %d, want 7", lookedUp.Id)
	}
}

func TestLoadUserFromJsonFileRebindsAndClearsClientOwnerPointers(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{Id: 7, Username: "tenant", Password: "secret", Status: 1}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &Client{
		Id:          11,
		VerifyKey:   "owner-client",
		OwnerUserID: user.Id,
		UserId:      user.Id,
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ownerBefore := client.OwnerUser()
	if ownerBefore == nil || ownerBefore.Username != "tenant" {
		t.Fatalf("client.OwnerUser() = %+v, want initial bound owner tenant", ownerBefore)
	}

	reloadPayload := []byte(`[
  {"Id":7,"Username":"tenant-reloaded","Password":"secret","Status":1}
]`)
	if err := os.WriteFile(db.JsonDb.UserFilePath, reloadPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(users.json) error = %v", err)
	}

	db.JsonDb.LoadUserFromJsonFile()

	currentClient, err := db.GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", client.Id, err)
	}
	currentOwner := currentClient.OwnerUser()
	if currentOwner == nil {
		t.Fatal("client.OwnerUser() = nil after reload, want rebound owner")
	}
	if currentOwner == ownerBefore {
		t.Fatal("client.OwnerUser() still points to pre-reload user object, want rebound runtime owner")
	}
	if currentOwner.Username != "tenant-reloaded" {
		t.Fatalf("client.OwnerUser().Username = %q, want tenant-reloaded", currentOwner.Username)
	}

	if err := os.WriteFile(db.JsonDb.UserFilePath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFile(empty users.json) error = %v", err)
	}
	db.JsonDb.LoadUserFromJsonFile()

	currentClient, err = db.GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) after clear error = %v", client.Id, err)
	}
	if currentClient.OwnerUser() != nil {
		t.Fatalf("client.OwnerUser() = %+v after user removal, want nil", currentClient.OwnerUser())
	}
}
