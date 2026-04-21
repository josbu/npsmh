package client

import (
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
)

func TestClientP2PProviderChannelConnectRuntimeConnectSuccessWritesOK(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	targetServer, targetClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()
	defer func() { _ = targetServer.Close() }()
	defer func() { _ = targetClient.Close() }()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	steps := make([]string, 0, 2)
	runtime := clientP2PProviderChannelConnectRuntime{
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			steps = append(steps, "dial")
			return targetServer, nil
		},
		writeConnectResult: func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool {
			steps = append(steps, "write")
			return true
		},
	}

	got, ok := runtime.Connect(&TRPClient{}, srcServer, link)
	if !ok || got != targetServer {
		t.Fatal("Connect() did not return established target")
	}
	want := []string{"dial", "write"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Connect() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelConnectRuntimeConnectDialErrorClosesSource(t *testing.T) {
	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	dialErr := errors.New("dial failed")
	var status conn.ConnectResultStatus
	steps := make([]string, 0, 4)
	runtime := clientP2PProviderChannelConnectRuntime{
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			steps = append(steps, "dial")
			return nil, dialErr
		},
		writeConnectResult: func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool {
			steps = append(steps, "write")
			status = conn.DialConnectResult(dialErr)
			return true
		},
		logDialError: func(*conn.Link, error) {
			steps = append(steps, "log")
		},
		closeSource: func(net.Conn) {
			steps = append(steps, "close_source")
		},
	}

	got, ok := runtime.Connect(&TRPClient{}, nil, link)
	if ok || got != nil {
		t.Fatal("Connect() unexpectedly succeeded on dial error")
	}
	if status != conn.DialConnectResult(dialErr) {
		t.Fatalf("Connect() status = %v, want %v", status, conn.DialConnectResult(dialErr))
	}
	want := []string{"dial", "write", "log", "close_source"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Connect() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelConnectRuntimeConnectClosesBothWhenWriteFails(t *testing.T) {
	targetServer, targetClient := net.Pipe()
	defer func() { _ = targetServer.Close() }()
	defer func() { _ = targetClient.Close() }()

	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(0))
	steps := make([]string, 0, 4)
	runtime := clientP2PProviderChannelConnectRuntime{
		dialTarget: func(*TRPClient, *conn.Link) (net.Conn, error) {
			steps = append(steps, "dial")
			return targetServer, nil
		},
		writeConnectResult: func(net.Conn, *conn.Link, conn.ConnectResultStatus) bool {
			steps = append(steps, "write")
			return false
		},
		closeTarget: func(net.Conn) {
			steps = append(steps, "close_target")
		},
		closeSource: func(net.Conn) {
			steps = append(steps, "close_source")
		},
	}

	got, ok := runtime.Connect(&TRPClient{}, nil, link)
	if ok || got != nil {
		t.Fatal("Connect() unexpectedly succeeded when result write failed")
	}
	want := []string{"dial", "write", "close_target", "close_source"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Connect() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderChannelResultRuntimeSkipsWriteWhenConnectResultNotRequested(t *testing.T) {
	called := false
	runtime := clientP2PProviderChannelResultRuntime{
		write: func(net.Conn, conn.ConnectResultStatus, time.Duration) error {
			called = true
			return nil
		},
	}

	if !runtime.Write(nil, conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(time.Second)), conn.ConnectResultOK) {
		t.Fatal("Write() = false, want true when connect result is not requested")
	}
	if called {
		t.Fatal("Write() unexpectedly called low-level writer")
	}
}

func TestClientP2PProviderChannelResultRuntimeWritesRequestedConnectResult(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	runtime := clientP2PProviderChannelResultRuntime{
		write: conn.WriteConnectResult,
	}
	link := conn.NewLink("tcp", "127.0.0.1:18080", false, false, "", false, conn.LinkTimeout(time.Second), conn.WithConnectResult(true))

	okCh := make(chan bool, 1)
	go func() {
		okCh <- runtime.Write(server, link, conn.ConnectResultConnectionRefused)
	}()

	status, err := conn.ReadConnectResult(client, time.Second)
	if err != nil {
		t.Fatalf("ReadConnectResult() error = %v", err)
	}
	if status != conn.ConnectResultConnectionRefused {
		t.Fatalf("connect result = %v, want %v", status, conn.ConnectResultConnectionRefused)
	}
	if ok := <-okCh; !ok {
		t.Fatal("Write() = false, want true")
	}
}

func TestClientP2PProviderChannelRelayRuntimeLogsAndCopies(t *testing.T) {
	steps := make([]string, 0, 3)
	runtime := clientP2PProviderChannelRelayRuntime{
		logRelay: func(*conn.Link) {
			steps = append(steps, "log")
		},
		isFramed: func(*conn.Link) bool {
			steps = append(steps, "framed")
			return true
		},
		copy: func(net.Conn, net.Conn, *conn.Link, bool) {
			steps = append(steps, "copy")
		},
	}

	runtime.Relay(nil, nil, &conn.Link{ConnType: "udp", Host: "127.0.0.1:18080"})

	want := []string{"log", "framed", "copy"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Relay() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderRuntimeRootDispatchChannelWrapsAssociationAndLaunches(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	var (
		wrappedAssociation string
		dispatchedConn     net.Conn
	)
	root := &clientP2PProviderRuntimeRoot{
		channelLaunchAsync: func(run func()) {
			if run != nil {
				run()
			}
		},
		channelHandle: clientP2PProviderChannelHandleRuntime{
			prepare: func(src net.Conn) (*conn.Link, bool) {
				dispatchedConn = src
				return nil, false
			},
		},
	}

	root.dispatchChannel(&TRPClient{}, &providerPunchSession{associationID: "assoc-1"}, srcServer)
	wrappedAssociation = p2pAssociationIDFromConn(dispatchedConn)

	if wrappedAssociation != "assoc-1" {
		t.Fatalf("dispatchChannel() associationID = %q, want %q", wrappedAssociation, "assoc-1")
	}
	if dispatchedConn == nil || dispatchedConn == srcServer {
		t.Fatalf("dispatchChannel() conn = %#v, want wrapped conn", dispatchedConn)
	}
}

func TestClientP2PProviderRuntimeRootDispatchChannelSkipsTypedNilConn(t *testing.T) {
	var typedNil *typedNilClientConnStub
	called := false

	root := &clientP2PProviderRuntimeRoot{
		channelLaunchAsync: func(run func()) {
			called = true
			if run != nil {
				run()
			}
		},
		channelHandle: clientP2PProviderChannelHandleRuntime{
			prepare: func(net.Conn) (*conn.Link, bool) {
				called = true
				return nil, false
			},
		},
	}

	root.dispatchChannel(&TRPClient{}, &providerPunchSession{associationID: "assoc-1"}, typedNil)

	if called {
		t.Fatal("dispatchChannel() unexpectedly launched typed nil conn")
	}
}

func TestClientP2PProviderRuntimeRootDispatchChannelSkipsNilConn(t *testing.T) {
	called := false
	root := &clientP2PProviderRuntimeRoot{
		channelLaunchAsync: func(run func()) {
			called = true
			if run != nil {
				run()
			}
		},
		channelHandle: clientP2PProviderChannelHandleRuntime{
			prepare: func(net.Conn) (*conn.Link, bool) {
				called = true
				return nil, false
			},
		},
	}

	root.dispatchChannel(&TRPClient{}, &providerPunchSession{associationID: "assoc-1"}, nil)

	if called {
		t.Fatal("dispatchChannel() unexpectedly called launch/handle for nil conn")
	}
}

func TestClientP2PProviderRuntimeRootDispatchChannelUsesInjectedHandler(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	called := false
	root := &clientP2PProviderRuntimeRoot{
		channelHandle: clientP2PProviderChannelHandleRuntime{
			prepare: func(src net.Conn) (*conn.Link, bool) {
				called = src == srcServer
				return nil, false
			},
		},
	}

	root.dispatchChannel(&TRPClient{}, nil, srcServer)

	if !called {
		t.Fatal("dispatchChannel() did not use channel handle runtime")
	}
}

func TestClientP2PProviderRuntimeRootDispatchChannelUsesInjectedLauncher(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	steps := make([]string, 0, 2)
	root := &clientP2PProviderRuntimeRoot{}
	root.channelLaunchAsync = func(run func()) {
		steps = append(steps, "launch")
		if run != nil {
			run()
		}
	}
	root.channelHandle = clientP2PProviderChannelHandleRuntime{
		prepare: func(c net.Conn) (*conn.Link, bool) {
			if c != srcServer {
				t.Fatalf("prepare() conn = %#v, want %#v", c, srcServer)
			}
			steps = append(steps, "handle")
			return nil, false
		},
	}

	root.dispatchChannel(&TRPClient{}, nil, srcServer)

	if len(steps) != 2 || steps[0] != "launch" || steps[1] != "handle" {
		t.Fatalf("dispatchChannel() steps = %v, want [launch handle]", steps)
	}
}

func TestClientP2PProviderRuntimeRootDispatchChannelRunsInlineWhenLauncherMissing(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	steps := make([]string, 0, 1)
	root := &clientP2PProviderRuntimeRoot{
		channelHandle: clientP2PProviderChannelHandleRuntime{
			prepare: func(c net.Conn) (*conn.Link, bool) {
				if c != srcServer {
					t.Fatalf("prepare() conn = %#v, want %#v", c, srcServer)
				}
				steps = append(steps, "handle")
				return nil, false
			},
		},
	}

	root.dispatchChannel(&TRPClient{}, nil, srcServer)

	if len(steps) != 1 || steps[0] != "handle" {
		t.Fatalf("dispatchChannel() steps = %v, want [handle]", steps)
	}
}

func TestClientP2PProviderChannelWrapRuntimeWrapsAssociationID(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	got := wrapP2PAssociationConn(srcServer, "assoc-1")
	if p2pAssociationIDFromConn(got) != "assoc-1" {
		t.Fatalf("wrapP2PAssociationConn() associationID = %q, want %q", p2pAssociationIDFromConn(got), "assoc-1")
	}
}
