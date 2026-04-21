package proxy

import (
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/p2p"
	"github.com/djylb/nps/server/p2pstate"
)

type P2PServer struct {
	basePort   int
	extraReply bool
	listeners  []*net.UDPConn
	ports      []int
	workers    chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
	listenerWG sync.WaitGroup
	workerWG   sync.WaitGroup
	probeHook  func(*net.UDPConn, int, *net.UDPAddr, []byte)
}

type p2pProbeIngress struct {
	addr *net.UDPAddr
	data []byte
}

func NewP2PServer(basePort int, extraReply bool) *P2PServer {
	workerCount := runtime.GOMAXPROCS(0) * 32
	if workerCount < 64 {
		workerCount = 64
	}
	return &P2PServer{
		basePort:   basePort,
		extraReply: extraReply,
		workers:    make(chan struct{}, workerCount),
		done:       make(chan struct{}),
	}
}

var p2pProbeListenPorts = listenProbeListeners
var errP2PServerUnavailable = errors.New("p2p server unavailable")

func (s *P2PServer) available() bool {
	return s != nil && s.workers != nil && s.done != nil
}

func (s *P2PServer) Start() error {
	if !s.available() {
		return errP2PServerUnavailable
	}
	if err := s.bootstrapListeners(); err != nil {
		_ = s.Close()
		return err
	}
	return s.runListeners()
}

func (s *P2PServer) StartBackground() error {
	if !s.available() {
		return errP2PServerUnavailable
	}
	if err := s.bootstrapListeners(); err != nil {
		_ = s.Close()
		return err
	}
	go func() {
		if err := s.runListeners(); err != nil && !errors.Is(err, net.ErrClosed) {
			logs.Error("p2p probe server stopped unexpectedly: %v", err)
		}
	}()
	return nil
}

func (s *P2PServer) bootstrapListeners() error {
	ports := []int{s.basePort, s.basePort + 1, s.basePort + 2}
	s.listeners = make([]*net.UDPConn, 0, len(ports))
	s.ports = make([]int, 0, len(ports)*2)
	for _, port := range ports {
		listeners, err := p2pProbeListenPorts(port)
		if err != nil {
			return err
		}
		for _, listener := range listeners {
			s.listeners = append(s.listeners, listener)
			s.ports = append(s.ports, port)
		}
	}
	return nil
}

func (s *P2PServer) runListeners() error {
	errCh := make(chan error, len(s.listeners))
	for i, listener := range s.listeners {
		s.listenerWG.Add(1)
		go s.serveListener(listener, s.ports[i], errCh)
	}
	remaining := len(s.listeners)
	for remaining > 0 {
		err := <-errCh
		if err != nil && !errors.Is(err, net.ErrClosed) {
			_ = s.Close()
			return err
		}
		remaining--
	}
	return nil
}

func (s *P2PServer) Close() error {
	var closeErr error
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.done != nil {
			close(s.done)
		}
		for _, listener := range s.listeners {
			if listener == nil {
				continue
			}
			if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = err
			}
		}
		s.listenerWG.Wait()
		s.workerWG.Wait()
	})
	return closeErr
}

func (s *P2PServer) acquireWorker() bool {
	if !s.available() {
		return false
	}
	select {
	case s.workers <- struct{}{}:
		return true
	case <-s.done:
		return false
	}
}

func (s *P2PServer) serveListener(listener *net.UDPConn, listenPort int, errCh chan<- error) {
	defer s.listenerWG.Done()
	for {
		ingress, err := readP2PProbeIngress(listener)
		if err != nil {
			logs.Trace("[P2P] probe listener read err port=%d err=%v", listenPort, err)
			errCh <- err
			return
		}
		if !s.acquireWorker() {
			releaseP2PProbeIngress(ingress)
			reportP2PListenerStop(errCh)
			return
		}
		select {
		case <-s.done:
			releaseP2PProbeIngress(ingress)
			<-s.workers
			reportP2PListenerStop(errCh)
			return
		default:
		}
		s.dispatchProbeIngress(listener, listenPort, ingress)
	}
}

func readP2PProbeIngress(listener *net.UDPConn) (p2pProbeIngress, error) {
	buf := common.BufPoolUdp.Get()
	n, addr, err := listener.ReadFromUDP(buf)
	if err != nil {
		common.BufPoolUdp.Put(buf)
		return p2pProbeIngress{}, err
	}
	return p2pProbeIngress{
		addr: addr,
		data: buf[:n],
	}, nil
}

func releaseP2PProbeIngress(ingress p2pProbeIngress) {
	if ingress.data == nil {
		return
	}
	common.BufPoolUdp.Put(ingress.data[:cap(ingress.data)])
}

func (s *P2PServer) dispatchProbeIngress(listener *net.UDPConn, listenPort int, ingress p2pProbeIngress) {
	s.workerWG.Add(1)
	go func() {
		defer func() {
			s.workerWG.Done()
			releaseP2PProbeIngress(ingress)
			<-s.workers
		}()
		s.handleProbeIngress(listener, listenPort, ingress.addr, ingress.data)
	}()
}

func (s *P2PServer) handleProbeIngress(listener *net.UDPConn, listenPort int, addr *net.UDPAddr, data []byte) {
	if s != nil && s.probeHook != nil {
		s.probeHook(listener, listenPort, addr, data)
		return
	}
	s.handleProbe(listener, listenPort, addr, data)
}

func reportP2PListenerStop(errCh chan<- error) {
	if errCh == nil {
		return
	}
	select {
	case errCh <- net.ErrClosed:
	default:
	}
}

func (s *P2PServer) handleProbe(listener *net.UDPConn, listenPort int, addr *net.UDPAddr, data []byte) {
	packet := decodeAcceptedP2PProbe(data)
	if packet == nil {
		return
	}
	s.recordProbeObservation(listener, listenPort, addr, packet)
	s.writeProbeAck(listener, listenPort, addr, packet)
	if !s.extraReply {
		return
	}
	s.sendExtraReply(listenPort, addr, packet)
}

func decodeAcceptedP2PProbe(data []byte) *p2p.UDPPacket {
	packet, err := p2p.DecodeUDPPacketWithLookup(data, p2pstate.LookupSession)
	if err != nil || packet.Type != "probe" {
		return nil
	}
	if !p2pstate.AcceptPacket(packet.WireID, packet.Timestamp, packet.Nonce) {
		return nil
	}
	return packet
}

func (s *P2PServer) recordProbeObservation(listener *net.UDPConn, listenPort int, addr *net.UDPAddr, packet *p2p.UDPPacket) {
	logs.Trace("[P2P] probe from=%s observed public port=%d", addr.String(), addr.Port)
	p2pstate.RecordObservation(packet.WireID, packet.Role, p2p.ProbeSample{
		Provider:        p2p.ProbeProviderNPS,
		Mode:            p2p.ProbeModeUDP,
		ProbePort:       listenPort,
		ObservedAddr:    addr.String(),
		ServerReplyAddr: listener.LocalAddr().String(),
	})
}

func (s *P2PServer) writeProbeAck(listener *net.UDPConn, listenPort int, addr *net.UDPAddr, packet *p2p.UDPPacket) {
	ack := p2p.NewProbeAckPacketWithWire(packet.SessionID, packet.Token, packet.Role, listenPort, addr.String(), false, p2p.P2PWireSpec{RouteID: packet.WireID})
	raw, err := p2p.EncodeUDPPacket(ack)
	if err != nil {
		return
	}
	_, _ = listener.WriteToUDP(raw, addr)
}

func (s *P2PServer) sendExtraReply(listenPort int, target *net.UDPAddr, packet *p2p.UDPPacket) {
	if target == nil || packet == nil {
		return
	}
	network := "udp4"
	if target.IP != nil && target.IP.To4() == nil {
		network = "udp6"
	}
	extraConn, err := net.ListenUDP(network, nil)
	if err != nil {
		logs.Trace("[P2P] extra reply fail port=%d target=%s err=%v", listenPort, target.String(), err)
		return
	}
	defer func() { _ = extraConn.Close() }()
	_ = extraConn.SetWriteDeadline(time.Now().Add(1 * time.Second))
	extraAck := p2p.NewProbeAckPacketWithWire(packet.SessionID, packet.Token, packet.Role, listenPort, target.String(), true, p2p.P2PWireSpec{RouteID: packet.WireID})
	extraRaw, err := p2p.EncodeUDPPacket(extraAck)
	if err != nil {
		logs.Trace("[P2P] extra reply fail port=%d target=%s err=%v", listenPort, target.String(), err)
		return
	}
	if _, err := extraConn.WriteToUDP(extraRaw, target); err == nil {
		logs.Trace("[P2P] extra reply success port=%d target=%s", listenPort, target.String())
	} else {
		logs.Trace("[P2P] extra reply fail port=%d target=%s err=%v", listenPort, target.String(), err)
	}
}

func listenProbeListeners(port int) ([]*net.UDPConn, error) {
	listeners := make([]*net.UDPConn, 0, 2)
	v4, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return nil, err
	}
	listeners = append(listeners, v4)
	logs.Info("start p2p probe listener network=udp4 port %d", port)

	if v6, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6unspecified, Port: port}); err == nil {
		listeners = append(listeners, v6)
		logs.Info("start p2p probe listener network=udp6 port %d", port)
	} else if isOptionalIPv6ListenErr(err) {
		logs.Trace("[P2P] skip udp6 probe listener port=%d err=%v", port, err)
	} else {
		for _, listener := range listeners {
			_ = listener.Close()
		}
		return nil, err
	}
	return listeners, nil
}

func isOptionalIPv6ListenErr(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "address family not supported") ||
		strings.Contains(message, "cannot assign requested address") ||
		strings.Contains(message, "no such device")
}
