package proxy

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/rate"
)

var (
	socks5UDPAssociateIdleTimeout = 3 * time.Minute
	socks5UDPSweepInterval        = 30 * time.Second
)

type socks5UDPRegistry struct {
	task            *file.Tunnel
	linkOpener      BridgeLinkOpener
	serverRole      BridgeServerRole
	policyRoot      func() socks5UDPSourcePolicy
	allowLocalProxy bool
	listener        net.PacketConn
	serviceLimiter  func(*file.Client, *file.Tunnel, *file.Host) rate.Limiter
	bridgeLimiter   func(*file.Client) rate.Limiter
	ctx             context.Context
	cancel          context.CancelFunc
	closeOnce       sync.Once
	wg              sync.WaitGroup

	mu             sync.RWMutex
	closed         bool
	sessions       map[*socks5UDPSession]struct{}
	sessionsByAddr map[string]*socks5UDPSession
	sessionsByIP   map[string]map[*socks5UDPSession]struct{}
	pendingByIP    map[string][]*socks5UDPSession
}

type socks5UDPSourcePolicy interface {
	IsClientSourceAccessDenied(*file.Client, *file.Tunnel, string) bool
}

func newSocks5UDPRegistry(linkOpener BridgeLinkOpener, serverRole BridgeServerRole, task *file.Tunnel, allowLocalProxy bool, addr string, serviceLimiter func(*file.Client, *file.Tunnel, *file.Host) rate.Limiter, bridgeLimiter func(*file.Client) rate.Limiter) (*socks5UDPRegistry, error) {
	return newSocks5UDPRegistryWithPolicyRoot(linkOpener, serverRole, func() socks5UDPSourcePolicy {
		return currentProxyRuntimeRoot().accessPolicy
	}, task, allowLocalProxy, addr, serviceLimiter, bridgeLimiter)
}

func newSocks5UDPRegistryWithPolicy(linkOpener BridgeLinkOpener, serverRole BridgeServerRole, policy socks5UDPSourcePolicy, task *file.Tunnel, allowLocalProxy bool, addr string, serviceLimiter func(*file.Client, *file.Tunnel, *file.Host) rate.Limiter, bridgeLimiter func(*file.Client) rate.Limiter) (*socks5UDPRegistry, error) {
	return newSocks5UDPRegistryWithPolicyRoot(linkOpener, serverRole, func() socks5UDPSourcePolicy { return policy }, task, allowLocalProxy, addr, serviceLimiter, bridgeLimiter)
}

func newSocks5UDPRegistryWithPolicyRoot(linkOpener BridgeLinkOpener, serverRole BridgeServerRole, policyRoot func() socks5UDPSourcePolicy, task *file.Tunnel, allowLocalProxy bool, addr string, serviceLimiter func(*file.Client, *file.Tunnel, *file.Host) rate.Limiter, bridgeLimiter func(*file.Client) rate.Limiter) (*socks5UDPRegistry, error) {
	listener, err := conn.NewUdpConnByAddr(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &socks5UDPRegistry{
		task:            task,
		linkOpener:      linkOpener,
		serverRole:      serverRole,
		policyRoot:      policyRoot,
		allowLocalProxy: allowLocalProxy,
		listener:        listener,
		serviceLimiter:  serviceLimiter,
		bridgeLimiter:   bridgeLimiter,
		ctx:             ctx,
		cancel:          cancel,
		sessions:        make(map[*socks5UDPSession]struct{}),
		sessionsByAddr:  make(map[string]*socks5UDPSession),
		sessionsByIP:    make(map[string]map[*socks5UDPSession]struct{}),
		pendingByIP:     make(map[string][]*socks5UDPSession),
	}, nil
}

func (r *socks5UDPRegistry) ServiceRateLimiter() rate.Limiter {
	if r == nil || r.serviceLimiter == nil || r.task == nil {
		return nil
	}
	return r.serviceLimiter(r.task.Client, r.task, nil)
}

func (r *socks5UDPRegistry) BridgeRateLimiter(client *file.Client) rate.Limiter {
	if r == nil || r.bridgeLimiter == nil {
		return nil
	}
	return r.bridgeLimiter(client)
}

func (r *socks5UDPRegistry) BridgeIsServer() bool {
	return r != nil && !isNilProxyRuntimeValue(r.serverRole) && r.serverRole.IsServer()
}

func (r *socks5UDPRegistry) OpenBridgeLink(clientID int, link *conn.Link, task *file.Tunnel) (net.Conn, error) {
	if r == nil || isNilProxyRuntimeValue(r.linkOpener) {
		return nil, errProxyBridgeUnavailable
	}
	inheritLinkRouteUUID(link, task)
	return r.linkOpener.SendLinkInfo(clientID, link, task)
}

func (r *socks5UDPRegistry) start() {
	r.wg.Add(2)
	go r.readLoop()
	go r.cleanupLoop()
}

func (r *socks5UDPRegistry) LocalAddr() net.Addr {
	if r == nil || r.listener == nil {
		return nil
	}
	return r.listener.LocalAddr()
}

func (r *socks5UDPRegistry) newSession(control net.Conn) (*socks5UDPSession, error) {
	controlAddr, clientIP, err := resolveSocks5UDPClientIdentity(control)
	if err != nil {
		return nil, err
	}
	return r.newSessionWithClientIdentity(control, controlAddr, clientIP)
}

func (r *socks5UDPRegistry) newSessionWithClientIdentity(control net.Conn, controlAddr string, clientIP net.IP) (*socks5UDPSession, error) {
	if r == nil || r.isClosed() {
		return nil, net.ErrClosed
	}
	session, err := newSocks5UDPSessionWithClientIdentity(r, control, controlAddr, clientIP)
	if err != nil {
		return nil, err
	}
	if !r.registerSession(session) {
		session.Close()
		return nil, net.ErrClosed
	}
	return session, nil
}

func (r *socks5UDPRegistry) registerSession(session *socks5UDPSession) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}

	r.sessions[session] = struct{}{}
	r.addSessionIPLocked(session)
	r.addPendingSessionLocked(session)
	return true
}

func (r *socks5UDPRegistry) addSessionIPLocked(session *socks5UDPSession) {
	if r.sessionsByIP[session.clientIPKey] == nil {
		r.sessionsByIP[session.clientIPKey] = make(map[*socks5UDPSession]struct{})
	}
	r.sessionsByIP[session.clientIPKey][session] = struct{}{}
}

func (r *socks5UDPRegistry) addPendingSessionLocked(session *socks5UDPSession) {
	r.pendingByIP[session.clientIPKey] = append(r.pendingByIP[session.clientIPKey], session)
}

func (r *socks5UDPRegistry) unregisterSession(session *socks5UDPSession) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.sessions, session)
	r.removeBoundSessionLocked(session)
	r.removePendingSessionLocked(session)
	r.removeSessionIPLocked(session)
}

func (r *socks5UDPRegistry) removeBoundSessionLocked(session *socks5UDPSession) {
	boundKey, _ := session.boundIdentity()
	if boundKey == "" {
		return
	}
	if current, ok := r.sessionsByAddr[boundKey]; ok && current == session {
		delete(r.sessionsByAddr, boundKey)
	}
}

func (r *socks5UDPRegistry) removeSessionIPLocked(session *socks5UDPSession) {
	sessions := r.sessionsByIP[session.clientIPKey]
	if len(sessions) == 0 {
		return
	}
	delete(sessions, session)
	if len(sessions) == 0 {
		delete(r.sessionsByIP, session.clientIPKey)
	}
}

func (r *socks5UDPRegistry) removePendingSessionLocked(session *socks5UDPSession) {
	r.pendingByIP[session.clientIPKey] = removePendingSession(r.pendingByIP[session.clientIPKey], session)
	if len(r.pendingByIP[session.clientIPKey]) == 0 {
		delete(r.pendingByIP, session.clientIPKey)
	}
}

func (r *socks5UDPRegistry) sessionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

func (r *socks5UDPRegistry) readLoop() {
	defer r.wg.Done()

	for {
		buf := common.BufPoolUdp.Get()
		n, addr, err := r.listener.ReadFrom(buf)
		if err != nil {
			common.BufPoolUdp.Put(buf)
			if r.ctx.Err() != nil || isClosedPacketConnError(err) {
				return
			}
			logs.Debug("socks5 udp listener read error: %v", err)
			continue
		}

		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok || udpAddr == nil {
			common.BufPoolUdp.Put(buf)
			continue
		}
		if r.shouldDropClientDatagram(udpAddr) {
			common.BufPoolUdp.Put(buf)
			continue
		}

		session := r.pickSession(udpAddr)
		if session == nil {
			common.BufPoolUdp.Put(buf)
			logs.Debug("drop socks5 udp datagram from %v without active associate", udpAddr)
			continue
		}
		if !session.enqueue(buf, n) {
			common.BufPoolUdp.Put(buf)
		}
	}
}

func (r *socks5UDPRegistry) shouldDropClientDatagram(addr *net.UDPAddr) bool {
	if addr == nil || !r.BridgeIsServer() || r.task == nil || r.task.Client == nil {
		return false
	}
	policy := socks5UDPSourcePolicy(nil)
	if r != nil && r.policyRoot != nil {
		policy = r.policyRoot()
	}
	if policy == nil {
		return false
	}
	return policy.IsClientSourceAccessDenied(r.task.Client, r.task, addr.String())
}

func (r *socks5UDPRegistry) pickSession(addr *net.UDPAddr) *socks5UDPSession {
	if addr == nil {
		return nil
	}
	key := addr.String()
	ipKey := normalizeUDPIPKey(addr.IP)
	if ipKey == "" {
		return nil
	}

	if session := r.sessionByAddr(key); session != nil {
		return session
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	session := r.selectSessionLocked(key, ipKey, addr.Port)
	if session == nil {
		return nil
	}
	r.bindSessionLocked(session, addr)
	return session
}

func (r *socks5UDPRegistry) sessionByAddr(addrKey string) *socks5UDPSession {
	if r == nil || addrKey == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessionByAddrLocked(addrKey)
}

func (r *socks5UDPRegistry) selectSessionLocked(addrKey, ipKey string, port int) *socks5UDPSession {
	if session := r.sessionByAddrLocked(addrKey); session != nil {
		return session
	}
	if len(r.pendingByIP[ipKey]) == 1 {
		return r.singlePendingSessionByIPLocked(ipKey)
	}
	if len(r.sessionsByIP[ipKey]) > 0 || len(r.pendingByIP[ipKey]) > 0 {
		logSocks5UDPAmbiguousDrop(r.task, ipKey, port, len(r.sessionsByIP[ipKey]), len(r.pendingByIP[ipKey]))
	}
	return nil
}

func (r *socks5UDPRegistry) sessionByAddrLocked(addrKey string) *socks5UDPSession {
	return r.sessionsByAddr[addrKey]
}

func (r *socks5UDPRegistry) singlePendingSessionByIPLocked(ipKey string) *socks5UDPSession {
	pending := r.pendingByIP[ipKey]
	if len(pending) != 1 {
		return nil
	}
	return pending[0]
}

func (r *socks5UDPRegistry) bindSessionLocked(session *socks5UDPSession, addr *net.UDPAddr) {
	oldKey, _ := session.boundIdentity()
	if oldKey != "" {
		return
	}
	boundKey := session.bindClientAddr(addr)
	r.sessionsByAddr[boundKey] = session
	r.removePendingSessionLocked(session)
}

func (r *socks5UDPRegistry) Close() error {
	r.closeOnce.Do(func() {
		r.cancel()

		r.mu.Lock()
		r.closed = true
		sessions := make([]*socks5UDPSession, 0, len(r.sessions))
		for session := range r.sessions {
			sessions = append(sessions, session)
		}
		r.mu.Unlock()

		if r.listener != nil {
			_ = r.listener.Close()
		}

		closeSocks5UDPSessions(sessions)
		for _, session := range sessions {
			session.Wait()
		}
		r.wg.Wait()
	})
	return nil
}

func (r *socks5UDPRegistry) isClosed() bool {
	if r == nil {
		return true
	}
	r.mu.RLock()
	closed := r.closed
	r.mu.RUnlock()
	return closed
}

func (r *socks5UDPRegistry) cleanupLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(socks5UDPSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			idleSessions := r.collectIdleSessions(time.Now(), socks5UDPAssociateIdleTimeout)
			for _, session := range idleSessions {
				logs.Debug("close idle socks5 udp associate: client=%s idle=%s", session.controlRemoteAddr(), session.idleFor(time.Now()))
				session.Close()
			}
		}
	}
}

func (r *socks5UDPRegistry) collectIdleSessions(now time.Time, idleTimeout time.Duration) []*socks5UDPSession {
	if idleTimeout <= 0 {
		return nil
	}

	sessions := make([]*socks5UDPSession, 0)
	for _, session := range r.snapshotSessions() {
		if session.idleFor(now) >= idleTimeout {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (r *socks5UDPRegistry) snapshotSessions() []*socks5UDPSession {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	sessions := make([]*socks5UDPSession, 0, len(r.sessions))
	for session := range r.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

func removePendingSession(sessions []*socks5UDPSession, target *socks5UDPSession) []*socks5UDPSession {
	if len(sessions) == 0 {
		return sessions
	}
	out := sessions[:0]
	for _, session := range sessions {
		if session != target {
			out = append(out, session)
		}
	}
	return out
}

func normalizeUDPIPKey(ip net.IP) string {
	ip = common.NormalizeIP(ip)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}
	out := *addr
	if addr.IP != nil {
		out.IP = append(net.IP(nil), addr.IP...)
	}
	return &out
}

func isClosedPacketConnError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, net.ErrClosed)
}

func closeSocks5UDPSessions(sessions []*socks5UDPSession) {
	parallelCloseTargets(sessions, func(session *socks5UDPSession) {
		if session != nil {
			session.Close()
		}
	})
}
