package servercfg

import (
	"sort"
	"strings"
)

func (cfg RuntimeConfig) DisconnectTimeoutSeconds() int {
	if cfg.DisconnectTimeout <= 0 {
		return 30
	}
	return cfg.DisconnectTimeout
}

func (cfg RuntimeConfig) NodeEventLogSizeValue() int {
	return normalizeRuntimeLimit(cfg.NodeEventLogSize, 1024, 100, 100000)
}

func (cfg RuntimeConfig) NodeBatchMaxItemsValue() int {
	return normalizeRuntimeLimit(cfg.NodeBatchMaxItems, 50, 1, 500)
}

func (cfg RuntimeConfig) NodeIdempotencyTTLSeconds() int {
	return normalizeRuntimeLimit(cfg.NodeIdempotencyTTL, 300, 10, 86400)
}

func (cfg RuntimeConfig) NodeTrafficReportIntervalSeconds() int {
	switch {
	case cfg.NodeTrafficReportInterval < 0:
		return 0
	case cfg.NodeTrafficReportInterval == 0:
		return 0
	default:
		return normalizeRuntimeLimit(cfg.NodeTrafficReportInterval, 1, 1, 3600)
	}
}

func (cfg RuntimeConfig) NodeTrafficReportStepBytes() int64 {
	switch {
	case cfg.NodeTrafficReportStep < 0:
		return 0
	case cfg.NodeTrafficReportStep == 0:
		return 0
	case cfg.NodeTrafficReportStep < 1024:
		return 1024
	case cfg.NodeTrafficReportStep > 1<<40:
		return 1 << 40
	default:
		return cfg.NodeTrafficReportStep
	}
}

func normalizeRuntimeLimit(value, fallback, min, max int) int {
	if value <= 0 {
		value = fallback
	}
	if min > 0 && value < min {
		return min
	}
	if max > 0 && value > max {
		return max
	}
	return value
}

func (cfg RuntimeConfig) EnabledManagementPlatforms() []ManagementPlatformConfig {
	items := make([]ManagementPlatformConfig, 0, len(cfg.ManagementPlatforms))
	for _, item := range cfg.ManagementPlatforms {
		if isEnabledManagementPlatform(item) {
			items = append(items, item)
		}
	}
	return items
}

func (cfg RuntimeConfig) EnabledReverseManagementPlatforms() []ManagementPlatformConfig {
	items := make([]ManagementPlatformConfig, 0, len(cfg.ManagementPlatforms))
	for _, item := range cfg.ManagementPlatforms {
		if isEnabledManagementPlatform(item) && item.SupportsReverse() {
			items = append(items, item)
		}
	}
	return items
}

func (cfg RuntimeConfig) EnabledCallbackManagementPlatforms() []ManagementPlatformConfig {
	items := make([]ManagementPlatformConfig, 0, len(cfg.ManagementPlatforms))
	for _, item := range cfg.ManagementPlatforms {
		if isEnabledManagementPlatform(item) && item.SupportsCallback() {
			items = append(items, item)
		}
	}
	return items
}

func (cfg RuntimeConfig) HasReverseManagementPlatforms() bool {
	for _, item := range cfg.ManagementPlatforms {
		if isEnabledManagementPlatform(item) && item.SupportsReverse() {
			return true
		}
	}
	return false
}

func (cfg RuntimeConfig) HasCallbackManagementPlatforms() bool {
	for _, item := range cfg.ManagementPlatforms {
		if isEnabledManagementPlatform(item) && item.SupportsCallback() {
			return true
		}
	}
	return false
}

func (cfg RuntimeConfig) HasManagementPlatforms() bool {
	for _, item := range cfg.ManagementPlatforms {
		if isEnabledManagementPlatform(item) {
			return true
		}
	}
	return false
}

func (cfg RuntimeConfig) FindManagementPlatformByToken(token string) (ManagementPlatformConfig, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ManagementPlatformConfig{}, false
	}
	for _, item := range cfg.ManagementPlatforms {
		if !isEnabledManagementPlatform(item) {
			continue
		}
		if item.Token == token {
			return item, true
		}
	}
	return ManagementPlatformConfig{}, false
}

func isEnabledManagementPlatform(item ManagementPlatformConfig) bool {
	return item.Enabled && strings.TrimSpace(item.PlatformID) != "" && strings.TrimSpace(item.Token) != ""
}

func (cfg ManagementPlatformConfig) SupportsDirect() bool {
	switch strings.ToLower(strings.TrimSpace(cfg.ConnectMode)) {
	case "", "direct", "dual":
		return true
	default:
		return false
	}
}

func (cfg ManagementPlatformConfig) SupportsReverse() bool {
	if !cfg.Enabled || !cfg.ReverseEnabled || strings.TrimSpace(cfg.ReverseWSURL) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(cfg.ConnectMode)) {
	case "reverse", "dual":
		return true
	default:
		return false
	}
}

func (cfg ManagementPlatformConfig) SupportsCallback() bool {
	return cfg.Enabled && cfg.CallbackEnabled && strings.TrimSpace(cfg.CallbackURL) != ""
}

func normalizeCallbackRetryMax(value int) int {
	switch {
	case value < 0:
		return 2
	case value > 10:
		return 10
	default:
		return value
	}
}

func normalizeCallbackRetryBackoffSeconds(value int) int {
	return normalizeRuntimeLimit(value, 2, 1, 300)
}

func normalizeCallbackQueueMax(value int) int {
	switch {
	case value < 0:
		return 100
	case value > 10000:
		return 10000
	default:
		return value
	}
}

func defaultPlatformServiceUsername(platformID string) string {
	platformID = strings.TrimSpace(strings.ToLower(platformID))
	if platformID == "" {
		platformID = "platform"
	}
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_")
	return "__platform_" + replacer.Replace(platformID)
}

func DefaultPlatformServiceUsername(platformID string) string {
	return defaultPlatformServiceUsername(platformID)
}

var baseNodeCapabilities = []string{
	"node.local_store",
	"node.local_truth",
	"node.management.multi_platform",
	"node.api.status",
	"node.api.manage",
	"node.api.changes",
	"node.api.changes_durable",
	"node.api.usage_snapshot",
	"node.api.config_snapshot",
	"node.api.batch",
	"node.api.idempotency",
	"node.api.traffic_events",
	"node.api.sync_local_reload",
	"node.api.kick",
	"node.api.ws",
	"node.api.ws_events",
	"node.api.ws_request",
	"node.api.ws_subscriptions",
	"node.api.webhooks",
	"node.api.event_templates",
	"node.api.boot_session",
	"node.client.owner_user",
	"node.client.unowned",
	"node.client.manager_acl",
	"node.client.source_metadata",
	"node.client.revision",
	"node.user.platform_service",
	"node.user.hidden_service",
	"node.user.local_auth_only",
	"node.migration.legacy_client_auth",
}

func NodeCapabilities(cfg *Snapshot) []string {
	capabilities := append([]string(nil), baseNodeCapabilities...)
	if cfg == nil {
		return normalizeNodeCapabilities(capabilities)
	}
	capabilities = appendNodeFeatureCapabilities(capabilities, cfg.Feature)
	capabilities = appendNodeRuntimeCapabilities(capabilities, cfg.Runtime)
	return normalizeNodeCapabilities(capabilities)
}

func appendNodeFeatureCapabilities(capabilities []string, feature FeatureConfig) []string {
	if feature.AllowFlowLimit {
		capabilities = append(capabilities, "node.limit.flow")
	}
	if feature.AllowRateLimit {
		capabilities = append(capabilities, "node.limit.rate")
	}
	if feature.AllowTimeLimit {
		capabilities = append(capabilities, "node.limit.expire")
	}
	if feature.AllowConnectionNumLimit {
		capabilities = append(capabilities, "node.limit.connections")
	}
	if feature.AllowTunnelNumLimit {
		capabilities = append(capabilities, "node.limit.tunnels")
	}
	return capabilities
}

func appendNodeRuntimeCapabilities(capabilities []string, runtime RuntimeConfig) []string {
	hasReverse := false
	hasCallback := false
	for _, platform := range runtime.ManagementPlatforms {
		if !isEnabledManagementPlatform(platform) {
			continue
		}
		if platform.SupportsReverse() {
			hasReverse = true
		}
		if platform.SupportsCallback() {
			hasCallback = true
		}
		if strings.EqualFold(strings.TrimSpace(platform.ControlScope), "account") {
			capabilities = append(capabilities, "node.platform.account_scope")
			continue
		}
		capabilities = append(capabilities, "node.platform.full_scope")
	}
	if hasReverse {
		capabilities = append(capabilities, "node.api.ws_reverse", "node.api.ws_reverse_resume")
	}
	if hasCallback {
		capabilities = append(capabilities, "node.api.callbacks", "node.api.callbacks_queue")
	}
	return capabilities
}

func normalizeNodeCapabilities(capabilities []string) []string {
	seen := make(map[string]struct{}, len(capabilities))
	normalized := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		normalized = append(normalized, capability)
	}
	sort.Strings(normalized)
	return normalized
}
