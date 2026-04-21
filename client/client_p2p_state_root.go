package client

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/p2p"
)

type clientP2PStateRoot struct {
	state        clientP2PStateStore
	associations clientP2PAssociationStore
}

type p2pAssociationRuntime struct {
	Association p2p.P2PAssociation
	PeerUUID    string
	Phase       string
	RouteRefs   map[int]p2p.P2PRouteContext
	RouteGrants map[int]p2p.P2PAccessPolicy
	UpdatedAt   time.Time
}

type p2pPeerPolicyRuntime struct {
	PeerUUID        string
	EffectivePolicy p2p.P2PAccessPolicy
	RouteGrants     map[int]p2p.P2PAccessPolicy
	UpdatedAt       time.Time
}

type clientP2PStateStore struct {
	withLock       func(*TRPClient, func(*clientP2PStateView))
	newAssociation func() *p2pAssociationRuntime
	newPeerPolicy  func(string) *p2pPeerPolicyRuntime
}

type clientP2PStateView struct {
	associations map[string]*p2pAssociationRuntime
	peerPolicies map[string]*p2pPeerPolicyRuntime
}

type clientP2PRuntimeStateHolder struct {
	mu           sync.Mutex
	associations map[string]*p2pAssociationRuntime
	peerPolicies map[string]*p2pPeerPolicyRuntime
}

var runtimeClientP2PStateRoot = newClientP2PStateRoot()

func newClientP2PStateRoot() *clientP2PStateRoot {
	ctx := &clientP2PStateRoot{}
	ctx.state = clientP2PStateStore{
		withLock: func(s *TRPClient, fn func(*clientP2PStateView)) {
			if s == nil || fn == nil {
				return
			}
			holder := s.ensureP2PStateHolder()
			if holder == nil {
				return
			}
			holder.mu.Lock()
			defer holder.mu.Unlock()
			fn(&clientP2PStateView{
				associations: holder.associations,
				peerPolicies: holder.peerPolicies,
			})
		},
		newAssociation: func() *p2pAssociationRuntime {
			return &p2pAssociationRuntime{
				RouteRefs:   make(map[int]p2p.P2PRouteContext),
				RouteGrants: make(map[int]p2p.P2PAccessPolicy),
			}
		},
		newPeerPolicy: func(peerUUID string) *p2pPeerPolicyRuntime {
			return &p2pPeerPolicyRuntime{
				PeerUUID:    peerUUID,
				RouteGrants: make(map[int]p2p.P2PAccessPolicy),
			}
		},
	}
	ctx.associations = clientP2PAssociationStore{
		now:   time.Now,
		state: ctx.state,
		associationPeerUUID: func(s *TRPClient, association p2p.P2PAssociation) string {
			if s == nil {
				return ""
			}
			return p2pPeerUUIDForAssociation(s.uuid, association)
		},
	}
	return ctx
}

func (c *clientP2PStateRoot) EnsureAssociation(s *TRPClient, associationID string) *p2pAssociationRuntime {
	if c == nil {
		return nil
	}
	var runtime *p2pAssociationRuntime
	if c.state.withLock != nil {
		c.state.withLock(s, func(state *clientP2PStateView) {
			runtime = state.EnsureAssociation(c.state, associationID)
		})
	}
	return runtime
}

func (c *clientP2PStateRoot) EnsurePeerPolicy(s *TRPClient, peerUUID string) *p2pPeerPolicyRuntime {
	if c == nil {
		return nil
	}
	var runtime *p2pPeerPolicyRuntime
	if c.state.withLock != nil {
		c.state.withLock(s, func(state *clientP2PStateView) {
			runtime = state.EnsurePeerPolicy(c.state, peerUUID)
		})
	}
	return runtime
}

func (c *clientP2PStateRoot) Association(s *TRPClient, associationID string) (*p2pAssociationRuntime, bool) {
	if c == nil {
		return nil, false
	}
	var (
		runtime *p2pAssociationRuntime
		ok      bool
	)
	if c.state.withLock != nil {
		c.state.withLock(s, func(state *clientP2PStateView) {
			runtime, ok = state.Association(associationID)
		})
	}
	return runtime, ok
}

func (c *clientP2PStateRoot) PeerPolicy(s *TRPClient, peerUUID string) (*p2pPeerPolicyRuntime, bool) {
	if c == nil {
		return nil, false
	}
	var (
		runtime *p2pPeerPolicyRuntime
		ok      bool
	)
	if c.state.withLock != nil {
		c.state.withLock(s, func(state *clientP2PStateView) {
			runtime, ok = state.PeerPolicy(peerUUID)
		})
	}
	return runtime, ok
}

func (c *clientP2PStateRoot) RecordBind(s *TRPClient, bind p2p.P2PAssociationBind) {
	if c == nil || !shouldRecordP2PAssociationBind(s, bind) {
		return
	}
	c.associations.RecordBind(s, bind)
}

func (c *clientP2PStateRoot) RecordPunchStart(s *TRPClient, start p2p.P2PPunchStart) {
	if c == nil || !shouldRecordP2PPunchStart(s, start) {
		return
	}
	c.associations.RecordPunchStart(s, start)
}

func (c *clientP2PStateRoot) AssociationPolicy(s *TRPClient, associationID string) (p2p.P2PAccessPolicy, bool) {
	if c == nil {
		return p2p.P2PAccessPolicy{}, false
	}
	return c.associations.Policy(s, associationID)
}

func newClientP2PRuntimeStateHolder() *clientP2PRuntimeStateHolder {
	return &clientP2PRuntimeStateHolder{
		associations: make(map[string]*p2pAssociationRuntime),
		peerPolicies: make(map[string]*p2pPeerPolicyRuntime),
	}
}

func p2pPeerUUIDForAssociation(localUUID string, association p2p.P2PAssociation) string {
	localUUID = strings.TrimSpace(localUUID)
	visitorUUID := strings.TrimSpace(association.Visitor.UUID)
	providerUUID := strings.TrimSpace(association.Provider.UUID)
	switch {
	case localUUID != "" && localUUID == providerUUID:
		return visitorUUID
	case localUUID != "" && localUUID == visitorUUID:
		return providerUUID
	case visitorUUID == "":
		return providerUUID
	case providerUUID == "":
		return visitorUUID
	default:
		// Bridge bind is currently pushed to provider main signal first.
		return visitorUUID
	}
}

func shouldRecordP2PAssociationBind(s *TRPClient, bind p2p.P2PAssociationBind) bool {
	associationID := strings.TrimSpace(bind.Association.AssociationID)
	if associationID == "" {
		return false
	}
	localUUID := ""
	if s != nil {
		localUUID = strings.TrimSpace(s.uuid)
	}
	if localUUID == "" {
		return true
	}
	providerUUID := strings.TrimSpace(bind.Association.Provider.UUID)
	if providerUUID == "" {
		return strings.TrimSpace(bind.Association.Visitor.UUID) != localUUID
	}
	return providerUUID == localUUID
}

func shouldRecordP2PPunchStart(s *TRPClient, start p2p.P2PPunchStart) bool {
	if strings.TrimSpace(start.AssociationID) == "" {
		return false
	}
	if strings.TrimSpace(start.Role) != common.WORK_P2P_PROVIDER {
		return false
	}
	localUUID := ""
	if s != nil {
		localUUID = strings.TrimSpace(s.uuid)
	}
	if localUUID == "" {
		return true
	}
	selfUUID := strings.TrimSpace(start.Self.UUID)
	if selfUUID != "" {
		return selfUUID == localUUID
	}
	return strings.TrimSpace(start.Peer.UUID) != localUUID
}

func hasP2PAccessGrant(policy p2p.P2PAccessPolicy) bool {
	return strings.TrimSpace(policy.Mode) != "" || len(policy.Targets) > 0 || strings.TrimSpace(policy.OpenReason) != ""
}

func mergeP2PAccessGrant(current, grant p2p.P2PAccessPolicy) p2p.P2PAccessPolicy {
	if !hasP2PAccessGrant(grant) {
		return p2p.NormalizeP2PAccessPolicy(current)
	}
	grant = p2p.NormalizeP2PAccessPolicy(grant)
	if !hasP2PAccessGrant(current) {
		return grant
	}
	return p2p.MergeP2PAccessPolicy(current, grant)
}

func applyP2PAssociationGrant(runtime *p2pAssociationRuntime, peerPolicy *p2pPeerPolicyRuntime, route p2p.P2PRouteContext, grant p2p.P2PAccessPolicy, updatedAt time.Time) {
	grant = p2p.NormalizeP2PAccessPolicy(grant)
	if runtime != nil {
		runtime.UpdatedAt = updatedAt
		if peerPolicy != nil {
			runtime.PeerUUID = peerPolicy.PeerUUID
		}
		if route.TunnelID > 0 {
			if runtime.RouteRefs == nil {
				runtime.RouteRefs = make(map[int]p2p.P2PRouteContext)
			}
			if runtime.RouteGrants == nil {
				runtime.RouteGrants = make(map[int]p2p.P2PAccessPolicy)
			}
			runtime.RouteRefs[route.TunnelID] = route
			runtime.RouteGrants[route.TunnelID] = grant
		}
	}
	if peerPolicy == nil {
		return
	}
	peerPolicy.UpdatedAt = updatedAt
	peerPolicy.EffectivePolicy = mergeP2PAccessGrant(peerPolicy.EffectivePolicy, grant)
	if route.TunnelID > 0 {
		if peerPolicy.RouteGrants == nil {
			peerPolicy.RouteGrants = make(map[int]p2p.P2PAccessPolicy)
		}
		peerPolicy.RouteGrants[route.TunnelID] = grant
	}
}

type p2pAssociationConn struct {
	net.Conn
	associationID string
}

func wrapP2PAssociationConn(c net.Conn, associationID string) net.Conn {
	if isNilClientNetConn(c) {
		return nil
	}
	associationID = strings.TrimSpace(associationID)
	if associationID == "" {
		return c
	}
	return &p2pAssociationConn{
		Conn:          c,
		associationID: associationID,
	}
}

func (c *p2pAssociationConn) P2PAssociationID() string {
	if c == nil {
		return ""
	}
	return c.associationID
}

func (c *p2pAssociationConn) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}

func p2pAssociationIDFromConn(c net.Conn) string {
	if isNilClientNetConn(c) {
		return ""
	}
	switch carrier := c.(type) {
	case interface{ P2PAssociationID() string }:
		return strings.TrimSpace(carrier.P2PAssociationID())
	case interface{ Unwrap() net.Conn }:
		return p2pAssociationIDFromConn(carrier.Unwrap())
	default:
		return ""
	}
}

func p2pAccessPolicyAllows(policy p2p.P2PAccessPolicy, target string) bool {
	policy = p2p.NormalizeP2PAccessPolicy(policy)
	if policy.Mode != p2p.P2PAccessModeWhitelist {
		return true
	}
	target = canonicalP2PAccessTarget(target)
	if target == "" {
		return false
	}
	for _, allowed := range policy.Targets {
		if target == canonicalP2PAccessTarget(allowed) {
			return true
		}
	}
	return false
}

func canonicalP2PAccessTarget(raw string) string {
	raw = strings.TrimSpace(common.ExtractHost(raw))
	if raw == "" {
		return ""
	}
	if common.IsPort(raw) {
		return net.JoinHostPort("127.0.0.1", raw)
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		if common.IsDomain(raw) {
			return strings.ToLower(raw)
		}
		return raw
	}
	if common.IsDomain(host) {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, port)
}

func (s *TRPClient) ensureP2PStateHolder() *clientP2PRuntimeStateHolder {
	if s == nil {
		return nil
	}
	if s.p2pState != nil {
		return s.p2pState
	}
	s.p2pStateOnce.Do(func() {
		if s.p2pState == nil {
			s.p2pState = newClientP2PRuntimeStateHolder()
		}
	})
	if s.p2pState == nil {
		s.p2pState = newClientP2PRuntimeStateHolder()
	}
	return s.p2pState
}

type clientP2PAssociationStore struct {
	now                 func() time.Time
	state               clientP2PStateStore
	associationPeerUUID func(*TRPClient, p2p.P2PAssociation) string
}

func (r clientP2PAssociationStore) RecordBind(s *TRPClient, bind p2p.P2PAssociationBind) {
	associationID := strings.TrimSpace(bind.Association.AssociationID)
	if s == nil || associationID == "" {
		return
	}
	now := r.now()
	if r.state.withLock != nil {
		r.state.withLock(s, func(state *clientP2PStateView) {
			runtime := state.EnsureAssociation(r.state, associationID)
			runtime.Association = bind.Association
			runtime.Phase = strings.TrimSpace(bind.Phase)
			peerUUID := r.associationPeerUUID(s, bind.Association)
			if peerUUID != "" {
				runtime.PeerUUID = peerUUID
			}
			applyP2PAssociationGrant(runtime, state.EnsurePeerPolicy(r.state, peerUUID), bind.Route, bind.AssociationPolicy, now)
			logs.Trace("p2p association bind association=%s peer=%s phase=%s tunnel=%d", associationID, bind.Association.Visitor.UUID, runtime.Phase, bind.Route.TunnelID)
		})
	}
}

func (r clientP2PAssociationStore) RecordPunchStart(s *TRPClient, start p2p.P2PPunchStart) {
	associationID := strings.TrimSpace(start.AssociationID)
	if s == nil || associationID == "" {
		return
	}
	now := r.now()
	if r.state.withLock != nil {
		r.state.withLock(s, func(state *clientP2PStateView) {
			runtime := state.EnsureAssociation(r.state, associationID)
			runtime.Association = p2p.P2PAssociation{AssociationID: associationID}
			if start.Role == common.WORK_P2P_PROVIDER {
				runtime.Association.Provider = start.Self
				runtime.Association.Visitor = start.Peer
			} else {
				runtime.Association.Visitor = start.Self
				runtime.Association.Provider = start.Peer
			}
			runtime.Phase = "punching"
			peerUUID := strings.TrimSpace(start.Peer.UUID)
			if peerUUID != "" {
				runtime.PeerUUID = peerUUID
			}
			applyP2PAssociationGrant(runtime, state.EnsurePeerPolicy(r.state, peerUUID), start.Route, start.AssociationPolicy, now)
			logs.Trace("p2p punch start association=%s self=%s peer=%s tunnel=%d", associationID, start.Self.UUID, start.Peer.UUID, start.Route.TunnelID)
		})
	}
}

func (r clientP2PAssociationStore) Policy(s *TRPClient, associationID string) (p2p.P2PAccessPolicy, bool) {
	associationID = strings.TrimSpace(associationID)
	if s == nil || associationID == "" {
		return p2p.P2PAccessPolicy{}, false
	}
	var (
		policy p2p.P2PAccessPolicy
		ok     bool
	)
	if r.state.withLock != nil {
		r.state.withLock(s, func(state *clientP2PStateView) {
			runtime, found := state.Association(associationID)
			if !found {
				return
			}
			peerUUID := strings.TrimSpace(runtime.PeerUUID)
			if peerUUID == "" {
				peerUUID = r.associationPeerUUID(s, runtime.Association)
			}
			if peerUUID == "" {
				return
			}
			peerPolicy, found := state.PeerPolicy(peerUUID)
			if !found || !hasP2PAccessGrant(peerPolicy.EffectivePolicy) {
				return
			}
			policy = p2p.NormalizeP2PAccessPolicy(peerPolicy.EffectivePolicy)
			ok = true
		})
	}
	return policy, ok
}

func (v *clientP2PStateView) EnsureAssociation(state clientP2PStateStore, associationID string) *p2pAssociationRuntime {
	associationID = strings.TrimSpace(associationID)
	if v == nil || associationID == "" {
		return nil
	}
	if v.associations == nil {
		v.associations = make(map[string]*p2pAssociationRuntime)
	}
	runtime, ok := v.associations[associationID]
	if ok && runtime != nil {
		return runtime
	}
	runtime = state.newAssociation()
	v.associations[associationID] = runtime
	return runtime
}

func (v *clientP2PStateView) EnsurePeerPolicy(state clientP2PStateStore, peerUUID string) *p2pPeerPolicyRuntime {
	peerUUID = strings.TrimSpace(peerUUID)
	if v == nil || peerUUID == "" {
		return nil
	}
	if v.peerPolicies == nil {
		v.peerPolicies = make(map[string]*p2pPeerPolicyRuntime)
	}
	runtime, ok := v.peerPolicies[peerUUID]
	if ok && runtime != nil {
		return runtime
	}
	runtime = state.newPeerPolicy(peerUUID)
	v.peerPolicies[peerUUID] = runtime
	return runtime
}

func (v *clientP2PStateView) Association(associationID string) (*p2pAssociationRuntime, bool) {
	associationID = strings.TrimSpace(associationID)
	if v == nil || associationID == "" {
		return nil, false
	}
	runtime, ok := v.associations[associationID]
	return runtime, ok && runtime != nil
}

func (v *clientP2PStateView) PeerPolicy(peerUUID string) (*p2pPeerPolicyRuntime, bool) {
	peerUUID = strings.TrimSpace(peerUUID)
	if v == nil || peerUUID == "" {
		return nil, false
	}
	runtime, ok := v.peerPolicies[peerUUID]
	return runtime, ok && runtime != nil
}
