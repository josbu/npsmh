package framework

import (
	"net/http/httptest"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func TestBuildSessionStoreUsesSecureMode(t *testing.T) {
	store, err := buildSessionStore(&servercfg.Snapshot{
		Security: servercfg.SecurityConfig{SecureMode: true},
	})
	if err != nil {
		t.Fatalf("buildSessionStore() error = %v", err)
	}
	if store.Options == nil || !store.Options.Secure {
		t.Fatalf("buildSessionStore() should enable secure cookies when secure_mode is true")
	}

	store, err = buildSessionStore(&servercfg.Snapshot{
		Security: servercfg.SecurityConfig{SecureMode: false},
	})
	if err != nil {
		t.Fatalf("buildSessionStore() error = %v", err)
	}
	if store.Options == nil {
		t.Fatalf("buildSessionStore() returned nil options")
	}
	if store.Options.Secure {
		t.Fatalf("buildSessionStore() should not force secure cookies when secure_mode is false")
	}
}

func TestBuildSessionStoreScopesCookiePathToCanonicalWebBaseURL(t *testing.T) {
	store, err := buildSessionStore(&servercfg.Snapshot{
		Web: servercfg.WebConfig{BaseURL: "/ops/platform/admin"},
	})
	if err != nil {
		t.Fatalf("buildSessionStore() error = %v", err)
	}
	if store.Options == nil {
		t.Fatalf("buildSessionStore() returned nil options")
	}
	if store.Options.Path != "/ops/platform/admin" {
		t.Fatalf("buildSessionStore() Path = %q, want /ops/platform/admin", store.Options.Path)
	}

	normalizedStore, err := buildSessionStore(&servercfg.Snapshot{
		Web: servercfg.WebConfig{BaseURL: "ops/platform/admin/"},
	})
	if err != nil {
		t.Fatalf("buildSessionStore() normalized error = %v", err)
	}
	if normalizedStore.Options == nil {
		t.Fatalf("buildSessionStore() normalized returned nil options")
	}
	if normalizedStore.Options.Path != "/ops/platform/admin" {
		t.Fatalf("buildSessionStore() normalized Path = %q, want /ops/platform/admin", normalizedStore.Options.Path)
	}

	rootStore, err := buildSessionStore(&servercfg.Snapshot{
		Web: servercfg.WebConfig{BaseURL: "/"},
	})
	if err != nil {
		t.Fatalf("buildSessionStore() root error = %v", err)
	}
	if rootStore.Options == nil {
		t.Fatalf("buildSessionStore() root returned nil options")
	}
	if rootStore.Options.Path != "/" {
		t.Fatalf("buildSessionStore() root Path = %q, want /", rootStore.Options.Path)
	}
}

func TestClearSessionCookieAlsoExpiresRootCookieWhenScoped(t *testing.T) {
	store, err := buildSessionStore(&servercfg.Snapshot{
		Web: servercfg.WebConfig{BaseURL: "/ops/platform/admin"},
	})
	if err != nil {
		t.Fatalf("buildSessionStore() error = %v", err)
	}
	req := httptest.NewRequest("GET", "https://example.com/ops/platform/admin/api/system/discovery", nil)
	resp := httptest.NewRecorder()
	if err := clearSessionCookie(store, resp, req); err != nil {
		t.Fatalf("clearSessionCookie() error = %v", err)
	}
	cookies := resp.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("clearSessionCookie() wrote %d cookies, want 2", len(cookies))
	}
	seen := map[string]bool{}
	for _, cookie := range cookies {
		if cookie.Name != sessionName {
			t.Fatalf("cookie name = %q, want %q", cookie.Name, sessionName)
		}
		if cookie.MaxAge != -1 {
			t.Fatalf("cookie MaxAge = %d, want -1", cookie.MaxAge)
		}
		seen[cookie.Path] = true
	}
	if !seen["/ops/platform/admin"] || !seen["/"] {
		t.Fatalf("clearSessionCookie() paths = %#v, want scoped path and root", seen)
	}
}
