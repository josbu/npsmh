package file

import (
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/rate"
)

func TestClientNormalizeLifecycleFieldsFromLegacyFlow(t *testing.T) {
	expireAt := time.Date(2026, time.March, 21, 10, 11, 12, 0, time.UTC)
	client := &Client{
		Flow: &Flow{
			FlowLimit: 2,
			TimeLimit: expireAt,
		},
	}

	client.NormalizeLifecycleFields()

	if client.FlowLimit != 2*1024*1024 {
		t.Fatalf("NormalizeLifecycleFields() FlowLimit = %d, want %d", client.FlowLimit, 2*1024*1024)
	}
	if client.ExpireAt != expireAt.Unix() {
		t.Fatalf("NormalizeLifecycleFields() ExpireAt = %d, want %d", client.ExpireAt, expireAt.Unix())
	}
}

func TestClientSetLifecycleFieldsKeepsLegacyCompatibility(t *testing.T) {
	expireAt := time.Date(2026, time.March, 22, 11, 12, 13, 0, time.UTC).Unix()
	client := &Client{Flow: &Flow{}}

	client.SetFlowLimitBytes(3 * 1024 * 1024)
	client.SetExpireAt(expireAt)

	if client.FlowLimit != 3*1024*1024 {
		t.Fatalf("SetFlowLimitBytes() FlowLimit = %d, want %d", client.FlowLimit, 3*1024*1024)
	}
	if client.Flow.FlowLimit != 3 {
		t.Fatalf("SetFlowLimitBytes() legacy Flow.FlowLimit = %d, want 3", client.Flow.FlowLimit)
	}
	if client.ExpireAt != expireAt {
		t.Fatalf("SetExpireAt() ExpireAt = %d, want %d", client.ExpireAt, expireAt)
	}
	if !client.Flow.TimeLimit.Equal(time.Unix(expireAt, 0)) {
		t.Fatalf("SetExpireAt() legacy Flow.TimeLimit = %v, want %v", client.Flow.TimeLimit, time.Unix(expireAt, 0))
	}
}

func TestTunnelNormalizeLifecycleFieldsFromLegacyFlow(t *testing.T) {
	expireAt := time.Date(2026, time.March, 23, 12, 13, 14, 0, time.UTC)
	tunnel := &Tunnel{
		Flow: &Flow{
			FlowLimit: 4,
			TimeLimit: expireAt,
		},
	}

	tunnel.NormalizeLifecycleFields()

	if tunnel.FlowLimit != 4*1024*1024 {
		t.Fatalf("NormalizeLifecycleFields() FlowLimit = %d, want %d", tunnel.FlowLimit, 4*1024*1024)
	}
	if tunnel.ExpireAt != expireAt.Unix() {
		t.Fatalf("NormalizeLifecycleFields() ExpireAt = %d, want %d", tunnel.ExpireAt, expireAt.Unix())
	}
}

func TestHostSetLifecycleFieldsKeepsLegacyCompatibility(t *testing.T) {
	expireAt := time.Date(2026, time.March, 24, 13, 14, 15, 0, time.UTC).Unix()
	host := &Host{Flow: &Flow{}}

	host.SetFlowLimitBytes(5 * 1024 * 1024)
	host.SetExpireAt(expireAt)

	if host.FlowLimit != 5*1024*1024 {
		t.Fatalf("SetFlowLimitBytes() FlowLimit = %d, want %d", host.FlowLimit, 5*1024*1024)
	}
	if host.Flow.FlowLimit != 5 {
		t.Fatalf("SetFlowLimitBytes() legacy Flow.FlowLimit = %d, want 5", host.Flow.FlowLimit)
	}
	if host.ExpireAt != expireAt {
		t.Fatalf("SetExpireAt() ExpireAt = %d, want %d", host.ExpireAt, expireAt)
	}
	if !host.Flow.TimeLimit.Equal(time.Unix(expireAt, 0)) {
		t.Fatalf("SetExpireAt() legacy Flow.TimeLimit = %v, want %v", host.Flow.TimeLimit, time.Unix(expireAt, 0))
	}
}

func TestNewTunnelByHostCarriesRuntimeLimitFields(t *testing.T) {
	hostRate := rate.NewRate(64 * 1024)
	hostRate.Start()
	host := &Host{
		Client:    &Client{Id: 7},
		Flow:      &Flow{},
		ExpireAt:  time.Date(2026, time.March, 25, 14, 15, 16, 0, time.UTC).Unix(),
		FlowLimit: 6 * 1024 * 1024,
		RateLimit: 64,
		Rate:      hostRate,
		MaxConn:   3,
	}

	tunnel := NewTunnelByHost(host, 443)

	if tunnel.ExpireAt != host.ExpireAt {
		t.Fatalf("NewTunnelByHost() ExpireAt = %d, want %d", tunnel.ExpireAt, host.ExpireAt)
	}
	if tunnel.FlowLimit != host.FlowLimit {
		t.Fatalf("NewTunnelByHost() FlowLimit = %d, want %d", tunnel.FlowLimit, host.FlowLimit)
	}
	if tunnel.RateLimit != host.RateLimit {
		t.Fatalf("NewTunnelByHost() RateLimit = %d, want %d", tunnel.RateLimit, host.RateLimit)
	}
	if tunnel.Rate != hostRate {
		t.Fatalf("NewTunnelByHost() Rate = %#v, want host rate pointer", tunnel.Rate)
	}
	if tunnel.MaxConn != host.MaxConn {
		t.Fatalf("NewTunnelByHost() MaxConn = %d, want %d", tunnel.MaxConn, host.MaxConn)
	}
}

func TestNewTunnelByHostSharesRuntimeTrafficWithHost(t *testing.T) {
	host := &Host{
		Client: &Client{Id: 8},
		Flow:   &Flow{},
	}

	tunnel := NewTunnelByHost(host, 443)

	if tunnel.ServiceTraffic == nil || tunnel.ServiceMeter == nil {
		t.Fatalf("NewTunnelByHost() runtime traffic = %#v / %#v, want initialized host-backed runtime traffic", tunnel.ServiceTraffic, tunnel.ServiceMeter)
	}
	if tunnel.ServiceTraffic != host.ServiceTraffic {
		t.Fatal("NewTunnelByHost() should share host ServiceTraffic pointer")
	}
	if tunnel.ServiceMeter != host.ServiceMeter {
		t.Fatal("NewTunnelByHost() should share host ServiceMeter pointer")
	}

	if err := tunnel.ObserveServiceTraffic(3, 4); err != nil {
		t.Fatalf("tunnel.ObserveServiceTraffic() error = %v", err)
	}
	in, out, total := host.ServiceTrafficTotals()
	if in != 3 || out != 4 || total != 7 {
		t.Fatalf("host.ServiceTrafficTotals() = %d/%d/%d, want 3/4/7", in, out, total)
	}
}

func TestObserveTrafficFlowLimitsStillTripAtCurrentAccumulatedTotal(t *testing.T) {
	user := &User{FlowLimit: 3, TotalFlow: &Flow{}}
	InitializeUserRuntime(user)
	if err := user.ObserveTotalTraffic(1, 1); err != nil {
		t.Fatalf("user.ObserveTotalTraffic(first) error = %v", err)
	}
	if err := user.ObserveTotalTraffic(1, 0); err == nil {
		t.Fatal("user.ObserveTotalTraffic(second) should fail once total reaches flow limit")
	}

	client := &Client{FlowLimit: 3, Flow: &Flow{}, Cnf: &Config{}}
	InitializeClientRuntime(client)
	if err := client.ObserveBridgeTraffic(1, 1); err != nil {
		t.Fatalf("client.ObserveBridgeTraffic(first) error = %v", err)
	}
	if err := client.ObserveServiceTraffic(1, 0); err == nil {
		t.Fatal("client.ObserveServiceTraffic(second) should fail once total reaches flow limit")
	}

	tunnel := &Tunnel{FlowLimit: 3, Flow: &Flow{}, Target: &Target{TargetStr: "example.com:80"}}
	InitializeTunnelRuntime(tunnel)
	if err := tunnel.ObserveServiceTraffic(1, 1); err != nil {
		t.Fatalf("tunnel.ObserveServiceTraffic(first) error = %v", err)
	}
	if err := tunnel.ObserveServiceTraffic(1, 0); err == nil {
		t.Fatal("tunnel.ObserveServiceTraffic(second) should fail once total reaches flow limit")
	}

	host := &Host{FlowLimit: 3, Flow: &Flow{}, Target: &Target{}}
	InitializeHostRuntime(host)
	if err := host.ObserveServiceTraffic(1, 1); err != nil {
		t.Fatalf("host.ObserveServiceTraffic(first) error = %v", err)
	}
	if err := host.ObserveServiceTraffic(1, 0); err == nil {
		t.Fatal("host.ObserveServiceTraffic(second) should fail once total reaches flow limit")
	}
}

func TestClientHasTunnelMatchesFileTaskWithoutPortOrPassword(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        21,
		VerifyKey: "vk-file-client",
		Flow:      &Flow{},
		Cnf:       &Config{},
		Status:    true,
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	existing := &Tunnel{
		Id:        41,
		Mode:      "file",
		Client:    client,
		Flow:      &Flow{},
		Target:    &Target{TargetStr: "file://existing"},
		LocalPath: "/srv/data",
		StripPre:  "/files",
		ReadOnly:  true,
		MultiAccount: &MultiAccount{
			Content: "guest=demo",
		},
	}
	if err := db.NewTask(existing); err != nil {
		t.Fatalf("NewTask(existing) error = %v", err)
	}

	candidate := &Tunnel{
		Mode:      "file",
		Client:    client,
		LocalPath: "/srv/data",
		StripPre:  "/files",
		ReadOnly:  true,
		MultiAccount: &MultiAccount{
			Content: "guest=demo",
		},
	}

	got, ok := client.HasTunnel(candidate)
	if !ok || got != existing {
		t.Fatalf("HasTunnel(file candidate) = %#v, %v, want existing task %#v, true", got, ok, existing)
	}

	changedPath := &Tunnel{
		Mode:      "file",
		Client:    client,
		LocalPath: "/srv/other",
		StripPre:  "/files",
		ReadOnly:  true,
		MultiAccount: &MultiAccount{
			Content: "guest=demo",
		},
	}
	if got, ok := client.HasTunnel(changedPath); ok || got != nil {
		t.Fatalf("HasTunnel(changedPath) = %#v, %v, want nil, false", got, ok)
	}
}

func TestClientHasTunnelDistinguishesFileTaskAuthVariants(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{
		Id:        22,
		VerifyKey: "vk-file-auth-client",
		Flow:      &Flow{},
		Cnf:       &Config{U: "demo", P: "secret"},
		Status:    true,
	}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	existing := &Tunnel{
		Id:        42,
		Mode:      "file",
		Client:    client,
		Flow:      &Flow{},
		Target:    &Target{TargetStr: "file://existing"},
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			Content: "ops=token-a",
		},
	}
	if err := db.NewTask(existing); err != nil {
		t.Fatalf("NewTask(existing) error = %v", err)
	}

	changedUserAuth := &Tunnel{
		Mode:      "file",
		Client:    client,
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			Content: "ops=token-b",
		},
	}
	if got, ok := client.HasTunnel(changedUserAuth); ok || got != nil {
		t.Fatalf("HasTunnel(changedUserAuth) = %#v, %v, want nil, false", got, ok)
	}

	changedClientAuth := &Tunnel{
		Mode:      "file",
		Client:    &Client{Cnf: &Config{U: "demo", P: "different"}},
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			Content: "ops=token-a",
		},
	}
	if got, ok := client.HasTunnel(changedClientAuth); ok || got != nil {
		t.Fatalf("HasTunnel(changedClientAuth) = %#v, %v, want nil, false", got, ok)
	}
}

func TestFileTunnelRuntimeKeyIncludesAuthInputs(t *testing.T) {
	baseClient := &Client{
		VerifyKey: "vk-file-key",
		Cnf:       &Config{U: "demo", P: "secret"},
	}
	base := &Tunnel{
		Mode:      "file",
		Client:    baseClient,
		ServerIp:  "127.0.0.1",
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			Content: "ops=token-a",
		},
	}
	same := &Tunnel{
		Mode:      "file",
		Client:    &Client{Cnf: &Config{U: "demo", P: "secret"}},
		ServerIp:  "127.0.0.1",
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			AccountMap: map[string]string{"ops": "token-a"},
		},
	}
	changed := &Tunnel{
		Mode:      "file",
		Client:    &Client{Cnf: &Config{U: "demo", P: "secret"}},
		ServerIp:  "127.0.0.1",
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			Content: "ops=token-b",
		},
	}

	baseKey := FileTunnelRuntimeKey(baseClient.VerifyKey, base)
	if sameKey := FileTunnelRuntimeKey(baseClient.VerifyKey, same); sameKey != baseKey {
		t.Fatalf("FileTunnelRuntimeKey(same) = %q, want %q", sameKey, baseKey)
	}
	if changedKey := FileTunnelRuntimeKey(baseClient.VerifyKey, changed); changedKey == baseKey {
		t.Fatalf("FileTunnelRuntimeKey(changed) = %q, want auth-sensitive difference from %q", changedKey, baseKey)
	}
}

func TestGetAccountMapParsesContentFallback(t *testing.T) {
	accounts := GetAccountMap(&MultiAccount{
		Content: "ops=token\n guest = demo \n#ignored\nreadonly\n",
	})

	if len(accounts) != 3 {
		t.Fatalf("GetAccountMap() size = %d, want 3", len(accounts))
	}
	if accounts["ops"] != "token" {
		t.Fatalf("GetAccountMap()[ops] = %q, want %q", accounts["ops"], "token")
	}
	if accounts["guest"] != "demo" {
		t.Fatalf("GetAccountMap()[guest] = %q, want %q", accounts["guest"], "demo")
	}
	if accounts["readonly"] != "" {
		t.Fatalf("GetAccountMap()[readonly] = %q, want empty password", accounts["readonly"])
	}
}

func TestGetAccountMapPrefersNormalizedContentOverLegacyMap(t *testing.T) {
	accounts := GetAccountMap(&MultiAccount{
		Content:    "#ignored\nops=token\nreadonly\n",
		AccountMap: map[string]string{"#ignored": "", "ops": "token", "readonly": ""},
	})

	if len(accounts) != 2 {
		t.Fatalf("GetAccountMap() size = %d, want 2", len(accounts))
	}
	if _, ok := accounts["#ignored"]; ok {
		t.Fatal("GetAccountMap() should ignore legacy comment entries from stale AccountMap")
	}
	if accounts["ops"] != "token" || accounts["readonly"] != "" {
		t.Fatalf("GetAccountMap() = %#v, want normalized content-derived map", accounts)
	}
}

func TestTargetGetRandomTargetRefreshesChangedTargetStr(t *testing.T) {
	target := &Target{TargetStr: "alpha:80\nbeta:81"}

	first, err := target.GetRandomTarget()
	if err != nil {
		t.Fatalf("GetRandomTarget(initial) error = %v", err)
	}
	if first != "alpha:80" {
		t.Fatalf("GetRandomTarget(initial) = %q, want %q", first, "alpha:80")
	}

	target.TargetStr = "gamma:82\ndelta:83"

	second, err := target.GetRandomTarget()
	if err != nil {
		t.Fatalf("GetRandomTarget(updated) error = %v", err)
	}
	if second != "gamma:82" {
		t.Fatalf("GetRandomTarget(updated) = %q, want %q", second, "gamma:82")
	}
}

func TestTargetGetRouteTargetSeedsInitialOffset(t *testing.T) {
	target := &Target{TargetStr: "alpha:80\nbeta:81"}

	first, err := target.GetRouteTarget("node-b")
	if err != nil {
		t.Fatalf("GetRouteTarget(first) error = %v", err)
	}
	if first != "beta:81" {
		t.Fatalf("GetRouteTarget(first) = %q, want %q", first, "beta:81")
	}

	second, err := target.GetRouteTarget("node-b")
	if err != nil {
		t.Fatalf("GetRouteTarget(second) error = %v", err)
	}
	if second != "alpha:80" {
		t.Fatalf("GetRouteTarget(second) = %q, want %q", second, "alpha:80")
	}
}

func TestFileTunnelRuntimeKeyNormalizesContentOnlyAuthComments(t *testing.T) {
	client := &Client{
		VerifyKey: "vk-file-comments",
		Cnf:       &Config{U: "demo", P: "secret"},
	}
	contentOnly := &Tunnel{
		Mode:      "file",
		Client:    client,
		ServerIp:  "127.0.0.1",
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			Content: "# comment\nops = token-a\n",
		},
	}
	normalized := &Tunnel{
		Mode:      "file",
		Client:    &Client{Cnf: &Config{U: "demo", P: "secret"}},
		ServerIp:  "127.0.0.1",
		LocalPath: "/srv/data",
		StripPre:  "/files",
		UserAuth: &MultiAccount{
			AccountMap: map[string]string{"ops": "token-a"},
		},
	}

	contentKey := FileTunnelRuntimeKey(client.VerifyKey, contentOnly)
	normalizedKey := FileTunnelRuntimeKey(client.VerifyKey, normalized)
	if contentKey != normalizedKey {
		t.Fatalf("FileTunnelRuntimeKey(contentOnly) = %q, want normalized key %q", contentKey, normalizedKey)
	}
}

func TestUserGetConnHonorsLimitAndRelease(t *testing.T) {
	user := &User{MaxConnections: 1}
	if !user.GetConn() {
		t.Fatal("first GetConn() should succeed")
	}
	if user.GetConn() {
		t.Fatal("second GetConn() should fail at limit")
	}
	user.CutConn()
	if !user.GetConn() {
		t.Fatal("GetConn() should succeed again after CutConn()")
	}
}

func TestPromoteLegacyBlacklistPolicy(t *testing.T) {
	mode, rules := promoteLegacyBlacklistPolicy(AclOff, "", []string{"127.0.0.1", "10.0.0.0/8"})
	if mode != AclBlacklist {
		t.Fatalf("mode = %d, want %d", mode, AclBlacklist)
	}
	if rules != "127.0.0.1\n10.0.0.0/8" {
		t.Fatalf("rules = %q, want legacy blacklist rules", rules)
	}

	client := &Client{}
	client.SetLegacyBlackIPImport([]string{"127.0.0.1", "10.0.0.0/8"})
	client.CompileSourcePolicy()
	if client.EntryAclMode != AclBlacklist || client.EntryAclRules != "127.0.0.1\n10.0.0.0/8" {
		t.Fatalf("CompileSourcePolicy() = (%d, %q), want promoted blacklist", client.EntryAclMode, client.EntryAclRules)
	}
	if client.AllowsSourceAddr("127.0.0.1:80") {
		t.Fatal("promoted legacy blacklist entry should deny matching source")
	}
	if !client.AllowsSourceAddr("8.8.8.8:53") {
		t.Fatal("promoted legacy blacklist should allow non-matching source")
	}
}

func TestExplicitEntryACLsApplyToUserTunnelAndHost(t *testing.T) {
	user := &User{EntryAclMode: AclWhitelist, EntryAclRules: "192.168.0.0/16"}
	InitializeUserRuntime(user)
	if !user.AllowsSourceAddr("192.168.1.10:443") {
		t.Fatal("user whitelist should allow matching source")
	}
	if user.AllowsSourceAddr("10.0.0.1:443") {
		t.Fatal("user whitelist should deny non-matching source")
	}

	tunnel := &Tunnel{EntryAclMode: AclBlacklist, EntryAclRules: "10.0.0.0/8"}
	tunnel.CompileEntryACL()
	if tunnel.AllowsSourceAddr("10.1.2.3:9000") {
		t.Fatal("tunnel blacklist should deny private source")
	}
	if !tunnel.AllowsSourceAddr("8.8.8.8:9000") {
		t.Fatal("tunnel blacklist should allow public source")
	}

	host := &Host{EntryAclMode: AclWhitelist, EntryAclRules: "::1/128"}
	host.CompileEntryACL()
	if !host.AllowsSourceAddr("[::1]:443") {
		t.Fatal("host whitelist should allow matching IPv6 source")
	}
	if host.AllowsSourceAddr("[2001:db8::1]:443") {
		t.Fatal("host whitelist should deny non-matching IPv6 source")
	}
}

func TestUserAndTunnelDestinationACLsApply(t *testing.T) {
	user := &User{
		DestAclMode:  AclWhitelist,
		DestAclRules: "full:db.internal.example\n10.0.0.0/8",
	}
	InitializeUserRuntime(user)
	if !user.AllowsDestination("db.internal.example:443") {
		t.Fatal("user destination whitelist should allow matching host")
	}
	if !user.AllowsDestination("10.1.2.3:443") {
		t.Fatal("user destination whitelist should allow matching IP")
	}
	if user.AllowsDestination("api.public.example:443") {
		t.Fatal("user destination whitelist should deny unmatched host")
	}
	if user.AllowsDestinationIP("db.internal.example:443") {
		t.Fatal("ip-only destination policy should ignore user domain rules")
	}
	if !user.AllowsDestinationIP("10.1.2.3:443") {
		t.Fatal("ip-only destination policy should still allow matching IP")
	}

	tunnel := &Tunnel{
		DestAclMode:  AclBlacklist,
		DestAclRules: "full:blocked.internal.example",
	}
	tunnel.CompileDestACL()
	if tunnel.AllowsDestination("blocked.internal.example:443") {
		t.Fatal("tunnel destination blacklist should deny matching host")
	}
	if !tunnel.AllowsDestination("db.internal.example:443") {
		t.Fatal("tunnel destination blacklist should allow unmatched host")
	}
	if !tunnel.AllowsDestinationIP("db.internal.example:443") {
		t.Fatal("ip-only destination blacklist should ignore non-IP tokens")
	}
}

func TestConcurrentRuntimeTrafficInitialization(t *testing.T) {
	user := &User{TotalFlow: &Flow{InletFlow: 1, ExportFlow: 2}}
	client := &Client{Flow: &Flow{InletFlow: 3, ExportFlow: 4}}
	tunnel := &Tunnel{Flow: &Flow{InletFlow: 5, ExportFlow: 6}}
	host := &Host{Flow: &Flow{InletFlow: 7, ExportFlow: 8}}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if err := user.ObserveTotalTraffic(1, 1); err != nil {
					t.Errorf("ObserveTotalTraffic() error = %v", err)
					return
				}
				if err := client.ObserveBridgeTraffic(1, 0); err != nil {
					t.Errorf("ObserveBridgeTraffic() error = %v", err)
					return
				}
				if err := client.ObserveServiceTraffic(0, 1); err != nil {
					t.Errorf("ObserveServiceTraffic(client) error = %v", err)
					return
				}
				if err := tunnel.ObserveServiceTraffic(1, 1); err != nil {
					t.Errorf("ObserveServiceTraffic(tunnel) error = %v", err)
					return
				}
				if err := host.ObserveServiceTraffic(1, 1); err != nil {
					t.Errorf("ObserveServiceTraffic(host) error = %v", err)
					return
				}
				_, _, _ = user.TotalTrafficTotals()
				_, _, _ = client.TotalTrafficTotals()
				_, _, _ = tunnel.ServiceTrafficTotals()
				_, _, _ = host.ServiceTrafficTotals()
			}
		}()
	}
	wg.Wait()

	if user.TotalTraffic == nil || user.TotalMeter == nil {
		t.Fatalf("user runtime traffic = %#v / %#v, want initialized", user.TotalTraffic, user.TotalMeter)
	}
	if client.BridgeTraffic == nil || client.ServiceTraffic == nil || client.BridgeMeter == nil || client.ServiceMeter == nil || client.TotalMeter == nil {
		t.Fatalf("client runtime traffic not fully initialized: %+v", client)
	}
	if tunnel.ServiceTraffic == nil || tunnel.ServiceMeter == nil {
		t.Fatalf("tunnel runtime traffic = %#v / %#v, want initialized", tunnel.ServiceTraffic, tunnel.ServiceMeter)
	}
	if host.ServiceTraffic == nil || host.ServiceMeter == nil {
		t.Fatalf("host runtime traffic = %#v / %#v, want initialized", host.ServiceTraffic, host.ServiceMeter)
	}
}
