package bridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/p2p"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
	"github.com/djylb/nps/server/p2pstate"
	"github.com/quic-go/quic-go"
)

var bridgeWriteP2PMessage = p2p.WriteBridgeMessage
var currentBridgeP2PRuntimeRoot = connection.CurrentP2PRuntime
var currentBridgeConfigRoot = servercfg.Current

const (
	p2pAssociationPhaseBinding     = "binding"
	p2pAssociationPhasePunching    = "punching"
	p2pAssociationPhaseEstablished = "established"
	p2pAssociationPhaseStale       = "stale"
	p2pAssociationPhaseFailed      = "failed"
)

const p2pAssociationPendingTTL = 30 * time.Second
const p2pAssociationSweepInterval = p2pAssociationPendingTTL

type p2pAssociationState struct {
	association p2p.P2PAssociation
	phase       string
	updatedAt   time.Time
}

type p2pAssociationManager struct {
	mu        sync.Mutex
	byID      map[string]*p2pAssociationState
	lastSweep time.Time
}

type bridgeP2PResolvedRoute struct {
	task        *file.Tunnel
	node        *Node
	signal      *conn.Conn
	association p2p.P2PAssociation
	accessGrant p2p.P2PAccessPolicy
	route       p2p.P2PRouteContext
	phase       string
	needPunch   bool
}

type bridgeP2PTaskSelection struct {
	task               *file.Tunnel
	selectedTask       *file.Tunnel
	hintedProviderUUID string
}

type bridgeP2PProviderRuntime struct {
	client *Client
	node   *Node
	signal *conn.Conn
}

type bridgeP2PAssociationDispatch struct {
	association p2p.P2PAssociation
	accessGrant p2p.P2PAccessPolicy
	route       p2p.P2PRouteContext
	phase       string
	needPunch   bool
}

type bridgeP2PPasswordRouteResolution struct {
	task           *file.Tunnel
	providerClient *Client
	node           *Node
	association    p2p.P2PAssociation
	accessGrant    p2p.P2PAccessPolicy
	route          p2p.P2PRouteContext
	phase          string
	needPunch      bool
}

func currentBridgeP2PRuntime() connection.P2PRuntimeConfig {
	if currentBridgeP2PRuntimeRoot != nil {
		return currentBridgeP2PRuntimeRoot()
	}
	return connection.CurrentP2PRuntime()
}

func currentBridgeConfig() *servercfg.Snapshot {
	return servercfg.ResolveProvider(currentBridgeConfigRoot)
}

func currentP2PRouteGrant(route p2p.P2PRouteContext) p2p.P2PAccessPolicy {
	return p2p.NormalizeP2PAccessPolicy(route.AccessPolicy)
}

func newP2PAssociationManager() *p2pAssociationManager {
	return &p2pAssociationManager{
		byID: make(map[string]*p2pAssociationState),
	}
}

func buildP2PAssociationID(visitor p2p.P2PPeerRuntime, provider p2p.P2PPeerRuntime) string {
	return crypt.GenerateUUID(
		"assoc",
		visitor.UUID,
		provider.UUID,
		strconv.Itoa(visitor.ClientID),
		strconv.Itoa(provider.ClientID),
	).String()
}

func normalizeP2PAssociationPhase(phase string) string {
	switch phase {
	case p2pAssociationPhaseBinding, p2pAssociationPhasePunching, p2pAssociationPhaseEstablished, p2pAssociationPhaseStale, p2pAssociationPhaseFailed:
		return phase
	default:
		return p2pAssociationPhaseBinding
	}
}

func (m *p2pAssociationManager) attach(visitor, provider p2p.P2PPeerRuntime, route p2p.P2PRouteContext) (p2p.P2PAssociation, p2p.P2PAccessPolicy, string, bool) {
	return m.attachAt(visitor, provider, route, time.Now())
}

func (m *p2pAssociationManager) attachAt(visitor, provider p2p.P2PPeerRuntime, route p2p.P2PRouteContext, now time.Time) (p2p.P2PAssociation, p2p.P2PAccessPolicy, string, bool) {
	grant := currentP2PRouteGrant(route)
	association := p2p.P2PAssociation{
		AssociationID: buildP2PAssociationID(visitor, provider),
		Visitor:       visitor,
		Provider:      provider,
	}
	if m == nil {
		return association, grant, p2pAssociationPhaseBinding, true
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepExpiredLocked(now)

	state, ok := m.byID[association.AssociationID]
	if !ok || state == nil {
		state = &p2pAssociationState{
			association: association,
			phase:       p2pAssociationPhaseBinding,
			updatedAt:   now,
		}
		m.byID[association.AssociationID] = state
		return association, grant, state.phase, true
	}

	state.phase = normalizePendingP2PAssociationPhase(state.phase, state.updatedAt, now)
	state.association = association
	needPunch := state.phase != p2pAssociationPhaseBinding && state.phase != p2pAssociationPhasePunching && state.phase != p2pAssociationPhaseEstablished
	if state.phase == p2pAssociationPhaseFailed || state.phase == p2pAssociationPhaseStale {
		state.phase = p2pAssociationPhaseBinding
		needPunch = true
	}
	state.updatedAt = now
	return state.association, grant, state.phase, needPunch
}

func normalizePendingP2PAssociationPhase(phase string, updatedAt time.Time, now time.Time) string {
	phase = normalizeP2PAssociationPhase(phase)
	if updatedAt.IsZero() {
		return phase
	}
	switch phase {
	case p2pAssociationPhaseBinding, p2pAssociationPhasePunching:
		if now.Sub(updatedAt) > p2pAssociationPendingTTL {
			return p2pAssociationPhaseStale
		}
	}
	return phase
}

func (m *p2pAssociationManager) markPunching(associationID string) {
	m.setPhase(associationID, p2pAssociationPhasePunching)
}

func (m *p2pAssociationManager) markEstablished(associationID string) {
	if m == nil || associationID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byID, associationID)
}

func (m *p2pAssociationManager) markFailed(associationID string) {
	if m == nil || associationID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.byID, associationID)
}

func (m *p2pAssociationManager) setPhase(associationID string, phase string) {
	if m == nil || associationID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.byID[associationID]
	if !ok || state == nil {
		return
	}
	state.phase = normalizeP2PAssociationPhase(phase)
	state.updatedAt = time.Now()
}

func (m *p2pAssociationManager) sweepExpiredLocked(now time.Time) {
	if m == nil || len(m.byID) == 0 {
		return
	}
	if !m.lastSweep.IsZero() && now.Sub(m.lastSweep) < p2pAssociationSweepInterval {
		return
	}
	for associationID, state := range m.byID {
		if shouldPruneP2PAssociationState(state, now) {
			delete(m.byID, associationID)
		}
	}
	m.lastSweep = now
}

func shouldPruneP2PAssociationState(state *p2pAssociationState, now time.Time) bool {
	if state == nil {
		return true
	}
	switch normalizeP2PAssociationPhase(state.phase) {
	case p2pAssociationPhaseEstablished, p2pAssociationPhaseFailed, p2pAssociationPhaseStale:
		return true
	case p2pAssociationPhaseBinding, p2pAssociationPhasePunching:
		if state.updatedAt.IsZero() {
			return false
		}
		return now.Sub(state.updatedAt) > p2pAssociationPendingTTL
	default:
		return true
	}
}

func decodeJSONPayload[T any](c *conn.Conn) (T, error) {
	var zero T
	if c == nil {
		return zero, errors.New("nil conn")
	}
	raw, err := c.GetShortLenContent()
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func buildProbeConfig(c *conn.Conn) p2p.P2PProbeConfig {
	p2pCfg := currentBridgeP2PRuntime()
	policy := buildProbePolicy(p2pCfg.Port)
	cfg := p2p.P2PProbeConfig{
		Version:          2,
		Provider:         p2p.ProbeProviderNPS,
		Mode:             p2p.ProbeModeUDP,
		Network:          p2p.ProbeNetworkUDP,
		ExpectExtraReply: policy.ProbeExtraReply,
		Policy:           policy,
	}
	cfg.Endpoints = buildBridgeP2PProbeEndpoints(collectProbeBaseHosts(c, p2pCfg), p2pCfg.Port)
	return cfg
}

func buildBridgeP2PProbeEndpoints(baseHosts []string, basePort int) []p2p.P2PProbeEndpoint {
	endpoints := make([]p2p.P2PProbeEndpoint, 0, len(baseHosts)*3)
	if basePort <= 0 {
		return endpoints
	}
	for hostIndex, host := range baseHosts {
		for i := 0; i < 3; i++ {
			endpoints = append(endpoints, p2p.P2PProbeEndpoint{
				ID:       fmt.Sprintf("probe-%d-%d", hostIndex+1, i+1),
				Provider: p2p.ProbeProviderNPS,
				Mode:     p2p.ProbeModeUDP,
				Network:  p2p.ProbeNetworkUDP,
				Address:  common.BuildAddress(host, strconv.Itoa(basePort+i)),
			})
		}
	}
	return endpoints
}

func collectProbeBaseHosts(c *conn.Conn, p2pCfg connection.P2PRuntimeConfig) []string {
	hosts := make([]string, 0, 3)
	seen := make(map[string]struct{}, 4)
	resolveBaseCtx := bridgeConnResolveContext(c)
	addHost := func(host string) {
		host = strings.TrimSpace(host)
		host = strings.Trim(host, "[]")
		if host == "" {
			return
		}
		if ip := net.ParseIP(host); ip != nil {
			ip = common.NormalizeIP(ip)
			if ip == nil || common.IsZeroIP(ip) || ip.IsUnspecified() {
				return
			}
			host = ip.String()
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	addResolvedHosts := func(host string) {
		addHost(host)
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" || net.ParseIP(host) != nil {
			return
		}
		resolveCtx, cancel := context.WithTimeout(resolveBaseCtx, 250*time.Millisecond)
		addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
		cancel()
		if err != nil {
			return
		}
		for _, addr := range addrs {
			addHost(addr.IP.String())
		}
	}

	if localAddr := bridgeConnLocalAddrString(c); localAddr != "" {
		addHost(common.GetIpByAddr(localAddr))
	}
	addResolvedHosts(strings.TrimSpace(p2pCfg.IP))
	addResolvedHosts(common.GetServerIp(p2pCfg.IP))
	return hosts
}

type bridgeConnContextProvider interface {
	Context() context.Context
}

type bridgeConnQUICSessionProvider interface {
	GetSession() *quic.Conn
}

func bridgeConnResolveContext(c *conn.Conn) context.Context {
	if sess := bridgeConnSession(c); sess != nil {
		if ctx := sess.Context(); ctx != nil {
			return ctx
		}
	}
	if ctx := bridgeConnContext(c); ctx != nil {
		return ctx
	}
	return context.Background()
}

func bridgeConnSession(c *conn.Conn) (sess *quic.Conn) {
	if c == nil || c.Conn == nil {
		return nil
	}
	sessionConn, ok := c.Conn.(bridgeConnQUICSessionProvider)
	if !ok {
		return nil
	}
	defer func() {
		if recover() != nil {
			sess = nil
		}
	}()
	return sessionConn.GetSession()
}

func bridgeConnContext(c *conn.Conn) (ctx context.Context) {
	if c == nil || c.Conn == nil {
		return nil
	}
	ctxConn, ok := c.Conn.(bridgeConnContextProvider)
	if !ok {
		return nil
	}
	defer func() {
		if recover() != nil {
			ctx = nil
		}
	}()
	return ctxConn.Context()
}

func bridgeConnLocalAddrString(c *conn.Conn) string {
	addr := bridgeConnLocalAddr(c)
	if addr == nil {
		return ""
	}
	return addr.String()
}

func bridgeConnLocalAddr(c *conn.Conn) (addr net.Addr) {
	if c == nil || c.Conn == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			addr = nil
		}
	}()
	return c.LocalAddr()
}

func mergeProbeObservation(sessionID string, self p2p.P2PPeerInfo) p2p.P2PPeerInfo {
	serverSamples := p2pstate.GetObservations(sessionID, self.Role)
	if len(serverSamples) == 0 && len(self.Families) == 0 {
		return self
	}
	families := p2p.NormalizePeerFamilyInfos(self)
	if len(families) == 0 {
		combinedSamples := p2p.MergeProbeSamples(serverSamples, self.Nat.Samples)
		mergedNat := p2p.BuildNatObservationWithEvidence(combinedSamples, self.Nat.FilteringTested && !self.Nat.ProbePortRestricted, self.Nat.FilteringTested)
		if self.Nat.MappingConfidenceLow {
			mergedNat.MappingConfidenceLow = true
		}
		mergedNat.ConflictingSignals = mergedNat.ConflictingSignals || self.Nat.ConflictingSignals
		if self.Nat.PortMapping != nil {
			mergedNat.PortMapping = self.Nat.PortMapping
		}
		self.Nat = mergedNat
		return self
	}

	mergedFamilies := make([]p2p.P2PFamilyInfo, 0, len(families))
	for _, family := range families {
		familySamples := p2p.FilterProbeSamplesByFamily(serverSamples, family.Family)
		combinedSamples := p2p.MergeProbeSamples(familySamples, family.Nat.Samples)
		mergedNat := p2p.BuildNatObservationWithEvidence(combinedSamples, family.Nat.FilteringTested && !family.Nat.ProbePortRestricted, family.Nat.FilteringTested)
		if family.Nat.MappingConfidenceLow {
			mergedNat.MappingConfidenceLow = true
		}
		mergedNat.ConflictingSignals = mergedNat.ConflictingSignals || family.Nat.ConflictingSignals
		if family.Nat.PortMapping != nil {
			mergedNat.PortMapping = family.Nat.PortMapping
		}
		family.Nat = mergedNat
		mergedFamilies = append(mergedFamilies, family)
	}
	return p2p.BuildPeerInfo(self.Role, self.TransportMode, self.TransportData, mergedFamilies)
}

func generateP2PSessionToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func buildSummaryHintsModel(self, peer p2p.P2PPeerInfo) *p2p.P2PSummaryHints {
	sharedFamilies := sharedPeerFamilies(self, peer)
	return &p2p.P2PSummaryHints{
		ProbePortRestricted:     self.Nat.ProbePortRestricted || peer.Nat.ProbePortRestricted,
		MappingConfidenceLow:    self.Nat.MappingConfidenceLow || peer.Nat.MappingConfidenceLow,
		FilteringLikely:         self.Nat.ProbePortRestricted || peer.Nat.ProbePortRestricted,
		SelfProbeIPCount:        self.Nat.ProbeIPCount,
		PeerProbeIPCount:        peer.Nat.ProbeIPCount,
		SelfProbeEndpointCount:  self.Nat.ProbeEndpointCount,
		PeerProbeEndpointCount:  peer.Nat.ProbeEndpointCount,
		SelfFilteringTested:     self.Nat.FilteringTested,
		PeerFilteringTested:     peer.Nat.FilteringTested,
		SelfConflictingSignals:  self.Nat.ConflictingSignals,
		PeerConflictingSignals:  peer.Nat.ConflictingSignals,
		SelfPortMappingMethod:   portMappingMethod(self.Nat.PortMapping),
		PeerPortMappingMethod:   portMappingMethod(peer.Nat.PortMapping),
		SelfProbeProviderCount:  probeProviderCount(self.Nat.Samples),
		PeerProbeProviderCount:  probeProviderCount(peer.Nat.Samples),
		SelfObservedBasePort:    self.Nat.ObservedBasePort,
		SelfObservedInterval:    self.Nat.ObservedInterval,
		SelfMappingBehavior:     self.Nat.MappingBehavior,
		SelfFilteringBehavior:   self.Nat.FilteringBehavior,
		SelfClassificationLevel: self.Nat.ClassificationLevel,
		PeerObservedBasePort:    peer.Nat.ObservedBasePort,
		PeerObservedInterval:    peer.Nat.ObservedInterval,
		PeerMappingBehavior:     peer.Nat.MappingBehavior,
		PeerFilteringBehavior:   peer.Nat.FilteringBehavior,
		PeerClassificationLevel: peer.Nat.ClassificationLevel,
		SelfFamilyCount:         len(p2p.NormalizePeerFamilyInfos(self)),
		PeerFamilyCount:         len(p2p.NormalizePeerFamilyInfos(peer)),
		SharedFamilyCount:       len(sharedFamilies),
		SharedFamilies:          sharedFamilies,
		DualStackParallel:       len(sharedFamilies) > 1,
		SelfFamilyDetails:       buildPeerFamilyHints(self),
		PeerFamilyDetails:       buildPeerFamilyHints(peer),
	}
}

func buildProbePolicy(basePort int) *p2p.P2PPolicy {
	policy := currentBridgeConfig().P2PProbePolicy(basePort)
	return &policy
}

func loadP2PTimeouts() p2p.P2PTimeouts {
	return currentBridgeConfig().P2PProbeTimeouts()
}

func sessionTTL(timeouts p2p.P2PTimeouts) time.Duration {
	ttl := time.Duration(timeouts.ProbeTimeoutMs+timeouts.HandshakeTimeoutMs+5000) * time.Millisecond
	if ttl < 30*time.Second {
		return 30 * time.Second
	}
	return ttl
}

func postSummaryTTL(timeouts p2p.P2PTimeouts) time.Duration {
	if timeouts.HandshakeTimeoutMs <= 0 || timeouts.TransportTimeoutMs <= 0 {
		return 20 * time.Second
	}
	ttl := time.Duration(timeouts.HandshakeTimeoutMs+timeouts.TransportTimeoutMs)*time.Millisecond + time.Second
	if ttl < 500*time.Millisecond {
		return 500 * time.Millisecond
	}
	return ttl
}

func punchGoDelayMs(timeouts p2p.P2PTimeouts, hints *p2p.P2PSummaryHints) int {
	delay := 120
	if hints != nil && hints.SharedFamilyCount > 1 {
		delay -= 15
	}
	if hints != nil && hints.MappingConfidenceLow {
		delay += 25
	}
	if hints != nil && hints.ProbePortRestricted {
		delay += 20
	}
	if hints == nil || !hints.SelfFilteringTested || !hints.PeerFilteringTested {
		delay += 15
	}
	if hints != nil && hints.SelfProbeEndpointCount > 1 && hints.PeerProbeEndpointCount > 1 {
		delay -= 10
	}
	if timeouts.HandshakeTimeoutMs > 0 {
		maxDelay := timeouts.HandshakeTimeoutMs / 5
		if maxDelay > 0 && delay > maxDelay {
			delay = maxDelay
		}
	}
	if delay < 40 {
		delay = 40
	}
	return delay
}

func portMappingMethod(info *p2p.PortMappingInfo) string {
	if info == nil {
		return ""
	}
	return info.Method
}

func probeProviderCount(samples []p2p.ProbeSample) int {
	if len(samples) == 0 {
		return 0
	}
	providers := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		provider := sample.Provider
		if provider == "" {
			provider = p2p.ProbeProviderNPS
		}
		providers[provider] = struct{}{}
	}
	return len(providers)
}

func buildPeerFamilyHints(peer p2p.P2PPeerInfo) map[string]p2p.P2PFamilyHintDetail {
	families := p2p.NormalizePeerFamilyInfos(peer)
	if len(families) == 0 {
		return nil
	}
	out := make(map[string]p2p.P2PFamilyHintDetail, len(families))
	for _, family := range families {
		out[family.Family] = p2p.P2PFamilyHintDetail{
			NATType:              family.Nat.NATType,
			MappingBehavior:      family.Nat.MappingBehavior,
			FilteringBehavior:    family.Nat.FilteringBehavior,
			ClassificationLevel:  family.Nat.ClassificationLevel,
			ProbeIPCount:         family.Nat.ProbeIPCount,
			ProbeEndpointCount:   family.Nat.ProbeEndpointCount,
			ProbeProviderCount:   probeProviderCount(family.Nat.Samples),
			ObservedBasePort:     family.Nat.ObservedBasePort,
			ObservedInterval:     family.Nat.ObservedInterval,
			MappingConfidenceLow: family.Nat.MappingConfidenceLow,
			FilteringTested:      family.Nat.FilteringTested,
			PortMappingMethod:    portMappingMethod(family.Nat.PortMapping),
			SampleCount:          len(family.Nat.Samples),
		}
	}
	return out
}

func sharedPeerFamilies(left, right p2p.P2PPeerInfo) []string {
	leftFamilies := p2p.NormalizePeerFamilyInfos(left)
	rightFamilies := p2p.NormalizePeerFamilyInfos(right)
	if len(leftFamilies) == 0 || len(rightFamilies) == 0 {
		return nil
	}
	rightSet := make(map[string]struct{}, len(rightFamilies))
	for _, family := range rightFamilies {
		if family.Family != "" {
			rightSet[family.Family] = struct{}{}
		}
	}
	shared := make([]string, 0, len(leftFamilies))
	for _, family := range leftFamilies {
		if family.Family == "" {
			continue
		}
		if _, ok := rightSet[family.Family]; ok {
			shared = append(shared, family.Family)
		}
	}
	sort.Strings(shared)
	return shared
}

func encodeJSONPayload(c *conn.Conn, payload any) error {
	if c == nil {
		return errors.New("nil conn")
	}
	_, err := c.SendInfo(payload, "")
	return err
}

func buildP2PPeerRuntime(clientID int, node *Node) p2p.P2PPeerRuntime {
	runtime := p2p.P2PPeerRuntime{ClientID: clientID}
	if node == nil {
		return runtime
	}
	runtime.UUID = node.UUID
	runtime.BaseVer = node.BaseVer
	return runtime
}

func lookupBridgeP2PTaskByPassword(passwordMD5 string) (*file.Tunnel, error) {
	passwordMD5 = strings.TrimSpace(passwordMD5)
	task := currentBridgeDB().GetTaskByMd5Password(passwordMD5)
	if task == nil {
		task = currentBridgeDB().GetTaskByMd5PasswordOld(passwordMD5)
	}
	if task == nil {
		return nil, errors.New("p2p task not found")
	}
	if task.Mode != "p2p" {
		return nil, fmt.Errorf("p2p is not supported in %s mode", task.Mode)
	}
	return task, nil
}

func buildP2PRouteContext(task *file.Tunnel, routeHint p2p.P2PRouteContext) p2p.P2PRouteContext {
	if task == nil {
		return routeHint
	}
	tunnelMode := strings.TrimSpace(routeHint.TunnelMode)
	if tunnelMode == "" {
		tunnelMode = strings.TrimSpace(task.Mode)
	}
	targetType := strings.TrimSpace(routeHint.TargetType)
	if targetType == "" {
		targetType = strings.TrimSpace(task.TargetType)
	}
	targetRaw := ""
	proxyLike := task.Mode == "mixProxy" || task.HttpProxy || task.Socks5Proxy
	if task.Target != nil {
		targetRaw = strings.TrimSpace(task.Target.TargetStr)
	}
	accessPolicy := routeHint.AccessPolicy
	if accessPolicy.Mode == "" && len(accessPolicy.Targets) == 0 && accessPolicy.OpenReason == "" {
		accessPolicy = p2p.BuildP2PAccessPolicy(targetRaw, proxyLike)
	}
	return p2p.P2PRouteContext{
		TunnelID:       task.Id,
		TunnelMode:     tunnelMode,
		TargetType:     targetType,
		DestAclMode:    task.DestAclMode,
		DestAclRules:   strings.TrimSpace(task.DestAclRules),
		AccessPolicy:   p2p.NormalizeP2PAccessPolicy(accessPolicy),
		PolicyRevision: task.Revision,
	}
}

func (s *Bridge) selectP2PProviderNode(task *file.Tunnel, client *Client, hintedUUID string) (*Node, *conn.Conn, error) {
	if client == nil {
		return nil, nil, errors.New("provider client missing")
	}
	hintedUUID = strings.TrimSpace(hintedUUID)
	if hintedUUID == "" {
		node := client.GetNode()
		if node == nil {
			return nil, nil, errors.New("provider node unavailable")
		}
		signal := s.currentBridgeP2PProviderSignal(task, node)
		if signal == nil {
			return node, nil, errors.New("provider signal unavailable")
		}
		return node, signal, nil
	}
	node, ok := client.GetNodeByUUID(hintedUUID)
	if !ok || node == nil {
		return nil, nil, fmt.Errorf("provider node %s unavailable", hintedUUID)
	}
	signal := s.currentBridgeP2PProviderSignal(task, node)
	if signal == nil {
		return node, nil, fmt.Errorf("provider node %s signal unavailable", hintedUUID)
	}
	return node, signal, nil
}

func selectBridgeP2PTaskRoute(task *file.Tunnel, hintedProviderUUID string) *file.Tunnel {
	if task == nil {
		return nil
	}
	hintedProviderUUID = strings.TrimSpace(hintedProviderUUID)
	if hintedProviderUUID != "" {
		return task.SelectRuntimeRouteByUUID(hintedProviderUUID)
	}
	return task.SelectRuntimeRoute()
}

func resolveBridgeP2PVisitorRuntime(visitorID int, visitorUUID string, visitorBaseVer int) p2p.P2PPeerRuntime {
	return p2p.P2PPeerRuntime{
		ClientID: visitorID,
		UUID:     strings.TrimSpace(visitorUUID),
		BaseVer:  visitorBaseVer,
	}
}

func resolveBridgeP2PTaskSelection(passwordMD5 string, hintedProviderUUID string) (bridgeP2PTaskSelection, error) {
	task, err := lookupBridgeP2PTaskByPassword(passwordMD5)
	if err != nil {
		return bridgeP2PTaskSelection{}, err
	}
	return bridgeP2PTaskSelection{
		task:               task,
		selectedTask:       selectBridgeP2PTaskRoute(task, hintedProviderUUID),
		hintedProviderUUID: strings.TrimSpace(hintedProviderUUID),
	}, nil
}

func (s *Bridge) resolveBridgeP2PProviderRuntime(selection bridgeP2PTaskSelection) (bridgeP2PProviderRuntime, error) {
	if selection.selectedTask == nil || selection.selectedTask.Client == nil {
		return bridgeP2PProviderRuntime{}, errors.New("provider client missing")
	}
	providerClient, ok := s.loadRuntimeClient(selection.selectedTask.Client.Id)
	if !ok {
		return bridgeP2PProviderRuntime{}, errors.New("provider client offline")
	}
	selectedUUID := selection.hintedProviderUUID
	if selectedUUID == "" {
		selectedUUID = selection.selectedTask.RuntimeRouteUUID()
	}
	providerNode, providerSignal, err := s.selectP2PProviderNode(selection.selectedTask, providerClient, selectedUUID)
	if err != nil {
		return bridgeP2PProviderRuntime{client: providerClient, node: providerNode}, err
	}
	return bridgeP2PProviderRuntime{
		client: providerClient,
		node:   providerNode,
		signal: providerSignal,
	}, nil
}

func (s *Bridge) resolveBridgeP2PProvider(task *file.Tunnel, hintedUUID string) (*Client, *Node, *conn.Conn, error) {
	selection := bridgeP2PTaskSelection{
		task:               task,
		selectedTask:       selectBridgeP2PTaskRoute(task, hintedUUID),
		hintedProviderUUID: strings.TrimSpace(hintedUUID),
	}
	runtime, err := s.resolveBridgeP2PProviderRuntime(selection)
	if err != nil {
		return runtime.client, runtime.node, runtime.signal, err
	}
	return runtime.client, runtime.node, runtime.signal, nil
}

func (s *Bridge) buildBridgeP2PAssociationDispatch(visitor p2p.P2PPeerRuntime, selection bridgeP2PTaskSelection, runtime bridgeP2PProviderRuntime, routeHint p2p.P2PRouteContext) bridgeP2PAssociationDispatch {
	providerRuntime := buildP2PPeerRuntime(selection.selectedTask.Client.Id, runtime.node)
	route := buildP2PRouteContext(selection.selectedTask, routeHint)
	association, accessGrant, phase, needPunch := s.p2pAssociations.attach(visitor, providerRuntime, route)
	return bridgeP2PAssociationDispatch{
		association: association,
		accessGrant: accessGrant,
		route:       route,
		phase:       phase,
		needPunch:   needPunch,
	}
}

func (s *Bridge) resolveP2PRouteByPasswordResult(visitorID int, visitorUUID string, visitorBaseVer int, passwordMD5 string, hintedProviderUUID string, routeHint p2p.P2PRouteContext) (bridgeP2PPasswordRouteResolution, error) {
	selection, err := resolveBridgeP2PTaskSelection(passwordMD5, hintedProviderUUID)
	if err != nil {
		return bridgeP2PPasswordRouteResolution{}, err
	}
	runtime, err := s.resolveBridgeP2PProviderRuntime(selection)
	if err != nil {
		return bridgeP2PPasswordRouteResolution{
			task:           selection.selectedTask,
			providerClient: runtime.client,
		}, err
	}
	visitorRuntime := resolveBridgeP2PVisitorRuntime(visitorID, visitorUUID, visitorBaseVer)
	dispatch := s.buildBridgeP2PAssociationDispatch(visitorRuntime, selection, runtime, routeHint)
	return bridgeP2PPasswordRouteResolution{
		task:           selection.selectedTask,
		providerClient: runtime.client,
		node:           runtime.node,
		association:    dispatch.association,
		accessGrant:    dispatch.accessGrant,
		route:          dispatch.route,
		phase:          dispatch.phase,
		needPunch:      dispatch.needPunch,
	}, nil
}

func (s *Bridge) resolveP2PRouteByPassword(visitorID int, visitorUUID string, visitorBaseVer int, passwordMD5 string, hintedProviderUUID string, routeHint p2p.P2PRouteContext) (*file.Tunnel, *Client, *Node, p2p.P2PAssociation, p2p.P2PAccessPolicy, p2p.P2PRouteContext, string, bool, error) {
	result, err := s.resolveP2PRouteByPasswordResult(visitorID, visitorUUID, visitorBaseVer, passwordMD5, hintedProviderUUID, routeHint)
	if err != nil {
		return result.task, result.providerClient, result.node, p2p.P2PAssociation{}, p2p.P2PAccessPolicy{}, p2p.P2PRouteContext{}, "", false, err
	}
	return result.task, result.providerClient, result.node, result.association, result.accessGrant, result.route, result.phase, result.needPunch, nil
}

func (s *Bridge) currentBridgeP2PProviderSignal(task *file.Tunnel, node *Node) *conn.Conn {
	if node == nil {
		return nil
	}
	signal := node.GetSignal()
	if signal != nil && !signal.IsClosed() {
		return signal
	}
	if s != nil {
		s.pruneBridgeP2PProviderNode(task, node)
	}
	return nil
}

func (s *Bridge) pruneBridgeP2PProviderNode(task *file.Tunnel, node *Node) {
	if s == nil || node == nil {
		return
	}
	if !node.CloseIfOffline() {
		return
	}
	if client := node.Client; client != nil {
		if client.closeAndRemoveNodeIfCurrent(strings.TrimSpace(node.UUID), node) {
			clientID := 0
			if client != nil {
				clientID = client.Id
			}
			if clientID <= 0 && task != nil && task.Client != nil {
				clientID = task.Client.Id
			}
			s.removeEmptyRuntimeClient(clientID, client)
			return
		}
	}
	if task == nil || task.Client == nil || task.Client.Id <= 0 {
		return
	}
	client, ok := s.loadRuntimeClient(task.Client.Id)
	if !ok || client == nil {
		return
	}
	if client.closeAndRemoveNodeIfCurrent(strings.TrimSpace(node.UUID), node) {
		s.removeEmptyRuntimeClient(task.Client.Id, client)
	}
}

func (s *Bridge) resolveBridgeP2PRoute(id int, uuid string, ver int, passwordMD5, providerUUID string, routeHint p2p.P2PRouteContext) (bridgeP2PResolvedRoute, error) {
	var zero bridgeP2PResolvedRoute
	result, err := s.resolveP2PRouteByPasswordResult(id, uuid, ver, passwordMD5, providerUUID, routeHint)
	if err != nil {
		return zero, err
	}
	signal := s.currentBridgeP2PProviderSignal(result.task, result.node)
	if signal == nil {
		return zero, fmt.Errorf("provider signal unavailable")
	}
	return bridgeP2PResolvedRoute{
		task:        result.task,
		node:        result.node,
		signal:      signal,
		association: result.association,
		accessGrant: result.accessGrant,
		route:       result.route,
		phase:       result.phase,
		needPunch:   result.needPunch,
	}, nil
}

func buildP2PAssociationBind(association p2p.P2PAssociation, accessGrant p2p.P2PAccessPolicy, route p2p.P2PRouteContext, phase string) p2p.P2PAssociationBind {
	return p2p.P2PAssociationBind{
		Association:       association,
		AssociationPolicy: accessGrant,
		Route:             route,
		Phase:             phase,
	}
}

func sendBridgeMessageToNodeSignal(node *Node, signal *conn.Conn, flag string, payload any) error {
	if signal == nil {
		return errors.New("provider signal missing")
	}
	if err := bridgeWriteP2PMessage(signal, flag, payload); err != nil {
		if node == nil {
			return err
		}
		current := node.GetSignal()
		if current == nil || current == signal || current.IsClosed() {
			return err
		}
		return bridgeWriteP2PMessage(current, flag, payload)
	}
	return nil
}

func (s *Bridge) sendP2PAssociationBind(node *Node, signal *conn.Conn, bind p2p.P2PAssociationBind) error {
	return sendBridgeMessageToNodeSignal(node, signal, common.P2P_ASSOCIATION_BIND, bind)
}
