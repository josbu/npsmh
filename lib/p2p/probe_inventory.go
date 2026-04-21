package p2p

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
)

type resolvedProbeEndpoint struct {
	endpoint P2PProbeEndpoint
	addr     *net.UDPAddr
	key      string
}

type probeEndpointInventory struct {
	endpoints []P2PProbeEndpoint
}

func newProbeEndpointInventory(probe P2PProbeConfig) probeEndpointInventory {
	return probeEndpointInventory{endpoints: NormalizeProbeEndpoints(probe)}
}

func (inv probeEndpointInventory) all() []P2PProbeEndpoint {
	return append([]P2PProbeEndpoint(nil), inv.endpoints...)
}

func (inv probeEndpointInventory) npsUDP() []P2PProbeEndpoint {
	out := make([]P2PProbeEndpoint, 0, len(inv.endpoints))
	for _, endpoint := range inv.endpoints {
		if endpoint.Provider == ProbeProviderNPS && endpoint.Mode == ProbeModeUDP && endpoint.Network == ProbeNetworkUDP {
			out = append(out, endpoint)
		}
	}
	return out
}

func (inv probeEndpointInventory) familyScores(ctx context.Context, timeout time.Duration) map[udpAddrFamily]int {
	scores := map[udpAddrFamily]int{
		udpFamilyV4: 0,
		udpFamilyV6: 0,
	}
	for _, endpoint := range inv.endpoints {
		families := probeEndpointFamilies(ctx, endpoint, timeout)
		for _, family := range families {
			scores[family]++
		}
	}
	return scores
}

func (inv probeEndpointInventory) addrs() []string {
	addrs := make([]string, 0, len(inv.endpoints))
	for _, endpoint := range inv.endpoints {
		addr := endpoint.Address
		if host, port, err := net.SplitHostPort(addr); err == nil && host != "" && port != "" {
			addrs = append(addrs, net.JoinHostPort(strings.Trim(host, "[]"), port))
		}
	}
	return addrs
}

func (inv probeEndpointInventory) resolveNPS(ctx context.Context, localAddr net.Addr, timeout time.Duration) ([]resolvedProbeEndpoint, map[string]struct{}, error) {
	endpoints := compatibleNPSProbeEndpoints(localAddr, inv.npsUDP())
	if len(endpoints) == 0 {
		return nil, nil, fmt.Errorf("no compatible nps probe endpoints for %s", addrString(localAddr))
	}
	resolvedEndpoints := make([]resolvedProbeEndpoint, 0, len(endpoints))
	seenResolved := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		udpAddr, err := resolveUDPAddrContext(ctx, localAddr, endpoint.Address, timeout)
		if err != nil {
			continue
		}
		key := npsProbeKey(common.GetPortByAddr(endpoint.Address), udpAddr.String())
		if key == "" {
			continue
		}
		if _, ok := seenResolved[key]; ok {
			continue
		}
		seenResolved[key] = struct{}{}
		resolvedEndpoints = append(resolvedEndpoints, resolvedProbeEndpoint{
			endpoint: endpoint,
			addr:     udpAddr,
			key:      key,
		})
	}
	if len(resolvedEndpoints) == 0 {
		return nil, nil, fmt.Errorf("no resolvable nps probe endpoints for %s", addrString(localAddr))
	}
	expectedKeys := make(map[string]struct{}, len(resolvedEndpoints))
	for _, endpoint := range resolvedEndpoints {
		expectedKeys[endpoint.key] = struct{}{}
	}
	if len(expectedKeys) == 0 {
		return nil, nil, fmt.Errorf("no valid nps probe endpoints for %s", addrString(localAddr))
	}
	return resolvedEndpoints, expectedKeys, nil
}

func probeResolveTimeout(probe P2PProbeConfig, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = 250 * time.Millisecond
	}
	if policy := probe.Policy; policy != nil && policy.ProbeTimeoutMs > 0 {
		timeout := time.Duration(policy.ProbeTimeoutMs) * time.Millisecond / 4
		if timeout > 0 && timeout < fallback {
			return timeout
		}
	}
	return fallback
}

func compatibleNPSProbeEndpoints(localAddr net.Addr, endpoints []P2PProbeEndpoint) []P2PProbeEndpoint {
	localIP := common.ParseIPFromAddr(addrString(localAddr))
	if localIP == nil {
		return append([]P2PProbeEndpoint(nil), endpoints...)
	}
	wantIPv6 := localIP.To4() == nil
	out := make([]P2PProbeEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		host := common.GetIpByAddr(endpoint.Address)
		ip := net.ParseIP(host)
		if ip != nil && (ip.To4() == nil) != wantIPv6 {
			continue
		}
		out = append(out, endpoint)
	}
	return out
}

func resolveUDPAddrContext(ctx context.Context, localAddr net.Addr, address string, timeout time.Duration) (*net.UDPAddr, error) {
	resolveCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	portNum, err := net.LookupPort("udp", port)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if familyMismatch(localAddr, ip) {
			return nil, fmt.Errorf("resolved ip family mismatch for %s", address)
		}
		return &net.UDPAddr{IP: ip, Port: portNum}, nil
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return nil, err
	}
	var fallback net.IP
	for _, addr := range addrs {
		if ip := common.NormalizeIP(addr.IP); ip != nil {
			if familyMismatch(localAddr, ip) {
				if fallback == nil {
					fallback = ip
				}
				continue
			}
			return &net.UDPAddr{IP: ip, Port: portNum}, nil
		}
	}
	if fallback != nil {
		return nil, fmt.Errorf("resolved only incompatible ip family for %s", address)
	}
	return nil, fmt.Errorf("udp resolve returned no ip for %s", address)
}

func familyMismatch(localAddr net.Addr, ip net.IP) bool {
	return !detectAddrFamily(localAddr).matchesIP(ip)
}

func probeResolveNetwork(localAddr net.Addr) string {
	return detectAddrFamily(localAddr).network()
}

func buildLocalAddrs(localAddr net.Addr) []string {
	if localAddr == nil {
		return nil
	}
	port := common.GetPortStrByAddr(localAddr.String())
	if port == "" {
		return nil
	}
	if ip := common.NormalizeIP(common.ParseIPFromAddr(addrString(localAddr))); ip != nil && !common.IsZeroIP(ip) && !ip.IsUnspecified() {
		return []string{net.JoinHostPort(ip.String(), port)}
	}
	out := make([]string, 0, 8)
	add := func(ip net.IP) {
		if ip == nil || port == "" {
			return
		}
		addr := net.JoinHostPort(ip.String(), port)
		if addr == "" {
			return
		}
		for _, existing := range out {
			if existing == addr {
				return
			}
		}
		out = append(out, addr)
	}
	if ip := common.ParseIPFromAddr(addrString(localAddr)); ip != nil {
		add(ip)
	}
	if udp4, err := common.GetLocalUdp4IP(); err == nil {
		add(udp4)
	}
	if udp6, err := common.GetLocalUdp6IP(); err == nil {
		add(udp6)
	}
	for _, ip := range listInterfaceIPs() {
		add(ip)
	}
	return out
}

func listInterfaceIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]net.IP, 0, 8)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch value := addr.(type) {
			case *net.IPNet:
				ip = value.IP
			case *net.IPAddr:
				ip = value.IP
			}
			ip = common.NormalizeIP(ip)
			if ip == nil || common.IsZeroIP(ip) || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			out = append(out, ip)
		}
	}
	return out
}

func normalizeLocalProbeCandidate(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return ""
	}
	ip := common.NormalizeIP(udpAddr.IP)
	if ip == nil || ip.IsUnspecified() {
		return ""
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(udpAddr.Port))
}

func preferredProbeBindAddr(preferredAddr string) (string, udpAddrFamily, bool) {
	preferredAddr = strings.TrimSpace(preferredAddr)
	preferredAddr = strings.Trim(preferredAddr, "[]")
	if preferredAddr == "" {
		return "", udpFamilyAny, false
	}
	ip := net.ParseIP(preferredAddr)
	if ip == nil || ip.IsUnspecified() {
		return "", udpFamilyAny, false
	}
	family := detectIPFamily(ip)
	if family == udpFamilyAny {
		return "", udpFamilyAny, false
	}
	return net.JoinHostPort(ip.String(), "0"), family, true
}

func probeEndpointFamilies(ctx context.Context, endpoint P2PProbeEndpoint, timeout time.Duration) []udpAddrFamily {
	host := common.GetIpByAddr(endpoint.Address)
	if host == "" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		family := detectIPFamily(ip)
		if family == udpFamilyAny {
			return nil
		}
		return []udpAddrFamily{family}
	}

	resolveCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil {
		return nil
	}
	families := make([]udpAddrFamily, 0, 2)
	seen := make(map[udpAddrFamily]struct{}, 2)
	for _, addr := range addrs {
		family := detectIPFamily(addr.IP)
		if family == udpFamilyAny {
			continue
		}
		if _, ok := seen[family]; ok {
			continue
		}
		seen[family] = struct{}{}
		families = append(families, family)
	}
	sort.SliceStable(families, func(i, j int) bool { return families[i] < families[j] })
	return families
}

func probeEndpointAddrs(probe P2PProbeConfig) []string {
	return newProbeEndpointInventory(probe).addrs()
}
