package proxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/transport"
)

type TunnelModeServer struct {
	*BaseServer
	handler           tunnelHandler
	listenAddr        string
	listener          net.Listener
	socks5UDP         *socks5UDPRegistry
	activeConnections sync.Map
	httpProxyMu       sync.RWMutex
	httpProxyPool     *tunnelHTTPBackendTransportPool
}

var (
	errTunnelServerUnavailable      = errors.New("tunnel server unavailable")
	errMixedProxyUnknownProtocol    = errors.New("mixed proxy: unknown protocol")
	errMixedHTTPProxyDisabled       = errors.New("mixed proxy: http proxy is disabled")
	errMixedSocks5ProxyDisabled     = errors.New("mixed proxy: socks5 proxy is disabled")
	errMixedSocks5InvalidMethodList = errors.New("mixed proxy: invalid socks5 method list")
)

// NewTunnelModeServer tcp|host|mixproxy
func NewTunnelModeServer(handler tunnelHandler, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	return newTunnelModeServerWithRuntimeSources(handler, currentProxyRuntimeSources(), bridge, task)
}

func NewTunnelModeServerWithRuntime(handler tunnelHandler, runtime proxyRuntimeContext, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	return newTunnelModeServerWithRuntimeSources(handler, injectedProxyRuntimeSources(runtime), bridge, task)
}

func NewTunnelModeServerWithRuntimeRoot(handler tunnelHandler, runtimeRoot func() proxyRuntimeContext, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	return NewTunnelModeServerWithRuntimeRootAndLocalProxyAllowed(handler, runtimeRoot, currentProxyLocalProxyAllowed, bridge, task)
}

func NewTunnelModeServerWithRuntimeRootAndLocalProxyAllowed(handler tunnelHandler, runtimeRoot func() proxyRuntimeContext, localProxyAllowed func() bool, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	return newTunnelModeServerWithRuntimeSources(handler, proxyRuntimeSources{
		runtimeRoot:       runtimeRoot,
		localProxyAllowed: localProxyAllowed,
	}, bridge, task)
}

func newTunnelModeServerWithRuntimeSources(handler tunnelHandler, sources proxyRuntimeSources, bridge NetBridge, task *file.Tunnel) *TunnelModeServer {
	return &TunnelModeServer{
		BaseServer:        newBaseServerWithRuntimeSources(sources, bridge, task),
		handler:           handler,
		activeConnections: sync.Map{},
	}
}

func (s *TunnelModeServer) Start() error {
	if s == nil || s.BaseServer == nil || s.handler == nil {
		return errTunnelServerUnavailable
	}
	task := s.CurrentTask()
	if err := validateTunnelRuntimeTask(task); err != nil {
		return err
	}
	s.listenAddr = s.resolveTunnelListenAddr(task)
	if err := s.startTunnelSocks5UDP(task); err != nil {
		return err
	}
	listener, err := s.openTunnelListener()
	if err != nil {
		s.closeTunnelSocks5UDP()
		return err
	}
	s.listener = listener
	s.serveTunnelListener(listener)
	s.closeTunnelSocks5UDP()
	return nil
}

func (s *TunnelModeServer) resolveTunnelListenAddr(task *file.Tunnel) string {
	if task == nil {
		return common.BuildAddress("0.0.0.0", "0")
	}
	serverIP := task.ServerIp
	if serverIP == "" {
		serverIP = "0.0.0.0"
	}
	return common.BuildAddress(serverIP, strconv.Itoa(task.Port))
}

func (s *TunnelModeServer) startTunnelSocks5UDP(task *file.Tunnel) error {
	if !s.usesSocks5UDP() {
		return nil
	}
	registry, err := newSocks5UDPRegistryWithPolicyRoot(s.linkOpener, s.serverRole, func() socks5UDPSourcePolicy { return s.runtimeContext().accessPolicy }, task, s.LocalProxyAllowed(), s.listenAddr, s.ServiceRateLimiter, s.BridgeRateLimiter)
	if err != nil {
		return err
	}
	s.socks5UDP = registry
	s.socks5UDP.start()
	return nil
}

func (s *TunnelModeServer) closeTunnelSocks5UDP() {
	if s.socks5UDP != nil {
		_ = s.socks5UDP.Close()
	}
}

func (s *TunnelModeServer) openTunnelListener() (net.Listener, error) {
	return transport.ListenTCP(s.listenAddr, s.usesTransparentTCP())
}

func (s *TunnelModeServer) serveTunnelListener(listener net.Listener) {
	conn.Accept(listener, s.handleConn)
}

func (s *TunnelModeServer) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	if s.listener != nil {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = err
		}
	}
	if s.socks5UDP != nil {
		_ = s.socks5UDP.Close()
	}
	if pool := s.detachHTTPProxyBackendPool(); pool != nil {
		pool.CloseIdleConnections()
	}
	closeActiveConnections(&s.activeConnections)
	return closeErr
}

func (s *TunnelModeServer) httpProxyBackendPool() *tunnelHTTPBackendTransportPool {
	if s == nil {
		return newTunnelHTTPBackendTransportPool()
	}
	s.httpProxyMu.RLock()
	pool := s.httpProxyPool
	s.httpProxyMu.RUnlock()
	if pool != nil {
		return pool
	}
	s.httpProxyMu.Lock()
	defer s.httpProxyMu.Unlock()
	if s.httpProxyPool == nil {
		s.httpProxyPool = newTunnelHTTPBackendTransportPool()
	}
	return s.httpProxyPool
}

func (s *TunnelModeServer) detachHTTPProxyBackendPool() *tunnelHTTPBackendTransportPool {
	if s == nil {
		return nil
	}
	s.httpProxyMu.Lock()
	defer s.httpProxyMu.Unlock()
	pool := s.httpProxyPool
	s.httpProxyPool = nil
	return pool
}

func (s *TunnelModeServer) deleteActiveConnectionEntryIfCurrent(key, value interface{}) bool {
	if s == nil || key == nil || value == nil {
		return false
	}
	return s.activeConnections.CompareAndDelete(key, value)
}

type activeConnectionCloseTarget struct {
	key   interface{}
	value interface{}
	conn  net.Conn
}

func closeActiveConnections(active *sync.Map) {
	parallelCloseTargets(snapshotActiveConnectionCloseTargets(active), func(target activeConnectionCloseTarget) {
		closeActiveConnectionTarget(active, target)
	})
}

func snapshotActiveConnectionCloseTargets(active *sync.Map) []activeConnectionCloseTarget {
	if active == nil {
		return nil
	}

	targets := make([]activeConnectionCloseTarget, 0)
	active.Range(func(key, value interface{}) bool {
		c, ok := key.(net.Conn)
		if !ok || c == nil {
			active.CompareAndDelete(key, value)
			return true
		}
		targets = append(targets, activeConnectionCloseTarget{
			key:   key,
			value: value,
			conn:  c,
		})
		return true
	})
	return targets
}

func closeActiveConnectionTarget(active *sync.Map, target activeConnectionCloseTarget) {
	if active == nil {
		return
	}
	active.CompareAndDelete(target.key, target.value)
	if target.conn != nil {
		_ = target.conn.Close()
	}
}

func (s *TunnelModeServer) ServeVirtual(c net.Conn) {
	if c != nil {
		go s.handleConn(c)
	}
}

func (s *TunnelModeServer) DialVirtual(rAddr string) (net.Conn, error) {
	a, b := net.Pipe()
	c, err := conn.NewAddrOverrideConn(b, rAddr, s.listenAddr)
	if err != nil {
		_ = a.Close()
		_ = b.Close()
		return nil, err
	}
	go s.handleConn(c)
	return a, nil
}

func (s *TunnelModeServer) handleConn(c net.Conn) {
	if c == nil {
		return
	}
	s.activeConnections.Store(c, struct{}{})
	defer func() {
		s.activeConnections.Delete(c)
		if c != nil {
			_ = c.Close()
		}
	}()

	task := s.CurrentTask()
	if err := validateTunnelRuntimeTask(task); err != nil {
		logs.Warn("reject tunnel connection with malformed runtime task: %v", err)
		return
	}

	if s.BridgeIsServer() {
		lease, err := s.CheckFlowAndConnNum(task.Client, task, nil)
		if err != nil {
			logs.Warn("client Id %d, task Id %d, error %v, when tcp connection", task.Client.Id, task.Id, err)
			_ = c.Close()
			return
		}
		defer lease.Release()
	}
	logs.Trace("new tcp connection,local port %d,client %d,remote address %v", task.Port, task.Client.Id, c.RemoteAddr())

	_ = s.handler(conn.NewConn(c), s)
}

func validateTunnelRuntimeTask(task *file.Tunnel) error {
	switch {
	case task == nil:
		return errors.New("nil tunnel task")
	case task.Client == nil:
		return errors.New("nil tunnel task client")
	case task.Client.Cnf == nil:
		return errors.New("nil tunnel task client config")
	case task.Target == nil:
		return errors.New("nil tunnel task target")
	case task.Flow == nil:
		return errors.New("nil tunnel task flow")
	case task.Client.Flow == nil:
		return errors.New("nil tunnel task client flow")
	default:
		return nil
	}
}

func selectValidatedTunnelRuntimeTask(task *file.Tunnel) (*file.Tunnel, error) {
	if err := validateTunnelRuntimeTask(task); err != nil {
		return nil, err
	}
	selected := task.SelectRuntimeRoute()
	if err := validateTunnelRuntimeTask(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *TunnelModeServer) usesSocks5UDP() bool {
	if s == nil {
		return false
	}
	task := s.CurrentTask()
	if task == nil {
		return false
	}
	_, socksEnabled := effectiveMixProxyFlags(task)
	return socksEnabled
}

func (s *TunnelModeServer) usesTransparentTCP() bool {
	if s == nil {
		return false
	}
	task := s.CurrentTask()
	return task != nil && isTransparentTunnelMode(task.Mode)
}

func isTransparentTunnelMode(mode string) bool {
	switch mode {
	case "p2pt", "tcpTrans":
		return true
	default:
		return false
	}
}

func effectiveMixProxyFlags(task *file.Tunnel) (httpEnabled, socksEnabled bool) {
	if task == nil {
		return false, false
	}
	switch task.Mode {
	case "socks5":
		return false, true
	case "httpProxy":
		return true, false
	case "mixProxy":
		return task.HttpProxy, task.Socks5Proxy
	default:
		return false, false
	}
}

func looksLikeHTTPProxyRequest(prefix []byte) bool {
	switch string(prefix) {
	case "GE", "PO", "HE", "PU", "DE", "OP", "CO", "TR", "PA", "PR", "MK", "MO", "LO", "UN", "RE", "AC", "SE", "LI":
		return true
	default:
		return false
	}
}

// ProcessMix multiplexes HTTP proxy and SOCKS5 on the same listener.
func ProcessMix(c *conn.Conn, s *TunnelModeServer) error {
	task := s.CurrentTask()
	if err := validateTunnelRuntimeTask(task); err != nil {
		_ = c.Close()
		return err
	}
	httpEnabled, socksEnabled := effectiveMixProxyFlags(task)

	header, protocol, err := readMixedProxyProtocol(c)
	if err != nil {
		logs.Warn("negotiation err %v", err)
		_ = c.Close()
		return err
	}
	switch protocol {
	case "http":
		return s.handleMixedHTTPProxy(c, task, header[:], httpEnabled)
	case "socks5":
		return s.handleMixedSocks5(c, task, int(header[1]), socksEnabled)
	default:
		logs.Trace("Socks5 Buf: %s", header[:])
		logs.Warn("only support socks5 and http, request from: %v", c.RemoteAddr())
		_ = c.Close()
		return errMixedProxyUnknownProtocol
	}
}

func readMixedProxyProtocol(c *conn.Conn) ([2]byte, string, error) {
	var header [2]byte
	if _, err := io.ReadFull(c, header[:]); err != nil {
		return header, "", err
	}
	if header[0] == 5 {
		return header, "socks5", nil
	}
	if looksLikeHTTPProxyRequest(header[:]) {
		return header, "http", nil
	}
	return header, "unknown", nil
}

func (s *TunnelModeServer) handleMixedHTTPProxy(c *conn.Conn, task *file.Tunnel, prefix []byte, httpEnabled bool) error {
	if !httpEnabled {
		logs.Warn("http proxy is disable, client %d request from: %v", task.Client.Id, c.RemoteAddr())
		_ = c.Close()
		return errMixedHTTPProxyDisabled
	}
	if err := ProcessHttp(c.SetRb(prefix), s); err != nil {
		logs.Warn("http proxy error: %v", err)
		_ = c.Close()
		return err
	}
	_ = c.Close()
	return nil
}

func (s *TunnelModeServer) handleMixedSocks5(c *conn.Conn, task *file.Tunnel, methodCount int, socksEnabled bool) error {
	if !socksEnabled {
		logs.Warn("socks5 proxy is disable, client %d request from: %v", task.Client.Id, c.RemoteAddr())
		_ = c.Close()
		return errMixedSocks5ProxyDisabled
	}
	methods, err := readSocks5Methods(c, methodCount)
	if err != nil {
		logs.Warn("read socks5 method list error: %v", err)
		_ = c.Close()
		return fmt.Errorf("%w: %v", errMixedSocks5InvalidMethodList, err)
	}
	if err := s.negotiateSocks5Method(c, methods); err != nil {
		_ = c.Close()
		return err
	}
	s.handleSocks5Request(c)
	return nil
}

// ProcessTunnel tcp proxy
func ProcessTunnel(c *conn.Conn, s *TunnelModeServer) error {
	task, targetAddr, err := s.prepareTunnelRouteTarget(c)
	if err != nil {
		return err
	}
	return s.DealClient(c, task.Client, targetAddr, nil, common.CONN_TCP, nil, []*file.Flow{task.Flow, task.Client.Flow}, task.Target.ProxyProtocol, task.Target.LocalProxy, task)
}

func (s *TunnelModeServer) prepareTunnelRouteTarget(c *conn.Conn) (*file.Tunnel, string, error) {
	task, err := selectValidatedTunnelRuntimeTask(s.CurrentTask())
	if err != nil {
		if c != nil {
			_ = c.Close()
		}
		return nil, "", err
	}
	targetAddr, err := task.Target.GetRandomTarget()
	if err == nil {
		return task, targetAddr, nil
	}
	if task.Mode != "file" && s.BridgeIsServer() {
		_ = c.Close()
		logs.Warn("tcp port %d, client Id %d, task Id %d connect error %v", task.Port, task.Client.Id, task.Id, err)
		return nil, "", err
	}
	return task, "", nil
}

type tunnelHandler func(c *conn.Conn, s *TunnelModeServer) error
