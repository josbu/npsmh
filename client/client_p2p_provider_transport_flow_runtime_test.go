package client

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go/v5"
)

type stubProviderMux struct {
	closed int
}

func (m *stubProviderMux) Accept() (net.Conn, error) { return nil, errors.New("unused") }
func (m *stubProviderMux) Close() error {
	m.closed++
	return nil
}
func (m *stubProviderMux) Addr() net.Addr { return &net.TCPAddr{} }

func TestClientP2PProviderQUICStreamRuntimeUsesAcceptedSessionBuilder(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	session := &stubAcceptedQUICSession{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}}
	runtime := clientP2PProviderQUICStreamRuntime{
		acceptStream: func(*providerTransportRuntime, quicProviderAcceptedSession) (*quic.Stream, error) {
			return nil, nil
		},
		buildConn: func(sess quicProviderAcceptedSession, stream *quic.Stream) net.Conn {
			if sess != session {
				t.Fatalf("buildConn() session = %#v, want %#v", sess, session)
			}
			if stream != nil {
				t.Fatalf("buildConn() stream = %#v, want nil", stream)
			}
			return srcServer
		},
	}

	c, err := runtime.NextConn(&providerTransportRuntime{}, session)
	if err != nil {
		t.Fatalf("NextConn() error = %v, want nil", err)
	}
	if c != srcServer {
		t.Fatalf("NextConn() conn = %#v, want %#v", c, srcServer)
	}
}

func TestClientP2PProviderQUICStreamRuntimeReportsAcceptError(t *testing.T) {
	runtime := clientP2PProviderQUICStreamRuntime{
		acceptStream: func(*providerTransportRuntime, quicProviderAcceptedSession) (*quic.Stream, error) {
			return nil, errors.New("stream failed")
		},
		buildConn: func(quicProviderAcceptedSession, *quic.Stream) net.Conn {
			t.Fatal("buildConn() unexpectedly called after accept error")
			return nil
		},
	}

	_, err := runtime.NextConn(&providerTransportRuntime{}, &stubAcceptedQUICSession{})
	if err == nil || err.Error() != "stream failed" {
		t.Fatalf("NextConn() error = %v, want stream failed", err)
	}
}

func TestStubAcceptedQUICSessionBuildConnCompilesForInterface(t *testing.T) {
	session := &stubAcceptedQUICSession{}
	if conn := session.BuildConn(nil); conn != nil {
		t.Fatalf("BuildConn() = %#v, want nil", conn)
	}
	if _, err := session.AcceptStream(context.Background()); err == nil {
		t.Fatal("AcceptStream() error = nil, want non-nil")
	}
}

func TestClientP2PProviderKCPMuxRuntimeBuildsAcceptsAndClosesMux(t *testing.T) {
	steps := make([]string, 0, 6)
	mx := &stubProviderMux{}
	runtime := clientP2PProviderKCPMuxRuntime{
		setUDPSession: func(*kcp.UDPSession) {
			steps = append(steps, "udp")
		},
		logEstablished: func(net.Addr) {
			steps = append(steps, "established")
		},
		newMux: func(*TRPClient, *kcp.UDPSession) providerAcceptedMux {
			steps = append(steps, "mux")
			return mx
		},
		acceptMux: func(providerAcceptedMux, func(net.Conn)) {
			steps = append(steps, "accept")
		},
		dispatchConn: func(*TRPClient, *providerPunchSession, net.Conn) {
			steps = append(steps, "dispatch")
		},
		logClosed: func(net.Addr) {
			steps = append(steps, "closed")
		},
	}

	runtime.Serve(&TRPClient{}, &providerPunchSession{}, &kcp.UDPSession{})

	want := []string{"udp", "established", "mux", "accept", "closed"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Serve() steps = %v, want %v", steps, want)
	}
	if mx.closed != 1 {
		t.Fatalf("Serve() closed mux %d times, want 1", mx.closed)
	}
}

func TestClientP2PProviderQUICServeRuntimeDispatchesUntilStreamError(t *testing.T) {
	srcServer, srcClient := net.Pipe()
	defer func() { _ = srcServer.Close() }()
	defer func() { _ = srcClient.Close() }()

	steps := make([]string, 0, 4)
	session := &stubAcceptedQUICSession{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}}
	index := 0
	runtime := clientP2PProviderQUICServeRuntime{
		nextConn: func(*providerTransportRuntime, quicProviderAcceptedSession) (net.Conn, error) {
			index++
			if index == 1 {
				steps = append(steps, "next")
				return srcServer, nil
			}
			steps = append(steps, "next_err")
			return nil, errors.New("stream closed")
		},
		reportStreamError: func(quicProviderAcceptedSession, error) {
			steps = append(steps, "error")
		},
		dispatchConn: func(*TRPClient, *providerPunchSession, net.Conn) {
			steps = append(steps, "dispatch")
		},
	}

	runtime.Serve(&TRPClient{}, &providerTransportRuntime{}, &providerPunchSession{}, session)

	want := []string{"next", "dispatch", "next_err", "error"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Serve() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderKCPServeRuntimeBuildsMuxDispatchesAndCloses(t *testing.T) {
	steps := make([]string, 0, 6)
	tunnel := &stubAcceptedKCPTunnel{}
	runtime := clientP2PProviderKCPServeRuntime{
		serveMux: func(*TRPClient, *providerPunchSession, kcpProviderAcceptedTunnel) {
			steps = append(steps, "serve")
		},
	}

	runtime.Serve(&TRPClient{}, &providerPunchSession{}, tunnel)

	want := []string{"serve"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Serve() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderQUICAcceptRuntimeRejectsUnexpectedPeerAndServesAcceptedSession(t *testing.T) {
	bad := &stubAcceptedQUICSession{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 1}}
	good := &stubAcceptedQUICSession{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2}}
	index := 0
	steps := make([]string, 0, 6)

	runtime := clientP2PProviderQUICAcceptRuntime{
		contextOf: func(*providerTransportRuntime) context.Context {
			return context.Background()
		},
		acceptSession: func(*providerTransportRuntime) (quicProviderAcceptedSession, error) {
			steps = append(steps, "accept")
			index++
			if index == 1 {
				return bad, nil
			}
			return good, nil
		},
		reportAcceptError: func(*providerPunchSession, error) {
			steps = append(steps, "error")
		},
		remoteMatches: func(_ *providerPunchSession, remote net.Addr) bool {
			return remote.String() == good.remoteAddr.String()
		},
		rejectUnexpected: func(sess quicProviderAcceptedSession) {
			steps = append(steps, "reject")
			_ = sess.CloseWithError(0, "unexpected peer")
		},
		promote: func(*providerTransportRuntime, *providerPunchSession, quicProviderSessionCloser) bool {
			steps = append(steps, "promote")
			return true
		},
		serveEstablished: func(*TRPClient, *providerTransportRuntime, *providerPunchSession, quicProviderAcceptedSession) {
			steps = append(steps, "serve")
		},
	}

	runtime.Serve(&TRPClient{}, &providerTransportRuntime{}, &providerPunchSession{})

	want := []string{"accept", "reject", "accept", "promote", "serve"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Serve() steps = %v, want %v", steps, want)
	}
	if !reflect.DeepEqual(bad.closeReasons, []string{"unexpected peer"}) {
		t.Fatalf("unexpected session close reasons = %v, want unexpected peer", bad.closeReasons)
	}
}

func TestClientP2PProviderKCPAcceptRuntimeReportsAcceptError(t *testing.T) {
	steps := make([]string, 0, 3)
	runtime := clientP2PProviderKCPAcceptRuntime{
		contextOf: func(*providerTransportRuntime) context.Context {
			return context.Background()
		},
		acceptTunnel: func(*providerTransportRuntime) (kcpProviderAcceptedTunnel, error) {
			steps = append(steps, "accept")
			return nil, errors.New("accept failed")
		},
		reportAcceptError: func(*providerPunchSession, net.Addr, error) {
			steps = append(steps, "error")
		},
		remoteMatches: func(*providerPunchSession, net.Addr) bool {
			return true
		},
		rejectUnexpected: func(kcpProviderAcceptedTunnel) {
			steps = append(steps, "reject")
		},
		promote: func(*providerTransportRuntime, *providerPunchSession, providerTunnelCloser) bool {
			steps = append(steps, "promote")
			return true
		},
		serveEstablished: func(*TRPClient, *providerPunchSession, kcpProviderAcceptedTunnel) {
			steps = append(steps, "serve")
		},
	}

	runtime.Serve(&TRPClient{}, &providerTransportRuntime{}, &providerPunchSession{})

	want := []string{"accept", "error"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Serve() steps = %v, want %v", steps, want)
	}
}

func TestClientP2PProviderKCPAcceptRuntimeServesAcceptedTunnel(t *testing.T) {
	tunnel := &stubAcceptedKCPTunnel{remoteAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2}}
	steps := make([]string, 0, 4)
	runtime := clientP2PProviderKCPAcceptRuntime{
		contextOf: func(*providerTransportRuntime) context.Context {
			return context.Background()
		},
		acceptTunnel: func(*providerTransportRuntime) (kcpProviderAcceptedTunnel, error) {
			steps = append(steps, "accept")
			return tunnel, nil
		},
		reportAcceptError: func(*providerPunchSession, net.Addr, error) {
			steps = append(steps, "error")
		},
		remoteMatches: func(*providerPunchSession, net.Addr) bool {
			return true
		},
		rejectUnexpected: func(kcpProviderAcceptedTunnel) {
			steps = append(steps, "reject")
		},
		promote: func(*providerTransportRuntime, *providerPunchSession, providerTunnelCloser) bool {
			steps = append(steps, "promote")
			return true
		},
		serveEstablished: func(*TRPClient, *providerPunchSession, kcpProviderAcceptedTunnel) {
			steps = append(steps, "serve")
		},
	}

	runtime.Serve(&TRPClient{}, &providerTransportRuntime{}, &providerPunchSession{})

	want := []string{"accept", "promote", "serve"}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Serve() steps = %v, want %v", steps, want)
	}
	if tunnel.closed != 0 {
		t.Fatalf("Serve() closed accepted tunnel %d times, want 0", tunnel.closed)
	}
}
