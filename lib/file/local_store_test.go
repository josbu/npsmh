package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
)

func resetStoreTestDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	oldDb := Db
	oldIndexes := SnapshotRuntimeIndexes()
	db := &DbUtils{JsonDb: NewJsonDb(root)}
	db.JsonDb.Global = &Glob{}
	ReplaceDb(db)
	ReplaceRuntimeIndexes(NewRuntimeIndexes())
	t.Cleanup(func() {
		ReplaceDb(oldDb)
		ReplaceRuntimeIndexes(oldIndexes)
	})
	return root
}

func TestNewLocalStoreDoesNotBindLegacyUserTrafficMirror(t *testing.T) {
	resetStoreTestDB(t)

	if err := GetDb().NewUser(&User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if err := GetDb().NewClient(&Client{
		Id:        11,
		UserId:    7,
		VerifyKey: "demo",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	local := NewLocalStore()
	if local == nil {
		t.Fatal("NewLocalStore() = nil")
	}
	client, err := GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	client.Flow.Add(10, 20)

	user, err := GetDb().GetUser(7)
	if err != nil || user == nil {
		t.Fatalf("GetUser() after client flow update error = %v", err)
	}
	if user.TotalFlow == nil || user.TotalFlow.InletFlow != 0 || user.TotalFlow.ExportFlow != 0 {
		t.Fatalf("user total flow should stay unchanged without explicit traffic aggregation, got %+v", user.TotalFlow)
	}
}

func TestLocalStoreGetAllClientsSkipsAndCleansInvalidEntries(t *testing.T) {
	resetStoreTestDB(t)

	db := GetDb()
	db.JsonDb.Clients.Store(11, &Client{Id: 11, VerifyKey: "demo", Cnf: &Config{}, Flow: &Flow{}})
	db.JsonDb.Clients.Store(98, "bad-client")
	db.JsonDb.Clients.Store(99, (*Client)(nil))

	clients := NewLocalStore().GetAllClients()
	if len(clients) != 1 || clients[0] == nil || clients[0].Id != 11 {
		t.Fatalf("GetAllClients() = %+v, want only client 11", clients)
	}
	if _, ok := db.JsonDb.Clients.Load(98); ok {
		t.Fatal("invalid client entry should be removed during GetAllClients")
	}
	if _, ok := db.JsonDb.Clients.Load(99); ok {
		t.Fatal("nil client entry should be removed during GetAllClients")
	}
}

func TestLocalStoreExportConfigSnapshotReturnsDetachedCopy(t *testing.T) {
	resetStoreTestDB(t)

	db := GetDb()
	db.JsonDb.Global = &Glob{EntryAclMode: AclWhitelist, EntryAclRules: "127.0.0.1"}
	if err := db.NewUser(&User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if err := db.NewClient(&Client{
		Id:        11,
		UserId:    7,
		VerifyKey: "demo",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
		Remark:    "before",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	local := NewLocalStore()
	snapshot, err := local.ExportConfigSnapshot()
	if err != nil {
		t.Fatalf("ExportConfigSnapshot() error = %v", err)
	}

	snapshot.Users[0].Username = "changed"
	snapshot.Clients[0].Remark = "changed"
	snapshot.Global.EntryAclRules = "0.0.0.0/0"

	user, err := db.GetUser(7)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if user.Username != "tenant" {
		t.Fatalf("stored user username = %q, want tenant", user.Username)
	}

	client, err := db.GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client.Remark != "before" {
		t.Fatalf("stored client remark = %q, want before", client.Remark)
	}
	if db.JsonDb.Global == nil || db.JsonDb.Global.EntryAclRules != "127.0.0.1" {
		t.Fatalf("stored global ACL = %q, want %q", db.JsonDb.Global.EntryAclRules, "127.0.0.1")
	}
}

func TestLocalStoreImportConfigSnapshotClonesInputAndInitializesRuntime(t *testing.T) {
	resetStoreTestDB(t)

	local := NewLocalStore()
	snapshot := &ConfigSnapshot{
		Users: []*User{
			{
				Id:        7,
				Username:  "tenant",
				Password:  "secret",
				Status:    1,
				TotalFlow: &Flow{},
			},
			{
				Id:        8,
				Username:  "manager",
				Password:  "secret",
				Status:    1,
				TotalFlow: &Flow{},
			},
		},
		Clients: []*Client{
			{
				Id:             11,
				UserId:         7,
				VerifyKey:      "demo",
				ManagerUserIDs: []int{8},
				Status:         true,
				Cnf:            &Config{},
				Flow:           &Flow{},
			},
		},
		Tunnels: []*Tunnel{
			{
				Id:         13,
				Client:     &Client{Id: 11},
				Flow:       &Flow{},
				Mode:       "socks5",
				Password:   "pw",
				TargetType: "invalid",
				Target:     &Target{TargetStr: "127.0.0.1:80"},
			},
		},
		Hosts: []*Host{
			{
				Id:       17,
				Client:   &Client{Id: 11},
				Flow:     &Flow{},
				Host:     "example.com",
				Target:   &Target{TargetStr: "127.0.0.1:80"},
				CertFile: "cert.pem",
				KeyFile:  "key.pem",
			},
		},
		Global: &Glob{EntryAclMode: AclWhitelist, EntryAclRules: "127.0.0.1"},
	}

	if err := local.ImportConfigSnapshot(snapshot); err != nil {
		t.Fatalf("ImportConfigSnapshot() error = %v", err)
	}

	snapshot.Users[0].Username = "changed"
	snapshot.Clients[0].VerifyKey = "changed"
	snapshot.Tunnels[0].Mode = "tcp"
	snapshot.Hosts[0].Host = "changed.example.com"
	snapshot.Global.EntryAclRules = "0.0.0.0/0"

	user, err := GetDb().GetUser(7)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if user.Username != "tenant" {
		t.Fatalf("stored user username = %q, want tenant", user.Username)
	}

	client, err := GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client.VerifyKey != "demo" {
		t.Fatalf("stored client verify key = %q, want demo", client.VerifyKey)
	}
	if lookedUp, err := GetDb().GetClientByVerifyKey("demo"); err != nil {
		t.Fatalf("GetClientByVerifyKey(demo) error = %v", err)
	} else if lookedUp.Id != client.Id {
		t.Fatalf("GetClientByVerifyKey(demo) id = %d, want %d", lookedUp.Id, client.Id)
	}
	if got := GetDb().GetClientsByUserId(7); len(got) != 1 || got[0].Id != client.Id {
		t.Fatalf("GetClientsByUserId(7) = %+v, want client %d", got, client.Id)
	}
	if got := GetDb().GetVisibleManagedClientIDsByUserId(8); len(got) != 1 || got[0] != client.Id {
		t.Fatalf("GetVisibleManagedClientIDsByUserId(8) = %v, want [%d]", got, client.Id)
	}
	if got := GetDb().CountResourcesByClientID(client.Id); got != 2 {
		t.Fatalf("CountResourcesByClientID(%d) = %d, want 2", client.Id, got)
	}

	rawTunnel, ok := GetDb().JsonDb.Tasks.Load(13)
	if !ok {
		t.Fatal("imported tunnel missing from store")
	}
	tunnel := rawTunnel.(*Tunnel)
	if tunnel.Mode != "mixProxy" || !tunnel.Socks5Proxy || tunnel.HttpProxy {
		t.Fatalf("unexpected imported tunnel mode flags: mode=%q socks5=%v http=%v", tunnel.Mode, tunnel.Socks5Proxy, tunnel.HttpProxy)
	}
	if tunnel.TargetType != common.CONN_ALL {
		t.Fatalf("unexpected imported tunnel target type = %q, want %q", tunnel.TargetType, common.CONN_ALL)
	}
	if tunnel.Client != client {
		t.Fatal("imported tunnel client was not rebound to the canonical stored client")
	}

	rawHost, ok := GetDb().JsonDb.Hosts.Load(17)
	if !ok {
		t.Fatal("imported host missing from store")
	}
	host := rawHost.(*Host)
	if host.Host != "example.com" {
		t.Fatalf("stored host name = %q, want example.com", host.Host)
	}
	if host.Client != client {
		t.Fatal("imported host client was not rebound to the canonical stored client")
	}
	if host.CertType == "" || host.CertHash == "" {
		t.Fatalf("imported host TLS metadata not initialized: certType=%q certHash=%q", host.CertType, host.CertHash)
	}
	if ids := CurrentHostIndex().Lookup("example.com"); len(ids) != 1 || ids[0] != host.Id {
		t.Fatalf("HostIndex.Lookup(example.com) = %v, want [%d]", ids, host.Id)
	}
	if GetDb().JsonDb.Global == nil || GetDb().JsonDb.Global.EntryAclRules != "127.0.0.1" {
		t.Fatalf("stored global ACL = %q, want %q", GetDb().JsonDb.Global.EntryAclRules, "127.0.0.1")
	}
	if lookedUpUser, err := GetDb().GetUserByUsername("tenant"); err != nil {
		t.Fatalf("GetUserByUsername(tenant) error = %v", err)
	} else if lookedUpUser.Id != user.Id {
		t.Fatalf("GetUserByUsername(tenant) id = %d, want %d", lookedUpUser.Id, user.Id)
	}
}

func TestLocalStoreImportConfigSnapshotSkipsResourcesWithMissingClient(t *testing.T) {
	resetStoreTestDB(t)

	local := NewLocalStore()
	snapshot := &ConfigSnapshot{
		Users: []*User{
			{Id: 7, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &Flow{}},
		},
		Clients: []*Client{
			{Id: 11, UserId: 7, VerifyKey: "demo", Status: true, Cnf: &Config{}, Flow: &Flow{}},
		},
		Tunnels: []*Tunnel{
			{Id: 13, Client: &Client{Id: 99}, Flow: &Flow{}, Mode: "tcp", Target: &Target{TargetStr: "127.0.0.1:80"}},
		},
		Hosts: []*Host{
			{Id: 17, Client: &Client{Id: 98}, Flow: &Flow{}, Host: "missing.example.com", Target: &Target{TargetStr: "127.0.0.1:81"}},
		},
	}

	if err := local.ImportConfigSnapshot(snapshot); err != nil {
		t.Fatalf("ImportConfigSnapshot() error = %v", err)
	}

	if _, err := GetDb().GetTask(13); err == nil {
		t.Fatal("GetTask(13) error = nil, want skipped tunnel with missing client")
	}
	if _, err := GetDb().GetHostById(17); err == nil {
		t.Fatal("GetHostById(17) error = nil, want skipped host with missing client")
	}
	if got := GetDb().CountResourcesByClientID(11); got != 0 {
		t.Fatalf("CountResourcesByClientID(11) = %d, want 0", got)
	}
}

func TestLocalStoreImportConfigSnapshotReplacesDuplicateIDsWithoutStaleIndexes(t *testing.T) {
	resetStoreTestDB(t)

	snapshot := &ConfigSnapshot{
		Users: []*User{
			{Id: 7, Username: "old-user", Kind: "platform_service", ExternalPlatformID: "old-platform", TotalFlow: &Flow{}},
			{Id: 7, Username: "new-user", Kind: "platform_service", ExternalPlatformID: "new-platform", TotalFlow: &Flow{}},
		},
		Clients: []*Client{
			{Id: 11, UserId: 7, VerifyKey: "old-vkey", Cnf: &Config{}, Flow: &Flow{}},
			{Id: 11, UserId: 7, VerifyKey: "new-vkey", Cnf: &Config{}, Flow: &Flow{}},
		},
		Tunnels: []*Tunnel{
			{Id: 13, Client: &Client{Id: 11}, Password: "old-secret", Mode: "tcp", Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:80"}},
			{Id: 13, Client: &Client{Id: 11}, Password: "new-secret", Mode: "tcp", Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:81"}},
		},
		Hosts: []*Host{
			{Id: 17, Client: &Client{Id: 11}, Host: "old.example.com", Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:82"}},
			{Id: 17, Client: &Client{Id: 11}, Host: "new.example.com", Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:83"}},
		},
	}

	if err := NewLocalStore().ImportConfigSnapshot(snapshot); err != nil {
		t.Fatalf("ImportConfigSnapshot() error = %v", err)
	}

	if _, err := GetDb().GetUserByUsername("old-user"); err == nil {
		t.Fatal("old username index should be removed after same-id user replacement")
	}
	if user, err := GetDb().GetUserByUsername("new-user"); err != nil || user.Id != 7 {
		t.Fatalf("GetUserByUsername(new-user) = %+v, %v; want user 7", user, err)
	}
	if _, ok := CurrentPlatformUserIndex().Get("old-platform"); ok {
		t.Fatal("old platform user index should be removed after same-id user replacement")
	}
	if id, ok := CurrentPlatformUserIndex().Get("new-platform"); !ok || id != 7 {
		t.Fatalf("new platform index = %d, %v; want 7, true", id, ok)
	}
	if _, err := GetDb().GetClientByVerifyKey("old-vkey"); err == nil {
		t.Fatal("old verify-key index should be removed after same-id client replacement")
	}
	if client, err := GetDb().GetClientByVerifyKey("new-vkey"); err != nil || client.Id != 11 {
		t.Fatalf("GetClientByVerifyKey(new-vkey) = %+v, %v; want client 11", client, err)
	}
	if task := GetDb().GetTaskByMd5Password(crypt.Md5("old-secret")); task != nil {
		t.Fatalf("old task password index = %+v, want nil", task)
	}
	if task := GetDb().GetTaskByMd5Password(crypt.Md5("new-secret")); task == nil || task.Id != 13 {
		t.Fatalf("new task password index = %+v, want task 13", task)
	}
	if ids := CurrentHostIndex().Lookup("old.example.com"); len(ids) != 0 {
		t.Fatalf("old host index = %v, want empty", ids)
	}
	if ids := CurrentHostIndex().Lookup("new.example.com"); len(ids) != 1 || ids[0] != 17 {
		t.Fatalf("new host index = %v, want [17]", ids)
	}
}

func TestInitializeImportedResourcesNormalizeMultiAccountContent(t *testing.T) {
	client := &Client{Id: 11, Cnf: &Config{}, Flow: &Flow{}}
	clients := map[int]*Client{11: client}

	tunnel := &Tunnel{
		Client: &Client{Id: 11},
		Flow:   &Flow{},
		UserAuth: &MultiAccount{
			Content:    "#ignored\nops=token\nreadonly\n",
			AccountMap: map[string]string{"#ignored": "", "ops": "token", "readonly": ""},
		},
	}
	initializeImportedTunnel(tunnel, clients)
	if tunnel.Client != client {
		t.Fatal("initializeImportedTunnel() should rebind canonical client")
	}
	if got := GetAccountMap(tunnel.UserAuth); len(got) != 2 || got["ops"] != "token" || got["readonly"] != "" {
		t.Fatalf("initializeImportedTunnel() auth map = %#v, want normalized content-derived map", got)
	}
	if _, ok := tunnel.UserAuth.AccountMap["#ignored"]; ok {
		t.Fatal("initializeImportedTunnel() should drop comment entries from stale AccountMap")
	}

	host := &Host{
		Client: &Client{Id: 11},
		Flow:   &Flow{},
		MultiAccount: &MultiAccount{
			Content:    "#comment\nworker=secret\n",
			AccountMap: map[string]string{"#comment": "", "worker": "secret"},
		},
	}
	initializeImportedHost(host, clients)
	if host.Client != client {
		t.Fatal("initializeImportedHost() should rebind canonical client")
	}
	if got := GetAccountMap(host.MultiAccount); len(got) != 1 || got["worker"] != "secret" {
		t.Fatalf("initializeImportedHost() account map = %#v, want normalized content-derived map", got)
	}
	if _, ok := host.MultiAccount.AccountMap["#comment"]; ok {
		t.Fatal("initializeImportedHost() should drop comment entries from stale AccountMap")
	}
}

func TestLocalStoreExportConfigSnapshotSkipsAndCleansInvalidEntries(t *testing.T) {
	resetStoreTestDB(t)

	db := GetDb()
	db.JsonDb.Global = &Glob{EntryAclRules: "127.0.0.1"}
	db.JsonDb.Users.Store(1, &User{Id: 1, Username: "tenant", TotalFlow: &Flow{}})
	db.JsonDb.Users.Store(99, "bad-user")
	db.JsonDb.Clients.Store(11, &Client{Id: 11, VerifyKey: "demo", Cnf: &Config{}, Flow: &Flow{}})
	db.JsonDb.Clients.Store(98, (*Client)(nil))
	db.JsonDb.Tasks.Store(13, &Tunnel{Id: 13, Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:80"}})
	db.JsonDb.Tasks.Store(97, 123)
	db.JsonDb.Hosts.Store(17, &Host{Id: 17, Host: "example.com", Flow: &Flow{}, Target: &Target{TargetStr: "127.0.0.1:80"}})
	db.JsonDb.Hosts.Store(96, "bad-host")

	snapshot, err := NewLocalStore().ExportConfigSnapshot()
	if err != nil {
		t.Fatalf("ExportConfigSnapshot() error = %v", err)
	}

	if len(snapshot.Users) != 1 || snapshot.Users[0].Id != 1 {
		t.Fatalf("snapshot users = %+v, want only user 1", snapshot.Users)
	}
	if len(snapshot.Clients) != 1 || snapshot.Clients[0].Id != 11 {
		t.Fatalf("snapshot clients = %+v, want only client 11", snapshot.Clients)
	}
	if len(snapshot.Tunnels) != 1 || snapshot.Tunnels[0].Id != 13 {
		t.Fatalf("snapshot tunnels = %+v, want only tunnel 13", snapshot.Tunnels)
	}
	if len(snapshot.Hosts) != 1 || snapshot.Hosts[0].Id != 17 {
		t.Fatalf("snapshot hosts = %+v, want only host 17", snapshot.Hosts)
	}

	if _, ok := db.JsonDb.Users.Load(99); ok {
		t.Fatal("invalid user entry should be removed during snapshot export")
	}
	if _, ok := db.JsonDb.Clients.Load(98); ok {
		t.Fatal("invalid client entry should be removed during snapshot export")
	}
	if _, ok := db.JsonDb.Tasks.Load(97); ok {
		t.Fatal("invalid tunnel entry should be removed during snapshot export")
	}
	if _, ok := db.JsonDb.Hosts.Load(96); ok {
		t.Fatal("invalid host entry should be removed during snapshot export")
	}
}
