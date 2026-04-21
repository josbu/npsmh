package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/nps/server/tool"
	"github.com/quic-go/quic-go"
)

const clientConnectGraceWindow = 3 * time.Second

type bridgeRuntimeLinkTarget struct {
	node   *Node
	tunnel any
}

type bridgeLocalTarget struct {
	scheme string
	target string
}

type clientLinkRouteState uint8

const (
	clientLinkRouteMissing clientLinkRouteState = iota
	clientLinkRouteRetry
	clientLinkRouteOffline
	clientLinkRouteReady
)

type clientLinkRouteResult struct {
	node  *Node
	state clientLinkRouteState
}

type clientLinkTunnelFailure uint8

const (
	clientLinkTunnelRetryConnect clientLinkTunnelFailure = iota
	clientLinkTunnelRetryReconnect
	clientLinkTunnelOffline
	clientLinkTunnelUnavailable
)

var (
	errBridgeClientRouteUUIDRequired  = errors.New("missing client route uuid")
	errBridgeClientConnecting         = errors.New("client is connecting, please retry")
	errBridgeClientConnectUnavailable = errors.New("the client connect error")
	errBridgeClientTunnelReconnecting = errors.New("client tunnel is reconnecting, please retry")
	errBridgeClientOffline            = errors.New("the client is offline")
	errBridgeClientTunnelUnavailable  = errors.New("the client tunnel is unavailable")
)

func (s *Bridge) SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	if err := s.validateLinkRequest(link); err != nil {
		return nil, err
	}

	client, err := s.loadLinkClient(clientId)
	if err != nil {
		return nil, err
	}
	if link.LocalProxy || clientId < 0 {
		return s.connectLocalTarget(link, t)
	}
	if strings.TrimSpace(link.Option.RouteUUID) == "" && t != nil {
		link.Option.RouteUUID = t.RuntimeRouteUUID()
	}

	runtimeTarget, err := s.resolveRuntimeLinkTarget(clientId, client, link)
	if err != nil {
		return nil, err
	}
	if runtimeTarget.node != nil {
		link.Option.RouteUUID = runtimeTarget.node.UUID
	}
	target, err := openRuntimeTunnelConn(runtimeTarget.tunnel, link.Option.Timeout)
	if err != nil {
		return nil, err
	}
	if err := sendLinkTargetInfo(target, link); err != nil {
		return nil, err
	}
	if err := s.readLinkTargetAck(target, client, runtimeTarget.node, runtimeTarget.tunnel, link); err != nil {
		return nil, err
	}
	warnLegacyUDPClient(runtimeTarget.node, link)
	return target, nil
}

func (s *Bridge) validateLinkRequest(link *conn.Link) error {
	if link == nil {
		return errors.New("link is nil")
	}
	if !s.IPVerifyEnabled() {
		return nil
	}
	ip := common.GetIpByAddr(link.RemoteAddr)
	ipValue, ok := s.Register.Load(ip)
	if !ok {
		return fmt.Errorf("the ip %s is not in the validation list", ip)
	}
	expireAt, ok := ipValue.(time.Time)
	if !ok {
		s.removeRegistrationValueIfCurrent(ip, ipValue)
		return fmt.Errorf("the validity of the ip %s is invalid", ip)
	}
	if !expireAt.After(time.Now()) {
		s.removeRegistrationIfCurrent(ip, expireAt)
		return fmt.Errorf("the validity of the ip %s has expired", ip)
	}
	return nil
}

func (s *Bridge) loadLinkClient(clientId int) (*Client, error) {
	client, ok := s.loadRuntimeClient(clientId)
	if !ok {
		return nil, fmt.Errorf("the client %d is not connect", clientId)
	}
	return client, nil
}

func (s *Bridge) resolveRuntimeLinkTarget(clientId int, client *Client, link *conn.Link) (bridgeRuntimeLinkTarget, error) {
	node, tunnel, err := s.resolveClientTunnel(clientId, client, link)
	if err != nil {
		return bridgeRuntimeLinkTarget{}, err
	}
	return bridgeRuntimeLinkTarget{
		node:   node,
		tunnel: tunnel,
	}, nil
}

func clientConnectGraceActive(client *Client) bool {
	return client != nil && client.InConnectGraceWindow(clientConnectGraceWindow)
}

func (s *Bridge) resolveClientTunnel(clientId int, client *Client, link *conn.Link) (*Node, any, error) {
	rawHost := strings.TrimSpace(link.Host)
	routeUUID := strings.TrimSpace(link.Option.RouteUUID)
	var (
		node   *Node
		tunnel any
		err    error
	)
	if strings.HasPrefix(rawHost, "file://") {
		if routeUUID != "" {
			node, tunnel, err = s.resolveClientUUIDTunnel(client, routeUUID)
		} else {
			node, tunnel, err = s.resolveClientFileTunnel(client, link, rawHost)
		}
	} else if routeUUID != "" {
		node, tunnel, err = s.resolveClientUUIDTunnel(client, routeUUID)
	} else {
		node = client.GetNode()
		if node == nil {
			return nil, nil, s.clientNodeUnavailable(clientId, client)
		}
		tunnel = node.GetTunnel()
	}
	if err != nil {
		return nil, nil, err
	}
	if tunnel == nil {
		return nil, nil, s.clientTunnelUnavailable(clientId, client, node)
	}
	return node, tunnel, nil
}

func (s *Bridge) resolveClientRouteNode(client *Client, uuid string) clientLinkRouteResult {
	if client == nil {
		return clientLinkRouteResult{state: clientLinkRouteMissing}
	}
	candidate := client.evaluateNodeCandidate(uuid, true)
	switch candidate.action {
	case clientNodeCandidateReady:
		return clientLinkRouteResult{node: candidate.node, state: clientLinkRouteReady}
	case clientNodeCandidateDefer:
		return clientLinkRouteResult{node: candidate.node, state: clientLinkRouteRetry}
	case clientNodeCandidateMissing:
		if clientConnectGraceActive(client) {
			return clientLinkRouteResult{state: clientLinkRouteRetry}
		}
		client.pruneMissingNodeUUID(candidate.uuid)
		return clientLinkRouteResult{state: clientLinkRouteMissing}
	case clientNodeCandidateInvalid:
		if clientConnectGraceActive(client) {
			return clientLinkRouteResult{state: clientLinkRouteRetry}
		}
		return clientLinkRouteResult{state: clientLinkRouteMissing}
	case clientNodeCandidatePrune:
		s.pruneUnavailableClientNode(client, candidate.node)
		return clientLinkRouteResult{node: candidate.node, state: clientLinkRouteOffline}
	default:
		return clientLinkRouteResult{state: clientLinkRouteMissing}
	}
}

func (s *Bridge) resolveClientUUIDTunnel(client *Client, uuid string) (*Node, any, error) {
	uuid = strings.TrimSpace(uuid)
	if client == nil || uuid == "" {
		return nil, nil, errBridgeClientRouteUUIDRequired
	}
	route := s.resolveClientRouteNode(client, uuid)
	switch route.state {
	case clientLinkRouteRetry:
		return nil, nil, errBridgeClientConnecting
	case clientLinkRouteMissing:
		return nil, nil, fmt.Errorf("the client instance %s is not connect", uuid)
	case clientLinkRouteOffline:
		return nil, nil, fmt.Errorf("the client instance %s is offline", uuid)
	}
	tunnel := route.node.GetTunnel()
	if tunnel == nil {
		if clientConnectGraceActive(client) {
			return nil, nil, errBridgeClientTunnelReconnecting
		}
		s.pruneUnavailableClientNode(client, route.node)
		return nil, nil, fmt.Errorf("the client instance %s tunnel is unavailable", uuid)
	}
	return route.node, tunnel, nil
}

func (s *Bridge) resolveClientFileTunnel(client *Client, link *conn.Link, rawHost string) (*Node, any, error) {
	key := strings.TrimPrefix(rawHost, "file://")
	link.ConnType = "file"
	node, ok := client.GetNodeByFile(key)
	if !ok {
		logs.Warn("Failed to find tunnel for host: %s", link.Host)
		client.RemoveOfflineNodes(false)
		s.removeEmptyRuntimeClient(client.Id, client)
		return nil, nil, fmt.Errorf("failed to find tunnel for host: %s", link.Host)
	}
	return node, node.GetTunnel(), nil
}

func (s *Bridge) clientNodeUnavailable(clientId int, client *Client) error {
	if clientConnectGraceActive(client) {
		return errBridgeClientConnecting
	}
	s.DelClient(clientId)
	return errBridgeClientConnectUnavailable
}

func (s *Bridge) classifyClientTunnelFailure(clientId int, client *Client, node *Node) clientLinkTunnelFailure {
	clientConnecting := clientConnectGraceActive(client)
	if s.pruneUnavailableClientNode(client, node) {
		if clientConnecting {
			return clientLinkTunnelRetryConnect
		}
		return clientLinkTunnelUnavailable
	}
	sig := node.GetSignal()
	if sig == nil || sig.IsClosed() {
		if clientConnecting {
			return clientLinkTunnelRetryConnect
		}
		s.DelClient(clientId)
		return clientLinkTunnelOffline
	}
	if clientConnecting {
		return clientLinkTunnelRetryReconnect
	}
	s.DelClient(clientId)
	return clientLinkTunnelUnavailable
}

func (s *Bridge) clientTunnelUnavailable(clientId int, client *Client, node *Node) error {
	switch s.classifyClientTunnelFailure(clientId, client, node) {
	case clientLinkTunnelRetryConnect:
		return errBridgeClientConnecting
	case clientLinkTunnelRetryReconnect:
		return errBridgeClientTunnelReconnecting
	case clientLinkTunnelOffline:
		return errBridgeClientOffline
	default:
		return errBridgeClientTunnelUnavailable
	}
}

func (s *Bridge) pruneUnavailableClientNode(client *Client, node *Node) bool {
	if client == nil || node == nil {
		return false
	}
	if !node.CloseIfOffline() {
		return false
	}
	if !client.closeAndRemoveNodeIfCurrent(strings.TrimSpace(node.UUID), node) {
		return false
	}
	s.removeEmptyRuntimeClient(client.Id, client)
	return true
}

func openRuntimeTunnelConn(tunnel any, timeout time.Duration) (net.Conn, error) {
	tunnel = normalizeBridgeRuntimeTunnel(tunnel)
	switch tun := tunnel.(type) {
	case *mux.Mux:
		return tun.NewConnTimeout(timeout)
	case *quic.Conn:
		return openQuicTunnelStream(tun, timeout)
	default:
		return nil, errors.New("the tunnel is unavailable")
	}
}

func sendLinkTargetInfo(target net.Conn, link *conn.Link) error {
	if _, err := conn.NewConn(target).SendInfo(link, ""); err != nil {
		logs.Info("new connection error, the target %s refused to connect", link.Host)
		_ = target.Close()
		return err
	}
	return nil
}

func (s *Bridge) closeNodeOnLinkAckFailure(client *Client, node *Node, tunnel any) {
	if s == nil || client == nil || node == nil {
		return
	}
	if !node.CloseIfTunnelCurrent(tunnel) {
		return
	}
	if client.closeAndRemoveNodeIfCurrent(strings.TrimSpace(node.UUID), node) {
		s.removeEmptyRuntimeClient(client.Id, client)
	}
}

func (s *Bridge) readLinkTargetAck(target net.Conn, client *Client, node *Node, tunnel any, link *conn.Link) error {
	if !link.Option.NeedAck || node.BaseVer <= 5 {
		return nil
	}
	if err := conn.ReadACK(target, link.Option.Timeout); err != nil {
		_ = target.Close()
		logs.Trace("ReadACK failed: %v", err)
		s.closeNodeOnLinkAckFailure(client, node, tunnel)
		return err
	}
	return nil
}

func warnLegacyUDPClient(node *Node, link *conn.Link) {
	if link.ConnType == "udp" && node.BaseVer < 7 {
		logs.Warn("UDP connection requires client v%s or newer.", version.GetVersion(7))
	}
}

const bridgeLinkOpenDefaultTimeout = 5 * time.Second

var bridgeDirectDial = func(timeout time.Duration, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{}
	if timeout > 0 {
		dialer.Timeout = timeout
	}
	return dialer.DialContext(context.Background(), network, address)
}

var bridgeHandleUDP5 = conn.HandleUdp5

func (s *Bridge) connectLocalTarget(link *conn.Link, t *file.Tunnel) (net.Conn, error) {
	link.Option.WaitConnectResult = false
	if link.ConnType == "udp5" {
		serverSide, handlerSide := net.Pipe()
		go bridgeHandleUDP5(context.Background(), handlerSide, normalizeBridgeLinkTimeout(link.Option.Timeout), "")
		return serverSide, nil
	}
	raw := strings.TrimSpace(link.Host)
	if raw == "" {
		return nil, fmt.Errorf("empty host")
	}

	localTarget, handled, err := parseBridgeLocalTarget(raw)
	if err != nil {
		return nil, err
	}
	if handled {
		return s.openLocalTarget(link, t, raw, localTarget)
	}
	return dialDirectTarget(link, raw)
}

func parseBridgeLocalTarget(raw string) (bridgeLocalTarget, bool, error) {
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return bridgeLocalTarget{}, false, nil
	}
	scheme := strings.ToLower(strings.TrimSpace(raw[:idx]))
	targetStr := strings.TrimSpace(raw[idx+3:])
	if targetStr == "" {
		return bridgeLocalTarget{}, false, fmt.Errorf("missing target in %q", raw)
	}
	switch scheme {
	case "tunnel", "bridge":
		return bridgeLocalTarget{scheme: scheme, target: targetStr}, true, nil
	default:
		return bridgeLocalTarget{}, false, fmt.Errorf("unsupported scheme %q in %q", scheme, raw)
	}
}

func (s *Bridge) openLocalTarget(link *conn.Link, t *file.Tunnel, raw string, target bridgeLocalTarget) (net.Conn, error) {
	switch target.scheme {
	case "tunnel":
		return s.openLocalTunnelTarget(link, t, raw, target.target)
	case "bridge":
		return s.openLocalBridgeTarget(link, raw, target.target)
	default:
		return nil, fmt.Errorf("unsupported scheme %q in %q", target.scheme, raw)
	}
}

func (s *Bridge) openLocalTunnelTarget(link *conn.Link, t *file.Tunnel, raw, targetStr string) (net.Conn, error) {
	id, convErr := strconv.Atoi(targetStr)
	if convErr != nil {
		return nil, fmt.Errorf("invalid tunnel id %q: %w", targetStr, convErr)
	}
	if t != nil && t.Id == id {
		return nil, fmt.Errorf("task %d cannot connect to itself (tunnel://%d)", t.Id, id)
	}
	target, err := tool.GetTunnelConn(id, link.RemoteAddr)
	return target, err
}

func (s *Bridge) openLocalBridgeTarget(link *conn.Link, raw, targetStr string) (net.Conn, error) {
	switch strings.ToLower(targetStr) {
	case common.CONN_TCP:
		return dialBridgeVirtualListener(s.VirtualTcpListener, link.RemoteAddr, raw, targetStr)
	case common.CONN_TLS:
		return dialBridgeVirtualListener(s.VirtualTlsListener, link.RemoteAddr, raw, targetStr)
	case common.CONN_WS:
		return dialBridgeVirtualListener(s.VirtualWsListener, link.RemoteAddr, raw, targetStr)
	case common.CONN_WSS:
		return dialBridgeVirtualListener(s.VirtualWssListener, link.RemoteAddr, raw, targetStr)
	case common.CONN_WEB:
		target, err := tool.GetWebServerConn(link.RemoteAddr)
		return target, err
	default:
		return nil, fmt.Errorf("invalid bridge target %q in %q", targetStr, raw)
	}
}

func dialBridgeVirtualListener(listener *conn.VirtualListener, remoteAddr, raw, targetStr string) (net.Conn, error) {
	if listener == nil {
		return nil, fmt.Errorf("bridge target %q in %q is unavailable", targetStr, raw)
	}
	return listener.DialVirtual(remoteAddr)
}

func dialDirectTarget(link *conn.Link, rawHost string) (net.Conn, error) {
	network := "tcp"
	if link.ConnType == common.CONN_UDP {
		network = "udp"
	}
	return bridgeDirectDial(normalizeBridgeLinkTimeout(link.Option.Timeout), network, common.FormatAddress(strings.TrimSpace(rawHost)))
}

func openQuicTunnelStream(tun *quic.Conn, timeout time.Duration) (net.Conn, error) {
	if normalizeBridgeRuntimeTunnel(tun) == nil {
		return nil, errors.New("the tunnel is unavailable")
	}
	timeout = normalizeBridgeLinkTimeout(timeout)
	ctx := quicRuntimeContext(tun)
	cancel := func() {}
	if timeout > 0 {
		ctxWithTimeout, cancelFn := context.WithTimeout(ctx, timeout)
		ctx = ctxWithTimeout
		cancel = cancelFn
	}
	defer cancel()

	stream, err := tun.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return conn.NewQuicStreamConn(stream, tun), nil
}

func normalizeBridgeLinkTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return bridgeLinkOpenDefaultTimeout
	}
	return timeout
}
