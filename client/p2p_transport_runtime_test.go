package client

import (
	"net"
	"testing"

	"github.com/djylb/nps/lib/mux"
	"github.com/quic-go/quic-go"
)

func newLiveP2PMuxPair(t *testing.T) (*mux.Mux, *mux.Mux) {
	t.Helper()

	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = client.Close() })

	left := mux.NewMux(client, "tcp", 30, true)
	right := mux.NewMux(server, "tcp", 30, false)
	t.Cleanup(func() { _ = left.Close() })
	t.Cleanup(func() { _ = right.Close() })
	return left, right
}

func TestCloseP2PTransportIgnoresMalformedMuxAndQUIC(t *testing.T) {
	closeP2PTransport(p2pTransportState{
		muxSession: &mux.Mux{},
		quicConn:   &quic.Conn{},
	}, "test malformed transport")
}

func TestP2PPeerStateHealthyRejectsMalformedTransport(t *testing.T) {
	t.Run("MalformedMux", func(t *testing.T) {
		peer := &p2pPeerState{
			statusOK: true,
			transport: p2pTransportState{
				muxSession: &mux.Mux{},
			},
		}
		if peer.healthy() {
			t.Fatal("healthy() should reject malformed zero-value mux transport")
		}
	})

	t.Run("MalformedQUIC", func(t *testing.T) {
		peer := &p2pPeerState{
			statusOK: true,
			transport: p2pTransportState{
				quicConn: &quic.Conn{},
			},
		}
		if peer.healthy() {
			t.Fatal("healthy() should reject malformed zero-value quic transport")
		}
	})

	t.Run("LiveMux", func(t *testing.T) {
		liveMux, _ := newLiveP2PMuxPair(t)
		peer := &p2pPeerState{
			statusOK: true,
			transport: p2pTransportState{
				muxSession: liveMux,
			},
		}
		if !peer.healthy() {
			t.Fatal("healthy() should accept a live mux transport")
		}
	})
}
