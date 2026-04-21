package routers

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeRealtimeSessionRegistryInvalidateAllReturnsWithoutWaitingForSlowHandlers(t *testing.T) {
	registry := newNodeRealtimeSessionRegistry()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	registry.Register(func(string, string) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	})

	done := make(chan struct{})
	go func() {
		registry.InvalidateAll("config_import", "epoch-1")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("InvalidateAll() should return without waiting for slow handlers")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("InvalidateAll() should still dispatch registered handlers")
	}

	close(release)
}

func TestNodeRealtimeSessionRegistryInvalidateAllInvokesEachHandlerOnce(t *testing.T) {
	registry := newNodeRealtimeSessionRegistry()
	var count atomic.Int32
	var wg sync.WaitGroup

	for index := 0; index < 5; index++ {
		wg.Add(1)
		registry.Register(func(reason, configEpoch string) {
			defer wg.Done()
			if reason != "state_close" || configEpoch != "epoch-2" {
				t.Errorf("invalidate args = (%q, %q), want (%q, %q)", reason, configEpoch, "state_close", "epoch-2")
			}
			count.Add(1)
		})
	}

	registry.InvalidateAll("state_close", "epoch-2")

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("InvalidateAll() did not finish dispatching handlers")
	}

	if got := count.Load(); got != 5 {
		t.Fatalf("handler count = %d, want 5", got)
	}
}

func TestNodeRealtimeSessionRegistryInvalidateAllDoesNotSerializeMultipleHandlers(t *testing.T) {
	registry := newNodeRealtimeSessionRegistry()
	firstStarted := make(chan struct{}, 1)
	secondStarted := make(chan struct{}, 1)
	release := make(chan struct{})

	registry.Register(func(string, string) {
		select {
		case firstStarted <- struct{}{}:
		default:
		}
		<-release
	})
	registry.Register(func(string, string) {
		select {
		case secondStarted <- struct{}{}:
		default:
		}
	})

	registry.InvalidateAll("config_import", "epoch-3")

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first invalidate handler did not start")
	}

	select {
	case <-secondStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second invalidate handler should not be blocked behind the first handler")
	}

	close(release)
}
