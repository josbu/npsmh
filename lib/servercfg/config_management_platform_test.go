package servercfg

import (
	"strings"
	"testing"
)

func TestLoadManagementPlatformsFromCSV(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"platform_ids=master-a,master-b",
		"platform_tokens=token-a,token-b",
		"platform_scopes=full,account",
		"platform_enabled=true,true",
		"platform_service_users=svc_a,svc_b",
		"platform_urls=https://a.example,https://b.example",
		"platform_connect_modes=direct,dual",
		"platform_reverse_ws_urls=,wss://b.example/node/ws",
		"platform_reverse_enabled=false,true",
		"platform_reverse_heartbeat_seconds=0,45",
		"platform_callback_urls=https://a.example/callback,https://b.example/callback",
		"platform_callback_enabled=true,false",
		"platform_callback_timeout_seconds=5,30",
		"platform_callback_retry_max=4,1",
		"platform_callback_retry_backoff_seconds=3,8",
		"platform_callback_queue_max=16,0",
		"platform_callback_signing_keys=sign-a,",
		"node_changes_window=2048",
		"node_batch_max_items=80",
		"node_idempotency_ttl_seconds=600",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if len(cfg.Runtime.ManagementPlatforms) != 2 {
		t.Fatalf("len(Current().Runtime.ManagementPlatforms) = %d, want 2", len(cfg.Runtime.ManagementPlatforms))
	}
	if cfg.Runtime.ManagementPlatforms[0].PlatformID != "master-a" || cfg.Runtime.ManagementPlatforms[0].ControlScope != "full" {
		t.Fatalf("first management platform = %+v", cfg.Runtime.ManagementPlatforms[0])
	}
	if cfg.Runtime.ManagementPlatforms[1].PlatformID != "master-b" || cfg.Runtime.ManagementPlatforms[1].ControlScope != "account" {
		t.Fatalf("second management platform = %+v", cfg.Runtime.ManagementPlatforms[1])
	}
	if cfg.Runtime.ManagementPlatforms[0].ConnectMode != "direct" || cfg.Runtime.ManagementPlatforms[0].ReverseEnabled {
		t.Fatalf("first management platform reverse config = %+v", cfg.Runtime.ManagementPlatforms[0])
	}
	if cfg.Runtime.ManagementPlatforms[1].ConnectMode != "dual" || !cfg.Runtime.ManagementPlatforms[1].ReverseEnabled || cfg.Runtime.ManagementPlatforms[1].ReverseWSURL != "wss://b.example/node/ws" || cfg.Runtime.ManagementPlatforms[1].ReverseHeartbeatSeconds != 45 {
		t.Fatalf("second management platform reverse config = %+v", cfg.Runtime.ManagementPlatforms[1])
	}
	if !cfg.Runtime.ManagementPlatforms[0].CallbackEnabled || cfg.Runtime.ManagementPlatforms[0].CallbackURL != "https://a.example/callback" || cfg.Runtime.ManagementPlatforms[0].CallbackTimeoutSeconds != 5 || cfg.Runtime.ManagementPlatforms[0].CallbackRetryMax != 4 || cfg.Runtime.ManagementPlatforms[0].CallbackRetryBackoffSec != 3 || cfg.Runtime.ManagementPlatforms[0].CallbackQueueMax != 16 || cfg.Runtime.ManagementPlatforms[0].CallbackSigningKey != "sign-a" {
		t.Fatalf("first management platform callback config = %+v", cfg.Runtime.ManagementPlatforms[0])
	}
	if cfg.Runtime.ManagementPlatforms[1].CallbackEnabled || cfg.Runtime.ManagementPlatforms[1].CallbackURL != "https://b.example/callback" || cfg.Runtime.ManagementPlatforms[1].CallbackTimeoutSeconds != 30 || cfg.Runtime.ManagementPlatforms[1].CallbackRetryMax != 1 || cfg.Runtime.ManagementPlatforms[1].CallbackRetryBackoffSec != 8 || cfg.Runtime.ManagementPlatforms[1].CallbackQueueMax != 0 || cfg.Runtime.ManagementPlatforms[1].CallbackSigningKey != "" {
		t.Fatalf("second management platform callback config = %+v", cfg.Runtime.ManagementPlatforms[1])
	}
	if cfg.Runtime.NodeToken != "token-a" {
		t.Fatalf("Current().Runtime.NodeToken = %q, want token-a", cfg.Runtime.NodeToken)
	}
	if !cfg.Runtime.HasReverseManagementPlatforms() {
		t.Fatalf("Current().Runtime.HasReverseManagementPlatforms() = false, want true")
	}
	if !cfg.Runtime.HasCallbackManagementPlatforms() {
		t.Fatalf("Current().Runtime.HasCallbackManagementPlatforms() = false, want true")
	}
	if cfg.Runtime.NodeEventLogSizeValue() != 2048 || cfg.Runtime.NodeBatchMaxItemsValue() != 80 || cfg.Runtime.NodeIdempotencyTTLSeconds() != 600 {
		t.Fatalf("runtime protocol tuning = %+v", cfg.Runtime)
	}
	if cfg.Runtime.NodeTrafficReportIntervalSeconds() != 0 || cfg.Runtime.NodeTrafficReportStepBytes() != 0 {
		t.Fatalf("traffic report tuning = %+v", cfg.Runtime)
	}
}

func TestLoadManagementPlatformsFromManagementPlatformAliases(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"management_platform_ids=master-a,master-b",
		"management_platform_tokens=token-a,token-b",
		"management_platform_control_scopes=full,account",
		"management_platform_enabled=true,true",
		"management_platform_service_usernames=svc_a,svc_b",
		"management_platform_urls=https://a.example,https://b.example",
		"management_platform_connect_modes=direct,dual",
		"management_platform_reverse_ws_urls=,wss://b.example/node/ws",
		"management_platform_reverse_enabled=false,true",
		"management_platform_reverse_heartbeat_seconds=0,45",
		"management_platform_callback_urls=https://a.example/callback,https://b.example/callback",
		"management_platform_callback_enabled=true,false",
		"management_platform_callback_timeout_seconds=5,30",
		"management_platform_callback_retry_max=4,1",
		"management_platform_callback_retry_backoff_seconds=3,8",
		"management_platform_callback_queue_max=16,0",
		"management_platform_callback_signing_keys=sign-a,",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if len(cfg.Runtime.ManagementPlatforms) != 2 {
		t.Fatalf("len(Current().Runtime.ManagementPlatforms) = %d, want 2", len(cfg.Runtime.ManagementPlatforms))
	}
	if cfg.Runtime.ManagementPlatforms[0].PlatformID != "master-a" || cfg.Runtime.ManagementPlatforms[0].ControlScope != "full" {
		t.Fatalf("first aliased management platform = %+v", cfg.Runtime.ManagementPlatforms[0])
	}
	if cfg.Runtime.ManagementPlatforms[1].PlatformID != "master-b" || cfg.Runtime.ManagementPlatforms[1].ControlScope != "account" {
		t.Fatalf("second aliased management platform = %+v", cfg.Runtime.ManagementPlatforms[1])
	}
	if !cfg.Runtime.ManagementPlatforms[0].CallbackEnabled || cfg.Runtime.ManagementPlatforms[0].CallbackURL != "https://a.example/callback" {
		t.Fatalf("first aliased callback config = %+v", cfg.Runtime.ManagementPlatforms[0])
	}
	if cfg.Runtime.ManagementPlatforms[1].ReverseWSURL != "wss://b.example/node/ws" || !cfg.Runtime.ManagementPlatforms[1].ReverseEnabled {
		t.Fatalf("second aliased reverse config = %+v", cfg.Runtime.ManagementPlatforms[1])
	}
}

func TestLoadLegacyNodeTokenSynthesizesManagementPlatform(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"master_url=https://master.internal",
		"node_token=legacy-secret",
	}, "\n")+"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if len(cfg.Runtime.ManagementPlatforms) != 1 {
		t.Fatalf("len(Current().Runtime.ManagementPlatforms) = %d, want 1", len(cfg.Runtime.ManagementPlatforms))
	}
	platform := cfg.Runtime.ManagementPlatforms[0]
	if platform.PlatformID != "legacy-master" || platform.Token != "legacy-secret" || platform.ControlScope != "full" {
		t.Fatalf("legacy management platform = %+v", platform)
	}
	if platform.ServiceUsername != DefaultPlatformServiceUsername("legacy-master") {
		t.Fatalf("legacy service username = %q, want %q", platform.ServiceUsername, DefaultPlatformServiceUsername("legacy-master"))
	}
	if platform.ConnectMode != "direct" || platform.ReverseEnabled || platform.ReverseWSURL != "" {
		t.Fatalf("legacy management platform reverse config = %+v", platform)
	}
}

func TestLoadManagementPlatformsFromStructuredConfig(t *testing.T) {
	resetTestState(t)

	path := writeConfig(t, "nps.json", `{
  "management_platforms": [
    {
      "platform_id": "master-a",
      "token": "token-a",
      "control_scope": "full",
      "enabled": true,
      "service_username": "svc_a",
      "master_url": "https://a.example",
      "connect_mode": "dual",
      "reverse_ws_url": "wss://a.example/reverse",
      "reverse_enabled": true,
      "reverse_heartbeat_seconds": 55
    },
    {
      "platform_id": "master-b",
      "token": "token-b",
      "control_scope": "account",
      "enabled": true,
      "service_username": "svc_b",
      "master_url": "https://b.example"
    }
  ]
}`)

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := Current()
	if len(cfg.Runtime.ManagementPlatforms) != 2 {
		t.Fatalf("len(Current().Runtime.ManagementPlatforms) = %d, want 2", len(cfg.Runtime.ManagementPlatforms))
	}
	if cfg.Runtime.ManagementPlatforms[0].ConnectMode != "dual" || !cfg.Runtime.ManagementPlatforms[0].SupportsReverse() {
		t.Fatalf("first structured management platform = %+v", cfg.Runtime.ManagementPlatforms[0])
	}
	if cfg.Runtime.ManagementPlatforms[1].ConnectMode != "direct" || cfg.Runtime.ManagementPlatforms[1].SupportsReverse() {
		t.Fatalf("second structured management platform = %+v", cfg.Runtime.ManagementPlatforms[1])
	}
	if !containsString(NodeCapabilities(cfg), "node.api.ws_reverse") {
		t.Fatalf("NodeCapabilities() = %v, want node.api.ws_reverse", NodeCapabilities(cfg))
	}
	if !containsString(NodeCapabilities(cfg), "node.api.changes_durable") {
		t.Fatalf("NodeCapabilities() = %v, want node.api.changes_durable", NodeCapabilities(cfg))
	}
	if !containsString(NodeCapabilities(cfg), "node.api.traffic_events") {
		t.Fatalf("NodeCapabilities() = %v, want node.api.traffic_events", NodeCapabilities(cfg))
	}
}

func TestRuntimeManagementPlatformAccessorsIgnoreInvalidEntriesWithoutAllocatingEnabledSlices(t *testing.T) {
	cfg := RuntimeConfig{
		ManagementPlatforms: []ManagementPlatformConfig{
			{PlatformID: "disabled", Token: "token-disabled", Enabled: false, CallbackEnabled: true, CallbackURL: "https://disabled.example/callback"},
			{PlatformID: "  ", Token: "token-empty-id", Enabled: true},
			{PlatformID: "missing-token", Token: "   ", Enabled: true},
			{PlatformID: "direct-only", Token: "token-direct", Enabled: true, ConnectMode: "direct"},
			{PlatformID: "callback", Token: "token-callback", Enabled: true, CallbackEnabled: true, CallbackURL: "https://callback.example/hook"},
			{PlatformID: "reverse", Token: "token-reverse", Enabled: true, ConnectMode: "reverse", ReverseEnabled: true, ReverseWSURL: "wss://reverse.example/ws"},
		},
	}

	if !cfg.HasManagementPlatforms() {
		t.Fatal("HasManagementPlatforms() = false, want true")
	}
	if !cfg.HasCallbackManagementPlatforms() {
		t.Fatal("HasCallbackManagementPlatforms() = false, want true")
	}
	if !cfg.HasReverseManagementPlatforms() {
		t.Fatal("HasReverseManagementPlatforms() = false, want true")
	}
	if platform, ok := cfg.FindManagementPlatformByToken("token-callback"); !ok || platform.PlatformID != "callback" {
		t.Fatalf("FindManagementPlatformByToken(token-callback) = %+v, %t, want callback/true", platform, ok)
	}
	if _, ok := cfg.FindManagementPlatformByToken("token-disabled"); ok {
		t.Fatal("FindManagementPlatformByToken(token-disabled) = true, want false for disabled entry")
	}
	if _, ok := cfg.FindManagementPlatformByToken("token-empty-id"); ok {
		t.Fatal("FindManagementPlatformByToken(token-empty-id) = true, want false for invalid platform id")
	}
	if _, ok := cfg.FindManagementPlatformByToken("missing"); ok {
		t.Fatal("FindManagementPlatformByToken(missing) = true, want false")
	}
}

func TestNodeCapabilitiesIgnoreInvalidManagementPlatformEntriesAndDeduplicateScopes(t *testing.T) {
	cfg := &Snapshot{
		Runtime: RuntimeConfig{
			ManagementPlatforms: []ManagementPlatformConfig{
				{PlatformID: "full-a", Token: "token-full-a", Enabled: true, ControlScope: "full"},
				{PlatformID: "full-b", Token: "token-full-b", Enabled: true, ControlScope: "full", ConnectMode: "reverse", ReverseEnabled: true, ReverseWSURL: "wss://full-b.example/ws"},
				{PlatformID: "account-a", Token: "token-account-a", Enabled: true, ControlScope: "account", CallbackEnabled: true, CallbackURL: "https://account-a.example/callback"},
				{PlatformID: "disabled", Token: "token-disabled", Enabled: false, ControlScope: "account", CallbackEnabled: true, CallbackURL: "https://disabled.example/callback"},
				{PlatformID: " ", Token: "token-empty", Enabled: true, ControlScope: "account"},
			},
		},
	}

	capabilities := NodeCapabilities(cfg)
	if !containsString(capabilities, "node.api.ws_reverse") || !containsString(capabilities, "node.api.ws_reverse_resume") {
		t.Fatalf("NodeCapabilities() = %v, want reverse capabilities", capabilities)
	}
	if !containsString(capabilities, "node.api.callbacks") || !containsString(capabilities, "node.api.callbacks_queue") {
		t.Fatalf("NodeCapabilities() = %v, want callback capabilities", capabilities)
	}
	if countString(capabilities, "node.platform.full_scope") != 1 {
		t.Fatalf("NodeCapabilities() full scope count = %d, want 1 (%v)", countString(capabilities, "node.platform.full_scope"), capabilities)
	}
	if countString(capabilities, "node.platform.account_scope") != 1 {
		t.Fatalf("NodeCapabilities() account scope count = %d, want 1 (%v)", countString(capabilities, "node.platform.account_scope"), capabilities)
	}
}

func TestRuntimeManagementPlatformSpecificListsIgnoreInvalidEntries(t *testing.T) {
	cfg := RuntimeConfig{
		ManagementPlatforms: []ManagementPlatformConfig{
			{PlatformID: "reverse", Token: "token-reverse", Enabled: true, ConnectMode: "reverse", ReverseEnabled: true, ReverseWSURL: "wss://reverse.example/ws"},
			{PlatformID: "callback", Token: "token-callback", Enabled: true, CallbackEnabled: true, CallbackURL: "https://callback.example/hook"},
			{PlatformID: "dual", Token: "token-dual", Enabled: true, ConnectMode: "dual", ReverseEnabled: true, ReverseWSURL: "wss://dual.example/ws", CallbackEnabled: true, CallbackURL: "https://dual.example/hook"},
			{PlatformID: "missing-token", Token: "   ", Enabled: true, ConnectMode: "dual", ReverseEnabled: true, ReverseWSURL: "wss://missing-token.example/ws", CallbackEnabled: true, CallbackURL: "https://missing-token.example/hook"},
			{PlatformID: "disabled", Token: "token-disabled", Enabled: false, ConnectMode: "dual", ReverseEnabled: true, ReverseWSURL: "wss://disabled.example/ws", CallbackEnabled: true, CallbackURL: "https://disabled.example/hook"},
		},
	}

	reversePlatforms := cfg.EnabledReverseManagementPlatforms()
	if len(reversePlatforms) != 2 || reversePlatforms[0].PlatformID != "reverse" || reversePlatforms[1].PlatformID != "dual" {
		t.Fatalf("EnabledReverseManagementPlatforms() = %+v, want [reverse dual]", reversePlatforms)
	}

	callbackPlatforms := cfg.EnabledCallbackManagementPlatforms()
	if len(callbackPlatforms) != 2 || callbackPlatforms[0].PlatformID != "callback" || callbackPlatforms[1].PlatformID != "dual" {
		t.Fatalf("EnabledCallbackManagementPlatforms() = %+v, want [callback dual]", callbackPlatforms)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func countString(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}
