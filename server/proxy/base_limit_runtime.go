package proxy

import (
	"errors"
	"sync"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

var (
	errProxyClientDisabled       = errors.New("client is disabled")
	errProxyUserDisabled         = errors.New("user is disabled")
	errProxyUserNotFound         = errors.New("user not found")
	errProxyServiceAccessExpired = errors.New("service access expired")
	errProxyTrafficLimitExceeded = errors.New("traffic limit exceeded")
	errProxyConnectionLimit      = errors.New("connection limit exceeded")
)

type proxyUserQuotaProvider interface {
	Check(*file.Client, int64) error
	RateLimiter(*file.Client) *rate.Rate
}

type proxyClientLifecycleProvider interface {
	CheckEnabled(*file.Client) error
	CheckLifecycle(*file.Client, int64) error
	AcquireConnection(*file.Client) error
}

type proxyRateLimiterProvider interface {
	Service(*file.Client, *file.Tunnel, *file.Host) rate.Limiter
	Bridge(*file.Client) rate.Limiter
}

type proxyLimitRuntime struct {
	now        func() time.Time
	userQuota  proxyUserQuotaProvider
	clientLife proxyClientLifecycleProvider
	rateLimits proxyRateLimiterProvider
}

type proxyConnectionLease struct {
	once   sync.Once
	user   *file.User
	client *file.Client
	tunnel *file.Tunnel
	host   *file.Host
}

func newProxyLimitRuntime(runtime proxyRuntimeContext) proxyLimitRuntime {
	return proxyLimitRuntime{
		now:        time.Now,
		userQuota:  runtime.userQuota,
		clientLife: runtime.clientLifecycle,
		rateLimits: runtime.rateLimiters,
	}
}

func (l *proxyConnectionLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.host != nil {
			l.host.CutConn()
		}
		if l.tunnel != nil {
			l.tunnel.CutConn()
		}
		if l.client != nil {
			l.client.CutConn()
		}
		if l.user != nil {
			l.user.CutConn()
		}
	})
}

func (s *BaseServer) limitRuntime() proxyLimitRuntime {
	return newProxyLimitRuntime(s.runtimeContext())
}

func (s *BaseServer) CheckFlowAndConnNum(client *file.Client, tunnel *file.Tunnel, host *file.Host) (*proxyConnectionLease, error) {
	return s.limitRuntime().CheckFlowAndConnNum(client, tunnel, host)
}

func (s *BaseServer) ServiceRateLimiter(client *file.Client, tunnel *file.Tunnel, host *file.Host) rate.Limiter {
	return s.limitRuntime().ServiceRateLimiter(client, tunnel, host)
}

func (s *BaseServer) BridgeRateLimiter(client *file.Client) rate.Limiter {
	return s.limitRuntime().BridgeRateLimiter(client)
}

func (r proxyLimitRuntime) withDefaults() proxyLimitRuntime {
	if r.now != nil && r.userQuota != nil && r.clientLife != nil && r.rateLimits != nil {
		return r
	}
	runtime := currentProxyRuntimeRoot()
	if r.now == nil {
		r.now = time.Now
	}
	if r.userQuota == nil {
		r.userQuota = runtime.userQuota
	}
	if r.clientLife == nil {
		r.clientLife = runtime.clientLifecycle
	}
	if r.rateLimits == nil {
		r.rateLimits = runtime.rateLimiters
	}
	return r
}

func (r proxyLimitRuntime) CheckFlowAndConnNum(client *file.Client, tunnel *file.Tunnel, host *file.Host) (*proxyConnectionLease, error) {
	r = r.withDefaults()
	if err := r.clientLife.CheckEnabled(client); err != nil {
		return nil, err
	}
	now := r.now()
	nowUnix := now.Unix()
	if err := r.userQuota.Check(client, nowUnix); err != nil {
		return nil, err
	}
	if err := r.clientLife.CheckLifecycle(client, nowUnix); err != nil {
		return nil, err
	}
	if err := checkProxyResourceLifecycle(tunnel, host, nowUnix); err != nil {
		return nil, err
	}
	return r.acquireConnectionLease(r.clientLife, client, tunnel, host)
}

func (r proxyLimitRuntime) ServiceRateLimiter(client *file.Client, tunnel *file.Tunnel, host *file.Host) rate.Limiter {
	return r.withDefaults().rateLimits.Service(client, tunnel, host)
}

func (r proxyLimitRuntime) BridgeRateLimiter(client *file.Client) rate.Limiter {
	return r.withDefaults().rateLimits.Bridge(client)
}

func (r proxyLimitRuntime) acquireConnectionLease(clientLife proxyClientLifecycleProvider, client *file.Client, tunnel *file.Tunnel, host *file.Host) (*proxyConnectionLease, error) {
	lease := &proxyConnectionLease{
		client: client,
		tunnel: tunnel,
		host:   host,
	}
	if user := resolveClientUser(client); user != nil {
		if !user.GetConn() {
			return nil, errProxyConnectionLimit
		}
		lease.user = user
	}
	if err := clientLife.AcquireConnection(client); err != nil {
		lease.Release()
		return nil, err
	}
	if !acquireProxyResourceConnection(tunnel, host) {
		lease.Release()
		return nil, errProxyConnectionLimit
	}
	return lease, nil
}

func checkProxyResourceLifecycle(tunnel *file.Tunnel, host *file.Host, nowUnix int64) error {
	switch {
	case tunnel != nil:
		if expireAt := tunnel.EffectiveExpireAt(); expireAt > 0 && expireAt <= nowUnix {
			return errProxyServiceAccessExpired
		}
		if flowLimit := tunnel.EffectiveFlowLimitBytes(); flowLimit > 0 {
			_, _, total := tunnel.ServiceTrafficTotals()
			if total >= flowLimit {
				return errProxyTrafficLimitExceeded
			}
		}
	case host != nil:
		if expireAt := host.EffectiveExpireAt(); expireAt > 0 && expireAt <= nowUnix {
			return errProxyServiceAccessExpired
		}
		if flowLimit := host.EffectiveFlowLimitBytes(); flowLimit > 0 {
			_, _, total := host.ServiceTrafficTotals()
			if total >= flowLimit {
				return errProxyTrafficLimitExceeded
			}
		}
	}
	return nil
}

func acquireProxyResourceConnection(tunnel *file.Tunnel, host *file.Host) bool {
	switch {
	case tunnel != nil:
		return tunnel.GetConn()
	case host != nil:
		return host.GetConn()
	default:
		return true
	}
}

func chargeLimiterBytes(limiter rate.Limiter, size int) {
	if limiter == nil || size <= 0 {
		return
	}
	limiter.Get(int64(size))
}

func refundLimiterBytes(limiter rate.Limiter, reserved, written int) {
	if limiter == nil || reserved <= written {
		return
	}
	limiter.ReturnBucket(int64(reserved - written))
}

func writeWithLimiter(limiter rate.Limiter, payload []byte, write func([]byte) (int, error)) (int, error) {
	size := len(payload)
	chargeLimiterBytes(limiter, size)
	n, err := write(payload)
	refundLimiterBytes(limiter, size, n)
	return n, err
}
