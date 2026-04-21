package service

import (
	"reflect"
	"sort"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func TestDefaultClientServiceUsesInjectedRepositoryForList(t *testing.T) {
	repo := stubRepository{
		listVisibleClient: func(ListClientsInput) ([]*file.Client, int) {
			return []*file.Client{{Id: 7, Remark: "from-backend"}}, 1
		},
	}
	service := DefaultClientService{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{}
		},
		Backend: Backend{Repository: repo, Runtime: stubRuntime{}},
	}

	result := service.List(ListClientsInput{})
	if result.Total != 1 || len(result.Rows) != 1 || result.Rows[0].Remark != "from-backend" {
		t.Fatalf("List() = %+v, want injected repository result", result)
	}
}

func TestDefaultRepositoryRangeClientsReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:            11,
		VerifyKey:     "demo-vkey",
		Remark:        "original-client",
		Status:        true,
		Cnf:           &file.Config{U: "demo", P: "secret"},
		Flow:          &file.Flow{},
		EntryAclMode:  file.AclBlacklist,
		EntryAclRules: "127.0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	var got *file.Client
	defaultRepository{}.RangeClients(func(client *file.Client) bool {
		got = client
		return false
	})
	if got == nil {
		t.Fatal("RangeClients() should return a client")
	}

	stored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if got == stored {
		t.Fatal("RangeClients() should return a cloned client, not the live pointer")
	}

	got.Remark = "mutated-client"
	got.Cnf.U = "mutated"
	got.Flow.Add(10, 20)
	got.EntryAclRules = "8.8.8.8"

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

func TestDefaultRepositoryRangeClientsDropsInvalidEntry(t *testing.T) {
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
	file.GetDb().JsonDb.Clients.Store(999, "bad-client-entry")

	count := 0
	defaultRepository{}.RangeClients(func(client *file.Client) bool {
		count++
		return true
	})

	if count != 1 {
		t.Fatalf("RangeClients() count = %d, want 1 valid client", count)
	}
	if _, ok := file.GetDb().JsonDb.Clients.Load(999); ok {
		t.Fatal("RangeClients() should remove invalid client entry")
	}
}

func TestDefaultRepositoryRangeMethodsAllowNilCallback(t *testing.T) {
	resetBackendTestDB(t)

	defaultRepository{}.RangeClients(nil)
	defaultRepository{}.RangeUsers(nil)
	defaultRepository{}.RangeTunnels(nil)
	defaultRepository{}.RangeHosts(nil)
}

func TestDefaultRepositoryRangeUsersReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewUser(&file.User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	var got *file.User
	defaultRepository{}.RangeUsers(func(user *file.User) bool {
		got = user
		return false
	})
	if got == nil {
		t.Fatal("RangeUsers() should return a user")
	}

	stored, err := file.GetDb().GetUser(7)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if got == stored {
		t.Fatal("RangeUsers() should return a cloned user, not the live pointer")
	}

	got.Username = "mutated"
	got.TotalFlow.Add(5, 6)

	if stored.Username != "tenant" {
		t.Fatalf("stored user username = %q, want %q", stored.Username, "tenant")
	}
	if stored.TotalFlow == nil || stored.TotalFlow.InletFlow != 0 || stored.TotalFlow.ExportFlow != 0 {
		t.Fatalf("stored user total flow mutated = %+v", stored.TotalFlow)
	}
}

func TestDefaultRepositoryRangeUsersDropsInvalidEntry(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewUser(&file.User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	file.GetDb().JsonDb.Users.Store(999, "bad-user-entry")

	count := 0
	defaultRepository{}.RangeUsers(func(user *file.User) bool {
		count++
		return true
	})

	if count != 1 {
		t.Fatalf("RangeUsers() count = %d, want 1 valid user", count)
	}
	if _, ok := file.GetDb().JsonDb.Users.Load(999); ok {
		t.Fatal("RangeUsers() should remove invalid user entry")
	}
}

func TestDefaultRepositoryGetClientReturnsWorkingCopy(t *testing.T) {
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

	got, err := defaultRepository{}.GetClient(11)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	stored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("db GetClient() error = %v", err)
	}
	if got == stored {
		t.Fatal("GetClient() should return a cloned client, not the live pointer")
	}

	got.Remark = "mutated-client"
	got.Flow.Add(1, 2)
	if stored.Remark != "original-client" {
		t.Fatalf("stored client remark = %q, want %q", stored.Remark, "original-client")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored client flow mutated = %+v", stored.Flow)
	}
}

func TestDefaultRepositoryGetUserAndGetUserByUsernameReturnWorkingCopy(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewUser(&file.User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	gotByID, err := defaultRepository{}.GetUser(7)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	gotByName, err := defaultRepository{}.GetUserByUsername("tenant")
	if err != nil {
		t.Fatalf("GetUserByUsername() error = %v", err)
	}
	stored, err := file.GetDb().GetUser(7)
	if err != nil {
		t.Fatalf("db GetUser() error = %v", err)
	}
	if gotByID == stored || gotByName == stored {
		t.Fatal("GetUser/GetUserByUsername should return cloned users, not the live pointer")
	}

	gotByID.Username = "mutated-id"
	gotByName.TotalFlow.Add(3, 4)
	if stored.Username != "tenant" {
		t.Fatalf("stored user username = %q, want %q", stored.Username, "tenant")
	}
	if stored.TotalFlow == nil || stored.TotalFlow.InletFlow != 0 || stored.TotalFlow.ExportFlow != 0 {
		t.Fatalf("stored user total flow mutated = %+v", stored.TotalFlow)
	}
}

func TestDefaultRepositoryGetUserByExternalPlatformIDReturnsWorkingCopy(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewUser(&file.User{
		Id:                 7,
		Username:           "svc-master-a",
		Password:           "secret",
		Kind:               "platform_service",
		ExternalPlatformID: "master-a",
		Hidden:             true,
		Status:             1,
		TotalFlow:          &file.Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	got, err := defaultRepository{}.GetUserByExternalPlatformID("master-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID() error = %v", err)
	}
	stored, err := file.GetDb().GetUser(7)
	if err != nil {
		t.Fatalf("db GetUser() error = %v", err)
	}
	if got == stored {
		t.Fatal("GetUserByExternalPlatformID() should return a cloned user, not the live pointer")
	}

	got.Username = "mutated"
	got.TotalFlow.Add(3, 4)
	if stored.Username != "svc-master-a" {
		t.Fatalf("stored user username = %q, want %q", stored.Username, "svc-master-a")
	}
	if stored.TotalFlow == nil || stored.TotalFlow.InletFlow != 0 || stored.TotalFlow.ExportFlow != 0 {
		t.Fatalf("stored user total flow mutated = %+v", stored.TotalFlow)
	}
}

func TestDefaultRepositoryGetClientsByUserIDReturnsWorkingCopies(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewUser(&file.User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:            11,
		VerifyKey:     "demo-vkey",
		Remark:        "original-client",
		OwnerUserID:   7,
		Status:        true,
		Cnf:           &file.Config{U: "demo"},
		Flow:          &file.Flow{},
		EntryAclMode:  file.AclBlacklist,
		EntryAclRules: "127.0.0.1",
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got, err := defaultRepository{}.GetClientsByUserID(7)
	if err != nil {
		t.Fatalf("GetClientsByUserID() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("GetClientsByUserID() len = %d, want 1", len(got))
	}
	stored, err := file.GetDb().GetClient(11)
	if err != nil {
		t.Fatalf("db GetClient() error = %v", err)
	}
	if got[0] == stored {
		t.Fatal("GetClientsByUserID() should return cloned clients, not live pointers")
	}

	got[0].Remark = "mutated-client"
	got[0].Flow.Add(1, 2)
	if stored.Remark != "original-client" {
		t.Fatalf("stored client remark = %q, want %q", stored.Remark, "original-client")
	}
	if stored.Flow == nil || stored.Flow.InletFlow != 0 || stored.Flow.ExportFlow != 0 {
		t.Fatalf("stored client flow mutated = %+v", stored.Flow)
	}
}

func TestDefaultRepositoryGetClientIDsByUserIDUsesStoredEntries(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewUser(&file.User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:          11,
		VerifyKey:   "owner-a",
		OwnerUserID: 7,
		Status:      true,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(owner-a) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:          12,
		VerifyKey:   "owner-b",
		OwnerUserID: 7,
		Status:      true,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(owner-b) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:          13,
		VerifyKey:   "other-owner",
		OwnerUserID: 8,
		Status:      true,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(other-owner) error = %v", err)
	}
	file.GetDb().JsonDb.Clients.Store(999, "bad-client-entry")

	clientIDs, err := defaultRepository{}.GetClientIDsByUserID(7)
	if err != nil {
		t.Fatalf("GetClientIDsByUserID() error = %v", err)
	}
	sort.Ints(clientIDs)
	if !reflect.DeepEqual(clientIDs, []int{11, 12}) {
		t.Fatalf("GetClientIDsByUserID() = %v, want [11 12]", clientIDs)
	}
}

func TestDefaultRepositoryGetManagedClientIDsByUserIDUsesIndexedVisibilityFilters(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().NewClient(&file.Client{
		Id:          11,
		VerifyKey:   "owner-client",
		OwnerUserID: 7,
		Status:      true,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(owner) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:             12,
		VerifyKey:      "manager-client",
		OwnerUserID:    8,
		ManagerUserIDs: []int{7},
		Status:         true,
		Cnf:            &file.Config{},
		Flow:           &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(manager) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:          13,
		VerifyKey:   "hidden-client",
		OwnerUserID: 7,
		Status:      true,
		NoDisplay:   true,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(hidden) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{
		Id:          14,
		VerifyKey:   "disabled-client",
		OwnerUserID: 7,
		Status:      false,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(disabled) error = %v", err)
	}
	clientIDs, err := defaultRepository{}.GetManagedClientIDsByUserID(7)
	if err != nil {
		t.Fatalf("GetManagedClientIDsByUserID() error = %v", err)
	}
	sort.Ints(clientIDs)
	if !reflect.DeepEqual(clientIDs, []int{11, 12}) {
		t.Fatalf("GetManagedClientIDsByUserID() = %v, want [11 12]", clientIDs)
	}
}
