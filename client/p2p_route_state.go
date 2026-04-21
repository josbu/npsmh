package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
)

type p2pRouteBinding struct {
	localKey       string
	passwordMD5    string
	peerUUID       string
	associationID  string
	peerKey        string
	routeContext   p2p.P2PRouteContext
	fallbackSecret bool
}

type p2pAssociationState struct {
	association p2p.P2PAssociation
	phase       string
	peerKey     string
	routeRefs   map[string]*p2pRouteBinding
	dialing     bool
	readyCh     chan struct{}
	err         error
}

type p2pPeerState struct {
	key           string
	associationID string
	peer          p2p.P2PPeerRuntime
	transport     p2pTransportState
	statusOK      bool
	lastActive    time.Time
	mode          string
}

func buildLocalBindingKey(local *config.LocalServer) string {
	if local == nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%s|%s|%s|%t", strings.TrimSpace(local.Type), local.Port, strings.TrimSpace(local.Password), strings.TrimSpace(local.Target), strings.TrimSpace(local.TargetType), local.LocalProxy)
}

func buildPeerKey(peerUUID, transportMode string) string {
	peerUUID = strings.TrimSpace(peerUUID)
	transportMode = strings.TrimSpace(transportMode)
	if transportMode == "" {
		transportMode = P2PMode
	}
	return peerUUID + "|" + transportMode
}

func decodeJSONResponse[T any](c *conn.Conn) (T, error) {
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

var p2pSendResolveRequest = func(c *conn.Conn, req *p2p.P2PResolveRequest) error {
	if c == nil {
		return nil
	}
	_, err := c.SendInfo(req, "")
	return err
}

var p2pReadResolveResponse = func(c *conn.Conn) (p2p.P2PResolveResult, error) {
	return decodeJSONResponse[p2p.P2PResolveResult](c)
}

func buildLocalP2PRouteHint(local *config.LocalServer) p2p.P2PRouteContext {
	if local == nil {
		return p2p.P2PRouteContext{}
	}
	mode := strings.TrimSpace(local.Type)
	if mode == "" {
		mode = "p2p"
	}
	targetType := strings.TrimSpace(local.TargetType)
	if targetType == "" {
		targetType = common.CONN_ALL
	}
	proxyLike := mode == "p2ps" || mode == "p2pt"
	return p2p.P2PRouteContext{
		TunnelMode:   mode,
		TargetType:   targetType,
		AccessPolicy: p2p.BuildP2PAccessPolicy(strings.TrimSpace(local.Target), proxyLike),
	}
}

func (mgr *P2PManager) openBridgeWorkConn(ctx context.Context, workFlag string) (*conn.Conn, error) {
	bridgeConn, _, err := mgr.openBridgeWorkConnWithSecret(ctx, workFlag)
	return bridgeConn, err
}

func (mgr *P2PManager) openBridgeWorkConnWithSecret(ctx context.Context, workFlag string) (*conn.Conn, any, error) {
	controlConn, err := mgr.openP2PControlConnWithPolicy(ctx, mgr.cfg, true)
	if err != nil {
		return nil, nil, err
	}
	if err := p2pWorkSendType(controlConn.Conn, workFlag, controlConn.UUID); err != nil {
		mgr.discardSecretConnIfCurrent(controlConn.SecretConn, "bridge work send type failed")
		_ = controlConn.Conn.Close()
		return nil, nil, err
	}
	return controlConn.Conn, controlConn.SecretConn, nil
}

func (mgr *P2PManager) resolveRouteBinding(ctx context.Context, local *config.LocalServer) (*p2pRouteBinding, *p2pAssociationState, error) {
	ctx = mgr.normalizeP2PContext(ctx)
	if local == nil {
		return nil, nil, errors.New("local server missing")
	}
	passwordMD5 := crypt.Md5(local.Password)
	bridgeConn, secretConn, err := mgr.openBridgeWorkConnWithSecret(ctx, common.WORK_P2P_RESOLVE)
	if err != nil {
		return nil, nil, err
	}
	stopCloseOnCancel := closeConnOnContextDone(ctx, bridgeConn.Conn)
	defer func() { _ = bridgeConn.Close() }()
	defer stopCloseOnCancel()

	if err := p2pSendResolveRequest(bridgeConn, &p2p.P2PResolveRequest{
		PasswordMD5: passwordMD5,
		RouteHint:   buildLocalP2PRouteHint(local),
	}); err != nil {
		mgr.discardSecretConnIfCurrent(secretConn, "bridge resolve request failed")
		if ctx != nil && ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, err
	}
	resp, err := p2pReadResolveResponse(bridgeConn)
	if err != nil {
		mgr.discardSecretConnIfCurrent(secretConn, "bridge resolve response failed")
		if ctx != nil && ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, err
	}
	mgr.mu.Lock()
	binding, assoc, staleTransport, err := mgr.storeResolvedRouteBindingLocked(local, passwordMD5, resp)
	mgr.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	closeP2PTransport(staleTransport, "close stale peer transport after route rebind")
	return binding, assoc, nil
}

func (mgr *P2PManager) storeResolvedRouteBindingLocked(local *config.LocalServer, passwordMD5 string, resp p2p.P2PResolveResult) (*p2pRouteBinding, *p2pAssociationState, p2pTransportState, error) {
	var zero p2pTransportState

	peerUUID := strings.TrimSpace(resp.Association.Provider.UUID)
	if peerUUID == "" {
		return nil, nil, zero, errors.New("resolve missing provider uuid")
	}
	associationID := strings.TrimSpace(resp.Association.AssociationID)
	if associationID == "" {
		return nil, nil, zero, errors.New("resolve missing association id")
	}
	localKey := buildLocalBindingKey(local)
	staleTransport := mgr.detachLocalBindingLocked(localKey, associationID)

	assoc, ok := mgr.associations[associationID]
	if !ok || assoc == nil {
		assoc = &p2pAssociationState{
			association: resp.Association,
			phase:       strings.TrimSpace(resp.Phase),
			routeRefs:   make(map[string]*p2pRouteBinding),
		}
		mgr.associations[associationID] = assoc
	} else {
		assoc.association = resp.Association
		if strings.TrimSpace(resp.Phase) != "" {
			assoc.phase = strings.TrimSpace(resp.Phase)
		}
	}
	if assoc.phase == "" {
		assoc.phase = "binding"
	}
	if assoc.peerKey == "" {
		assoc.peerKey = buildPeerKey(peerUUID, P2PMode)
	}

	binding := &p2pRouteBinding{
		localKey:       localKey,
		passwordMD5:    passwordMD5,
		peerUUID:       peerUUID,
		associationID:  associationID,
		peerKey:        assoc.peerKey,
		routeContext:   resp.Route,
		fallbackSecret: local.Fallback,
	}
	mgr.localBindings[localKey] = binding
	mgr.peerIndex[peerUUID] = associationID
	assoc.routeRefs[localKey] = binding
	return binding, assoc, staleTransport, nil
}

func (mgr *P2PManager) detachLocalBindingLocked(localKey, keepAssociationID string) p2pTransportState {
	var zero p2pTransportState

	binding, ok := mgr.localBindings[localKey]
	if !ok || binding == nil {
		return zero
	}
	delete(mgr.localBindings, localKey)

	associationID := strings.TrimSpace(binding.associationID)
	if associationID == "" {
		return zero
	}
	assoc, ok := mgr.associations[associationID]
	if !ok || assoc == nil {
		if mgr.peerIndex[binding.peerUUID] == associationID {
			delete(mgr.peerIndex, binding.peerUUID)
		}
		return zero
	}

	delete(assoc.routeRefs, localKey)
	if associationID == strings.TrimSpace(keepAssociationID) || len(assoc.routeRefs) > 0 {
		return zero
	}

	delete(mgr.associations, associationID)
	if mgr.peerIndex[binding.peerUUID] == associationID {
		delete(mgr.peerIndex, binding.peerUUID)
	}

	stalePeerKey := strings.TrimSpace(assoc.peerKey)
	if stalePeerKey == "" {
		stalePeerKey = strings.TrimSpace(binding.peerKey)
	}
	if stalePeerKey == "" {
		return zero
	}

	peer, ok := mgr.peers[stalePeerKey]
	if !ok || peer == nil || peer.associationID != associationID {
		return zero
	}
	delete(mgr.peers, stalePeerKey)
	return peer.transport
}

func (mgr *P2PManager) getPeerStateLocked(peerKey string) *p2pPeerState {
	peer, ok := mgr.peers[peerKey]
	if !ok || peer == nil {
		return nil
	}
	return peer
}

func (peer *p2pPeerState) healthy() bool {
	if peer == nil || !peer.statusOK {
		return false
	}
	if quicConn := normalizeP2PQUICConn(peer.transport.quicConn); quicConn != nil {
		return quicConn.Context().Err() == nil
	}
	if muxSession := normalizeP2PMuxSession(peer.transport.muxSession); muxSession != nil {
		return !muxSession.IsClosed()
	}
	return false
}

func (mgr *P2PManager) waitOrEstablishPeer(ctx context.Context, local *config.LocalServer) (*p2pRouteBinding, *p2pPeerState, error) {
	ctx = mgr.normalizeP2PContext(ctx)
	binding, assoc, err := mgr.resolveRouteBinding(ctx, local)
	if err != nil {
		return nil, nil, err
	}
	for {
		mgr.mu.Lock()
		if assoc != nil && assoc.peerKey != "" {
			binding.peerKey = assoc.peerKey
		}
		peer := mgr.getPeerStateLocked(binding.peerKey)
		if peer != nil && peer.healthy() {
			mgr.mu.Unlock()
			return binding, peer, nil
		}
		if assoc != nil && assoc.dialing && assoc.readyCh != nil {
			readyCh := assoc.readyCh
			mgr.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-readyCh:
			}
			mgr.mu.Lock()
			peer = mgr.getPeerStateLocked(binding.peerKey)
			waitErr := error(nil)
			if assoc != nil {
				waitErr = assoc.err
			}
			mgr.mu.Unlock()
			if waitErr != nil {
				return nil, nil, waitErr
			}
			if peer != nil && peer.healthy() {
				return binding, peer, nil
			}
			continue
		}
		currentAssoc := assoc
		if currentAssoc == nil {
			mgr.mu.Unlock()
			return nil, nil, errors.New("association missing")
		}
		currentAssoc.dialing = true
		currentAssoc.phase = "punching"
		currentAssoc.err = nil
		currentAssoc.readyCh = make(chan struct{})
		mgr.mu.Unlock()

		peer, err = mgr.establishPeerTransport(ctx, local, binding, currentAssoc)

		mgr.mu.Lock()
		currentAssoc.dialing = false
		currentAssoc.err = err
		if err != nil {
			currentAssoc.phase = "failed"
		} else {
			currentAssoc.phase = "established"
		}
		if currentAssoc.readyCh != nil {
			close(currentAssoc.readyCh)
			currentAssoc.readyCh = nil
		}
		mgr.mu.Unlock()
		if err != nil {
			return nil, nil, err
		}
		return binding, peer, nil
	}
}

func (mgr *P2PManager) establishPeerTransport(ctx context.Context, local *config.LocalServer, binding *p2pRouteBinding, assoc *p2pAssociationState) (*p2pPeerState, error) {
	preferredLocalAddr := preferredP2PLocalAddr(mgr.cfg.LocalIP)
	session, err := mgr.openVisitorPunchSession(ctx, preferredLocalAddr, mgr.cfg, local, binding)
	if err != nil {
		return nil, err
	}
	defer session.close()

	udpTunnel, sess, err := mgr.establishVisitorTransport(ctx, session, mgr.cfg)
	if err != nil {
		return nil, err
	}

	state := p2pTransportState{}
	if session.mode == common.CONN_QUIC {
		state.quicConn = sess
		state.quicPacket = session.localConn
	} else {
		state.udpConn = udpTunnel
		state.muxSession = mux.NewMux(udpTunnel, "kcp", mgr.cfg.DisconnectTime, false)
	}

	actualPeerKey := buildPeerKey(binding.peerUUID, session.mode)
	var previous p2pTransportState

	mgr.mu.Lock()
	peer := mgr.peers[actualPeerKey]
	if peer == nil {
		peer = mgr.peers[binding.peerKey]
		if peer != nil && binding.peerKey != actualPeerKey {
			delete(mgr.peers, binding.peerKey)
		}
	}
	if peer == nil {
		peer = &p2pPeerState{}
	}
	previous = peer.transport
	peer.key = actualPeerKey
	peer.associationID = binding.associationID
	if assoc != nil {
		peer.peer = assoc.association.Provider
		assoc.peerKey = actualPeerKey
		for _, ref := range assoc.routeRefs {
			ref.peerKey = actualPeerKey
		}
	}
	peer.transport = state
	peer.statusOK = true
	peer.lastActive = time.Now()
	peer.mode = session.mode
	mgr.peers[actualPeerKey] = peer
	mgr.mu.Unlock()

	closeP2PTransport(previous, "replace peer transport")
	return peer, nil
}
