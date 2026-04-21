package client

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

func TestNewClientP2PProviderRuntimeRootBuildsDefaultWiring(t *testing.T) {
	ctx := newClientP2PProviderRuntimeRoot()
	if ctx == nil {
		t.Fatal("newClientP2PProviderRuntimeRoot() = nil")
	}
	if ctx.lifecycle.deriveContext == nil || ctx.lifecycle.newTimeout == nil || ctx.lifecycle.newWatchdog == nil {
		t.Fatal("lifecycle runtime is not fully wired")
	}
	if ctx.setup.listenQUIC == nil || ctx.setup.listenKCP == nil || ctx.setup.bindKCPListener == nil {
		t.Fatal("setup runtime is not fully wired")
	}
	if ctx.promotion.stopWatchdog == nil || ctx.promotion.stopTimer == nil || ctx.promotion.signalPreConn == nil {
		t.Fatal("promotion runtime is not fully wired")
	}
	if ctx.adapters.wrapQUIC == nil || ctx.adapters.wrapKCP == nil {
		t.Fatal("transport adapters are not fully wired")
	}
	if ctx.channelSpecial.handleUDP5 == nil || ctx.channelSpecial.handleFile == nil || ctx.channelAuthorization.associationID == nil || ctx.channelAuthorization.policyOf == nil || ctx.channelDial.dial == nil || ctx.channelResult.write == nil || ctx.channelRelay.copy == nil || ctx.channelPrepare.readLink == nil || ctx.channelPrepare.ackLink == nil || ctx.channelConnect.dialTarget == nil || ctx.channelConnect.writeConnectResult == nil || ctx.channelLaunchAsync == nil {
		t.Fatal("provider channel stage runtimes are not fully wired")
	}
	if ctx.channelHandle.prepare == nil || ctx.channelHandle.authorizeTarget == nil || ctx.channelHandle.connectTarget == nil || ctx.channelHandle.relayTarget == nil {
		t.Fatal("provider channel handle runtime is not fully wired")
	}
	if ctx.streams.acceptStream == nil || ctx.streams.buildConn == nil {
		t.Fatal("quic stream runtime is not fully wired")
	}
	if ctx.kcpMux.setUDPSession == nil || ctx.kcpMux.newMux == nil || ctx.kcpMux.acceptMux == nil || ctx.kcpMux.dispatchConn == nil {
		t.Fatal("kcp mux runtime is not fully wired")
	}
	if ctx.quicServe.nextConn == nil || ctx.quicServe.reportStreamError == nil || ctx.quicServe.dispatchConn == nil {
		t.Fatal("quic serve runtime is not fully wired")
	}
	if ctx.kcpServe.serveMux == nil {
		t.Fatal("kcp serve runtime is not fully wired")
	}
	if ctx.quicAccept.acceptSession == nil || ctx.quicAccept.promote == nil || ctx.quicAccept.serveEstablished == nil {
		t.Fatal("quic accept runtime is not fully wired")
	}
	if ctx.kcpAccept.acceptTunnel == nil || ctx.kcpAccept.promote == nil || ctx.kcpAccept.serveEstablished == nil {
		t.Fatal("kcp accept runtime is not fully wired")
	}
}

type stubQUICProviderSession struct {
	closeReasons []string
}

func (s *stubQUICProviderSession) CloseWithError(_ quic.ApplicationErrorCode, reason string) error {
	s.closeReasons = append(s.closeReasons, reason)
	return nil
}

type stubProviderTunnel struct {
	closed int
}

func (t *stubProviderTunnel) Close() error {
	t.closed++
	return nil
}

type stubAcceptedQUICSession struct {
	remoteAddr   net.Addr
	closeReasons []string
}

func (s *stubAcceptedQUICSession) RemoteAddr() net.Addr {
	return s.remoteAddr
}

func (s *stubAcceptedQUICSession) AcceptStream(context.Context) (*quic.Stream, error) {
	return nil, errors.New("not used")
}

func (s *stubAcceptedQUICSession) BuildConn(*quic.Stream) net.Conn {
	return nil
}

func (s *stubAcceptedQUICSession) CloseWithError(_ quic.ApplicationErrorCode, reason string) error {
	s.closeReasons = append(s.closeReasons, reason)
	return nil
}

type stubAcceptedKCPTunnel struct {
	remoteAddr net.Addr
	closed     int
}

func (t *stubAcceptedKCPTunnel) RemoteAddr() net.Addr {
	return t.remoteAddr
}

func (t *stubAcceptedKCPTunnel) Close() error {
	t.closed++
	return nil
}

func (t *stubAcceptedKCPTunnel) Session() *kcp.UDPSession {
	return nil
}

func TestClientP2PProviderLifecycleRuntimeNewBuildsRuntimeState(t *testing.T) {
	timeoutTimer := time.NewTimer(time.Hour)
	watchdogTimer := time.NewTimer(time.Hour)
	t.Cleanup(func() {
		timeoutTimer.Stop()
		watchdogTimer.Stop()
	})

	derivedCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runtime := clientP2PProviderLifecycleRuntime{
		deriveContext: func(parent context.Context) (context.Context, context.CancelFunc) {
			return derivedCtx, cancel
		},
		newTimeout: func(*providerPunchSession, context.CancelFunc) *time.Timer {
			return timeoutTimer
		},
		newWatchdog: func(*providerPunchSession) *time.Timer {
			return watchdogTimer
		},
		stopWatchdog:  func(*providerTransportRuntime) {},
		stopTimer:     func(*providerTransportRuntime) {},
		signalPreConn: func(*providerTransportRuntime) {},
		closeQUIC:     func(*providerTransportRuntime) {},
		closeKCP:      func(*providerTransportRuntime) {},
		cancel:        func(*providerTransportRuntime) {},
	}

	state := runtime.New(context.Background(), &providerPunchSession{})
	if state.ctx != derivedCtx {
		t.Fatalf("New() ctx = %#v, want %#v", state.ctx, derivedCtx)
	}
	if state.cancel == nil {
		t.Fatal("New() cancel = nil, want non-nil")
	}
	if state.timer != timeoutTimer || state.watchdog != watchdogTimer {
		t.Fatal("New() did not wire timeout/watchdog timers")
	}
}

func TestClientP2PProviderLifecycleRuntimeNewHandlesNilParentContext(t *testing.T) {
	timeoutTimer := time.NewTimer(time.Hour)
	watchdogTimer := time.NewTimer(time.Hour)
	t.Cleanup(func() {
		timeoutTimer.Stop()
		watchdogTimer.Stop()
	})

	derivedCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var receivedParent context.Context
	runtime := clientP2PProviderLifecycleRuntime{
		deriveContext: func(parent context.Context) (context.Context, context.CancelFunc) {
			receivedParent = parent
			return derivedCtx, cancel
		},
		newTimeout: func(*providerPunchSession, context.CancelFunc) *time.Timer {
			return timeoutTimer
		},
		newWatchdog: func(*providerPunchSession) *time.Timer {
			return watchdogTimer
		},
		stopWatchdog:  func(*providerTransportRuntime) {},
		stopTimer:     func(*providerTransportRuntime) {},
		signalPreConn: func(*providerTransportRuntime) {},
		closeQUIC:     func(*providerTransportRuntime) {},
		closeKCP:      func(*providerTransportRuntime) {},
		cancel:        func(*providerTransportRuntime) {},
	}

	state := runtime.New(nil, &providerPunchSession{})
	if receivedParent == nil {
		t.Fatal("New(nil, ...) forwarded nil parent context to deriveContext")
	}
	if state.ctx != derivedCtx {
		t.Fatalf("New(nil, ...) ctx = %#v, want %#v", state.ctx, derivedCtx)
	}
}

func TestClientP2PProviderLifecycleRuntimeCloseRunsCleanupSteps(t *testing.T) {
	steps := make([]string, 0, 6)
	runtime := clientP2PProviderLifecycleRuntime{
		deriveContext: context.WithCancel,
		newTimeout: func(*providerPunchSession, context.CancelFunc) *time.Timer {
			return nil
		},
		newWatchdog: func(*providerPunchSession) *time.Timer {
			return nil
		},
		stopWatchdog: func(*providerTransportRuntime) {
			steps = append(steps, "watchdog")
		},
		stopTimer: func(*providerTransportRuntime) {
			steps = append(steps, "timer")
		},
		signalPreConn: func(*providerTransportRuntime) {
			steps = append(steps, "preconn")
		},
		closeQUIC: func(*providerTransportRuntime) {
			steps = append(steps, "quic")
		},
		closeKCP: func(*providerTransportRuntime) {
			steps = append(steps, "kcp")
		},
		cancel: func(*providerTransportRuntime) {
			steps = append(steps, "cancel")
		},
	}

	runtime.Close(&providerTransportRuntime{})

	want := []string{"watchdog", "timer", "preconn", "quic", "kcp", "cancel"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Close() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderLifecycleRuntimeSignalPreConnDoneClosesOnce(t *testing.T) {
	state := &providerTransportRuntime{preConnDone: make(chan struct{})}

	signalProviderTransportPreConnDone(state)
	select {
	case <-state.preConnDone:
	default:
		t.Fatal("SignalPreConnDone() did not close channel")
	}

	signalProviderTransportPreConnDone(state)
}

func TestClientP2PProviderSetupRuntimeConfiguresQUICListener(t *testing.T) {
	listener := &quic.Listener{}
	var reported []string
	runtime := clientP2PProviderSetupRuntime{
		listenQUIC: func(*providerPunchSession) (*quic.Listener, error) {
			return listener, nil
		},
		listenKCP: func(*providerPunchSession) (*kcp.Listener, error) {
			t.Fatal("Setup() unexpectedly called listenKCP for quic mode")
			return nil, nil
		},
		bindKCPListener: func(*providerTransportRuntime, *kcp.Listener) {
			t.Fatal("Setup() unexpectedly bound kcp listener for quic mode")
		},
		reportSetupErr: func(_ *providerPunchSession, stage string, err error) {
			reported = append(reported, stage)
		},
	}

	state := &providerTransportRuntime{ctx: context.Background()}
	err := runtime.Setup(state, &providerPunchSession{mode: common.CONN_QUIC})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if state.quicListener != listener {
		t.Fatalf("Setup() quicListener = %#v, want %#v", state.quicListener, listener)
	}
	if len(reported) != 0 {
		t.Fatalf("Setup() reported errors = %v, want none", reported)
	}
}

func TestClientP2PProviderSetupRuntimeConfiguresKCPListenerAndPreConn(t *testing.T) {
	listener := &kcp.Listener{}
	bound := false
	runtime := clientP2PProviderSetupRuntime{
		listenQUIC: func(*providerPunchSession) (*quic.Listener, error) {
			t.Fatal("Setup() unexpectedly called listenQUIC for kcp mode")
			return nil, nil
		},
		listenKCP: func(*providerPunchSession) (*kcp.Listener, error) {
			return listener, nil
		},
		bindKCPListener: func(state *providerTransportRuntime, got *kcp.Listener) {
			bound = true
			state.preConnDone = make(chan struct{})
			if got != listener {
				t.Fatalf("bindKCPListener() listener = %#v, want %#v", got, listener)
			}
		},
		reportSetupErr: func(_ *providerPunchSession, stage string, err error) {
			t.Fatalf("Setup() unexpectedly reported %s: %v", stage, err)
		},
	}

	state := &providerTransportRuntime{ctx: context.Background()}
	err := runtime.Setup(state, &providerPunchSession{mode: common.CONN_KCP})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if state.kcpListener != listener {
		t.Fatalf("Setup() kcpListener = %#v, want %#v", state.kcpListener, listener)
	}
	if state.preConnDone == nil || !bound {
		t.Fatal("Setup() did not bind KCP listener / preConnDone")
	}
}

func TestClientP2PProviderSetupRuntimeReportsSetupError(t *testing.T) {
	expectedErr := errors.New("listen failed")
	var (
		reportedStage string
		reportedErr   error
	)
	runtime := clientP2PProviderSetupRuntime{
		listenQUIC: func(*providerPunchSession) (*quic.Listener, error) {
			return nil, expectedErr
		},
		listenKCP: func(*providerPunchSession) (*kcp.Listener, error) {
			return nil, nil
		},
		bindKCPListener: func(*providerTransportRuntime, *kcp.Listener) {},
		reportSetupErr: func(_ *providerPunchSession, stage string, err error) {
			reportedStage = stage
			reportedErr = err
		},
	}

	err := runtime.Setup(&providerTransportRuntime{ctx: context.Background()}, &providerPunchSession{mode: common.CONN_QUIC})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Setup() error = %v, want %v", err, expectedErr)
	}
	if reportedStage != "quic_listen" || !errors.Is(reportedErr, expectedErr) {
		t.Fatalf("Setup() reported (%q, %v), want (quic_listen, %v)", reportedStage, reportedErr, expectedErr)
	}
}

func TestClientP2PProviderPromotionRuntimePromoteQUICSuccess(t *testing.T) {
	steps := make([]string, 0, 4)
	runtime := clientP2PProviderPromotionRuntime{
		stopWatchdog: func(*providerTransportRuntime) bool {
			steps = append(steps, "watchdog")
			return true
		},
		stopTimer: func(*providerTransportRuntime) bool {
			steps = append(steps, "timer")
			return true
		},
		writeEstablished: func(*providerPunchSession) {
			steps = append(steps, "established")
		},
		closeControl: func(*providerPunchSession) {
			steps = append(steps, "control")
		},
		signalPreConn: func(*providerTransportRuntime) {
			steps = append(steps, "preconn")
		},
	}

	ok := runtime.PromoteQUIC(&providerTransportRuntime{}, &providerPunchSession{}, &stubQUICProviderSession{})
	if !ok {
		t.Fatal("PromoteQUIC() = false, want true")
	}
	want := []string{"watchdog", "timer", "established", "control"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("PromoteQUIC() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderPromotionRuntimePromoteQUICClosesLateSession(t *testing.T) {
	session := &stubQUICProviderSession{}
	wrote := false
	runtime := clientP2PProviderPromotionRuntime{
		stopWatchdog: func(*providerTransportRuntime) bool {
			return false
		},
		stopTimer: func(*providerTransportRuntime) bool {
			return true
		},
		writeEstablished: func(*providerPunchSession) {
			wrote = true
		},
		closeControl:  func(*providerPunchSession) {},
		signalPreConn: func(*providerTransportRuntime) {},
	}

	ok := runtime.PromoteQUIC(&providerTransportRuntime{}, &providerPunchSession{}, session)
	if ok {
		t.Fatal("PromoteQUIC() = true, want false when watchdog already fired")
	}
	if wrote {
		t.Fatal("PromoteQUIC() unexpectedly wrote established progress on failure")
	}
	if !reflect.DeepEqual(session.closeReasons, []string{"watchdog already fired"}) {
		t.Fatalf("PromoteQUIC() close reasons = %v, want watchdog reason", session.closeReasons)
	}
}

func TestClientP2PProviderPromotionRuntimePromoteKCPSignalsPreConnOnSuccess(t *testing.T) {
	steps := make([]string, 0, 5)
	tunnel := &stubProviderTunnel{}
	runtime := clientP2PProviderPromotionRuntime{
		stopWatchdog: func(*providerTransportRuntime) bool {
			steps = append(steps, "watchdog")
			return true
		},
		stopTimer: func(*providerTransportRuntime) bool {
			steps = append(steps, "timer")
			return true
		},
		writeEstablished: func(*providerPunchSession) {
			steps = append(steps, "established")
		},
		closeControl: func(*providerPunchSession) {
			steps = append(steps, "control")
		},
		signalPreConn: func(*providerTransportRuntime) {
			steps = append(steps, "preconn")
		},
	}

	ok := runtime.PromoteKCP(&providerTransportRuntime{}, &providerPunchSession{}, tunnel)
	if !ok {
		t.Fatal("PromoteKCP() = false, want true")
	}
	if tunnel.closed != 0 {
		t.Fatalf("PromoteKCP() closed tunnel %d times, want 0", tunnel.closed)
	}
	want := []string{"watchdog", "timer", "established", "control", "preconn"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("PromoteKCP() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderRuntimeRootServeTransportUsesQUICPath(t *testing.T) {
	steps := make([]string, 0, 4)
	root := &clientP2PProviderRuntimeRoot{
		lifecycle: clientP2PProviderLifecycleRuntime{
			deriveContext: context.WithCancel,
			newTimeout: func(*providerPunchSession, context.CancelFunc) *time.Timer {
				return nil
			},
			newWatchdog: func(*providerPunchSession) *time.Timer {
				return nil
			},
			stopWatchdog: func(*providerTransportRuntime) {
				steps = append(steps, "close")
			},
			stopTimer:     func(*providerTransportRuntime) {},
			signalPreConn: func(*providerTransportRuntime) {},
			closeQUIC:     func(*providerTransportRuntime) {},
			closeKCP:      func(*providerTransportRuntime) {},
			cancel:        func(*providerTransportRuntime) {},
		},
		setup: clientP2PProviderSetupRuntime{
			listenQUIC: func(*providerPunchSession) (*quic.Listener, error) {
				return nil, nil
			},
			listenKCP: func(*providerPunchSession) (*kcp.Listener, error) {
				return nil, nil
			},
			bindKCPListener: func(*providerTransportRuntime, *kcp.Listener) {},
			reportSetupErr:  func(*providerPunchSession, string, error) {},
		},
		quicAccept: clientP2PProviderQUICAcceptRuntime{
			contextOf: func(*providerTransportRuntime) context.Context {
				return context.Background()
			},
			acceptSession: func(*providerTransportRuntime) (quicProviderAcceptedSession, error) {
				return &stubAcceptedQUICSession{
					remoteAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9000},
				}, nil
			},
			reportAcceptError: func(*providerPunchSession, error) {},
			remoteMatches:     func(*providerPunchSession, net.Addr) bool { return true },
			rejectUnexpected:  func(quicProviderAcceptedSession) {},
			promote: func(*providerTransportRuntime, *providerPunchSession, quicProviderSessionCloser) bool {
				return true
			},
			serveEstablished: func(*TRPClient, *providerTransportRuntime, *providerPunchSession, quicProviderAcceptedSession) {
				steps = append(steps, "quic")
			},
		},
		kcpAccept: clientP2PProviderKCPAcceptRuntime{
			contextOf: func(*providerTransportRuntime) context.Context { return context.Background() },
			acceptTunnel: func(*providerTransportRuntime) (kcpProviderAcceptedTunnel, error) {
				return nil, nil
			},
			reportAcceptError: func(*providerPunchSession, net.Addr, error) {},
			remoteMatches:     func(*providerPunchSession, net.Addr) bool { return true },
			rejectUnexpected:  func(kcpProviderAcceptedTunnel) {},
			promote: func(*providerTransportRuntime, *providerPunchSession, providerTunnelCloser) bool {
				return true
			},
			serveEstablished: func(*TRPClient, *providerPunchSession, kcpProviderAcceptedTunnel) {},
		},
	}
	root.lifecycle.newTimeout = func(*providerPunchSession, context.CancelFunc) *time.Timer {
		steps = append(steps, "new")
		return nil
	}
	root.setup = clientP2PProviderSetupRuntime{
		listenQUIC: func(*providerPunchSession) (*quic.Listener, error) {
			steps = append(steps, "setup")
			return &quic.Listener{}, nil
		},
		listenKCP: func(*providerPunchSession) (*kcp.Listener, error) {
			t.Fatal("ServeTransport() unexpectedly configured KCP for quic mode")
			return nil, nil
		},
		bindKCPListener: func(*providerTransportRuntime, *kcp.Listener) {},
		reportSetupErr:  func(*providerPunchSession, string, error) {},
	}

	root.ServeTransport(&TRPClient{ctx: context.Background()}, &providerPunchSession{
		mode: common.CONN_QUIC,
	})

	want := []string{"new", "setup", "quic", "close"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("ServeTransport() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderRuntimeRootServeTransportStopsAfterSetupError(t *testing.T) {
	steps := make([]string, 0, 4)
	root := &clientP2PProviderRuntimeRoot{
		lifecycle: clientP2PProviderLifecycleRuntime{
			deriveContext: context.WithCancel,
			newTimeout: func(*providerPunchSession, context.CancelFunc) *time.Timer {
				steps = append(steps, "new")
				return nil
			},
			newWatchdog: func(*providerPunchSession) *time.Timer {
				return nil
			},
			stopWatchdog: func(*providerTransportRuntime) {
				steps = append(steps, "close")
			},
			stopTimer:     func(*providerTransportRuntime) {},
			signalPreConn: func(*providerTransportRuntime) {},
			closeQUIC:     func(*providerTransportRuntime) {},
			closeKCP:      func(*providerTransportRuntime) {},
			cancel:        func(*providerTransportRuntime) {},
		},
		setup: clientP2PProviderSetupRuntime{
			listenQUIC: func(*providerPunchSession) (*quic.Listener, error) {
				return nil, nil
			},
			listenKCP: func(*providerPunchSession) (*kcp.Listener, error) {
				steps = append(steps, "setup")
				return nil, errors.New("setup failed")
			},
			bindKCPListener: func(*providerTransportRuntime, *kcp.Listener) {},
			reportSetupErr:  func(*providerPunchSession, string, error) {},
		},
		quicAccept: clientP2PProviderQUICAcceptRuntime{},
		kcpAccept:  clientP2PProviderKCPAcceptRuntime{},
	}

	root.ServeTransport(&TRPClient{ctx: context.Background()}, &providerPunchSession{
		mode: common.CONN_KCP,
	})

	want := []string{"new", "setup", "close"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("ServeTransport() steps = %v, want %v", steps, want)
	}
}
