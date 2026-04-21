package routers

import (
	"context"
	"sync"
	"testing"

	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

func TestEnsureNodeManagementPlatformRuntimeStoreConcurrent(t *testing.T) {
	state := NewStateWithApp(webapi.New(nil))

	const workers = 32
	var wg sync.WaitGroup
	results := make(chan webservice.ManagementPlatformRuntimeStatusStore, workers)

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			results <- ensureNodeManagementPlatformRuntimeStore(state)
		}()
	}
	wg.Wait()
	close(results)

	var first *nodeManagementPlatformRuntimeStore
	for current := range results {
		wrapped, ok := current.(*nodeManagementPlatformRuntimeStore)
		if !ok || wrapped == nil {
			t.Fatalf("ensureNodeManagementPlatformRuntimeStore() = %#v, want wrapped runtime store", current)
		}
		if wrapped.base == nil {
			t.Fatal("wrapped runtime store should have initialized base store")
		}
		if first == nil {
			first = wrapped
			continue
		}
		if wrapped != first {
			t.Fatalf("ensureNodeManagementPlatformRuntimeStore() returned multiple wrappers: first=%p current=%p", first, wrapped)
		}
	}
}

func TestNodeManagementPlatformRuntimeStoreSkipsNoopStatusEvents(t *testing.T) {
	events := make([]webapi.Event, 0, 4)
	store := wrapNodeManagementPlatformRuntimeStore(
		webservice.NewInMemoryManagementPlatformRuntimeStatusStore(),
		func(ctx context.Context, event webapi.Event) {
			events = append(events, event)
		},
		func() context.Context { return context.Background() },
	)

	store.NoteConfigured("platform-a", "reverse", "wss://example.test/ws", true)
	store.NoteConfigured("platform-a", "reverse", "wss://example.test/ws", true)
	if len(events) != 1 {
		t.Fatalf("configured event count = %d, want 1", len(events))
	}

	store.NoteCallbackConfigured("platform-a", "https://example.test/callback", true, 5, 3, 2, 4, 0)
	store.NoteCallbackConfigured("platform-a", "https://example.test/callback", true, 5, 3, 2, 4, 0)
	if len(events) != 2 {
		t.Fatalf("callback configured event count = %d, want 2", len(events))
	}

	store.NoteCallbackQueueSize("platform-a", 0)
	if len(events) != 2 {
		t.Fatalf("noop callback queue size event count = %d, want 2", len(events))
	}

	store.NoteCallbackQueueSize("platform-a", 1)
	if len(events) != 3 {
		t.Fatalf("callback queue size event count = %d, want 3", len(events))
	}
	if cause := events[2].Fields["cause"]; cause != "callback_queue_size" {
		t.Fatalf("callback queue size cause = %v, want %q", cause, "callback_queue_size")
	}
}
