package routers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeWebhookStoreMutationsDoNotPanicAfterClose(t *testing.T) {
	sinkStore := &nodeWebhookStore{
		items: map[int64]nodeWebhookConfig{
			1: {
				ID:    1,
				Owner: webapi.AdminActorWithFallback("admin", "admin"),
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
				},
			},
		},
		runtime:  make(map[int64]nodeWebhookRuntimeState),
		changeCh: make(chan struct{}, 1),
	}
	actor := webapi.AdminActorWithFallback("admin", "admin")
	mustNotPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("%s panicked after Close(): %v", name, recovered)
			}
		}()
		fn()
	}

	sinkStore.Close()
	mustNotPanic("SetEnabled", func() {
		if _, err := sinkStore.SetEnabled(context.Background(), actor, webapi.RequestMetadata{}, 1, false, "shutdown"); err != nil {
			t.Fatalf("SetEnabled() error = %v", err)
		}
	})
	mustNotPanic("Delete", func() {
		if ok := sinkStore.Delete(context.Background(), actor, webapi.RequestMetadata{}, 1); !ok {
			t.Fatal("Delete() = false, want true")
		}
	})
}

func TestNodeWebhookSetEnabledNoopSkipsWriteAndEvent(t *testing.T) {
	actor := webapi.AdminActorWithFallback("admin", "admin")
	emitted := make([]webapi.Event, 0, 1)
	store := newNodeWebhookStore(
		"",
		nil,
		func(ctx context.Context, event webapi.Event) {
			emitted = append(emitted, event)
		},
		nil,
	)
	store.items[1] = nodeWebhookConfig{
		ID:             1,
		URL:            "https://example.invalid/hook",
		Method:         http.MethodPost,
		TimeoutSeconds: nodeWebhookDefaultTimeoutSeconds,
		Owner:          actor,
		CreatedAt:      5,
		UpdatedAt:      7,
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled:     false,
			ContentMode: nodeEventSinkContentCanonical,
			ContentType: "application/json",
		},
	}
	store.runtime[1] = nodeWebhookRuntimeState{
		LastDisabledReason: "manual_status",
		LastDisabledAt:     6,
	}

	payload, err := store.SetEnabled(context.Background(), actor, webapi.RequestMetadata{}, 1, false, "manual_status")
	if err != nil {
		t.Fatalf("SetEnabled(noop) error = %v", err)
	}
	if payload.UpdatedAt != 7 {
		t.Fatalf("payload.UpdatedAt = %d, want 7", payload.UpdatedAt)
	}
	if payload.LastDisabledReason != "manual_status" || payload.LastDisabledAt != 6 {
		t.Fatalf("payload disable runtime = (%q, %d), want (%q, %d)", payload.LastDisabledReason, payload.LastDisabledAt, "manual_status", 6)
	}
	if len(emitted) != 0 {
		t.Fatalf("len(emitted) = %d, want 0", len(emitted))
	}
	select {
	case <-store.Changed():
		t.Fatal("SetEnabled(noop) should not notify change listeners")
	default:
	}
	if got := store.items[1].UpdatedAt; got != 7 {
		t.Fatalf("store.items[1].UpdatedAt = %d, want 7", got)
	}
}

func TestNodeWebhookUpdateTracksEnabledTransitionRuntimeState(t *testing.T) {
	actor := webapi.AdminActorWithFallback("admin", "admin")
	emitted := make([]webapi.Event, 0, 2)
	store := newNodeWebhookStore(
		"",
		nil,
		func(ctx context.Context, event webapi.Event) {
			emitted = append(emitted, event)
		},
		nil,
	)
	store.items[1] = nodeWebhookConfig{
		ID:             1,
		URL:            "https://example.invalid/hook",
		Method:         http.MethodPost,
		TimeoutSeconds: nodeWebhookDefaultTimeoutSeconds,
		Owner:          actor,
		CreatedAt:      5,
		UpdatedAt:      7,
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled:     true,
			ContentMode: nodeEventSinkContentCanonical,
			ContentType: "application/json",
		},
	}

	disabled := false
	payload, err := store.Update(context.Background(), actor, webapi.RequestMetadata{}, 1, nodeWebhookWriteRequest{
		URL:     "https://example.invalid/hook",
		Method:  http.MethodPost,
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("Update(disable) error = %v", err)
	}
	if payload.Enabled {
		t.Fatalf("payload.Enabled = %v, want false", payload.Enabled)
	}
	if payload.LastDisabledReason != nodeWebhookDisableConfigUpdate {
		t.Fatalf("payload.LastDisabledReason = %q, want %q", payload.LastDisabledReason, nodeWebhookDisableConfigUpdate)
	}
	if payload.LastDisabledAt <= 0 {
		t.Fatalf("payload.LastDisabledAt = %d, want > 0", payload.LastDisabledAt)
	}

	enabled := true
	payload, err = store.Update(context.Background(), actor, webapi.RequestMetadata{}, 1, nodeWebhookWriteRequest{
		URL:     "https://example.invalid/hook",
		Method:  http.MethodPost,
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("Update(enable) error = %v", err)
	}
	if !payload.Enabled {
		t.Fatalf("payload.Enabled = %v, want true", payload.Enabled)
	}
	if payload.LastDisabledReason != "" || payload.LastDisabledAt != 0 {
		t.Fatalf("payload disable runtime after enable = (%q, %d), want empty/0", payload.LastDisabledReason, payload.LastDisabledAt)
	}
	if len(emitted) != 2 {
		t.Fatalf("len(emitted) = %d, want 2", len(emitted))
	}
	if emitted[0].Name != "webhook.updated" || emitted[1].Name != "webhook.updated" {
		t.Fatalf("emitted names = [%q %q], want webhook.updated twice", emitted[0].Name, emitted[1].Name)
	}
}

func TestNodeWebhookRuntimeMutationsNormalizeFailureState(t *testing.T) {
	actor := webapi.AdminActorWithFallback("admin", "admin")
	emitted := make([]webapi.Event, 0, 2)
	store := newNodeWebhookStore(
		"",
		nil,
		func(ctx context.Context, event webapi.Event) {
			emitted = append(emitted, event)
		},
		nil,
	)
	store.items[1] = nodeWebhookConfig{
		ID:             1,
		URL:            "https://example.invalid/hook",
		Method:         http.MethodPost,
		TimeoutSeconds: nodeWebhookDefaultTimeoutSeconds,
		Owner:          actor,
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled:     true,
			ContentMode: nodeEventSinkContentCanonical,
			ContentType: "application/json",
		},
	}
	store.runtime[1] = nodeWebhookRuntimeState{
		LastError:           "previous error",
		LastErrorAt:         9,
		LastStatusCode:      500,
		Failures:            2,
		ConsecutiveFailures: 2,
	}

	store.NoteFailed(context.Background(), 1, nil, http.StatusBadGateway)
	runtime := store.runtime[1]
	if runtime.LastStatusCode != http.StatusBadGateway {
		t.Fatalf("runtime.LastStatusCode = %d, want %d", runtime.LastStatusCode, http.StatusBadGateway)
	}
	if runtime.LastError != "" || runtime.LastErrorAt != 0 {
		t.Fatalf("runtime failure state after status-only failure = (%q, %d), want empty/0", runtime.LastError, runtime.LastErrorAt)
	}
	if runtime.Failures != 3 || runtime.ConsecutiveFailures != 3 {
		t.Fatalf("runtime counters after failure = (%d, %d), want (3, 3)", runtime.Failures, runtime.ConsecutiveFailures)
	}

	store.NoteDelivered(context.Background(), 1, http.StatusNoContent)
	runtime = store.runtime[1]
	if runtime.LastStatusCode != http.StatusNoContent {
		t.Fatalf("runtime.LastStatusCode after success = %d, want %d", runtime.LastStatusCode, http.StatusNoContent)
	}
	if runtime.LastError != "" || runtime.LastErrorAt != 0 {
		t.Fatalf("runtime failure state after success = (%q, %d), want empty/0", runtime.LastError, runtime.LastErrorAt)
	}
	if runtime.Deliveries != 1 || runtime.ConsecutiveFailures != 0 {
		t.Fatalf("runtime counters after success = (%d deliveries, %d consecutive_failures), want (1, 0)", runtime.Deliveries, runtime.ConsecutiveFailures)
	}
	if len(emitted) != 2 || emitted[0].Name != "webhook.delivery_failed" || emitted[1].Name != "webhook.delivery_succeeded" {
		t.Fatalf("emitted events = %+v, want failed then succeeded", emitted)
	}
}

func TestPrepareNodeEventSinkConfigCompilesCustomTemplates(t *testing.T) {
	config, err := prepareNodeEventSinkConfig(nodeEventSinkConfig{
		Enabled:      true,
		ContentMode:  nodeEventSinkContentCustom,
		ContentType:  "application/json",
		BodyTemplate: `{"event":{{ quote .Event.Name }}}`,
		HeaderTemplates: map[string]string{
			"X-Event-Action": "{{ upper .Event.Action }}",
		},
	})
	if err != nil {
		t.Fatalf("prepareNodeEventSinkConfig() error = %v", err)
	}
	if config.compiledBody == nil {
		t.Fatal("compiledBody = nil, want compiled template")
	}
	if config.compiledHeaders["X-Event-Action"] == nil {
		t.Fatal("compiledHeaders[X-Event-Action] = nil, want compiled template")
	}

	preparedAgain, err := prepareNodeEventSinkConfig(config)
	if err != nil {
		t.Fatalf("prepareNodeEventSinkConfig(compiled) error = %v", err)
	}
	if preparedAgain.compiledBody != config.compiledBody {
		t.Fatal("compiledBody pointer changed, want compiled template reuse")
	}
	if preparedAgain.compiledHeaders["X-Event-Action"] != config.compiledHeaders["X-Event-Action"] {
		t.Fatal("compiled header pointer changed, want compiled template reuse")
	}

	payload, err := renderNodeEventSinkPayload(nil, "sink-1", "webhook", preparedAgain, webapi.Event{
		Name:     "client.created",
		Action:   "create",
		Resource: "client",
	})
	if err != nil {
		t.Fatalf("renderNodeEventSinkPayload() error = %v", err)
	}
	if got := payload.Headers["X-Event-Action"]; got != "CREATE" {
		t.Fatalf("payload.Headers[X-Event-Action] = %q, want CREATE", got)
	}
	if got := string(payload.Body); got != `{"event":"client.created"}` {
		t.Fatalf("payload.Body = %s, want rendered JSON body", got)
	}
	if payload.BodyMode != wsResponseBodyModeUnknown {
		t.Fatalf("payload.BodyMode = %v, want unknown fallback mode for custom json", payload.BodyMode)
	}

	canonicalPayload, err := renderNodeEventSinkPayload(nil, "sink-2", "webhook", nodeEventSinkConfig{
		Enabled: true,
	}, webapi.Event{
		Name:     "client.created",
		Action:   "create",
		Resource: "client",
	})
	if err != nil {
		t.Fatalf("renderNodeEventSinkPayload(canonical) error = %v", err)
	}
	if canonicalPayload.BodyMode != wsResponseBodyModeJSON {
		t.Fatalf("canonical payload.BodyMode = %v, want trusted json mode", canonicalPayload.BodyMode)
	}
}

func TestNodeWebhookProcessEventTracksSequenceAndSelector(t *testing.T) {
	manager := &nodeWebhookDispatcherManager{}
	worker := &nodeWebhookWorker{
		config: nodeWebhookConfig{
			ID: 9,
			nodeEventSinkConfig: nodeEventSinkConfig{
				Selector: nodeEventSelector{
					EventNames: []string{"client.created"},
					Resources:  []string{"client"},
				},
			},
		},
	}
	lastSequence := int64(0)

	if delivered := manager.processWebhookEvent(worker, webapi.Event{
		Name:     "client.updated",
		Resource: "client",
		Action:   "update",
		Sequence: 7,
	}, &lastSequence, true); delivered {
		t.Fatal("processWebhookEvent(filtered) = true, want false")
	}
	if lastSequence != 7 {
		t.Fatalf("lastSequence after filtered event = %d, want 7", lastSequence)
	}

	if delivered := manager.processWebhookEvent(worker, webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
		Sequence: 8,
	}, &lastSequence, true); !delivered {
		t.Fatal("processWebhookEvent(matching) = false, want true")
	}
	if lastSequence != 8 {
		t.Fatalf("lastSequence after matching event = %d, want 8", lastSequence)
	}
}

func TestNodeWebhookDeliverSkipsStaleWorkerConfig(t *testing.T) {
	actor := webapi.AdminActorWithFallback("admin", "admin")
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	baseConfig := nodeWebhookConfig{
		ID:             9,
		URL:            server.URL + "/hook",
		Method:         http.MethodPost,
		TimeoutSeconds: nodeWebhookDefaultTimeoutSeconds,
		Owner:          actor,
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled:     true,
			ContentMode: nodeEventSinkContentCanonical,
			ContentType: "application/json",
		},
	}
	manager := &nodeWebhookDispatcherManager{
		ctx:   context.Background(),
		store: newNodeWebhookStore("", nil, nil, nil),
	}
	worker := &nodeWebhookWorker{
		config: baseConfig,
		ctx:    context.Background(),
		client: server.Client(),
	}
	event := webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
	}

	t.Run("config_changed", func(t *testing.T) {
		current := baseConfig
		current.URL = server.URL + "/new-hook"
		manager.store.items[current.ID] = current

		manager.deliverWebhookEvent(worker, event)

		if got := atomic.LoadInt32(&hits); got != 0 {
			t.Fatalf("webhook deliveries after config change = %d, want 0", got)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		current := baseConfig
		current.Enabled = false
		manager.store.items[current.ID] = current

		manager.deliverWebhookEvent(worker, event)

		if got := atomic.LoadInt32(&hits); got != 0 {
			t.Fatalf("webhook deliveries after disable = %d, want 0", got)
		}
	})

	t.Run("deleted", func(t *testing.T) {
		delete(manager.store.items, baseConfig.ID)

		manager.deliverWebhookEvent(worker, event)

		if got := atomic.LoadInt32(&hits); got != 0 {
			t.Fatalf("webhook deliveries after delete = %d, want 0", got)
		}
	})
}

type blockingWebhookIdleCloseTransport struct {
	enter   chan struct{}
	release <-chan struct{}
}

func (t *blockingWebhookIdleCloseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected round trip")
}

func (t *blockingWebhookIdleCloseTransport) CloseIdleConnections() {
	if t == nil {
		return
	}
	select {
	case t.enter <- struct{}{}:
	default:
	}
	if t.release != nil {
		<-t.release
	}
}

type cancelAwareBlockingTransport struct {
	enter chan struct{}
}

func (t *cancelAwareBlockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t != nil && t.enter != nil {
		select {
		case t.enter <- struct{}{}:
		default:
		}
	}
	<-req.Context().Done()
	return nil, req.Context().Err()
}

type fixedErrorTransport struct {
	err error
}

func (t *fixedErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if t == nil {
		return nil, nil
	}
	return nil, t.err
}

func TestNodeWebhookSyncWorkersStopsOutsideManagerLock(t *testing.T) {
	release := make(chan struct{})
	transport := &blockingWebhookIdleCloseTransport{
		enter:   make(chan struct{}, 1),
		release: release,
	}
	manager := &nodeWebhookDispatcherManager{
		state: &State{NodeEvents: newNodeEventHub(nil)},
		store: &nodeWebhookStore{
			items:   make(map[int64]nodeWebhookConfig),
			runtime: make(map[int64]nodeWebhookRuntimeState),
		},
		ctx:     context.Background(),
		workers: map[int64]*nodeWebhookWorker{1: {client: &http.Client{Transport: transport}}},
	}
	done := make(chan struct{})
	go func() {
		manager.syncWorkers()
		close(done)
	}()

	select {
	case <-transport.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("syncWorkers() did not reach worker stop")
	}

	locked := make(chan struct{})
	go func() {
		manager.mu.Lock()
		manager.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("syncWorkers() held manager lock while stopping worker")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("syncWorkers() did not finish after releasing worker stop")
	}
}

func TestNodeWebhookStopAllStopsOutsideManagerLock(t *testing.T) {
	release := make(chan struct{})
	transport := &blockingWebhookIdleCloseTransport{
		enter:   make(chan struct{}, 1),
		release: release,
	}
	manager := &nodeWebhookDispatcherManager{
		workers: map[int64]*nodeWebhookWorker{1: {client: &http.Client{Transport: transport}}},
	}
	done := make(chan struct{})
	go func() {
		manager.stopAll()
		close(done)
	}()

	select {
	case <-transport.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("stopAll() did not reach worker stop")
	}

	locked := make(chan struct{})
	go func() {
		manager.mu.Lock()
		manager.mu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("stopAll() held manager lock while stopping worker")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopAll() did not finish after releasing worker stop")
	}
	if len(manager.workers) != 0 {
		t.Fatalf("len(manager.workers) = %d, want 0", len(manager.workers))
	}
}

func TestNodeWebhookRecoverWorkerSkipsBufferedEventsAfterCancel(t *testing.T) {
	requestCh := make(chan struct{}, 1)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestCh <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookSrv.Close()

	hub := newNodeEventHub(nil)
	store := newNodeWebhookStore("", nil, nil, nil)
	manager := newNodeWebhookDispatcherManager(&State{NodeEvents: hub})
	manager.store = store

	workerCtx, workerCancel := context.WithCancel(manager.ctx)
	worker := &nodeWebhookWorker{
		config: nodeWebhookConfig{
			ID:             1,
			URL:            webhookSrv.URL,
			Method:         http.MethodPost,
			TimeoutSeconds: 1,
			Owner:          webapi.AdminActorWithFallback("admin", "admin"),
			nodeEventSinkConfig: nodeEventSinkConfig{
				Enabled: true,
			},
		},
		ctx:    workerCtx,
		cancel: workerCancel,
		client: &http.Client{Timeout: time.Second},
	}
	worker.sub = hub.SubscribeWithOptions(worker.config.Owner, worker.matchesEvent, 8)
	if worker.sub == nil {
		t.Fatal("SubscribeWithOptions() = nil")
	}

	hub.Publish(webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
		Sequence: 1,
	})

	workerCancel()
	if recovered := manager.recoverWebhookWorker(worker, new(int64)); recovered {
		t.Fatal("recoverWebhookWorker() = true, want false after cancel")
	}
	select {
	case <-requestCh:
		t.Fatal("canceled webhook worker should not deliver buffered event during recover")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNodeWebhookCanceledRequestSkipsFailureMutation(t *testing.T) {
	transport := &cancelAwareBlockingTransport{enter: make(chan struct{}, 1)}
	emitted := make([]webapi.Event, 0, 1)
	store := newNodeWebhookStore(
		"",
		nil,
		func(ctx context.Context, event webapi.Event) {
			emitted = append(emitted, event)
		},
		nil,
	)
	store.items[1] = nodeWebhookConfig{
		ID:             1,
		URL:            "https://example.invalid/hook",
		Method:         http.MethodPost,
		TimeoutSeconds: nodeWebhookDefaultTimeoutSeconds,
		Owner:          webapi.AdminActorWithFallback("admin", "admin"),
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled:     true,
			ContentMode: nodeEventSinkContentCanonical,
			ContentType: "application/json",
		},
	}
	store.runtime[1] = nodeWebhookRuntimeState{}

	manager := newNodeWebhookDispatcherManager(nil)
	manager.store = store
	workerCtx, workerCancel := context.WithCancel(manager.ctx)
	worker := &nodeWebhookWorker{
		config: store.items[1],
		ctx:    workerCtx,
		cancel: workerCancel,
		client: &http.Client{Transport: transport},
	}

	done := make(chan struct{})
	go func() {
		manager.deliverWebhookEvent(worker, webapi.Event{
			Name:     "client.created",
			Resource: "client",
			Action:   "create",
		})
		close(done)
	}()

	select {
	case <-transport.enter:
	case <-time.After(2 * time.Second):
		t.Fatal("deliverWebhookEvent() did not start request")
	}
	workerCancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliverWebhookEvent() did not exit after cancel")
	}

	runtime := store.runtime[1]
	if runtime.Failures != 0 || runtime.ConsecutiveFailures != 0 || runtime.LastError != "" || runtime.LastErrorAt != 0 {
		t.Fatalf("runtime after canceled webhook request = %+v, want zero failure state", runtime)
	}
	if len(emitted) != 0 {
		t.Fatalf("len(emitted) = %d, want 0", len(emitted))
	}
}

func TestNodeWebhookTimeoutCountsAsFailure(t *testing.T) {
	emitted := make([]webapi.Event, 0, 1)
	store := newNodeWebhookStore(
		"",
		nil,
		func(ctx context.Context, event webapi.Event) {
			emitted = append(emitted, event)
		},
		nil,
	)
	store.items[1] = nodeWebhookConfig{
		ID:             1,
		URL:            "https://example.invalid/hook",
		Method:         http.MethodPost,
		TimeoutSeconds: nodeWebhookDefaultTimeoutSeconds,
		Owner:          webapi.AdminActorWithFallback("admin", "admin"),
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled:     true,
			ContentMode: nodeEventSinkContentCanonical,
			ContentType: "application/json",
		},
	}

	manager := newNodeWebhookDispatcherManager(nil)
	manager.store = store
	workerCtx, workerCancel := context.WithCancel(manager.ctx)
	defer workerCancel()
	worker := &nodeWebhookWorker{
		config: store.items[1],
		ctx:    workerCtx,
		cancel: workerCancel,
		client: &http.Client{Transport: &fixedErrorTransport{err: context.DeadlineExceeded}},
	}

	manager.deliverWebhookEvent(worker, webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Action:   "create",
	})

	runtime := store.runtime[1]
	if runtime.Failures != 1 || runtime.ConsecutiveFailures != 1 {
		t.Fatalf("runtime failure counters = (%d, %d), want (1, 1)", runtime.Failures, runtime.ConsecutiveFailures)
	}
	if runtime.LastError == "" {
		t.Fatal("runtime.LastError = empty, want timeout failure recorded")
	}
	if len(emitted) != 1 || emitted[0].Name != "webhook.delivery_failed" {
		t.Fatalf("emitted events = %+v, want one webhook.delivery_failed event", emitted)
	}
}

func TestNodeWebhookDeliversCustomPayloadAndHeaders(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-hook",
		"platform_tokens=hook-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_hook",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	type webhookRequest struct {
		Header http.Header
		Body   []byte
	}
	requestCh := make(chan webhookRequest, 4)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		requestCh <- webhookRequest{Header: r.Header.Clone(), Body: body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookSrv.Close()

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	createWebhookBody, _ := json.Marshal(map[string]interface{}{
		"name":          "client-created-webhook",
		"url":           webhookSrv.URL,
		"event_names":   []string{"client.created"},
		"resources":     []string{"client"},
		"content_mode":  "custom",
		"content_type":  "application/json",
		"body_template": `{"event":{{ quote .Event.Name }},"resource":{{ quote .Event.Resource }},"client_id":{{ .IDs.ClientID }},"action":{{ quote .Event.Action }}}`,
		"header_templates": map[string]string{
			"X-Event-Action": "{{ .Event.Action }}",
			"X-Sink-Name":    "{{ .Sink.Name }}",
		},
	})
	createWebhookReq := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(createWebhookBody))
	createWebhookReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createWebhookReq, "hook-secret")
	createWebhookResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createWebhookResp, createWebhookReq)
	if createWebhookResp.Code != http.StatusOK {
		t.Fatalf("POST /api/webhooks status = %d body=%s", createWebhookResp.Code, createWebhookResp.Body.String())
	}

	var createWebhookPayload nodeWebhookMutationPayload
	decodeManagementData(t, createWebhookResp.Body.Bytes(), &createWebhookPayload)
	if createWebhookPayload.Item == nil || createWebhookPayload.Item.ID <= 0 {
		t.Fatalf("create webhook payload = %+v, want item id", createWebhookPayload)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(runtime.State.NodeWebhooks.Active()) != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	createClientReq := httptest.NewRequest(
		http.MethodPost,
		"/api/clients",
		strings.NewReader(fmt.Sprintf(`{"verify_key":"hook-client-%d","remark":"webhook event"}`, time.Now().UnixNano())),
	)
	createClientReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createClientReq, "hook-secret")
	createClientResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	select {
	case received := <-requestCh:
		if got := received.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("webhook Content-Type = %q, want application/json", got)
		}
		if got := received.Header.Get("X-Event-Action"); got != "create" {
			t.Fatalf("webhook X-Event-Action = %q, want create", got)
		}
		if got := received.Header.Get("X-Sink-Name"); got != "client-created-webhook" {
			t.Fatalf("webhook X-Sink-Name = %q, want client-created-webhook", got)
		}
		if got := received.Header.Get("X-Webhook-ID"); got != strconv.FormatInt(createWebhookPayload.Item.ID, 10) {
			t.Fatalf("webhook X-Webhook-ID = %q, want %d", got, createWebhookPayload.Item.ID)
		}
		var payload struct {
			Event    string `json:"event"`
			Resource string `json:"resource"`
			ClientID int    `json:"client_id"`
			Action   string `json:"action"`
		}
		if err := json.Unmarshal(received.Body, &payload); err != nil {
			t.Fatalf("json.Unmarshal(webhook body) error = %v body=%s", err, string(received.Body))
		}
		if payload.Event != "client.created" || payload.Resource != "client" || payload.Action != "create" || payload.ClientID <= 0 {
			t.Fatalf("unexpected webhook payload = %+v body=%s", payload, string(received.Body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}
}

func TestNodeWebhookEndpointsRejectDelegatedPlatformUser(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-hook-user",
		"platform_tokens=hook-user-secret",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_hook_user",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	createWebhookBody, _ := json.Marshal(map[string]interface{}{
		"name":        "forbidden-user-webhook",
		"url":         "https://example.invalid/hook",
		"event_names": []string{"client.created"},
		"resources":   []string{"client"},
	})

	createReq := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(createWebhookBody))
	createReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformUserRequestHeaders(createReq, "hook-user-secret")
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/webhooks as platform user status = %d, want 403 body=%s", createResp.Code, createResp.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/webhooks", nil)
	applyNodePlatformUserRequestHeaders(listReq, "hook-user-secret")
	listResp := httptest.NewRecorder()
	handler.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/webhooks as platform user status = %d, want 403 body=%s", listResp.Code, listResp.Body.String())
	}
}

func TestNodeWebhookEndpointsRequireAuthentication(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	getReq := httptest.NewRequest(http.MethodGet, "/api/webhooks/1", nil)
	getResp := httptest.NewRecorder()
	handler.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusUnauthorized || !strings.Contains(getResp.Body.String(), "\"code\":\"unauthorized\"") {
		t.Fatalf("GET /api/webhooks/1 without auth status = %d body=%s, want 401 unauthorized", getResp.Code, getResp.Body.String())
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader([]byte(`{"url":"https://example.invalid/hook"}`)))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusUnauthorized || !strings.Contains(createResp.Body.String(), "\"code\":\"unauthorized\"") {
		t.Fatalf("POST /api/webhooks without auth status = %d body=%s, want 401 unauthorized", createResp.Code, createResp.Body.String())
	}
}

func TestNodeWebhookEndpointsRejectInvalidWebhookID(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-hook-invalid-id",
		"platform_tokens=hook-invalid-id-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_hook_invalid_id",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/webhooks/not-a-number", nil)
	applyNodePlatformAdminRequestHeaders(req, "hook-invalid-id-secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "\"code\":\"invalid_webhook_id\"") {
		t.Fatalf("GET /api/webhooks/not-a-number status = %d body=%s, want 400 invalid_webhook_id", resp.Code, resp.Body.String())
	}
}

func TestNodeWebhookEndpointsRejectTrailingJSON(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-hook-trailing-json",
		"platform_tokens=hook-trailing-json-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_hook_trailing_json",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader([]byte(`{"name":"hook","url":"https://example.invalid/hook"}{"name":"extra"}`)))
	req.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(req, "hook-trailing-json-secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "\"code\":\"invalid_json_body\"") {
		t.Fatalf("POST /api/webhooks with trailing json status = %d body=%s, want 400 invalid_json_body", resp.Code, resp.Body.String())
	}
}

func TestNodeWSWebhookAndSubscriptionRejectInvalidIDs(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-hook-ws-invalid-id",
		"platform_tokens=hook-ws-invalid-id-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_hook_ws_invalid_id",
		"node_changes_window=2048",
		"node_batch_max_items=80",
		"node_idempotency_ttl_seconds=600",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	applyNodePlatformAdminWSHeaders(headers, "hook-ws-invalid-id-secret")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	webhookReq := nodeWSFrame{
		ID:     "webhook-invalid-id",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/webhooks/not-a-number",
	}
	if err := conn.WriteJSON(webhookReq); err != nil {
		t.Fatalf("WriteJSON(webhook invalid id) error = %v", err)
	}
	webhookResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == webhookReq.ID
	})
	if webhookResp.Status != http.StatusBadRequest || webhookResp.Error != "" || !strings.Contains(string(webhookResp.Body), `"code":"invalid_webhook_id"`) {
		t.Fatalf("websocket invalid webhook id response = %+v, want 400 invalid_webhook_id body=%s", webhookResp, string(webhookResp.Body))
	}

	subscriptionReq := nodeWSFrame{
		ID:     "subscription-invalid-id",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/realtime/subscriptions/not-a-number",
	}
	if err := conn.WriteJSON(subscriptionReq); err != nil {
		t.Fatalf("WriteJSON(subscription invalid id) error = %v", err)
	}
	subscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == subscriptionReq.ID
	})
	if subscriptionResp.Status != http.StatusBadRequest || subscriptionResp.Error != "" || !strings.Contains(string(subscriptionResp.Body), `"code":"invalid_subscription_id"`) {
		t.Fatalf("websocket invalid subscription id response = %+v, want 400 invalid_subscription_id body=%s", subscriptionResp, string(subscriptionResp.Body))
	}
}

func TestNodeWebhookScrubsInvalidSelectorsAfterResourceDelete(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-hook-clean",
		"platform_tokens=hook-clean-secret",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_hook_clean",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{PlatformID: "master-hook-clean", ServiceUsername: "svc_master_hook_clean", Enabled: true},
	})
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-hook-clean")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-hook-clean) error = %v", err)
	}
	client := &file.Client{
		Id:        1,
		UserId:    serviceUser.Id,
		VerifyKey: "hook-clean-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	client.SetOwnerUserID(serviceUser.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &file.Host{
		Id:       1,
		Host:     "hook-clean.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Flow:     &file.Flow{},
		Target: &file.Target{
			TargetStr: "127.0.0.1:8080",
		},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookSrv.Close()

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	createWebhookBody, _ := json.Marshal(map[string]interface{}{
		"name":        "host-delete-webhook",
		"url":         webhookSrv.URL,
		"event_names": []string{"host.deleted"},
		"resources":   []string{"host"},
		"host_ids":    []int{1},
	})
	createWebhookReq := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(createWebhookBody))
	createWebhookReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createWebhookReq, "hook-clean-secret")
	createWebhookResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createWebhookResp, createWebhookReq)
	if createWebhookResp.Code != http.StatusOK {
		t.Fatalf("POST /api/webhooks status = %d body=%s", createWebhookResp.Code, createWebhookResp.Body.String())
	}

	var createWebhookPayload nodeWebhookMutationPayload
	decodeManagementData(t, createWebhookResp.Body.Bytes(), &createWebhookPayload)
	if createWebhookPayload.Item == nil || createWebhookPayload.Item.ID <= 0 {
		t.Fatalf("create webhook payload = %+v, want item id", createWebhookPayload)
	}

	deleteHostReq := httptest.NewRequest(http.MethodPost, "/api/hosts/1/actions/delete", nil)
	applyNodePlatformAdminRequestHeaders(deleteHostReq, "hook-clean-secret")
	deleteHostResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(deleteHostResp, deleteHostReq)
	if deleteHostResp.Code != http.StatusOK {
		t.Fatalf("POST /api/hosts/1/actions/delete status = %d body=%s", deleteHostResp.Code, deleteHostResp.Body.String())
	}

	getWebhookReq := httptest.NewRequest(http.MethodGet, "/api/webhooks/"+strconv.FormatInt(createWebhookPayload.Item.ID, 10), nil)
	applyNodePlatformAdminRequestHeaders(getWebhookReq, "hook-clean-secret")
	getWebhookResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(getWebhookResp, getWebhookReq)
	if getWebhookResp.Code != http.StatusOK {
		t.Fatalf("GET /api/webhooks/%d status = %d body=%s", createWebhookPayload.Item.ID, getWebhookResp.Code, getWebhookResp.Body.String())
	}

	var getWebhookPayload nodeWebhookMutationPayload
	decodeManagementData(t, getWebhookResp.Body.Bytes(), &getWebhookPayload)
	if getWebhookPayload.Item == nil {
		t.Fatalf("get webhook payload = %+v, want item", getWebhookPayload)
	}
	if getWebhookPayload.Item.Enabled {
		t.Fatalf("scrubbed webhook should be disabled, got %+v", getWebhookPayload.Item)
	}
	if len(getWebhookPayload.Item.HostIDs) != 0 {
		t.Fatalf("scrubbed webhook host_ids = %v, want empty", getWebhookPayload.Item.HostIDs)
	}
	if getWebhookPayload.Item.LastDisabledReason != "selector_invalid" {
		t.Fatalf("scrubbed webhook LastDisabledReason = %q, want selector_invalid", getWebhookPayload.Item.LastDisabledReason)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsWebhookDeliveryEvent(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-webhook-live",
		"platform_tokens=webhook-live-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_webhook_live",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookSrv.Close()

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	createWebhookBody, _ := json.Marshal(map[string]interface{}{
		"name":        "webhook-live-hook",
		"url":         webhookSrv.URL,
		"event_names": []string{"client.created"},
		"resources":   []string{"client"},
	})
	createWebhookReq := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(createWebhookBody))
	createWebhookReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createWebhookReq, "webhook-live-secret")
	createWebhookResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createWebhookResp, createWebhookReq)
	if createWebhookResp.Code != http.StatusOK {
		t.Fatalf("POST /api/webhooks status = %d body=%s", createWebhookResp.Code, createWebhookResp.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(runtime.State.NodeWebhooks.Active()) != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	srv := httptest.NewServer(runtime.Handler)
	defer srv.Close()
	conn := dialNodeWSTestConnection(t, srv.URL, "webhook-live-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "webhook-live-subscription",
		"event_names": []string{"webhook.delivery_succeeded"},
		"resources":   []string{"webhook"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "webhook-live-subscription-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(webhook live subscription create) error = %v", err)
	}

	createSubscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "webhook-live-subscription-create"
	})
	if createSubscriptionResp.Status != http.StatusOK {
		t.Fatalf("unexpected webhook live subscription response: %+v body=%s", createSubscriptionResp, string(createSubscriptionResp.Body))
	}
	var subscriptionPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createSubscriptionResp, &subscriptionPayload)
	if subscriptionPayload.Item.ID <= 0 {
		t.Fatalf("webhook live subscription payload = %+v, want item id", subscriptionPayload)
	}

	createClientReq := httptest.NewRequest(
		http.MethodPost,
		"/api/clients",
		strings.NewReader(fmt.Sprintf(`{"verify_key":"webhook-live-client-%d","remark":"webhook live event"}`, time.Now().UnixNano())),
	)
	createClientReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createClientReq, "webhook-live-secret")
	createClientResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(subscriptionPayload.Item.ID, 10)
	})
	body := string(callbackFrame.Body)
	if !strings.Contains(body, "\"name\":\"webhook.delivery_succeeded\"") || !strings.Contains(body, "\"resource\":\"webhook\"") {
		t.Fatalf("unexpected webhook delivery callback frame body = %s", body)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsWebhookSelectorScrubbedEvent(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-webhook-scrub",
		"platform_tokens=webhook-scrub-secret",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_webhook_scrub",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{PlatformID: "master-webhook-scrub", ServiceUsername: "svc_master_webhook_scrub", Enabled: true},
	})
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-webhook-scrub")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-webhook-scrub) error = %v", err)
	}
	client := &file.Client{
		Id:        1,
		UserId:    serviceUser.Id,
		VerifyKey: "webhook-scrub-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	client.SetOwnerUserID(serviceUser.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &file.Host{
		Id:       1,
		Host:     "webhook-scrub.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Flow:     &file.Flow{},
		Target: &file.Target{
			TargetStr: "127.0.0.1:8080",
		},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhookSrv.Close()

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	createWebhookBody, _ := json.Marshal(map[string]interface{}{
		"name":        "webhook-scrub-hook",
		"url":         webhookSrv.URL,
		"event_names": []string{"host.deleted"},
		"resources":   []string{"host"},
		"host_ids":    []int{1},
	})
	createWebhookReq := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(createWebhookBody))
	createWebhookReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createWebhookReq, "webhook-scrub-secret")
	createWebhookResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createWebhookResp, createWebhookReq)
	if createWebhookResp.Code != http.StatusOK {
		t.Fatalf("POST /api/webhooks status = %d body=%s", createWebhookResp.Code, createWebhookResp.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(runtime.State.NodeWebhooks.Active()) != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	srv := httptest.NewServer(runtime.Handler)
	defer srv.Close()
	conn := dialNodeWSTestConnection(t, srv.URL, "webhook-scrub-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "webhook-scrub-subscription",
		"event_names": []string{"webhook.selector_scrubbed"},
		"resources":   []string{"webhook"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "webhook-scrub-subscription-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(webhook scrub subscription create) error = %v", err)
	}

	createSubscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "webhook-scrub-subscription-create"
	})
	if createSubscriptionResp.Status != http.StatusOK {
		t.Fatalf("unexpected webhook scrub subscription response: %+v body=%s", createSubscriptionResp, string(createSubscriptionResp.Body))
	}
	var subscriptionPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createSubscriptionResp, &subscriptionPayload)
	if subscriptionPayload.Item.ID <= 0 {
		t.Fatalf("webhook scrub subscription payload = %+v, want item id", subscriptionPayload)
	}

	deleteHostReq := httptest.NewRequest(http.MethodPost, "/api/hosts/1/actions/delete", nil)
	applyNodePlatformAdminRequestHeaders(deleteHostReq, "webhook-scrub-secret")
	deleteHostResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(deleteHostResp, deleteHostReq)
	if deleteHostResp.Code != http.StatusOK {
		t.Fatalf("POST /api/hosts/1/actions/delete status = %d body=%s", deleteHostResp.Code, deleteHostResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(subscriptionPayload.Item.ID, 10)
	})
	body := string(callbackFrame.Body)
	if !strings.Contains(body, "\"name\":\"webhook.selector_scrubbed\"") ||
		!strings.Contains(body, "\"resource\":\"webhook\"") ||
		!strings.Contains(body, "\"last_disabled_reason\":\"selector_invalid\"") {
		t.Fatalf("unexpected webhook scrub callback frame body = %s", body)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsCallbackQueueDeliveredEvent(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackSrv.Close()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-callback-live",
		"platform_tokens=callback-live-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_callback_live",
		"platform_callback_urls=" + callbackSrv.URL + "/node/callback",
		"platform_callback_enabled=true",
		"platform_callback_timeout_seconds=5",
		"platform_callback_retry_max=0",
		"platform_callback_retry_backoff_seconds=1",
		"platform_callback_queue_max=4",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	srv := httptest.NewServer(runtime.Handler)
	defer srv.Close()
	conn := dialNodeWSTestConnection(t, srv.URL, "callback-live-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "callback-queue-delivered-subscription",
		"event_names": []string{"callbacks_queue.updated"},
		"resources":   []string{"callbacks_queue"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "callback-queue-delivered-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(callback queue delivered subscription create) error = %v", err)
	}

	createSubscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "callback-queue-delivered-create"
	})
	if createSubscriptionResp.Status != http.StatusOK {
		t.Fatalf("unexpected callback queue delivered subscription response: %+v body=%s", createSubscriptionResp, string(createSubscriptionResp.Body))
	}
	var subscriptionPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createSubscriptionResp, &subscriptionPayload)
	if subscriptionPayload.Item.ID <= 0 {
		t.Fatalf("callback queue delivered subscription payload = %+v, want item id", subscriptionPayload)
	}

	createClientReq := httptest.NewRequest(
		http.MethodPost,
		"/api/clients",
		strings.NewReader(fmt.Sprintf(`{"verify_key":"callback-live-client-%d","remark":"callback live event"}`, time.Now().UnixNano())),
	)
	createClientReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createClientReq, "callback-live-secret")
	createClientResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(subscriptionPayload.Item.ID, 10)
	})
	body := string(callbackFrame.Body)
	if !strings.Contains(body, "\"name\":\"callbacks_queue.updated\"") ||
		!strings.Contains(body, "\"resource\":\"callbacks_queue\"") ||
		!strings.Contains(body, "\"platform_id\":\"master-callback-live\"") ||
		!strings.Contains(body, "\"cause\":\"delivered\"") {
		t.Fatalf("unexpected callback queue delivered callback frame body = %s", body)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsCallbackQueueQueuedEvent(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer callbackSrv.Close()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-callback-queued",
		"platform_tokens=callback-queued-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_callback_queued",
		"platform_callback_urls=" + callbackSrv.URL + "/node/callback",
		"platform_callback_enabled=true",
		"platform_callback_timeout_seconds=5",
		"platform_callback_retry_max=0",
		"platform_callback_retry_backoff_seconds=1",
		"platform_callback_queue_max=4",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	srv := httptest.NewServer(runtime.Handler)
	defer srv.Close()
	conn := dialNodeWSTestConnection(t, srv.URL, "callback-queued-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "callback-queue-queued-subscription",
		"event_names": []string{"callbacks_queue.updated"},
		"resources":   []string{"callbacks_queue"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "callback-queue-queued-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(callback queue queued subscription create) error = %v", err)
	}

	createSubscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "callback-queue-queued-create"
	})
	if createSubscriptionResp.Status != http.StatusOK {
		t.Fatalf("unexpected callback queue queued subscription response: %+v body=%s", createSubscriptionResp, string(createSubscriptionResp.Body))
	}
	var subscriptionPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createSubscriptionResp, &subscriptionPayload)
	if subscriptionPayload.Item.ID <= 0 {
		t.Fatalf("callback queue queued subscription payload = %+v, want item id", subscriptionPayload)
	}

	createClientReq := httptest.NewRequest(
		http.MethodPost,
		"/api/clients",
		strings.NewReader(fmt.Sprintf(`{"verify_key":"callback-queued-client-%d","remark":"callback queued event"}`, time.Now().UnixNano())),
	)
	createClientReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createClientReq, "callback-queued-secret")
	createClientResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(subscriptionPayload.Item.ID, 10)
	})
	body := string(callbackFrame.Body)
	if !strings.Contains(body, "\"name\":\"callbacks_queue.updated\"") ||
		!strings.Contains(body, "\"resource\":\"callbacks_queue\"") ||
		!strings.Contains(body, "\"platform_id\":\"master-callback-queued\"") ||
		!strings.Contains(body, "\"cause\":\"queued\"") ||
		!strings.Contains(body, "\"callback_queue_size\":1") {
		t.Fatalf("unexpected callback queue queued callback frame body = %s", body)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsOperationUpdatedEvent(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-operations-live",
		"platform_tokens=operations-live-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_operations_live",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	srv := httptest.NewServer(runtime.Handler)
	defer srv.Close()
	conn := dialNodeWSTestConnection(t, srv.URL, "operations-live-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "operations-live-subscription",
		"event_names": []string{"operations.updated"},
		"resources":   []string{"operations"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "operations-live-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(operations live subscription create) error = %v", err)
	}

	createSubscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "operations-live-create"
	})
	if createSubscriptionResp.Status != http.StatusOK {
		t.Fatalf("unexpected operations live subscription response: %+v body=%s", createSubscriptionResp, string(createSubscriptionResp.Body))
	}
	var subscriptionPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createSubscriptionResp, &subscriptionPayload)
	if subscriptionPayload.Item.ID <= 0 {
		t.Fatalf("operations live subscription payload = %+v, want item id", subscriptionPayload)
	}

	const operationID = "ops-live-1"
	createClientReq := httptest.NewRequest(
		http.MethodPost,
		"/api/clients",
		strings.NewReader(fmt.Sprintf(`{"verify_key":"operations-live-client-%d","remark":"operations live event"}`, time.Now().UnixNano())),
	)
	createClientReq.Header.Set("Content-Type", "application/json")
	createClientReq.Header.Set("X-Operation-ID", operationID)
	applyNodePlatformAdminRequestHeaders(createClientReq, "operations-live-secret")
	createClientResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(subscriptionPayload.Item.ID, 10)
	})
	body := string(callbackFrame.Body)
	if !strings.Contains(body, "\"name\":\"operations.updated\"") ||
		!strings.Contains(body, "\"resource\":\"operations\"") ||
		!strings.Contains(body, "\"operation_id\":\""+operationID+"\"") ||
		!strings.Contains(body, "\"kind\":\"resource\"") {
		t.Fatalf("unexpected operations callback frame body = %s", body)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsManagementPlatformUpdatedEvent(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackSrv.Close()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-platform-status-live",
		"platform_tokens=platform-status-live-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_platform_status_live",
		"platform_callback_urls=" + callbackSrv.URL + "/node/callback",
		"platform_callback_enabled=true",
		"platform_callback_timeout_seconds=5",
		"platform_callback_retry_max=0",
		"platform_callback_retry_backoff_seconds=1",
		"platform_callback_queue_max=4",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	srv := httptest.NewServer(runtime.Handler)
	defer srv.Close()
	conn := dialNodeWSTestConnection(t, srv.URL, "platform-status-live-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "management-platform-live-subscription",
		"event_names": []string{"management_platforms.updated"},
		"resources":   []string{"management_platforms"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "management-platform-live-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(management platform live subscription create) error = %v", err)
	}

	createSubscriptionResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "management-platform-live-create"
	})
	if createSubscriptionResp.Status != http.StatusOK {
		t.Fatalf("unexpected management platform live subscription response: %+v body=%s", createSubscriptionResp, string(createSubscriptionResp.Body))
	}
	var subscriptionPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createSubscriptionResp, &subscriptionPayload)
	if subscriptionPayload.Item.ID <= 0 {
		t.Fatalf("management platform live subscription payload = %+v, want item id", subscriptionPayload)
	}

	createClientReq := httptest.NewRequest(
		http.MethodPost,
		"/api/clients",
		strings.NewReader(fmt.Sprintf(`{"verify_key":"platform-status-client-%d","remark":"platform status live event"}`, time.Now().UnixNano())),
	)
	createClientReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createClientReq, "platform-status-live-secret")
	createClientResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(subscriptionPayload.Item.ID, 10)
	})
	body := string(callbackFrame.Body)
	if !strings.Contains(body, "\"name\":\"management_platforms.updated\"") ||
		!strings.Contains(body, "\"resource\":\"management_platforms\"") ||
		!strings.Contains(body, "\"platform_id\":\"master-platform-status-live\"") ||
		!strings.Contains(body, "\"cause\":\"callback_delivered\"") ||
		!strings.Contains(body, "\"callback_deliveries\":1") {
		t.Fatalf("unexpected management platform callback frame body = %s", body)
	}
}

func TestNodeWSRealtimeSubscriptionEmitsCustomCallbackFrame(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-hook",
		"platform_tokens=ws-hook-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_hook",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	conn := dialNodeWSTestConnection(t, srv.URL, "ws-hook-secret")
	defer closeNodeWSTestConnection(t, conn)

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":          "client-created-subscription",
		"event_names":   []string{"client.created"},
		"resources":     []string{"client"},
		"content_mode":  "custom",
		"content_type":  "application/json",
		"body_template": `{"event":{{ quote .Event.Name }},"client_id":{{ .IDs.ClientID }},"action":{{ quote .Event.Action }}}`,
		"header_templates": map[string]string{
			"X-Event-Action": "{{ .Event.Action }}",
		},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "subscription-create",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(subscription create) error = %v", err)
	}

	createResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "subscription-create"
	})
	if createResp.Status != http.StatusOK {
		t.Fatalf("unexpected realtime subscription create response: %+v body=%s", createResp, string(createResp.Body))
	}
	var createPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createResp, &createPayload)
	if createPayload.Item.ID <= 0 {
		t.Fatalf("subscription create payload = %+v, want item id", createPayload)
	}

	createClientReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"ws-sub-client","remark":"ws subscription event"}`))
	createClientReq.Header.Set("Content-Type", "application/json")
	applyNodePlatformAdminRequestHeaders(createClientReq, "ws-hook-secret")
	createClientResp := httptest.NewRecorder()
	handler.ServeHTTP(createClientResp, createClientReq)
	if createClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", createClientResp.Code, createClientResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(createPayload.Item.ID, 10)
	})
	if got := callbackFrame.Headers["X-Subscription-ID"]; got != strconv.FormatInt(createPayload.Item.ID, 10) {
		t.Fatalf("callback X-Subscription-ID = %q, want %d", got, createPayload.Item.ID)
	}
	if got := callbackFrame.Headers["X-Event-Action"]; got != "create" {
		t.Fatalf("callback X-Event-Action = %q, want create", got)
	}
	if got := callbackFrame.Headers["Content-Type"]; got != "application/json" {
		t.Fatalf("callback Content-Type = %q, want application/json", got)
	}
	var payload struct {
		Event    string `json:"event"`
		ClientID int    `json:"client_id"`
		Action   string `json:"action"`
	}
	if err := json.Unmarshal(callbackFrame.Body, &payload); err != nil {
		t.Fatalf("json.Unmarshal(callback frame body) error = %v body=%s", err, string(callbackFrame.Body))
	}
	if payload.Event != "client.created" || payload.Action != "create" || payload.ClientID <= 0 {
		t.Fatalf("unexpected callback frame payload = %+v body=%s", payload, string(callbackFrame.Body))
	}
}

func TestNodeWSWebhookRegistrationRejectsDelegatedPlatformUser(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-hook-user",
		"platform_tokens=ws-hook-user-secret",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_hook_user",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	conn := dialNodeWSUserTestConnection(t, srv.URL, "ws-hook-user-secret")
	defer func() { _ = conn.Close() }()

	createWebhookBody, _ := json.Marshal(map[string]interface{}{
		"name":        "forbidden-user-webhook",
		"url":         "https://example.invalid/hook",
		"event_names": []string{"client.created"},
		"resources":   []string{"client"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "webhook-create-user",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/webhooks",
		Body:   createWebhookBody,
	}); err != nil {
		t.Fatalf("WriteJSON(webhook create) error = %v", err)
	}

	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "webhook-create-user"
	})
	if resp.Status != http.StatusForbidden || resp.Error != "" || !strings.Contains(string(resp.Body), `"code":"forbidden"`) {
		t.Fatalf("websocket webhook create response = %+v, want 403 forbidden body=%s", resp, string(resp.Body))
	}
}

func TestNodeWSRealtimeSubscriptionScrubsDeletedResourceSelector(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-clean",
		"platform_tokens=ws-clean-secret",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_clean",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{PlatformID: "master-ws-clean", ServiceUsername: "svc_master_ws_clean", Enabled: true},
	})
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-ws-clean")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-ws-clean) error = %v", err)
	}
	client := &file.Client{
		Id:        1,
		UserId:    serviceUser.Id,
		VerifyKey: "ws-clean-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	client.SetOwnerUserID(serviceUser.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &file.Host{
		Id:       1,
		Host:     "ws-clean.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Flow:     &file.Flow{},
		Target: &file.Target{
			TargetStr: "127.0.0.1:8080",
		},
	}
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	conn := dialNodeWSTestConnection(t, srv.URL, "ws-clean-secret")
	defer func() { _ = conn.Close() }()

	createSubscriptionBody, _ := json.Marshal(map[string]interface{}{
		"name":        "host-delete-subscription",
		"event_names": []string{"host.deleted"},
		"resources":   []string{"host"},
		"host_ids":    []int{1},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "subscription-create-delete",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createSubscriptionBody,
	}); err != nil {
		t.Fatalf("WriteJSON(delete subscription create) error = %v", err)
	}

	createResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "subscription-create-delete"
	})
	if createResp.Status != http.StatusOK {
		t.Fatalf("unexpected realtime delete subscription create response: %+v body=%s", createResp, string(createResp.Body))
	}
	var createPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	decodeManagementFrameData(t, createResp, &createPayload)
	if createPayload.Item.ID <= 0 {
		t.Fatalf("delete subscription create payload = %+v, want item id", createPayload)
	}

	deleteHostReq := httptest.NewRequest(http.MethodPost, "/api/hosts/1/actions/delete", nil)
	applyNodePlatformAdminRequestHeaders(deleteHostReq, "ws-clean-secret")
	deleteHostResp := httptest.NewRecorder()
	handler.ServeHTTP(deleteHostResp, deleteHostReq)
	if deleteHostResp.Code != http.StatusOK {
		t.Fatalf("POST /api/hosts/1/actions/delete status = %d body=%s", deleteHostResp.Code, deleteHostResp.Body.String())
	}

	callbackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "callback" && frame.ID == strconv.FormatInt(createPayload.Item.ID, 10)
	})
	if !strings.Contains(string(callbackFrame.Body), "\"name\":\"host.deleted\"") {
		t.Fatalf("unexpected delete callback frame body = %s", string(callbackFrame.Body))
	}

	getReqID := "subscription-get-after-delete"
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     getReqID,
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/realtime/subscriptions/" + strconv.FormatInt(createPayload.Item.ID, 10),
	}); err != nil {
		t.Fatalf("WriteJSON(subscription get) error = %v", err)
	}

	getResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == getReqID
	})
	if getResp.Status != http.StatusNotFound || getResp.Error != "" || !strings.Contains(string(getResp.Body), `"code":"subscription_not_found"`) {
		t.Fatalf("subscription after delete response = %+v, want 404 subscription_not_found body=%s", getResp, string(getResp.Body))
	}
}

func applyNodePlatformAdminRequestHeaders(req *http.Request, token string) {
	if req == nil {
		return
	}
	req.Header.Set("X-Node-Token", token)
	req.Header.Set("X-Platform-Role", "admin")
	req.Header.Set("X-Platform-Username", "platform-admin")
	req.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
}

func applyNodePlatformUserRequestHeaders(req *http.Request, token string) {
	if req == nil {
		return
	}
	req.Header.Set("X-Node-Token", token)
	req.Header.Set("X-Platform-Role", "user")
	req.Header.Set("X-Platform-Username", "platform-user")
	req.Header.Set("X-Platform-Actor-ID", "platform-user-1")
	req.Header.Set("X-Platform-Client-IDs", "1")
}

func dialNodeWSTestConnection(t *testing.T, serverURL, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/ws"
	headers := http.Header{}
	applyNodePlatformAdminWSHeaders(headers, token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}
	return conn
}

func closeNodeWSTestConnection(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	if conn == nil {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
	_ = conn.SetReadDeadline(deadline)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	_ = conn.Close()
}

func applyNodePlatformAdminWSHeaders(headers http.Header, token string) {
	headers.Set("X-Node-Token", token)
	headers.Set("X-Platform-Role", "admin")
	headers.Set("X-Platform-Username", "platform-admin")
	headers.Set("X-Platform-Actor-ID", "platform-admin-1")
}

func dialNodeWSUserTestConnection(t *testing.T, serverURL, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", token)
	headers.Set("X-Platform-Role", "user")
	headers.Set("X-Platform-Username", "platform-user")
	headers.Set("X-Platform-Actor-ID", "platform-user-1")
	headers.Set("X-Platform-Client-IDs", "1")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}
	return conn
}
