package client

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustResolveLaunchSpec(t *testing.T, raw string) *LaunchSpec {
	t.Helper()
	spec, err := ResolveLaunchSpec(context.Background(), raw)
	if err != nil {
		t.Fatalf("ResolveLaunchSpec() error = %v", err)
	}
	return spec
}

func mustResolveLaunchInputs(t *testing.T, inputs []string) *LaunchSpec {
	t.Helper()
	spec, err := ResolveLaunchInputs(context.Background(), inputs)
	if err != nil {
		t.Fatalf("ResolveLaunchInputs() error = %v", err)
	}
	return spec
}

func newLaunchSpecServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

func writeLaunchSpecFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestLaunchConfigBuildWithContextHandlesNilContextForSource(t *testing.T) {
	source := writeLaunchSpecFile(t, "launch-source.json", `{
		"common": {
			"server_addr": "127.0.0.1:8024",
			"vkey": "demo"
		}
	}`)
	cfg, err := (&LaunchConfig{Source: source}).BuildWithContext(nil)
	if err != nil {
		t.Fatalf("BuildWithContext(nil) error = %v", err)
	}
	if cfg == nil || cfg.CommonConfig == nil {
		t.Fatal("BuildWithContext(nil) returned empty config")
	}
	if cfg.CommonConfig.Server != "127.0.0.1:8024" || cfg.CommonConfig.VKey != "demo" {
		t.Fatalf("unexpected common config: %+v", cfg.CommonConfig)
	}
}

func TestResolveLaunchSpecHandlesNilContextForHTTPSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"direct": {
				"server": "127.0.0.1:8024",
				"vkey": "demo"
			}
		}`))
	}))
	defer server.Close()

	spec, err := ResolveLaunchSpec(nil, server.URL)
	if err != nil {
		t.Fatalf("ResolveLaunchSpec(nil, remote) error = %v", err)
	}
	if spec == nil || spec.Direct == nil {
		t.Fatalf("ResolveLaunchSpec(nil, remote) returned invalid spec: %+v", spec)
	}
	if spec.Direct.Server.JoinComma() != "127.0.0.1:8024" || spec.Direct.VKey.JoinComma() != "demo" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_ConfigJSONShorthand(t *testing.T) {
	spec := mustResolveLaunchSpec(t, `{
		"common": {
			"server_addr": "127.0.0.1:8024",
			"vkey": "abc",
			"conn_type": "tls",
			"auto_reconnection": false,
			"dns_server": "1.1.1.1"
		},
		"tasks": [{
			"remark": "demo",
			"mode": "tcp",
			"server_port": "10080",
			"target_addr": ["127.0.0.1:80", "127.0.0.1:81"]
		}],
		"local_servers": [{
			"local_port": 2001,
			"local_type": "p2p",
			"password": "secret",
			"target_type": "tcp"
		}]
	}`)
	if got := spec.Mode(); got != "config" {
		t.Fatalf("spec.Mode() = %q, want config", got)
	}
	cfg, err := spec.BuildConfig()
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}
	if cfg.CommonConfig.Server != "127.0.0.1:8024" || cfg.CommonConfig.VKey != "abc" || cfg.CommonConfig.Tp != "tls" {
		t.Fatalf("unexpected common config: %+v", cfg.CommonConfig)
	}
	if cfg.CommonConfig.AutoReconnection {
		t.Fatalf("AutoReconnection = true, want false")
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].Target.TargetStr != "127.0.0.1:80\n127.0.0.1:81" {
		t.Fatalf("unexpected tasks: %+v", cfg.Tasks)
	}
	if len(cfg.LocalServer) != 1 || cfg.LocalServer[0].Port != 2001 || cfg.LocalServer[0].Password != "secret" {
		t.Fatalf("unexpected local servers: %+v", cfg.LocalServer)
	}
}

func TestResolveLaunchSpec_NPCURLDirect(t *testing.T) {
	spec := mustResolveLaunchSpec(t, "npc://demo-vkey@127.0.0.1:8024?type=tls&log=off&local_ip=192.168.1.10")
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if !spec.Direct.Server.HasValue() || spec.Direct.Server.JoinComma() != "127.0.0.1:8024" {
		t.Fatalf("unexpected direct server: %+v", spec.Direct.Server)
	}
	if !spec.Direct.VKey.HasValue() || spec.Direct.VKey.JoinComma() != "demo-vkey" {
		t.Fatalf("unexpected direct vkey: %+v", spec.Direct.VKey)
	}
	if spec.Runtime == nil || spec.Runtime.Log == nil || *spec.Runtime.Log != "off" {
		t.Fatalf("unexpected runtime: %+v", spec.Runtime)
	}
}

func TestResolveLaunchSpec_NPCURLStructuredConnect(t *testing.T) {
	spec := mustResolveLaunchSpec(t, "npc://connect?server=127.0.0.1:8024/ws&vkey=demo-vkey&type=ws&log=off")
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "127.0.0.1:8024/ws" || spec.Direct.VKey.JoinComma() != "demo-vkey" || spec.Direct.Type.JoinComma() != "ws" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_NPCURLStructuredLocal(t *testing.T) {
	spec := mustResolveLaunchSpec(t, "npc://local?server=127.0.0.1:8024&vkey=demo-vkey&type=tcp&password=secret&local_type=p2p&target=node-b")
	if got := spec.Mode(); got != "local" {
		t.Fatalf("spec.Mode() = %q, want local", got)
	}
	if spec.Local.Server != "127.0.0.1:8024" || spec.Local.VKey != "demo-vkey" || spec.Local.Password != "secret" || spec.Local.Target != "node-b" {
		t.Fatalf("unexpected local spec: %+v", spec.Local)
	}
}

func TestResolveLaunchSpec_Base64Payload(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"direct":{"server":["10.0.0.1:8024"],"vkey":["k1"],"type":["tcp"]}}`))
	spec := mustResolveLaunchSpec(t, encoded)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "10.0.0.1:8024" || spec.Direct.VKey.JoinComma() != "k1" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_FilePayload(t *testing.T) {
	path := writeLaunchSpecFile(t, "launch.txt", `{"direct":{"server":"10.0.0.9:8024","vkey":"kf","type":"tcp"}}`)
	spec := mustResolveLaunchSpec(t, "@"+path)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "10.0.0.9:8024" || spec.Direct.VKey.JoinComma() != "kf" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_NPCHostWrappedBase64Payload(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"direct":{"server":"10.0.0.3:8024","vkey":"k3","type":"tcp"}}`))
	spec := mustResolveLaunchSpec(t, "npc://"+encoded)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "10.0.0.3:8024" || spec.Direct.VKey.JoinComma() != "k3" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_NPCHostWrappedBase64PayloadWithRuntime(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"direct":{"server":"10.0.0.4:8024","vkey":"k4","type":"tcp"}}`))
	spec := mustResolveLaunchSpec(t, "npc://"+encoded+"?log=off")
	if spec.Runtime == nil || spec.Runtime.Log == nil || *spec.Runtime.Log != "off" {
		t.Fatalf("unexpected runtime overlay: %+v", spec.Runtime)
	}
}

func TestResolveLaunchSpec_RemoteURL(t *testing.T) {
	server := newLaunchSpecServer(t, `{"direct":{"server":"192.168.0.2:8024","vkey":"vx","type":"ws"}}`)
	defer server.Close()

	spec := mustResolveLaunchSpec(t, server.URL)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Type.JoinComma() != "ws" {
		t.Fatalf("unexpected direct type: %+v", spec.Direct.Type)
	}
}

func TestResolveLaunchSpec_RemoteURLRedirect(t *testing.T) {
	target := newLaunchSpecServer(t, `{"direct":{"server":"192.168.0.22:8024","vkey":"redir","type":"tls"}}`)
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	spec := mustResolveLaunchSpec(t, redirect.URL)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "192.168.0.22:8024" || spec.Direct.VKey.JoinComma() != "redir" || spec.Direct.Type.JoinComma() != "tls" {
		t.Fatalf("unexpected redirected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_RemoteURLReturningBase64(t *testing.T) {
	server := newLaunchSpecServer(t, base64.RawURLEncoding.EncodeToString([]byte(`{"direct":{"server":"192.168.0.3:8024","vkey":"vb","type":"tls"}}`)))
	defer server.Close()

	spec := mustResolveLaunchSpec(t, server.URL)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "192.168.0.3:8024" || spec.Direct.Type.JoinComma() != "tls" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_RemoteURLReturningURL(t *testing.T) {
	second := newLaunchSpecServer(t, `{"direct":{"server":"192.168.0.4:8024","vkey":"vu","type":"tcp"}}`)
	defer second.Close()
	first := newLaunchSpecServer(t, second.URL)
	defer first.Close()

	spec := mustResolveLaunchSpec(t, first.URL)
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "192.168.0.4:8024" || spec.Direct.VKey.JoinComma() != "vu" {
		t.Fatalf("unexpected direct spec: %+v", spec.Direct)
	}
}

func TestResolveLaunchSpec_NPCURLPreservesServerPath(t *testing.T) {
	spec := mustResolveLaunchSpec(t, "npc://demo-vkey@127.0.0.1:8024/ws?type=ws")
	if got := spec.Mode(); got != "direct" {
		t.Fatalf("spec.Mode() = %q, want direct", got)
	}
	if spec.Direct.Server.JoinComma() != "127.0.0.1:8024/ws" {
		t.Fatalf("unexpected direct server path: %+v", spec.Direct.Server)
	}
}

func TestResolveLaunchSpec_NPCWrappedPlainURLRejected(t *testing.T) {
	if _, err := ResolveLaunchSpec(context.Background(), "npc://https://example.com/npc-launch"); err == nil {
		t.Fatalf("ResolveLaunchSpec() error = nil, want rejection for plain nested payload")
	}
}

func TestResolveLaunchSpec_LegacyNPCBase64RouteRejected(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"direct":{"server":"10.1.1.1:8024/ws","vkey":"k2","type":"wss"}}`))
	if _, err := ResolveLaunchSpec(context.Background(), "npc://base64/"+encoded); err == nil {
		t.Fatal("ResolveLaunchSpec() error = nil, want rejection for non-canonical npc wrapper route")
	}
}

func TestResolveLaunchSpec_LegacyNPCLaunchDataRejected(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"direct":{"server":"10.1.1.1:8024","vkey":"k2","type":"tcp"}}`))
	if _, err := ResolveLaunchSpec(context.Background(), "npc://launch?data="+encoded); err == nil {
		t.Fatal("ResolveLaunchSpec() error = nil, want rejection for non-canonical npc launch route")
	}
}

func TestResolveLaunchInputs_MultiplePayloads(t *testing.T) {
	spec := mustResolveLaunchInputs(t, []string{
		"npc://edge-a@10.0.0.1:8024?type=tcp",
		"npc://edge-b@10.0.0.2:8024/ws?type=ws&log=off",
	})
	if got := spec.Mode(); got != "profiles" {
		t.Fatalf("spec.Mode() = %q, want profiles", got)
	}
	if spec.Version != 2 {
		t.Fatalf("spec.Version = %d, want 2", spec.Version)
	}
	profiles := spec.ExpandProfiles()
	if len(profiles) != 2 {
		t.Fatalf("ExpandProfiles() len = %d, want 2", len(profiles))
	}
	if profiles[0].Direct.Server.JoinComma() != "10.0.0.1:8024" || profiles[1].Direct.Server.JoinComma() != "10.0.0.2:8024/ws" {
		t.Fatalf("unexpected direct profiles: %+v", profiles)
	}
	if spec.Runtime == nil || spec.Runtime.Log == nil || *spec.Runtime.Log != "off" {
		t.Fatalf("unexpected merged runtime: %+v", spec.Runtime)
	}
}

func TestResolveLaunchInputs_MultiplePayloadsRuntimeConflict(t *testing.T) {
	inputs := []string{
		"npc://edge-a@10.0.0.1:8024?type=tcp&log=off",
		"npc://edge-b@10.0.0.2:8024?type=tls&log=stdout",
	}
	if _, err := ResolveLaunchInputs(context.Background(), inputs); err == nil || !strings.Contains(err.Error(), "runtime conflict") {
		t.Fatalf("ResolveLaunchInputs() error = %v, want runtime conflict", err)
	}
}

func TestResolveLaunchSpec_ProfileBundle(t *testing.T) {
	spec := mustResolveLaunchSpec(t, `{
		"version": 2,
		"runtime": {
			"log": "off"
		},
		"profiles": [
			{
				"name": "edge-a",
				"direct": {
					"server": ["10.0.0.1:8024/quic-a"],
					"vkey": ["k1"],
					"type": ["quic"]
				}
			},
			{
				"name": "edge-b",
				"config": {
					"common": {
						"server": "10.0.0.2:8024/ws",
						"vkey": "k2",
						"type": "ws"
					},
					"tunnels": [{
						"remark": "demo",
						"mode": "tcp",
						"server_port": "10080",
						"target_addr": ["127.0.0.1:80"]
					}]
				}
			}
		]
	}`)
	if got := spec.Mode(); got != "profiles" {
		t.Fatalf("spec.Mode() = %q, want profiles", got)
	}
	profiles := spec.ExpandProfiles()
	if len(profiles) != 2 {
		t.Fatalf("ExpandProfiles() len = %d, want 2", len(profiles))
	}
	cfg, err := profiles[1].BuildConfigWithRuntime(spec.Runtime)
	if err != nil {
		t.Fatalf("BuildConfigWithRuntime() error = %v", err)
	}
	if cfg.CommonConfig.Server != "10.0.0.2:8024/ws" || cfg.CommonConfig.Tp != "ws" {
		t.Fatalf("unexpected config profile common: %+v", cfg.CommonConfig)
	}
}

func TestResolveLaunchSpec_ProfileArrayJSON(t *testing.T) {
	spec := mustResolveLaunchSpec(t, `[
		{
			"name": "edge-a",
			"direct": {
				"server": "10.0.0.1:8024",
				"vkey": "k1",
				"type": "tcp"
			}
		}
	]`)
	if got := spec.Mode(); got != "profiles" {
		t.Fatalf("spec.Mode() = %q, want profiles", got)
	}
	if len(spec.ExpandProfiles()) != 1 {
		t.Fatalf("ExpandProfiles() len = %d, want 1", len(spec.ExpandProfiles()))
	}
}

func TestResolveLaunchSpec_ConfigAliasesAndExtendedFields(t *testing.T) {
	spec := mustResolveLaunchSpec(t, `{
		"config": {
			"common": {
				"server": "127.0.0.1:8024/ws",
				"vkey": "abc",
				"type": "ws",
				"proxy": "http://127.0.0.1:8080",
				"entry_acl_mode": 1,
				"entry_acl_rules": ["127.0.0.1/32", "10.0.0.0/8"]
			},
			"hosts": [{
				"remark": "web",
				"host": "example.com",
				"target_addr": ["127.0.0.1:8080"],
				"headers": {"X-Test": "a"},
				"response_headers": {"Cache-Control": "no-cache"},
				"local_proxy": true,
				"entry_acl_mode": 2,
				"entry_acl_rules": ["192.168.0.0/16"]
			}],
			"tunnels": [{
				"remark": "tcp-demo",
				"mode": "tcp",
				"server_port": "10080",
				"target_addr": ["127.0.0.1:80"],
				"local_proxy": true,
				"target_type": "tcp",
				"entry_acl_mode": 1,
				"entry_acl_rules": ["172.16.0.0/12"],
				"dest_acl_mode": 2,
				"dest_acl_rules": ["10.0.0.0/8"],
				"user_auth": {"ops":"secret"}
			}],
			"locals": [{
				"local_port": 2001,
				"password": "secret",
				"target_addr": "10.0.0.1:22"
			}]
		}
	}`)
	cfg, err := spec.BuildConfig()
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}
	if cfg.CommonConfig.Server != "127.0.0.1:8024/ws" || cfg.CommonConfig.Tp != "ws" || cfg.CommonConfig.ProxyUrl != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected common config: %+v", cfg.CommonConfig)
	}
	if cfg.CommonConfig.Client.EntryAclMode != 1 || cfg.CommonConfig.Client.EntryAclRules != "127.0.0.1/32\n10.0.0.0/8" {
		t.Fatalf("unexpected client acl config: %+v", cfg.CommonConfig.Client)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].HeaderChange != "X-Test:a\n" || cfg.Hosts[0].RespHeaderChange != "Cache-Control:no-cache\n" || !cfg.Hosts[0].Target.LocalProxy {
		t.Fatalf("unexpected hosts: %+v", cfg.Hosts)
	}
	if len(cfg.Tasks) != 1 || !cfg.Tasks[0].Target.LocalProxy || cfg.Tasks[0].TargetType != "tcp" || cfg.Tasks[0].EntryAclRules != "172.16.0.0/12" {
		t.Fatalf("unexpected tasks: %+v", cfg.Tasks)
	}
	if cfg.Tasks[0].UserAuth == nil || cfg.Tasks[0].UserAuth.AccountMap["ops"] != "secret" {
		t.Fatalf("unexpected task user auth: %+v", cfg.Tasks[0].UserAuth)
	}
	if len(cfg.LocalServer) != 1 || cfg.LocalServer[0].Target != "10.0.0.1:22" {
		t.Fatalf("unexpected local servers: %+v", cfg.LocalServer)
	}
}

func TestResolveLaunchSpec_ConfigSourceLocalWithInlineOverlay(t *testing.T) {
	path := writeLaunchSpecFile(t, "npc.conf", `
[common]
server_addr=127.0.0.1:8024
vkey=src-key
conn_type=tcp
proxy_url=http://127.0.0.1:9000

[demo]
mode=tcp
server_port=10080
target_addr=127.0.0.1:80
`)
	spec := mustResolveLaunchSpec(t, `{
		"config": {
			"source": "`+strings.ReplaceAll(path, `\`, `\\`)+`",
			"common": {
				"server_addr": "10.0.0.2:8024/ws",
				"vkey": "inline-key",
				"type": "ws"
			},
			"tasks": [{
				"remark": "extra",
				"mode": "tcp",
				"server_port": "10081",
				"target_addr": ["127.0.0.1:81"]
			}]
		}
	}`)
	cfg, err := spec.BuildConfig()
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}
	if cfg.CommonConfig.Server != "10.0.0.2:8024/ws" || cfg.CommonConfig.VKey != "inline-key" || cfg.CommonConfig.Tp != "ws" {
		t.Fatalf("unexpected merged common config: %+v", cfg.CommonConfig)
	}
	if cfg.CommonConfig.ProxyUrl != "http://127.0.0.1:9000" {
		t.Fatalf("unexpected preserved proxy config: %+v", cfg.CommonConfig)
	}
	if len(cfg.Tasks) != 2 {
		t.Fatalf("len(cfg.Tasks) = %d, want 2", len(cfg.Tasks))
	}
}

func TestResolveLaunchSpec_ConfigSourceRemote(t *testing.T) {
	server := newLaunchSpecServer(t, `
[common]
server_addr=10.0.0.9:8024
vkey=remote-key
conn_type=tls

[demo]
mode=tcp
server_port=10080
target_addr=127.0.0.1:80
`)
	defer server.Close()

	spec := mustResolveLaunchSpec(t, `{"config":{"source":"`+server.URL+`"}}`)
	cfg, err := spec.BuildConfigWithContext(context.Background())
	if err != nil {
		t.Fatalf("BuildConfigWithContext() error = %v", err)
	}
	if cfg.CommonConfig.Server != "10.0.0.9:8024" || cfg.CommonConfig.VKey != "remote-key" || cfg.CommonConfig.Tp != "tls" {
		t.Fatalf("unexpected remote config common: %+v", cfg.CommonConfig)
	}
	if len(cfg.Tasks) != 1 {
		t.Fatalf("len(cfg.Tasks) = %d, want 1", len(cfg.Tasks))
	}
}

func TestResolveLaunchSpec_ConfigSourceRemoteRedirect(t *testing.T) {
	target := newLaunchSpecServer(t, `
[common]
server_addr=10.0.0.19:8024
vkey=redirect-key
conn_type=ws

[demo]
mode=tcp
server_port=10080
target_addr=127.0.0.1:80
`)
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()

	spec := mustResolveLaunchSpec(t, `{"config":{"source":"`+redirect.URL+`"}}`)
	cfg, err := spec.BuildConfigWithContext(context.Background())
	if err != nil {
		t.Fatalf("BuildConfigWithContext() error = %v", err)
	}
	if cfg.CommonConfig.Server != "10.0.0.19:8024" || cfg.CommonConfig.VKey != "redirect-key" || cfg.CommonConfig.Tp != "ws" {
		t.Fatalf("unexpected redirected config common: %+v", cfg.CommonConfig)
	}
	if len(cfg.Tasks) != 1 {
		t.Fatalf("len(cfg.Tasks) = %d, want 1", len(cfg.Tasks))
	}
}

func TestResolveLaunchSpec_ConfigSourceRemoteYAMLWithSeparatorAliases(t *testing.T) {
	server := newLaunchSpecServer(t, `
common:
  server-addr: 10.0.0.11:8024
  conn.type: ws
  vkey: yaml-key
tasks:
  - remark: demo
    server-port: "10080"
    mode: tcp
    target-addr:
      - 127.0.0.1:80
`)
	defer server.Close()

	spec := mustResolveLaunchSpec(t, `{"config":{"source":"`+server.URL+`"}}`)
	cfg, err := spec.BuildConfigWithContext(context.Background())
	if err != nil {
		t.Fatalf("BuildConfigWithContext() error = %v", err)
	}
	if cfg.CommonConfig.Server != "10.0.0.11:8024" || cfg.CommonConfig.VKey != "yaml-key" || cfg.CommonConfig.Tp != "ws" {
		t.Fatalf("unexpected yaml config common: %+v", cfg.CommonConfig)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].Ports != "10080" || cfg.Tasks[0].Target.TargetStr != "127.0.0.1:80" {
		t.Fatalf("unexpected yaml tasks: %+v", cfg.Tasks)
	}
}

func TestResolveLaunchSpec_ConfigInlineJSONSeparatorAliases(t *testing.T) {
	spec := mustResolveLaunchSpec(t, `{
		"config": {
			"common": {
				"server-addr": "127.0.0.1:8024/ws",
				"conn.type": "ws",
				"vkey": "json-key"
			},
			"local-servers": [{
				"local-port": 2001,
				"target-type": "tcp",
				"password": "secret"
			}]
		}
	}`)
	cfg, err := spec.BuildConfig()
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}
	if cfg.CommonConfig.Server != "127.0.0.1:8024/ws" || cfg.CommonConfig.Tp != "ws" || cfg.CommonConfig.VKey != "json-key" {
		t.Fatalf("unexpected inline json common: %+v", cfg.CommonConfig)
	}
	if len(cfg.LocalServer) != 1 || cfg.LocalServer[0].Port != 2001 || cfg.LocalServer[0].TargetType != "tcp" || cfg.LocalServer[0].Password != "secret" {
		t.Fatalf("unexpected inline json local servers: %+v", cfg.LocalServer)
	}
}
