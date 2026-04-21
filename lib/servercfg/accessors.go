package servercfg

import (
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/djylb/nps/lib/p2p"
)

const defaultProxyResponseHeaderTimeout = 100 * time.Second
const defaultProxySSLCacheIdleTimeout = 60 * time.Minute

// Resolve returns the provided snapshot or falls back to the current runtime snapshot.
func Resolve(cfg *Snapshot) *Snapshot {
	if cfg != nil {
		return cfg
	}
	return Current()
}

// ResolveProvider returns the provider result or falls back to the current runtime snapshot.
func ResolveProvider(provider func() *Snapshot) *Snapshot {
	if provider != nil {
		return Resolve(provider())
	}
	return Current()
}

func (cfg *Snapshot) NTPIntervalDuration() time.Duration {
	return time.Duration(Resolve(cfg).App.NTPInterval) * time.Minute
}

func (cfg *Snapshot) FlowStoreIntervalDuration() time.Duration {
	return time.Duration(Resolve(cfg).Runtime.FlowStoreInterval) * time.Minute
}

func (cfg *Snapshot) ProxyResponseHeaderTimeout() time.Duration {
	timeout := time.Duration(Resolve(cfg).Proxy.ResponseTimeout) * time.Second
	if timeout <= 0 {
		return defaultProxyResponseHeaderTimeout
	}
	return timeout
}

func (cfg *Snapshot) ProxySSLCacheMaxEntries() int {
	maxEntries := Resolve(cfg).Proxy.SSL.CacheMax
	if maxEntries < 0 {
		return 0
	}
	return maxEntries
}

func (cfg *Snapshot) ProxySSLCacheReloadInterval() time.Duration {
	reload := time.Duration(Resolve(cfg).Proxy.SSL.CacheReload) * time.Second
	if reload < 0 {
		return 0
	}
	return reload
}

func (cfg *Snapshot) ProxySSLCacheIdleInterval() time.Duration {
	idle := time.Duration(Resolve(cfg).Proxy.SSL.CacheIdle) * time.Minute
	if idle < 0 {
		return defaultProxySSLCacheIdleTimeout
	}
	return idle
}

func (cfg *Snapshot) AllowLocalProxyEnabled() bool {
	return Resolve(cfg).Feature.AllowLocalProxy
}

func (cfg *Snapshot) AllowSecretLocalEnabled() bool {
	return Resolve(cfg).Feature.AllowSecretLocal
}

func (cfg P2PConfig) ProbePolicy(basePort int) p2p.P2PPolicy {
	timeouts := cfg.ProbeTimeouts()
	return p2p.P2PPolicy{
		Layout:          "triple-port",
		BasePort:        basePort,
		ProbeTimeoutMs:  timeouts.ProbeTimeoutMs,
		ProbeExtraReply: cfg.ProbeExtraReply,
		Traversal: p2p.P2PTraversalPolicy{
			ForcePredictOnRestricted:  cfg.ForcePredictOnRestricted,
			EnableTargetSpray:         cfg.EnableTargetSpray,
			EnableBirthdayAttack:      cfg.EnableBirthdayAttack,
			DefaultPredictionInterval: cfg.DefaultPredictionInterval,
			TargetSpraySpan:           cfg.TargetSpraySpan,
			TargetSprayRounds:         cfg.TargetSprayRounds,
			TargetSprayBurst:          cfg.TargetSprayBurst,
			TargetSprayPacketSleepMs:  cfg.TargetSprayPacketSleepMs,
			TargetSprayBurstGapMs:     cfg.TargetSprayBurstGapMs,
			TargetSprayPhaseGapMs:     cfg.TargetSprayPhaseGapMs,
			BirthdayListenPorts:       cfg.BirthdayListenPorts,
			BirthdayTargetsPerPort:    cfg.BirthdayTargetsPerPort,
		},
		PortMapping: p2p.P2PPortMappingPolicy{
			EnableUPNPPortmap:   cfg.EnableUPNPPortmap,
			EnablePCPPortmap:    cfg.EnablePCPPortmap,
			EnableNATPMPPortmap: cfg.EnableNATPMPPortmap,
			LeaseSeconds:        cfg.PortmapLeaseSeconds,
		},
	}
}

func (cfg P2PConfig) ProbeTimeouts() p2p.P2PTimeouts {
	defaults := p2p.DefaultTimeouts()
	return p2p.P2PTimeouts{
		ProbeTimeoutMs:     normalizeP2PTimeout(cfg.ProbeTimeoutMs, defaults.ProbeTimeoutMs),
		HandshakeTimeoutMs: normalizeP2PTimeout(cfg.HandshakeTimeoutMs, defaults.HandshakeTimeoutMs),
		TransportTimeoutMs: normalizeP2PTimeout(cfg.TransportTimeoutMs, defaults.TransportTimeoutMs),
	}
}

func normalizeP2PTimeout(value int, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (cfg *Snapshot) P2PProbePolicy(basePort int) p2p.P2PPolicy {
	cfg = Resolve(cfg)
	return cfg.P2P.ProbePolicy(basePort)
}

func (cfg *Snapshot) P2PProbeTimeouts() p2p.P2PTimeouts {
	cfg = Resolve(cfg)
	return cfg.P2P.ProbeTimeouts()
}

func (cfg *Snapshot) P2PProbeBasePort() int {
	cfg = Resolve(cfg)
	basePort := cfg.Network.P2PPort
	if basePort <= 0 || basePort > 65533 {
		return 0
	}
	return basePort
}

const (
	defaultStandaloneTokenTTLSeconds int64 = 3600
	minStandaloneTokenTTLSeconds     int64 = 60
	maxStandaloneTokenTTLSeconds     int64 = 30 * 24 * 3600
)

func (cfg *Snapshot) StandaloneAllowedOrigins() []string {
	if cfg == nil {
		return nil
	}
	parts := strings.Split(cfg.Web.StandaloneAllowedOrigins, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		origin := normalizeStandaloneOrigin(part)
		if origin == "" {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins
}

func (cfg *Snapshot) StandaloneAllowsAnyOrigin() bool {
	for _, origin := range cfg.StandaloneAllowedOrigins() {
		if origin == "*" {
			return true
		}
	}
	return false
}

func (cfg *Snapshot) StandaloneAllowsOrigin(origin string) bool {
	origin = normalizeStandaloneOrigin(origin)
	if origin == "" {
		return false
	}
	for _, allowed := range cfg.StandaloneAllowedOrigins() {
		if allowed == "*" || allowed == origin {
			return true
		}
	}
	return false
}

func (cfg *Snapshot) StandaloneAllowCredentials() bool {
	return cfg != nil && cfg.Web.StandaloneAllowCredentials
}

func (cfg *Snapshot) StandaloneTokenTTLSecondsValue() int64 {
	if cfg == nil {
		return defaultStandaloneTokenTTLSeconds
	}
	value := cfg.Web.StandaloneTokenTTLSeconds
	if value <= 0 {
		value = defaultStandaloneTokenTTLSeconds
	}
	if value < minStandaloneTokenTTLSeconds {
		return minStandaloneTokenTTLSeconds
	}
	if value > maxStandaloneTokenTTLSeconds {
		return maxStandaloneTokenTTLSeconds
	}
	return value
}

func (cfg *Snapshot) StandaloneTokenTTL() time.Duration {
	return time.Duration(cfg.StandaloneTokenTTLSecondsValue()) * time.Second
}

func (cfg *Snapshot) StandaloneTokenSecret() string {
	if cfg == nil {
		return ""
	}
	if secret := strings.TrimSpace(cfg.Web.StandaloneTokenSecret); secret != "" {
		return secret
	}
	seed := strings.Join([]string{
		strings.TrimSpace(cfg.Auth.Key),
		strings.TrimSpace(cfg.Auth.CryptKey),
		strings.TrimSpace(cfg.Web.Username),
		cfg.Web.Password,
		strings.TrimSpace(cfg.App.Name),
	}, "|")
	if strings.Trim(seed, "|") == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(seed))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func normalizeStandaloneOrigin(origin string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	if origin == "*" {
		return origin
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed == nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if scheme == "" || host == "" {
		return ""
	}
	port := strings.TrimSpace(parsed.Port())
	defaultPort := defaultStandaloneOriginPort(scheme)
	if port == defaultPort {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}

func defaultStandaloneOriginPort(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	default:
		return ""
	}
}
