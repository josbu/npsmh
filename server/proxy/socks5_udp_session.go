package proxy

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

type socks5UDPPacket struct {
	buf []byte
	n   int
}

const socks5UDPPacketQueueSize = 1024

type socks5UDPSession struct {
	registry     *socks5UDPRegistry
	control      net.Conn
	framed       *conn.FramedConn
	timeout      time.Duration
	routeStats   proxyRouteRuntimeCollector
	boundNode    proxyRouteRuntimeBoundNode
	clientIPKey  string
	controlAddr  string
	packetCh     chan socks5UDPPacket
	done         chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
	lastActiveNS atomic.Int64

	queueMu            sync.Mutex
	closed             bool
	closeWatchers      map[uint64]func()
	nextCloseWatcherID uint64

	mu        sync.RWMutex
	boundAddr *net.UDPAddr
	boundKey  string
	routeUUID string
}

func newSocks5UDPSession(registry *socks5UDPRegistry, control net.Conn) (*socks5UDPSession, error) {
	controlAddr, clientIP, err := resolveSocks5UDPClientIdentity(control)
	if err != nil {
		return nil, err
	}
	return newSocks5UDPSessionWithClientIdentity(registry, control, controlAddr, clientIP)
}

func newSocks5UDPSessionWithClientIdentity(registry *socks5UDPRegistry, control net.Conn, controlAddr string, clientIP net.IP) (*socks5UDPSession, error) {
	if registry == nil {
		return nil, errors.New("nil socks5 udp registry")
	}
	if control == nil {
		return nil, errors.New("nil socks5 udp control conn")
	}
	if err := validateSocks5UDPTunnelTask(registry.task); err != nil {
		return nil, err
	}
	if clientIP == nil {
		return nil, errors.New("parse socks5 udp client ip failed")
	}
	if controlAddr == "" {
		controlAddr = socks5UDPControlRemoteAddr(control)
	}

	session := &socks5UDPSession{
		registry:    registry,
		control:     control,
		timeout:     socks5UDPAssociateIdleTimeout,
		clientIPKey: clientIP.String(),
		controlAddr: controlAddr,
		packetCh:    make(chan socks5UDPPacket, socks5UDPPacketQueueSize),
		done:        make(chan struct{}),
	}
	if err := session.openTunnel(); err != nil {
		return nil, err
	}
	return session, nil
}

func resolveSocks5UDPClientIdentity(control net.Conn) (string, net.IP, error) {
	if control == nil {
		return "", nil, errors.New("nil socks5 udp control conn")
	}
	controlAddr := socks5UDPControlRemoteAddr(control)
	if controlAddr == "" {
		return "", nil, errors.New("parse socks5 udp client ip failed")
	}
	clientIP := common.NormalizeIP(common.ParseIPFromAddr(controlAddr))
	if clientIP == nil {
		return controlAddr, nil, errors.New("parse socks5 udp client ip failed")
	}
	return controlAddr, clientIP, nil
}

func socks5UDPControlRemoteAddr(control net.Conn) string {
	if control == nil || control.RemoteAddr() == nil {
		return ""
	}
	return control.RemoteAddr().String()
}

func validateSocks5UDPTunnelTask(task *file.Tunnel) error {
	switch {
	case task == nil:
		return errors.New("nil socks5 udp task")
	case task.Client == nil:
		return errors.New("nil socks5 udp task client")
	case task.Client.Cnf == nil:
		return errors.New("nil socks5 udp client config")
	case task.Target == nil:
		return errors.New("nil socks5 udp target")
	case task.Flow == nil:
		return errors.New("nil socks5 udp task flow")
	case task.Client.Flow == nil:
		return errors.New("nil socks5 udp client flow")
	default:
		return nil
	}
}

func selectValidatedSocks5UDPRuntimeTask(task *file.Tunnel) (*file.Tunnel, error) {
	if err := validateSocks5UDPTunnelTask(task); err != nil {
		return nil, err
	}
	selected := task.SelectRuntimeRoute()
	if err := validateSocks5UDPTunnelTask(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *socks5UDPSession) openTunnel() error {
	task, err := selectValidatedSocks5UDPRuntimeTask(s.registry.task)
	if err != nil {
		return err
	}
	link := conn.NewLink(
		"udp5",
		"",
		task.Client.Cnf.Crypt,
		task.Client.Cnf.Compress,
		s.controlRemoteAddr(),
		s.registry.allowLocalProxy && task.Target.LocalProxy,
	)
	link.Option.Timeout = s.timeout

	target, err := s.registry.OpenBridgeLink(task.Client.Id, link, task)
	if err != nil {
		return err
	}
	s.routeStats = routeStatsCollectorFromLinkOpener(s.registry.linkOpener)
	s.boundNode = resolveRouteRuntimeBoundNodeFromCollector(s.routeStats, task.Client.Id, link.Option.RouteUUID)
	target = wrapRouteTrackedConnWithBoundNode(target, s.routeStats, task.Client.Id, link.Option.RouteUUID, s.boundNode)
	timeoutConn := conn.NewTimeoutConn(target, link.Option.Timeout)
	bridgeLimiter := rate.Limiter(nil)
	if !link.LocalProxy {
		bridgeLimiter = s.registry.BridgeRateLimiter(task.Client)
	}
	bridgeConn := conn.WrapNetConnWithLimiter(timeoutConn, bridgeLimiter)
	bridgeConn = conn.WrapNetConnWithTrafficObserver(bridgeConn, bridgeTrafficObserverWithBoundNode(task.Client, s.routeStats, link.Option.RouteUUID, s.boundNode))
	flowConn := conn.NewFlowConn(bridgeConn, task.Flow, task.Client.Flow)
	s.framed = conn.WrapFramed(flowConn)
	s.routeUUID = link.Option.RouteUUID
	return nil
}

func (s *socks5UDPSession) start() {
	s.touch()
	s.wg.Add(2)
	go s.writeToTunnelLoop()
	go s.readFromTunnelLoop()
}

func (s *socks5UDPSession) Wait() {
	s.wg.Wait()
}

func (s *socks5UDPSession) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		var watchers []func()
		s.queueMu.Lock()
		s.closed = true
		if len(s.closeWatchers) > 0 {
			watchers = make([]func(), 0, len(s.closeWatchers))
			for _, wake := range s.closeWatchers {
				watchers = append(watchers, wake)
			}
			s.closeWatchers = nil
		}
		if s.done != nil {
			close(s.done)
		}
		if s.packetCh != nil {
			close(s.packetCh)
		}
		s.queueMu.Unlock()

		for _, wake := range watchers {
			if wake != nil {
				wake()
			}
		}
		if s.registry != nil {
			s.registry.unregisterSession(s)
		}
		if s.framed != nil {
			_ = s.framed.Close()
		}
	})
}

func (s *socks5UDPSession) enqueue(buf []byte, n int) bool {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	if s.closed {
		return false
	}
	select {
	case s.packetCh <- socks5UDPPacket{buf: buf, n: n}:
		s.touch()
		return true
	default:
		logs.Debug("drop socks5 udp datagram from %s due to full queue", s.controlRemoteAddr())
		return false
	}
}

func (s *socks5UDPSession) writeToTunnelLoop() {
	defer s.wg.Done()

	discardOnly := false
	task := s.registry.task
	serviceLimiter := s.registry.ServiceRateLimiter()
	for pkt := range s.packetCh {
		datagram, err := parseSocks5UDPEdgePacket(pkt.buf[:pkt.n])
		if err != nil {
			common.BufPoolUdp.Put(pkt.buf)
			if errors.Is(err, errSocks5UDPFragmentNotSupported) {
				logs.Warn("socks5 udp frag not supported, drop")
			} else {
				logs.Debug("drop invalid socks5 udp datagram from %s: %v", s.controlRemoteAddr(), err)
			}
			continue
		}
		wirePacket, err := marshalSocks5UDPEdgePacket(datagram)
		common.BufPoolUdp.Put(pkt.buf)
		if err != nil {
			logs.Debug("encode socks5 udp datagram for tunnel error: %v", err)
			continue
		}
		if len(wirePacket) > conn.MaxFramePayload {
			logs.Debug("socks5 udp datagram too large: %d > %d", len(wirePacket), conn.MaxFramePayload)
			continue
		}
		if discardOnly {
			continue
		}
		n, err := writeWithLimiter(serviceLimiter, wirePacket, s.framed.Write)
		if err != nil {
			logs.Debug("write socks5 udp frame to tunnel error: %v", err)
			discardOnly = true
			s.Close()
			continue
		}
		if err := observeServiceTrafficWithBoundNode(task.Client, task, nil, s.routeStats, s.routeUUID, s.boundNode, int64(n), 0); err != nil {
			logs.Debug("observe socks5 udp service ingress error: %v", err)
			discardOnly = true
			s.Close()
			continue
		}
		s.touch()
	}
}

func (s *socks5UDPSession) readFromTunnelLoop() {
	defer s.wg.Done()

	buf := common.BufPool.Get()
	defer common.BufPool.Put(buf)
	task := s.registry.task
	serviceLimiter := s.registry.ServiceRateLimiter()

	for {
		n, err := s.framed.Read(buf)
		if err != nil || n <= 0 || n > len(buf) {
			logs.Debug("read socks5 udp frame from tunnel error: %v", err)
			s.Close()
			return
		}
		addr := s.clientAddr()
		if addr == nil {
			logs.Debug("drop socks5 udp response without bound client addr")
			continue
		}
		datagram, err := common.ReadUDPDatagram(bytes.NewReader(buf[:n]))
		if err != nil {
			logs.Warn("parse socks5 udp frame from tunnel error: %v", err)
			s.Close()
			return
		}
		edgePacket, err := marshalSocks5UDPEdgePacket(datagram)
		if err != nil {
			logs.Warn("encode socks5 udp response for client error: %v", err)
			s.Close()
			return
		}
		written, err := writeWithLimiter(serviceLimiter, edgePacket, func(payload []byte) (int, error) {
			return s.registry.listener.WriteTo(payload, addr)
		})
		if err != nil {
			logs.Warn("write socks5 udp response to client error: %v", err)
			s.Close()
			return
		}
		if err := observeServiceTrafficWithBoundNode(task.Client, task, nil, s.routeStats, s.routeUUID, s.boundNode, 0, int64(written)); err != nil {
			logs.Warn("observe socks5 udp service egress error: %v", err)
			s.Close()
			return
		}
		s.touch()
	}
}

func (s *socks5UDPSession) bindClientAddr(addr *net.UDPAddr) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.boundKey = addr.String()
	s.boundAddr = cloneUDPAddr(addr)
	return s.boundKey
}

func (s *socks5UDPSession) boundIdentity() (string, *net.UDPAddr) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.boundKey, cloneUDPAddr(s.boundAddr)
}

func (s *socks5UDPSession) clientAddr() *net.UDPAddr {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// bindClientAddr stores a private copy and the session never mutates it again,
	// so the read loop can reuse the bound address without cloning on every packet.
	return s.boundAddr
}

func (s *socks5UDPSession) controlRemoteAddr() string {
	if s == nil {
		return "<nil>"
	}
	if s.controlAddr != "" {
		return s.controlAddr
	}
	if remoteAddr := socks5UDPControlRemoteAddr(s.control); remoteAddr != "" {
		return remoteAddr
	}
	return "<nil>"
}

func (s *socks5UDPSession) Done() <-chan struct{} {
	return s.done
}

func (s *socks5UDPSession) doneClosed() bool {
	if s == nil {
		return true
	}
	if s.done == nil {
		return false
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *socks5UDPSession) registerCloseWake(wake func()) func() {
	if s == nil || wake == nil {
		return func() {}
	}

	s.queueMu.Lock()
	if s.closed {
		s.queueMu.Unlock()
		wake()
		return func() {}
	}
	if s.closeWatchers == nil {
		s.closeWatchers = make(map[uint64]func())
	}
	s.nextCloseWatcherID++
	id := s.nextCloseWatcherID
	s.closeWatchers[id] = wake
	s.queueMu.Unlock()

	return func() {
		s.queueMu.Lock()
		if s.closeWatchers != nil {
			delete(s.closeWatchers, id)
		}
		s.queueMu.Unlock()
	}
}

func (s *socks5UDPSession) touch() {
	s.lastActiveNS.Store(time.Now().UnixNano())
}

func (s *socks5UDPSession) idleFor(now time.Time) time.Duration {
	last := s.lastActiveNS.Load()
	if last == 0 {
		return 0
	}
	return now.Sub(time.Unix(0, last))
}

var (
	errSocks5UDPShortPacket          = errors.New("socks5 udp: short packet")
	errSocks5UDPFragmentNotSupported = errors.New("socks5 udp: fragment not supported")
	errSocks5UDPUnsupportedAddrType  = errors.New("socks5 udp: unsupported address type")
)

func parseSocks5UDPEdgePacket(packet []byte) (*common.UDPDatagram, error) {
	if len(packet) < 4 {
		return nil, errSocks5UDPShortPacket
	}
	if packet[2] != 0 {
		return nil, errSocks5UDPFragmentNotSupported
	}

	pos := 4
	addr := &common.Addr{Type: packet[3]}
	switch addr.Type {
	case ipV4:
		if len(packet) < pos+4+2 {
			return nil, errSocks5UDPShortPacket
		}
		addr.Host = net.IP(packet[pos : pos+4]).String()
		pos += 4
	case ipV6:
		if len(packet) < pos+16+2 {
			return nil, errSocks5UDPShortPacket
		}
		addr.Host = net.IP(packet[pos : pos+16]).String()
		pos += 16
	case domainName:
		if len(packet) < pos+1 {
			return nil, errSocks5UDPShortPacket
		}
		length := int(packet[pos])
		pos++
		if len(packet) < pos+length+2 {
			return nil, errSocks5UDPShortPacket
		}
		addr.Host = string(packet[pos : pos+length])
		pos += length
	default:
		return nil, errSocks5UDPUnsupportedAddrType
	}

	addr.Port = binary.BigEndian.Uint16(packet[pos : pos+2])
	pos += 2

	return common.NewUDPDatagram(common.NewUDPHeader(0, 0, addr), packet[pos:]), nil
}

func marshalSocks5UDPEdgePacket(datagram *common.UDPDatagram) ([]byte, error) {
	if datagram == nil || datagram.Header == nil || datagram.Header.Addr == nil {
		datagram = common.NewUDPDatagram(common.NewUDPHeader(0, 0, &common.Addr{}), nil)
	}

	normalized := common.NewUDPDatagram(
		common.NewUDPHeader(0, 0, datagram.Header.Addr),
		datagram.Data,
	)

	var buf bytes.Buffer
	if err := normalized.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
