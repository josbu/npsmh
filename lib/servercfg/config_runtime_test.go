package servercfg

import (
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/p2p"
)

func TestDefaultP2PSettingsFavorAggressiveTraversal(t *testing.T) {
	resetTestState(t)

	cfg := Current()
	if !cfg.P2P.EnableBirthdayAttack {
		t.Fatal("Current().P2P.EnableBirthdayAttack = false, want true")
	}
	if cfg.P2P.TargetSpraySpan != 128 {
		t.Fatalf("Current().P2P.TargetSpraySpan = %d, want 128", cfg.P2P.TargetSpraySpan)
	}
	if cfg.P2P.BirthdayListenPorts != 64 {
		t.Fatalf("Current().P2P.BirthdayListenPorts = %d, want 64", cfg.P2P.BirthdayListenPorts)
	}
	if cfg.P2P.BirthdayTargetsPerPort != 128 {
		t.Fatalf("Current().P2P.BirthdayTargetsPerPort = %d, want 128", cfg.P2P.BirthdayTargetsPerPort)
	}
	if !cfg.P2P.EnableUPNPPortmap || !cfg.P2P.EnablePCPPortmap || !cfg.P2P.EnableNATPMPPortmap {
		t.Fatalf("Current().P2P port mapping defaults = %+v, want all enabled", cfg.P2P)
	}

	policy := cfg.P2PProbePolicy(10206)
	if policy.BasePort != 10206 || policy.Layout != "triple-port" {
		t.Fatalf("Current().P2PProbePolicy() base/layout = %+v", policy)
	}
	if !policy.ProbeExtraReply || !policy.Traversal.EnableBirthdayAttack || policy.Traversal.TargetSpraySpan != 128 {
		t.Fatalf("Current().P2PProbePolicy() defaults = %+v", policy)
	}
	if !policy.PortMapping.EnableUPNPPortmap || !policy.PortMapping.EnablePCPPortmap || !policy.PortMapping.EnableNATPMPPortmap {
		t.Fatalf("Current().P2PProbePolicy() port mapping defaults = %+v", policy.PortMapping)
	}
	timeouts := cfg.P2PProbeTimeouts()
	if timeouts != (p2p.P2PTimeouts{ProbeTimeoutMs: 5000, HandshakeTimeoutMs: 20000, TransportTimeoutMs: 10000}) {
		t.Fatalf("Current().P2PProbeTimeouts() = %+v, want default timeouts", timeouts)
	}
}

func TestP2PProbeAccessorsMirrorSnapshotValues(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.yaml", strings.Join([]string{
		"p2p:",
		"  probe-extra-reply: false",
		"  force-predict-on-restricted: false",
		"  enable-target-spray: false",
		"  enable-birthday-attack: false",
		"  enable-upnp-portmap: false",
		"  enable-pcp-portmap: true",
		"  enable-natpmp-portmap: false",
		"  portmap-lease_seconds: 1800",
		"  default_prediction_interval: 7",
		"  target_spray_span: 96",
		"  target_spray_rounds: 3",
		"  target_spray_burst: 5",
		"  target_spray_packet_sleep_ms: 9",
		"  target_spray_burst_gap_ms: 12",
		"  target_spray_phase_gap_ms: 21",
		"  birthday_listen_ports: 12",
		"  birthday_targets_per_port: 34",
		"  probe_timeout_ms: 4000",
		"  handshake_timeout_ms: 15000",
		"  transport_timeout_ms: 9000",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	policy := cfg.P2PProbePolicy(10207)
	expectedPolicy := p2p.P2PPolicy{
		Layout:          "triple-port",
		BasePort:        10207,
		ProbeTimeoutMs:  4000,
		ProbeExtraReply: false,
		Traversal: p2p.P2PTraversalPolicy{
			ForcePredictOnRestricted:  false,
			EnableTargetSpray:         false,
			EnableBirthdayAttack:      false,
			DefaultPredictionInterval: 7,
			TargetSpraySpan:           96,
			TargetSprayRounds:         3,
			TargetSprayBurst:          5,
			TargetSprayPacketSleepMs:  9,
			TargetSprayBurstGapMs:     12,
			TargetSprayPhaseGapMs:     21,
			BirthdayListenPorts:       12,
			BirthdayTargetsPerPort:    34,
		},
		PortMapping: p2p.P2PPortMappingPolicy{
			EnableUPNPPortmap:   false,
			EnablePCPPortmap:    true,
			EnableNATPMPPortmap: false,
			LeaseSeconds:        1800,
		},
	}
	if policy != expectedPolicy {
		t.Fatalf("Current().P2PProbePolicy() = %+v, want %+v", policy, expectedPolicy)
	}

	timeouts := cfg.P2PProbeTimeouts()
	expectedTimeouts := p2p.P2PTimeouts{
		ProbeTimeoutMs:     4000,
		HandshakeTimeoutMs: 15000,
		TransportTimeoutMs: 9000,
	}
	if timeouts != expectedTimeouts {
		t.Fatalf("Current().P2PProbeTimeouts() = %+v, want %+v", timeouts, expectedTimeouts)
	}
}

func TestP2PProbeAccessorsNormalizeNonPositiveTimeouts(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"p2p_probe_timeout_ms=0",
		"p2p_handshake_timeout_ms=-10",
		"p2p_transport_timeout_ms=0",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	timeouts := cfg.P2PProbeTimeouts()
	expected := p2p.DefaultTimeouts()
	if timeouts != expected {
		t.Fatalf("Current().P2PProbeTimeouts() = %+v, want %+v", timeouts, expected)
	}

	policy := cfg.P2PProbePolicy(12000)
	if policy.ProbeTimeoutMs != expected.ProbeTimeoutMs {
		t.Fatalf("Current().P2PProbePolicy().ProbeTimeoutMs = %d, want %d", policy.ProbeTimeoutMs, expected.ProbeTimeoutMs)
	}
}

func TestP2PProbeBasePortAccessorNormalizesInvalidRange(t *testing.T) {
	if got := (&Snapshot{Network: NetworkConfig{P2PPort: 6000}}).P2PProbeBasePort(); got != 6000 {
		t.Fatalf("P2PProbeBasePort() = %d, want 6000", got)
	}
	if got := (&Snapshot{Network: NetworkConfig{P2PPort: 65534}}).P2PProbeBasePort(); got != 0 {
		t.Fatalf("P2PProbeBasePort() with 65534 = %d, want 0", got)
	}
	if got := (&Snapshot{Network: NetworkConfig{P2PPort: 70000}}).P2PProbeBasePort(); got != 0 {
		t.Fatalf("P2PProbeBasePort() with 70000 = %d, want 0", got)
	}
}

func TestRuntimeProtocolTuningNormalization(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"node_changes_window=1",
		"node_batch_max_items=9999",
		"node_idempotency_ttl_seconds=2",
		"node_traffic_report_interval_seconds=0",
		"node_traffic_report_step_bytes=512",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if cfg.Runtime.NodeEventLogSizeValue() != 100 {
		t.Fatalf("NodeEventLogSizeValue() = %d, want 100", cfg.Runtime.NodeEventLogSizeValue())
	}
	if cfg.Runtime.NodeBatchMaxItemsValue() != 500 {
		t.Fatalf("NodeBatchMaxItemsValue() = %d, want 500", cfg.Runtime.NodeBatchMaxItemsValue())
	}
	if cfg.Runtime.NodeIdempotencyTTLSeconds() != 10 {
		t.Fatalf("NodeIdempotencyTTLSeconds() = %d, want 10", cfg.Runtime.NodeIdempotencyTTLSeconds())
	}
	if cfg.Runtime.NodeTrafficReportIntervalSeconds() != 0 {
		t.Fatalf("NodeTrafficReportIntervalSeconds() = %d, want 0", cfg.Runtime.NodeTrafficReportIntervalSeconds())
	}
	if cfg.Runtime.NodeTrafficReportStepBytes() != 1024 {
		t.Fatalf("NodeTrafficReportStepBytes() = %d, want 1024", cfg.Runtime.NodeTrafficReportStepBytes())
	}
}

func TestDisconnectTimeoutAccessorNormalizesNonPositiveValues(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", "disconnect_timeout=0\n")
	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Current().Runtime.DisconnectTimeoutSeconds(); got != 30 {
		t.Fatalf("DisconnectTimeoutSeconds() with zero = %d, want 30", got)
	}

	path = writeConfig(t, "nps.conf", "disconnect_timeout=-5\n")
	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := Current().Runtime.DisconnectTimeoutSeconds(); got != 30 {
		t.Fatalf("DisconnectTimeoutSeconds() with negative = %d, want 30", got)
	}
}

func TestLoadLegacyNodeEventLogSizeAlias(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", "node_event_log_size=2048\n")
	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if cfg.Runtime.NodeEventLogSizeValue() != 2048 {
		t.Fatalf("NodeEventLogSizeValue() = %d, want 2048", cfg.Runtime.NodeEventLogSizeValue())
	}
}

func TestLoadStandaloneWebConfigAndAccessors(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"appname=nps-unit",
		"auth_key=auth-seed",
		"auth_crypt_key=crypt-seed",
		"web_username=admin",
		"web_password=secret",
		"web_standalone_allowed_origins=https://console.example.com, https://console.example.com:443, http://localhost:5173/ , *",
		"web_standalone_allow_credentials=true",
		"web_standalone_token_ttl_seconds=30",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if !cfg.StandaloneAllowCredentials() {
		t.Fatal("StandaloneAllowCredentials() = false, want true")
	}
	if cfg.StandaloneTokenTTLSecondsValue() != 60 {
		t.Fatalf("StandaloneTokenTTLSecondsValue() = %d, want 60", cfg.StandaloneTokenTTLSecondsValue())
	}
	if cfg.StandaloneTokenTTL() != time.Minute {
		t.Fatalf("StandaloneTokenTTL() = %v, want 1m", cfg.StandaloneTokenTTL())
	}
	if origins := cfg.StandaloneAllowedOrigins(); len(origins) != 3 {
		t.Fatalf("StandaloneAllowedOrigins() len = %d, want 3; got %v", len(origins), origins)
	}
	if !cfg.StandaloneAllowsAnyOrigin() {
		t.Fatal("StandaloneAllowsAnyOrigin() = false, want true")
	}
	if !cfg.StandaloneAllowsOrigin("https://console.example.com") {
		t.Fatal("StandaloneAllowsOrigin(https://console.example.com) = false, want true")
	}
	if !cfg.StandaloneAllowsOrigin("http://localhost:5173") {
		t.Fatal("StandaloneAllowsOrigin(http://localhost:5173) = false, want true")
	}
	if cfg.StandaloneTokenSecret() == "" {
		t.Fatal("StandaloneTokenSecret() = empty, want derived secret")
	}
}

func TestStandaloneTokenTTLAndOriginNormalization(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"web_standalone_allowed_origins=https://console.example.com:443,http://localhost:8080",
		"web_standalone_token_secret=explicit-secret",
		"web_standalone_token_ttl_seconds=999999999",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if cfg.StandaloneTokenTTLSecondsValue() != maxStandaloneTokenTTLSeconds {
		t.Fatalf("StandaloneTokenTTLSecondsValue() = %d, want %d", cfg.StandaloneTokenTTLSecondsValue(), maxStandaloneTokenTTLSeconds)
	}
	if cfg.StandaloneTokenSecret() != "explicit-secret" {
		t.Fatalf("StandaloneTokenSecret() = %q, want explicit-secret", cfg.StandaloneTokenSecret())
	}
	if !cfg.StandaloneAllowsOrigin("https://console.example.com") {
		t.Fatal("StandaloneAllowsOrigin() should normalize default https port")
	}
	if cfg.StandaloneAllowsOrigin("https://evil.example.com") {
		t.Fatal("StandaloneAllowsOrigin() = true for unexpected origin")
	}
}
