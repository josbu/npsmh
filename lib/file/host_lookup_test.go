package file

import (
	"net/http/httptest"
	"testing"
)

func TestGetInfoByHostDefaultsEmptyServerRequestSchemeToHTTP(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	client := &Client{Id: 11, VerifyKey: "demo"}
	if err := db.NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	host := &Host{
		Id:       21,
		Host:     "demo.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Flow:     &Flow{},
		Target:   &Target{TargetStr: "127.0.0.1:80"},
	}
	if err := db.NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	req := httptest.NewRequest("GET", "http://demo.example.com/api", nil)
	req.URL.Scheme = ""

	got, err := db.GetInfoByHost("demo.example.com", req)
	if err != nil {
		t.Fatalf("GetInfoByHost() error = %v", err)
	}
	if got == nil || got.Id != host.Id {
		t.Fatalf("GetInfoByHost() = %+v, want host id %d", got, host.Id)
	}
}

func TestIsHostExistDoesNotMutateStoredRouteFields(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()
	stored := &Host{Id: 31, Host: "exists.example.com", Location: "", Scheme: "invalid"}
	db.JsonDb.Hosts.Store(stored.Id, stored)

	if !db.IsHostExist(&Host{Id: 99, Host: "exists.example.com", Location: "", Scheme: "http"}) {
		t.Fatal("IsHostExist() = false, want duplicate route detection")
	}
	if stored.Location != "" {
		t.Fatalf("stored.Location = %q, want unchanged blank value", stored.Location)
	}
	if stored.Scheme != "invalid" {
		t.Fatalf("stored.Scheme = %q, want unchanged invalid value", stored.Scheme)
	}
}

func TestSelectReusableCertHostPrefersMostSpecificDomain(t *testing.T) {
	candidates := []*Host{
		{Id: 1, Host: "*.example.com", Scheme: "all", CertFile: "wildcard-cert", KeyFile: "wildcard-key"},
		{Id: 2, Host: "*.svc.example.com", Scheme: "all", CertFile: "svc-cert", KeyFile: "svc-key"},
	}

	match := SelectReusableCertHost("api.svc.example.com", candidates, 0)
	if match == nil || match.Id != 2 {
		t.Fatalf("SelectReusableCertHost() picked %+v, want host id 2", match)
	}
}

func TestSelectReusableCertHostPrefersShorterLocationForSameDomain(t *testing.T) {
	candidates := []*Host{
		{Id: 1, Host: "*.example.com", Location: "/deep/path", Scheme: "all", CertFile: "deep-cert", KeyFile: "deep-key"},
		{Id: 2, Host: "*.example.com", Location: "/", Scheme: "all", CertFile: "root-cert", KeyFile: "root-key"},
	}

	match := SelectReusableCertHost("api.example.com", candidates, 0)
	if match == nil || match.Id != 2 {
		t.Fatalf("SelectReusableCertHost() picked %+v, want host id 2", match)
	}
}

func TestSelectReusableCertHostSkipsIneligibleCandidates(t *testing.T) {
	candidates := []*Host{
		{Id: 1, Host: "*.example.com", Scheme: "http", CertFile: "http-cert", KeyFile: "http-key"},
		{Id: 2, Host: "*.example.com", Scheme: "all", HttpsJustProxy: true, CertFile: "pass-cert", KeyFile: "pass-key"},
		{Id: 3, Host: "*.example.com", Scheme: "all", CertFile: "", KeyFile: ""},
		{Id: 4, Host: "*.example.com", Scheme: "all", CertFile: "reuse-cert", KeyFile: "reuse-key"},
	}

	match := SelectReusableCertHost("demo.example.com", candidates, 4)
	if match != nil {
		t.Fatalf("SelectReusableCertHost() = %+v, want nil when only excluded/invalid candidates remain", match)
	}

	match = SelectReusableCertHost("demo.example.com", candidates, 0)
	if match == nil || match.Id != 4 {
		t.Fatalf("SelectReusableCertHost() picked %+v, want host id 4", match)
	}
}

func TestSelectReusableCertHostAllowsDisabledCertificateHolder(t *testing.T) {
	candidates := []*Host{
		{Id: 9, Host: "*.example.com", Scheme: "all", IsClose: true, CertFile: "shared-cert", KeyFile: "shared-key"},
	}

	match := SelectReusableCertHost("demo.example.com", candidates, 0)
	if match == nil || match.Id != 9 {
		t.Fatalf("SelectReusableCertHost() picked %+v, want disabled host id 9", match)
	}
}

func TestHostLookupFunctionsDropInvalidEntries(t *testing.T) {
	resetMigrationTestDB(t)

	db := GetDb()

	CurrentHostIndex().Add("bad.example.com", 71)
	db.JsonDb.Hosts.Store(71, "invalid")
	req := httptest.NewRequest("GET", "http://bad.example.com/", nil)
	if _, err := db.GetInfoByHost("bad.example.com", req); err == nil {
		t.Fatal("GetInfoByHost(bad.example.com) error = nil, want invalid host lookup rejection")
	}
	if _, ok := db.JsonDb.Hosts.Load(71); ok {
		t.Fatal("GetInfoByHost() should drop invalid host entry")
	}

	CurrentHostIndex().Add("badcert.example.com", 72)
	db.JsonDb.Hosts.Store(72, "invalid")
	if _, err := db.FindCertByHost("badcert.example.com"); err == nil {
		t.Fatal("FindCertByHost(badcert.example.com) error = nil, want invalid host lookup rejection")
	}
	if _, ok := db.JsonDb.Hosts.Load(72); ok {
		t.Fatal("FindCertByHost() should drop invalid host entry")
	}

	CurrentHostIndex().Add("badreuse.example.com", 73)
	db.JsonDb.Hosts.Store(73, "invalid")
	if _, err := db.FindReusableCertHost("badreuse.example.com", 0); err == nil {
		t.Fatal("FindReusableCertHost(badreuse.example.com) error = nil, want invalid host lookup rejection")
	}
	if _, ok := db.JsonDb.Hosts.Load(73); ok {
		t.Fatal("FindReusableCertHost() should drop invalid host entry")
	}
}
