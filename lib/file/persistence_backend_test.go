package file

import (
	"testing"
)

type stubPersistenceBackend struct {
	users             []*User
	clients           []*Client
	tasks             []*Tunnel
	hosts             []*Host
	global            *Glob
	storeUsersCalls   int
	storeClientsCalls int
	storeTasksCalls   int
	storeHostsCalls   int
	storeGlobalCalls  int
}

func (s *stubPersistenceBackend) LoadUsers(*JsonDb) ([]*User, error)     { return s.users, nil }
func (s *stubPersistenceBackend) LoadClients(*JsonDb) ([]*Client, error) { return s.clients, nil }
func (s *stubPersistenceBackend) LoadTasks(*JsonDb) ([]*Tunnel, error)   { return s.tasks, nil }
func (s *stubPersistenceBackend) LoadHosts(*JsonDb) ([]*Host, error)     { return s.hosts, nil }
func (s *stubPersistenceBackend) LoadGlobal(*JsonDb) (*Glob, error) {
	if s.global == nil {
		return &Glob{}, nil
	}
	return s.global, nil
}
func (s *stubPersistenceBackend) StoreUsers(*JsonDb) error {
	s.storeUsersCalls++
	return nil
}
func (s *stubPersistenceBackend) StoreClients(*JsonDb) error {
	s.storeClientsCalls++
	return nil
}
func (s *stubPersistenceBackend) StoreTasks(*JsonDb) error {
	s.storeTasksCalls++
	return nil
}
func (s *stubPersistenceBackend) StoreHosts(*JsonDb) error {
	s.storeHostsCalls++
	return nil
}
func (s *stubPersistenceBackend) StoreGlobal(*JsonDb) error {
	s.storeGlobalCalls++
	return nil
}

func TestJsonDbPersistenceBackendSeamHandlesLoadAndStore(t *testing.T) {
	oldIndexes := SnapshotRuntimeIndexes()
	ReplaceRuntimeIndexes(NewRuntimeIndexes())
	t.Cleanup(func() {
		ReplaceRuntimeIndexes(oldIndexes)
	})

	db := NewJsonDb(t.TempDir())
	backend := &stubPersistenceBackend{
		users: []*User{
			{Id: 7, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &Flow{}},
		},
		clients: []*Client{
			{Id: 11, OwnerUserID: 7, VerifyKey: "demo", Status: true, Cnf: &Config{}, Flow: &Flow{}},
		},
		global: &Glob{EntryAclMode: AclBlacklist, EntryAclRules: "127.0.0.1"},
	}
	db.persistence = backend

	db.LoadUsers()
	db.LoadClients()
	db.LoadGlobal()

	user, ok := loadUserEntry(&db.Users, 7)
	if !ok || user == nil || user.Username != "tenant" {
		t.Fatalf("load users via persistence backend failed, user=%+v ok=%v", user, ok)
	}
	client, ok := loadClientEntry(&db.Clients, 11)
	if !ok || client == nil || client.VerifyKey != "demo" {
		t.Fatalf("load clients via persistence backend failed, client=%+v ok=%v", client, ok)
	}
	if client.OwnerUser() != user {
		t.Fatal("loaded client should bind canonical owner user after persistence load")
	}
	if db.Global == nil || db.Global.EntryAclRules != "127.0.0.1" {
		t.Fatalf("load global via persistence backend failed, global=%+v", db.Global)
	}

	db.StoreUsers()
	db.StoreClients()
	db.StoreTasks()
	db.StoreHosts()
	db.StoreGlobal()

	if backend.storeUsersCalls != 1 {
		t.Fatalf("StoreUsers() calls = %d, want 1", backend.storeUsersCalls)
	}
	if backend.storeClientsCalls != 1 {
		t.Fatalf("StoreClients() calls = %d, want 1", backend.storeClientsCalls)
	}
	if backend.storeTasksCalls != 1 {
		t.Fatalf("StoreTasks() calls = %d, want 1", backend.storeTasksCalls)
	}
	if backend.storeHostsCalls != 1 {
		t.Fatalf("StoreHosts() calls = %d, want 1", backend.storeHostsCalls)
	}
	if backend.storeGlobalCalls != 1 {
		t.Fatalf("StoreGlobal() calls = %d, want 1", backend.storeGlobalCalls)
	}
}

func TestJsonDbDeferredPersistenceCoalescesDirtyStores(t *testing.T) {
	db := NewJsonDb(t.TempDir())
	backend := &stubPersistenceBackend{}
	db.persistence = backend

	if err := db.WithDeferredPersistence(func() error {
		db.StoreClients()
		db.StoreClients()
		db.StoreHosts()
		db.StoreHosts()
		db.StoreTasks()
		db.StoreUsers()
		db.StoreGlobal()
		return nil
	}); err != nil {
		t.Fatalf("WithDeferredPersistence() error = %v", err)
	}

	if backend.storeUsersCalls != 1 {
		t.Fatalf("StoreUsers() calls = %d, want 1", backend.storeUsersCalls)
	}
	if backend.storeClientsCalls != 1 {
		t.Fatalf("StoreClients() calls = %d, want 1", backend.storeClientsCalls)
	}
	if backend.storeTasksCalls != 1 {
		t.Fatalf("StoreTasks() calls = %d, want 1", backend.storeTasksCalls)
	}
	if backend.storeHostsCalls != 1 {
		t.Fatalf("StoreHosts() calls = %d, want 1", backend.storeHostsCalls)
	}
	if backend.storeGlobalCalls != 1 {
		t.Fatalf("StoreGlobal() calls = %d, want 1", backend.storeGlobalCalls)
	}
}

func TestJsonDbDeferredPersistenceSupportsNestedScopes(t *testing.T) {
	db := NewJsonDb(t.TempDir())
	backend := &stubPersistenceBackend{}
	db.persistence = backend

	if err := db.WithDeferredPersistence(func() error {
		db.StoreClients()
		return db.WithDeferredPersistence(func() error {
			db.StoreClients()
			db.StoreHosts()
			return nil
		})
	}); err != nil {
		t.Fatalf("WithDeferredPersistence() error = %v", err)
	}

	if backend.storeClientsCalls != 1 {
		t.Fatalf("StoreClients() calls = %d, want 1 after nested scope", backend.storeClientsCalls)
	}
	if backend.storeHostsCalls != 1 {
		t.Fatalf("StoreHosts() calls = %d, want 1 after nested scope", backend.storeHostsCalls)
	}
}
