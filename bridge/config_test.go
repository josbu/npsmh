package bridge

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
)

type configWriteFailConn struct {
	reader   *bytes.Reader
	writeErr error
}

func (c *configWriteFailConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *configWriteFailConn) Write([]byte) (int, error)        { return 0, c.writeErr }
func (c *configWriteFailConn) Close() error                     { return nil }
func (c *configWriteFailConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *configWriteFailConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *configWriteFailConn) SetDeadline(time.Time) error      { return nil }
func (c *configWriteFailConn) SetReadDeadline(time.Time) error  { return nil }
func (c *configWriteFailConn) SetWriteDeadline(time.Time) error { return nil }

func newConfigWriteFailBridgeConn(t *testing.T, payload interface{}, flag string, writeErr error) *conn.Conn {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	lenBytes, err := conn.GetLenBytes(body)
	if err != nil {
		t.Fatalf("GetLenBytes() error = %v", err)
	}
	raw := append([]byte(flag), lenBytes...)
	raw = append(raw, body...)
	return conn.NewConn(&configWriteFailConn{
		reader:   bytes.NewReader(raw),
		writeErr: writeErr,
	})
}

func resetBridgeConfigTestDB(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	oldDb := file.Db
	oldIndexes := file.SnapshotRuntimeIndexes()
	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(db)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())
	t.Cleanup(func() {
		file.ReplaceDb(oldDb)
		file.ReplaceRuntimeIndexes(oldIndexes)
	})
}

func assertBridgeEventBackpressureBounded(t *testing.T, start time.Time) {
	t.Helper()
	maxElapsed := bridgeConfigEventEnqueueTimeout + 3*time.Second
	if maxElapsed <= 0 {
		maxElapsed = 3 * time.Second
	}
	if elapsed := time.Since(start); elapsed > maxElapsed {
		t.Fatalf("enqueue elapsed %s, want bounded backpressure within %s", elapsed, maxElapsed)
	}
}

func TestPublishNoStoreHostUpdateRollsBackWhenQueueIsFull(t *testing.T) {
	oldTimeout := bridgeConfigEventEnqueueTimeout
	bridgeConfigEventEnqueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		bridgeConfigEventEnqueueTimeout = oldTimeout
	})

	b := NewTunnel(false, nil, 0)
	b.OpenHost = make(chan *file.Host, 1)
	b.OpenHost <- &file.Host{}

	host := &file.Host{
		Remark:       "old",
		Target:       &file.Target{TargetStr: "127.0.0.1:8080"},
		NoStore:      true,
		EntryAclMode: file.AclWhitelist,
	}
	updated := &file.Host{
		Remark:       "new",
		Target:       &file.Target{TargetStr: "127.0.0.1:9090"},
		EntryAclMode: file.AclBlacklist,
	}

	start := time.Now()
	if b.publishNoStoreHostUpdate(host, updated, "node-1") {
		t.Fatal("publishNoStoreHostUpdate() = true, want false when queue is full")
	}
	assertBridgeEventBackpressureBounded(t, start)
	if host.Remark != "old" {
		t.Fatalf("host.Remark = %q, want old value restored", host.Remark)
	}
	if host.Target == nil || host.Target.TargetStr != "127.0.0.1:8080" {
		t.Fatalf("host.Target = %+v, want old target restored", host.Target)
	}
	if host.EntryAclMode != file.AclWhitelist {
		t.Fatalf("host.EntryAclMode = %d, want %d", host.EntryAclMode, file.AclWhitelist)
	}
}

func TestPublishNoStoreTaskUpdateRollsBackWhenQueueIsFull(t *testing.T) {
	oldTimeout := bridgeConfigEventEnqueueTimeout
	bridgeConfigEventEnqueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		bridgeConfigEventEnqueueTimeout = oldTimeout
	})

	b := NewTunnel(false, nil, 0)
	b.OpenTask = make(chan *file.Tunnel, 1)
	b.OpenTask <- &file.Tunnel{}

	task := &file.Tunnel{
		Remark:       "old",
		Target:       &file.Target{TargetStr: "127.0.0.1:7000"},
		NoStore:      true,
		EntryAclMode: file.AclWhitelist,
	}
	updated := &file.Tunnel{
		Remark:       "new",
		Target:       &file.Target{TargetStr: "127.0.0.1:8000"},
		EntryAclMode: file.AclBlacklist,
	}

	start := time.Now()
	if b.publishNoStoreTaskUpdate(task, updated, "node-1") {
		t.Fatal("publishNoStoreTaskUpdate() = true, want false when queue is full")
	}
	assertBridgeEventBackpressureBounded(t, start)
	if task.Remark != "old" {
		t.Fatalf("task.Remark = %q, want old value restored", task.Remark)
	}
	if task.Target == nil || task.Target.TargetStr != "127.0.0.1:7000" {
		t.Fatalf("task.Target = %+v, want old target restored", task.Target)
	}
	if task.EntryAclMode != file.AclWhitelist {
		t.Fatalf("task.EntryAclMode = %d, want %d", task.EntryAclMode, file.AclWhitelist)
	}
}

func TestPublishNewTaskDeletesTaskWhenQueueIsFull(t *testing.T) {
	resetBridgeConfigTestDB(t)

	oldTimeout := bridgeConfigEventEnqueueTimeout
	bridgeConfigEventEnqueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		bridgeConfigEventEnqueueTimeout = oldTimeout
	})

	b := NewTunnel(false, nil, 0)
	b.OpenTask = make(chan *file.Tunnel, 1)
	b.OpenTask <- &file.Tunnel{}

	client := file.NewClient("bridge-config-test", true, false)
	client.Id = 12
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:       34,
		Port:     9000,
		Mode:     "tcp",
		Status:   true,
		Client:   client,
		Flow:     &file.Flow{},
		NoStore:  true,
		Target:   &file.Target{TargetStr: "127.0.0.1:9001"},
		Password: "",
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	start := time.Now()
	if b.publishNewTask(task) {
		t.Fatal("publishNewTask() = true, want false when queue is full")
	}
	assertBridgeEventBackpressureBounded(t, start)
	if _, err := file.GetDb().GetTask(task.Id); err == nil {
		t.Fatalf("task %d should be deleted after enqueue failure", task.Id)
	}
}
func TestGetConfigRejectsFileTaskWhenRuntimeClientLacksCurrentNode(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-file-config", false, false)
	client.Id = 101
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeClient := NewClient(client.Id, NewNode("old-node", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "new-node")
	}()

	task := &file.Tunnel{
		Mode:      "file",
		Remark:    "broken-file-task",
		LocalPath: t.TempDir(),
		Target:    &file.Target{TargetStr: ""},
	}
	if _, err := clientConn.SendInfo(task, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(NEW_TASK) error = %v", err)
	}
	if clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = true, want false when file runtime mapping fails")
	}
	<-done

	if _, ok := b.Client.Load(client.Id); ok {
		t.Fatalf("runtime client %d should be removed after failed file task config", client.Id)
	}

	taskCount := 0
	file.GetDb().JsonDb.Tasks.Range(func(_, _ interface{}) bool {
		taskCount++
		return true
	})
	if taskCount != 0 {
		t.Fatalf("task count = %d, want 0 after rejected file task config", taskCount)
	}
}

func TestGetConfigKeepsRuntimeClientOnRecoverableTaskFailure(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-survives-task-failure", false, false)
	client.Id = 102
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeClient := NewClient(client.Id, NewNode("node-1", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	invalidTask := &file.Tunnel{
		Mode:   "tcp",
		Ports:  "",
		Remark: "invalid-task",
		Target: &file.Target{TargetStr: "127.0.0.1:9000"},
	}
	if _, err := clientConn.SendInfo(invalidTask, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(invalid NEW_TASK) error = %v", err)
	}
	if clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = true, want false for invalid NEW_TASK")
	}

	validHost := &file.Host{
		Host:   "recoverable.example.com",
		Scheme: "all",
		Remark: "survives-task-failure",
		Target: &file.Target{TargetStr: "127.0.0.1:9001"},
	}
	if _, err := clientConn.SendInfo(validHost, common.NEW_HOST); err != nil {
		t.Fatalf("SendInfo(valid NEW_HOST) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = false, want true for valid NEW_HOST after recoverable task failure")
	}

	_ = clientConn.Close()
	<-done

	current, ok := b.Client.Load(client.Id)
	if !ok || current != runtimeClient {
		t.Fatalf("runtime client for id %d should remain unchanged, current=%v ok=%v", client.Id, current, ok)
	}
	if runtimeClient.IsClosed() {
		t.Fatal("runtime client should remain open after recoverable task failure")
	}

	hostCount := 0
	file.GetDb().JsonDb.Hosts.Range(func(_, value interface{}) bool {
		host := value.(*file.Host)
		if host.Client != nil && host.Client.Id == client.Id && host.Host == validHost.Host {
			hostCount++
		}
		return true
	})
	if hostCount != 1 {
		t.Fatalf("host count = %d, want 1 successfully added host after recoverable task failure", hostCount)
	}
}

func TestGetConfigWritesAddFailWhenNewHostPersistFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-missing-host-client", false, false)
	client.Id = 105
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}

	runtimeClient := NewClient(client.Id, NewNode("node-1", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	host := &file.Host{
		Host:   "missing-client.example.com",
		Scheme: "all",
		Remark: "missing-client",
		Target: &file.Target{TargetStr: "127.0.0.1:9010"},
	}
	if _, err := clientConn.SendInfo(host, common.NEW_HOST); err != nil {
		t.Fatalf("SendInfo(NEW_HOST) error = %v", err)
	}
	if clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = true, want false when NewHost() persistence fails")
	}

	_ = clientConn.Close()
	<-done

	if current, ok := b.Client.Load(client.Id); !ok || current != runtimeClient {
		t.Fatalf("runtime client for id %d should remain unchanged, current=%v ok=%v", client.Id, current, ok)
	}
	if runtimeClient.IsClosed() {
		t.Fatal("runtime client should remain open after recoverable host persistence failure")
	}

	found := false
	file.GetDb().JsonDb.Hosts.Range(func(_, value interface{}) bool {
		stored := value.(*file.Host)
		if stored.Host == host.Host {
			found = true
			return false
		}
		return true
	})
	if found {
		t.Fatal("host should not be stored when NewHost() persistence fails")
	}
}

func TestGetConfigRollsBackNewHostWhenResponseWriteFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-host-write-failure", false, false)
	client.Id = 107
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &file.Host{
		Host:   "rollback-host-write-failure.example.com",
		Scheme: "all",
		Remark: "rollback-host-write-failure",
		Target: &file.Target{TargetStr: "127.0.0.1:9012"},
	}

	serverConn := newConfigWriteFailBridgeConn(t, host, common.NEW_HOST, io.ErrClosedPipe)
	b.getConfig(serverConn, false, client, 7, "test", "node-1")

	found := false
	file.GetDb().JsonDb.Hosts.Range(func(_, value interface{}) bool {
		stored := value.(*file.Host)
		if stored.Host == host.Host {
			found = true
			return false
		}
		return true
	})
	if found {
		t.Fatal("host should be rolled back when NEW_HOST response write fails")
	}
}

func TestGetConfigRollsBackNoStoreHostUpdateWhenResponseWriteFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-host-update-write-failure", false, false)
	client.Id = 108
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &file.Host{
		Host:    "rollback-nostore-host.example.com",
		Scheme:  "all",
		Remark:  "old-host",
		NoStore: true,
		Client:  client,
		Target:  &file.Target{TargetStr: "127.0.0.1:9013"},
	}
	oldRemark := host.Remark
	oldTarget := host.Target.TargetStr
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	updated := &file.Host{
		Host:   host.Host,
		Scheme: host.Scheme,
		Remark: "new-host",
		Target: &file.Target{TargetStr: "127.0.0.1:9014"},
	}

	serverConn := newConfigWriteFailBridgeConn(t, updated, common.NEW_HOST, io.ErrClosedPipe)
	b.getConfig(serverConn, false, client, 7, "test", "node-1")

	stored, err := file.GetDb().GetHostById(host.Id)
	if err != nil {
		t.Fatalf("GetHostById() error = %v", err)
	}
	if stored.Remark != oldRemark {
		t.Fatalf("stored.Remark = %q, want %q", stored.Remark, oldRemark)
	}
	if stored.Target == nil || stored.Target.TargetStr != oldTarget {
		t.Fatalf("stored.Target = %+v, want original target %q", stored.Target, oldTarget)
	}
}

func TestGetConfigRollsBackNoStoreTaskUpdateWhenResponseWriteFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-task-update-write-failure", false, false)
	client.Id = 109
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	task := &file.Tunnel{
		Mode:    "tcp",
		Port:    12005,
		Remark:  "old-task",
		NoStore: true,
		Status:  true,
		Flow:    &file.Flow{},
		Client:  client,
		Target:  &file.Target{TargetStr: "127.0.0.1:9201"},
	}
	oldRemark := task.Remark
	oldTarget := task.Target.TargetStr
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	updated := &file.Tunnel{
		Mode:   "tcp",
		Ports:  strconv.Itoa(task.Port),
		Remark: "new-task",
		Target: &file.Target{TargetStr: "127.0.0.1:9202"},
	}

	serverConn := newConfigWriteFailBridgeConn(t, updated, common.NEW_TASK, io.ErrClosedPipe)
	b.getConfig(serverConn, false, client, 7, "test", "node-1")

	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if stored.Remark != oldRemark {
		t.Fatalf("stored.Remark = %q, want %q", stored.Remark, oldRemark)
	}
	if stored.Target == nil || stored.Target.TargetStr != oldTarget {
		t.Fatalf("stored.Target = %+v, want original target %q", stored.Target, oldTarget)
	}
}

func TestGetConfigRollsBackFileTaskWhenResponseWriteFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-file-task-write-failure", false, false)
	client.Id = 110
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeClient := NewClient(client.Id, NewNode("node-1", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	task := &file.Tunnel{
		Mode:      "file",
		Remark:    "file-task-write-failure",
		LocalPath: t.TempDir(),
		Target:    &file.Target{TargetStr: ""},
	}

	key := file.FileTunnelRuntimeKey(client.VerifyKey, task)
	serverConn := newConfigWriteFailBridgeConn(t, task, common.NEW_TASK, io.ErrClosedPipe)
	b.getConfig(serverConn, false, client, 7, "test", "node-1")

	found := false
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		stored := value.(*file.Tunnel)
		if stored.Client != nil && stored.Client.Id == client.Id && stored.Remark == task.Remark {
			found = true
			return false
		}
		return true
	})
	if found {
		t.Fatal("file task should be rolled back when NEW_TASK response write fails")
	}
	if _, ok := runtimeClient.GetNodeByFile(key); ok {
		t.Fatalf("file runtime key %q should be removed after rollback", key)
	}
	if current, ok := b.Client.Load(client.Id); !ok || current != runtimeClient {
		t.Fatalf("runtime client should remain unchanged after file task rollback, current=%v ok=%v", current, ok)
	}
}

func TestGetConfigRemovesFileRuntimeKeyWhenTaskPublishQueueIsFull(t *testing.T) {
	resetBridgeConfigTestDB(t)

	oldTimeout := bridgeConfigEventEnqueueTimeout
	bridgeConfigEventEnqueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		bridgeConfigEventEnqueueTimeout = oldTimeout
	})

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	b.OpenTask = make(chan *file.Tunnel, 1)
	b.OpenTask <- &file.Tunnel{}

	client := file.NewClient("bridge-config-file-task-queue-full", false, false)
	client.Id = 111
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeClient := NewClient(client.Id, NewNode("node-1", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	task := &file.Tunnel{
		Mode:      "file",
		Remark:    "file-task-queue-full",
		LocalPath: t.TempDir(),
		Target:    &file.Target{TargetStr: ""},
	}
	if _, err := clientConn.SendInfo(task, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(NEW_TASK) error = %v", err)
	}
	if clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = true, want false when file task publish queue is full")
	}

	key := file.FileTunnelRuntimeKey(client.VerifyKey, task)

	_ = clientConn.Close()
	<-done

	found := false
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		stored := value.(*file.Tunnel)
		if stored.Client != nil && stored.Client.Id == client.Id && stored.Remark == task.Remark {
			found = true
			return false
		}
		return true
	})
	if found {
		t.Fatal("file task should be rolled back when publish queue is full")
	}
	if _, ok := runtimeClient.GetNodeByFile(key); ok {
		t.Fatalf("file runtime key %q should be removed when publish queue is full", key)
	}
}

func TestGetConfigNoStoreFileUpdateRollbackKeepsExistingOwnerMapping(t *testing.T) {
	resetBridgeConfigTestDB(t)

	oldTimeout := bridgeConfigEventEnqueueTimeout
	bridgeConfigEventEnqueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		bridgeConfigEventEnqueueTimeout = oldTimeout
	})

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	b.OpenTask = make(chan *file.Tunnel, 1)
	b.OpenTask <- &file.Tunnel{}

	client := file.NewClient("bridge-config-file-nostore-owner-rollback", false, false)
	client.Id = 116
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeClient := NewClient(client.Id, NewNode("node-1", "test", 0))
	runtimeClient.Id = -1
	runtimeClient.AddNode(NewNode("node-2", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	localPath := t.TempDir()
	fileKey := file.FileTunnelRuntimeKey(client.VerifyKey, &file.Tunnel{
		Mode:      "file",
		Client:    client,
		LocalPath: localPath,
		Target:    &file.Target{},
	})
	if err := runtimeClient.AddFile(fileKey, "node-1"); err != nil {
		t.Fatalf("AddFile(existing owner) error = %v", err)
	}

	existing := &file.Tunnel{
		Mode:      "file",
		Port:      0,
		Remark:    "existing-file-nostore",
		NoStore:   true,
		Status:    true,
		Flow:      &file.Flow{},
		Client:    client,
		LocalPath: localPath,
		Target:    &file.Target{TargetStr: "file://" + fileKey},
	}
	existing.BindRuntimeOwner("node-1", existing)
	if err := file.GetDb().NewTask(existing); err != nil {
		t.Fatalf("NewTask(existing file task) error = %v", err)
	}

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-2")
	}()

	incoming := &file.Tunnel{
		Mode:      "file",
		Remark:    "incoming-file-nostore",
		LocalPath: localPath,
		Target:    &file.Target{TargetStr: ""},
	}
	if _, err := clientConn.SendInfo(incoming, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(NEW_TASK) error = %v", err)
	}
	if clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = true, want false when no-store file task publish queue is full")
	}

	_ = clientConn.Close()
	<-done

	got, ok := runtimeClient.GetNodeByFile(fileKey)
	if !ok || got == nil || got.UUID != "node-1" {
		t.Fatalf("GetNodeByFile(%q) = %#v, %v, want existing owner node-1 after rollback", fileKey, got, ok)
	}

	runtimeClient.RemoveFileOwner(fileKey, "node-1")
	if got, ok := runtimeClient.GetNodeByFile(fileKey); ok || got != nil {
		t.Fatalf("GetNodeByFile(%q) after removing node-1 = %#v, %v, want nil, false", fileKey, got, ok)
	}
}

func TestGetConfigFileTasksWithDifferentAuthRemainDistinct(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-file-auth-distinct", false, false)
	client.Id = 117
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	runtimeClient := NewClient(client.Id, NewNode("node-1", "test", 0))
	b.Client.Store(client.Id, runtimeClient)

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	localPath := t.TempDir()
	first := &file.Tunnel{
		Mode:      "file",
		Remark:    "file-auth-a",
		LocalPath: localPath,
		Target:    &file.Target{TargetStr: ""},
		UserAuth: &file.MultiAccount{
			Content: "ops=token-a",
		},
	}
	second := &file.Tunnel{
		Mode:      "file",
		Remark:    "file-auth-b",
		LocalPath: localPath,
		Target:    &file.Target{TargetStr: ""},
		UserAuth: &file.MultiAccount{
			Content: "ops=token-b",
		},
	}
	if _, err := clientConn.SendInfo(first, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(first NEW_TASK) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("first GetAddStatus() = false, want true for first file task")
	}
	if _, err := clientConn.SendInfo(second, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(second NEW_TASK) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("second GetAddStatus() = false, want true for distinct-auth file task")
	}

	_ = clientConn.Close()
	<-done

	count := 0
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		stored := value.(*file.Tunnel)
		if stored.Client != nil && stored.Client.Id == client.Id && stored.Mode == "file" && stored.LocalPath == localPath {
			count++
		}
		return true
	})
	if count != 2 {
		t.Fatalf("stored file task count = %d, want 2 for distinct auth variants", count)
	}

	firstKey := file.FileTunnelRuntimeKey(client.VerifyKey, &file.Tunnel{
		Mode:      "file",
		Client:    client,
		LocalPath: localPath,
		Target:    &file.Target{},
		UserAuth: &file.MultiAccount{
			Content: "ops=token-a",
		},
	})
	secondKey := file.FileTunnelRuntimeKey(client.VerifyKey, &file.Tunnel{
		Mode:      "file",
		Client:    client,
		LocalPath: localPath,
		Target:    &file.Target{},
		UserAuth: &file.MultiAccount{
			Content: "ops=token-b",
		},
	})
	if firstKey == secondKey {
		t.Fatalf("file runtime keys should differ for distinct auth variants, both=%q", firstKey)
	}
	if _, ok := runtimeClient.files.Load(firstKey); !ok {
		t.Fatalf("runtime mapping for first auth variant key %q should exist", firstKey)
	}
	if _, ok := runtimeClient.files.Load(secondKey); !ok {
		t.Fatalf("runtime mapping for second auth variant key %q should exist", secondKey)
	}
}

func TestGetConfigRollsBackTaskWhenPortCheckFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()
	busyPort := listener.Addr().(*net.TCPAddr).Port

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-port-check-failure", false, false)
	client.Id = 106
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	task := &file.Tunnel{
		Mode:   "tcp",
		Ports:  strconv.Itoa(busyPort),
		Remark: "port-check-failure",
		Target: &file.Target{TargetStr: "127.0.0.1:9100"},
	}
	if _, err := clientConn.SendInfo(task, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(NEW_TASK) error = %v", err)
	}
	if clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = true, want false when TestTunnelPort() fails")
	}

	host := &file.Host{
		Host:   "after-port-check-failure.example.com",
		Scheme: "all",
		Remark: "after-port-check-failure",
		Target: &file.Target{TargetStr: "127.0.0.1:9011"},
	}
	if _, err := clientConn.SendInfo(host, common.NEW_HOST); err != nil {
		t.Fatalf("SendInfo(valid NEW_HOST) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = false, want true for NEW_HOST after task port-check failure")
	}

	_ = clientConn.Close()
	<-done

	foundTask := false
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		stored := value.(*file.Tunnel)
		if stored.Client != nil && stored.Client.Id == client.Id && stored.Remark == task.Remark {
			foundTask = true
			return false
		}
		return true
	})
	if foundTask {
		t.Fatal("task should be rolled back when TestTunnelPort() fails")
	}
}

func TestGetConfigMultiPortTaskFailureKeepsStatusStreamAligned(t *testing.T) {
	resetBridgeConfigTestDB(t)

	oldTimeout := bridgeConfigEventEnqueueTimeout
	bridgeConfigEventEnqueueTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		bridgeConfigEventEnqueueTimeout = oldTimeout
	})

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	b.OpenTask = make(chan *file.Tunnel, 1)
	client := file.NewClient("bridge-config-multi-port", false, false)
	client.Id = 103
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	task := &file.Tunnel{
		Mode:   "tcp",
		Ports:  "12000,12001",
		Remark: "multi-port",
		Target: &file.Target{TargetStr: "9000,9001"},
	}
	if _, err := clientConn.SendInfo(task, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(multi-port NEW_TASK) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("first GetAddStatus() = false, want true for the first published port")
	}
	if clientConn.GetAddStatus() {
		t.Fatal("second GetAddStatus() = true, want false after later publish failure in the same multi-port task")
	}

	host := &file.Host{
		Host:   "aligned.example.com",
		Scheme: "all",
		Remark: "after-multi-port-failure",
		Target: &file.Target{TargetStr: "127.0.0.1:9002"},
	}
	if _, err := clientConn.SendInfo(host, common.NEW_HOST); err != nil {
		t.Fatalf("SendInfo(valid NEW_HOST) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = false, want true for NEW_HOST after multi-port task failure")
	}

	_ = clientConn.Close()
	<-done
}

func TestGetConfigCopiesTaskSecurityAndHealthFields(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-task-fields", false, false)
	client.Id = 114
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	task := &file.Tunnel{
		Mode:          "secret",
		Remark:        "copied-task-fields",
		Password:      "copied-task-password",
		TargetAddr:    "127.0.0.1",
		TargetType:    common.CONN_UDP,
		EntryAclMode:  file.AclWhitelist,
		EntryAclRules: "203.0.113.10",
		DestAclMode:   file.AclBlacklist,
		DestAclRules:  "full:blocked.example",
		ExpireAt:      123456,
		FlowLimit:     654321,
		RateLimit:     12,
		MaxConn:       3,
		Target: &file.Target{
			TargetStr:     "127.0.0.1:9300",
			LocalProxy:    true,
			ProxyProtocol: 2,
		},
		UserAuth: &file.MultiAccount{
			Content:    "user=pass",
			AccountMap: map[string]string{"user": "pass"},
		},
		MultiAccount: &file.MultiAccount{
			Content:    "guest=demo",
			AccountMap: map[string]string{"guest": "demo"},
		},
		Health: file.Health{
			HealthCheckTimeout:  7,
			HealthMaxFail:       2,
			HealthCheckInterval: 11,
			HttpHealthUrl:       "/healthz",
			HealthCheckType:     "http",
			HealthCheckTarget:   "127.0.0.1:9301",
		},
	}
	if _, err := clientConn.SendInfo(task, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(NEW_TASK) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = false, want true for copied task fields")
	}

	_ = clientConn.Close()
	<-done

	var stored *file.Tunnel
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		current := value.(*file.Tunnel)
		if current.Client != nil && current.Client.Id == client.Id && current.Remark == task.Remark {
			stored = current
			return false
		}
		return true
	})
	if stored == nil {
		t.Fatal("stored task not found")
	}
	if stored.TargetType != common.CONN_UDP {
		t.Fatalf("stored.TargetType = %q, want %q", stored.TargetType, common.CONN_UDP)
	}
	if stored.EntryAclMode != file.AclWhitelist || stored.EntryAclRules != "203.0.113.10" {
		t.Fatalf("stored entry acl = (%d, %q), want (%d, %q)", stored.EntryAclMode, stored.EntryAclRules, file.AclWhitelist, "203.0.113.10")
	}
	if stored.DestAclMode != file.AclBlacklist || stored.DestAclRules != "full:blocked.example" {
		t.Fatalf("stored dest acl = (%d, %q), want (%d, %q)", stored.DestAclMode, stored.DestAclRules, file.AclBlacklist, "full:blocked.example")
	}
	if stored.ExpireAt != 123456 || stored.FlowLimit != 654321 || stored.RateLimit != 12 || stored.MaxConn != 3 {
		t.Fatalf("stored limits = expire:%d flow:%d rate:%d maxconn:%d, want 123456/654321/12/3", stored.ExpireAt, stored.FlowLimit, stored.RateLimit, stored.MaxConn)
	}
	if stored.Target == nil || stored.Target.TargetStr != "127.0.0.1:9300" || !stored.Target.LocalProxy || stored.Target.ProxyProtocol != 2 {
		t.Fatalf("stored.Target = %+v, want target 127.0.0.1:9300 with local_proxy and proxy protocol 2", stored.Target)
	}
	if stored.UserAuth == nil || stored.UserAuth.Content != "user=pass" || stored.UserAuth.AccountMap["user"] != "pass" {
		t.Fatalf("stored.UserAuth = %+v, want copied user auth", stored.UserAuth)
	}
	if stored.MultiAccount == nil || stored.MultiAccount.Content != "guest=demo" || stored.MultiAccount.AccountMap["guest"] != "demo" {
		t.Fatalf("stored.MultiAccount = %+v, want copied multi account", stored.MultiAccount)
	}
	if stored.HealthCheckTimeout != 7 || stored.HealthMaxFail != 2 || stored.HealthCheckInterval != 11 {
		t.Fatalf("stored health config = timeout:%d maxfail:%d interval:%d, want 7/2/11", stored.HealthCheckTimeout, stored.HealthMaxFail, stored.HealthCheckInterval)
	}
	if stored.HttpHealthUrl != "/healthz" || stored.HealthCheckType != "http" || stored.HealthCheckTarget != "127.0.0.1:9301" {
		t.Fatalf("stored health routing = url:%q type:%q target:%q, want /healthz/http/127.0.0.1:9301", stored.HttpHealthUrl, stored.HealthCheckType, stored.HealthCheckTarget)
	}
}

func TestGetConfigCopiesTaskLocalProxyForMultiPortTargets(t *testing.T) {
	resetBridgeConfigTestDB(t)

	nextFreePort := func() int {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen() error = %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		return port
	}

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-multi-local-proxy", false, false)
	client.Id = 115
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	port1 := nextFreePort()
	port2 := nextFreePort()
	task := &file.Tunnel{
		Mode:   "tcp",
		Ports:  strconv.Itoa(port1) + "," + strconv.Itoa(port2),
		Remark: "copied-multi-local-proxy",
		Target: &file.Target{
			TargetStr:  "127.0.0.1:9300,127.0.0.1:9301",
			LocalProxy: true,
		},
	}
	if _, err := clientConn.SendInfo(task, common.NEW_TASK); err != nil {
		t.Fatalf("SendInfo(NEW_TASK) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("first GetAddStatus() = false, want true for first multi-port local-proxy task")
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("second GetAddStatus() = false, want true for second multi-port local-proxy task")
	}

	_ = clientConn.Close()
	<-done

	storedPorts := map[int]bool{}
	file.GetDb().JsonDb.Tasks.Range(func(_, value interface{}) bool {
		stored := value.(*file.Tunnel)
		if stored.Client == nil || stored.Client.Id != client.Id {
			return true
		}
		if stored.Remark != task.Remark+"_"+strconv.Itoa(port1) && stored.Remark != task.Remark+"_"+strconv.Itoa(port2) {
			return true
		}
		if stored.Target == nil || !stored.Target.LocalProxy {
			t.Fatalf("stored.Target = %+v, want local_proxy preserved for multi-port task", stored.Target)
		}
		storedPorts[stored.Port] = true
		return true
	})
	if !storedPorts[port1] || !storedPorts[port2] || len(storedPorts) != 2 {
		t.Fatalf("stored ports = %#v, want both %d and %d", storedPorts, port1, port2)
	}
}

func TestGetConfigNewConfIgnoresCallerSuppliedClientID(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)

	existing := file.NewClient("existing-config-client", false, false)
	existing.Id = 41
	existing.Flow = &file.Flow{}
	existing.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(existing); err != nil {
		t.Fatalf("NewClient(existing) error = %v", err)
	}
	runtimeClient := NewClient(existing.Id, NewNode("existing-node", "test", 0))
	b.Client.Store(existing.Id, runtimeClient)

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, true, existing, 7, "test", "new-node")
	}()

	incoming := &file.Client{
		Id:        existing.Id,
		VerifyKey: "incoming-config-client",
		Remark:    "incoming",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if _, err := clientConn.SendInfo(incoming, common.NEW_CONF); err != nil {
		t.Fatalf("SendInfo(NEW_CONF) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = false, want true for NEW_CONF")
	}
	verifyKey, err := clientConn.GetShortContent(16)
	if err != nil {
		t.Fatalf("GetShortContent(16) error = %v", err)
	}
	_ = clientConn.Close()
	<-done

	storedExisting, err := file.GetDb().GetClient(existing.Id)
	if err != nil {
		t.Fatalf("GetClient(existing) error = %v", err)
	}
	if storedExisting.VerifyKey != existing.VerifyKey {
		t.Fatalf("existing client verify key = %q, want %q", storedExisting.VerifyKey, existing.VerifyKey)
	}
	if storedExisting.Remark != existing.Remark {
		t.Fatalf("existing client remark = %q, want %q", storedExisting.Remark, existing.Remark)
	}
	if current, ok := b.Client.Load(existing.Id); !ok || current != runtimeClient {
		t.Fatalf("runtime client for existing id should remain unchanged, current=%v ok=%v", current, ok)
	}

	var created *file.Client
	file.GetDb().JsonDb.Clients.Range(func(_, value interface{}) bool {
		client := value.(*file.Client)
		if client.Id != existing.Id && client.Remark == incoming.Remark {
			created = client
			return false
		}
		return true
	})
	if created == nil {
		t.Fatal("expected a newly created client with a new server-assigned id")
	}
	if created.Id == existing.Id {
		t.Fatalf("created client id = %d, want a new server-assigned id", created.Id)
	}
	if created.VerifyKey != string(verifyKey) {
		t.Fatalf("created client verify key = %q, want %q", created.VerifyKey, string(verifyKey))
	}
	if len(created.VerifyKey) != 16 {
		t.Fatalf("created client verify key length = %d, want 16", len(created.VerifyKey))
	}
	if created.VerifyKey == incoming.VerifyKey {
		t.Fatalf("created client verify key = %q, want server-assigned value instead of caller-supplied key", created.VerifyKey)
	}
	if created.Remark != incoming.Remark {
		t.Fatalf("created client remark = %q, want %q", created.Remark, incoming.Remark)
	}
	if current, ok := b.Client.Load(created.Id); !ok {
		t.Fatalf("runtime client for new id %d should exist", created.Id)
	} else if runtimeNew, ok := current.(*Client); !ok || runtimeNew == nil {
		t.Fatalf("runtime client for new id %d = %T, want *Client", created.Id, current)
	}
	if runtimeClient.IsClosed() {
		t.Fatal("existing runtime client should not be closed by caller-supplied NEW_CONF id")
	}
}

func TestGetConfigRollsBackNewConfWhenResponseWriteFails(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)

	incoming := &file.Client{
		VerifyKey: "rollback-on-write-failure",
		Remark:    "rollback-new-conf",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}

	serverConn := newConfigWriteFailBridgeConn(t, incoming, common.NEW_CONF, io.ErrClosedPipe)
	b.getConfig(serverConn, true, nil, 7, "test", "new-node")

	found := false
	file.GetDb().JsonDb.Clients.Range(func(_, value interface{}) bool {
		client := value.(*file.Client)
		if client.Remark == incoming.Remark {
			found = true
			return false
		}
		return true
	})
	if found {
		t.Fatal("new client should be rolled back when NEW_CONF response write fails")
	}

	runtimeFound := false
	b.Client.Range(func(_, value interface{}) bool {
		if runtimeClient, ok := value.(*Client); ok && runtimeClient != nil && runtimeClient.Id != 0 {
			runtimeFound = true
			return false
		}
		return true
	})
	if runtimeFound {
		t.Fatal("runtime bridge client should not be published when NEW_CONF response write fails")
	}
}

func TestGetConfigDrainsUnknownPayloadBeforeNextMessage(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-unknown-command", false, false)
	client.Id = 104
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	if _, err := clientConn.SendInfo(map[string]string{"noop": "1"}, "UNKN"); err != nil {
		t.Fatalf("SendInfo(unknown) error = %v", err)
	}

	host := &file.Host{
		Host:   "after-unknown.example.com",
		Scheme: "all",
		Remark: "after-unknown",
		Target: &file.Target{TargetStr: "127.0.0.1:9003"},
	}
	if _, err := clientConn.SendInfo(host, common.NEW_HOST); err != nil {
		t.Fatalf("SendInfo(valid NEW_HOST) error = %v", err)
	}
	if !clientConn.GetAddStatus() {
		t.Fatal("GetAddStatus() = false, want true for NEW_HOST after unknown command")
	}

	_ = clientConn.Close()
	<-done

	found := false
	file.GetDb().JsonDb.Hosts.Range(func(_, value interface{}) bool {
		stored := value.(*file.Host)
		if stored.Client != nil && stored.Client.Id == client.Id && stored.Host == host.Host {
			found = true
			return false
		}
		return true
	})
	if !found {
		t.Fatal("valid host after unknown command should still be stored")
	}
}

func TestGetConfigWorkStatusDropsInvalidTaskAndHostEntries(t *testing.T) {
	resetBridgeConfigTestDB(t)

	runList := &sync.Map{}
	b := NewTunnel(false, runList, 0)
	client := file.NewClient("bridge-config-work-status", false, false)
	client.Id = 113
	client.Flow = &file.Flow{}
	client.Cnf = &file.Config{}
	client.IsConnect = true
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &file.Host{
		Host:   "status.example.com",
		Scheme: "all",
		Remark: "status-host",
		Client: client,
		Target: &file.Target{TargetStr: "127.0.0.1:9400"},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	task := &file.Tunnel{
		Mode:   "tcp",
		Port:   12020,
		Remark: "status-task",
		Status: true,
		Flow:   &file.Flow{},
		Client: client,
		Target: &file.Target{TargetStr: "127.0.0.1:9401"},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	runList.Store(task.Id, struct{}{})
	file.GetDb().JsonDb.Hosts.Store("bad-host", "bad-host-entry")
	file.GetDb().JsonDb.Tasks.Store("bad-task", "bad-task-entry")

	serverSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = serverSide.Close() })
	t.Cleanup(func() { _ = clientSide.Close() })

	serverConn := conn.NewConn(serverSide)
	clientConn := conn.NewConn(clientSide)
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.getConfig(serverConn, false, client, 7, "test", "node-1")
	}()

	if _, err := clientConn.Write([]byte(common.WORK_STATUS)); err != nil {
		t.Fatalf("Write(WORK_STATUS) error = %v", err)
	}
	if _, err := clientConn.Write([]byte(crypt.Blake2b(client.VerifyKey))); err != nil {
		t.Fatalf("Write(blake2b verify key) error = %v", err)
	}

	length, err := clientConn.GetLen()
	if err != nil {
		t.Fatalf("GetLen() error = %v", err)
	}
	body, err := clientConn.GetShortContent(length)
	if err != nil {
		t.Fatalf("GetShortContent(%d) error = %v", length, err)
	}
	_ = clientConn.Close()
	<-done

	want := host.Remark + common.CONN_DATA_SEQ + task.Remark + common.CONN_DATA_SEQ
	if got := string(body); got != want {
		t.Fatalf("WORK_STATUS payload = %q, want %q", got, want)
	}
	if _, ok := file.GetDb().JsonDb.Hosts.Load("bad-host"); ok {
		t.Fatal("invalid host entry should be removed during WORK_STATUS scan")
	}
	if _, ok := file.GetDb().JsonDb.Tasks.Load("bad-task"); ok {
		t.Fatal("invalid task entry should be removed during WORK_STATUS scan")
	}
}
