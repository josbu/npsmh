package p2p

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

type typedNilPacketConnStub struct{}

func (s *typedNilPacketConnStub) ReadFrom([]byte) (int, net.Addr, error) {
	panic("unexpected ReadFrom call on typed nil packet conn")
}

func (s *typedNilPacketConnStub) WriteTo([]byte, net.Addr) (int, error) {
	panic("unexpected WriteTo call on typed nil packet conn")
}

func (s *typedNilPacketConnStub) Close() error {
	panic("unexpected Close call on typed nil packet conn")
}

func (s *typedNilPacketConnStub) LocalAddr() net.Addr {
	panic("unexpected LocalAddr call on typed nil packet conn")
}

func (s *typedNilPacketConnStub) SetDeadline(time.Time) error {
	panic("unexpected SetDeadline call on typed nil packet conn")
}

func (s *typedNilPacketConnStub) SetReadDeadline(time.Time) error {
	panic("unexpected SetReadDeadline call on typed nil packet conn")
}

func (s *typedNilPacketConnStub) SetWriteDeadline(time.Time) error {
	panic("unexpected SetWriteDeadline call on typed nil packet conn")
}

func TestInterruptPacketReadOnContextIgnoresTypedNilConn(t *testing.T) {
	var typedNil *typedNilPacketConnStub
	if restore := interruptPacketReadOnContext(context.Background(), typedNil); restore != nil {
		t.Fatal("interruptPacketReadOnContext() returned restore func for typed nil conn")
	}
}

func TestRuntimeSessionPacketConnHelpersIgnoreTypedNilConn(t *testing.T) {
	var typedNil *typedNilPacketConnStub
	session := &runtimeSession{
		start: P2PPunchStart{
			SessionID: "session-1",
			Token:     "token-1",
			Role:      common.WORK_P2P_PROVIDER,
		},
		sockets: []net.PacketConn{typedNil},
	}

	if err := session.writeUDPToConnWithEpoch(typedNil, packetTypePunch, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000}, 1); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("writeUDPToConnWithEpoch() error = %v, want %v", err, net.ErrClosed)
	}
	if got := session.socketForLocalAddr("127.0.0.1:9000"); got != nil {
		t.Fatalf("socketForLocalAddr() = %#v, want nil", got)
	}
	if got := session.snapshotSockets(); len(got) != 0 {
		t.Fatalf("snapshotSockets() len = %d, want 0", len(got))
	}
	if family := progressFamily("", "", typedNil); family != "" {
		t.Fatalf("progressFamily() = %q, want empty", family)
	}
	session.startReadLoop(context.Background(), typedNil)
	if len(session.readLoopDone) != 0 {
		t.Fatalf("startReadLoop() readLoopDone len = %d, want 0", len(session.readLoopDone))
	}
	session.readLoopOnConn(context.Background(), typedNil)
	session.signalConfirmed(typedNil, "local", "remote")
	session.closeSockets(typedNil)
	if session.ownsSocket(typedNil) {
		t.Fatal("ownsSocket() = true, want false for typed nil conn")
	}
	if !session.stopSocketReadLoop(typedNil, time.Millisecond) {
		t.Fatal("stopSocketReadLoop() = false, want true for typed nil conn")
	}
}
