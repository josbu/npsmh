package proxy

import (
	"errors"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

type proxyRuntimeContextGlobalStub struct{}

func (proxyRuntimeContextGlobalStub) AllowsSourceAddr(string) bool { return true }

type proxyUserRuntimeStoreStub struct {
	user    *file.User
	clients []*file.Client
}

func (s proxyUserRuntimeStoreStub) GetUser(id int) (*file.User, error) {
	if s.user != nil && s.user.Id == id {
		return s.user, nil
	}
	return nil, errors.New("not found")
}

func (s proxyUserRuntimeStoreStub) GetClientsByUserId(userID int) []*file.Client {
	if s.user == nil || s.user.Id != userID {
		return nil
	}
	return s.clients
}

type proxyUserRuntimeDBStub struct {
	user *file.User
}

func (d proxyUserRuntimeDBStub) GetUser(id int) (*file.User, error) {
	if d.user != nil && d.user.Id == id {
		return d.user, nil
	}
	return nil, errors.New("not found")
}

type proxyRateLimiterUserQuotaStub struct {
	limiter *rate.Rate
}

func (s proxyRateLimiterUserQuotaStub) Check(*file.Client, int64) error {
	return nil
}

func (s proxyRateLimiterUserQuotaStub) RateLimiter(*file.Client) *rate.Rate {
	return s.limiter
}

func TestProxyRuntimeContextWiresSharedUserDependencies(t *testing.T) {
	user := &file.User{
		Id:        5,
		Status:    1,
		RateLimit: 64,
		TotalFlow: &file.Flow{},
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{Id: 9, UserId: user.Id, Flow: &file.Flow{}}
	users := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{user: user, clients: []*file.Client{client}}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{}
		},
	}

	ctx := newProxyRuntimeContextWith(users, func() proxyGlobalAccessSource {
		return proxyRuntimeContextGlobalStub{}
	})

	if resolved := ctx.users.Resolve(client); resolved != user {
		t.Fatalf("Users().Resolve() = %+v, want %+v", resolved, user)
	}
	if resolved := ctx.accessPolicy.users.Resolve(client); resolved != user {
		t.Fatalf("accessPolicy.users.Resolve() = %+v, want %+v", resolved, user)
	}
	if limiter := ctx.userQuota.RateLimiter(client); limiter == nil || limiter.Limit() != 64*1024 {
		t.Fatalf("UserQuota().RateLimiter() = %+v, want limit %d", limiter, 64*1024)
	}
	if limiter := ctx.rateLimiters.UserRate(client); limiter == nil || limiter.Limit() != 64*1024 {
		t.Fatalf("RateLimiters().UserRate() = %+v, want limit %d", limiter, 64*1024)
	}
	if err := ctx.userQuota.Check(client, 0); err != nil {
		t.Fatalf("UserQuota().Check() unexpected error = %v", err)
	}
}

func TestProxyRuntimeContextDefaultBuildersArePresent(t *testing.T) {
	ctx := newProxyRuntimeContext()

	if ctx.users.currentStore == nil || ctx.users.currentDB == nil {
		t.Fatal("Users() should expose default store/db builders")
	}
	if ctx.accessPolicy.currentGlobal == nil {
		t.Fatal("AccessPolicy() should expose default global builder")
	}
	if ctx.rateLimiters.userQuota == nil {
		t.Fatal("RateLimiters() should be wired to a user quota provider")
	}
}

func TestProxyClientUserRuntimeResolveAndClientsByUserID(t *testing.T) {
	user := &file.User{Id: 7}
	client := &file.Client{Id: 11, UserId: 7}
	runtime := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{user: user, clients: []*file.Client{client}}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{}
		},
	}

	resolved := runtime.Resolve(client)
	if resolved != user {
		t.Fatalf("Resolve() = %+v, want %+v", resolved, user)
	}
	if cached := client.OwnerUser(); cached != user {
		t.Fatalf("Resolve() should cache owner user, got %+v", cached)
	}

	clients := runtime.ClientsByUserID(user.Id)
	if len(clients) != 1 || clients[0] != client {
		t.Fatalf("ClientsByUserID() = %+v, want [%+v]", clients, client)
	}
}

func TestProxyClientUserRuntimeFallsBackToDB(t *testing.T) {
	user := &file.User{Id: 9}
	client := &file.Client{Id: 12, UserId: 9}
	runtime := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{user: user}
		},
	}

	if resolved := runtime.Resolve(client); resolved != user {
		t.Fatalf("Resolve() fallback = %+v, want %+v", resolved, user)
	}
}

func TestProxyClientUserRuntimeResolveUsesOwnerUserIDFallback(t *testing.T) {
	user := &file.User{Id: 15}
	client := &file.Client{Id: 12, OwnerUserID: 15}
	runtime := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return proxyUserRuntimeStoreStub{user: user}
		},
		currentDB: func() proxyUserLookupDB {
			return proxyUserRuntimeDBStub{}
		},
	}

	if resolved := runtime.Resolve(client); resolved != user {
		t.Fatalf("Resolve() = %+v, want %+v", resolved, user)
	}
}

func TestProxyUserQuotaRuntimeCheckRejectsConnectionLimitAcrossUserClients(t *testing.T) {
	user := &file.User{
		Id:             3,
		Status:         1,
		TotalFlow:      &file.Flow{},
		MaxConnections: 2,
		NowConn:        2,
	}
	file.InitializeUserRuntime(user)
	clientA := &file.Client{Id: 1, UserId: user.Id, Flow: &file.Flow{}, NowConn: 1}
	clientB := &file.Client{Id: 2, UserId: user.Id, Flow: &file.Flow{}, NowConn: 1}
	runtime := proxyUserQuotaRuntime{
		users: proxyClientUserRuntime{
			currentStore: func() proxyUserLookupStore {
				return proxyTestStore{
					user:    user,
					clients: []*file.Client{clientA, clientB},
				}
			},
		},
	}

	err := runtime.Check(clientA, time.Now().Unix())
	if err != errProxyConnectionLimit {
		t.Fatalf("Check() error = %v, want %v", err, errProxyConnectionLimit)
	}
}

func TestProxyUserQuotaRuntimeRateLimiterUsesResolvedUser(t *testing.T) {
	user := &file.User{
		Id:        4,
		Status:    1,
		RateLimit: 128,
		TotalFlow: &file.Flow{},
	}
	file.InitializeUserRuntime(user)
	client := &file.Client{Id: 9, UserId: user.Id, Flow: &file.Flow{}}
	runtime := proxyUserQuotaRuntime{
		users: proxyClientUserRuntime{
			currentStore: func() proxyUserLookupStore {
				return proxyTestStore{
					user:    user,
					clients: []*file.Client{client},
				}
			},
		},
	}

	limiter := runtime.RateLimiter(client)
	if limiter == nil {
		t.Fatal("RateLimiter() = nil, want user limiter")
	}
	if limiter.Limit() != 128*1024 {
		t.Fatalf("RateLimiter() limit = %d, want %d", limiter.Limit(), 128*1024)
	}
}

func TestProxyClientLifecycleRuntimeCheckLifecycleUsesEffectiveFields(t *testing.T) {
	runtime := proxyClientLifecycleRuntime{}

	t.Run("expire_at is enforced", func(t *testing.T) {
		client := &file.Client{
			ExpireAt: time.Now().Add(-time.Second).Unix(),
			Flow:     &file.Flow{},
		}
		err := runtime.CheckLifecycle(client, time.Now().Unix())
		if err != errProxyServiceAccessExpired {
			t.Fatalf("CheckLifecycle() error = %v, want %v", err, errProxyServiceAccessExpired)
		}
	})

	t.Run("flow_limit bytes are enforced", func(t *testing.T) {
		client := &file.Client{
			FlowLimit: 1024,
			Flow: &file.Flow{
				ExportFlow: 1024,
				InletFlow:  1,
			},
		}
		err := runtime.CheckLifecycle(client, time.Now().Unix())
		if err != errProxyTrafficLimitExceeded {
			t.Fatalf("CheckLifecycle() error = %v, want %v", err, errProxyTrafficLimitExceeded)
		}
	})
}

func TestProxyClientLifecycleRuntimeAcquireConnection(t *testing.T) {
	runtime := proxyClientLifecycleRuntime{}

	t.Run("increments connection count", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 2, NowConn: 0}
		err := runtime.AcquireConnection(client)
		if err != nil {
			t.Fatalf("AcquireConnection() unexpected error = %v", err)
		}
		if client.NowConn != 1 {
			t.Fatalf("client.NowConn = %d, want 1", client.NowConn)
		}
	})

	t.Run("rejects at connection limit", func(t *testing.T) {
		client := &file.Client{Flow: &file.Flow{}, MaxConn: 1, NowConn: 1}
		err := runtime.AcquireConnection(client)
		if err != errProxyConnectionLimit {
			t.Fatalf("AcquireConnection() error = %v, want %v", err, errProxyConnectionLimit)
		}
	})
}

func TestProxyRateLimitRuntimeServiceIncludesClientAndUserRates(t *testing.T) {
	clientRate := rate.NewRate(64 * 1024)
	clientRate.Start()
	userRate := rate.NewRate(128 * 1024)
	userRate.Start()
	runtime := proxyRateLimitRuntime{
		userQuota: proxyRateLimiterUserQuotaStub{limiter: userRate},
	}

	limiter := runtime.Service(&file.Client{Rate: clientRate}, nil, nil)
	if _, ok := limiter.(*rate.HierarchicalLimiter); !ok {
		t.Fatalf("Service() type = %T, want *rate.HierarchicalLimiter", limiter)
	}
}

func TestProxyRateLimitRuntimeServiceSkipsDisabledLimiters(t *testing.T) {
	clientRate := rate.NewRate(0)
	clientRate.Start()
	runtime := proxyRateLimitRuntime{
		userQuota: proxyRateLimiterUserQuotaStub{},
	}

	limiter := runtime.Service(&file.Client{Rate: clientRate}, nil, nil)
	if limiter != nil {
		t.Fatalf("Service() = %#v, want nil", limiter)
	}
}

func TestProxyRateLimitRuntimeServiceCollapsesSingleEnabledLimiter(t *testing.T) {
	clientRate := rate.NewRate(64 * 1024)
	clientRate.Start()
	runtime := proxyRateLimitRuntime{
		userQuota: proxyRateLimiterUserQuotaStub{},
	}

	limiter := runtime.Service(&file.Client{Rate: clientRate}, nil, nil)
	if limiter != clientRate {
		t.Fatalf("Service() = %#v, want direct client rate pointer %#v", limiter, clientRate)
	}
}

func TestNewTunnelModeServerUsesCurrentGlobalLocalProxyProvider(t *testing.T) {
	loadProxyRuntimeTestConfig(t, map[string]any{
		"feature": map[string]any{
			"allow_local_proxy": false,
		},
	})

	server := NewTunnelModeServer(ProcessTunnel, &noCallServerBridge{}, &file.Tunnel{Flow: &file.Flow{}})
	if server.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = true, want false from initial config")
	}

	loadProxyRuntimeTestConfig(t, map[string]any{
		"feature": map[string]any{
			"allow_local_proxy": true,
		},
	})
	if !server.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = false, want true after global config change")
	}
}

func TestNewUdpModeServerUsesCurrentGlobalLocalProxyProvider(t *testing.T) {
	loadProxyRuntimeTestConfig(t, map[string]any{
		"feature": map[string]any{
			"allow_local_proxy": false,
		},
	})

	server := NewUdpModeServer(&noCallServerBridge{}, &file.Tunnel{})
	if server.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = true, want false from initial config")
	}

	loadProxyRuntimeTestConfig(t, map[string]any{
		"feature": map[string]any{
			"allow_local_proxy": true,
		},
	})
	if !server.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = false, want true after global config change")
	}
}

func TestNewSecretServerWithRuntimeRootAndLocalProxyAllowedUsesInjectedProvider(t *testing.T) {
	server := NewSecretServerWithRuntimeRootAndLocalProxyAllowed(
		func() proxyRuntimeContext { return proxyRuntimeContext{} },
		func() bool { return true },
		&noCallServerBridge{},
		&file.Tunnel{},
		false,
	)
	if !server.LocalProxyAllowed() {
		t.Fatal("LocalProxyAllowed() = false, want injected provider result")
	}
}

func TestProxyRateLimitRuntimeBridgeUsesClientAndUserOnly(t *testing.T) {
	clientRate := rate.NewRate(64 * 1024)
	clientRate.Start()
	userRate := rate.NewRate(128 * 1024)
	userRate.Start()
	tunnelRate := rate.NewRate(256 * 1024)
	tunnelRate.Start()
	runtime := proxyRateLimitRuntime{
		userQuota: proxyRateLimiterUserQuotaStub{limiter: userRate},
	}

	bridgeLimiter := runtime.Bridge(&file.Client{Rate: clientRate})
	if _, ok := bridgeLimiter.(*rate.HierarchicalLimiter); !ok {
		t.Fatalf("Bridge() type = %T, want *rate.HierarchicalLimiter", bridgeLimiter)
	}

	serviceLimiter := runtime.Service(&file.Client{Rate: clientRate}, &file.Tunnel{Rate: tunnelRate}, nil)
	if _, ok := serviceLimiter.(*rate.HierarchicalLimiter); !ok {
		t.Fatalf("Service() type = %T, want *rate.HierarchicalLimiter", serviceLimiter)
	}
}

func TestProxyRateLimitRuntimeBridgeCollapsesSingleEnabledLimiter(t *testing.T) {
	userRate := rate.NewRate(128 * 1024)
	userRate.Start()
	runtime := proxyRateLimitRuntime{
		userQuota: proxyRateLimiterUserQuotaStub{limiter: userRate},
	}

	limiter := runtime.Bridge(&file.Client{})
	if limiter != userRate {
		t.Fatalf("Bridge() = %#v, want direct user rate pointer %#v", limiter, userRate)
	}
}
