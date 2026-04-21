package p2p

import (
	"sort"
	"strconv"

	"github.com/djylb/nps/lib/common"
)

func NormalizeProbeEndpoint(probe P2PProbeConfig, endpoint P2PProbeEndpoint) P2PProbeEndpoint {
	if endpoint.Provider == "" {
		endpoint.Provider = probe.Provider
	}
	if endpoint.Mode == "" {
		endpoint.Mode = probe.Mode
	}
	if endpoint.Network == "" {
		endpoint.Network = probe.Network
	}
	return endpoint
}

func NormalizeProbeEndpoints(probe P2PProbeConfig) []P2PProbeEndpoint {
	endpoints := make([]P2PProbeEndpoint, 0, len(probe.Endpoints))
	for _, endpoint := range probe.Endpoints {
		normalized := NormalizeProbeEndpoint(probe, endpoint)
		if normalized.Address == "" {
			continue
		}
		endpoints = append(endpoints, normalized)
	}
	return endpoints
}

func MergeProbeSamples(serverSamples, clientSamples []ProbeSample) []ProbeSample {
	out := make([]ProbeSample, 0, len(serverSamples)+len(clientSamples))
	indexByKey := make(map[string]int, len(serverSamples)+len(clientSamples))
	addOrMerge := func(sample ProbeSample, preferReply bool) {
		normalizeProbeSample(&sample)
		key := probeSampleMergeKey(sample)
		if idx, ok := indexByKey[key]; ok {
			out[idx] = mergeProbeSample(out[idx], sample, preferReply)
			return
		}
		indexByKey[key] = len(out)
		out = append(out, sample)
	}

	for _, sample := range clientSamples {
		addOrMerge(sample, true)
	}
	for _, sample := range serverSamples {
		normalizeProbeSample(&sample)
		if isWildcardNPSProbeSample(sample) {
			mergedAny := false
			for i := range out {
				if canMergeWildcardNPSProbeSample(sample, out[i]) {
					out[i] = mergeProbeSample(out[i], sample, false)
					mergedAny = true
				}
			}
			if mergedAny {
				continue
			}
		}
		addOrMerge(sample, false)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		if out[i].ProbePort != out[j].ProbePort {
			return out[i].ProbePort < out[j].ProbePort
		}
		if out[i].EndpointID != out[j].EndpointID {
			return out[i].EndpointID < out[j].EndpointID
		}
		if out[i].ObservedAddr != out[j].ObservedAddr {
			return out[i].ObservedAddr < out[j].ObservedAddr
		}
		return out[i].ServerReplyAddr < out[j].ServerReplyAddr
	})
	return out
}

func probeSampleMergeKey(sample ProbeSample) string {
	if sample.Provider == ProbeProviderNPS && sample.ProbePort > 0 {
		if replyIP := probeSampleReplyIP(sample.ServerReplyAddr); replyIP != "" {
			return sample.Provider + "|" + sample.Mode + "|" + strconv.Itoa(sample.ProbePort) + "|" + replyIP
		}
		return sample.Provider + "|" + sample.Mode + "|" + strconv.Itoa(sample.ProbePort)
	}
	if sample.EndpointID != "" {
		return sample.Provider + "|" + sample.Mode + "|" + sample.EndpointID
	}
	return sample.Provider + "|" + sample.Mode + "|" + strconv.Itoa(sample.ProbePort) + "|" + sample.ObservedAddr + "|" + sample.ServerReplyAddr
}

func mergeProbeSample(existing, sample ProbeSample, preferReply bool) ProbeSample {
	if existing.EndpointID == "" {
		existing.EndpointID = sample.EndpointID
	}
	if existing.Provider == "" {
		existing.Provider = sample.Provider
	}
	if existing.Mode == "" {
		existing.Mode = sample.Mode
	}
	if existing.ProbePort == 0 {
		existing.ProbePort = sample.ProbePort
	}
	if existing.ObservedAddr == "" {
		existing.ObservedAddr = sample.ObservedAddr
	}
	if preferReply {
		if sample.ServerReplyAddr != "" {
			existing.ServerReplyAddr = sample.ServerReplyAddr
		}
	} else if existing.ServerReplyAddr == "" {
		existing.ServerReplyAddr = sample.ServerReplyAddr
	}
	existing.ExtraReply = existing.ExtraReply || sample.ExtraReply
	return existing
}

func isWildcardNPSProbeSample(sample ProbeSample) bool {
	return sample.Provider == ProbeProviderNPS && sample.ProbePort > 0 && probeSampleReplyIP(sample.ServerReplyAddr) == ""
}

func canMergeWildcardNPSProbeSample(wildcard, existing ProbeSample) bool {
	return wildcard.Provider == ProbeProviderNPS &&
		existing.Provider == ProbeProviderNPS &&
		wildcard.Mode == existing.Mode &&
		wildcard.ProbePort == existing.ProbePort &&
		wildcard.ProbePort > 0
}

func probeSampleReplyIP(addr string) string {
	ip := common.NormalizeIP(common.ParseIPFromAddr(addr))
	if ip == nil || common.IsZeroIP(ip) || ip.IsUnspecified() {
		return ""
	}
	return ip.String()
}

func normalizeProbeSample(sample *ProbeSample) {
	if sample == nil {
		return
	}
	if sample.Provider == "" {
		sample.Provider = ProbeProviderNPS
	}
	if sample.Mode == "" {
		sample.Mode = ProbeModeUDP
	}
}
