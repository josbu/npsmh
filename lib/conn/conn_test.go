package conn

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

type overlapDetectConn struct {
	active   atomic.Int32
	overlaps atomic.Int32
}

func (c *overlapDetectConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (c *overlapDetectConn) Close() error                       { return nil }
func (c *overlapDetectConn) LocalAddr() net.Addr                { return dummyAddr("local") }
func (c *overlapDetectConn) RemoteAddr() net.Addr               { return dummyAddr("remote") }
func (c *overlapDetectConn) SetDeadline(_ time.Time) error      { return nil }
func (c *overlapDetectConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *overlapDetectConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *overlapDetectConn) Write(b []byte) (int, error) {
	if !c.active.CompareAndSwap(0, 1) {
		c.overlaps.Add(1)
	}
	time.Sleep(10 * time.Millisecond)
	c.active.Store(0)
	return len(b), nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

type shortWriteConn struct {
	maxChunk   int
	failOnCall int
	failErr    error
	calls      int
	written    bytes.Buffer
}

func (c *shortWriteConn) Read(_ []byte) (int, error)         { return 0, io.EOF }
func (c *shortWriteConn) Close() error                       { return nil }
func (c *shortWriteConn) LocalAddr() net.Addr                { return dummyAddr("local") }
func (c *shortWriteConn) RemoteAddr() net.Addr               { return dummyAddr("remote") }
func (c *shortWriteConn) SetDeadline(_ time.Time) error      { return nil }
func (c *shortWriteConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *shortWriteConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *shortWriteConn) Write(p []byte) (int, error) {
	c.calls++
	n := len(p)
	if c.maxChunk > 0 && n > c.maxChunk {
		n = c.maxChunk
	}
	if n > 0 {
		_, _ = c.written.Write(p[:n])
	}
	if c.failOnCall > 0 && c.calls >= c.failOnCall {
		if c.failErr == nil {
			c.failErr = errors.New("synthetic write failure")
		}
		return n, c.failErr
	}
	return n, nil
}

func TestConnWriteSerializesConcurrentWriters(t *testing.T) {
	raw := &overlapDetectConn{}
	c := NewConn(raw)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := c.Write([]byte("payload")); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if overlaps := raw.overlaps.Load(); overlaps != 0 {
		t.Fatalf("concurrent writes overlapped %d times", overlaps)
	}
}

func TestConnWriteHandlesShortUnderlyingWrites(t *testing.T) {
	raw := &shortWriteConn{maxChunk: 2}
	c := NewConn(raw)

	n, err := c.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 6 {
		t.Fatalf("Write() n = %d, want 6", n)
	}
	if got := raw.written.String(); got != "abcdef" {
		t.Fatalf("wire bytes = %q, want %q", got, "abcdef")
	}
	if raw.calls < 3 {
		t.Fatalf("underlying Write() calls = %d, want at least 3 short writes", raw.calls)
	}
}

func TestConnWriteFlushesBufferedDataAcrossShortWrites(t *testing.T) {
	raw := &shortWriteConn{maxChunk: 2}
	c := NewConn(raw)
	if _, err := c.BufferWrite([]byte("abc")); err != nil {
		t.Fatalf("BufferWrite() error = %v", err)
	}

	n, err := c.Write([]byte("def"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Write() n = %d, want 3", n)
	}
	if got := raw.written.String(); got != "abcdef" {
		t.Fatalf("wire bytes = %q, want %q", got, "abcdef")
	}
	if c.wBuf == nil {
		t.Fatal("buffer should remain allocated after buffered Write()")
	}
	if c.wBuf.Len() != 0 {
		t.Fatalf("buffer len after Write() = %d, want 0", c.wBuf.Len())
	}
}

func TestConnFlushBufPreservesUnsentTailOnWriteError(t *testing.T) {
	wantErr := errors.New("synthetic write failure")
	raw := &shortWriteConn{maxChunk: 2, failOnCall: 2, failErr: wantErr}
	c := NewConn(raw)
	if _, err := c.BufferWrite([]byte("abcdef")); err != nil {
		t.Fatalf("BufferWrite() error = %v", err)
	}

	err := c.FlushBuf()
	if !errors.Is(err, wantErr) {
		t.Fatalf("FlushBuf() error = %v, want %v", err, wantErr)
	}
	if got := raw.written.String(); got != "abcd" {
		t.Fatalf("wire bytes after failed flush = %q, want %q", got, "abcd")
	}
	if c.wBuf == nil {
		t.Fatal("buffer should retain unsent tail after failed flush")
	}
	if c.wBuf.String() != "ef" {
		t.Fatalf("buffer after failed flush = %q, want %q", c.wBuf.String(), "ef")
	}

	raw.failOnCall = 0
	raw.failErr = nil
	raw.calls = 0
	if err := c.FlushBuf(); err != nil {
		t.Fatalf("FlushBuf() retry error = %v", err)
	}
	if got := raw.written.String(); got != "abcdef" {
		t.Fatalf("wire bytes after retry = %q, want %q", got, "abcdef")
	}
	if c.wBuf == nil {
		t.Fatal("buffer should remain allocated after retry flush")
	}
	if c.wBuf.Len() != 0 {
		t.Fatalf("buffer len after retry = %d, want 0", c.wBuf.Len())
	}
}

func TestResolveUDPBindTargetHonorsSpecificAndWildcardHosts(t *testing.T) {
	tests := []struct {
		addr        string
		wantNetwork string
		wantBind    string
		wantExact   bool
	}{
		{addr: ":0", wantExact: false},
		{addr: "127.0.0.1:0", wantNetwork: "udp4", wantBind: "127.0.0.1:0", wantExact: true},
		{addr: "0.0.0.0:0", wantNetwork: "udp4", wantBind: "0.0.0.0:0", wantExact: true},
		{addr: "[::1]:0", wantNetwork: "udp6", wantBind: "[::1]:0", wantExact: true},
		{addr: "[::]:0", wantNetwork: "udp6", wantBind: "[::]:0", wantExact: true},
	}
	for _, tt := range tests {
		_, gotNetwork, gotBind, gotExact, err := resolveUDPBindTarget(tt.addr)
		if err != nil {
			t.Fatalf("resolveUDPBindTarget(%q) error = %v", tt.addr, err)
		}
		if gotExact != tt.wantExact || gotNetwork != tt.wantNetwork || gotBind != tt.wantBind {
			t.Fatalf("resolveUDPBindTarget(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.addr, gotNetwork, gotBind, gotExact, tt.wantNetwork, tt.wantBind, tt.wantExact)
		}
	}
}

func TestConnGetInfoUnmarshalsPointerTargets(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	want := &file.Host{
		Host:   "example.com",
		Remark: "edge route",
	}

	writeErr := make(chan error, 1)
	go func() {
		payload, err := json.Marshal(want)
		if err != nil {
			writeErr <- err
			return
		}
		packet, err := GetLenBytes(payload)
		if err != nil {
			writeErr <- err
			return
		}
		_, err = server.Write(packet)
		writeErr <- err
	}()

	c := NewConn(client)
	var got *file.Host
	if err := c.getInfo(&got); err != nil {
		t.Fatalf("getInfo() error = %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("pipe write error = %v", err)
	}
	if got == nil {
		t.Fatal("getInfo() left target nil")
	}
	if got.Host != want.Host || got.Remark != want.Remark {
		t.Fatalf("getInfo() = %+v, want Host=%q Remark=%q", got, want.Host, want.Remark)
	}
}

func TestConnReadLenAllowsZeroLength(t *testing.T) {
	c := NewConn(addrOnlyConn{})
	n, err := c.ReadLen(0, make([]byte, 1))
	if err != nil {
		t.Fatalf("ReadLen(0) error = %v", err)
	}
	if n != 0 {
		t.Fatalf("ReadLen(0) = %d, want 0", n)
	}
}

func TestConnHelpersHandleNilUnderlyingConn(t *testing.T) {
	c := NewConn(nil)

	if err := c.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Close() error = %v, want %v", err, net.ErrClosed)
	}
	if got := c.LocalAddr(); got != nil {
		t.Fatalf("LocalAddr() = %v, want nil", got)
	}
	if got := c.RemoteAddr(); got != nil {
		t.Fatalf("RemoteAddr() = %v, want nil", got)
	}
	if err := c.SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if err := c.SetWriteDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetWriteDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if err := c.SetReadDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetReadDeadline() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := c.BufferWrite([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("BufferWrite() error = %v, want %v", err, net.ErrClosed)
	}
	if err := c.FlushBuf(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("FlushBuf() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Read() error = %v, want %v", err, net.ErrClosed)
	}
}

func TestConnDeadlineHelpersHandleNilReceiver(t *testing.T) {
	var c *Conn

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("deadline helper panicked on nil receiver: %v", recovered)
		}
	}()

	c.SetAlive()
	c.SetReadDeadlineBySecond(1)
}

func TestConnGetHostSetsMethodAndDefaultPort(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	req := "CONNECT example.com HTTP/1.1\r\nHost: example.com\r\n\r\n"
	writeErrCh := make(chan error, 1)
	go func() {
		_, err := io.WriteString(server, req)
		writeErrCh <- err
	}()

	method, address, rb, parsed, err := NewConn(client).GetHost()
	if err != nil {
		t.Fatalf("GetHost() error = %v", err)
	}
	if err := <-writeErrCh; err != nil {
		t.Fatalf("pipe write error = %v", err)
	}
	if method != "CONNECT" {
		t.Fatalf("GetHost() method = %q, want CONNECT", method)
	}
	if address != "example.com:443" {
		t.Fatalf("GetHost() address = %q, want %q", address, "example.com:443")
	}
	if string(rb) != req {
		t.Fatalf("GetHost() buffered request = %q, want %q", string(rb), req)
	}
	if parsed == nil || parsed.Method != method {
		t.Fatalf("GetHost() parsed request method = %v, want %q", parsed, method)
	}
}
