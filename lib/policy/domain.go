package policy

import (
	"net/netip"
	"regexp"
	"strings"

	"github.com/djylb/nps/lib/logs"
)

type DomainMatcher struct {
	exact      map[string]struct{}
	tree       domainTree
	keywords   []string
	regexps    []*regexp.Regexp
	dotless    []string
	dotlessAny bool
}

type domainTree struct {
	root *domainNode
}

type domainNode struct {
	children     map[string]*domainNode
	matchSelf    bool
	matchSubOnly bool
}

func (m *DomainMatcher) Contains(host string) bool {
	host = normalizeHostFromAddr(host)
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	if len(m.exact) != 0 {
		if _, ok := m.exact[host]; ok {
			return true
		}
	}
	if m.tree.match(host) {
		return true
	}
	for _, keyword := range m.keywords {
		if strings.Contains(host, keyword) {
			return true
		}
	}
	for _, re := range m.regexps {
		if re.MatchString(host) {
			return true
		}
	}
	if !strings.Contains(host, ".") {
		if m.dotlessAny {
			return true
		}
		for _, pattern := range m.dotless {
			if strings.Contains(host, pattern) {
				return true
			}
		}
	}
	return false
}

func (t *domainTree) add(domain string, includeSelf bool, subOnly bool) {
	if domain == "" {
		return
	}
	if t.root == nil {
		t.root = &domainNode{}
	}
	node := t.root
	for _, label := range reverseDomainLabels(domain) {
		if node.children == nil {
			node.children = make(map[string]*domainNode)
		}
		child := node.children[label]
		if child == nil {
			child = &domainNode{}
			node.children[label] = child
		}
		node = child
	}
	if includeSelf {
		node.matchSelf = true
	}
	if subOnly {
		node.matchSubOnly = true
	}
}

func (t *domainTree) match(domain string) bool {
	if t == nil || t.root == nil || domain == "" {
		return false
	}
	node := t.root
	labels := reverseDomainLabels(domain)
	for i, label := range labels {
		if node.children == nil {
			return false
		}
		node = node.children[label]
		if node == nil {
			return false
		}
		extraLabels := len(labels) - i - 1
		if node.matchSelf {
			return true
		}
		if node.matchSubOnly && extraLabels > 0 {
			return true
		}
	}
	return false
}

func reverseDomainLabels(domain string) []string {
	parts := strings.Split(domain, ".")
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return parts
}

func compileDomainMatcher(entries []string, opts Options, scope string, ignoreNonDomainTokens bool) (*DomainMatcher, bool) {
	matcher := &DomainMatcher{}
	hasEntries := false
	for _, entry := range entries {
		token := strings.TrimSpace(strings.ReplaceAll(entry, "：", ":"))
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "#") || strings.HasPrefix(token, ";") {
			continue
		}
		hasEntries = true

		handled, valid := compileStructuredDomainRule(token, matcher)
		if handled {
			if !valid {
				logs.Warn("ignore invalid %s domain rule: %s", scope, token)
			}
			continue
		}
		handled, valid = compileGeoSiteToken(token, opts, matcher, scope)
		if handled {
			if !valid {
				logs.Warn("ignore invalid %s domain rule: %s", scope, token)
			}
			continue
		}

		if strings.HasPrefix(strings.ToLower(token), "geoip:") {
			continue
		}
		if _, ok := parseIPv4WildcardPrefix(token); ok {
			continue
		}
		if strings.Contains(token, "/") {
			if _, err := netip.ParsePrefix(token); err == nil {
				continue
			}
			if ignoreNonDomainTokens {
				continue
			}
			logs.Warn("ignore invalid %s domain rule: %s", scope, token)
			continue
		}
		if strings.Contains(token, "*") && !strings.HasPrefix(token, "*") {
			if ignoreNonDomainTokens {
				logs.Warn("ignore invalid %s ip wildcard rule: %s", scope, token)
				continue
			}
			logs.Warn("ignore invalid %s domain rule: %s", scope, token)
			continue
		}
		if strings.HasPrefix(token, "*") {
			subOnly := strings.HasPrefix(token, "*.")
			suffixRaw := strings.TrimPrefix(token, "*")
			if subOnly {
				suffixRaw = strings.TrimPrefix(token, "*.")
			}
			suffix, ok := normalizeDomainToken(suffixRaw)
			if !ok {
				logs.Warn("ignore invalid %s domain rule: %s", scope, token)
				continue
			}
			matcher.tree.add(suffix, !subOnly, subOnly)
			continue
		}
		ihost, ok := normalizeDomainToken(token)
		if !ok {
			if ignoreNonDomainTokens {
				continue
			}
			logs.Warn("ignore invalid %s domain rule: %s", scope, token)
			continue
		}
		pattern := normalizeDomainKeywordToken(ihost)
		if pattern == "" {
			continue
		}
		matcher.keywords = append(matcher.keywords, pattern)
	}

	if len(matcher.exact) == 0 &&
		matcher.tree.root == nil &&
		len(matcher.keywords) == 0 &&
		len(matcher.regexps) == 0 &&
		len(matcher.dotless) == 0 &&
		!matcher.dotlessAny {
		return nil, hasEntries
	}
	return matcher, hasEntries
}

func compileStructuredDomainRule(token string, matcher *DomainMatcher) (handled bool, valid bool) {
	lower := strings.ToLower(token)
	switch {
	case strings.HasPrefix(lower, "full:"):
		host, ok := normalizeDomainToken(token[len("full:"):])
		if !ok {
			return true, false
		}
		if matcher.exact == nil {
			matcher.exact = make(map[string]struct{})
		}
		matcher.exact[host] = struct{}{}
		return true, true
	case strings.HasPrefix(lower, "domain:"):
		host, ok := normalizeDomainToken(token[len("domain:"):])
		if !ok {
			return true, false
		}
		matcher.tree.add(host, true, false)
		return true, true
	case strings.HasPrefix(lower, "keyword:"):
		pattern := normalizeDomainKeywordToken(token[len("keyword:"):])
		if pattern == "" {
			return true, false
		}
		matcher.keywords = append(matcher.keywords, pattern)
		return true, true
	case strings.HasPrefix(lower, "regexp:"):
		pattern := strings.TrimSpace(token[len("regexp:"):])
		if pattern == "" {
			return true, false
		}
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return true, false
		}
		matcher.regexps = append(matcher.regexps, re)
		return true, true
	case strings.HasPrefix(lower, "dotless:"):
		raw := strings.TrimSpace(token[len("dotless:"):])
		if raw == "" {
			matcher.dotlessAny = true
			return true, true
		}
		if strings.Contains(raw, ".") {
			return true, false
		}
		pattern := normalizeDomainKeywordToken(raw)
		if pattern == "" {
			return true, false
		}
		matcher.dotless = append(matcher.dotless, pattern)
		return true, true
	default:
		return false, false
	}
}

func normalizeDomainToken(input string) (string, bool) {
	host, ok := normalizeHostToken(input)
	if !ok {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	return host, true
}

func normalizeDomainKeywordToken(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}
	if strings.ContainsAny(input, " \t\r\n/?#") {
		return ""
	}
	return strings.TrimSuffix(input, ".")
}
