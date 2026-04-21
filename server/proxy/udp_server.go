package proxy

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
)

type UdpModeServer struct {
	*BaseServer
	listener            *net.UDPConn
	sessions            sync.Map // key: clientAddr.String(), value: *udpClientSession
	backchannelDeadline time.Duration
	workerWG            sync.WaitGroup
	sessionWorker       udpSessionWorkerFunc
}

var errUDPServerUnavailable = errors.New("udp server unavailable")

func NewUdpModeServer(bridge NetBridge, task *file.Tunnel) *UdpModeServer {
	return newUdpModeServerWithRuntimeSources(currentProxyRuntimeSources(), bridge, task)
}

func NewUdpModeServerWithRuntime(runtime proxyRuntimeContext, bridge NetBridge, task *file.Tunnel) *UdpModeServer {
	return newUdpModeServerWithRuntimeSources(injectedProxyRuntimeSources(runtime), bridge, task)
}

func NewUdpModeServerWithRuntimeRoot(runtimeRoot func() proxyRuntimeContext, bridge NetBridge, task *file.Tunnel) *UdpModeServer {
	return NewUdpModeServerWithRuntimeRootAndLocalProxyAllowed(runtimeRoot, currentProxyLocalProxyAllowed, bridge, task)
}

func NewUdpModeServerWithRuntimeRootAndLocalProxyAllowed(runtimeRoot func() proxyRuntimeContext, localProxyAllowed func() bool, bridge NetBridge, task *file.Tunnel) *UdpModeServer {
	return newUdpModeServerWithRuntimeSources(proxyRuntimeSources{
		runtimeRoot:       runtimeRoot,
		localProxyAllowed: localProxyAllowed,
	}, bridge, task)
}

func newUdpModeServerWithRuntimeSources(sources proxyRuntimeSources, bridge NetBridge, task *file.Tunnel) *UdpModeServer {
	return &UdpModeServer{
		BaseServer:          newBaseServerWithRuntimeSources(sources, bridge, task),
		backchannelDeadline: 60 * time.Second,
	}
}

func (s *UdpModeServer) Start() error {
	if s == nil || s.BaseServer == nil {
		return errUDPServerUnavailable
	}
	task := s.CurrentTask()
	if err := validateUDPRuntimeTask(task); err != nil {
		return err
	}
	listener, err := openUDPListener(task)
	if err != nil {
		return err
	}
	s.listener = listener
	return s.serveUDPPackets(task)
}

func openUDPListener(task *file.Tunnel) (*net.UDPConn, error) {
	return net.ListenUDP("udp", resolveUDPListenAddr(task))
}

func resolveUDPListenAddr(task *file.Tunnel) *net.UDPAddr {
	serverIP := "0.0.0.0"
	if task != nil && task.ServerIp != "" {
		serverIP = task.ServerIp
	}
	port := 0
	if task != nil {
		port = task.Port
	}
	return &net.UDPAddr{IP: net.ParseIP(serverIP), Port: port}
}

func (s *UdpModeServer) serveUDPPackets(task *file.Tunnel) error {
	for {
		packet, err := s.readUDPPacket()
		if err != nil {
			if shouldStopUDPServerReadLoop(err) {
				break
			}
			continue
		}
		s.handleUDPPacket(task, packet)
	}
	return nil
}

func (s *UdpModeServer) readUDPPacket() (udpInboundPacket, error) {
	buf := common.BufPoolUdp.Get()
	n, addr, err := s.listener.ReadFromUDP(buf)
	if err != nil {
		common.BufPoolUdp.Put(buf)
		return udpInboundPacket{}, err
	}
	return udpInboundPacket{
		udpPacket: udpPacket{buf: buf, n: n},
		addr:      addr,
	}, nil
}

func (s *UdpModeServer) handleUDPPacket(task *file.Tunnel, packet udpInboundPacket) {
	if s.shouldDropUDPPacket(task, packet.addr) {
		common.BufPoolUdp.Put(packet.buf)
		return
	}
	session := s.sessionForPacket(packet.addr)
	if session == nil {
		common.BufPoolUdp.Put(packet.buf)
		return
	}
	s.enqueueUDPPacket(session, packet.udpPacket)
}

func (s *UdpModeServer) shouldDropUDPPacket(task *file.Tunnel, addr *net.UDPAddr) bool {
	if addr == nil {
		return true
	}
	return s.BridgeIsServer() && s.IsClientSourceAccessDenied(task.Client, task, addr.String())
}

func (s *UdpModeServer) enqueueUDPPacket(session *udpClientSession, packet udpPacket) {
	select {
	case <-session.ctx.Done():
		common.BufPoolUdp.Put(packet.buf)
	case session.ch <- packet:
	default:
		common.BufPoolUdp.Put(packet.buf)
	}
}

func shouldStopUDPServerReadLoop(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

func validateUDPRuntimeTask(task *file.Tunnel) error {
	switch {
	case task == nil:
		return errors.New("nil udp task")
	case task.Client == nil:
		return errors.New("nil udp task client")
	case task.Client.Cnf == nil:
		return errors.New("nil udp client config")
	case task.Target == nil:
		return errors.New("nil udp target")
	case task.Flow == nil:
		return errors.New("nil udp task flow")
	case task.Client.Flow == nil:
		return errors.New("nil udp client flow")
	default:
		return nil
	}
}

func (s *UdpModeServer) Close() error {
	if s == nil {
		return nil
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	closeUDPSessions(snapshotUDPSessions(&s.sessions))
	s.workerWG.Wait()
	return nil
}
