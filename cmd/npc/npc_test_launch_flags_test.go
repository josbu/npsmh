//go:build !sdk

package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	flag "github.com/spf13/pflag"
)

func TestCommandNeedsLaunchResolution(t *testing.T) {
	for _, cmd := range []string{"start", "stop", "restart", "install", "uninstall", "update"} {
		if commandNeedsLaunchResolution(cmd) {
			t.Fatalf("commandNeedsLaunchResolution(%q) = true, want false", cmd)
		}
	}
	for _, cmd := range []string{"", "status", "register", "service"} {
		if !commandNeedsLaunchResolution(cmd) {
			t.Fatalf("commandNeedsLaunchResolution(%q) = false, want true", cmd)
		}
	}
}

func TestResolveLaunchFlags_DirectLaunchPopulatesFlags(t *testing.T) {
	resetNPCFlagState()
	*launchPayloads = []string{"npc://demo@127.0.0.1:8024?type=tls&local_ip=192.168.1.10&log=off"}

	if err := resolveLaunchFlags(); err != nil {
		t.Fatalf("resolveLaunchFlags() error = %v", err)
	}
	if *serverAddr != "127.0.0.1:8024" || *verifyKey != "demo" || *connType != "tls" {
		t.Fatalf("unexpected direct flags: server=%q vkey=%q type=%q", *serverAddr, *verifyKey, *connType)
	}
	if *localIP != "192.168.1.10" || *logType != "off" {
		t.Fatalf("unexpected local/log flags: local_ip=%q log=%q", *localIP, *logType)
	}
	if resolvedLaunch == nil || resolvedLaunch.Mode() != "direct" {
		t.Fatalf("unexpected resolved launch: %+v", resolvedLaunch)
	}
}

func TestResolveLaunchFlags_DoesNotOverrideExplicitCLI(t *testing.T) {
	resetNPCFlagState()
	*launchPayloads = []string{"npc://launch-key@127.0.0.1:8024?type=tls&proxy=http://127.0.0.1:8080&local_ip=192.168.1.10&log=off"}
	*serverAddr = "cli.example.com:9000"
	*verifyKey = "cli-key"
	*connType = "ws"
	*proxyUrl = "socks5://127.0.0.1:1080"
	*localIP = "10.0.0.2"
	for _, name := range []string{"server", "vkey", "type", "proxy", "local_ip"} {
		if f := flag.CommandLine.Lookup(name); f != nil {
			f.Changed = true
		}
	}

	if err := resolveLaunchFlags(); err != nil {
		t.Fatalf("resolveLaunchFlags() error = %v", err)
	}
	if *serverAddr != "cli.example.com:9000" || *verifyKey != "cli-key" || *connType != "ws" {
		t.Fatalf("explicit connection flags were overwritten: server=%q vkey=%q type=%q", *serverAddr, *verifyKey, *connType)
	}
	if *proxyUrl != "socks5://127.0.0.1:1080" || *localIP != "10.0.0.2" {
		t.Fatalf("explicit proxy/local flags were overwritten: proxy=%q local_ip=%q", *proxyUrl, *localIP)
	}
	if *logType != "off" {
		t.Fatalf("runtime fallback flag not applied, log=%q", *logType)
	}
}

func TestResolveLaunchFlags_UsesEnvFallback(t *testing.T) {
	resetNPCFlagState()
	t.Setenv("NPC_LAUNCH", "npc://env-key@127.0.0.1:8024?type=quic")

	if err := resolveLaunchFlags(); err != nil {
		t.Fatalf("resolveLaunchFlags() error = %v", err)
	}
	if *serverAddr != "127.0.0.1:8024" || *verifyKey != "env-key" || *connType != "quic" {
		t.Fatalf("unexpected env launch flags: server=%q vkey=%q type=%q", *serverAddr, *verifyKey, *connType)
	}
	if resolvedLaunch == nil || resolvedLaunch.Mode() != "direct" {
		t.Fatalf("unexpected resolved launch from environment: %+v", resolvedLaunch)
	}
}

func TestResolveLaunchFlags_MultipleLaunchFlagsMergeProfiles(t *testing.T) {
	resetNPCFlagState()
	*launchPayloads = []string{
		"npc://edge-a@10.0.0.1:8024?type=tcp",
		"npc://edge-b@10.0.0.2:8024/ws?type=ws&log=off",
	}

	if err := resolveLaunchFlags(); err != nil {
		t.Fatalf("resolveLaunchFlags() error = %v", err)
	}
	if resolvedLaunch == nil || resolvedLaunch.Mode() != "profiles" {
		t.Fatalf("unexpected resolved launch: %+v", resolvedLaunch)
	}
	if len(resolvedLaunch.ExpandProfiles()) != 2 {
		t.Fatalf("ExpandProfiles() len = %d, want 2", len(resolvedLaunch.ExpandProfiles()))
	}
	if *logType != "off" {
		t.Fatalf("runtime overlay not applied, log=%q", *logType)
	}
}

func TestLaunchCommandArgs_UsesConfigLaunchCommon(t *testing.T) {
	resetNPCFlagState()
	resolvedLaunch = &client.LaunchSpec{
		Config: &client.LaunchConfig{
			Common: &client.LaunchCommon{
				ServerAddr: "10.0.0.2:8024",
				VKey:       "cfg-key",
				ConnType:   "ws",
				ProxyURL:   "http://127.0.0.1:8080",
				LocalIP:    "10.0.0.3",
			},
		},
	}

	server, vkey, tp, proxy, ip, err := launchCommandArgs()
	if err != nil {
		t.Fatalf("launchCommandArgs() error = %v", err)
	}
	if server != "10.0.0.2:8024" || vkey != "cfg-key" || tp != "ws" || proxy != "http://127.0.0.1:8080" || ip != "10.0.0.3" {
		t.Fatalf("unexpected command args: %q %q %q %q %q", server, vkey, tp, proxy, ip)
	}
}

func TestLaunchCommandArgs_PrefersExplicitFlags(t *testing.T) {
	resetNPCFlagState()
	resolvedLaunch = &client.LaunchSpec{
		Config: &client.LaunchConfig{
			Common: &client.LaunchCommon{
				ServerAddr: "10.0.0.2:8024",
				VKey:       "cfg-key",
				ConnType:   "ws",
				ProxyURL:   "http://127.0.0.1:8080",
				LocalIP:    "10.0.0.3",
			},
		},
	}
	*serverAddr = "cli.example.com:9000"
	*verifyKey = "cli-key"
	*connType = "tls"
	*proxyUrl = "socks5://127.0.0.1:1080"
	*localIP = "10.0.0.9"

	server, vkey, tp, proxy, ip, err := launchCommandArgs()
	if err != nil {
		t.Fatalf("launchCommandArgs() error = %v", err)
	}
	if server != "cli.example.com:9000" || vkey != "cli-key" || tp != "tls" || proxy != "socks5://127.0.0.1:1080" || ip != "10.0.0.9" {
		t.Fatalf("explicit command args should win: %q %q %q %q %q", server, vkey, tp, proxy, ip)
	}
}

func TestLaunchCommandArgs_MultiProfileFails(t *testing.T) {
	resetNPCFlagState()
	var err error
	resolvedLaunch, err = client.ResolveLaunchSpec(context.Background(), `{
		"profiles": [
			{"name":"edge-a","direct":{"server":"10.0.0.1:8024","vkey":"k1","type":"tcp"}},
			{"name":"edge-b","direct":{"server":"10.0.0.2:8024","vkey":"k2","type":"tcp"}}
		]
	}`)
	if err != nil {
		t.Fatalf("ResolveLaunchSpec() error = %v", err)
	}

	if _, _, _, _, _, err := launchCommandArgs(); err == nil {
		t.Fatal("launchCommandArgs() error = nil, want non-nil for multiple profiles")
	}
}

func TestPlanLaunchSourceFailure_TemporaryUsesLastGood(t *testing.T) {
	resetNPCFlagState()
	plan := planLaunchSourceFailure("launch input 1", &client.LaunchSourceError{
		Source:     "https://example.com/launch",
		Temporary:  true,
		RetryAfter: 12 * time.Second,
		Err:        context.DeadlineExceeded,
	}, true)
	if plan.Status != "source_retry" {
		t.Fatalf("plan.Status = %q, want source_retry", plan.Status)
	}
	if !plan.UseLastGood {
		t.Fatal("plan.UseLastGood = false, want true")
	}
	if plan.Delay != 12*time.Second {
		t.Fatalf("plan.Delay = %s, want 12s", plan.Delay)
	}
	if plan.Source != "https://example.com/launch" {
		t.Fatalf("plan.Source = %q, want remote source", plan.Source)
	}
}

func TestPlanLaunchSourceFailure_RevokedPausesWithoutLastGood(t *testing.T) {
	resetNPCFlagState()
	plan := planLaunchSourceFailure("launch input 1", &client.LaunchSourceError{
		Source:     "https://example.com/launch",
		StatusCode: 410,
		Revoked:    true,
		Err:        context.Canceled,
	}, true)
	if plan.Status != "source_revoked" {
		t.Fatalf("plan.Status = %q, want source_revoked", plan.Status)
	}
	if plan.UseLastGood {
		t.Fatal("plan.UseLastGood = true, want false")
	}
	if plan.Delay != defaultLaunchSourcePauseDelay {
		t.Fatalf("plan.Delay = %s, want %s", plan.Delay, defaultLaunchSourcePauseDelay)
	}
}

func TestNormalizeLegacyLongFlags_RewritesKnownFlags(t *testing.T) {
	oldArgs := append([]string(nil), os.Args...)
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"npc",
		"-server=1.1.1.1:8024",
		"-local-ip=192.168.1.10",
		"-unknown=value",
		"--config=conf/npc.conf",
		"-s",
		"status",
	}

	normalizeLegacyLongFlags()

	want := []string{
		"npc",
		"--server=1.1.1.1:8024",
		"--local-ip=192.168.1.10",
		"-unknown=value",
		"--config=conf/npc.conf",
		"-s",
		"status",
	}
	if !reflect.DeepEqual(os.Args, want) {
		t.Fatalf("normalizeLegacyLongFlags() args = %#v, want %#v", os.Args, want)
	}
}

func TestApplyLaunchLocal_PopulatesFlags(t *testing.T) {
	resetNPCFlagState()
	port := 2300
	fallback := false
	enableProxy := true

	applyLaunchLocal(&client.LaunchLocal{
		Server:         " 127.0.0.1:8024 ",
		VKey:           " demo-key ",
		Type:           " TLS ",
		Proxy:          " socks5://127.0.0.1:1080 ",
		LocalIP:        " 192.168.1.10 ",
		LocalType:      " secret ",
		LocalPort:      &port,
		Password:       " pass ",
		TargetAddr:     " 10.0.0.2:9000 ",
		TargetType:     " udp ",
		FallbackSecret: &fallback,
		LocalProxy:     &enableProxy,
	})

	if *serverAddr != "127.0.0.1:8024" || *verifyKey != "demo-key" || *connType != "TLS" {
		t.Fatalf("unexpected server flags: server=%q vkey=%q type=%q", *serverAddr, *verifyKey, *connType)
	}
	if *proxyUrl != "socks5://127.0.0.1:1080" || *localIP != "192.168.1.10" || *localType != "secret" {
		t.Fatalf("unexpected local flags: proxy=%q local_ip=%q local_type=%q", *proxyUrl, *localIP, *localType)
	}
	if *localPort != 2300 || *password != "pass" || *target != "10.0.0.2:9000" || *targetType != "udp" {
		t.Fatalf("unexpected local target flags: port=%d password=%q target=%q type=%q", *localPort, *password, *target, *targetType)
	}
	if *fallbackSecret || !*localProxy {
		t.Fatalf("unexpected bool flags: fallback_secret=%t local_proxy=%t", *fallbackSecret, *localProxy)
	}
}

func TestApplyLaunchLocal_DoesNotOverrideExplicitFlags(t *testing.T) {
	resetNPCFlagState()
	*serverAddr = "cli.example.com:9000"
	*verifyKey = "cli-key"
	*connType = "ws"
	*proxyUrl = "http://127.0.0.1:8080"
	*localIP = "10.0.0.2"
	*localType = "p2p"
	*localPort = 9999
	*password = "cli-pass"
	*target = "cli-target"
	*targetType = "tcp"
	*fallbackSecret = true
	*localProxy = false

	for _, name := range []string{
		"server", "vkey", "type", "proxy", "local_ip",
		"local_type", "local_port", "password", "target", "target_type",
		"fallback_secret", "local_proxy",
	} {
		setNPCFlagChanged(t, name, true)
	}

	applyLaunchLocal(&client.LaunchLocal{
		Server:         "10.0.0.1:8024",
		VKey:           "launch-key",
		Type:           "tls",
		Proxy:          "socks5://127.0.0.1:1080",
		LocalIP:        "192.168.1.10",
		LocalType:      "secret",
		LocalPort:      npcInt(2300),
		Password:       "launch-pass",
		Target:         "launch-target",
		TargetType:     "udp",
		FallbackSecret: npcBool(false),
		LocalProxy:     npcBool(true),
	})

	if *serverAddr != "cli.example.com:9000" || *verifyKey != "cli-key" || *connType != "ws" {
		t.Fatalf("explicit server flags were overwritten: server=%q vkey=%q type=%q", *serverAddr, *verifyKey, *connType)
	}
	if *proxyUrl != "http://127.0.0.1:8080" || *localIP != "10.0.0.2" || *localType != "p2p" {
		t.Fatalf("explicit local flags were overwritten: proxy=%q local_ip=%q local_type=%q", *proxyUrl, *localIP, *localType)
	}
	if *localPort != 9999 || *password != "cli-pass" || *target != "cli-target" || *targetType != "tcp" {
		t.Fatalf("explicit target flags were overwritten: port=%d password=%q target=%q type=%q", *localPort, *password, *target, *targetType)
	}
	if !*fallbackSecret || *localProxy {
		t.Fatalf("explicit bool flags were overwritten: fallback_secret=%t local_proxy=%t", *fallbackSecret, *localProxy)
	}
}

func TestLaunchCommandArgs_ProfileModes(t *testing.T) {
	tests := []struct {
		name        string
		spec        *client.LaunchSpec
		wantServer  string
		wantVKey    string
		wantType    string
		wantProxy   string
		wantLocalIP string
	}{
		{
			name: "direct profile defaults to tcp",
			spec: &client.LaunchSpec{Profiles: []client.LaunchProfile{{
				Name: "edge",
				Direct: &client.LaunchDirect{
					Server: []string{"10.0.0.1:8024"},
					VKey:   []string{"edge-key"},
				},
			}}},
			wantServer: "10.0.0.1:8024",
			wantVKey:   "edge-key",
			wantType:   "tcp",
		},
		{
			name: "config profile defaults to tcp",
			spec: &client.LaunchSpec{Profiles: []client.LaunchProfile{{
				Config: &client.LaunchConfig{
					Common: &client.LaunchCommon{
						ServerAddr: "10.0.0.2:8024",
						VKey:       "cfg-key",
						ProxyURL:   "http://127.0.0.1:8080",
						LocalIP:    "10.0.0.3",
					},
				},
			}}},
			wantServer:  "10.0.0.2:8024",
			wantVKey:    "cfg-key",
			wantType:    "tcp",
			wantProxy:   "http://127.0.0.1:8080",
			wantLocalIP: "10.0.0.3",
		},
		{
			name: "local profile defaults to tcp",
			spec: &client.LaunchSpec{Profiles: []client.LaunchProfile{{
				Local: &client.LaunchLocal{
					Server:  "10.0.0.4:8024",
					VKey:    "local-key",
					Proxy:   "socks5://127.0.0.1:1080",
					LocalIP: "10.0.0.4",
				},
			}}},
			wantServer:  "10.0.0.4:8024",
			wantVKey:    "local-key",
			wantType:    "tcp",
			wantProxy:   "socks5://127.0.0.1:1080",
			wantLocalIP: "10.0.0.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetNPCFlagState()
			resolvedLaunch = tt.spec

			server, vkey, tp, proxy, ip, err := launchCommandArgs()
			if err != nil {
				t.Fatalf("launchCommandArgs() error = %v", err)
			}
			if server != tt.wantServer || vkey != tt.wantVKey || tp != tt.wantType || proxy != tt.wantProxy || ip != tt.wantLocalIP {
				t.Fatalf("launchCommandArgs() = %q %q %q %q %q, want %q %q %q %q %q", server, vkey, tp, proxy, ip, tt.wantServer, tt.wantVKey, tt.wantType, tt.wantProxy, tt.wantLocalIP)
			}
		})
	}
}

func TestLaunchCommandArgs_ProfileConfigWithoutCommonFails(t *testing.T) {
	resetNPCFlagState()
	resolvedLaunch = &client.LaunchSpec{Profiles: []client.LaunchProfile{{
		Name:   "remote",
		Config: &client.LaunchConfig{Source: "https://example.com/npc-launch.json"},
	}}}

	if _, _, _, _, _, err := launchCommandArgs(); err == nil || !strings.Contains(err.Error(), "config common settings") {
		t.Fatalf("launchCommandArgs() error = %v, want config common settings failure", err)
	}
}

func TestHasExplicitLegacyModeFlags(t *testing.T) {
	resetNPCFlagState()
	if hasExplicitLegacyModeFlags() {
		t.Fatal("hasExplicitLegacyModeFlags() = true, want false")
	}

	setNPCFlagChanged(t, "target", true)
	if !hasExplicitLegacyModeFlags() {
		t.Fatal("hasExplicitLegacyModeFlags() = false, want true")
	}
}

func TestResolvedLaunchRuntime(t *testing.T) {
	resetNPCFlagState()
	if resolvedLaunchRuntime() != nil {
		t.Fatal("resolvedLaunchRuntime() != nil for empty state")
	}

	runtime := &client.LaunchRuntime{Log: npcString("off")}
	resolvedLaunch = &client.LaunchSpec{Runtime: runtime}
	if resolvedLaunchRuntime() != runtime {
		t.Fatalf("resolvedLaunchRuntime() = %p, want %p", resolvedLaunchRuntime(), runtime)
	}
}
