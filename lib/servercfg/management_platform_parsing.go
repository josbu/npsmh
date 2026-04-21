package servercfg

import (
	"strconv"
	"strings"
)

func splitAndTrimCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func splitCSVPreserveEmpty(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		items = append(items, strings.TrimSpace(part))
	}
	return items
}

func portString(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}

func parseManagementPlatforms(r valueReader) []ManagementPlatformConfig {
	items := make([]ManagementPlatformConfig, 0)
	if raw, ok := r.rawValue("management_platforms", "platforms"); ok {
		switch typed := raw.(type) {
		case []any:
			for _, item := range typed {
				if cfg, ok := parseManagementPlatformItem(item); ok {
					items = append(items, cfg)
				}
			}
		case []map[string]any:
			for _, item := range typed {
				if cfg, ok := parseManagementPlatformItem(item); ok {
					items = append(items, cfg)
				}
			}
		}
	}
	if len(items) > 0 {
		return normalizeManagementPlatforms(items)
	}

	ids := splitCSVPreserveEmpty(r.stringValue("platform_ids", "management_platform_ids"))
	tokens := splitCSVPreserveEmpty(r.stringValue("platform_tokens", "management_platform_tokens"))
	if len(ids) == 0 || len(tokens) == 0 {
		return nil
	}
	scopes := splitCSVPreserveEmpty(r.stringValue("platform_scopes", "management_platform_control_scopes"))
	enabledValues := splitCSVPreserveEmpty(r.stringValue("platform_enabled", "management_platform_enabled"))
	serviceUsernames := splitCSVPreserveEmpty(r.stringValue("platform_service_users", "management_platform_service_usernames"))
	masterURLs := splitCSVPreserveEmpty(r.stringValue("platform_urls", "management_platform_urls"))
	connectModes := splitCSVPreserveEmpty(r.stringValue("platform_connect_modes", "management_platform_connect_modes"))
	reverseWSURLs := splitCSVPreserveEmpty(r.stringValue("platform_reverse_ws_urls", "management_platform_reverse_ws_urls"))
	reverseEnabledValues := splitCSVPreserveEmpty(r.stringValue("platform_reverse_enabled", "management_platform_reverse_enabled"))
	reverseHeartbeatValues := splitCSVPreserveEmpty(r.stringValue("platform_reverse_heartbeat_seconds", "management_platform_reverse_heartbeat_seconds"))
	callbackURLs := splitCSVPreserveEmpty(r.stringValue("platform_callback_urls", "management_platform_callback_urls"))
	callbackEnabledValues := splitCSVPreserveEmpty(r.stringValue("platform_callback_enabled", "management_platform_callback_enabled"))
	callbackTimeoutValues := splitCSVPreserveEmpty(r.stringValue("platform_callback_timeout_seconds", "management_platform_callback_timeout_seconds"))
	callbackRetryMaxValues := splitCSVPreserveEmpty(r.stringValue("platform_callback_retry_max", "management_platform_callback_retry_max"))
	callbackRetryBackoffValues := splitCSVPreserveEmpty(r.stringValue("platform_callback_retry_backoff_seconds", "management_platform_callback_retry_backoff_seconds"))
	callbackQueueMaxValues := splitCSVPreserveEmpty(r.stringValue("platform_callback_queue_max", "management_platform_callback_queue_max"))
	callbackSigningKeys := splitCSVPreserveEmpty(r.stringValue("platform_callback_signing_keys", "management_platform_callback_signing_keys"))
	limit := len(ids)
	if len(tokens) < limit {
		limit = len(tokens)
	}
	for index := 0; index < limit; index++ {
		cfg := ManagementPlatformConfig{
			PlatformID:      ids[index],
			Token:           tokens[index],
			ControlScope:    sliceString(scopes, index),
			Enabled:         sliceBool(enabledValues, index, true),
			ServiceUsername: sliceString(serviceUsernames, index),
			MasterURL:       sliceString(masterURLs, index),
			ConnectMode:     sliceString(connectModes, index),
			ReverseWSURL:    sliceString(reverseWSURLs, index),
			ReverseEnabled:  sliceBool(reverseEnabledValues, index, false),
			ReverseHeartbeatSeconds: sliceInt(
				reverseHeartbeatValues,
				index,
				30,
			),
			CallbackURL:     sliceString(callbackURLs, index),
			CallbackEnabled: sliceBool(callbackEnabledValues, index, false),
			CallbackTimeoutSeconds: sliceInt(
				callbackTimeoutValues,
				index,
				10,
			),
			CallbackRetryMax: normalizeCallbackRetryMax(sliceInt(
				callbackRetryMaxValues,
				index,
				2,
			)),
			CallbackRetryBackoffSec: normalizeCallbackRetryBackoffSeconds(sliceInt(
				callbackRetryBackoffValues,
				index,
				2,
			)),
			CallbackQueueMax: normalizeCallbackQueueMax(sliceInt(
				callbackQueueMaxValues,
				index,
				100,
			)),
			CallbackSigningKey: strings.TrimSpace(sliceString(callbackSigningKeys, index)),
		}
		items = append(items, cfg)
	}
	return normalizeManagementPlatforms(items)
}

func parseManagementPlatformItem(value any) (ManagementPlatformConfig, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return ManagementPlatformConfig{}, false
	}
	cfg := ManagementPlatformConfig{
		PlatformID:              strings.TrimSpace(stringifyValue(item["platform_id"])),
		Token:                   strings.TrimSpace(stringifyValue(item["token"])),
		ControlScope:            strings.TrimSpace(stringifyValue(item["control_scope"])),
		Enabled:                 true,
		ServiceUsername:         strings.TrimSpace(stringifyValue(item["service_username"])),
		MasterURL:               strings.TrimSpace(stringifyValue(item["master_url"])),
		ConnectMode:             strings.TrimSpace(stringifyValue(item["connect_mode"])),
		ReverseWSURL:            strings.TrimSpace(stringifyValue(item["reverse_ws_url"])),
		CallbackURL:             strings.TrimSpace(stringifyValue(item["callback_url"])),
		ReverseHeartbeatSeconds: 30,
		CallbackTimeoutSeconds:  10,
		CallbackRetryMax:        2,
		CallbackRetryBackoffSec: 2,
		CallbackQueueMax:        100,
		CallbackSigningKey:      strings.TrimSpace(stringifyValue(item["callback_signing_key"])),
	}
	if value, ok := item["enabled"]; ok {
		if parsed, err := toBool(value); err == nil {
			cfg.Enabled = parsed
		}
	}
	if value, ok := item["reverse_enabled"]; ok {
		if parsed, err := toBool(value); err == nil {
			cfg.ReverseEnabled = parsed
		}
	}
	if value, ok := item["reverse_heartbeat_seconds"]; ok {
		if parsed, err := toInt(value); err == nil {
			cfg.ReverseHeartbeatSeconds = parsed
		}
	}
	if value, ok := item["callback_enabled"]; ok {
		if parsed, err := toBool(value); err == nil {
			cfg.CallbackEnabled = parsed
		}
	}
	if value, ok := item["callback_timeout_seconds"]; ok {
		if parsed, err := toInt(value); err == nil {
			cfg.CallbackTimeoutSeconds = parsed
		}
	}
	if value, ok := item["callback_retry_max"]; ok {
		if parsed, err := toInt(value); err == nil {
			cfg.CallbackRetryMax = parsed
		}
	}
	if value, ok := item["callback_retry_backoff_seconds"]; ok {
		if parsed, err := toInt(value); err == nil {
			cfg.CallbackRetryBackoffSec = parsed
		}
	}
	if value, ok := item["callback_queue_max"]; ok {
		if parsed, err := toInt(value); err == nil {
			cfg.CallbackQueueMax = parsed
		}
	}
	if cfg.PlatformID == "" || cfg.Token == "" {
		return ManagementPlatformConfig{}, false
	}
	return normalizeManagementPlatform(cfg), true
}

func normalizeManagementPlatforms(items []ManagementPlatformConfig) []ManagementPlatformConfig {
	normalized := make([]ManagementPlatformConfig, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = normalizeManagementPlatform(item)
		if item.PlatformID == "" || item.Token == "" || !item.Enabled {
			continue
		}
		if _, ok := seen[item.PlatformID]; ok {
			continue
		}
		seen[item.PlatformID] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized
}

func normalizeManagementPlatform(item ManagementPlatformConfig) ManagementPlatformConfig {
	item.PlatformID = strings.TrimSpace(item.PlatformID)
	item.Token = strings.TrimSpace(item.Token)
	item.ControlScope = strings.ToLower(strings.TrimSpace(item.ControlScope))
	if item.ControlScope != "account" {
		item.ControlScope = "full"
	}
	item.ServiceUsername = strings.TrimSpace(item.ServiceUsername)
	if item.ServiceUsername == "" {
		item.ServiceUsername = defaultPlatformServiceUsername(item.PlatformID)
	}
	item.MasterURL = strings.TrimSpace(item.MasterURL)
	item.ConnectMode = normalizeManagementPlatformConnectMode(item.ConnectMode, item.ReverseWSURL, item.ReverseEnabled)
	item.ReverseWSURL = strings.TrimSpace(item.ReverseWSURL)
	if item.ReverseHeartbeatSeconds <= 0 {
		item.ReverseHeartbeatSeconds = 30
	}
	item.CallbackURL = strings.TrimSpace(item.CallbackURL)
	if item.CallbackTimeoutSeconds <= 0 {
		item.CallbackTimeoutSeconds = 10
	}
	item.CallbackRetryMax = normalizeCallbackRetryMax(item.CallbackRetryMax)
	item.CallbackRetryBackoffSec = normalizeCallbackRetryBackoffSeconds(item.CallbackRetryBackoffSec)
	item.CallbackQueueMax = normalizeCallbackQueueMax(item.CallbackQueueMax)
	item.CallbackSigningKey = strings.TrimSpace(item.CallbackSigningKey)
	if item.CallbackURL == "" {
		item.CallbackEnabled = false
	}
	switch item.ConnectMode {
	case "reverse", "dual":
		if item.ReverseWSURL == "" {
			item.ConnectMode = "direct"
			item.ReverseEnabled = false
		} else if !item.ReverseEnabled {
			item.ReverseEnabled = true
		}
	default:
		item.ReverseEnabled = false
	}
	return item
}

func normalizeManagementPlatformConnectMode(mode, reverseWSURL string, reverseEnabled bool) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "direct", "reverse", "dual":
		return mode
	}
	if reverseEnabled || strings.TrimSpace(reverseWSURL) != "" {
		return "dual"
	}
	return "direct"
}

func sliceString(values []string, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return values[index]
}

func sliceBool(values []string, index int, defaultValue bool) bool {
	if index < 0 || index >= len(values) {
		return defaultValue
	}
	value := strings.TrimSpace(values[index])
	if value == "" {
		return defaultValue
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return defaultValue
	}
}

func sliceInt(values []string, index int, defaultValue int) int {
	if index < 0 || index >= len(values) {
		return defaultValue
	}
	value := strings.TrimSpace(values[index])
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}
