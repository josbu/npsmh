package policy

import "strings"

type Mode uint8

const (
	ModeOff Mode = iota
	ModeWhitelist
	ModeBlacklist
)

type Options struct {
	GeoIPPath   string
	GeoSitePath string
}

type SourceIPPolicy struct {
	mode       Mode
	hasEntries bool
	matcher    *IPMatcher
}

type DestinationPolicy struct {
	mode          Mode
	hasEntries    bool
	ipMatcher     *IPMatcher
	domainMatcher *DomainMatcher
}

func NormalizeMode(mode int) Mode {
	switch mode {
	case int(ModeWhitelist):
		return ModeWhitelist
	case int(ModeBlacklist):
		return ModeBlacklist
	default:
		return ModeOff
	}
}

func CompileSourceIPPolicy(mode Mode, entries []string, opts Options) *SourceIPPolicy {
	matcher, hasEntries := compileIPMatcher(entries, opts, "source access", false)
	return &SourceIPPolicy{
		mode:       mode,
		hasEntries: hasEntries,
		matcher:    matcher,
	}
}

func (p *SourceIPPolicy) AllowsAddr(addr string) bool {
	if p == nil {
		return true
	}
	parsed, ok := parseAddr(addr)
	if !ok {
		return allows(p.mode, p.hasEntries, false)
	}
	return allows(p.mode, p.hasEntries, p.matcher != nil && p.matcher.Contains(parsed))
}

func (p *SourceIPPolicy) BlocksAddr(addr string) bool {
	return !p.AllowsAddr(addr)
}

func CompileDestinationPolicy(mode Mode, raw string, opts Options) *DestinationPolicy {
	entries := splitRules(raw)
	ipMatcher, ipEntries := compileIPMatcher(entries, opts, "destination access", true)
	domainMatcher, domainEntries := compileDomainMatcher(entries, opts, "destination access", true)
	return &DestinationPolicy{
		mode:          mode,
		hasEntries:    ipEntries || domainEntries,
		ipMatcher:     ipMatcher,
		domainMatcher: domainMatcher,
	}
}

func (p *DestinationPolicy) AllowsAddr(addr string) bool {
	if p == nil {
		return true
	}
	matched := false
	if parsed, ok := parseAddr(addr); ok {
		matched = p.ipMatcher != nil && p.ipMatcher.Contains(parsed)
	} else {
		host := normalizeHostFromAddr(addr)
		if host != "" && p.domainMatcher != nil {
			matched = p.domainMatcher.Contains(host)
		}
	}
	return allows(p.mode, p.hasEntries, matched)
}

func splitRules(raw string) []string {
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func allows(mode Mode, hasEntries bool, matched bool) bool {
	switch mode {
	case ModeWhitelist:
		if !hasEntries {
			return false
		}
		return matched
	case ModeBlacklist:
		if !hasEntries {
			return true
		}
		return !matched
	default:
		return true
	}
}
