package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/config"
	"github.com/djylb/nps/lib/crypt"
)

func normalizeLaunchContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func fetchLaunchPayload(ctx context.Context, rawURL string) (string, error) {
	ctx = normalizeLaunchContext(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "npc-launch/1")
	httpClient := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &LaunchSourceError{Source: rawURL, Temporary: true, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		sourceErr := &LaunchSourceError{
			Source:     rawURL,
			StatusCode: resp.StatusCode,
			RetryAfter: parseLaunchRetryAfter(resp.Header.Get("Retry-After")),
			Err:        fmt.Errorf("launch url returned status %s", resp.Status),
		}
		switch {
		case resp.StatusCode == http.StatusGone:
			sourceErr.Revoked = true
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode >= 500:
			sourceErr.Temporary = true
		}
		return "", sourceErr
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", &LaunchSourceError{Source: rawURL, Temporary: true, Err: err}
	}
	return strings.TrimSpace(string(body)), nil
}

func parseLaunchRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		wait := time.Until(when)
		if wait > 0 {
			return wait
		}
	}
	return 0
}

func readLaunchPayloadFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("launch file path is empty")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(body) > 1<<20 {
		return "", fmt.Errorf("launch file is too large")
	}
	return strings.TrimSpace(string(body)), nil
}

func decodeLaunchPayload(raw string) (string, bool) {
	normalized := normalizeBase64(raw)
	if normalized != "" {
		if decrypted, err := crypt.DecryptStringWithPrivateKey(normalized); err == nil {
			decrypted = strings.TrimSpace(decrypted)
			if decrypted != "" && decrypted != raw {
				return decrypted, true
			}
		}
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		if normalized == "" {
			break
		}
		decoded, err := encoding.DecodeString(normalized)
		if err != nil || !utf8.Valid(decoded) {
			continue
		}
		text := strings.TrimSpace(string(decoded))
		if text != "" && text != raw {
			return text, true
		}
	}
	return "", false
}

func overlayRuntimeOnConfig(runtime *LaunchRuntime, cfg *config.Config) {
	if runtime == nil || cfg == nil || cfg.CommonConfig == nil {
		return
	}
	if runtime.DNSServer != nil {
		cfg.CommonConfig.DnsServer = strings.TrimSpace(*runtime.DNSServer)
	}
	if runtime.NTPServer != nil {
		cfg.CommonConfig.NtpServer = strings.TrimSpace(*runtime.NTPServer)
	}
	if runtime.NTPInterval != nil {
		cfg.CommonConfig.NtpInterval = *runtime.NTPInterval
	}
	if runtime.DisconnectTimeout != nil {
		cfg.CommonConfig.DisconnectTime = *runtime.DisconnectTimeout
	}
	if runtime.AutoReconnect != nil {
		cfg.CommonConfig.AutoReconnection = *runtime.AutoReconnect
	}
}

func mergeLaunchRuntime(current, overlay *LaunchRuntime) *LaunchRuntime {
	if current == nil {
		return overlay
	}
	if overlay == nil {
		return current
	}
	merged := *current
	if overlay.Debug != nil {
		merged.Debug = overlay.Debug
	}
	if overlay.Log != nil {
		merged.Log = overlay.Log
	}
	if overlay.LogLevel != nil {
		merged.LogLevel = overlay.LogLevel
	}
	if overlay.LogPath != nil {
		merged.LogPath = overlay.LogPath
	}
	if overlay.LogMaxSize != nil {
		merged.LogMaxSize = overlay.LogMaxSize
	}
	if overlay.LogMaxDays != nil {
		merged.LogMaxDays = overlay.LogMaxDays
	}
	if overlay.LogMaxFiles != nil {
		merged.LogMaxFiles = overlay.LogMaxFiles
	}
	if overlay.LogCompress != nil {
		merged.LogCompress = overlay.LogCompress
	}
	if overlay.LogColor != nil {
		merged.LogColor = overlay.LogColor
	}
	if overlay.PProf != nil {
		merged.PProf = overlay.PProf
	}
	if overlay.ProtoVersion != nil {
		merged.ProtoVersion = overlay.ProtoVersion
	}
	if overlay.SkipVerify != nil {
		merged.SkipVerify = overlay.SkipVerify
	}
	if overlay.KeepAlive != nil {
		merged.KeepAlive = overlay.KeepAlive
	}
	if overlay.DNSServer != nil {
		merged.DNSServer = overlay.DNSServer
	}
	if overlay.NTPServer != nil {
		merged.NTPServer = overlay.NTPServer
	}
	if overlay.NTPInterval != nil {
		merged.NTPInterval = overlay.NTPInterval
	}
	if overlay.Timezone != nil {
		merged.Timezone = overlay.Timezone
	}
	if overlay.DisableP2P != nil {
		merged.DisableP2P = overlay.DisableP2P
	}
	if overlay.P2PType != nil {
		merged.P2PType = overlay.P2PType
	}
	if overlay.LocalIPForward != nil {
		merged.LocalIPForward = overlay.LocalIPForward
	}
	if overlay.AutoReconnect != nil {
		merged.AutoReconnect = overlay.AutoReconnect
	}
	if overlay.DisconnectTimeout != nil {
		merged.DisconnectTimeout = overlay.DisconnectTimeout
	}
	if overlay.P2PTimeout != nil {
		merged.P2PTimeout = overlay.P2PTimeout
	}
	return &merged
}

func mergeCompatibleLaunchRuntime(current, overlay *LaunchRuntime) (*LaunchRuntime, error) {
	if current == nil {
		return overlay, nil
	}
	if overlay == nil {
		return current, nil
	}
	merged := *current
	if err := mergeRuntimeBoolField(&merged.Debug, current.Debug, overlay.Debug, "debug"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.Log, current.Log, overlay.Log, "log"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.LogLevel, current.LogLevel, overlay.LogLevel, "log_level"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.LogPath, current.LogPath, overlay.LogPath, "log_path"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.LogMaxSize, current.LogMaxSize, overlay.LogMaxSize, "log_max_size"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.LogMaxDays, current.LogMaxDays, overlay.LogMaxDays, "log_max_days"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.LogMaxFiles, current.LogMaxFiles, overlay.LogMaxFiles, "log_max_files"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeBoolField(&merged.LogCompress, current.LogCompress, overlay.LogCompress, "log_compress"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeBoolField(&merged.LogColor, current.LogColor, overlay.LogColor, "log_color"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.PProf, current.PProf, overlay.PProf, "pprof"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.ProtoVersion, current.ProtoVersion, overlay.ProtoVersion, "proto_version"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeBoolField(&merged.SkipVerify, current.SkipVerify, overlay.SkipVerify, "skip_verify"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.KeepAlive, current.KeepAlive, overlay.KeepAlive, "keepalive"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.DNSServer, current.DNSServer, overlay.DNSServer, "dns_server"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.NTPServer, current.NTPServer, overlay.NTPServer, "ntp_server"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.NTPInterval, current.NTPInterval, overlay.NTPInterval, "ntp_interval"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.Timezone, current.Timezone, overlay.Timezone, "timezone"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeBoolField(&merged.DisableP2P, current.DisableP2P, overlay.DisableP2P, "disable_p2p"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeStringField(&merged.P2PType, current.P2PType, overlay.P2PType, "p2p_type"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeBoolField(&merged.LocalIPForward, current.LocalIPForward, overlay.LocalIPForward, "local_ip_forward"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeBoolField(&merged.AutoReconnect, current.AutoReconnect, overlay.AutoReconnect, "auto_reconnect"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.DisconnectTimeout, current.DisconnectTimeout, overlay.DisconnectTimeout, "disconnect_timeout"); err != nil {
		return nil, err
	}
	if err := mergeRuntimeIntField(&merged.P2PTimeout, current.P2PTimeout, overlay.P2PTimeout, "p2p_timeout"); err != nil {
		return nil, err
	}
	return &merged, nil
}

func mergeRuntimeStringField(dst **string, current, overlay *string, name string) error {
	if overlay == nil {
		return nil
	}
	if current != nil && strings.TrimSpace(*current) != strings.TrimSpace(*overlay) {
		return fmt.Errorf("field %q differs between launch payloads", name)
	}
	*dst = overlay
	return nil
}

func mergeRuntimeBoolField(dst **bool, current, overlay *bool, name string) error {
	if overlay == nil {
		return nil
	}
	if current != nil && *current != *overlay {
		return fmt.Errorf("field %q differs between launch payloads", name)
	}
	*dst = overlay
	return nil
}

func mergeRuntimeIntField(dst **int, current, overlay *int, name string) error {
	if overlay == nil {
		return nil
	}
	if current != nil && *current != *overlay {
		return fmt.Errorf("field %q differs between launch payloads", name)
	}
	*dst = overlay
	return nil
}

func runtimeFromQuery(query url.Values) *LaunchRuntime {
	runtime := &LaunchRuntime{
		Debug:             parseOptionalBool(firstNonEmptyQuery(query, "debug")),
		Log:               parseOptionalString(firstNonEmptyQuery(query, "log")),
		LogLevel:          parseOptionalString(firstNonEmptyQuery(query, "log_level")),
		LogPath:           parseOptionalString(firstNonEmptyQuery(query, "log_path")),
		LogMaxSize:        parseOptionalInt(firstNonEmptyQuery(query, "log_max_size")),
		LogMaxDays:        parseOptionalInt(firstNonEmptyQuery(query, "log_max_days")),
		LogMaxFiles:       parseOptionalInt(firstNonEmptyQuery(query, "log_max_files")),
		LogCompress:       parseOptionalBool(firstNonEmptyQuery(query, "log_compress")),
		LogColor:          parseOptionalBool(firstNonEmptyQuery(query, "log_color")),
		PProf:             parseOptionalString(firstNonEmptyQuery(query, "pprof")),
		ProtoVersion:      parseOptionalInt(firstNonEmptyQuery(query, "proto_version")),
		SkipVerify:        parseOptionalBool(firstNonEmptyQuery(query, "skip_verify")),
		KeepAlive:         parseOptionalInt(firstNonEmptyQuery(query, "keepalive")),
		DNSServer:         parseOptionalString(firstNonEmptyQuery(query, "dns_server")),
		NTPServer:         parseOptionalString(firstNonEmptyQuery(query, "ntp_server")),
		NTPInterval:       parseOptionalInt(firstNonEmptyQuery(query, "ntp_interval")),
		Timezone:          parseOptionalString(firstNonEmptyQuery(query, "timezone")),
		DisableP2P:        parseOptionalBool(firstNonEmptyQuery(query, "disable_p2p")),
		P2PType:           parseOptionalString(firstNonEmptyQuery(query, "p2p_type")),
		LocalIPForward:    parseOptionalBool(firstNonEmptyQuery(query, "local_ip_forward")),
		AutoReconnect:     parseOptionalBool(firstNonEmptyQuery(query, "auto_reconnect")),
		DisconnectTimeout: parseOptionalInt(firstNonEmptyQuery(query, "disconnect_timeout")),
		P2PTimeout:        parseOptionalInt(firstNonEmptyQuery(query, "p2p_timeout")),
	}
	if !runtime.HasValue() {
		return nil
	}
	return runtime
}

func parseLaunchURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" {
		return nil, false
	}
	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "http", "https", "npc":
		return u, true
	default:
		return nil, false
	}
}

func parseOptionalString(raw string) *string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return &raw
}

func parseOptionalBool(raw string) *bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value := common.GetBoolByStr(strings.ToLower(raw))
	return &value
}

func parseOptionalInt(raw string) *int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}

func queryStringList(query url.Values, key string) launchStringList {
	return queryStringListKeys(query, key)
}

func queryStringListKeys(query url.Values, keys ...string) launchStringList {
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, 0)
	for _, key := range keys {
		values := query[key]
		for _, value := range values {
			out = append(out, splitLaunchValues(value)...)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeNPCRoute(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func shouldTreatNPCAsWrappedPayload(u *url.URL, query url.Values) bool {
	if u == nil {
		return false
	}
	hostRoute := normalizeNPCRoute(u.Hostname())
	if isSpecialNPCRoute(hostRoute) {
		return false
	}
	if u.User != nil {
		return false
	}
	for _, key := range []string{
		"server", "server_addr", "vkey", "proxy", "proxy_url", "local_ip",
		"password", "local_type", "local_port", "target", "target_addr", "target_type",
		"fallback_secret", "local_proxy", "route", "mode", "action",
	} {
		if firstNonEmptyQuery(query, key) != "" {
			return false
		}
	}
	if candidate := npcWrappedPayloadCandidate(u, true); isWrappedPayloadCandidate(candidate) {
		return true
	}
	return isWrappedPayloadCandidate(npcWrappedPayloadCandidate(u, false))
}

func npcWrappedPayloadCandidate(u *url.URL, includeQuery bool) string {
	if u == nil {
		return ""
	}
	var b strings.Builder
	if u.User != nil {
		b.WriteString(u.User.String())
		b.WriteByte('@')
	}
	b.WriteString(strings.TrimSpace(u.Host))
	b.WriteString(strings.TrimSpace(u.Path))
	if includeQuery && strings.TrimSpace(u.RawQuery) != "" {
		b.WriteByte('?')
		b.WriteString(strings.TrimSpace(u.RawQuery))
	}
	return strings.TrimSpace(b.String())
}

func isWrappedPayloadCandidate(candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || len(candidate) < 12 {
		return false
	}
	for _, r := range candidate {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '+', r == '/', r == '-', r == '_', r == '=':
		default:
			return false
		}
	}
	return true
}
