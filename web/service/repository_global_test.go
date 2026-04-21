package service

import (
	"testing"

	"github.com/djylb/nps/lib/file"
)

func TestDefaultRepositoryGetGlobalReturnsWorkingCopy(t *testing.T) {
	resetBackendTestDB(t)

	if err := file.GetDb().SaveGlobal(&file.Glob{EntryAclMode: file.AclBlacklist, EntryAclRules: "127.0.0.1"}); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}

	got := defaultRepository{}.GetGlobal()
	if got == nil {
		t.Fatal("GetGlobal() should return a global config")
	}
	stored := file.GetDb().GetGlobal()
	if got == stored {
		t.Fatal("GetGlobal() should return a cloned global config, not the live pointer")
	}

	got.EntryAclRules = "8.8.8.8"
	if stored.EntryAclMode != file.AclBlacklist || stored.EntryAclRules != "127.0.0.1" {
		t.Fatalf("stored global acl mutated = (%d, %q)", stored.EntryAclMode, stored.EntryAclRules)
	}
	if got.EntryAclMode != file.AclBlacklist || got.EntryAclRules != "8.8.8.8" {
		t.Fatalf("cloned global acl = (%d, %q), want mutated blacklist 8.8.8.8", got.EntryAclMode, got.EntryAclRules)
	}
}

func TestDefaultRepositoryGetGlobalPreservesNilCompatibility(t *testing.T) {
	resetBackendTestDB(t)
	file.GetDb().JsonDb.Global = nil

	repo := defaultRepository{}
	if got := repo.GetGlobal(); got != nil {
		t.Fatalf("GetGlobal() = %#v, want nil when global config is not initialized", got)
	}
}

func TestDefaultRepositorySaveGlobalClonesInput(t *testing.T) {
	resetBackendTestDB(t)

	input := &file.Glob{EntryAclMode: file.AclWhitelist, EntryAclRules: "10.0.0.0/8"}
	repo := defaultRepository{}
	if err := repo.SaveGlobal(input); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}

	input.EntryAclMode = file.AclBlacklist
	input.EntryAclRules = "8.8.8.8"
	stored := file.GetDb().GetGlobal()
	if stored.EntryAclMode != file.AclWhitelist || stored.EntryAclRules != "10.0.0.0/8" {
		t.Fatalf("stored global acl mutated with caller input = (%d, %q)", stored.EntryAclMode, stored.EntryAclRules)
	}
}
