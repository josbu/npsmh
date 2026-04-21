package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/logs"
)

type probeRuntimeFamily struct {
	family      udpAddrFamily
	conn        net.PacketConn
	observation NatObservation
	localAddrs  []string
}

type runtimeFamilyWorker struct {
	family  udpAddrFamily
	rs      *runtimeSession
	cancel  context.CancelFunc
	summary P2PProbeSummary
}

func openProbeRuntimeFamilies(ctx context.Context, preferredLocalAddr string, start P2PPunchStart) ([]probeRuntimeFamily, error) {
	localAddrs, err := ChooseLocalProbeAddrsContext(ctx, preferredLocalAddr, start.Probe)
	if err != nil {
		return nil, err
	}
	type probeResult struct {
		index  int
		family probeRuntimeFamily
		err    string
	}
	results := make(chan probeResult, len(localAddrs))
	var wg sync.WaitGroup
	for index, bindAddr := range localAddrs {
		wg.Add(1)
		go func(index int, bindAddr string) {
			defer wg.Done()
			localConn, err := conn.NewUdpConnByAddr(bindAddr)
			if err != nil {
				results <- probeResult{index: index, err: err.Error()}
				return
			}
			observation, err := runProbe(ctx, localConn, start)
			if err != nil {
				_ = localConn.Close()
				results <- probeResult{index: index, err: err.Error()}
				return
			}
			portMappingConn := net.PacketConn(localConn)
			if detectAddrFamily(localConn.LocalAddr()) == udpFamilyV4 {
				var portMapping *PortMappingInfo
				portMappingConn, portMapping = maybeEnablePortMapping(ctx, localConn, start.Probe, observation)
				if portMapping != nil {
					observation.PortMapping = portMapping
				}
			}
			results <- probeResult{
				index: index,
				family: probeRuntimeFamily{
					family:      detectAddrFamily(portMappingConn.LocalAddr()),
					conn:        portMappingConn,
					observation: observation,
					localAddrs:  buildLocalAddrs(portMappingConn.LocalAddr()),
				},
			}
		}(index, bindAddr)
	}
	wg.Wait()
	close(results)

	ordered := make([]probeResult, 0, len(localAddrs))
	for result := range results {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].index < ordered[j].index })

	families := make([]probeRuntimeFamily, 0, len(localAddrs))
	errs := make([]string, 0, len(localAddrs))
	for _, result := range ordered {
		if result.err != "" {
			errs = append(errs, result.err)
			continue
		}
		families = append(families, result.family)
	}
	if len(families) == 0 {
		if len(errs) == 0 {
			return nil, fmt.Errorf("no usable local probe family")
		}
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return families, nil
}

func buildRuntimeFamilyWorkers(start P2PPunchStart, summary P2PProbeSummary, control *conn.Conn, families []probeRuntimeFamily) ([]*runtimeFamilyWorker, error) {
	if len(families) == 0 {
		return nil, fmt.Errorf("empty local probe families")
	}
	confirmed := make(chan confirmedPair, 1)
	workers := make([]*runtimeFamilyWorker, 0, len(families))
	for _, family := range families {
		selfInfo, ok := PeerInfoForFamily(summary.Self, family.family)
		if !ok {
			continue
		}
		peerInfo, ok := PeerInfoForFamily(summary.Peer, family.family)
		if !ok {
			continue
		}
		familySummary := summary
		familySummary.Self = selfInfo
		familySummary.Peer = peerInfo
		plan := ApplyProbePlanOptions(SelectPunchPlan(familySummary.Self, familySummary.Peer, familySummary.Timeouts), start.Probe, familySummary.Self, familySummary.Peer)
		plan = ApplySummaryHintsToPlan(plan, familySummary)
		logs.Info("[P2P] selected PunchPlan role=%s family=%s mappingConfidenceLow=%v probePortRestricted=%v prediction=%v targetSpray=%v birthday=%v birthdayFallback=%v intervals=%v timeout=%s",
			start.Role, family.family.String(), familySummary.Self.Nat.MappingConfidenceLow, familySummary.Self.Nat.ProbePortRestricted,
			plan.EnablePrediction, plan.UseTargetSpray, plan.UseBirthdayAttack, plan.AllowBirthdayFallback, plan.predictionIntervals(), plan.HandshakeTimeout)
		workers = append(workers, &runtimeFamilyWorker{
			family:  family.family,
			rs:      newRuntimeSession(start, familySummary, plan, control, family.conn, confirmed),
			summary: familySummary,
		})
	}
	if len(workers) == 0 {
		return nil, fmt.Errorf("no compatible punch family between peers")
	}
	sortRuntimeFamilyWorkers(workers)
	for _, worker := range workers {
		profileScore := adaptiveProfileScore(worker.summary)
		meta := map[string]string{
			"family":                   worker.family.String(),
			"probe_public_ip":          worker.summary.Self.Nat.PublicIP,
			"nat_type":                 worker.summary.Self.Nat.NATType,
			"mapping_behavior":         worker.summary.Self.Nat.MappingBehavior,
			"filtering_behavior":       worker.summary.Self.Nat.FilteringBehavior,
			"classification_level":     worker.summary.Self.Nat.ClassificationLevel,
			"prediction":               strconv.FormatBool(worker.rs.plan.EnablePrediction),
			"target_spray":             strconv.FormatBool(worker.rs.plan.UseTargetSpray),
			"birthday":                 strconv.FormatBool(worker.rs.plan.UseBirthdayAttack),
			"birthday_fallback":        strconv.FormatBool(worker.rs.plan.AllowBirthdayFallback),
			"handshake_timeout_ms":     strconv.FormatInt(worker.rs.plan.HandshakeTimeout.Milliseconds(), 10),
			"transport_timeout_ms":     strconv.Itoa(worker.summary.Timeouts.TransportTimeoutMs),
			"probe_endpoint_count":     strconv.Itoa(worker.summary.Self.Nat.ProbeEndpointCount),
			"probe_provider_count":     strconv.Itoa(probeSampleProviderCount(worker.summary.Self.Nat.Samples)),
			"probe_observed_base_port": strconv.Itoa(worker.summary.Self.Nat.ObservedBasePort),
			"probe_observed_interval":  strconv.Itoa(worker.summary.Self.Nat.ObservedInterval),
			"probe_port_restricted":    strconv.FormatBool(worker.summary.Self.Nat.ProbePortRestricted),
			"probe_filtering_tested":   strconv.FormatBool(worker.summary.Self.Nat.FilteringTested),
			"mapping_confidence_low":   strconv.FormatBool(worker.summary.Self.Nat.MappingConfidenceLow),
			"adaptive_profile_score":   strconv.Itoa(profileScore),
		}
		if portMapping := worker.summary.Self.Nat.PortMapping; portMapping != nil {
			meta["port_mapping_method"] = portMapping.Method
			meta["port_mapping_external_addr"] = portMapping.ExternalAddr
			meta["port_mapping_internal_addr"] = portMapping.InternalAddr
			meta["port_mapping_lease_seconds"] = strconv.Itoa(portMapping.LeaseSeconds)
		}
		_ = worker.rs.sendProgress("family_ready", "ok", "", addrString(worker.rs.localConn.LocalAddr()), firstObservedAddr(worker.summary.Peer), meta)
	}
	return workers, nil
}

func sortRuntimeFamilyWorkers(workers []*runtimeFamilyWorker) {
	sort.SliceStable(workers, func(i, j int) bool {
		left := workers[i]
		right := workers[j]
		if left == nil || left.rs == nil {
			return false
		}
		if right == nil || right.rs == nil {
			return true
		}
		leftScore := adaptiveProfileScore(left.summary)
		rightScore := adaptiveProfileScore(right.summary)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if left.summary.Self.Nat.MappingConfidenceLow != right.summary.Self.Nat.MappingConfidenceLow {
			return !left.summary.Self.Nat.MappingConfidenceLow
		}
		return left.family < right.family
	})
}

func probeSampleProviderCount(samples []ProbeSample) int {
	if len(samples) == 0 {
		return 0
	}
	providers := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		provider := sample.Provider
		if provider == "" {
			provider = ProbeProviderNPS
		}
		providers[provider] = struct{}{}
	}
	return len(providers)
}

func runProbe(ctx context.Context, localConn net.PacketConn, start P2PPunchStart) (NatObservation, error) {
	inventory := newProbeEndpointInventory(start.Probe)
	endpoints := inventory.all()
	if len(endpoints) == 0 {
		return NatObservation{}, fmt.Errorf("empty probe endpoints")
	}
	npsEndpoints := inventory.npsUDP()
	if len(npsEndpoints) == 0 {
		return NatObservation{}, fmt.Errorf("unsupported probe configuration provider=%s mode=%s network=%s", start.Probe.Provider, start.Probe.Mode, start.Probe.Network)
	}

	samples := make([]ProbeSample, 0, len(endpoints))
	var sawExtraReply bool
	var npsSucceeded bool
	if len(npsEndpoints) > 0 {
		npsSamples, extraReplySeen, err := runNPSProbe(ctx, localConn, start, npsEndpoints)
		if err != nil {
			logs.Trace("[P2P] nps probe unavailable err=%v", err)
		} else {
			samples = append(samples, npsSamples...)
			sawExtraReply = extraReplySeen
			npsSucceeded = len(npsSamples) > 0
		}
	}
	if len(samples) == 0 {
		return NatObservation{}, fmt.Errorf("no successful probe endpoints for %s", addrString(localConn.LocalAddr()))
	}
	filteringTested := start.Probe.ExpectExtraReply && npsSucceeded
	return BuildNatObservationWithEvidence(samples, sawExtraReply, filteringTested), nil
}

func runNPSProbe(ctx context.Context, localConn net.PacketConn, start P2PPunchStart, endpoints []P2PProbeEndpoint) ([]ProbeSample, bool, error) {
	inventory := probeEndpointInventory{endpoints: endpoints}
	resolvedEndpoints, expectedKeys, err := inventory.resolveNPS(ctx, localConn.LocalAddr(), probeResolveTimeout(start.Probe, 250*time.Millisecond))
	if err != nil {
		return nil, false, err
	}
	restoreReadDeadline := interruptPacketReadOnContext(ctx, localConn)
	if restoreReadDeadline != nil {
		defer restoreReadDeadline()
	}
	samples := make(map[string]ProbeSample, len(expectedKeys))
	var extraReplySeen bool
	buf := common.BufPoolUdp.Get()
	defer common.BufPoolUdp.Put(buf)

	sendProbe := func() {
		for _, endpoint := range resolvedEndpoints {
			packet := newUDPPacketWithWire(start.SessionID, start.Token, start.Role, packetTypeProbe, p2pStartWireSpec(start))
			packet.ProbePort = common.GetPortByAddr(endpoint.endpoint.Address)
			raw, err := EncodeUDPPacket(packet)
			if err != nil {
				continue
			}
			_, _ = localConn.WriteTo(raw, endpoint.addr)
		}
	}

	sendProbe()
	deadline := time.Now().Add(time.Duration(start.Timeouts.ProbeTimeoutMs) * time.Millisecond)
	if start.Timeouts.ProbeTimeoutMs <= 0 {
		deadline = time.Now().Add(5 * time.Second)
	}
	for time.Now().Before(deadline) {
		if len(samples) >= len(expectedKeys) && (!start.Probe.ExpectExtraReply || extraReplySeen) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, false, mapP2PContextError(ctx.Err())
		default:
		}
		_ = localConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, replyAddr, err := localConn.ReadFrom(buf)
		_ = localConn.SetReadDeadline(time.Time{})
		if err != nil {
			if conn.IsTimeout(err) {
				sendProbe()
				continue
			}
			if isIgnorableUDPIcmpError(err) {
				continue
			}
			return nil, false, err
		}
		packet, err := DecodeUDPPacket(buf[:n], start.Token)
		if err != nil {
			continue
		}
		if packet.SessionID != start.SessionID || packet.Token != start.Token || !SameWireRoute(packet.WireID, p2pStartWireSpec(start).RouteID) {
			continue
		}
		if packet.Type != packetTypeAck && packet.Type != packetTypeProbeX {
			continue
		}
		sample := ProbeSample{
			Provider:        ProbeProviderNPS,
			Mode:            ProbeModeUDP,
			ProbePort:       packet.ProbePort,
			ObservedAddr:    packet.ObservedAddr,
			ServerReplyAddr: replyAddr.String(),
			ExtraReply:      packet.ExtraReply || packet.Type == packetTypeProbeX,
		}
		if sample.ExtraReply {
			extraReplySeen = true
		}
		if key := npsProbeSampleKey(sample); key != "" && sample.ObservedAddr != "" {
			if existing, ok := samples[key]; ok {
				if existing.ObservedAddr == "" {
					existing.ObservedAddr = sample.ObservedAddr
				}
				if existing.ServerReplyAddr == "" || !sample.ExtraReply {
					existing.ServerReplyAddr = sample.ServerReplyAddr
				}
				existing.ExtraReply = existing.ExtraReply || sample.ExtraReply
				samples[key] = existing
			} else {
				samples[key] = sample
			}
		}
	}
	out := make([]ProbeSample, 0, len(samples))
	for _, sample := range samples {
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProbePort != out[j].ProbePort {
			return out[i].ProbePort < out[j].ProbePort
		}
		return out[i].ServerReplyAddr < out[j].ServerReplyAddr
	})
	return out, extraReplySeen, nil
}

func npsProbeSampleKey(sample ProbeSample) string {
	return npsProbeKey(sample.ProbePort, sample.ServerReplyAddr)
}

func npsProbeKey(probePort int, addr string) string {
	replyIP := probeSampleReplyIP(addr)
	if probePort <= 0 || replyIP == "" {
		return ""
	}
	return fmt.Sprintf("%d|%s", probePort, replyIP)
}

func firstObservedAddr(peer P2PPeerInfo) string {
	for _, sample := range peer.Nat.Samples {
		if addr := common.ValidateAddr(sample.ObservedAddr); addr != "" {
			return addr
		}
	}
	if peer.Nat.PublicIP != "" && peer.Nat.ObservedBasePort > 0 {
		return net.JoinHostPort(peer.Nat.PublicIP, fmt.Sprintf("%d", peer.Nat.ObservedBasePort))
	}
	return ""
}

func ChooseLocalProbeAddr(probe P2PProbeConfig) (string, error) {
	return ChooseLocalProbeAddrContext(context.Background(), probe)
}

func ChooseLocalProbeAddrContext(ctx context.Context, probe P2PProbeConfig) (string, error) {
	addrs, err := ChooseLocalProbeAddrsContext(ctx, "", probe)
	if err != nil {
		return "", err
	}
	return addrs[0], nil
}

func ChooseLocalProbeAddrs(preferredAddr string, probe P2PProbeConfig) ([]string, error) {
	return ChooseLocalProbeAddrsContext(context.Background(), preferredAddr, probe)
}

func ChooseLocalProbeAddrsContext(ctx context.Context, preferredAddr string, probe P2PProbeConfig) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inventory := newProbeEndpointInventory(probe)
	type localCandidate struct {
		family udpAddrFamily
		addr   string
	}
	familyCandidates := make(map[udpAddrFamily]string, 2)
	setCandidate := func(addr string) {
		addr = normalizeLocalProbeCandidate(addr)
		if addr == "" {
			return
		}
		family := detectAddressFamily(addr)
		if family == udpFamilyAny {
			return
		}
		familyCandidates[family] = addr
	}
	if addr, err := common.GetLocalUdp4Addr(); err == nil {
		setCandidate(addr.String())
	}
	if addr, err := common.GetLocalUdp6Addr(); err == nil {
		setCandidate(addr.String())
	}
	if len(familyCandidates) == 0 {
		return nil, fmt.Errorf("no local udp probe address available")
	}

	resolveTimeout := probeResolveTimeout(probe, 250*time.Millisecond)
	resolveCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	scores := inventory.familyScores(resolveCtx, resolveTimeout)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if bindAddr, family, ok := preferredProbeBindAddr(preferredAddr); ok && scores[family] > 0 {
		familyCandidates[family] = bindAddr
	}

	candidates := make([]localCandidate, 0, len(familyCandidates))
	for family, addr := range familyCandidates {
		if scores[family] == 0 {
			continue
		}
		candidates = append(candidates, localCandidate{
			family: family,
			addr:   addr,
		})
	}
	if len(candidates) == 0 {
		for family, addr := range familyCandidates {
			candidates = append(candidates, localCandidate{
				family: family,
				addr:   addr,
			})
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		leftScore := scores[left.family]
		rightScore := scores[right.family]
		switch {
		case leftScore != rightScore:
			return leftScore > rightScore
		default:
			return left.family < right.family
		}
	})
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.addr)
	}
	return out, nil
}
