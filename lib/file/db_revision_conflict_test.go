package file

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/common"
)

func TestUpdateUserRejectsExpectedRevisionMismatch(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	user := &User{Id: 1, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &Flow{}}
	if err := db.NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	current, err := db.GetUser(1)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	current.Username = "tenant-updated"
	current.TouchMeta()
	current.ExpectedRevision = 999

	if err := db.UpdateUser(current); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("UpdateUser() error = %v, want %v", err, ErrRevisionConflict)
	}
}

func TestUpdateClientRejectsExpectedRevisionMismatch(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{Id: 1, VerifyKey: "vk-1", Status: true, Cnf: &Config{}, Flow: &Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	current, err := db.GetClient(1)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	current.Remark = "updated"
	current.TouchMeta("", "", "")
	current.ExpectedRevision = 999

	if err := db.UpdateClient(current); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("UpdateClient() error = %v, want %v", err, ErrRevisionConflict)
	}
}

func TestUpdateTaskRejectsExpectedRevisionMismatch(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{Id: 1, VerifyKey: "vk-task", Status: true, Cnf: &Config{}, Flow: &Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	tunnel := &Tunnel{
		Id:         1,
		Mode:       "tcp",
		Status:     true,
		Client:     client,
		Port:       10080,
		TargetType: common.CONN_TCP,
		Target:     &Target{TargetStr: "127.0.0.1:80"},
		Flow:       &Flow{},
	}
	if err := db.NewTask(tunnel); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	current, err := db.GetTask(1)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	current.Remark = "updated"
	current.TouchMeta()
	current.ExpectedRevision = 999

	if err := db.UpdateTask(current); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("UpdateTask() error = %v, want %v", err, ErrRevisionConflict)
	}
}

func TestUpdateHostRejectsExpectedRevisionMismatch(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{Id: 1, VerifyKey: "vk-host", Status: true, Cnf: &Config{}, Flow: &Flow{}}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &Host{
		Id:     1,
		Host:   "demo.example.com",
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	current, err := db.GetHostById(1)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	current.Remark = "updated"
	current.TouchMeta()
	current.ExpectedRevision = 999

	if err := db.UpdateHost(current); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("UpdateHost() error = %v, want %v", err, ErrRevisionConflict)
	}
}
