package p2p

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestShouldAttemptPortMappingSkipsEasyObservedNAT(t *testing.T) {
	obs := NatObservation{
		PublicIP:            "1.1.1.1",
		ObservedBasePort:    5000,
		NATType:             NATTypeRestrictedCone,
		MappingBehavior:     NATMappingEndpointIndependent,
		FilteringBehavior:   NATFilteringOpen,
		FilteringTested:     true,
		ProbePortRestricted: false,
	}
	if shouldAttemptPortMapping(obs) {
		t.Fatalf("shouldAttemptPortMapping(%#v) = true, want false", obs)
	}
}

func TestShouldAttemptPortMappingKeepsHardObservedNATEligible(t *testing.T) {
	obs := NatObservation{
		PublicIP:            "1.1.1.1",
		ObservedBasePort:    5000,
		NATType:             NATTypeSymmetric,
		MappingBehavior:     NATMappingEndpointDependent,
		FilteringBehavior:   NATFilteringPortRestricted,
		FilteringTested:     true,
		ProbePortRestricted: true,
	}
	if !shouldAttemptPortMapping(obs) {
		t.Fatalf("shouldAttemptPortMapping(%#v) = false, want true", obs)
	}
}

func TestNewPortMappingCoordinatorDerivesPolicyAndPriority(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	coordinator := newPortMappingCoordinator(localConn, P2PProbeConfig{
		Policy: &P2PPolicy{
			PortMapping: P2PPortMappingPolicy{
				EnablePCPPortmap:    true,
				EnableNATPMPPortmap: true,
				LeaseSeconds:        7200,
			},
		},
	}, NatObservation{PublicIP: "1.1.1.1"})
	if coordinator == nil {
		t.Fatal("newPortMappingCoordinator() returned nil")
	}
	if !coordinator.enabled() {
		t.Fatal("coordinator should be enabled")
	}
	if coordinator.leaseSeconds != 7200 {
		t.Fatalf("leaseSeconds = %d, want 7200", coordinator.leaseSeconds)
	}
	if coordinator.internalIP != "127.0.0.1" {
		t.Fatalf("internalIP = %q, want 127.0.0.1", coordinator.internalIP)
	}
	if coordinator.internalPort <= 0 {
		t.Fatalf("internalPort = %d, want > 0", coordinator.internalPort)
	}
	if len(coordinator.attempts) != 2 {
		t.Fatalf("len(attempts) = %d, want 2", len(coordinator.attempts))
	}
	if coordinator.attempts[0].method != "pcp" || coordinator.attempts[1].method != "nat-pmp" {
		t.Fatalf("unexpected coordinator priority %#v", coordinator.attempts)
	}
}

func TestMaybeEnablePortMappingRespectsDisabledPolicyPath(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	obs := NatObservation{PublicIP: "1.1.1.1", NATType: NATTypeRestrictedCone, FilteringTested: true}
	probe := P2PProbeConfig{
		Policy: &P2PPolicy{
			PortMapping: P2PPortMappingPolicy{
				EnableUPNPPortmap:   false,
				EnablePCPPortmap:    false,
				EnableNATPMPPortmap: false,
			},
		},
	}

	typedConn, typedInfo := maybeEnablePortMapping(context.Background(), localConn, probe, obs)
	if typedConn != localConn {
		t.Fatal("disabled port mapping should return the original packet conn")
	}
	if typedInfo != nil {
		t.Fatalf("disabled port mapping should not create mappings, got %#v", typedInfo)
	}
}

func TestPortMappingCoordinatorAttemptHandlesNilContext(t *testing.T) {
	packetConn := &testPacketConn{
		localAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4010},
	}
	var sawDeadline bool
	coordinator := &portMappingCoordinator{
		packetConn:   packetConn,
		observation:  NatObservation{PublicIP: "1.1.1.1"},
		internalIP:   "127.0.0.1",
		internalPort: 4010,
		leaseSeconds: 3600,
		attempts: []portMappingAttempt{{
			method: "stub",
			try: func(ctx context.Context, internalIP string, internalPort, leaseSeconds int) (*PortMappingInfo, func() error, func() error) {
				if ctx == nil {
					t.Fatal("attempt context = nil, want normalized context")
				}
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("attempt context missing default timeout deadline")
				}
				if remaining := time.Until(deadline); remaining <= 0 || remaining > defaultPortMappingAttemptTimeout {
					t.Fatalf("attempt context deadline remaining = %s, want within (0, %s]", remaining, defaultPortMappingAttemptTimeout)
				}
				sawDeadline = true
				return nil, nil, nil
			},
		}},
	}

	var nilCtx context.Context
	gotConn, gotInfo := coordinator.attempt(nilCtx)
	if gotConn != packetConn {
		t.Fatal("attempt(nil) should return original packet conn")
	}
	if gotInfo != nil {
		t.Fatalf("attempt(nil) info = %#v, want nil", gotInfo)
	}
	if !sawDeadline {
		t.Fatal("attempt(nil) should invoke mapping try with normalized deadline context")
	}
}

func TestPortMappingCoordinatorAttemptSkipsCanceledContext(t *testing.T) {
	packetConn := &testPacketConn{
		localAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4011},
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	var tryCalls atomic.Int32
	coordinator := &portMappingCoordinator{
		packetConn:   packetConn,
		observation:  NatObservation{PublicIP: "1.1.1.1"},
		internalIP:   "127.0.0.1",
		internalPort: 4011,
		leaseSeconds: 3600,
		attempts: []portMappingAttempt{{
			method: "stub",
			try: func(ctx context.Context, internalIP string, internalPort, leaseSeconds int) (*PortMappingInfo, func() error, func() error) {
				tryCalls.Add(1)
				return nil, nil, nil
			},
		}},
	}

	gotConn, gotInfo := coordinator.attempt(canceledCtx)
	if gotConn != packetConn {
		t.Fatal("attempt(canceled) should return original packet conn")
	}
	if gotInfo != nil {
		t.Fatalf("attempt(canceled) info = %#v, want nil", gotInfo)
	}
	if got := tryCalls.Load(); got != 0 {
		t.Fatalf("attempt(canceled) try calls = %d, want 0", got)
	}
}

func TestPortMappingRenewIntervalTracksLeaseLifetime(t *testing.T) {
	if got := portMappingRenewInterval(20); got != 10*time.Second {
		t.Fatalf("portMappingRenewInterval(20) = %s, want 10s", got)
	}
	if got := portMappingRenewInterval(5); got != 2500*time.Millisecond {
		t.Fatalf("portMappingRenewInterval(5) = %s, want 2500ms", got)
	}
	if got := portMappingRenewInterval(1); got != 750*time.Millisecond {
		t.Fatalf("portMappingRenewInterval(1) = %s, want 750ms", got)
	}
}

func TestPortMappingRequestTimeoutHonorsContextState(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := portMappingRequestTimeout(canceledCtx, 750*time.Millisecond); got != time.Millisecond {
		t.Fatalf("portMappingRequestTimeout(canceled) = %s, want 1ms", got)
	}

	expiredCtx, expiredCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expiredCancel()
	if got := portMappingRequestTimeout(expiredCtx, 750*time.Millisecond); got != time.Millisecond {
		t.Fatalf("portMappingRequestTimeout(expired) = %s, want 1ms", got)
	}

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(40*time.Millisecond))
	defer deadlineCancel()
	got := portMappingRequestTimeout(deadlineCtx, time.Second)
	if got <= 0 || got > 40*time.Millisecond {
		t.Fatalf("portMappingRequestTimeout(short deadline) = %s, want within (0, 40ms]", got)
	}

	if got := portMappingRequestTimeout(context.Background(), 500*time.Millisecond); got != 500*time.Millisecond {
		t.Fatalf("portMappingRequestTimeout(background) = %s, want 500ms", got)
	}
}

func TestPortMappingRequestTimeoutHandlesNilContext(t *testing.T) {
	var nilCtx context.Context
	if got := portMappingRequestTimeout(nilCtx, 350*time.Millisecond); got != 350*time.Millisecond {
		t.Fatalf("portMappingRequestTimeout(nil) = %s, want 350ms", got)
	}
}

func TestNormalizePortMappingContextHandlesNil(t *testing.T) {
	var nilCtx context.Context
	if got := normalizePortMappingContext(nilCtx); got == nil {
		t.Fatal("normalizePortMappingContext(nil) = nil, want background context")
	}
}

func TestManagedPacketConnCloseDoesNotWaitForCleanup(t *testing.T) {
	var closeCalls atomic.Int32
	var cancelCalls atomic.Int32
	var cleanupCalls atomic.Int32
	cleanupStarted := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	cleanupDone := make(chan struct{}, 1)

	conn := &managedPacketConn{
		PacketConn: &testPacketConn{
			localAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4000},
			onClose: func() {
				closeCalls.Add(1)
			},
		},
		cancelRenew: func() {
			cancelCalls.Add(1)
		},
		cleanup: func() error {
			cleanupCalls.Add(1)
			select {
			case cleanupStarted <- struct{}{}:
			default:
			}
			<-cleanupRelease
			select {
			case cleanupDone <- struct{}{}:
			default:
			}
			return nil
		},
	}

	start := time.Now()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Close() took %v, want under %v", elapsed, 100*time.Millisecond)
	}

	select {
	case <-cleanupStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cleanup should start asynchronously")
	}

	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("packet close calls = %d, want 1", got)
	}
	if got := cancelCalls.Load(); got != 1 {
		t.Fatalf("cancelRenew calls = %d, want 1", got)
	}
	if got := cleanupCalls.Load(); got != 1 {
		t.Fatalf("cleanup calls = %d, want 1", got)
	}

	close(cleanupRelease)
	select {
	case <-cleanupDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cleanup should finish after release")
	}
}

func TestCloseSocketsDoesNotHoldSessionLockDuringClose(t *testing.T) {
	session := &runtimeSession{}
	socket := &testPacketConn{
		localAddr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4000},
		onClose: func() {
			_ = session.snapshotSockets()
		},
	}
	session.sockets = []net.PacketConn{socket}

	done := make(chan struct{})
	go func() {
		session.closeSockets(nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("closeSockets should not deadlock when Close re-enters runtimeSession state")
	}
	if sockets := session.snapshotSockets(); len(sockets) != 0 {
		t.Fatalf("closeSockets should clear session sockets, got %#v", sockets)
	}
}

func TestStopSocketReadLoopInterruptsWinnerReadLoop(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	session := &runtimeSession{
		localConn:    localConn,
		sockets:      []net.PacketConn{localConn},
		readLoopDone: make(map[net.PacketConn]chan struct{}),
		cm:           NewCandidateManager(""),
		replay:       NewReplayWindow(30 * time.Second),
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.startReadLoop(ctx, localConn)
	time.Sleep(20 * time.Millisecond)
	cancel()

	if ok := session.stopSocketReadLoop(localConn, 250*time.Millisecond); !ok {
		t.Fatal("stopSocketReadLoop() should unblock the read loop before handover")
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		session.mu.Lock()
		_, exists := session.readLoopDone[localConn]
		session.mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read loop bookkeeping should be cleared after shutdown")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartReadLoopStopsPromptlyOnContextCancel(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	session := &runtimeSession{
		localConn:    localConn,
		sockets:      []net.PacketConn{localConn},
		readLoopDone: make(map[net.PacketConn]chan struct{}),
		cm:           NewCandidateManager(""),
		replay:       NewReplayWindow(30 * time.Second),
	}
	ctx, cancel := context.WithCancel(context.Background())
	session.startReadLoop(ctx, localConn)
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	cancel()

	deadline := start.Add(350 * time.Millisecond)
	for {
		session.mu.Lock()
		_, exists := session.readLoopDone[localConn]
		session.mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startReadLoop() should stop promptly after context cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type testPacketConn struct {
	localAddr net.Addr
	onClose   func()
}

func (c *testPacketConn) ReadFrom(_ []byte) (int, net.Addr, error) {
	return 0, nil, errors.New("not implemented")
}
func (c *testPacketConn) WriteTo(b []byte, _ net.Addr) (int, error) { return len(b), nil }
func (c *testPacketConn) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}
func (c *testPacketConn) LocalAddr() net.Addr                { return c.localAddr }
func (c *testPacketConn) SetDeadline(_ time.Time) error      { return nil }
func (c *testPacketConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *testPacketConn) SetWriteDeadline(_ time.Time) error { return nil }

func TestBuildPCPRequestMappingPacket(t *testing.T) {
	clientIP := netip.MustParseAddr("192.168.1.10")
	prevExternalIP := netip.MustParseAddr("203.0.113.20")
	packet, nonce := buildPCPRequestMappingPacket(clientIP, 12345, 54321, 7200, prevExternalIP)
	if len(packet) != 60 {
		t.Fatalf("len(packet) = %d, want 60", len(packet))
	}
	if packet[0] != pcpVersion {
		t.Fatalf("packet version = %d, want %d", packet[0], pcpVersion)
	}
	if packet[1] != pcpOpMap {
		t.Fatalf("packet opcode = %d, want %d", packet[1], pcpOpMap)
	}
	if got := binary.BigEndian.Uint32(packet[4:8]); got != 7200 {
		t.Fatalf("lifetime = %d, want 7200", got)
	}
	var clientIP16 [16]byte
	copy(clientIP16[:], packet[8:24])
	if got := netip.AddrFrom16(clientIP16).Unmap(); got != clientIP {
		t.Fatalf("client ip = %s, want %s", got, clientIP)
	}
	if packet[36] != pcpUDPMapping {
		t.Fatalf("protocol = %d, want %d", packet[36], pcpUDPMapping)
	}
	if string(packet[24:36]) != string(nonce[:]) {
		t.Fatal("request nonce should be embedded in packet")
	}
	if got := binary.BigEndian.Uint16(packet[40:42]); got != 12345 {
		t.Fatalf("local port = %d, want 12345", got)
	}
	if got := binary.BigEndian.Uint16(packet[42:44]); got != 54321 {
		t.Fatalf("previous external port = %d, want 54321", got)
	}
	var externalIP16 [16]byte
	copy(externalIP16[:], packet[44:60])
	if got := netip.AddrFrom16(externalIP16).Unmap(); got != prevExternalIP {
		t.Fatalf("previous external ip = %s, want %s", got, prevExternalIP)
	}
}

func TestParsePCPMapResponse(t *testing.T) {
	response := make([]byte, 60)
	response[0] = pcpVersion
	response[1] = pcpOpMap | pcpOpReply
	response[3] = byte(pcpCodeOK)
	binary.BigEndian.PutUint32(response[4:8], 3600)
	binary.BigEndian.PutUint32(response[8:12], 42)
	var nonce [12]byte
	copy(nonce[:], "pcpnonce1234")
	copy(response[24:36], nonce[:])
	binary.BigEndian.PutUint16(response[42:44], 54321)
	externalIP := netip.MustParseAddr("198.51.100.25").As16()
	copy(response[44:60], externalIP[:])

	parsed, err := parsePCPMapResponse(response, nonce)
	if err != nil {
		t.Fatalf("parsePCPMapResponse() error = %v", err)
	}
	if parsed.ResultCode != pcpCodeOK {
		t.Fatalf("ResultCode = %s, want %s", parsed.ResultCode, pcpCodeOK)
	}
	if parsed.LifetimeSecs != 3600 {
		t.Fatalf("LifetimeSecs = %d, want 3600", parsed.LifetimeSecs)
	}
	if parsed.Epoch != 42 {
		t.Fatalf("Epoch = %d, want 42", parsed.Epoch)
	}
	if parsed.ExternalAddr != netip.MustParseAddrPort("198.51.100.25:54321") {
		t.Fatalf("ExternalAddr = %s, want 198.51.100.25:54321", parsed.ExternalAddr)
	}
}

func TestParsePCPMapResponseRejectsNonOKCodes(t *testing.T) {
	response := make([]byte, 60)
	response[0] = pcpVersion
	response[1] = pcpOpMap | pcpOpReply
	response[3] = byte(pcpCodeNotAuthorized)
	if _, err := parsePCPMapResponse(response, [12]byte{}); err == nil {
		t.Fatal("parsePCPMapResponse() should reject non-OK response codes")
	}
}

func TestParsePCPMapResponseRejectsNonceMismatch(t *testing.T) {
	response := make([]byte, 60)
	response[0] = pcpVersion
	response[1] = pcpOpMap | pcpOpReply
	response[3] = byte(pcpCodeOK)
	copy(response[24:36], "pcpnonce1234")
	binary.BigEndian.PutUint16(response[42:44], 54321)
	externalIP := netip.MustParseAddr("198.51.100.25").As16()
	copy(response[44:60], externalIP[:])

	if _, err := parsePCPMapResponse(response, [12]byte{'w', 'r', 'o', 'n', 'g'}); err == nil {
		t.Fatal("parsePCPMapResponse() should reject nonce mismatch")
	}
}
