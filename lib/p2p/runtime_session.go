package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

var (
	ErrNATNotSupportP2P            = errors.New("nat type is not support p2p")
	ErrP2PTokenMismatch            = errors.New("p2p token mismatch")
	ErrP2PSessionAbort             = errors.New("p2p session aborted")
	ErrUnexpectedP2PControlPayload = errors.New("unexpected p2p control payload")
)

func mapP2PContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrNATNotSupportP2P
	}
	if err != nil {
		return err
	}
	return ErrNATNotSupportP2P
}

type SessionEndpoints struct {
	RendezvousRemote string
	CandidateRemote  string
	ConfirmedRemote  string
}

type confirmedPair struct {
	owner      *runtimeSession
	conn       net.PacketConn
	localAddr  string
	remoteAddr string
}

type outboundNominationState struct {
	key       string
	epoch     uint32
	startedAt time.Time
}

type inboundProposalState struct {
	key   string
	epoch uint32
}

type retryLoopToken struct{}

type sessionEvent string

type sessionEventSpec struct {
	nextPhase sessionPhase
	samePhase bool
}

const (
	sessionEventSessionCreated     sessionEvent = "session_created"
	sessionEventReadySent          sessionEvent = "ready_sent"
	sessionEventPunchGo            sessionEvent = "punch_go"
	sessionEventSprayStarted       sessionEvent = "spray_started"
	sessionEventCandidateHit       sessionEvent = "candidate_hit"
	sessionEventNominationQueued   sessionEvent = "nomination_queued"
	sessionEventProposalAdopted    sessionEvent = "proposal_adopted"
	sessionEventCandidateNominated sessionEvent = "candidate_nominated"
	sessionEventAcceptReceived     sessionEvent = "accept_received"
	sessionEventReadyReceived      sessionEvent = "ready_received"
	sessionEventCandidateConfirmed sessionEvent = "candidate_confirmed"
	sessionEventHandoverBegin      sessionEvent = "handover_begin"
	sessionEventHandshakeTimeout   sessionEvent = "handshake_timeout"
)

var sessionEventSpecs = map[sessionEvent]sessionEventSpec{
	sessionEventSessionCreated:     {nextPhase: sessionPhaseCreated},
	sessionEventReadySent:          {nextPhase: sessionPhaseReadySent},
	sessionEventPunchGo:            {nextPhase: sessionPhasePunching},
	sessionEventSprayStarted:       {nextPhase: sessionPhasePunching},
	sessionEventCandidateHit:       {nextPhase: sessionPhaseNegotiating},
	sessionEventNominationQueued:   {samePhase: true},
	sessionEventProposalAdopted:    {nextPhase: sessionPhaseNegotiating},
	sessionEventCandidateNominated: {nextPhase: sessionPhaseNegotiating},
	sessionEventAcceptReceived:     {samePhase: true},
	sessionEventReadyReceived:      {samePhase: true},
	sessionEventCandidateConfirmed: {nextPhase: sessionPhaseConfirmed},
	sessionEventHandoverBegin:      {nextPhase: sessionPhaseHandover},
	sessionEventHandshakeTimeout:   {nextPhase: sessionPhaseFailed},
}

type sessionPhase string

const (
	sessionPhaseCreated     sessionPhase = "created"
	sessionPhaseReadySent   sessionPhase = "ready_sent"
	sessionPhasePunching    sessionPhase = "punching"
	sessionPhaseNegotiating sessionPhase = "negotiating"
	sessionPhaseConfirmed   sessionPhase = "confirmed"
	sessionPhaseHandover    sessionPhase = "handover"
	sessionPhaseFailed      sessionPhase = "failed"
)

type sessionPhaseTransition struct {
	From  sessionPhase
	To    sessionPhase
	Event sessionEvent
	At    time.Time
}

var sessionPhaseOrder = map[sessionPhase]int{
	sessionPhaseCreated:     0,
	sessionPhaseReadySent:   1,
	sessionPhasePunching:    2,
	sessionPhaseNegotiating: 3,
	sessionPhaseConfirmed:   4,
	sessionPhaseHandover:    5,
}

const defaultReplayWindow = 30 * time.Second
const defaultReplayWindowMaxEntries = 512

type ReplayWindow struct {
	mu         sync.Mutex
	maxAge     time.Duration
	maxEntries int
	observed   map[string]int64
}

func NewReplayWindow(maxAge time.Duration) *ReplayWindow {
	return newReplayWindow(maxAge, defaultReplayWindowMaxEntries)
}

func newReplayWindow(maxAge time.Duration, maxEntries int) *ReplayWindow {
	if maxAge <= 0 {
		maxAge = defaultReplayWindow
	}
	if maxEntries <= 0 {
		maxEntries = defaultReplayWindowMaxEntries
	}
	return &ReplayWindow{
		maxAge:     maxAge,
		maxEntries: maxEntries,
		observed:   make(map[string]int64),
	}
}

func (w *ReplayWindow) Accept(timestampMs int64, nonce string) bool {
	if w == nil {
		return true
	}
	if nonce == "" || timestampMs <= 0 {
		return false
	}
	now := time.Now().UnixMilli()
	windowMs := w.maxAge.Milliseconds()
	if timestampMs < now-windowMs || timestampMs > now+windowMs {
		return false
	}
	expireBefore := now - windowMs
	w.mu.Lock()
	defer w.mu.Unlock()
	for seenNonce, seenTs := range w.observed {
		if seenTs < expireBefore {
			delete(w.observed, seenNonce)
		}
	}
	w.pruneOverflowLocked()
	if _, ok := w.observed[nonce]; ok {
		return false
	}
	w.observed[nonce] = timestampMs
	return true
}

func (w *ReplayWindow) pruneOverflowLocked() {
	if w == nil || w.maxEntries <= 0 || len(w.observed) < w.maxEntries {
		return
	}
	overflow := len(w.observed) - w.maxEntries + 1
	type replaySeen struct {
		nonce string
		ts    int64
	}
	oldest := make([]replaySeen, 0, len(w.observed))
	for nonce, ts := range w.observed {
		oldest = append(oldest, replaySeen{nonce: nonce, ts: ts})
	}
	sort.Slice(oldest, func(i, j int) bool {
		if oldest[i].ts != oldest[j].ts {
			return oldest[i].ts < oldest[j].ts
		}
		return oldest[i].nonce < oldest[j].nonce
	})
	for i := 0; i < overflow && i < len(oldest); i++ {
		delete(w.observed, oldest[i].nonce)
	}
}

type runtimeSession struct {
	start     P2PPunchStart
	summary   P2PProbeSummary
	plan      PunchPlan
	control   *conn.Conn
	localConn net.PacketConn
	sockets   []net.PacketConn
	cm        *CandidateManager
	ranker    *CandidateRanker
	pacer     *sessionPacer
	replay    *ReplayWindow

	mu                  sync.Mutex
	readLoopDone        map[net.PacketConn]chan struct{}
	resolvedTargetAddrs map[string]*net.UDPAddr
	endpoints           SessionEndpoints
	confirmed           chan confirmedPair
	nominate            map[string]context.CancelFunc
	accept              map[string]context.CancelFunc
	nominateTokens      map[string]*retryLoopToken
	acceptTokens        map[string]*retryLoopToken
	outboundNomination  outboundNominationState
	inboundProposal     inboundProposalState
	nextNominationEpoch uint32
	nominationScheduled bool
	phase               sessionPhase
	lastEvent           sessionEvent
	phaseTransitions    []sessionPhaseTransition
	stats               sessionStats
}

type sessionStats struct {
	tokenMismatchDropped int
	tokenVerified        bool
	replayDropped        int
	sessionEventDropped  int
	lastDroppedEvent     sessionEvent
}

func (s *runtimeSession) snapshotStats() sessionStats {
	if s == nil {
		return sessionStats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *runtimeSession) currentPhase() sessionPhase {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

func (s *runtimeSession) snapshotPhaseTransitions() []sessionPhaseTransition {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sessionPhaseTransition, len(s.phaseTransitions))
	copy(out, s.phaseTransitions)
	return out
}

func (s *runtimeSession) phaseMeta() (string, string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.phase), string(s.lastEvent)
}

func (s *runtimeSession) dispatchSessionEvent(event sessionEvent) bool {
	if s == nil || event == "" {
		return false
	}
	spec, ok := sessionEventSpecs[event]
	if !ok {
		s.recordDroppedSessionEvent(event)
		return false
	}
	if spec.samePhase {
		return s.recordPhaseEvent(event)
	}
	return s.transitionPhase(event, spec.nextPhase)
}

func (s *runtimeSession) transitionPhase(event sessionEvent, next sessionPhase) bool {
	if s == nil || next == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.phase
	if current == next {
		s.recordDroppedSessionEventLocked(event)
		return false
	}
	switch current {
	case sessionPhaseFailed, sessionPhaseHandover:
		s.recordDroppedSessionEventLocked(event)
		return false
	}
	if next == sessionPhaseFailed {
		s.recordPhaseTransitionLocked(current, next, event)
		return true
	}
	if current == sessionPhaseFailed {
		s.recordDroppedSessionEventLocked(event)
		return false
	}
	if sessionPhaseOrder[next] < sessionPhaseOrder[current] {
		s.recordDroppedSessionEventLocked(event)
		return false
	}
	s.recordPhaseTransitionLocked(current, next, event)
	return true
}

func (s *runtimeSession) recordPhaseEvent(event sessionEvent) bool {
	if s == nil || event == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.phase {
	case sessionPhaseFailed, sessionPhaseHandover:
		s.recordDroppedSessionEventLocked(event)
		return false
	}
	s.recordPhaseTransitionLocked(s.phase, s.phase, event)
	return true
}

func (s *runtimeSession) recordDroppedSessionEvent(event sessionEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordDroppedSessionEventLocked(event)
}

func (s *runtimeSession) recordDroppedSessionEventLocked(event sessionEvent) {
	s.stats.sessionEventDropped++
	if event != "" {
		s.stats.lastDroppedEvent = event
	}
}

func (s *runtimeSession) recordPhaseTransitionLocked(from, to sessionPhase, event sessionEvent) {
	s.phase = to
	s.lastEvent = event
	s.phaseTransitions = append(s.phaseTransitions, sessionPhaseTransition{
		From:  from,
		To:    to,
		Event: event,
		At:    time.Now(),
	})
}

func normalizeRuntimeSessionContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func packetConnLocalAddr(c net.PacketConn) (addr net.Addr) {
	if c == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			addr = nil
		}
	}()
	return c.LocalAddr()
}

func packetConnUsable(c net.PacketConn) bool {
	return packetConnLocalAddr(c) != nil
}

func validateRuntimePunchStart(start P2PPunchStart, expectedRole string) error {
	if strings.TrimSpace(start.SessionID) == "" {
		return fmt.Errorf("%w: missing punch start session id", ErrUnexpectedP2PControlPayload)
	}
	if strings.TrimSpace(start.Token) == "" {
		return fmt.Errorf("%w: missing punch start token", ErrUnexpectedP2PControlPayload)
	}
	role := strings.TrimSpace(start.Role)
	if role == "" {
		return fmt.Errorf("%w: missing punch start role", ErrUnexpectedP2PControlPayload)
	}
	if expectedRole != "" && role != expectedRole {
		return fmt.Errorf("%w: punch start role=%q want %q", ErrUnexpectedP2PControlPayload, role, expectedRole)
	}
	if role == strings.TrimSpace(start.PeerRole) {
		return fmt.Errorf("%w: punch start peer role mirrors self role %q", ErrUnexpectedP2PControlPayload, role)
	}
	return nil
}

func validateRuntimeSummary(start P2PPunchStart, summary P2PProbeSummary) error {
	if strings.TrimSpace(summary.SessionID) != strings.TrimSpace(start.SessionID) {
		return fmt.Errorf("%w: summary session=%q want %q", ErrUnexpectedP2PControlPayload, summary.SessionID, start.SessionID)
	}
	if strings.TrimSpace(summary.Token) != strings.TrimSpace(start.Token) {
		return fmt.Errorf("%w: summary token mismatch", ErrUnexpectedP2PControlPayload)
	}
	if strings.TrimSpace(summary.Role) != strings.TrimSpace(start.Role) {
		return fmt.Errorf("%w: summary role=%q want %q", ErrUnexpectedP2PControlPayload, summary.Role, start.Role)
	}
	if expectedPeerRole := strings.TrimSpace(start.PeerRole); expectedPeerRole != "" && strings.TrimSpace(summary.PeerRole) != expectedPeerRole {
		return fmt.Errorf("%w: summary peer role=%q want %q", ErrUnexpectedP2PControlPayload, summary.PeerRole, expectedPeerRole)
	}
	if role := strings.TrimSpace(start.Role); role != "" && strings.TrimSpace(summary.Self.Role) != "" && strings.TrimSpace(summary.Self.Role) != role {
		return fmt.Errorf("%w: summary self role=%q want %q", ErrUnexpectedP2PControlPayload, summary.Self.Role, role)
	}
	if peerRole := strings.TrimSpace(start.PeerRole); peerRole != "" && strings.TrimSpace(summary.Peer.Role) != "" && strings.TrimSpace(summary.Peer.Role) != peerRole {
		return fmt.Errorf("%w: summary peer self role=%q want %q", ErrUnexpectedP2PControlPayload, summary.Peer.Role, peerRole)
	}
	return nil
}

func validateRuntimePunchGo(start P2PPunchStart, goMsg P2PPunchGo) error {
	if strings.TrimSpace(goMsg.SessionID) != strings.TrimSpace(start.SessionID) {
		return fmt.Errorf("%w: punch go session=%q want %q", ErrUnexpectedP2PControlPayload, goMsg.SessionID, start.SessionID)
	}
	if strings.TrimSpace(goMsg.Role) != strings.TrimSpace(start.Role) {
		return fmt.Errorf("%w: punch go role=%q want %q", ErrUnexpectedP2PControlPayload, goMsg.Role, start.Role)
	}
	return nil
}

func RunVisitorSession(ctx context.Context, control *conn.Conn, localAddr, transportMode, transportData string) (net.PacketConn, string, string, string, string, string, string, time.Duration, error) {
	ctx = normalizeRuntimeSessionContext(ctx)
	start, err := ReadBridgeJSONContext[P2PPunchStart](ctx, control, common.P2P_PUNCH_START)
	if err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	start = NormalizeP2PPunchStart(start)
	if err := validateRuntimePunchStart(start, common.WORK_P2P_VISITOR); err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	return runBridgeSession(ctx, control, start, localAddr, transportMode, transportData)
}

func RunProviderSession(ctx context.Context, control *conn.Conn, start P2PPunchStart, localAddr, transportMode, transportData string) (net.PacketConn, string, string, string, string, string, string, time.Duration, error) {
	ctx = normalizeRuntimeSessionContext(ctx)
	start = NormalizeP2PPunchStart(start)
	if err := validateRuntimePunchStart(start, common.WORK_P2P_PROVIDER); err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	return runBridgeSession(ctx, control, start, localAddr, transportMode, transportData)
}

func runBridgeSession(ctx context.Context, control *conn.Conn, start P2PPunchStart, localAddr, transportMode, transportData string) (net.PacketConn, string, string, string, string, string, string, time.Duration, error) {
	ctx = normalizeRuntimeSessionContext(ctx)
	start = NormalizeP2PPunchStart(start)
	_ = trySendProgress(control, start.SessionID, start.Role, "probe_started", "")
	families, err := openProbeRuntimeFamilies(ctx, localAddr, start)
	if err != nil {
		_ = trySendProgressWithStatus(control, start.SessionID, start.Role, "probe_failed", "fail", err.Error(), localAddr, "", nil, nil)
		_ = trySendAbort(control, start.SessionID, start.Role, fmt.Sprintf("probe failed: %v", err))
		return nil, "", "", "", "", "", "", 0, err
	}
	defer func() {
		if err == nil {
			return
		}
		closeProbeRuntimeFamilies(families)
	}()

	selfFamilies := make([]P2PFamilyInfo, 0, len(families))
	for _, family := range families {
		selfFamilies = append(selfFamilies, P2PFamilyInfo{
			Family:     family.family.String(),
			Nat:        family.observation,
			LocalAddrs: family.localAddrs,
		})
	}
	self := BuildPeerInfo(start.Role, transportMode, transportData, selfFamilies)
	report := P2PProbeReport{
		SessionID: start.SessionID,
		Token:     start.Token,
		Role:      start.Role,
		PeerRole:  start.PeerRole,
		Self:      self,
	}
	_ = trySendProgress(control, start.SessionID, start.Role, "probe_completed", self.Nat.NATType)
	if err = WriteBridgeMessage(control, common.P2P_PROBE_REPORT, report); err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	_ = trySendProgress(control, start.SessionID, start.Role, "probe_reported", "")

	summary, err := ReadBridgeJSONContext[P2PProbeSummary](ctx, control, common.P2P_PROBE_SUMMARY)
	if err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	if err = validateRuntimeSummary(start, summary); err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	_ = trySendProgress(control, start.SessionID, start.Role, "summary_received", summary.Peer.Nat.NATType)
	workers, err := buildRuntimeFamilyWorkers(start, summary, control, families)
	if err != nil {
		_ = trySendProgressWithStatus(control, start.SessionID, start.Role, "family_select_failed", "fail", err.Error(), "", "", nil, nil)
		_ = trySendAbort(control, start.SessionID, start.Role, err.Error())
		return nil, "", "", "", "", "", "", 0, err
	}
	defer func() {
		if err == nil {
			return
		}
		failRuntimeWorkers(workers)
	}()

	primaryLocalAddr := primaryRuntimeWorkerLocalAddr(workers)
	handshakeTimeout := resolveRuntimeTimeout(time.Duration(summary.Timeouts.HandshakeTimeoutMs)*time.Millisecond, 20*time.Second)
	punchCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	if err = WriteBridgeMessage(control, common.P2P_PUNCH_READY, P2PPunchReady{
		SessionID: start.SessionID,
		Role:      start.Role,
	}); err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	markRuntimeWorkersReady(workers)
	if err = awaitPunchGo(punchCtx, control, start, primaryLocalAddr); err != nil {
		return nil, "", "", "", "", "", "", 0, err
	}
	startRuntimeWorkers(workers, punchCtx)
	transportTimeout := resolveRuntimeTimeout(time.Duration(summary.Timeouts.TransportTimeoutMs)*time.Millisecond, 10*time.Second)

	if pair, ok := awaitRuntimeConfirmation(punchCtx, workers); ok {
		result, resultErr := finalizeRuntimeHandover(start, transportMode, workers, pair, transportTimeout)
		if resultErr != nil {
			err = resultErr
			return nil, "", "", "", "", "", "", 0, err
		}
		return result.conn, result.remoteAddr, result.localAddr, result.sessionID, result.role, result.mode, result.transportData, result.transportTimeout, nil
	}
	err = failRuntimeHandshake(control, start, workers, mapP2PContextError(punchCtx.Err()))
	return nil, "", "", "", "", "", "", 0, err
}

func newRuntimeSession(start P2PPunchStart, summary P2PProbeSummary, plan PunchPlan, control *conn.Conn, localConn net.PacketConn, confirmed chan confirmedPair) *runtimeSession {
	rs := &runtimeSession{
		start:          start,
		summary:        summary,
		plan:           plan,
		control:        control,
		localConn:      localConn,
		sockets:        []net.PacketConn{localConn},
		cm:             NewCandidateManager(firstObservedAddr(summary.Peer)),
		ranker:         NewCandidateRanker(summary.Self, summary.Peer, plan),
		pacer:          newSessionPacer(start),
		replay:         NewReplayWindow(30 * time.Second),
		readLoopDone:   make(map[net.PacketConn]chan struct{}),
		confirmed:      confirmed,
		nominate:       make(map[string]context.CancelFunc),
		accept:         make(map[string]context.CancelFunc),
		nominateTokens: make(map[string]*retryLoopToken),
		acceptTokens:   make(map[string]*retryLoopToken),
	}
	rs.endpoints.RendezvousRemote = firstObservedAddr(summary.Peer)
	rs.markSessionCreated()
	return rs
}

func (s *runtimeSession) markSessionCreated() bool {
	return s.dispatchSessionEvent(sessionEventSessionCreated)
}

func (s *runtimeSession) markReadySent() bool {
	return s.dispatchSessionEvent(sessionEventReadySent)
}

func (s *runtimeSession) markPunching() bool {
	return s.dispatchSessionEvent(sessionEventPunchGo)
}

func (s *runtimeSession) markSprayStarted() bool {
	return s.dispatchSessionEvent(sessionEventSprayStarted)
}

func (s *runtimeSession) markCandidateHit() bool {
	return s.dispatchSessionEvent(sessionEventCandidateHit)
}

func (s *runtimeSession) markNominationQueued() bool {
	return s.dispatchSessionEvent(sessionEventNominationQueued)
}

func (s *runtimeSession) markProposalAdopted() bool {
	return s.dispatchSessionEvent(sessionEventProposalAdopted)
}

func (s *runtimeSession) markCandidateNominated() bool {
	return s.dispatchSessionEvent(sessionEventCandidateNominated)
}

func (s *runtimeSession) markAcceptReceived() bool {
	return s.dispatchSessionEvent(sessionEventAcceptReceived)
}

func (s *runtimeSession) markReadyReceived() bool {
	return s.dispatchSessionEvent(sessionEventReadyReceived)
}

func (s *runtimeSession) markCandidateConfirmed() bool {
	return s.dispatchSessionEvent(sessionEventCandidateConfirmed)
}

func (s *runtimeSession) markHandoverBegin() bool {
	return s.dispatchSessionEvent(sessionEventHandoverBegin)
}

func (s *runtimeSession) markHandshakeTimeout() bool {
	return s.dispatchSessionEvent(sessionEventHandshakeTimeout)
}

func (s *runtimeSession) dispatchValidatedPacket(ctx context.Context, readConn net.PacketConn, addr net.Addr, localAddr, remoteAddr string, packet *UDPPacket) {
	if s == nil || packet == nil {
		return
	}
	switch packet.Type {
	case packetTypeProbe, packetTypePunch:
		s.handleProbePacket(ctx, readConn, addr, localAddr, remoteAddr)
	case packetTypeSucc:
		s.handleSuccessPacket(ctx, readConn, addr, localAddr, remoteAddr)
	case packetTypeEnd:
		s.handleProposalPacket(ctx, readConn, addr, localAddr, remoteAddr, effectiveNominationEpoch(packet))
	case packetTypeAccept:
		s.handleAcceptPacket(ctx, readConn, addr, localAddr, remoteAddr, effectiveNominationEpoch(packet))
	case packetTypeReady:
		s.handleReadyPacket(readConn, localAddr, remoteAddr, effectiveNominationEpoch(packet))
	}
}

func (s *runtimeSession) beginRetryLoop(loopMap *map[string]context.CancelFunc, tokenMap *map[string]*retryLoopToken, key string, cancel context.CancelFunc) (bool, []context.CancelFunc, *retryLoopToken) {
	if s == nil {
		if cancel != nil {
			cancel()
		}
		return true, nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if *loopMap == nil {
		*loopMap = make(map[string]context.CancelFunc)
	}
	if *tokenMap == nil {
		*tokenMap = make(map[string]*retryLoopToken)
	}
	if current, ok := (*loopMap)[key]; ok && current != nil {
		return true, nil, nil
	}
	stale := make([]context.CancelFunc, 0, len(*loopMap))
	for otherKey, otherCancel := range *loopMap {
		if otherKey == key {
			continue
		}
		delete(*loopMap, otherKey)
		delete(*tokenMap, otherKey)
		if otherCancel != nil {
			stale = append(stale, otherCancel)
		}
	}
	token := &retryLoopToken{}
	(*loopMap)[key] = cancel
	(*tokenMap)[key] = token
	return false, stale, token
}

func (s *runtimeSession) finishRetryLoop(loopMap *map[string]context.CancelFunc, tokenMap *map[string]*retryLoopToken, key string, token *retryLoopToken) {
	if s == nil || loopMap == nil || tokenMap == nil || token == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if *loopMap == nil || *tokenMap == nil {
		return
	}
	if current, ok := (*tokenMap)[key]; ok && current == token {
		delete(*loopMap, key)
		delete(*tokenMap, key)
	}
}

func (s *runtimeSession) cancelRetryLoops() {
	if s == nil {
		return
	}
	s.cancelRetryLoopSet(&s.nominate, &s.nominateTokens)
	s.cancelRetryLoopSet(&s.accept, &s.acceptTokens)
}

func (s *runtimeSession) cancelRetryLoopSet(loopMap *map[string]context.CancelFunc, tokenMap *map[string]*retryLoopToken) {
	if s == nil || loopMap == nil || tokenMap == nil {
		return
	}
	s.mu.Lock()
	if *loopMap == nil || len(*loopMap) == 0 {
		s.mu.Unlock()
		return
	}
	cancels := make([]context.CancelFunc, 0, len(*loopMap))
	for key, cancel := range *loopMap {
		delete(*loopMap, key)
		delete(*tokenMap, key)
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func closeProbeRuntimeFamilies(families []probeRuntimeFamily) {
	for _, family := range families {
		if family.conn != nil {
			_ = family.conn.Close()
		}
	}
}

func failRuntimeWorkers(workers []*runtimeFamilyWorker) {
	for _, worker := range workers {
		if worker == nil || worker.rs == nil {
			continue
		}
		worker.rs.markHandshakeTimeout()
		if worker.cancel != nil {
			worker.cancel()
		}
		worker.rs.closeSockets(nil)
	}
}

func markRuntimeWorkersReady(workers []*runtimeFamilyWorker) {
	for _, worker := range workers {
		if worker == nil || worker.rs == nil {
			continue
		}
		worker.rs.markReadySent()
	}
}

func startRuntimeWorkers(workers []*runtimeFamilyWorker, punchCtx context.Context) {
	for _, worker := range workers {
		if worker == nil || worker.rs == nil {
			continue
		}
		worker.rs.markPunching()
		workerCtx, workerCancel := context.WithCancel(punchCtx)
		worker.cancel = workerCancel
		worker.rs.startReadLoop(workerCtx, worker.rs.localConn)
		go worker.rs.candidateMaintenanceLoop(workerCtx)
		go worker.rs.startSpray(workerCtx)
	}
}

func stopRuntimeWorkersForWinner(workers []*runtimeFamilyWorker, winner *runtimeFamilyWorker) {
	for _, worker := range workers {
		if worker == nil || worker.rs == nil {
			continue
		}
		if worker.cancel != nil {
			worker.cancel()
		}
		if winner == nil || worker != winner {
			worker.rs.closeSockets(nil)
		}
	}
}

func workerForConfirmedPair(workers []*runtimeFamilyWorker, pair confirmedPair) *runtimeFamilyWorker {
	for _, worker := range workers {
		if worker == nil || worker.rs == nil {
			continue
		}
		if pair.owner != nil && worker.rs == pair.owner {
			return worker
		}
		if pair.conn != nil && worker.rs.ownsSocket(pair.conn) {
			return worker
		}
	}
	return nil
}

type runtimeSessionResult struct {
	conn             net.PacketConn
	remoteAddr       string
	localAddr        string
	sessionID        string
	role             string
	mode             string
	transportData    string
	transportTimeout time.Duration
}

func resolveRuntimeTimeout(timeout time.Duration, fallback time.Duration) time.Duration {
	if timeout <= 0 {
		return fallback
	}
	return timeout
}

func primaryRuntimeWorkerLocalAddr(workers []*runtimeFamilyWorker) string {
	if len(workers) == 0 || workers[0] == nil || workers[0].rs == nil || workers[0].rs.localConn == nil {
		return ""
	}
	return addrString(workers[0].rs.localConn.LocalAddr())
}

func awaitPunchGo(ctx context.Context, control *conn.Conn, start P2PPunchStart, primaryLocalAddr string) error {
	goMsg, err := ReadBridgeJSONContext[P2PPunchGo](ctx, control, common.P2P_PUNCH_GO)
	if err != nil {
		return err
	}
	if err := validateRuntimePunchGo(start, goMsg); err != nil {
		return err
	}
	if goMsg.DelayMs > 0 {
		_ = trySendProgressWithStatus(control, start.SessionID, start.Role, "punch_go_wait", "ok", fmt.Sprintf("%dms", goMsg.DelayMs), primaryLocalAddr, "", nil, nil)
		if !sleepContext(ctx, time.Duration(goMsg.DelayMs)*time.Millisecond) {
			return mapP2PContextError(ctx.Err())
		}
	}
	_ = trySendProgressWithStatus(control, start.SessionID, start.Role, "punch_go", "ok", "", primaryLocalAddr, "", nil, nil)
	return nil
}

func awaitRuntimeConfirmation(ctx context.Context, workers []*runtimeFamilyWorker) (confirmedPair, bool) {
	if len(workers) == 0 || workers[0] == nil || workers[0].rs == nil || workers[0].rs.confirmed == nil {
		return confirmedPair{}, false
	}
	confirmedCh := workers[0].rs.confirmed
	if ctx == nil {
		pair, ok := <-confirmedCh
		return pair, ok
	}
	select {
	case pair, ok := <-confirmedCh:
		return pair, ok
	case <-ctx.Done():
		select {
		case pair, ok := <-confirmedCh:
			return pair, ok
		default:
			return confirmedPair{}, false
		}
	}
}

func finalizeRuntimeHandover(start P2PPunchStart, transportMode string, workers []*runtimeFamilyWorker, pair confirmedPair, transportTimeout time.Duration) (runtimeSessionResult, error) {
	winner := workerForConfirmedPair(workers, pair)
	stopRuntimeWorkersForWinner(workers, winner)
	if winner == nil {
		return runtimeSessionResult{}, fmt.Errorf("confirmed pair missing family worker")
	}
	if !winner.rs.stopSocketReadLoop(pair.conn, 300*time.Millisecond) {
		logs.Warn("[P2P] handover read loop did not stop promptly role=%s family=%s local=%s remote=%s",
			start.Role, winner.family.String(), pair.localAddr, pair.remoteAddr)
	}
	mode := negotiateTransportMode(transportMode, winner.summary.Peer.TransportMode)
	winner.rs.closeSockets(pair.conn)
	_ = winner.rs.sendProgress("candidate_confirmed", "ok", pair.remoteAddr, pair.localAddr, pair.remoteAddr, nil)
	_ = winner.rs.sendProgress("handover_begin", "ok", mode, pair.localAddr, pair.remoteAddr, map[string]string{
		"transport_mode": mode,
		"family":         winner.family.String(),
	})
	recordPredictionSuccess(winner.summary.Peer.Nat, pair.remoteAddr)
	recordAdaptiveProfileSuccess(winner.summary, mode)
	winner.rs.logTokenStats()
	winner.rs.markHandoverBegin()
	logs.Info("[P2P] handover begin role=%s family=%s confirmed pair=%s local=%s transport=%s", start.Role, winner.family.String(), pair.remoteAddr, pair.localAddr, mode)
	return runtimeSessionResult{
		conn:             pair.conn,
		remoteAddr:       pair.remoteAddr,
		localAddr:        pair.localAddr,
		sessionID:        start.SessionID,
		role:             start.Role,
		mode:             mode,
		transportData:    winner.summary.Peer.TransportData,
		transportTimeout: transportTimeout,
	}, nil
}

func failRuntimeHandshake(control *conn.Conn, start P2PPunchStart, workers []*runtimeFamilyWorker, err error) error {
	for _, worker := range workers {
		if worker == nil || worker.rs == nil {
			continue
		}
		worker.rs.markHandshakeTimeout()
		recordAdaptiveProfileTimeout(worker.summary)
		_ = worker.rs.sendProgress("handshake_timeout", "fail", err.Error(), addrString(worker.rs.localConn.LocalAddr()), worker.rs.cm.CandidateRemote(), nil)
		worker.rs.logTokenStats()
	}
	_ = trySendAbort(control, start.SessionID, start.Role, err.Error())
	return err
}

func negotiateTransportMode(selfMode, peerMode string) string {
	selfMode = strings.TrimSpace(selfMode)
	peerMode = strings.TrimSpace(peerMode)
	if selfMode == "" {
		selfMode = common.CONN_KCP
	}
	if peerMode == "" {
		peerMode = selfMode
	}
	if selfMode == peerMode {
		return selfMode
	}
	return common.CONN_KCP
}
