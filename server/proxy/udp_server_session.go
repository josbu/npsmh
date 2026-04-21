package proxy

import (
	"context"
	"net"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

type udpPacket struct {
	buf []byte
	n   int
}

type udpInboundPacket struct {
	udpPacket
	addr *net.UDPAddr
}

type udpClientSession struct {
	ch     chan udpPacket
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once

	connMu        sync.RWMutex
	conn          net.Conn
	closeOnce     sync.Once
	backchannelWG sync.WaitGroup
}

type udpSessionWorkerFunc func(addr *net.UDPAddr, session *udpClientSession)

func (s *UdpModeServer) sessionForPacket(addr *net.UDPAddr) *udpClientSession {
	if addr == nil {
		return nil
	}
	key := addr.String()
	for {
		if session := s.loadActiveSession(key); session != nil {
			return session
		}
		session := s.newUDPSession(addr)
		actual, loaded := s.sessions.LoadOrStore(key, session)
		if loaded {
			session.close()
			existing, ok := actual.(*udpClientSession)
			if ok && existing.isUsable() {
				return existing
			}
			s.deleteSessionValueIfCurrent(key, actual)
			continue
		}
		s.startUDPSessionWorker(addr, session)
		return session
	}
}

func (s *UdpModeServer) startUDPSessionWorker(addr *net.UDPAddr, session *udpClientSession) {
	if s == nil || session == nil {
		return
	}
	worker := s.sessionWorker
	if worker == nil {
		worker = s.clientWorker
	}
	s.workerWG.Add(1)
	go func() {
		defer s.workerWG.Done()
		worker(addr, session)
	}()
}

func (s *UdpModeServer) newUDPSession(addr *net.UDPAddr) *udpClientSession {
	task := s.CurrentTask()
	if task != nil && task.Client != nil {
		logs.Trace("New udp packet from client %d: %v", task.Client.Id, addr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &udpClientSession{
		ch:     make(chan udpPacket, 1024),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *udpClientSession) isUsable() bool {
	if s == nil || s.ch == nil || s.ctx == nil || s.cancel == nil {
		return false
	}
	return s.ctx.Err() == nil
}

func (s *UdpModeServer) loadActiveSession(key string) *udpClientSession {
	if key == "" {
		return nil
	}
	value, loaded := s.sessions.Load(key)
	if !loaded {
		return nil
	}
	session, ok := value.(*udpClientSession)
	if !ok || session == nil {
		s.deleteSessionValueIfCurrent(key, value)
		return nil
	}
	if session.isUsable() {
		return session
	}
	s.deleteSessionIfCurrent(key, session)
	return nil
}

func (s *udpClientSession) setConn(c net.Conn) {
	if s == nil || c == nil {
		return
	}
	s.connMu.Lock()
	if s.ctx == nil || s.ctx.Err() != nil {
		s.connMu.Unlock()
		_ = c.Close()
		return
	}
	s.conn = c
	s.connMu.Unlock()
}

func (s *udpClientSession) close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		s.connMu.RLock()
		currentConn := s.conn
		s.connMu.RUnlock()
		if currentConn != nil {
			_ = currentConn.Close()
		}
	})
}

func (s *udpClientSession) waitBackchannel() {
	if s == nil {
		return
	}
	s.backchannelWG.Wait()
}

func (s *UdpModeServer) clientWorker(addr *net.UDPAddr, session *udpClientSession) {
	key := ""
	if addr != nil {
		key = addr.String()
	}
	defer s.cleanupUDPClientSession(key, session)

	task := s.CurrentTask()
	if err := validateUDPRuntimeTask(task); err != nil {
		logs.Warn("reject udp worker with malformed runtime task: %v", err)
		return
	}
	task = task.SelectRuntimeRoute()
	if err := validateUDPRuntimeTask(task); err != nil {
		logs.Warn("reject udp worker with malformed selected runtime task: %v", err)
		return
	}

	lease, err := s.acquireUDPWorkerLease(task)
	if err != nil {
		return
	}
	if lease != nil {
		defer lease.Release()
	}

	runtime, err := s.prepareUDPWorkerRuntime(task, addr, key, session)
	if err != nil {
		logs.Trace("SendLinkInfo error: %v", err)
		return
	}

	s.startUDPBackchannel(session, runtime)
	s.forwardUDPClientPackets(session, runtime)
}

func (s *UdpModeServer) cleanupUDPClientSession(key string, session *udpClientSession) {
	if session == nil {
		return
	}
	session.close()
	s.deleteSessionIfCurrent(key, session)
	session.waitBackchannel()
	for {
		select {
		case pkt := <-session.ch:
			common.BufPoolUdp.Put(pkt.buf)
		default:
			return
		}
	}
}

func (s *UdpModeServer) acquireUDPWorkerLease(task *file.Tunnel) (*proxyConnectionLease, error) {
	if !s.BridgeIsServer() {
		return nil, nil
	}
	lease, err := s.CheckFlowAndConnNum(task.Client, task, nil)
	if err != nil {
		logs.Warn("client Id %d, task Id %d flow/conn limit: %v", task.Client.Id, task.Id, err)
		return nil, err
	}
	return lease, nil
}

func (s *UdpModeServer) deleteSessionIfCurrent(key string, session *udpClientSession) bool {
	if s == nil || key == "" || session == nil {
		return false
	}
	return s.sessions.CompareAndDelete(key, session)
}

func (s *UdpModeServer) deleteSessionValueIfCurrent(key string, value interface{}) bool {
	if s == nil || key == "" || value == nil {
		return false
	}
	return s.sessions.CompareAndDelete(key, value)
}

func (s *UdpModeServer) deleteSessionEntryIfCurrent(key, value interface{}) bool {
	if s == nil || key == nil || value == nil {
		return false
	}
	return s.sessions.CompareAndDelete(key, value)
}

func snapshotUDPSessions(sessions *sync.Map) []*udpClientSession {
	if sessions == nil {
		return nil
	}

	targets := make([]*udpClientSession, 0)
	sessions.Range(func(key, value interface{}) bool {
		session, ok := value.(*udpClientSession)
		if !ok || session == nil {
			sessions.CompareAndDelete(key, value)
			return true
		}
		targets = append(targets, session)
		return true
	})
	return targets
}

func closeUDPSessions(sessions []*udpClientSession) {
	parallelCloseTargets(sessions, func(session *udpClientSession) {
		if session != nil {
			session.close()
		}
	})
}
