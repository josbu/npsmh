package routers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

func TestNodeCallbackWorkerRecoversOverflowByReplayingEventLog(t *testing.T) {
	app := webapi.New(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "callback-overflow-node"},
	})
	hub := newNodeEventHub(app.Services.Authz)
	log := newNodeEventLog(8, "")
	app.Hooks = compositeManagementHooks{
		primary: app.Hooks,
		hub:     hub,
		log:     log,
	}
	state := &State{
		App:          app,
		NodeEvents:   hub,
		NodeEventLog: log,
	}

	var mu sync.Mutex
	sequences := make([]int64, 0, 33)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll(callback body) error = %v", err)
		}
		_ = r.Body.Close()
		var envelope nodeCallbackEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("json.Unmarshal(callback body) error = %v body=%s", err, string(body))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		sequences = append(sequences, envelope.Event.Sequence)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackSrv.Close()

	worker := &nodeCallbackWorker{
		config: nodeCallbackWorkerConfig{
			PlatformID: "master-overflow",
			Platform: servercfg.ManagementPlatformConfig{
				PlatformID:       "master-overflow",
				Token:            "secret",
				CallbackURL:      callbackSrv.URL,
				CallbackRetryMax: 0,
			},
		},
		actor:  webapi.PlatformActor("master-overflow", "admin", "callback", "callback:master-overflow", true, nil, 7),
		client: &http.Client{},
	}
	worker.sub = state.NodeEvents.Subscribe(worker.actor)
	if worker.sub == nil {
		t.Fatal("Subscribe(callback worker) = nil")
	}
	defer worker.sub.Close()

	manager := &nodeCallbackManager{
		state: state,
		ctx:   context.Background(),
	}
	lastSequence := int64(0)

	for i := 1; i <= 33; i++ {
		event := state.NodeEventLog.Record(webapi.Event{
			Name:     "client.created",
			Resource: "client",
			Fields: map[string]interface{}{
				"id":            i,
				"owner_user_id": 7,
			},
		})
		state.NodeEvents.Publish(event)
	}

	select {
	case <-worker.sub.Done():
	default:
		t.Fatal("callback worker subscription did not overflow as expected")
	}

	if !manager.recoverWorkerSubscription(worker, &lastSequence) {
		t.Fatal("recoverWorkerSubscription() = false, want true")
	}
	if lastSequence != 33 {
		t.Fatalf("lastSequence = %d, want 33", lastSequence)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sequences) != 33 {
		t.Fatalf("delivered callback count = %d, want 33", len(sequences))
	}
	for i, sequence := range sequences {
		if want := int64(i + 1); sequence != want {
			t.Fatalf("callback sequence[%d] = %d, want %d", i, sequence, want)
		}
	}
}

func TestNodeCallbackProcessEventTracksSequence(t *testing.T) {
	manager := &nodeCallbackManager{}
	worker := &nodeCallbackWorker{
		config: nodeCallbackWorkerConfig{
			PlatformID: "master-callback-process",
		},
	}
	lastSequence := int64(0)

	if delivered := manager.processCallbackEvent(worker, webapi.Event{
		Name:     "webhook.delivery_succeeded",
		Resource: "webhook",
		Action:   "delivery",
		Sequence: 7,
	}, &lastSequence); delivered {
		t.Fatal("processCallbackEvent(filtered) = true, want false")
	}
	if lastSequence != 7 {
		t.Fatalf("lastSequence after filtered event = %d, want 7", lastSequence)
	}

	if delivered := manager.processCallbackEvent(worker, webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
		Sequence: 8,
	}, &lastSequence); !delivered {
		t.Fatal("processCallbackEvent(matching) = false, want true")
	}
	if lastSequence != 8 {
		t.Fatalf("lastSequence after matching event = %d, want 8", lastSequence)
	}
}

func TestQueryNodeCallbackQueueUsesReverseStatusWithoutQueueStore(t *testing.T) {
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"platform_callback_urls=https://example.test/callback",
		"platform_callback_enabled=true",
		"platform_callback_timeout_seconds=5",
		"platform_callback_retry_max=2",
		"platform_callback_retry_backoff_seconds=1",
		"platform_callback_queue_max=7",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	state := NewStateWithApp(webapi.New(nil))
	t.Cleanup(state.Close)
	state.NodeCallbackQueue = nil
	runtimeStatus := state.ManagementPlatformRuntimeStatus()
	runtimeStatus.NoteCallbackConfigured("master-a", "https://example.test/callback", true, 5, 2, 1, 7, 3)
	runtimeStatus.NoteCallbackQueued("master-a", 3, true)

	scope := webservice.ResolveNodeAccessScope(webservice.Principal{
		Authenticated: true,
		Kind:          "admin",
		IsAdmin:       true,
	})
	payload, err := queryNodeCallbackQueue(state, scope, "", 20)
	if err != nil {
		t.Fatalf("queryNodeCallbackQueue() error = %v", err)
	}
	if len(payload.Platforms) != 1 {
		t.Fatalf("len(payload.Platforms) = %d, want 1", len(payload.Platforms))
	}
	platform := payload.Platforms[0]
	if platform.PlatformID != "master-a" || platform.CallbackQueueMax != 7 || platform.CallbackQueueSize != 3 || platform.CallbackDropped != 1 {
		t.Fatalf("platform payload = %+v", platform)
	}
}

func TestMutateNodeCallbackQueueReturnsVisiblePlatformsWithoutQueueStore(t *testing.T) {
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"platform_callback_urls=https://example.test/callback",
		"platform_callback_enabled=true",
		"platform_callback_queue_max=7",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	state := NewStateWithApp(webapi.New(nil))
	t.Cleanup(state.Close)
	state.NodeCallbackQueue = nil
	scope := webservice.ResolveNodeAccessScope(webservice.Principal{
		Authenticated: true,
		Kind:          "admin",
		IsAdmin:       true,
	})

	payload, err := mutateNodeCallbackQueue(state, scope, nodeCallbackQueueMutationRequest{Action: "clear"})
	if err != nil {
		t.Fatalf("mutateNodeCallbackQueue() error = %v", err)
	}
	if len(payload.Platforms) != 1 {
		t.Fatalf("len(payload.Platforms) = %d, want 1", len(payload.Platforms))
	}
	platform := payload.Platforms[0]
	if platform.PlatformID != "master-a" || platform.CallbackQueueMax != 7 || platform.CallbackQueueSize != 0 || platform.Cleared != 0 {
		t.Fatalf("mutation payload = %+v", platform)
	}
}

func TestNodeCallbackQueueHelpersRejectNilState(t *testing.T) {
	scope := webservice.ResolveNodeAccessScope(webservice.Principal{
		Authenticated: true,
		Kind:          "admin",
		IsAdmin:       true,
	})
	if _, err := queryNodeCallbackQueue(nil, scope, "", 20); !errors.Is(err, errNodeCallbackQueueStateUnavailable) {
		t.Fatalf("queryNodeCallbackQueue(nil) error = %v, want %v", err, errNodeCallbackQueueStateUnavailable)
	}
	if _, err := mutateNodeCallbackQueue(nil, scope, nodeCallbackQueueMutationRequest{Action: "clear"}); !errors.Is(err, errNodeCallbackQueueStateUnavailable) {
		t.Fatalf("mutateNodeCallbackQueue(nil) error = %v, want %v", err, errNodeCallbackQueueStateUnavailable)
	}
}

func TestStampNodeCallbackQueueResponseDataTimestamp(t *testing.T) {
	query := nodeCallbackQueuePayload{Timestamp: 1}
	stampedQuery, ok := stampNodeCallbackQueueResponseDataTimestamp(query, 55).(nodeCallbackQueuePayload)
	if !ok {
		t.Fatalf("stamped query type = %T, want nodeCallbackQueuePayload", stampNodeCallbackQueueResponseDataTimestamp(query, 55))
	}
	if stampedQuery.Timestamp != 55 {
		t.Fatalf("stamped query timestamp = %d, want 55", stampedQuery.Timestamp)
	}

	mutation := nodeCallbackQueueMutationPayload{Timestamp: 2}
	stampedMutation, ok := stampNodeCallbackQueueResponseDataTimestamp(mutation, 66).(nodeCallbackQueueMutationPayload)
	if !ok {
		t.Fatalf("stamped mutation type = %T, want nodeCallbackQueueMutationPayload", stampNodeCallbackQueueResponseDataTimestamp(mutation, 66))
	}
	if stampedMutation.Timestamp != 66 {
		t.Fatalf("stamped mutation timestamp = %d, want 66", stampedMutation.Timestamp)
	}
}

type reentrantNodeCallbackRuntimeStatusStore struct {
	webservice.ManagementPlatformRuntimeStatusStore
	onQueueSize func()
}

type queueLockProbeValue struct {
	onMarshal func()
}

func (v queueLockProbeValue) MarshalJSON() ([]byte, error) {
	if v.onMarshal != nil {
		v.onMarshal()
	}
	return []byte(`"probe"`), nil
}

func (s *reentrantNodeCallbackRuntimeStatusStore) NoteCallbackQueueSize(platformID string, queueSize int) {
	if s.onQueueSize != nil {
		s.onQueueSize()
	}
	s.ManagementPlatformRuntimeStatusStore.NoteCallbackQueueSize(platformID, queueSize)
}

func TestNodeCallbackQueueStoreStatusUpdatesDoNotHoldQueueLock(t *testing.T) {
	store := newNodeCallbackQueueStore(
		"",
		[]nodeCallbackWorkerConfig{{PlatformID: "platform-a", CallbackQueueMax: 4}},
		nil,
		nil,
		nil,
	)
	store.runtimeStatus = &reentrantNodeCallbackRuntimeStatusStore{
		ManagementPlatformRuntimeStatusStore: webservice.NewInMemoryManagementPlatformRuntimeStatusStore(),
	}

	if queueSize, dropped := store.Enqueue("platform-a", nodeCallbackEnvelope{
		PlatformID: "platform-a",
		Type:       "event",
	}); queueSize != 1 || dropped {
		t.Fatalf("Enqueue() = (%d, %v), want (1, false)", queueSize, dropped)
	}
	item, ok := store.Peek("platform-a")
	if !ok {
		t.Fatal("Peek() = false, want true")
	}

	store.runtimeStatus.(*reentrantNodeCallbackRuntimeStatusStore).onQueueSize = func() {
		done := make(chan struct{})
		go func() {
			_ = store.Size("platform-a")
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("callback queue lock was held while runtime status was updated")
		}
	}

	if queueSize := store.Remove("platform-a", item.ID); queueSize != 0 {
		t.Fatalf("Remove() = %d, want 0", queueSize)
	}
}

func TestNodeCallbackQueueStorePersistenceDoesNotHoldQueueLock(t *testing.T) {
	store := newNodeCallbackQueueStore(
		filepath.Join(t.TempDir(), "callbacks.json"),
		[]nodeCallbackWorkerConfig{{PlatformID: "platform-a", CallbackQueueMax: 4}},
		nil,
		nil,
		nil,
	)
	t.Cleanup(store.Close)

	probeReached := make(chan struct{}, 1)
	if queueSize, dropped := store.Enqueue("platform-a", nodeCallbackEnvelope{
		PlatformID: "platform-a",
		Type:       "event",
		Event: webapi.Event{
			Name: "client.created",
			Fields: map[string]interface{}{
				"probe": queueLockProbeValue{
					onMarshal: func() {
						select {
						case probeReached <- struct{}{}:
						default:
						}
						done := make(chan struct{})
						go func() {
							_ = store.Size("platform-a")
							close(done)
						}()
						select {
						case <-done:
						case <-time.After(2 * time.Second):
							t.Fatal("callback queue lock was held while persistence marshaled queued payload")
						}
					},
				},
			},
		},
	}); queueSize != 1 || dropped {
		t.Fatalf("Enqueue() = (%d, %v), want (1, false)", queueSize, dropped)
	}
	select {
	case <-probeReached:
	case <-time.After(2 * time.Second):
		t.Fatal("queued payload was not persisted through marshal path")
	}
}

func TestTrimNodeCallbackQueueItemsInPlaceClearsDiscardedTail(t *testing.T) {
	queue := []nodeCallbackQueuedItem{
		{ID: 1, Payload: nodeCallbackEnvelope{Event: webapi.Event{Name: "one", Fields: map[string]interface{}{"id": 1}}}},
		{ID: 2, Payload: nodeCallbackEnvelope{Event: webapi.Event{Name: "two", Fields: map[string]interface{}{"id": 2}}}},
		{ID: 3, Payload: nodeCallbackEnvelope{Event: webapi.Event{Name: "three", Fields: map[string]interface{}{"id": 3}}}},
	}

	trimmed, dropped := trimNodeCallbackQueueItemsInPlace(queue, 2)
	if !dropped {
		t.Fatal("trimNodeCallbackQueueItemsInPlace() dropped = false, want true")
	}
	if len(trimmed) != 2 {
		t.Fatalf("len(trimmed) = %d, want 2", len(trimmed))
	}
	if trimmed[0].ID != 2 || trimmed[1].ID != 3 {
		t.Fatalf("trimmed ids = [%d %d], want [2 3]", trimmed[0].ID, trimmed[1].ID)
	}
	if queue[2].ID != 0 || queue[2].Payload.Event.Fields != nil || queue[2].Payload.Event.Name != "" {
		t.Fatal("discarded callback queue tail was not cleared")
	}
}

func TestNodeCallbackRecoverWorkerSkipsBufferedEventsAfterCancel(t *testing.T) {
	requestCh := make(chan struct{}, 1)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestCh <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackSrv.Close()

	hub := newNodeEventHub(nil)
	manager := newNodeCallbackManager(&State{NodeEvents: hub})
	worker := &nodeCallbackWorker{
		config: nodeCallbackWorkerConfig{
			PlatformID: "platform-a",
			Platform: servercfg.ManagementPlatformConfig{
				PlatformID:  "platform-a",
				CallbackURL: callbackSrv.URL,
			},
		},
		actor:  webapi.AdminActorWithFallback("admin", "admin"),
		client: &http.Client{Timeout: time.Second},
	}
	worker.sub = hub.Subscribe(worker.actor)
	if worker.sub == nil {
		t.Fatal("Subscribe() = nil")
	}

	hub.Publish(webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
		Sequence: 1,
	})

	manager.cancel()
	if recovered := manager.recoverWorkerSubscription(worker, new(int64)); recovered {
		t.Fatal("recoverWorkerSubscription() = true, want false after cancel")
	}
	select {
	case <-requestCh:
		t.Fatal("canceled callback worker should not deliver buffered event during recover")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNodeCallbackCanceledRequestSkipsQueueMutation(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	manager := newNodeCallbackManager(state)
	manager.queue = newNodeCallbackQueueStore(
		"",
		[]nodeCallbackWorkerConfig{{PlatformID: "platform-a", CallbackQueueMax: 4}},
		webservice.NewInMemoryManagementPlatformRuntimeStatusStore(),
		nil,
		nil,
	)
	transport := &cancelAwareBlockingTransport{enter: make(chan struct{}, 1)}
	worker := &nodeCallbackWorker{
		config: nodeCallbackWorkerConfig{
			PlatformID: "platform-a",
			Platform: servercfg.ManagementPlatformConfig{
				PlatformID:  "platform-a",
				CallbackURL: "https://example.invalid/callback",
				Token:       "secret",
			},
		},
		actor:  webapi.AdminActorWithFallback("admin", "admin"),
		client: &http.Client{Transport: transport},
	}

	done := make(chan struct{})
	go func() {
		manager.deliverCallbackEvent(worker, webapi.Event{
			Name:     "client.created",
			Resource: "client",
			Action:   "create",
		})
		close(done)
	}()

	select {
	case <-transport.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("deliverCallbackEvent() did not start request")
	}
	manager.cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliverCallbackEvent() did not exit after cancel")
	}
	if got := manager.queue.Size("platform-a"); got != 0 {
		t.Fatalf("callback queue size after canceled request = %d, want 0", got)
	}
	status := state.ManagementPlatformRuntimeStatus().Status("platform-a")
	if status.CallbackFailures != 0 || status.LastCallbackError != "" || status.LastCallbackErrorAt != 0 {
		t.Fatalf("canceled callback request should not record runtime failure, got %+v", status)
	}
}

func TestNodeCallbackTimeoutEnqueuesRetry(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	manager := newNodeCallbackManager(state)
	manager.queue = newNodeCallbackQueueStore(
		"",
		[]nodeCallbackWorkerConfig{{PlatformID: "platform-a", CallbackQueueMax: 4}},
		webservice.NewInMemoryManagementPlatformRuntimeStatusStore(),
		nil,
		nil,
	)
	worker := &nodeCallbackWorker{
		config: nodeCallbackWorkerConfig{
			PlatformID: "platform-a",
			Platform: servercfg.ManagementPlatformConfig{
				PlatformID:  "platform-a",
				CallbackURL: "https://example.invalid/callback",
				Token:       "secret",
			},
		},
		actor:  webapi.AdminActorWithFallback("admin", "admin"),
		client: &http.Client{Transport: &fixedErrorTransport{err: context.DeadlineExceeded}},
	}

	manager.deliverCallbackEvent(worker, webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
	})

	if got := manager.queue.Size("platform-a"); got != 1 {
		t.Fatalf("callback queue size after timeout request = %d, want 1", got)
	}
}
