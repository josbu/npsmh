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
	"github.com/gin-gonic/gin"
)

func TestManagementDiscoveryAndAuthContract(t *testing.T) {
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
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	discoveryResp := httptest.NewRecorder()
	discoveryReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	discoveryReq.Header.Set("X-Request-ID", "discovery-request-1")
	handler.ServeHTTP(discoveryResp, discoveryReq)
	if discoveryResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery status = %d, want 200", discoveryResp.Code)
	}
	if got := discoveryResp.Result().Header.Get("X-Request-ID"); got != "discovery-request-1" {
		t.Fatalf("GET /api/system/discovery should echo request id header, got %q", got)
	}
	if got := discoveryResp.Result().Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /api/system/discovery Cache-Control = %q, want no-store", got)
	}
	if got := discoveryResp.Result().Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("GET /api/system/discovery Pragma = %q, want no-cache", got)
	}
	discoveryBody := discoveryResp.Body.String()
	if !strings.Contains(discoveryBody, "\"routes\":{\"api_base\":\"/api\",\"health\":\"/api/system/health\",\"discovery\":\"/api/system/discovery\"") {
		t.Fatalf("discovery response missing canonical routes: %s", discoveryBody)
	}
	if !strings.Contains(discoveryBody, "\"session\":\"/api/auth/session\"") ||
		!strings.Contains(discoveryBody, "\"token\":\"/api/auth/token\"") ||
		!strings.Contains(discoveryBody, "\"register\":\"/api/auth/register\"") ||
		!strings.Contains(discoveryBody, "\"logout\":\"/api/auth/session/logout\"") ||
		!strings.Contains(discoveryBody, "\"access_ip_limit_register\":\"/api/access/ip-limit/actions/register\"") {
		t.Fatalf("discovery response missing auth routes: %s", discoveryBody)
	}
	if strings.Contains(discoveryBody, "\"captcha_new\":\"/captcha/new\"") {
		t.Fatalf("discovery response should not publish captcha routes when open_captcha is disabled: %s", discoveryBody)
	}
	if !strings.Contains(discoveryBody, "\"actions\":[]") {
		t.Fatalf("anonymous discovery should not expose management actions: %s", discoveryBody)
	}
	if strings.Contains(discoveryBody, "\"dashboard\":\"/api/system/dashboard\"") ||
		strings.Contains(discoveryBody, "\"status\":\"/api/system/status\"") ||
		strings.Contains(discoveryBody, "\"webhooks\":\"/api/webhooks\"") ||
		strings.Contains(discoveryBody, "\"realtime_subscriptions\":\"/api/realtime/subscriptions\"") ||
		strings.Contains(discoveryBody, "\"batch\":\"/api/batch\"") ||
		strings.Contains(discoveryBody, "\"ws\":\"/api/ws\"") {
		t.Fatalf("anonymous discovery should not expose protected routes: %s", discoveryBody)
	}
	for _, forbidden := range []string{
		"\"pages\":",
		"\"shell\":",
		"\"head_custom_code\":",
		"\"auth_key\":",
		"\"time\":",
		"\"cert\":",
		"\"login_challenge\":",
		"\"login_verify\":",
		"\"/app\"",
		"\"/auth/page\"",
		"\"/auth/render\"",
		"\"/login/index\"",
		"\"/login/out\"",
	} {
		if strings.Contains(discoveryBody, forbidden) {
			t.Fatalf("discovery response should not expose retired ui contract field %s: %s", forbidden, discoveryBody)
		}
	}

	sessionResp := httptest.NewRecorder()
	sessionReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessionReq.Header.Set("X-Request-ID", "session-request-1")
	handler.ServeHTTP(sessionResp, sessionReq)
	if sessionResp.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/session status = %d, want 200", sessionResp.Code)
	}
	if got := sessionResp.Result().Header.Get("X-Request-ID"); got != "session-request-1" {
		t.Fatalf("GET /api/auth/session should echo request id header, got %q", got)
	}
	if got := sessionResp.Result().Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /api/auth/session Cache-Control = %q, want no-store", got)
	}
	if got := sessionResp.Result().Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("GET /api/auth/session Pragma = %q, want no-cache", got)
	}
	sessionBody := sessionResp.Body.String()
	if !strings.Contains(sessionBody, "\"data\":{\"session\":") || !strings.Contains(sessionBody, "\"authenticated\":false") {
		t.Fatalf("GET /api/auth/session should return session payload, got %s", sessionBody)
	}
	if !strings.Contains(sessionBody, "\"meta\":{\"request_id\":\"session-request-1\"") {
		t.Fatalf("GET /api/auth/session should return management meta, got %s", sessionBody)
	}

	healthResp := httptest.NewRecorder()
	healthReq := httptest.NewRequest(http.MethodGet, "/api/system/health", nil)
	healthReq.Header.Set("X-Request-ID", "health-request-1")
	handler.ServeHTTP(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/health status = %d, want 200", healthResp.Code)
	}
	if got := healthResp.Result().Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /api/system/health Cache-Control = %q, want no-store", got)
	}
	if got := healthResp.Result().Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("GET /api/system/health Pragma = %q, want no-cache", got)
	}
	healthBody := healthResp.Body.String()
	if !strings.Contains(healthBody, "\"data\":{\"ok\":true") || !strings.Contains(healthBody, "\"boot_id\":\"") {
		t.Fatalf("GET /api/system/health should return health payload, got %s", healthBody)
	}
	if !strings.Contains(healthBody, "\"meta\":{\"request_id\":\"health-request-1\"") {
		t.Fatalf("GET /api/system/health should return management meta, got %s", healthBody)
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/app"},
		{method: http.MethodGet, path: "/app/bootstrap"},
		{method: http.MethodGet, path: "/login/index"},
		{method: http.MethodGet, path: "/auth/page/login/index"},
		{method: http.MethodGet, path: "/auth/render/login/index"},
		{method: http.MethodPost, path: "/client/list"},
		{method: http.MethodPost, path: "/user/list"},
		{method: http.MethodPost, path: "/index/gettunnel"},
		{method: http.MethodPost, path: "/global/banlist"},
	} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, request.path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", request.method, request.path, resp.Code)
		}
	}
}

func TestManagementDiscoveryPublishesCaptchaRoutesWhenEnabled(t *testing.T) {
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
		"allow_user_register=false",
		"open_captcha=true",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	discoveryResp := httptest.NewRecorder()
	discoveryReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	handler.ServeHTTP(discoveryResp, discoveryReq)
	if discoveryResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery status = %d, want 200", discoveryResp.Code)
	}
	discoveryBody := discoveryResp.Body.String()
	if !strings.Contains(discoveryBody, "\"captcha_new\":\"/captcha/new\"") {
		t.Fatalf("discovery response missing captcha routes when open_captcha is enabled: %s", discoveryBody)
	}
}
