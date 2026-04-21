package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
	"github.com/quic-go/quic-go"
)

type TRPClient struct {
	svrAddr        string
	bridgeConnType string
	proxyUrl       string
	localIP        string
	vKey           string
	uuid           string
	tunnel         any
	signal         *conn.Conn
	fsm            *FileServerManager
	ticker         *time.Ticker
	cnf            *config.Config
	disconnectTime int
	ctx            context.Context
	cancel         context.CancelFunc
	healthChecker  *HealthChecker
	once           sync.Once
	startedNano    int64
	reasonMu       sync.RWMutex
	closeReason    string
	p2pStateOnce   sync.Once
	p2pState       *clientP2PRuntimeStateHolder
}

type clientStartupRuntime struct {
	establishSignal func(*TRPClient) error
	startMonitor    func(*TRPClient)
	startHealth     func(*TRPClient)
	markReady       func(*TRPClient)
}

type clientMainEvent struct {
	Flag       string
	Bind       *p2p.P2PAssociationBind
	PunchStart *p2p.P2PPunchStart
}

type clientMainRuntime struct {
	readEvent     func(*TRPClient) (*clientMainEvent, error)
	dispatchEvent func(*TRPClient, *clientMainEvent)
}

type clientBridgeControlConn struct {
	Conn *conn.Conn
	UUID string
}

type clientMonitorRuntime struct {
	interval     time.Duration
	newTicker    func(time.Duration) *time.Ticker
	tunnelClosed func(*TRPClient) bool
	closeReason  func(*TRPClient) string
	noteReason   func(*TRPClient, string)
	closeClient  func(*TRPClient)
}

type clientP2PJoinRuntime struct {
	timeout         func(*TRPClient) time.Duration
	preferredLocal  func(*TRPClient) string
	openSession     func(*TRPClient, context.Context, p2p.P2PPunchStart, string) (*providerPunchSession, error)
	writeProgress   func(*providerPunchSession)
	serveSession    func(*TRPClient, *providerPunchSession)
	closeControl    func(*providerPunchSession)
	closePacketConn func(net.PacketConn)
	logError        func(error)
}

type clientShutdownRuntime struct {
	captureReason func(*TRPClient)
	logSummary    func(*TRPClient)
	stopHealth    func(*TRPClient)
	cancelContext func(*TRPClient)
	closeTunnel   func(*TRPClient, string)
	closeSignal   func(*TRPClient)
	stopTicker    func(*TRPClient)
	closeFiles    func(*TRPClient)
}

type clientDisconnectSummary struct {
	Server   string
	Bridge   string
	UUID     string
	Uptime   time.Duration
	TunnelUp bool
	SignalUp bool
	Reason   string
}

var clientCloseMuxTunnel = func(tunnel *mux.Mux) error {
	return tunnel.Close()
}

var clientCloseQUICConn = func(tunnel *quic.Conn, reason string) error {
	return tunnel.CloseWithError(0, reason)
}

func normalizeClientRuntimeTunnel(value any) any {
	switch tunnel := value.(type) {
	case nil:
		return nil
	case *mux.Mux:
		if tunnel == nil {
			return nil
		}
		return tunnel
	case *quic.Conn:
		if tunnel == nil {
			return nil
		}
		return tunnel
	default:
		return value
	}
}

func isNilClientNetConn(connection net.Conn) bool {
	if connection == nil {
		return true
	}
	rv := reflect.ValueOf(connection)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}

func clientRuntimeTunnelClosed(value any) bool {
	switch tunnel := normalizeClientRuntimeTunnel(value).(type) {
	case *mux.Mux:
		return tunnel.IsClosed()
	case *quic.Conn:
		return tunnel.Context().Err() != nil
	default:
		return true
	}
}

func clientRuntimeTunnelCloseReason(value any) string {
	switch tunnel := normalizeClientRuntimeTunnel(value).(type) {
	case *mux.Mux:
		return tunnel.CloseReason()
	case *quic.Conn:
		if err := tunnel.Context().Err(); err != nil {
			return "quic tunnel closed: " + err.Error()
		}
	}
	return ""
}

func closeClientRuntimeTunnel(value any, reason string) error {
	switch tunnel := normalizeClientRuntimeTunnel(value).(type) {
	case *mux.Mux:
		return clientCloseMuxTunnel(tunnel)
	case *quic.Conn:
		return clientCloseQUICConn(tunnel, reason)
	default:
		return nil
	}
}

// NewRPClient new client
func NewRPClient(svrAddr, vKey, bridgeConnType, proxyUrl, localIP, uuid string, cnf *config.Config, disconnectTime int, fsm *FileServerManager) *TRPClient {
	return &TRPClient{
		svrAddr:        svrAddr,
		vKey:           vKey,
		bridgeConnType: bridgeConnType,
		proxyUrl:       proxyUrl,
		localIP:        localIP,
		uuid:           uuid,
		cnf:            cnf,
		disconnectTime: disconnectTime,
		fsm:            fsm,
		p2pState:       newClientP2PRuntimeStateHolder(),
		once:           sync.Once{},
	}
}

var NowStatus int
var HasFailed = false

var clientMainSendType = SendType

var errClientTunnelNotConnected = errors.New("tunnel is not connected")
var errClientMainSignalMuxOpen = errors.New("open mux main signal")
var errClientMainSignalQuicOpen = errors.New("open quic main signal")
var errClientUnsupportedTunnelType = errors.New("unsupported tunnel type")

var clientOpenMainMuxConn = func(tunnel *mux.Mux) (net.Conn, error) {
	rawConn, err := tunnel.NewConn()
	if err != nil {
		return nil, err
	}
	rawConn.SetPriority()
	return rawConn, nil
}

var clientOpenMainQUICConn = func(ctx context.Context, tunnel *quic.Conn) (net.Conn, error) {
	stream, err := tunnel.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	return conn.NewQuicAutoCloseConn(stream, tunnel), nil
}

var runtimeClientStartup = clientStartupRuntime{
	establishSignal: func(s *TRPClient) error {
		return s.establishSignalRuntime()
	},
	startMonitor: func(s *TRPClient) {
		go runtimeClientMonitor.Run(s)
	},
	startHealth: func(s *TRPClient) {
		if s.cnf != nil && len(s.cnf.Healths) > 0 {
			s.healthChecker = NewHealthChecker(s.ctx, s.cnf.Healths, s.signal)
			s.healthChecker.Start()
		}
	},
	markReady: func(s *TRPClient) {
		NowStatus = 1
		atomic.StoreInt64(&s.startedNano, time.Now().UnixNano())
	},
}

var runtimeClientMain = clientMainRuntime{
	readEvent: func(s *TRPClient) (*clientMainEvent, error) {
		return s.readMainEvent()
	},
	dispatchEvent: func(s *TRPClient, event *clientMainEvent) {
		s.dispatchMainEvent(event)
	},
}

var runtimeClientMonitor = clientMonitorRuntime{
	interval: 5 * time.Second,
	newTicker: func(interval time.Duration) *time.Ticker {
		return time.NewTicker(interval)
	},
	tunnelClosed: func(s *TRPClient) bool {
		return s.isTunnelClosed()
	},
	closeReason: func(s *TRPClient) string {
		return s.snapshotCloseReason()
	},
	noteReason: func(s *TRPClient, reason string) {
		s.noteCloseReason(reason)
	},
	closeClient: func(s *TRPClient) {
		s.Close()
	},
}

var runtimeClientP2PJoin = newClientP2PJoinRuntime()

var runtimeClientShutdown = clientShutdownRuntime{
	captureReason: func(s *TRPClient) {
		s.captureCloseReasonRuntime()
	},
	logSummary: func(s *TRPClient) {
		s.logDisconnectSummary()
	},
	stopHealth: func(s *TRPClient) {
		s.stopHealthRuntime()
	},
	cancelContext: func(s *TRPClient) {
		s.cancelClientContext()
	},
	closeTunnel: func(s *TRPClient, reason string) {
		s.closeTunnelRuntime(reason)
	},
	closeSignal: func(s *TRPClient) {
		s.closeSignalRuntime()
	},
	stopTicker: func(s *TRPClient) {
		s.stopTickerRuntime()
	},
	closeFiles: func(s *TRPClient) {
		s.closeFileServersRuntime()
	},
}

func newClientP2PJoinRuntime() clientP2PJoinRuntime {
	return clientP2PJoinRuntime{
		timeout: func(s *TRPClient) time.Duration {
			return s.p2pJoinTimeout()
		},
		preferredLocal: func(s *TRPClient) string {
			return preferredP2PLocalAddr(s.localIP)
		},
		openSession: func(s *TRPClient, p2pCtx context.Context, start p2p.P2PPunchStart, preferredLocal string) (*providerPunchSession, error) {
			return s.openProviderPunchSession(p2pCtx, start, preferredLocal)
		},
		writeProgress: func(session *providerPunchSession) {
			if session == nil {
				return
			}
			writeP2PTransportProgress(session.controlConn, session.sessionID, session.role, "transport_start", "ok", session.preferredLocal, session.remoteAddress, session.mode, map[string]string{
				"transport_mode": session.mode,
			})
		},
		serveSession: func(s *TRPClient, session *providerPunchSession) {
			if runtimeClientP2PProviderRoot != nil {
				runtimeClientP2PProviderRoot.ServeTransport(s, session)
			}
		},
		closeControl: func(session *providerPunchSession) {
			session.closeControl()
		},
		closePacketConn: closePacketConnSilently,
		logError: func(err error) {
			logs.Error("run provider P2P session error: %v", err)
		},
	}
}

func (s *TRPClient) Start(ctx context.Context) {
	ctx = normalizeClientParentContext(ctx)
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.Close()
	NowStatus = 0
	if err := runtimeClientStartup.Start(s); err != nil {
		s.handleStartupError(err)
		return
	}
	s.handleMain()
}

// handle main connection
func (s *TRPClient) handleMain() {
	defer s.Close()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		event, err := runtimeClientMain.readEvent(s)
		if err != nil {
			s.handleMainReadError(err)
			return
		}
		runtimeClientMain.dispatchEvent(s, event)
	}
}

func (s *TRPClient) Close() {
	s.once.Do(s.closing)
}

func (s *TRPClient) closing() {
	runtimeClientShutdown.Shutdown(s)
}

func (s *TRPClient) noteCloseReason(reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	s.reasonMu.Lock()
	defer s.reasonMu.Unlock()
	if s.closeReason == "" {
		s.closeReason = reason
	}
}

func (s *TRPClient) getCloseReason() string {
	s.reasonMu.RLock()
	defer s.reasonMu.RUnlock()
	return s.closeReason
}

func (r clientStartupRuntime) Start(s *TRPClient) error {
	if err := r.establishSignal(s); err != nil {
		return err
	}
	r.startMonitor(s)
	r.startHealth(s)
	r.markReady(s)
	return nil
}

func (s *TRPClient) establishSignalRuntime() error {
	if Ver < 5 {
		return s.establishLegacySignalRuntime()
	}
	return s.establishTunnelSignalRuntime()
}

func (s *TRPClient) establishLegacySignalRuntime() error {
	signal, err := s.openLegacyMainSignal()
	if err != nil {
		return err
	}
	s.signal = signal
	logs.Info("Successful connection with server %s", s.svrAddr)
	return nil
}

func (s *TRPClient) establishTunnelSignalRuntime() error {
	tunnel, err := s.openBridgeTunnel()
	if err != nil {
		return err
	}
	if err := s.installBridgeTunnel(tunnel); err != nil {
		return err
	}
	s.serveTunnelAcceptLoop(tunnel)
	signal, err := s.openTunnelMainSignal()
	if err != nil {
		return err
	}
	s.signal = signal
	logs.Info("Successful connection with server %s", s.svrAddr)
	return nil
}

func (s *TRPClient) handleStartupError(err error) {
	switch {
	case errors.Is(err, errClientTunnelNotConnected):
		logs.Error("The tunnel is not connected")
	case errors.Is(err, errClientUnsupportedTunnelType):
		logs.Error("%v", err)
	case errors.Is(err, errClientMainSignalQuicOpen):
		logs.Error("Quic OpenStreamSync failed, retrying: %v", err)
		s.Close()
	case errors.Is(err, errClientMainSignalMuxOpen):
		logs.Error("Failed to get new connection, possible version mismatch: %v", err)
		s.Close()
	default:
		HasFailed = true
		logs.Error("The connection server failed and will be reconnected in five seconds, error %v", err)
	}
}

func (s *TRPClient) normalizeClientContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if s != nil && s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *TRPClient) openBridgeControlConn(ctx context.Context) (*clientBridgeControlConn, error) {
	ctx = s.normalizeClientContext(ctx)
	controlConn, uuid, err := p2pClientNewConnContext(ctx, s.bridgeConnType, s.vKey, s.svrAddr, s.proxyUrl, s.localIP, false)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if s != nil {
		if s.uuid == "" {
			s.uuid = uuid
		} else {
			uuid = s.uuid
		}
	}
	return &clientBridgeControlConn{
		Conn: controlConn,
		UUID: uuid,
	}, nil
}

func (s *TRPClient) openLegacyMainSignal() (*conn.Conn, error) {
	bridgeConn, err := s.openBridgeControlConn(s.ctx)
	if err != nil {
		return nil, err
	}
	if err := clientMainSendType(bridgeConn.Conn, common.WORK_MAIN, bridgeConn.UUID); err != nil {
		_ = bridgeConn.Conn.Close()
		return nil, err
	}
	return bridgeConn.Conn, nil
}

func (s *TRPClient) openTunnelMainSignal() (*conn.Conn, error) {
	tunnelValue := normalizeClientRuntimeTunnel(s.tunnel)
	if s != nil {
		s.tunnel = tunnelValue
	}
	if tunnelValue == nil {
		return nil, errClientTunnelNotConnected
	}
	switch tunnel := tunnelValue.(type) {
	case *mux.Mux:
		return s.openMuxMainSignal(tunnel)
	case *quic.Conn:
		return s.openQUICMainSignal(tunnel)
	default:
		return nil, fmt.Errorf("%w: %T", errClientUnsupportedTunnelType, tunnel)
	}
}

func (s *TRPClient) openMuxMainSignal(tunnel *mux.Mux) (*conn.Conn, error) {
	rawConn, err := clientOpenMainMuxConn(tunnel)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errClientMainSignalMuxOpen, err)
	}
	return s.wrapTunnelMainSignal(rawConn)
}

func (s *TRPClient) openQUICMainSignal(tunnel *quic.Conn) (*conn.Conn, error) {
	rawConn, err := clientOpenMainQUICConn(s.normalizeClientContext(nil), tunnel)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errClientMainSignalQuicOpen, err)
	}
	return s.wrapTunnelMainSignal(rawConn)
}

func (s *TRPClient) wrapTunnelMainSignal(rawConn net.Conn) (*conn.Conn, error) {
	mainConn := conn.NewConn(rawConn)
	if err := clientMainSendType(mainConn, common.WORK_MAIN, s.uuid); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return mainConn, nil
}

func readClientMainJSON[T any](signal *conn.Conn) (*T, error) {
	if signal == nil {
		return nil, nil
	}
	raw, err := signal.GetShortLenContent()
	if err != nil {
		return nil, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func drainClientMainPayload(signal *conn.Conn) error {
	if signal == nil {
		return nil
	}
	_, err := signal.GetShortLenContent()
	return err
}

func (s *TRPClient) readMainEvent() (*clientMainEvent, error) {
	if s == nil || s.signal == nil {
		return nil, nil
	}
	flag, err := s.signal.ReadFlag()
	if err != nil {
		return nil, err
	}
	return s.decodeMainEvent(flag)
}

func (s *TRPClient) decodeMainEvent(flag string) (*clientMainEvent, error) {
	event := &clientMainEvent{Flag: flag}
	switch flag {
	case common.P2P_ASSOCIATION_BIND:
		bind, err := readClientMainJSON[p2p.P2PAssociationBind](s.signal)
		if err != nil {
			return nil, err
		}
		event.Bind = bind
	case common.P2P_PUNCH_START:
		start, err := readClientMainJSON[p2p.P2PPunchStart](s.signal)
		if err != nil {
			return nil, err
		}
		event.PunchStart = start
	default:
		if err := drainClientMainPayload(s.signal); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return event, nil
}

func (s *TRPClient) dispatchMainEvent(event *clientMainEvent) {
	if s == nil || event == nil {
		return
	}
	switch {
	case event.Bind != nil:
		runtimeClientP2PStateRoot.RecordBind(s, *event.Bind)
	case event.PunchStart != nil:
		s.dispatchPunchStartEvent(*event.PunchStart)
	}
}

func (s *TRPClient) dispatchPunchStartEvent(start p2p.P2PPunchStart) {
	if !shouldRecordP2PPunchStart(s, start) {
		return
	}
	runtimeClientP2PStateRoot.RecordPunchStart(s, start)
	if !DisableP2P {
		go runtimeClientP2PJoin.Run(s, start)
	}
}

func (s *TRPClient) handleMainReadError(err error) {
	if err == nil {
		return
	}
	if s != nil && s.signal != nil {
		s.noteCloseReason("signal read failed: " + conn.DescribeNetError(err, s.signal.Conn))
		logs.Error("Accept server data error %s, end this service", conn.DescribeNetError(err, s.signal.Conn))
		return
	}
	logs.Error("Accept server data error %v, end this service", err)
}

func (r clientMonitorRuntime) Run(s *TRPClient) {
	if s == nil {
		return
	}
	s.ticker = r.newTicker(r.interval)
	for {
		select {
		case <-s.ticker.C:
			if r.tunnelClosed(s) {
				r.noteReason(s, r.closeReason(s))
				r.closeClient(s)
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (r clientP2PJoinRuntime) Run(s *TRPClient, start p2p.P2PPunchStart) {
	if s == nil {
		return
	}
	p2pCtx, cancelP2P := context.WithTimeout(s.normalizeClientContext(nil), r.timeout(s))
	defer cancelP2P()

	session, err := r.openSession(s, p2pCtx, start, r.preferredLocal(s))
	if err != nil {
		r.logError(err)
		return
	}
	defer r.closeControl(session)
	defer r.closePacketConn(session.localConn)

	r.writeProgress(session)
	r.serveSession(s, session)
}

func (s *TRPClient) p2pJoinTimeout() time.Duration {
	wait := time.Duration(s.disconnectTime) * time.Second
	if wait <= 0 {
		return 30 * time.Second
	}
	return wait
}

func (r clientShutdownRuntime) Shutdown(s *TRPClient) {
	NowStatus = 0
	r.captureReason(s)
	r.stopHealth(s)
	r.cancelContext(s)
	r.closeTunnel(s, "close")
	r.closeSignal(s)
	r.stopTicker(s)
	r.closeFiles(s)
	r.logSummary(s)
}

func (s *TRPClient) captureCloseReasonRuntime() {
	if reason := s.snapshotCloseReason(); reason != "" {
		s.noteCloseReason(reason)
	}
}

func (s *TRPClient) stopHealthRuntime() {
	if s == nil || s.healthChecker == nil {
		return
	}
	s.healthChecker.Stop()
	s.healthChecker = nil
}

func (s *TRPClient) cancelClientContext() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *TRPClient) closeSignalRuntime() {
	if s == nil || s.signal == nil {
		return
	}
	_ = s.signal.Close()
	s.signal = nil
}

func (s *TRPClient) stopTickerRuntime() {
	if s == nil || s.ticker == nil {
		return
	}
	s.ticker.Stop()
	s.ticker = nil
}

func (s *TRPClient) closeFileServersRuntime() {
	if s == nil || s.fsm == nil {
		return
	}
	s.fsm.CloseAll()
	s.fsm = nil
}

func (s *TRPClient) isTunnelClosed() bool {
	return clientRuntimeTunnelClosed(s.tunnel)
}

func (s *TRPClient) snapshotCloseReason() string {
	if reason := s.getCloseReason(); reason != "" {
		return reason
	}
	if reason := clientRuntimeTunnelCloseReason(s.tunnel); reason != "" {
		return reason
	}
	if s.signal != nil && s.signal.IsClosed() {
		return "signal connection closed"
	}
	return ""
}

func (s *TRPClient) buildDisconnectSummary(now time.Time) (clientDisconnectSummary, bool) {
	started := atomic.LoadInt64(&s.startedNano)
	reason := s.getCloseReason()
	tunnelValue := normalizeClientRuntimeTunnel(s.tunnel)
	if s != nil {
		s.tunnel = tunnelValue
	}
	if started == 0 && s.signal == nil && tunnelValue == nil && reason == "" {
		return clientDisconnectSummary{}, false
	}
	if reason == "" {
		reason = "close requested"
	}
	uptime := time.Duration(0)
	if started > 0 {
		uptime = now.Sub(time.Unix(0, started)).Round(time.Millisecond)
	}
	return clientDisconnectSummary{
		Server:   s.svrAddr,
		Bridge:   s.bridgeConnType,
		UUID:     s.uuid,
		Uptime:   uptime,
		TunnelUp: !s.isTunnelClosed(),
		SignalUp: s.signal != nil && !s.signal.IsClosed(),
		Reason:   reason,
	}, true
}

func (s *TRPClient) logDisconnectSummary() {
	summary, ok := s.buildDisconnectSummary(time.Now())
	if !ok {
		return
	}
	logs.Warn(
		"Disconnect summary event=disconnect_summary role=client server=%s bridge=%s uuid=%s uptime=%s tunnel_up=%t signal_up=%t reason=%q",
		summary.Server,
		summary.Bridge,
		summary.UUID,
		summary.Uptime,
		summary.TunnelUp,
		summary.SignalUp,
		summary.Reason,
	)
}

func (s *TRPClient) closeTunnelRuntime(reason string) {
	if s == nil {
		return
	}
	_ = closeClientRuntimeTunnel(s.tunnel, reason)
	s.tunnel = nil
}
