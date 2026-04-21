package p2p

import (
	"sort"
	"strconv"

	"github.com/djylb/nps/lib/common"
)

func BuildNatObservation(samples []ProbeSample, extraReplySeen bool) NatObservation {
	return BuildNatObservationWithEvidence(samples, extraReplySeen, true)
}

func BuildNatObservationWithEvidence(samples []ProbeSample, extraReplySeen, filteringTested bool) NatObservation {
	obs := NatObservation{
		ProbePortRestricted: filteringTested && !extraReplySeen,
		FilteringTested:     filteringTested,
		MappingBehavior:     NATMappingUnknown,
		FilteringBehavior:   NATFilteringUnknown,
		NATType:             NATTypeUnknown,
		ClassificationLevel: ClassificationConfidenceLow,
		Samples:             append([]ProbeSample(nil), samples...),
	}
	if len(samples) == 0 {
		obs.MappingConfidenceLow = true
		return obs
	}
	sorted := append([]ProbeSample(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ProbePort < sorted[j].ProbePort })
	first := sorted[0]
	obs.PublicIP = common.GetIpByAddr(first.ObservedAddr)
	ports := make([]int, 0, len(sorted))
	probeIPs := make(map[string]struct{}, len(sorted))
	probeEndpoints := make(map[string]struct{}, len(sorted))
	publicIPs := make(map[string]struct{}, len(sorted))
	uniquePorts := make(map[int]struct{}, len(sorted))
	var hasNPS bool
	for _, sample := range sorted {
		if publicIP := common.GetIpByAddr(sample.ObservedAddr); publicIP != "" {
			publicIPs[publicIP] = struct{}{}
		}
		if replyIP := probeSampleReplyIP(sample.ServerReplyAddr); replyIP != "" {
			probeIPs[replyIP] = struct{}{}
		}
		if endpointKey := probeObservationEndpointKey(sample); endpointKey != "" {
			probeEndpoints[endpointKey] = struct{}{}
		}
		if sample.Provider == ProbeProviderNPS || sample.Provider == "" {
			hasNPS = true
		}
		if p := common.GetPortByAddr(sample.ObservedAddr); p > 0 {
			ports = append(ports, p)
			uniquePorts[p] = struct{}{}
		}
	}
	obs.ObservedBasePort = minPositive(ports)
	obs.ProbeIPCount = len(probeIPs)
	if obs.ProbeIPCount == 0 {
		obs.ProbeIPCount = 1
	}
	obs.ProbeEndpointCount = len(probeEndpoints)
	if obs.ProbeEndpointCount < obs.ProbeIPCount {
		obs.ProbeEndpointCount = obs.ProbeIPCount
	}
	if obs.ProbeEndpointCount == 0 {
		obs.ProbeEndpointCount = 1
	}
	obs.ObservedInterval = computeObservedInterval(ports)
	obs.ConflictingSignals = len(publicIPs) > 1
	mappingEvidenceStrong := obs.ProbeEndpointCount > 1
	crossIPEvidence := obs.ProbeIPCount > 1
	switch {
	case mappingEvidenceStrong && len(uniquePorts) == 1:
		obs.MappingBehavior = NATMappingEndpointIndependent
	case mappingEvidenceStrong && len(uniquePorts) > 1:
		obs.MappingBehavior = NATMappingEndpointDependent
	}
	if hasNPS && filteringTested {
		if obs.ProbePortRestricted {
			obs.FilteringBehavior = NATFilteringPortRestricted
		} else {
			obs.FilteringBehavior = NATFilteringOpen
		}
	}
	switch {
	case obs.MappingBehavior == NATMappingEndpointDependent:
		obs.NATType = NATTypeSymmetric
		obs.ClassificationLevel = ClassificationConfidenceMed
	case obs.MappingBehavior == NATMappingEndpointIndependent && obs.FilteringBehavior == NATFilteringPortRestricted:
		obs.NATType = NATTypePortRestricted
		obs.ClassificationLevel = ClassificationConfidenceMed
	case obs.MappingBehavior == NATMappingEndpointIndependent && obs.FilteringBehavior == NATFilteringOpen:
		obs.NATType = NATTypeRestrictedCone
		obs.ClassificationLevel = ClassificationConfidenceMed
	}
	obs.MappingConfidenceLow = len(ports) < 3 ||
		!mappingEvidenceStrong ||
		obs.ConflictingSignals ||
		(obs.MappingBehavior == NATMappingEndpointIndependent && !crossIPEvidence) ||
		(obs.ObservedInterval == 0 && obs.MappingBehavior == NATMappingUnknown)
	if !obs.ConflictingSignals {
		switch obs.NATType {
		case NATTypeSymmetric:
			if obs.ProbeEndpointCount > 1 && len(uniquePorts) > 1 {
				obs.MappingConfidenceLow = false
			}
		case NATTypePortRestricted, NATTypeRestrictedCone, NATTypeCone:
			// A stable mapping observed only from one probe IP is not enough to prove
			// endpoint-independent mapping. Keep it conservative unless the evidence
			// spans multiple probe IPs.
			if obs.MappingBehavior == NATMappingEndpointIndependent && obs.ProbeEndpointCount > 1 && crossIPEvidence {
				obs.MappingConfidenceLow = false
			}
		}
	}
	if obs.MappingConfidenceLow {
		obs.ClassificationLevel = ClassificationConfidenceLow
		if obs.NATType == NATTypeSymmetric && mappingEvidenceStrong {
			obs.ClassificationLevel = ClassificationConfidenceMed
		}
	} else if crossIPEvidence {
		obs.ClassificationLevel = ClassificationConfidenceHigh
	}
	return obs
}

func probeObservationEndpointKey(sample ProbeSample) string {
	normalizeProbeSample(&sample)
	switch {
	case sample.ServerReplyAddr != "":
		return sample.Provider + "|" + sample.Mode + "|" + common.ValidateAddr(sample.ServerReplyAddr)
	case sample.EndpointID != "":
		return sample.Provider + "|" + sample.Mode + "|" + sample.EndpointID
	case sample.ProbePort > 0:
		return sample.Provider + "|" + sample.Mode + "|" + strconv.Itoa(sample.ProbePort)
	default:
		return ""
	}
}
