package client

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
)

func TestClientShutdownRuntimeRunsStepsInOrder(t *testing.T) {
	var calls []string
	runtime := clientShutdownRuntime{
		captureReason: func(*TRPClient) {
			calls = append(calls, "reason")
		},
		logSummary: func(*TRPClient) {
			calls = append(calls, "summary")
		},
		stopHealth: func(*TRPClient) {
			calls = append(calls, "health")
		},
		cancelContext: func(*TRPClient) {
			calls = append(calls, "cancel")
		},
		closeTunnel: func(*TRPClient, string) {
			calls = append(calls, "tunnel")
		},
		closeSignal: func(*TRPClient) {
			calls = append(calls, "signal")
		},
		stopTicker: func(*TRPClient) {
			calls = append(calls, "ticker")
		},
		closeFiles: func(*TRPClient) {
			calls = append(calls, "files")
		},
	}

	runtime.Shutdown(&TRPClient{})

	want := []string{"reason", "health", "cancel", "tunnel", "signal", "ticker", "files", "summary"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("shutdown calls = %#v, want %#v", calls, want)
	}
}

func TestCaptureCloseReasonRuntimeUsesClosedSignal(t *testing.T) {
	server, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = clientConn.Close()
	})

	trp := &TRPClient{
		signal: conn.NewConn(clientConn),
	}

	_ = trp.signal.Close()
	trp.captureCloseReasonRuntime()

	if got := trp.getCloseReason(); got != "signal connection closed" {
		t.Fatalf("getCloseReason() = %q, want %q", got, "signal connection closed")
	}
}

func TestTRPClientCloseShutsDownOwnedFileServers(t *testing.T) {
	fsm := NewFileServerManager(context.Background())
	tunnel := &file.Tunnel{
		Mode:      "file",
		ServerIp:  "127.0.0.1",
		Ports:     "0",
		LocalPath: t.TempDir(),
	}
	fsm.StartFileServer(tunnel, "vkey")
	listener, ok := waitListener(fsm, time.Second)
	if !ok {
		t.Fatal("file listener not registered")
	}

	trp := &TRPClient{fsm: fsm}
	trp.Close()

	if _, err := listener.Accept(); err == nil {
		t.Fatal("listener Accept() succeeded after TRPClient.Close()")
	}
}

func TestBuildDisconnectSummaryDefaultsReasonAndReportsSignalState(t *testing.T) {
	server, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = clientConn.Close()
	})

	now := time.Now()
	trp := &TRPClient{
		svrAddr:        "127.0.0.1:8024",
		bridgeConnType: "tcp",
		uuid:           "client-uuid",
		signal:         conn.NewConn(clientConn),
		startedNano:    now.Add(-3 * time.Second).UnixNano(),
	}

	summary, ok := trp.buildDisconnectSummary(now)
	if !ok {
		t.Fatal("buildDisconnectSummary() should produce a summary")
	}
	if summary.Reason != "close requested" {
		t.Fatalf("summary.Reason = %q, want close requested", summary.Reason)
	}
	if !summary.SignalUp {
		t.Fatal("summary.SignalUp = false, want true")
	}
	if summary.TunnelUp {
		t.Fatal("summary.TunnelUp = true, want false without tunnel")
	}
	if summary.Uptime < 2900*time.Millisecond || summary.Uptime > 3100*time.Millisecond {
		t.Fatalf("summary.Uptime = %s, want about 3s", summary.Uptime)
	}
}

func TestBuildDisconnectSummaryReturnsFalseForEmptyState(t *testing.T) {
	summary, ok := (&TRPClient{}).buildDisconnectSummary(time.Now())
	if ok {
		t.Fatalf("buildDisconnectSummary() = %#v, want no summary", summary)
	}
}

func TestCloseTunnelRuntimeIgnoresTypedNilMuxTunnelAndClearsState(t *testing.T) {
	oldCloseMux := clientCloseMuxTunnel
	t.Cleanup(func() {
		clientCloseMuxTunnel = oldCloseMux
	})

	called := false
	clientCloseMuxTunnel = func(*mux.Mux) error {
		called = true
		return nil
	}

	trp := &TRPClient{tunnel: (*mux.Mux)(nil)}
	trp.closeTunnelRuntime("close")

	if called {
		t.Fatal("closeTunnelRuntime() should ignore typed nil mux tunnel")
	}
	if trp.tunnel != nil {
		t.Fatalf("trp.tunnel = %#v, want nil", trp.tunnel)
	}
}

func TestCloseTunnelRuntimeIgnoresTypedNilQUICConnAndClearsState(t *testing.T) {
	oldCloseQUIC := clientCloseQUICConn
	t.Cleanup(func() {
		clientCloseQUICConn = oldCloseQUIC
	})

	called := false
	clientCloseQUICConn = func(_ *quic.Conn, reason string) error {
		called = true
		return nil
	}

	trp := &TRPClient{tunnel: (*quic.Conn)(nil)}
	trp.closeTunnelRuntime("close")

	if called {
		t.Fatal("closeTunnelRuntime() should ignore typed nil quic tunnel")
	}
	if trp.tunnel != nil {
		t.Fatalf("trp.tunnel = %#v, want nil", trp.tunnel)
	}
}

func TestTRPClientCloseWithoutStartDoesNotPanic(t *testing.T) {
	c := &TRPClient{}
	c.Close()
	c.Close()
}
