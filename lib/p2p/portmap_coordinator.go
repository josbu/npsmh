package p2p

import (
	"context"
	"net"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

type portMappingAttempt struct {
	method string
	try    func(ctx context.Context, internalIP string, internalPort, leaseSeconds int) (*PortMappingInfo, func() error, func() error)
}

type portMappingCoordinator struct {
	packetConn   net.PacketConn
	observation  NatObservation
	internalIP   string
	internalPort int
	leaseSeconds int
	attempts     []portMappingAttempt
}

func newPortMappingCoordinator(packetConn net.PacketConn, probe P2PProbeConfig, observation NatObservation) *portMappingCoordinator {
	if packetConn == nil {
		return nil
	}
	coordinator := &portMappingCoordinator{
		packetConn:   packetConn,
		observation:  observation,
		internalPort: common.GetPortByAddr(packetConn.LocalAddr().String()),
		internalIP:   portMappingInternalIP(packetConn.LocalAddr()),
		leaseSeconds: defaultPortMappingLeaseSeconds,
	}
	if policy := probe.Policy; policy != nil {
		if policy.PortMapping.LeaseSeconds > 0 {
			coordinator.leaseSeconds = policy.PortMapping.LeaseSeconds
		}
		if policy.PortMapping.EnablePCPPortmap {
			coordinator.attempts = append(coordinator.attempts, portMappingAttempt{method: "pcp", try: tryPCPPortMapping})
		}
		if policy.PortMapping.EnableNATPMPPortmap {
			coordinator.attempts = append(coordinator.attempts, portMappingAttempt{method: "nat-pmp", try: tryNATPMPPortMapping})
		}
		if policy.PortMapping.EnableUPNPPortmap {
			coordinator.attempts = append(coordinator.attempts, portMappingAttempt{method: "upnp", try: tryUPnPPortMapping})
		}
	}
	return coordinator
}

func (c *portMappingCoordinator) enabled() bool {
	return c != nil && len(c.attempts) > 0
}

func (c *portMappingCoordinator) eligible() bool {
	if !c.enabled() {
		return false
	}
	if !shouldAttemptPortMapping(c.observation) {
		logs.Trace("[P2P] skip port mapping nat_type=%s mapping_confidence_low=%v probe_port_restricted=%v filtering_tested=%v",
			c.observation.NATType, c.observation.MappingConfidenceLow, c.observation.ProbePortRestricted, c.observation.FilteringTested)
		return false
	}
	return c.internalPort > 0 && c.internalIP != ""
}

func (c *portMappingCoordinator) attempt(ctx context.Context) (net.PacketConn, *PortMappingInfo) {
	if c == nil || c.packetConn == nil {
		return nil, nil
	}
	if !c.eligible() {
		return c.packetConn, nil
	}
	ctx = normalizePortMappingContext(ctx)
	if ctx.Err() != nil {
		return c.packetConn, nil
	}
	attemptCtx, cancel := context.WithTimeout(ctx, defaultPortMappingAttemptTimeout)
	defer cancel()
	for _, attempt := range c.attempts {
		if info, cleanup, renew := attempt.try(attemptCtx, c.internalIP, c.internalPort, c.leaseSeconds); info != nil {
			logs.Info("[P2P] port mapping method=%s external=%s internal=%s lease=%ds", info.Method, info.ExternalAddr, info.InternalAddr, info.LeaseSeconds)
			return wrapManagedPacketConn(c.packetConn, cleanup, renew, c.leaseSeconds), info
		}
	}
	return c.packetConn, nil
}
