package bridge

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/conn"
)

type probeContextBridgeConnStub struct {
	ctx  context.Context
	addr net.Addr
}

func (s *probeContextBridgeConnStub) Read([]byte) (int, error)         { return 0, io.EOF }
func (s *probeContextBridgeConnStub) Write(b []byte) (int, error)      { return len(b), nil }
func (s *probeContextBridgeConnStub) Close() error                     { return nil }
func (s *probeContextBridgeConnStub) LocalAddr() net.Addr              { return s.addr }
func (s *probeContextBridgeConnStub) RemoteAddr() net.Addr             { return s.addr }
func (s *probeContextBridgeConnStub) SetDeadline(time.Time) error      { return nil }
func (s *probeContextBridgeConnStub) SetReadDeadline(time.Time) error  { return nil }
func (s *probeContextBridgeConnStub) SetWriteDeadline(time.Time) error { return nil }
func (s *probeContextBridgeConnStub) Context() context.Context         { return s.ctx }

func TestBridgeConnResolveContextUsesConnectionContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := bridgeConnResolveContext(&conn.Conn{
		Conn: &probeContextBridgeConnStub{
			ctx:  ctx,
			addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12001},
		},
	})
	if got != ctx {
		t.Fatalf("bridgeConnResolveContext() = %v, want original connection context", got)
	}
}

func TestBridgeConnResolveContextFallsBackForMalformedConn(t *testing.T) {
	var typedNil *typedNilBridgeConnStub

	cases := []*conn.Conn{
		nil,
		{},
		{Conn: typedNil},
		{Conn: &probeContextBridgeConnStub{ctx: nil}},
	}
	for i, tc := range cases {
		if got := bridgeConnResolveContext(tc); got == nil {
			t.Fatalf("case %d bridgeConnResolveContext() = nil, want background context", i)
		}
	}
}
