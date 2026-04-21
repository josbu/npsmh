package conn

import (
	"errors"
	"net"
	"testing"
)

func TestSnappyConnHelpersHandleNilState(t *testing.T) {
	var nilConn *SnappyConn
	if _, err := nilConn.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := nilConn.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Write() error = %v, want %v", err, net.ErrClosed)
	}
	if err := nilConn.Close(); err != nil {
		t.Fatalf("nil Close() error = %v, want nil", err)
	}
	if got := nilConn.GetRawConn(); got != nil {
		t.Fatalf("nil GetRawConn() = %v, want nil", got)
	}

	malformed := &SnappyConn{}
	if _, err := malformed.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed Read() error = %v, want %v", err, net.ErrClosed)
	}
	if _, err := malformed.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("malformed Write() error = %v, want %v", err, net.ErrClosed)
	}
	if err := malformed.Close(); err != nil {
		t.Fatalf("malformed Close() error = %v, want nil", err)
	}
	if got := malformed.GetRawConn(); got != nil {
		t.Fatalf("malformed GetRawConn() = %v, want nil", got)
	}

	base := &countedCloseConn{}
	halfInit := &SnappyConn{c: base, raw: base}
	if err := halfInit.Close(); err != nil {
		t.Fatalf("halfInit Close() error = %v", err)
	}
	if calls := base.Calls(); calls != 1 {
		t.Fatalf("halfInit Close() calls = %d, want 1", calls)
	}
}
