package httpproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/server/proxy"
)

const ctxBackendSelection ctxKey = "nps_backend_selection"

const (
	backendTransportPoolMaxEntries = 64
	backendTransportPoolIdleTTL    = 5 * time.Minute
)

var (
	errHTTPProxyInvalidBackend    = errors.New("invalid http proxy backend")
	errHTTPProxyDestinationDenied = errors.New("destination denied by dest acl")
)

type backendTransportEntry struct {
	transport  *http.Transport
	lastUsedNS atomic.Int64
}

type backendSelection struct {
	routeUUID     string
	targetAddr    string
	targetIsHTTPS bool
}

type resolvedHTTPProxyBackend struct {
	remoteAddr   string
	host         *file.Host
	selection    backendSelection
	routeRuntime *proxy.RouteRuntimeContext
}

type backendTransportPool struct {
	mu          sync.RWMutex
	items       map[string]*backendTransportEntry
	maxEntries  int
	idleTTL     time.Duration
	nextPruneAt time.Time
}

func (s backendSelection) normalized() backendSelection {
	s.routeUUID = strings.TrimSpace(s.routeUUID)
	s.targetAddr = strings.TrimSpace(s.targetAddr)
	return s
}

func (s backendSelection) valid() bool {
	return strings.TrimSpace(s.targetAddr) != ""
}

func (s backendSelection) key() string {
	s = s.normalized()
	scheme := "http"
	if s.targetIsHTTPS {
		scheme = "https"
	}
	return scheme + "|" + s.routeUUID + "|" + s.targetAddr
}

func httpProxyContextBackendSelection(ctx context.Context) (backendSelection, bool) {
	if ctx == nil {
		return backendSelection{}, false
	}
	if data, ok := httpProxyContextData(ctx); ok && data.selection.valid() {
		return data.selection, true
	}
	selection, ok := ctx.Value(ctxBackendSelection).(backendSelection)
	if !ok {
		return backendSelection{}, false
	}
	selection = selection.normalized()
	return selection, selection.valid()
}

func withHTTPProxyBackendSelection(ctx context.Context, selection backendSelection) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if data, ok := httpProxyContextData(ctx); ok {
		data.selection = selection
		return withHTTPProxyRequestData(ctx, data)
	}
	return context.WithValue(ctx, ctxBackendSelection, selection.normalized())
}

func validateHTTPBackendHost(host *file.Host) error {
	switch {
	case host == nil:
		return errHTTPProxyInvalidBackend
	case host.Client == nil:
		return errHTTPProxyInvalidBackend
	case host.Client.Cnf == nil:
		return errHTTPProxyInvalidBackend
	case host.Client.Flow == nil:
		return errHTTPProxyInvalidBackend
	case host.Flow == nil:
		return errHTTPProxyInvalidBackend
	case host.Target == nil:
		return errHTTPProxyInvalidBackend
	default:
		return nil
	}
}

func (s *HttpServer) resolveBackendSelection(host *file.Host) (backendSelection, error) {
	if err := validateHTTPBackendHost(host); err != nil {
		return backendSelection{}, err
	}
	routeUUID := s.SelectClientRouteUUID(host.Client, host.RuntimeRouteUUID())
	targetAddr, err := host.Target.GetRouteTarget(routeUUID)
	if err != nil {
		return backendSelection{}, err
	}
	return backendSelection{
		routeUUID:     routeUUID,
		targetAddr:    targetAddr,
		targetIsHTTPS: host.TargetIsHttps,
	}.normalized(), nil
}

func resolveHTTPProxyRemoteAddr(ctx context.Context, request *http.Request) string {
	if request != nil {
		if remote := strings.TrimSpace(request.RemoteAddr); remote != "" {
			return remote
		}
	}
	remote, _ := httpProxyContextString(ctx, ctxRemoteAddr)
	return remote
}

func resolveHTTPProxyBackendSNI(ctx context.Context, request *http.Request, host *file.Host) string {
	if sni, ok := httpProxyContextString(ctx, ctxSNI); ok {
		return sni
	}
	if host != nil {
		if sni := strings.TrimSpace(host.HostChange); sni != "" {
			return sni
		}
	}
	if request != nil {
		return strings.TrimSpace(request.Host)
	}
	return ""
}

func resolveHTTPProxyRouteRuntime(ctx context.Context, host *file.Host, selection backendSelection, newRuntime func(*file.Client, string) *proxy.RouteRuntimeContext) *proxy.RouteRuntimeContext {
	routeRuntime, _ := httpProxyContextRouteRuntime(ctx)
	if routeRuntime == nil {
		if host == nil || newRuntime == nil {
			return nil
		}
		return newRuntime(host.Client, selection.routeUUID)
	}
	if selection.routeUUID != "" {
		routeRuntime.SetRouteUUID(selection.routeUUID)
	}
	return routeRuntime
}

func withResolvedHTTPProxyBackendData(ctx context.Context, request *http.Request, resolved resolvedHTTPProxyBackend) context.Context {
	data, _ := httpProxyContextData(ctx)
	data.remoteAddr = resolveHTTPProxyRemoteAddr(ctx, request)
	data.host = resolved.host
	data.sni = resolveHTTPProxyBackendSNI(ctx, request, resolved.host)
	data.routeRuntime = resolved.routeRuntime
	data.selection = resolved.selection
	return withHTTPProxyRequestData(ctx, data)
}

func (s *HttpServer) resolveHTTPProxyBackendState(ctx context.Context, request *http.Request, fallbackHost *file.Host, fallbackSelection backendSelection) (context.Context, resolvedHTTPProxyBackend, error) {
	if ctx == nil {
		if request != nil {
			ctx = request.Context()
		} else {
			ctx = context.Background()
		}
	}

	host, err := resolveHTTPBackendHost(ctx, fallbackHost)
	if err != nil {
		return ctx, resolvedHTTPProxyBackend{}, err
	}
	if err := validateHTTPBackendHost(host); err != nil {
		return ctx, resolvedHTTPProxyBackend{}, err
	}
	selection, err := resolveHTTPBackendSelection(ctx, fallbackSelection, host, s.resolveBackendSelection)
	if err != nil {
		return ctx, resolvedHTTPProxyBackend{}, err
	}

	resolved := resolvedHTTPProxyBackend{
		host:         host,
		selection:    selection,
		routeRuntime: resolveHTTPProxyRouteRuntime(ctx, host, selection, s.NewRouteRuntimeContext),
	}
	ctx = withResolvedHTTPProxyBackendData(ctx, request, resolved)
	resolved.remoteAddr, _ = httpProxyContextString(ctx, ctxRemoteAddr)
	return ctx, resolved, nil
}

func requireResolvedHTTPProxyRemoteAddr(remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", fmt.Errorf("missing remote address context")
	}
	return remote, nil
}

func newBackendTransportPool() *backendTransportPool {
	return &backendTransportPool{
		items:      make(map[string]*backendTransportEntry),
		maxEntries: backendTransportPoolMaxEntries,
		idleTTL:    backendTransportPoolIdleTTL,
	}
}

func (p *backendTransportPool) GetOrCreate(selection backendSelection, create func() *http.Transport) *http.Transport {
	if p == nil || create == nil {
		return nil
	}
	selection = selection.normalized()
	if !selection.valid() {
		return create()
	}

	now := time.Now()
	key := selection.key()

	if transport := p.loadUsableTransport(key, now); transport != nil {
		return transport
	}

	transport := create()
	if transport == nil {
		return nil
	}

	stale, actual, stored := p.storeTransport(key, transport, now)
	closeIdleBackendTransports(stale...)
	if !stored {
		closeIdleBackendTransports(transport)
	}
	return actual
}

func (p *backendTransportPool) Remove(selection backendSelection) {
	if p == nil {
		return
	}
	selection = selection.normalized()
	if !selection.valid() {
		return
	}
	p.removeByKey(selection.key())
}

func (p *backendTransportPool) removeByKey(key string) {
	if p == nil || strings.TrimSpace(key) == "" {
		return
	}
	p.mu.Lock()
	entry := p.items[key]
	delete(p.items, key)
	p.mu.Unlock()
	if entry != nil && entry.transport != nil {
		entry.transport.CloseIdleConnections()
	}
}

func (p *backendTransportPool) CloseIdleConnections() {
	if p == nil {
		return
	}
	p.mu.Lock()
	items := p.items
	p.items = make(map[string]*backendTransportEntry)
	p.nextPruneAt = time.Time{}
	p.mu.Unlock()
	for _, entry := range items {
		if entry != nil && entry.transport != nil {
			entry.transport.CloseIdleConnections()
		}
	}
}

func (p *backendTransportPool) pruneExpiredLocked(now time.Time) []*http.Transport {
	if p == nil || len(p.items) == 0 || p.idleTTL <= 0 {
		return nil
	}
	var transports []*http.Transport
	for key, entry := range p.items {
		if entry == nil || entry.transport == nil {
			delete(p.items, key)
			continue
		}
		if now.Sub(entry.lastUsed()) < p.idleTTL {
			continue
		}
		transports = append(transports, entry.transport)
		delete(p.items, key)
	}
	return transports
}

func (p *backendTransportPool) maybePruneExpiredLocked(now time.Time) []*http.Transport {
	if p == nil || p.idleTTL <= 0 {
		return nil
	}
	if !p.pruneDueLocked(now) {
		return nil
	}
	transports := p.pruneExpiredLocked(now)
	p.nextPruneAt = now.Add(p.pruneInterval())
	return transports
}

func (p *backendTransportPool) pruneDueLocked(now time.Time) bool {
	return p != nil && (p.nextPruneAt.IsZero() || !now.Before(p.nextPruneAt))
}

func (p *backendTransportPool) pruneInterval() time.Duration {
	if p == nil || p.idleTTL <= 0 {
		return 0
	}
	interval := p.idleTTL / 4
	if interval <= 0 {
		interval = p.idleTTL
	}
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	if interval < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	return interval
}

func (p *backendTransportPool) enforceCapacityLocked() *http.Transport {
	if p == nil || p.maxEntries <= 0 || len(p.items) <= p.maxEntries {
		return nil
	}

	var (
		oldestKey string
		oldestAt  time.Time
		found     bool
	)
	for key, entry := range p.items {
		if entry == nil || entry.transport == nil {
			delete(p.items, key)
			continue
		}
		usedAt := entry.lastUsed()
		if !found || usedAt.Before(oldestAt) {
			oldestKey = key
			oldestAt = usedAt
			found = true
		}
	}
	if !found {
		return nil
	}
	entry := p.items[oldestKey]
	delete(p.items, oldestKey)
	if entry != nil && entry.transport != nil {
		return entry.transport
	}
	return nil
}

func (p *backendTransportPool) Size() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.items)
}

func (p *backendTransportPool) loadUsableTransport(key string, now time.Time) *http.Transport {
	if p == nil || strings.TrimSpace(key) == "" {
		return nil
	}
	p.mu.RLock()
	pruneDue := p.pruneDueLocked(now)
	entry := p.items[key]
	p.mu.RUnlock()
	if pruneDue || entry == nil || entry.transport == nil {
		return nil
	}
	entry.markUsed(now)
	return entry.transport
}

func (p *backendTransportPool) storeTransport(key string, transport *http.Transport, now time.Time) ([]*http.Transport, *http.Transport, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	stale := p.maybePruneExpiredLocked(now)
	if entry := p.items[key]; entry != nil && entry.transport != nil {
		entry.markUsed(now)
		return stale, entry.transport, false
	}
	if p.items == nil {
		p.items = make(map[string]*backendTransportEntry)
	}
	p.items[key] = newBackendTransportEntry(transport, now)
	evicted := p.enforceCapacityLocked()
	return append(stale, evicted), transport, true
}

func newBackendTransportEntry(transport *http.Transport, now time.Time) *backendTransportEntry {
	entry := &backendTransportEntry{transport: transport}
	entry.markUsed(now)
	return entry
}

func (e *backendTransportEntry) markUsed(now time.Time) {
	if e == nil {
		return
	}
	e.lastUsedNS.Store(now.UnixNano())
}

func (e *backendTransportEntry) lastUsed() time.Time {
	if e == nil {
		return time.Time{}
	}
	lastUsedNS := e.lastUsedNS.Load()
	if lastUsedNS == 0 {
		return time.Time{}
	}
	return time.Unix(0, lastUsedNS)
}

func (s *HttpServer) backendTransportPool(hostID int) *backendTransportPool {
	if s == nil || s.HttpProxyCache == nil || hostID == 0 {
		return newBackendTransportPool()
	}

	for {
		if cached, ok := s.HttpProxyCache.Get(hostID); ok {
			if pool, ok := cached.(*backendTransportPool); ok && pool != nil {
				return pool
			}
			if s.HttpProxyCache.CompareAndDelete(hostID, cached) {
				if closer, ok := cached.(interface{ CloseIdleConnections() }); ok {
					closer.CloseIdleConnections()
				}
			}
			continue
		}

		pool := newBackendTransportPool()
		actual, loaded := s.HttpProxyCache.LoadOrStore(hostID, pool)
		if !loaded {
			return pool
		}
		if existing, ok := actual.(*backendTransportPool); ok && existing != nil {
			return existing
		}
		if s.HttpProxyCache.CompareAndDelete(hostID, actual) {
			if closer, ok := actual.(interface{ CloseIdleConnections() }); ok {
				closer.CloseIdleConnections()
			}
		}
	}
}

func closeIdleBackendTransports(transports ...*http.Transport) {
	for _, transport := range transports {
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
}

func (s *HttpServer) removeBackendTransport(hostID int, selection backendSelection) {
	if s == nil || s.HttpProxyCache == nil || hostID == 0 {
		return
	}
	cached, ok := s.HttpProxyCache.Get(hostID)
	if !ok {
		return
	}
	if pool, ok := cached.(*backendTransportPool); ok && pool != nil {
		if selection.valid() {
			pool.Remove(selection)
			return
		}
		pool.CloseIdleConnections()
		s.HttpProxyCache.Remove(hostID)
		return
	}
	if closer, ok := cached.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	s.HttpProxyCache.Remove(hostID)
}

func (s *HttpServer) transportForBackend(host *file.Host, selection backendSelection) *http.Transport {
	selection = selection.normalized()
	pool := s.backendTransportPool(host.Id)
	return pool.GetOrCreate(selection, func() *http.Transport {
		return s.newBackendTransport(host, selection)
	})
}

func (s *HttpServer) newBackendTransport(host *file.Host, selection backendSelection) *http.Transport {
	selection = selection.normalized()
	return &http.Transport{
		ResponseHeaderTimeout: s.currentConfig().ProxyResponseHeaderTimeout(),
		DisableKeepAlives:     host.CompatMode,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.dialSelectedBackendContext(ctx, host, selection)
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := s.dialSelectedBackendContext(ctx, host, selection)
			if err != nil {
				return nil, err
			}

			sni, _ := httpProxyContextString(ctx, ctxSNI)
			tlsConn, err := conn.GetTlsConn(c, sni, false)
			if err != nil {
				_ = c.Close()
				return nil, err
			}
			return proxy.WrapRuntimeRouteConn(tlsConn, proxy.RuntimeRouteUUIDFromConn(c)), nil
		},
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
	}
}

func (s *HttpServer) dialSelectedBackendContext(ctx context.Context, host *file.Host, selection backendSelection) (net.Conn, error) {
	ctx, resolved, err := s.resolveHTTPProxyBackendState(ctx, nil, host, selection)
	if err != nil {
		return nil, err
	}
	return s.dialResolvedBackendContext(ctx, resolved)
}

func (s *HttpServer) dialResolvedBackendContext(ctx context.Context, resolved resolvedHTTPProxyBackend) (net.Conn, error) {
	remote, err := requireResolvedHTTPProxyRemoteAddr(resolved.remoteAddr)
	if err != nil {
		return nil, err
	}
	if resolved.host == nil || resolved.routeRuntime == nil {
		return nil, errHTTPProxyInvalidBackend
	}
	if s.IsClientDestinationAccessDenied(resolved.host.Client, nil, resolved.selection.targetAddr) {
		return nil, errHTTPProxyDestinationDenied
	}
	isLocal := s.LocalProxyAllowed() && resolved.host.Target.LocalProxy || resolved.host.Client.Id < 0
	link := conn.NewLink("tcp", resolved.selection.targetAddr, resolved.host.Client.Cnf.Crypt, resolved.host.Client.Cnf.Compress, remote, isLocal, conn.WithRouteUUID(resolved.selection.routeUUID))
	target, err := s.OpenBridgeLink(resolved.host.Client.Id, link, nil)
	if err != nil {
		logs.Info("DialContext: connection to host %d (target %s) failed: %v", resolved.host.Id, resolved.selection.targetAddr, err)
		return nil, err
	}
	resolved.routeRuntime.UpdateFromLink(link)
	target = resolved.routeRuntime.TrackConn(target)
	bridgeLimiter := s.BridgeRateLimiter(resolved.host.Client)
	if isLocal {
		bridgeLimiter = nil
	}
	rawConn := conn.GetConn(target, link.Crypt, link.Compress, bridgeLimiter, true, isLocal)
	rawConn = conn.WrapReadWriteCloserWithTrafficObserver(rawConn, resolved.routeRuntime.BridgeObserver(resolved.host.Client))
	flowConn := conn.NewFlowConn(rawConn, resolved.host.Flow, resolved.host.Client.Flow)
	if resolved.host.Target.ProxyProtocol != 0 {
		ra, _ := net.ResolveTCPAddr("tcp", remote)
		if ra == nil || ra.IP == nil {
			ra = &net.TCPAddr{IP: net.IPv4zero, Port: 0}
		}
		la, _ := ctx.Value(http.LocalAddrContextKey).(*net.TCPAddr)
		hdr := conn.BuildProxyProtocolHeaderByAddr(ra, la, resolved.host.Target.ProxyProtocol)
		if hdr != nil {
			_ = flowConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := flowConn.Write(hdr); err != nil {
				_ = flowConn.Close()
				return nil, fmt.Errorf("write PROXY header: %w", err)
			}
			_ = flowConn.SetWriteDeadline(time.Time{})
		}
	}
	return proxy.WrapRuntimeRouteConn(flowConn, resolved.routeRuntime.RouteUUID()), nil
}

func resolveHTTPBackendHost(ctx context.Context, fallback *file.Host) (*file.Host, error) {
	if host, ok := httpProxyContextHost(ctx); ok {
		return host, nil
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errHTTPProxyInvalidBackend
}

func resolveHTTPBackendSelection(ctx context.Context, fallback backendSelection, host *file.Host, resolver func(*file.Host) (backendSelection, error)) (backendSelection, error) {
	if selection, ok := httpProxyContextBackendSelection(ctx); ok {
		return selection, nil
	}
	fallback = fallback.normalized()
	if fallback.valid() {
		return fallback, nil
	}
	if resolver == nil {
		return backendSelection{}, errHTTPProxyInvalidBackend
	}
	return resolver(host)
}
