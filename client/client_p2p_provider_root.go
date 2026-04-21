package client

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

type clientP2PProviderRuntimeRoot struct {
	lifecycle            clientP2PProviderLifecycleRuntime
	setup                clientP2PProviderSetupRuntime
	promotion            clientP2PProviderPromotionRuntime
	adapters             clientP2PProviderTransportAdapterRuntime
	channelSpecial       clientP2PProviderChannelSpecialRuntime
	channelAuthorization clientP2PProviderChannelAuthorizationRuntime
	channelDial          clientP2PProviderChannelDialRuntime
	channelResult        clientP2PProviderChannelResultRuntime
	channelRelay         clientP2PProviderChannelRelayRuntime
	channelPrepare       clientP2PProviderChannelPrepareRuntime
	channelConnect       clientP2PProviderChannelConnectRuntime
	channelHandle        clientP2PProviderChannelHandleRuntime
	channelLaunchAsync   func(func())
	streams              clientP2PProviderQUICStreamRuntime
	kcpMux               clientP2PProviderKCPMuxRuntime
	quicServe            clientP2PProviderQUICServeRuntime
	kcpServe             clientP2PProviderKCPServeRuntime
	quicAccept           clientP2PProviderQUICAcceptRuntime
	kcpAccept            clientP2PProviderKCPAcceptRuntime
}

var runtimeClientP2PProviderRoot = newClientP2PProviderRuntimeRoot()

func newClientP2PProviderRuntimeRoot() *clientP2PProviderRuntimeRoot {
	ctx := &clientP2PProviderRuntimeRoot{}
	ctx.initChannelRuntimeWiring()
	ctx.initTransportSupportRuntimeWiring()
	ctx.initLifecycleRuntimeWiring()
	return ctx
}

func (ctx *clientP2PProviderRuntimeRoot) initChannelRuntimeWiring() {
	if ctx == nil {
		return
	}
	ctx.channelSpecial = clientP2PProviderChannelSpecialRuntime{
		handleUDP5: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
			if s == nil {
				return false
			}
			return s.handleUDP5Channel(src, lk)
		},
		handleFile: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
			if s == nil {
				return false
			}
			return s.handleFileChannel(src, lk)
		},
	}
	ctx.channelAuthorization = clientP2PProviderChannelAuthorizationRuntime{
		associationID: p2pAssociationIDFromConn,
		policyOf: func(s *TRPClient, associationID string) (p2p.P2PAccessPolicy, bool) {
			return runtimeClientP2PStateRoot.AssociationPolicy(s, associationID)
		},
		allowsTarget: p2pAccessPolicyAllows,
		denyMissing:  providerDenyMissingChannelTarget,
		denyTarget:   providerDenyChannelTarget,
	}
	ctx.channelDial = clientP2PProviderChannelDialRuntime{
		dial: func(s *TRPClient, lk *conn.Link) (net.Conn, error) {
			return runtimeClientChannelDial.Dial(s, lk)
		},
	}
	ctx.channelResult = clientP2PProviderChannelResultRuntime{
		write:          conn.WriteConnectResult,
		logWriteFailed: logProviderChannelConnectResultWriteFailed,
	}
	ctx.channelRelay = clientP2PProviderChannelRelayRuntime{
		logRelay: logProviderChannelRelay,
		isFramed: func(lk *conn.Link) bool {
			return lk != nil && lk.ConnType == "udp" && Ver > 6
		},
		copy: func(src, targetConn net.Conn, lk *conn.Link, isFramed bool) {
			conn.CopyWaitGroup(src, targetConn, lk.Crypt, lk.Compress, nil, nil, nil, false, 0, nil, nil, false, isFramed)
		},
	}
	ctx.channelPrepare = clientP2PProviderChannelPrepareRuntime{
		readLink:     readChannelLink,
		ackLink:      ackChannelLink,
		closeConn:    closeProviderChannelConn,
		formatHost:   common.FormatAddress,
		logReadError: logProviderChannelReadError,
		logEmptyHost: logProviderChannelEmptyHost,
	}
	ctx.channelConnect = clientP2PProviderChannelConnectRuntime{
		dialTarget: func(s *TRPClient, lk *conn.Link) (net.Conn, error) {
			return ctx.channelDial.Dial(s, lk)
		},
		writeConnectResult: func(src net.Conn, lk *conn.Link, result conn.ConnectResultStatus) bool {
			return ctx.channelResult.Write(src, lk, result)
		},
		closeSource:  closeProviderChannelConn,
		closeTarget:  closeProviderChannelConn,
		logDialError: logProviderChannelDialError,
	}
	ctx.channelLaunchAsync = func(run func()) {
		if run != nil {
			go run()
		}
	}
	ctx.channelHandle = clientP2PProviderChannelHandleRuntime{
		prepare: func(src net.Conn) (*conn.Link, bool) {
			return ctx.channelPrepare.Prepare(src)
		},
		handleSpecial: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
			return ctx.channelSpecial.Handle(s, src, lk)
		},
		authorizeTarget: func(s *TRPClient, src net.Conn, lk *conn.Link) bool {
			return ctx.channelAuthorization.Authorize(s, src, lk)
		},
		connectTarget: func(s *TRPClient, src net.Conn, lk *conn.Link) (net.Conn, bool) {
			return ctx.channelConnect.Connect(s, src, lk)
		},
		relayTarget: func(src, targetConn net.Conn, lk *conn.Link) {
			ctx.channelRelay.Relay(src, targetConn, lk)
		},
	}
}

func (ctx *clientP2PProviderRuntimeRoot) dispatchChannel(s *TRPClient, session *providerPunchSession, c net.Conn) {
	if ctx == nil || s == nil || isNilClientNetConn(c) {
		return
	}
	associationID := ""
	if session != nil {
		associationID = session.associationID
	}
	wrapped := wrapP2PAssociationConn(c, associationID)
	if isNilClientNetConn(wrapped) {
		return
	}
	run := func() {
		ctx.channelHandle.Handle(s, wrapped)
	}
	if ctx.channelLaunchAsync == nil {
		run()
		return
	}
	ctx.channelLaunchAsync(run)
}

type clientP2PProviderChannelSpecialRuntime struct {
	handleUDP5 func(*TRPClient, net.Conn, *conn.Link) bool
	handleFile func(*TRPClient, net.Conn, *conn.Link) bool
}

func (r clientP2PProviderChannelSpecialRuntime) Handle(s *TRPClient, src net.Conn, lk *conn.Link) bool {
	if lk == nil {
		return true
	}
	if lk.ConnType == "udp5" {
		if r.handleUDP5 == nil {
			return false
		}
		return r.handleUDP5(s, src, lk)
	}
	if lk.ConnType == "file" {
		if r.handleFile == nil {
			return false
		}
		return r.handleFile(s, src, lk)
	}
	return false
}

type clientP2PProviderChannelAuthorizationRuntime struct {
	associationID func(net.Conn) string
	policyOf      func(*TRPClient, string) (p2p.P2PAccessPolicy, bool)
	allowsTarget  func(p2p.P2PAccessPolicy, string) bool
	denyMissing   func(net.Conn, *conn.Link, string)
	denyTarget    func(net.Conn, *conn.Link, string)
}

func (r clientP2PProviderChannelAuthorizationRuntime) Authorize(s *TRPClient, src net.Conn, lk *conn.Link) bool {
	if lk == nil {
		return false
	}
	associationID := r.associationID(src)
	if associationID == "" {
		return true
	}
	policy, ok := r.policyOf(s, associationID)
	if !ok {
		r.denyMissing(src, lk, associationID)
		return false
	}
	if !r.allowsTarget(policy, lk.Host) {
		r.denyTarget(src, lk, associationID)
		return false
	}
	return true
}

func providerDenyMissingChannelTarget(src net.Conn, lk *conn.Link, associationID string) {
	_ = writeChannelConnectResult(src, lk, conn.ConnectResultNotAllowed)
	logs.Warn("deny p2p provider association=%s target=%s because acl runtime is missing", associationID, lk.Host)
}

func providerDenyChannelTarget(src net.Conn, lk *conn.Link, associationID string) {
	_ = writeChannelConnectResult(src, lk, conn.ConnectResultNotAllowed)
	logs.Warn("deny p2p provider association=%s target=%s by whitelist policy", associationID, lk.Host)
}

type clientP2PProviderChannelDialRuntime struct {
	dial func(*TRPClient, *conn.Link) (net.Conn, error)
}

func (r clientP2PProviderChannelDialRuntime) Dial(s *TRPClient, lk *conn.Link) (net.Conn, error) {
	if r.dial == nil {
		return nil, net.InvalidAddrError("nil provider channel dial runtime")
	}
	return r.dial(s, lk)
}

type clientP2PProviderChannelResultRuntime struct {
	write          func(net.Conn, conn.ConnectResultStatus, time.Duration) error
	logWriteFailed func(*conn.Link, error)
}

func (r clientP2PProviderChannelResultRuntime) Write(src net.Conn, lk *conn.Link, result conn.ConnectResultStatus) bool {
	if lk == nil || !lk.Option.WaitConnectResult {
		return true
	}
	if r.write == nil {
		return false
	}
	if err := r.write(src, result, lk.Option.Timeout); err != nil {
		if r.logWriteFailed != nil {
			r.logWriteFailed(lk, err)
		}
		return false
	}
	return true
}

func logProviderChannelConnectResultWriteFailed(lk *conn.Link, err error) {
	if lk == nil || err == nil {
		return
	}
	logs.Warn("write provider connect result for %s failed: %v", lk.Host, err)
}

type clientP2PProviderChannelRelayRuntime struct {
	logRelay func(*conn.Link)
	isFramed func(*conn.Link) bool
	copy     func(net.Conn, net.Conn, *conn.Link, bool)
}

func (r clientP2PProviderChannelRelayRuntime) Relay(src, targetConn net.Conn, lk *conn.Link) {
	if lk == nil {
		return
	}
	if r.logRelay != nil {
		r.logRelay(lk)
	}
	isFramed := false
	if r.isFramed != nil {
		isFramed = r.isFramed(lk)
	}
	if r.copy != nil {
		r.copy(src, targetConn, lk, isFramed)
	}
}

func logProviderChannelRelay(lk *conn.Link) {
	if lk == nil {
		return
	}
	logs.Trace("new %s provider connection with the goal of %s, remote address:%s", lk.ConnType, lk.Host, lk.RemoteAddr)
}

type clientP2PProviderChannelPrepareRuntime struct {
	readLink     func(net.Conn) (*conn.Link, error)
	ackLink      func(net.Conn, *conn.Link) bool
	closeConn    func(net.Conn)
	formatHost   func(string) string
	logReadError func(error)
	logEmptyHost func(*conn.Link)
}

func (r clientP2PProviderChannelPrepareRuntime) Prepare(src net.Conn) (*conn.Link, bool) {
	if r.readLink == nil {
		if r.closeConn != nil {
			r.closeConn(src)
		}
		return nil, false
	}
	lk, err := r.readLink(src)
	if err != nil || lk == nil {
		if r.closeConn != nil {
			r.closeConn(src)
		}
		if r.logReadError != nil {
			r.logReadError(err)
		}
		return nil, false
	}
	if !r.ackLink(src, lk) {
		return nil, false
	}
	if lk.Host == "" {
		if r.logEmptyHost != nil {
			r.logEmptyHost(lk)
		}
		if r.closeConn != nil {
			r.closeConn(src)
		}
		return nil, false
	}
	if r.formatHost != nil {
		lk.Host = r.formatHost(lk.Host)
	}
	return lk, true
}

func closeProviderChannelConn(c net.Conn) {
	if c != nil {
		_ = c.Close()
	}
}

func logProviderChannelReadError(err error) {
	logs.Error("get connection info from server error %v", err)
}

func logProviderChannelEmptyHost(lk *conn.Link) {
	if lk == nil {
		return
	}
	logs.Trace("new %s connection, remote address:%s", lk.ConnType, lk.RemoteAddr)
}

type clientP2PProviderChannelConnectRuntime struct {
	dialTarget         func(*TRPClient, *conn.Link) (net.Conn, error)
	writeConnectResult func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool
	closeSource        func(net.Conn)
	closeTarget        func(net.Conn)
	logDialError       func(*conn.Link, error)
}

func (r clientP2PProviderChannelConnectRuntime) Connect(s *TRPClient, src net.Conn, lk *conn.Link) (net.Conn, bool) {
	if lk == nil || r.dialTarget == nil {
		return nil, false
	}
	targetConn, err := r.dialTarget(s, lk)
	if err == nil && isNilClientNetConn(targetConn) {
		err = net.InvalidAddrError("nil provider channel target conn")
	}
	if err != nil {
		if r.writeConnectResult != nil {
			_ = r.writeConnectResult(src, lk, conn.DialConnectResult(err))
		}
		if r.logDialError != nil {
			r.logDialError(lk, err)
		}
		if r.closeSource != nil {
			r.closeSource(src)
		}
		return nil, false
	}
	if r.writeConnectResult != nil && !r.writeConnectResult(src, lk, conn.ConnectResultOK) {
		if r.closeTarget != nil && !isNilClientNetConn(targetConn) {
			r.closeTarget(targetConn)
		}
		if r.closeSource != nil {
			r.closeSource(src)
		}
		return nil, false
	}
	return targetConn, true
}

func logProviderChannelDialError(lk *conn.Link, err error) {
	if lk == nil || err == nil {
		return
	}
	logs.Warn("connect to %s error %v", lk.Host, err)
}

type clientP2PProviderChannelHandleRuntime struct {
	prepare         func(net.Conn) (*conn.Link, bool)
	handleSpecial   func(*TRPClient, net.Conn, *conn.Link) bool
	authorizeTarget func(*TRPClient, net.Conn, *conn.Link) bool
	connectTarget   func(*TRPClient, net.Conn, *conn.Link) (net.Conn, bool)
	relayTarget     func(net.Conn, net.Conn, *conn.Link)
}

func (r clientP2PProviderChannelHandleRuntime) Handle(s *TRPClient, src net.Conn) {
	if r.prepare == nil || isNilClientNetConn(src) {
		return
	}
	lk, ok := r.prepare(src)
	if !ok || lk == nil {
		return
	}
	if r.handleSpecial(s, src, lk) {
		return
	}
	if !r.authorizeTarget(s, src, lk) {
		_ = src.Close()
		return
	}
	if r.connectTarget == nil {
		return
	}
	targetConn, ok := r.connectTarget(s, src, lk)
	if !ok || isNilClientNetConn(targetConn) {
		return
	}
	r.relayTarget(src, targetConn, lk)
}

func (c *clientP2PProviderRuntimeRoot) ServeTransport(s *TRPClient, session *providerPunchSession) {
	if c == nil || s == nil || session == nil {
		return
	}
	runtime := c.lifecycle.New(s.ctx, session)
	if runtime == nil {
		return
	}
	defer c.lifecycle.Close(runtime)
	if err := c.setup.Setup(runtime, session); err != nil {
		return
	}
	logProviderTransportListen(session)
	if session.mode == common.CONN_QUIC {
		c.quicAccept.Serve(s, runtime, session)
		return
	}
	c.kcpAccept.Serve(s, runtime, session)
}

func logProviderTransportListen(session *providerPunchSession) {
	if session == nil || session.localConn == nil {
		return
	}
	logs.Trace("start local p2p udp[%s] listen, role[%s], local address %s %v", session.mode, session.role, session.preferredLocal, session.localConn.LocalAddr())
	if session.data != "" {
		logs.Trace("P2P udp data is %s", session.data)
	}
}

func signalProviderTransportPreConnDone(runtime *providerTransportRuntime) {
	if runtime == nil {
		return
	}
	runtime.preConnDoneOnce.Do(func() {
		if runtime.preConnDone != nil {
			close(runtime.preConnDone)
		}
	})
}

func (ctx *clientP2PProviderRuntimeRoot) initTransportSupportRuntimeWiring() {
	if ctx == nil {
		return
	}
	ctx.adapters = clientP2PProviderTransportAdapterRuntime{
		wrapQUIC: func(sess *quic.Conn) quicProviderAcceptedSession {
			if sess == nil {
				return nil
			}
			return runtimeAcceptedQUICSession{sess: sess}
		},
		wrapKCP: func(tunnel *kcp.UDPSession) kcpProviderAcceptedTunnel {
			if tunnel == nil {
				return nil
			}
			return runtimeAcceptedKCPTunnel{tunnel: tunnel}
		},
	}
	ctx.streams = clientP2PProviderQUICStreamRuntime{
		acceptStream: func(runtime *providerTransportRuntime, sess quicProviderAcceptedSession) (*quic.Stream, error) {
			return sess.AcceptStream(runtime.ctx)
		},
		buildConn: func(sess quicProviderAcceptedSession, stream *quic.Stream) net.Conn {
			if sess == nil {
				return nil
			}
			return sess.BuildConn(stream)
		},
	}
	ctx.kcpMux = clientP2PProviderKCPMuxRuntime{
		setUDPSession: conn.SetUdpSession,
		logEstablished: func(remote net.Addr) {
			logs.Trace("successful connection with client ,address %v", remote)
		},
		newMux: func(s *TRPClient, tunnel *kcp.UDPSession) providerAcceptedMux {
			return mux.NewMux(tunnel, "kcp", s.disconnectTime, true)
		},
		acceptMux: func(listener providerAcceptedMux, handler func(net.Conn)) {
			conn.Accept(listener, handler)
		},
		dispatchConn: func(s *TRPClient, session *providerPunchSession, c net.Conn) {
			ctx.dispatchChannel(s, session, c)
		},
		logClosed: func(remote net.Addr) {
			logs.Trace("P2P connection closed, remote %v", remote)
		},
	}
	ctx.quicServe = clientP2PProviderQUICServeRuntime{
		nextConn: func(runtime *providerTransportRuntime, sess quicProviderAcceptedSession) (net.Conn, error) {
			return ctx.streams.NextConn(runtime, sess)
		},
		reportStreamError: func(sess quicProviderAcceptedSession, err error) {
			logs.Trace("QUIC accept stream error: %v", err)
			if sess != nil {
				_ = sess.CloseWithError(0, "accept stream error")
			}
		},
		dispatchConn: func(s *TRPClient, session *providerPunchSession, c net.Conn) {
			ctx.dispatchChannel(s, session, c)
		},
	}
	ctx.kcpServe = clientP2PProviderKCPServeRuntime{
		serveMux: func(s *TRPClient, session *providerPunchSession, tunnel kcpProviderAcceptedTunnel) {
			if tunnel == nil {
				return
			}
			ctx.kcpMux.Serve(s, session, tunnel.Session())
		},
	}
}

func (ctx *clientP2PProviderRuntimeRoot) initLifecycleRuntimeWiring() {
	if ctx == nil {
		return
	}
	ctx.lifecycle = clientP2PProviderLifecycleRuntime{
		deriveContext: context.WithCancel,
		newTimeout: func(session *providerPunchSession, cancel context.CancelFunc) *time.Timer {
			return time.AfterFunc(session.transportTimeout, cancel)
		},
		newWatchdog: func(session *providerPunchSession) *time.Timer {
			return time.AfterFunc(session.transportTimeout, func() {
				logs.Warn("P2P provider punch timeout, closing local socket %v", session.localConn.LocalAddr())
				_ = session.localConn.Close()
			})
		},
		stopWatchdog: func(runtime *providerTransportRuntime) {
			if runtime != nil && runtime.watchdog != nil {
				runtime.watchdog.Stop()
			}
		},
		stopTimer: func(runtime *providerTransportRuntime) {
			if runtime != nil && runtime.timer != nil {
				runtime.timer.Stop()
			}
		},
		signalPreConn: signalProviderTransportPreConnDone,
		closeQUIC: func(runtime *providerTransportRuntime) {
			if runtime != nil && runtime.quicListener != nil {
				_ = runtime.quicListener.Close()
			}
		},
		closeKCP: func(runtime *providerTransportRuntime) {
			if runtime != nil && runtime.kcpListener != nil {
				_ = runtime.kcpListener.Close()
			}
		},
		cancel: func(runtime *providerTransportRuntime) {
			if runtime != nil && runtime.cancel != nil {
				runtime.cancel()
			}
		},
	}
	ctx.setup = clientP2PProviderSetupRuntime{
		listenQUIC: func(session *providerPunchSession) (*quic.Listener, error) {
			return quic.Listen(session.localConn, crypt.GetCertCfg(), QuicConfig)
		},
		listenKCP: func(session *providerPunchSession) (*kcp.Listener, error) {
			return kcp.ServeConn(nil, 10, 3, session.localConn)
		},
		bindKCPListener: func(runtime *providerTransportRuntime, listener *kcp.Listener) {
			if runtime == nil {
				return
			}
			runtime.preConnDone = make(chan struct{})
			go func() {
				select {
				case <-runtime.ctx.Done():
					_ = listener.Close()
				case <-runtime.preConnDone:
					return
				}
			}()
		},
		reportSetupErr: func(session *providerPunchSession, stage string, err error) {
			logs.Error("%s err: %v", stage, err)
			writeP2PTransportFailure(session.controlConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddress, stage)
		},
	}
	ctx.promotion = clientP2PProviderPromotionRuntime{
		stopWatchdog: func(runtime *providerTransportRuntime) bool {
			return runtime != nil && runtime.watchdog != nil && runtime.watchdog.Stop()
		},
		stopTimer: func(runtime *providerTransportRuntime) bool {
			return runtime != nil && runtime.timer != nil && runtime.timer.Stop()
		},
		writeEstablished: func(session *providerPunchSession) {
			if session == nil {
				return
			}
			writeP2PTransportProgress(session.controlConn, session.sessionID, session.role, "transport_established", "ok", session.preferredLocal, session.remoteAddress, session.mode, nil)
		},
		closeControl: func(session *providerPunchSession) {
			if session != nil {
				session.closeControl()
			}
		},
		signalPreConn: signalProviderTransportPreConnDone,
	}
	ctx.quicAccept = clientP2PProviderQUICAcceptRuntime{
		contextOf: func(runtime *providerTransportRuntime) context.Context {
			return runtime.ctx
		},
		acceptSession: func(runtime *providerTransportRuntime) (quicProviderAcceptedSession, error) {
			sess, err := runtime.quicListener.Accept(runtime.ctx)
			if err != nil {
				return nil, err
			}
			return ctx.adapters.wrapQUIC(sess), nil
		},
		reportAcceptError: func(session *providerPunchSession, err error) {
			logs.Warn("QUIC accept session error: %v", err)
			writeP2PTransportFailure(session.controlConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddress, "quic_accept")
		},
		remoteMatches: func(session *providerPunchSession, remote net.Addr) bool {
			return session != nil && remote != nil && remote.String() == session.remoteAddress
		},
		rejectUnexpected: func(sess quicProviderAcceptedSession) {
			if sess != nil {
				_ = sess.CloseWithError(0, "unexpected peer")
			}
		},
		promote: func(runtime *providerTransportRuntime, session *providerPunchSession, sess quicProviderSessionCloser) bool {
			return ctx.promotion.PromoteQUIC(runtime, session, sess)
		},
		serveEstablished: func(s *TRPClient, runtime *providerTransportRuntime, session *providerPunchSession, sess quicProviderAcceptedSession) {
			ctx.quicServe.Serve(s, runtime, session, sess)
		},
	}
	ctx.kcpAccept = clientP2PProviderKCPAcceptRuntime{
		contextOf: func(runtime *providerTransportRuntime) context.Context {
			return runtime.ctx
		},
		acceptTunnel: func(runtime *providerTransportRuntime) (kcpProviderAcceptedTunnel, error) {
			tunnel, err := runtime.kcpListener.AcceptKCP()
			if err != nil {
				return nil, err
			}
			return ctx.adapters.wrapKCP(tunnel), nil
		},
		reportAcceptError: func(session *providerPunchSession, localAddr net.Addr, err error) {
			logs.Error("acceptKCP failed on listener %v waiting for remote %s: %v", localAddr, session.remoteAddress, err)
			writeP2PTransportFailure(session.controlConn, session.sessionID, session.role, session.preferredLocal, session.remoteAddress, "kcp_accept")
		},
		remoteMatches: func(session *providerPunchSession, remote net.Addr) bool {
			return session != nil && remote != nil && remote.String() == session.remoteAddress
		},
		rejectUnexpected: func(tunnel kcpProviderAcceptedTunnel) {
			if tunnel != nil {
				_ = tunnel.Close()
			}
		},
		promote: func(runtime *providerTransportRuntime, session *providerPunchSession, tunnel providerTunnelCloser) bool {
			return ctx.promotion.PromoteKCP(runtime, session, tunnel)
		},
		serveEstablished: func(s *TRPClient, session *providerPunchSession, tunnel kcpProviderAcceptedTunnel) {
			ctx.kcpServe.Serve(s, session, tunnel)
		},
	}
}

type quicProviderAcceptedSession interface {
	quicProviderSessionCloser
	RemoteAddr() net.Addr
	AcceptStream(context.Context) (*quic.Stream, error)
	BuildConn(*quic.Stream) net.Conn
}

type kcpProviderAcceptedTunnel interface {
	providerTunnelCloser
	RemoteAddr() net.Addr
	Session() *kcp.UDPSession
}

type clientP2PProviderTransportAdapterRuntime struct {
	wrapQUIC func(*quic.Conn) quicProviderAcceptedSession
	wrapKCP  func(*kcp.UDPSession) kcpProviderAcceptedTunnel
}

type runtimeAcceptedQUICSession struct {
	sess *quic.Conn
}

type runtimeAcceptedKCPTunnel struct {
	tunnel *kcp.UDPSession
}

func (s runtimeAcceptedQUICSession) RemoteAddr() net.Addr {
	if s.sess == nil {
		return nil
	}
	return s.sess.RemoteAddr()
}

func (s runtimeAcceptedQUICSession) AcceptStream(ctx context.Context) (*quic.Stream, error) {
	if s.sess == nil {
		return nil, net.InvalidAddrError("missing quic session")
	}
	return s.sess.AcceptStream(ctx)
}

func (s runtimeAcceptedQUICSession) CloseWithError(code quic.ApplicationErrorCode, reason string) error {
	if s.sess == nil {
		return nil
	}
	return s.sess.CloseWithError(code, reason)
}

func (s runtimeAcceptedQUICSession) BuildConn(stream *quic.Stream) net.Conn {
	if s.sess == nil || stream == nil {
		return nil
	}
	return conn.NewQuicStreamConn(stream, s.sess)
}

func (t runtimeAcceptedKCPTunnel) RemoteAddr() net.Addr {
	if t.tunnel == nil {
		return nil
	}
	return t.tunnel.RemoteAddr()
}

func (t runtimeAcceptedKCPTunnel) Close() error {
	if t.tunnel == nil {
		return nil
	}
	return t.tunnel.Close()
}

func (t runtimeAcceptedKCPTunnel) Session() *kcp.UDPSession {
	return t.tunnel
}

type clientP2PProviderQUICStreamRuntime struct {
	acceptStream func(*providerTransportRuntime, quicProviderAcceptedSession) (*quic.Stream, error)
	buildConn    func(quicProviderAcceptedSession, *quic.Stream) net.Conn
}

func (r clientP2PProviderQUICStreamRuntime) NextConn(runtime *providerTransportRuntime, sess quicProviderAcceptedSession) (net.Conn, error) {
	if sess == nil {
		return nil, net.InvalidAddrError("missing quic session")
	}
	stream, err := r.acceptStream(runtime, sess)
	if err != nil {
		return nil, err
	}
	return r.buildConn(sess, stream), nil
}

type providerAcceptedMux interface {
	net.Listener
	Close() error
}

type clientP2PProviderKCPMuxRuntime struct {
	setUDPSession  func(*kcp.UDPSession)
	logEstablished func(net.Addr)
	newMux         func(*TRPClient, *kcp.UDPSession) providerAcceptedMux
	acceptMux      func(providerAcceptedMux, func(net.Conn))
	dispatchConn   func(*TRPClient, *providerPunchSession, net.Conn)
	logClosed      func(net.Addr)
}

func (r clientP2PProviderKCPMuxRuntime) Serve(s *TRPClient, session *providerPunchSession, tunnel *kcp.UDPSession) {
	if tunnel == nil {
		return
	}
	r.setUDPSession(tunnel)
	r.logEstablished(tunnel.RemoteAddr())
	mx := r.newMux(s, tunnel)
	if mx == nil {
		return
	}
	defer func() { _ = mx.Close() }()
	r.acceptMux(mx, func(c net.Conn) {
		r.dispatchConn(s, session, c)
	})
	r.logClosed(tunnel.RemoteAddr())
}

type clientP2PProviderQUICServeRuntime struct {
	nextConn          func(*providerTransportRuntime, quicProviderAcceptedSession) (net.Conn, error)
	reportStreamError func(quicProviderAcceptedSession, error)
	dispatchConn      func(*TRPClient, *providerPunchSession, net.Conn)
}

type clientP2PProviderKCPServeRuntime struct {
	serveMux func(*TRPClient, *providerPunchSession, kcpProviderAcceptedTunnel)
}

func (r clientP2PProviderQUICServeRuntime) Serve(s *TRPClient, runtime *providerTransportRuntime, session *providerPunchSession, sess quicProviderAcceptedSession) {
	for {
		c, err := r.nextConn(runtime, sess)
		if err != nil {
			r.reportStreamError(sess, err)
			return
		}
		r.dispatchConn(s, session, c)
	}
}

func (r clientP2PProviderKCPServeRuntime) Serve(s *TRPClient, session *providerPunchSession, tunnel kcpProviderAcceptedTunnel) {
	if tunnel == nil || r.serveMux == nil {
		return
	}
	r.serveMux(s, session, tunnel)
}

type clientP2PProviderLifecycleRuntime struct {
	deriveContext func(context.Context) (context.Context, context.CancelFunc)
	newTimeout    func(*providerPunchSession, context.CancelFunc) *time.Timer
	newWatchdog   func(*providerPunchSession) *time.Timer
	stopWatchdog  func(*providerTransportRuntime)
	stopTimer     func(*providerTransportRuntime)
	signalPreConn func(*providerTransportRuntime)
	closeQUIC     func(*providerTransportRuntime)
	closeKCP      func(*providerTransportRuntime)
	cancel        func(*providerTransportRuntime)
}

func (r clientP2PProviderLifecycleRuntime) New(parent context.Context, session *providerPunchSession) *providerTransportRuntime {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := r.deriveContext(parent)
	return &providerTransportRuntime{
		ctx:      ctx,
		cancel:   cancel,
		timer:    r.newTimeout(session, cancel),
		watchdog: r.newWatchdog(session),
	}
}

func (r clientP2PProviderLifecycleRuntime) Close(runtime *providerTransportRuntime) {
	if runtime == nil {
		return
	}
	r.stopWatchdog(runtime)
	r.stopTimer(runtime)
	r.signalPreConn(runtime)
	r.closeQUIC(runtime)
	r.closeKCP(runtime)
	r.cancel(runtime)
}

type clientP2PProviderSetupRuntime struct {
	listenQUIC      func(*providerPunchSession) (*quic.Listener, error)
	listenKCP       func(*providerPunchSession) (*kcp.Listener, error)
	bindKCPListener func(*providerTransportRuntime, *kcp.Listener)
	reportSetupErr  func(*providerPunchSession, string, error)
}

func (r clientP2PProviderSetupRuntime) Setup(runtime *providerTransportRuntime, session *providerPunchSession) error {
	if session == nil || runtime == nil {
		return nil
	}
	if session.mode == common.CONN_QUIC {
		listener, err := r.listenQUIC(session)
		if err != nil {
			r.reportSetupErr(session, "quic_listen", err)
			return err
		}
		runtime.quicListener = listener
		return nil
	}
	listener, err := r.listenKCP(session)
	if err != nil {
		r.reportSetupErr(session, "kcp_listen", err)
		return err
	}
	runtime.kcpListener = listener
	r.bindKCPListener(runtime, listener)
	return nil
}

type quicProviderSessionCloser interface {
	CloseWithError(quic.ApplicationErrorCode, string) error
}

type providerTunnelCloser interface {
	Close() error
}

type clientP2PProviderPromotionRuntime struct {
	stopWatchdog     func(*providerTransportRuntime) bool
	stopTimer        func(*providerTransportRuntime) bool
	writeEstablished func(*providerPunchSession)
	closeControl     func(*providerPunchSession)
	signalPreConn    func(*providerTransportRuntime)
}

func (r clientP2PProviderPromotionRuntime) PromoteQUIC(runtime *providerTransportRuntime, session *providerPunchSession, sess quicProviderSessionCloser) bool {
	if !r.stopWatchdog(runtime) {
		logs.Warn("QUIC watchdog already fired, abort promoting accepted session")
		if sess != nil {
			_ = sess.CloseWithError(0, "watchdog already fired")
		}
		return false
	}
	if !r.stopTimer(runtime) {
		logs.Warn("QUIC pre-connection timer already fired")
		if sess != nil {
			_ = sess.CloseWithError(0, "timer already fired")
		}
		return false
	}
	r.writeEstablished(session)
	r.closeControl(session)
	return true
}

func (r clientP2PProviderPromotionRuntime) PromoteKCP(runtime *providerTransportRuntime, session *providerPunchSession, tunnel providerTunnelCloser) bool {
	if !r.stopWatchdog(runtime) {
		logs.Warn("KCP watchdog already fired, abort promoting accepted tunnel")
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return false
	}
	if !r.stopTimer(runtime) {
		logs.Warn("KCP pre-connection timer already fired")
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return false
	}
	r.writeEstablished(session)
	r.closeControl(session)
	r.signalPreConn(runtime)
	return true
}

type providerTransportRuntime struct {
	ctx             context.Context
	cancel          context.CancelFunc
	timer           *time.Timer
	watchdog        *time.Timer
	kcpListener     *kcp.Listener
	quicListener    *quic.Listener
	preConnDone     chan struct{}
	preConnDoneOnce sync.Once
}

type clientP2PProviderQUICAcceptRuntime struct {
	contextOf         func(*providerTransportRuntime) context.Context
	acceptSession     func(*providerTransportRuntime) (quicProviderAcceptedSession, error)
	reportAcceptError func(*providerPunchSession, error)
	remoteMatches     func(*providerPunchSession, net.Addr) bool
	rejectUnexpected  func(quicProviderAcceptedSession)
	promote           func(*providerTransportRuntime, *providerPunchSession, quicProviderSessionCloser) bool
	serveEstablished  func(*TRPClient, *providerTransportRuntime, *providerPunchSession, quicProviderAcceptedSession)
}

type clientP2PProviderKCPAcceptRuntime struct {
	contextOf         func(*providerTransportRuntime) context.Context
	acceptTunnel      func(*providerTransportRuntime) (kcpProviderAcceptedTunnel, error)
	reportAcceptError func(*providerPunchSession, net.Addr, error)
	remoteMatches     func(*providerPunchSession, net.Addr) bool
	rejectUnexpected  func(kcpProviderAcceptedTunnel)
	promote           func(*providerTransportRuntime, *providerPunchSession, providerTunnelCloser) bool
	serveEstablished  func(*TRPClient, *providerPunchSession, kcpProviderAcceptedTunnel)
}

func (r clientP2PProviderQUICAcceptRuntime) Serve(s *TRPClient, runtime *providerTransportRuntime, session *providerPunchSession) {
	for {
		select {
		case <-r.contextOf(runtime).Done():
			return
		default:
		}
		sess, err := r.acceptSession(runtime)
		if err != nil {
			r.reportAcceptError(session, err)
			return
		}
		if !r.remoteMatches(session, sess.RemoteAddr()) {
			r.rejectUnexpected(sess)
			continue
		}
		if !r.promote(runtime, session, sess) {
			return
		}
		r.serveEstablished(s, runtime, session, sess)
		return
	}
}

func (r clientP2PProviderKCPAcceptRuntime) Serve(s *TRPClient, runtime *providerTransportRuntime, session *providerPunchSession) {
	for {
		select {
		case <-r.contextOf(runtime).Done():
			return
		default:
		}
		tunnel, err := r.acceptTunnel(runtime)
		if err != nil {
			var localAddr net.Addr
			if session != nil && session.localConn != nil {
				localAddr = session.localConn.LocalAddr()
			}
			r.reportAcceptError(session, localAddr, err)
			return
		}
		if !r.remoteMatches(session, tunnel.RemoteAddr()) {
			r.rejectUnexpected(tunnel)
			continue
		}
		if !r.promote(runtime, session, tunnel) {
			return
		}
		r.serveEstablished(s, session, tunnel)
		return
	}
}
