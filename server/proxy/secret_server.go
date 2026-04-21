package proxy

import (
	"errors"
	"net"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var errSecretTargetRequired = errors.New("secret target is required")

type SecretServer struct {
	*BaseServer
	allowSecretLocal bool
}

type secretIngress struct {
	conn       *conn.Conn
	link       *conn.Link
	buffer     []byte
	needAck    bool
	remoteAddr string
}

type secretBackend struct {
	target       net.Conn
	link         *conn.Link
	routeRuntime *RouteRuntimeContext
	localProxy   bool
	connType     string
}

func NewSecretServer(bridge NetBridge, task *file.Tunnel, allowSecretLocal bool) *SecretServer {
	return newSecretServerWithRuntimeSources(currentProxyRuntimeSources(), bridge, task, allowSecretLocal)
}

func NewSecretServerWithRuntime(runtime proxyRuntimeContext, bridge NetBridge, task *file.Tunnel, allowSecretLocal bool) *SecretServer {
	return newSecretServerWithRuntimeSources(injectedProxyRuntimeSources(runtime), bridge, task, allowSecretLocal)
}

func NewSecretServerWithRuntimeRoot(runtimeRoot func() proxyRuntimeContext, bridge NetBridge, task *file.Tunnel, allowSecretLocal bool) *SecretServer {
	return NewSecretServerWithRuntimeRootAndLocalProxyAllowed(runtimeRoot, currentProxyLocalProxyAllowed, bridge, task, allowSecretLocal)
}

func NewSecretServerWithRuntimeRootAndLocalProxyAllowed(runtimeRoot func() proxyRuntimeContext, localProxyAllowed func() bool, bridge NetBridge, task *file.Tunnel, allowSecretLocal bool) *SecretServer {
	return newSecretServerWithRuntimeSources(proxyRuntimeSources{
		runtimeRoot:       runtimeRoot,
		localProxyAllowed: localProxyAllowed,
	}, bridge, task, allowSecretLocal)
}

func newSecretServerWithRuntimeSources(sources proxyRuntimeSources, bridge NetBridge, task *file.Tunnel, allowSecretLocal bool) *SecretServer {
	return &SecretServer{
		BaseServer:       newBaseServerWithRuntimeSources(sources, bridge, task),
		allowSecretLocal: allowSecretLocal,
	}
}

func (s *SecretServer) HandleSecret(src net.Conn) error {
	task, err := selectValidatedTunnelRuntimeTask(s.CurrentTask())
	if err != nil {
		if src != nil {
			_ = src.Close()
		}
		return err
	}
	lease, err := s.acquireSecretLease(task, src)
	if err != nil {
		return err
	}
	defer lease.Release()

	ingress := readSecretIngress(src)
	backend, err := s.openSecretBackend(task, ingress)
	if err != nil {
		_ = ingress.conn.Close()
		return err
	}
	if err := writeSecretACKIfNeeded(ingress, backend); err != nil {
		logs.Warn("write ACK failed: %v", err)
		_ = ingress.conn.Close()
		_ = backend.target.Close()
		return err
	}
	s.copySecretTraffic(task, ingress, backend)
	return nil
}

func (s *SecretServer) acquireSecretLease(task *file.Tunnel, src net.Conn) (*proxyConnectionLease, error) {
	lease, err := s.CheckFlowAndConnNum(task.Client, task, nil)
	if err != nil {
		_ = src.Close()
		logs.Warn("Connection limit exceeded, client id %d, host id %d, error %v", task.Client.Id, task.Id, err)
		return nil, err
	}
	return lease, nil
}

func readSecretIngress(src net.Conn) secretIngress {
	tee := conn.NewTeeConn(src)
	c := conn.NewConn(tee)
	lk, err := c.GetLinkInfo()
	ingress := secretIngress{
		conn:       c,
		link:       lk,
		remoteAddr: proxyConnRemoteAddr(c),
	}
	if err != nil || lk == nil {
		ingress.buffer = tee.Buffered()
	}
	tee.StopAndClean()
	if lk != nil {
		ingress.needAck = lk.Option.NeedAck
	}
	return ingress
}

func (s *SecretServer) openSecretBackend(task *file.Tunnel, ingress secretIngress) (*secretBackend, error) {
	connType, host, localProxy, err := resolveSecretRoute(task, ingress.link, s.allowSecretLocal)
	if err != nil {
		logs.Warn("resolve secret route failed, task id %d, error %v", task.Id, err)
		return nil, err
	}
	localProxy = s.LocalProxyAllowed() && localProxy || task.Client.Id < 0
	if s.IsClientDestinationAccessDenied(task.Client, task, host) {
		return nil, errProxyAccessDenied
	}
	link := conn.NewLink(connType, host, task.Client.Cnf.Crypt, task.Client.Cnf.Compress, ingress.remoteAddr, localProxy)
	target, err := s.OpenBridgeLink(task.Client.Id, link, task)
	if err != nil {
		logs.Warn("failed to get backend connection: %v", err)
		return nil, err
	}
	routeRuntime := s.NewRouteRuntimeContext(task.Client, task.RuntimeRouteUUID())
	routeRuntime.UpdateFromLink(link)
	return &secretBackend{
		target:       routeRuntime.TrackConn(target),
		link:         link,
		routeRuntime: routeRuntime,
		localProxy:   localProxy,
		connType:     connType,
	}, nil
}

func writeSecretACKIfNeeded(ingress secretIngress, backend *secretBackend) error {
	if !ingress.needAck || ingress.conn == nil || backend == nil || backend.link == nil {
		return nil
	}
	if err := conn.WriteACK(ingress.conn.Conn, backend.link.Option.Timeout); err != nil {
		return err
	}
	logs.Trace("sent ACK before proceeding")
	return nil
}

func (s *SecretServer) copySecretTraffic(task *file.Tunnel, ingress secretIngress, backend *secretBackend) {
	serviceObserver := backend.routeRuntime.ServiceObserver(task.Client, task, nil)
	serviceConn := conn.WrapNetConnWithTrafficObserver(ingress.conn.Conn, serviceObserver)
	serviceLimiter := s.ServiceRateLimiter(task.Client, task, nil)
	if err := ObserveBufferedIngress(ingress.buffer, serviceLimiter, serviceObserver.OnRead); err != nil {
		logs.Warn("observe secret buffered ingress failed: %v", err)
		_ = ingress.conn.Close()
		_ = backend.target.Close()
		return
	}
	if backend.localProxy {
		isFramed := backend.connType == common.CONN_UDP
		conn.CopyWaitGroup(backend.target, serviceConn, false, false, nil, serviceLimiter, []*file.Flow{task.Flow, task.Client.Flow}, false, task.Target.ProxyProtocol, ingress.buffer, task, backend.localProxy, isFramed)
		return
	}
	bridgeConn := conn.WrapNetConnWithTrafficObserver(backend.target, backend.routeRuntime.BridgeObserver(task.Client))
	conn.CopyWaitGroup(bridgeConn, serviceConn, backend.link.Crypt, backend.link.Compress, s.BridgeRateLimiter(task.Client), serviceLimiter, []*file.Flow{task.Flow, task.Client.Flow}, true, task.Target.ProxyProtocol, ingress.buffer, task, backend.localProxy, false)
}

func resolveSecretRoute(task *file.Tunnel, lk *conn.Link, allowSecretLocal bool) (string, string, bool, error) {
	if task == nil || task.Target == nil {
		return "", "", false, errSecretTargetRequired
	}
	if strings.TrimSpace(task.Target.TargetStr) != "" {
		host, err := task.Target.GetRandomTarget()
		if err != nil {
			return "", "", false, err
		}
		return resolveSecretConfiguredRoute(task, lk), common.FormatAddress(host), task.Target.LocalProxy, nil
	}
	if lk == nil {
		return "", "", false, errSecretTargetRequired
	}
	rawHost := strings.TrimSpace(lk.Host)
	if rawHost == "" {
		return "", "", false, errSecretTargetRequired
	}
	host := common.FormatAddress(rawHost)
	return resolveSecretDynamicConnType(lk.ConnType), host, allowSecretLocal && lk.LocalProxy, nil
}

func resolveSecretConfiguredRoute(task *file.Tunnel, lk *conn.Link) string {
	if task == nil {
		return common.CONN_TCP
	}
	switch strings.TrimSpace(task.TargetType) {
	case common.CONN_UDP:
		return common.CONN_UDP
	case common.CONN_ALL:
		if lk != nil && resolveSecretDynamicConnType(lk.ConnType) == common.CONN_UDP {
			return common.CONN_UDP
		}
	}
	return common.CONN_TCP
}

func resolveSecretDynamicConnType(connType string) string {
	if strings.TrimSpace(connType) == common.CONN_UDP {
		return common.CONN_UDP
	}
	return common.CONN_TCP
}
