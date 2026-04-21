package service

import (
	"errors"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func TestNewNodeRuntimeIdentityRotatesConfigEpoch(t *testing.T) {
	identity := NewNodeRuntimeIdentity()
	if identity == nil {
		t.Fatal("NewNodeRuntimeIdentity() = nil")
	}
	if identity.BootID() == "" {
		t.Fatal("BootID() = empty")
	}
	if identity.StartedAt() == 0 {
		t.Fatal("StartedAt() = 0")
	}
	before := identity.ConfigEpoch()
	if before == "" {
		t.Fatal("ConfigEpoch() = empty")
	}
	after := identity.RotateConfigEpoch()
	if after == "" || after == before {
		t.Fatalf("RotateConfigEpoch() = %q, want non-empty new epoch (before %q)", after, before)
	}
}

func TestInMemoryNodeRuntimeStatusStoreNoteOperationTracksMutationFields(t *testing.T) {
	store := NewInMemoryNodeRuntimeStatusStore()

	store.NoteOperation("resource", nil)
	ops := store.Operations()
	if ops.LastMutationAt == 0 {
		t.Fatal("LastMutationAt = 0, want non-zero after resource mutation")
	}

	beforeMutation := ops.LastMutationAt
	store.NoteOperation("sync", nil)
	ops = store.Operations()
	if ops.LastSyncAt == 0 {
		t.Fatal("LastSyncAt = 0, want non-zero after sync")
	}
	if ops.LastMutationAt != beforeMutation {
		t.Fatalf("LastMutationAt changed on sync: got %d want %d", ops.LastMutationAt, beforeMutation)
	}
}

func TestInMemoryNodeOperationStoreFiltersByScope(t *testing.T) {
	store := NewInMemoryNodeOperationStore(10)
	store.Record(NodeOperationRecordPayload{
		OperationID:  "node-admin-op",
		Kind:         "batch",
		Actor:        NodeOperationActorPayload{Kind: "admin", SubjectID: "admin:root", Username: "root"},
		Count:        1,
		SuccessCount: 1,
	})
	store.Record(NodeOperationRecordPayload{
		OperationID:  "platform-admin-op",
		Kind:         "batch",
		Actor:        NodeOperationActorPayload{Kind: "platform_admin", SubjectID: "platform:master-a:ops", Username: "ops", PlatformID: "master-a"},
		Count:        2,
		SuccessCount: 2,
	})
	store.Record(NodeOperationRecordPayload{
		OperationID:  "platform-user-op",
		Kind:         "batch",
		Actor:        NodeOperationActorPayload{Kind: "platform_user", SubjectID: "platform:master-a:user-1", Username: "user-1", PlatformID: "master-a"},
		Count:        3,
		SuccessCount: 3,
	})

	full := store.Query(NodeOperationQueryInput{Scope: testNodeFullAdminScope()})
	if len(full.Items) != 3 {
		t.Fatalf("full scope items = %d, want 3", len(full.Items))
	}

	account := store.Query(NodeOperationQueryInput{
		Scope:     testNodePlatformAdminScope("master-a", 10),
		SubjectID: "platform:master-a:ops",
	})
	if len(account.Items) != 2 {
		t.Fatalf("account scope items = %d, want 2", len(account.Items))
	}

	user := store.Query(NodeOperationQueryInput{
		Scope: ResolveNodeAccessScope(Principal{
			Authenticated: true,
			Kind:          "platform_user",
			SubjectID:     "platform:master-a:user-1",
			Username:      "user-1",
			Roles:         []string{RoleUser},
			ClientIDs:     []int{1},
			Attributes: map[string]string{
				"platform_id":              "master-a",
				"platform_service_user_id": "10",
			},
		}),
		SubjectID: "platform:master-a:user-1",
	})
	if len(user.Items) != 1 || user.Items[0].OperationID != "platform-user-op" {
		t.Fatalf("platform user items = %+v, want only platform-user-op", user.Items)
	}
}

func TestInMemoryNodeOperationStoreReturnsLatestFirstAndSupportsLookup(t *testing.T) {
	store := NewInMemoryNodeOperationStore(2)
	store.Record(NodeOperationRecordPayload{OperationID: "op-1", Kind: "batch", Actor: NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"}, FinishedAt: 1})
	store.Record(NodeOperationRecordPayload{OperationID: "op-2", Kind: "batch", Actor: NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"}, FinishedAt: 2})
	store.Record(NodeOperationRecordPayload{OperationID: "op-3", Kind: "batch", Actor: NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"}, FinishedAt: 3})

	result := store.Query(NodeOperationQueryInput{
		Scope:     testNodePlatformAdminScope("master-a", 10),
		SubjectID: "platform:master-a:ops",
		Limit:     10,
	})
	if len(result.Items) != 2 || result.Items[0].OperationID != "op-3" || result.Items[1].OperationID != "op-2" {
		t.Fatalf("latest items = %+v, want op-3 then op-2", result.Items)
	}

	single := store.Query(NodeOperationQueryInput{
		Scope:       testNodePlatformAdminScope("master-a", 10),
		SubjectID:   "platform:master-a:ops",
		OperationID: "op-2",
	})
	if len(single.Items) != 1 || single.Items[0].OperationID != "op-2" {
		t.Fatalf("lookup items = %+v, want only op-2", single.Items)
	}
}

func TestInMemoryNodeOperationStoreTrimsWithoutLosingLatestRecords(t *testing.T) {
	store := NewInMemoryNodeOperationStore(2)
	store.Record(NodeOperationRecordPayload{OperationID: "op-1", Kind: "batch", Actor: NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"}, FinishedAt: 1})
	store.Record(NodeOperationRecordPayload{OperationID: "op-2", Kind: "batch", Actor: NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"}, FinishedAt: 2})
	store.Record(NodeOperationRecordPayload{OperationID: "op-3", Kind: "batch", Actor: NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"}, FinishedAt: 3})

	snapshot := store.Snapshot()
	if len(snapshot) != 2 || snapshot[0].OperationID != "op-2" || snapshot[1].OperationID != "op-3" {
		t.Fatalf("Snapshot() = %+v, want [op-2 op-3]", snapshot)
	}
}

func TestInMemoryNodeOperationStoreSnapshotAndQueryDetachPaths(t *testing.T) {
	store := NewInMemoryNodeOperationStore(5)
	store.Record(NodeOperationRecordPayload{
		OperationID: "op-1",
		Kind:        "batch",
		Actor:       NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"},
		FinishedAt:  1,
		Paths:       []string{"/api/system/status"},
	})

	query := store.Query(NodeOperationQueryInput{
		Scope:     testNodePlatformAdminScope("master-a", 10),
		SubjectID: "platform:master-a:ops",
	})
	if len(query.Items) != 1 || len(query.Items[0].Paths) != 1 {
		t.Fatalf("Query() = %+v, want detached path payload", query.Items)
	}
	query.Items[0].Paths[0] = "/mutated/query"

	snapshot := store.Snapshot()
	if len(snapshot) != 1 || len(snapshot[0].Paths) != 1 || snapshot[0].Paths[0] != "/api/system/status" {
		t.Fatalf("Snapshot() after query mutation = %+v, want original path", snapshot)
	}
	snapshot[0].Paths[0] = "/mutated/snapshot"

	secondQuery := store.Query(NodeOperationQueryInput{
		Scope:     testNodePlatformAdminScope("master-a", 10),
		SubjectID: "platform:master-a:ops",
	})
	if len(secondQuery.Items) != 1 || len(secondQuery.Items[0].Paths) != 1 || secondQuery.Items[0].Paths[0] != "/api/system/status" {
		t.Fatalf("Query() after snapshot mutation = %+v, want original path", secondQuery.Items)
	}
}

func TestInMemoryNodeOperationStoreRestoreNormalizesRecordsBeforeTrimming(t *testing.T) {
	store := NewInMemoryNodeOperationStore(2)
	store.Restore([]NodeOperationRecordPayload{
		{
			OperationID: " op-1 ",
			Kind:        " batch ",
			Actor:       NodeOperationActorPayload{Kind: " platform_admin ", SubjectID: " platform:master-a:ops ", Username: " ops ", PlatformID: " master-a "},
			FinishedAt:  1,
			Paths:       []string{" /api/system/status ", " ", ""},
		},
		{
			OperationID: " ",
			Kind:        "batch",
			Actor:       NodeOperationActorPayload{Kind: "platform_admin", PlatformID: "master-a"},
			FinishedAt:  2,
			Paths:       []string{"/ignored"},
		},
		{
			OperationID: "op-2",
			Kind:        " sync ",
			Actor:       NodeOperationActorPayload{Kind: " platform_admin ", SubjectID: " platform:master-a:ops ", Username: " ops ", PlatformID: " master-a "},
			FinishedAt:  3,
			Paths:       []string{" /api/system/operations "},
		},
	})

	snapshot := store.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("Snapshot() len = %d, want 2 after dropping blank operation id", len(snapshot))
	}
	if snapshot[0].OperationID != "op-1" || snapshot[1].OperationID != "op-2" {
		t.Fatalf("Snapshot() ids = %+v, want [op-1 op-2]", snapshot)
	}
	if snapshot[0].Kind != "batch" || snapshot[0].Actor.Kind != "platform_admin" || snapshot[0].Actor.SubjectID != "platform:master-a:ops" || snapshot[0].Actor.Username != "ops" || snapshot[0].Actor.PlatformID != "master-a" {
		t.Fatalf("Snapshot()[0] normalization = %+v, want trimmed actor/kind fields", snapshot[0])
	}
	if len(snapshot[0].Paths) != 1 || snapshot[0].Paths[0] != "/api/system/status" {
		t.Fatalf("Snapshot()[0].Paths = %+v, want trimmed visible path", snapshot[0].Paths)
	}
	if snapshot[1].Kind != "sync" || len(snapshot[1].Paths) != 1 || snapshot[1].Paths[0] != "/api/system/operations" {
		t.Fatalf("Snapshot()[1] normalization = %+v, want trimmed kind/path", snapshot[1])
	}
}

type nodeStorageTestStore struct {
	clientsByID   map[int]*file.Client
	clientsByVKey map[string]*file.Client
	usersByID     map[int]*file.User
}

func (s nodeStorageTestStore) GetUser(id int) (*file.User, error) {
	if user, ok := s.usersByID[id]; ok {
		return user, nil
	}
	return nil, file.ErrUserNotFound
}

func (nodeStorageTestStore) GetUserByUsername(string) (*file.User, error) {
	return nil, errors.New("not implemented")
}

func (nodeStorageTestStore) CreateUser(*file.User) error           { return errors.New("not implemented") }
func (nodeStorageTestStore) UpdateUser(*file.User) error           { return errors.New("not implemented") }
func (nodeStorageTestStore) UpdateClient(*file.Client) error       { return nil }
func (nodeStorageTestStore) GetAllClients() []*file.Client         { return nil }
func (nodeStorageTestStore) GetClientsByUserId(int) []*file.Client { return nil }
func (nodeStorageTestStore) GetTunnelsByUserId(int) int            { return 0 }
func (nodeStorageTestStore) GetHostsByUserId(int) int              { return 0 }
func (nodeStorageTestStore) AddTraffic(int, int64, int64)          {}
func (nodeStorageTestStore) Flush() error                          { return nil }

func (s nodeStorageTestStore) GetClient(vkey string) (*file.Client, error) {
	if client, ok := s.clientsByVKey[vkey]; ok {
		return client, nil
	}
	return nil, file.ErrClientNotFound
}

func (s nodeStorageTestStore) GetClientByID(id int) (*file.Client, error) {
	if client, ok := s.clientsByID[id]; ok {
		return client, nil
	}
	return nil, file.ErrClientNotFound
}

func TestDefaultNodeStorageRejectsReservedRuntimeClients(t *testing.T) {
	oldStore := file.GlobalStore
	file.GlobalStore = nodeStorageTestStore{
		clientsByID: map[int]*file.Client{
			1: {Id: 1, VerifyKey: "visible-vkey", Cnf: &file.Config{}, Flow: &file.Flow{}},
			2: {Id: 2, VerifyKey: "hidden-vkey", NoStore: true, NoDisplay: true, Cnf: &file.Config{}, Flow: &file.Flow{}},
		},
		clientsByVKey: map[string]*file.Client{
			"visible-vkey": {Id: 1, VerifyKey: "visible-vkey", Cnf: &file.Config{}, Flow: &file.Flow{}},
			"hidden-vkey":  {Id: 2, VerifyKey: "hidden-vkey", NoStore: true, NoDisplay: true, Cnf: &file.Config{}, Flow: &file.Flow{}},
		},
	}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	storage := DefaultNodeStorage{}

	if client, err := storage.ResolveClient(NodeClientTarget{ClientID: 1}); err != nil || client == nil || client.Id != 1 {
		t.Fatalf("ResolveClient(visible id) = %+v, %v, want visible client", client, err)
	}
	if client, err := storage.ResolveClient(NodeClientTarget{VerifyKey: "visible-vkey"}); err != nil || client == nil || client.Id != 1 {
		t.Fatalf("ResolveClient(visible vkey) = %+v, %v, want visible client", client, err)
	}
	if _, err := storage.ResolveClient(NodeClientTarget{ClientID: 2}); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ResolveClient(hidden id) error = %v, want %v", err, ErrClientNotFound)
	}
	if _, err := storage.ResolveClient(NodeClientTarget{VerifyKey: "hidden-vkey"}); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ResolveClient(hidden vkey) error = %v, want %v", err, ErrClientNotFound)
	}
	if _, err := storage.ResolveTrafficClient(file.TrafficDelta{ClientID: 2}); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ResolveTrafficClient(hidden id) error = %v, want %v", err, ErrClientNotFound)
	}
	if _, err := storage.ResolveTrafficClient(file.TrafficDelta{VerifyKey: "hidden-vkey"}); !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ResolveTrafficClient(hidden vkey) error = %v, want %v", err, ErrClientNotFound)
	}
	if _, err := storage.ResolveClient(NodeClientTarget{ClientID: 1, VerifyKey: "other-vkey"}); !errors.Is(err, ErrClientIdentifierConflict) {
		t.Fatalf("ResolveClient(conflicting identifiers) error = %v, want %v", err, ErrClientIdentifierConflict)
	}
	if _, err := storage.ResolveTrafficClient(file.TrafficDelta{ClientID: 1, VerifyKey: "other-vkey"}); !errors.Is(err, ErrClientIdentifierConflict) {
		t.Fatalf("ResolveTrafficClient(conflicting identifiers) error = %v, want %v", err, ErrClientIdentifierConflict)
	}
}

func TestDefaultNodeStorageReturnsWorkingCopies(t *testing.T) {
	originalUser := &file.User{
		Id:        7,
		Username:  "tenant",
		Password:  "secret",
		TotalFlow: &file.Flow{InletFlow: 3, ExportFlow: 4},
	}
	originalClient := &file.Client{
		Id:        1,
		VerifyKey: "visible-vkey",
		Remark:    "before",
		Cnf:       &file.Config{U: "demo", P: "secret"},
		Flow:      &file.Flow{InletFlow: 5, ExportFlow: 6},
	}
	oldStore := file.GlobalStore
	file.GlobalStore = nodeStorageTestStore{
		usersByID: map[int]*file.User{
			7: originalUser,
		},
		clientsByID: map[int]*file.Client{
			1: originalClient,
		},
		clientsByVKey: map[string]*file.Client{
			"visible-vkey": originalClient,
		},
	}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	storage := DefaultNodeStorage{}

	clientByID, err := storage.ResolveClient(NodeClientTarget{ClientID: 1})
	if err != nil {
		t.Fatalf("ResolveClient(id) error = %v", err)
	}
	if clientByID == originalClient {
		t.Fatal("ResolveClient(id) returned live client pointer")
	}
	clientByID.Remark = "after"
	clientByID.Cnf.U = "changed"
	clientByID.Flow.InletFlow = 99
	if originalClient.Remark != "before" || originalClient.Cnf.U != "demo" || originalClient.Flow.InletFlow != 5 {
		t.Fatalf("original client mutated through ResolveClient(id) = %+v", originalClient)
	}

	clientByVKey, err := storage.ResolveTrafficClient(file.TrafficDelta{VerifyKey: "visible-vkey"})
	if err != nil {
		t.Fatalf("ResolveTrafficClient(vkey) error = %v", err)
	}
	if clientByVKey == originalClient {
		t.Fatal("ResolveTrafficClient(vkey) returned live client pointer")
	}
	clientByVKey.Flow.ExportFlow = 88
	if originalClient.Flow.ExportFlow != 6 {
		t.Fatalf("original client mutated through ResolveTrafficClient(vkey) = %+v", originalClient.Flow)
	}

	user, err := storage.ResolveUser(7)
	if err != nil {
		t.Fatalf("ResolveUser() error = %v", err)
	}
	if user == originalUser {
		t.Fatal("ResolveUser() returned live user pointer")
	}
	user.Username = "changed-user"
	user.TotalFlow.ExportFlow = 77
	if originalUser.Username != "tenant" || originalUser.TotalFlow.ExportFlow != 4 {
		t.Fatalf("original user mutated through ResolveUser() = %+v", originalUser)
	}
}

type nodeStorageErrorStore struct {
	clientErr error
	userErr   error
}

func (s nodeStorageErrorStore) GetUser(id int) (*file.User, error) { return nil, s.userErr }

func (nodeStorageErrorStore) GetUserByUsername(string) (*file.User, error) {
	return nil, errors.New("not implemented")
}

func (nodeStorageErrorStore) CreateUser(*file.User) error           { return errors.New("not implemented") }
func (nodeStorageErrorStore) UpdateUser(*file.User) error           { return errors.New("not implemented") }
func (nodeStorageErrorStore) UpdateClient(*file.Client) error       { return nil }
func (nodeStorageErrorStore) GetAllClients() []*file.Client         { return nil }
func (nodeStorageErrorStore) GetClientsByUserId(int) []*file.Client { return nil }
func (nodeStorageErrorStore) GetTunnelsByUserId(int) int            { return 0 }
func (nodeStorageErrorStore) GetHostsByUserId(int) int              { return 0 }
func (nodeStorageErrorStore) AddTraffic(int, int64, int64)          {}
func (nodeStorageErrorStore) Flush() error                          { return nil }

func (s nodeStorageErrorStore) GetClient(string) (*file.Client, error)  { return nil, s.clientErr }
func (s nodeStorageErrorStore) GetClientByID(int) (*file.Client, error) { return nil, s.clientErr }

func TestDefaultNodeStoragePropagatesUnexpectedLookupErrors(t *testing.T) {
	clientErr := errors.New("store unavailable")
	userErr := errors.New("user backend unavailable")
	oldStore := file.GlobalStore
	file.GlobalStore = nodeStorageErrorStore{
		clientErr: clientErr,
		userErr:   userErr,
	}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	storage := DefaultNodeStorage{}

	if _, err := storage.ResolveClient(NodeClientTarget{ClientID: 1}); !errors.Is(err, clientErr) {
		t.Fatalf("ResolveClient(id) error = %v, want %v", err, clientErr)
	}
	if _, err := storage.ResolveClient(NodeClientTarget{VerifyKey: "visible-vkey"}); !errors.Is(err, clientErr) {
		t.Fatalf("ResolveClient(vkey) error = %v, want %v", err, clientErr)
	}
	if _, err := storage.ResolveTrafficClient(file.TrafficDelta{ClientID: 1}); !errors.Is(err, clientErr) {
		t.Fatalf("ResolveTrafficClient(id) error = %v, want %v", err, clientErr)
	}
	if _, err := storage.ResolveTrafficClient(file.TrafficDelta{VerifyKey: "visible-vkey"}); !errors.Is(err, clientErr) {
		t.Fatalf("ResolveTrafficClient(vkey) error = %v, want %v", err, clientErr)
	}
	if _, err := storage.ResolveUser(7); !errors.Is(err, userErr) {
		t.Fatalf("ResolveUser() error = %v, want %v", err, userErr)
	}
}

func TestDefaultNodeTrafficReporterReportsInitialThenStepAndInterval(t *testing.T) {
	current := time.Unix(100, 0)
	reporter := &DefaultNodeTrafficReporter{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Runtime: servercfg.RuntimeConfig{
					NodeTrafficReportInterval: 1,
					NodeTrafficReportStep:     10 * 1024,
				},
			}
		},
		now: func() time.Time { return current },
	}
	client := &file.Client{Id: 1, FlowLimit: 1024, Flow: &file.Flow{}}

	client.Flow.Add(300, 100)
	report, ok := reporter.Observe(client, 300, 100)
	if !ok || report == nil || report.Trigger != "initial" || report.DeltaIn != 300 || report.DeltaOut != 100 {
		t.Fatalf("initial Observe() = %#v, %v", report, ok)
	}

	client.Flow.Add(200, 100)
	if report, ok = reporter.Observe(client, 200, 100); ok || report != nil {
		t.Fatalf("second Observe() = %#v, %v, want suppressed before thresholds", report, ok)
	}

	client.Flow.Add(6000, 4100)
	report, ok = reporter.Observe(client, 6000, 4100)
	if !ok || report == nil || report.Trigger != "step" || report.DeltaIn != 6200 || report.DeltaOut != 4200 {
		t.Fatalf("step Observe() = %#v, %v", report, ok)
	}

	current = current.Add(1100 * time.Millisecond)
	client.Flow.Add(1, 1)
	report, ok = reporter.Observe(client, 1, 1)
	if !ok || report == nil || report.Trigger != "interval" || report.DeltaIn != 1 || report.DeltaOut != 1 {
		t.Fatalf("interval Observe() = %#v, %v", report, ok)
	}
}

func TestDefaultNodeTrafficReporterCanBeDisabled(t *testing.T) {
	reporter := &DefaultNodeTrafficReporter{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Runtime: servercfg.RuntimeConfig{
					NodeTrafficReportInterval: 0,
					NodeTrafficReportStep:     0,
				},
			}
		},
	}
	client := &file.Client{Id: 1, FlowLimit: 1024, Flow: &file.Flow{InletFlow: 3, ExportFlow: 4}}
	if report, ok := reporter.Observe(client, 3, 4); ok || report != nil {
		t.Fatalf("Observe() = %#v, %v, want disabled reporter", report, ok)
	}
}

func TestDefaultNodeTrafficReporterSkipsClientWithoutFlowLimit(t *testing.T) {
	reporter := &DefaultNodeTrafficReporter{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Runtime: servercfg.RuntimeConfig{
					NodeTrafficReportInterval: 1,
				},
			}
		},
	}
	client := &file.Client{Id: 1, Flow: &file.Flow{}}
	if report, ok := reporter.Observe(client, 3, 4); ok || report != nil {
		t.Fatalf("Observe() = %#v, %v, want skipped for client without flow limit", report, ok)
	}
}

func TestDefaultNodeTrafficReporterDoesNotHoldReporterLockAcrossClientSnapshot(t *testing.T) {
	reporter := &DefaultNodeTrafficReporter{
		ConfigProvider: func() *servercfg.Snapshot {
			return &servercfg.Snapshot{
				Runtime: servercfg.RuntimeConfig{
					NodeTrafficReportInterval: 1,
				},
			}
		},
		now: func() time.Time { return time.Unix(100, 0) },
	}
	blockedClient := &file.Client{Id: 1, FlowLimit: 1024, Flow: &file.Flow{}}
	readyClient := &file.Client{Id: 2, FlowLimit: 1024, Flow: &file.Flow{}}

	blockedClient.Lock()
	firstDone := make(chan struct{})
	go func() {
		reporter.Observe(blockedClient, 3, 4)
		close(firstDone)
	}()

	time.Sleep(50 * time.Millisecond)

	secondDone := make(chan struct{})
	var (
		report *NodeTrafficThresholdReport
		ok     bool
	)
	go func() {
		report, ok = reporter.Observe(readyClient, 5, 6)
		close(secondDone)
	}()

	select {
	case <-secondDone:
	case <-time.After(300 * time.Millisecond):
		blockedClient.Unlock()
		<-firstDone
		t.Fatal("second Observe blocked behind reporter mutex while first call waited on client snapshot")
	}

	blockedClient.Unlock()

	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first Observe did not finish after releasing blocked client lock")
	}

	if !ok || report == nil {
		t.Fatalf("second Observe() = %#v, %v, want successful initial report", report, ok)
	}
	if report.Client == nil || report.Client.Id != readyClient.Id {
		t.Fatalf("second Observe report client = %#v, want client %d snapshot", report.Client, readyClient.Id)
	}
	if report.Trigger != "initial" || report.DeltaIn != 5 || report.DeltaOut != 6 {
		t.Fatalf("second Observe report = %#v, want initial report for ready client", report)
	}
}

func TestDefaultNodeTrafficReporterAllowsNilReceiver(t *testing.T) {
	var reporter *DefaultNodeTrafficReporter
	client := &file.Client{Id: 1, FlowLimit: 1024, Flow: &file.Flow{}}

	if report, ok := reporter.Observe(client, 3, 4); ok || report != nil {
		t.Fatalf("Observe() = %#v, %v, want nil/false for nil receiver", report, ok)
	}
}
