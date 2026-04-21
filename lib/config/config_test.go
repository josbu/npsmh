package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestGetAllTitle_DuplicateRejected(t *testing.T) {
	_, err := getAllTitle("[common]\na=1\n[common]\nb=2\n")
	if err == nil {
		t.Fatalf("expected duplicate title to return an error")
	}
}

func TestGetAllTitle_CaseInsensitiveDuplicateRejected(t *testing.T) {
	_, err := getAllTitle("[Web]\na=1\n[web]\nb=2\n")
	if err == nil {
		t.Fatalf("expected case-insensitive duplicate title to return an error")
	}
}

func TestGetAllTitle_ParseAll(t *testing.T) {
	titles, err := getAllTitle("[common]\na=1\n[web]\nhost=a.com\n[tcp]\nmode=tcp\n")
	if err != nil {
		t.Fatalf("getAllTitle returned error: %v", err)
	}
	if len(titles) != 3 {
		t.Fatalf("expected 3 titles, got %d", len(titles))
	}
	if titles[0] != "[common]" || titles[1] != "[web]" || titles[2] != "[tcp]" {
		t.Fatalf("unexpected title order/content: %#v", titles)
	}
}

func TestReg(t *testing.T) {
	content := `
[common]
server=127.0.0.1:8284
tp=tcp
vkey=123
[web2]
host=www.baidu.com
host_change=www.sina.com
target=127.0.0.1:8080,127.0.0.1:8082
header_cookkile=122123
header_user-Agent=122123
[web2]
host=www.baidu.com
host_change=www.sina.com
target=127.0.0.1:8080,127.0.0.1:8082
header_cookkile="122123"
header_user-Agent=122123
[tunnel1]
type=udp
target=127.0.0.1:8080
port=9001
compress=snappy
crypt=true
u=1
p=2
[tunnel2]
type=tcp
target=127.0.0.1:8080
port=9001
compress=snappy
crypt=true
u=1
p=2
`
	re, err := regexp.Compile(`\[.+?]`)
	if err != nil {
		t.Fatalf("compile regexp failed: %v", err)
	}
	all := re.FindAllString(content, -1)
	if len(all) != 5 {
		t.Fatalf("unexpected title count: %d", len(all))
	}
}

func TestDealCommon(t *testing.T) {
	s := `server_addr=127.0.0.1:8284
conn_type=kcp
vkey=123
auto_reconnection=false
basic_username=admin
basic_password=pass
compress=false
crypt=false
web_username=user
web_password=web-pass
rate_limit=1024
flow_limit=2048
max_conn=12
disconnect_timeout=30
tls_enable=true`

	c := dealCommon(s)
	if c.Server != "127.0.0.1:8284" || c.Tp != "kcp" || c.VKey != "123" {
		t.Fatalf("basic common fields parse failed: %+v", c)
	}
	if c.AutoReconnection {
		t.Fatalf("auto_reconnection should be false")
	}
	if c.Client == nil || c.Client.Cnf == nil {
		t.Fatalf("client or client config not initialized")
	}
	if c.Client.Cnf.U != "admin" || c.Client.Cnf.P != "pass" {
		t.Fatalf("basic auth parse failed: %+v", c.Client.Cnf)
	}
	webUsername, webPassword, _ := c.Client.LegacyWebLoginImport()
	if webUsername != "user" || webPassword != "web-pass" {
		t.Fatalf("web auth parse failed: username=%q password=%q", webUsername, webPassword)
	}
	if c.Client.RateLimit != 1024 || c.Client.Flow.FlowLimit != 2048 || c.Client.MaxConn != 12 {
		t.Fatalf("limit fields parse failed: %+v", c.Client)
	}
	if c.DisconnectTime != 30 || !c.TlsEnable {
		t.Fatalf("disconnect or tls parse failed: %+v", c)
	}
}

func TestDealCommon_Defaults(t *testing.T) {
	c := dealCommon(`vkey=abc`)
	if c.Tp != "tcp" {
		t.Fatalf("unexpected default tp: %s", c.Tp)
	}
	if !c.AutoReconnection {
		t.Fatalf("unexpected default auto_reconnection: %v", c.AutoReconnection)
	}
	if c.Client == nil || c.Client.Cnf == nil {
		t.Fatalf("client defaults are not initialized")
	}
}

func TestGetTitleContent(t *testing.T) {
	s := "[common]"
	if getTitleContent(s) != "common" {
		t.Fail()
	}
}

func TestStripCommentLines(t *testing.T) {
	content := "a=1\n#comment\n  #comment2\nb=2\n"
	cleaned := stripCommentLines(content)
	if cleaned != "a=1\nb=2\n" {
		t.Fatalf("unexpected cleaned config: %q", cleaned)
	}
}

func TestDealHost_HeaderAndResponseMapping(t *testing.T) {
	h, err := dealHost(`host=example.com
target_addr=127.0.0.1:8080,127.0.0.1:8081
proxy_protocol=2
local_proxy=true
auto_cors=true
header_X-Test=test-value
response_Cache-Control=no-cache`)
	if err != nil {
		t.Fatalf("dealHost() error = %v", err)
	}

	if h.Host != "example.com" {
		t.Fatalf("unexpected host: %s", h.Host)
	}
	if h.Target.TargetStr != "127.0.0.1:8080\n127.0.0.1:8081" {
		t.Fatalf("unexpected target addresses: %q", h.Target.TargetStr)
	}
	if h.Target.ProxyProtocol != 2 {
		t.Fatalf("unexpected proxy protocol: %d", h.Target.ProxyProtocol)
	}
	if !h.Target.LocalProxy {
		t.Fatalf("local_proxy not parsed")
	}
	if !h.AutoCORS {
		t.Fatalf("auto_cors not parsed")
	}
	if h.HeaderChange != "X-Test:test-value\n" {
		t.Fatalf("unexpected header mapping: %q", h.HeaderChange)
	}
	if h.RespHeaderChange != "Cache-Control:no-cache\n" {
		t.Fatalf("unexpected response header mapping: %q", h.RespHeaderChange)
	}
}

func TestDealTunnel_ParseFlagsAndRules(t *testing.T) {
	tnl, err := dealTunnel(`server_port=10080
server_ip=0.0.0.0
mode=http
target_addr=127.0.0.1:80,127.0.0.1:8080
proxy_protocol=1
local_proxy=true
password=pwd
socks5_proxy=true
http_proxy=false
dest_acl_mode=2
dest_acl_rules=10.0.0.0/8,192.168.0.0/16
local_path=/tmp
strip_pre=/api
read_only=true`)
	if err != nil {
		t.Fatalf("dealTunnel() error = %v", err)
	}

	if tnl.Ports != "10080" || tnl.ServerIp != "0.0.0.0" || tnl.Mode != "http" {
		t.Fatalf("basic tunnel fields parse failed: %+v", tnl)
	}
	if tnl.Target.TargetStr != "127.0.0.1:80\n127.0.0.1:8080" {
		t.Fatalf("unexpected target addresses: %q", tnl.Target.TargetStr)
	}
	if !tnl.Target.LocalProxy {
		t.Fatalf("local_proxy not parsed")
	}
	if !tnl.Socks5Proxy || tnl.HttpProxy {
		t.Fatalf("proxy flags parse failed: socks5=%v http=%v", tnl.Socks5Proxy, tnl.HttpProxy)
	}
	if tnl.DestAclMode != 2 || tnl.DestAclRules != "10.0.0.0/8\n192.168.0.0/16" {
		t.Fatalf("dest acl parse failed: mode=%d rules=%q", tnl.DestAclMode, tnl.DestAclRules)
	}
	if tnl.LocalPath != "/tmp" || tnl.StripPre != "/api" || !tnl.ReadOnly {
		t.Fatalf("path/read-only parse failed: %+v", tnl)
	}
}

func TestDealMultiUser_IgnoreInvalidAndComments(t *testing.T) {
	accounts := dealMultiUser("\n#comment\nalice = a1\nbob=b2\ncharlie\n =drop\n")

	if len(accounts) != 3 {
		t.Fatalf("unexpected account count: %#v", accounts)
	}
	if accounts["alice"] != "a1" || accounts["bob"] != "b2" || accounts["charlie"] != "" {
		t.Fatalf("unexpected account map: %#v", accounts)
	}
	if _, ok := accounts[""]; ok {
		t.Fatalf("empty key should be ignored: %#v", accounts)
	}
}

func TestDelLocalService_ParseAllFields(t *testing.T) {
	local := delLocalService(`local_port=9000
local_type=tcp
local_ip=127.0.0.1
password=secret
target_addr=10.0.0.1:22
target_type=tcp
local_proxy=true
fallback_secret=true`)

	if local.Port != 9000 || local.Type != "tcp" || local.Ip != "127.0.0.1" {
		t.Fatalf("basic local fields parse failed: %+v", local)
	}
	if local.Password != "secret" || local.Target != "10.0.0.1:22" || local.TargetType != "tcp" {
		t.Fatalf("target/auth parse failed: %+v", local)
	}
	if !local.LocalProxy || !local.Fallback {
		t.Fatalf("boolean flags parse failed: %+v", local)
	}
}

func TestConfigParser_AcceptsDashAndDotSeparators(t *testing.T) {
	c := dealCommon(`server-addr=127.0.0.1:8284
conn.type=kcp
auto-reconnection=false
disconnect.timeout=30`)
	if c.Server != "127.0.0.1:8284" || c.Tp != "kcp" || c.AutoReconnection || c.DisconnectTime != 30 {
		t.Fatalf("dealCommon separator normalization failed: %+v", c)
	}

	h, err := dealHost(`host=example.com
target-addr=127.0.0.1:8080
auto.cors=true
header.X-Test=test-value
response.Cache-Control=no-cache`)
	if err != nil {
		t.Fatalf("dealHost() error = %v", err)
	}
	if h.Target.TargetStr != "127.0.0.1:8080" || !h.AutoCORS || h.HeaderChange != "X-Test:test-value\n" || h.RespHeaderChange != "Cache-Control:no-cache\n" {
		t.Fatalf("dealHost separator normalization failed: %+v", h)
	}

	tnl, err := dealTunnel(`server-port=10080
dest.acl.mode=2
dest-acl-rules=10.0.0.0/8`)
	if err != nil {
		t.Fatalf("dealTunnel() error = %v", err)
	}
	if tnl.Ports != "10080" || tnl.DestAclMode != 2 || tnl.DestAclRules != "10.0.0.0/8" {
		t.Fatalf("dealTunnel separator normalization failed: %+v", tnl)
	}

	local := delLocalService(`local-port=9000
fallback.secret=true`)
	if local.Port != 9000 || !local.Fallback {
		t.Fatalf("delLocalService separator normalization failed: %+v", local)
	}
}

func TestNewConfigFromContent_UsesINIParserForComments(t *testing.T) {
	raw := `
; top comment
[common]
server_addr=127.0.0.1:8024 ; inline comment
vkey=comment-key # inline comment
conn_type=ws

[demo]
mode=tcp
server_port=10080
target_addr=127.0.0.1:80 ; tunnel comment
`

	cfg, err := NewConfigFromContent(raw)
	if err != nil {
		t.Fatalf("NewConfigFromContent() error = %v", err)
	}
	if cfg.CommonConfig == nil {
		t.Fatal("CommonConfig = nil")
	}
	if cfg.CommonConfig.Server != "127.0.0.1:8024" || cfg.CommonConfig.VKey != "comment-key" || cfg.CommonConfig.Tp != "ws" {
		t.Fatalf("unexpected common config: %+v", cfg.CommonConfig)
	}
	if len(cfg.Tasks) != 1 {
		t.Fatalf("len(cfg.Tasks) = %d, want 1", len(cfg.Tasks))
	}
	if cfg.Tasks[0].Ports != "10080" || cfg.Tasks[0].Target.TargetStr != "127.0.0.1:80" {
		t.Fatalf("unexpected task config: %+v", cfg.Tasks[0])
	}
}

func TestNewConfigFromContent_AcceptsMixedCaseSectionNames(t *testing.T) {
	raw := `
[Common]
server_addr=127.0.0.1:8024
vkey=mixed-case

[HealthMain]
health_check_timeout=5

[Demo]
mode=tcp
server_port=10080
target_addr=127.0.0.1:80
`

	cfg, err := NewConfigFromContent(raw)
	if err != nil {
		t.Fatalf("NewConfigFromContent() error = %v", err)
	}
	if cfg.CommonConfig == nil || cfg.CommonConfig.Server != "127.0.0.1:8024" || cfg.CommonConfig.VKey != "mixed-case" {
		t.Fatalf("unexpected common config: %+v", cfg.CommonConfig)
	}
	if len(cfg.Healths) != 1 || cfg.Healths[0].HealthCheckTimeout != 5 {
		t.Fatalf("unexpected health config: %+v", cfg.Healths)
	}
	if len(cfg.Tasks) != 1 || cfg.Tasks[0].Ports != "10080" {
		t.Fatalf("unexpected task config: %+v", cfg.Tasks)
	}
}

func TestConfigParsersPreserveEqualsInValues(t *testing.T) {
	commonCfg := dealCommon("proxy_url=https://user:pa=ss@example.com")
	if commonCfg.ProxyUrl != "https://user:pa=ss@example.com" {
		t.Fatalf("dealCommon proxy_url = %q", commonCfg.ProxyUrl)
	}

	host, err := dealHost(`host=example.com
redirect_url=https://example.com/login?token=a=b
header_X-Test=alpha=beta`)
	if err != nil {
		t.Fatalf("dealHost() error = %v", err)
	}
	if host.RedirectURL != "https://example.com/login?token=a=b" {
		t.Fatalf("dealHost redirect_url = %q", host.RedirectURL)
	}
	if host.HeaderChange != "X-Test:alpha=beta\n" {
		t.Fatalf("dealHost header change = %q", host.HeaderChange)
	}

	health := dealHealth("health_http_url=https://example.com/check?sig=a=b")
	if health.HttpHealthUrl != "https://example.com/check?sig=a=b" {
		t.Fatalf("dealHealth health_http_url = %q", health.HttpHealthUrl)
	}

	tunnel, err := dealTunnel(`server_port=10080
password=abc=123`)
	if err != nil {
		t.Fatalf("dealTunnel() error = %v", err)
	}
	if tunnel.Password != "abc=123" {
		t.Fatalf("dealTunnel password = %q", tunnel.Password)
	}

	local := delLocalService("password=secret=token")
	if local.Password != "secret=token" {
		t.Fatalf("delLocalService password = %q", local.Password)
	}
}

func TestNewConfigFromContent_ReturnsErrorForMissingMultiAccountFile(t *testing.T) {
	raw := `
[common]
server_addr=127.0.0.1:8024
vkey=comment-key

[demo]
mode=file
server_port=10080
target_addr=127.0.0.1:80
multi_account=` + filepath.Join(t.TempDir(), "missing.conf") + `
`

	if _, err := NewConfigFromContent(raw); err == nil {
		t.Fatal("NewConfigFromContent() error = nil, want missing multi_account file error")
	}
}

func TestNewConfigFromContent_ReturnsErrorForInvalidMultiAccountTemplate(t *testing.T) {
	dir := t.TempDir()
	multiAccountPath := filepath.Join(dir, "multi_account.conf")
	if err := os.WriteFile(multiAccountPath, []byte("alice={{\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(multi_account.conf) error = %v", err)
	}

	raw := `
[common]
server_addr=127.0.0.1:8024
vkey=comment-key

[demo]
mode=mixProxy
server_port=10080
target_addr=127.0.0.1:80
multi_account=` + multiAccountPath + `
`

	if _, err := NewConfigFromContent(raw); err == nil {
		t.Fatal("NewConfigFromContent() error = nil, want invalid multi_account template error")
	}
}
