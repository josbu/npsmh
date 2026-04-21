package proxy

import (
	"net"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

type udpPacketWriter interface {
	WriteTo([]byte, net.Addr) (int, error)
}

type udpWorkerRuntime struct {
	task           *file.Tunnel
	addr           *net.UDPAddr
	key            string
	link           *conn.Link
	routeStats     proxyRouteRuntimeCollector
	boundNode      proxyRouteRuntimeBoundNode
	bridgeConn     net.Conn
	sessionConn    net.Conn
	serviceLimiter rate.Limiter
}

func (s *UdpModeServer) prepareUDPWorkerRuntime(task *file.Tunnel, addr *net.UDPAddr, key string, session *udpClientSession) (*udpWorkerRuntime, error) {
	isLocal := s.LocalProxyAllowed() && task.Target.LocalProxy || task.Client.Id < 0
	link := conn.NewLink(common.CONN_UDP, task.Target.TargetStr, task.Client.Cnf.Crypt, task.Client.Cnf.Compress, key, isLocal)
	clientConn, err := s.OpenBridgeLink(task.Client.Id, link, task)
	if err != nil {
		return nil, err
	}
	routeStats := routeStatsCollectorFromLinkOpener(s.linkOpener)
	boundNode := resolveRouteRuntimeBoundNodeFromCollector(routeStats, task.Client.Id, link.Option.RouteUUID)
	clientConn = wrapRouteTrackedConnWithBoundNode(clientConn, routeStats, task.Client.Id, link.Option.RouteUUID, boundNode)
	bridgeLimiter := s.BridgeRateLimiter(task.Client)
	if isLocal {
		bridgeLimiter = nil
	}
	target := conn.GetConn(clientConn, task.Client.Cnf.Crypt, task.Client.Cnf.Compress, bridgeLimiter, true, isLocal)
	target = conn.WrapReadWriteCloserWithTrafficObserver(target, bridgeTrafficObserverWithBoundNode(task.Client, routeStats, link.Option.RouteUUID, boundNode))
	flowConn := conn.NewFlowConn(target, task.Flow, task.Client.Flow)
	sessionConn := net.Conn(flowConn)
	if !isLocal || !s.BridgeIsServer() {
		sessionConn = conn.WrapFramed(flowConn)
	}
	session.setConn(sessionConn)
	return &udpWorkerRuntime{
		task:           task,
		addr:           addr,
		key:            key,
		link:           link,
		routeStats:     routeStats,
		boundNode:      boundNode,
		bridgeConn:     clientConn,
		sessionConn:    sessionConn,
		serviceLimiter: s.ServiceRateLimiter(task.Client, task, nil),
	}, nil
}

func (s *UdpModeServer) startUDPBackchannel(session *udpClientSession, runtime *udpWorkerRuntime) {
	session.backchannelWG.Add(1)
	go func() {
		defer session.backchannelWG.Done()
		buf := common.BufPoolUdp.Get()
		defer common.BufPoolUdp.Put(buf)

		for {
			select {
			case <-session.ctx.Done():
				return
			default:
			}

			_ = runtime.bridgeConn.SetReadDeadline(time.Now().Add(s.backchannelDeadline))
			nr, err := runtime.sessionConn.Read(buf)
			if err != nil {
				logs.Trace("back-channel read error or idle: %v", err)
				session.close()
				return
			}
			if err := s.writeUDPBackchannelPacket(session, runtime, buf[:nr]); err != nil {
				session.close()
				return
			}
		}
	}()
}

func (s *UdpModeServer) writeUDPBackchannelPacket(session *udpClientSession, runtime *udpWorkerRuntime, payload []byte) error {
	if err := writeObservedUDPBackchannel(s.listener, runtime.addr, runtime.serviceLimiter, payload, runtime.task, runtime.routeStats, runtime.link.Option.RouteUUID, runtime.boundNode); err != nil {
		logs.Warn("error writing back to client: %v", err)
		return err
	}
	return nil
}

func writeObservedUDPBackchannel(writer udpPacketWriter, addr net.Addr, limiter rate.Limiter, payload []byte, task *file.Tunnel, routeStats proxyRouteRuntimeCollector, routeUUID string, boundNode proxyRouteRuntimeBoundNode) error {
	if writer == nil {
		return net.ErrClosed
	}
	written, err := writeWithLimiter(limiter, payload, func(current []byte) (int, error) {
		return writer.WriteTo(current, addr)
	})
	if err != nil {
		return err
	}
	if written <= 0 {
		return nil
	}
	var client *file.Client
	if task != nil {
		client = task.Client
	}
	if err := observeServiceTrafficWithBoundNode(client, task, nil, routeStats, routeUUID, boundNode, 0, int64(written)); err != nil {
		logs.Warn("udp service traffic observer rejected outbound payload: %v", err)
		return err
	}
	return nil
}

func (s *UdpModeServer) forwardUDPClientPackets(session *udpClientSession, runtime *udpWorkerRuntime) {
	for {
		select {
		case <-session.ctx.Done():
			return
		case pkt, ok := <-session.ch:
			if !ok {
				return
			}
			if err := s.writeUDPBridgePacket(session, runtime, pkt); err != nil {
				common.BufPoolUdp.Put(pkt.buf)
				session.close()
				return
			}
			common.BufPoolUdp.Put(pkt.buf)
		}
	}
}

func (s *UdpModeServer) writeUDPBridgePacket(session *udpClientSession, runtime *udpWorkerRuntime, pkt udpPacket) error {
	data, serviceBytes, headerBytes := buildUDPBridgePayload(session, runtime.task, runtime.addr, pkt)
	chargeLimiterBytes(runtime.serviceLimiter, serviceBytes)
	n, err := runtime.sessionConn.Write(data)
	payloadWritten := n - headerBytes
	if payloadWritten < 0 {
		payloadWritten = 0
	}
	refundLimiterBytes(runtime.serviceLimiter, serviceBytes, payloadWritten)
	if err != nil {
		return err
	}
	if payloadWritten > 0 {
		return observeServiceTrafficWithBoundNode(runtime.task.Client, runtime.task, nil, runtime.routeStats, runtime.link.Option.RouteUUID, runtime.boundNode, int64(payloadWritten), 0)
	}
	return nil
}

func buildUDPBridgePayload(session *udpClientSession, task *file.Tunnel, addr *net.UDPAddr, pkt udpPacket) ([]byte, int, int) {
	data := pkt.buf[:pkt.n]
	serviceBytes := len(data)
	headerBytes := 0
	session.once.Do(func() {
		if task.Target.ProxyProtocol != 0 {
			hdr := conn.BuildProxyProtocolHeaderByAddr(addr, &net.UDPAddr{Port: task.Port}, task.Target.ProxyProtocol)
			headerBytes = len(hdr)
			if headerBytes > 0 {
				mergeBuf := make([]byte, headerBytes+len(data))
				copy(mergeBuf, hdr)
				copy(mergeBuf[headerBytes:], data)
				data = mergeBuf
			}
		}
	})
	return data, serviceBytes, headerBytes
}
