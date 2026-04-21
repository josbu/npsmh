package p2p

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
)

func TestSessionPacerDeterministicAndBounded(t *testing.T) {
	start := NormalizeP2PPunchStart(P2PPunchStart{
		SessionID: "session-1",
		Token:     "token-1",
		Wire:      P2PWireSpec{RouteID: "route-1"},
	})
	left := newSessionPacer(start)
	right := newSessionPacer(start)
	if got, want := left.sprayPacketGap(1, 2, 2*time.Millisecond), right.sprayPacketGap(1, 2, 2*time.Millisecond); got != want {
		t.Fatalf("sprayPacketGap mismatch: got %s want %s", got, want)
	}
	if gap := left.sprayPacketGap(0, 0, 2*time.Millisecond); gap < minPunchBindingGap {
		t.Fatalf("sprayPacketGap = %s, want >= %s", gap, minPunchBindingGap)
	}
	if got, want := left.periodicRetryDelay(2), right.periodicRetryDelay(2); got != want {
		t.Fatalf("periodicRetryDelay mismatch: got %s want %s", got, want)
	}
}

func TestSessionPacerSeparatesRoles(t *testing.T) {
	visitor := newSessionPacer(NormalizeP2PPunchStart(P2PPunchStart{
		SessionID: "session-1",
		Token:     "token-1",
		Wire:      P2PWireSpec{RouteID: "route-1"},
		Role:      common.WORK_P2P_VISITOR,
	}))
	provider := newSessionPacer(NormalizeP2PPunchStart(P2PPunchStart{
		SessionID: "session-1",
		Token:     "token-1",
		Wire:      P2PWireSpec{RouteID: "route-1"},
		Role:      common.WORK_P2P_PROVIDER,
	}))
	if visitor.seed == provider.seed {
		t.Fatal("pacer seed should differ across roles")
	}
}

func TestUDPPacketTokenValidation(t *testing.T) {
	packet := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeProbe)
	raw, err := EncodeUDPPacket(packet)
	if err != nil {
		t.Fatalf("EncodeUDPPacket() error = %v", err)
	}
	decoded, err := DecodeUDPPacket(raw, "token-1")
	if err != nil {
		t.Fatalf("DecodeUDPPacket() error = %v", err)
	}
	if decoded.SessionID != "session-1" || decoded.Token != "token-1" {
		t.Fatalf("decoded packet mismatch: %#v", decoded)
	}
	tamperedRaw := append([]byte(nil), raw...)
	tamperedRaw[2] ^= 0x01
	if _, err := DecodeUDPPacket(tamperedRaw, "token-1"); !errors.Is(err, ErrP2PTokenMismatch) {
		t.Fatalf("expected ErrP2PTokenMismatch, got %v", err)
	}
	if _, err := DecodeUDPPacket(raw, "wrong-token"); !errors.Is(err, ErrP2PTokenMismatch) {
		t.Fatalf("expected ErrP2PTokenMismatch for wrong token, got %v", err)
	}
	for _, plain := range [][]byte{
		[]byte("session-1"),
		[]byte("token-1"),
		[]byte(packetTypeProbe),
	} {
		if bytes.Contains(raw, plain) {
			t.Fatalf("wire packet should not expose plaintext %q", string(plain))
		}
	}
}

func TestUDPPacketEncodingAddsRandomLengthVariation(t *testing.T) {
	lengths := make(map[int]struct{}, 4)
	bodies := make(map[string]struct{}, 4)
	for i := 0; i < 8; i++ {
		packet := newUDPPacket("session-1", "token-1", common.WORK_P2P_VISITOR, packetTypeProbe)
		raw, err := EncodeUDPPacket(packet)
		if err != nil {
			t.Fatalf("EncodeUDPPacket() error = %v", err)
		}
		lengths[len(raw)] = struct{}{}
		bodies[string(raw)] = struct{}{}
		decoded, err := DecodeUDPPacket(raw, "token-1")
		if err != nil {
			t.Fatalf("DecodeUDPPacket() error = %v", err)
		}
		if decoded.SessionID != "session-1" || decoded.Type != packetTypeProbe {
			t.Fatalf("decoded packet mismatch: %#v", decoded)
		}
	}
	if len(bodies) < 2 {
		t.Fatal("encoded packets should differ because nonce/padding are randomized")
	}
	if len(lengths) < 2 {
		t.Fatalf("encoded packets should vary in length, got %v", lengths)
	}
}

func TestReadBridgeJSONHandlesAbort(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		bridgeConn := conn.NewConn(server)
		_ = WriteBridgeMessage(bridgeConn, common.P2P_PUNCH_ABORT, P2PPunchAbort{
			SessionID: "session-1",
			Role:      common.WORK_P2P_VISITOR,
			Reason:    "probe failed",
		})
	}()

	_, err := ReadBridgeJSON[P2PProbeSummary](conn.NewConn(client), common.P2P_PROBE_SUMMARY)
	<-writeDone
	if !errors.Is(err, ErrP2PSessionAbort) {
		t.Fatalf("expected ErrP2PSessionAbort, got %v", err)
	}
}

func TestReadBridgeJSONContextRespectsDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := ReadBridgeJSONContext[P2PProbeSummary](ctx, conn.NewConn(client), common.P2P_PROBE_SUMMARY)
	if !errors.Is(err, ErrNATNotSupportP2P) {
		t.Fatalf("expected ErrNATNotSupportP2P, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("ReadBridgeJSONContext() elapsed %s, want prompt local timeout", elapsed)
	}
}

func TestReadBridgeJSONContextReturnsPayloadBeforeCancel(t *testing.T) {
	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	defer func() { _ = client.Close() }()

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		time.Sleep(20 * time.Millisecond)
		bridgeConn := conn.NewConn(server)
		_ = WriteBridgeMessage(bridgeConn, common.P2P_PUNCH_GO, P2PPunchGo{
			SessionID: "session-1",
			Role:      common.WORK_P2P_VISITOR,
			DelayMs:   25,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	goMsg, err := ReadBridgeJSONContext[P2PPunchGo](ctx, conn.NewConn(client), common.P2P_PUNCH_GO)
	<-writeDone
	if err != nil {
		t.Fatalf("ReadBridgeJSONContext() error = %v", err)
	}
	if goMsg.DelayMs != 25 {
		t.Fatalf("DelayMs = %d, want 25", goMsg.DelayMs)
	}
}

func TestDefaultTimeouts(t *testing.T) {
	timeouts := DefaultTimeouts()
	if timeouts.HandshakeTimeoutMs != 20000 {
		t.Fatalf("HandshakeTimeoutMs = %d, want 20000", timeouts.HandshakeTimeoutMs)
	}
	if timeouts.TransportTimeoutMs != 10000 {
		t.Fatalf("TransportTimeoutMs = %d, want 10000", timeouts.TransportTimeoutMs)
	}
}

func TestMapP2PContextError(t *testing.T) {
	if !errors.Is(mapP2PContextError(context.DeadlineExceeded), ErrNATNotSupportP2P) {
		t.Fatal("deadline should map to ErrNATNotSupportP2P")
	}
	plainErr := errors.New("plain")
	if !errors.Is(mapP2PContextError(plainErr), plainErr) {
		t.Fatal("plain error should be returned directly")
	}
}

func TestFilteringEvidenceKnown(t *testing.T) {
	if filteringEvidenceKnown(NatObservation{}) {
		t.Fatal("empty observation should not report filtering evidence")
	}
	if !filteringEvidenceKnown(NatObservation{FilteringTested: true}) {
		t.Fatal("explicit filtering tested flag should report evidence")
	}
	if !filteringEvidenceKnown(NatObservation{FilteringBehavior: NATFilteringPortRestricted}) {
		t.Fatal("explicit filtering behavior should report evidence")
	}
	if !filteringEvidenceKnown(NatObservation{NATType: NATTypeRestrictedCone}) {
		t.Fatal("restricted cone nat type should imply filtering evidence")
	}
}

func TestIsIgnorableUDPIcmpError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "windows 10054", err: errors.New("wsarecvfrom: 10054"), want: true},
		{name: "connection refused", err: errors.New("read udp: connection refused"), want: true},
		{name: "connection reset by peer", err: errors.New("connection reset by peer"), want: true},
		{name: "other", err: errors.New("use of closed network connection"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIgnorableUDPIcmpError(tt.err); got != tt.want {
				t.Fatalf("isIgnorableUDPIcmpError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDecodeUDPPacketWithLookup(t *testing.T) {
	packet := newUDPPacket("session-lookup", "token-lookup", common.WORK_P2P_VISITOR, packetTypeProbe)
	raw, err := EncodeUDPPacket(packet)
	if err != nil {
		t.Fatalf("EncodeUDPPacket() error = %v", err)
	}
	wantRouteKey := WireRouteKey(packet.WireID)
	decoded, err := DecodeUDPPacketWithLookup(raw, func(routeKey string) (UDPPacketLookupResult, bool) {
		if routeKey != wantRouteKey {
			return UDPPacketLookupResult{}, false
		}
		return UDPPacketLookupResult{
			SessionID: "session-lookup",
			Token:     "token-lookup",
		}, true
	})
	if err != nil {
		t.Fatalf("DecodeUDPPacketWithLookup() error = %v", err)
	}
	if decoded.SessionID != "session-lookup" || decoded.Token != "token-lookup" {
		t.Fatalf("decoded packet mismatch: %#v", decoded)
	}
	if decoded.WireID != wantRouteKey {
		t.Fatalf("decoded wire route = %q, want %q", decoded.WireID, wantRouteKey)
	}
	if _, err := DecodeUDPPacketWithLookup(raw, func(routeKey string) (UDPPacketLookupResult, bool) {
		return UDPPacketLookupResult{}, false
	}); !errors.Is(err, ErrP2PTokenMismatch) {
		t.Fatalf("expected ErrP2PTokenMismatch for missing lookup, got %v", err)
	}
}
