package proxy

import (
	"sync/atomic"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
)

type proxyRuntimeContext struct {
	users           proxyClientUserRuntime
	accessPolicy    proxyAccessPolicyRuntime
	userQuota       proxyUserQuotaRuntime
	clientLifecycle proxyClientLifecycleRuntime
	rateLimiters    proxyRateLimitRuntime
}

type proxyRuntimeSources struct {
	runtimeRoot       func() proxyRuntimeContext
	localProxyAllowed func() bool
}

var runtimeProxy = newProxyRuntimeContext()
var currentProxyRuntimeRoot = func() proxyRuntimeContext {
	return runtimeProxy
}
var currentProxyUserLookupDB = func() proxyUserLookupDB {
	return file.GetDb()
}
var currentProxyGlobalAccessRoot = func() proxyGlobalAccessSource {
	if db := file.GetDb(); db != nil {
		return db.GetGlobal()
	}
	return nil
}
var currentProxyLocalProxyAllowed = func() bool {
	return servercfg.Current().AllowLocalProxyEnabled()
}

func currentProxyRuntimeSources() proxyRuntimeSources {
	return proxyRuntimeSources{
		runtimeRoot:       currentProxyRuntimeRoot,
		localProxyAllowed: currentProxyLocalProxyAllowed,
	}
}

func injectedProxyRuntimeSources(runtime proxyRuntimeContext) proxyRuntimeSources {
	sources := currentProxyRuntimeSources()
	sources.runtimeRoot = func() proxyRuntimeContext {
		return runtime
	}
	return sources
}

func (s proxyRuntimeSources) resolvedRuntimeRoot() func() proxyRuntimeContext {
	if s.runtimeRoot != nil {
		return s.runtimeRoot
	}
	return currentProxyRuntimeRoot
}

func (s proxyRuntimeSources) resolvedLocalProxyAllowed() func() bool {
	if s.localProxyAllowed != nil {
		return s.localProxyAllowed
	}
	return currentProxyLocalProxyAllowed
}

func newProxyRuntimeContext() proxyRuntimeContext {
	users := proxyClientUserRuntime{
		currentStore: func() proxyUserLookupStore {
			return file.GlobalStore
		},
		currentDB: currentProxyUserLookupDB,
	}
	return newProxyRuntimeContextWith(users, currentProxyGlobalAccessRoot)
}

func newProxyRuntimeContextWith(users proxyClientUserRuntime, currentGlobal func() proxyGlobalAccessSource) proxyRuntimeContext {
	accessPolicy := proxyAccessPolicyRuntime{
		users:         users,
		currentGlobal: currentGlobal,
	}
	userQuota := proxyUserQuotaRuntime{
		users: users,
	}
	clientLifecycle := proxyClientLifecycleRuntime{}
	rateLimiters := proxyRateLimitRuntime{
		userQuota: userQuota,
	}
	return proxyRuntimeContext{
		users:           users,
		accessPolicy:    accessPolicy,
		userQuota:       userQuota,
		clientLifecycle: clientLifecycle,
		rateLimiters:    rateLimiters,
	}
}

type proxyUserLookupStore interface {
	GetUser(int) (*file.User, error)
	GetClientsByUserId(int) []*file.Client
}

type proxyUserLookupDB interface {
	GetUser(int) (*file.User, error)
}

type proxyClientUserRuntime struct {
	currentStore func() proxyUserLookupStore
	currentDB    func() proxyUserLookupDB
}

func (r proxyClientUserRuntime) Resolve(client *file.Client) *file.User {
	ownerID := 0
	if client != nil {
		ownerID = client.OwnerID()
	}
	if ownerID <= 0 {
		return nil
	}
	if user := client.OwnerUser(); user != nil {
		return user
	}
	if r.currentStore != nil {
		if runtimeStore := r.currentStore(); runtimeStore != nil {
			if user, err := runtimeStore.GetUser(ownerID); err == nil && user != nil {
				client.BindOwnerUser(user)
				return user
			}
		}
	}
	if r.currentDB != nil {
		if runtimeDB := r.currentDB(); runtimeDB != nil {
			if user, err := runtimeDB.GetUser(ownerID); err == nil && user != nil {
				client.BindOwnerUser(user)
				return user
			}
		}
	}
	return nil
}

func (r proxyClientUserRuntime) ClientsByUserID(userID int) []*file.Client {
	if userID <= 0 || r.currentStore == nil {
		return nil
	}
	runtimeStore := r.currentStore()
	if runtimeStore == nil {
		return nil
	}
	return runtimeStore.GetClientsByUserId(userID)
}

type proxyTransportPolicy interface {
	IsClientSourceAccessDenied(*file.Client, *file.Tunnel, string) bool
	IsClientDestinationAccessDenied(*file.Client, *file.Tunnel, string) bool
}

type proxyGlobalAccessSource interface {
	AllowsSourceAddr(string) bool
}

type proxyAccessPolicyRuntime struct {
	users         proxyClientUserRuntime
	currentGlobal func() proxyGlobalAccessSource
}

func (r proxyAccessPolicyRuntime) IsGlobalBlackIP(ipPort string) bool {
	global := r.globalSource()
	if global == nil || global.AllowsSourceAddr(ipPort) {
		return false
	}
	return true
}

func (r proxyAccessPolicyRuntime) HasActiveDestinationACL(client *file.Client, task *file.Tunnel) bool {
	if user := r.users.Resolve(client); user != nil && user.DestAclMode != file.AclOff {
		return true
	}
	return task != nil && task.DestAclMode != file.AclOff
}

func (r proxyAccessPolicyRuntime) IsClientDestinationAccessDenied(client *file.Client, task *file.Tunnel, addr string) bool {
	user := r.users.Resolve(client)
	allowDomain := task != nil && task.Mode == "mixProxy"
	if allowed, deniedBy := allowsDestinationByMode(user, task, addr, allowDomain); !allowed {
		logDestinationAccessDenied(client, task, user, addr, deniedBy)
		return true
	}
	return false
}

func (r proxyAccessPolicyRuntime) IsClientSourceAccessDenied(client *file.Client, task *file.Tunnel, ipPort string) bool {
	return r.globalSourceDenied(ipPort) ||
		IsUserSourceDenied(r.users.Resolve(client), ipPort) ||
		IsClientBlackIp(client, ipPort) ||
		IsTunnelSourceDenied(task, ipPort)
}

func (r proxyAccessPolicyRuntime) IsHostSourceAccessDenied(host *file.Host, ipPort string) bool {
	if host == nil {
		return true
	}
	return r.globalSourceDenied(ipPort) ||
		IsUserSourceDenied(r.users.Resolve(host.Client), ipPort) ||
		IsClientBlackIp(host.Client, ipPort) ||
		IsHostSourceDenied(host, ipPort)
}

func (r proxyAccessPolicyRuntime) globalSource() proxyGlobalAccessSource {
	if r.currentGlobal == nil {
		return nil
	}
	return r.currentGlobal()
}

func (r proxyAccessPolicyRuntime) globalSourceDenied(ipPort string) bool {
	if !r.IsGlobalBlackIP(ipPort) {
		return false
	}
	logGlobalSourceAccessDenied(ipPort)
	return true
}

type proxyUserQuotaRuntime struct {
	users proxyClientUserRuntime
}

func (r proxyUserQuotaRuntime) Check(client *file.Client, nowUnix int64) error {
	if client == nil || client.OwnerID() <= 0 {
		return nil
	}
	user := r.users.Resolve(client)
	if user == nil {
		return errProxyUserNotFound
	}
	if user.Status == 0 {
		return errProxyUserDisabled
	}
	if user.ExpireAt > 0 && user.ExpireAt <= nowUnix {
		return errProxyServiceAccessExpired
	}
	if user.FlowLimit > 0 {
		_, _, total := user.TotalTrafficTotals()
		if total >= user.FlowLimit {
			return errProxyTrafficLimitExceeded
		}
	}
	if user.MaxConnections > 0 && int(atomic.LoadInt32(&user.NowConn)) >= user.MaxConnections {
		return errProxyConnectionLimit
	}
	return nil
}

func (r proxyUserQuotaRuntime) RateLimiter(client *file.Client) *rate.Rate {
	if client == nil || client.OwnerID() <= 0 {
		return nil
	}
	user := r.users.Resolve(client)
	if user == nil {
		return nil
	}
	return user.Rate
}

type proxyClientLifecycleRuntime struct{}

func (proxyClientLifecycleRuntime) CheckEnabled(client *file.Client) error {
	if client == nil {
		return errProxyClientDisabled
	}
	if client.Id != 0 && !client.Status {
		return errProxyClientDisabled
	}
	return nil
}

func (proxyClientLifecycleRuntime) CheckLifecycle(client *file.Client, nowUnix int64) error {
	if client == nil {
		return errProxyClientDisabled
	}
	if expireAt := client.EffectiveExpireAt(); expireAt > 0 && expireAt <= nowUnix {
		return errProxyServiceAccessExpired
	}
	if flowLimit := client.EffectiveFlowLimitBytes(); flowLimit > 0 {
		_, _, total := client.TotalTrafficTotals()
		if total >= flowLimit {
			return errProxyTrafficLimitExceeded
		}
	}
	return nil
}

func (proxyClientLifecycleRuntime) AcquireConnection(client *file.Client) error {
	if client == nil {
		return errProxyClientDisabled
	}
	if !client.GetConn() {
		return errProxyConnectionLimit
	}
	return nil
}

type proxyRateLimitRuntime struct {
	userQuota proxyUserQuotaProvider
}

func (r proxyRateLimitRuntime) Service(client *file.Client, tunnel *file.Tunnel, host *file.Host) rate.Limiter {
	resourceRate := (*rate.Rate)(nil)
	switch {
	case tunnel != nil:
		resourceRate = enabledRateLimiter(tunnel.Rate)
	case host != nil:
		resourceRate = enabledRateLimiter(host.Rate)
	}
	return rate.NewHierarchicalLimiter3(
		resourceRate,
		enabledRateLimiter(clientRateLimiter(client)),
		enabledRateLimiter(r.UserRate(client)),
	)
}

func (r proxyRateLimitRuntime) Bridge(client *file.Client) rate.Limiter {
	return rate.NewHierarchicalLimiter2(
		enabledRateLimiter(clientRateLimiter(client)),
		enabledRateLimiter(r.UserRate(client)),
	)
}

func (r proxyRateLimitRuntime) UserRate(client *file.Client) *rate.Rate {
	if client == nil || r.userQuota == nil {
		return nil
	}
	return r.userQuota.RateLimiter(client)
}

func clientRateLimiter(client *file.Client) *rate.Rate {
	if client == nil {
		return nil
	}
	return client.Rate
}

func enabledRateLimiter(current *rate.Rate) *rate.Rate {
	if current == nil || current.Limit() <= 0 {
		return nil
	}
	return current
}

func resolveClientUser(client *file.Client) *file.User {
	return runtimeProxy.users.Resolve(client)
}
