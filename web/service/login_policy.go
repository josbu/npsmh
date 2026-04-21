package service

import (
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/policy"
	"github.com/djylb/nps/lib/servercfg"
)

type LoginPolicySettings struct {
	BanTime      int64
	IPBanTime    int64
	UserBanTime  int64
	MaxFailTimes int
	MaxLoginBody int64
	MaxSkew      int64
}

func (s LoginPolicySettings) LoginDelayMillis() int64 {
	return s.BanTime * 1000
}

type LoginBanRecord struct {
	Key           string `json:"key"`
	FailTimes     int    `json:"fail_times"`
	LastLoginTime string `json:"last_login_time"`
	IsBanned      bool   `json:"is_banned"`
	BanType       string `json:"ban_type"`
}

type LoginPolicyService interface {
	Settings() LoginPolicySettings
	AllowsIP(ip string) bool
	IsIPBanned(ip string) bool
	IsUserBanned(username string) bool
	RecordFailure(key string, explicit bool)
	RemoveBan(key string) bool
	RemoveAllBans()
	Clean(force bool)
	BanList() []LoginBanRecord
}

type GlobalService interface {
	Save(SaveGlobalInput) error
	Get() *file.Glob
	BanList() []LoginBanRecord
	Unban(key string) bool
	UnbanAll()
	CleanBans()
}

type DefaultGlobalService struct {
	LoginPolicy LoginPolicyService
	Repo        GlobalRepository
	Backend     Backend
}

type SaveGlobalInput struct {
	EntryACLMode  int
	EntryACLRules string
}

type DefaultLoginPolicy struct {
	ConfigProvider func() *servercfg.Snapshot
	records        sync.Map
	staticMu       sync.RWMutex
	staticSource   *policy.SourceIPPolicy
	staticMode     int
	staticRules    string
	staticGeoIP    string
}

var sharedLoginPolicy = NewDefaultLoginPolicy(servercfg.Current)

type loginFailureRecord struct {
	mu                sync.Mutex
	hasLoginFailTimes int
	lastLoginTime     time.Time
}

func NewDefaultLoginPolicy(provider func() *servercfg.Snapshot) *DefaultLoginPolicy {
	return &DefaultLoginPolicy{ConfigProvider: provider}
}

func SharedLoginPolicy() *DefaultLoginPolicy {
	return sharedLoginPolicy
}

func (s DefaultGlobalService) Save(input SaveGlobalInput) error {
	entryACLMode, entryACLRules := normalizeEntryACLInput(input.EntryACLMode, input.EntryACLRules)
	return s.repo().SaveGlobal(&file.Glob{
		EntryAclMode:  entryACLMode,
		EntryAclRules: entryACLRules,
	})
}

func (s DefaultGlobalService) Get() *file.Glob {
	return s.repo().GetGlobal()
}

func (s DefaultGlobalService) BanList() []LoginBanRecord {
	return s.loginPolicy().BanList()
}

func (s DefaultGlobalService) Unban(key string) bool {
	return s.loginPolicy().RemoveBan(key)
}

func (s DefaultGlobalService) UnbanAll() {
	s.loginPolicy().RemoveAllBans()
}

func (s DefaultGlobalService) CleanBans() {
	s.loginPolicy().Clean(true)
}

func (s DefaultGlobalService) loginPolicy() LoginPolicyService {
	if !isNilServiceValue(s.LoginPolicy) {
		return s.LoginPolicy
	}
	return SharedLoginPolicy()
}

func (s DefaultGlobalService) repo() GlobalRepository {
	if !isNilServiceValue(s.Repo) {
		return s.Repo
	}
	if !isNilServiceValue(s.Backend.Repository) {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}

func (s *DefaultLoginPolicy) Settings() LoginPolicySettings {
	cfg := s.config()
	return LoginPolicySettings{
		BanTime:      cfg.Security.LoginBanTime,
		IPBanTime:    cfg.Security.LoginIPBanTime,
		UserBanTime:  cfg.Security.LoginUserBanTime,
		MaxFailTimes: cfg.Security.LoginMaxFailTimes,
		MaxLoginBody: cfg.Security.LoginMaxBody,
		MaxSkew:      cfg.Security.LoginMaxSkew,
	}
}

func (s *DefaultLoginPolicy) AllowsIP(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return true
	}
	return s.sourcePolicy().AllowsAddr(ip)
}

func (s *DefaultLoginPolicy) IsIPBanned(ip string) bool {
	return s.isBanned(ip, s.Settings().IPBanTime)
}

func (s *DefaultLoginPolicy) IsUserBanned(username string) bool {
	return s.isBanned(username, s.Settings().UserBanTime)
}

func (s *DefaultLoginPolicy) RecordFailure(key string, explicit bool) {
	if !explicit || key == "" {
		return
	}
	now := time.Now()
	value, loaded := s.loadOrStoreRecord(key, &loginFailureRecord{hasLoginFailTimes: 1, lastLoginTime: now})
	if loaded {
		current := value
		current.mu.Lock()
		current.lastLoginTime = now
		current.hasLoginFailTimes++
		current.mu.Unlock()
	}
}

func (s *DefaultLoginPolicy) RemoveBan(key string) bool {
	if key == "" {
		return false
	}
	if _, ok := s.records.Load(key); ok {
		s.records.Delete(key)
		return true
	}
	return false
}

func (s *DefaultLoginPolicy) RemoveAllBans() {
	s.records.Range(func(key, _ interface{}) bool {
		s.records.Delete(key)
		return true
	})
}

func (s *DefaultLoginPolicy) Clean(force bool) {
	if rand.Intn(100) != 1 && !force {
		return
	}

	settings := s.Settings()
	now := time.Now()
	s.records.Range(func(key, value interface{}) bool {
		currentKey, ok := key.(string)
		if !ok {
			s.removeRecordEntryIfCurrent(key, value)
			return true
		}
		current, ok := value.(*loginFailureRecord)
		if !ok || current == nil {
			s.removeRecordEntryIfCurrent(currentKey, value)
			return true
		}

		ttl := settings.UserBanTime
		if net.ParseIP(currentKey) != nil {
			ttl = settings.IPBanTime
		}

		current.mu.Lock()
		last := current.lastLoginTime
		current.mu.Unlock()

		if now.Unix()-last.Unix() >= ttl {
			s.records.Delete(key)
		}
		return true
	})
}

func (s *DefaultLoginPolicy) BanList() []LoginBanRecord {
	settings := s.Settings()
	var list []LoginBanRecord
	now := time.Now()

	s.records.Range(func(key, value interface{}) bool {
		currentKey, ok := key.(string)
		if !ok {
			s.removeRecordEntryIfCurrent(key, value)
			return true
		}
		current, ok := value.(*loginFailureRecord)
		if !ok || current == nil {
			s.removeRecordEntryIfCurrent(currentKey, value)
			return true
		}

		banType := "username"
		ttl := settings.UserBanTime
		if net.ParseIP(currentKey) != nil {
			banType = "ip"
			ttl = settings.IPBanTime
		}

		current.mu.Lock()
		fail := current.hasLoginFailTimes
		last := current.lastLoginTime
		current.mu.Unlock()

		duration := now.Unix() - last.Unix()
		isBanned := duration < settings.BanTime || (fail >= settings.MaxFailTimes && duration < ttl)
		list = append(list, LoginBanRecord{
			Key:           currentKey,
			FailTimes:     fail,
			LastLoginTime: last.Format("2006-01-02 15:04:05"),
			IsBanned:      isBanned,
			BanType:       banType,
		})
		return true
	})
	return list
}

func (s *DefaultLoginPolicy) isBanned(key string, ttl int64) bool {
	if key == "" {
		return false
	}
	settings := s.Settings()
	if current, ok := s.loadRecord(key); ok {
		current.mu.Lock()
		defer current.mu.Unlock()

		duration := time.Now().Unix() - current.lastLoginTime.Unix()
		if duration < settings.BanTime {
			logs.Warn("%s request rate too high, login blocked", key)
			return true
		}
		if duration >= ttl {
			current.hasLoginFailTimes = 0
		}
		if current.hasLoginFailTimes >= settings.MaxFailTimes {
			logs.Warn("%s has reached maximum failed attempts, login blocked", key)
			return true
		}
	}
	return false
}

func (s *DefaultLoginPolicy) loadRecord(key string) (*loginFailureRecord, bool) {
	if s == nil || key == "" {
		return nil, false
	}
	value, ok := s.records.Load(key)
	if !ok {
		return nil, false
	}
	current, ok := value.(*loginFailureRecord)
	if !ok || current == nil {
		s.removeRecordEntryIfCurrent(key, value)
		return nil, false
	}
	return current, true
}

func (s *DefaultLoginPolicy) loadOrStoreRecord(key string, candidate *loginFailureRecord) (*loginFailureRecord, bool) {
	if s == nil || key == "" || candidate == nil {
		return nil, false
	}
	for {
		value, loaded := s.records.LoadOrStore(key, candidate)
		if !loaded {
			return candidate, false
		}
		current, ok := value.(*loginFailureRecord)
		if ok && current != nil {
			return current, true
		}
		s.removeRecordEntryIfCurrent(key, value)
	}
}

func (s *DefaultLoginPolicy) removeRecordEntryIfCurrent(key, value interface{}) bool {
	if s == nil || key == nil || value == nil {
		return false
	}
	return s.records.CompareAndDelete(key, value)
}

func (s *DefaultLoginPolicy) IsBannedForTTL(key string, ttl int64) bool {
	return s.isBanned(key, ttl)
}

func (s *DefaultLoginPolicy) config() *servercfg.Snapshot {
	if s == nil {
		return servercfg.Current()
	}
	return servercfg.ResolveProvider(s.ConfigProvider)
}

func (s *DefaultLoginPolicy) sourcePolicy() *policy.SourceIPPolicy {
	cfg := s.config()
	mode := 0
	rules := ""
	geoIPPath := ""
	if cfg != nil {
		mode = cfg.Security.LoginACLMode
		rules = cfg.Security.LoginACLRules
		geoIPPath = cfg.App.GeoIPPath
	}
	s.staticMu.RLock()
	if s.staticSource != nil && s.staticMode == mode && s.staticRules == rules && s.staticGeoIP == geoIPPath {
		compiled := s.staticSource
		s.staticMu.RUnlock()
		return compiled
	}
	s.staticMu.RUnlock()

	compiled := compileLoginSourcePolicy(cfg)

	s.staticMu.Lock()
	s.staticSource = compiled
	s.staticMode = mode
	s.staticRules = rules
	s.staticGeoIP = geoIPPath
	s.staticMu.Unlock()
	return compiled
}

func compileLoginSourcePolicy(cfg *servercfg.Snapshot) *policy.SourceIPPolicy {
	if cfg == nil {
		return policy.CompileSourceIPPolicy(policy.ModeOff, nil, policy.Options{})
	}
	return policy.CompileSourceIPPolicy(
		policy.NormalizeMode(cfg.Security.LoginACLMode),
		splitLoginACLRules(cfg.Security.LoginACLRules),
		policy.Options{GeoIPPath: cfg.App.GeoIPPath},
	)
}

func splitLoginACLRules(raw string) []string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	})
	if len(fields) == 0 {
		return nil
	}
	entries := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			entries = append(entries, field)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}
