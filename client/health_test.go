package client

import (
	"container/heap"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
)

type healthCheckIdleCloserStub struct {
	calls int32
}

func (s *healthCheckIdleCloserStub) CloseIdleConnections() {
	atomic.AddInt32(&s.calls, 1)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestNewHealthCheckerInitializesOnlyValidHealthConfigs(t *testing.T) {
	healths := []*file.Health{
		{HealthMaxFail: 1, HealthCheckInterval: 1, HealthCheckTimeout: 1, HealthCheckTarget: "127.0.0.1:80"},
		{HealthMaxFail: 0, HealthCheckInterval: 1, HealthCheckTimeout: 1, HealthCheckTarget: "127.0.0.1:81"},
	}

	hc := NewHealthChecker(context.Background(), healths, nil)
	t.Cleanup(hc.Stop)

	if got := hc.heap.Len(); got != 1 {
		t.Fatalf("expected only one valid health entry in heap, got %d", got)
	}
	if healths[0].HealthMap == nil {
		t.Fatal("expected valid health entry to initialize HealthMap")
	}
	if healths[0].HealthNextTime.IsZero() {
		t.Fatal("expected valid health entry to have HealthNextTime initialized")
	}
	if healths[1].HealthMap != nil {
		t.Fatal("expected invalid health entry to keep nil HealthMap")
	}
}

func TestNewHealthCheckerHandlesNilParentContext(t *testing.T) {
	var nilCtx context.Context
	hc := NewHealthChecker(nilCtx, nil, nil)
	if hc == nil {
		t.Fatal("NewHealthChecker(nil, nil, nil) = nil, want runtime")
	}
	t.Cleanup(hc.Stop)
	if hc.ctx == nil {
		t.Fatal("HealthChecker.ctx = nil, want background-derived context")
	}
	if hc.cancel == nil {
		t.Fatal("HealthChecker.cancel = nil, want cancel func")
	}
}

func TestNewHealthCheckerUsesDedicatedHTTPTransport(t *testing.T) {
	hc := NewHealthChecker(context.Background(), nil, nil)
	t.Cleanup(hc.Stop)

	if hc.client == nil {
		t.Fatal("HealthChecker.client = nil, want http client")
	}
	transport, ok := hc.client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("HealthChecker.client.Transport = %T, want *http.Transport", hc.client.Transport)
	}
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || defaultTransport == nil {
		t.Fatal("http.DefaultTransport is not a *http.Transport")
	}
	if transport == defaultTransport {
		t.Fatal("HealthChecker.client.Transport reuses http.DefaultTransport, want cloned dedicated transport")
	}
	if hc.idleCloser != transport {
		t.Fatal("HealthChecker idle closer does not track owned transport")
	}
}

func TestHealthCheckerResetKeepsInvalidHealthsInactive(t *testing.T) {
	healths := []*file.Health{
		{HealthMaxFail: 1, HealthCheckInterval: 1, HealthCheckTimeout: 1, HealthCheckTarget: "127.0.0.1:80"},
		{HealthMaxFail: 1, HealthCheckInterval: 0, HealthCheckTimeout: 1, HealthCheckTarget: "127.0.0.1:81"},
	}

	hc := NewHealthChecker(context.Background(), healths, nil)
	t.Cleanup(hc.Stop)

	hc.Reset()

	if got := hc.heap.Len(); got != 1 {
		t.Fatalf("expected reset heap to keep only valid health entries, got %d", got)
	}
	if healths[1].HealthMap != nil {
		t.Fatal("expected invalid health entry to remain inactive after reset")
	}
}

func TestHealthCheckerRunChecksSkipsInvalidHealthsWhenRebuildingHeap(t *testing.T) {
	healths := []*file.Health{
		{
			HealthMaxFail:       2,
			HealthCheckInterval: 1,
			HealthCheckTimeout:  1,
			HealthCheckType:     "http",
			HealthCheckTarget:   "127.0.0.1:65535",
			HttpHealthUrl:       "/health",
			HealthMap:           map[string]int{},
		},
		{
			HealthMaxFail:       1,
			HealthCheckInterval: 0,
			HealthCheckTimeout:  1,
			HealthCheckType:     "http",
			HealthCheckTarget:   "127.0.0.1:65534",
		},
	}

	hc := NewHealthChecker(context.Background(), healths, nil)
	t.Cleanup(hc.Stop)

	hc.runChecks()

	if got := hc.heap.Len(); got != 1 {
		t.Fatalf("expected rebuilt heap to keep only valid health entries, got %d", got)
	}
	if healths[1].HealthMap != nil {
		t.Fatal("expected invalid health entry to remain unscheduled after runChecks")
	}
}

func TestDoCheckUnsupportedTypeSendsDownEvent(t *testing.T) {
	sideA, sideB := net.Pipe()
	defer func() { _ = sideA.Close() }()
	defer func() { _ = sideB.Close() }()

	readCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		_ = sideB.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 128)
		n, err := sideB.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- string(buf[:n])
	}()

	hc := &HealthChecker{ctx: context.Background(), serverConn: conn.NewConn(sideA)}
	h := &file.Health{HealthCheckTimeout: 1, HealthMaxFail: 1, HealthCheckType: "invalid", HealthCheckTarget: "node-a", HealthMap: map[string]int{}}

	hc.doCheck(h)

	select {
	case err := <-errCh:
		t.Fatalf("expected health event to be written, got error: %v", err)
	case payload := <-readCh:
		if !strings.Contains(payload, "node-a") || !strings.Contains(payload, common.CONN_DATA_SEQ+"0") {
			t.Fatalf("unexpected health payload: %q", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for health event")
	}
	if got := h.HealthMap["node-a"]; got != 1 {
		t.Fatalf("expected fail count to be incremented to 1, got %d", got)
	}
}

func TestDoCheckTCPSuccessAfterFailuresSendsRecoveryEvent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	sideA, sideB := net.Pipe()
	defer func() { _ = sideA.Close() }()
	defer func() { _ = sideB.Close() }()

	readCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		_ = sideB.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 128)
		n, err := sideB.Read(buf)
		if err != nil {
			errCh <- err
			return
		}
		readCh <- string(buf[:n])
	}()

	hc := &HealthChecker{ctx: context.Background(), serverConn: conn.NewConn(sideA)}
	target := ln.Addr().String()
	h := &file.Health{HealthCheckTimeout: 1, HealthMaxFail: 1, HealthCheckType: "tcp", HealthCheckTarget: target, HealthMap: map[string]int{target: 1}}

	hc.doCheck(h)

	select {
	case err := <-errCh:
		t.Fatalf("expected recovery event to be written, got error: %v", err)
	case payload := <-readCh:
		if !strings.Contains(payload, target) || !strings.Contains(payload, common.CONN_DATA_SEQ+"1") {
			t.Fatalf("unexpected recovery payload: %q", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recovery event")
	}
	if got := h.HealthMap[target]; got != 0 {
		t.Fatalf("expected fail count to reset to 0, got %d", got)
	}
}

func TestDoCheckTCPUsesHealthCheckerContext(t *testing.T) {
	originalDial := healthCheckDialContext
	healthCheckDialContext = func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
		if network != "tcp" {
			t.Fatalf("dial network = %q, want tcp", network)
		}
		if dialer == nil || dialer.Timeout != 5*time.Second {
			t.Fatalf("dial timeout = %v, want 5s", dialer.Timeout)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() {
		healthCheckDialContext = originalDial
	})

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()

	hc := &HealthChecker{ctx: parentCtx}
	h := &file.Health{
		HealthCheckTimeout: 5,
		HealthMaxFail:      10,
		HealthCheckType:    "tcp",
		HealthCheckTarget:  "127.0.0.1:65535",
		HealthMap:          map[string]int{},
	}

	start := time.Now()
	hc.doCheck(h)
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("doCheck() took %v after context cancellation, want <250ms", elapsed)
	}
	if got := h.HealthMap[h.HealthCheckTarget]; got != 1 {
		t.Fatalf("expected fail count to increment to 1, got %d", got)
	}
}

func TestDoCheckHTTPHandlesNilHealthCheckerContext(t *testing.T) {
	hc := &HealthChecker{
		client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Context() == nil {
				t.Fatal("request context = nil, want background-derived context")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		})},
	}
	h := &file.Health{
		HealthCheckTimeout: 1,
		HealthMaxFail:      1,
		HealthCheckType:    "http",
		HealthCheckTarget:  "example.com",
		HttpHealthUrl:      "/health",
		HealthMap:          map[string]int{},
	}

	hc.doCheck(h)

	if got := h.HealthMap[h.HealthCheckTarget]; got != 0 {
		t.Fatalf("expected fail count to stay at 0, got %d", got)
	}
}

func TestStopAndDrainHandlesTriggeredTimer(t *testing.T) {
	timer := time.NewTimer(10 * time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	stopAndDrain(timer)

	select {
	case <-timer.C:
		t.Fatal("expected timer channel to be drained")
	default:
	}
}

func TestHealthCheckerStopClosesOwnedIdleConnections(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	idleCloser := &healthCheckIdleCloserStub{}
	hc := &HealthChecker{
		ctx:        context.Background(),
		cancel:     func() {},
		timer:      timer,
		idleCloser: idleCloser,
	}

	hc.Stop()

	if got := atomic.LoadInt32(&idleCloser.calls); got != 1 {
		t.Fatalf("CloseIdleConnections() calls = %d, want 1", got)
	}
}

func TestHealthCheckerStopHandlesNilReceiver(t *testing.T) {
	var hc *HealthChecker
	hc.Stop()
}

func TestDoCheckHTTPStatusNotOKIncrementsFailCountWithoutEventBeforeThreshold(t *testing.T) {
	server := &httpStatusServer{statusCode: 503}
	ts := server.start(t)
	defer ts.Close()

	hc := &HealthChecker{ctx: context.Background(), client: ts.Client()}
	h := &file.Health{
		HealthCheckTimeout: 1,
		HealthMaxFail:      3,
		HealthCheckType:    "http",
		HealthCheckTarget:  strings.TrimPrefix(ts.URL, "http://"),
		HttpHealthUrl:      "/health",
		HealthMap:          map[string]int{},
	}

	hc.doCheck(h)

	if got := h.HealthMap[h.HealthCheckTarget]; got != 1 {
		t.Fatalf("expected fail count to be 1, got %d", got)
	}
}

func TestDoCheckWithoutServerConnDoesNotPanicAtThreshold(t *testing.T) {
	server := &httpStatusServer{statusCode: 503}
	ts := server.start(t)
	defer ts.Close()

	hc := &HealthChecker{ctx: context.Background(), client: ts.Client()}
	h := &file.Health{
		HealthCheckTimeout: 1,
		HealthMaxFail:      1,
		HealthCheckType:    "http",
		HealthCheckTarget:  strings.TrimPrefix(ts.URL, "http://"),
		HttpHealthUrl:      "/health",
		HealthMap:          map[string]int{},
	}

	hc.doCheck(h)

	if got := h.HealthMap[h.HealthCheckTarget]; got != 1 {
		t.Fatalf("expected fail count to be 1, got %d", got)
	}
}

func TestHealthCheckerRunChecksKeepsFutureSchedulesUntouched(t *testing.T) {
	now := time.Now()
	due := &file.Health{
		HealthCheckTimeout:  1,
		HealthMaxFail:       10,
		HealthCheckInterval: 5,
		HealthCheckType:     "invalid",
		HealthCheckTarget:   "due-target",
		HealthMap:           map[string]int{},
		HealthNextTime:      now.Add(-time.Second),
	}
	future := &file.Health{
		HealthCheckTimeout:  1,
		HealthMaxFail:       10,
		HealthCheckInterval: 5,
		HealthCheckType:     "invalid",
		HealthCheckTarget:   "future-target",
		HealthMap:           map[string]int{},
		HealthNextTime:      now.Add(30 * time.Second),
	}
	hc := &HealthChecker{
		ctx:     context.Background(),
		healths: []*file.Health{due, future},
		heap:    newHealthScheduleHeap(),
		client:  &http.Client{},
	}
	heap.Push(hc.heap, healthSchedule{health: due, next: due.HealthNextTime})
	heap.Push(hc.heap, healthSchedule{health: future, next: future.HealthNextTime})

	hc.runChecks()

	if got := hc.heap.Len(); got != 2 {
		t.Fatalf("heap.Len() = %d, want 2", got)
	}
	if !future.HealthNextTime.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("future.HealthNextTime changed to %v, want %v", future.HealthNextTime, now.Add(30*time.Second))
	}
	if !due.HealthNextTime.After(now) {
		t.Fatalf("due.HealthNextTime = %v, want rescheduled after %v", due.HealthNextTime, now)
	}
}

func TestNextHealthCheckTimeSkipsMissedIntervals(t *testing.T) {
	h := &file.Health{HealthCheckInterval: 5}
	previous := time.Unix(1_700_000_000, 0)
	now := previous.Add(17 * time.Second)

	got := nextHealthCheckTime(h, previous, now)
	want := previous.Add(20 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("nextHealthCheckTime() = %v, want %v", got, want)
	}
}

type httpStatusServer struct{ statusCode int }

func (s *httpStatusServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(s.statusCode)
		_, _ = io.WriteString(w, "ok")
	}))
}
