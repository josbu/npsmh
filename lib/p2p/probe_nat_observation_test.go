package p2p

import (
	"strings"
	"testing"
)

func TestBuildNatObservation(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "10.0.0.1:10206"},
		{ProbePort: 10207, ObservedAddr: "1.1.1.1:4002", ServerReplyAddr: "10.0.0.2:10207"},
		{ProbePort: 10208, ObservedAddr: "1.1.1.1:4004", ServerReplyAddr: "10.0.0.3:10208"},
	}, true)
	if obs.PublicIP != "1.1.1.1" {
		t.Fatalf("PublicIP = %q, want 1.1.1.1", obs.PublicIP)
	}
	if obs.ObservedBasePort != 4000 {
		t.Fatalf("ObservedBasePort = %d, want 4000", obs.ObservedBasePort)
	}
	if obs.ObservedInterval != 2 {
		t.Fatalf("ObservedInterval = %d, want 2", obs.ObservedInterval)
	}
	if obs.ProbePortRestricted {
		t.Fatal("extra reply should keep ProbePortRestricted=false")
	}
	if obs.ProbeIPCount != 3 {
		t.Fatalf("ProbeIPCount = %d, want 3", obs.ProbeIPCount)
	}
	if obs.MappingConfidenceLow {
		t.Fatal("multi-probe-ip regular samples should not be low confidence")
	}
	if obs.MappingBehavior != NATMappingEndpointDependent || obs.NATType != NATTypeSymmetric {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
}

func TestBuildNatObservationLowConfidence(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "10.0.0.1:10206"},
	}, false)
	if !obs.ProbePortRestricted {
		t.Fatal("missing extra reply should mark probe port restricted")
	}
	if !obs.MappingConfidenceLow {
		t.Fatal("single sample should stay low confidence")
	}
}

func TestBuildNatObservationSingleProbeIPMultiEndpointStillClassifiesMapping(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "10.0.0.1:10206"},
		{ProbePort: 10207, ObservedAddr: "1.1.1.1:4002", ServerReplyAddr: "10.0.0.1:10207"},
		{ProbePort: 10208, ObservedAddr: "1.1.1.1:4004", ServerReplyAddr: "10.0.0.1:10208"},
	}, true)
	if obs.ProbeIPCount != 1 {
		t.Fatalf("ProbeIPCount = %d, want 1", obs.ProbeIPCount)
	}
	if obs.ProbeEndpointCount != 3 {
		t.Fatalf("ProbeEndpointCount = %d, want 3", obs.ProbeEndpointCount)
	}
	if obs.MappingConfidenceLow {
		t.Fatalf("single probe ip with multi-endpoint evidence should classify, got %#v", obs)
	}
	if obs.NATType != NATTypeSymmetric || obs.MappingBehavior != NATMappingEndpointDependent {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
	if obs.ClassificationLevel != ClassificationConfidenceMed {
		t.Fatalf("ClassificationLevel = %q, want %q", obs.ClassificationLevel, ClassificationConfidenceMed)
	}
}

func TestBuildNatObservationConflictingSignalsStayLowConfidence(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "10.0.0.1:10206"},
		{ProbePort: 10207, ObservedAddr: "2.2.2.2:4002", ServerReplyAddr: "10.0.0.2:10207"},
		{ProbePort: 10208, ObservedAddr: "1.1.1.1:4004", ServerReplyAddr: "10.0.0.3:10208"},
	}, false)
	if !obs.ConflictingSignals {
		t.Fatal("multiple observed public IPs should be marked conflicting")
	}
	if !obs.MappingConfidenceLow {
		t.Fatal("conflicting signals should stay low confidence")
	}
}

func TestBuildNatObservationClassifiesRestrictedConeWhenNPSMultiEndpointStable(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10206"},
		{ProbePort: 10207, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.11:10207"},
		{ProbePort: 10208, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.12:10208"},
	}, true)
	if obs.NATType != NATTypeRestrictedCone || obs.MappingBehavior != NATMappingEndpointIndependent || obs.FilteringBehavior != NATFilteringOpen {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
	if obs.MappingConfidenceLow {
		t.Fatalf("multi-probe-ip stable mapping should not stay low confidence %#v", obs)
	}
}

func TestBuildNatObservationClassifiesPortRestrictedWhenMappingStableAndExtraReplyFails(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10206"},
		{ProbePort: 10207, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.11:10207"},
		{ProbePort: 10208, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.12:10208"},
	}, false)
	if obs.NATType != NATTypePortRestricted || obs.FilteringBehavior != NATFilteringPortRestricted {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
}

func TestBuildNatObservationSingleProbeIPStableMappingStaysLowConfidence(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10206"},
		{ProbePort: 10207, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10207"},
		{ProbePort: 10208, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10208"},
	}, true)
	if obs.NATType != NATTypeRestrictedCone || obs.MappingBehavior != NATMappingEndpointIndependent || obs.FilteringBehavior != NATFilteringOpen {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
	if !obs.MappingConfidenceLow {
		t.Fatalf("single-probe-ip stable mapping evidence should stay low confidence %#v", obs)
	}
	if obs.ClassificationLevel != ClassificationConfidenceLow {
		t.Fatalf("ClassificationLevel = %q, want %q", obs.ClassificationLevel, ClassificationConfidenceLow)
	}
}

func TestBuildNatObservationSingleProbeIPStablePortRestrictedStaysLowConfidence(t *testing.T) {
	obs := BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10206"},
		{ProbePort: 10207, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10207"},
		{ProbePort: 10208, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10208"},
	}, false)
	if obs.NATType != NATTypePortRestricted || obs.FilteringBehavior != NATFilteringPortRestricted {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
	if !obs.MappingConfidenceLow {
		t.Fatalf("single-probe-ip stable mapping evidence should stay low confidence %#v", obs)
	}
}

func TestBuildNatObservationWithoutFilteringProbeKeepsFilteringUnknown(t *testing.T) {
	obs := BuildNatObservationWithEvidence([]ProbeSample{
		{ProbePort: 10206, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.10:10206"},
		{ProbePort: 10207, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.11:10207"},
		{ProbePort: 10208, Provider: ProbeProviderNPS, Mode: ProbeModeUDP, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "203.0.113.12:10208"},
	}, false, false)
	if obs.FilteringTested {
		t.Fatal("filtering should stay untested when extra-reply probe is disabled")
	}
	if obs.FilteringBehavior != NATFilteringUnknown {
		t.Fatalf("FilteringBehavior = %q, want unknown", obs.FilteringBehavior)
	}
	if obs.NATType != NATTypeUnknown {
		t.Fatalf("NATType = %q, want unknown", obs.NATType)
	}
	if obs.ProbePortRestricted {
		t.Fatal("untested filtering should not be forced to port restricted")
	}
}

func TestMergeProbeSamplesKeepsDistinctNPSReplyIPsOnSameProbePort(t *testing.T) {
	serverSamples := []ProbeSample{
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10206,
			ObservedAddr:    "1.1.1.1:4000",
			ServerReplyAddr: "0.0.0.0:10206",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10207,
			ObservedAddr:    "1.1.1.1:4002",
			ServerReplyAddr: "0.0.0.0:10207",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10208,
			ObservedAddr:    "1.1.1.1:4004",
			ServerReplyAddr: "0.0.0.0:10208",
		},
	}
	clientSamples := []ProbeSample{
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10206,
			ObservedAddr:    "1.1.1.1:4000",
			ServerReplyAddr: "203.0.113.10:10206",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10207,
			ObservedAddr:    "1.1.1.1:4002",
			ServerReplyAddr: "203.0.113.10:10207",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10208,
			ObservedAddr:    "1.1.1.1:4004",
			ServerReplyAddr: "203.0.113.10:10208",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10206,
			ObservedAddr:    "1.1.1.1:4010",
			ServerReplyAddr: "203.0.113.11:10206",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10207,
			ObservedAddr:    "1.1.1.1:4012",
			ServerReplyAddr: "203.0.113.11:10207",
		},
		{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       10208,
			ObservedAddr:    "1.1.1.1:4014",
			ServerReplyAddr: "203.0.113.11:10208",
		},
	}

	merged := MergeProbeSamples(serverSamples, clientSamples)
	if len(merged) != 6 {
		t.Fatalf("len(merged) = %d, want 6 (%#v)", len(merged), merged)
	}
	for _, sample := range merged {
		if strings.HasPrefix(sample.ServerReplyAddr, "0.0.0.0:") {
			t.Fatalf("wildcard server sample should merge into concrete client samples, got %#v", merged)
		}
	}
	obs := BuildNatObservation(merged, true)
	if obs.ProbeIPCount != 2 {
		t.Fatalf("ProbeIPCount = %d, want 2 (%#v)", obs.ProbeIPCount, merged)
	}
	if obs.MappingBehavior != NATMappingEndpointDependent || obs.NATType != NATTypeSymmetric {
		t.Fatalf("unexpected nat classification %#v", obs)
	}
}

func TestNPSProbeKeyIgnoresReplyPortButKeepsReplyIP(t *testing.T) {
	keyA := npsProbeKey(10206, "203.0.113.10:10206")
	keyB := npsProbeKey(10206, "203.0.113.10:49152")
	keyC := npsProbeKey(10206, "203.0.113.11:10206")
	if keyA == "" {
		t.Fatal("expected concrete reply IP to produce a probe key")
	}
	if keyA != keyB {
		t.Fatalf("same reply IP should share the same key: %q vs %q", keyA, keyB)
	}
	if keyA == keyC {
		t.Fatalf("different reply IPs should not share the same key: %q vs %q", keyA, keyC)
	}
	if got := npsProbeKey(10206, "0.0.0.0:10206"); got != "" {
		t.Fatalf("wildcard reply addr should not produce a key, got %q", got)
	}
}
