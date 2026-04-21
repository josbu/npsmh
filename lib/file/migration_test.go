package file

import (
	"os"
	"path/filepath"
	"testing"
)

func resetMigrationTestDB(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := ensureMigrationDirs(root); err != nil {
		t.Fatalf("ensureMigrationDirs() error = %v", err)
	}
	oldDB := Db
	oldIndexes := SnapshotRuntimeIndexes()

	db := &DbUtils{JsonDb: NewJsonDb(root)}
	db.JsonDb.Global = &Glob{}
	ReplaceDb(db)
	ReplaceRuntimeIndexes(NewRuntimeIndexes())

	t.Cleanup(func() {
		ReplaceDb(oldDB)
		ReplaceRuntimeIndexes(oldIndexes)
	})
}

func ensureMigrationDirs(root string) error {
	return os.MkdirAll(filepath.Join(root, "conf"), 0o755)
}

func TestMigrateLegacyDataCreatesUsersAndPreservesUnownedClients(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	c1 := &Client{Id: 1, VerifyKey: "vk-1", Flow: &Flow{InletFlow: 10, ExportFlow: 20}, Cnf: &Config{}, Status: true}
	c1.SetLegacyWebLoginImport("tenant", "pw-1", "JBSWY3DPEHPK3PXP")
	c2 := &Client{Id: 2, VerifyKey: "vk-2", Flow: &Flow{InletFlow: 1, ExportFlow: 2}, Cnf: &Config{}, Status: true}
	c2.SetLegacyWebLoginImport("tenant", "pw-1", "")
	c3 := &Client{Id: 3, VerifyKey: "vk-3", Flow: &Flow{InletFlow: 5, ExportFlow: 6}, Cnf: &Config{}, Status: true}
	c3.SetLegacyWebLoginImport("tenant", "pw-2", "")
	c4 := &Client{Id: 4, VerifyKey: "vk-4", Flow: &Flow{InletFlow: 7, ExportFlow: 8}, Cnf: &Config{}, Status: true}
	for _, client := range []*Client{c1, c2, c3, c4} {
		if err := db.NewClient(client); err != nil {
			t.Fatalf("NewClient(%d) error = %v", client.Id, err)
		}
	}

	MigrateLegacyData()

	migratedC1, _ := db.GetClient(1)
	migratedC2, _ := db.GetClient(2)
	migratedC3, _ := db.GetClient(3)
	migratedC4, _ := db.GetClient(4)

	if migratedC1.OwnerID() <= 0 || migratedC1.OwnerID() != migratedC2.OwnerID() {
		t.Fatalf("expected first two legacy clients to merge into one owner, got %d and %d", migratedC1.OwnerID(), migratedC2.OwnerID())
	}
	if migratedC3.OwnerID() <= 0 || migratedC3.OwnerID() == migratedC1.OwnerID() {
		t.Fatalf("expected conflicting legacy credentials to create a separate owner, got owner1=%d owner3=%d", migratedC1.OwnerID(), migratedC3.OwnerID())
	}
	if migratedC4.OwnerID() != 0 {
		t.Fatalf("expected unowned legacy client to remain unowned, got owner=%d", migratedC4.OwnerID())
	}
	for _, client := range []*Client{migratedC1, migratedC2, migratedC3, migratedC4} {
		username, password, totpSecret := client.LegacyWebLoginImport()
		if username != "" || password != "" || totpSecret != "" {
			t.Fatalf("legacy web credentials should be cleared after migration, got username=%q password=%q totp=%q", username, password, totpSecret)
		}
	}

	owner1, err := db.GetUser(migratedC1.OwnerID())
	if err != nil {
		t.Fatalf("GetUser(owner1) error = %v", err)
	}
	if owner1.Username != "tenant" {
		t.Fatalf("owner1.Username = %q, want tenant", owner1.Username)
	}
	if owner1.TotalFlow == nil || owner1.TotalFlow.InletFlow != 11 || owner1.TotalFlow.ExportFlow != 22 {
		t.Fatalf("owner1.TotalFlow = %+v, want 11/22", owner1.TotalFlow)
	}
	if owner1.TOTPSecret != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("owner1.TOTPSecret = %q, want migrated legacy TOTP secret", owner1.TOTPSecret)
	}

	owner3, err := db.GetUser(migratedC3.OwnerID())
	if err != nil {
		t.Fatalf("GetUser(owner3) error = %v", err)
	}
	if owner3.Username != "tenant__legacy_3" {
		t.Fatalf("owner3.Username = %q, want tenant__legacy_3", owner3.Username)
	}
}

func TestEnsureManagementPlatformUsersCreatesHiddenServiceUsers(t *testing.T) {
	resetMigrationTestDB(t)

	changed := EnsureManagementPlatformUsers([]ManagementPlatformBinding{
		{PlatformID: "master-a", Enabled: true},
		{PlatformID: "master-b", Enabled: true, ServiceUsername: "service_b"},
	})
	if !changed {
		t.Fatal("EnsureManagementPlatformUsers() should report changes when users are created")
	}

	userA, err := GetDb().GetUserByExternalPlatformID("master-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-a) error = %v", err)
	}
	if userA.Username != "__platform_master-a" {
		t.Fatalf("service user A username = %q, want __platform_master-a", userA.Username)
	}
	if !userA.Hidden || userA.Kind != "platform_service" {
		t.Fatalf("service user A = %+v, want hidden platform_service", userA)
	}
	if userA.TotalTraffic == nil || userA.TotalMeter == nil || userA.Rate == nil {
		t.Fatalf("service user A runtime fields not initialized: traffic=%v meter=%v rate=%v", userA.TotalTraffic, userA.TotalMeter, userA.Rate)
	}

	userB, err := GetDb().GetUserByExternalPlatformID("master-b")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-b) error = %v", err)
	}
	if userB.Username != "service_b" {
		t.Fatalf("service user B username = %q, want service_b", userB.Username)
	}
}

func TestEnsureManagementPlatformUsersKeepsConflictingLocalUserIntact(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	localUser := &User{
		Id:        int(db.JsonDb.GetUserId()),
		Username:  "service_b",
		Password:  "local-secret",
		Kind:      "local",
		Status:    1,
		TotalFlow: &Flow{},
	}
	localUser.TouchMeta()
	if err := db.NewUser(localUser); err != nil {
		t.Fatalf("NewUser(localUser) error = %v", err)
	}

	changed := EnsureManagementPlatformUsers([]ManagementPlatformBinding{
		{PlatformID: "master-b", Enabled: true, ServiceUsername: "service_b"},
	})
	if !changed {
		t.Fatal("EnsureManagementPlatformUsers() should report changes when a fallback service user is created")
	}

	preservedLocalUser, err := db.GetUser(localUser.Id)
	if err != nil {
		t.Fatalf("GetUser(localUser) error = %v", err)
	}
	if preservedLocalUser.Kind != "local" || preservedLocalUser.Hidden || preservedLocalUser.ExternalPlatformID != "" {
		t.Fatalf("local user should stay intact, got %+v", preservedLocalUser)
	}

	serviceUser, err := db.GetUserByExternalPlatformID("master-b")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-b) error = %v", err)
	}
	if serviceUser.Id == localUser.Id {
		t.Fatal("service user should not reuse the conflicting local user")
	}
	if serviceUser.Username != "service_b_2" {
		t.Fatalf("service user username = %q, want service_b_2", serviceUser.Username)
	}
}

func TestMigrateLegacyDataNormalizesAccessPolicies(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	db.JsonDb.Global = &Glob{}
	db.JsonDb.Global.SetLegacyBlackIPImport([]string{" 127.0.0.1 ", "10.0.0.0/8"})

	user := &User{
		Id:            int(db.JsonDb.GetUserId()),
		Username:      "user-a",
		Password:      "pw",
		Status:        1,
		TotalFlow:     &Flow{},
		EntryAclMode:  AclWhitelist,
		EntryAclRules: " 192.168.0.0/16\r\n192.168.1.0/24 ",
	}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser(user) error = %v", err)
	}

	client := &Client{
		Id:        1,
		VerifyKey: "vk-1",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	client.SetLegacyBlackIPImport([]string{" 127.0.0.1 ", "10.0.0.0/8"})
	db.JsonDb.Clients.Store(client.Id, client)

	ownerClient := &Client{
		Id:          2,
		VerifyKey:   "vk-2",
		Status:      true,
		Cnf:         &Config{},
		Flow:        &Flow{},
		UserId:      user.Id,
		OwnerUserID: user.Id,
	}
	ownerClient.SetLegacyWebLoginImport("stale", "secret", "JBSWY3DPEHPK3PXP")
	if err := db.NewClient(ownerClient); err != nil {
		t.Fatalf("NewClient(ownerClient) error = %v", err)
	}

	tunnel := &Tunnel{
		Id:            int(db.JsonDb.GetTaskId()),
		Client:        client,
		Mode:          "tcp",
		Status:        true,
		RunStatus:     true,
		Flow:          &Flow{},
		EntryAclMode:  AclBlacklist,
		EntryAclRules: " 10.0.0.0/8\r\n",
		DestAclMode:   AclWhitelist,
		DestAclRules:  " full:example.com \r\n 1.1.1.1/32 ",
	}
	if err := db.NewTask(tunnel); err != nil {
		t.Fatalf("NewTask(tunnel) error = %v", err)
	}

	host := &Host{
		Id:            int(db.JsonDb.GetHostId()),
		Host:          "demo.example.com",
		Location:      "/",
		Scheme:        "http",
		Client:        client,
		Flow:          &Flow{},
		EntryAclMode:  AclWhitelist,
		EntryAclRules: " ::1/128 \r\n",
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost(host) error = %v", err)
	}

	MigrateLegacyData()

	if legacy := db.JsonDb.Global.LegacyBlackIPImport(); len(legacy) != 0 {
		t.Fatalf("global legacy blacklist should be cleared after migration, got %v", legacy)
	}
	if db.JsonDb.Global.EntryAclMode != AclBlacklist {
		t.Fatalf("global.EntryAclMode = %d, want %d", db.JsonDb.Global.EntryAclMode, AclBlacklist)
	}
	if db.JsonDb.Global.EntryAclRules != "127.0.0.1\n10.0.0.0/8" {
		t.Fatalf("global.EntryAclRules = %q", db.JsonDb.Global.EntryAclRules)
	}

	migratedUser, err := db.GetUser(user.Id)
	if err != nil {
		t.Fatalf("GetUser(user) error = %v", err)
	}
	if migratedUser.EntryAclRules != "192.168.0.0/16\n192.168.1.0/24" {
		t.Fatalf("user.EntryAclRules = %q", migratedUser.EntryAclRules)
	}

	migratedClient, err := db.GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(client) error = %v", err)
	}
	if migratedClient.EntryAclMode != AclBlacklist {
		t.Fatalf("client.EntryAclMode = %d, want %d", migratedClient.EntryAclMode, AclBlacklist)
	}
	if migratedClient.EntryAclRules != "127.0.0.1\n10.0.0.0/8" {
		t.Fatalf("client.EntryAclRules = %q", migratedClient.EntryAclRules)
	}
	if legacy := migratedClient.LegacyBlackIPImport(); len(legacy) != 0 {
		t.Fatalf("client legacy blacklist should be cleared after migration, got %v", legacy)
	}

	migratedOwnerClient, err := db.GetClient(ownerClient.Id)
	if err != nil {
		t.Fatalf("GetClient(ownerClient) error = %v", err)
	}
	username, password, totpSecret := migratedOwnerClient.LegacyWebLoginImport()
	if username != "" || password != "" || totpSecret != "" {
		t.Fatalf("owner client stale web credentials should be cleared, got username=%q password=%q totp=%q", username, password, totpSecret)
	}

	migratedTunnel, err := db.GetTask(tunnel.Id)
	if err != nil {
		t.Fatalf("GetTask(tunnel) error = %v", err)
	}
	if migratedTunnel.EntryAclRules != "10.0.0.0/8" {
		t.Fatalf("tunnel.EntryAclRules = %q", migratedTunnel.EntryAclRules)
	}
	if migratedTunnel.DestAclRules != "full:example.com\n1.1.1.1/32" {
		t.Fatalf("tunnel.DestAclRules = %q", migratedTunnel.DestAclRules)
	}

	migratedHost, err := db.GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById(host) error = %v", err)
	}
	if migratedHost.EntryAclRules != "::1/128" {
		t.Fatalf("host.EntryAclRules = %q", migratedHost.EntryAclRules)
	}
}
