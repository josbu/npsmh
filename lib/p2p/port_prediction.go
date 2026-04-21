package p2p

import (
	"sort"

	"github.com/djylb/nps/lib/common"
)

func BuildTargetSprayPortsMulti(basePort int, intervals []int, span int) []int {
	if span <= 0 || basePort <= 0 {
		return nil
	}
	if len(intervals) == 0 {
		intervals = []int{DefaultPredictionInterval}
	}
	ports := make([]int, 0, span)
	seen := make(map[int]struct{}, span)
	appendPort := func(port int) {
		if len(ports) >= span {
			return
		}
		if port < 1 || port > 65535 {
			return
		}
		if _, ok := seen[port]; ok {
			return
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	appendPort(basePort)
	for step := 1; len(ports) < span/2; step++ {
		for _, interval := range intervals {
			if interval <= 0 {
				continue
			}
			appendPort(basePort + step*interval)
			appendPort(basePort - step*interval)
			if len(ports) >= span/2 {
				break
			}
		}
	}
	for delta := 1; len(ports) < span; delta++ {
		appendPort(basePort - delta)
		appendPort(basePort + delta)
	}
	return ports
}

func BuildPredictedPorts(obs NatObservation, intervals []int, span int) []int {
	stages := buildPredictedPortStages(obs, intervals, span)
	if len(stages) == 0 {
		return nil
	}
	ports := make([]int, 0, span)
	for _, stage := range stages {
		ports = append(ports, stage.ports...)
	}
	return ports
}

type predictedPortStage struct {
	name  string
	ports []int
}

func buildPredictedPortStages(obs NatObservation, intervals []int, span int) []predictedPortStage {
	if span <= 0 || obs.PublicIP == "" || obs.ObservedBasePort <= 0 {
		return nil
	}
	candidates := []predictedPortStage{
		{name: "target_likely_next", ports: buildLikelyNextPredictionPorts(obs, intervals, minInt(span, 4))},
		{name: "target_history_exact", ports: buildHistoricalPredictionPorts(obs, span)},
		{name: "target_history_neighbor", ports: buildHistoricalPredictionNeighborPorts(obs, intervals, span)},
		{name: "target_interval_sweep", ports: BuildTargetSprayPortsMulti(obs.ObservedBasePort, intervals, span)},
	}
	remaining := span
	seen := make(map[int]struct{}, span)
	out := make([]predictedPortStage, 0, len(candidates))
	for _, candidate := range candidates {
		if remaining <= 0 {
			break
		}
		filtered := make([]int, 0, len(candidate.ports))
		for _, port := range candidate.ports {
			if remaining <= 0 {
				break
			}
			if port < 1 || port > 65535 {
				continue
			}
			if _, ok := seen[port]; ok {
				continue
			}
			seen[port] = struct{}{}
			filtered = append(filtered, port)
			remaining--
		}
		if len(filtered) == 0 {
			continue
		}
		candidate.ports = filtered
		out = append(out, candidate)
	}
	return out
}

func buildLikelyNextPredictionPorts(obs NatObservation, intervals []int, span int) []int {
	if span <= 0 {
		return nil
	}
	observed := observedPorts(obs)
	if len(observed) < 2 {
		return nil
	}
	anchor := obs.ObservedBasePort
	if len(observed) > 0 {
		anchor = observed[len(observed)-1]
	}
	steps := likelyNextPredictionSteps(obs, intervals)
	out := make([]int, 0, span)
	seen := make(map[int]struct{}, span)
	add := func(port int) {
		if len(out) >= span {
			return
		}
		if port < 1 || port > 65535 {
			return
		}
		if _, ok := seen[port]; ok {
			return
		}
		seen[port] = struct{}{}
		out = append(out, port)
	}
	for _, step := range steps {
		add(anchor + step)
		if step > 1 {
			add(anchor + 1)
		}
		if len(observed) >= 2 {
			add(anchor + 2*step)
		}
	}
	return out
}

func buildHistoricalPredictionPorts(obs NatObservation, span int) []int {
	if span <= 0 || obs.ObservedBasePort <= 0 {
		return nil
	}
	offsets := predictionHistoryOffsets(obs)
	if len(offsets) == 0 {
		return nil
	}
	out := make([]int, 0, len(offsets))
	seen := make(map[int]struct{}, len(offsets))
	for _, offset := range offsets {
		port := obs.ObservedBasePort + offset
		if port < 1 || port > 65535 {
			continue
		}
		if _, ok := seen[port]; ok {
			continue
		}
		seen[port] = struct{}{}
		out = append(out, port)
		if len(out) >= span {
			break
		}
	}
	return out
}

func buildHistoricalPredictionNeighborPorts(obs NatObservation, intervals []int, span int) []int {
	if span <= 0 || obs.ObservedBasePort <= 0 {
		return nil
	}
	offsets := predictionHistoryOffsets(obs)
	if len(offsets) == 0 {
		return nil
	}
	steps := predictionNeighborSteps(intervals)
	anchors := offsets
	if len(anchors) > 3 {
		anchors = anchors[:3]
	}
	out := make([]int, 0, span)
	seen := make(map[int]struct{}, span)
	add := func(port int) {
		if len(out) >= span {
			return
		}
		if port < 1 || port > 65535 {
			return
		}
		if _, ok := seen[port]; ok {
			return
		}
		seen[port] = struct{}{}
		out = append(out, port)
	}
	for _, offset := range anchors {
		base := obs.ObservedBasePort + offset
		for _, step := range steps {
			add(base - step)
			add(base + step)
			if len(out) >= span {
				return out
			}
		}
	}
	return out
}

func observedPorts(obs NatObservation) []int {
	if len(obs.Samples) == 0 {
		if obs.ObservedBasePort > 0 {
			return []int{obs.ObservedBasePort}
		}
		return nil
	}
	out := make([]int, 0, len(obs.Samples))
	seen := make(map[int]struct{}, len(obs.Samples))
	for _, sample := range obs.Samples {
		port := common.GetPortByAddr(sample.ObservedAddr)
		if port <= 0 {
			continue
		}
		if _, ok := seen[port]; ok {
			continue
		}
		seen[port] = struct{}{}
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func likelyNextPredictionSteps(obs NatObservation, intervals []int) []int {
	steps := make([]int, 0, 4)
	seen := make(map[int]struct{}, 4)
	add := func(step int) {
		if step <= 0 || step > 64 {
			return
		}
		if _, ok := seen[step]; ok {
			return
		}
		seen[step] = struct{}{}
		steps = append(steps, step)
	}
	add(obs.ObservedInterval)
	diffs := samplePortDiffs(obs.Samples)
	add(dominantPositive(diffs))
	add(medianPositive(diffs))
	for _, interval := range intervals {
		add(interval)
		if len(steps) >= 4 {
			break
		}
	}
	add(1)
	return steps
}

func predictionNeighborSteps(intervals []int) []int {
	steps := make([]int, 0, 4)
	seen := make(map[int]struct{}, 4)
	add := func(step int) {
		if step <= 0 || step > 64 {
			return
		}
		if _, ok := seen[step]; ok {
			return
		}
		seen[step] = struct{}{}
		steps = append(steps, step)
	}
	add(1)
	for _, interval := range intervals {
		add(interval)
		if len(steps) >= 4 {
			break
		}
	}
	return steps
}

func BuildTargetSprayPorts(basePort, interval, span int) []int {
	if span <= 0 {
		return nil
	}
	if basePort <= 0 {
		return nil
	}
	if interval <= 0 {
		interval = DefaultPredictionInterval
	}
	ports := make([]int, 0, span)
	seen := make(map[int]struct{}, span)
	appendPort := func(port int) {
		if len(ports) >= span {
			return
		}
		if port < 1 || port > 65535 {
			return
		}
		if _, ok := seen[port]; ok {
			return
		}
		seen[port] = struct{}{}
		ports = append(ports, port)
	}
	appendPort(basePort)
	for delta := 1; len(ports) < span/2; delta++ {
		appendPort(basePort + delta*interval)
		appendPort(basePort - delta*interval)
	}
	for delta := 1; len(ports) < span; delta++ {
		appendPort(basePort - delta)
		appendPort(basePort + delta)
	}
	return ports
}

func computeObservedInterval(ports []int) int {
	if len(ports) < 2 {
		return 0
	}
	sorted := append([]int(nil), ports...)
	sort.Ints(sorted)
	diffs := make([]int, 0, len(sorted)-1)
	for i := 1; i < len(sorted); i++ {
		diff := sorted[i] - sorted[i-1]
		if diff < 0 {
			diff = -diff
		}
		if diff > 0 {
			diffs = append(diffs, diff)
		}
	}
	if len(diffs) == 0 {
		return 0
	}
	if dominant := dominantPositive(diffs); dominant > 0 {
		return dominant
	}
	if gcd := gcdPositive(diffs); gcd > 1 {
		return gcd
	}
	return minPositive(diffs)
}

func samplePortDiffs(samples []ProbeSample) []int {
	if len(samples) < 2 {
		return nil
	}
	ports := make([]int, 0, len(samples))
	for _, sample := range samples {
		if port := common.GetPortByAddr(sample.ObservedAddr); port > 0 {
			ports = append(ports, port)
		}
	}
	if len(ports) < 2 {
		return nil
	}
	sort.Ints(ports)
	diffs := make([]int, 0, len(ports)-1)
	for i := 1; i < len(ports); i++ {
		diff := ports[i] - ports[i-1]
		if diff < 0 {
			diff = -diff
		}
		if diff > 0 {
			diffs = append(diffs, diff)
		}
	}
	return diffs
}

func minPositive(values []int) int {
	minValue := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if minValue == 0 || value < minValue {
			minValue = value
		}
	}
	return minValue
}

func medianPositive(values []int) int {
	filtered := make([]int, 0, len(values))
	for _, value := range values {
		if value > 0 {
			filtered = append(filtered, value)
		}
	}
	if len(filtered) == 0 {
		return 0
	}
	sort.Ints(filtered)
	return filtered[len(filtered)/2]
}

func gcdPositive(values []int) int {
	gcd := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if gcd == 0 {
			gcd = value
			continue
		}
		gcd = gcdPair(gcd, value)
	}
	return gcd
}

func dominantPositive(values []int) int {
	counts := make(map[int]int, len(values))
	bestValue := 0
	bestCount := 0
	for _, value := range values {
		if value <= 0 {
			continue
		}
		counts[value]++
		if counts[value] > bestCount || (counts[value] == bestCount && (bestValue == 0 || value < bestValue)) {
			bestValue = value
			bestCount = counts[value]
		}
	}
	return bestValue
}

func gcdPair(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
