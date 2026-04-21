package p2p

import "strings"

const (
	packetTypeProbe   = "probe"
	packetTypeAck     = "probe_ack"
	packetTypeProbeX  = "probe_extra"
	packetTypePunch   = "punch"
	packetTypeSucc    = "success"
	packetTypeEnd     = "end"
	packetTypeAccept  = "accept"
	packetTypeReady   = "ready"
	udpPayloadVersion = 1
	udpFlagExtraReply = 1 << 0

	ProbeProviderNPS = "nps"
	ProbeModeUDP     = "udp_probe"
	ProbeNetworkUDP  = "udp"

	NATMappingUnknown             = "unknown"
	NATMappingEndpointIndependent = "endpoint_independent"
	NATMappingEndpointDependent   = "endpoint_dependent"
	NATFilteringUnknown           = "unknown"
	NATFilteringOpen              = "open_or_address_restricted"
	NATFilteringPortRestricted    = "port_restricted"
	NATTypeUnknown                = "unknown"
	NATTypeCone                   = "cone"
	NATTypeRestrictedCone         = "restricted_cone"
	NATTypePortRestricted         = "port_restricted"
	NATTypeSymmetric              = "symmetric"
	ClassificationConfidenceLow   = "low"
	ClassificationConfidenceMed   = "medium"
	ClassificationConfidenceHigh  = "high"
)

type ProbeSample struct {
	EndpointID      string `json:"endpoint_id,omitempty"`
	Provider        string `json:"provider,omitempty"`
	Mode            string `json:"mode,omitempty"`
	ProbePort       int    `json:"probe_port"`
	ObservedAddr    string `json:"observed_addr"`
	ServerReplyAddr string `json:"server_reply_addr,omitempty"`
	ExtraReply      bool   `json:"extra_reply,omitempty"`
}

type PortMappingInfo struct {
	Method       string `json:"method,omitempty"`
	ExternalAddr string `json:"external_addr,omitempty"`
	InternalAddr string `json:"internal_addr,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type NatObservation struct {
	PublicIP             string           `json:"public_ip"`
	ObservedBasePort     int              `json:"observed_base_port"`
	ObservedInterval     int              `json:"observed_interval"`
	ProbePortRestricted  bool             `json:"probe_port_restricted"`
	FilteringTested      bool             `json:"filtering_tested,omitempty"`
	MappingBehavior      string           `json:"mapping_behavior,omitempty"`
	FilteringBehavior    string           `json:"filtering_behavior,omitempty"`
	NATType              string           `json:"nat_type,omitempty"`
	ClassificationLevel  string           `json:"classification_level,omitempty"`
	ProbeIPCount         int              `json:"probe_ip_count,omitempty"`
	ProbeEndpointCount   int              `json:"probe_endpoint_count,omitempty"`
	ConflictingSignals   bool             `json:"conflicting_signals,omitempty"`
	MappingConfidenceLow bool             `json:"mapping_confidence_low"`
	PortMapping          *PortMappingInfo `json:"port_mapping,omitempty"`
	Samples              []ProbeSample    `json:"samples,omitempty"`
}

type P2PTimeouts struct {
	ProbeTimeoutMs     int `json:"probe_timeout_ms"`
	HandshakeTimeoutMs int `json:"handshake_timeout_ms"`
	TransportTimeoutMs int `json:"transport_timeout_ms"`
}

type P2PPeerInfo struct {
	Role          string          `json:"role"`
	Nat           NatObservation  `json:"nat"`
	LocalAddrs    []string        `json:"local_addrs,omitempty"`
	Families      []P2PFamilyInfo `json:"families,omitempty"`
	TransportMode string          `json:"transport_mode,omitempty"`
	TransportData string          `json:"transport_data,omitempty"`
}

type P2PFamilyInfo struct {
	Family     string         `json:"family"`
	Nat        NatObservation `json:"nat"`
	LocalAddrs []string       `json:"local_addrs,omitempty"`
}

type P2PProbeEndpoint struct {
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Network  string `json:"network,omitempty"`
	Address  string `json:"address"`
}

type P2PProbeConfig struct {
	Version          int                `json:"version"`
	Provider         string             `json:"provider"`
	Mode             string             `json:"mode"`
	Network          string             `json:"network"`
	Endpoints        []P2PProbeEndpoint `json:"endpoints"`
	ExpectExtraReply bool               `json:"expect_extra_reply,omitempty"`
	Policy           *P2PPolicy         `json:"policy,omitempty"`
}

func HasProbeProvider(probe P2PProbeConfig, provider string) bool {
	for _, endpoint := range NormalizeProbeEndpoints(probe) {
		if endpoint.Provider == provider {
			return true
		}
	}
	return false
}

func HasUsableProbeEndpoint(probe P2PProbeConfig) bool {
	for _, endpoint := range NormalizeProbeEndpoints(probe) {
		if isSupportedProbeEndpoint(endpoint) {
			return true
		}
	}
	return false
}

func isSupportedProbeEndpoint(endpoint P2PProbeEndpoint) bool {
	return endpoint.Provider == ProbeProviderNPS && endpoint.Mode == ProbeModeUDP && endpoint.Network == ProbeNetworkUDP
}

type P2PPeerRuntime struct {
	ClientID int    `json:"client_id"`
	UUID     string `json:"uuid"`
	BaseVer  int    `json:"base_ver"`
}

type P2PAssociation struct {
	AssociationID string         `json:"association_id"`
	Visitor       P2PPeerRuntime `json:"visitor"`
	Provider      P2PPeerRuntime `json:"provider"`
}

type P2PRouteContext struct {
	TunnelID       int             `json:"tunnel_id"`
	TunnelMode     string          `json:"tunnel_mode"`
	TargetType     string          `json:"target_type"`
	DestAclMode    int             `json:"dest_acl_mode,omitempty"`
	DestAclRules   string          `json:"dest_acl_rules,omitempty"`
	AccessPolicy   P2PAccessPolicy `json:"access_policy,omitempty"`
	RouteToken     string          `json:"route_token,omitempty"`
	PolicyRevision int64           `json:"policy_revision,omitempty"`
}

type P2PResolveRequest struct {
	PasswordMD5 string          `json:"password_md5"`
	RouteHint   P2PRouteContext `json:"route_hint,omitempty"`
}

type P2PResolveResult struct {
	Association       P2PAssociation  `json:"association"`
	AssociationPolicy P2PAccessPolicy `json:"association_policy,omitempty"`
	Route             P2PRouteContext `json:"route"`
	Phase             string          `json:"phase"`
	NeedPunch         bool            `json:"need_punch"`
}

type P2PConnectRequest struct {
	PasswordMD5   string          `json:"password_md5"`
	AssociationID string          `json:"association_id"`
	ProviderUUID  string          `json:"provider_uuid"`
	RouteHint     P2PRouteContext `json:"route_hint,omitempty"`
}

type P2PAssociationBind struct {
	Association       P2PAssociation  `json:"association"`
	AssociationPolicy P2PAccessPolicy `json:"association_policy,omitempty"`
	Route             P2PRouteContext `json:"route"`
	Phase             string          `json:"phase"`
}

type P2PPunchStart struct {
	SessionID         string          `json:"session_id"`
	Token             string          `json:"token"`
	Wire              P2PWireSpec     `json:"wire"`
	Role              string          `json:"role"`
	PeerRole          string          `json:"peer_role"`
	Probe             P2PProbeConfig  `json:"probe"`
	Timeouts          P2PTimeouts     `json:"timeouts"`
	AssociationID     string          `json:"association_id,omitempty"`
	AssociationPolicy P2PAccessPolicy `json:"association_policy,omitempty"`
	Self              P2PPeerRuntime  `json:"self,omitempty"`
	Peer              P2PPeerRuntime  `json:"peer,omitempty"`
	Route             P2PRouteContext `json:"route,omitempty"`
}

type P2PProbeReport struct {
	SessionID string      `json:"session_id"`
	Token     string      `json:"token"`
	Role      string      `json:"role"`
	PeerRole  string      `json:"peer_role"`
	Self      P2PPeerInfo `json:"self"`
}

type P2PProbeSummary struct {
	SessionID    string           `json:"session_id"`
	Token        string           `json:"token"`
	Role         string           `json:"role"`
	PeerRole     string           `json:"peer_role"`
	Self         P2PPeerInfo      `json:"self"`
	Peer         P2PPeerInfo      `json:"peer"`
	Timeouts     P2PTimeouts      `json:"timeouts"`
	SummaryHints *P2PSummaryHints `json:"summary_hints,omitempty"`
}

type P2PPunchReady struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
}

type P2PPunchGo struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	DelayMs   int    `json:"delay_ms"`
	SentAtMs  int64  `json:"sent_at_ms,omitempty"`
}

type P2PPunchProgress struct {
	SessionID  string            `json:"session_id"`
	Role       string            `json:"role"`
	Stage      string            `json:"stage"`
	Status     string            `json:"status,omitempty"`
	Detail     string            `json:"detail,omitempty"`
	LocalAddr  string            `json:"local_addr,omitempty"`
	RemoteAddr string            `json:"remote_addr,omitempty"`
	Timestamp  int64             `json:"timestamp,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
	Counters   map[string]int    `json:"counters,omitempty"`
}

type P2PPunchAbort struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Reason    string `json:"reason"`
}

type P2PSessionJoin struct {
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
}

type UDPPacket struct {
	WireID          string `json:"-"`
	PayloadMinBytes int    `json:"-"`
	PayloadMaxBytes int    `json:"-"`
	SessionID       string `json:"session_id"`
	Token           string `json:"-"`
	Type            string `json:"type,omitempty"`
	Role            string `json:"role,omitempty"`
	NominationEpoch uint32 `json:"nomination_epoch,omitempty"`
	ProbePort       int    `json:"probe_port,omitempty"`
	ObservedAddr    string `json:"observed_addr,omitempty"`
	ExtraReply      bool   `json:"extra_reply,omitempty"`
	Timestamp       int64  `json:"timestamp"`
	Nonce           string `json:"nonce,omitempty"`
}

func DefaultTimeouts() P2PTimeouts {
	return P2PTimeouts{
		ProbeTimeoutMs:     5000,
		HandshakeTimeoutMs: 20000,
		TransportTimeoutMs: 10000,
	}
}

func isIgnorableUDPIcmpError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connection reset by peer") {
		return true
	}
	if strings.Contains(errStr, "wsarecvfrom") && (strings.Contains(errStr, "10054") || strings.Contains(errStr, "wsaeconnreset")) {
		return true
	}
	return false
}
