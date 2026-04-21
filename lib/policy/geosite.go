package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	geoSiteTypePlain  = 0
	geoSiteTypeRegex  = 1
	geoSiteTypeDomain = 2
	geoSiteTypeFull   = 3
)

type geoSiteDomain struct {
	typ   int
	value string
	attrs map[string]struct{}
}

var geoSiteCache = struct {
	sync.RWMutex
	entries map[string][]geoSiteDomain
}{
	entries: make(map[string][]geoSiteDomain),
}

var defaultGeoSitePath = struct {
	sync.RWMutex
	value string
}{}

func ResetGeoSiteCache() {
	geoSiteCache.Lock()
	geoSiteCache.entries = make(map[string][]geoSiteDomain)
	geoSiteCache.Unlock()
}

func SetDefaultGeoSitePath(path string) {
	defaultGeoSitePath.Lock()
	defaultGeoSitePath.value = strings.TrimSpace(path)
	defaultGeoSitePath.Unlock()
}

func ResolveGeoSitePath(configPath, configuredPath string) string {
	if path := strings.TrimSpace(configuredPath); path != "" {
		if filepath.IsAbs(path) {
			return path
		}
		if configPath != "" {
			return filepath.Join(filepath.Dir(configPath), path)
		}
		return common.ResolvePath(path)
	}
	if configPath != "" {
		return filepath.Join(filepath.Dir(configPath), "geosite.dat")
	}
	return filepath.Join(common.GetRunPath(), "conf", "geosite.dat")
}

func resolveGeoSitePath(opts Options) string {
	if path := strings.TrimSpace(opts.GeoSitePath); path != "" {
		return ResolveGeoSitePath("", path)
	}
	defaultGeoSitePath.RLock()
	path := defaultGeoSitePath.value
	defaultGeoSitePath.RUnlock()
	if path != "" {
		return path
	}
	return filepath.Join(common.GetRunPath(), "conf", "geosite.dat")
}

func resolveGeoDataExternalPath(defaultPath, file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	if filepath.IsAbs(file) {
		return file
	}
	if defaultPath != "" {
		return filepath.Join(filepath.Dir(defaultPath), file)
	}
	return common.ResolvePath(file)
}

func loadGeoSiteDomains(opts Options, path, code string) ([]geoSiteDomain, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return nil, fmt.Errorf("empty geosite code")
	}
	if path == "" {
		path = resolveGeoSitePath(opts)
	}
	cacheKey := path + "\x00" + code

	geoSiteCache.RLock()
	if domains, ok := geoSiteCache.entries[cacheKey]; ok {
		geoSiteCache.RUnlock()
		return cloneGeoSiteDomains(domains), nil
	}
	geoSiteCache.RUnlock()

	domains, err := readGeoSiteDomains(path, code)
	if err != nil {
		return nil, err
	}

	geoSiteCache.Lock()
	geoSiteCache.entries[cacheKey] = cloneGeoSiteDomains(domains)
	geoSiteCache.Unlock()
	return domains, nil
}

func cloneGeoSiteDomains(domains []geoSiteDomain) []geoSiteDomain {
	if len(domains) == 0 {
		return nil
	}
	out := make([]geoSiteDomain, 0, len(domains))
	for _, domain := range domains {
		cloned := geoSiteDomain{
			typ:   domain.typ,
			value: domain.value,
		}
		if len(domain.attrs) > 0 {
			cloned.attrs = make(map[string]struct{}, len(domain.attrs))
			for key := range domain.attrs {
				cloned.attrs[key] = struct{}{}
			}
		}
		out = append(out, cloned)
	}
	return out
}

func readGeoSiteDomains(path, code string) ([]geoSiteDomain, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	record, err := findFramedCodeRecord(file, []byte(code))
	if err != nil {
		return nil, err
	}
	domains, err := decodeGeoSiteRecord(record)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("geosite code %s has no domains", code)
	}
	return domains, nil
}

func decodeGeoSiteRecord(record []byte) ([]geoSiteDomain, error) {
	domains := make([]geoSiteDomain, 0, 64)
	for len(record) > 0 {
		num, typ, n := protowire.ConsumeTag(record)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		record = record[n:]
		switch num {
		case 2:
			if typ != protowire.BytesType {
				skip, err := skipFieldValue(num, typ, record)
				if err != nil {
					return nil, err
				}
				record = record[skip:]
				continue
			}
			value, m := protowire.ConsumeBytes(record)
			if m < 0 {
				return nil, protowire.ParseError(m)
			}
			record = record[m:]
			domain, ok, err := decodeGeoSiteDomain(value)
			if err != nil {
				return nil, err
			}
			if ok {
				domains = append(domains, domain)
			}
		default:
			skip, err := skipFieldValue(num, typ, record)
			if err != nil {
				return nil, err
			}
			record = record[skip:]
		}
	}
	return domains, nil
}

func decodeGeoSiteDomain(record []byte) (geoSiteDomain, bool, error) {
	var domain geoSiteDomain
	for len(record) > 0 {
		num, typ, n := protowire.ConsumeTag(record)
		if n < 0 {
			return geoSiteDomain{}, false, protowire.ParseError(n)
		}
		record = record[n:]
		switch num {
		case 1:
			value, m := protowire.ConsumeVarint(record)
			if m < 0 {
				return geoSiteDomain{}, false, protowire.ParseError(m)
			}
			record = record[m:]
			domain.typ = int(value)
		case 2:
			value, m := protowire.ConsumeBytes(record)
			if m < 0 {
				return geoSiteDomain{}, false, protowire.ParseError(m)
			}
			record = record[m:]
			domain.value = strings.TrimSpace(strings.ToLower(string(value)))
		case 3:
			value, m := protowire.ConsumeBytes(record)
			if m < 0 {
				return geoSiteDomain{}, false, protowire.ParseError(m)
			}
			record = record[m:]
			key, ok, err := decodeGeoSiteAttrKey(value)
			if err != nil {
				return geoSiteDomain{}, false, err
			}
			if ok {
				if domain.attrs == nil {
					domain.attrs = make(map[string]struct{})
				}
				domain.attrs[key] = struct{}{}
			}
		default:
			skip, err := skipFieldValue(num, typ, record)
			if err != nil {
				return geoSiteDomain{}, false, err
			}
			record = record[skip:]
		}
	}
	if domain.value == "" {
		return geoSiteDomain{}, false, nil
	}
	switch domain.typ {
	case geoSiteTypePlain, geoSiteTypeRegex, geoSiteTypeDomain, geoSiteTypeFull:
		return domain, true, nil
	default:
		return geoSiteDomain{}, false, nil
	}
}

func decodeGeoSiteAttrKey(record []byte) (string, bool, error) {
	for len(record) > 0 {
		num, typ, n := protowire.ConsumeTag(record)
		if n < 0 {
			return "", false, protowire.ParseError(n)
		}
		record = record[n:]
		switch num {
		case 1:
			value, m := protowire.ConsumeBytes(record)
			if m < 0 {
				return "", false, protowire.ParseError(m)
			}
			record = record[m:]
			key := strings.TrimSpace(strings.ToLower(string(value)))
			if key != "" {
				return key, true, nil
			}
		default:
			skip, err := skipFieldValue(num, typ, record)
			if err != nil {
				return "", false, err
			}
			record = record[skip:]
		}
	}
	return "", false, nil
}

func compileGeoSiteToken(token string, opts Options, matcher *DomainMatcher, scope string) (handled bool, valid bool) {
	lower := strings.ToLower(token)
	switch {
	case strings.HasPrefix(lower, "geosite:"):
		site := strings.TrimSpace(token[len("geosite:"):])
		domains, err := loadGeoSiteDomainsWithAttr(opts, resolveGeoSitePath(opts), site)
		if err != nil {
			logs.Warn("load %s geosite rule %s failed: %v", scope, token, err)
			return true, false
		}
		addGeoSiteDomains(matcher, domains)
		return true, true
	case strings.HasPrefix(lower, "ext-domain:"):
		file, site, ok := splitExternalGeoDataRule(token[len("ext-domain:"):])
		if !ok {
			return true, false
		}
		path := resolveGeoDataExternalPath(resolveGeoSitePath(opts), file)
		domains, err := loadGeoSiteDomainsWithAttr(opts, path, site)
		if err != nil {
			logs.Warn("load %s geosite rule %s failed: %v", scope, token, err)
			return true, false
		}
		addGeoSiteDomains(matcher, domains)
		return true, true
	case strings.HasPrefix(lower, "ext:"):
		file, site, ok := splitExternalGeoDataRule(token[len("ext:"):])
		if !ok {
			return true, false
		}
		path := resolveGeoDataExternalPath(resolveGeoSitePath(opts), file)
		domains, err := loadGeoSiteDomainsWithAttr(opts, path, site)
		if err != nil {
			logs.Warn("load %s geosite rule %s failed: %v", scope, token, err)
			return true, false
		}
		addGeoSiteDomains(matcher, domains)
		return true, true
	default:
		return false, false
	}
}

func loadGeoSiteDomainsWithAttr(opts Options, path, siteWithAttr string) ([]geoSiteDomain, error) {
	parts := strings.Split(siteWithAttr, "@")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty geosite")
	}
	code := strings.ToUpper(strings.TrimSpace(parts[0]))
	if code == "" {
		return nil, fmt.Errorf("empty geosite code")
	}
	domains, err := loadGeoSiteDomains(opts, path, code)
	if err != nil {
		return nil, err
	}
	if len(parts) == 1 {
		return domains, nil
	}
	attrs := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		attr := strings.TrimSpace(strings.ToLower(part))
		if attr != "" {
			attrs = append(attrs, attr)
		}
	}
	if len(attrs) == 0 {
		return domains, nil
	}
	filtered := make([]geoSiteDomain, 0, len(domains))
	for _, domain := range domains {
		if matchGeoSiteAttrs(domain, attrs) {
			filtered = append(filtered, domain)
		}
	}
	return filtered, nil
}

func matchGeoSiteAttrs(domain geoSiteDomain, attrs []string) bool {
	if len(attrs) == 0 {
		return true
	}
	if len(domain.attrs) == 0 {
		return false
	}
	for _, attr := range attrs {
		if _, ok := domain.attrs[attr]; !ok {
			return false
		}
	}
	return true
}

func addGeoSiteDomains(matcher *DomainMatcher, domains []geoSiteDomain) {
	for _, domain := range domains {
		switch domain.typ {
		case geoSiteTypeFull:
			if matcher.exact == nil {
				matcher.exact = make(map[string]struct{})
			}
			matcher.exact[domain.value] = struct{}{}
		case geoSiteTypeDomain:
			matcher.tree.add(domain.value, true, false)
		case geoSiteTypePlain:
			matcher.keywords = append(matcher.keywords, domain.value)
		case geoSiteTypeRegex:
			re, err := regexp.Compile(domain.value)
			if err == nil {
				matcher.regexps = append(matcher.regexps, re)
			}
		}
	}
}

func splitExternalGeoDataRule(value string) (file string, code string, ok bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	file, code, ok = strings.Cut(value, ":")
	if !ok {
		return "", "", false
	}
	file = strings.TrimSpace(file)
	code = strings.TrimSpace(code)
	if file == "" || code == "" {
		return "", "", false
	}
	return file, code, true
}
