package client

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/p2p"
)

func TestClientChannelRuntimeHandleSuccessfulRelay(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	targetServer, targetClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()
	defer func() { _ = targetServer.Close() }()
	defer func() { _ = targetClient.Close() }()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	steps := make([]string, 0, 7)

	runtime := clientChannelRuntime{
		readLink: func(net.Conn) (*conn.Link, error) {
			steps = append(steps, "read")
			return link, nil
		},
		ackLink: func(net.Conn, *conn.Link) bool {
			steps = append(steps, "ack")
			return true
		},
		handleSpecial: func(*TRPClient, net.Conn, *conn.Link) bool {
			steps = append(steps, "special")
			return false
		},
		authorizeTarget: func(*TRPClient, net.Conn, *conn.Link) bool {
			steps = append(steps, "authorize")
			return true
		},
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			steps = append(steps, "dial")
			return targetServer, nil
		},
		writeConnectResult: func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool {
			steps = append(steps, "write")
			return true
		},
		relayTarget: func(net.Conn, net.Conn, *conn.Link) {
			steps = append(steps, "relay")
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	want := []string{"read", "ack", "special", "authorize", "dial", "write", "relay"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Handle() steps = %v, want %v", steps, want)
	}
}

func TestClientChannelRuntimeHandleDialErrorReportsConnectResult(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	dialErr := errors.New("dial failed")
	var status conn.ConnectResultStatus
	relayCalled := false

	runtime := clientChannelRuntime{
		readLink: func(net.Conn) (*conn.Link, error) {
			return link, nil
		},
		ackLink: func(net.Conn, *conn.Link) bool {
			return true
		},
		handleSpecial: func(*TRPClient, net.Conn, *conn.Link) bool {
			return false
		},
		authorizeTarget: func(*TRPClient, net.Conn, *conn.Link) bool {
			return true
		},
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			return nil, dialErr
		},
		writeConnectResult: func(_ net.Conn, _ *conn.Link, got conn.ConnectResultStatus) bool {
			status = got
			return true
		},
		relayTarget: func(net.Conn, net.Conn, *conn.Link) {
			relayCalled = true
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	if status != conn.DialConnectResult(dialErr) {
		t.Fatalf("Handle() status = %v, want %v", status, conn.DialConnectResult(dialErr))
	}
	if relayCalled {
		t.Fatal("Handle() unexpectedly relayed target after dial error")
	}
}

func TestClientChannelRuntimeHandleRejectsTypedNilTargetConn(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	var typedNil *typedNilClientConnStub
	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	var status conn.ConnectResultStatus
	relayCalled := false

	runtime := clientChannelRuntime{
		readLink: func(net.Conn) (*conn.Link, error) {
			return link, nil
		},
		ackLink: func(net.Conn, *conn.Link) bool {
			return true
		},
		handleSpecial: func(*TRPClient, net.Conn, *conn.Link) bool {
			return false
		},
		authorizeTarget: func(*TRPClient, net.Conn, *conn.Link) bool {
			return true
		},
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			return typedNil, nil
		},
		writeConnectResult: func(_ net.Conn, _ *conn.Link, got conn.ConnectResultStatus) bool {
			status = got
			return true
		},
		relayTarget: func(net.Conn, net.Conn, *conn.Link) {
			relayCalled = true
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	if status != conn.ConnectResultServerFailure {
		t.Fatalf("Handle() status = %v, want %v", status, conn.ConnectResultServerFailure)
	}
	if relayCalled {
		t.Fatal("Handle() unexpectedly relayed typed nil target conn")
	}
}

func TestRuntimeClientChannelAuthorizeTargetUsesAuthorizationRuntime(t *testing.T) {
	originalRuntime := runtimeClientChannelAuthorization
	t.Cleanup(func() {
		runtimeClientChannelAuthorization = originalRuntime
	})

	called := false
	runtimeClientChannelAuthorization = clientChannelAuthorizationRuntime{
		associationID: func(net.Conn) string {
			called = true
			return ""
		},
		policyOf: func(*TRPClient, string) (p2p.P2PAccessPolicy, bool) {
			t.Fatal("policyOf() unexpectedly called")
			return p2p.P2PAccessPolicy{}, false
		},
		allowsTarget: func(p2p.P2PAccessPolicy, string) bool {
			t.Fatal("allowsTarget() unexpectedly called")
			return false
		},
		denyMissing: func(net.Conn, *conn.Link, string) {
			t.Fatal("denyMissing() unexpectedly called")
		},
		denyTarget: func(net.Conn, *conn.Link, string) {
			t.Fatal("denyTarget() unexpectedly called")
		},
	}

	if !runtimeClientChannel.authorizeTarget(&TRPClient{}, nil, &conn.Link{Host: "127.0.0.1:18080"}) {
		t.Fatal("authorizeTarget() = false, want true")
	}
	if !called {
		t.Fatal("authorizeTarget() did not delegate to authorization runtime")
	}
}

func TestRuntimeClientChannelHandleSpecialUsesSpecialRuntime(t *testing.T) {
	originalRuntime := runtimeClientSpecialChannel
	t.Cleanup(func() {
		runtimeClientSpecialChannel = originalRuntime
	})

	called := false
	runtimeClientSpecialChannel = clientSpecialChannelRuntime{
		handleUDP5: func(*TRPClient, net.Conn, *conn.Link) bool {
			called = true
			return true
		},
		handleFile: func(*TRPClient, net.Conn, *conn.Link) bool {
			t.Fatal("handleFile() unexpectedly called")
			return false
		},
	}

	if !runtimeClientChannel.handleSpecial(&TRPClient{}, nil, &conn.Link{ConnType: "udp5"}) {
		t.Fatal("handleSpecial() = false, want true")
	}
	if !called {
		t.Fatal("handleSpecial() did not delegate to special runtime")
	}
}

func TestRuntimeClientChannelDialTargetUsesDialRuntime(t *testing.T) {
	originalRuntime := runtimeClientChannelDial
	t.Cleanup(func() {
		runtimeClientChannelDial = originalRuntime
	})

	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	called := false
	runtimeClientChannelDial = clientChannelDialRuntime{
		contextOf: func(*TRPClient) context.Context {
			return context.Background()
		},
		bindAddr: func(*TRPClient, *conn.Link) net.Addr {
			return nil
		},
		dial: func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
			called = true
			return server, nil
		},
	}

	got, err := runtimeClientChannel.dialTarget(&TRPClient{}, &conn.Link{ConnType: "tcp", Host: "127.0.0.1:18080"})
	if err != nil {
		t.Fatalf("dialTarget() error = %v, want nil", err)
	}
	if got != server {
		t.Fatalf("dialTarget() conn = %#v, want %#v", got, server)
	}
	if !called {
		t.Fatal("dialTarget() did not delegate to dial runtime")
	}
}

func TestOpenBridgeTunnelUsesClientContextForControlDial(t *testing.T) {
	oldNewConn := p2pClientNewConnContext
	oldSendType := clientTunnelSendType
	t.Cleanup(func() {
		p2pClientNewConnContext = oldNewConn
		clientTunnelSendType = oldSendType
	})

	left, right := net.Pipe()
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
	})

	expectedCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var gotCtx context.Context
	p2pClientNewConnContext = func(ctx context.Context, tp, vkey, server, proxyURL, localIP string, verifyCertificate bool) (*conn.Conn, string, error) {
		gotCtx = ctx
		return conn.NewConn(left), "bridge-uuid", nil
	}
	clientTunnelSendType = func(c *conn.Conn, connType, uuid string) error {
		if c == nil || c.Conn != left {
			t.Fatalf("clientTunnelSendType() conn = %#v, want dialed conn", c)
		}
		if connType != common.WORK_CHAN || uuid != "bridge-uuid" {
			t.Fatalf("clientTunnelSendType() got (%q, %q), want (%q, %q)", connType, uuid, common.WORK_CHAN, "bridge-uuid")
		}
		return nil
	}

	client := &TRPClient{
		ctx:            expectedCtx,
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: common.CONN_TCP,
		vKey:           "demo",
	}

	tunnel, err := client.openBridgeTunnel()
	if err != nil {
		t.Fatalf("openBridgeTunnel() error = %v", err)
	}
	if tunnel == nil || tunnel.Conn != left {
		t.Fatalf("openBridgeTunnel() tunnel = %#v, want dialed conn", tunnel)
	}
	if gotCtx != expectedCtx {
		t.Fatalf("openBridgeTunnel() dial ctx = %#v, want client ctx %#v", gotCtx, expectedCtx)
	}
}

func TestClientChannelAuthorizationRuntimeAllowsNonAssociationTraffic(t *testing.T) {
	runtime := clientChannelAuthorizationRuntime{
		associationID: func(net.Conn) string {
			return ""
		},
		policyOf: func(*TRPClient, string) (p2p.P2PAccessPolicy, bool) {
			t.Fatal("policyOf() unexpectedly called for non-association traffic")
			return p2p.P2PAccessPolicy{}, false
		},
		allowsTarget: func(p2p.P2PAccessPolicy, string) bool {
			t.Fatal("allowsTarget() unexpectedly called for non-association traffic")
			return false
		},
		denyMissing: func(net.Conn, *conn.Link, string) {
			t.Fatal("denyMissing() unexpectedly called for non-association traffic")
		},
		denyTarget: func(net.Conn, *conn.Link, string) {
			t.Fatal("denyTarget() unexpectedly called for non-association traffic")
		},
	}

	if !runtime.Authorize(&TRPClient{}, nil, &conn.Link{Host: "127.0.0.1:18080"}) {
		t.Fatal("Authorize() = false, want true for non-association traffic")
	}
}

func TestClientChannelAuthorizationRuntimeRejectsMissingPolicy(t *testing.T) {
	steps := make([]string, 0, 3)
	runtime := clientChannelAuthorizationRuntime{
		associationID: func(net.Conn) string {
			steps = append(steps, "association")
			return "assoc-1"
		},
		policyOf: func(*TRPClient, string) (p2p.P2PAccessPolicy, bool) {
			steps = append(steps, "policy")
			return p2p.P2PAccessPolicy{}, false
		},
		allowsTarget: func(p2p.P2PAccessPolicy, string) bool {
			steps = append(steps, "allow")
			return true
		},
		denyMissing: func(net.Conn, *conn.Link, string) {
			steps = append(steps, "deny_missing")
		},
		denyTarget: func(net.Conn, *conn.Link, string) {
			steps = append(steps, "deny_target")
		},
	}

	if runtime.Authorize(&TRPClient{}, nil, &conn.Link{Host: "127.0.0.1:18080"}) {
		t.Fatal("Authorize() = true, want false when policy is missing")
	}

	want := []string{"association", "policy", "deny_missing"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Authorize() steps = %v, want %v", steps, want)
	}
}

func TestClientChannelAuthorizationRuntimeRejectsDisallowedTarget(t *testing.T) {
	steps := make([]string, 0, 4)
	runtime := clientChannelAuthorizationRuntime{
		associationID: func(net.Conn) string {
			steps = append(steps, "association")
			return "assoc-1"
		},
		policyOf: func(*TRPClient, string) (p2p.P2PAccessPolicy, bool) {
			steps = append(steps, "policy")
			return p2p.P2PAccessPolicy{Mode: p2p.P2PAccessModeWhitelist}, true
		},
		allowsTarget: func(p2p.P2PAccessPolicy, string) bool {
			steps = append(steps, "allow")
			return false
		},
		denyMissing: func(net.Conn, *conn.Link, string) {
			steps = append(steps, "deny_missing")
		},
		denyTarget: func(net.Conn, *conn.Link, string) {
			steps = append(steps, "deny_target")
		},
	}

	if runtime.Authorize(&TRPClient{}, nil, &conn.Link{Host: "127.0.0.1:18080"}) {
		t.Fatal("Authorize() = true, want false when target is denied")
	}

	want := []string{"association", "policy", "allow", "deny_target"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Authorize() steps = %v, want %v", steps, want)
	}
}

func TestClientChannelAuthorizationRuntimeAllowsWhitelistedTarget(t *testing.T) {
	runtime := clientChannelAuthorizationRuntime{
		associationID: func(net.Conn) string {
			return "assoc-1"
		},
		policyOf: func(*TRPClient, string) (p2p.P2PAccessPolicy, bool) {
			return p2p.P2PAccessPolicy{Mode: p2p.P2PAccessModeWhitelist}, true
		},
		allowsTarget: func(p2p.P2PAccessPolicy, string) bool {
			return true
		},
		denyMissing: func(net.Conn, *conn.Link, string) {
			t.Fatal("denyMissing() unexpectedly called for allowed target")
		},
		denyTarget: func(net.Conn, *conn.Link, string) {
			t.Fatal("denyTarget() unexpectedly called for allowed target")
		},
	}

	if !runtime.Authorize(&TRPClient{}, nil, &conn.Link{Host: "127.0.0.1:18080"}) {
		t.Fatal("Authorize() = false, want true when target is allowed")
	}
}

func TestClientChannelDispatchRuntimeHandleUsesInjectedHandler(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	called := false
	runtime := clientChannelDispatchRuntime{
		handle: func(_ *TRPClient, c net.Conn) {
			called = c == srcServer
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	if !called {
		t.Fatal("Handle() did not use injected handler")
	}
}

func TestClientChannelDispatchRuntimeLaunchUsesInjectedLauncher(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	steps := make([]string, 0, 2)
	runtime := clientChannelDispatchRuntime{
		launch: func(run func()) {
			steps = append(steps, "launch")
			run()
		},
		handle: func(_ *TRPClient, c net.Conn) {
			if c != srcServer {
				t.Fatalf("handle() conn = %#v, want %#v", c, srcServer)
			}
			steps = append(steps, "handle")
		},
	}

	runtime.Launch(&TRPClient{}, srcServer)

	if len(steps) != 2 || steps[0] != "launch" || steps[1] != "handle" {
		t.Fatalf("Launch() steps = %v, want [launch handle]", steps)
	}
}

func TestDialChannelTargetUsesClientContext(t *testing.T) {
	oldDial := clientDialContext
	t.Cleanup(func() {
		clientDialContext = oldDial
	})

	clientDialContext = func(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &TRPClient{ctx: ctx}
	link := conn.NewLink("tcp", "example.com:443", false, false, "", false, conn.LinkTimeout(5*time.Second))

	started := time.Now()
	_, err := client.dialChannelTarget(link)
	elapsed := time.Since(started)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dialChannelTarget() error = %v, want context canceled", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("dialChannelTarget() ignored canceled client context for %s", elapsed)
	}
}

func TestDialChannelTargetBindsPublicTCPWithLocalIPForward(t *testing.T) {
	oldLocalIPForward := LocalIPForward
	t.Cleanup(func() {
		LocalIPForward = oldLocalIPForward
	})

	LocalIPForward = true
	client := &TRPClient{localIP: "127.0.0.1"}
	link := conn.NewLink("tcp", "example.com:443", false, false, "", false, conn.LinkTimeout(5*time.Second))

	addr, ok := client.channelDialBindAddr(link).(*net.TCPAddr)
	if !ok {
		t.Fatalf("channelDialBindAddr() local addr type = %T, want *net.TCPAddr", client.channelDialBindAddr(link))
	}
	want := common.BuildTCPBindAddr("127.0.0.1").(*net.TCPAddr)
	if !addr.IP.Equal(want.IP) {
		t.Fatalf("channelDialBindAddr() local addr IP = %v, want %v", addr.IP, want.IP)
	}
}

func TestHandleUDP5ChannelUsesConfiguredLocalIPWhenForwardEnabled(t *testing.T) {
	oldHandleUDP5 := clientHandleUDP5
	oldLocalIPForward := LocalIPForward
	t.Cleanup(func() {
		clientHandleUDP5 = oldHandleUDP5
		LocalIPForward = oldLocalIPForward
	})

	LocalIPForward = true
	var (
		gotContext context.Context
		gotTimeout time.Duration
		gotLocalIP string
	)
	clientHandleUDP5 = func(ctx context.Context, src net.Conn, timeout time.Duration, localIP string) {
		gotContext = ctx
		gotTimeout = timeout
		gotLocalIP = localIP
	}

	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	ctx := context.Background()
	client := &TRPClient{ctx: ctx, localIP: "127.0.0.1"}
	link := conn.NewLink("udp5", "127.0.0.1:9000", false, false, "", false, conn.LinkTimeout(3*time.Second))

	if !client.handleSpecialChannel(srcServer, link) {
		t.Fatal("handleSpecialChannel() = false, want true for udp5")
	}
	if gotContext != ctx {
		t.Fatalf("handleSpecialChannel() context = %v, want %v", gotContext, ctx)
	}
	if gotTimeout != 3*time.Second {
		t.Fatalf("handleSpecialChannel() timeout = %v, want %v", gotTimeout, 3*time.Second)
	}
	if gotLocalIP != "127.0.0.1" {
		t.Fatalf("handleSpecialChannel() local IP = %q, want %q", gotLocalIP, "127.0.0.1")
	}
}

func TestHandleUDP5ChannelDropsLocalIPWhenForwardDisabled(t *testing.T) {
	oldHandleUDP5 := clientHandleUDP5
	oldLocalIPForward := LocalIPForward
	t.Cleanup(func() {
		clientHandleUDP5 = oldHandleUDP5
		LocalIPForward = oldLocalIPForward
	})

	LocalIPForward = false
	var gotLocalIP string
	clientHandleUDP5 = func(ctx context.Context, src net.Conn, timeout time.Duration, localIP string) {
		gotLocalIP = localIP
	}

	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	client := &TRPClient{ctx: context.Background(), localIP: "127.0.0.1"}
	link := conn.NewLink("udp5", "127.0.0.1:9000", false, false, "", false, conn.LinkTimeout(time.Second))

	if !client.handleSpecialChannel(srcServer, link) {
		t.Fatal("handleSpecialChannel() = false, want true for udp5")
	}
	if gotLocalIP != "" {
		t.Fatalf("handleSpecialChannel() local IP = %q, want empty string when forwarding disabled", gotLocalIP)
	}
}
