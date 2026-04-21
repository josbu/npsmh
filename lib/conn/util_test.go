package conn

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/crypt"
)

type countingLimiter struct {
	getCalls    int64
	returnCalls int64
	bytes       int64
}

func (l *countingLimiter) Get(size int64) {
	atomic.AddInt64(&l.getCalls, 1)
	atomic.AddInt64(&l.bytes, size)
}

func (l *countingLimiter) ReturnBucket(size int64) {
	atomic.AddInt64(&l.returnCalls, 1)
	atomic.AddInt64(&l.bytes, -size)
}

type fakeNetError struct {
	msg       string
	temporary bool
	timeout   bool
}

func (e fakeNetError) Error() string   { return e.msg }
func (e fakeNetError) Temporary() bool { return e.temporary }
func (e fakeNetError) Timeout() bool   { return e.timeout }

type addrOnlyConn struct {
	local  net.Addr
	remote net.Addr
}

func (c addrOnlyConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c addrOnlyConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c addrOnlyConn) Close() error                     { return nil }
func (c addrOnlyConn) LocalAddr() net.Addr              { return c.local }
func (c addrOnlyConn) RemoteAddr() net.Addr             { return c.remote }
func (c addrOnlyConn) SetDeadline(time.Time) error      { return nil }
func (c addrOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (c addrOnlyConn) SetWriteDeadline(time.Time) error { return nil }

func TestGetLenBytes(t *testing.T) {
	payload := []byte("hello")
	got, err := GetLenBytes(payload)
	if err != nil {
		t.Fatalf("GetLenBytes() error = %v", err)
	}

	if len(got) != 4+len(payload) {
		t.Fatalf("GetLenBytes() len = %d, want %d", len(got), 4+len(payload))
	}

	var n int32
	if err := binary.Read(bytes.NewReader(got[:4]), binary.LittleEndian, &n); err != nil {
		t.Fatalf("binary.Read(len) error = %v", err)
	}
	if int(n) != len(payload) {
		t.Fatalf("encoded length = %d, want %d", n, len(payload))
	}
	if !bytes.Equal(got[4:], payload) {
		t.Fatalf("payload = %q, want %q", got[4:], payload)
	}
}

func TestIsTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "net temporary", err: fakeNetError{msg: "temp", temporary: true}, want: false},
		{name: "net timeout", err: fakeNetError{msg: "timeout", timeout: true}, want: true},
		{name: "plain timeout text", err: errors.New("request timeout"), want: true},
		{name: "plain non-timeout text", err: io.EOF, want: false},
		{name: "plain non-timeout text", err: io.ErrNoProgress, want: false},
		{name: "net.Error without timeout flag", err: &net.DNSError{Err: "i/o timeout"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTimeout(tt.err); got != tt.want {
				t.Fatalf("IsTimeout(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestReadACKTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	err := ReadACK(client, 50*time.Millisecond)
	if err == nil {
		t.Fatal("ReadACK() error = nil, want timeout error")
	}
	if !IsTimeout(err) {
		t.Fatalf("ReadACK() error = %v, want timeout-compatible error", err)
	}
}

func TestGetTlsConn(t *testing.T) {
	crypt.InitTls(tls.Certificate{})
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	defer func() { _ = clientConn.Close() }()

	errCh := make(chan error, 1)
	go func() {
		tlsServer := crypt.NewTlsServerConn(serverConn)
		errCh <- tlsServer.(*tls.Conn).Handshake()
	}()

	tlsClient, err := GetTlsConn(clientConn, "example.com:443", false)
	if err != nil {
		t.Fatalf("GetTlsConn() error = %v", err)
	}
	defer func() { _ = tlsClient.Close() }()

	if err := <-errCh; err != nil {
		t.Fatalf("server TLS handshake error = %v", err)
	}
}

func TestGetTlsConnClosesRawConnOnHandshakeFailure(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()

	spy := &closeSpyConn{Conn: clientConn}
	writeErrCh := make(chan error, 1)
	go func() {
		_, err := serverConn.Write([]byte("not-tls"))
		_ = serverConn.Close()
		writeErrCh <- err
	}()

	if _, err := GetTlsConn(spy, "example.com:443", false); err == nil {
		t.Fatal("GetTlsConn() error = nil, want handshake failure")
	}
	if !spy.isClosed() {
		t.Fatal("GetTlsConn() should close raw conn on handshake failure")
	}
	if err := <-writeErrCh; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("server write error = %v", err)
	}
}

func TestGetTlsConnRejectsUntrustedCertificateWhenVerificationEnabled(t *testing.T) {
	crypt.InitTls(tls.Certificate{})
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()

	serverErrCh := make(chan error, 1)
	go func() {
		tlsServer := crypt.NewTlsServerConn(serverConn)
		serverErrCh <- tlsServer.(*tls.Conn).Handshake()
	}()

	if _, err := GetTlsConn(clientConn, "example.com:443", true); err == nil {
		t.Fatal("GetTlsConn() error = nil, want certificate verification failure")
	}
	if err := <-serverErrCh; err == nil {
		t.Fatal("server TLS handshake error = nil, want client rejection")
	}
}

func TestWriteACKAndReadACK(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		errCh <- WriteACK(server, 2*time.Second)
	}()

	if err := ReadACK(client, 2*time.Second); err != nil {
		t.Fatalf("ReadACK() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteACK() error = %v", err)
	}
}

func TestReadACKUnexpectedValue(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()
	errCh := make(chan error, 1)
	go func() {
		_ = server.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := server.Write([]byte("NAK"))
		errCh <- err
	}()

	err := ReadACK(client, 2*time.Second)
	if err == nil {
		t.Fatal("ReadACK() error = nil, want non-nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadACK() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server write error = %v", err)
	}
}

func TestWriteConnectResultAndReadConnectResult(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- WriteConnectResult(server, ConnectResultConnectionRefused, 2*time.Second)
	}()

	status, err := ReadConnectResult(client, 2*time.Second)
	if err != nil {
		t.Fatalf("ReadConnectResult() error = %v", err)
	}
	if status != ConnectResultConnectionRefused {
		t.Fatalf("ReadConnectResult() status = %d, want %d", status, ConnectResultConnectionRefused)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteConnectResult() error = %v", err)
	}
}

func TestNormalizeTargetIP(t *testing.T) {
	tests := []struct {
		name string
		src  net.IP
		dst  net.IP
		want net.IP
	}{
		{
			name: "ipv4 source without target",
			src:  net.ParseIP("192.0.2.10"),
			dst:  nil,
			want: net.IPv4zero,
		},
		{
			name: "ipv6 source without target",
			src:  net.ParseIP("2001:db8::10"),
			dst:  nil,
			want: net.IPv6zero,
		},
		{
			name: "ipv6 source normalizes ipv4 target",
			src:  net.ParseIP("2001:db8::10"),
			dst:  net.ParseIP("203.0.113.8"),
			want: append(net.IPv6zero[:12], net.ParseIP("203.0.113.8").To4()...),
		},
		{
			name: "matching family keeps target",
			src:  net.ParseIP("192.0.2.10"),
			dst:  net.ParseIP("198.51.100.8"),
			want: net.ParseIP("198.51.100.8"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTargetIP(tt.src, tt.dst)
			if !got.Equal(tt.want) {
				t.Fatalf("normalizeTargetIP(%v, %v) = %v, want %v", tt.src, tt.dst, got, tt.want)
			}
		})
	}
}

func TestBuildProxyProtocolHeaderByAddr(t *testing.T) {
	clientAddr := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 1234}

	if got := BuildProxyProtocolHeaderByAddr(clientAddr, nil, 0); got != nil {
		t.Fatalf("BuildProxyProtocolHeaderByAddr(protocol=0) = %q, want nil", got)
	}
	if got := BuildProxyProtocolHeaderByAddr(clientAddr, nil, 9); got != nil {
		t.Fatalf("BuildProxyProtocolHeaderByAddr(protocol=9) = %q, want nil", got)
	}

	v1 := BuildProxyProtocolHeaderByAddr(clientAddr, nil, 1)
	wantV1 := "PROXY TCP4 192.0.2.10 0.0.0.0 1234 0\r\n"
	if string(v1) != wantV1 {
		t.Fatalf("BuildProxyProtocolHeaderByAddr(v1) = %q, want %q", string(v1), wantV1)
	}

	v2 := BuildProxyProtocolHeaderByAddr(&net.UDPAddr{IP: net.ParseIP("2001:db8::10"), Port: 5353}, nil, 2)
	if len(v2) != 52 {
		t.Fatalf("BuildProxyProtocolHeaderByAddr(v2) len = %d, want 52", len(v2))
	}
	if !bytes.HasPrefix(v2, []byte("\r\n\r\n\x00\r\nQUIT\n")) {
		t.Fatalf("BuildProxyProtocolHeaderByAddr(v2) missing signature: %v", v2[:12])
	}
	if famProto := v2[13]; famProto != 0x22 {
		t.Fatalf("BuildProxyProtocolHeaderByAddr(v2) fam/proto = 0x%x, want 0x22", famProto)
	}
}

func TestBuildProxyProtocolHeaderFromConn(t *testing.T) {
	header := BuildProxyProtocolHeader(addrOnlyConn{
		local:  &net.TCPAddr{IP: net.ParseIP("198.51.100.20"), Port: 8080},
		remote: &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 3456},
	}, 1)

	want := "PROXY TCP4 192.0.2.20 198.51.100.20 3456 8080\r\n"
	if string(header) != want {
		t.Fatalf("BuildProxyProtocolHeader() = %q, want %q", string(header), want)
	}
}

func TestBuildProxyProtocolV2HeaderFallsBackToLocalForUnsupportedAddr(t *testing.T) {
	header := BuildProxyProtocolHeader(addrOnlyConn{
		local:  dummyAddr("local"),
		remote: dummyAddr("remote"),
	}, 2)

	if len(header) != 16 {
		t.Fatalf("BuildProxyProtocolHeader(v2 unsupported) len = %d, want 16", len(header))
	}
	if !bytes.HasPrefix(header, []byte("\r\n\r\n\x00\r\nQUIT\n")) {
		t.Fatalf("BuildProxyProtocolHeader(v2 unsupported) missing signature: %v", header[:12])
	}
	if header[12] != 0x20 {
		t.Fatalf("BuildProxyProtocolHeader(v2 unsupported) ver/cmd = 0x%x, want 0x20", header[12])
	}
	if header[13] != 0x00 {
		t.Fatalf("BuildProxyProtocolHeader(v2 unsupported) fam/proto = 0x%x, want 0x00", header[13])
	}
}

func TestCopyWaitGroupAppliesBothConnLimiters(t *testing.T) {
	leftA, leftB := net.Pipe()
	rightA, rightB := net.Pipe()
	defer func() { _ = leftB.Close() }()
	defer func() { _ = rightB.Close() }()

	bridgeLimiter := &countingLimiter{}
	serviceLimiter := &countingLimiter{}
	done := make(chan struct{})

	go func() {
		CopyWaitGroup(leftA, rightA, false, false, bridgeLimiter, serviceLimiter, nil, false, 0, nil, nil, false, false)
		close(done)
	}()

	payload := []byte("hello")
	writeDone := make(chan error, 1)
	go func() {
		_, err := rightB.Write(payload)
		_ = rightB.Close()
		writeDone <- err
	}()

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(leftB, buf); err != nil {
		t.Fatalf("ReadFull(leftB) error = %v", err)
	}
	if !bytes.Equal(buf, payload) {
		t.Fatalf("payload = %q, want %q", buf, payload)
	}
	_ = leftB.Close()

	if err := <-writeDone; err != nil {
		t.Fatalf("Write(rightB) error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("CopyWaitGroup() did not finish")
	}

	if got := atomic.LoadInt64(&bridgeLimiter.bytes); got != int64(len(payload)) {
		t.Fatalf("bridge limiter bytes = %d, want %d", got, len(payload))
	}
	if got := atomic.LoadInt64(&serviceLimiter.bytes); got != int64(len(payload)) {
		t.Fatalf("service limiter bytes = %d, want %d", got, len(payload))
	}
	if got := atomic.LoadInt64(&bridgeLimiter.getCalls); got == 0 {
		t.Fatal("bridge limiter Get() was not called")
	}
	if got := atomic.LoadInt64(&serviceLimiter.getCalls); got == 0 {
		t.Fatal("service limiter Get() was not called")
	}
}

func TestGetRealIP(t *testing.T) {
	t.Run("custom header wins", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		req.RemoteAddr = "10.0.0.9:1234"
		req.Header.Set("X-Client-IP", "203.0.113.10:8443, 198.51.100.10")

		if got := GetRealIP(req, "X-Client-IP"); got != "203.0.113.10" {
			t.Fatalf("GetRealIP(custom) = %q, want %q", got, "203.0.113.10")
		}
	})

	t.Run("custom header falls back to remote addr", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		req.RemoteAddr = "10.0.0.9:1234"

		if got := GetRealIP(req, "X-Client-IP"); got != "10.0.0.9" {
			t.Fatalf("GetRealIP(custom fallback) = %q, want %q", got, "10.0.0.9")
		}
	})

	t.Run("xff used before x-real-ip", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		req.RemoteAddr = "10.0.0.9:1234"
		req.Header.Set("X-Forwarded-For", "198.51.100.22, 192.0.2.1")
		req.Header.Set("X-Real-IP", "203.0.113.20")

		if got := GetRealIP(req, ""); got != "198.51.100.22" {
			t.Fatalf("GetRealIP(xff) = %q, want %q", got, "198.51.100.22")
		}
	})
}

func TestParseTCPAddrMaybe(t *testing.T) {
	addr, err := parseTCPAddrMaybe("")
	if err != nil || addr != nil {
		t.Fatalf("parseTCPAddrMaybe(\"\") = (%v, %v), want (nil, nil)", addr, err)
	}

	addr, err = parseTCPAddrMaybe("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("parseTCPAddrMaybe(valid) error = %v", err)
	}
	if addr == nil || addr.IP.String() != "127.0.0.1" || addr.Port != 8080 {
		t.Fatalf("parseTCPAddrMaybe(valid) = %+v, want 127.0.0.1:8080", addr)
	}

	if _, err := parseTCPAddrMaybe("bad-addr"); err == nil {
		t.Fatal("parseTCPAddrMaybe(invalid) error = nil, want non-nil")
	}
}
