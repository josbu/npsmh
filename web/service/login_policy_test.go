package service

import (
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func TestDefaultLoginPolicyUsesConfigProvider(t *testing.T) {
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Security: servercfg.SecurityConfig{
				LoginBanTime:      7,
				LoginIPBanTime:    11,
				LoginUserBanTime:  13,
				LoginMaxFailTimes: 5,
				LoginMaxBody:      2048,
				LoginMaxSkew:      9000,
			},
		}
	})

	settings := policy.Settings()
	if settings.BanTime != 7 || settings.IPBanTime != 11 || settings.UserBanTime != 13 {
		t.Fatalf("Settings() returned unexpected ban settings: %+v", settings)
	}
	if settings.MaxFailTimes != 5 || settings.MaxLoginBody != 2048 || settings.MaxSkew != 9000 {
		t.Fatalf("Settings() returned unexpected limits: %+v", settings)
	}
}

func TestDefaultLoginPolicyBanLifecycle(t *testing.T) {
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Security: servercfg.SecurityConfig{
				LoginBanTime:      60,
				LoginIPBanTime:    60,
				LoginUserBanTime:  60,
				LoginMaxFailTimes: 3,
			},
		}
	})

	policy.RecordFailure("127.0.0.1", true)
	if !policy.IsIPBanned("127.0.0.1") {
		t.Fatal("IsIPBanned() = false, want true after explicit failure")
	}

	records := policy.BanList()
	if len(records) != 1 || records[0].BanType != "ip" || !records[0].IsBanned {
		t.Fatalf("BanList() = %+v, want one banned ip record", records)
	}

	if !policy.RemoveBan("127.0.0.1") {
		t.Fatal("RemoveBan() = false, want true")
	}
	if policy.IsIPBanned("127.0.0.1") {
		t.Fatal("IsIPBanned() = true after RemoveBan(), want false")
	}
}

func TestDefaultLoginPolicyAllowsIPWithStaticACL(t *testing.T) {
	current := &servercfg.Snapshot{
		Security: servercfg.SecurityConfig{
			LoginACLMode:  1,
			LoginACLRules: "127.0.0.1,10.0.0.0/8",
		},
	}
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return current
	})

	if !policy.AllowsIP("127.0.0.1") {
		t.Fatal("AllowsIP() = false, want true for exact match")
	}
	if !policy.AllowsIP("10.1.2.3") {
		t.Fatal("AllowsIP() = false, want true for cidr match")
	}
	if policy.AllowsIP("192.168.1.1") {
		t.Fatal("AllowsIP() = true, want false for whitelist miss")
	}

	current = &servercfg.Snapshot{
		Security: servercfg.SecurityConfig{
			LoginACLMode:  2,
			LoginACLRules: "::1/128",
		},
	}

	if policy.AllowsIP("::1") {
		t.Fatal("AllowsIP() = true, want false for blacklist hit")
	}
	if !policy.AllowsIP("2001:db8::1") {
		t.Fatal("AllowsIP() = false, want true for blacklist miss")
	}
}

func TestDefaultLoginPolicyRecordFailureReplacesInvalidEntry(t *testing.T) {
	policy := NewDefaultLoginPolicy(servercfg.Current)
	policy.records.Store("alice", "bad-record")

	policy.RecordFailure("alice", true)

	current, ok := policy.loadRecord("alice")
	if !ok || current == nil {
		t.Fatal("RecordFailure should replace invalid record entry")
	}
	current.mu.Lock()
	failTimes := current.hasLoginFailTimes
	current.mu.Unlock()
	if failTimes != 1 {
		t.Fatalf("failTimes = %d, want 1 after replacement", failTimes)
	}
}

func TestDefaultLoginPolicyCleanAndBanListDropInvalidEntries(t *testing.T) {
	policy := NewDefaultLoginPolicy(func() *servercfg.Snapshot {
		return &servercfg.Snapshot{
			Security: servercfg.SecurityConfig{
				LoginBanTime:      60,
				LoginIPBanTime:    60,
				LoginUserBanTime:  60,
				LoginMaxFailTimes: 3,
			},
		}
	})
	valid := &loginFailureRecord{hasLoginFailTimes: 2, lastLoginTime: time.Now()}
	policy.records.Store("bob", valid)
	policy.records.Store("bad-user", "bad-record")
	policy.records.Store(12345, "bad-key")

	policy.Clean(true)
	records := policy.BanList()

	if _, ok := policy.records.Load("bad-user"); ok {
		t.Fatal("Clean should remove invalid record value")
	}
	if _, ok := policy.records.Load(12345); ok {
		t.Fatal("Clean should remove invalid record key")
	}
	if len(records) != 1 || records[0].Key != "bob" {
		t.Fatalf("BanList() = %+v, want only the valid bob record", records)
	}
}

func TestDefaultGlobalServiceSaveSupportsWhitelist(t *testing.T) {
	resetBackendTestDB(t)

	service := DefaultGlobalService{Backend: Backend{Repository: defaultRepository{}}}
	if err := service.Save(SaveGlobalInput{
		EntryACLMode:  file.AclWhitelist,
		EntryACLRules: " 10.0.0.0/8 \r\n 192.168.* ",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stored := file.GetDb().GetGlobal()
	if stored == nil {
		t.Fatal("stored global config should not be nil")
	}
	if stored.EntryAclMode != file.AclWhitelist || stored.EntryAclRules != "10.0.0.0/8\n192.168.*" {
		t.Fatalf("stored global acl = (%d, %q), want whitelist normalized rules", stored.EntryAclMode, stored.EntryAclRules)
	}
	if legacy := stored.LegacyBlackIPImport(); len(legacy) != 0 {
		t.Fatalf("global legacy blacklist should stay cleared in whitelist mode, got %v", legacy)
	}
	if !stored.AllowsSourceAddr("10.1.2.3:80") {
		t.Fatal("whitelist should allow matching source")
	}
	if stored.AllowsSourceAddr("8.8.8.8:53") {
		t.Fatal("whitelist should deny non-matching source")
	}
}

func TestDefaultGlobalServiceSaveUsesFormalBlacklistInput(t *testing.T) {
	resetBackendTestDB(t)

	service := DefaultGlobalService{Backend: Backend{Repository: defaultRepository{}}}
	if err := service.Save(SaveGlobalInput{
		EntryACLMode:  file.AclBlacklist,
		EntryACLRules: " 10.0.0.1 \r\n 10.0.0.2 \n",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stored := file.GetDb().GetGlobal()
	if stored == nil {
		t.Fatal("stored global config should not be nil")
	}
	if stored.EntryAclMode != file.AclBlacklist || stored.EntryAclRules != "10.0.0.1\n10.0.0.2" {
		t.Fatalf("stored global acl = (%d, %q), want blacklist normalized rules", stored.EntryAclMode, stored.EntryAclRules)
	}
	if legacy := stored.LegacyBlackIPImport(); len(legacy) != 0 {
		t.Fatalf("global legacy blacklist should stay cleared in blacklist mode, got %v", legacy)
	}
	if stored.AllowsSourceAddr("10.0.0.1:443") {
		t.Fatal("blacklist should deny matching source")
	}
	if !stored.AllowsSourceAddr("8.8.8.8:53") {
		t.Fatal("blacklist should allow non-matching source")
	}
}
