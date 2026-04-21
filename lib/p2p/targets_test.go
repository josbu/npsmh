package p2p

import (
	"reflect"
	"testing"
)

func TestBuildTargetSprayPorts(t *testing.T) {
	ports := BuildTargetSprayPorts(5000, 2, 6)
	wantHead := []int{5000, 5002, 4998}
	for i, want := range wantHead {
		if ports[i] != want {
			t.Fatalf("ports[%d] = %d, want %d (%#v)", i, ports[i], want, ports)
		}
	}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			t.Fatalf("invalid port %d in %#v", port, ports)
		}
	}
}

func TestBuildTargetSprayPortsNormalizesInterval(t *testing.T) {
	ports := BuildTargetSprayPorts(10, 0, 4)
	if len(ports) != 4 {
		t.Fatalf("len(ports) = %d, want 4", len(ports))
	}
	if ports[0] != 10 || ports[1] != 11 || ports[2] != 9 {
		t.Fatalf("unexpected interval-normalized result %#v", ports)
	}
}

func TestBuildTargetSprayPortsMulti(t *testing.T) {
	ports := BuildTargetSprayPortsMulti(5000, []int{4, 2, 1}, 8)
	if len(ports) != 8 {
		t.Fatalf("len(ports) = %d, want 8 (%#v)", len(ports), ports)
	}
	if ports[0] != 5000 {
		t.Fatalf("ports[0] = %d, want 5000", ports[0])
	}
	expected := map[int]bool{5004: false, 4996: false, 5002: false, 4998: false}
	for _, port := range ports {
		if _, ok := expected[port]; ok {
			expected[port] = true
		}
	}
	for port, seen := range expected {
		if !seen {
			t.Fatalf("expected multi-interval spray to include %d in %#v", port, ports)
		}
	}
}

func TestBuildPunchTargetsFiltersAndDedupes(t *testing.T) {
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
				{ObservedAddr: "1.1.1.1:5002"},
			},
		},
		LocalAddrs: []string{
			"192.168.0.2:5000",
			"[2001:db8::1]:5000",
			"192.168.0.2:5000",
		},
	}
	targets := BuildPunchTargets(peer, PunchPlan{
		PredictionInterval: 2,
		SpraySpan:          6,
	})
	if len(targets) == 0 {
		t.Fatal("expected punch targets")
	}
	for _, target := range targets {
		if target == "[2001:db8::1]:5000" {
			t.Fatalf("ipv6 local addr should be filtered for ipv4 peer: %#v", targets)
		}
	}
	if targets[0] != "1.1.1.1:5000" {
		t.Fatalf("first target = %q, want first observed addr", targets[0])
	}
}

func TestBuildDirectPunchTargetsFiltersAndDedupes(t *testing.T) {
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP: "1.1.1.1",
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{
			"192.168.0.2:5000",
			"[2001:db8::1]:5000",
			"192.168.0.2:5000",
		},
	}
	targets := BuildDirectPunchTargets(peer)
	if len(targets) != 2 {
		t.Fatalf("len(targets) = %d, want 2 (%#v)", len(targets), targets)
	}
	if targets[0] != "1.1.1.1:5000" || targets[1] != "192.168.0.2:5000" {
		t.Fatalf("unexpected direct targets %#v", targets)
	}
}

func TestBuildDirectPunchTargetsPrefersPortMapping(t *testing.T) {
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PortMapping: &PortMappingInfo{ExternalAddr: "2.2.2.2:6000"},
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{"192.168.0.2:5000"},
	}
	targets := BuildDirectPunchTargets(peer)
	if len(targets) == 0 || targets[0] != "2.2.2.2:6000" {
		t.Fatalf("port mapping target should be first, got %#v", targets)
	}
}

func TestBuildPreferredDirectPunchTargetsPrefersMatchingPeerLocalAddr(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{
			"192.168.0.10:4000",
		},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PortMapping: &PortMappingInfo{ExternalAddr: "2.2.2.2:6000"},
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{
			"192.168.0.20:5000",
			"[2001:db8::20]:5000",
		},
	}
	targets := BuildPreferredDirectPunchTargets(self, peer)
	if len(targets) == 0 {
		t.Fatal("expected preferred direct targets")
	}
	if targets[0] != "192.168.0.20:5000" {
		t.Fatalf("first preferred direct target = %q, want peer ipv4 local addr (%#v)", targets[0], targets)
	}
	for _, target := range targets {
		if target == "[2001:db8::20]:5000" {
			t.Fatalf("ipv6 peer local addr should be skipped when self only supports ipv4: %#v", targets)
		}
	}
}

func TestBuildPreferredDirectPunchTargetsDoesNotPrioritizeForeignPrivateSubnet(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{
			"10.0.0.10:4000",
		},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PortMapping: &PortMappingInfo{ExternalAddr: "2.2.2.2:6000"},
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{
			"192.168.0.20:5000",
		},
	}
	targets := BuildPreferredDirectPunchTargets(self, peer)
	if len(targets) < 2 {
		t.Fatalf("expected preferred direct targets, got %#v", targets)
	}
	if targets[0] != "2.2.2.2:6000" {
		t.Fatalf("foreign private subnet should not outrank public candidates, got %#v", targets)
	}
}

func TestBuildPreferredPunchTargetsKeepsDirectCandidatesBeforePrediction(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{"192.168.0.10:4000"},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{"192.168.0.20:5000"},
	}
	targets := BuildPreferredPunchTargets(self, peer, PunchPlan{
		PredictionIntervals: []int{2},
		SpraySpan:           6,
	})
	if len(targets) < 3 {
		t.Fatalf("expected enough targets, got %#v", targets)
	}
	if targets[0] != "192.168.0.20:5000" {
		t.Fatalf("first target = %q, want direct local addr first (%#v)", targets[0], targets)
	}
	if targets[1] != "1.1.1.1:5000" {
		t.Fatalf("second target = %q, want observed addr second (%#v)", targets[1], targets)
	}
}

func TestBuildPreferredPunchTargetsDefersForeignPrivateLocalFallback(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{"10.0.0.10:4000"},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{"192.168.0.20:5000"},
	}
	targets := BuildPreferredPunchTargets(self, peer, PunchPlan{
		PredictionIntervals: []int{2},
		SpraySpan:           4,
	})
	if len(targets) < 3 {
		t.Fatalf("expected enough targets, got %#v", targets)
	}
	if targets[0] != "1.1.1.1:5000" {
		t.Fatalf("first target = %q, want observed public target first (%#v)", targets[0], targets)
	}
	if targets[len(targets)-1] != "192.168.0.20:5000" {
		t.Fatalf("foreign private local addr should be deferred to the end, got %#v", targets)
	}
}

func TestBuildPreferredPunchTargetsUsesPredictionHistoryBeforeDefaultSpray(t *testing.T) {
	resetPredictionHistoryForTest()
	t.Cleanup(resetPredictionHistoryForTest)

	self := P2PPeerInfo{
		LocalAddrs: []string{"192.168.0.10:4000"},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			NATType:          NATTypeSymmetric,
			MappingBehavior:  NATMappingEndpointDependent,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
	}
	recordPredictionSuccess(peer.Nat, "1.1.1.1:5006")
	recordPredictionSuccess(peer.Nat, "1.1.1.1:5006")
	recordPredictionSuccess(peer.Nat, "1.1.1.1:5008")

	targets := BuildPreferredPunchTargets(self, peer, PunchPlan{
		PredictionIntervals: []int{2},
		SpraySpan:           8,
	})
	if len(targets) < 3 {
		t.Fatalf("expected enough targets, got %#v", targets)
	}
	if targets[0] != "1.1.1.1:5000" {
		t.Fatalf("first target = %q, want observed addr first (%#v)", targets[0], targets)
	}
	if targets[1] != "1.1.1.1:5006" {
		t.Fatalf("second target = %q, want historical offset first (%#v)", targets[1], targets)
	}
}

func TestCandidateRankerKeepsForeignPrivateFallbackBelowPublicFallback(t *testing.T) {
	ranker := NewCandidateRanker(
		P2PPeerInfo{LocalAddrs: []string{"192.168.1.10:4000"}},
		P2PPeerInfo{Nat: NatObservation{PublicIP: "203.0.113.10", ObservedBasePort: 5000}},
		PunchPlan{},
	)
	publicFallback := ranker.Priority("", "203.0.113.10:5002")
	foreignPrivate := ranker.Priority("", "10.0.0.5:6000")
	sameSubnetPrivate := ranker.Priority("", "192.168.1.20:6000")
	if foreignPrivate.Score >= publicFallback.Score {
		t.Fatalf("foreign private fallback should stay below public fallback: private=%#v public=%#v", foreignPrivate, publicFallback)
	}
	if sameSubnetPrivate.Score <= foreignPrivate.Score {
		t.Fatalf("same-subnet private fallback should outrank foreign private fallback: same=%#v foreign=%#v", sameSubnetPrivate, foreignPrivate)
	}
}

func TestCandidateRankerReusesPreferredTargetPlans(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{"192.168.0.10:4000"},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
		LocalAddrs: []string{"192.168.0.20:5000"},
	}
	plan := PunchPlan{
		UseTargetSpray:         true,
		AllowBirthdayFallback:  true,
		PredictionIntervals:    []int{2},
		SpraySpan:              6,
		BirthdayTargetsPerPort: 4,
	}
	ranker := NewCandidateRanker(self, peer, plan)
	if !reflect.DeepEqual(ranker.DirectTargets(), BuildPreferredDirectPunchTargets(self, peer)) {
		t.Fatalf("direct targets diverged: got %#v want %#v", ranker.DirectTargets(), BuildPreferredDirectPunchTargets(self, peer))
	}
	if !reflect.DeepEqual(ranker.PrimaryTargets(true), BuildPreferredPunchTargets(self, peer, plan)) {
		t.Fatalf("primary targets diverged: got %#v want %#v", ranker.PrimaryTargets(true), BuildPreferredPunchTargets(self, peer, plan))
	}
	if !reflect.DeepEqual(ranker.BirthdayTargets(), BuildBirthdayPunchTargets(peer, plan)) {
		t.Fatalf("birthday targets diverged: got %#v want %#v", ranker.BirthdayTargets(), BuildBirthdayPunchTargets(peer, plan))
	}
}

func TestCandidateRankerTargetOnlyTargetsExcludeDirectSet(t *testing.T) {
	self := P2PPeerInfo{LocalAddrs: []string{"192.168.0.10:4000"}}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			Samples:          []ProbeSample{{ObservedAddr: "1.1.1.1:5000"}},
		},
	}
	ranker := NewCandidateRanker(self, peer, PunchPlan{
		UseTargetSpray:      true,
		PredictionIntervals: []int{2},
		SpraySpan:           6,
	})
	direct := ranker.DirectTargets()
	targetOnly := ranker.TargetOnlyTargets()
	for _, directAddr := range direct {
		for _, targetAddr := range targetOnly {
			if directAddr == targetAddr {
				t.Fatalf("target-only targets should exclude direct target %q", directAddr)
			}
		}
	}
	if len(targetOnly) == 0 {
		t.Fatal("target-only targets should preserve prediction addresses")
	}
}

func TestCandidateRankerTargetStagesPreferLikelyNextOnSequentialMappings(t *testing.T) {
	self := P2PPeerInfo{LocalAddrs: []string{"192.168.0.10:4000"}}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			MappingBehavior:  NATMappingEndpointDependent,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
				{ObservedAddr: "1.1.1.1:5002"},
				{ObservedAddr: "1.1.1.1:5004"},
			},
		},
	}
	ranker := NewCandidateRanker(self, peer, PunchPlan{
		UseTargetSpray:      true,
		PredictionIntervals: []int{2},
		SpraySpan:           6,
	})
	stages := ranker.TargetStages()
	if len(stages) == 0 {
		t.Fatal("target stages should be available")
	}
	if stages[0].Name != "target_likely_next" {
		t.Fatalf("first target stage = %q, want target_likely_next", stages[0].Name)
	}
	if len(stages[0].Targets) == 0 || stages[0].Targets[0] != "1.1.1.1:5006" {
		t.Fatalf("likely-next stage should lead with the next sequential port, got %#v", stages[0].Targets)
	}
}

func TestCandidateRankerResponsivePriorityPromotesAuthenticatedPublicCandidate(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{"192.168.0.10:4000"},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
	}
	ranker := NewCandidateRanker(self, peer, PunchPlan{UseTargetSpray: true})
	staticPriority := ranker.Priority("", "1.1.1.1:5012")
	responsivePriority := ranker.ResponsivePriority("1.1.1.1:5012")
	if responsivePriority.Score <= staticPriority.Score {
		t.Fatalf("responsive candidate should outrank static fallback: responsive=%#v static=%#v", responsivePriority, staticPriority)
	}
	if responsivePriority.Reason != "responsive_public(delta=12)" {
		t.Fatalf("responsive reason = %q, want responsive_public(delta=12)", responsivePriority.Reason)
	}
}

func TestCandidateRankerResponsivePriorityPrefersSameSubnetLocalCandidate(t *testing.T) {
	self := P2PPeerInfo{
		LocalAddrs: []string{"192.168.0.10:4000"},
	}
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
		},
	}
	ranker := NewCandidateRanker(self, peer, PunchPlan{})
	sameSubnet := ranker.ResponsivePriority("192.168.0.20:5000")
	public := ranker.ResponsivePriority("1.1.1.1:5000")
	if sameSubnet.Score <= public.Score {
		t.Fatalf("same subnet responsive candidate should outrank public responsive candidate: same=%#v public=%#v", sameSubnet, public)
	}
}

func TestBuildBirthdayPunchTargets(t *testing.T) {
	peer := P2PPeerInfo{
		Nat: NatObservation{
			PublicIP:         "1.1.1.1",
			ObservedBasePort: 5000,
			ObservedInterval: 2,
			Samples: []ProbeSample{
				{ObservedAddr: "1.1.1.1:5000"},
			},
		},
	}
	targets := BuildBirthdayPunchTargets(peer, PunchPlan{
		PredictionInterval:     2,
		BirthdayTargetsPerPort: 4,
	})
	if len(targets) < 4 {
		t.Fatalf("expected birthday targets, got %#v", targets)
	}
	if targets[0] != "1.1.1.1:5000" {
		t.Fatalf("first birthday target = %q", targets[0])
	}
}
