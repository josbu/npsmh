package file

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/djylb/nps/lib/crypt"
)

func TestDeleteNoStoreTaskIfCurrentOwnerlessDeletesMatchingTask(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "delete-ownerless-task-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &Tunnel{
		Mode:    "tcp",
		Port:    18001,
		Status:  true,
		NoStore: true,
		Client:  client,
		Target:  &Target{TargetStr: "127.0.0.1:80"},
		Flow:    &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.BindRuntimeOwner("node-a", task)
	if remaining := task.RemoveRuntimeOwner("node-a"); remaining != 0 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 0", remaining)
	}

	if !db.DeleteNoStoreTaskIfCurrentOwnerless(task) {
		t.Fatal("DeleteNoStoreTaskIfCurrentOwnerless() = false, want true")
	}
	if _, err := db.GetTask(task.Id); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("GetTask() error = %v, want ErrTaskNotFound", err)
	}
}

func TestDeleteNoStoreTaskIfCurrentOwnerlessIgnoresReplacementOrOwnedTask(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "delete-ownerless-task-guard-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	original := &Tunnel{
		Mode:    "tcp",
		Port:    18002,
		Status:  true,
		NoStore: true,
		Client:  client,
		Target:  &Target{TargetStr: "127.0.0.1:80"},
		Flow:    &Flow{},
	}
	if err := db.NewTask(original); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	replacement := &Tunnel{
		Id:      original.Id,
		Mode:    "tcp",
		Port:    original.Port,
		Status:  true,
		NoStore: true,
		Client:  client,
		Target:  &Target{TargetStr: "127.0.0.1:81"},
		Flow:    &Flow{},
		Remark:  "replacement",
	}
	replacement.BindRuntimeOwner("node-b", replacement)
	db.JsonDb.Tasks.Store(replacement.Id, replacement)

	if db.DeleteNoStoreTaskIfCurrentOwnerless(original) {
		t.Fatal("DeleteNoStoreTaskIfCurrentOwnerless() = true, want false for replacement entry")
	}
	current, err := db.GetTask(original.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if current != replacement {
		t.Fatalf("current task = %p, want replacement %p", current, replacement)
	}

	if db.DeleteNoStoreTaskIfCurrentOwnerless(replacement) {
		t.Fatal("DeleteNoStoreTaskIfCurrentOwnerless() = true, want false while runtime owner remains")
	}
	current, err = db.GetTask(original.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if current != replacement {
		t.Fatalf("current task after ownerful delete attempt = %p, want replacement %p", current, replacement)
	}
}

func TestDeleteNoStoreHostIfCurrentOwnerlessDeletesMatchingHost(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "delete-ownerless-host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &Host{
		Host:     "delete-ownerless.example.com",
		Location: "/",
		Scheme:   "all",
		NoStore:  true,
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	host.BindRuntimeOwner("node-a", host)
	if remaining := host.RemoveRuntimeOwner("node-a"); remaining != 0 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 0", remaining)
	}

	if !db.DeleteNoStoreHostIfCurrentOwnerless(host) {
		t.Fatal("DeleteNoStoreHostIfCurrentOwnerless() = false, want true")
	}
	if _, err := db.GetHostById(host.Id); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("GetHostById() error = %v, want ErrHostNotFound", err)
	}
}

func TestDeleteNoStoreHostIfCurrentOwnerlessIgnoresReplacementOrOwnedHost(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "delete-ownerless-host-guard-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	original := &Host{
		Host:     "delete-ownerless-guard.example.com",
		Location: "/",
		Scheme:   "all",
		NoStore:  true,
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(original); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	replacement := &Host{
		Id:       original.Id,
		Host:     original.Host,
		Location: original.Location,
		Scheme:   original.Scheme,
		NoStore:  true,
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:81"},
		Remark:   "replacement",
	}
	replacement.BindRuntimeOwner("node-b", replacement)
	db.JsonDb.Hosts.Store(replacement.Id, replacement)

	if db.DeleteNoStoreHostIfCurrentOwnerless(original) {
		t.Fatal("DeleteNoStoreHostIfCurrentOwnerless() = true, want false for replacement entry")
	}
	current, err := db.GetHostById(original.Id)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if current != replacement {
		t.Fatalf("current host = %p, want replacement %p", current, replacement)
	}

	if db.DeleteNoStoreHostIfCurrentOwnerless(replacement) {
		t.Fatal("DeleteNoStoreHostIfCurrentOwnerless() = true, want false while runtime owner remains")
	}
	current, err = db.GetHostById(original.Id)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if current != replacement {
		t.Fatalf("current host after ownerful delete attempt = %p, want replacement %p", current, replacement)
	}
}

func TestNewTaskAssignsMissingIDAndAdvancesCounter(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "task-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &Tunnel{
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	if task.Id <= 0 {
		t.Fatalf("task.Id = %d, want auto-assigned positive id", task.Id)
	}
	if next := db.JsonDb.GetTaskId(); int(next) != task.Id+1 {
		t.Fatalf("next task id = %d, want %d after auto assignment", next, task.Id+1)
	}
}

func TestNewTaskAdvancesCounterForExplicitID(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "task-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &Tunnel{
		Id:     7,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	if next := db.JsonDb.GetTaskId(); int(next) != 8 {
		t.Fatalf("next task id = %d, want 8 after explicit id 7", next)
	}
}

func TestGetTaskAndHostByIDDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "task-host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &Tunnel{
		Id:     11,
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
		Id:       12,
		Host:     "task-host.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Target:   &Target{TargetStr: "127.0.0.1:81"},
		Flow:     &Flow{},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	db.JsonDb.Tasks.Store(99, "invalid")
	db.JsonDb.Hosts.Store(98, "invalid")

	gotTask, err := db.GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask(%d) error = %v", task.Id, err)
	}
	if gotTask.Id != task.Id {
		t.Fatalf("GetTask(%d) id = %d, want %d", task.Id, gotTask.Id, task.Id)
	}
	gotHost, err := db.GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById(%d) error = %v", host.Id, err)
	}
	if gotHost.Id != host.Id {
		t.Fatalf("GetHostById(%d) id = %d, want %d", host.Id, gotHost.Id, host.Id)
	}
	if _, err := db.GetTask(99); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("GetTask(99) error = %v, want %v", err, ErrTaskNotFound)
	}
	if _, err := db.GetHostById(98); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("GetHostById(98) error = %v, want %v", err, ErrHostNotFound)
	}
	if _, ok := db.JsonDb.Tasks.Load(99); ok {
		t.Fatal("invalid task entry should be dropped")
	}
	if _, ok := db.JsonDb.Hosts.Load(98); ok {
		t.Fatal("invalid host entry should be dropped")
	}
}

func TestIsHostExistAndDelHostDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &Host{
		Id:       12,
		Host:     "exists.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Target:   &Target{TargetStr: "127.0.0.1:81"},
		Flow:     &Flow{},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	db.JsonDb.Hosts.Store("bad-host-key", "invalid")
	db.JsonDb.Hosts.Store(99, "invalid")

	if db.IsHostExist(&Host{Id: 77, Host: "missing.example.com", Location: "", Scheme: "http"}) {
		t.Fatal("IsHostExist(missing) = true, want false")
	}
	if !db.IsHostExist(&Host{Id: 77, Host: "exists.example.com", Location: "", Scheme: "http"}) {
		t.Fatal("IsHostExist() = false, want duplicate route detection")
	}
	if _, ok := db.JsonDb.Hosts.Load("bad-host-key"); ok {
		t.Fatal("IsHostExist() should drop invalid host key entry")
	}

	if err := db.DelHost(99); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("DelHost(99) error = %v, want %v", err, ErrHostNotFound)
	}
	if _, ok := db.JsonDb.Hosts.Load(99); ok {
		t.Fatal("DelHost() should drop invalid host value entry")
	}
}

func TestNewHostAssignsMissingIDAndAdvancesCounter(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &Host{
		Host:     "demo.example.com",
		Location: "/",
		Scheme:   "all",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if host.Id <= 0 {
		t.Fatalf("host.Id = %d, want auto-assigned positive id", host.Id)
	}
	if next := db.JsonDb.GetHostId(); int(next) != host.Id+1 {
		t.Fatalf("next host id = %d, want %d after auto assignment", next, host.Id+1)
	}
}

func TestNewHostAdvancesCounterForExplicitID(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &Host{
		Id:       9,
		Host:     "demo.example.com",
		Location: "/",
		Scheme:   "all",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	if next := db.JsonDb.GetHostId(); int(next) != 10 {
		t.Fatalf("next host id = %d, want 10 after explicit id 9", next)
	}
}

func TestNewTaskAndNewHostRejectNil(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.NewTask(nil); err == nil {
		t.Fatal("NewTask(nil) error = nil, want rejection")
	}
	if err := db.NewHost(nil); err == nil {
		t.Fatal("NewHost(nil) error = nil, want rejection")
	}
}

func TestNewTaskAndNewHostRejectMissingClient(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.NewTask(&Tunnel{
		Mode:   "tcp",
		Status: true,
		Client: &Client{Id: 77},
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}); err == nil {
		t.Fatal("NewTask(missing client) error = nil, want rejection")
	}
	if err := db.NewHost(&Host{
		Host:     "demo.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   &Client{Id: 77},
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}); err == nil {
		t.Fatal("NewHost(missing client) error = nil, want rejection")
	}
}

func TestUpdateTaskRejectsNilAndMissingTask(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.UpdateTask(nil); err == nil {
		t.Fatal("UpdateTask(nil) error = nil, want rejection")
	}

	client := &Client{
		Id:        1,
		VerifyKey: "task-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if err := db.UpdateTask(&Tunnel{
		Id:     77,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}); err == nil {
		t.Fatal("UpdateTask(missing) error = nil, want rejection")
	}
	if _, err := db.GetTask(77); err == nil {
		t.Fatal("GetTask(77) error = nil, want no implicit upsert on UpdateTask")
	}
}

func TestUpdateTaskRejectsMissingClientReference(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "task-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &Tunnel{
		Id:     7,
		Mode:   "tcp",
		Status: true,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	updated := &Tunnel{
		Id:       task.Id,
		Mode:     task.Mode,
		Status:   task.Status,
		Client:   &Client{Id: 99},
		Target:   task.Target,
		Flow:     task.Flow,
		Password: task.Password,
	}
	if err := db.UpdateTask(updated); err == nil {
		t.Fatal("UpdateTask(missing client) error = nil, want rejection")
	}

	stored, err := db.GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask(%d) error = %v", task.Id, err)
	}
	if stored.Client == nil || stored.Client.Id != client.Id {
		t.Fatalf("stored task client = %+v, want original client id %d", stored.Client, client.Id)
	}
}

func TestUpdateHostRejectsNilAndMissingHost(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.UpdateHost(nil); err == nil {
		t.Fatal("UpdateHost(nil) error = nil, want rejection")
	}

	client := &Client{
		Id:        1,
		VerifyKey: "host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if err := db.UpdateHost(&Host{
		Id:       88,
		Host:     "missing.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}); err == nil {
		t.Fatal("UpdateHost(missing) error = nil, want rejection")
	}
	if _, err := db.GetHostById(88); err == nil {
		t.Fatal("GetHostById(88) error = nil, want no implicit upsert on UpdateHost")
	}
}

func TestUpdateHostRejectsMissingClientReference(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &Host{
		Id:       9,
		Host:     "demo.example.com",
		Location: "/api",
		Scheme:   "https",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	updated := &Host{
		Id:       host.Id,
		Host:     host.Host,
		Location: host.Location,
		Scheme:   host.Scheme,
		Client:   &Client{Id: 99},
		Flow:     host.Flow,
		Target:   host.Target,
	}
	if err := db.UpdateHost(updated); err == nil {
		t.Fatal("UpdateHost(missing client) error = nil, want rejection")
	}

	stored, err := db.GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById(%d) error = %v", host.Id, err)
	}
	if stored.Client == nil || stored.Client.Id != client.Id {
		t.Fatalf("stored host client = %+v, want original client id %d", stored.Client, client.Id)
	}
}

func TestDelTaskAndDelHostRejectMissingRecords(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	if err := db.DelTask(404); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("DelTask(404) error = %v, want %v", err, ErrTaskNotFound)
	}
	if err := db.DelHost(405); !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("DelHost(405) error = %v, want %v", err, ErrHostNotFound)
	}
}

func TestUpdateHostRefreshesIndexAndCertMetadata(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "host-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &Host{
		Id:       9,
		Host:     "old.example.com",
		Location: "/api",
		Scheme:   "https",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	cert := "-----BEGIN CERTIFICATE-----\nupdated-cert\n-----END CERTIFICATE-----"
	key := "-----BEGIN PRIVATE KEY-----\nupdated-key\n-----END PRIVATE KEY-----"
	updated := &Host{
		Id:       host.Id,
		Host:     "new.example.com",
		Location: "",
		Scheme:   "invalid",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:81"},
		CertFile: cert,
		KeyFile:  key,
	}
	if err := db.UpdateHost(updated); err != nil {
		t.Fatalf("UpdateHost() error = %v", err)
	}

	stored, err := db.GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById(%d) error = %v", host.Id, err)
	}
	if stored.Host != "new.example.com" {
		t.Fatalf("stored.Host = %q, want new.example.com", stored.Host)
	}
	if stored.Location != "/" {
		t.Fatalf("stored.Location = %q, want /", stored.Location)
	}
	if stored.Scheme != "all" {
		t.Fatalf("stored.Scheme = %q, want all", stored.Scheme)
	}
	if stored.CertType != "text" {
		t.Fatalf("stored.CertType = %q, want text", stored.CertType)
	}
	expectedHash := crypt.FNV1a64("text", cert, key)
	if stored.CertHash != expectedHash {
		t.Fatalf("stored.CertHash = %q, want %q", stored.CertHash, expectedHash)
	}
	if ids := CurrentHostIndex().Lookup("old.example.com"); containsHostID(ids, host.Id) {
		t.Fatalf("HostIndex.Lookup(old.example.com) = %v, want old id removed", ids)
	}
	if ids := CurrentHostIndex().Lookup("new.example.com"); !containsHostID(ids, host.Id) {
		t.Fatalf("HostIndex.Lookup(new.example.com) = %v, want new id present", ids)
	}
}

func containsHostID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestLoadTaskAndHostFromJsonFileClearRemovedRecordsOnReload(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	taskPayload := []byte(`[
  {"Id":9,"Mode":"secret","Password":"reload-secret","Status":true,"Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	hostPayload := []byte(`[
  {"Id":11,"Host":"reload.example.com","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.TaskFilePath, taskPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(tasks.json) error = %v", err)
	}
	if err := os.WriteFile(db.JsonDb.HostFilePath, hostPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(hosts.json) error = %v", err)
	}

	db.JsonDb.LoadTaskFromJsonFile()
	db.JsonDb.LoadHostFromJsonFile()

	if _, err := db.GetTask(9); err != nil {
		t.Fatalf("GetTask(9) error = %v", err)
	}
	if _, err := db.GetHostById(11); err != nil {
		t.Fatalf("GetHostById(11) error = %v", err)
	}
	if ids := CurrentHostIndex().Lookup("reload.example.com"); !containsHostID(ids, 11) {
		t.Fatalf("HostIndex.Lookup(reload.example.com) = %v, want host id 11", ids)
	}
	if task := db.GetTaskByMd5Password(crypt.Md5("reload-secret")); task == nil || task.Id != 9 {
		t.Fatalf("GetTaskByMd5Password(reload-secret) = %+v, want task id 9", task)
	}

	if err := os.WriteFile(db.JsonDb.TaskFilePath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFile(empty tasks.json) error = %v", err)
	}
	if err := os.WriteFile(db.JsonDb.HostFilePath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFile(empty hosts.json) error = %v", err)
	}

	db.JsonDb.LoadTaskFromJsonFile()
	db.JsonDb.LoadHostFromJsonFile()

	if _, err := db.GetTask(9); err == nil {
		t.Fatal("GetTask(9) error = nil, want removed task after reload")
	}
	if _, err := db.GetHostById(11); err == nil {
		t.Fatal("GetHostById(11) error = nil, want removed host after reload")
	}
	if task := db.GetTaskByMd5Password(crypt.Md5("reload-secret")); task != nil {
		t.Fatalf("GetTaskByMd5Password(reload-secret) = %+v, want nil after reload", task)
	}
	if ids := CurrentHostIndex().Lookup("reload.example.com"); len(ids) != 0 {
		t.Fatalf("HostIndex.Lookup(reload.example.com) = %v, want empty after reload", ids)
	}
	if db.JsonDb.TaskIncreaseId != 0 {
		t.Fatalf("TaskIncreaseId = %d, want 0 after reloading an empty task file", db.JsonDb.TaskIncreaseId)
	}
	if db.JsonDb.HostIncreaseId != 0 {
		t.Fatalf("HostIncreaseId = %d, want 0 after reloading an empty host file", db.JsonDb.HostIncreaseId)
	}
}

func TestLoadTaskFromJsonFileSkipsDuplicatePasswordRecords(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	taskPayload := []byte(`[
  {"Id":9,"Mode":"secret","Password":"reload-secret","Status":true,"Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":10,"Mode":"secret","Password":"reload-secret","Status":true,"Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.TaskFilePath, taskPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(tasks.json) error = %v", err)
	}

	db.JsonDb.LoadTaskFromJsonFile()

	first, err := db.GetTask(9)
	if err != nil {
		t.Fatalf("GetTask(9) error = %v", err)
	}
	if first.Password != "reload-secret" {
		t.Fatalf("first.Password = %q, want reload-secret", first.Password)
	}
	if _, err := db.GetTask(10); err == nil {
		t.Fatal("GetTask(10) error = nil, want duplicate-password task skipped on load")
	}
	lookedUp := db.GetTaskByMd5Password(crypt.Md5("reload-secret"))
	if lookedUp == nil || lookedUp.Id != 9 {
		t.Fatalf("GetTaskByMd5Password(reload-secret) = %+v, want task id 9", lookedUp)
	}
}

func TestLoadTaskFromJsonFileReplacesSameIDAndRemovesOldPasswordIndex(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	taskPayload := []byte(`[
  {"Id":9,"Mode":"secret","Password":"reload-secret-old","Status":true,"Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":9,"Mode":"secret","Password":"reload-secret-new","Status":true,"Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.TaskFilePath, taskPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(tasks.json) error = %v", err)
	}

	db.JsonDb.LoadTaskFromJsonFile()

	current, err := db.GetTask(9)
	if err != nil {
		t.Fatalf("GetTask(9) error = %v", err)
	}
	if current.Password != "reload-secret-new" {
		t.Fatalf("current.Password = %q, want reload-secret-new", current.Password)
	}
	if got := db.GetTaskByMd5Password(crypt.Md5("reload-secret-old")); got != nil {
		t.Fatalf("GetTaskByMd5Password(reload-secret-old) = %+v, want nil after same-id replacement", got)
	}
	if got := db.GetTaskByMd5Password(crypt.Md5("reload-secret-new")); got == nil || got.Id != 9 {
		t.Fatalf("GetTaskByMd5Password(reload-secret-new) = %+v, want task id 9", got)
	}
}

func TestLoadHostFromJsonFileNormalizesRoutingFields(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	hostPayload := []byte(`[
  {"Id":11,"Host":"reload.example.com","Location":"","Scheme":"invalid","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.HostFilePath, hostPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(hosts.json) error = %v", err)
	}

	db.JsonDb.LoadHostFromJsonFile()

	stored, err := db.GetHostById(11)
	if err != nil {
		t.Fatalf("GetHostById(11) error = %v", err)
	}
	if stored.Location != "/" {
		t.Fatalf("stored.Location = %q, want /", stored.Location)
	}
	if stored.Scheme != "all" {
		t.Fatalf("stored.Scheme = %q, want all", stored.Scheme)
	}
}

func TestLoadHostFromJsonFileSkipsDuplicateRouteRecords(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	hostPayload := []byte(`[
  {"Id":11,"Host":"reload.example.com","Location":"/api","Scheme":"http","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":12,"Host":"reload.example.com","Location":"/api","Scheme":"http","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.HostFilePath, hostPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(hosts.json) error = %v", err)
	}

	db.JsonDb.LoadHostFromJsonFile()

	first, err := db.GetHostById(11)
	if err != nil {
		t.Fatalf("GetHostById(11) error = %v", err)
	}
	if first.Host != "reload.example.com" {
		t.Fatalf("first.Host = %q, want reload.example.com", first.Host)
	}
	if _, err := db.GetHostById(12); err == nil {
		t.Fatal("GetHostById(12) error = nil, want duplicate-route host skipped on reload")
	}
	if ids := CurrentHostIndex().Lookup("reload.example.com"); !containsHostID(ids, 11) || containsHostID(ids, 12) {
		t.Fatalf("HostIndex.Lookup(reload.example.com) = %v, want only id 11", ids)
	}
}

func TestLoadHostFromJsonFileReplacesSameIDAndRemovesOldIndex(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	hostPayload := []byte(`[
  {"Id":11,"Host":"old.example.com","Location":"/api","Scheme":"http","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":11,"Host":"new.example.com","Location":"/api","Scheme":"http","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.HostFilePath, hostPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(hosts.json) error = %v", err)
	}

	db.JsonDb.LoadHostFromJsonFile()

	current, err := db.GetHostById(11)
	if err != nil {
		t.Fatalf("GetHostById(11) error = %v", err)
	}
	if current.Host != "new.example.com" {
		t.Fatalf("current.Host = %q, want new.example.com", current.Host)
	}
	if ids := CurrentHostIndex().Lookup("old.example.com"); len(ids) != 0 {
		t.Fatalf("HostIndex.Lookup(old.example.com) = %v, want empty after same-id replacement", ids)
	}
	if ids := CurrentHostIndex().Lookup("new.example.com"); !containsHostID(ids, 11) {
		t.Fatalf("HostIndex.Lookup(new.example.com) = %v, want id 11", ids)
	}
}

func TestLoadHostFromJsonFileRecomputesTLSMetadata(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	cert := "-----BEGIN CERTIFICATE-----\nreloaded-cert\n-----END CERTIFICATE-----"
	key := "-----BEGIN PRIVATE KEY-----\nreloaded-key\n-----END PRIVATE KEY-----"
	hostPayload, err := json.Marshal([]Host{{
		Id:       11,
		Host:     "reload.example.com",
		Location: "/",
		Scheme:   "https",
		Client:   &Client{Id: 7},
		CertType: "stale",
		CertHash: "stale-hash",
		CertFile: cert,
		KeyFile:  key,
		Flow:     &Flow{},
	}})
	if err != nil {
		t.Fatalf("json.Marshal(host payload) error = %v", err)
	}
	if err := os.WriteFile(db.JsonDb.HostFilePath, hostPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(hosts.json) error = %v", err)
	}

	db.JsonDb.LoadHostFromJsonFile()

	stored, err := db.GetHostById(11)
	if err != nil {
		t.Fatalf("GetHostById(11) error = %v", err)
	}
	if stored.CertType != "text" {
		t.Fatalf("stored.CertType = %q, want text", stored.CertType)
	}
	expectedHash := crypt.FNV1a64("text", cert, key)
	if stored.CertHash != expectedHash {
		t.Fatalf("stored.CertHash = %q, want %q", stored.CertHash, expectedHash)
	}
}

func TestLoadClientFromJsonFileRelinksAndPrunesTaskHostClientReferences(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &Tunnel{
		Id:       9,
		Mode:     "secret",
		Password: "reload-secret",
		Status:   true,
		Client:   client,
		Target:   &Target{TargetStr: "127.0.0.1:80"},
		Flow:     &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	host := &Host{
		Id:       11,
		Host:     "reload.example.com",
		Location: "/",
		Scheme:   "all",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:81"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	originalTaskClient := task.Client
	originalHostClient := host.Client

	reloadPayload := []byte(`[
  {"Id":7,"VerifyKey":"reload-client-updated","Remark":"after","Status":true,"Cnf":{},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.ClientFilePath, reloadPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(clients.json) error = %v", err)
	}

	db.JsonDb.LoadClientFromJsonFile()

	currentClient, err := db.GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", client.Id, err)
	}
	if currentClient.VerifyKey != "reload-client-updated" {
		t.Fatalf("currentClient.VerifyKey = %q, want reload-client-updated", currentClient.VerifyKey)
	}
	currentTask, err := db.GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask(%d) error = %v", task.Id, err)
	}
	if currentTask.Client != currentClient {
		t.Fatalf("task.Client = %p, want rebound client %p", currentTask.Client, currentClient)
	}
	if currentTask.Client == originalTaskClient {
		t.Fatal("task.Client still points to pre-reload client object, want rebound client")
	}
	currentHost, err := db.GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById(%d) error = %v", host.Id, err)
	}
	if currentHost.Client != currentClient {
		t.Fatalf("host.Client = %p, want rebound client %p", currentHost.Client, currentClient)
	}
	if currentHost.Client == originalHostClient {
		t.Fatal("host.Client still points to pre-reload client object, want rebound client")
	}

	if err := os.WriteFile(db.JsonDb.ClientFilePath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("WriteFile(empty clients.json) error = %v", err)
	}
	db.JsonDb.LoadClientFromJsonFile()

	if _, err := db.GetClient(client.Id); err == nil {
		t.Fatalf("GetClient(%d) error = nil, want removed client after reload", client.Id)
	}
	if _, err := db.GetTask(task.Id); err == nil {
		t.Fatalf("GetTask(%d) error = nil, want orphan task pruned after client reload", task.Id)
	}
	if _, err := db.GetHostById(host.Id); err == nil {
		t.Fatalf("GetHostById(%d) error = nil, want orphan host pruned after client reload", host.Id)
	}
	if got := db.GetTaskByMd5Password(crypt.Md5("reload-secret")); got != nil {
		t.Fatalf("GetTaskByMd5Password(reload-secret) = %+v, want nil after orphan task prune", got)
	}
	if ids := CurrentHostIndex().Lookup("reload.example.com"); len(ids) != 0 {
		t.Fatalf("HostIndex.Lookup(reload.example.com) = %v, want empty after orphan host prune", ids)
	}
}

func TestLoadTaskAndHostFromJsonFileSkipEntriesWithoutResolvableClient(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        7,
		VerifyKey: "reload-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	taskPayload := []byte(`[
  {"Id":9,"Mode":"secret","Password":"valid-secret","Status":true,"Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":10,"Mode":"secret","Password":"missing-client","Status":true,"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":11,"Mode":"secret","Password":"orphan-client","Status":true,"Client":{"Id":999},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	hostPayload := []byte(`[
  {"Id":21,"Host":"valid.example.com","Client":{"Id":7},"Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":22,"Host":"missing-client.example.com","Flow":{"ExportFlow":0,"InletFlow":0}},
  {"Id":23,"Host":"orphan-client.example.com","Client":{"Id":999},"Flow":{"ExportFlow":0,"InletFlow":0}}
]`)
	if err := os.WriteFile(db.JsonDb.TaskFilePath, taskPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(tasks.json) error = %v", err)
	}
	if err := os.WriteFile(db.JsonDb.HostFilePath, hostPayload, 0o600); err != nil {
		t.Fatalf("WriteFile(hosts.json) error = %v", err)
	}

	db.JsonDb.LoadTaskFromJsonFile()
	db.JsonDb.LoadHostFromJsonFile()

	if task, err := db.GetTask(9); err != nil {
		t.Fatalf("GetTask(9) error = %v", err)
	} else if task.Client != client {
		t.Fatalf("task.Client = %p, want %p", task.Client, client)
	}
	if _, err := db.GetTask(10); err == nil {
		t.Fatal("GetTask(10) error = nil, want missing-client task skipped")
	}
	if _, err := db.GetTask(11); err == nil {
		t.Fatal("GetTask(11) error = nil, want orphan-client task skipped")
	}
	if task := db.GetTaskByMd5Password(crypt.Md5("missing-client")); task != nil {
		t.Fatalf("GetTaskByMd5Password(missing-client) = %+v, want nil for skipped task", task)
	}
	if task := db.GetTaskByMd5Password(crypt.Md5("orphan-client")); task != nil {
		t.Fatalf("GetTaskByMd5Password(orphan-client) = %+v, want nil for skipped task", task)
	}

	if host, err := db.GetHostById(21); err != nil {
		t.Fatalf("GetHostById(21) error = %v", err)
	} else if host.Client != client {
		t.Fatalf("host.Client = %p, want %p", host.Client, client)
	}
	if _, err := db.GetHostById(22); err == nil {
		t.Fatal("GetHostById(22) error = nil, want missing-client host skipped")
	}
	if _, err := db.GetHostById(23); err == nil {
		t.Fatal("GetHostById(23) error = nil, want orphan-client host skipped")
	}
	if ids := CurrentHostIndex().Lookup("missing-client.example.com"); len(ids) != 0 {
		t.Fatalf("HostIndex.Lookup(missing-client.example.com) = %v, want empty for skipped host", ids)
	}
	if ids := CurrentHostIndex().Lookup("orphan-client.example.com"); len(ids) != 0 {
		t.Fatalf("HostIndex.Lookup(orphan-client.example.com) = %v, want empty for skipped host", ids)
	}
}
