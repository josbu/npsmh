package server

import (
	"sync/atomic"
	"testing"
	"time"
)

type fakeLifecycleMonitorTicker struct {
	ch        chan time.Time
	stopCalls atomic.Int32
	stopped   chan struct{}
}

func newFakeLifecycleMonitorTicker() *fakeLifecycleMonitorTicker {
	return &fakeLifecycleMonitorTicker{
		ch:      make(chan time.Time, 1),
		stopped: make(chan struct{}),
	}
}

func (t *fakeLifecycleMonitorTicker) Chan() <-chan time.Time {
	return t.ch
}

func (t *fakeLifecycleMonitorTicker) Stop() {
	if t.stopCalls.Add(1) == 1 {
		close(t.stopped)
	}
}

func TestStartLifecycleMonitorStartsOnlyOneWorker(t *testing.T) {
	oldMonitor := runtimeLifecycleMonitor
	t.Cleanup(func() {
		runtimeLifecycleMonitor = oldMonitor
	})

	ticker := newFakeLifecycleMonitorTicker()
	var (
		tickerCreates atomic.Int32
		monitorCalls  atomic.Int32
	)
	runtimeLifecycleMonitor = &lifecycleMonitorRuntime{
		newTicker: func(time.Duration) flowSessionTicker {
			tickerCreates.Add(1)
			return ticker
		},
		monitor: func(now int64) {
			if now != 42 {
				t.Errorf("monitor now = %d, want 42", now)
			}
			monitorCalls.Add(1)
		},
		now: func() int64 { return 42 },
	}

	StartLifecycleMonitor()
	StartLifecycleMonitor()

	ticker.ch <- time.Unix(42, 0)
	close(ticker.ch)

	select {
	case <-ticker.stopped:
	case <-time.After(time.Second):
		t.Fatal("lifecycle monitor did not stop after ticker close")
	}

	if got := tickerCreates.Load(); got != 1 {
		t.Fatalf("ticker creates = %d, want 1", got)
	}
	if got := monitorCalls.Load(); got != 1 {
		t.Fatalf("monitor calls = %d, want 1", got)
	}
	if got := ticker.stopCalls.Load(); got != 1 {
		t.Fatalf("ticker stop calls = %d, want 1", got)
	}
}

func TestStartLifecycleMonitorSkipsNilTicker(t *testing.T) {
	oldMonitor := runtimeLifecycleMonitor
	t.Cleanup(func() {
		runtimeLifecycleMonitor = oldMonitor
	})

	var tickerCreates atomic.Int32
	runtimeLifecycleMonitor = &lifecycleMonitorRuntime{
		newTicker: func(time.Duration) flowSessionTicker {
			tickerCreates.Add(1)
			return nil
		},
	}

	StartLifecycleMonitor()
	StartLifecycleMonitor()

	if got := tickerCreates.Load(); got != 1 {
		t.Fatalf("ticker creates = %d, want 1", got)
	}
}
