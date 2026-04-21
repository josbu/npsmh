package proxy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

const (
	tunnelHTTPBackendPoolMaxEntries = 128
	tunnelHTTPBackendPoolIdleTTL    = 5 * time.Minute
)

var errTunnelHTTPProxyMissingTarget = errors.New("missing http proxy target")
var errTunnelHTTPProxyInvalidBackend = errors.New("invalid http proxy backend")

type tunnelHTTPBackendSelection struct {
	routeUUID  string
	targetAddr string
	task       *file.Tunnel
}

func (s tunnelHTTPBackendSelection) normalized() tunnelHTTPBackendSelection {
	s.routeUUID = strings.TrimSpace(s.routeUUID)
	s.targetAddr = strings.TrimSpace(s.targetAddr)
	return s
}

func (s tunnelHTTPBackendSelection) valid() bool {
	return s.task != nil && strings.TrimSpace(s.targetAddr) != ""
}

func validateTunnelHTTPBackendTask(task *file.Tunnel) error {
	switch {
	case task == nil:
		return errTunnelHTTPProxyInvalidBackend
	case task.Client == nil:
		return errTunnelHTTPProxyInvalidBackend
	case task.Client.Cnf == nil:
		return errTunnelHTTPProxyInvalidBackend
	case task.Client.Flow == nil:
		return errTunnelHTTPProxyInvalidBackend
	case task.Flow == nil:
		return errTunnelHTTPProxyInvalidBackend
	case task.Target == nil:
		return errTunnelHTTPProxyInvalidBackend
	default:
		return nil
	}
}

func (s tunnelHTTPBackendSelection) key() string {
	s = s.normalized()
	return s.routeUUID + "|" + s.targetAddr
}

type tunnelHTTPBackendEntry struct {
	transport  *http.Transport
	lastUsedNS atomic.Int64
}

type tunnelHTTPBackendTransportPool struct {
	mu          sync.RWMutex
	items       map[string]*tunnelHTTPBackendEntry
	maxEntries  int
	idleTTL     time.Duration
	nextPruneAt time.Time
}

func newTunnelHTTPBackendTransportPool() *tunnelHTTPBackendTransportPool {
	return &tunnelHTTPBackendTransportPool{
		items:      make(map[string]*tunnelHTTPBackendEntry),
		maxEntries: tunnelHTTPBackendPoolMaxEntries,
		idleTTL:    tunnelHTTPBackendPoolIdleTTL,
	}
}

func (p *tunnelHTTPBackendTransportPool) GetOrCreate(selection tunnelHTTPBackendSelection, create func() *http.Transport) *http.Transport {
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
	closeIdleTunnelHTTPTransports(stale...)
	if !stored {
		closeIdleTunnelHTTPTransports(transport)
	}
	return actual
}

func (p *tunnelHTTPBackendTransportPool) Remove(selection tunnelHTTPBackendSelection) {
	if p == nil {
		return
	}
	selection = selection.normalized()
	if !selection.valid() {
		return
	}
	p.removeByKey(selection.key())
}

func (p *tunnelHTTPBackendTransportPool) removeByKey(key string) {
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

func (p *tunnelHTTPBackendTransportPool) CloseIdleConnections() {
	if p == nil {
		return
	}
	p.mu.Lock()
	items := p.items
	p.items = make(map[string]*tunnelHTTPBackendEntry)
	p.nextPruneAt = time.Time{}
	p.mu.Unlock()
	for _, entry := range items {
		if entry != nil && entry.transport != nil {
			entry.transport.CloseIdleConnections()
		}
	}
}

func (p *tunnelHTTPBackendTransportPool) Size() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.items)
}

func (p *tunnelHTTPBackendTransportPool) loadUsableTransport(key string, now time.Time) *http.Transport {
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

func (p *tunnelHTTPBackendTransportPool) storeTransport(key string, transport *http.Transport, now time.Time) ([]*http.Transport, *http.Transport, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	stale := p.maybePruneExpiredLocked(now)
	if entry := p.items[key]; entry != nil && entry.transport != nil {
		entry.markUsed(now)
		return stale, entry.transport, false
	}
	if p.items == nil {
		p.items = make(map[string]*tunnelHTTPBackendEntry)
	}
	p.items[key] = newTunnelHTTPBackendEntry(transport, now)
	evicted := p.enforceCapacityLocked()
	return append(stale, evicted), transport, true
}

func (p *tunnelHTTPBackendTransportPool) pruneExpiredLocked(now time.Time) []*http.Transport {
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

func (p *tunnelHTTPBackendTransportPool) maybePruneExpiredLocked(now time.Time) []*http.Transport {
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

func (p *tunnelHTTPBackendTransportPool) pruneDueLocked(now time.Time) bool {
	return p != nil && (p.nextPruneAt.IsZero() || !now.Before(p.nextPruneAt))
}

func (p *tunnelHTTPBackendTransportPool) pruneInterval() time.Duration {
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

func (p *tunnelHTTPBackendTransportPool) enforceCapacityLocked() *http.Transport {
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

func newTunnelHTTPBackendEntry(transport *http.Transport, now time.Time) *tunnelHTTPBackendEntry {
	entry := &tunnelHTTPBackendEntry{transport: transport}
	entry.markUsed(now)
	return entry
}

func (e *tunnelHTTPBackendEntry) markUsed(now time.Time) {
	if e == nil {
		return
	}
	e.lastUsedNS.Store(now.UnixNano())
}

func (e *tunnelHTTPBackendEntry) lastUsed() time.Time {
	if e == nil {
		return time.Time{}
	}
	lastUsedNS := e.lastUsedNS.Load()
	if lastUsedNS == 0 {
		return time.Time{}
	}
	return time.Unix(0, lastUsedNS)
}

func selectTunnelHTTPBackend(task *file.Tunnel, server *TunnelModeServer, targetAddr string) (tunnelHTTPBackendSelection, error) {
	targetAddr = strings.TrimSpace(targetAddr)
	if task == nil {
		return tunnelHTTPBackendSelection{}, errTunnelHTTPProxyMissingTarget
	}
	if targetAddr == "" {
		return tunnelHTTPBackendSelection{}, errTunnelHTTPProxyMissingTarget
	}
	selectedTask := task.SelectRuntimeRoute()
	if selectedTask == nil {
		return tunnelHTTPBackendSelection{}, errTunnelHTTPProxyMissingTarget
	}
	if err := validateTunnelHTTPBackendTask(selectedTask); err != nil {
		return tunnelHTTPBackendSelection{}, err
	}
	routeUUID := ""
	if server != nil {
		routeUUID = server.SelectClientRouteUUID(selectedTask.Client, selectedTask.RuntimeRouteUUID())
	}
	return tunnelHTTPBackendSelection{
		routeUUID:  routeUUID,
		targetAddr: targetAddr,
		task:       selectedTask,
	}.normalized(), nil
}

func (s *TunnelModeServer) transportForHTTPBackend(selection tunnelHTTPBackendSelection) *http.Transport {
	selection = selection.normalized()
	pool := s.httpProxyBackendPool()
	return pool.GetOrCreate(selection, func() *http.Transport {
		return s.newHTTPBackendTransport(selection)
	})
}

func (s *TunnelModeServer) removeHTTPBackendTransport(selection tunnelHTTPBackendSelection) {
	if s == nil {
		return
	}
	s.httpProxyBackendPool().Remove(selection)
}

func (s *TunnelModeServer) newHTTPBackendTransport(selection tunnelHTTPBackendSelection) *http.Transport {
	selection = selection.normalized()
	return &http.Transport{
		ResponseHeaderTimeout: 100 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.dialSelectedHTTPBackend(ctx, selection, "")
		},
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
	}
}

func (s *TunnelModeServer) dialSelectedHTTPBackend(ctx context.Context, selection tunnelHTTPBackendSelection, remoteAddr string) (net.Conn, error) {
	ctx, resolved, err := s.resolveTunnelHTTPBackendState(ctx, selection, remoteAddr)
	if err != nil {
		return nil, err
	}
	return s.dialResolvedTunnelHTTPBackend(ctx, resolved)
}

func (s *TunnelModeServer) resolveTunnelHTTPBackendState(ctx context.Context, fallbackSelection tunnelHTTPBackendSelection, fallbackRemoteAddr string) (context.Context, tunnelHTTPProxyResolvedRequest, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	selection, err := resolveTunnelHTTPBackendSelection(ctx, fallbackSelection)
	if err != nil {
		return ctx, tunnelHTTPProxyResolvedRequest{}, err
	}
	if !selection.valid() {
		return ctx, tunnelHTTPProxyResolvedRequest{}, errTunnelHTTPProxyMissingTarget
	}
	if err := validateTunnelHTTPBackendTask(selection.task); err != nil {
		return ctx, tunnelHTTPProxyResolvedRequest{}, err
	}
	remoteAddr, err := resolveTunnelHTTPBackendRemoteAddr(ctx, fallbackRemoteAddr)
	if err != nil {
		return ctx, tunnelHTTPProxyResolvedRequest{}, err
	}
	resolved := tunnelHTTPProxyResolvedRequest{
		selection:    selection,
		routeRuntime: s.resolveTunnelHTTPRouteRuntime(ctx, selection),
		remoteAddr:   remoteAddr,
	}.normalized()
	ctx = withTunnelHTTPProxyResolvedRequest(ctx, resolved)
	return ctx, resolved, nil
}

func (s *TunnelModeServer) dialResolvedTunnelHTTPBackend(ctx context.Context, resolved tunnelHTTPProxyResolvedRequest) (net.Conn, error) {
	if resolved.routeRuntime == nil {
		return nil, errTunnelHTTPProxyInvalidBackend
	}
	if s.IsClientDestinationAccessDenied(resolved.selection.task.Client, resolved.selection.task, resolved.selection.targetAddr) {
		return nil, errTunnelHTTPProxyDestinationDenied
	}
	link, isLocal := s.buildTunnelHTTPBackendLink(resolved.selection, resolved.remoteAddr)
	target, err := s.OpenBridgeLink(resolved.selection.task.Client.Id, link, nil)
	if err != nil {
		logs.Trace("DialContext: connection to target %s failed: %v", resolved.selection.targetAddr, err)
		return nil, err
	}
	resolved.routeRuntime.UpdateFromLink(link)
	return s.wrapTunnelHTTPBackendConn(resolved.selection, resolved.routeRuntime, target, link, isLocal), nil
}

func resolveTunnelHTTPBackendSelection(ctx context.Context, fallback tunnelHTTPBackendSelection) (tunnelHTTPBackendSelection, error) {
	if ctx != nil {
		if data, ok := tunnelHTTPProxyContextValue(ctx); ok && data.selection.valid() {
			return data.selection, nil
		}
		if selection, ok := ctx.Value(tunnelHTTPProxyBackendKey).(tunnelHTTPBackendSelection); ok {
			selection = selection.normalized()
			if selection.valid() {
				return selection, nil
			}
		}
	}
	fallback = fallback.normalized()
	if fallback.valid() {
		return fallback, nil
	}
	return tunnelHTTPBackendSelection{}, errTunnelHTTPProxyMissingTarget
}

func resolveTunnelHTTPBackendRemoteAddr(ctx context.Context, fallback string) (string, error) {
	if ctx != nil {
		if data, ok := tunnelHTTPProxyContextValue(ctx); ok {
			if remoteAddr := strings.TrimSpace(data.remoteAddr); remoteAddr != "" {
				return remoteAddr, nil
			}
		}
		if remoteAddr, ok := ctx.Value(tunnelHTTPProxyRemoteAddrKey).(string); ok {
			remoteAddr = strings.TrimSpace(remoteAddr)
			if remoteAddr != "" {
				return remoteAddr, nil
			}
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("missing remote address context")
}

func closeIdleTunnelHTTPTransports(transports ...*http.Transport) {
	for _, transport := range transports {
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
}

func (s *TunnelModeServer) resolveTunnelHTTPRouteRuntime(ctx context.Context, selection tunnelHTTPBackendSelection) *RouteRuntimeContext {
	if data, ok := tunnelHTTPProxyContextValue(ctx); ok && data.routeRuntime != nil {
		if selection.routeUUID != "" {
			data.routeRuntime.SetRouteUUID(selection.routeUUID)
		}
		return data.routeRuntime
	}
	routeRuntime, _ := ctx.Value(tunnelHTTPProxyRouteRuntimeKey).(*RouteRuntimeContext)
	if routeRuntime == nil {
		return s.NewRouteRuntimeContext(selection.task.Client, selection.routeUUID)
	}
	if selection.routeUUID != "" {
		routeRuntime.SetRouteUUID(selection.routeUUID)
	}
	return routeRuntime
}

func (s *TunnelModeServer) buildTunnelHTTPBackendLink(selection tunnelHTTPBackendSelection, remoteAddr string) (*conn.Link, bool) {
	isLocal := s.LocalProxyAllowed() && selection.task.Target.LocalProxy || selection.task.Client.Id < 0
	return conn.NewLink("tcp", selection.targetAddr, selection.task.Client.Cnf.Crypt, selection.task.Client.Cnf.Compress, remoteAddr, isLocal, conn.WithRouteUUID(selection.routeUUID)), isLocal
}

func (s *TunnelModeServer) wrapTunnelHTTPBackendConn(selection tunnelHTTPBackendSelection, routeRuntime *RouteRuntimeContext, target net.Conn, link *conn.Link, isLocal bool) net.Conn {
	target = routeRuntime.TrackConn(target)
	bridgeLimiter := s.BridgeRateLimiter(selection.task.Client)
	if isLocal {
		bridgeLimiter = nil
	}
	rawConn := conn.GetConn(target, link.Crypt, link.Compress, bridgeLimiter, true, isLocal)
	rawConn = conn.WrapReadWriteCloserWithTrafficObserver(rawConn, routeRuntime.BridgeObserver(selection.task.Client))
	return WrapRuntimeRouteConn(conn.NewFlowConn(rawConn, selection.task.Flow, selection.task.Client.Flow), routeRuntime.RouteUUID())
}

func normalizeTunnelHTTPProxyTarget(req *http.Request) string {
	if req == nil {
		return ""
	}
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if parsed, err := url.Parse("//" + host); err == nil && parsed.Host != "" {
		host = parsed.Host
		if _, _, err := net.SplitHostPort(host); err == nil {
			return host
		}
	}
	defaultPort := "80"
	if req.Method == http.MethodConnect {
		defaultPort = "443"
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return net.JoinHostPort(host, defaultPort)
}
