package p2p

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProbePolicyJSONRoundTrip(t *testing.T) {
	policy := &P2PPolicy{
		Layout:          "triple-port",
		BasePort:        10206,
		ProbeTimeoutMs:  1500,
		ProbeExtraReply: true,
		Traversal: P2PTraversalPolicy{
			ForcePredictOnRestricted:  true,
			EnableTargetSpray:         true,
			EnableBirthdayAttack:      true,
			DefaultPredictionInterval: 8,
			TargetSpraySpan:           128,
			TargetSprayRounds:         3,
			TargetSprayBurst:          10,
			TargetSprayPacketSleepMs:  5,
			TargetSprayBurstGapMs:     8,
			TargetSprayPhaseGapMs:     30,
			BirthdayListenPorts:       64,
			BirthdayTargetsPerPort:    128,
			NominationDelayMs:         120,
			NominationRetryMs:         220,
		},
		PortMapping: P2PPortMappingPolicy{
			EnableUPNPPortmap:   true,
			EnablePCPPortmap:    true,
			EnableNATPMPPortmap: true,
			LeaseSeconds:        3600,
		},
	}

	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("json.Marshal(policy) error = %v", err)
	}
	var roundTrip P2PPolicy
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal(policy) error = %v", err)
	}
	if !reflect.DeepEqual(*policy, roundTrip) {
		t.Fatalf("typed policy JSON round trip mismatch want=%#v got=%#v", *policy, roundTrip)
	}
}

func TestSummaryHintsJSONRoundTrip(t *testing.T) {
	hints := &P2PSummaryHints{
		ProbePortRestricted:    true,
		MappingConfidenceLow:   true,
		SelfProbeEndpointCount: 3,
		PeerProbeEndpointCount: 4,
		SharedFamilyCount:      2,
		SharedFamilies:         []string{"udp4", "udp6"},
		DualStackParallel:      true,
		SelfFamilyDetails: map[string]P2PFamilyHintDetail{
			"udp4": {NATType: NATTypeRestrictedCone, ProbeProviderCount: 1, SampleCount: 3},
		},
	}

	raw, err := json.Marshal(hints)
	if err != nil {
		t.Fatalf("json.Marshal(hints) error = %v", err)
	}
	var roundTrip P2PSummaryHints
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal(hints) error = %v", err)
	}
	if !reflect.DeepEqual(*hints, roundTrip) {
		t.Fatalf("typed summary hints JSON round trip mismatch want=%#v got=%#v", *hints, roundTrip)
	}
}
