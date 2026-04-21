package conn

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/quic-go/quic-go"
)

func TestQuicAutoCloseConnCloseReturnsPromptlyWhenPeerKeepsStreamOpen(t *testing.T) {
	crypt.InitTls(tls.Certificate{})

	listener, err := quic.ListenAddr("127.0.0.1:0", crypt.GetCertCfg(), &quic.Config{})
	if err != nil {
		t.Fatalf("quic.ListenAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	serverSessCh := make(chan *quic.Conn, 1)
	serverErrCh := make(chan error, 1)
	go func() {
		sess, err := listener.Accept(context.Background())
		if err != nil {
			serverErrCh <- err
			return
		}
		serverSessCh <- sess
	}()

	clientSess, err := quic.DialAddr(context.Background(), listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("quic.DialAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientSess.CloseWithError(0, "test cleanup")
	})

	clientStream, err := clientSess.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync() error = %v", err)
	}
	t.Cleanup(func() {
		_ = clientStream.Close()
	})
	if _, err := clientStream.Write([]byte("init")); err != nil {
		t.Fatalf("clientStream.Write() error = %v", err)
	}

	var serverSess *quic.Conn
	select {
	case err := <-serverErrCh:
		t.Fatalf("listener.Accept() error = %v", err)
	case serverSess = <-serverSessCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server quic session")
	}

	serverStream, err := serverSess.AcceptStream(context.Background())
	if err != nil {
		t.Fatalf("AcceptStream() error = %v", err)
	}

	qc := NewQuicAutoCloseConn(serverStream, serverSess)
	started := time.Now()
	if err := qc.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Close() took %s, want < 200ms even when peer keeps stream open", elapsed)
	}

	select {
	case <-serverSess.Context().Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("server quic session should close promptly with the auto-close conn")
	}
}

func TestDialQuicWithLocalIPClosesDedicatedPacketConnWhenSessionEnds(t *testing.T) {
	crypt.InitTls(tls.Certificate{})

	listener, err := quic.ListenAddr("127.0.0.1:0", crypt.GetCertCfg(), &quic.Config{})
	if err != nil {
		t.Fatalf("quic.ListenAddr() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		sess, err := listener.Accept(context.Background())
		if err != nil {
			return
		}
		defer func() { _ = sess.CloseWithError(0, "server cleanup") }()
		stream, err := sess.AcceptStream(context.Background())
		if err != nil {
			return
		}
		defer func() { _ = stream.Close() }()
		buf := make([]byte, 4)
		_, _ = stream.Read(buf)
	}()

	var spy *net.UDPConn
	oldListen := listenQUICPacketConn
	listenQUICPacketConn = func(localIP string) (net.PacketConn, error) {
		pc, err := net.ListenUDP("udp", common.BuildUDPBindAddr(localIP))
		if err != nil {
			return nil, err
		}
		spy = pc
		return spy, nil
	}
	t.Cleanup(func() {
		listenQUICPacketConn = oldListen
	})

	sess, err := DialQuicWithLocalIP(context.Background(), listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
	}, &quic.Config{}, "127.0.0.1")
	if err != nil {
		t.Fatalf("DialQuicWithLocalIP() error = %v", err)
	}
	if spy == nil {
		t.Fatal("listenQUICPacketConn spy was not installed")
	}

	stream, err := sess.OpenStreamSync(context.Background())
	if err != nil {
		t.Fatalf("OpenStreamSync() error = %v", err)
	}
	if _, err := stream.Write([]byte("init")); err != nil {
		t.Fatalf("stream.Write() error = %v", err)
	}
	_ = stream.Close()

	if err := sess.CloseWithError(0, "client close"); err != nil {
		t.Fatalf("CloseWithError() error = %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = spy.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
		_, err := spy.WriteToUDP([]byte("x"), listener.Addr().(*net.UDPAddr))
		if errors.Is(err, net.ErrClosed) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dedicated quic packet conn should close when session ends")
}

func TestQuicStreamConnHelpersHandleNilState(t *testing.T) {
	var nilConn *QuicStreamConn
	if got := nilConn.GetSession(); got != nil {
		t.Fatalf("nil GetSession() = %v, want nil", got)
	}
	if got := nilConn.LocalAddr(); got != nil {
		t.Fatalf("nil LocalAddr() = %v, want nil", got)
	}
	if got := nilConn.RemoteAddr(); got != nil {
		t.Fatalf("nil RemoteAddr() = %v, want nil", got)
	}
	if _, err := nilConn.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := nilConn.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Write() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilConn.SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil SetDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilConn.SetReadDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil SetReadDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilConn.SetWriteDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil SetWriteDeadline() error = %v, want %v", err, net.ErrClosed)
	}

	malformed := &QuicStreamConn{}
	if got := malformed.GetSession(); got != nil {
		t.Fatalf("malformed GetSession() = %v, want nil", got)
	}
	if got := malformed.LocalAddr(); got != nil {
		t.Fatalf("malformed LocalAddr() = %v, want nil", got)
	}
	if got := malformed.RemoteAddr(); got != nil {
		t.Fatalf("malformed RemoteAddr() = %v, want nil", got)
	}
	if _, err := malformed.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := malformed.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed Write() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformed.SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed SetDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformed.SetReadDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed SetReadDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformed.SetWriteDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed SetWriteDeadline() error = %v, want %v", err, net.ErrClosed)
	}

	var nilAuto *QuicAutoCloseConn
	if err := nilAuto.Close(); err != nil {
		t.Fatalf("nil QuicAutoCloseConn.Close() error = %v, want nil", err)
	}

	malformedAuto := &QuicAutoCloseConn{QuicStreamConn: &QuicStreamConn{}}
	if err := malformedAuto.Close(); err != nil {
		t.Fatalf("malformed QuicAutoCloseConn.Close() error = %v, want nil", err)
	}
}
