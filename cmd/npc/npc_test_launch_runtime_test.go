//go:build !sdk

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/client"
	"github.com/djylb/nps/lib/config"
)

func TestBuildLaunchLocalConfig_DefaultsAndNormalization(t *testing.T) {
	port := 2300
	fallback := false
	enableProxy := true

	commonConfig, localServer := buildLaunchLocalConfig(&client.LaunchLocal{
		Server:         " 127.0.0.1:8024 ",
		VKey:           " demo-key ",
		Proxy:          " http://127.0.0.1:8080 ",
		LocalIP:        " 192.168.1.10 ",
		LocalPort:      &port,
		Password:       " pass ",
		TargetAddr:     " 10.0.0.2:9000 ",
		FallbackSecret: &fallback,
		LocalProxy:     &enableProxy,
	})

	if commonConfig.Server != "127.0.0.1:8024" || commonConfig.VKey != "demo-key" || commonConfig.Tp != "tcp" {
		t.Fatalf("unexpected common config: server=%q vkey=%q type=%q", commonConfig.Server, commonConfig.VKey, commonConfig.Tp)
	}
	if commonConfig.ProxyUrl != "http://127.0.0.1:8080" || commonConfig.LocalIP != "192.168.1.10" {
		t.Fatalf("unexpected common network settings: proxy=%q local_ip=%q", commonConfig.ProxyUrl, commonConfig.LocalIP)
	}
	if commonConfig.Client == nil || commonConfig.Client.Cnf == nil {
		t.Fatal("buildLaunchLocalConfig() should initialize client config stubs")
	}
	if localServer.Type != "p2p" || localServer.Port != 2300 || localServer.Password != "pass" {
		t.Fatalf("unexpected local server basics: type=%q port=%d password=%q", localServer.Type, localServer.Port, localServer.Password)
	}
	if localServer.Target != "10.0.0.2:9000" || localServer.TargetType != "all" {
		t.Fatalf("unexpected local target settings: target=%q type=%q", localServer.Target, localServer.TargetType)
	}
	if localServer.Fallback || !localServer.LocalProxy {
		t.Fatalf("unexpected local bool settings: fallback=%t local_proxy=%t", localServer.Fallback, localServer.LocalProxy)
	}
}

func TestApplyLaunchLocalRuntimeDefaultsUsesDisconnectTimeout(t *testing.T) {
	resetNPCFlagState()
	*disconnectTime = 43
	*p2pTime = 7

	commonConfig := &config.CommonConfig{}
	applyLaunchLocalRuntimeDefaults(commonConfig)

	if commonConfig.DisconnectTime != 43 {
		t.Fatalf("DisconnectTime = %d, want disconnect_timeout 43 instead of p2p_timeout", commonConfig.DisconnectTime)
	}
}

func TestBuildPreparedDirectNodes_ExpandsInputs(t *testing.T) {
	nodes, err := buildPreparedDirectNodes(&client.LaunchDirect{
		Server:  []string{"10.0.0.1:8024", "10.0.0.2:8024"},
		VKey:    []string{"demo-key"},
		Proxy:   "socks5://127.0.0.1:1080",
		LocalIP: []string{"192.168.1.10"},
	})
	if err != nil {
		t.Fatalf("buildPreparedDirectNodes() error = %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("buildPreparedDirectNodes() len = %d, want 2", len(nodes))
	}
	if nodes[0].Type != "tcp" || nodes[1].Type != "tcp" {
		t.Fatalf("buildPreparedDirectNodes() default type = %#v, want tcp for all nodes", nodes)
	}
	if nodes[1].VKey != "demo-key" || nodes[1].LocalIP != "192.168.1.10" {
		t.Fatalf("buildPreparedDirectNodes() should extend shorter arrays, got %#v", nodes[1])
	}
}

func TestBuildPreparedDirectNodes_Errors(t *testing.T) {
	tests := []struct {
		name   string
		direct *client.LaunchDirect
	}{
		{name: "nil direct", direct: nil},
		{name: "missing server", direct: &client.LaunchDirect{VKey: []string{"demo-key"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildPreparedDirectNodes(tt.direct); err == nil {
				t.Fatal("buildPreparedDirectNodes() error = nil, want non-nil")
			}
		})
	}
}

func TestPrepareLaunchBundle_MixedProfiles(t *testing.T) {
	bundle, err := prepareLaunchBundle(context.Background(), "launch input 1", &client.LaunchSpec{
		Profiles: []client.LaunchProfile{
			{
				Name: "edge",
				Direct: &client.LaunchDirect{
					Server: []string{"10.0.0.1:8024"},
					VKey:   []string{"edge-key"},
					Type:   []string{"TLS"},
				},
			},
			{
				Config: &client.LaunchConfig{
					Common: &client.LaunchCommon{
						ServerAddr: "10.0.0.2:8024",
						VKey:       "cfg-key",
						ConnType:   "ws",
					},
				},
			},
			{
				Local: &client.LaunchLocal{
					Server:     "10.0.0.3:8024",
					VKey:       "local-key",
					Password:   "secret",
					TargetAddr: "127.0.0.1:9000",
				},
			},
		},
	}, &client.LaunchRuntime{
		DNSServer:         npcString("1.1.1.1"),
		AutoReconnect:     npcBool(false),
		DisconnectTimeout: npcInt(45),
	})
	if err != nil {
		t.Fatalf("prepareLaunchBundle() error = %v", err)
	}
	if bundle.Label != "launch input 1" || len(bundle.Runners) != 3 {
		t.Fatalf("unexpected bundle shape: %+v", bundle)
	}
	if bundle.Runners[0].ID != "profile:edge" || bundle.Runners[0].Label != "launch input 1 edge" {
		t.Fatalf("unexpected direct runner metadata: %+v", bundle.Runners[0])
	}
	if len(bundle.Runners[0].DirectNodes) != 1 || bundle.Runners[0].DirectNodes[0].Type != "tls" {
		t.Fatalf("unexpected direct runner nodes: %+v", bundle.Runners[0].DirectNodes)
	}
	if bundle.Runners[1].ID != "profile:1" || bundle.Runners[1].Label != "launch input 1 profile 2" {
		t.Fatalf("unexpected config runner metadata: %+v", bundle.Runners[1])
	}
	if bundle.Runners[1].Config == nil || bundle.Runners[1].Config.CommonConfig == nil {
		t.Fatal("config runner should contain built config")
	}
	if bundle.Runners[1].Config.CommonConfig.DnsServer != "1.1.1.1" || bundle.Runners[1].Config.CommonConfig.AutoReconnection || bundle.Runners[1].Config.CommonConfig.DisconnectTime != 45 {
		t.Fatalf("runtime overlay not applied to config runner: %+v", bundle.Runners[1].Config.CommonConfig)
	}
	if bundle.Runners[2].ID != "profile:2" || bundle.Runners[2].Label != "launch input 1 profile 3" || bundle.Runners[2].Local == nil {
		t.Fatalf("unexpected local runner metadata: %+v", bundle.Runners[2])
	}
}

func TestPrepareLaunchBundle_Errors(t *testing.T) {
	tests := []struct {
		name string
		spec *client.LaunchSpec
		want string
	}{
		{
			name: "nil spec",
			spec: nil,
			want: "launch spec is empty",
		},
		{
			name: "empty spec",
			spec: &client.LaunchSpec{},
			want: "does not contain runnable profiles",
		},
		{
			name: "unsupported profile mode",
			spec: &client.LaunchSpec{Profiles: []client.LaunchProfile{{Name: "broken"}}},
			want: "unsupported launch mode",
		},
		{
			name: "invalid local profile",
			spec: &client.LaunchSpec{Profiles: []client.LaunchProfile{{Local: &client.LaunchLocal{
				Server: "10.0.0.3:8024",
				VKey:   "local-key",
			}}}},
			want: "requires password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := prepareLaunchBundle(context.Background(), "launch input 1", tt.spec, nil); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("prepareLaunchBundle() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestStartLaunchInputs_RejectsEmptyTrimmedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := startLaunchInputs(ctx, cancel, []string{" ", "\t"}, nil); err == nil {
		t.Fatal("startLaunchInputs() error = nil, want non-nil for empty inputs")
	}
}

func TestStartLaunchInputs_CanceledContextReturnsQuickly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := startLaunchInputs(ctx, cancel, []string{" npc://demo@127.0.0.1:8024 "}, nil); err != nil {
		t.Fatalf("startLaunchInputs() error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)
}

func TestRunPreparedBundleOnce_EmptyBundle(t *testing.T) {
	result := runPreparedBundleOnce(context.Background(), func() {}, nil, map[string]string{})
	if result.Err == nil || result.Label != "launch bundle" || result.Reconnect {
		t.Fatalf("unexpected runPreparedBundleOnce() result: %+v", result)
	}
}

func TestRunPreparedRunnerOnce_EmptyRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := runPreparedRunnerOnce(ctx, cancel, cancel, newLaunchLocalManagerRegistry(ctx, cancel), launchPreparedRunner{ID: "empty", Label: "empty"}, "")
	if result.Err == nil || result.ID != "empty" || result.Reconnect {
		t.Fatalf("unexpected runPreparedRunnerOnce() result: %+v", result)
	}
}

func TestPlanLaunchSourceFailure_PausedAndGenericBranches(t *testing.T) {
	resetNPCFlagState()

	paused := planLaunchSourceFailure("launch input 1", &client.LaunchSourceError{
		Source:     "https://example.com/launch",
		StatusCode: 404,
		Err:        errors.New("not found"),
	}, true)
	if paused.Status != "source_paused" || paused.UseLastGood || paused.Delay != defaultLaunchSourcePauseDelay {
		t.Fatalf("unexpected paused plan: %+v", paused)
	}

	generic := planLaunchSourceFailure("launch input 2", errors.New("boom"), true)
	if generic.Status != "source_invalid" || !generic.UseLastGood || generic.Source != "launch input 2" || generic.Delay != defaultLaunchSourceRetryDelay {
		t.Fatalf("unexpected generic plan: %+v", generic)
	}
}

func TestLogLaunchSourceFailure_NoPanic(t *testing.T) {
	for _, plan := range []launchSourceFailurePlan{
		{Status: "source_retry", Source: "retry-source", Delay: time.Second, UseLastGood: true},
		{Status: "source_revoked", Source: "revoked-source", Delay: time.Second},
		{Status: "source_paused", Source: "paused-source", Delay: time.Second},
		{Status: "source_invalid", Source: "invalid-source", Delay: time.Second},
	} {
		logLaunchSourceFailure("launch input 1", errors.New("boom"), plan)
	}
}

func TestSleepLaunchDelay(t *testing.T) {
	if !sleepLaunchDelay(context.Background(), 0) {
		t.Fatal("sleepLaunchDelay() = false, want true for zero delay")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepLaunchDelay(ctx, 50*time.Millisecond) {
		t.Fatal("sleepLaunchDelay() = true, want false for canceled context")
	}

	if !sleepLaunchDelay(context.Background(), 5*time.Millisecond) {
		t.Fatal("sleepLaunchDelay() = false, want true after timer fires")
	}
}

func TestStartLaunchDirectAndLocal_ValidateInputs(t *testing.T) {
	resetNPCFlagState()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := startLaunchDirect(ctx, cancel, "", "", "", "", ""); err == nil {
		t.Fatal("startLaunchDirect() error = nil, want non-nil")
	}
	if err := startLaunchLocal(ctx, cancel, nil); err == nil {
		t.Fatal("startLaunchLocal() error = nil, want non-nil")
	}
}

func TestStartFromConfigFilesCancelsOnLoaderError(t *testing.T) {
	resetNPCFlagState()
	oldStartFromFile := npcStartFromFile
	defer func() { npcStartFromFile = oldStartFromFile }()

	calls := make(chan string, 2)
	npcStartFromFile = func(ctx context.Context, cancel context.CancelFunc, path string) error {
		calls <- path
		return errors.New("boom")
	}

	*configPath = " first.conf , , second.conf "
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startFromConfigFiles(ctx, cancel)

	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startFromConfigFiles() did not cancel context after loader error")
	}

	got := map[string]struct{}{}
	for len(got) < 2 {
		select {
		case path := <-calls:
			got[path] = struct{}{}
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("startFromConfigFiles() invoked loaders for %#v, want both non-empty paths", got)
		}
	}
	if _, ok := got["first.conf"]; !ok {
		t.Fatalf("startFromConfigFiles() missing first.conf call, got %#v", got)
	}
	if _, ok := got["second.conf"]; !ok {
		t.Fatalf("startFromConfigFiles() missing second.conf call, got %#v", got)
	}
}
