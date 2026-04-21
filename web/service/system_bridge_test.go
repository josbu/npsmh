package service

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server/connection"
)

func TestBestBridgeUsesDisplayOverrideFromSnapshot(t *testing.T) {
	original := connection.CurrentConfig()
	t.Cleanup(func() {
		connection.ApplySnapshot(original.Snapshot())
	})

	snapshot := &servercfg.Snapshot{
		Network: servercfg.NetworkConfig{
			BridgePort:    8024,
			BridgeTLSPort: 8025,
		},
		Bridge: servercfg.BridgeConfig{
			Addr:        "origin.example.com",
			PrimaryType: "tcp",
			Display: servercfg.BridgeDisplayConfig{
				TLS: servercfg.BridgeDisplayEndpoint{
					Show: true,
					IP:   "tls.example.com",
					Port: "9443",
				},
			},
		},
	}
	connection.ApplySnapshot(snapshot)

	bridge := BestBridge(snapshot, "198.51.100.10")
	if bridge.Type != "tls" || bridge.IP != "tls.example.com" || bridge.Port != "9443" || bridge.Addr != "tls.example.com:9443" {
		t.Fatalf("BestBridge() = %+v, want TLS display override", bridge)
	}
}

func TestDefaultSystemServiceBridgeDisplayHandlesEmptyQuicALPN(t *testing.T) {
	originalConfig := connection.CurrentConfig()
	updatedConfig := originalConfig
	updatedConfig.QuicAlpn = nil
	connection.ApplySnapshot(updatedConfig.Snapshot())
	defer func() {
		connection.ApplySnapshot(originalConfig.Snapshot())
	}()

	display := DefaultSystemService{}.BridgeDisplay(&servercfg.Snapshot{
		Bridge: servercfg.BridgeConfig{
			Display: servercfg.BridgeDisplayConfig{
				QUIC: servercfg.BridgeDisplayEndpoint{
					Show: true,
					IP:   "quic.example",
					Port: "7443",
				},
			},
		},
	}, "example.com")

	if !display.QUIC.Enabled {
		t.Fatal("QUIC display should be enabled")
	}
	if display.QUIC.ALPN != "nps" {
		t.Fatalf("QUIC ALPN = %q, want nps fallback", display.QUIC.ALPN)
	}
	if display.QUIC.Addr != "quic.example:7443" {
		t.Fatalf("QUIC Addr = %q, want quic.example:7443", display.QUIC.Addr)
	}
}
