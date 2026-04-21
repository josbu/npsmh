package file

import (
	"strings"

	"github.com/djylb/nps/lib/policy"
)

func normalizePolicyRules(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			normalized = append(normalized, line)
		}
	}
	return strings.Join(normalized, "\n")
}

func normalizePolicyRuleEntries(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = normalizePolicyRules(entry)
		if entry == "" {
			continue
		}
		for _, line := range strings.Split(entry, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				normalized = append(normalized, line)
			}
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func policyRulesToLines(raw string) []string {
	return normalizePolicyRuleEntries([]string{raw})
}

func promoteLegacyBlacklistPolicy(mode int, raw string, legacy []string) (int, string) {
	normalizedMode := int(policy.NormalizeMode(mode))
	normalizedRules := normalizePolicyRules(raw)
	legacyEntries := normalizePolicyRuleEntries(legacy)
	if normalizedMode == AclOff && len(legacyEntries) > 0 {
		normalizedMode = AclBlacklist
		normalizedRules = strings.Join(legacyEntries, "\n")
	}
	if normalizedMode == AclBlacklist && normalizedRules == "" && len(legacyEntries) > 0 {
		normalizedRules = strings.Join(legacyEntries, "\n")
	}
	return normalizedMode, normalizedRules
}

func compileExplicitSourcePolicy(mode int, raw string) (int, string, *policy.SourceIPPolicy) {
	normalizedMode := int(policy.NormalizeMode(mode))
	normalizedRules := normalizePolicyRules(raw)
	if normalizedMode == AclOff {
		normalizedRules = ""
	}
	entries := policyRulesToLines(normalizedRules)
	compiled := policy.CompileSourceIPPolicy(policy.NormalizeMode(normalizedMode), entries, policy.Options{})
	return normalizedMode, normalizedRules, compiled
}

func compileDestinationPolicies(mode int, raw string) (int, string, *policy.SourceIPPolicy, *policy.DestinationPolicy) {
	normalizedMode := int(policy.NormalizeMode(mode))
	normalizedRules := normalizePolicyRules(raw)
	if normalizedMode == AclOff {
		normalizedRules = ""
	}
	entries := policyRulesToLines(normalizedRules)
	ipCompiled := policy.CompileSourceIPPolicy(policy.NormalizeMode(normalizedMode), entries, policy.Options{})
	compiled := policy.CompileDestinationPolicy(policy.NormalizeMode(normalizedMode), normalizedRules, policy.Options{})
	return normalizedMode, normalizedRules, ipCompiled, compiled
}

func (u *User) CompileSourcePolicy() {
	if u == nil {
		return
	}

	u.Lock()
	mode := u.EntryAclMode
	raw := u.EntryAclRules
	u.Unlock()

	mode, raw, compiled := compileExplicitSourcePolicy(mode, raw)

	u.Lock()
	u.EntryAclMode = mode
	u.EntryAclRules = raw
	u.sourcePolicy = compiled
	u.Unlock()
}

func (u *User) AllowsSourceAddr(addr string) bool {
	if u == nil {
		return true
	}

	u.RLock()
	compiled := u.sourcePolicy
	u.RUnlock()
	if compiled == nil {
		u.CompileSourcePolicy()
		u.RLock()
		compiled = u.sourcePolicy
		u.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (u *User) CompileDestACL() {
	if u == nil {
		return
	}

	u.Lock()
	mode := u.DestAclMode
	raw := u.DestAclRules
	u.Unlock()

	mode, raw, ipCompiled, compiled := compileDestinationPolicies(mode, raw)

	u.Lock()
	u.DestAclMode = mode
	u.DestAclRules = raw
	u.destIPPolicy = ipCompiled
	u.destPolicy = compiled
	u.Unlock()
}

func (u *User) AllowsDestination(addr string) bool {
	if u == nil {
		return true
	}

	u.RLock()
	compiled := u.destPolicy
	u.RUnlock()
	if compiled == nil {
		u.CompileDestACL()
		u.RLock()
		compiled = u.destPolicy
		u.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (u *User) AllowsDestinationIP(addr string) bool {
	if u == nil {
		return true
	}

	u.RLock()
	compiled := u.destIPPolicy
	u.RUnlock()
	if compiled == nil {
		u.CompileDestACL()
		u.RLock()
		compiled = u.destIPPolicy
		u.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (s *Client) CompileSourcePolicy() {
	if s == nil {
		return
	}

	s.RLock()
	mode := s.EntryAclMode
	raw := s.EntryAclRules
	legacy := s.LegacyBlackIPImport()
	s.RUnlock()

	mode, raw = promoteLegacyBlacklistPolicy(mode, raw, legacy)
	normalizedMode, normalizedRules, compiled := compileExplicitSourcePolicy(mode, raw)

	s.Lock()
	s.EntryAclMode = normalizedMode
	s.EntryAclRules = normalizedRules
	s.legacyBlackIPList = nil
	s.sourcePolicy = compiled
	s.Unlock()
}

func (s *Client) AllowsSourceAddr(addr string) bool {
	if s == nil {
		return true
	}
	s.RLock()
	compiled := s.sourcePolicy
	s.RUnlock()
	if compiled == nil {
		s.CompileSourcePolicy()
		s.RLock()
		compiled = s.sourcePolicy
		s.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (t *Tunnel) CompileDestACL() {
	if t == nil {
		return
	}

	t.RLock()
	mode := t.DestAclMode
	raw := t.DestAclRules
	t.RUnlock()

	normalizedMode, normalizedRules, ipCompiled, compiled := compileDestinationPolicies(mode, raw)

	t.Lock()
	t.DestAclMode = normalizedMode
	t.DestAclRules = normalizedRules
	t.DestAclSet = compiled
	t.destIPPolicy = ipCompiled
	t.destPolicy = compiled
	t.Unlock()
}

func (t *Tunnel) AllowsDestination(addr string) bool {
	if t == nil {
		return true
	}

	t.RLock()
	compiled := t.destPolicy
	t.RUnlock()
	if compiled == nil {
		t.CompileDestACL()
		t.RLock()
		compiled = t.destPolicy
		t.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (t *Tunnel) AllowsDestinationIP(addr string) bool {
	if t == nil {
		return true
	}

	t.RLock()
	compiled := t.destIPPolicy
	t.RUnlock()
	if compiled == nil {
		t.CompileDestACL()
		t.RLock()
		compiled = t.destIPPolicy
		t.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (t *Tunnel) CompileEntryACL() {
	if t == nil {
		return
	}

	t.RLock()
	mode := t.EntryAclMode
	raw := t.EntryAclRules
	t.RUnlock()

	compiledMode, normalizedRules, compiled := compileExplicitSourcePolicy(mode, raw)

	t.Lock()
	t.EntryAclMode = compiledMode
	t.EntryAclRules = normalizedRules
	t.entryPolicy = compiled
	t.Unlock()
}

func (t *Tunnel) AllowsSourceAddr(addr string) bool {
	if t == nil {
		return true
	}

	t.RLock()
	compiled := t.entryPolicy
	t.RUnlock()
	if compiled == nil {
		t.CompileEntryACL()
		t.RLock()
		compiled = t.entryPolicy
		t.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (h *Host) CompileEntryACL() {
	if h == nil {
		return
	}

	h.RLock()
	mode := h.EntryAclMode
	raw := h.EntryAclRules
	h.RUnlock()

	compiledMode, normalizedRules, compiled := compileExplicitSourcePolicy(mode, raw)

	h.Lock()
	h.EntryAclMode = compiledMode
	h.EntryAclRules = normalizedRules
	h.entryPolicy = compiled
	h.Unlock()
}

func (h *Host) AllowsSourceAddr(addr string) bool {
	if h == nil {
		return true
	}

	h.RLock()
	compiled := h.entryPolicy
	h.RUnlock()
	if compiled == nil {
		h.CompileEntryACL()
		h.RLock()
		compiled = h.entryPolicy
		h.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func (g *Glob) CompileSourcePolicy() {
	if g == nil {
		return
	}

	g.RLock()
	mode := g.EntryAclMode
	raw := g.EntryAclRules
	legacy := g.LegacyBlackIPImport()
	g.RUnlock()

	mode, raw = promoteLegacyBlacklistPolicy(mode, raw, legacy)
	normalizedMode, normalizedRules, compiled := compileExplicitSourcePolicy(mode, raw)

	g.Lock()
	g.EntryAclMode = normalizedMode
	g.EntryAclRules = normalizedRules
	g.legacyBlackIPList = nil
	g.sourcePolicy = compiled
	g.Unlock()
}

func (g *Glob) AllowsSourceAddr(addr string) bool {
	if g == nil {
		return true
	}
	g.RLock()
	compiled := g.sourcePolicy
	g.RUnlock()
	if compiled == nil {
		g.CompileSourcePolicy()
		g.RLock()
		compiled = g.sourcePolicy
		g.RUnlock()
	}
	if compiled == nil {
		return true
	}
	return compiled.AllowsAddr(addr)
}

func InitializeGlobalRuntime(glob *Glob) {
	if glob == nil {
		return
	}
	glob.CompileSourcePolicy()
}

func RecompileAccessPoliciesIfLoaded() {
	db := GetDb()
	if db == nil || db.JsonDb == nil {
		return
	}
	policy.ResetGeoIPCache()
	policy.ResetGeoSiteCache()
	if db.JsonDb.Global != nil {
		db.JsonDb.Global.CompileSourcePolicy()
	}
	db.RangeUsers(func(user *User) bool {
		user.CompileSourcePolicy()
		user.CompileDestACL()
		return true
	})
	db.RangeClients(func(client *Client) bool {
		client.CompileSourcePolicy()
		return true
	})
	db.RangeTasks(func(tunnel *Tunnel) bool {
		tunnel.CompileEntryACL()
		tunnel.CompileDestACL()
		return true
	})
	db.RangeHosts(func(host *Host) bool {
		host.CompileEntryACL()
		return true
	})
}
