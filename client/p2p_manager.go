package client

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
	"github.com/djylb/nps/server/proxy"
	"github.com/quic-go/quic-go"
)

type P2PManager struct {
	ctx           context.Context
	cancel        context.CancelFunc
	pCancel       context.CancelFunc
	mu            sync.Mutex
	wg            sync.WaitGroup
	watchStopOnce sync.Once
	cfg           *config.CommonConfig
	monitor       bool
	associations  map[string]*p2pAssociationState
	peerIndex     map[string]string
	peers         map[string]*p2pPeerState
	localBindings map[string]*p2pRouteBinding
	udpConn       net.Conn
	muxSession    *mux.Mux
	quicConn      *quic.Conn
	quicPacket    net.PacketConn
	uuid          string
	secretConn    any
	statusOK      bool
	statusCh      chan struct{}
	proxyServers  []Closer
	lastActive    time.Time
	watchStop     chan struct{}
}

type p2pTransportState struct {
	udpConn    net.Conn
	muxSession *mux.Mux
	quicConn   *quic.Conn
	quicPacket net.PacketConn
}

type p2pControlDialConfig struct {
	Tp       string
	VKey     string
	Server   string
	ProxyURL string
	LocalIP  string
}

type p2pControlConn struct {
	Conn       *conn.Conn
	SecretConn any
	UUID       string
}

type p2pMonitorState struct {
	notReadyRetry      int
	natHardFailCount   int
	natHardBackoff     int
	nextHardNATRetryAt time.Time
}

type p2pMonitorRoundResult struct {
	hardNATHits int
	softRetries int
	established bool
}

var startP2PParentCancelWatch = func(done <-chan struct{}, stop <-chan struct{}, closeFn func()) {
	go func() {
		select {
		case <-done:
			closeFn()
		case <-stop:
		}
	}()
}

func NewP2PManager(pCtx context.Context, pCancel context.CancelFunc, cfg *config.CommonConfig) *P2PManager {
	if pCtx == nil {
		pCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(pCtx)
	mgr := &P2PManager{
		ctx:           ctx,
		cancel:        cancel,
		pCancel:       pCancel,
		cfg:           cfg,
		monitor:       false,
		associations:  make(map[string]*p2pAssociationState),
		peerIndex:     make(map[string]string),
		peers:         make(map[string]*p2pPeerState),
		localBindings: make(map[string]*p2pRouteBinding),
		statusCh:      make(chan struct{}, 1),
		proxyServers:  make([]Closer, 0),
		watchStop:     make(chan struct{}),
	}
	if done := pCtx.Done(); done != nil {
		startP2PParentCancelWatch(done, mgr.watchStop, mgr.Close)
	}
	return mgr
}

func (mgr *P2PManager) resetStatus(ok bool) {
	mgr.mu.Lock()
	oldStatus := mgr.statusOK
	mgr.statusOK = ok
	mgr.mu.Unlock()
	if !ok && oldStatus {
		select {
		case mgr.statusCh <- struct{}{}:
		default:
		}
	}
}

func (mgr *P2PManager) Close() {
	if mgr == nil {
		return
	}
	mgr.watchStopOnce.Do(func() {
		if mgr.watchStop != nil {
			close(mgr.watchStop)
		}
	})
	mgr.cancel()
	mgr.mu.Lock()
	psList := mgr.proxyServers
	mgr.proxyServers = nil
	peerStates := make([]p2pTransportState, 0, len(mgr.peers))
	for _, peer := range mgr.peers {
		if peer == nil {
			continue
		}
		peerStates = append(peerStates, peer.transport)
	}
	mgr.peers = make(map[string]*p2pPeerState)
	mgr.peerIndex = make(map[string]string)
	mgr.associations = make(map[string]*p2pAssociationState)
	mgr.localBindings = make(map[string]*p2pRouteBinding)
	mgr.mu.Unlock()
	secretConn := mgr.clearSecretConn()
	transport := mgr.clearActiveTransport()

	for _, srv := range psList {
		_ = srv.Close()
	}
	closeP2PTransport(transport, "close quic")
	for _, state := range peerStates {
		closeP2PTransport(state, "close peer transport")
	}
	closeSecretTunnel(secretConn, "p2p close")
	mgr.wg.Wait()
}

func (mgr *P2PManager) startManagedService(srv proxy.Service) {
	if mgr == nil || srv == nil {
		return
	}
	mgr.mu.Lock()
	mgr.proxyServers = append(mgr.proxyServers, srv)
	mgr.mu.Unlock()
	mgr.wg.Add(1)
	go func() {
		defer mgr.wg.Done()
		defer mgr.removeManagedService(srv)
		if err := srv.Start(); err != nil {
			logs.Error("managed local service stopped with error: %v", err)
			if closeErr := srv.Close(); closeErr != nil {
				logs.Warn("managed local service cleanup failed: %v", closeErr)
			}
		}
	}()
}

func (mgr *P2PManager) removeManagedService(srv proxy.Service) {
	if mgr == nil || srv == nil {
		return
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for i, current := range mgr.proxyServers {
		if current != srv {
			continue
		}
		copy(mgr.proxyServers[i:], mgr.proxyServers[i+1:])
		mgr.proxyServers = mgr.proxyServers[:len(mgr.proxyServers)-1]
		return
	}
}

func (mgr *P2PManager) snapshotTransportStateLocked() p2pTransportState {
	return p2pTransportState{
		udpConn:    mgr.udpConn,
		muxSession: mgr.muxSession,
		quicConn:   mgr.quicConn,
		quicPacket: mgr.quicPacket,
	}
}

func (mgr *P2PManager) clearActiveTransport() p2pTransportState {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	state := mgr.snapshotTransportStateLocked()
	mgr.udpConn = nil
	mgr.muxSession = nil
	mgr.quicConn = nil
	mgr.quicPacket = nil
	mgr.statusOK = false
	return state
}

func (mgr *P2PManager) installActiveTransport(mode string, udpTunnel net.Conn, quicConn *quic.Conn, quicPacket net.PacketConn, disconnectTime int) p2pTransportState {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	previous := mgr.snapshotTransportStateLocked()
	if mode == common.CONN_QUIC {
		mgr.quicConn = quicConn
		mgr.quicPacket = quicPacket
		mgr.udpConn = nil
		mgr.muxSession = nil
	} else {
		mgr.udpConn = udpTunnel
		mgr.muxSession = mux.NewMux(udpTunnel, "kcp", disconnectTime, false)
		mgr.quicConn = nil
		mgr.quicPacket = nil
	}
	mgr.lastActive = time.Now()
	mgr.statusOK = true
	return previous
}

func closeP2PTransport(state p2pTransportState, reason string) {
	if muxSession := normalizeP2PMuxSession(state.muxSession); muxSession != nil {
		_ = muxSession.Close()
	}
	if state.udpConn != nil {
		_ = state.udpConn.Close()
	}
	if quicConn := normalizeP2PQUICConn(state.quicConn); quicConn != nil {
		if quicConn.Context().Err() != nil {
			logs.Debug("quic connection context error: %v", quicConn.Context().Err())
		}
		_ = quicConn.CloseWithError(0, reason)
	}
	if state.quicPacket != nil {
		_ = state.quicPacket.Close()
	}
}

func normalizeP2PMuxSession(session *mux.Mux) *mux.Mux {
	if session == nil || session.CloseChan() == nil {
		return nil
	}
	return session
}

func normalizeP2PQUICConn(session *quic.Conn) *quic.Conn {
	if session == nil || session.Context() == nil {
		return nil
	}
	return session
}

func (mgr *P2PManager) normalizeP2PContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if mgr != nil && mgr.ctx != nil {
		return mgr.ctx
	}
	return context.Background()
}

func (mgr *P2PManager) currentP2PUUID() string {
	if mgr == nil {
		return ""
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.uuid
}

func (mgr *P2PManager) storeOrKeepP2PUUID(uuid string) string {
	if mgr == nil {
		return uuid
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.uuid == "" {
		mgr.uuid = uuid
		return uuid
	}
	return mgr.uuid
}

func (mgr *P2PManager) resolveP2PControlDialConfig(cfg *config.CommonConfig) p2pControlDialConfig {
	if cfg == nil && mgr != nil {
		cfg = mgr.cfg
	}
	if cfg == nil {
		return p2pControlDialConfig{}
	}
	return p2pControlDialConfig{
		Tp:       cfg.Tp,
		VKey:     cfg.VKey,
		Server:   cfg.Server,
		ProxyURL: cfg.ProxyUrl,
		LocalIP:  cfg.LocalIP,
	}
}

func (mgr *P2PManager) openP2PControlConn(ctx context.Context, cfg *config.CommonConfig) (*p2pControlConn, error) {
	return mgr.openP2PControlConnWithPolicy(ctx, cfg, false)
}

func (mgr *P2PManager) openP2PControlConnWithPolicy(ctx context.Context, cfg *config.CommonConfig, requireUUID bool) (*p2pControlConn, error) {
	ctx = mgr.normalizeP2PContext(ctx)

	rawConn, secretConn, err := mgr.openReusableSecretConn(ctx)
	if err != nil {
		return nil, err
	}
	uuid := mgr.currentP2PUUID()
	if rawConn != nil && requireUUID && uuid == "" {
		_ = rawConn.Close()
		rawConn = nil
	}
	if rawConn != nil {
		return &p2pControlConn{
			Conn:       conn.NewConn(rawConn),
			SecretConn: secretConn,
			UUID:       uuid,
		}, nil
	}

	dialCfg := mgr.resolveP2PControlDialConfig(cfg)
	controlConn, uuid, err := p2pClientNewConnContext(ctx, dialCfg.Tp, dialCfg.VKey, dialCfg.Server, dialCfg.ProxyURL, dialCfg.LocalIP, false)
	if err != nil {
		return nil, err
	}
	return &p2pControlConn{
		Conn: controlConn,
		UUID: mgr.storeOrKeepP2PUUID(uuid),
	}, nil
}

func (state *p2pMonitorState) consumeRoundResult(result p2pMonitorRoundResult, maxRetry int) bool {
	if result.established {
		state.notReadyRetry = 0
	}
	state.notReadyRetry += result.softRetries
	if result.hardNATHits == 0 {
		state.natHardFailCount = 0
		state.natHardBackoff = 0
		state.nextHardNATRetryAt = time.Time{}
	}
	if result.established {
		state.notReadyRetry = 0
		return false
	}
	if result.hardNATHits > 0 {
		state.natHardFailCount += result.hardNATHits
	}
	if state.natHardFailCount >= 6 {
		state.natHardBackoff++
		delay := p2pHardNATRetryDelay(state.natHardBackoff)
		state.nextHardNATRetryAt = time.Now().Add(delay)
		state.natHardFailCount = 0
		state.notReadyRetry = 0
		logs.Warn("P2P hard-NAT handshake failed repeatedly, back off punching for %s and keep relay/secret available.", delay)
		return false
	}
	if state.notReadyRetry >= maxRetry {
		return true
	}
	logs.Warn("P2P not established yet (retry %d/%d).", state.notReadyRetry, maxRetry)
	return false
}

func p2pHardNATRetryDelay(level int) time.Duration {
	if level < 1 {
		level = 1
	}
	delay := 15 * time.Second
	for i := 1; i < level; i++ {
		delay *= 2
		if delay >= 2*time.Minute {
			return 2 * time.Minute
		}
	}
	return delay
}

func (mgr *P2PManager) StartLocalServer(l *config.LocalServer) error {
	if mgr.ctx.Err() != nil {
		return errors.New("parent context canceled")
	}
	pb := NewP2pBridge(mgr, l)
	if pb.p2p && l != nil {
		if _, _, err := mgr.resolveRouteBinding(mgr.ctx, l); err != nil {
			logs.Warn("initial p2p resolve failed for local port %d: %v", l.Port, err)
		}
	}

	task := mgr.newLocalTunnelTask(l)

	switch l.Type {
	case "p2ps":
		logs.Info("start http/socks5 monitor port %d", l.Port)
		mgr.startManagedService(proxy.NewTunnelModeServer(proxy.ProcessMix, pb, task))
		return nil
	case "p2pt":
		logs.Info("start tcp trans monitor port %d", l.Port)
		mgr.startManagedService(proxy.NewTunnelModeServer(proxy.HandleTrans, pb, task))
		return nil
	}

	if l.TargetType == common.CONN_ALL || l.TargetType == common.CONN_TCP {
		logs.Info("local tcp monitoring started on port %d", l.Port)
		mgr.startManagedService(proxy.NewTunnelModeServer(proxy.ProcessTunnel, pb, task))
	}
	if l.TargetType == common.CONN_ALL || l.TargetType == common.CONN_UDP {
		logs.Info("local udp monitoring started on port %d", l.Port)
		mgr.startManagedService(proxy.NewUdpModeServer(pb, task))
	}

	return nil
}

func (mgr *P2PManager) newLocalTunnelTask(l *config.LocalServer) *file.Tunnel {
	if l == nil {
		l = &config.LocalServer{}
	}
	task := &file.Tunnel{
		Port:     l.Port,
		ServerIp: "0.0.0.0",
		Status:   true,
		Client: &file.Client{
			Cnf: &file.Config{
				U:        "",
				P:        "",
				Compress: mgr.cfg.Client.Cnf.Compress,
			},
			Status:    true,
			IsConnect: true,
			RateLimit: 0,
			Flow:      &file.Flow{},
		},
		HttpProxy:   true,
		Socks5Proxy: true,
		Flow:        &file.Flow{},
		Target: &file.Target{
			TargetStr:  l.Target,
			LocalProxy: l.LocalProxy,
		},
	}
	if l.Type == "p2ps" {
		task.Mode = "mixProxy"
	} else if l.Type == "p2pt" {
		task.Mode = "p2pt"
	}
	return task
}

func preferredP2PLocalAddr(localIP string) string {
	localIP = strings.TrimSpace(localIP)
	localIP = strings.Trim(localIP, "[]")
	if localIP == "" {
		return ""
	}
	if ip := net.ParseIP(localIP); ip != nil {
		if ip.IsUnspecified() {
			return ""
		}
		return ip.String()
	}
	return localIP
}

func writeP2PTransportProgress(remoteConn *conn.Conn, sessionID, role, stage, status, localAddr, remoteAddr, detail string, meta map[string]string) {
	if remoteConn == nil {
		return
	}
	_ = p2p.WritePunchProgress(remoteConn, p2p.P2PPunchProgress{
		SessionID:  sessionID,
		Role:       role,
		Stage:      stage,
		Status:     status,
		LocalAddr:  localAddr,
		RemoteAddr: remoteAddr,
		Detail:     detail,
		Meta:       meta,
	})
}

func writeP2PTransportFailure(remoteConn *conn.Conn, sessionID, role, localAddr, remoteAddr, detail string) {
	writeP2PTransportProgress(remoteConn, sessionID, role, "transport_fail_stage", "fail", localAddr, remoteAddr, detail, nil)
}

func closePacketConnSilently(packetConn net.PacketConn) {
	if packetConn != nil {
		_ = packetConn.Close()
	}
}

func resolveP2PTransportTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 10 * time.Second
	}
	return timeout
}

func normalizeP2PTransportMode(mode, preferred string) string {
	mode = strings.TrimSpace(mode)
	preferred = strings.TrimSpace(preferred)
	if mode == "" {
		return common.CONN_KCP
	}
	if preferred != "" && mode != preferred {
		return common.CONN_KCP
	}
	return mode
}

func (mgr *P2PManager) establishVisitorTransport(ctx context.Context, session *visitorPunchSession, cfg *config.CommonConfig) (net.Conn, *quic.Conn, error) {
	if session == nil {
		return nil, nil, errors.New("visitor punch session nil")
	}
	if ctx == nil {
		ctx = mgr.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	writeP2PTransportProgress(session.remoteConn, session.sessionID, session.role, "transport_start", "ok", session.preferredLocal, session.remoteAddr, session.mode, map[string]string{
		"transport_mode": session.mode,
	})
	logs.Debug("visitor p2p result local=%s remote=%s role=%s mode=%s transport_data_len=%d", session.preferredLocal, session.remoteAddr, session.role, session.mode, len(session.data))

	rUDPAddr, err := net.ResolveUDPAddr("udp", session.remoteAddr)
	if err != nil {
		logs.Error("Failed to resolve remote UDP addr: %v", err)
		writeP2PTransportFailure(session.remoteConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddr, "resolve_remote")
		closePacketConnSilently(session.localConn)
		return nil, nil, err
	}

	if session.mode == common.CONN_QUIC {
		dialCtx, cancelDial := context.WithTimeout(ctx, session.transportTimeout)
		sess, err := quic.Dial(dialCtx, session.localConn, rUDPAddr, newClientP2PTLSConfig(false), QuicConfig)
		cancelDial()
		if err != nil {
			logs.Error("QUIC dial error: %v", err)
			writeP2PTransportFailure(session.remoteConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddr, "quic_dial")
			closePacketConnSilently(session.localConn)
			return nil, nil, err
		}
		state := sess.ConnectionState().TLS
		if len(state.PeerCertificates) == 0 {
			logs.Error("Failed to get QUIC certificate")
			writeP2PTransportFailure(session.remoteConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddr, "quic_cert_missing")
			closePacketConnSilently(session.localConn)
			return nil, nil, errors.New("quic certificate missing")
		}
		leaf := state.PeerCertificates[0]
		if !crypt.VerifyPeerTransportData(cfg.VKey, session.data, leaf.Raw) {
			logs.Error("Failed to verify QUIC certificate")
			writeP2PTransportFailure(session.remoteConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddr, "quic_cert_verify")
			closePacketConnSilently(session.localConn)
			return nil, nil, errors.New("quic certificate verify failed")
		}
		writeP2PTransportProgress(session.remoteConn, session.sessionID, session.role, "transport_established", "ok", session.preferredLocal, session.remoteAddr, session.mode, nil)
		return nil, sess, nil
	}

	udpTunnel, err := conn.NewKCPSessionWithConn(rUDPAddr, session.localConn)
	if err != nil {
		logs.Warn("KCP create failed: %v", err)
		writeP2PTransportFailure(session.remoteConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddr, "kcp_create")
		closePacketConnSilently(session.localConn)
		return nil, nil, err
	}
	writeP2PTransportProgress(session.remoteConn, session.sessionID, session.role, "transport_established", "ok", session.preferredLocal, session.remoteAddr, session.mode, nil)
	return udpTunnel, nil, nil
}

var p2pOpenRawConnFromSecretTunnel = openRawConnFromSecretTunnel

var p2pClientNewConnContext = NewConnContextWithTLSVerify

var p2pRunVisitorSession = p2p.RunVisitorSession

var p2pVisitorSendType = SendType

var p2pVisitorSendConnectRequest = func(c *conn.Conn, req *p2p.P2PConnectRequest) error {
	if c == nil {
		return nil
	}
	_, err := c.SendInfo(req, "")
	return err
}

var p2pProviderSendType = SendType

var p2pProviderSendSessionJoin = func(c *conn.Conn, join *p2p.P2PSessionJoin) error {
	if c == nil {
		return nil
	}
	_, err := c.SendInfo(join, "")
	return err
}

type visitorPunchSession struct {
	remoteConn       *conn.Conn
	localConn        net.PacketConn
	remoteAddr       string
	preferredLocal   string
	sessionID        string
	role             string
	mode             string
	data             string
	transportTimeout time.Duration
	stopCancelClose  func()
}

type providerPunchSession struct {
	controlConn      *conn.Conn
	localConn        net.PacketConn
	remoteAddress    string
	preferredLocal   string
	sessionID        string
	associationID    string
	role             string
	mode             string
	data             string
	transportTimeout time.Duration
	stopCancelClose  func()
}

func (mgr *P2PManager) snapshotSecretConn() any {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return normalizeClientRuntimeTunnel(mgr.secretConn)
}

func (mgr *P2PManager) storeSecretConn(secretConn any) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.secretConn = normalizeClientRuntimeTunnel(secretConn)
}

func (mgr *P2PManager) clearSecretConn() any {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	secretConn := normalizeClientRuntimeTunnel(mgr.secretConn)
	mgr.secretConn = nil
	return secretConn
}

func (mgr *P2PManager) openReusableSecretConn(ctx context.Context) (net.Conn, any, error) {
	secretConn := normalizeClientRuntimeTunnel(mgr.snapshotSecretConn())
	if secretConn == nil {
		return nil, nil, nil
	}
	rawConn, err := p2pOpenRawConnFromSecretTunnel(ctx, secretConn)
	if err != nil {
		mgr.clearSecretConn()
		return nil, nil, err
	}
	return rawConn, secretConn, nil
}

func (mgr *P2PManager) getSecretConnContext(ctx context.Context) (c net.Conn, err error) {
	ctx = mgr.normalizeP2PContext(ctx)

	controlConn, err := mgr.openP2PControlConn(ctx, mgr.cfg)
	if err != nil {
		return nil, err
	}
	secretConn := normalizeClientRuntimeTunnel(controlConn.SecretConn)
	uuid := controlConn.UUID
	dialCfg := mgr.resolveP2PControlDialConfig(mgr.cfg)
	disconnectTime := 0
	if mgr.cfg != nil {
		disconnectTime = mgr.cfg.DisconnectTime
	}
	if secretConn == nil {
		pc := controlConn.Conn
		if pc == nil {
			logs.Error("secret GetConn failed: nil control conn")
			return nil, errors.New("secret conn nil")
		}
		if Ver > 5 {
			err = SendType(pc, common.WORK_VISITOR, uuid)
			if err != nil {
				logs.Error("secret SendType failed: %v", err)
				_ = pc.Close()
				return nil, err
			}
			if dialCfg.Tp == common.CONN_QUIC {
				qc, ok := pc.Conn.(*conn.QuicAutoCloseConn)
				if !ok {
					logs.Error("failed to get quic session")
					_ = pc.Close()
					return nil, errors.New("failed to get quic session")
				}
				sess := qc.GetSession()
				var stream *quic.Stream
				stream, err = p2pOpenQUICStreamSync(ctx, sess)
				if err != nil {
					logs.Error("secret OpenStreamSync failed: %v", err)
					_ = pc.Close()
					return nil, err
				}
				c = conn.NewQuicStreamConn(stream, sess)
				secretConn = sess
			} else {
				muxConn := mux.NewMux(pc.Conn, dialCfg.Tp, disconnectTime, true)
				c, err = muxConn.NewConnTimeout(secretMuxOpenTimeout(ctx))
				if err != nil {
					err = mapSecretMuxOpenError(ctx, err)
					logs.Error("secret muxConn failed: %v", err)
					_ = muxConn.Close()
					_ = pc.Close()
					return nil, err
				}
				secretConn = muxConn
			}
			mgr.storeSecretConn(secretConn)
		} else {
			c = pc
		}
	} else if controlConn.Conn != nil {
		c = controlConn.Conn.Conn
	}
	if c == nil {
		logs.Error("secret GetConn failed: %v", err)
		return nil, errors.New("secret conn nil")
	}
	err = p2pSecretSendType(conn.NewConn(c), common.WORK_SECRET, uuid)
	if err != nil {
		logs.Error("secret SendType failed: %v", err)
		mgr.discardSecretConnIfCurrent(secretConn, "secret send type failed")
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func closeCachedSecretTunnel(secretConn any, reason string) {
	_ = closeClientRuntimeTunnel(secretConn, reason)
}

func (mgr *P2PManager) discardSecretConnIfCurrent(secretConn any, reason string) {
	secretConn = normalizeClientRuntimeTunnel(secretConn)
	if mgr == nil || secretConn == nil {
		return
	}
	shouldClose := false
	mgr.mu.Lock()
	if normalizeClientRuntimeTunnel(mgr.secretConn) == secretConn {
		mgr.secretConn = nil
		shouldClose = true
	}
	mgr.mu.Unlock()
	if shouldClose {
		closeCachedSecretTunnel(secretConn, reason)
	}
}

func openRawConnFromSecretTunnel(ctx context.Context, secretConn any) (net.Conn, error) {
	secretConn = normalizeClientRuntimeTunnel(secretConn)
	switch tun := secretConn.(type) {
	case *mux.Mux:
		rawConn, err := tun.NewConnTimeout(secretMuxOpenTimeout(ctx))
		if err != nil {
			_ = tun.Close()
			err = mapSecretMuxOpenError(ctx, err)
		}
		return rawConn, err
	case *quic.Conn:
		stream, err := tun.OpenStreamSync(ctx)
		if err != nil {
			_ = tun.CloseWithError(0, err.Error())
			return nil, err
		}
		return conn.NewQuicStreamConn(stream, tun), nil
	default:
		err := errors.New("the tunnel is unavailable")
		logs.Error("the tunnel is unavailable")
		return nil, err
	}
}

func secretMuxOpenTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Millisecond
	}
	return remaining
}

func mapSecretMuxOpenError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if secretMuxOpenTimeout(ctx) > 0 && strings.Contains(err.Error(), "create connection timeout") {
			return context.DeadlineExceeded
		}
	}
	return err
}

func closeSecretTunnel(secretConn any, reason string) {
	secretConn = normalizeClientRuntimeTunnel(secretConn)
	if secretConn == nil {
		return
	}
	_ = closeClientRuntimeTunnel(secretConn, reason)
}

func (session *visitorPunchSession) close() {
	if session == nil {
		return
	}
	if session.stopCancelClose != nil {
		session.stopCancelClose()
		session.stopCancelClose = nil
	}
	if session.remoteConn == nil {
		return
	}
	_ = session.remoteConn.Close()
	session.remoteConn = nil
}

func (mgr *P2PManager) openVisitorPunchSession(ctx context.Context, preferredLocalAddr string, cfg *config.CommonConfig, l *config.LocalServer, binding *p2pRouteBinding) (*visitorPunchSession, error) {
	ctx = mgr.normalizeP2PContext(ctx)
	var err error
	controlConn, err := mgr.openP2PControlConn(ctx, cfg)
	if err != nil {
		logs.Error("Failed to connect to server: %v", err)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	remoteConn := controlConn.Conn
	secretConn := controlConn.SecretConn
	if remoteConn == nil {
		logs.Error("Get conn failed: %v", err)
		return nil, err
	}
	stopCloseOnCancel := closeConnOnContextDone(ctx, remoteConn.Conn)
	keepCancelClose := false
	defer func() {
		if !keepCancelClose {
			stopCloseOnCancel()
		}
	}()

	if err := p2pVisitorSendType(remoteConn, common.WORK_P2P, controlConn.UUID); err != nil {
		logs.Error("Failed to send type to server: %v", err)
		mgr.discardSecretConnIfCurrent(secretConn, "p2p visitor send type failed")
		_ = remoteConn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	passwordMD5 := crypt.Md5(l.Password)
	if binding != nil && binding.passwordMD5 != "" {
		passwordMD5 = binding.passwordMD5
	}
	req := &p2p.P2PConnectRequest{
		PasswordMD5: passwordMD5,
		RouteHint:   buildLocalP2PRouteHint(l),
	}
	if binding != nil {
		req.AssociationID = binding.associationID
		req.ProviderUUID = binding.peerUUID
		req.RouteHint = binding.routeContext
	}
	if err := p2pVisitorSendConnectRequest(remoteConn, req); err != nil {
		logs.Error("Failed to send p2p connect request to server: %v", err)
		mgr.discardSecretConnIfCurrent(secretConn, "p2p visitor connect request failed")
		_ = remoteConn.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	localConn, remoteAddr, preferredLocalAddr, sessionID, role, mode, data, transportTimeout, err := p2pRunVisitorSession(ctx, remoteConn, preferredLocalAddr, P2PMode, "")
	if err != nil {
		logs.Error("Run visitor P2P session failed: %v", err)
		_ = remoteConn.Close()
		return nil, err
	}

	keepCancelClose = true
	return &visitorPunchSession{
		remoteConn:       remoteConn,
		localConn:        localConn,
		remoteAddr:       remoteAddr,
		preferredLocal:   preferredLocalAddr,
		sessionID:        sessionID,
		role:             role,
		mode:             normalizeP2PTransportMode(mode, ""),
		data:             data,
		transportTimeout: resolveP2PTransportTimeout(transportTimeout),
		stopCancelClose:  stopCloseOnCancel,
	}, nil
}

func (s *TRPClient) openProviderPunchSession(ctx context.Context, start p2p.P2PPunchStart, preferredLocalAddr string) (*providerPunchSession, error) {
	ctx = s.normalizeClientContext(ctx)
	bridgeConn, err := s.openBridgeControlConn(ctx)
	if err != nil {
		return nil, err
	}
	controlConn := bridgeConn.Conn
	cleanup := true
	defer func() {
		if cleanup {
			_ = controlConn.Close()
		}
	}()
	stopCloseOnCancel := closeConnOnContextDone(ctx, controlConn.Conn)
	defer func() {
		if cleanup {
			stopCloseOnCancel()
		}
	}()

	if err := p2pProviderSendType(controlConn, common.WORK_P2P_SESSION, bridgeConn.UUID); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err := p2pProviderSendSessionJoin(controlConn, &p2p.P2PSessionJoin{
		SessionID: start.SessionID,
		Token:     start.Token,
	}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}

	sendData := ""
	if P2PMode == common.CONN_QUIC {
		sendData = crypt.EncodePeerTransportData(crypt.GetCert().Certificate[0])
	}
	localConn, remoteAddress, preferredLocalAddr, sessionID, role, mode, data, transportTimeout, err := p2p.RunProviderSession(ctx, controlConn, start, preferredLocalAddr, P2PMode, sendData)
	if err != nil {
		return nil, err
	}
	cleanup = false
	return &providerPunchSession{
		controlConn:      controlConn,
		localConn:        localConn,
		remoteAddress:    remoteAddress,
		preferredLocal:   preferredLocalAddr,
		sessionID:        sessionID,
		associationID:    strings.TrimSpace(start.AssociationID),
		role:             role,
		mode:             normalizeP2PTransportMode(mode, P2PMode),
		data:             data,
		transportTimeout: resolveP2PTransportTimeout(transportTimeout),
		stopCancelClose:  stopCloseOnCancel,
	}, nil
}

func (session *providerPunchSession) closeControl() {
	if session == nil {
		return
	}
	if session.stopCancelClose != nil {
		session.stopCancelClose()
		session.stopCancelClose = nil
	}
	if session.controlConn == nil {
		return
	}
	_ = session.controlConn.Close()
	session.controlConn = nil
}
