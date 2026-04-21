package p2p

import "time"

const (
	DefaultPredictionInterval = 1
	DefaultSpraySpan          = 128
	DefaultSprayRounds        = 2
	DefaultSprayBurst         = 8
	DefaultBirthdayPorts      = 64
	DefaultBirthdayTargets    = 128
)

type PunchPlan struct {
	EnablePrediction        bool
	UseTargetSpray          bool
	UseBirthdayAttack       bool
	AllowBirthdayFallback   bool
	PredictionInterval      int
	PredictionIntervals     []int
	HandshakeTimeout        time.Duration
	NominationDelay         time.Duration
	NominationRetryInterval time.Duration
	SpraySpan               int
	SprayRounds             int
	SprayBurst              int
	SprayPerPacketSleep     time.Duration
	SprayBurstGap           time.Duration
	SprayPhaseGap           time.Duration
	BirthdayListenPorts     int
	BirthdayTargetsPerPort  int
}

func SelectPunchPlan(self, peer P2PPeerInfo, timeouts P2PTimeouts) PunchPlan {
	selfFilteringKnown := filteringEvidenceKnown(self.Nat)
	peerFilteringKnown := filteringEvidenceKnown(peer.Nat)
	selfNPSOnly := conservativeNPSOnlyEvidence(self.Nat)
	peerNPSOnly := conservativeNPSOnlyEvidence(peer.Nat)
	enablePrediction := self.Nat.ProbePortRestricted || peer.Nat.ProbePortRestricted ||
		!selfFilteringKnown || !peerFilteringKnown ||
		self.Nat.MappingConfidenceLow || peer.Nat.MappingConfidenceLow ||
		self.Nat.NATType == NATTypeSymmetric || peer.Nat.NATType == NATTypeSymmetric ||
		selfNPSOnly || peerNPSOnly
	allowBirthdayFallback := shouldAllowBirthdayFallback(self, peer)
	intervals := BuildPredictionIntervals(peer.Nat, enablePrediction, enablePrediction)
	interval := 0
	if len(intervals) > 0 {
		interval = intervals[0]
	}
	handshakeTimeout := time.Duration(timeouts.HandshakeTimeoutMs) * time.Millisecond
	if handshakeTimeout <= 0 {
		handshakeTimeout = 20 * time.Second
	}
	plan := PunchPlan{
		EnablePrediction:        enablePrediction,
		UseTargetSpray:          enablePrediction || len(peer.Nat.Samples) == 0,
		UseBirthdayAttack:       false,
		AllowBirthdayFallback:   allowBirthdayFallback,
		PredictionInterval:      interval,
		PredictionIntervals:     intervals,
		HandshakeTimeout:        handshakeTimeout,
		NominationDelay:         120 * time.Millisecond,
		NominationRetryInterval: 300 * time.Millisecond,
		SpraySpan:               DefaultSpraySpan,
		SprayRounds:             DefaultSprayRounds,
		SprayBurst:              DefaultSprayBurst,
		SprayPerPacketSleep:     5 * time.Millisecond,
		SprayBurstGap:           10 * time.Millisecond,
		SprayPhaseGap:           40 * time.Millisecond,
		BirthdayListenPorts:     DefaultBirthdayPorts,
		BirthdayTargetsPerPort:  DefaultBirthdayTargets,
	}
	return tunePunchPlan(plan, self, peer)
}

func ApplyProbePlanOptions(plan PunchPlan, probe P2PProbeConfig, self, peer P2PPeerInfo) PunchPlan {
	if policy := probe.Policy; policy != nil {
		if !policy.Traversal.ForcePredictOnRestricted {
			if !self.Nat.MappingConfidenceLow && !peer.Nat.MappingConfidenceLow && (self.Nat.ProbePortRestricted || peer.Nat.ProbePortRestricted) {
				plan.EnablePrediction = false
				plan.PredictionInterval = 0
				if len(peer.Nat.Samples) > 0 {
					plan.UseTargetSpray = false
				}
			}
		}
		plan.UseTargetSpray = policy.Traversal.EnableTargetSpray
		if value := policy.Traversal.DefaultPredictionInterval; value > 0 && plan.EnablePrediction && plan.PredictionInterval == DefaultPredictionInterval {
			plan.PredictionInterval = value
			if len(plan.PredictionIntervals) == 0 {
				plan.PredictionIntervals = []int{value}
			} else {
				plan.PredictionIntervals[0] = value
			}
		}
		if value := policy.Traversal.TargetSpraySpan; value > 0 {
			plan.SpraySpan = value
		}
		if value := policy.Traversal.TargetSprayRounds; value > 0 {
			plan.SprayRounds = value
		}
		if value := policy.Traversal.TargetSprayBurst; value > 0 {
			plan.SprayBurst = value
		}
		if value := policy.Traversal.TargetSprayPacketSleepMs; value >= 0 {
			plan.SprayPerPacketSleep = time.Duration(value) * time.Millisecond
		}
		if value := policy.Traversal.TargetSprayBurstGapMs; value >= 0 {
			plan.SprayBurstGap = time.Duration(value) * time.Millisecond
		}
		if value := policy.Traversal.TargetSprayPhaseGapMs; value >= 0 {
			plan.SprayPhaseGap = time.Duration(value) * time.Millisecond
		}
		if value := policy.Traversal.BirthdayListenPorts; value > 0 {
			plan.BirthdayListenPorts = value
		}
		if value := policy.Traversal.BirthdayTargetsPerPort; value > 0 {
			plan.BirthdayTargetsPerPort = value
		}
		if value := policy.Traversal.NominationDelayMs; value > 0 {
			plan.NominationDelay = time.Duration(value) * time.Millisecond
		}
		if value := policy.Traversal.NominationRetryMs; value > 0 {
			plan.NominationRetryInterval = time.Duration(value) * time.Millisecond
		}
		if policy.Traversal.EnableBirthdayAttack {
			if shouldAllowBirthdayFallback(self, peer) {
				plan.AllowBirthdayFallback = true
				plan.UseBirthdayAttack = true
			}
		} else {
			plan.AllowBirthdayFallback = false
			plan.UseBirthdayAttack = false
		}
		return plan
	}
	return plan
}

func ApplySummaryHintsToPlan(plan PunchPlan, summary P2PProbeSummary) PunchPlan {
	sharedFamilies := 0
	if hints := summary.SummaryHints; hints != nil {
		sharedFamilies = hints.SharedFamilyCount
	}
	strongEvidence := hasStrongProbeEvidence(summary.Self.Nat) && hasStrongProbeEvidence(summary.Peer.Nat)
	profileScore := adaptiveProfileScore(summary)
	if sharedFamilies > 1 {
		if !isHardOrUnknownNat(summary.Self.Nat) && !isHardOrUnknownNat(summary.Peer.Nat) {
			if plan.SprayRounds > 1 {
				plan.SprayRounds--
			}
			if plan.SprayBurst > 4 {
				plan.SprayBurst--
			}
		}
		delay := plan.NominationDelay
		if delay < 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		delay += 20 * time.Millisecond
		if plan.HandshakeTimeout > 0 {
			maxDelay := plan.HandshakeTimeout / 5
			if maxDelay > 0 && delay > maxDelay {
				delay = maxDelay
			}
		}
		plan.NominationDelay = delay
	}
	if strongEvidence && !isHardOrUnknownNat(summary.Self.Nat) && !isHardOrUnknownNat(summary.Peer.Nat) {
		if plan.SprayRounds > 1 {
			plan.SprayRounds--
		}
		if plan.SprayBurst > 4 {
			plan.SprayBurst = maxInt(4, plan.SprayBurst-2)
		}
		if plan.NominationDelay > 70*time.Millisecond {
			plan.NominationDelay -= 20 * time.Millisecond
		}
		if plan.NominationRetryInterval > 220*time.Millisecond {
			plan.NominationRetryInterval = 220 * time.Millisecond
		}
	}
	switch {
	case profileScore >= 24:
		if plan.SprayRounds > 1 {
			plan.SprayRounds--
		}
		if plan.SprayBurst > 4 {
			plan.SprayBurst = maxInt(4, plan.SprayBurst-1)
		}
		if plan.NominationDelay > 70*time.Millisecond {
			plan.NominationDelay -= 15 * time.Millisecond
		}
		if plan.NominationRetryInterval > 200*time.Millisecond {
			plan.NominationRetryInterval = 200 * time.Millisecond
		}
	case profileScore <= -16 && sharedFamilies > 1:
		if plan.SprayBurst > 4 {
			plan.SprayBurst = maxInt(4, plan.SprayBurst-2)
		}
		if plan.SprayRounds > 1 {
			plan.SprayRounds--
		}
		plan.NominationDelay += 20 * time.Millisecond
	}
	return plan
}

func BuildPredictionIntervals(obs NatObservation, enablePrediction, aggressive bool) []int {
	if !enablePrediction {
		return nil
	}
	candidates := make([]int, 0, 8)
	seen := make(map[int]struct{}, 8)
	add := func(interval int) {
		if interval < 0 {
			interval = -interval
		}
		if interval <= 0 {
			return
		}
		if interval > 512 {
			return
		}
		if _, ok := seen[interval]; ok {
			return
		}
		seen[interval] = struct{}{}
		candidates = append(candidates, interval)
	}

	add(obs.ObservedInterval)
	diffs := samplePortDiffs(obs.Samples)
	add(minPositive(diffs))
	add(medianPositive(diffs))
	add(gcdPositive(diffs))
	if aggressive {
		base := append([]int(nil), candidates...)
		for _, interval := range base {
			add(interval - 1)
			add(interval + 1)
		}
	}
	add(DefaultPredictionInterval)
	if len(candidates) > 6 {
		candidates = candidates[:6]
	}
	return candidates
}

func shouldAllowBirthdayFallback(self, peer P2PPeerInfo) bool {
	if self.Nat.NATType == NATTypeSymmetric && peer.Nat.NATType == NATTypeSymmetric {
		return true
	}
	return isHardOrUnknownNat(self.Nat) && isHardOrUnknownNat(peer.Nat)
}

func tunePunchPlan(plan PunchPlan, self, peer P2PPeerInfo) PunchPlan {
	easyPair := !plan.EnablePrediction && !isHardOrUnknownNat(self.Nat) && !isHardOrUnknownNat(peer.Nat)
	hardPair := shouldAllowBirthdayFallback(self, peer)
	switch {
	case easyPair:
		plan.SprayRounds = 1
		plan.SprayBurst = 4
		plan.SprayPerPacketSleep = 5 * time.Millisecond
		plan.SprayBurstGap = 6 * time.Millisecond
		plan.SprayPhaseGap = 20 * time.Millisecond
		plan.NominationDelay = 80 * time.Millisecond
		plan.NominationRetryInterval = 180 * time.Millisecond
	case hardPair:
		plan.SprayRounds = 3
		plan.SprayBurst = 10
		plan.SprayPerPacketSleep = 5 * time.Millisecond
		plan.SprayBurstGap = 8 * time.Millisecond
		plan.SprayPhaseGap = 30 * time.Millisecond
		plan.NominationDelay = 140 * time.Millisecond
	}
	return plan
}

func isHardOrUnknownNat(obs NatObservation) bool {
	if obs.ProbePortRestricted || obs.MappingConfidenceLow || obs.NATType == NATTypeSymmetric {
		return true
	}
	if obs.NATType == NATTypeUnknown || obs.ClassificationLevel == ClassificationConfidenceLow {
		return true
	}
	if !filteringEvidenceKnown(obs) && hasProbeEvidence(obs) {
		return true
	}
	return false
}

func hasProbeEvidence(obs NatObservation) bool {
	return obs.PublicIP != "" || obs.ProbeIPCount > 0 || obs.ProbeEndpointCount > 0 || len(obs.Samples) > 0
}

func conservativeNPSOnlyEvidence(obs NatObservation) bool {
	if !hasProbeEvidence(obs) || natProbeProviderCount(obs) != 1 || !natHasProbeProvider(obs, ProbeProviderNPS) {
		return false
	}
	if obs.MappingConfidenceLow || obs.NATType == NATTypeUnknown || obs.ClassificationLevel == ClassificationConfidenceLow {
		return true
	}
	return !filteringEvidenceKnown(obs)
}

func filteringEvidenceKnown(obs NatObservation) bool {
	if obs.FilteringTested {
		return true
	}
	if obs.FilteringBehavior != "" && obs.FilteringBehavior != NATFilteringUnknown {
		return true
	}
	switch obs.NATType {
	case NATTypePortRestricted, NATTypeRestrictedCone, NATTypeCone:
		return true
	default:
		return false
	}
}

func natProbeProviderCount(obs NatObservation) int {
	if len(obs.Samples) == 0 {
		return 0
	}
	providers := make(map[string]struct{}, len(obs.Samples))
	for _, sample := range obs.Samples {
		provider := sample.Provider
		if provider == "" {
			provider = ProbeProviderNPS
		}
		providers[provider] = struct{}{}
	}
	return len(providers)
}

func natHasProbeProvider(obs NatObservation, provider string) bool {
	for _, sample := range obs.Samples {
		sampleProvider := sample.Provider
		if sampleProvider == "" {
			sampleProvider = ProbeProviderNPS
		}
		if sampleProvider == provider {
			return true
		}
	}
	return false
}

func hasStrongProbeEvidence(obs NatObservation) bool {
	if obs.MappingConfidenceLow || isHardOrUnknownNat(obs) {
		return false
	}
	if obs.ClassificationLevel == ClassificationConfidenceLow {
		return false
	}
	if !filteringEvidenceKnown(obs) {
		return false
	}
	return obs.ProbeEndpointCount > 1 || obs.ProbeIPCount > 1
}

func (p PunchPlan) predictionIntervals() []int {
	if len(p.PredictionIntervals) != 0 {
		return p.PredictionIntervals
	}
	if p.PredictionInterval > 0 {
		return []int{p.PredictionInterval}
	}
	return []int{DefaultPredictionInterval}
}

func (p PunchPlan) nominationDelay() time.Duration {
	delay := p.NominationDelay
	if delay <= 0 {
		return 0
	}
	if p.HandshakeTimeout > 0 {
		maxDelay := p.HandshakeTimeout / 6
		if maxDelay > 0 && delay > maxDelay {
			delay = maxDelay
		}
	}
	return delay
}

func (p PunchPlan) nominationRetryInterval() time.Duration {
	interval := p.NominationRetryInterval
	if interval <= 0 {
		interval = 300 * time.Millisecond
	}
	if p.HandshakeTimeout > 0 {
		maxInterval := p.HandshakeTimeout / 5
		if maxInterval > 0 && interval > maxInterval {
			interval = maxInterval
		}
	}
	if interval <= 0 {
		return 50 * time.Millisecond
	}
	return interval
}

func (p PunchPlan) renominationTimeout() time.Duration {
	timeout := p.nominationRetryInterval() * 3
	if timeout < 450*time.Millisecond {
		timeout = 450 * time.Millisecond
	}
	if p.HandshakeTimeout > 0 {
		maxTimeout := p.HandshakeTimeout / 4
		if maxTimeout > 0 && timeout > maxTimeout {
			timeout = maxTimeout
		}
	}
	return timeout
}

func (p PunchPlan) renominationCooldown() time.Duration {
	cooldown := p.nominationRetryInterval() * 2
	if cooldown < 250*time.Millisecond {
		cooldown = 250 * time.Millisecond
	}
	if p.HandshakeTimeout > 0 {
		maxCooldown := p.HandshakeTimeout / 5
		if maxCooldown > 0 && cooldown > maxCooldown {
			cooldown = maxCooldown
		}
	}
	return cooldown
}
