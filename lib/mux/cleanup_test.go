package mux

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestReceiveWindowQueueStopIsIdempotentAndUnblocksPop(t *testing.T) {
	q := newReceiveWindowQueue()
	errCh := make(chan error, 1)

	go func() {
		_, err := q.Pop()
		errCh <- err
	}()

	q.Stop()
	q.Stop()

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Pop() error = %v, want EOF after Stop", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pop() did not unblock after Stop")
	}
}

func TestSendWindowCloseWindowIsIdempotent(t *testing.T) {
	var w sendWindow
	w.New(nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- w.waitReceiveWindow()
	}()

	w.CloseWindow()
	w.CloseWindow()

	select {
	case err := <-errCh:
		if err == nil || err.Error() != "conn.writeWindow: window closed" {
			t.Fatalf("waitReceiveWindow() error = %v, want closed window error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitReceiveWindow() did not unblock after CloseWindow")
	}
}

func TestNilConnMethodsAreSafe(t *testing.T) {
	var c *Conn

	c.SetPriority()
	c.SetClosingFlag()

	if !c.IsClosed() {
		t.Fatal("nil Conn should report closed")
	}
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Conn Read() error = %v, want net.ErrClosed", err)
	}
	if _, err := c.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Conn Write() error = %v, want net.ErrClosed", err)
	}
	if err := c.CloseWrite(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Conn CloseWrite() error = %v, want net.ErrClosed", err)
	}
	if err := c.SetReadDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Conn SetReadDeadline() error = %v, want net.ErrClosed", err)
	}
	if err := c.SetWriteDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Conn SetWriteDeadline() error = %v, want net.ErrClosed", err)
	}
	if err := c.SetDeadline(time.Now()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Conn SetDeadline() error = %v, want net.ErrClosed", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("nil Conn Close() error = %v, want nil", err)
	}
	if c.LocalAddr() != nil {
		t.Fatal("nil Conn LocalAddr() = non-nil")
	}
	if c.RemoteAddr() != nil {
		t.Fatal("nil Conn RemoteAddr() = non-nil")
	}
}

func TestNilMuxMethodsAreSafe(t *testing.T) {
	var m *Mux

	if !m.IsClosed() {
		t.Fatal("nil Mux should report closed")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("nil Mux Close() error = %v, want nil", err)
	}
	if m.CloseChan() != nil {
		t.Fatal("nil Mux CloseChan() = non-nil")
	}
	if got := m.Config(); got != (MuxConfig{}) {
		t.Fatalf("nil Mux Config() = %+v, want zero config", got)
	}
	if got := m.CloseReason(); got != "" {
		t.Fatalf("nil Mux CloseReason() = %q, want empty", got)
	}
	if _, err := m.NewConn(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Mux NewConn() error = %v, want net.ErrClosed", err)
	}
	if _, err := m.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("nil Mux Accept() error = %v, want net.ErrClosed", err)
	}
	if m.Addr() != nil {
		t.Fatal("nil Mux Addr() = non-nil")
	}
}
