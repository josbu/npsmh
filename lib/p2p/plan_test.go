package p2p

import (
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func TestSelectPunchPlanPredictionDefaults(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}},
		P2PPeerInfo{Nat: NatObservation{ObservedBasePort: 5000, ObservedInterval: 0}},
		DefaultTimeouts(),
	)
	if !plan.EnablePrediction {
		t.Fatal("restricted/low-confidence observation should enable prediction")
	}
	if !plan.UseTargetSpray {
		t.Fatal("default main path should use target spray")
	}
	if plan.UseBirthdayAttack {
		t.Fatal("birthday attack must stay disabled by default")
	}
	if plan.AllowBirthdayFallback {
		t.Fatal("single-sided hard NAT should stay on the default target-spray main path")
	}
	if len(plan.PredictionIntervals) == 0 || plan.PredictionIntervals[0] != DefaultPredictionInterval {
		t.Fatalf("unexpected PredictionIntervals %#v", plan.PredictionIntervals)
	}
	if plan.PredictionInterval != DefaultPredictionInterval {
		t.Fatalf("PredictionInterval = %d, want %d", plan.PredictionInterval, DefaultPredictionInterval)
	}
	if plan.HandshakeTimeout != 20*time.Second {
		t.Fatalf("HandshakeTimeout = %s, want 20s", plan.HandshakeTimeout)
	}
}

func TestPunchPlanNominationTimingCapsToHandshakeTimeout(t *testing.T) {
	plan := PunchPlan{
		HandshakeTimeout:        300 * time.Millisecond,
		NominationDelay:         120 * time.Millisecond,
		NominationRetryInterval: 300 * time.Millisecond,
	}
	if got := plan.nominationDelay(); got != 50*time.Millisecond {
		t.Fatalf("nominationDelay() = %s, want %s", got, 50*time.Millisecond)
	}
	if got := plan.nominationRetryInterval(); got != 60*time.Millisecond {
		t.Fatalf("nominationRetryInterval() = %s, want %s", got, 60*time.Millisecond)
	}
	if got := plan.renominationTimeout(); got != 75*time.Millisecond {
		t.Fatalf("renominationTimeout() = %s, want %s", got, 75*time.Millisecond)
	}
	if got := plan.renominationCooldown(); got != 60*time.Millisecond {
		t.Fatalf("renominationCooldown() = %s, want %s", got, 60*time.Millisecond)
	}
}

func TestSelectPunchPlanSingleProbeIPMultiEndpointStillTriggersPrediction(t *testing.T) {
	self := P2PPeerInfo{Nat: BuildNatObservation([]ProbeSample{
		{ProbePort: 10206, ObservedAddr: "1.1.1.1:4000", ServerReplyAddr: "10.0.0.1:10206"},
		{ProbePort: 10207, ObservedAddr: "1.1.1.1:4002", ServerReplyAddr: "10.0.0.1:10207"},
		{ProbePort: 10208, ObservedAddr: "1.1.1.1:4004", ServerReplyAddr: "10.0.0.1:10208"},
	}, true)}
	peer := P2PPeerInfo{Nat: NatObservation{ObservedBasePort: 5000, ObservedInterval: 2}}
	plan := SelectPunchPlan(self, peer, DefaultTimeouts())
	if self.Nat.MappingConfidenceLow {
		t.Fatalf("single probe ip should now classify from multi-endpoint evidence: %#v", self.Nat)
	}
	if self.Nat.NATType != NATTypeSymmetric {
		t.Fatalf("NATType = %q, want %q", self.Nat.NATType, NATTypeSymmetric)
	}
	if !plan.EnablePrediction || !plan.UseTargetSpray {
		t.Fatalf("unexpected plan %#v", plan)
	}
	if len(plan.PredictionIntervals) == 0 {
		t.Fatal("prediction should build interval candidates")
	}
}

func TestSelectPunchPlanAllowsBirthdayFallbackForHardPeers(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}},
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true, ObservedBasePort: 5000}},
		DefaultTimeouts(),
	)
	if !plan.AllowBirthdayFallback {
		t.Fatal("dual hard/unknown peers should allow birthday fallback after target spray failure")
	}
	if plan.UseBirthdayAttack {
		t.Fatal("birthday attack must still be off until the target-spray path fails")
	}
}

func TestSelectPunchPlanEnablesPredictionForClassifiedSymmetricPeer(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{NATType: NATTypeSymmetric, MappingBehavior: NATMappingEndpointDependent}},
		P2PPeerInfo{Nat: NatObservation{ObservedBasePort: 5000, NATType: NATTypeRestrictedCone}},
		DefaultTimeouts(),
	)
	if !plan.EnablePrediction || !plan.UseTargetSpray {
		t.Fatalf("classified symmetric nat should still enable prediction, got %#v", plan)
	}
}

func TestSelectPunchPlanKeepsBirthdayFallbackDisabledForLoosePeers(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{ObservedBasePort: 5000, ObservedInterval: 2}},
		P2PPeerInfo{Nat: NatObservation{ObservedBasePort: 6000, ObservedInterval: 2}},
		DefaultTimeouts(),
	)
	if plan.AllowBirthdayFallback {
		t.Fatal("loose peers should not enable birthday fallback")
	}
	if plan.UseBirthdayAttack {
		t.Fatal("birthday attack must not be enabled directly by default")
	}
}

func TestSelectPunchPlanTreatsUnknownFilteringAsConservative(t *testing.T) {
	self := P2PPeerInfo{Nat: NatObservation{
		ObservedBasePort:    5000,
		ProbeEndpointCount:  3,
		MappingBehavior:     NATMappingEndpointIndependent,
		NATType:             NATTypeUnknown,
		ClassificationLevel: ClassificationConfidenceLow,
		Samples: []ProbeSample{
			{ProbePort: 10206, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.10:10206"},
			{ProbePort: 10207, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.10:10207"},
			{ProbePort: 10208, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.10:10208"},
		},
	}}
	peer := P2PPeerInfo{Nat: NatObservation{
		ObservedBasePort:    6000,
		ProbeEndpointCount:  3,
		MappingBehavior:     NATMappingEndpointIndependent,
		NATType:             NATTypeUnknown,
		ClassificationLevel: ClassificationConfidenceLow,
		Samples: []ProbeSample{
			{ProbePort: 10206, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "203.0.113.20:10206"},
			{ProbePort: 10207, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "203.0.113.20:10207"},
			{ProbePort: 10208, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "203.0.113.20:10208"},
		},
	}}
	plan := SelectPunchPlan(self, peer, DefaultTimeouts())
	if !plan.EnablePrediction {
		t.Fatalf("unknown filtering should keep prediction enabled, got %#v", plan)
	}
	if !plan.AllowBirthdayFallback {
		t.Fatalf("unknown filtering on both peers should keep conservative fallback enabled, got %#v", plan)
	}
}

func TestSelectPunchPlanTunesEasyPeersForLowerSprayBudget(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, Samples: []ProbeSample{{ObservedAddr: "1.1.1.1:5000"}}}},
		P2PPeerInfo{Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, ObservedBasePort: 6000, Samples: []ProbeSample{{ObservedAddr: "2.2.2.2:6000"}}}},
		DefaultTimeouts(),
	)
	if plan.EnablePrediction || plan.UseTargetSpray {
		t.Fatalf("easy peers should stay on the light direct path, got %#v", plan)
	}
	if plan.SprayRounds != 1 || plan.SprayBurst != 4 {
		t.Fatalf("easy peers should reduce spray budget, got %#v", plan)
	}
	if plan.SprayPerPacketSleep != 5*time.Millisecond {
		t.Fatalf("easy peers should respect the paced spray floor, got %#v", plan)
	}
	if plan.NominationDelay != 80*time.Millisecond || plan.NominationRetryInterval != 180*time.Millisecond {
		t.Fatalf("easy peers should use faster nomination timing, got %#v", plan)
	}
}

func TestSelectPunchPlanKeepsPredictionForLowConfidenceNPSOnlyEvidence(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{
			PublicIP:            "1.1.1.1",
			ObservedBasePort:    5000,
			MappingBehavior:     NATMappingEndpointIndependent,
			FilteringBehavior:   NATFilteringOpen,
			NATType:             NATTypeRestrictedCone,
			ClassificationLevel: ClassificationConfidenceLow,
			Samples: []ProbeSample{
				{Provider: ProbeProviderNPS, ProbePort: 10206, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.10:10206"},
				{Provider: ProbeProviderNPS, ProbePort: 10207, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.10:10207"},
			},
		}},
		P2PPeerInfo{Nat: NatObservation{
			PublicIP:            "2.2.2.2",
			ObservedBasePort:    6000,
			MappingBehavior:     NATMappingEndpointIndependent,
			FilteringBehavior:   NATFilteringOpen,
			NATType:             NATTypeRestrictedCone,
			ClassificationLevel: ClassificationConfidenceLow,
			Samples: []ProbeSample{
				{Provider: ProbeProviderNPS, ProbePort: 10206, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "203.0.113.20:10206"},
				{Provider: ProbeProviderNPS, ProbePort: 10207, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "203.0.113.20:10207"},
			},
		}},
		DefaultTimeouts(),
	)
	if !plan.EnablePrediction || !plan.UseTargetSpray {
		t.Fatalf("low-confidence NPS-only evidence should keep conservative prediction enabled, got %#v", plan)
	}
}

func TestSelectPunchPlanTunesHardPeersForHigherSprayBudget(t *testing.T) {
	plan := SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{NATType: NATTypeSymmetric, ProbePortRestricted: true, MappingConfidenceLow: true}},
		P2PPeerInfo{Nat: NatObservation{NATType: NATTypeSymmetric, ProbePortRestricted: true, MappingConfidenceLow: true, ObservedBasePort: 6000}},
		DefaultTimeouts(),
	)
	if !plan.AllowBirthdayFallback || !plan.EnablePrediction {
		t.Fatalf("hard peers should keep the hard path enabled, got %#v", plan)
	}
	if plan.SprayRounds != 3 || plan.SprayBurst != 10 {
		t.Fatalf("hard peers should increase spray budget, got %#v", plan)
	}
	if plan.SprayPerPacketSleep != 5*time.Millisecond {
		t.Fatalf("hard peers should keep the paced spray floor, got %#v", plan)
	}
}

func TestApplyProbePlanOptionsRespectsBirthdayDefaultOff(t *testing.T) {
	plan := ApplyProbePlanOptions(SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}},
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}},
		DefaultTimeouts(),
	), P2PProbeConfig{
		Policy: &P2PPolicy{
			Traversal: P2PTraversalPolicy{
				EnableBirthdayAttack: false,
			},
		},
	}, P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}}, P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}})
	if plan.AllowBirthdayFallback {
		t.Fatal("birthday fallback should stay disabled when config option is false")
	}
}

func TestApplyProbePlanOptionsOverridesSprayConfig(t *testing.T) {
	plan := ApplyProbePlanOptions(SelectPunchPlan(
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}},
		P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}},
		DefaultTimeouts(),
	), P2PProbeConfig{
		Policy: &P2PPolicy{
			Traversal: P2PTraversalPolicy{
				EnableTargetSpray:         false,
				EnableBirthdayAttack:      true,
				DefaultPredictionInterval: 3,
				TargetSpraySpan:           12,
				TargetSprayRounds:         4,
				TargetSprayBurst:          2,
				TargetSprayPacketSleepMs:  7,
				TargetSprayBurstGapMs:     11,
				TargetSprayPhaseGapMs:     13,
				BirthdayListenPorts:       6,
				BirthdayTargetsPerPort:    9,
			},
		},
	}, P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}}, P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true, MappingConfidenceLow: true}})
	if plan.UseTargetSpray {
		t.Fatal("enable_target_spray=false should disable target spray")
	}
	if !plan.AllowBirthdayFallback {
		t.Fatal("enable_birthday_attack=true should preserve birthday fallback availability")
	}
	if !plan.UseBirthdayAttack {
		t.Fatal("enable_birthday_attack=true should mark the birthday path as enabled for hard peers")
	}
	if plan.PredictionInterval != 3 || plan.SpraySpan != 12 || plan.SprayRounds != 4 || plan.SprayBurst != 2 {
		t.Fatalf("unexpected spray config %#v", plan)
	}
	if plan.SprayPerPacketSleep != 7*time.Millisecond || plan.SprayBurstGap != 11*time.Millisecond || plan.SprayPhaseGap != 13*time.Millisecond {
		t.Fatalf("unexpected spray timings %#v", plan)
	}
	if plan.BirthdayListenPorts != 6 || plan.BirthdayTargetsPerPort != 9 {
		t.Fatalf("unexpected birthday config %#v", plan)
	}
}

func TestApplyProbePlanOptionsKeepsBirthdayFallbackForSymmetricPeersWhenEnabled(t *testing.T) {
	self := P2PPeerInfo{Nat: NatObservation{NATType: NATTypeSymmetric, MappingBehavior: NATMappingEndpointDependent}}
	peer := P2PPeerInfo{Nat: NatObservation{NATType: NATTypeSymmetric, MappingBehavior: NATMappingEndpointDependent, ObservedBasePort: 5000}}
	plan := ApplyProbePlanOptions(SelectPunchPlan(self, peer, DefaultTimeouts()), P2PProbeConfig{
		Policy: &P2PPolicy{
			Traversal: P2PTraversalPolicy{
				EnableBirthdayAttack: true,
			},
		},
	}, self, peer)
	if !plan.AllowBirthdayFallback {
		t.Fatal("symmetric peers should keep birthday fallback when config enables it")
	}
	if !plan.UseBirthdayAttack {
		t.Fatal("birthday attack should be marked enabled when config allows it for symmetric peers")
	}
}

func TestBuildPredictionIntervals(t *testing.T) {
	intervals := BuildPredictionIntervals(NatObservation{
		ObservedInterval: 4,
		Samples: []ProbeSample{
			{ObservedAddr: "1.1.1.1:4000"},
			{ObservedAddr: "1.1.1.1:4004"},
			{ObservedAddr: "1.1.1.1:4008"},
		},
	}, true, true)
	if len(intervals) < 3 {
		t.Fatalf("expected multiple intervals, got %#v", intervals)
	}
	if intervals[0] != 4 {
		t.Fatalf("primary interval = %d, want 4", intervals[0])
	}
	foundOne := false
	for _, interval := range intervals {
		if interval == 1 {
			foundOne = true
			break
		}
	}
	if !foundOne {
		t.Fatalf("interval candidates should include normalized fallback 1, got %#v", intervals)
	}
}

func TestApplyProbePlanOptionsCanDisableForcedPrediction(t *testing.T) {
	self := P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: true}}
	peer := P2PPeerInfo{Nat: NatObservation{ProbePortRestricted: false, Samples: []ProbeSample{{ObservedAddr: "1.1.1.1:5000"}}}}
	plan := ApplyProbePlanOptions(SelectPunchPlan(self, peer, DefaultTimeouts()), P2PProbeConfig{
		Policy: &P2PPolicy{
			Traversal: P2PTraversalPolicy{
				ForcePredictOnRestricted: false,
			},
		},
	}, self, peer)
	if plan.EnablePrediction {
		t.Fatal("force_predict_on_restricted=false should disable prediction when low confidence is absent")
	}
	if plan.UseTargetSpray {
		t.Fatal("target spray should be disabled when forced prediction is turned off and peer samples exist")
	}
}

func TestApplySummaryHintsToPlanDualStackStrongEvidence(t *testing.T) {
	plan := PunchPlan{
		HandshakeTimeout:        2 * time.Second,
		NominationDelay:         80 * time.Millisecond,
		NominationRetryInterval: 300 * time.Millisecond,
		SprayRounds:             2,
		SprayBurst:              8,
	}
	summary := P2PProbeSummary{
		Self: P2PPeerInfo{Nat: NatObservation{
			NATType:             NATTypeRestrictedCone,
			MappingBehavior:     NATMappingEndpointIndependent,
			FilteringBehavior:   NATFilteringOpen,
			FilteringTested:     true,
			ClassificationLevel: ClassificationConfidenceHigh,
			ProbeIPCount:        2,
			ProbeEndpointCount:  2,
			Samples: []ProbeSample{
				{Provider: ProbeProviderNPS, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.10:10206"},
				{Provider: ProbeProviderNPS, ObservedAddr: "1.1.1.1:5000", ServerReplyAddr: "203.0.113.11:10207"},
			},
		}},
		Peer: P2PPeerInfo{Nat: NatObservation{
			NATType:             NATTypeRestrictedCone,
			MappingBehavior:     NATMappingEndpointIndependent,
			FilteringBehavior:   NATFilteringOpen,
			FilteringTested:     true,
			ClassificationLevel: ClassificationConfidenceHigh,
			ProbeIPCount:        2,
			ProbeEndpointCount:  2,
			Samples: []ProbeSample{
				{Provider: ProbeProviderNPS, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "198.51.100.10:10206"},
				{Provider: ProbeProviderNPS, ObservedAddr: "2.2.2.2:6000", ServerReplyAddr: "198.51.100.11:10207"},
			},
		}},
		SummaryHints: &P2PSummaryHints{
			SharedFamilyCount: 2,
		},
	}
	tuned := ApplySummaryHintsToPlan(plan, summary)
	if tuned.SprayRounds != 1 || tuned.SprayBurst != 5 {
		t.Fatalf("ApplySummaryHintsToPlan() spray budget = (%d,%d), want (1,5)", tuned.SprayRounds, tuned.SprayBurst)
	}
	if tuned.NominationDelay != 100*time.Millisecond {
		t.Fatalf("ApplySummaryHintsToPlan() nomination delay = %s, want 100ms", tuned.NominationDelay)
	}
	if tuned.NominationRetryInterval != 220*time.Millisecond {
		t.Fatalf("ApplySummaryHintsToPlan() nomination retry = %s, want 220ms", tuned.NominationRetryInterval)
	}
}

func TestApplySummaryHintsToPlanUsesAdaptiveProfileHistory(t *testing.T) {
	resetAdaptiveProfileHistoryForTest()
	t.Cleanup(resetAdaptiveProfileHistoryForTest)

	summary := P2PProbeSummary{
		Self: P2PPeerInfo{Nat: NatObservation{
			NATType:             NATTypeRestrictedCone,
			MappingBehavior:     NATMappingEndpointIndependent,
			FilteringBehavior:   NATFilteringOpen,
			FilteringTested:     true,
			ClassificationLevel: ClassificationConfidenceMed,
			Samples: []ProbeSample{
				{Provider: ProbeProviderNPS, ObservedAddr: "1.1.1.1:5000"},
			},
		}},
		Peer: P2PPeerInfo{Nat: NatObservation{
			NATType:             NATTypeRestrictedCone,
			MappingBehavior:     NATMappingEndpointIndependent,
			FilteringBehavior:   NATFilteringOpen,
			FilteringTested:     true,
			ClassificationLevel: ClassificationConfidenceMed,
			ObservedBasePort:    6000,
			Samples: []ProbeSample{
				{Provider: ProbeProviderNPS, ObservedAddr: "2.2.2.2:6000"},
			},
		}},
	}
	recordAdaptiveProfileSuccess(summary, common.CONN_KCP)
	recordAdaptiveProfileSuccess(summary, common.CONN_KCP)

	plan := PunchPlan{
		HandshakeTimeout:        2 * time.Second,
		NominationDelay:         120 * time.Millisecond,
		NominationRetryInterval: 300 * time.Millisecond,
		SprayRounds:             2,
		SprayBurst:              8,
	}
	tuned := ApplySummaryHintsToPlan(plan, summary)
	if tuned.SprayRounds != 1 || tuned.SprayBurst != 7 {
		t.Fatalf("adaptive history should lighten plan, got rounds=%d burst=%d", tuned.SprayRounds, tuned.SprayBurst)
	}
	if tuned.NominationDelay != 105*time.Millisecond || tuned.NominationRetryInterval != 200*time.Millisecond {
		t.Fatalf("adaptive history should tighten nomination timing, got delay=%s retry=%s", tuned.NominationDelay, tuned.NominationRetryInterval)
	}
}

func TestAdaptiveProfileTimeoutReducesScore(t *testing.T) {
	resetAdaptiveProfileHistoryForTest()
	t.Cleanup(resetAdaptiveProfileHistoryForTest)

	summary := P2PProbeSummary{
		Self: P2PPeerInfo{Families: []P2PFamilyInfo{{Family: "udp4", Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, FilteringTested: true, ClassificationLevel: ClassificationConfidenceMed}}}},
		Peer: P2PPeerInfo{Families: []P2PFamilyInfo{{Family: "udp4", Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, FilteringTested: true, ClassificationLevel: ClassificationConfidenceMed}}}},
	}
	recordAdaptiveProfileSuccess(summary, common.CONN_KCP)
	before := adaptiveProfileScore(summary)
	recordAdaptiveProfileTimeout(summary)
	after := adaptiveProfileScore(summary)
	if after >= before {
		t.Fatalf("adaptiveProfileScore() should drop after timeout, before=%d after=%d", before, after)
	}
}

func TestSortRuntimeFamilyWorkersPrefersAdaptiveProfileScore(t *testing.T) {
	resetAdaptiveProfileHistoryForTest()
	t.Cleanup(resetAdaptiveProfileHistoryForTest)

	summary4 := P2PProbeSummary{
		Self: P2PPeerInfo{Families: []P2PFamilyInfo{{Family: "udp4", Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, FilteringTested: true, ClassificationLevel: ClassificationConfidenceMed}}}},
		Peer: P2PPeerInfo{Families: []P2PFamilyInfo{{Family: "udp4", Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, FilteringTested: true, ClassificationLevel: ClassificationConfidenceMed, ObservedBasePort: 6000}}}},
	}
	summary6 := P2PProbeSummary{
		Self: P2PPeerInfo{Families: []P2PFamilyInfo{{Family: "udp6", Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, FilteringTested: true, ClassificationLevel: ClassificationConfidenceMed}}}},
		Peer: P2PPeerInfo{Families: []P2PFamilyInfo{{Family: "udp6", Nat: NatObservation{NATType: NATTypeRestrictedCone, MappingBehavior: NATMappingEndpointIndependent, FilteringBehavior: NATFilteringOpen, FilteringTested: true, ClassificationLevel: ClassificationConfidenceMed, ObservedBasePort: 7000}}}},
	}
	recordAdaptiveProfileSuccess(summary6, common.CONN_KCP)
	recordAdaptiveProfileSuccess(summary6, common.CONN_KCP)

	workers := []*runtimeFamilyWorker{
		{family: udpFamilyV4, rs: &runtimeSession{}, summary: summary4},
		{family: udpFamilyV6, rs: &runtimeSession{}, summary: summary6},
	}
	sortRuntimeFamilyWorkers(workers)
	if workers[0].family != udpFamilyV6 {
		t.Fatalf("sortRuntimeFamilyWorkers() should prefer adaptive success family, got first=%s", workers[0].family.String())
	}
}
