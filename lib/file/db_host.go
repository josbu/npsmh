package file

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
)

func normalizeHostRoutingFields(host *Host) {
	if host == nil {
		return
	}
	if host.Location == "" {
		host.Location = "/"
	}
	host.Scheme = normalizedHostScheme(host.Scheme)
}

func finalizeHostForStore(host *Host) {
	if host == nil {
		return
	}
	host.CertType = common.GetCertType(host.CertFile)
	host.CertHash = crypt.FNV1a64(host.CertType, host.CertFile, host.KeyFile)
	if host.Flow == nil {
		host.Flow = new(Flow)
	}
	InitializeHostRuntime(host)
	host.CompileEntryACL()
}

func (s *DbUtils) DelHost(id int) error {
	h, ok := loadHostEntry(&s.JsonDb.Hosts, id)
	if !ok {
		return ErrHostNotFound
	}
	runtimeHostIndex().Remove(h.Host, id)
	if h.Rate != nil {
		h.Rate.Stop()
	}
	if h.Client != nil {
		s.JsonDb.hostClientIndex.remove(h.Client.Id, h.Id)
	}
	s.JsonDb.Hosts.Delete(id)
	s.JsonDb.hostClientIndex.markReady()
	s.JsonDb.StoreHosts()
	return nil
}

func (s *DbUtils) DeleteNoStoreHostIfCurrentOwnerless(host *Host) bool {
	if s == nil || s.JsonDb == nil || host == nil {
		return false
	}

	host.Lock()
	defer host.Unlock()

	current, ok := s.JsonDb.Hosts.Load(host.Id)
	if !ok || current != host || !host.NoStore || host.Client == nil {
		return false
	}
	if host.runtimeOwners != nil && host.runtimeOwners.count() > 0 {
		return false
	}

	runtimeHostIndex().Remove(host.Host, host.Id)
	if host.Rate != nil {
		host.Rate.Stop()
	}
	if host.Client != nil {
		s.JsonDb.hostClientIndex.remove(host.Client.Id, host.Id)
	}
	s.JsonDb.Hosts.Delete(host.Id)
	s.JsonDb.hostClientIndex.markReady()
	s.JsonDb.StoreHosts()
	return true
}

func (s *DbUtils) IsHostExist(h *Host) bool {
	if h == nil {
		return false
	}
	var exist bool
	location := normalizedHostLocation(h.Location)
	scheme := normalizedHostScheme(h.Scheme)
	s.RangeHosts(func(v *Host) bool {
		if v.Id != h.Id &&
			v.Host == h.Host &&
			normalizedHostLocation(v.Location) == location &&
			(normalizedHostScheme(v.Scheme) == "all" || normalizedHostScheme(v.Scheme) == scheme) {
			exist = true
			return false
		}
		return true
	})
	return exist
}

func (s *DbUtils) IsHostModify(h *Host) bool {
	if h == nil {
		return true
	}

	existingHost, err := s.GetHostById(h.Id)
	if err != nil {
		return true
	}

	if existingHost.IsClose != h.IsClose ||
		existingHost.Host != h.Host ||
		existingHost.Location != h.Location ||
		existingHost.Scheme != h.Scheme ||
		existingHost.HttpsJustProxy != h.HttpsJustProxy ||
		existingHost.CertFile != h.CertFile ||
		existingHost.KeyFile != h.KeyFile {
		return true
	}

	return false
}

func (s *DbUtils) NewHost(t *Host) error {
	if t == nil {
		return errors.New("host is nil")
	}
	var err error
	if t.Client, err = resolveStoredClientRef(s, t.Client); err != nil {
		return err
	}
	normalizeHostRoutingFields(t)
	if s.IsHostExist(t) {
		return errors.New("host has exist")
	}
	if t.Id == 0 {
		t.Id = int(s.JsonDb.GetHostId())
	} else if t.Id > int(s.JsonDb.HostIncreaseId) {
		s.JsonDb.HostIncreaseId = int32(t.Id)
	}
	finalizeHostForStore(t)
	runtimeHostIndex().Add(t.Host, t.Id)
	s.JsonDb.Hosts.Store(t.Id, t)
	s.JsonDb.hostClientIndex.add(t.Client.Id, t.Id)
	s.JsonDb.hostClientIndex.markReady()
	s.JsonDb.StoreHosts()
	return nil
}

func (s *DbUtils) UpdateHost(t *Host) error {
	if t == nil {
		return errors.New("host is nil")
	}
	var err error
	if t.Client, err = resolveStoredClientRef(s, t.Client); err != nil {
		return err
	}
	old, ok := loadHostEntry(&s.JsonDb.Hosts, t.Id)
	if !ok {
		return ErrHostNotFound
	}
	if t.ExpectedRevision > 0 {
		if old.Revision != t.ExpectedRevision {
			return ErrRevisionConflict
		}
	}
	normalizeHostRoutingFields(t)
	if s.IsHostExist(t) {
		return errors.New("host has exist")
	}
	t.ExpectedRevision = 0
	finalizeHostForStore(t)
	runtimeHostIndex().Remove(old.Host, t.Id)
	runtimeHostIndex().Add(t.Host, t.Id)
	s.JsonDb.Hosts.Store(t.Id, t)
	if old.Client != nil {
		s.JsonDb.hostClientIndex.remove(old.Client.Id, old.Id)
	}
	s.JsonDb.hostClientIndex.add(t.Client.Id, t.Id)
	s.JsonDb.hostClientIndex.markReady()
	s.JsonDb.StoreHosts()
	return nil
}

func (s *DbUtils) GetHost(start, length int, id int, search string) ([]*Host, int) {
	list := make([]*Host, 0)
	var cnt int
	originLength := length
	searchId := common.GetIntNoErrByStr(search)
	keys := GetMapKeys(&s.JsonDb.Hosts, false, "", "")
	for _, key := range keys {
		v, ok := loadHostEntry(&s.JsonDb.Hosts, key)
		if !ok {
			continue
		}
		verifyKey := ""
		clientID := 0
		if v.Client != nil {
			verifyKey = v.Client.VerifyKey
			clientID = v.Client.Id
		}
		if search != "" && v.Id != searchId && !common.ContainsFold(v.Host, search) && !common.ContainsFold(v.Remark, search) && !common.ContainsFold(verifyKey, search) {
			continue
		}
		if id == 0 || clientID == id {
			cnt++
			if start--; start < 0 {
				if originLength == 0 {
					list = append(list, v)
				} else if length--; length >= 0 {
					list = append(list, v)
				}
			}
		}
	}
	return list, cnt
}

func (s *DbUtils) GetHostById(id int) (h *Host, err error) {
	if h, ok := loadHostEntry(&s.JsonDb.Hosts, id); ok {
		return h, nil
	}
	err = ErrHostNotFound
	return
}

func normalizedHostLocation(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return "/"
	}
	return location
}

func normalizedHostScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http", "https":
		return strings.ToLower(strings.TrimSpace(scheme))
	default:
		return "all"
	}
}

func requestPathForHostLookup(r *http.Request) string {
	if r == nil {
		return "/"
	}
	requestPath := ""
	if r.URL != nil {
		requestPath = strings.TrimSpace(r.URL.Path)
		if requestPath != "" {
			return requestPath
		}
	}
	requestPath = strings.TrimSpace(r.RequestURI)
	if requestPath == "" {
		return "/"
	}
	if parsed, err := url.ParseRequestURI(requestPath); err == nil {
		if path := strings.TrimSpace(parsed.Path); path != "" {
			return path
		}
	}
	return requestPath
}

func requestSchemeForHostLookup(r *http.Request) string {
	if r == nil {
		return "http"
	}
	if r.URL != nil {
		if scheme := normalizedLookupRequestScheme(r.URL.Scheme); scheme != "" {
			return scheme
		}
	}
	if r.TLS != nil {
		return "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		if scheme := normalizedLookupRequestScheme(strings.Split(forwarded, ",")[0]); scheme != "" {
			return scheme
		}
	}
	return "http"
}

func normalizedLookupRequestScheme(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "http", "https":
		return strings.ToLower(strings.TrimSpace(scheme))
	default:
		return ""
	}
}

func hostMatchesPattern(pattern, host string) bool {
	pattern = common.GetIpByAddr(pattern)
	host = common.GetIpByAddr(host)
	if pattern == "" || host == "" {
		return false
	}
	if pattern == host {
		return true
	}
	return strings.HasPrefix(pattern, "*") && strings.HasSuffix(host, pattern[1:])
}

func hostHasReusableManualCert(host *Host) bool {
	if host == nil {
		return false
	}
	if normalizedHostScheme(host.Scheme) == "http" || host.HttpsJustProxy {
		return false
	}
	return strings.TrimSpace(host.CertFile) != "" && strings.TrimSpace(host.KeyFile) != ""
}

// SelectReusableCertHost picks the best matching host whose manual certificate
// can be reused for the provided domain. The current host can be excluded so a
// rule without its own cert can inherit from an existing wildcard/exact rule.
func SelectReusableCertHost(host string, candidates []*Host, excludeID int) *Host {
	host = common.GetIpByAddr(host)
	if host == "" {
		return nil
	}

	var bestMatch *Host
	var bestDomainLength int
	var bestLocationLength int
	for _, candidate := range candidates {
		if candidate == nil || candidate.Id == excludeID || !hostHasReusableManualCert(candidate) {
			continue
		}
		if !hostMatchesPattern(candidate.Host, host) {
			continue
		}

		curDomainLength := len(strings.TrimPrefix(candidate.Host, "*"))
		curLocationLength := len(normalizedHostLocation(candidate.Location))
		if bestMatch == nil {
			bestMatch = candidate
			bestDomainLength = curDomainLength
			bestLocationLength = curLocationLength
			continue
		}
		if curDomainLength > bestDomainLength {
			bestMatch = candidate
			bestDomainLength = curDomainLength
			bestLocationLength = curLocationLength
			continue
		}
		if curDomainLength == bestDomainLength {
			if curLocationLength < bestLocationLength {
				bestMatch = candidate
				bestDomainLength = curDomainLength
				bestLocationLength = curLocationLength
				continue
			}
			if curLocationLength == bestLocationLength {
				if bestMatch.IsClose && !candidate.IsClose {
					bestMatch = candidate
					bestDomainLength = curDomainLength
					bestLocationLength = curLocationLength
					continue
				}
				if candidate.Id < bestMatch.Id {
					bestMatch = candidate
					bestDomainLength = curDomainLength
					bestLocationLength = curLocationLength
				}
			}
		}
	}
	return bestMatch
}

// GetInfoByHost get key by host from x
func (s *DbUtils) GetInfoByHost(host string, r *http.Request) (h *Host, err error) {
	host = common.GetIpByAddr(host)
	hostLength := len(host)

	requestPath := requestPathForHostLookup(r)
	scheme := requestSchemeForHostLookup(r)

	ids := runtimeHostIndex().Lookup(host)
	if len(ids) == 0 {
		return nil, errors.New("the host could not be parsed")
	}

	var bestMatch *Host
	var bestDomainLength int
	var bestLocationLength int
	for _, id := range ids {
		v, ok := loadHostEntry(&s.JsonDb.Hosts, id)
		if !ok {
			continue
		}

		hostScheme := normalizedHostScheme(v.Scheme)
		if v.IsClose || (hostScheme != "all" && hostScheme != scheme) {
			continue
		}

		curDomainLength := len(strings.TrimPrefix(v.Host, "*"))
		if hostLength < curDomainLength {
			continue
		}

		equaled := v.Host == host
		matched := equaled || (strings.HasPrefix(v.Host, "*") && strings.HasSuffix(host, v.Host[1:]))
		if !matched {
			continue
		}

		location := v.Location
		if location == "" {
			location = "/"
		}

		if !strings.HasPrefix(requestPath, location) {
			continue
		}

		curLocationLength := len(location)
		if bestMatch == nil {
			bestMatch = v
			bestDomainLength = curDomainLength
			bestLocationLength = curLocationLength
			continue
		}
		if curLocationLength > bestLocationLength {
			bestMatch = v
			bestDomainLength = curDomainLength
			bestLocationLength = curLocationLength
			continue
		}
		if curLocationLength == bestLocationLength {
			if curDomainLength > bestDomainLength {
				bestMatch = v
				bestDomainLength = curDomainLength
				bestLocationLength = curLocationLength
				continue
			}
			if equaled {
				bestMatch = v
				bestDomainLength = curDomainLength
				bestLocationLength = curLocationLength
				continue
			}
		}
	}

	if bestMatch != nil {
		return bestMatch.SelectRuntimeRoute(), nil
	}
	return nil, errors.New("the host could not be parsed")
}

func (s *DbUtils) FindCertByHost(host string) (*Host, error) {
	if host == "" {
		return nil, errors.New("invalid Host")
	}

	host = common.GetIpByAddr(host)
	hostLength := len(host)

	ids := runtimeHostIndex().Lookup(host)
	if len(ids) == 0 {
		return nil, errors.New("the host could not be parsed")
	}

	var bestMatch *Host
	var bestDomainLength int
	for _, id := range ids {
		v, ok := loadHostEntry(&s.JsonDb.Hosts, id)
		if !ok {
			continue
		}

		if v.IsClose || normalizedHostScheme(v.Scheme) == "http" {
			continue
		}

		curDomainLength := len(strings.TrimPrefix(v.Host, "*"))
		if hostLength < curDomainLength {
			continue
		}

		equaled := v.Host == host
		matched := false
		location := v.Location == "/" || v.Location == ""
		if equaled {
			if location {
				bestMatch = v
				break
			}
			matched = true
		} else if strings.HasPrefix(v.Host, "*") && strings.HasSuffix(host, v.Host[1:]) {
			matched = true
		}
		if !matched {
			continue
		}

		if bestMatch == nil {
			bestMatch = v
			bestDomainLength = curDomainLength
			continue
		}
		if curDomainLength > bestDomainLength {
			bestMatch = v
			bestDomainLength = curDomainLength
			continue
		}
		if curDomainLength == bestDomainLength {
			if equaled && (len(v.Location) <= len(bestMatch.Location) || strings.HasPrefix(bestMatch.Host, "*")) {
				bestMatch = v
				bestDomainLength = curDomainLength
				continue
			}
			if len(v.Location) <= len(bestMatch.Location) && strings.HasPrefix(bestMatch.Host, "*") {
				bestMatch = v
				bestDomainLength = curDomainLength
				continue
			}
		}
	}
	if bestMatch != nil {
		return bestMatch, nil
	}
	return nil, errors.New("the host could not be parsed")
}

func (s *DbUtils) FindReusableCertHost(host string, excludeID int) (*Host, error) {
	if host == "" {
		return nil, errors.New("invalid Host")
	}

	host = common.GetIpByAddr(host)
	ids := runtimeHostIndex().Lookup(host)
	if len(ids) == 0 {
		return nil, errors.New("the host could not be parsed")
	}

	candidates := make([]*Host, 0, len(ids))
	for _, id := range ids {
		value, ok := loadHostEntry(&s.JsonDb.Hosts, id)
		if !ok {
			continue
		}
		candidates = append(candidates, value)
	}
	if bestMatch := SelectReusableCertHost(host, candidates, excludeID); bestMatch != nil {
		return bestMatch, nil
	}
	return nil, errors.New("the host could not be parsed")
}
