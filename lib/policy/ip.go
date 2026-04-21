package policy

import (
	"net/netip"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"go4.org/netipx"
)

type IPMatcher struct {
	exact    map[netip.Addr]struct{}
	set      *netipx.IPSet
	negative []*netipx.IPSet
}

func (m *IPMatcher) Contains(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if len(m.exact) != 0 {
		if _, ok := m.exact[addr]; ok {
			return true
		}
	}
	if m.set != nil && m.set.Contains(addr) {
		return true
	}
	for _, set := range m.negative {
		if set != nil && !set.Contains(addr) {
			return true
		}
	}
	return false
}

func compileIPMatcher(entries []string, opts Options, scope string, ignoreNonIPTokens bool) (*IPMatcher, bool) {
	var (
		builder     netipx.IPSetBuilder
		exact       map[netip.Addr]struct{}
		hasPrefixes bool
		hasEntries  bool
		negative    []*netipx.IPSet
	)

	for _, entry := range entries {
		token := strings.TrimSpace(strings.ReplaceAll(entry, "：", ":"))
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "#") || strings.HasPrefix(token, ";") {
			continue
		}
		hasEntries = true
		if compileGeoIPToken(token, opts, scope, &builder, &negative, &hasPrefixes) {
			continue
		}
		if prefix, ok := parseIPv4WildcardPrefix(token); ok {
			builder.AddPrefix(prefix)
			hasPrefixes = true
			continue
		}
		if strings.Contains(token, "*") {
			if ignoreNonIPTokens && strings.HasPrefix(token, "*") {
				continue
			}
			logs.Warn("ignore invalid %s ip wildcard rule: %s", scope, token)
			continue
		}
		if strings.Contains(token, "/") {
			prefix, err := netip.ParsePrefix(token)
			if err != nil {
				logs.Warn("ignore invalid %s cidr rule: %s", scope, token)
				continue
			}
			builder.AddPrefix(prefix.Masked())
			hasPrefixes = true
			continue
		}

		host, ok := normalizeHostToken(token)
		if !ok {
			logs.Warn("ignore invalid %s ip rule: %s", scope, token)
			continue
		}
		addr, err := netip.ParseAddr(host)
		if err != nil {
			if ignoreNonIPTokens {
				continue
			}
			logs.Warn("ignore invalid %s ip rule: %s", scope, token)
			continue
		}
		addr = addr.Unmap()
		if exact == nil {
			exact = make(map[netip.Addr]struct{})
		}
		exact[addr] = struct{}{}
	}

	var set *netipx.IPSet
	if hasPrefixes {
		var err error
		set, err = builder.IPSet()
		if err != nil {
			logs.Warn("build %s ip set failed: %v", scope, err)
			set = nil
		}
	}

	if len(exact) == 0 && set == nil && len(negative) == 0 {
		return nil, hasEntries
	}
	return &IPMatcher{exact: exact, set: set, negative: negative}, hasEntries
}

func compileGeoIPToken(token string, opts Options, scope string, builder *netipx.IPSetBuilder, negative *[]*netipx.IPSet, hasPrefixes *bool) bool {
	path, code, handled, ok := parseGeoIPRule(token, opts)
	if !handled {
		return false
	}
	if !ok {
		logs.Warn("ignore invalid %s geoip rule: %s", scope, token)
		return true
	}
	reverse := strings.HasPrefix(code, "!")
	if reverse {
		code = strings.TrimSpace(code[1:])
	}
	if code == "" {
		logs.Warn("ignore invalid %s geoip rule: %s", scope, token)
		return true
	}
	prefixes, err := loadGeoIPPrefixes(opts, path, code)
	if err != nil {
		logs.Warn("load %s geoip rule %s failed: %v", scope, token, err)
		return true
	}
	if reverse {
		set, err := buildIPSet(prefixes)
		if err != nil {
			logs.Warn("build %s geoip rule %s failed: %v", scope, token, err)
			return true
		}
		*negative = append(*negative, set)
		return true
	}
	for _, prefix := range prefixes {
		builder.AddPrefix(prefix)
		*hasPrefixes = true
	}
	return true
}

func parseGeoIPRule(token string, opts Options) (path string, code string, handled bool, ok bool) {
	lower := strings.ToLower(token)
	switch {
	case strings.HasPrefix(lower, "geoip:"):
		return resolveGeoIPPath(opts), strings.TrimSpace(token[len("geoip:"):]), true, true
	case strings.HasPrefix(lower, "ext-ip:"):
		file, code, ok := splitExternalGeoDataRule(token[len("ext-ip:"):])
		if !ok {
			return "", "", true, false
		}
		return resolveGeoDataExternalPath(resolveGeoIPPath(opts), file), code, true, true
	case strings.HasPrefix(lower, "ext:"):
		file, code, ok := splitExternalGeoDataRule(token[len("ext:"):])
		if !ok {
			return "", "", true, false
		}
		return resolveGeoDataExternalPath(resolveGeoIPPath(opts), file), code, true, true
	default:
		return "", "", false, false
	}
}

func buildIPSet(prefixes []netip.Prefix) (*netipx.IPSet, error) {
	var builder netipx.IPSetBuilder
	for _, prefix := range prefixes {
		builder.AddPrefix(prefix)
	}
	return builder.IPSet()
}

func parseIPv4WildcardPrefix(token string) (netip.Prefix, bool) {
	if !strings.Contains(token, "*") {
		return netip.Prefix{}, false
	}
	if strings.Contains(token, ":") || strings.Contains(token, "/") {
		return netip.Prefix{}, false
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return netip.Prefix{}, false
	}
	for len(parts) < 4 {
		parts = append(parts, "*")
	}
	fixed := 0
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "*" {
			for j := i + 1; j < len(parts); j++ {
				if strings.TrimSpace(parts[j]) != "*" {
					return netip.Prefix{}, false
				}
			}
			break
		}
		value, ok := parseOctet(part)
		if !ok {
			return netip.Prefix{}, false
		}
		parts[i] = value
		fixed++
	}
	if fixed == 0 || fixed == len(parts) {
		return netip.Prefix{}, false
	}

	addrText := strings.Join([]string{parts[0], parts[1], parts[2], parts[3]}, ".")
	addr, err := netip.ParseAddr(strings.ReplaceAll(addrText, "*", "0"))
	if err != nil || !addr.Is4() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, fixed*8).Masked(), true
}

func parseOctet(part string) (string, bool) {
	if part == "" {
		return "", false
	}
	for _, r := range part {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	if len(part) > 1 && part[0] == '0' {
		part = strings.TrimLeft(part, "0")
		if part == "" {
			part = "0"
		}
	}
	if len(part) > 3 {
		return "", false
	}
	value := common.GetIntNoErrByStr(part)
	if value < 0 || value > 255 {
		return "", false
	}
	return part, true
}

func parseAddr(addr string) (netip.Addr, bool) {
	ip := common.ParseIPFromAddr(strings.TrimSpace(addr))
	if ip == nil {
		return netip.Addr{}, false
	}
	parsed, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return parsed.Unmap(), true
}

func normalizeHostToken(input string) (string, bool) {
	host := normalizeHostFromAddr(input)
	if host == "" {
		return "", false
	}
	if strings.ContainsAny(host, " \t\r\n/?#") {
		return "", false
	}
	return host, true
}

func normalizeHostFromAddr(input string) string {
	host := common.ExtractHost(strings.TrimSpace(input))
	host = common.RemovePortFromHost(host)
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if i := strings.LastIndexByte(host, '%'); i != -1 {
		host = host[:i]
	}
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}
