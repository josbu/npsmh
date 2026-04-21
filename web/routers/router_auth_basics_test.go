package routers

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestWebBaseURLPrefix(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=/nps",
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	for _, path := range []string{"/nps/api/system/discovery", "/nps/api/auth/session"} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.Code)
		}
	}

	for _, path := range []string{"/api/system/discovery", "/api/auth/session"} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404 when web_base_url is /nps", path, resp.Code)
		}
	}

	discoveryResp := httptest.NewRecorder()
	discoveryReq := httptest.NewRequest(http.MethodGet, "/nps/api/system/discovery", nil)
	handler.ServeHTTP(discoveryResp, discoveryReq)
	body := discoveryResp.Body.String()
	if !strings.Contains(body, "\"base\":\"/nps\"") || !strings.Contains(body, "\"api_base\":\"/nps/api\"") {
		t.Fatalf("discovery response missing prefixed routes: %s", body)
	}

	baseResp := httptest.NewRecorder()
	baseReq := httptest.NewRequest(http.MethodGet, "/nps", nil)
	handler.ServeHTTP(baseResp, baseReq)
	if baseResp.Code != http.StatusNotFound {
		t.Fatalf("GET /nps status = %d, want 404", baseResp.Code)
	}

	adminCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "test",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
	}).Normalize())
	adminResp := httptest.NewRecorder()
	adminReq := httptest.NewRequest(http.MethodGet, "/nps/api/system/discovery", nil)
	for _, cookie := range adminCookies {
		adminReq.AddCookie(cookie)
	}
	handler.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusOK {
		t.Fatalf("GET /nps/api/system/discovery as admin status = %d, want 200", adminResp.Code)
	}
	if body := adminResp.Body.String(); !strings.Contains(body, "\"authenticated\":true") || !strings.Contains(body, "\"is_admin\":true") {
		t.Fatalf("discovery should expose authenticated admin session, got %s", body)
	}
}

func TestWebBaseURLPrefixAutoNormalizesConfigValue(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=nps/",
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := servercfg.Current().Web.BaseURL; got != "/nps" {
		t.Fatalf("Current().Web.BaseURL = %q, want /nps", got)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	for _, path := range []string{"/nps/api/system/discovery", "/nps/api/auth/session"} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.Code)
		}
	}

	for _, path := range []string{"/api/system/discovery", "/api/auth/session", "/nps//api/system/discovery"} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404 when normalized web_base_url is /nps", path, resp.Code)
		}
	}

	discoveryResp := httptest.NewRecorder()
	discoveryReq := httptest.NewRequest(http.MethodGet, "/nps/api/system/discovery", nil)
	handler.ServeHTTP(discoveryResp, discoveryReq)
	body := discoveryResp.Body.String()
	if !strings.Contains(body, "\"base\":\"/nps\"") || !strings.Contains(body, "\"api_base\":\"/nps/api\"") {
		t.Fatalf("discovery response missing normalized prefixed routes: %s", body)
	}
}

func TestWebBaseURLMultiLevelPrefix(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=ops/platform/admin/",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := servercfg.Current().Web.BaseURL; got != "/ops/platform/admin" {
		t.Fatalf("Current().Web.BaseURL = %q, want /ops/platform/admin", got)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	for _, path := range []string{
		"/ops/platform/admin/api/system/discovery",
		"/ops/platform/admin/api/auth/session",
	} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.Code)
		}
	}

	for _, path := range []string{
		"/api/system/discovery",
		"/platform/admin/api/system/discovery",
		"/ops/platform/api/system/discovery",
	} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404 when web_base_url is /ops/platform/admin", path, resp.Code)
		}
	}

	discoveryResp := httptest.NewRecorder()
	discoveryReq := httptest.NewRequest(http.MethodGet, "/ops/platform/admin/api/system/discovery", nil)
	handler.ServeHTTP(discoveryResp, discoveryReq)
	body := discoveryResp.Body.String()
	if !strings.Contains(body, "\"base\":\"/ops/platform/admin\"") ||
		!strings.Contains(body, "\"api_base\":\"/ops/platform/admin/api\"") {
		t.Fatalf("discovery response missing multi-level prefixed routes: %s", body)
	}
}

func TestWebBaseURLPrefixWorksWithRawSnapshotValue(t *testing.T) {
	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := buildEngine(NewState(&servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username: "admin",
			Password: "secret",
			BaseURL:  "ops/platform/admin/",
		},
	}))

	for _, path := range []string{
		"/ops/platform/admin/api/system/discovery",
		"/ops/platform/admin/api/auth/session",
		"/ops/platform/admin/captcha/new",
	} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, resp.Code)
		}
	}

	discoveryResp := httptest.NewRecorder()
	discoveryReq := httptest.NewRequest(http.MethodGet, "/ops/platform/admin/api/system/discovery", nil)
	handler.ServeHTTP(discoveryResp, discoveryReq)
	body := discoveryResp.Body.String()
	if !strings.Contains(body, "\"base\":\"/ops/platform/admin\"") ||
		!strings.Contains(body, "\"api_base\":\"/ops/platform/admin/api\"") {
		t.Fatalf("raw snapshot discovery response missing canonical prefixed routes: %s", body)
	}
}

func TestInvalidSessionCookieFallsBackToFreshSession(t *testing.T) {
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
	gin.SetMode(gin.TestMode)
	handler := Init()

	sessionResp := httptest.NewRecorder()
	sessionReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessionReq.AddCookie(&http.Cookie{Name: "nps_session", Value: "invalid-session-cookie"})
	handler.ServeHTTP(sessionResp, sessionReq)
	if sessionResp.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/session with invalid session cookie status = %d, want 200", sessionResp.Code)
	}
	if cookieByName(sessionResp.Result().Cookies(), "nps_session") == nil {
		t.Fatal("GET /api/auth/session with invalid session cookie should issue a fresh session cookie")
	}
}

func TestFormalManagementAPIResponsesDisableCaching(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()
	resetTestDB(t)

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=",
		"web_password=",
		"web_base_url=",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	clientResp := httptest.NewRecorder()
	clientReq := httptest.NewRequest(http.MethodGet, "/api/clients", nil)
	handler.ServeHTTP(clientResp, clientReq)
	if clientResp.Code != http.StatusOK {
		t.Fatalf("GET /api/clients status = %d, want 200", clientResp.Code)
	}
	if got := clientResp.Result().Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET /api/clients Cache-Control = %q, want no-store", got)
	}
	if got := clientResp.Result().Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("GET /api/clients Pragma = %q, want no-cache", got)
	}

	registerResp := httptest.NewRecorder()
	registerReq := httptest.NewRequest(http.MethodPost, "/api/access/ip-limit/actions/register", strings.NewReader(`{}`))
	registerReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(registerResp, registerReq)
	if registerResp.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/access/ip-limit/actions/register status = %d, want 401", registerResp.Code)
	}
	if got := registerResp.Result().Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("POST /api/access/ip-limit/actions/register Cache-Control = %q, want no-store", got)
	}
	if got := registerResp.Result().Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("POST /api/access/ip-limit/actions/register Pragma = %q, want no-cache", got)
	}
}

func TestBlankAdminCredentialsAutoLogin(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=",
		"web_password=",
		"web_base_url=",
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
	handler.ServeHTTP(discoveryResp, discoveryReq)
	if discoveryResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery with blank admin credentials status = %d, want 200", discoveryResp.Code)
	}
	body := discoveryResp.Body.String()
	if !strings.Contains(body, "\"authenticated\":true") || !strings.Contains(body, "\"is_admin\":true") {
		t.Fatalf("GET /api/system/discovery should expose authenticated admin session, got %s", body)
	}
}

func TestCreateSessionRejectsOversizedChunkedBody(t *testing.T) {
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
		"login_max_body=32",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	body := `{"username":"admin","password":"` + strings.Repeat("x", 128) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/session", io.NopCloser(strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /api/auth/session with oversized chunked body status = %d, want 413", resp.Code)
	}
	if body := resp.Body.String(); !strings.Contains(body, "\"code\":\"payload_too_large\"") || !strings.Contains(body, "\"message\":\"payload too large\"") {
		t.Fatalf("POST /api/auth/session with oversized chunked body should return typed error payload, got %s", body)
	}
}

func TestCloseUnknownPathWithoutResponseWhenEnabled(t *testing.T) {
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
		"web_close_on_not_found=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	ts := httptest.NewServer(Init())
	defer ts.Close()

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if _, err := fmt.Fprintf(conn, "GET /definitely-missing HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", addr); err != nil {
		t.Fatalf("write raw request error = %v", err)
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("unknown path should close without HTTP response, got %q", string(raw))
	}
}
