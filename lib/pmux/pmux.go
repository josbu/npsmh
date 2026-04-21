// Package pmux handles shared external TCP listeners only.
// It classifies bridge/http/https/web ingress traffic and does not own the
// bridge/web internal virtual-listener pipeline.
package pmux

import (
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

var classifyReadTimeout = 10 * time.Second

const (
	HTTP_GET     = 716984
	HTTP_POST    = 807983
	HTTP_HEAD    = 726965
	HTTP_PUT     = 808585
	HTTP_DELETE  = 686976
	HTTP_CONNECT = 677978
	HTTP_OPTIONS = 798084
	HTTP_TRACE   = 848265
	CLIENT       = 848384
)

type PortMux struct {
	net.Listener
	bindIP      string
	port        int
	closed      chan struct{}
	closeOnce   sync.Once
	managerHost string
	clientHost  string
	bridgePath  string
	registry    routeRegistry
}

type routeKind string

const (
	routeClient            routeKind = "client_tcp"
	routeClientTLS         routeKind = "client_tls"
	routeClientWS          routeKind = "client_ws"
	routeClientWSS         routeKind = "client_wss"
	routeClientReservedTLS routeKind = "client_reserved_tls"
	routeHTTP              routeKind = "http"
	routeHTTPS             routeKind = "https"
	routeManager           routeKind = "manager_http"
	routeManagerTLS        routeKind = "manager_tls"
)

type routeFamily string

const (
	routeFamilyHTTP routeFamily = "http"
	routeFamilyTLS  routeFamily = "tls"
)

type routeMatcher struct {
	family        routeFamily
	host          string
	path          string
	websocketOnly bool
	allowEmptySNI bool
	fallback      bool
	priority      int
}

type registeredRoute struct {
	kind    routeKind
	route   *portRoute
	matcher routeMatcher
}

type routeRegistry struct {
	routes map[routeKind]*registeredRoute
}

func newRouteRegistry() routeRegistry {
	return routeRegistry{
		routes: make(map[routeKind]*registeredRoute),
	}
}

func (r *routeRegistry) ensure(kind routeKind, addr net.Addr, matcher routeMatcher) *portRoute {
	if r.routes == nil {
		r.routes = make(map[routeKind]*registeredRoute)
	}
	if existing := r.routes[kind]; existing != nil {
		existing.matcher = matcher
		return existing.route
	}
	route := newPortRoute(addr)
	r.routes[kind] = &registeredRoute{
		kind:    kind,
		route:   route,
		matcher: matcher,
	}
	return route
}

func (r *routeRegistry) get(kind routeKind) *portRoute {
	if entry := r.entry(kind); entry != nil {
		return entry.route
	}
	return nil
}

func (r *routeRegistry) entry(kind routeKind) *registeredRoute {
	if r == nil || r.routes == nil {
		return nil
	}
	return r.routes[kind]
}

func (r *routeRegistry) closeAll() {
	if r == nil {
		return
	}
	for _, entry := range r.routes {
		if entry != nil && entry.route != nil {
			entry.route.close()
		}
	}
}

func (r *routeRegistry) entriesByFamily(family routeFamily) []*registeredRoute {
	if r == nil || r.routes == nil {
		return nil
	}
	entries := make([]*registeredRoute, 0, len(r.routes))
	for _, entry := range r.routes {
		if entry == nil || entry.route == nil || entry.matcher.family != family {
			continue
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].matcher.priority == entries[j].matcher.priority {
			return entries[i].kind < entries[j].kind
		}
		return entries[i].matcher.priority > entries[j].matcher.priority
	})
	return entries
}

func normalizeRouteHost(host string) string {
	return strings.TrimSpace(common.GetIpByAddr(host))
}

func NewPortMux(bindIP string, port int, managerHost, clientHost, bridgePath string) *PortMux {
	if bindIP == "" {
		bindIP = "0.0.0.0"
	}
	pMux := &PortMux{
		bindIP:      bindIP,
		managerHost: managerHost,
		clientHost:  clientHost,
		bridgePath:  bridgePath,
		port:        port,
		closed:      make(chan struct{}),
		registry:    newRouteRegistry(),
	}
	return pMux
}

func (pMux *PortMux) Start() error {
	if pMux.Listener != nil {
		return nil
	}
	// Port multiplexing is based on TCP only
	tcpAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(pMux.bindIP, strconv.Itoa(pMux.port)))
	if err != nil {
		return err
	}
	pMux.Listener, err = net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}
	go func() {
		for {
			conn, err := pMux.Listener.Accept()
			if err != nil {
				select {
				case <-pMux.closed:
					return
				default:
				}
				logs.Warn("%v", err)
				_ = pMux.Close()
				return
			}
			go pMux.process(conn)
		}
	}()
	return nil
}

func (pMux *PortMux) process(conn net.Conn) {
	if conn == nil {
		return
	}
	if classifyReadTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(classifyReadTimeout))
	}
	// Recognition according to different signs
	// read 3 byte
	buf := make([]byte, 3)
	if n, err := io.ReadFull(conn, buf); err != nil || n != 3 {
		_ = conn.Close()
		return
	}
	var route *portRoute
	var rs []byte
	switch common.BytesToNum(buf) {
	case HTTP_CONNECT, HTTP_DELETE, HTTP_GET, HTTP_HEAD, HTTP_OPTIONS, HTTP_POST, HTTP_PUT, HTTP_TRACE: //http and manager
		route, rs, _ = pMux.classifyHTTP(conn, buf)
		if route == nil {
			_ = conn.Close()
			return
		}
	case CLIENT: // client connection
		route = pMux.getRoute(routeClient)
		if route == nil {
			_ = conn.Close()
			return
		}
	default: // https or clientTls or clientWss
		route, rs, _ = pMux.classifyTLS(conn, buf)
		if route == nil {
			_ = conn.Close()
			return
		}
	}
	if len(rs) == 0 {
		rs = buf
	}
	if route == nil {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	route.dispatch(newPortConn(conn, rs), pMux.closed)
}

func (pMux *PortMux) Close() error {
	pMux.closeOnce.Do(func() {
		if pMux.closed != nil {
			close(pMux.closed)
		}
		pMux.registry.closeAll()
		if pMux.Listener != nil {
			_ = pMux.Listener.Close()
		}
	})
	return nil
}

func (pMux *PortMux) GetClientListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeClient))
}

func (pMux *PortMux) GetClientTlsListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeClientTLS))
}

func (pMux *PortMux) GetClientWsListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeClientWS))
}

func (pMux *PortMux) GetClientWssListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeClientWSS))
}

func (pMux *PortMux) GetClientReservedTLSListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeClientReservedTLS))
}

func (pMux *PortMux) GetHttpListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeHTTP))
}

func (pMux *PortMux) GetHttpsListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeHTTPS))
}

func (pMux *PortMux) GetManagerListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeManager))
}

func (pMux *PortMux) GetManagerTLSListener() net.Listener {
	return NewPortListener(pMux.ensureRoute(routeManagerTLS))
}

func (pMux *PortMux) routeMatcher(kind routeKind) routeMatcher {
	switch kind {
	case routeClientWS:
		return routeMatcher{
			family:        routeFamilyHTTP,
			host:          normalizeRouteHost(pMux.clientHost),
			path:          pMux.normalizedBridgePath(),
			websocketOnly: true,
			priority:      100,
		}
	case routeManager:
		return routeMatcher{
			family:   routeFamilyHTTP,
			host:     normalizeRouteHost(pMux.managerHost),
			priority: 90,
		}
	case routeHTTP:
		return routeMatcher{
			family:   routeFamilyHTTP,
			fallback: true,
			priority: 10,
		}
	case routeManagerTLS:
		return routeMatcher{
			family:   routeFamilyTLS,
			host:     normalizeRouteHost(pMux.managerHost),
			priority: 100,
		}
	case routeClientWSS:
		return routeMatcher{
			family:   routeFamilyTLS,
			host:     normalizeRouteHost(pMux.clientHost),
			priority: 90,
		}
	case routeClientReservedTLS:
		return routeMatcher{
			family:   routeFamilyTLS,
			host:     normalizeRouteHost(pMux.clientHost),
			priority: 95,
		}
	case routeClientTLS:
		return routeMatcher{
			family:        routeFamilyTLS,
			host:          normalizeRouteHost(pMux.clientHost),
			allowEmptySNI: true,
			priority:      80,
		}
	case routeHTTPS:
		return routeMatcher{
			family:   routeFamilyTLS,
			fallback: true,
			priority: 10,
		}
	default:
		return routeMatcher{}
	}
}

func (pMux *PortMux) routeAddr() net.Addr {
	if pMux == nil {
		return nil
	}
	if pMux.Listener != nil {
		return pMux.Listener.Addr()
	}
	ip := net.ParseIP(pMux.bindIP)
	if ip == nil && pMux.bindIP == "" {
		ip = net.ParseIP("0.0.0.0")
	}
	return &net.TCPAddr{IP: ip, Port: pMux.port}
}

func (pMux *PortMux) ensureRoute(kind routeKind) *portRoute {
	if pMux == nil {
		return nil
	}
	return pMux.registry.ensure(kind, pMux.routeAddr(), pMux.routeMatcher(kind))
}

func (pMux *PortMux) getRoute(kind routeKind) *portRoute {
	if pMux == nil {
		return nil
	}
	return pMux.registry.get(kind)
}

func (pMux *PortMux) getRouteEntry(kind routeKind) *registeredRoute {
	if pMux == nil {
		return nil
	}
	return pMux.registry.entry(kind)
}

func (pMux *PortMux) routeEntries(family routeFamily) []*registeredRoute {
	if pMux == nil {
		return nil
	}
	return pMux.registry.entriesByFamily(family)
}
