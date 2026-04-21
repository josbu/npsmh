package p2p

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func TestNormalizeProbeEndpoints(t *testing.T) {
	endpoints := NormalizeProbeEndpoints(P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "probe-1", Address: "1.1.1.1:10206"},
			{ID: "probe-2", Provider: ProbeProviderNPS, Address: "1.1.1.2:10207"},
		},
	})
	if len(endpoints) != 2 {
		t.Fatalf("len(endpoints) = %d, want 2", len(endpoints))
	}
	if endpoints[0].Provider != ProbeProviderNPS || endpoints[0].Mode != ProbeModeUDP || endpoints[0].Network != ProbeNetworkUDP {
		t.Fatalf("unexpected normalized NPS endpoint %#v", endpoints[0])
	}
	if endpoints[1].Provider != ProbeProviderNPS || endpoints[1].Mode != ProbeModeUDP || endpoints[1].Network != ProbeNetworkUDP {
		t.Fatalf("unexpected normalized probe endpoint %#v", endpoints[1])
	}
}

func TestHasUsableProbeEndpointRequiresSupportedTransport(t *testing.T) {
	if !HasUsableProbeEndpoint(P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "probe-1", Address: "1.1.1.1:10206"},
		},
	}) {
		t.Fatal("supported NPS UDP probe endpoint should be usable")
	}
	if HasUsableProbeEndpoint(P2PProbeConfig{
		Endpoints: []P2PProbeEndpoint{
			{ID: "probe-1", Provider: ProbeProviderNPS, Mode: "unsupported_mode", Network: ProbeNetworkUDP, Address: "1.1.1.1:10206"},
			{ID: "probe-2", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: "tcp", Address: "1.1.1.2:10207"},
		},
	}) {
		t.Fatal("unsupported probe endpoint transport should not be treated as usable")
	}
	if HasUsableProbeEndpoint(P2PProbeConfig{
		Endpoints: []P2PProbeEndpoint{
			{ID: "probe-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: "tcp", Address: "1.1.1.1:10206"},
		},
	}) {
		t.Fatal("non-udp NPS probe endpoints should not be treated as usable")
	}
}

func TestResolveUDPAddrContextPrefersLocalSocketFamily(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	lookup, err := net.DefaultResolver.LookupIPAddr(context.Background(), "localhost")
	if err != nil {
		t.Fatalf("LookupIPAddr(localhost) error = %v", err)
	}
	hasV4 := false
	for _, addr := range lookup {
		if ip := common.NormalizeIP(addr.IP); ip != nil && ip.To4() != nil {
			hasV4 = true
			break
		}
	}
	if !hasV4 {
		t.Skip("localhost does not resolve to IPv4 on this host")
	}

	resolved, err := resolveUDPAddrContext(context.Background(), localConn.LocalAddr(), "localhost:3478", 250*time.Millisecond)
	if err != nil {
		t.Fatalf("resolveUDPAddrContext() error = %v", err)
	}
	if resolved.IP == nil || resolved.IP.To4() == nil {
		t.Fatalf("resolveUDPAddrContext() = %v, want IPv4 address", resolved)
	}
}

func TestResolveUDPAddrContextRejectsMismatchedLiteralFamily(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	if _, err := resolveUDPAddrContext(context.Background(), localConn.LocalAddr(), "[::1]:3478", 250*time.Millisecond); err == nil {
		t.Fatal("expected mismatched literal family to be rejected")
	}
}

func TestRunProbeRejectsUnsupportedProbeConfig(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()
	obs, err := runProbe(context.Background(), localConn, P2PPunchStart{
		SessionID: "session-1",
		Token:     "token-1",
		Role:      common.WORK_P2P_VISITOR,
		Probe: P2PProbeConfig{
			Version:  2,
			Provider: ProbeProviderNPS,
			Mode:     "unsupported_mode",
			Network:  "tcp",
			Endpoints: []P2PProbeEndpoint{
				{ID: "probe-1", Provider: ProbeProviderNPS, Mode: "unsupported_mode", Network: "tcp", Address: "127.0.0.1:10206"},
			},
		},
		Timeouts: DefaultTimeouts(),
	})
	if err == nil {
		t.Fatalf("runProbe() expected error for unsupported config, got %#v", obs)
	}
}

func TestCompatibleNPSProbeEndpointsFiltersBySocketFamily(t *testing.T) {
	v4Conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(v4) error = %v", err)
	}
	defer func() { _ = v4Conn.Close() }()

	endpoints := compatibleNPSProbeEndpoints(v4Conn.LocalAddr(), []P2PProbeEndpoint{
		{ID: "v4", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10206"},
		{ID: "v6", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "[::1]:10206"},
		{ID: "host", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "probe.example.test:10206"},
	})
	if len(endpoints) != 2 {
		t.Fatalf("len(endpoints) = %d, want 2 (%#v)", len(endpoints), endpoints)
	}
	if endpoints[0].ID != "v4" || endpoints[1].ID != "host" {
		t.Fatalf("unexpected compatible endpoints %#v", endpoints)
	}
	if network := probeResolveNetwork(v4Conn.LocalAddr()); network != "udp4" {
		t.Fatalf("probeResolveNetwork(v4) = %q, want udp4", network)
	}
}

func TestChooseLocalProbeAddrPrefersFamilyWithMoreNPSEndpoints(t *testing.T) {
	if _, err := common.GetLocalUdp4Addr(); err != nil {
		t.Skip("local IPv4 UDP addr unavailable")
	}
	addr, err := ChooseLocalProbeAddr(P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v6-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "[::1]:10206"},
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
			{ID: "v4-2", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10208"},
		},
	})
	if err != nil {
		t.Fatalf("ChooseLocalProbeAddr() error = %v", err)
	}
	if family := detectAddressFamily(addr); family != udpFamilyV4 {
		t.Fatalf("ChooseLocalProbeAddr() = %q, want IPv4 family", addr)
	}
}

func TestChooseLocalProbeAddrsContextRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ChooseLocalProbeAddrsContext(ctx, "", P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
		},
	})
	if err == nil {
		t.Fatal("ChooseLocalProbeAddrsContext() expected context cancellation error")
	}
	if err != context.Canceled {
		t.Fatalf("ChooseLocalProbeAddrsContext() error = %v, want context.Canceled", err)
	}
}

func TestChooseLocalProbeAddrsIncludesDualStackWhenBothFamiliesUsable(t *testing.T) {
	if _, err := common.GetLocalUdp4Addr(); err != nil {
		t.Skip("local IPv4 UDP addr unavailable")
	}
	if _, err := common.GetLocalUdp6Addr(); err != nil {
		t.Skip("local IPv6 UDP addr unavailable")
	}
	addrs, err := ChooseLocalProbeAddrs("", P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
			{ID: "v6-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "[::1]:10208"},
		},
	})
	if err != nil {
		t.Fatalf("ChooseLocalProbeAddrs() error = %v", err)
	}
	if len(addrs) < 2 {
		t.Fatalf("dual-stack probe selection should keep both families, got %#v", addrs)
	}
	if detectAddressFamily(addrs[0]) == detectAddressFamily(addrs[1]) {
		t.Fatalf("expected both IPv4 and IPv6 probe addrs, got %#v", addrs)
	}
}

func TestChooseLocalProbeAddrsPrefersExplicitBindIP(t *testing.T) {
	addrs, err := ChooseLocalProbeAddrs("127.0.0.1", P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
		},
	})
	if err != nil {
		t.Fatalf("ChooseLocalProbeAddrs() error = %v", err)
	}
	if len(addrs) == 0 || addrs[0] != "127.0.0.1:0" {
		t.Fatalf("ChooseLocalProbeAddrs() = %#v, want explicit bind addr first", addrs)
	}
	if len(addrs) != 1 {
		t.Fatalf("ChooseLocalProbeAddrs() should keep a single candidate for the same family, got %#v", addrs)
	}
}

func TestChooseLocalProbeAddrsRespectsProbeTimeoutPolicy(t *testing.T) {
	probe := P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
		},
		Policy: &P2PPolicy{ProbeTimeoutMs: 800},
	}

	addrs, err := ChooseLocalProbeAddrs("127.0.0.1", probe)
	if err != nil {
		t.Fatalf("ChooseLocalProbeAddrs() error = %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "127.0.0.1:0" {
		t.Fatalf("ChooseLocalProbeAddrs() = %#v, want explicit bind addr with typed timeout policy", addrs)
	}
}

func TestChooseLocalProbeAddrsDoesNotForceUnsupportedPreferredFamily(t *testing.T) {
	addrs, err := ChooseLocalProbeAddrs("2001:db8::10", P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
		},
	})
	if err != nil {
		t.Fatalf("ChooseLocalProbeAddrs() error = %v", err)
	}
	if len(addrs) == 0 {
		t.Fatal("ChooseLocalProbeAddrs() should keep a usable fallback family")
	}
	if detectAddressFamily(addrs[0]) != udpFamilyV4 {
		t.Fatalf("ChooseLocalProbeAddrs() should not force unsupported preferred family, got %#v", addrs)
	}
}

func TestChooseLocalProbeAddrsUsesPreferredOnlyWithinMatchingFamily(t *testing.T) {
	if _, err := common.GetLocalUdp6Addr(); err != nil {
		t.Skip("local IPv6 UDP addr unavailable")
	}
	addrs, err := ChooseLocalProbeAddrs("127.0.0.1", P2PProbeConfig{
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
			{ID: "v6-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "[::1]:10208"},
		},
	})
	if err != nil {
		t.Fatalf("ChooseLocalProbeAddrs() error = %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("ChooseLocalProbeAddrs() should keep one candidate per family, got %#v", addrs)
	}
	var v4Count, v6Count int
	for _, addr := range addrs {
		switch family := detectAddressFamily(addr); family {
		case udpFamilyV4:
			v4Count++
			if addr != "127.0.0.1:0" {
				t.Fatalf("expected explicit IPv4 bind addr, got %#v", addrs)
			}
		case udpFamilyV6:
			v6Count++
		case udpFamilyAny:
			t.Fatalf("expected concrete UDP family for %q", addr)
		default:
			t.Fatalf("unexpected UDP family %q for %q", family.String(), addr)
		}
	}
	if v4Count != 1 || v6Count != 1 {
		t.Fatalf("ChooseLocalProbeAddrs() should keep exactly one candidate per family, got %#v", addrs)
	}
}

func TestPeerInfoForFamilyReturnsSplitFamilyView(t *testing.T) {
	peer := BuildPeerInfo(common.WORK_P2P_VISITOR, common.CONN_KCP, "", []P2PFamilyInfo{
		{
			Family:     "udp4",
			Nat:        NatObservation{PublicIP: "1.1.1.1", ObservedBasePort: 5000, Samples: []ProbeSample{{ObservedAddr: "1.1.1.1:5000"}}},
			LocalAddrs: []string{"192.168.0.10:4000"},
		},
		{
			Family:     "udp6",
			Nat:        NatObservation{PublicIP: "2001:db8::1", ObservedBasePort: 6000, Samples: []ProbeSample{{ObservedAddr: "[2001:db8::1]:6000"}}},
			LocalAddrs: []string{"[fd00::10]:4000"},
		},
	})
	v6, ok := PeerInfoForFamily(peer, udpFamilyV6)
	if !ok {
		t.Fatal("PeerInfoForFamily(udp6) should succeed")
	}
	if v6.Nat.PublicIP != "2001:db8::1" || len(v6.Families) != 1 || v6.Families[0].Family != "udp6" {
		t.Fatalf("unexpected udp6 peer info %#v", v6)
	}
}

func TestBuildPeerInfoUsesConservativePrimaryFamilyAndMergedLocalAddrs(t *testing.T) {
	peer := BuildPeerInfo(common.WORK_P2P_VISITOR, common.CONN_KCP, "", []P2PFamilyInfo{
		{
			Family: "udp4",
			Nat: NatObservation{
				PublicIP:            "1.1.1.1",
				ObservedBasePort:    5000,
				NATType:             NATTypeRestrictedCone,
				ClassificationLevel: ClassificationConfidenceMed,
			},
			LocalAddrs: []string{"192.168.0.10:4000"},
		},
		{
			Family: "udp6",
			Nat: NatObservation{
				PublicIP:             "2001:db8::1",
				ObservedBasePort:     6000,
				NATType:              NATTypeSymmetric,
				MappingConfidenceLow: true,
				ClassificationLevel:  ClassificationConfidenceLow,
			},
			LocalAddrs: []string{"[fd00::10]:4000"},
		},
	})
	if peer.Nat.NATType != NATTypeSymmetric {
		t.Fatalf("top-level peer nat should keep the conservative family view, got %#v", peer.Nat)
	}
	if len(peer.LocalAddrs) != 2 {
		t.Fatalf("top-level peer local addrs should merge all families, got %#v", peer.LocalAddrs)
	}
}

func TestProbeEndpointAddrs(t *testing.T) {
	addrs := probeEndpointAddrs(P2PProbeConfig{
		Version:  1,
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "probe-1", Network: ProbeNetworkUDP, Address: "1.1.1.1:10206"},
			{ID: "probe-2", Network: ProbeNetworkUDP, Address: "bad"},
			{ID: "probe-3", Network: ProbeNetworkUDP, Address: "1.1.1.1:10208"},
		},
	})
	if len(addrs) != 2 {
		t.Fatalf("len(addrs) = %d, want 2 (%#v)", len(addrs), addrs)
	}
	if addrs[0] != "1.1.1.1:10206" || addrs[1] != "1.1.1.1:10208" {
		t.Fatalf("unexpected addrs %#v", addrs)
	}
}

func TestProbeEndpointInventoryFamilyScores(t *testing.T) {
	inventory := newProbeEndpointInventory(P2PProbeConfig{
		Version:  1,
		Provider: ProbeProviderNPS,
		Mode:     ProbeModeUDP,
		Network:  ProbeNetworkUDP,
		Endpoints: []P2PProbeEndpoint{
			{ID: "v4-1", Address: "127.0.0.1:10206"},
			{ID: "v4-2", Address: "127.0.0.1:10207"},
			{ID: "v6-1", Address: "[::1]:10208"},
		},
	})
	scores := inventory.familyScores(context.Background(), 0)
	if scores[udpFamilyV4] != 2 {
		t.Fatalf("IPv4 score = %d, want 2", scores[udpFamilyV4])
	}
	if scores[udpFamilyV6] != 1 {
		t.Fatalf("IPv6 score = %d, want 1", scores[udpFamilyV6])
	}
}

func TestProbeEndpointInventoryResolveNPSDeduplicatesResolvedTargets(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	inventory := probeEndpointInventory{endpoints: []P2PProbeEndpoint{
		{ID: "direct", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10206"},
		{ID: "host", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "localhost:10206"},
	}}
	resolved, keys, err := inventory.resolveNPS(context.Background(), localConn.LocalAddr(), 250*time.Millisecond)
	if err != nil {
		t.Fatalf("resolveNPS() error = %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("len(resolved) = %d, want 1 (%#v)", len(resolved), resolved)
	}
	if len(keys) != 1 {
		t.Fatalf("len(keys) = %d, want 1 (%#v)", len(keys), keys)
	}
	if resolved[0].key == "" || resolved[0].addr == nil {
		t.Fatalf("unexpected resolved endpoint %#v", resolved[0])
	}
}

func TestBuildLocalAddrsKeepsOnlyConcreteBoundAddr(t *testing.T) {
	addrs := buildLocalAddrs(&net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 45678})
	if len(addrs) != 1 {
		t.Fatalf("len(buildLocalAddrs()) = %d, want 1 (%#v)", len(addrs), addrs)
	}
	if addrs[0] != "192.0.2.10:45678" {
		t.Fatalf("buildLocalAddrs() = %#v, want only the bound addr", addrs)
	}
}

func TestOpenProbeRuntimeFamiliesUsesAttemptContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	cancel()

	_, err := openProbeRuntimeFamilies(ctx, "", P2PPunchStart{
		SessionID: "session-1",
		Token:     "token-1",
		Role:      common.WORK_P2P_VISITOR,
		Probe: P2PProbeConfig{
			Provider: ProbeProviderNPS,
			Mode:     ProbeModeUDP,
			Network:  ProbeNetworkUDP,
			Endpoints: []P2PProbeEndpoint{
				{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:10207"},
			},
		},
		Timeouts: DefaultTimeouts(),
	})
	if err == nil {
		t.Fatal("openProbeRuntimeFamilies() expected attempt context error")
	}
	if err != context.Canceled {
		t.Fatalf("openProbeRuntimeFamilies() error = %v, want context.Canceled", err)
	}
}

func TestRunNPSProbeUsesAttemptContextForReadLoop(t *testing.T) {
	localConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket(local) error = %v", err)
	}
	defer func() { _ = localConn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err = runNPSProbe(ctx, localConn, P2PPunchStart{
		SessionID: "session-ctx-read",
		Token:     "token-ctx-read",
		Role:      common.WORK_P2P_VISITOR,
		Probe: P2PProbeConfig{
			Provider: ProbeProviderNPS,
			Mode:     ProbeModeUDP,
			Network:  ProbeNetworkUDP,
			Endpoints: []P2PProbeEndpoint{
				{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:9"},
			},
		},
		Timeouts: P2PTimeouts{ProbeTimeoutMs: 2000},
	}, []P2PProbeEndpoint{
		{ID: "v4-1", Provider: ProbeProviderNPS, Mode: ProbeModeUDP, Network: ProbeNetworkUDP, Address: "127.0.0.1:9"},
	})
	if err == nil {
		t.Fatal("runNPSProbe() expected attempt context error")
	}
	if err != ErrNATNotSupportP2P {
		t.Fatalf("runNPSProbe() error = %v, want %v", err, ErrNATNotSupportP2P)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("runNPSProbe() elapsed %s, want prompt context cancellation", elapsed)
	}
}
