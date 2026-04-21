package client

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/mux"
	"github.com/djylb/nps/lib/p2p"
	"github.com/quic-go/quic-go"
)

func TestOpenBridgeControlConnStoresDialedUUID(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(left), "bridge-uuid", nil
	}

	client := &TRPClient{
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: common.CONN_TCP,
		vKey:           "demo",
	}

	bridgeConn, err := client.openBridgeControlConn(context.Background())
	if err != nil {
		t.Fatalf("openBridgeControlConn() error = %v", err)
	}
	if bridgeConn.Conn == nil || bridgeConn.Conn.Conn != left {
		t.Fatalf("bridgeConn.Conn = %#v, want dialed conn", bridgeConn.Conn)
	}
	if bridgeConn.UUID != "bridge-uuid" {
		t.Fatalf("bridgeConn.UUID = %q, want bridge-uuid", bridgeConn.UUID)
	}
	if client.uuid != "bridge-uuid" {
		t.Fatalf("client.uuid = %q, want bridge-uuid", client.uuid)
	}
}

func TestOpenBridgeControlConnKeepsExistingUUID(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(left), "new-uuid", nil
	}

	client := &TRPClient{
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: common.CONN_TCP,
		vKey:           "demo",
		uuid:           "existing-uuid",
	}

	bridgeConn, err := client.openBridgeControlConn(context.Background())
	if err != nil {
		t.Fatalf("openBridgeControlConn() error = %v", err)
	}
	if bridgeConn.UUID != "existing-uuid" {
		t.Fatalf("bridgeConn.UUID = %q, want existing-uuid", bridgeConn.UUID)
	}
	if client.uuid != "existing-uuid" {
		t.Fatalf("client.uuid = %q, want existing-uuid", client.uuid)
	}
}

func TestOpenLegacyMainSignalUsesStoredUUID(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldSendType := clientMainSendType
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		clientMainSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	var gotConnType, gotUUID string
	p2pClientNewConnContext = func(context.Context, string, string, string, string, string, bool) (*conn.Conn, string, error) {
		return conn.NewConn(left), "legacy-uuid", nil
	}
	clientMainSendType = func(c *conn.Conn, connType, uuid string) error {
		gotConnType = connType
		gotUUID = uuid
		return nil
	}

	client := &TRPClient{
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: common.CONN_TCP,
		vKey:           "demo",
	}

	mainConn, err := client.openLegacyMainSignal()
	if err != nil {
		t.Fatalf("openLegacyMainSignal() error = %v", err)
	}
	if mainConn == nil || mainConn.Conn != left {
		t.Fatalf("mainConn = %#v, want dialed conn", mainConn)
	}
	if gotConnType != common.WORK_MAIN || gotUUID != "legacy-uuid" {
		t.Fatalf("send type = (%q, %q), want (%q, %q)", gotConnType, gotUUID, common.WORK_MAIN, "legacy-uuid")
	}
}

func TestOpenTunnelMainSignalUsesExistingUUIDForMux(t *testing.T) {
	oldOpenMux := clientOpenMainMuxConn
	oldSendType := clientMainSendType
	t.Cleanup(func() {
		clientOpenMainMuxConn = oldOpenMux
		clientMainSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	var gotConnType, gotUUID string
	clientOpenMainMuxConn = func(*mux.Mux) (net.Conn, error) {
		return left, nil
	}
	clientMainSendType = func(c *conn.Conn, connType, uuid string) error {
		gotConnType = connType
		gotUUID = uuid
		return nil
	}

	client := &TRPClient{
		uuid:   "main-uuid",
		tunnel: &mux.Mux{},
	}

	mainConn, err := client.openTunnelMainSignal()
	if err != nil {
		t.Fatalf("openTunnelMainSignal() error = %v", err)
	}
	if mainConn == nil || mainConn.Conn != left {
		t.Fatalf("mainConn = %#v, want mux conn", mainConn)
	}
	if gotConnType != common.WORK_MAIN || gotUUID != "main-uuid" {
		t.Fatalf("send type = (%q, %q), want (%q, %q)", gotConnType, gotUUID, common.WORK_MAIN, "main-uuid")
	}
}

func TestOpenTunnelMainSignalHandlesNilContextForQUIC(t *testing.T) {
	oldOpenQUIC := clientOpenMainQUICConn
	oldSendType := clientMainSendType
	t.Cleanup(func() {
		clientOpenMainQUICConn = oldOpenQUIC
		clientMainSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	var gotConnType, gotUUID string
	clientOpenMainQUICConn = func(ctx context.Context, _ *quic.Conn) (net.Conn, error) {
		if ctx == nil {
			t.Fatal("clientOpenMainQUICConn received nil context")
		}
		return left, nil
	}
	clientMainSendType = func(c *conn.Conn, connType, uuid string) error {
		gotConnType = connType
		gotUUID = uuid
		return nil
	}

	client := &TRPClient{
		uuid:   "quic-main-uuid",
		tunnel: &quic.Conn{},
	}

	mainConn, err := client.openTunnelMainSignal()
	if err != nil {
		t.Fatalf("openTunnelMainSignal() error = %v", err)
	}
	if mainConn == nil || mainConn.Conn != left {
		t.Fatalf("mainConn = %#v, want quic conn", mainConn)
	}
	if gotConnType != common.WORK_MAIN || gotUUID != "quic-main-uuid" {
		t.Fatalf("send type = (%q, %q), want (%q, %q)", gotConnType, gotUUID, common.WORK_MAIN, "quic-main-uuid")
	}
}

func TestOpenTunnelMainSignalWrapsMuxOpenFailure(t *testing.T) {
	oldOpenMux := clientOpenMainMuxConn
	t.Cleanup(func() {
		clientOpenMainMuxConn = oldOpenMux
	})

	expectedErr := errors.New("mux open failed")
	clientOpenMainMuxConn = func(*mux.Mux) (net.Conn, error) {
		return nil, expectedErr
	}

	client := &TRPClient{tunnel: &mux.Mux{}}
	_, err := client.openTunnelMainSignal()
	if !errors.Is(err, errClientMainSignalMuxOpen) {
		t.Fatalf("openTunnelMainSignal() error = %v, want errClientMainSignalMuxOpen", err)
	}
}

func TestOpenTunnelMainSignalRejectsTypedNilTunnel(t *testing.T) {
	client := &TRPClient{tunnel: (*mux.Mux)(nil)}

	_, err := client.openTunnelMainSignal()
	if !errors.Is(err, errClientTunnelNotConnected) {
		t.Fatalf("openTunnelMainSignal() error = %v, want errClientTunnelNotConnected", err)
	}
	if client.tunnel != nil {
		t.Fatalf("client.tunnel = %#v, want nil after typed nil normalization", client.tunnel)
	}
}

func TestReadMainEventDecodesAssociationBind(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	bind := p2p.P2PAssociationBind{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-1",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-uuid"},
			Visitor:       p2p.P2PPeerRuntime{UUID: "visitor-uuid"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 101},
		Phase: "binding",
	}

	go func() {
		defer func() { _ = server.Close() }()
		_, _ = conn.NewConn(server).SendInfo(bind, common.P2P_ASSOCIATION_BIND)
	}()

	trp := &TRPClient{signal: conn.NewConn(client)}
	event, err := trp.readMainEvent()
	if err != nil {
		t.Fatalf("readMainEvent() error = %v", err)
	}
	if event == nil || event.Bind == nil {
		t.Fatalf("readMainEvent() = %#v, want bind event", event)
	}
	if event.Flag != common.P2P_ASSOCIATION_BIND {
		t.Fatalf("event.Flag = %q, want %q", event.Flag, common.P2P_ASSOCIATION_BIND)
	}
	if event.Bind.Association.AssociationID != "assoc-1" {
		t.Fatalf("association id = %q, want assoc-1", event.Bind.Association.AssociationID)
	}
}

func TestReadMainEventDrainsUnknownPayloadBeforeNextMessage(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	bind := p2p.P2PAssociationBind{
		Association: p2p.P2PAssociation{
			AssociationID: "assoc-after-unknown",
			Provider:      p2p.P2PPeerRuntime{UUID: "provider-uuid"},
			Visitor:       p2p.P2PPeerRuntime{UUID: "visitor-uuid"},
		},
		Route: p2p.P2PRouteContext{TunnelID: 111},
		Phase: "binding",
	}

	go func() {
		defer func() { _ = server.Close() }()
		c := conn.NewConn(server)
		_, _ = c.SendInfo(map[string]string{"event": "unknown"}, "UNKN")
		_, _ = c.SendInfo(bind, common.P2P_ASSOCIATION_BIND)
	}()

	trp := &TRPClient{signal: conn.NewConn(client)}
	event, err := trp.readMainEvent()
	if err != nil {
		t.Fatalf("first readMainEvent() error = %v", err)
	}
	if event != nil {
		t.Fatalf("first readMainEvent() = %#v, want nil for unknown flag", event)
	}

	event, err = trp.readMainEvent()
	if err != nil {
		t.Fatalf("second readMainEvent() error = %v", err)
	}
	if event == nil || event.Bind == nil {
		t.Fatalf("second readMainEvent() = %#v, want bind event", event)
	}
	if event.Bind.Association.AssociationID != "assoc-after-unknown" {
		t.Fatalf("association id = %q, want assoc-after-unknown", event.Bind.Association.AssociationID)
	}
}

func TestDispatchMainEventRecordsPunchStartWithoutJoiningWhenDisabled(t *testing.T) {
	originalDisableP2P := DisableP2P
	DisableP2P = true
	t.Cleanup(func() {
		DisableP2P = originalDisableP2P
	})

	client := &TRPClient{uuid: "provider-runtime"}
	start := p2p.P2PPunchStart{
		AssociationID: "assoc-2",
		AssociationPolicy: p2p.P2PAccessPolicy{
			Mode: p2p.P2PAccessModeOpen,
		},
		Role:  common.WORK_P2P_PROVIDER,
		Self:  p2p.P2PPeerRuntime{UUID: "provider-runtime"},
		Peer:  p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		Route: p2p.P2PRouteContext{TunnelID: 202},
	}

	client.dispatchMainEvent(&clientMainEvent{
		Flag:       common.P2P_PUNCH_START,
		PunchStart: &start,
	})

	runtime, ok := runtimeClientP2PStateRoot.Association(client, "assoc-2")
	if !ok || runtime == nil {
		t.Fatal("dispatchMainEvent() should record punch-start runtime")
	}
	if runtime.Phase != "punching" {
		t.Fatalf("runtime.Phase = %q, want punching", runtime.Phase)
	}
	if runtime.PeerUUID != "visitor-runtime" {
		t.Fatalf("runtime.PeerUUID = %q, want visitor-runtime", runtime.PeerUUID)
	}
}

func TestDispatchMainEventIgnoresMismatchedPunchStart(t *testing.T) {
	originalDisableP2P := DisableP2P
	originalRuntime := runtimeClientP2PJoin
	DisableP2P = false
	t.Cleanup(func() {
		DisableP2P = originalDisableP2P
		runtimeClientP2PJoin = originalRuntime
	})

	joinCalled := make(chan struct{}, 1)
	runtimeClientP2PJoin = clientP2PJoinRuntime{
		timeout:        func(*TRPClient) time.Duration { return time.Second },
		preferredLocal: func(*TRPClient) string { return "" },
		openSession: func(*TRPClient, context.Context, p2p.P2PPunchStart, string) (*providerPunchSession, error) {
			select {
			case joinCalled <- struct{}{}:
			default:
			}
			return nil, errors.New("unexpected join")
		},
		writeProgress:   func(*providerPunchSession) {},
		serveSession:    func(*TRPClient, *providerPunchSession) {},
		closeControl:    func(*providerPunchSession) {},
		closePacketConn: func(net.PacketConn) {},
		logError:        func(error) {},
	}

	client := &TRPClient{
		uuid: "provider-runtime",
		ctx:  context.Background(),
	}
	start := p2p.P2PPunchStart{
		AssociationID: "assoc-ignore-dispatch",
		Role:          common.WORK_P2P_VISITOR,
		Self:          p2p.P2PPeerRuntime{UUID: "visitor-runtime"},
		Peer:          p2p.P2PPeerRuntime{UUID: "provider-runtime"},
		Route:         p2p.P2PRouteContext{TunnelID: 203},
	}

	client.dispatchMainEvent(&clientMainEvent{
		Flag:       common.P2P_PUNCH_START,
		PunchStart: &start,
	})

	if _, ok := runtimeClientP2PStateRoot.Association(client, "assoc-ignore-dispatch"); ok {
		t.Fatal("dispatchMainEvent() should ignore mismatched punch-start runtime")
	}
	select {
	case <-joinCalled:
		t.Fatal("dispatchMainEvent() unexpectedly started provider join for mismatched punch start")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleMainUsesRuntimeDispatch(t *testing.T) {
	originalRuntime := runtimeClientMain
	t.Cleanup(func() {
		runtimeClientMain = originalRuntime
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var dispatched []string
	runtimeClientMain = clientMainRuntime{
		readEvent: func(*TRPClient) (*clientMainEvent, error) {
			cancel()
			return &clientMainEvent{Flag: common.P2P_ASSOCIATION_BIND}, io.EOF
		},
		dispatchEvent: func(*TRPClient, *clientMainEvent) {
			dispatched = append(dispatched, "dispatch")
		},
	}

	trp := &TRPClient{ctx: ctx, cancel: cancel}
	trp.handleMain()

	if len(dispatched) != 0 {
		t.Fatalf("dispatch calls = %d, want 0 when readEvent returns error", len(dispatched))
	}
}

func TestHandleMainDispatchesDecodedEvent(t *testing.T) {
	originalRuntime := runtimeClientMain
	t.Cleanup(func() {
		runtimeClientMain = originalRuntime
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := []*clientMainEvent{
		{Flag: common.P2P_ASSOCIATION_BIND, Bind: &p2p.P2PAssociationBind{Association: p2p.P2PAssociation{AssociationID: "assoc-3"}}},
	}
	var dispatched []string
	runtimeClientMain = clientMainRuntime{
		readEvent: func(*TRPClient) (*clientMainEvent, error) {
			if len(events) == 0 {
				cancel()
				return nil, context.Canceled
			}
			event := events[0]
			events = events[1:]
			return event, nil
		},
		dispatchEvent: func(_ *TRPClient, event *clientMainEvent) {
			dispatched = append(dispatched, event.Flag)
			cancel()
		},
	}

	trp := &TRPClient{ctx: ctx, cancel: cancel}
	done := make(chan struct{})
	go func() {
		defer close(done)
		trp.handleMain()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleMain() did not exit")
	}

	if len(dispatched) != 1 || dispatched[0] != common.P2P_ASSOCIATION_BIND {
		t.Fatalf("dispatched = %#v, want bind event", dispatched)
	}
}

func TestClientMonitorRuntimeClosesClientWhenTunnelCloses(t *testing.T) {
	done := make(chan struct{}, 1)
	var notedReason atomic.Value

	runtime := clientMonitorRuntime{
		interval:  2 * time.Millisecond,
		newTicker: time.NewTicker,
		tunnelClosed: func(*TRPClient) bool {
			return true
		},
		closeReason: func(*TRPClient) string {
			return "tunnel closed"
		},
		noteReason: func(_ *TRPClient, reason string) {
			notedReason.Store(reason)
		},
		closeClient: func(*TRPClient) {
			select {
			case done <- struct{}{}:
			default:
			}
		},
	}

	client := &TRPClient{ctx: context.Background()}
	go runtime.Run(client)

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run() did not close client after tunnel closed")
	}
	if got := notedReason.Load(); got != "tunnel closed" {
		t.Fatalf("Run() noted reason = %v, want %q", got, "tunnel closed")
	}
}

func TestClientMonitorRuntimeStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calledClose := make(chan struct{}, 1)
	runtime := clientMonitorRuntime{
		interval:  2 * time.Millisecond,
		newTicker: time.NewTicker,
		tunnelClosed: func(*TRPClient) bool {
			return false
		},
		closeReason: func(*TRPClient) string {
			return ""
		},
		noteReason: func(*TRPClient, string) {},
		closeClient: func(*TRPClient) {
			select {
			case calledClose <- struct{}{}:
			default:
			}
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.Run(&TRPClient{ctx: ctx})
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run() did not stop after context cancel")
	}
	select {
	case <-calledClose:
		t.Fatal("Run() unexpectedly closed client after context cancel")
	default:
	}
}

func TestClientStartupRuntimeRunsStartupStepsInOrder(t *testing.T) {
	var calls []string
	runtime := clientStartupRuntime{
		establishSignal: func(*TRPClient) error {
			calls = append(calls, "signal")
			return nil
		},
		startMonitor: func(*TRPClient) {
			calls = append(calls, "monitor")
		},
		startHealth: func(*TRPClient) {
			calls = append(calls, "health")
		},
		markReady: func(*TRPClient) {
			calls = append(calls, "ready")
		},
	}

	if err := runtime.Start(&TRPClient{}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	want := []string{"signal", "monitor", "health", "ready"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("startup calls = %#v, want %#v", calls, want)
	}
}

func TestClientStartupRuntimeStopsWhenSignalSetupFails(t *testing.T) {
	expectedErr := errors.New("signal setup failed")
	var calls []string
	runtime := clientStartupRuntime{
		establishSignal: func(*TRPClient) error {
			calls = append(calls, "signal")
			return expectedErr
		},
		startMonitor: func(*TRPClient) {
			calls = append(calls, "monitor")
		},
		startHealth: func(*TRPClient) {
			calls = append(calls, "health")
		},
		markReady: func(*TRPClient) {
			calls = append(calls, "ready")
		},
	}

	err := runtime.Start(&TRPClient{})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Start() error = %v, want %v", err, expectedErr)
	}

	want := []string{"signal"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("startup calls = %#v, want %#v", calls, want)
	}
}

func TestNewClientP2PJoinRuntimeBuildsDefaultWiring(t *testing.T) {
	runtime := newClientP2PJoinRuntime()
	if runtimeClientP2PProviderRoot == nil {
		t.Fatal("provider root = nil")
	}
	if runtimeClientP2PProviderRoot.lifecycle.newTimeout == nil || runtimeClientP2PProviderRoot.setup.listenQUIC == nil || runtimeClientP2PProviderRoot.quicAccept.acceptSession == nil {
		t.Fatal("provider transport root is not fully wired")
	}
	if runtimeClientP2PProviderRoot.channelHandle.prepare == nil || runtimeClientP2PProviderRoot.channelAuthorization.associationID == nil || runtimeClientP2PProviderRoot.channelResult.write == nil || runtimeClientP2PProviderRoot.channelRelay.copy == nil || runtimeClientP2PProviderRoot.channelPrepare.readLink == nil || runtimeClientP2PProviderRoot.channelConnect.dialTarget == nil {
		t.Fatal("provider channel root is not fully wired")
	}
	if runtime.timeout == nil || runtime.openSession == nil || runtime.serveSession == nil {
		t.Fatal("join runtime is not fully wired")
	}
}

type stubPacketConn struct {
	net.PacketConn
	closed bool
}

func (c *stubPacketConn) Close() error {
	c.closed = true
	return nil
}

func TestClientP2PJoinRuntimeUsesPreferredLocalAndMinimumTimeout(t *testing.T) {
	start := p2p.P2PPunchStart{SessionID: "session-1"}
	packetConn := &stubPacketConn{}
	calledServe := false
	progressCalled := false
	controlClosed := false
	var gotPreferredLocal string
	var gotDeadline time.Time

	runtime := clientP2PJoinRuntime{
		timeout: func(s *TRPClient) time.Duration {
			return s.p2pJoinTimeout()
		},
		preferredLocal: func(s *TRPClient) string {
			return preferredP2PLocalAddr(s.localIP)
		},
		openSession: func(_ *TRPClient, ctx context.Context, _ p2p.P2PPunchStart, preferredLocal string) (*providerPunchSession, error) {
			gotPreferredLocal = preferredLocal
			var ok bool
			gotDeadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("openSession() context missing deadline")
			}
			return &providerPunchSession{
				localConn:      packetConn,
				preferredLocal: preferredLocal,
				sessionID:      "session-1",
				role:           "provider",
				mode:           "kcp",
			}, nil
		},
		writeProgress: func(*providerPunchSession) {
			progressCalled = true
		},
		serveSession: func(*TRPClient, *providerPunchSession) {
			calledServe = true
		},
		closeControl: func(*providerPunchSession) {
			controlClosed = true
		},
		closePacketConn: func(conn net.PacketConn) {
			if conn != nil {
				_ = conn.Close()
			}
		},
		logError: func(error) {
			t.Fatal("Run() unexpectedly logged error")
		},
	}

	before := time.Now()
	client := &TRPClient{
		ctx:            context.Background(),
		localIP:        "127.0.0.1",
		disconnectTime: 0,
	}

	runtime.Run(client, start)

	if gotPreferredLocal != "127.0.0.1" {
		t.Fatalf("Run() preferred local = %q, want %q", gotPreferredLocal, "127.0.0.1")
	}
	if remaining := gotDeadline.Sub(before); remaining < 29*time.Second || remaining > 31*time.Second {
		t.Fatalf("Run() timeout window = %v, want about 30s", remaining)
	}
	if !progressCalled {
		t.Fatal("Run() did not write progress")
	}
	if !calledServe {
		t.Fatal("Run() did not serve provider session")
	}
	if !controlClosed {
		t.Fatal("Run() did not close control connection")
	}
	if !packetConn.closed {
		t.Fatal("Run() did not close packet connection")
	}
}

func TestClientP2PJoinRuntimeReturnsAfterOpenError(t *testing.T) {
	expectedErr := errors.New("open failed")
	served := false
	loggedErr := error(nil)

	runtime := clientP2PJoinRuntime{
		timeout: func(*TRPClient) time.Duration {
			return time.Second
		},
		preferredLocal: func(*TRPClient) string {
			return ""
		},
		openSession: func(*TRPClient, context.Context, p2p.P2PPunchStart, string) (*providerPunchSession, error) {
			return nil, expectedErr
		},
		writeProgress: func(*providerPunchSession) {
			t.Fatal("Run() unexpectedly wrote progress")
		},
		serveSession: func(*TRPClient, *providerPunchSession) {
			served = true
		},
		closeControl:    func(*providerPunchSession) {},
		closePacketConn: func(net.PacketConn) {},
		logError: func(err error) {
			loggedErr = err
		},
	}

	runtime.Run(&TRPClient{ctx: context.Background()}, p2p.P2PPunchStart{})

	if !errors.Is(loggedErr, expectedErr) {
		t.Fatalf("Run() logged error = %v, want %v", loggedErr, expectedErr)
	}
	if served {
		t.Fatal("Run() unexpectedly served session after open error")
	}
}

func TestClientP2PJoinRuntimeHandlesNilClientContext(t *testing.T) {
	start := p2p.P2PPunchStart{SessionID: "session-nil-ctx"}
	packetConn := &stubPacketConn{}
	progressCalled := false
	served := false
	controlClosed := false
	var gotDeadline time.Time

	runtime := clientP2PJoinRuntime{
		timeout: func(*TRPClient) time.Duration {
			return 200 * time.Millisecond
		},
		preferredLocal: func(*TRPClient) string {
			return ""
		},
		openSession: func(_ *TRPClient, ctx context.Context, _ p2p.P2PPunchStart, _ string) (*providerPunchSession, error) {
			var ok bool
			gotDeadline, ok = ctx.Deadline()
			if !ok {
				t.Fatal("openSession() context missing deadline")
			}
			return &providerPunchSession{
				localConn: packetConn,
				sessionID: start.SessionID,
				role:      "provider",
				mode:      "kcp",
			}, nil
		},
		writeProgress: func(*providerPunchSession) {
			progressCalled = true
		},
		serveSession: func(*TRPClient, *providerPunchSession) {
			served = true
		},
		closeControl: func(*providerPunchSession) {
			controlClosed = true
		},
		closePacketConn: func(conn net.PacketConn) {
			if conn != nil {
				_ = conn.Close()
			}
		},
		logError: func(error) {
			t.Fatal("Run() unexpectedly logged error")
		},
	}

	before := time.Now()
	runtime.Run(&TRPClient{}, start)

	if remaining := gotDeadline.Sub(before); remaining < 150*time.Millisecond || remaining > 500*time.Millisecond {
		t.Fatalf("Run() timeout window = %v, want about 200ms from background-derived context", remaining)
	}
	if !progressCalled {
		t.Fatal("Run() did not write progress")
	}
	if !served {
		t.Fatal("Run() did not serve provider session")
	}
	if !controlClosed {
		t.Fatal("Run() did not close control connection")
	}
	if !packetConn.closed {
		t.Fatal("Run() did not close packet connection")
	}
}

func TestTRPClientStartHandlesNilContext(t *testing.T) {
	originalRuntime := runtimeClientStartup
	t.Cleanup(func() {
		runtimeClientStartup = originalRuntime
	})

	runtimeClientStartup = clientStartupRuntime{
		establishSignal: func(*TRPClient) error {
			return errClientTunnelNotConnected
		},
		startMonitor: func(*TRPClient) {},
		startHealth:  func(*TRPClient) {},
		markReady:    func(*TRPClient) {},
	}

	client := NewRPClient("", "", "", "", "", "", nil, 0, nil)
	var nilCtx context.Context
	client.Start(nilCtx)

	if client.ctx == nil {
		t.Fatal("client.ctx = nil, want background-derived context")
	}
	if client.cancel == nil {
		t.Fatal("client.cancel = nil, want cancel func")
	}
	select {
	case <-client.ctx.Done():
	default:
		t.Fatal("client context not canceled after Start() returned")
	}
}
