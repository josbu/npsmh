package server

import (
	"errors"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type stubRuntimeSpecialClientStore struct {
	clientsByID      map[int]*file.Client
	clientsByVerify  map[string]*file.Client
	createdClientIDs []int
	deletedClientIDs []int
}

func (s *stubRuntimeSpecialClientStore) GetClient(id int) (*file.Client, error) {
	if client, ok := s.clientsByID[id]; ok {
		return client, nil
	}
	return nil, errors.New("not found")
}

func (s *stubRuntimeSpecialClientStore) GetClientByVerifyKey(vkey string) (*file.Client, error) {
	if client, ok := s.clientsByVerify[vkey]; ok {
		return client, nil
	}
	return nil, errors.New("not found")
}

func (s *stubRuntimeSpecialClientStore) CreateClient(client *file.Client) error {
	if s.clientsByID == nil {
		s.clientsByID = make(map[int]*file.Client)
	}
	if s.clientsByVerify == nil {
		s.clientsByVerify = make(map[string]*file.Client)
	}
	if client.Id == 0 {
		client.Id = len(s.clientsByID) + 1
	}
	s.clientsByID[client.Id] = client
	s.clientsByVerify[client.VerifyKey] = client
	s.createdClientIDs = append(s.createdClientIDs, client.Id)
	return nil
}

func (s *stubRuntimeSpecialClientStore) DeleteClient(id int) error {
	client, ok := s.clientsByID[id]
	if !ok {
		return errors.New("not found")
	}
	delete(s.clientsByID, id)
	delete(s.clientsByVerify, client.VerifyKey)
	s.deletedClientIDs = append(s.deletedClientIDs, id)
	return nil
}

func (s *stubRuntimeSpecialClientStore) RangeClients(fn func(*file.Client) bool) {
	for _, client := range s.clientsByID {
		if !fn(client) {
			return
		}
	}
}

type stubRuntimeSpecialClientRuntime struct {
	published []int
	hidden    []int
	dropped   []int
}

func (s *stubRuntimeSpecialClientRuntime) Publish(id int) {
	s.published = append(s.published, id)
}

func (s *stubRuntimeSpecialClientRuntime) Unpublish(id int) {
	s.hidden = append(s.hidden, id)
}

func (s *stubRuntimeSpecialClientRuntime) DropBridgeClient(id int) {
	s.dropped = append(s.dropped, id)
}

type stubManagedTaskStore struct {
	tasks []*file.Tunnel
}

func (s stubManagedTaskStore) RangeTasks(fn func(*file.Tunnel) bool) {
	for _, task := range s.tasks {
		if !fn(task) {
			return
		}
	}
}

type stubRuntimePresence struct {
	ids map[int]bool
}

func (s stubRuntimePresence) Contains(id int) bool {
	return s.ids[id]
}

type stubPersistedClientPresence struct {
	ids map[int]bool
}

func (s stubPersistedClientPresence) HasClient(id int) bool {
	return s.ids[id]
}

type stubActiveBridgeClients struct {
	ids          []int
	disconnected []int
}

func (s *stubActiveBridgeClients) RangeClientIDs(fn func(int) bool) {
	for _, id := range s.ids {
		if !fn(id) {
			return
		}
	}
}

func (s *stubActiveBridgeClients) Disconnect(id int) {
	s.disconnected = append(s.disconnected, id)
}

type stubBridgeEventStore struct {
	client      *file.Client
	clientErr   error
	deletedID   int
	secretTasks map[string]*file.Tunnel
}

func (s *stubBridgeEventStore) GetClient(id int) (*file.Client, error) {
	if s.clientErr != nil {
		return nil, s.clientErr
	}
	if s.client != nil && s.client.Id == id {
		return s.client, nil
	}
	return nil, errRuntimeSpecialClientStoreUnavailable
}

func (s *stubBridgeEventStore) DeleteClient(id int) error {
	s.deletedID = id
	return nil
}

func (s *stubBridgeEventStore) GetTaskBySecret(password string) *file.Tunnel {
	return s.secretTasks[password]
}

type stubBridgeEventRuntime struct {
	removedHostIDs   []int
	restartedTaskIDs []int
	deletedClientIDs []int
	deletedByUUID    []struct {
		id   int
		uuid string
	}
	secretTaskCalls chan secretTaskCall
}

type secretTaskCall struct {
	task *file.Tunnel
	cfg  *servercfg.Snapshot
}

func TestBridgeRuntimeSpecialClientsUnpublishDoesNotRemoveReplacementRuntime(t *testing.T) {
	var entries sync.Map
	runtime := bridgeRuntimeSpecialClients{
		tasks: runtimeRegistry{
			entries: func() *sync.Map { return &entries },
		},
	}

	runtime.Publish(17)
	replacement := struct{}{}
	runtime.tasks.Store(17, replacement)

	runtime.Unpublish(17)

	current, ok := runtime.tasks.Load(17)
	if !ok || current != replacement {
		t.Fatalf("runtime task entry = %v, %v; want replacement runtime preserved", current, ok)
	}
}

func (s *stubBridgeEventRuntime) RemoveHostCache(id int) {
	s.removedHostIDs = append(s.removedHostIDs, id)
}

func (s *stubBridgeEventRuntime) RestartTask(id int) error {
	s.restartedTaskIDs = append(s.restartedTaskIDs, id)
	return nil
}

func (s *stubBridgeEventRuntime) DeleteClientResources(id int) {
	s.deletedClientIDs = append(s.deletedClientIDs, id)
}

func (s *stubBridgeEventRuntime) DeleteClientResourcesByUUID(id int, uuid string) {
	s.deletedByUUID = append(s.deletedByUUID, struct {
		id   int
		uuid string
	}{id: id, uuid: uuid})
}

func (s *stubBridgeEventRuntime) HandleSecretTask(task *file.Tunnel, _ *conn.Conn, cfg *servercfg.Snapshot) {
	if s.secretTaskCalls != nil {
		s.secretTaskCalls <- secretTaskCall{task: task, cfg: cfg}
	}
}

func TestManagedTaskCoordinatorStartPersisted(t *testing.T) {
	tasks := []*file.Tunnel{
		{Id: 1, Status: true},
		{Id: 2, Status: false},
		{Id: 3, Status: true},
		nil,
	}
	started := make([]int, 0)

	managedTaskCoordinator{
		store:   stubManagedTaskStore{tasks: tasks},
		runtime: stubRuntimePresence{ids: map[int]bool{3: true}},
		start: func(task *file.Tunnel) error {
			started = append(started, task.Id)
			return nil
		},
	}.StartPersisted()

	if !reflect.DeepEqual(started, []int{1}) {
		t.Fatalf("StartPersisted() started %v, want [1]", started)
	}
}

func TestManagedTaskCoordinatorStopPreserveStatus(t *testing.T) {
	tasks := []*file.Tunnel{
		{Id: 1},
		nil,
		{Id: 2},
	}
	stopped := make([]int, 0)

	managedTaskCoordinator{
		store: stubManagedTaskStore{tasks: tasks},
		stop: func(id int) {
			stopped = append(stopped, id)
		},
	}.StopPreserveStatus()

	if !reflect.DeepEqual(stopped, []int{1, 2}) {
		t.Fatalf("StopPreserveStatus() stopped %v, want [1 2]", stopped)
	}
}

func TestOrphanClientCoordinatorDisconnectOrphans(t *testing.T) {
	active := &stubActiveBridgeClients{ids: []int{1, 2, 3}}

	orphanClientCoordinator{
		active:    active,
		persisted: stubPersistedClientPresence{ids: map[int]bool{1: true, 3: true}},
	}.DisconnectOrphans()

	if !reflect.DeepEqual(active.disconnected, []int{2}) {
		t.Fatalf("DisconnectOrphans() disconnected %v, want [2]", active.disconnected)
	}
}

func TestRuntimeSpecialClientCoordinatorEnsureLocalProxy(t *testing.T) {
	store := &stubRuntimeSpecialClientStore{}
	runtime := &stubRuntimeSpecialClientRuntime{}
	coordinator := runtimeSpecialClientCoordinator{store: store, runtime: runtime}

	coordinator.EnsureLocalProxy(true)
	local, err := store.GetClient(-1)
	if err != nil {
		t.Fatalf("EnsureLocalProxy(true) local client error = %v", err)
	}
	if local.VerifyKey != "localproxy" || !local.ConfigConnAllow || !local.NoStore {
		t.Fatalf("EnsureLocalProxy(true) local client = %+v", local)
	}

	coordinator.EnsureLocalProxy(false)
	if _, err := store.GetClient(-1); err == nil {
		t.Fatal("EnsureLocalProxy(false) should delete local proxy client")
	}
	if !reflect.DeepEqual(runtime.dropped, []int{-1}) {
		t.Fatalf("DropBridgeClient calls = %v, want [-1]", runtime.dropped)
	}
	if !reflect.DeepEqual(runtime.hidden, []int{-1}) {
		t.Fatalf("Unpublish calls = %v, want [-1]", runtime.hidden)
	}
}

func TestRuntimeSpecialClientCoordinatorSyncVKeys(t *testing.T) {
	stale := file.NewClient("stale-public", true, true)
	stale.Remark = "public_vkey"
	stale.Id = 99
	store := &stubRuntimeSpecialClientStore{
		clientsByID: map[int]*file.Client{
			stale.Id: stale,
		},
		clientsByVerify: map[string]*file.Client{
			stale.VerifyKey: stale,
		},
	}
	runtime := &stubRuntimeSpecialClientRuntime{}
	coordinator := runtimeSpecialClientCoordinator{store: store, runtime: runtime}

	coordinator.SyncVKeys(&servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			PublicVKey:  "public-access",
			VisitorVKey: "visitor-access",
		},
	})

	publicClient, err := store.GetClientByVerifyKey("public-access")
	if err != nil {
		t.Fatalf("public client error = %v", err)
	}
	visitorClient, err := store.GetClientByVerifyKey("visitor-access")
	if err != nil {
		t.Fatalf("visitor client error = %v", err)
	}
	if publicClient.Remark != "public_vkey" || visitorClient.Remark != "visitor_vkey" {
		t.Fatalf("created runtime clients = public:%+v visitor:%+v", publicClient, visitorClient)
	}
	if _, err := store.GetClient(stale.Id); err == nil {
		t.Fatalf("stale client %d should be pruned", stale.Id)
	}
	if !reflect.DeepEqual(runtime.dropped, []int{stale.Id}) {
		t.Fatalf("DropBridgeClient calls = %v, want [%d]", runtime.dropped, stale.Id)
	}
	if !reflect.DeepEqual(runtime.hidden, []int{visitorClient.Id, stale.Id}) && !reflect.DeepEqual(runtime.hidden, []int{stale.Id, visitorClient.Id}) {
		t.Fatalf("Unpublish calls = %v, want visitor and stale ids", runtime.hidden)
	}
	if len(runtime.published) == 0 {
		t.Fatal("Publish should be called for public runtime client")
	}
	for _, id := range runtime.published {
		if id != publicClient.Id {
			t.Fatalf("Publish calls = %v, want only public client id %d", runtime.published, publicClient.Id)
		}
	}
}

func TestBridgeEventCoordinatorHandlesOpenTaskAndCloseClient(t *testing.T) {
	store := &stubBridgeEventStore{
		client: &file.Client{Id: 9, NoStore: true},
	}
	runtime := &stubBridgeEventRuntime{}
	coordinator := bridgeEventCoordinator{
		store:         store,
		runtime:       runtime,
		currentConfig: servercfg.Current,
	}

	coordinator.HandleOpenHost(&file.Host{Id: 7})
	coordinator.HandleOpenTask(&file.Tunnel{Id: 8})
	coordinator.HandleCloseClient(9)

	if !reflect.DeepEqual(runtime.removedHostIDs, []int{7}) {
		t.Fatalf("RemoveHostCache calls = %v, want [7]", runtime.removedHostIDs)
	}
	if !reflect.DeepEqual(runtime.restartedTaskIDs, []int{8}) {
		t.Fatalf("RestartTask calls = %v, want [8]", runtime.restartedTaskIDs)
	}
	if !reflect.DeepEqual(runtime.deletedClientIDs, []int{9}) {
		t.Fatalf("DeleteClientResources calls = %v, want [9]", runtime.deletedClientIDs)
	}
	if store.deletedID != 9 {
		t.Fatalf("DeleteClient id = %d, want 9", store.deletedID)
	}
}

func TestBridgeEventCoordinatorHandleSecretDispatchesEnabledTask(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	task := &file.Tunnel{Id: 11, Status: true}
	store := &stubBridgeEventStore{
		secretTasks: map[string]*file.Tunnel{
			"secret-key": task,
		},
	}
	runtime := &stubBridgeEventRuntime{
		secretTaskCalls: make(chan secretTaskCall, 1),
	}
	cfg := &servercfg.Snapshot{
		Feature: servercfg.FeatureConfig{
			AllowSecretLocal: true,
		},
	}
	coordinator := bridgeEventCoordinator{
		store:   store,
		runtime: runtime,
		currentConfig: func() *servercfg.Snapshot {
			return cfg
		},
	}

	coordinator.HandleSecret(conn.NewSecret("secret-key", conn.NewConn(serverConn)))

	select {
	case call := <-runtime.secretTaskCalls:
		if call.task != task {
			t.Fatalf("HandleSecret task = %+v, want %+v", call.task, task)
		}
		if call.cfg == nil || !call.cfg.AllowSecretLocalEnabled() {
			t.Fatalf("HandleSecret cfg = %+v, want allow secret local enabled", call.cfg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HandleSecret() did not dispatch enabled task")
	}
}

func TestDealBridgeTaskWithSourceDispatchesBridgeEvents(t *testing.T) {
	openHost := make(chan *file.Host, 1)
	openTask := make(chan *file.Tunnel, 1)
	secretChan := make(chan *conn.Secret, 1)
	stop := make(chan struct{})
	done := make(chan string, 3)

	serverConn, clientConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()

	go func() {
		dealBridgeTaskWithSource(
			bridgeEventSource{
				openHost: openHost,
				openTask: openTask,
				secret:   secretChan,
			},
			func(h *file.Host) {
				if h != nil && h.Id == 1 {
					done <- "open-host"
				}
			},
			func(tunnel *file.Tunnel) {
				if tunnel != nil && tunnel.Id == 2 {
					done <- "open-task"
				}
			},
			func(secret *conn.Secret) {
				if secret != nil && secret.Password == "secret-key" {
					done <- "secret"
				}
			},
			stop,
		)
	}()

	openHost <- &file.Host{Id: 1}
	openTask <- &file.Tunnel{Id: 2}
	secretChan <- conn.NewSecret("secret-key", conn.NewConn(serverConn))

	want := map[string]struct{}{
		"open-host": {},
		"open-task": {},
		"secret":    {},
	}
	deadline := time.After(2 * time.Second)
	for len(want) > 0 {
		select {
		case name := <-done:
			delete(want, name)
		case <-deadline:
			t.Fatalf("dealBridgeTaskWithSource() missing events: %v", want)
		}
	}

	close(stop)
}

func TestDealBridgeTaskWithSourceStopsAfterEventChannelsClose(t *testing.T) {
	openHost := make(chan *file.Host)
	openTask := make(chan *file.Tunnel)
	secretChan := make(chan *conn.Secret)
	done := make(chan struct{})
	nilDispatches := make(chan string, 3)

	go func() {
		dealBridgeTaskWithSource(
			bridgeEventSource{
				openHost: openHost,
				openTask: openTask,
				secret:   secretChan,
			},
			func(h *file.Host) {
				if h == nil {
					nilDispatches <- "open-host"
				}
			},
			func(tunnel *file.Tunnel) {
				if tunnel == nil {
					nilDispatches <- "open-task"
				}
			},
			func(secret *conn.Secret) {
				if secret == nil {
					nilDispatches <- "secret"
				}
			},
			nil,
		)
		close(done)
	}()

	close(openHost)
	close(openTask)
	close(secretChan)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dealBridgeTaskWithSource() did not exit after all event channels closed")
	}

	select {
	case got := <-nilDispatches:
		t.Fatalf("dealBridgeTaskWithSource() dispatched closed-channel zero value to handler %q", got)
	default:
	}
}
