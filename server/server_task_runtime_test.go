package server

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/bridge"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/index"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/proxy"
	"github.com/djylb/nps/server/tool"
)

type failingStartService struct {
	startGate  chan struct{}
	startDone  chan struct{}
	startErr   error
	closeCalls atomic.Int32
}

type replacingCloseService struct {
	replacement interface{}
	closeCalls  atomic.Int32
	onClose     func()
}

type failingCloseService struct {
	closeErr   error
	closeCalls atomic.Int32
}

func (s *replacingCloseService) Start() error { return nil }

func (s *replacingCloseService) Close() error {
	s.closeCalls.Add(1)
	if s.onClose != nil {
		s.onClose()
	}
	return nil
}

func (s *failingCloseService) Start() error { return nil }

func (s *failingCloseService) Close() error {
	s.closeCalls.Add(1)
	return s.closeErr
}

func newFailingStartService(err error) *failingStartService {
	return &failingStartService{
		startDone: make(chan struct{}),
		startErr:  err,
	}
}

func (s *failingStartService) Start() error {
	if s.startGate != nil {
		<-s.startGate
	}
	close(s.startDone)
	return s.startErr
}

func (s *failingStartService) Close() error {
	s.closeCalls.Add(1)
	return nil
}

func installTaskStartFailureHook(t *testing.T) <-chan int {
	t.Helper()
	done := make(chan int, 1)
	taskStartFailureHook.Store(taskStartFailureHookFunc(func(taskID int) {
		select {
		case done <- taskID:
		default:
		}
	}))
	t.Cleanup(func() {
		taskStartFailureHook.Store(taskStartFailureHookFunc(nil))
	})
	return done
}

func TestAddTaskRollsBackPersistedStatusOnAsyncStartFailure(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)
	failureDone := installTaskStartFailureHook(t)

	oldModes := runtimeTaskModes
	svc := newFailingStartService(errors.New("boom"))
	runtimeTaskModes.newMode = func(taskModeDependencies, *file.Tunnel) proxy.Service {
		return svc
	}
	t.Cleanup(func() {
		runtimeTaskModes = oldModes
	})

	client := &file.Client{
		Id:        1,
		VerifyKey: "task-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     31,
		Mode:   "httpHostServer",
		Status: true,
		Remark: "failing-task",
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	if err := AddTask(task); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}

	select {
	case <-svc.startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async task start failure")
	}
	select {
	case got := <-failureDone:
		if got != task.Id {
			t.Fatalf("task start failure hook id = %d, want %d", got, task.Id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async task failure cleanup")
	}

	if svc.closeCalls.Load() != 1 {
		t.Fatalf("Close() calls = %d, want 1", svc.closeCalls.Load())
	}
	if _, ok := runtimeTasks.Load(task.Id); ok {
		t.Fatalf("runtime task %d should be removed after async start failure", task.Id)
	}
	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if stored.Status {
		t.Fatal("persisted task status should roll back to false after async start failure")
	}
}

func TestAddTaskRejectsNilTask(t *testing.T) {
	resetServerRuntimeGlobals(t)

	if err := AddTask(nil); !errors.Is(err, errTaskIsNil) {
		t.Fatalf("AddTask(nil) error = %v, want %v", err, errTaskIsNil)
	}
}

func TestAddTaskStartFailureDoesNotClobberReplacementRuntime(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)
	failureDone := installTaskStartFailureHook(t)

	oldModes := runtimeTaskModes
	svc := newFailingStartService(errors.New("boom"))
	svc.startGate = make(chan struct{})
	replacement := &stubServerService{}
	runtimeTaskModes.newMode = func(taskModeDependencies, *file.Tunnel) proxy.Service {
		return svc
	}
	t.Cleanup(func() {
		runtimeTaskModes = oldModes
	})

	client := &file.Client{
		Id:        2,
		VerifyKey: "replacement-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     32,
		Mode:   "httpHostServer",
		Status: true,
		Remark: "replacement-task",
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	if err := AddTask(task); err != nil {
		t.Fatalf("AddTask() error = %v", err)
	}
	current, ok := runtimeTasks.Load(task.Id)
	if !ok || current != svc {
		t.Fatalf("runtime task entry = %v, %v; want pending failing service", current, ok)
	}
	runtimeTasks.Store(task.Id, replacement)
	close(svc.startGate)

	select {
	case <-svc.startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async task start failure")
	}
	select {
	case got := <-failureDone:
		if got != task.Id {
			t.Fatalf("task start failure hook id = %d, want %d", got, task.Id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async task failure cleanup")
	}

	if svc.closeCalls.Load() != 1 {
		t.Fatalf("Close() calls = %d, want 1", svc.closeCalls.Load())
	}
	current, ok = runtimeTasks.Load(task.Id)
	if !ok || current != replacement {
		t.Fatalf("runtime task entry = %v, %v; want replacement service", current, ok)
	}
	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !stored.Status {
		t.Fatal("replacement runtime should keep persisted task status unchanged")
	}
	if replacement.closeCalls != 0 {
		t.Fatalf("replacement Close() calls = %d, want 0", replacement.closeCalls)
	}
}

func TestStartTaskPersistsStatusWithoutClientLookup(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	client := &file.Client{
		Id:        1,
		VerifyKey: "start-task-status-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     41,
		Mode:   "secret",
		Status: false,
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	file.GetDb().JsonDb.Clients.Delete(client.Id)

	if err := StartTask(task.Id); err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}
	if !runtimeTasks.Contains(task.Id) {
		t.Fatalf("runtimeTasks should contain secret task %d after StartTask()", task.Id)
	}

	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !stored.Status {
		t.Fatal("stored task status = false, want true after StartTask()")
	}
}

func TestStartTaskAllowsHTTPHostServerWithoutTunnelPortProbe(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	oldModes := runtimeTaskModes
	svc := &stubServerService{}
	runtimeTaskModes.newMode = func(taskModeDependencies, *file.Tunnel) proxy.Service {
		return svc
	}
	t.Cleanup(func() {
		runtimeTaskModes = oldModes
	})

	client := &file.Client{
		Id:        11,
		VerifyKey: "http-host-start-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     54,
		Port:   -1,
		Mode:   "httpHostServer",
		Status: false,
		Remark: "http-host-start",
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	if err := StartTask(task.Id); err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}
	if !runtimeTasks.Contains(task.Id) {
		t.Fatalf("runtimeTasks should contain http host task %d after StartTask()", task.Id)
	}

	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !stored.Status {
		t.Fatal("stored task status = false, want true after StartTask()")
	}
}

func TestStopServerDoesNotClobberReplacementRuntime(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	client := &file.Client{
		Id:        41,
		VerifyKey: "stop-runtime-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     51,
		Mode:   "httpHostServer",
		Status: true,
		Remark: "stop-runtime",
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	replacement := &stubServerService{}
	closing := &replacingCloseService{
		replacement: replacement,
		onClose: func() {
			runtimeTasks.Store(task.Id, replacement)
		},
	}
	runtimeTasks.Store(task.Id, closing)

	if err := StopServer(task.Id); err != nil {
		t.Fatalf("StopServer() error = %v", err)
	}
	if closing.closeCalls.Load() != 1 {
		t.Fatalf("Close() calls = %d, want 1", closing.closeCalls.Load())
	}
	current, ok := runtimeTasks.Load(task.Id)
	if !ok || current != replacement {
		t.Fatalf("runtime task entry = %v, %v; want replacement service", current, ok)
	}
}

func TestStopServerRollsBackPersistedStatusOnCloseFailure(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	client := &file.Client{
		Id:        42,
		VerifyKey: "stop-failure-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     52,
		Mode:   "httpHostServer",
		Status: true,
		Remark: "stop-failure",
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	closeErr := errors.New("close failed")
	svc := &failingCloseService{closeErr: closeErr}
	runtimeTasks.Store(task.Id, svc)

	if err := StopServer(task.Id); !errors.Is(err, closeErr) {
		t.Fatalf("StopServer() error = %v, want %v", err, closeErr)
	}
	if svc.closeCalls.Load() != 1 {
		t.Fatalf("Close() calls = %d, want 1", svc.closeCalls.Load())
	}
	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if !stored.Status {
		t.Fatal("stored task status = false, want rollback to true on close failure")
	}
	current, ok := runtimeTasks.Load(task.Id)
	if !ok || current != svc {
		t.Fatalf("runtime task entry = %v, %v; want original failing service retained", current, ok)
	}
}

func TestStopServerKeepsPersistedStoppedStatusWhenTaskIsNotRunning(t *testing.T) {
	resetServerTestDB(t)
	resetServerRuntimeGlobals(t)

	client := &file.Client{
		Id:        43,
		VerifyKey: "stop-missing-runtime-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	task := &file.Tunnel{
		Id:     53,
		Mode:   "httpHostServer",
		Status: true,
		Remark: "stop-missing-runtime",
		Client: client,
		Target: &file.Target{},
		Flow:   &file.Flow{},
	}
	if err := file.GetDb().NewTask(task); err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}

	if err := StopServer(task.Id); !errors.Is(err, errTaskNotRunning) {
		t.Fatalf("StopServer() error = %v, want %v", err, errTaskNotRunning)
	}
	stored, err := file.GetDb().GetTask(task.Id)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if stored.Status {
		t.Fatal("stored task status = true, want persisted false when runtime task is already absent")
	}
}

func TestStopTaskRuntimeDoesNotClobberReplacementRuntime(t *testing.T) {
	resetServerRuntimeGlobals(t)

	replacement := &stubServerService{}
	closing := &replacingCloseService{
		replacement: replacement,
		onClose: func() {
			runtimeTasks.Store(61, replacement)
		},
	}
	runtimeTasks.Store(61, closing)

	stopTaskRuntime(61)

	if closing.closeCalls.Load() != 1 {
		t.Fatalf("Close() calls = %d, want 1", closing.closeCalls.Load())
	}
	current, ok := runtimeTasks.Load(61)
	if !ok || current != replacement {
		t.Fatalf("runtime task entry = %v, %v; want replacement service", current, ok)
	}
}

func TestRuntimeTasksUsesCurrentRunList(t *testing.T) {
	resetServerRuntimeGlobals(t)

	first := struct{}{}
	runtimeTasks.Store(1, first)
	if got, ok := RunList.Load(1); !ok || got != first {
		t.Fatal("runtimeTasks.Store() should write through to the current RunList")
	}

	RunList = sync.Map{}

	second := struct{}{}
	runtimeTasks.Store(2, second)
	if _, ok := RunList.Load(1); ok {
		t.Fatal("reset RunList should not retain entries from the previous runtime map")
	}
	if got, ok := runtimeTasks.Load(2); !ok || got != second {
		t.Fatal("runtimeTasks should follow the current RunList after reset")
	}

	runtimeTasks.Delete(2)
	if _, ok := RunList.Load(2); ok {
		t.Fatal("runtimeTasks.Delete() should remove the entry from the current RunList")
	}
}

func TestPingClientUsesRuntimeStateLinkOpener(t *testing.T) {
	oldState := runtimeState
	t.Cleanup(func() {
		runtimeState = oldState
	})

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})

	calls := 0
	runtimeState.sendLinkInfo = func(id int, link *conn.Link, tunnel *file.Tunnel) (net.Conn, error) {
		calls++
		if id != 7 {
			t.Fatalf("PingClient() id = %d, want 7", id)
		}
		if tunnel != nil {
			t.Fatalf("PingClient() tunnel = %+v, want nil", tunnel)
		}
		if link.ConnType != "ping" || link.RemoteAddr != "127.0.0.1" {
			t.Fatalf("PingClient() link = %+v", link)
		}
		if !link.Option.NeedAck || link.Option.Timeout != pingTimeout {
			t.Fatalf("PingClient() link options = %+v", link.Option)
		}
		return serverConn, nil
	}

	if got := PingClient(7, "127.0.0.1"); got < 0 {
		t.Fatalf("PingClient() = %d, want non-negative RTT", got)
	}
	if calls != 1 {
		t.Fatalf("PingClient() sendLinkInfo calls = %d, want 1", calls)
	}
}

func TestPingClientReturnsExpectedFallbacks(t *testing.T) {
	oldState := runtimeState
	t.Cleanup(func() {
		runtimeState = oldState
	})

	calls := 0
	runtimeState.sendLinkInfo = func(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
		calls++
		return nil, errors.New("boom")
	}

	if got := PingClient(0, "ignored"); got != 0 {
		t.Fatalf("PingClient(0) = %d, want 0", got)
	}
	if calls != 0 {
		t.Fatalf("PingClient(0) sendLinkInfo calls = %d, want 0", calls)
	}

	if got := PingClient(8, "127.0.0.1"); got != -1 {
		t.Fatalf("PingClient() error path = %d, want -1", got)
	}
	if calls != 1 {
		t.Fatalf("PingClient() error path sendLinkInfo calls = %d, want 1", calls)
	}
}

func TestTaskModeRegistryNew(t *testing.T) {
	registry := newTaskModeRegistry()
	deps := taskModeDependencies{}

	if got := registry.New(deps, nil); got != nil {
		t.Fatal("registry.New(nil, nil) should return nil")
	}

	tunnel := &file.Tunnel{Mode: "custom"}
	if got := registry.New(deps, tunnel); got != nil {
		t.Fatal("registry.New() should return nil when mode is not registered")
	}

	want := &stubServerService{}
	called := false
	runtimeBridge := &bridge.Bridge{}
	deps.Bridge = runtimeBridge
	registry.Register("custom", func(gotDeps taskModeDependencies, gotTunnel *file.Tunnel) proxy.Service {
		called = true
		if gotDeps.Bridge != runtimeBridge {
			t.Fatal("registry passed unexpected bridge dependency to factory")
		}
		if gotTunnel != tunnel {
			t.Fatal("registry passed unexpected tunnel to factory")
		}
		return want
	})

	got := registry.New(deps, tunnel)
	if !called {
		t.Fatal("registry should call registered factory")
	}
	if got != want {
		t.Fatal("registry should return factory result")
	}
}

func TestTaskModeRuntimeDependencies(t *testing.T) {
	runtimeBridge := &bridge.Bridge{}
	cache := index.NewAnyIntIndex()
	httpCfg := connection.HTTPRuntimeConfig{Port: 80, HTTPSPort: 443, HTTP3Port: 8443}
	runtime := taskModeRuntime{
		currentBridge: func() *bridge.Bridge { return runtimeBridge },
		currentHTTP: func() connection.HTTPRuntimeConfig {
			return httpCfg
		},
		proxyCache: func() *index.AnyIntIndex { return cache },
	}

	deps := runtime.Dependencies()
	if deps.Bridge != runtimeBridge {
		t.Fatalf("Dependencies() bridge = %+v, want %+v", deps.Bridge, runtimeBridge)
	}
	if deps.HTTP != httpCfg {
		t.Fatalf("Dependencies() http = %+v, want %+v", deps.HTTP, httpCfg)
	}
	if deps.ProxyCache != cache {
		t.Fatalf("Dependencies() proxy cache = %+v, want %+v", deps.ProxyCache, cache)
	}
}

func TestTaskModeRuntimeNewPassesDependenciesToFactory(t *testing.T) {
	runtimeBridge := &bridge.Bridge{}
	cache := index.NewAnyIntIndex()
	httpCfg := connection.HTTPRuntimeConfig{Port: 18080, HTTPSPort: 18443, HTTP3Port: 18444}
	tunnel := &file.Tunnel{Mode: "custom"}
	want := &stubServerService{}
	called := false
	runtime := taskModeRuntime{
		currentBridge: func() *bridge.Bridge { return runtimeBridge },
		currentHTTP: func() connection.HTTPRuntimeConfig {
			return httpCfg
		},
		proxyCache: func() *index.AnyIntIndex { return cache },
		newMode: func(deps taskModeDependencies, gotTunnel *file.Tunnel) proxy.Service {
			called = true
			if deps.Bridge != runtimeBridge {
				t.Fatalf("New() bridge = %+v, want %+v", deps.Bridge, runtimeBridge)
			}
			if deps.HTTP != httpCfg {
				t.Fatalf("New() http = %+v, want %+v", deps.HTTP, httpCfg)
			}
			if deps.ProxyCache != cache {
				t.Fatalf("New() cache = %+v, want %+v", deps.ProxyCache, cache)
			}
			if gotTunnel != tunnel {
				t.Fatalf("New() tunnel = %+v, want %+v", gotTunnel, tunnel)
			}
			return want
		},
	}

	got := runtime.New(tunnel)
	if !called {
		t.Fatal("New() should call injected mode builder")
	}
	if got != want {
		t.Fatal("New() should return injected mode builder result")
	}
}

type stubAddr string

func (a stubAddr) Network() string { return "stub" }
func (a stubAddr) String() string  { return string(a) }

type stubListener struct {
	closed chan struct{}
	once   sync.Once
}

func newStubListener() *stubListener {
	return &stubListener{closed: make(chan struct{})}
}

func (l *stubListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}

func (l *stubListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
	})
	return nil
}

func (l *stubListener) Addr() net.Addr {
	return stubAddr("stub-listener")
}

func TestServeHTTPListenersClosesPeersOnFirstExit(t *testing.T) {
	primary := newStubListener()
	secondary := newStubListener()
	expectedErr := errors.New("primary serve failed")

	err := serveHTTPListeners(
		httpServeTarget{
			listener: primary,
			serve: func(net.Listener) error {
				return expectedErr
			},
		},
		httpServeTarget{
			listener: secondary,
			serve: func(net.Listener) error {
				<-secondary.closed
				return net.ErrClosed
			},
		},
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("serveHTTPListeners() error = %v, want %v", err, expectedErr)
	}

	select {
	case <-primary.closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("primary listener was not closed after serve exit")
	}
	select {
	case <-secondary.closed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("secondary listener was not closed after peer serve exit")
	}
}

func TestServeHTTPListenersRejectsMissingTargets(t *testing.T) {
	if err := serveHTTPListeners(); !errors.Is(err, errNoServeTarget) {
		t.Fatalf("serveHTTPListeners() error = %v, want %v", err, errNoServeTarget)
	}
}

func TestSetWebHandlerUpdatesDynamicHandler(t *testing.T) {
	first := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first"))
	})
	second := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("second"))
	})

	SetWebHandler(first)
	t.Cleanup(func() {
		SetWebHandler(http.NotFoundHandler())
	})

	dispatcher := dynamicWebHandler{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	dispatcher.ServeHTTP(recorder, request)
	if body := recorder.Body.String(); body != "first" {
		t.Fatalf("dynamicWebHandler first response = %q, want %q", body, "first")
	}

	SetWebHandler(second)
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	dispatcher.ServeHTTP(recorder, request)
	if body := recorder.Body.String(); body != "second" {
		t.Fatalf("dynamicWebHandler second response = %q, want %q", body, "second")
	}
}

func TestNewWebServerWithRootsUsesInjectedProviders(t *testing.T) {
	webCfg := connection.WebRuntimeConfig{IP: "127.0.0.2", Port: 18888}
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{OpenSSL: true},
	}

	server := NewWebServerWithRoots(nil, func() connection.WebRuntimeConfig {
		return webCfg
	}, func() *servercfg.Snapshot {
		return cfg
	})

	if got := server.currentWebRuntime(); got != webCfg {
		t.Fatalf("currentWebRuntime() = %+v, want %+v", got, webCfg)
	}
	if got := server.currentConfig(); got != cfg {
		t.Fatalf("currentConfig() = %+v, want %+v", got, cfg)
	}
}

func TestNewWebServerUsesCurrentGlobalRoots(t *testing.T) {
	oldWebRuntimeRoot := currentWebRuntimeRoot
	oldConfigRoot := currentWebConfigRoot
	t.Cleanup(func() {
		currentWebRuntimeRoot = oldWebRuntimeRoot
		currentWebConfigRoot = oldConfigRoot
	})

	firstWeb := connection.WebRuntimeConfig{IP: "127.0.0.3", Port: 10080}
	secondWeb := connection.WebRuntimeConfig{IP: "127.0.0.4", Port: 10443}
	firstCfg := &servercfg.Snapshot{Web: servercfg.WebConfig{OpenSSL: false}}
	secondCfg := &servercfg.Snapshot{Web: servercfg.WebConfig{OpenSSL: true}}

	currentWeb := firstWeb
	currentCfg := firstCfg
	currentWebRuntimeRoot = func() connection.WebRuntimeConfig {
		return currentWeb
	}
	currentWebConfigRoot = func() *servercfg.Snapshot {
		return currentCfg
	}

	server := NewWebServer(nil)
	if got := server.currentWebRuntime(); got != firstWeb {
		t.Fatalf("currentWebRuntime() = %+v, want %+v", got, firstWeb)
	}
	if got := server.currentConfig(); got != firstCfg {
		t.Fatalf("currentConfig() = %+v, want %+v", got, firstCfg)
	}

	currentWeb = secondWeb
	currentCfg = secondCfg
	if got := server.currentWebRuntime(); got != secondWeb {
		t.Fatalf("currentWebRuntime() after root change = %+v, want %+v", got, secondWeb)
	}
	if got := server.currentConfig(); got != secondCfg {
		t.Fatalf("currentConfig() after root change = %+v, want %+v", got, secondCfg)
	}
}

func TestWebServerCloseOnlyClearsOwnedVirtualListener(t *testing.T) {
	original := tool.WebServerListener
	t.Cleanup(func() {
		if tool.WebServerListener != nil {
			_ = tool.WebServerListener.Close()
		}
		tool.WebServerListener = original
	})

	oldListener := conn.NewVirtualListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080})
	newListener := conn.NewVirtualListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8081})
	tool.WebServerListener = newListener

	server := &WebServer{virtualListener: oldListener}
	if err := server.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if tool.WebServerListener != newListener {
		t.Fatal("Close() should not clear a newer global web listener")
	}

	virtualConn, err := newListener.DialVirtual("127.0.0.1:12345")
	if err != nil {
		t.Fatalf("new listener should remain usable after old Close(): %v", err)
	}
	_ = virtualConn.Close()

	if _, err := oldListener.DialVirtual("127.0.0.1:12345"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("old listener DialVirtual() error = %v, want %v", err, net.ErrClosed)
	}
}
