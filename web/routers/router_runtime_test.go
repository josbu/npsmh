package routers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func TestNewRuntimeSessionStoreFailure(t *testing.T) {
	oldInitSessionStore := initSessionStore
	defer func() { initSessionStore = oldInitSessionStore }()

	initSessionStore = func(_ *servercfg.Snapshot) error {
		return errors.New("session-store-failed")
	}

	runtime := NewRuntime(nil)
	if runtime.Err == nil {
		t.Fatal("NewRuntime() Err = nil, want non-nil when session store init fails")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("runtime handler status = %d, want 500", resp.Code)
	}
}

func TestNewManagedRuntimeSessionStoreFailure(t *testing.T) {
	oldInitSessionStore := initSessionStore
	defer func() { initSessionStore = oldInitSessionStore }()

	initSessionStore = func(_ *servercfg.Snapshot) error {
		return errors.New("session-store-failed")
	}

	runtime := NewManagedRuntime(nil)
	if runtime.Err == nil {
		t.Fatal("NewManagedRuntime() Err = nil, want non-nil when session store init fails")
	}
	if runtime.Stop == nil {
		t.Fatal("NewManagedRuntime() Stop = nil, want base runtime stop")
	}
}

func TestReplaceManagedRuntimeSessionStoreFailureKeepsPreviousActive(t *testing.T) {
	oldInitSessionStore := initSessionStore
	defer func() { initSessionStore = oldInitSessionStore }()

	initSessionStore = func(_ *servercfg.Snapshot) error {
		return errors.New("session-store-failed")
	}

	StopManagedRuntime()
	t.Cleanup(StopManagedRuntime)

	previousStopped := make(chan struct{}, 1)
	previous := &Runtime{
		Stop: func() {
			select {
			case previousStopped <- struct{}{}:
			default:
			}
		},
	}
	activeManagedRuntimeMu.Lock()
	activeManagedRuntime = previous
	activeManagedRuntimeMu.Unlock()

	runtime := ReplaceManagedRuntime(nil)
	if runtime == nil {
		t.Fatal("ReplaceManagedRuntime() = nil, want failed runtime")
	}
	if runtime.Err == nil {
		t.Fatal("ReplaceManagedRuntime() Err = nil, want startup failure")
	}

	select {
	case <-previousStopped:
		t.Fatal("ReplaceManagedRuntime() should keep previous runtime active when replacement fails")
	default:
	}

	activeManagedRuntimeMu.Lock()
	current := activeManagedRuntime
	activeManagedRuntimeMu.Unlock()
	if current != previous {
		t.Fatalf("activeManagedRuntime = %p, want previous runtime %p after failed replace", current, previous)
	}

	if runtime.State == nil {
		t.Fatal("failed runtime State = nil, want cleanup target")
	}
	select {
	case <-runtime.State.BaseContext().Done():
	default:
		t.Fatal("failed runtime should be stopped and cleaned up")
	}
}

func TestReplaceManagedRuntimeSessionStoreFailureLeavesNoActiveRuntime(t *testing.T) {
	oldInitSessionStore := initSessionStore
	defer func() { initSessionStore = oldInitSessionStore }()

	initSessionStore = func(_ *servercfg.Snapshot) error {
		return errors.New("session-store-failed")
	}

	StopManagedRuntime()
	t.Cleanup(StopManagedRuntime)

	runtime := ReplaceManagedRuntime(nil)
	if runtime == nil {
		t.Fatal("ReplaceManagedRuntime() = nil, want failed runtime")
	}
	if runtime.Err == nil {
		t.Fatal("ReplaceManagedRuntime() Err = nil, want startup failure")
	}

	activeManagedRuntimeMu.Lock()
	current := activeManagedRuntime
	activeManagedRuntimeMu.Unlock()
	if current != nil {
		t.Fatalf("activeManagedRuntime = %p, want nil after failed replace without previous runtime", current)
	}

	if runtime.State == nil {
		t.Fatal("failed runtime State = nil, want cleanup target")
	}
	select {
	case <-runtime.State.BaseContext().Done():
	default:
		t.Fatal("failed runtime should be stopped and cleaned up")
	}
}
