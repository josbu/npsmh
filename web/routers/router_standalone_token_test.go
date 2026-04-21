package routers

import (
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
)

func TestStandaloneTokenFlowSupportsDiscoveryManagementAPIAndCORS(t *testing.T) {
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
		"web_base_url=/base",
		"web_standalone_allowed_origins=https://console.example.com",
		"web_standalone_allow_credentials=true",
		"web_standalone_token_secret=standalone-secret",
		"web_standalone_token_ttl_seconds=600",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	preflightReq := httptest.NewRequest(http.MethodOptions, "/base/api/auth/token", nil)
	preflightReq.Header.Set("Origin", "https://console.example.com")
	preflightReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflightReq.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	preflightResp := httptest.NewRecorder()
	handler.ServeHTTP(preflightResp, preflightReq)
	if preflightResp.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /base/api/auth/token status = %d, want 204 body=%s", preflightResp.Code, preflightResp.Body.String())
	}
	if got := preflightResp.Result().Header.Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("OPTIONS /base/api/auth/token allow origin = %q, want https://console.example.com", got)
	}
	if got := preflightResp.Result().Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("OPTIONS /base/api/auth/token allow credentials = %q, want true", got)
	}

	sessionPreflightReq := httptest.NewRequest(http.MethodOptions, "/base/api/auth/session", nil)
	sessionPreflightReq.Header.Set("Origin", "https://console.example.com")
	sessionPreflightReq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	sessionPreflightResp := httptest.NewRecorder()
	handler.ServeHTTP(sessionPreflightResp, sessionPreflightReq)
	if sessionPreflightResp.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /base/api/auth/session status = %d, want 204 body=%s", sessionPreflightResp.Code, sessionPreflightResp.Body.String())
	}
	if got := sessionPreflightResp.Result().Header.Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("OPTIONS /base/api/auth/session allow origin = %q, want https://console.example.com", got)
	}

	tokenReq := httptest.NewRequest(http.MethodPost, "/base/api/auth/token", strings.NewReader(`{"username":"admin","password":"secret"}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenReq.Header.Set("Origin", "https://console.example.com")
	tokenResp := httptest.NewRecorder()
	handler.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("POST /base/api/auth/token status = %d, want 200 body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	if got := tokenResp.Result().Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("POST /base/api/auth/token Cache-Control = %q, want no-store", got)
	}
	if got := tokenResp.Result().Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("POST /base/api/auth/token Pragma = %q, want no-cache", got)
	}
	if got := tokenResp.Result().Header.Get("Access-Control-Allow-Origin"); got != "https://console.example.com" {
		t.Fatalf("POST /base/api/auth/token allow origin = %q, want https://console.example.com", got)
	}
	if got := tokenResp.Result().Header.Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("POST /base/api/auth/token should not write session cookie, got %v", got)
	}
	var tokenPayload webapi.ManagementAuthTokenPayload
	decodeManagementData(t, tokenResp.Body.Bytes(), &tokenPayload)
	if tokenPayload.AccessToken == "" || tokenPayload.TokenType != "Bearer" || tokenPayload.ExpiresIn <= 0 {
		t.Fatalf("token payload = %+v", tokenPayload)
	}

	discoveryReq := httptest.NewRequest(http.MethodGet, "/base/api/system/discovery", nil)
	discoveryReq.Header.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	discoveryReq.Header.Set("Origin", "https://console.example.com")
	discoveryResp := httptest.NewRecorder()
	handler.ServeHTTP(discoveryResp, discoveryReq)
	if discoveryResp.Code != http.StatusOK {
		t.Fatalf("GET /base/api/system/discovery with standalone token status = %d, want 200 body=%s", discoveryResp.Code, discoveryResp.Body.String())
	}
	if got := discoveryResp.Result().Header.Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("GET /base/api/system/discovery with standalone token should not write session cookie, got %v", got)
	}
	discoveryBody := discoveryResp.Body.String()
	if !strings.Contains(discoveryBody, "\"authenticated\":true") ||
		!strings.Contains(discoveryBody, "\"username\":\"admin\"") ||
		!strings.Contains(discoveryBody, "\"api_base\":\"/base/api\"") {
		t.Fatalf("GET /base/api/system/discovery with standalone token body = %s", discoveryBody)
	}

	basicReq := httptest.NewRequest(http.MethodGet, "/base/api/system/discovery", nil)
	basicReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	basicResp := httptest.NewRecorder()
	handler.ServeHTTP(basicResp, basicReq)
	if basicResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /base/api/system/discovery with basic auth status = %d, want 401 body=%s", basicResp.Code, basicResp.Body.String())
	}
	if got := basicResp.Result().Header.Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("GET /base/api/system/discovery with basic auth should not write session cookie, got %v", got)
	}
	if body := basicResp.Body.String(); !strings.Contains(body, `"code":"unsupported_auth_scheme"`) {
		t.Fatalf("GET /base/api/system/discovery with basic auth body = %s", body)
	}

	nodePreflightReq := httptest.NewRequest(http.MethodOptions, "/base/api/system/status", nil)
	nodePreflightReq.Header.Set("Origin", "https://console.example.com")
	nodePreflightReq.Header.Set("Access-Control-Request-Method", http.MethodGet)
	nodePreflightResp := httptest.NewRecorder()
	handler.ServeHTTP(nodePreflightResp, nodePreflightReq)
	if nodePreflightResp.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /base/api/system/status status = %d, want 204 body=%s", nodePreflightResp.Code, nodePreflightResp.Body.String())
	}

	nodeReq := httptest.NewRequest(http.MethodGet, "/base/api/system/status", nil)
	nodeReq.Header.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	nodeResp := httptest.NewRecorder()
	handler.ServeHTTP(nodeResp, nodeReq)
	if nodeResp.Code != http.StatusOK {
		t.Fatalf("GET /base/api/system/status with standalone token status = %d, want 200 body=%s", nodeResp.Code, nodeResp.Body.String())
	}
	if body := nodeResp.Body.String(); !strings.Contains(body, "\"api_base\":\"/base/api\"") {
		t.Fatalf("GET /base/api/system/status with standalone token body = %s", body)
	}
}

func TestStandaloneTokenInvalidatesDisabledUserOnNextHTTPRequest(t *testing.T) {
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
		"web_standalone_token_secret=standalone-secret",
		"web_standalone_token_ttl_seconds=600",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	user := createTestUser(t, 2401, "token-disabled-user", "tenant-secret")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(`{"username":"token-disabled-user","password":"tenant-secret"}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp := httptest.NewRecorder()
	handler.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("POST /api/auth/token status = %d, want 200 body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenPayload webapi.ManagementAuthTokenPayload
	decodeManagementData(t, tokenResp.Body.Bytes(), &tokenPayload)
	if tokenPayload.AccessToken == "" {
		t.Fatalf("token payload = %+v, want issued token", tokenPayload)
	}

	savedUser, err := file.GetDb().GetUser(user.Id)
	if err != nil {
		t.Fatalf("GetUser(%d) error = %v", user.Id, err)
	}
	savedUser.Status = 0
	if err := file.GetDb().UpdateUser(savedUser); err != nil {
		t.Fatalf("UpdateUser(%d) error = %v", user.Id, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	req.Header.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/system/discovery with disabled user token status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("GET /api/system/discovery with disabled user token body = %s", body)
	}
}
