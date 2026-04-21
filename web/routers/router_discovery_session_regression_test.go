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
	webservice "github.com/djylb/nps/web/service"
)

func TestManagementDiscoveryHidesPlatformStatusForLocalScopes(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"allow_user_login=true",
		"allow_user_vkey_login=true",
		"platform_ids=master-a",
		"platform_tokens=secret-a",
		"platform_control_scopes=account",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	handler := Init()

	userCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{101},
		Roles:         []string{webservice.RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize())
	userResp := httptest.NewRecorder()
	userReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	for _, cookie := range userCookies {
		userReq.AddCookie(cookie)
	}
	handler.ServeHTTP(userResp, userReq)
	if userResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery as user status = %d, want 200", userResp.Code)
	}
	if strings.Contains(userResp.Body.String(), "\"management_platforms\":[") {
		t.Fatalf("user discovery should not expose management platform status, got %s", userResp.Body.String())
	}

	clientCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "client",
		Provider:      "test",
		SubjectID:     "client:vkey:101",
		Username:      "vkey-client-101",
		ClientIDs:     []int{101},
		Roles:         []string{webservice.RoleClient},
		Attributes: map[string]string{
			"client_id":  "101",
			"login_mode": "client_vkey",
		},
	}).Normalize())
	clientResp := httptest.NewRecorder()
	clientReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	for _, cookie := range clientCookies {
		clientReq.AddCookie(cookie)
	}
	handler.ServeHTTP(clientResp, clientReq)
	if clientResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery as client status = %d, want 200", clientResp.Code)
	}
	if strings.Contains(clientResp.Body.String(), "\"management_platforms\":[") {
		t.Fatalf("client discovery should not expose management platform status, got %s", clientResp.Body.String())
	}
}

func TestSessionLogoutClearsResponseSessionFields(t *testing.T) {
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
	identity := (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{101},
		Roles:         []string{webservice.RoleUser},
		Attributes: map[string]string{
			"user_id":    "7",
			"login_mode": "password",
		},
	}).Normalize()
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), identity)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session/logout", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/session/logout status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if strings.Contains(body, "\"authenticated\":true") ||
		strings.Contains(body, "\"username\":\"operator\"") ||
		strings.Contains(body, "\"client_ids\":[101]") ||
		strings.Contains(body, "\"is_admin\":true") {
		t.Fatalf("logout response should not retain previous session fields, got %s", body)
	}
}
