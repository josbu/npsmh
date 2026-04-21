package conn

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
)

type flowTestReadWriteCloser struct {
	readBuf  *bytes.Buffer
	writeBuf bytes.Buffer
}

func (c *flowTestReadWriteCloser) Read(p []byte) (int, error) {
	if c == nil || c.readBuf == nil {
		return 0, io.EOF
	}
	return c.readBuf.Read(p)
}

func (c *flowTestReadWriteCloser) Write(p []byte) (int, error) {
	if c == nil {
		return 0, io.ErrClosedPipe
	}
	return c.writeBuf.Write(p)
}

func (c *flowTestReadWriteCloser) Close() error {
	return nil
}

func TestFlowConnHandlesNilFlows(t *testing.T) {
	raw := &flowTestReadWriteCloser{
		readBuf: bytes.NewBufferString("abc"),
	}
	flowConn := NewFlowConn(raw, nil, nil)

	buf := make([]byte, 3)
	n, err := flowConn.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 || string(buf[:n]) != "abc" {
		t.Fatalf("Read() = %d %q, want 3 %q", n, string(buf[:n]), "abc")
	}

	n, err = flowConn.Write([]byte("xyz"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("Write() n = %d, want 3", n)
	}
	if got := raw.writeBuf.String(); got != "xyz" {
		t.Fatalf("wire bytes = %q, want %q", got, "xyz")
	}
}

func TestFlowConnHelpersHandleNilState(t *testing.T) {
	var nilLen *LenConn
	if _, err := nilLen.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil LenConn.Write() error = %v, want %v", err, net.ErrClosed)
	}

	malformedLen := &LenConn{}
	if _, err := malformedLen.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed LenConn.Write() error = %v, want %v", err, net.ErrClosed)
	}

	var nilRW *RWConn
	if _, err := nilRW.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil RWConn.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := nilRW.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil RWConn.Write() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilRW.Close(); err != nil {
		t.Fatalf("nil RWConn.Close() error = %v, want nil", err)
	}
	if got := nilRW.LocalAddr(); got != nil {
		t.Fatalf("nil RWConn.LocalAddr() = %v, want nil", got)
	}
	if got := nilRW.RemoteAddr(); got != nil {
		t.Fatalf("nil RWConn.RemoteAddr() = %v, want nil", got)
	}

	malformedRW := &RWConn{}
	if _, err := malformedRW.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed RWConn.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := malformedRW.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed RWConn.Write() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformedRW.Close(); err != nil {
		t.Fatalf("malformed RWConn.Close() error = %v, want nil", err)
	}

	var nilFlow *FlowConn
	if _, err := nilFlow.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil FlowConn.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := nilFlow.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil FlowConn.Write() error = %v, want %v", err, net.ErrClosed)
	}

	malformedFlow := &FlowConn{}
	if _, err := malformedFlow.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed FlowConn.Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := malformedFlow.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed FlowConn.Write() error = %v, want %v", err, net.ErrClosed)
	}
}
