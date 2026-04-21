package p2p

import (
	"net"
	"strconv"

	"github.com/djylb/nps/lib/common"
)

type PunchTargetStage struct {
	Name    string
	Targets []string
}

func BuildPunchTargets(peer P2PPeerInfo, plan PunchPlan) []string {
	return appendPredictedPunchTargets(BuildDirectPunchTargets(peer), peer.Nat, plan.predictionIntervals(), plan.SpraySpan)
}

func BuildPreferredPunchTargets(self, peer P2PPeerInfo, plan PunchPlan) []string {
	targets := appendPredictedPunchTargets(BuildPreferredDirectPunchTargets(self, peer), peer.Nat, plan.predictionIntervals(), plan.SpraySpan)
	return appendPeerLocalFallbackTargets(targets, BuildPreferredLocalFallbackTargets(self, peer))
}

func BuildPredictedPunchTargetStages(peer P2PPeerInfo, plan PunchPlan) []PunchTargetStage {
	if peer.Nat.PublicIP == "" || peer.Nat.ObservedBasePort <= 0 || plan.SpraySpan <= 0 {
		return nil
	}
	portStages := buildPredictedPortStages(peer.Nat, plan.predictionIntervals(), plan.SpraySpan)
	if len(portStages) == 0 {
		return nil
	}
	stages := make([]PunchTargetStage, 0, len(portStages))
	for _, portStage := range portStages {
		targets := make([]string, 0, len(portStage.ports))
		for _, port := range portStage.ports {
			addr := common.ValidateAddr(net.JoinHostPort(peer.Nat.PublicIP, strconv.Itoa(port)))
			if addr == "" {
				continue
			}
			targets = append(targets, addr)
		}
		if len(targets) == 0 {
			continue
		}
		stages = append(stages, PunchTargetStage{
			Name:    portStage.name,
			Targets: targets,
		})
	}
	return stages
}

func BuildDirectPunchTargets(peer P2PPeerInfo) []string {
	targets := make([]string, 0, len(peer.Nat.Samples)+len(peer.LocalAddrs))
	seen := make(map[string]struct{})
	add := func(addr string) {
		addr = common.ValidateAddr(addr)
		if addr == "" {
			return
		}
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		targets = append(targets, addr)
	}
	if peer.Nat.PortMapping != nil {
		add(peer.Nat.PortMapping.ExternalAddr)
	}
	for _, sample := range peer.Nat.Samples {
		add(sample.ObservedAddr)
	}
	wantIPv6 := peer.Nat.PublicIP != "" && net.ParseIP(peer.Nat.PublicIP) != nil && net.ParseIP(peer.Nat.PublicIP).To4() == nil
	for _, localAddr := range peer.LocalAddrs {
		host := common.GetIpByAddr(localAddr)
		ip := net.ParseIP(host)
		if ip != nil {
			isIPv6 := ip.To4() == nil
			if peer.Nat.PublicIP != "" && isIPv6 != wantIPv6 {
				continue
			}
		}
		add(localAddr)
	}
	return targets
}

func BuildPreferredDirectPunchTargets(self, peer P2PPeerInfo) []string {
	targets := make([]string, 0, len(peer.Nat.Samples)+len(peer.LocalAddrs)+1)
	seen := make(map[string]struct{})
	add := func(addr string) {
		addr = common.ValidateAddr(addr)
		if addr == "" {
			return
		}
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		targets = append(targets, addr)
	}
	supportV4, supportV6 := supportedLocalFamilies(self.LocalAddrs)
	for _, localAddr := range peer.LocalAddrs {
		if !supportsPeerLocalFamily(localAddr, supportV4, supportV6) || !isLikelyDirectLocalAddr(localAddr) || !prefersPeerLocalAddr(self.LocalAddrs, localAddr) {
			continue
		}
		add(localAddr)
	}
	if peer.Nat.PortMapping != nil {
		add(peer.Nat.PortMapping.ExternalAddr)
	}
	for _, sample := range peer.Nat.Samples {
		add(sample.ObservedAddr)
	}
	return targets
}

func BuildPreferredLocalFallbackTargets(self, peer P2PPeerInfo) []string {
	targets := make([]string, 0, len(peer.LocalAddrs))
	seen := make(map[string]struct{}, len(peer.LocalAddrs))
	supportV4, supportV6 := supportedLocalFamilies(self.LocalAddrs)
	for _, localAddr := range peer.LocalAddrs {
		if !supportsPeerLocalFamily(localAddr, supportV4, supportV6) || !isLikelyDirectLocalAddr(localAddr) {
			continue
		}
		addr := common.ValidateAddr(localAddr)
		if addr == "" || prefersPeerLocalAddr(self.LocalAddrs, addr) {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		targets = append(targets, addr)
	}
	return targets
}

func BuildBirthdayPunchTargets(peer P2PPeerInfo, plan PunchPlan) []string {
	targets := make([]string, 0, len(peer.Nat.Samples)+plan.BirthdayTargetsPerPort)
	seen := make(map[string]struct{})
	add := func(addr string) {
		addr = common.ValidateAddr(addr)
		if addr == "" {
			return
		}
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		targets = append(targets, addr)
	}
	for _, sample := range peer.Nat.Samples {
		add(sample.ObservedAddr)
	}
	return appendPredictedPunchTargets(targets, peer.Nat, plan.predictionIntervals(), plan.BirthdayTargetsPerPort)
}

func supportedLocalFamilies(localAddrs []string) (bool, bool) {
	if len(localAddrs) == 0 {
		return true, true
	}
	var supportV4, supportV6 bool
	for _, addr := range localAddrs {
		ip := common.ParseIPFromAddr(addr)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			supportV4 = true
		} else {
			supportV6 = true
		}
	}
	if !supportV4 && !supportV6 {
		return true, true
	}
	return supportV4, supportV6
}

func supportsPeerLocalFamily(addr string, supportV4, supportV6 bool) bool {
	ip := common.ParseIPFromAddr(addr)
	if ip == nil {
		return false
	}
	if ip.To4() != nil {
		return supportV4
	}
	return supportV6
}

func isLikelyDirectLocalAddr(addr string) bool {
	ip := common.ParseIPFromAddr(addr)
	if ip == nil {
		return false
	}
	ip = common.NormalizeIP(ip)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return !common.IsPublicIPStrict(ip)
}

func prefersPeerLocalAddr(selfLocalAddrs []string, peerAddr string) bool {
	peerIP := common.NormalizeIP(common.ParseIPFromAddr(peerAddr))
	if peerIP == nil || (!peerIP.IsPrivate() && !peerIP.IsLinkLocalUnicast()) {
		return false
	}
	for _, selfAddr := range selfLocalAddrs {
		selfIP := common.NormalizeIP(common.ParseIPFromAddr(selfAddr))
		if selfIP == nil {
			continue
		}
		if sameLikelyDirectSubnet(selfIP, peerIP) {
			return true
		}
	}
	return false
}

func sameLikelyDirectSubnet(a, b net.IP) bool {
	a = common.NormalizeIP(a)
	b = common.NormalizeIP(b)
	if a == nil || b == nil || (a.To4() == nil) != (b.To4() == nil) {
		return false
	}
	if av4 := a.To4(); av4 != nil {
		bv4 := b.To4()
		if bv4 == nil || !av4.IsPrivate() || !bv4.IsPrivate() {
			return false
		}
		return av4[0] == bv4[0] && av4[1] == bv4[1] && av4[2] == bv4[2]
	}
	if !(a.IsPrivate() || a.IsLinkLocalUnicast()) || !(b.IsPrivate() || b.IsLinkLocalUnicast()) {
		return false
	}
	return len(a) >= 8 && len(b) >= 8 && a[0] == b[0] && a[1] == b[1] && a[2] == b[2] && a[3] == b[3] &&
		a[4] == b[4] && a[5] == b[5] && a[6] == b[6] && a[7] == b[7]
}

func appendPredictedPunchTargets(targets []string, obs NatObservation, intervals []int, span int) []string {
	if len(targets) == 0 {
		targets = make([]string, 0, span)
	}
	seen := make(map[string]struct{}, len(targets)+span)
	for _, target := range targets {
		target = common.ValidateAddr(target)
		if target == "" {
			continue
		}
		seen[target] = struct{}{}
	}
	add := func(addr string) {
		addr = common.ValidateAddr(addr)
		if addr == "" {
			return
		}
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		targets = append(targets, addr)
	}
	for _, port := range BuildPredictedPorts(obs, intervals, span) {
		add(net.JoinHostPort(obs.PublicIP, strconv.Itoa(port)))
	}
	return targets
}

func appendPeerLocalFallbackTargets(targets, fallbacks []string) []string {
	if len(fallbacks) == 0 {
		return targets
	}
	seen := make(map[string]struct{}, len(targets)+len(fallbacks))
	for _, target := range targets {
		target = common.ValidateAddr(target)
		if target == "" {
			continue
		}
		seen[target] = struct{}{}
	}
	for _, fallback := range fallbacks {
		fallback = common.ValidateAddr(fallback)
		if fallback == "" {
			continue
		}
		if _, ok := seen[fallback]; ok {
			continue
		}
		seen[fallback] = struct{}{}
		targets = append(targets, fallback)
	}
	return targets
}
