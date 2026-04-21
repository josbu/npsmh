package file

import (
	"testing"

	"github.com/djylb/nps/lib/common"
)

func TestTunnelSelectRuntimeRouteUsesBoundOwner(t *testing.T) {
	tunnel := &Tunnel{
		Id:     1,
		Client: &Client{Id: 11, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "base:1"},
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{
		Target:      &Target{TargetStr: "owner-a:1"},
		TargetType:  "tcp",
		HttpProxy:   true,
		Socks5Proxy: false,
	})

	selected := tunnel.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}
	if got := selected.RuntimeRouteUUID(); got != "node-a" {
		t.Fatalf("RuntimeRouteUUID() = %q, want %q", got, "node-a")
	}
	if selected.Target == nil || selected.Target.TargetStr != "owner-a:1" {
		t.Fatalf("selected target = %+v, want owner target", selected.Target)
	}
	if !selected.HttpProxy {
		t.Fatal("selected route should inherit owner-specific fields")
	}
}

func TestTunnelSelectRuntimeRouteRoundRobinAcrossOwners(t *testing.T) {
	tunnel := &Tunnel{
		Id:     2,
		Client: &Client{Id: 12, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "base:2"},
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{Target: &Target{TargetStr: "owner-a:2"}})
	tunnel.BindRuntimeOwner("node-b", &Tunnel{Target: &Target{TargetStr: "owner-b:2"}})

	first := tunnel.SelectRuntimeRoute()
	second := tunnel.SelectRuntimeRoute()
	third := tunnel.SelectRuntimeRoute()
	if first == nil || second == nil || third == nil {
		t.Fatal("SelectRuntimeRoute() returned nil route")
	}
	if first.RuntimeRouteUUID() != "node-a" || second.RuntimeRouteUUID() != "node-b" || third.RuntimeRouteUUID() != "node-a" {
		t.Fatalf("round robin order = %q, %q, %q; want node-a, node-b, node-a", first.RuntimeRouteUUID(), second.RuntimeRouteUUID(), third.RuntimeRouteUUID())
	}
}

func TestHostRemoveRuntimeOwnerPromotesRemainingOwner(t *testing.T) {
	ownerAAuth := &MultiAccount{Content: "owner-a-auth"}
	ownerAUsers := &MultiAccount{Content: "owner-a-users"}
	ownerBAuth := &MultiAccount{Content: "owner-b-auth"}
	ownerBUsers := &MultiAccount{Content: "owner-b-users"}
	host := &Host{
		Id:            3,
		Client:        &Client{Id: 13, Cnf: &Config{}},
		Flow:          &Flow{},
		Target:        &Target{TargetStr: "base:3"},
		TargetIsHttps: false,
		HeaderChange:  "A=B",
		RedirectURL:   "https://owner-a.example.com",
		TlsOffload:    true,
		EntryAclMode:  AclWhitelist,
		UserAuth:      ownerAAuth,
		MultiAccount:  ownerAUsers,
	}
	host.BindRuntimeOwner("node-a", &Host{
		Target:           &Target{TargetStr: "owner-a:3"},
		TargetIsHttps:    false,
		HeaderChange:     "A=B",
		RespHeaderChange: "X=Y",
		RedirectURL:      "https://owner-a.example.com",
		TlsOffload:       true,
		EntryAclMode:     AclWhitelist,
		UserAuth:         ownerAAuth,
		MultiAccount:     ownerAUsers,
	})
	host.BindRuntimeOwner("node-b", &Host{
		Target:           &Target{TargetStr: "owner-b:3"},
		TargetIsHttps:    true,
		HeaderChange:     "C=D",
		RespHeaderChange: "M=N",
		RedirectURL:      "https://owner-b.example.com",
		TlsOffload:       false,
		EntryAclMode:     AclBlacklist,
		UserAuth:         ownerBAuth,
		MultiAccount:     ownerBUsers,
	})

	if remaining := host.RemoveRuntimeOwner("node-a"); remaining != 1 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 1", remaining)
	}

	selected := host.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}
	if got := selected.RuntimeRouteUUID(); got != "node-b" {
		t.Fatalf("RuntimeRouteUUID() = %q, want %q", got, "node-b")
	}
	if selected.Target == nil || selected.Target.TargetStr != "owner-b:3" {
		t.Fatalf("selected target = %+v, want owner-b", selected.Target)
	}
	if !selected.TargetIsHttps {
		t.Fatal("remaining owner state was not promoted")
	}
	if selected.RedirectURL != "https://owner-b.example.com" {
		t.Fatalf("selected.RedirectURL = %q, want owner-b redirect", selected.RedirectURL)
	}
	if selected.TlsOffload {
		t.Fatal("selected.TlsOffload = true, want owner-b TLS offload state")
	}
	if selected.EntryAclMode != AclBlacklist {
		t.Fatalf("selected.EntryAclMode = %d, want %d", selected.EntryAclMode, AclBlacklist)
	}
	if selected.UserAuth != ownerBAuth {
		t.Fatalf("selected.UserAuth = %p, want owner-b auth %p", selected.UserAuth, ownerBAuth)
	}
	if selected.MultiAccount != ownerBUsers {
		t.Fatalf("selected.MultiAccount = %p, want owner-b users %p", selected.MultiAccount, ownerBUsers)
	}
}

func TestHostSelectRuntimeRoutePreservesCanonicalFrontendFields(t *testing.T) {
	ownerAAuth := &MultiAccount{Content: "owner-a-auth"}
	ownerAUsers := &MultiAccount{Content: "owner-a-users"}
	ownerBAuth := &MultiAccount{Content: "owner-b-auth"}
	ownerBUsers := &MultiAccount{Content: "owner-b-users"}
	host := &Host{
		Id:            6,
		Client:        &Client{Id: 16, Cnf: &Config{}},
		Flow:          &Flow{},
		Target:        &Target{TargetStr: "base:6"},
		HeaderChange:  "A=B",
		PathRewrite:   "/stable",
		RedirectURL:   "https://stable.example.com",
		TlsOffload:    true,
		EntryAclMode:  AclWhitelist,
		UserAuth:      ownerAAuth,
		MultiAccount:  ownerAUsers,
		TargetIsHttps: false,
	}
	host.BindRuntimeOwner("node-a", &Host{
		Target:           &Target{TargetStr: "owner-a:6"},
		TargetIsHttps:    false,
		HeaderChange:     "A=B",
		PathRewrite:      "/stable",
		RedirectURL:      "https://stable.example.com",
		TlsOffload:       true,
		EntryAclMode:     AclWhitelist,
		UserAuth:         ownerAAuth,
		MultiAccount:     ownerAUsers,
		RespHeaderChange: "X=A",
	})
	host.BindRuntimeOwner("node-b", &Host{
		Target:           &Target{TargetStr: "owner-b:6"},
		TargetIsHttps:    true,
		HeaderChange:     "C=D",
		PathRewrite:      "/moving",
		RedirectURL:      "https://moving.example.com",
		TlsOffload:       false,
		EntryAclMode:     AclBlacklist,
		UserAuth:         ownerBAuth,
		MultiAccount:     ownerBUsers,
		RespHeaderChange: "X=B",
	})

	first := host.SelectRuntimeRoute()
	second := host.SelectRuntimeRoute()
	if first == nil || second == nil {
		t.Fatal("SelectRuntimeRoute() returned nil route")
	}
	if first.RuntimeRouteUUID() != "node-a" || second.RuntimeRouteUUID() != "node-b" {
		t.Fatalf("round robin order = %q, %q; want node-a, node-b", first.RuntimeRouteUUID(), second.RuntimeRouteUUID())
	}
	if second.Target == nil || second.Target.TargetStr != "owner-b:6" {
		t.Fatalf("selected target = %+v, want owner-b backend", second.Target)
	}
	if !second.TargetIsHttps {
		t.Fatal("selected backend should inherit owner-b target https flag")
	}
	if second.HeaderChange != "A=B" {
		t.Fatalf("selected.HeaderChange = %q, want canonical header change", second.HeaderChange)
	}
	if second.PathRewrite != "/stable" {
		t.Fatalf("selected.PathRewrite = %q, want canonical path rewrite", second.PathRewrite)
	}
	if second.RedirectURL != "https://stable.example.com" {
		t.Fatalf("selected.RedirectURL = %q, want canonical redirect", second.RedirectURL)
	}
	if !second.TlsOffload {
		t.Fatal("selected.TlsOffload = false, want canonical TLS offload")
	}
	if second.EntryAclMode != AclWhitelist {
		t.Fatalf("selected.EntryAclMode = %d, want %d", second.EntryAclMode, AclWhitelist)
	}
	if second.UserAuth != ownerAAuth {
		t.Fatalf("selected.UserAuth = %p, want canonical auth %p", second.UserAuth, ownerAAuth)
	}
	if second.MultiAccount != ownerAUsers {
		t.Fatalf("selected.MultiAccount = %p, want canonical users %p", second.MultiAccount, ownerAUsers)
	}
}

func TestTunnelSelectRuntimeRoutePreservesCanonicalFrontendFields(t *testing.T) {
	ownerAAuth := &MultiAccount{Content: "owner-a-auth"}
	ownerAUsers := &MultiAccount{Content: "owner-a-users"}
	ownerBAuth := &MultiAccount{Content: "owner-b-auth"}
	ownerBUsers := &MultiAccount{Content: "owner-b-users"}
	tunnel := &Tunnel{
		Id:           12,
		Client:       &Client{Id: 22, Cnf: &Config{}},
		Flow:         &Flow{},
		Mode:         "mixProxy",
		Target:       &Target{TargetStr: "owner-a:12"},
		TargetType:   common.CONN_ALL,
		HttpProxy:    true,
		Socks5Proxy:  false,
		EntryAclMode: AclWhitelist,
		DestAclMode:  AclBlacklist,
		DestAclRules: "full:blocked.example",
		LocalPath:    "/stable",
		StripPre:     "/files",
		ReadOnly:     true,
		UserAuth:     ownerAAuth,
		MultiAccount: ownerAUsers,
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{
		Mode:         "mixProxy",
		Target:       &Target{TargetStr: "owner-a:12"},
		TargetType:   common.CONN_ALL,
		HttpProxy:    true,
		Socks5Proxy:  false,
		EntryAclMode: AclWhitelist,
		DestAclMode:  AclBlacklist,
		DestAclRules: "full:blocked.example",
		LocalPath:    "/stable",
		StripPre:     "/files",
		ReadOnly:     true,
		UserAuth:     ownerAAuth,
		MultiAccount: ownerAUsers,
	})
	tunnel.BindRuntimeOwner("node-b", &Tunnel{
		Mode:         "tcp",
		Target:       &Target{TargetStr: "owner-b:12"},
		TargetType:   common.CONN_UDP,
		HttpProxy:    false,
		Socks5Proxy:  true,
		EntryAclMode: AclBlacklist,
		DestAclMode:  AclWhitelist,
		DestAclRules: "full:owner-b.example",
		LocalPath:    "/moving",
		StripPre:     "/alt",
		ReadOnly:     false,
		UserAuth:     ownerBAuth,
		MultiAccount: ownerBUsers,
	})

	first := tunnel.SelectRuntimeRoute()
	second := tunnel.SelectRuntimeRoute()
	if first == nil || second == nil {
		t.Fatal("SelectRuntimeRoute() returned nil route")
	}
	if first.RuntimeRouteUUID() != "node-a" || second.RuntimeRouteUUID() != "node-b" {
		t.Fatalf("round robin order = %q, %q; want node-a, node-b", first.RuntimeRouteUUID(), second.RuntimeRouteUUID())
	}
	if second.Target == nil || second.Target.TargetStr != "owner-b:12" {
		t.Fatalf("selected target = %+v, want owner-b backend", second.Target)
	}
	if second.Mode != "mixProxy" {
		t.Fatalf("selected.Mode = %q, want canonical mode %q", second.Mode, "mixProxy")
	}
	if second.TargetType != common.CONN_ALL {
		t.Fatalf("selected.TargetType = %q, want canonical target type %q", second.TargetType, common.CONN_ALL)
	}
	if !second.HttpProxy || second.Socks5Proxy {
		t.Fatalf("selected proxy flags = http:%v socks5:%v, want canonical http true socks5 false", second.HttpProxy, second.Socks5Proxy)
	}
	if second.EntryAclMode != AclWhitelist {
		t.Fatalf("selected.EntryAclMode = %d, want %d", second.EntryAclMode, AclWhitelist)
	}
	if second.DestAclMode != AclBlacklist {
		t.Fatalf("selected.DestAclMode = %d, want %d", second.DestAclMode, AclBlacklist)
	}
	if second.DestAclRules != "full:blocked.example" {
		t.Fatalf("selected.DestAclRules = %q, want canonical rules", second.DestAclRules)
	}
	if second.LocalPath != "/stable" {
		t.Fatalf("selected.LocalPath = %q, want canonical local path", second.LocalPath)
	}
	if second.StripPre != "/files" {
		t.Fatalf("selected.StripPre = %q, want canonical strip prefix", second.StripPre)
	}
	if !second.ReadOnly {
		t.Fatal("selected.ReadOnly = false, want canonical read-only")
	}
	if second.UserAuth != ownerAAuth {
		t.Fatalf("selected.UserAuth = %p, want canonical auth %p", second.UserAuth, ownerAAuth)
	}
	if second.MultiAccount != ownerAUsers {
		t.Fatalf("selected.MultiAccount = %p, want canonical users %p", second.MultiAccount, ownerAUsers)
	}
}

func TestTunnelSelectRuntimeRouteByUUIDUsesRequestedOwner(t *testing.T) {
	ownerAAuth := &MultiAccount{Content: "owner-a-auth"}
	ownerAUsers := &MultiAccount{Content: "owner-a-users"}
	ownerBAuth := &MultiAccount{Content: "owner-b-auth"}
	ownerBUsers := &MultiAccount{Content: "owner-b-users"}
	tunnel := &Tunnel{
		Id:           13,
		Client:       &Client{Id: 23, Cnf: &Config{}},
		Flow:         &Flow{},
		Mode:         "mixProxy",
		Target:       &Target{TargetStr: "owner-a:13"},
		TargetType:   common.CONN_ALL,
		HttpProxy:    true,
		Socks5Proxy:  false,
		EntryAclMode: AclWhitelist,
		DestAclMode:  AclBlacklist,
		DestAclRules: "full:blocked.example",
		LocalPath:    "/stable",
		StripPre:     "/files",
		ReadOnly:     true,
		UserAuth:     ownerAAuth,
		MultiAccount: ownerAUsers,
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{
		Mode:         "mixProxy",
		Target:       &Target{TargetStr: "owner-a:13"},
		TargetType:   common.CONN_ALL,
		HttpProxy:    true,
		Socks5Proxy:  false,
		EntryAclMode: AclWhitelist,
		DestAclMode:  AclBlacklist,
		DestAclRules: "full:blocked.example",
		LocalPath:    "/stable",
		StripPre:     "/files",
		ReadOnly:     true,
		UserAuth:     ownerAAuth,
		MultiAccount: ownerAUsers,
	})
	tunnel.BindRuntimeOwner("node-b", &Tunnel{
		Mode:         "tcp",
		Target:       &Target{TargetStr: "owner-b:13"},
		TargetType:   common.CONN_UDP,
		HttpProxy:    false,
		Socks5Proxy:  true,
		EntryAclMode: AclBlacklist,
		DestAclMode:  AclWhitelist,
		DestAclRules: "full:owner-b.example",
		LocalPath:    "/moving",
		StripPre:     "/alt",
		ReadOnly:     false,
		UserAuth:     ownerBAuth,
		MultiAccount: ownerBUsers,
	})

	selected := tunnel.SelectRuntimeRouteByUUID("node-b")
	if selected == nil {
		t.Fatal("SelectRuntimeRouteByUUID() = nil")
	}
	if selected.RuntimeRouteUUID() != "node-b" {
		t.Fatalf("RuntimeRouteUUID() = %q, want %q", selected.RuntimeRouteUUID(), "node-b")
	}
	if selected.Target == nil || selected.Target.TargetStr != "owner-b:13" {
		t.Fatalf("selected target = %+v, want owner-b backend", selected.Target)
	}
	if selected.Mode != "mixProxy" {
		t.Fatalf("selected.Mode = %q, want canonical mode %q", selected.Mode, "mixProxy")
	}
	if selected.TargetType != common.CONN_ALL {
		t.Fatalf("selected.TargetType = %q, want canonical target type %q", selected.TargetType, common.CONN_ALL)
	}
	if !selected.HttpProxy || selected.Socks5Proxy {
		t.Fatalf("selected proxy flags = http:%v socks5:%v, want canonical http true socks5 false", selected.HttpProxy, selected.Socks5Proxy)
	}
	if selected.DestAclRules != "full:blocked.example" {
		t.Fatalf("selected.DestAclRules = %q, want canonical rules", selected.DestAclRules)
	}
	if selected.UserAuth != ownerAAuth {
		t.Fatalf("selected.UserAuth = %p, want canonical auth %p", selected.UserAuth, ownerAAuth)
	}
	if selected.MultiAccount != ownerAUsers {
		t.Fatalf("selected.MultiAccount = %p, want canonical users %p", selected.MultiAccount, ownerAUsers)
	}
}

func TestTargetCountParsesConfiguredTargets(t *testing.T) {
	target := &Target{TargetStr: "127.0.0.1:80\n127.0.0.1:81"}
	if got := target.TargetCount(); got != 2 {
		t.Fatalf("TargetCount() = %d, want 2", got)
	}
}

func TestCloneTargetSnapshotPreservesCurrentRuntimeTargets(t *testing.T) {
	target := &Target{
		TargetStr:       "alpha:80\nbeta:81",
		TargetArr:       []string{"beta:81"},
		targetArrSource: normalizedTargetSource("alpha:80\nbeta:81"),
	}

	cloned := CloneTargetSnapshot(target)
	got, err := cloned.GetRandomTarget()
	if err != nil {
		t.Fatalf("GetRandomTarget() error = %v", err)
	}
	if got != "beta:81" {
		t.Fatalf("GetRandomTarget() = %q, want %q", got, "beta:81")
	}
}

func TestTunnelSelectRuntimeRouteSharesConnectionCounter(t *testing.T) {
	tunnel := &Tunnel{
		Id:      4,
		Client:  &Client{Id: 14, Cnf: &Config{}},
		Flow:    &Flow{},
		Target:  &Target{TargetStr: "base:4"},
		MaxConn: 1,
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{Target: &Target{TargetStr: "owner-a:4"}})

	selected := tunnel.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}
	if !selected.GetConn() {
		t.Fatal("GetConn() = false, want true for first routed connection")
	}
	if got := tunnel.NowConn; got != 1 {
		t.Fatalf("tunnel.NowConn = %d, want 1", got)
	}
	if selected.GetConn() {
		t.Fatal("GetConn() = true, want false after reaching canonical tunnel limit")
	}
	selected.CutConn()
	if got := tunnel.NowConn; got != 0 {
		t.Fatalf("tunnel.NowConn after release = %d, want 0", got)
	}
}

func TestHostSelectRuntimeRouteSharesConnectionCounter(t *testing.T) {
	host := &Host{
		Id:      5,
		Client:  &Client{Id: 15, Cnf: &Config{}},
		Flow:    &Flow{},
		Target:  &Target{TargetStr: "base:5"},
		MaxConn: 1,
	}
	host.BindRuntimeOwner("node-a", &Host{Target: &Target{TargetStr: "owner-a:5"}})

	selected := host.SelectRuntimeRoute()
	if selected == nil {
		t.Fatal("SelectRuntimeRoute() = nil")
	}
	if !selected.GetConn() {
		t.Fatal("GetConn() = false, want true for first routed host connection")
	}
	if got := host.NowConn; got != 1 {
		t.Fatalf("host.NowConn = %d, want 1", got)
	}
	if selected.GetConn() {
		t.Fatal("GetConn() = true, want false after reaching canonical host limit")
	}
	selected.CutConn()
	if got := host.NowConn; got != 0 {
		t.Fatalf("host.NowConn after release = %d, want 0", got)
	}
}

func TestTunnelUpdateRuntimeTargetHealthUsesOwnerSnapshot(t *testing.T) {
	tunnel := &Tunnel{
		Id:     7,
		Client: &Client{Id: 17, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "owner-a:7"},
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{Target: &Target{TargetStr: "owner-a:7"}})
	tunnel.BindRuntimeOwner("node-b", &Tunnel{Target: &Target{TargetStr: "owner-b:7"}})

	if !tunnel.UpdateRuntimeTargetHealth("node-b", "owner-b:7", false) {
		t.Fatal("UpdateRuntimeTargetHealth() = false, want owner-b unhealthy target applied")
	}

	first := tunnel.SelectRuntimeRoute()
	second := tunnel.SelectRuntimeRoute()
	if first == nil || second == nil {
		t.Fatal("SelectRuntimeRoute() returned nil route")
	}
	if first.RuntimeRouteUUID() != "node-a" || second.RuntimeRouteUUID() != "node-b" {
		t.Fatalf("round robin order = %q, %q; want node-a, node-b", first.RuntimeRouteUUID(), second.RuntimeRouteUUID())
	}
	if got := first.Target.TargetCount(); got != 1 {
		t.Fatalf("owner-a TargetCount() = %d, want 1", got)
	}
	if got := second.Target.TargetCount(); got != 0 {
		t.Fatalf("owner-b TargetCount() = %d, want 0 after health removal", got)
	}
	if got := tunnel.Target.TargetCount(); got != 1 {
		t.Fatalf("canonical TargetCount() = %d, want owner-a canonical target to remain healthy", got)
	}
}

func TestTunnelRemoveRuntimeOwnerPromotesRemainingHealthState(t *testing.T) {
	tunnel := &Tunnel{
		Id:     13,
		Client: &Client{Id: 23, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "owner-a:13"},
		Health: Health{
			HealthCheckType: "tcp",
		},
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{
		Target: &Target{TargetStr: "owner-a:13"},
		Health: Health{
			HealthCheckType: "tcp",
		},
	})
	tunnel.BindRuntimeOwner("node-b", &Tunnel{
		Target: &Target{TargetStr: "owner-b:13"},
		Health: Health{
			HealthCheckType: "http",
			HealthRemoveArr: []string{"owner-b:13"},
		},
	})

	if remaining := tunnel.RemoveRuntimeOwner("node-a"); remaining != 1 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 1", remaining)
	}
	if tunnel.HealthCheckType != "http" {
		t.Fatalf("tunnel.HealthCheckType = %q, want %q", tunnel.HealthCheckType, "http")
	}
	if len(tunnel.HealthRemoveArr) != 1 || tunnel.HealthRemoveArr[0] != "owner-b:13" {
		t.Fatalf("tunnel.HealthRemoveArr = %#v, want [\"owner-b:13\"]", tunnel.HealthRemoveArr)
	}
}

func TestTunnelBindRuntimeOwnerNormalizesSnapshot(t *testing.T) {
	tunnel := &Tunnel{
		Id:     9,
		Client: &Client{Id: 19, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "owner-a:9"},
	}
	tunnel.BindRuntimeOwner("node-a", &Tunnel{Target: &Target{TargetStr: "owner-a:9"}})
	tunnel.BindRuntimeOwner("node-b", &Tunnel{
		Mode:          "httpProxy",
		TargetType:    "unexpected",
		EntryAclMode:  AclWhitelist,
		EntryAclRules: " 203.0.113.10 \n",
		DestAclMode:   AclWhitelist,
		DestAclRules:  " 198.51.100.10:80 \n",
		Target:        &Target{TargetStr: "owner-b:9"},
	})

	if remaining := tunnel.RemoveRuntimeOwner("node-a"); remaining != 1 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 1", remaining)
	}
	if tunnel.Mode != "mixProxy" {
		t.Fatalf("tunnel.Mode = %q, want %q", tunnel.Mode, "mixProxy")
	}
	if !tunnel.HttpProxy || tunnel.Socks5Proxy {
		t.Fatalf("proxy flags = http:%v socks5:%v, want http true socks5 false", tunnel.HttpProxy, tunnel.Socks5Proxy)
	}
	if tunnel.TargetType != common.CONN_ALL {
		t.Fatalf("tunnel.TargetType = %q, want %q", tunnel.TargetType, common.CONN_ALL)
	}
	if tunnel.EntryAclRules != "203.0.113.10" {
		t.Fatalf("tunnel.EntryAclRules = %q, want normalized source ACL", tunnel.EntryAclRules)
	}
	if tunnel.DestAclRules != "198.51.100.10:80" {
		t.Fatalf("tunnel.DestAclRules = %q, want normalized dest ACL", tunnel.DestAclRules)
	}
}

func TestTunnelUpdateRuntimeTargetHealthRejectsSubstringMatch(t *testing.T) {
	tunnel := &Tunnel{
		Id:     10,
		Client: &Client{Id: 20, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "127.0.0.1:8080"},
	}
	if tunnel.UpdateRuntimeTargetHealth("", "127.0.0.1:80", false) {
		t.Fatal("UpdateRuntimeTargetHealth() = true, want false for substring-only match")
	}
	if len(tunnel.HealthRemoveArr) != 0 {
		t.Fatalf("HealthRemoveArr = %#v, want empty", tunnel.HealthRemoveArr)
	}
	if got := tunnel.Target.TargetCount(); got != 1 {
		t.Fatalf("TargetCount() = %d, want 1", got)
	}
}

func TestHostUpdateRuntimeTargetHealthUsesOwnerSnapshot(t *testing.T) {
	host := &Host{
		Id:     8,
		Client: &Client{Id: 18, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "owner-a:8"},
	}
	host.BindRuntimeOwner("node-a", &Host{Target: &Target{TargetStr: "owner-a:8"}})
	host.BindRuntimeOwner("node-b", &Host{Target: &Target{TargetStr: "owner-b:8"}})

	if !host.UpdateRuntimeTargetHealth("node-b", "owner-b:8", false) {
		t.Fatal("UpdateRuntimeTargetHealth() = false, want owner-b unhealthy target applied")
	}

	first := host.SelectRuntimeRoute()
	second := host.SelectRuntimeRoute()
	if first == nil || second == nil {
		t.Fatal("SelectRuntimeRoute() returned nil route")
	}
	if first.RuntimeRouteUUID() != "node-a" || second.RuntimeRouteUUID() != "node-b" {
		t.Fatalf("round robin order = %q, %q; want node-a, node-b", first.RuntimeRouteUUID(), second.RuntimeRouteUUID())
	}
	if got := first.Target.TargetCount(); got != 1 {
		t.Fatalf("owner-a TargetCount() = %d, want 1", got)
	}
	if got := second.Target.TargetCount(); got != 0 {
		t.Fatalf("owner-b TargetCount() = %d, want 0 after health removal", got)
	}
	if got := host.Target.TargetCount(); got != 1 {
		t.Fatalf("canonical TargetCount() = %d, want owner-a canonical target to remain healthy", got)
	}
}

func TestHostRemoveRuntimeOwnerPromotesRemainingHealthState(t *testing.T) {
	host := &Host{
		Id:     14,
		Client: &Client{Id: 24, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "owner-a:14"},
		Health: Health{
			HealthCheckType: "tcp",
		},
	}
	host.BindRuntimeOwner("node-a", &Host{
		Target: &Target{TargetStr: "owner-a:14"},
		Health: Health{
			HealthCheckType: "tcp",
		},
	})
	host.BindRuntimeOwner("node-b", &Host{
		Target: &Target{TargetStr: "owner-b:14"},
		Health: Health{
			HealthCheckType: "http",
			HealthRemoveArr: []string{"owner-b:14"},
		},
	})

	if remaining := host.RemoveRuntimeOwner("node-a"); remaining != 1 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 1", remaining)
	}
	if host.HealthCheckType != "http" {
		t.Fatalf("host.HealthCheckType = %q, want %q", host.HealthCheckType, "http")
	}
	if len(host.HealthRemoveArr) != 1 || host.HealthRemoveArr[0] != "owner-b:14" {
		t.Fatalf("host.HealthRemoveArr = %#v, want [\"owner-b:14\"]", host.HealthRemoveArr)
	}
}

func TestHostBindRuntimeOwnerNormalizesSnapshot(t *testing.T) {
	host := &Host{
		Id:       11,
		Client:   &Client{Id: 21, Cnf: &Config{}},
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "owner-a:11"},
		Location: "/",
		Scheme:   "all",
	}
	host.BindRuntimeOwner("node-a", &Host{Target: &Target{TargetStr: "owner-a:11"}})
	host.BindRuntimeOwner("node-b", &Host{
		EntryAclMode:  AclWhitelist,
		EntryAclRules: " 203.0.113.11 \n",
		Target:        &Target{TargetStr: "owner-b:11"},
	})

	if remaining := host.RemoveRuntimeOwner("node-a"); remaining != 1 {
		t.Fatalf("RemoveRuntimeOwner() remaining = %d, want 1", remaining)
	}
	if host.EntryAclRules != "203.0.113.11" {
		t.Fatalf("host.EntryAclRules = %q, want normalized source ACL", host.EntryAclRules)
	}
	if !host.AllowsSourceAddr("203.0.113.11:1") {
		t.Fatal("AllowsSourceAddr() = false, want true for promoted owner ACL")
	}
	if host.AllowsSourceAddr("203.0.113.12:1") {
		t.Fatal("AllowsSourceAddr() = true, want false for non-whitelisted addr")
	}
}

func TestHostUpdateRuntimeTargetHealthRejectsSubstringMatch(t *testing.T) {
	host := &Host{
		Id:     12,
		Client: &Client{Id: 22, Cnf: &Config{}},
		Flow:   &Flow{},
		Target: &Target{TargetStr: "127.0.0.1:8080"},
	}
	if host.UpdateRuntimeTargetHealth("", "127.0.0.1:80", false) {
		t.Fatal("UpdateRuntimeTargetHealth() = true, want false for substring-only match")
	}
	if len(host.HealthRemoveArr) != 0 {
		t.Fatalf("HealthRemoveArr = %#v, want empty", host.HealthRemoveArr)
	}
	if got := host.Target.TargetCount(); got != 1 {
		t.Fatalf("TargetCount() = %d, want 1", got)
	}
}
