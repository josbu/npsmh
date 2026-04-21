package proxy

import (
	"context"
	"net"
	"net/http/httptrace"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

type proxyTransportRuntime struct {
	localProxyAllowed  func() bool
	openBridgeLink     func(int, *conn.Link, *file.Tunnel) (net.Conn, error)
	serviceRateLimiter func(*file.Client, *file.Tunnel, *file.Host) rate.Limiter
	bridgeRateLimiter  func(*file.Client) rate.Limiter
	policy             proxyTransportPolicy
	routeStats         proxyRouteRuntimeCollector
}

func newProxyTransportRuntime(deps proxyBaseDependencies, limit proxyLimitRuntime, policy proxyTransportPolicy) proxyTransportRuntime {
	return proxyTransportRuntime{
		localProxyAllowed:  deps.LocalProxyAllowed,
		openBridgeLink:     deps.OpenBridgeLink,
		serviceRateLimiter: limit.ServiceRateLimiter,
		bridgeRateLimiter:  limit.BridgeRateLimiter,
		policy:             policy,
	}
}

func (r proxyTransportRuntime) withDefaults() proxyTransportRuntime {
	if r.policy == nil {
		r.policy = currentProxyRuntimeRoot().accessPolicy
	}
	return r
}

type proxyRouteRuntimeCollector interface {
	AddClientNodeConn(clientID int, uuid string)
	CutClientNodeConn(clientID int, uuid string)
	ObserveClientNodeBridgeTraffic(clientID int, uuid string, in, out int64)
	ObserveClientNodeServiceTraffic(clientID int, uuid string, in, out int64)
}

type proxyRouteRuntimeBoundNode interface {
	AddConn()
	CutConn()
	ObserveBridgeTraffic(in, out int64)
	ObserveServiceTraffic(in, out int64)
}

type proxyRouteRuntimeNodeBinder interface {
	LoadClientNodeRuntime(clientID int, uuid string) any
}

type proxyClientRuntimeNodeCounter interface {
	ClientOnlineNodeCount(clientID int) int
}

type proxyClientRuntimeMultiNodeChecker interface {
	ClientHasMultipleOnlineNodes(clientID int) bool
}

type proxyClientRuntimeSelectionPolicy interface {
	ClientSelectionCanRotate() bool
}

type proxyClientRouteSelector interface {
	SelectClientRouteUUID(clientID int) string
}

type runtimeRouteUUIDCarrier interface {
	RuntimeRouteUUID() string
}

type routeTrackedConn struct {
	net.Conn
	collector proxyRouteRuntimeCollector
	boundNode proxyRouteRuntimeBoundNode
	clientID  int
	routeUUID string
	closeOnce sync.Once
}

type routeTaggedConn struct {
	net.Conn
	routeUUID string
}

type routeRuntimeUUIDState struct {
	value string
}

type routeRuntimeNodeBindingState struct {
	routeUUID string
	node      proxyRouteRuntimeBoundNode
}

type proxyRouteRuntimeBinding struct {
	clientID  int
	routeUUID string
	boundNode proxyRouteRuntimeBoundNode
}

type RouteRuntimeContext struct {
	collector proxyRouteRuntimeCollector
	binder    proxyRouteRuntimeNodeBinder
	clientID  int

	routeUUID atomic.Pointer[routeRuntimeUUIDState]
	boundNode atomic.Pointer[routeRuntimeNodeBindingState]
}

func wrapRouteTrackedConn(target net.Conn, collector proxyRouteRuntimeCollector, clientID int, routeUUID string) net.Conn {
	return wrapRouteTrackedConnWithBoundNode(target, collector, clientID, routeUUID, resolveRouteRuntimeBoundNodeFromCollector(collector, clientID, routeUUID))
}

func wrapRouteTrackedConnWithBoundNode(target net.Conn, collector proxyRouteRuntimeCollector, clientID int, routeUUID string, boundNode proxyRouteRuntimeBoundNode) net.Conn {
	routeUUID = strings.TrimSpace(routeUUID)
	if isNilProxyRuntimeValue(target) {
		return nil
	}
	if boundNode != nil {
		boundNode.AddConn()
		return &routeTrackedConn{
			Conn:      target,
			boundNode: boundNode,
			clientID:  clientID,
			routeUUID: routeUUID,
		}
	}
	if isNilProxyRuntimeValue(collector) || clientID == 0 || routeUUID == "" {
		return target
	}
	collector.AddClientNodeConn(clientID, routeUUID)
	return &routeTrackedConn{
		Conn:      target,
		collector: collector,
		clientID:  clientID,
		routeUUID: routeUUID,
	}
}

func (c *routeTrackedConn) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		if c.boundNode != nil {
			c.boundNode.CutConn()
		} else if c.collector != nil && c.clientID != 0 && c.routeUUID != "" {
			c.collector.CutClientNodeConn(c.clientID, c.routeUUID)
		}
		if c.Conn != nil {
			err = c.Conn.Close()
		}
	})
	return err
}

func (c *routeTrackedConn) RuntimeRouteUUID() string {
	if c == nil {
		return ""
	}
	return c.routeUUID
}

func (c *routeTaggedConn) RuntimeRouteUUID() string {
	if c == nil {
		return ""
	}
	return c.routeUUID
}

func routeStatsCollectorFromLinkOpener(opener BridgeLinkOpener) proxyRouteRuntimeCollector {
	if isNilProxyRuntimeValue(opener) {
		return nil
	}
	collector, _ := opener.(proxyRouteRuntimeCollector)
	if isNilProxyRuntimeValue(collector) {
		return nil
	}
	return collector
}

func routeRuntimeBinderFromCollector(collector proxyRouteRuntimeCollector) proxyRouteRuntimeNodeBinder {
	if isNilProxyRuntimeValue(collector) {
		return nil
	}
	binder, _ := collector.(proxyRouteRuntimeNodeBinder)
	if isNilProxyRuntimeValue(binder) {
		return nil
	}
	return binder
}

func routeRuntimeBoundNodeFromValue(value any) proxyRouteRuntimeBoundNode {
	node, _ := value.(proxyRouteRuntimeBoundNode)
	if isNilProxyRuntimeValue(node) {
		return nil
	}
	return node
}

func resolveRouteRuntimeBoundNodeFromCollector(collector proxyRouteRuntimeCollector, clientID int, routeUUID string) proxyRouteRuntimeBoundNode {
	routeUUID = strings.TrimSpace(routeUUID)
	if clientID == 0 || routeUUID == "" {
		return nil
	}
	binder := routeRuntimeBinderFromCollector(collector)
	if binder == nil {
		return nil
	}
	return routeRuntimeBoundNodeFromValue(binder.LoadClientNodeRuntime(clientID, routeUUID))
}

func WrapRuntimeRouteConn(target net.Conn, routeUUID string) net.Conn {
	routeUUID = strings.TrimSpace(routeUUID)
	if isNilProxyRuntimeValue(target) {
		return nil
	}
	if routeUUID == "" {
		return target
	}
	if existing := RuntimeRouteUUIDFromConn(target); existing == routeUUID {
		return target
	}
	return &routeTaggedConn{
		Conn:      target,
		routeUUID: routeUUID,
	}
}

func RuntimeRouteUUIDFromConn(target net.Conn) string {
	if isNilProxyRuntimeValue(target) {
		return ""
	}
	carrier, ok := target.(runtimeRouteUUIDCarrier)
	if !ok || isNilProxyRuntimeValue(carrier) {
		return ""
	}
	return strings.TrimSpace(carrier.RuntimeRouteUUID())
}

func newProxyRouteRuntimeBinding(collector proxyRouteRuntimeCollector, client *file.Client, routeUUID string) proxyRouteRuntimeBinding {
	binding := proxyRouteRuntimeBinding{
		routeUUID: strings.TrimSpace(routeUUID),
	}
	if client != nil {
		binding.clientID = client.Id
	}
	binding.boundNode = resolveRouteRuntimeBoundNodeFromCollector(collector, binding.clientID, binding.routeUUID)
	return binding
}

func linkRuntimeRouteUUID(link *conn.Link) string {
	if link == nil {
		return ""
	}
	return strings.TrimSpace(link.Option.RouteUUID)
}

func serviceRuntimeRouteUUID(tunnel *file.Tunnel, host *file.Host) string {
	switch {
	case tunnel != nil:
		return strings.TrimSpace(tunnel.RuntimeRouteUUID())
	case host != nil:
		return strings.TrimSpace(host.RuntimeRouteUUID())
	default:
		return ""
	}
}

func WithRouteRuntimeTrace(ctx context.Context, routeRuntime *RouteRuntimeContext) context.Context {
	if routeRuntime == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if routeUUID := RuntimeRouteUUIDFromConn(info.Conn); routeUUID != "" {
				routeRuntime.SetRouteUUID(routeUUID)
			}
		},
	}
	return httptrace.WithClientTrace(ctx, trace)
}

func newRouteRuntimeContext(collector proxyRouteRuntimeCollector, client *file.Client, routeUUID string) *RouteRuntimeContext {
	ctx := &RouteRuntimeContext{
		collector: collector,
		binder:    routeRuntimeBinderFromCollector(collector),
	}
	if client != nil && client.Id > 0 {
		ctx.clientID = client.Id
	}
	ctx.SetRouteUUID(routeUUID)
	return ctx
}

func (s *BaseServer) routeStatsCollector() proxyRouteRuntimeCollector {
	if s == nil {
		return nil
	}
	return routeStatsCollectorFromLinkOpener(s.linkOpener)
}

func (s *BaseServer) NewRouteRuntimeContext(client *file.Client, routeUUID string) *RouteRuntimeContext {
	return newRouteRuntimeContext(s.routeStatsCollector(), client, routeUUID)
}

func (c *RouteRuntimeContext) RouteUUID() string {
	if c == nil {
		return ""
	}
	state := c.routeUUID.Load()
	if state == nil {
		return ""
	}
	return state.value
}

func (c *RouteRuntimeContext) SetRouteUUID(routeUUID string) {
	if c == nil {
		return
	}
	routeUUID = strings.TrimSpace(routeUUID)
	if current := c.routeUUID.Load(); current != nil && current.value == routeUUID {
		return
	}
	c.routeUUID.Store(&routeRuntimeUUIDState{value: routeUUID})
	c.rebindNodeRuntime(routeUUID)
}

func (c *RouteRuntimeContext) UpdateFromLink(link *conn.Link) {
	if c == nil || link == nil {
		return
	}
	if routeUUID := strings.TrimSpace(link.Option.RouteUUID); routeUUID != "" {
		c.SetRouteUUID(routeUUID)
	}
}

func (c *RouteRuntimeContext) TrackConn(target net.Conn) net.Conn {
	if c == nil {
		return target
	}
	binding := c.currentRouteBinding()
	return wrapRouteTrackedConnWithBoundNode(target, c.collector, c.clientID, binding.routeUUID, binding.boundNode)
}

func (c *RouteRuntimeContext) BridgeObserver(client *file.Client) conn.TrafficObserver {
	if c == nil {
		return bridgeTrafficObserver(client, nil, "")
	}
	binding := c.currentRouteBinding()
	return bridgeTrafficObserverWithBoundNode(client, c.collector, binding.routeUUID, binding.boundNode)
}

func (c *RouteRuntimeContext) ServiceObserver(client *file.Client, tunnel *file.Tunnel, host *file.Host) conn.TrafficObserver {
	if c == nil {
		return serviceTrafficObserver(client, tunnel, host, nil, "")
	}
	binding := c.currentRouteBinding()
	return serviceTrafficObserverWithBoundNode(client, tunnel, host, c.collector, binding.routeUUID, binding.boundNode)
}

func (c *RouteRuntimeContext) ObserveBridgeTraffic(client *file.Client, in, out int64) error {
	if c == nil {
		return observeBridgeTraffic(client, nil, "", in, out)
	}
	binding := c.currentRouteBinding()
	return observeBridgeTrafficWithBoundNode(client, c.collector, binding.routeUUID, binding.boundNode, in, out)
}

func (c *RouteRuntimeContext) ObserveServiceTraffic(client *file.Client, tunnel *file.Tunnel, host *file.Host, in, out int64) error {
	if c == nil {
		return observeServiceTraffic(client, tunnel, host, nil, "", in, out)
	}
	binding := c.currentRouteBinding()
	return observeServiceTrafficWithBoundNode(client, tunnel, host, c.collector, binding.routeUUID, binding.boundNode, in, out)
}

func (c *RouteRuntimeContext) rebindNodeRuntime(routeUUID string) {
	if c == nil {
		return
	}
	routeUUID = strings.TrimSpace(routeUUID)
	if c.binder == nil || c.clientID == 0 || routeUUID == "" {
		c.boundNode.Store(nil)
		return
	}
	c.boundNode.Store(&routeRuntimeNodeBindingState{
		routeUUID: routeUUID,
		node:      routeRuntimeBoundNodeFromValue(c.binder.LoadClientNodeRuntime(c.clientID, routeUUID)),
	})
}

func (c *RouteRuntimeContext) boundNodeRuntime(routeUUID string) proxyRouteRuntimeBoundNode {
	if c == nil {
		return nil
	}
	routeUUID = strings.TrimSpace(routeUUID)
	if routeUUID == "" {
		return nil
	}
	if state := c.boundNode.Load(); state != nil && state.routeUUID == routeUUID {
		return state.node
	}
	c.rebindNodeRuntime(routeUUID)
	if state := c.boundNode.Load(); state != nil && state.routeUUID == routeUUID {
		return state.node
	}
	return nil
}

func (c *RouteRuntimeContext) currentRouteBinding() proxyRouteRuntimeBinding {
	if c == nil {
		return proxyRouteRuntimeBinding{}
	}
	binding := proxyRouteRuntimeBinding{
		clientID:  c.clientID,
		routeUUID: c.RouteUUID(),
	}
	if binding.routeUUID != "" {
		binding.boundNode = c.boundNodeRuntime(binding.routeUUID)
	}
	return binding
}

func (s *BaseServer) transportRuntime() proxyTransportRuntime {
	runtimeContext := s.runtimeContext()
	runtime := newProxyTransportRuntime(newProxyBaseDependencies(s), newProxyLimitRuntime(runtimeContext), runtimeContext.accessPolicy)
	runtime.routeStats = s.routeStatsCollector()
	return runtime
}

func (s *BaseServer) ClientNeedsPerRequestBackend(client *file.Client, routeUUID string) bool {
	if s == nil || client == nil || client.Id <= 0 || strings.TrimSpace(routeUUID) != "" {
		return false
	}
	counter, counterOK := s.linkOpener.(proxyClientRuntimeNodeCounter)
	if checker, ok := s.linkOpener.(proxyClientRuntimeMultiNodeChecker); ok && !isNilProxyRuntimeValue(checker) {
		if !checker.ClientHasMultipleOnlineNodes(client.Id) {
			return false
		}
	} else {
		if !counterOK || isNilProxyRuntimeValue(counter) {
			return false
		}
		if counter.ClientOnlineNodeCount(client.Id) <= 1 {
			return false
		}
	}
	if policy, ok := s.linkOpener.(proxyClientRuntimeSelectionPolicy); ok && !isNilProxyRuntimeValue(policy) {
		return policy.ClientSelectionCanRotate()
	}
	return true
}

func (s *BaseServer) SelectClientRouteUUID(client *file.Client, routeUUID string) string {
	if s == nil {
		return strings.TrimSpace(routeUUID)
	}
	routeUUID = strings.TrimSpace(routeUUID)
	if routeUUID != "" || client == nil || client.Id <= 0 {
		return routeUUID
	}
	selector, ok := s.linkOpener.(proxyClientRouteSelector)
	if !ok || isNilProxyRuntimeValue(selector) {
		return ""
	}
	return strings.TrimSpace(selector.SelectClientRouteUUID(client.Id))
}

func isNilProxyRuntimeValue(value interface{}) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

const (
	minParallelCloseWorkers = 8
	maxParallelCloseWorkers = 128
)

func normalizedParallelCloseWorkers(total int) int {
	if total <= 1 {
		return total
	}

	parallelism := runtime.GOMAXPROCS(0) * 4
	if parallelism < minParallelCloseWorkers {
		parallelism = minParallelCloseWorkers
	}
	if parallelism > maxParallelCloseWorkers {
		parallelism = maxParallelCloseWorkers
	}
	if parallelism > total {
		parallelism = total
	}
	return parallelism
}

func parallelCloseTargets[T any](targets []T, closeTarget func(T)) {
	if len(targets) == 0 || closeTarget == nil {
		return
	}

	parallelism := normalizedParallelCloseWorkers(len(targets))
	if parallelism <= 1 {
		for _, target := range targets {
			closeTarget(target)
		}
		return
	}

	workCh := make(chan T, parallelism)
	var wg sync.WaitGroup
	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for target := range workCh {
				closeTarget(target)
			}
		}()
	}
	for _, target := range targets {
		workCh <- target
	}
	close(workCh)
	wg.Wait()
}

func (s *BaseServer) LocalProxyAllowed() bool {
	if s != nil {
		return s.sources.resolvedLocalProxyAllowed()()
	}
	return currentProxyLocalProxyAllowed()
}

func (s *BaseServer) DealClient(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []*file.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel) error {
	return s.transportRuntime().DealClient(c, client, addr, rb, tp, f, flows, proxyProtocol, localProxy, task)
}

func (s *BaseServer) DealClientWithOptions(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []*file.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel, opts ...conn.Option) error {
	return s.transportRuntime().DealClientWithOptions(c, client, addr, rb, tp, f, flows, proxyProtocol, localProxy, task, opts...)
}

func (s *BaseServer) openClientLink(c *conn.Conn, client *file.Client, addr, tp string, localProxy bool, task *file.Tunnel, opts ...conn.Option) (net.Conn, *conn.Link, bool, error) {
	return s.transportRuntime().OpenClientLink(c, client, addr, tp, localProxy, task, opts...)
}

func (s *BaseServer) pipeClientConn(target net.Conn, c *conn.Conn, link *conn.Link, client *file.Client, flows []*file.Flow, proxyProtocol int, rb []byte, task *file.Tunnel, isLocal bool, tp string) {
	s.transportRuntime().PipeClientConn(target, c, link, client, flows, proxyProtocol, rb, task, isLocal, tp)
}

func (r proxyTransportRuntime) DealClient(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []*file.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel) error {
	return r.DealClientWithOptions(c, client, addr, rb, tp, f, flows, proxyProtocol, localProxy, task)
}

func (r proxyTransportRuntime) DealClientWithOptions(c *conn.Conn, client *file.Client, addr string,
	rb []byte, tp string, f func(), flows []*file.Flow, proxyProtocol int, localProxy bool, task *file.Tunnel, opts ...conn.Option) error {
	target, link, isLocal, err := r.OpenClientLink(c, client, addr, tp, localProxy, task, opts...)
	if err != nil {
		_ = c.Close()
		return err
	}
	if target == nil || link == nil {
		_ = c.Close()
		return nil
	}

	if f != nil {
		f()
	}
	r.PipeClientConn(target, c, link, client, flows, proxyProtocol, rb, task, isLocal, tp)
	return nil
}

func (r proxyTransportRuntime) OpenClientLink(c *conn.Conn, client *file.Client, addr, tp string, localProxy bool, task *file.Tunnel, opts ...conn.Option) (net.Conn, *conn.Link, bool, error) {
	r = r.withDefaults()
	remoteAddr := proxyConnRemoteAddr(c)
	if r.accessDenied(client, task, remoteAddr, addr) {
		return nil, nil, false, errProxyAccessDenied
	}
	isLocal := (r.localProxyAllowed != nil && r.localProxyAllowed() && localProxy) || client.Id < 0
	link := conn.NewLink(tp, addr, client.Cnf.Crypt, client.Cnf.Compress, remoteAddr, isLocal, opts...)
	inheritLinkRouteUUID(link, task)
	if r.openBridgeLink == nil {
		return nil, nil, false, errProxyBridgeUnavailable
	}
	target, err := r.openBridgeLink(client.Id, link, task)
	if err != nil {
		logs.Warn("get connection from client Id %d  error %v", client.Id, err)
		return nil, nil, false, err
	}
	return target, link, isLocal, nil
}

func proxyConnRemoteAddr(c *conn.Conn) string {
	if c == nil || c.RemoteAddr() == nil {
		return ""
	}
	return c.RemoteAddr().String()
}

func inheritLinkRouteUUID(link *conn.Link, task *file.Tunnel) {
	if link == nil || task == nil {
		return
	}
	if strings.TrimSpace(link.Option.RouteUUID) != "" {
		return
	}
	if routeUUID := strings.TrimSpace(task.RuntimeRouteUUID()); routeUUID != "" {
		link.Option.RouteUUID = routeUUID
	}
}

func (r proxyTransportRuntime) accessDenied(client *file.Client, task *file.Tunnel, remoteAddr, targetAddr string) bool {
	r = r.withDefaults()
	return r.policy.IsClientSourceAccessDenied(client, task, remoteAddr) ||
		r.policy.IsClientDestinationAccessDenied(client, task, targetAddr)
}

func (r proxyTransportRuntime) PipeClientConn(target net.Conn, c *conn.Conn, link *conn.Link, client *file.Client, flows []*file.Flow, proxyProtocol int, rb []byte, task *file.Tunnel, isLocal bool, tp string) {
	isFramed := tp == common.CONN_UDP
	serviceLimiter := rate.Limiter(nil)
	if r.serviceRateLimiter != nil {
		serviceLimiter = r.serviceRateLimiter(client, task, nil)
	}
	bridgeLimiter := rate.Limiter(nil)
	binding := newProxyRouteRuntimeBinding(r.routeStats, client, linkRuntimeRouteUUID(link))
	if !isLocal {
		if r.bridgeRateLimiter != nil {
			bridgeLimiter = r.bridgeRateLimiter(client)
		}
		target = wrapRouteTrackedConnWithBoundNode(target, r.routeStats, binding.clientID, binding.routeUUID, binding.boundNode)
		target = conn.WrapNetConnWithTrafficObserver(target, bridgeTrafficObserverWithBoundNode(client, r.routeStats, binding.routeUUID, binding.boundNode))
	}
	serviceObserver := serviceTrafficObserverWithBoundNode(client, task, nil, r.routeStats, binding.routeUUID, binding.boundNode)
	serviceConn := conn.WrapNetConnWithTrafficObserver(c.Conn, serviceObserver)
	if err := ObserveBufferedIngress(rb, serviceLimiter, serviceObserver.OnRead); err != nil {
		logs.Warn("buffered service ingress observe failed: %v", err)
		_ = target.Close()
		_ = c.Close()
		return
	}
	conn.CopyWaitGroup(target, serviceConn, link.Crypt, link.Compress, bridgeLimiter, serviceLimiter, flows, true, proxyProtocol, rb, task, isLocal, isFramed)
}

// ObserveBufferedIngress accounts for bytes that were already read into a temporary
// buffer before the traffic observer/limiter wrappers were attached.
func ObserveBufferedIngress(rb []byte, limiter rate.Limiter, observer conn.ByteObserver) error {
	if len(rb) == 0 {
		return nil
	}
	size := int64(len(rb))
	if limiter != nil {
		limiter.Get(size)
	}
	if observer != nil {
		if err := observer(size); err != nil {
			if limiter != nil {
				limiter.ReturnBucket(size)
			}
			return err
		}
	}
	return nil
}

func observeBridgeTraffic(client *file.Client, routeStats proxyRouteRuntimeCollector, routeUUID string, in, out int64) error {
	return observeBridgeTrafficWithBoundNode(client, routeStats, routeUUID, nil, in, out)
}

func observeBridgeTrafficWithBoundNode(client *file.Client, routeStats proxyRouteRuntimeCollector, routeUUID string, boundNode proxyRouteRuntimeBoundNode, in, out int64) error {
	if (client == nil && routeStats == nil) || (in == 0 && out == 0) {
		return nil
	}
	if boundNode != nil {
		boundNode.ObserveBridgeTraffic(in, out)
	} else if routeStats != nil && client != nil && routeUUID != "" {
		routeStats.ObserveClientNodeBridgeTraffic(client.Id, routeUUID, in, out)
	}
	if client != nil {
		return client.ObserveBridgeTraffic(in, out)
	}
	return nil
}

func observeServiceTraffic(client *file.Client, tunnel *file.Tunnel, host *file.Host, routeStats proxyRouteRuntimeCollector, routeUUID string, in, out int64) error {
	return observeServiceTrafficWithBoundNode(client, tunnel, host, routeStats, routeUUID, nil, in, out)
}

func observeServiceTrafficWithBoundNode(client *file.Client, tunnel *file.Tunnel, host *file.Host, routeStats proxyRouteRuntimeCollector, routeUUID string, boundNode proxyRouteRuntimeBoundNode, in, out int64) error {
	if in == 0 && out == 0 {
		return nil
	}
	if boundNode != nil {
		boundNode.ObserveServiceTraffic(in, out)
	} else if routeStats != nil && client != nil && routeUUID != "" {
		routeStats.ObserveClientNodeServiceTraffic(client.Id, routeUUID, in, out)
	}
	switch {
	case tunnel != nil:
		if err := tunnel.ObserveServiceTraffic(in, out); err != nil {
			return err
		}
	case host != nil:
		if err := host.ObserveServiceTraffic(in, out); err != nil {
			return err
		}
	}
	if client != nil {
		return client.ObserveServiceTraffic(in, out)
	}
	return nil
}

func bridgeTrafficObserver(client *file.Client, routeStats proxyRouteRuntimeCollector, routeUUID string) conn.TrafficObserver {
	binding := newProxyRouteRuntimeBinding(routeStats, client, routeUUID)
	return bridgeTrafficObserverWithBoundNode(client, routeStats, binding.routeUUID, binding.boundNode)
}

func bridgeTrafficObserverWithBoundNode(client *file.Client, routeStats proxyRouteRuntimeCollector, routeUUID string, boundNode proxyRouteRuntimeBoundNode) conn.TrafficObserver {
	return conn.TrafficObserver{
		OnRead: func(size int64) error {
			return observeBridgeTrafficWithBoundNode(client, routeStats, routeUUID, boundNode, size, 0)
		},
		OnWrite: func(size int64) error {
			return observeBridgeTrafficWithBoundNode(client, routeStats, routeUUID, boundNode, 0, size)
		},
	}
}

func serviceTrafficObserver(client *file.Client, tunnel *file.Tunnel, host *file.Host, routeStats proxyRouteRuntimeCollector, routeUUID string) conn.TrafficObserver {
	binding := newProxyRouteRuntimeBinding(routeStats, client, routeUUID)
	return serviceTrafficObserverWithBoundNode(client, tunnel, host, routeStats, binding.routeUUID, binding.boundNode)
}

func serviceTrafficObserverWithBoundNode(client *file.Client, tunnel *file.Tunnel, host *file.Host, routeStats proxyRouteRuntimeCollector, routeUUID string, boundNode proxyRouteRuntimeBoundNode) conn.TrafficObserver {
	return conn.TrafficObserver{
		OnRead: func(size int64) error {
			return observeServiceTrafficWithBoundNode(client, tunnel, host, routeStats, routeUUID, boundNode, size, 0)
		},
		OnWrite: func(size int64) error {
			return observeServiceTrafficWithBoundNode(client, tunnel, host, routeStats, routeUUID, boundNode, 0, size)
		},
	}
}

func (s *BaseServer) ObserveServiceTraffic(client *file.Client, tunnel *file.Tunnel, host *file.Host, in, out int64) error {
	collector := s.routeStatsCollector()
	binding := newProxyRouteRuntimeBinding(collector, client, serviceRuntimeRouteUUID(tunnel, host))
	return observeServiceTrafficWithBoundNode(client, tunnel, host, collector, binding.routeUUID, binding.boundNode, in, out)
}

func (s *BaseServer) BridgeTrafficObserver(client *file.Client, routeUUID ...string) conn.TrafficObserver {
	selected := ""
	if len(routeUUID) > 0 {
		selected = routeUUID[0]
	}
	collector := s.routeStatsCollector()
	binding := newProxyRouteRuntimeBinding(collector, client, selected)
	return bridgeTrafficObserverWithBoundNode(client, collector, binding.routeUUID, binding.boundNode)
}

func (s *BaseServer) ServiceTrafficObserver(client *file.Client, tunnel *file.Tunnel, host *file.Host) conn.TrafficObserver {
	collector := s.routeStatsCollector()
	binding := newProxyRouteRuntimeBinding(collector, client, serviceRuntimeRouteUUID(tunnel, host))
	return serviceTrafficObserverWithBoundNode(client, tunnel, host, collector, binding.routeUUID, binding.boundNode)
}
