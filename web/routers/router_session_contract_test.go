package routers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gorilla/sessions"
)

func TestLegacySessionFieldsDoNotAuthenticateManagementSession(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	handler := Init()
	cookies := legacySessionOnlyCookies(t, servercfg.Current(), map[string]interface{}{
		"auth":     true,
		"isAdmin":  true,
		"username": "admin",
		"clientId": 7,
	})

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/session with legacy-only session cookie status = %d, want 200", resp.Code)
	}
	body := resp.Body.String()
	if strings.Contains(body, "\"authenticated\":true") || strings.Contains(body, "\"is_admin\":true") {
		t.Fatalf("legacy-only session cookie should not authenticate management session, got %s", body)
	}
}

func legacySessionOnlyCookies(t *testing.T, cfg *servercfg.Snapshot, values map[string]interface{}) []*http.Cookie {
	t.Helper()
	authKey, encKey := deriveTestSessionKeys(cfg)
	store := sessions.NewCookieStore(authKey, encKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	session, err := store.Get(req, "nps_session")
	if err != nil {
		t.Fatalf("CookieStore.Get() error = %v", err)
	}
	for key, value := range values {
		session.Values[key] = value
	}
	if err := session.Save(req, resp); err != nil {
		t.Fatalf("session.Save() error = %v", err)
	}
	return resp.Result().Cookies()
}
