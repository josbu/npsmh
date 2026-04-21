package file

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestUpdateTaskStatusPersistsWithoutClientLookup(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        1,
		VerifyKey: "task-status-client",
		Status:    true,
		Cnf:       &Config{},
		Flow:      &Flow{},
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &Tunnel{
		Id:     11,
		Port:   18011,
		Mode:   "tcp",
		Status: false,
		Client: client,
		Target: &Target{TargetStr: "127.0.0.1:80"},
		Flow:   &Flow{},
	}
	if err := db.NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	db.JsonDb.Clients.Delete(client.Id)

	if err := db.UpdateTaskStatus(task.Id, true); err != nil {
		t.Fatalf("UpdateTaskStatus() error = %v", err)
	}

	stored, err := db.GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !stored.Status {
		t.Fatal("stored task status = false, want true")
	}

	content, err := os.ReadFile(db.JsonDb.TaskFilePath)
	if err != nil {
		t.Fatalf("ReadFile(tasks.json) error = %v", err)
	}
	var tasks []*Tunnel
	if err := json.Unmarshal(content, &tasks); err != nil {
		t.Fatalf("Unmarshal(tasks.json) error = %v", err)
	}
	if len(tasks) != 1 || !tasks[0].Status {
		t.Fatalf("persisted tasks = %+v, want one started task", tasks)
	}
}

func TestUpdateTaskStatusRejectsMissingTask(t *testing.T) {
	resetMigrationTestDB(t)

	if err := GetDb().UpdateTaskStatus(99, true); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("UpdateTaskStatus(99) error = %v, want %v", err, ErrTaskNotFound)
	}
}
