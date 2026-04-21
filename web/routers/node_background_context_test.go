package routers

import (
	"context"
	"sync"
	"testing"

	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

type recordingManagementEventHooks struct {
	mu    sync.Mutex
	ctx   context.Context
	event webapi.Event
}

func (h *recordingManagementEventHooks) OnManagementEvent(ctx context.Context, event webapi.Event) error {
	h.mu.Lock()
	h.ctx = ctx
	h.event = event
	h.mu.Unlock()
	return nil
}

func (h *recordingManagementEventHooks) snapshot() (context.Context, webapi.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ctx, h.event
}

func TestStateBaseContextCancelsOnClose(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	baseCtx := state.BaseContext()
	state.Close()

	select {
	case <-baseCtx.Done():
	default:
		t.Fatal("BaseContext() should be canceled after State.Close()")
	}
}

func TestNodeManagementPlatformRuntimeStoreUsesStateBaseContext(t *testing.T) {
	hooks := &recordingManagementEventHooks{}
	app := webapi.NewWithOptions(nil, webapi.Options{Hooks: hooks})
	state := NewStateWithApp(app)

	store, ok := ensureNodeManagementPlatformRuntimeStore(state).(*nodeManagementPlatformRuntimeStore)
	if !ok || store == nil {
		t.Fatal("ensureNodeManagementPlatformRuntimeStore() did not return wrapped runtime store")
	}
	store.NoteConfigured("platform-a", "reverse", "wss://example.test/ws", true)

	ctx, event := hooks.snapshot()
	if ctx == nil {
		t.Fatal("management platform runtime event should receive non-nil context")
	}
	if event.Name != "management_platforms.updated" {
		t.Fatalf("event.Name = %q, want %q", event.Name, "management_platforms.updated")
	}
	state.Close()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("management platform runtime event context should be canceled when state closes")
	}
}

func TestNodeCallbackQueueStoreUsesBaseContextForBackgroundEvents(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		gotCtx   context.Context
		gotEvent webapi.Event
	)
	store := newNodeCallbackQueueStore(
		"",
		[]nodeCallbackWorkerConfig{{PlatformID: "platform-a", CallbackQueueMax: 4}},
		webservice.NewInMemoryManagementPlatformRuntimeStatusStore(),
		func(ctx context.Context, event webapi.Event) {
			gotCtx = ctx
			gotEvent = event
		},
		func() context.Context { return baseCtx },
	)

	queueSize, dropped := store.Enqueue("platform-a", nodeCallbackEnvelope{
		PlatformID: "platform-a",
		Type:       "event",
	})
	if queueSize != 1 || dropped {
		t.Fatalf("Enqueue() = (%d, %v), want (1, false)", queueSize, dropped)
	}
	if gotCtx != baseCtx {
		t.Fatal("callback queue background event should inherit configured base context")
	}
	if gotEvent.Name != "callbacks_queue.updated" {
		t.Fatalf("event.Name = %q, want %q", gotEvent.Name, "callbacks_queue.updated")
	}
}

func TestNodeOperationRuntimeStoreUsesBaseContextForBackgroundEvents(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		gotCtx   context.Context
		gotEvent webapi.Event
	)
	store := newNodeOperationRuntimeStore(
		"",
		webservice.NodeOperationDefaultHistoryLimit,
		func(ctx context.Context, event webapi.Event) {
			gotCtx = ctx
			gotEvent = event
		},
		func() context.Context { return baseCtx },
	)

	store.Record(webservice.NodeOperationRecordPayload{OperationID: "op-1"})

	if gotCtx != baseCtx {
		t.Fatal("operation runtime event should inherit configured base context")
	}
	if gotEvent.Name != "operations.updated" {
		t.Fatalf("event.Name = %q, want %q", gotEvent.Name, "operations.updated")
	}
}

func TestNodeWebhookStoreUsesBaseContextForBackgroundEvents(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		gotCtx   context.Context
		gotEvent webapi.Event
	)
	store := newNodeWebhookStore(
		"",
		nil,
		func(ctx context.Context, event webapi.Event) {
			gotCtx = ctx
			gotEvent = event
		},
		func() context.Context { return baseCtx },
	)

	store.NoteDelivered(nil, 1, 204)
	if gotCtx != nil || gotEvent.Name != "" {
		t.Fatalf("missing webhook should not emit event, got ctx=%v event=%+v", gotCtx, gotEvent)
	}

	store.items[1] = nodeWebhookConfig{
		ID:    1,
		URL:   "https://example.test/hook",
		Owner: webapi.AdminActorWithFallback("admin", "admin"),
		nodeEventSinkConfig: nodeEventSinkConfig{
			Enabled: true,
		},
	}
	store.runtime[1] = nodeWebhookRuntimeState{}

	store.NoteDelivered(nil, 1, 204)

	if gotCtx != baseCtx {
		t.Fatal("webhook background event should inherit configured base context")
	}
	if gotEvent.Name != "webhook.delivery_succeeded" {
		t.Fatalf("event.Name = %q, want %q", gotEvent.Name, "webhook.delivery_succeeded")
	}
}

func TestNodeCallbackManagerUsesStateBaseContext(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	manager := newNodeCallbackManager(state)

	if manager.ctx == nil {
		t.Fatal("callback manager context should be non-nil")
	}
	state.Close()
	select {
	case <-manager.ctx.Done():
	default:
		t.Fatal("callback manager context should be canceled when state closes")
	}
	manager.cancel()
}

func TestNodeReverseManagerUsesStateBaseContext(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	manager := newNodeReverseManager(state)

	if manager.ctx == nil {
		t.Fatal("reverse manager context should be non-nil")
	}
	state.Close()
	select {
	case <-manager.ctx.Done():
	default:
		t.Fatal("reverse manager context should be canceled when state closes")
	}
	manager.cancel()
}

func TestNodeWebhookDispatcherManagerUsesStateBaseContext(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))
	manager := newNodeWebhookDispatcherManager(state)

	if manager.ctx == nil {
		t.Fatal("webhook dispatcher manager context should be non-nil")
	}
	state.Close()
	select {
	case <-manager.ctx.Done():
	default:
		t.Fatal("webhook dispatcher manager context should be canceled when state closes")
	}
	manager.cancel()
}
