package policy

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestSourceIPPolicySupportsExactCIDRWildcardAndGeoIP(t *testing.T) {
	geoPath := writeGeoIPDat(t, "CN", "1.2.3.0/24")
	p := CompileSourceIPPolicy(ModeBlacklist, []string{
		"8.8.8.8",
		"10.*",
		"192.168.*",
		"geoip:cn",
	}, Options{GeoIPPath: geoPath})

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "8.8.8.8:80", want: false},
		{addr: "10.2.3.4:80", want: false},
		{addr: "192.168.1.9:80", want: false},
		{addr: "1.2.3.9:80", want: false},
		{addr: "9.9.9.9:53", want: true},
		{addr: "[2001:db8::1]:443", want: true},
	}

	for _, tt := range tests {
		if got := p.AllowsAddr(tt.addr); got != tt.want {
			t.Fatalf("AllowsAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestSourceIPPolicyAllowsWhenGeoIPFileIsMissing(t *testing.T) {
	p := CompileSourceIPPolicy(ModeBlacklist, []string{"geoip:cn"}, Options{GeoIPPath: filepath.Join(t.TempDir(), "missing.dat")})
	if !p.AllowsAddr("8.8.8.8:80") {
		t.Fatal("missing geoip data should not block unrelated traffic in blacklist mode")
	}
}

func TestSourceIPPolicySupportsReverseGeoIPRule(t *testing.T) {
	geoPath := writeGeoIPDat(t, "CN", "1.2.3.0/24")
	p := CompileSourceIPPolicy(ModeWhitelist, []string{"geoip:!cn"}, Options{GeoIPPath: geoPath})
	if !p.AllowsAddr("8.8.8.8:53") {
		t.Fatal("geoip:!cn should match addresses outside the geoip set")
	}
	if p.AllowsAddr("1.2.3.4:53") {
		t.Fatal("geoip:!cn should not match addresses inside the geoip set")
	}
}

func TestDestinationPolicySupportsIPAndDomainRules(t *testing.T) {
	geoPath := writeGeoIPDat(t, "CN", "1.2.3.0/24")
	p := CompileDestinationPolicy(ModeWhitelist, "geoip:cn\n*.example.com\n*root.test\n10.0.0.0/8\n", Options{GeoIPPath: geoPath})

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "1.2.3.9:443", want: true},
		{addr: "10.1.2.3:8080", want: true},
		{addr: "api.example.com:443", want: true},
		{addr: "example.com:443", want: false},
		{addr: "root.test:443", want: true},
		{addr: "a.root.test:443", want: true},
		{addr: "8.8.8.8:53", want: false},
		{addr: "deny.example.org:443", want: false},
	}

	for _, tt := range tests {
		if got := p.AllowsAddr(tt.addr); got != tt.want {
			t.Fatalf("AllowsAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestDestinationPolicySupportsStructuredDomainRules(t *testing.T) {
	p := CompileDestinationPolicy(ModeWhitelist, "full:exact.example\ndomain:service.local\nkeyword:tracker\nregexp:^API[0-9]+\\.EDGE\\.TEST$\ndotless:intra\n", Options{})

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "exact.example:443", want: true},
		{addr: "www.exact.example:443", want: false},
		{addr: "service.local:443", want: true},
		{addr: "a.service.local:443", want: true},
		{addr: "badservice.local:443", want: false},
		{addr: "ads-tracker-cdn.net:443", want: true},
		{addr: "api12.edge.test:443", want: true},
		{addr: "intranet:443", want: true},
		{addr: "public.example:443", want: false},
	}

	for _, tt := range tests {
		if got := p.AllowsAddr(tt.addr); got != tt.want {
			t.Fatalf("AllowsAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestDestinationPolicyRegexpRulePreservesEscapes(t *testing.T) {
	p := CompileDestinationPolicy(ModeWhitelist, "regexp:\\QAPI.\\Eexample\\.com$", Options{})

	if !p.AllowsAddr("api.example.com:443") {
		t.Fatal("escaped regexp rule should match normalized host without being corrupted during compile")
	}
	if p.AllowsAddr("other.example.com:443") {
		t.Fatal("escaped regexp rule should not match unrelated hosts")
	}
}

func TestDestinationPolicySupportsPureStringCommentsAndIPv6Rules(t *testing.T) {
	p := CompileDestinationPolicy(ModeWhitelist, "# comment\nSINA.COM\nregexp:^api[0-9]+\\.edge\\.test$\n2001:db8::/32\n", Options{})

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "sina.com:443", want: true},
		{addr: "www.sina.com:443", want: true},
		{addr: "sina.com.cn:443", want: true},
		{addr: "sina.cn:443", want: false},
		{addr: "api12.edge.test:443", want: true},
		{addr: "[2001:db8::1]:443", want: true},
		{addr: "[2001:db9::1]:443", want: false},
	}

	for _, tt := range tests {
		if got := p.AllowsAddr(tt.addr); got != tt.want {
			t.Fatalf("AllowsAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestDestinationPolicySupportsGeoSiteRules(t *testing.T) {
	sitePath := writeGeoSiteDat(t, "CN",
		geoSiteTestDomain{typ: geoSiteTypeFull, value: "full.cn"},
		geoSiteTestDomain{typ: geoSiteTypeDomain, value: "domain.cn"},
		geoSiteTestDomain{typ: geoSiteTypePlain, value: "keyword-cn"},
		geoSiteTestDomain{typ: geoSiteTypeRegex, value: "^api[0-9]+\\.cn$"},
	)
	p := CompileDestinationPolicy(ModeWhitelist, "geosite:cn", Options{GeoSitePath: sitePath})

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "full.cn:443", want: true},
		{addr: "a.domain.cn:443", want: true},
		{addr: "cdn-keyword-cn.example:443", want: true},
		{addr: "api3.cn:443", want: true},
		{addr: "other.example:443", want: false},
	}

	for _, tt := range tests {
		if got := p.AllowsAddr(tt.addr); got != tt.want {
			t.Fatalf("AllowsAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestDestinationPolicySupportsGeoSiteAttrsAndExternalFiles(t *testing.T) {
	sitePath := writeGeoSiteDat(t, "ADS",
		geoSiteTestDomain{typ: geoSiteTypeDomain, value: "ads.example", attrs: []string{"ads", "tracker"}},
		geoSiteTestDomain{typ: geoSiteTypeDomain, value: "other.example", attrs: []string{"other"}},
	)
	fileName := filepath.Base(sitePath)
	p := CompileDestinationPolicy(ModeWhitelist, "ext:"+fileName+":ads@ads", Options{GeoSitePath: sitePath})
	if !p.AllowsAddr("a.ads.example:443") {
		t.Fatal("external geosite rule with attrs should allow matched domain")
	}
	if p.AllowsAddr("a.other.example:443") {
		t.Fatal("external geosite attr filter should reject unmatched attrs")
	}
}

func TestDestinationPolicySupportsDotlessAnyRule(t *testing.T) {
	p := CompileDestinationPolicy(ModeWhitelist, "dotless:", Options{})
	if !p.AllowsAddr("intranet:80") {
		t.Fatal("dotless: should allow hostnames without dots")
	}
	if p.AllowsAddr("api.example.com:80") {
		t.Fatal("dotless: should reject dotted domains")
	}
}

func TestSourceIPPolicySupportsIPv6ExactAndCIDR(t *testing.T) {
	p := CompileSourceIPPolicy(ModeBlacklist, []string{
		"2001:db8::1",
		"2001:db8:abcd::/48",
	}, Options{})

	tests := []struct {
		addr string
		want bool
	}{
		{addr: "[2001:db8::1]:443", want: false},
		{addr: "[2001:db8:abcd::2]:443", want: false},
		{addr: "[2001:db8:abce::2]:443", want: true},
	}

	for _, tt := range tests {
		if got := p.AllowsAddr(tt.addr); got != tt.want {
			t.Fatalf("AllowsAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

func TestDestinationPolicyWhitelistWithInvalidRulesDeniesAll(t *testing.T) {
	p := CompileDestinationPolicy(ModeWhitelist, "1.1.1.1/32:80\n", Options{})
	if p.AllowsAddr("1.1.1.1:80") {
		t.Fatal("whitelist with only invalid entries should deny all")
	}
}

func TestResolveGeoIPPathUsesConfigDirAndConfiguredPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "conf", "nps.conf")
	if got := ResolveGeoIPPath(configPath, ""); got != filepath.Join(filepath.Dir(configPath), "geoip.dat") {
		t.Fatalf("ResolveGeoIPPath(default) = %q", got)
	}
	if got := ResolveGeoIPPath(configPath, "data/geoip.dat"); got != filepath.Join(filepath.Dir(configPath), "data", "geoip.dat") {
		t.Fatalf("ResolveGeoIPPath(relative) = %q", got)
	}
	absPath := filepath.Join(t.TempDir(), "custom", "geoip.dat")
	if got := ResolveGeoIPPath(configPath, absPath); got != absPath {
		t.Fatalf("ResolveGeoIPPath(abs) = %q, want %q", got, absPath)
	}
}

func TestResolveGeoSitePathUsesConfigDirAndConfiguredPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "conf", "nps.conf")
	if got := ResolveGeoSitePath(configPath, ""); got != filepath.Join(filepath.Dir(configPath), "geosite.dat") {
		t.Fatalf("ResolveGeoSitePath(default) = %q", got)
	}
	if got := ResolveGeoSitePath(configPath, "data/geosite.dat"); got != filepath.Join(filepath.Dir(configPath), "data", "geosite.dat") {
		t.Fatalf("ResolveGeoSitePath(relative) = %q", got)
	}
	absPath := filepath.Join(t.TempDir(), "custom", "geosite.dat")
	if got := ResolveGeoSitePath(configPath, absPath); got != absPath {
		t.Fatalf("ResolveGeoSitePath(abs) = %q, want %q", got, absPath)
	}
}

func writeGeoIPDat(t *testing.T, code string, prefixes ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "geoip.dat")
	body := protowire.AppendTag(nil, 1, protowire.BytesType)
	body = protowire.AppendString(body, code)
	for _, prefixText := range prefixes {
		prefix, err := parseTestPrefix(prefixText)
		if err != nil {
			t.Fatalf("parseTestPrefix(%q) error = %v", prefixText, err)
		}
		cidr := protowire.AppendTag(nil, 1, protowire.BytesType)
		cidr = protowire.AppendBytes(cidr, prefix.Addr().AsSlice())
		cidr = protowire.AppendTag(cidr, 2, protowire.VarintType)
		cidr = protowire.AppendVarint(cidr, uint64(prefix.Bits()))
		body = protowire.AppendTag(body, 2, protowire.BytesType)
		body = protowire.AppendBytes(body, cidr)
	}
	record := []byte{0x0a}
	record = protowire.AppendVarint(record, uint64(len(body)))
	record = append(record, body...)
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func parseTestPrefix(value string) (netip.Prefix, error) {
	return netip.ParsePrefix(value)
}

type geoSiteTestDomain struct {
	typ   int
	value string
	attrs []string
}

func writeGeoSiteDat(t *testing.T, code string, domains ...geoSiteTestDomain) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "geosite.dat")
	body := protowire.AppendTag(nil, 1, protowire.BytesType)
	body = protowire.AppendString(body, code)
	for _, domain := range domains {
		entry := protowire.AppendTag(nil, 1, protowire.VarintType)
		entry = protowire.AppendVarint(entry, uint64(domain.typ))
		entry = protowire.AppendTag(entry, 2, protowire.BytesType)
		entry = protowire.AppendString(entry, domain.value)
		for _, attr := range domain.attrs {
			attrBody := protowire.AppendTag(nil, 1, protowire.BytesType)
			attrBody = protowire.AppendString(attrBody, attr)
			entry = protowire.AppendTag(entry, 3, protowire.BytesType)
			entry = protowire.AppendBytes(entry, attrBody)
		}
		body = protowire.AppendTag(body, 2, protowire.BytesType)
		body = protowire.AppendBytes(body, entry)
	}
	record := []byte{0x0a}
	record = protowire.AppendVarint(record, uint64(len(body)))
	record = append(record, body...)
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
