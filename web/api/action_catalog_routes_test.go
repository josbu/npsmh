package api

import "testing"

func TestNodeRouteCatalogs(t *testing.T) {
	direct := NodeDirectRouteCatalog("/base")
	if direct.APIBase != "/base/api" {
		t.Fatalf("NodeDirectRouteCatalog().APIBase = %q, want /base/api", direct.APIBase)
	}
	if direct.OverviewURL(false) != direct.Overview {
		t.Fatalf("OverviewURL(false) = %q, want %q", direct.OverviewURL(false), direct.Overview)
	}
	if direct.OverviewURL(true) != direct.Overview+"?config=1" {
		t.Fatalf("OverviewURL(true) = %q, want %q", direct.OverviewURL(true), direct.Overview+"?config=1")
	}

	runtimeConfig := map[string]interface{}{}
	direct.ApplyRuntimeConfig(runtimeConfig)
	if runtimeConfig["health"] != direct.Health {
		t.Fatalf("ApplyRuntimeConfig(health) = %v, want %s", runtimeConfig["health"], direct.Health)
	}
	if runtimeConfig["discovery"] != direct.Discovery {
		t.Fatalf("ApplyRuntimeConfig(discovery) = %v, want %s", runtimeConfig["discovery"], direct.Discovery)
	}
	if runtimeConfig["status"] != direct.Status {
		t.Fatalf("ApplyRuntimeConfig(status) = %v, want %s", runtimeConfig["status"], direct.Status)
	}
	if runtimeConfig["dashboard"] != direct.Dashboard {
		t.Fatalf("ApplyRuntimeConfig(dashboard) = %v, want %s", runtimeConfig["dashboard"], direct.Dashboard)
	}
	if runtimeConfig["settings_global"] != direct.Global || runtimeConfig["security_bans"] != direct.BanList {
		t.Fatalf("ApplyRuntimeConfig(global routes) = %#v", runtimeConfig)
	}
	if runtimeConfig["system_import"] != direct.ConfigImport {
		t.Fatalf("ApplyRuntimeConfig(system_import) = %v, want %s", runtimeConfig["system_import"], direct.ConfigImport)
	}
	if runtimeConfig["users"] != direct.Users || runtimeConfig["clients"] != direct.Clients || runtimeConfig["clients_qrcode"] != direct.ClientsQR || runtimeConfig["tunnels"] != direct.Tunnels || runtimeConfig["hosts"] != direct.Hosts {
		t.Fatalf("ApplyRuntimeConfig(resource routes) = %#v", runtimeConfig)
	}
	if runtimeConfig["clients_clear"] != direct.ClientsClear {
		t.Fatalf("ApplyRuntimeConfig(clients_clear) = %v, want %s", runtimeConfig["clients_clear"], direct.ClientsClear)
	}
	if runtimeConfig["host_cert_suggestion"] != direct.HostCertSuggestion {
		t.Fatalf(
			"ApplyRuntimeConfig(host_cert_suggestion) = %v, want %s",
			runtimeConfig["host_cert_suggestion"],
			direct.HostCertSuggestion,
		)
	}
	if runtimeConfig["callbacks_queue_replay"] != direct.CallbackQueueReplay {
		t.Fatalf(
			"ApplyRuntimeConfig(callbacks_queue_replay) = %v, want %s",
			runtimeConfig["callbacks_queue_replay"],
			direct.CallbackQueueReplay,
		)
	}

	var discoveryRoutes ManagementRoutes
	direct.ApplyDiscoveryRoutes(&discoveryRoutes)
	if discoveryRoutes.Health != direct.Health {
		t.Fatalf("ApplyDiscoveryRoutes(Health) = %q, want %q", discoveryRoutes.Health, direct.Health)
	}
	if discoveryRoutes.Discovery != direct.Discovery {
		t.Fatalf("ApplyDiscoveryRoutes(Discovery) = %q, want %q", discoveryRoutes.Discovery, direct.Discovery)
	}
	if discoveryRoutes.Overview != direct.Overview {
		t.Fatalf("ApplyDiscoveryRoutes(Overview) = %q, want %q", discoveryRoutes.Overview, direct.Overview)
	}
	if discoveryRoutes.Dashboard != direct.Dashboard {
		t.Fatalf("ApplyDiscoveryRoutes(Dashboard) = %q, want %q", discoveryRoutes.Dashboard, direct.Dashboard)
	}
	if discoveryRoutes.SettingsGlobal != direct.Global || discoveryRoutes.SecurityBans != direct.BanList {
		t.Fatalf("ApplyDiscoveryRoutes(global routes) = %#v", discoveryRoutes)
	}
	if discoveryRoutes.SystemImport != direct.ConfigImport {
		t.Fatalf("ApplyDiscoveryRoutes(SystemImport) = %q, want %q", discoveryRoutes.SystemImport, direct.ConfigImport)
	}
	if discoveryRoutes.Users != direct.Users || discoveryRoutes.Clients != direct.Clients || discoveryRoutes.ClientsQRCode != direct.ClientsQR || discoveryRoutes.Tunnels != direct.Tunnels || discoveryRoutes.Hosts != direct.Hosts {
		t.Fatalf("ApplyDiscoveryRoutes(resource routes) = %#v", discoveryRoutes)
	}
	if discoveryRoutes.ClientsClear != direct.ClientsClear {
		t.Fatalf("ApplyDiscoveryRoutes(ClientsClear) = %q, want %q", discoveryRoutes.ClientsClear, direct.ClientsClear)
	}
	if discoveryRoutes.HostCertSuggestion != direct.HostCertSuggestion {
		t.Fatalf(
			"ApplyDiscoveryRoutes(HostCertSuggestion) = %q, want %q",
			discoveryRoutes.HostCertSuggestion,
			direct.HostCertSuggestion,
		)
	}
	if discoveryRoutes.WebSocket != direct.WebSocket {
		t.Fatalf("ApplyDiscoveryRoutes(WebSocket) = %q, want %q", discoveryRoutes.WebSocket, direct.WebSocket)
	}

	prefixes := NodeRoutePrefixes("/base")
	want := []string{"/base/api"}
	if len(prefixes) != len(want) {
		t.Fatalf("NodeRoutePrefixes() len = %d, want %d (%v)", len(prefixes), len(want), prefixes)
	}
	for index, prefix := range want {
		if prefixes[index] != prefix {
			t.Fatalf("NodeRoutePrefixes()[%d] = %q, want %q", index, prefixes[index], prefix)
		}
	}

	prefixes = NodeRoutePrefixes("/a/b/c")
	want = []string{"/a/b/c/api"}
	if len(prefixes) != len(want) {
		t.Fatalf("NodeRoutePrefixes(multi-level) len = %d, want %d (%v)", len(prefixes), len(want), prefixes)
	}
	for index, prefix := range want {
		if prefixes[index] != prefix {
			t.Fatalf("NodeRoutePrefixes(multi-level)[%d] = %q, want %q", index, prefixes[index], prefix)
		}
	}

	normalized := NodeDirectRouteCatalog("ops/platform/admin/")
	if normalized.APIBase != "/ops/platform/admin/api" {
		t.Fatalf("NodeDirectRouteCatalog(normalized).APIBase = %q, want /ops/platform/admin/api", normalized.APIBase)
	}
	if normalized.Discovery != "/ops/platform/admin/api/system/discovery" {
		t.Fatalf("NodeDirectRouteCatalog(normalized).Discovery = %q, want /ops/platform/admin/api/system/discovery", normalized.Discovery)
	}
}
