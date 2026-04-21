package client

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/p2p"
)

type typedNilClientConnStub struct{}

func (s *typedNilClientConnStub) Read([]byte) (int, error) {
	panic("unexpected Read call on typed nil client conn")
}

func (s *typedNilClientConnStub) Write([]byte) (int, error) {
	panic("unexpected Write call on typed nil client conn")
}

func (s *typedNilClientConnStub) Close() error {
	panic("unexpected Close call on typed nil client conn")
}

func (s *typedNilClientConnStub) LocalAddr() net.Addr {
	panic("unexpected LocalAddr call on typed nil client conn")
}

func (s *typedNilClientConnStub) RemoteAddr() net.Addr {
	panic("unexpected RemoteAddr call on typed nil client conn")
}

func (s *typedNilClientConnStub) SetDeadline(time.Time) error {
	panic("unexpected SetDeadline call on typed nil client conn")
}

func (s *typedNilClientConnStub) SetReadDeadline(time.Time) error {
	panic("unexpected SetReadDeadline call on typed nil client conn")
}

func (s *typedNilClientConnStub) SetWriteDeadline(time.Time) error {
	panic("unexpected SetWriteDeadline call on typed nil client conn")
}

func TestClientP2PProviderChannelSpecialRuntimeRoutesUDP5AndFile(t *testing.T) {
	steps := make([]string, 0, 2)
	runtime := clientP2PProviderChannelSpecialRuntime{
		handleUDP5: func(*TRPClient, net.Conn, *conn.Link) bool {
			steps = append(steps, "udp5")
			return true
		},
		handleFile: func(*TRPClient, net.Conn, *conn.Link) bool {
			steps = append(steps, "file")
			return true
		},
	}

	if !runtime.Handle(&TRPClient{}, nil, &conn.Link{ConnType: "udp5"}) {
		t.Fatal("Handle() = false, want true for udp5")
	}
	if !runtime.Handle(&TRPClient{}, nil, &conn.Link{ConnType: "file"}) {
		t.Fatal("Handle() = false, want true for file")
	}

	want := []string{"udp5", "file"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Handle() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelAuthorizationRuntimeAllowsNonAssociationTraffic(t *testing.T) {
	runtime := clientP2PProviderChannelAuthorizationRuntime{
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

func TestClientP2PProviderChannelAuthorizationRuntimeRejectsMissingPolicy(t *testing.T) {
	steps := make([]string, 0, 3)
	runtime := clientP2PProviderChannelAuthorizationRuntime{
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

func TestClientP2PProviderChannelAuthorizationRuntimeRejectsDisallowedTarget(t *testing.T) {
	steps := make([]string, 0, 4)
	runtime := clientP2PProviderChannelAuthorizationRuntime{
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

func TestClientP2PProviderChannelAuthorizationRuntimeAllowsWhitelistedTarget(t *testing.T) {
	runtime := clientP2PProviderChannelAuthorizationRuntime{
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

func TestClientP2PProviderRuntimeRootDialChannelStageUsesChannelRuntime(t *testing.T) {
	originalRuntime := runtimeClientChannelDial
	t.Cleanup(func() {
		runtimeClientChannelDial = originalRuntime
	})

	server, clientConn := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = clientConn.Close() }()

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

	ctx := newClientP2PProviderRuntimeRoot()
	got, err := ctx.channelDial.Dial(&TRPClient{}, &conn.Link{ConnType: "tcp", Host: "127.0.0.1:18080"})
	if err != nil {
		t.Fatalf("channelDial.Dial() error = %v, want nil", err)
	}
	if got != server {
		t.Fatalf("channelDial.Dial() conn = %#v, want %#v", got, server)
	}
	if !called {
		t.Fatal("channelDial.Dial() did not delegate to channel dial runtime")
	}
}

func TestClientP2PProviderChannelPrepareRuntimePrepareFormatsHostAfterAck(t *testing.T) {
	steps := make([]string, 0, 3)
	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	runtime := clientP2PProviderChannelPrepareRuntime{
		readLink: func(net.Conn) (*conn.Link, error) {
			steps = append(steps, "read")
			return link, nil
		},
		ackLink: func(net.Conn, *conn.Link) bool {
			steps = append(steps, "ack")
			return true
		},
		formatHost: func(host string) string {
			steps = append(steps, "format")
			return host + "-formatted"
		},
	}

	got, ok := runtime.Prepare(nil)
	if !ok {
		t.Fatal("Prepare() = false, want true")
	}
	if got.Host != "127.0.0.1:18080-formatted" {
		t.Fatalf("Prepare() host = %q, want formatted host", got.Host)
	}
	want := []string{"read", "ack", "format"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Prepare() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelPrepareRuntimePrepareClosesOnReadError(t *testing.T) {
	steps := make([]string, 0, 2)
	runtime := clientP2PProviderChannelPrepareRuntime{
		readLink: func(net.Conn) (*conn.Link, error) {
			steps = append(steps, "read")
			return nil, errors.New("read failed")
		},
		closeConn: func(net.Conn) {
			steps = append(steps, "close")
		},
		logReadError: func(error) {
			steps = append(steps, "log")
		},
	}

	got, ok := runtime.Prepare(nil)
	if ok || got != nil {
		t.Fatal("Prepare() unexpectedly succeeded on read error")
	}
	want := []string{"read", "close", "log"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Prepare() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelPrepareRuntimePrepareClosesEmptyHost(t *testing.T) {
	steps := make([]string, 0, 4)
	link := conn.NewLink("tcp", "", false, false, "", false, conn.LinkTimeout(0))
	runtime := clientP2PProviderChannelPrepareRuntime{
		readLink: func(net.Conn) (*conn.Link, error) {
			steps = append(steps, "read")
			return link, nil
		},
		ackLink: func(net.Conn, *conn.Link) bool {
			steps = append(steps, "ack")
			return true
		},
		logEmptyHost: func(*conn.Link) {
			steps = append(steps, "empty")
		},
		closeConn: func(net.Conn) {
			steps = append(steps, "close")
		},
	}

	got, ok := runtime.Prepare(nil)
	if ok || got != nil {
		t.Fatal("Prepare() unexpectedly succeeded with empty host")
	}
	want := []string{"read", "ack", "empty", "close"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Prepare() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelHandleRuntimeHandleSuccessfulRelay(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	targetServer, targetClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()
	defer func() { _ = targetServer.Close() }()
	defer func() { _ = targetClient.Close() }()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	steps := make([]string, 0, 5)

	runtime := clientP2PProviderChannelHandleRuntime{
		prepare: func(net.Conn) (*conn.Link, bool) {
			steps = append(steps, "prepare")
			return link, true
		},
		handleSpecial: func(*TRPClient, net.Conn, *conn.Link) bool {
			steps = append(steps, "special")
			return false
		},
		authorizeTarget: func(*TRPClient, net.Conn, *conn.Link) bool {
			steps = append(steps, "authorize")
			return true
		},
		connectTarget: func(*TRPClient, net.Conn, *conn.Link) (net.Conn, bool) {
			steps = append(steps, "connect")
			return targetServer, true
		},
		relayTarget: func(net.Conn, net.Conn, *conn.Link) {
			steps = append(steps, "relay")
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	want := []string{"prepare", "special", "authorize", "connect", "relay"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Handle() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelConnectRuntimeRejectsTypedNilTargetConn(t *testing.T) {
	var typedNil *typedNilClientConnStub
	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	var status conn.ConnectResultStatus
	steps := make([]string, 0, 4)

	runtime := clientP2PProviderChannelConnectRuntime{
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			steps = append(steps, "dial")
			return typedNil, nil
		},
		writeConnectResult: func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool {
			steps = append(steps, "write")
			status = conn.ConnectResultServerFailure
			return true
		},
		logDialError: func(*conn.Link, error) {
			steps = append(steps, "log")
		},
		closeSource: func(net.Conn) {
			steps = append(steps, "close_source")
		},
		closeTarget: func(net.Conn) {
			steps = append(steps, "close_target")
		},
	}

	got, ok := runtime.Connect(&TRPClient{}, nil, link)
	if ok || got != nil {
		t.Fatal("Connect() unexpectedly succeeded with typed nil target conn")
	}
	if status != conn.ConnectResultServerFailure {
		t.Fatalf("Connect() status = %v, want %v", status, conn.ConnectResultServerFailure)
	}
	want := []string{"dial", "write", "log", "close_source"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Connect() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelHandleRuntimeSkipsRelayForTypedNilTargetConn(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	var typedNil *typedNilClientConnStub
	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	relayCalled := false

	runtime := clientP2PProviderChannelHandleRuntime{
		prepare: func(net.Conn) (*conn.Link, bool) {
			return link, true
		},
		handleSpecial: func(*TRPClient, net.Conn, *conn.Link) bool {
			return false
		},
		authorizeTarget: func(*TRPClient, net.Conn, *conn.Link) bool {
			return true
		},
		connectTarget: func(*TRPClient, net.Conn, *conn.Link) (net.Conn, bool) {
			return typedNil, true
		},
		relayTarget: func(net.Conn, net.Conn, *conn.Link) {
			relayCalled = true
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	if relayCalled {
		t.Fatal("Handle() unexpectedly relayed typed nil provider target conn")
	}
}

func TestClientP2PProviderChannelHandleRuntimeSkipsRelayWhenConnectFails(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	relayCalled := false

	runtime := clientP2PProviderChannelHandleRuntime{
		prepare: func(net.Conn) (*conn.Link, bool) {
			return link, true
		},
		handleSpecial: func(*TRPClient, net.Conn, *conn.Link) bool {
			return false
		},
		authorizeTarget: func(*TRPClient, net.Conn, *conn.Link) bool {
			return true
		},
		connectTarget: func(*TRPClient, net.Conn, *conn.Link) (net.Conn, bool) {
			return nil, false
		},
		relayTarget: func(net.Conn, net.Conn, *conn.Link) {
			relayCalled = true
		},
	}

	runtime.Handle(&TRPClient{}, srcServer)

	if relayCalled {
		t.Fatal("Handle() unexpectedly relayed target after connect failure")
	}
}
