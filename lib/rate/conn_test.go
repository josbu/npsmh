package rate

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
)

type scriptedConn struct {
	readBuf  []byte
	writeN   int
	writeErr error
}

func (c *scriptedConn) Read(b []byte) (int, error) {
	if len(c.readBuf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *scriptedConn) Write(b []byte) (int, error) {
	if c.writeN > len(b) {
		c.writeN = len(b)
	}
	return c.writeN, c.writeErr
}

func (c *scriptedConn) Close() error { return nil }

func TestRateConnReadChargesActualBytes(t *testing.T) {
	r := NewRate(1 << 20)
	conn := NewRateConn(&scriptedConn{readBuf: []byte("ok")}, r)

	buf := make([]byte, 4)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v, want nil", err)
	}
	if n != 2 {
		t.Fatalf("Read() n = %d, want 2", n)
	}
	if string(buf[:n]) != "ok" {
		t.Fatalf("Read() data = %q, want %q", string(buf[:n]), "ok")
	}
	if got := atomic.LoadInt64(&r.bytesAcc); got != 2 {
		t.Fatalf("bytesAcc after Read() = %d, want 2", got)
	}
}

func TestRateConnWriteRefundsShortWrite(t *testing.T) {
	r := NewRate(1 << 20)
	wantErr := errors.New("short write")
	conn := NewRateConn(&scriptedConn{writeN: 2, writeErr: wantErr}, r)

	n, err := conn.Write([]byte("hello"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write() error = %v, want %v", err, wantErr)
	}
	if n != 2 {
		t.Fatalf("Write() n = %d, want 2", n)
	}
	if got := atomic.LoadInt64(&r.bytesAcc); got != 2 {
		t.Fatalf("bytesAcc after Write() = %d, want 2", got)
	}
}
