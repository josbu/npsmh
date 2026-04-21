package p2pstate

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/p2p"
)

type probeSession struct {
	mu           sync.RWMutex
	sessionID    string
	routeKey     string
	token        string
	expiresAt    time.Time
	observations map[string]map[string]p2p.ProbeSample
	replay       *p2p.ReplayWindow
}

var (
	probeSessionsByID    sync.Map
	probeSessionsByRoute sync.Map
	cleanerOnce          sync.Once
	currentProbeTime     = time.Now
)

func Register(sessionID string, wire p2p.P2PWireSpec, token string, ttl time.Duration) {
	if sessionID == "" || token == "" {
		return
	}
	routeKey := p2p.WireRouteKey(p2p.NormalizeP2PWireSpec(wire, sessionID).RouteID)
	if routeKey == "" {
		return
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	startCleaner()
	if existing := getByID(sessionID); existing != nil {
		deleteSession(existing)
	}
	if existing := getByRoute(routeKey); existing != nil {
		deleteSession(existing)
	}
	session := newProbeSession(sessionID, routeKey, token, currentProbeTime().Add(ttl))
	probeSessionsByID.Store(sessionID, session)
	probeSessionsByRoute.Store(routeKey, session)
}

func Unregister(sessionID string) {
	if sessionID == "" {
		return
	}
	value, ok := probeSessionsByID.Load(sessionID)
	if !ok {
		return
	}
	session, ok := value.(*probeSession)
	if ok && session != nil {
		deleteSession(session)
		return
	}
	deleteSessionIDValueIfCurrent(sessionID, value)
}

func ValidateToken(sessionID, token string) bool {
	session := getByID(sessionID)
	if session == nil {
		return false
	}
	expired, tokenMatch := session.validateToken(currentProbeTime(), token)
	if expired {
		deleteSession(session)
		return false
	}
	return tokenMatch
}

func LookupSession(routeKey string) (p2p.UDPPacketLookupResult, bool) {
	session := getByRoute(routeKey)
	if session == nil {
		return p2p.UDPPacketLookupResult{}, false
	}
	lookup, expired := session.lookup(currentProbeTime())
	if expired {
		deleteSession(session)
		return p2p.UDPPacketLookupResult{}, false
	}
	if lookup.Token == "" {
		return p2p.UDPPacketLookupResult{}, false
	}
	return lookup, true
}

func RecordObservation(routeKey, role string, sample p2p.ProbeSample) {
	if routeKey == "" || role == "" || sample.ProbePort == 0 || sample.ObservedAddr == "" {
		return
	}
	session := getByRoute(routeKey)
	if session == nil {
		return
	}
	if expired := session.recordObservation(currentProbeTime(), role, sample); expired {
		deleteSession(session)
	}
}

func AcceptPacket(routeKey string, timestampMs int64, nonce string) bool {
	session := getByRoute(routeKey)
	if session == nil {
		return false
	}
	accepted, expired := session.acceptPacket(currentProbeTime(), timestampMs, nonce)
	if expired {
		deleteSession(session)
	}
	return accepted
}

func GetObservations(sessionID, role string) []p2p.ProbeSample {
	if sessionID == "" || role == "" {
		return nil
	}
	session := getByID(sessionID)
	if session == nil {
		return nil
	}
	samples, expired := session.observationsForRole(currentProbeTime(), role)
	if expired {
		deleteSession(session)
		return nil
	}
	if len(samples) == 0 {
		return nil
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].ProbePort != samples[j].ProbePort {
			return samples[i].ProbePort < samples[j].ProbePort
		}
		return samples[i].ServerReplyAddr < samples[j].ServerReplyAddr
	})
	return samples
}

func observationKey(sample p2p.ProbeSample) string {
	replyAddr := common.ValidateAddr(sample.ServerReplyAddr)
	if replyAddr == "" {
		replyAddr = common.ValidateAddr(sample.ObservedAddr)
	}
	if replyAddr == "" {
		return fmt.Sprintf("%d", sample.ProbePort)
	}
	return fmt.Sprintf("%d|%s", sample.ProbePort, replyAddr)
}

func newProbeSession(sessionID, routeKey, token string, expiresAt time.Time) *probeSession {
	return &probeSession{
		sessionID:    sessionID,
		routeKey:     routeKey,
		token:        token,
		expiresAt:    expiresAt,
		observations: make(map[string]map[string]p2p.ProbeSample),
		replay:       p2p.NewReplayWindow(30 * time.Second),
	}
}

func (s *probeSession) expiredLocked(now time.Time) bool {
	return !now.Before(s.expiresAt)
}

func (s *probeSession) expired(now time.Time) bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expiredLocked(now)
}

func (s *probeSession) validateToken(now time.Time, token string) (expired, matched bool) {
	if s == nil {
		return true, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.expiredLocked(now), s.token == token
}

func (s *probeSession) lookup(now time.Time) (p2p.UDPPacketLookupResult, bool) {
	if s == nil {
		return p2p.UDPPacketLookupResult{}, true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return p2p.UDPPacketLookupResult{
		SessionID: s.sessionID,
		Token:     s.token,
	}, s.expiredLocked(now)
}

func (s *probeSession) recordObservation(now time.Time, role string, sample p2p.ProbeSample) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(now) {
		return true
	}
	if _, ok := s.observations[role]; !ok {
		s.observations[role] = make(map[string]p2p.ProbeSample)
	}
	s.observations[role][observationKey(sample)] = sample
	return false
}

func (s *probeSession) acceptPacket(now time.Time, timestampMs int64, nonce string) (accepted, expired bool) {
	if s == nil {
		return false, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expiredLocked(now) {
		return false, true
	}
	if s.replay == nil {
		return true, false
	}
	return s.replay.Accept(timestampMs, nonce), false
}

func (s *probeSession) observationsForRole(now time.Time, role string) ([]p2p.ProbeSample, bool) {
	if s == nil {
		return nil, true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.expiredLocked(now) {
		return nil, true
	}
	byPort, ok := s.observations[role]
	if !ok {
		return nil, false
	}
	samples := make([]p2p.ProbeSample, 0, len(byPort))
	for _, sample := range byPort {
		samples = append(samples, sample)
	}
	return samples, false
}

func getByID(sessionID string) *probeSession {
	if sessionID == "" {
		return nil
	}
	value, ok := probeSessionsByID.Load(sessionID)
	if !ok {
		return nil
	}
	session, ok := value.(*probeSession)
	if !ok || session == nil {
		deleteSessionIDValueIfCurrent(sessionID, value)
		return nil
	}
	return session
}

func getByRoute(routeKey string) *probeSession {
	if routeKey == "" {
		return nil
	}
	value, ok := probeSessionsByRoute.Load(routeKey)
	if !ok {
		return nil
	}
	session, ok := value.(*probeSession)
	if !ok || session == nil {
		deleteSessionRouteValueIfCurrent(routeKey, value)
		return nil
	}
	return session
}

func deleteSession(session *probeSession) {
	if session == nil {
		return
	}
	if session.routeKey != "" {
		probeSessionsByRoute.CompareAndDelete(session.routeKey, session)
	}
	if session.sessionID != "" {
		probeSessionsByID.CompareAndDelete(session.sessionID, session)
	}
}

func deleteSessionIDValueIfCurrent(sessionID string, value interface{}) bool {
	if sessionID == "" || value == nil {
		return false
	}
	return probeSessionsByID.CompareAndDelete(sessionID, value)
}

func deleteSessionIDEntryIfCurrent(key, value interface{}) bool {
	if key == nil || value == nil {
		return false
	}
	return probeSessionsByID.CompareAndDelete(key, value)
}

func deleteSessionRouteValueIfCurrent(routeKey string, value interface{}) bool {
	if routeKey == "" || value == nil {
		return false
	}
	return probeSessionsByRoute.CompareAndDelete(routeKey, value)
}

func deleteSessionRouteEntryIfCurrent(key, value interface{}) bool {
	if key == nil || value == nil {
		return false
	}
	return probeSessionsByRoute.CompareAndDelete(key, value)
}

func cleanupProbeSessionIDEntries(now time.Time) {
	probeSessionsByID.Range(func(key, value any) bool {
		session, ok := value.(*probeSession)
		if !ok || session == nil {
			deleteSessionIDEntryIfCurrent(key, value)
			return true
		}
		sessionID, ok := key.(string)
		if !ok || sessionID == "" || sessionID != session.sessionID {
			deleteSessionIDEntryIfCurrent(key, value)
			return true
		}
		if session.expired(now) {
			deleteSession(session)
		}
		return true
	})
}

func cleanupProbeSessionRouteEntries(now time.Time) {
	probeSessionsByRoute.Range(func(key, value any) bool {
		session, ok := value.(*probeSession)
		if !ok || session == nil {
			deleteSessionRouteEntryIfCurrent(key, value)
			return true
		}
		routeKey, ok := key.(string)
		if !ok || routeKey == "" || routeKey != session.routeKey {
			deleteSessionRouteEntryIfCurrent(key, value)
			return true
		}
		if session.expired(now) {
			deleteSession(session)
		}
		return true
	})
}

func startCleaner() {
	cleanerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				now := currentProbeTime()
				cleanupProbeSessionIDEntries(now)
				cleanupProbeSessionRouteEntries(now)
			}
		}()
	})
}
