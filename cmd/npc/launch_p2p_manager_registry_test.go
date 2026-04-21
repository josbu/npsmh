package main

import (
	"context"
	"testing"

	"github.com/djylb/nps/lib/config"
)

func TestLaunchLocalManagerRegistrySharesManagerByBridgeIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry := newLaunchLocalManagerRegistry(ctx, func() {})
	cfg1 := &config.CommonConfig{
		Server:   "bridge.example.com:8024",
		VKey:     "shared-key",
		Tp:       "TLS",
		ProxyUrl: "http://127.0.0.1:8080",
		LocalIP:  "192.168.1.10",
	}
	cfg2 := &config.CommonConfig{
		Server:   " bridge.example.com:8024 ",
		VKey:     "shared-key",
		Tp:       "tls",
		ProxyUrl: "http://127.0.0.1:8080",
		LocalIP:  "192.168.1.10",
	}

	mgr1 := registry.managerFor(cfg1)
	mgr2 := registry.managerFor(cfg2)
	if mgr1 == nil || mgr2 == nil {
		t.Fatal("managerFor() returned nil manager")
	}
	if mgr1 != mgr2 {
		t.Fatal("expected launch local profiles with same bridge identity to reuse one P2PManager")
	}
}

func TestLaunchLocalManagerRegistrySeparatesDifferentBridgeIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registry := newLaunchLocalManagerRegistry(ctx, func() {})
	cfg1 := &config.CommonConfig{
		Server: "bridge.example.com:8024",
		VKey:   "shared-key",
		Tp:     "tls",
	}
	cfg2 := &config.CommonConfig{
		Server: "bridge.example.com:8024",
		VKey:   "other-key",
		Tp:     "tls",
	}

	mgr1 := registry.managerFor(cfg1)
	mgr2 := registry.managerFor(cfg2)
	if mgr1 == nil || mgr2 == nil {
		t.Fatal("managerFor() returned nil manager")
	}
	if mgr1 == mgr2 {
		t.Fatal("expected different bridge identities to create different P2PManager instances")
	}
}
