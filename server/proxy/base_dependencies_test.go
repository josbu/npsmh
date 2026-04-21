package proxy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
)

type baseBridgeRuntimeStub struct {
	openCalls    int
	processCalls int
	isServer     bool
	gotClientID  int
	gotLink      *conn.Link
	gotTask      *file.Tunnel
	gotProcess   string
	err          error
	conn         net.Conn
}

type typedNilNetBridgeStub struct{}

func (s *baseBridgeRuntimeStub) SendLinkInfo(clientID int, link *conn.Link, task *file.Tunnel) (net.Conn, error) {
	s.openCalls++
	s.gotClientID = clientID
	s.gotLink = link
	s.gotTask = task
	return s.conn, s.err
}

func (s *baseBridgeRuntimeStub) IsServer() bool { return s.isServer }

func (s *baseBridgeRuntimeStub) CliProcess(_ *conn.Conn, tunnelType string) {
	s.processCalls++
	s.gotProcess = tunnelType
}

func (s *typedNilNetBridgeStub) SendLinkInfo(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
	panic("unexpected SendLinkInfo call on typed nil bridge")
}

func (s *typedNilNetBridgeStub) IsServer() bool {
	panic("unexpected IsServer call on typed nil bridge")
}

func (s *typedNilNetBridgeStub) CliProcess(*conn.Conn, string) {
	panic("unexpected CliProcess call on typed nil bridge")
}

type proxyTransportPolicyStub struct {
	sourceDenied bool
	destDenied   bool
}

func (s proxyTransportPolicyStub) IsClientSourceAccessDenied(*file.Client, *file.Tunnel, string) bool {
	return s.sourceDenied
}

func (s proxyTransportPolicyStub) IsClientDestinationAccessDenied(*file.Client, *file.Tunnel, string) bool {
	return s.destDenied
}

type proxyTransportPolicyRecorder struct {
	gotSource string
	gotTarget string
}

func (s *proxyTransportPolicyRecorder) IsClientSourceAccessDenied(_ *file.Client, _ *file.Tunnel, source string) bool {
	s.gotSource = source
	return false
}

func (s *proxyTransportPolicyRecorder) IsClientDestinationAccessDenied(_ *file.Client, _ *file.Tunnel, target string) bool {
	s.gotTarget = target
	return false
}

type proxyCountingLimiter struct {
	getCalls    int64
	returnCalls int64
	bytes       int64
}

func (l *proxyCountingLimiter) Get(size int64) {
	l.getCalls++
	l.bytes += size
}

func (l *proxyCountingLimiter) ReturnBucket(size int64) {
	l.returnCalls++
	l.bytes -= size
}

type proxyDeniedGlobalSourceStub struct{}

func (proxyDeniedGlobalSourceStub) AllowsSourceAddr(string) bool { return false }

func loadProxyRuntimeTestConfig(t *testing.T, cfg map[string]any) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "nps.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := servercfg.Load(path); err != nil {
		t.Fatalf("load config: %v", err)
	}

	t.Cleanup(func() {
		restorePath := filepath.Join(dir, "restore.json")
		if err := os.WriteFile(restorePath, []byte("{}"), 0o600); err != nil {
			t.Errorf("write restore config: %v", err)
			return
		}
		if err := servercfg.Load(restorePath); err != nil {
			t.Errorf("restore config: %v", err)
		}
	})
}

func TestProxyBaseDependenciesCaptureCurrentBaseServerState(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	initialTask := &file.Tunnel{Id: 1}
	updatedTask := &file.Tunnel{Id: 2}
	bridge := &baseBridgeRuntimeStub{isServer: true, conn: serverConn}
	base := NewBaseServer(bridge, initialTask)
	base.ErrorContent = []byte("initial")
	deps := newProxyBaseDependencies(base)

	base.Task = updatedTask
	base.ErrorContent = []byte("updated")

	if deps.task != initialTask {
		t.Fatalf("deps.task = %+v, want %+v", deps.task, initialTask)
	}
	if got := string(deps.errorContent); got != "initial" {
		t.Fatalf("deps.errorContent = %q, want %q", got, "initial")
	}

	updatedDeps := newProxyBaseDependencies(base)
	if updatedDeps.task != updatedTask {
		t.Fatalf("updatedDeps.task = %+v, want %+v", updatedDeps.task, updatedTask)
	}
	if got := string(updatedDeps.errorContent); got != "updated" {
		t.Fatalf("updatedDeps.errorContent = %q, want %q", got, "updated")
	}
	if !deps.BridgeIsServer() {
		t.Fatal("BridgeIsServer() = false, want true")
	}
	if deps.locker == nil {
		t.Fatal("Locker() = nil, want mutex-backed locker")
	}
	if deps.linkOpener == nil || deps.serverRole == nil || deps.clientProcessor == nil {
		t.Fatal("dependency object should expose bridge facets")
	}
	if deps.LocalProxyAllowed() != base.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() should delegate to BaseServer method")
	}

	link := conn.NewLink("tcp", "127.0.0.1:80", false, false, "127.0.0.1", false)
	gotConn, err := deps.OpenBridgeLink(7, link, updatedTask)
	if err != nil {
		t.Fatalf("OpenBridgeLink() error = %v", err)
	}
	if gotConn != serverConn {
		t.Fatalf("OpenBridgeLink() conn = %+v, want %+v", gotConn, serverConn)
	}
}

func TestProxyBaseDependenciesZeroValue(t *testing.T) {
	deps := newProxyBaseDependencies(nil)

	if deps.task != nil || deps.errorContent != nil {
		t.Fatalf("zero dependencies should not expose task/error, got task=%+v err=%v", deps.task, deps.errorContent)
	}
	if deps.linkOpener != nil || deps.serverRole != nil || deps.clientProcessor != nil {
		t.Fatal("zero dependencies should not expose bridge facets")
	}
	if deps.locker != nil {
		t.Fatal("zero dependencies should not expose locker")
	}
	if deps.LocalProxyAllowed() {
		t.Fatal("zero dependencies should report local proxy disabled")
	}
	if _, err := deps.OpenBridgeLink(1, nil, nil); err != errProxyBridgeUnavailable {
		t.Fatalf("OpenBridgeLink() error = %v, want %v", err, errProxyBridgeUnavailable)
	}
}

func TestBaseServerTaskAndErrorContentAccessors(t *testing.T) {
	initialTask := &file.Tunnel{Id: 1}
	updatedTask := &file.Tunnel{Id: 2}
	base := &BaseServer{
		Task:         initialTask,
		ErrorContent: []byte("initial"),
	}

	if base.CurrentTask() != initialTask {
		t.Fatalf("CurrentTask() = %+v, want %+v", base.CurrentTask(), initialTask)
	}
	if got := string(base.CurrentErrorContent()); got != "initial" {
		t.Fatalf("CurrentErrorContent() = %q, want %q", got, "initial")
	}

	base.SetTask(updatedTask)
	base.SetErrorContent([]byte("updated"))

	if base.Task != updatedTask || base.CurrentTask() != updatedTask {
		t.Fatalf("task state mismatch: field=%+v accessor=%+v want %+v", base.Task, base.CurrentTask(), updatedTask)
	}
	if got := string(base.ErrorContent); got != "updated" {
		t.Fatalf("ErrorContent field = %q, want %q", got, "updated")
	}
	if got := string(base.CurrentErrorContent()); got != "updated" {
		t.Fatalf("CurrentErrorContent() = %q, want %q", got, "updated")
	}
}

func TestBaseServerAccessPolicyMethodsMatchCompatibilityFunctions(t *testing.T) {
	user := &file.User{
		Id:           3,
		Status:       1,
		TotalFlow:    &file.Flow{},
		DestAclMode:  file.AclWhitelist,
		DestAclRules: "full:db.internal.example",
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:     7,
		UserId: 3,
		Flow:   &file.Flow{},
	}
	client.BindOwnerUser(user)
	task := &file.Tunnel{
		Id:           8,
		Mode:         "mixProxy",
		DestAclMode:  file.AclBlacklist,
		DestAclRules: "full:blocked.internal.example",
	}
	task.CompileDestACL()
	base := &BaseServer{}

	if got, want := base.HasActiveDestinationACL(client, task), HasActiveDestinationACL(client, task); got != want {
		t.Fatalf("HasActiveDestinationACL() = %v, want %v", got, want)
	}
	if got, want := base.IsClientDestinationAccessDenied(client, task, "blocked.internal.example:443"), IsClientDestinationAccessDenied(client, task, "blocked.internal.example:443"); got != want {
		t.Fatalf("IsClientDestinationAccessDenied() = %v, want %v", got, want)
	}
}

func TestBaseServerBridgeRuntimeFacets(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	task := &file.Tunnel{Id: 9}
	bridge := &baseBridgeRuntimeStub{isServer: true, conn: serverConn}
	base := NewBaseServer(bridge, task)

	link := conn.NewLink("tcp", "127.0.0.1:80", false, false, "127.0.0.1", false)
	gotConn, err := base.OpenBridgeLink(7, link, task)
	if err != nil {
		t.Fatalf("OpenBridgeLink() error = %v", err)
	}
	if gotConn != serverConn {
		t.Fatalf("OpenBridgeLink() conn = %+v, want %+v", gotConn, serverConn)
	}
	if bridge.openCalls != 1 || bridge.gotClientID != 7 || bridge.gotLink != link || bridge.gotTask != task {
		t.Fatalf("OpenBridgeLink() bridge calls = %+v", bridge)
	}
	if !base.BridgeIsServer() {
		t.Fatal("BridgeIsServer() = false, want true")
	}

	base.ProcessBridgeClient(conn.NewConn(clientConn), "quic")
	if bridge.processCalls != 1 || bridge.gotProcess != "quic" {
		t.Fatalf("ProcessBridgeClient() bridge calls = %+v", bridge)
	}
}

func TestBaseServerBridgeRuntimeUnavailable(t *testing.T) {
	base := &BaseServer{}
	link := conn.NewLink("tcp", "127.0.0.1:80", false, false, "127.0.0.1", false)
	if _, err := base.OpenBridgeLink(1, link, nil); !errors.Is(err, errProxyBridgeUnavailable) {
		t.Fatalf("OpenBridgeLink() error = %v, want errProxyBridgeUnavailable", err)
	}
	if base.BridgeIsServer() {
		t.Fatal("BridgeIsServer() = true, want false")
	}
	base.ProcessBridgeClient(nil, "tcp")
}

func TestBaseServerIgnoresTypedNilBridgeFacets(t *testing.T) {
	var bridge *typedNilNetBridgeStub
	base := NewBaseServer(bridge, &file.Tunnel{})
	link := conn.NewLink("tcp", "127.0.0.1:80", false, false, "127.0.0.1", false)

	if _, err := base.OpenBridgeLink(1, link, nil); !errors.Is(err, errProxyBridgeUnavailable) {
		t.Fatalf("OpenBridgeLink() error = %v, want %v", err, errProxyBridgeUnavailable)
	}
	if base.BridgeIsServer() {
		t.Fatal("BridgeIsServer() = true, want false")
	}

	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()
	base.ProcessBridgeClient(conn.NewConn(clientConn), "tcp")

	deps := newProxyBaseDependencies(base)
	if deps.linkOpener != nil || deps.serverRole != nil || deps.clientProcessor != nil {
		t.Fatalf("newProxyBaseDependencies() kept typed nil bridge facets: %+v", deps)
	}
	if _, err := deps.OpenBridgeLink(1, link, nil); !errors.Is(err, errProxyBridgeUnavailable) {
		t.Fatalf("deps.OpenBridgeLink() error = %v, want %v", err, errProxyBridgeUnavailable)
	}
	if deps.BridgeIsServer() {
		t.Fatal("deps.BridgeIsServer() = true, want false")
	}
	deps.ProcessBridgeClient(conn.NewConn(serverConn), "tcp")
}

func TestNewBaseServerWithRuntimeUsesInjectedRuntimeContext(t *testing.T) {
	user := &file.User{
		Id:        17,
		Status:    1,
		RateLimit: 64,
		TotalFlow: &file.Flow{},
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{Id: 7, UserId: user.Id, Flow: &file.Flow{}, Cnf: &file.Config{}}
	users := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{user: user, clients: []*file.Client{client}}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{}
		},
	}
	injectedRuntime := proxyRuntimeContext{
		accessPolicy: proxyAccessPolicyRuntime{
			users: users,
			currentGlobal: func() proxyGlobalAccessSource {
				return proxyDeniedGlobalSourceStub{}
			},
		},
		users:           users,
		userQuota:       proxyUserQuotaRuntime{users: users},
		clientLifecycle: proxyClientLifecycleRuntime{},
		rateLimiters: proxyRateLimitRuntime{
			userQuota: proxyUserQuotaRuntime{users: users},
		},
	}

	base := NewBaseServerWithRuntime(injectedRuntime, &baseBridgeRuntimeStub{}, &file.Tunnel{Flow: &file.Flow{}})
	ctx := newProxyServiceContext(base)

	if !base.IsClientSourceAccessDenied(nil, nil, "127.0.0.1:3000") {
		t.Fatal("IsClientSourceAccessDenied() should use injected global source policy")
	}
	if ctx.limit.userQuota == nil || ctx.limit.clientLife == nil || ctx.limit.rateLimits == nil {
		t.Fatalf("service context limit runtime should be wired from injected runtime, got %+v", ctx.limit)
	}
	if ctx.transport.policy == nil {
		t.Fatal("service context transport policy should come from injected runtime")
	}
	if limiter := ctx.transport.serviceRateLimiter(client, nil, nil); limiter == nil {
		t.Fatal("service context should use injected rate limiter runtime")
	}
	if limiter := ctx.transport.bridgeRateLimiter(client); limiter == nil {
		t.Fatal("bridge rate limiter should use injected runtime")
	}
}

func TestBaseServerRuntimeWrappersUseInjectedRuntimeContext(t *testing.T) {
	user := &file.User{
		Id:        17,
		Status:    1,
		RateLimit: 64,
		TotalFlow: &file.Flow{},
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{
		Id:      7,
		UserId:  user.Id,
		Flow:    &file.Flow{},
		MaxConn: 2,
		Cnf:     &file.Config{},
	}
	users := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{user: user, clients: []*file.Client{client}}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{}
		},
	}
	injectedRuntime := proxyRuntimeContext{
		accessPolicy: proxyAccessPolicyRuntime{
			users: users,
			currentGlobal: func() proxyGlobalAccessSource {
				return proxyDeniedGlobalSourceStub{}
			},
		},
		users:           users,
		userQuota:       proxyUserQuotaRuntime{users: users},
		clientLifecycle: proxyClientLifecycleRuntime{},
		rateLimiters: proxyRateLimitRuntime{
			userQuota: proxyUserQuotaRuntime{users: users},
		},
	}
	collector := &proxyRouteRuntimeBindingCollectorStub{}
	base := NewBaseServerWithRuntime(injectedRuntime, &baseBridgeRuntimeStub{}, &file.Tunnel{Flow: &file.Flow{}})
	base.linkOpener = proxyRouteRuntimeBindingBridgeLinkOpenerStub{
		proxyRouteRuntimeBindingCollectorStub: collector,
	}

	limit := base.limitRuntime()
	if limit.userQuota == nil || limit.clientLife == nil || limit.rateLimits == nil {
		t.Fatalf("limitRuntime() = %+v, want injected runtime providers", limit)
	}
	if limiter := base.ServiceRateLimiter(client, nil, nil); limiter == nil {
		t.Fatal("ServiceRateLimiter() should use injected limit runtime")
	}
	if limiter := base.BridgeRateLimiter(client); limiter == nil {
		t.Fatal("BridgeRateLimiter() should use injected limit runtime")
	}

	transport := base.transportRuntime()
	if transport.policy == nil {
		t.Fatal("transportRuntime() policy should come from injected runtime")
	}
	if wantCollector := routeStatsCollectorFromLinkOpener(base.linkOpener); transport.routeStats != wantCollector {
		t.Fatalf("transportRuntime() routeStats = %#v, want %#v", transport.routeStats, wantCollector)
	}
	if limiter := transport.serviceRateLimiter(client, nil, nil); limiter == nil {
		t.Fatal("transportRuntime() service limiter should use injected limit runtime")
	}
	if limiter := transport.bridgeRateLimiter(client); limiter == nil {
		t.Fatal("transportRuntime() bridge limiter should use injected limit runtime")
	}
}

func TestNewBaseServerUsesCurrentGlobalRuntimeContext(t *testing.T) {
	oldRuntime := runtimeProxy
	runtimeProxy = newProxyRuntimeContext()
	t.Cleanup(func() {
		runtimeProxy = oldRuntime
	})

	base := NewBaseServer(&baseBridgeRuntimeStub{}, &file.Tunnel{Flow: &file.Flow{}})

	runtimeProxy = proxyRuntimeContext{
		accessPolicy: proxyAccessPolicyRuntime{
			currentGlobal: func() proxyGlobalAccessSource {
				return proxyDeniedGlobalSourceStub{}
			},
		},
	}

	if !base.IsClientSourceAccessDenied(nil, nil, "127.0.0.1:3000") {
		t.Fatal("default BaseServer should keep following current global runtime context")
	}
}

func TestProxyServiceContextWiresBaseServerRuntimes(t *testing.T) {
	task := &file.Tunnel{Id: 7, Flow: &file.Flow{}}
	userRate := rate.NewRate(128 * 1024)
	userRate.Start()
	limit := proxyLimitRuntime{
		now: func() time.Time { return time.Unix(123, 0) },
		userQuota: proxyRateLimiterUserQuotaStub{
			limiter: userRate,
		},
		clientLife: proxyClientLifecycleRuntime{},
		rateLimits: proxyRateLimitRuntime{
			userQuota: proxyRateLimiterUserQuotaStub{
				limiter: userRate,
			},
		},
	}
	policy := proxyTransportPolicyStub{sourceDenied: true}
	base := &BaseServer{
		Task:         task,
		ErrorContent: []byte("detail"),
	}

	ctx := newProxyServiceContextWith(newProxyBaseDependencies(base), limit, policy)

	if got := string(ctx.auth.errorContent); got != "detail" {
		t.Fatalf("auth.errorContent = %q, want %q", got, "detail")
	}
	if ctx.flow.task != task || ctx.flow.locker == nil {
		t.Fatalf("flow = %+v, want task %+v with locker", ctx.flow, task)
	}
	if ctx.limit.now == nil {
		t.Fatal("limit.now should be wired")
	}
	if _, ok := ctx.transport.policy.(proxyTransportPolicyStub); !ok {
		t.Fatalf("transport.policy = %#v, want proxyTransportPolicyStub", ctx.transport.policy)
	}
	client := &file.Client{Flow: &file.Flow{}}
	serviceLimiter := ctx.transport.serviceRateLimiter(client, nil, nil)
	if serviceLimiter != userRate {
		t.Fatalf("transport.serviceRateLimiter() = %#v, want user rate pointer", serviceLimiter)
	}
	bridgeLimiter := ctx.transport.bridgeRateLimiter(client)
	if bridgeLimiter != userRate {
		t.Fatalf("transport.bridgeRateLimiter() = %#v, want user rate pointer", bridgeLimiter)
	}
}

func TestProxyServiceContextNilBaseServerIsZeroValue(t *testing.T) {
	ctx := newProxyServiceContext(nil)
	if ctx.auth.errorContent != nil {
		t.Fatalf("auth.errorContent = %v, want nil", ctx.auth.errorContent)
	}
	if ctx.flow.locker != nil || ctx.flow.task != nil {
		t.Fatalf("flow = %+v, want zero value", ctx.flow)
	}
	if ctx.transport.openBridgeLink != nil || ctx.transport.policy != nil {
		t.Fatalf("transport = %+v, want zero value", ctx.transport)
	}
	if ctx.limit.now != nil || ctx.limit.userQuota != nil || ctx.limit.clientLife != nil || ctx.limit.rateLimits != nil {
		t.Fatalf("limit = %+v, want zero value", ctx.limit)
	}
}

func TestTunnelProxyAuthPolicyRequiresAuth(t *testing.T) {
	cases := []struct {
		name string
		task *file.Tunnel
		want bool
	}{
		{
			name: "no credentials",
			task: &file.Tunnel{
				Client: &file.Client{Cnf: &file.Config{}},
			},
			want: false,
		},
		{
			name: "username only",
			task: &file.Tunnel{
				Client: &file.Client{Cnf: &file.Config{U: "demo"}},
			},
			want: true,
		},
		{
			name: "password only",
			task: &file.Tunnel{
				Client: &file.Client{Cnf: &file.Config{P: "secret"}},
			},
			want: true,
		},
		{
			name: "multi account only",
			task: &file.Tunnel{
				Client:       &file.Client{Cnf: &file.Config{}},
				MultiAccount: &file.MultiAccount{AccountMap: map[string]string{"worker": "p@ss"}},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tunnelProxyAuthPolicy(tc.task).RequiresAuth(); got != tc.want {
				t.Fatalf("tunnelProxyAuthPolicy().RequiresAuth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHostProxyAuthPolicyRequiresAuth(t *testing.T) {
	cases := []struct {
		name string
		host *file.Host
		want bool
	}{
		{
			name: "no credentials",
			host: &file.Host{
				Client: &file.Client{Cnf: &file.Config{}},
			},
			want: false,
		},
		{
			name: "username only",
			host: &file.Host{
				Client: &file.Client{Cnf: &file.Config{U: "demo"}},
			},
			want: true,
		},
		{
			name: "password only",
			host: &file.Host{
				Client: &file.Client{Cnf: &file.Config{P: "secret"}},
			},
			want: true,
		},
		{
			name: "user auth only",
			host: &file.Host{
				Client:   &file.Client{Cnf: &file.Config{}},
				UserAuth: &file.MultiAccount{AccountMap: map[string]string{"user": "token"}},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostProxyAuthPolicy(tc.host).RequiresAuth(); got != tc.want {
				t.Fatalf("hostProxyAuthPolicy().RequiresAuth() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProxyAuthPolicyCheckCredentials(t *testing.T) {
	cases := []struct {
		name   string
		policy proxyAuthPolicy
		user   string
		pass   string
		want   bool
	}{
		{
			name:   "username only",
			policy: newProxyAuthPolicy("demo", "", nil, nil),
			user:   "demo",
			pass:   "",
			want:   true,
		},
		{
			name:   "password only",
			policy: newProxyAuthPolicy("", "secret", nil, nil),
			user:   "",
			pass:   "secret",
			want:   true,
		},
		{
			name:   "multi account",
			policy: newProxyAuthPolicy("", "", &file.MultiAccount{AccountMap: map[string]string{"worker": "p@ss"}}, nil),
			user:   "worker",
			pass:   "p@ss",
			want:   true,
		},
		{
			name:   "wrong password",
			policy: newProxyAuthPolicy("demo", "", nil, nil),
			user:   "demo",
			pass:   "bad",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.CheckCredentials(tc.user, tc.pass); got != tc.want {
				t.Fatalf("CheckCredentials() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAuthAcceptsHalfConfiguredBasicCredentials(t *testing.T) {
	cases := []struct {
		name          string
		user          string
		pass          string
		authorization string
	}{
		{
			name:          "username only",
			user:          "demo",
			authorization: "demo:",
		},
		{
			name:          "password only",
			pass:          "secret",
			authorization: ":secret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &BaseServer{}
			r := httptest.NewRequest("GET", "http://example.com", nil)
			r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(tc.authorization)))
			if err := s.Auth(r, nil, tc.user, tc.pass, nil, nil); err != nil {
				t.Fatalf("Auth() error = %v, want nil", err)
			}
		})
	}
}

func TestBaseServerOpenClientLinkUsesTransportRuntimeDependencies(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	targetSide, targetPeer := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	defer func() { _ = targetSide.Close() }()
	defer func() { _ = targetPeer.Close() }()

	client := &file.Client{
		Id:   -1,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	task := &file.Tunnel{Id: 3}
	task.BindRuntimeOwner("node-a", &file.Tunnel{Target: &file.Target{TargetStr: "127.0.0.1:80"}})
	base := &BaseServer{}
	baseTransport := base.transportRuntime()
	baseTransport.localProxyAllowed = func() bool { return true }
	baseTransport.policy = proxyTransportPolicyStub{}
	baseTransport.openBridgeLink = func(id int, link *conn.Link, gotTask *file.Tunnel) (net.Conn, error) {
		if id != client.Id {
			t.Fatalf("OpenClientLink() client id = %d, want %d", id, client.Id)
		}
		if gotTask != task {
			t.Fatalf("OpenClientLink() task = %+v, want %+v", gotTask, task)
		}
		if link.ConnType != "tcp" || link.Host != "127.0.0.1:80" {
			t.Fatalf("OpenClientLink() link = %+v", link)
		}
		if !link.LocalProxy {
			t.Fatal("OpenClientLink() should preserve local proxy decision")
		}
		if link.Option.RouteUUID != "node-a" {
			t.Fatalf("OpenClientLink() route uuid = %q, want %q", link.Option.RouteUUID, "node-a")
		}
		return targetSide, nil
	}

	target, link, isLocal, err := baseTransport.OpenClientLink(
		conn.NewConn(serverSide),
		client,
		"127.0.0.1:80",
		"tcp",
		true,
		task,
	)
	if err != nil {
		t.Fatalf("OpenClientLink() error = %v", err)
	}
	if target != targetSide {
		t.Fatalf("OpenClientLink() target = %+v, want %+v", target, targetSide)
	}
	if link == nil || !isLocal {
		t.Fatalf("OpenClientLink() link=%+v isLocal=%v, want non-nil/true", link, isLocal)
	}
}

func TestBaseServerOpenClientLinkUsesResolvedRemoteAddrForPolicyAndLink(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	targetSide, targetPeer := net.Pipe()
	defer func() { _ = clientSide.Close() }()
	defer func() { _ = targetSide.Close() }()
	defer func() { _ = targetPeer.Close() }()

	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	task := &file.Tunnel{Id: 3, Flow: &file.Flow{}}
	recorder := &proxyTransportPolicyRecorder{}
	wantRemote := serverSide.RemoteAddr().String()

	baseTransport := (&BaseServer{}).transportRuntime()
	baseTransport.policy = recorder
	baseTransport.openBridgeLink = func(id int, link *conn.Link, gotTask *file.Tunnel) (net.Conn, error) {
		if link.RemoteAddr != wantRemote {
			t.Fatalf("OpenClientLink() link remote addr = %q, want %q", link.RemoteAddr, wantRemote)
		}
		if gotTask != task {
			t.Fatalf("OpenClientLink() task = %+v, want %+v", gotTask, task)
		}
		return targetSide, nil
	}

	target, link, isLocal, err := baseTransport.OpenClientLink(
		conn.NewConn(serverSide),
		client,
		"127.0.0.1:80",
		"tcp",
		false,
		task,
	)
	if err != nil {
		t.Fatalf("OpenClientLink() error = %v", err)
	}
	if target != targetSide || link == nil || isLocal {
		t.Fatalf("OpenClientLink() = (%+v, %+v, %v), want target/non-nil/false", target, link, isLocal)
	}
	if recorder.gotSource != wantRemote {
		t.Fatalf("IsClientSourceAccessDenied() source = %q, want %q", recorder.gotSource, wantRemote)
	}
	if recorder.gotTarget != "127.0.0.1:80" {
		t.Fatalf("IsClientDestinationAccessDenied() target = %q, want %q", recorder.gotTarget, "127.0.0.1:80")
	}
}

func TestBaseServerOpenBridgeLinkInheritsTaskRuntimeRouteUUID(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	task := &file.Tunnel{Id: 5}
	task.BindRuntimeOwner("node-b", &file.Tunnel{Target: &file.Target{TargetStr: "127.0.0.1:81"}})
	bridge := &baseBridgeRuntimeStub{conn: serverSide}
	base := &BaseServer{linkOpener: bridge}
	link := conn.NewLink("tcp", "127.0.0.1:81", false, false, "203.0.113.50:7000", false)

	target, err := base.OpenBridgeLink(9, link, task)
	if err != nil {
		t.Fatalf("OpenBridgeLink() error = %v", err)
	}
	if target != serverSide {
		t.Fatalf("OpenBridgeLink() target = %+v, want %+v", target, serverSide)
	}
	if bridge.gotLink == nil {
		t.Fatal("OpenBridgeLink() did not pass a link to the bridge opener")
	}
	if bridge.gotLink.Option.RouteUUID != "node-b" {
		t.Fatalf("OpenBridgeLink() route uuid = %q, want %q", bridge.gotLink.Option.RouteUUID, "node-b")
	}
	if link.Option.RouteUUID != "node-b" {
		t.Fatalf("OpenBridgeLink() should update original link route uuid to %q, got %q", "node-b", link.Option.RouteUUID)
	}
}

func TestBaseServerOpenClientLinkSkipsOpenerWhenPolicyDenies(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	task := &file.Tunnel{Id: 4}
	called := false
	baseTransport := (&BaseServer{}).transportRuntime()
	baseTransport.policy = proxyTransportPolicyStub{sourceDenied: true}
	baseTransport.openBridgeLink = func(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
		called = true
		return nil, nil
	}

	target, link, isLocal, err := baseTransport.OpenClientLink(
		conn.NewConn(serverSide),
		client,
		"127.0.0.1:80",
		"tcp",
		false,
		task,
	)
	if err != errProxyAccessDenied {
		t.Fatalf("OpenClientLink() error = %v, want errProxyAccessDenied", err)
	}
	if called {
		t.Fatal("OpenClientLink() should not call opener when policy denies access")
	}
	if target != nil || link != nil || isLocal {
		t.Fatalf("OpenClientLink() = (%+v, %+v, %v), want nil/nil/false", target, link, isLocal)
	}
}

func TestBaseServerOpenClientLinkUsesCurrentRuntimePolicyWhenTransportPolicyUnset(t *testing.T) {
	oldRuntime := currentProxyRuntimeRoot
	defer func() {
		currentProxyRuntimeRoot = oldRuntime
	}()

	rootCalls := 0
	currentProxyRuntimeRoot = func() proxyRuntimeContext {
		rootCalls++
		return proxyRuntimeContext{
			accessPolicy: proxyAccessPolicyRuntime{
				currentGlobal: func() proxyGlobalAccessSource {
					return proxyDeniedGlobalSourceStub{}
				},
			},
		}
	}

	serverSide, clientSide := net.Pipe()
	defer func() { _ = clientSide.Close() }()

	client := &file.Client{
		Id:   7,
		Cnf:  &file.Config{},
		Flow: &file.Flow{},
	}
	task := &file.Tunnel{Id: 4}
	called := false
	baseTransport := (&BaseServer{}).transportRuntime()
	baseTransport.openBridgeLink = func(int, *conn.Link, *file.Tunnel) (net.Conn, error) {
		called = true
		return nil, nil
	}

	target, link, isLocal, err := baseTransport.OpenClientLink(
		conn.NewConn(serverSide),
		client,
		"127.0.0.1:80",
		"tcp",
		false,
		task,
	)
	if err != errProxyAccessDenied {
		t.Fatalf("OpenClientLink() error = %v, want errProxyAccessDenied", err)
	}
	if called {
		t.Fatal("OpenClientLink() should not call opener when current runtime policy denies access")
	}
	if target != nil || link != nil || isLocal {
		t.Fatalf("OpenClientLink() = (%+v, %+v, %v), want nil/nil/false", target, link, isLocal)
	}
	if rootCalls != 1 {
		t.Fatalf("currentProxyRuntimeRoot() calls = %d, want 1", rootCalls)
	}
}

func TestWriteWithLimiterChargesFullWrite(t *testing.T) {
	limiter := &proxyCountingLimiter{}
	payload := []byte("hello")

	n, err := writeWithLimiter(limiter, payload, func(p []byte) (int, error) {
		return len(p), nil
	})
	if err != nil {
		t.Fatalf("writeWithLimiter() error = %v", err)
	}
	if n != len(payload) {
		t.Fatalf("writeWithLimiter() wrote %d bytes, want %d", n, len(payload))
	}
	if limiter.bytes != int64(len(payload)) || limiter.returnCalls != 0 {
		t.Fatalf("limiter state bytes=%d returnCalls=%d, want %d/0", limiter.bytes, limiter.returnCalls, len(payload))
	}
}

func TestWriteWithLimiterRefundsShortWrite(t *testing.T) {
	limiter := &proxyCountingLimiter{}
	payload := []byte("hello")

	n, err := writeWithLimiter(limiter, payload, func(p []byte) (int, error) {
		return 2, errors.New("short write")
	})
	if err == nil {
		t.Fatal("writeWithLimiter() error = nil, want short write error")
	}
	if n != 2 {
		t.Fatalf("writeWithLimiter() wrote %d bytes, want 2", n)
	}
	if limiter.bytes != 2 || limiter.returnCalls != 1 {
		t.Fatalf("limiter state bytes=%d returnCalls=%d, want 2/1", limiter.bytes, limiter.returnCalls)
	}
}

func TestObserveBufferedIngressRefundsLimiterOnObserverError(t *testing.T) {
	limiter := &proxyCountingLimiter{}
	wantErr := errors.New("observer failed")

	err := ObserveBufferedIngress([]byte("hello"), limiter, func(int64) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ObserveBufferedIngress() error = %v, want %v", err, wantErr)
	}
	if limiter.getCalls != 1 || limiter.returnCalls != 1 || limiter.bytes != 0 {
		t.Fatalf("limiter state getCalls=%d returnCalls=%d bytes=%d, want 1/1/0", limiter.getCalls, limiter.returnCalls, limiter.bytes)
	}
}

func TestNewBaseServerUsesCurrentGlobalLocalProxyProvider(t *testing.T) {
	loadProxyRuntimeTestConfig(t, map[string]any{
		"feature": map[string]any{
			"allow_local_proxy": false,
		},
	})

	base := NewBaseServer(&baseBridgeRuntimeStub{}, &file.Tunnel{})
	if base.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = true, want false from initial config")
	}

	loadProxyRuntimeTestConfig(t, map[string]any{
		"feature": map[string]any{
			"allow_local_proxy": true,
		},
	})
	if !base.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = false, want true after global config change")
	}
}

func TestNewBaseServerWithRuntimeRootAndLocalProxyAllowedUsesInjectedProvider(t *testing.T) {
	base := NewBaseServerWithRuntimeRootAndLocalProxyAllowed(
		func() proxyRuntimeContext { return proxyRuntimeContext{} },
		func() bool { return true },
		&baseBridgeRuntimeStub{},
		&file.Tunnel{},
	)
	if !base.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = false, want injected provider result")
	}
}
