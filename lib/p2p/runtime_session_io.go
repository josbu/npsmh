package p2p

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

func (s *runtimeSession) startReadLoop(ctx context.Context, readConn net.PacketConn) {
	if s == nil || !packetConnUsable(readConn) {
		return
	}
	done := make(chan struct{})
	s.mu.Lock()
	if s.readLoopDone == nil {
		s.readLoopDone = make(map[net.PacketConn]chan struct{})
	}
	if _, exists := s.readLoopDone[readConn]; exists {
		s.mu.Unlock()
		return
	}
	s.readLoopDone[readConn] = done
	s.mu.Unlock()

	go func() {
		defer s.finishReadLoop(readConn, done)
		s.readLoopOnConn(ctx, readConn)
	}()
}

func (s *runtimeSession) finishReadLoop(readConn net.PacketConn, done chan struct{}) {
	if done != nil {
		close(done)
	}
	if s == nil || !packetConnUsable(readConn) {
		return
	}
	s.mu.Lock()
	if current, ok := s.readLoopDone[readConn]; ok && current == done {
		delete(s.readLoopDone, readConn)
	}
	s.mu.Unlock()
}

func (s *runtimeSession) stopSocketReadLoop(readConn net.PacketConn, timeout time.Duration) bool {
	if s == nil || !packetConnUsable(readConn) {
		return true
	}
	s.mu.Lock()
	done := s.readLoopDone[readConn]
	s.mu.Unlock()
	if done == nil {
		return true
	}
	if timeout <= 0 {
		timeout = 300 * time.Millisecond
	}
	_ = readConn.SetReadDeadline(time.Now())
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	defer func() {
		_ = readConn.SetReadDeadline(time.Time{})
	}()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func addrString(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
}

func (s *runtimeSession) addSocket(socket net.PacketConn) {
	if s == nil || !packetConnUsable(socket) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.sockets {
		if existing == socket {
			return
		}
	}
	s.sockets = append(s.sockets, socket)
}

func (s *runtimeSession) snapshotSockets() []net.PacketConn {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]net.PacketConn, 0, len(s.sockets))
	for _, socket := range s.sockets {
		if !packetConnUsable(socket) {
			continue
		}
		out = append(out, socket)
	}
	return out
}

type resolvedPunchTarget struct {
	index int
	addr  *net.UDPAddr
}

func (s *runtimeSession) resolvePunchTargets(localAddr net.Addr, targets []string) []resolvedPunchTarget {
	if len(targets) == 0 || localAddr == nil {
		return nil
	}
	network := detectAddrFamily(localAddr).network()
	if network == "" {
		return nil
	}
	resolved := make([]resolvedPunchTarget, 0, len(targets))
	for index, target := range targets {
		addr := s.resolvePunchTarget(network, target)
		if addr == nil {
			continue
		}
		resolved = append(resolved, resolvedPunchTarget{
			index: index,
			addr:  addr,
		})
	}
	return resolved
}

func (s *runtimeSession) resolvePunchTarget(network, target string) *net.UDPAddr {
	if s == nil {
		return nil
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	key := network + "|" + target

	s.mu.Lock()
	if cached := s.resolvedTargetAddrs[key]; cached != nil {
		s.mu.Unlock()
		return cached
	}
	s.mu.Unlock()

	addr, err := net.ResolveUDPAddr(network, target)
	if err != nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolvedTargetAddrs == nil {
		s.resolvedTargetAddrs = make(map[string]*net.UDPAddr)
	}
	if cached := s.resolvedTargetAddrs[key]; cached != nil {
		return cached
	}
	s.resolvedTargetAddrs[key] = addr
	return addr
}

func (s *runtimeSession) ownsSocket(socket net.PacketConn) bool {
	if s == nil || !packetConnUsable(socket) {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.sockets {
		if existing == socket {
			return true
		}
	}
	return false
}

func (s *runtimeSession) closeSockets(keep net.PacketConn) {
	if s == nil {
		return
	}
	if !packetConnUsable(keep) {
		keep = nil
	}
	s.mu.Lock()
	toClose := make([]net.PacketConn, 0, len(s.sockets))
	for _, socket := range s.sockets {
		if !packetConnUsable(socket) || socket == keep {
			continue
		}
		toClose = append(toClose, socket)
	}
	if keep != nil {
		s.sockets = []net.PacketConn{keep}
	} else {
		s.sockets = nil
	}
	s.mu.Unlock()
	for _, socket := range toClose {
		_ = socket.Close()
	}
}

func (s *runtimeSession) enableBirthdayFallback() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan.UseBirthdayAttack = true
}

func (s *runtimeSession) ensureRanker() *CandidateRanker {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ranker == nil {
		s.ranker = NewCandidateRanker(s.summary.Self, s.summary.Peer, s.plan)
	}
	return s.ranker
}

func (s *runtimeSession) ensurePacer() *sessionPacer {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pacer == nil {
		s.pacer = newSessionPacer(s.start)
	}
	return s.pacer
}

func (s *runtimeSession) candidateMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	maxAge := 5 * time.Second
	if s.plan.HandshakeTimeout > 0 && s.plan.HandshakeTimeout < 20*time.Second {
		maxAge = s.plan.HandshakeTimeout / 3
		if maxAge < 2*time.Second {
			maxAge = 2 * time.Second
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pruned := s.cm.PruneStale(maxAge)
			s.cm.CleanupClosed(maxAge)
			if pruned > 0 {
				_ = s.sendProgress("candidate_pruned", "ok", fmt.Sprintf("%d", pruned), addrString(s.localConn.LocalAddr()), s.cm.CandidateRemote(), map[string]string{
					"max_age_ms": fmt.Sprintf("%d", maxAge.Milliseconds()),
				})
			}
		}
	}
}
