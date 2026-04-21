package servercfg

import (
	"testing"
	"time"
)

func TestResolveUsesCurrentSnapshotWhenNil(t *testing.T) {
	previous := Current()
	t.Cleanup(func() {
		currentSnapshot.Store(previous)
	})

	cfg := &Snapshot{
		App:     AppConfig{NTPInterval: 7},
		Runtime: RuntimeConfig{FlowStoreInterval: 3},
		Proxy:   ProxyConfig{ResponseTimeout: 9},
		Feature: FeatureConfig{AllowLocalProxy: true, AllowSecretLocal: true},
	}
	currentSnapshot.Store(cfg)

	resolved := Resolve(nil)
	if resolved != cfg {
		t.Fatalf("Resolve(nil) = %p, want %p", resolved, cfg)
	}
	if got := resolved.NTPIntervalDuration(); got != 7*time.Minute {
		t.Fatalf("NTPIntervalDuration() = %v, want 7m", got)
	}
	if got := resolved.FlowStoreIntervalDuration(); got != 3*time.Minute {
		t.Fatalf("FlowStoreIntervalDuration() = %v, want 3m", got)
	}
	if got := resolved.ProxyResponseHeaderTimeout(); got != 9*time.Second {
		t.Fatalf("ProxyResponseHeaderTimeout() = %v, want 9s", got)
	}
	if !resolved.AllowLocalProxyEnabled() || !resolved.AllowSecretLocalEnabled() {
		t.Fatalf("feature accessors = %+v, want all true", resolved.Feature)
	}
}

func TestResolveProviderFallsBackToCurrentSnapshot(t *testing.T) {
	previous := Current()
	t.Cleanup(func() {
		currentSnapshot.Store(previous)
	})

	cfg := &Snapshot{App: AppConfig{Name: "provider-fallback"}}
	currentSnapshot.Store(cfg)

	if got := ResolveProvider(nil); got != cfg {
		t.Fatalf("ResolveProvider(nil) = %p, want %p", got, cfg)
	}
	if got := ResolveProvider(func() *Snapshot { return nil }); got != cfg {
		t.Fatalf("ResolveProvider(nil provider result) = %p, want %p", got, cfg)
	}
}

func TestProxyResponseHeaderTimeoutNormalizesNonPositiveValues(t *testing.T) {
	if got := (&Snapshot{Proxy: ProxyConfig{ResponseTimeout: 0}}).ProxyResponseHeaderTimeout(); got != defaultProxyResponseHeaderTimeout {
		t.Fatalf("ProxyResponseHeaderTimeout() with zero = %v, want %v", got, defaultProxyResponseHeaderTimeout)
	}
	if got := (&Snapshot{Proxy: ProxyConfig{ResponseTimeout: -7}}).ProxyResponseHeaderTimeout(); got != defaultProxyResponseHeaderTimeout {
		t.Fatalf("ProxyResponseHeaderTimeout() with negative = %v, want %v", got, defaultProxyResponseHeaderTimeout)
	}
}

func TestProxySSLCacheAccessorsNormalizeNegativeValues(t *testing.T) {
	cfg := &Snapshot{
		Proxy: ProxyConfig{
			SSL: SSLConfig{
				CacheMax:    -2,
				CacheReload: -3,
				CacheIdle:   -4,
			},
		},
	}

	if got := cfg.ProxySSLCacheMaxEntries(); got != 0 {
		t.Fatalf("ProxySSLCacheMaxEntries() = %d, want 0", got)
	}
	if got := cfg.ProxySSLCacheReloadInterval(); got != 0 {
		t.Fatalf("ProxySSLCacheReloadInterval() = %v, want 0", got)
	}
	if got := cfg.ProxySSLCacheIdleInterval(); got != defaultProxySSLCacheIdleTimeout {
		t.Fatalf("ProxySSLCacheIdleInterval() = %v, want %v", got, defaultProxySSLCacheIdleTimeout)
	}
}
