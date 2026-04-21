package routers

import (
	"context"
	"sync"
	"testing"
	"time"

	webapi "github.com/djylb/nps/web/api"
)

func TestNodeWebhookScrubEventDoesNotHoldStoreLockDuringLookup(t *testing.T) {
	actor := webapi.AdminActorWithFallback("admin", "admin")
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	store := &nodeWebhookStore{
		items: map[int64]nodeWebhookConfig{
			1: {
				ID:    1,
				URL:   "https://example.test/hook",
				Owner: actor,
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
					Selector: nodeEventSelector{
						HostIDs: []int{1},
					},
				},
			},
		},
		runtime: make(map[int64]nodeWebhookRuntimeState),
		lookup: func() nodeEventResourceLookup {
			return nodeEventResourceLookup{
				HostExists: func(id int) bool {
					startedOnce.Do(func() { close(started) })
					<-release
					return false
				},
			}
		},
	}

	done := make(chan struct{})
	go func() {
		store.ScrubEvent(context.Background(), webapi.Event{Name: "host.deleted", Resource: "host"})
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scrub lookup to start")
	}

	readDone := make(chan error, 1)
	go func() {
		payload, ok := store.GetVisible(actor, 1)
		if !ok {
			readDone <- context.DeadlineExceeded
			return
		}
		if payload.ID != 1 {
			readDone <- context.Canceled
			return
		}
		readDone <- nil
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("GetVisible() during scrub returned unexpected error marker: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("GetVisible() blocked behind ScrubEvent lookup")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ScrubEvent() did not finish after lookup release")
	}

	payload, ok := store.GetVisible(actor, 1)
	if !ok {
		t.Fatal("GetVisible() after scrub = false, want payload")
	}
	if payload.Enabled {
		t.Fatalf("scrubbed webhook should be disabled, got %+v", payload)
	}
	if len(payload.HostIDs) != 0 {
		t.Fatalf("scrubbed webhook host ids = %v, want empty", payload.HostIDs)
	}
	if payload.LastDisabledReason != "selector_invalid" {
		t.Fatalf("scrubbed webhook LastDisabledReason = %q, want selector_invalid", payload.LastDisabledReason)
	}
}

func TestNodeWebhookScrubEventSkipsStaleConcurrentSelectorChanges(t *testing.T) {
	actor := webapi.AdminActorWithFallback("admin", "admin")
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	store := &nodeWebhookStore{
		items: map[int64]nodeWebhookConfig{
			1: {
				ID:    1,
				URL:   "https://example.test/original",
				Owner: actor,
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
					Selector: nodeEventSelector{
						HostIDs: []int{1},
					},
				},
			},
		},
		runtime: make(map[int64]nodeWebhookRuntimeState),
		lookup: func() nodeEventResourceLookup {
			return nodeEventResourceLookup{
				HostExists: func(id int) bool {
					startedOnce.Do(func() { close(started) })
					<-release
					return false
				},
			}
		},
	}

	done := make(chan struct{})
	go func() {
		store.ScrubEvent(context.Background(), webapi.Event{Name: "host.deleted", Resource: "host"})
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scrub lookup to start")
	}

	store.mu.Lock()
	item := store.items[1]
	item.URL = "https://example.test/updated"
	item.Selector = nodeEventSelector{HostIDs: []int{2}}
	store.items[1] = item
	store.mu.Unlock()

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ScrubEvent() did not finish after lookup release")
	}

	payload, ok := store.GetVisible(actor, 1)
	if !ok {
		t.Fatal("GetVisible() after concurrent update = false, want payload")
	}
	if !payload.Enabled {
		t.Fatalf("stale scrub should not disable concurrently updated webhook, got %+v", payload)
	}
	if payload.URL != "https://example.test/updated" {
		t.Fatalf("payload.URL = %q, want https://example.test/updated", payload.URL)
	}
	if len(payload.HostIDs) != 1 || payload.HostIDs[0] != 2 {
		t.Fatalf("payload.HostIDs = %v, want [2]", payload.HostIDs)
	}
	if payload.LastDisabledReason != "" {
		t.Fatalf("payload.LastDisabledReason = %q, want empty", payload.LastDisabledReason)
	}
}

func TestNodeWebhookScrubEventMemoizesRepeatedResourceLookups(t *testing.T) {
	owner := webapi.PlatformActor("platform-a", "admin", "webhook", "webhook:platform-a", false, nil, 77)
	hostLookups := 0
	platformLookups := 0
	userLookups := 0

	store := &nodeWebhookStore{
		items: map[int64]nodeWebhookConfig{
			1: {
				ID:    1,
				URL:   "https://example.test/one",
				Owner: owner,
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
					Selector: nodeEventSelector{
						HostIDs: []int{5},
					},
				},
			},
			2: {
				ID:    2,
				URL:   "https://example.test/two",
				Owner: owner,
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
					Selector: nodeEventSelector{
						HostIDs: []int{5},
					},
				},
			},
		},
		runtime: make(map[int64]nodeWebhookRuntimeState),
		lookup: func() nodeEventResourceLookup {
			return nodeEventResourceLookup{
				HostExists: func(id int) bool {
					hostLookups++
					return false
				},
				PlatformExists: func(platformID string) bool {
					platformLookups++
					return true
				},
				UserExists: func(id int) bool {
					userLookups++
					return true
				},
			}
		},
	}

	store.ScrubEvent(context.Background(), webapi.Event{Name: "host.deleted", Resource: "host"})

	if hostLookups != 1 {
		t.Fatalf("host lookup count = %d, want 1", hostLookups)
	}
	if platformLookups != 1 {
		t.Fatalf("platform lookup count = %d, want 1", platformLookups)
	}
	if userLookups != 1 {
		t.Fatalf("user lookup count = %d, want 1", userLookups)
	}
}
