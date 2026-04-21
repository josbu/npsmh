package p2p

type P2PTraversalPolicy struct {
	ForcePredictOnRestricted  bool `json:"force_predict_on_restricted,omitempty"`
	EnableTargetSpray         bool `json:"enable_target_spray,omitempty"`
	EnableBirthdayAttack      bool `json:"enable_birthday_attack,omitempty"`
	DefaultPredictionInterval int  `json:"default_prediction_interval,omitempty"`
	TargetSpraySpan           int  `json:"target_spray_span,omitempty"`
	TargetSprayRounds         int  `json:"target_spray_rounds,omitempty"`
	TargetSprayBurst          int  `json:"target_spray_burst,omitempty"`
	TargetSprayPacketSleepMs  int  `json:"target_spray_packet_sleep_ms,omitempty"`
	TargetSprayBurstGapMs     int  `json:"target_spray_burst_gap_ms,omitempty"`
	TargetSprayPhaseGapMs     int  `json:"target_spray_phase_gap_ms,omitempty"`
	BirthdayListenPorts       int  `json:"birthday_listen_ports,omitempty"`
	BirthdayTargetsPerPort    int  `json:"birthday_targets_per_port,omitempty"`
	NominationDelayMs         int  `json:"nomination_delay_ms,omitempty"`
	NominationRetryMs         int  `json:"nomination_retry_ms,omitempty"`
}

type P2PPortMappingPolicy struct {
	EnableUPNPPortmap   bool `json:"enable_upnp_portmap,omitempty"`
	EnablePCPPortmap    bool `json:"enable_pcp_portmap,omitempty"`
	EnableNATPMPPortmap bool `json:"enable_natpmp_portmap,omitempty"`
	LeaseSeconds        int  `json:"portmap_lease_seconds,omitempty"`
}

type P2PPolicy struct {
	Layout          string               `json:"layout,omitempty"`
	BasePort        int                  `json:"base_port,omitempty"`
	ProbeTimeoutMs  int                  `json:"probe_timeout_ms,omitempty"`
	ProbeExtraReply bool                 `json:"probe_extra_reply,omitempty"`
	Traversal       P2PTraversalPolicy   `json:"traversal,omitempty"`
	PortMapping     P2PPortMappingPolicy `json:"port_mapping,omitempty"`
}

type P2PFamilyHintDetail struct {
	NATType              string `json:"nat_type,omitempty"`
	MappingBehavior      string `json:"mapping_behavior,omitempty"`
	FilteringBehavior    string `json:"filtering_behavior,omitempty"`
	ClassificationLevel  string `json:"classification_level,omitempty"`
	ProbeIPCount         int    `json:"probe_ip_count,omitempty"`
	ProbeEndpointCount   int    `json:"probe_endpoint_count,omitempty"`
	ProbeProviderCount   int    `json:"probe_provider_count,omitempty"`
	ObservedBasePort     int    `json:"observed_base_port,omitempty"`
	ObservedInterval     int    `json:"observed_interval,omitempty"`
	MappingConfidenceLow bool   `json:"mapping_confidence_low,omitempty"`
	FilteringTested      bool   `json:"filtering_tested,omitempty"`
	PortMappingMethod    string `json:"port_mapping_method,omitempty"`
	SampleCount          int    `json:"sample_count,omitempty"`
}

type P2PSummaryHints struct {
	ProbePortRestricted     bool                           `json:"probe_port_restricted,omitempty"`
	MappingConfidenceLow    bool                           `json:"mapping_confidence_low,omitempty"`
	FilteringLikely         bool                           `json:"filtering_likely,omitempty"`
	SelfProbeIPCount        int                            `json:"self_probe_ip_count,omitempty"`
	PeerProbeIPCount        int                            `json:"peer_probe_ip_count,omitempty"`
	SelfProbeEndpointCount  int                            `json:"self_probe_endpoint_count,omitempty"`
	PeerProbeEndpointCount  int                            `json:"peer_probe_endpoint_count,omitempty"`
	SelfFilteringTested     bool                           `json:"self_filtering_tested,omitempty"`
	PeerFilteringTested     bool                           `json:"peer_filtering_tested,omitempty"`
	SelfConflictingSignals  bool                           `json:"self_conflicting_signals,omitempty"`
	PeerConflictingSignals  bool                           `json:"peer_conflicting_signals,omitempty"`
	SelfPortMappingMethod   string                         `json:"self_port_mapping_method,omitempty"`
	PeerPortMappingMethod   string                         `json:"peer_port_mapping_method,omitempty"`
	SelfProbeProviderCount  int                            `json:"self_probe_provider_count,omitempty"`
	PeerProbeProviderCount  int                            `json:"peer_probe_provider_count,omitempty"`
	SelfObservedBasePort    int                            `json:"self_observed_base_port,omitempty"`
	SelfObservedInterval    int                            `json:"self_observed_interval,omitempty"`
	SelfMappingBehavior     string                         `json:"self_mapping_behavior,omitempty"`
	SelfFilteringBehavior   string                         `json:"self_filtering_behavior,omitempty"`
	SelfClassificationLevel string                         `json:"self_classification_level,omitempty"`
	PeerObservedBasePort    int                            `json:"peer_observed_base_port,omitempty"`
	PeerObservedInterval    int                            `json:"peer_observed_interval,omitempty"`
	PeerMappingBehavior     string                         `json:"peer_mapping_behavior,omitempty"`
	PeerFilteringBehavior   string                         `json:"peer_filtering_behavior,omitempty"`
	PeerClassificationLevel string                         `json:"peer_classification_level,omitempty"`
	SelfFamilyCount         int                            `json:"self_family_count,omitempty"`
	PeerFamilyCount         int                            `json:"peer_family_count,omitempty"`
	SharedFamilyCount       int                            `json:"shared_family_count,omitempty"`
	SharedFamilies          []string                       `json:"shared_families,omitempty"`
	DualStackParallel       bool                           `json:"dual_stack_parallel,omitempty"`
	SelfFamilyDetails       map[string]P2PFamilyHintDetail `json:"self_family_details,omitempty"`
	PeerFamilyDetails       map[string]P2PFamilyHintDetail `json:"peer_family_details,omitempty"`
}
