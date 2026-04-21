package routers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeDashboardRequiresManagementViewScope(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nallow_user_login=true\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := file.GetDb().NewUser(&file.User{Id: 1, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &file.Flow{}}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()
	cfg := servercfg.Current()

	adminCookies := sessionCookiesFromIdentity(t, cfg, (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
	}).Normalize())
	adminReq := httptest.NewRequest(http.MethodGet, "/api/system/dashboard", nil)
	for _, cookie := range adminCookies {
		adminReq.AddCookie(cookie)
	}
	adminResp := httptest.NewRecorder()
	handler.ServeHTTP(adminResp, adminReq)
	if adminResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/dashboard as admin status = %d, want 200 body=%s", adminResp.Code, adminResp.Body.String())
	}

	userCookies := sessionCookiesFromIdentity(t, cfg, (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Roles:         []string{webservice.RoleUser},
		Attributes: map[string]string{
			"user_id": "1",
		},
	}).Normalize())
	userReq := httptest.NewRequest(http.MethodGet, "/api/system/dashboard", nil)
	for _, cookie := range userCookies {
		userReq.AddCookie(cookie)
	}
	userResp := httptest.NewRecorder()
	handler.ServeHTTP(userResp, userReq)
	if userResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/dashboard as user status = %d, want 403 body=%s", userResp.Code, userResp.Body.String())
	}
}

func TestRemoteNodePlatformRequestsWithoutRoleDefaultToPlatformScopeUnlessDelegated(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=token-a",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")

	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{PlatformID: "master-a", ServiceUsername: "svc_master_a", Enabled: true},
	})
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID() error = %v", err)
	}
	serviceClient := &file.Client{Id: 1, UserId: serviceUser.Id, VerifyKey: "svc-client", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{InletFlow: 10, ExportFlow: 20}}
	serviceClient.SetOwnerUserID(serviceUser.Id)
	if err := file.GetDb().NewClient(serviceClient); err != nil {
		t.Fatalf("NewClient(service) error = %v", err)
	}
	localUser := &file.User{Id: 20, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &file.Flow{}}
	if err := file.GetDb().NewUser(localUser); err != nil {
		t.Fatalf("NewUser(local) error = %v", err)
	}
	localClient := &file.Client{Id: 2, UserId: localUser.Id, VerifyKey: "local-client", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{InletFlow: 30, ExportFlow: 40}}
	localClient.SetOwnerUserID(localUser.Id)
	if err := file.GetDb().NewClient(localClient); err != nil {
		t.Fatalf("NewClient(local) error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	usageReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	usageReq.Header.Set("X-Node-Token", "token-a")
	usageResp := httptest.NewRecorder()
	handler.ServeHTTP(usageResp, usageReq)
	if usageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/usage-snapshot without explicit role status = %d, want 200 body=%s", usageResp.Code, usageResp.Body.String())
	}
	usageBody := usageResp.Body.String()
	if !strings.Contains(usageBody, "\"verify_key\":\"svc-client\"") || strings.Contains(usageBody, "\"verify_key\":\"local-client\"") {
		t.Fatalf("GET /api/system/usage-snapshot without explicit role should default to platform scope, got %s", usageBody)
	}

	delegatedReq := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	delegatedReq.Header.Set("X-Node-Token", "token-a")
	delegatedReq.Header.Set("X-Platform-Actor-ID", "delegated-user-1")
	delegatedReq.Header.Set("X-Platform-Client-IDs", "1")
	delegatedResp := httptest.NewRecorder()
	handler.ServeHTTP(delegatedResp, delegatedReq)
	if delegatedResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/export delegated without explicit role status = %d, want 403 body=%s", delegatedResp.Code, delegatedResp.Body.String())
	}
}

func TestNodeWebSocketHelloWithoutDelegatedHeadersDefaultsToAccountScope(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=token-a",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"node_changes_window=2048",
		"node_batch_max_items=80",
		"node_idempotency_ttl_seconds=600",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "token-a")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}
	body := string(hello.Body)
	overviewPath := webapi.NodeDirectRouteCatalog("").OverviewURL(false)
	if !strings.Contains(body, "\"initial_sync_scope\":\"account\"") ||
		!strings.Contains(body, "\"initial_sync_complete_config\":false") ||
		!strings.Contains(body, "\"initial_sync_routes\":[\""+overviewPath+"\"]") ||
		strings.Contains(body, "/api/system/export") {
		t.Fatalf("unexpected hello body = %s", body)
	}
}

func TestNodeClientQRCodeUsesFormalRoute(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/tools/qrcode?text=node-qr-demo", nil)
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/tools/qrcode status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if contentType := resp.Result().Header.Get("Content-Type"); !strings.Contains(contentType, "image/png") {
		t.Fatalf("GET /api/tools/qrcode content type = %q, want image/png", contentType)
	}
	if cacheControl := resp.Result().Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("GET /api/tools/qrcode Cache-Control = %q, want no-store", cacheControl)
	}
	if pragma := resp.Result().Header.Get("Pragma"); pragma != "no-cache" {
		t.Fatalf("GET /api/tools/qrcode Pragma = %q, want no-cache", pragma)
	}
	if !bytes.HasPrefix(resp.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		prefixLen := len(resp.Body.Bytes())
		if prefixLen > 4 {
			prefixLen = 4
		}
		t.Fatalf("GET /api/tools/qrcode should return PNG bytes, got prefix=%v", resp.Body.Bytes()[:prefixLen])
	}
}

func TestNodeClientQRCodeSupportsFormalPOSTJSONBody(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/tools/qrcode", strings.NewReader(`{"account":"tenant@example.com","secret":"JBSWY3DPEHPK3PXP"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/tools/qrcode status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if contentType := resp.Result().Header.Get("Content-Type"); !strings.Contains(contentType, "image/png") {
		t.Fatalf("POST /api/tools/qrcode content type = %q, want image/png", contentType)
	}
	if cacheControl := resp.Result().Header.Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("POST /api/tools/qrcode Cache-Control = %q, want no-store", cacheControl)
	}
	if pragma := resp.Result().Header.Get("Pragma"); pragma != "no-cache" {
		t.Fatalf("POST /api/tools/qrcode Pragma = %q, want no-cache", pragma)
	}
	if !bytes.HasPrefix(resp.Body.Bytes(), []byte{0x89, 'P', 'N', 'G'}) {
		prefixLen := len(resp.Body.Bytes())
		if prefixLen > 4 {
			prefixLen = 4
		}
		t.Fatalf("POST /api/tools/qrcode should return PNG bytes, got prefix=%v", resp.Body.Bytes()[:prefixLen])
	}
}

func TestNodeClientQRCodeReturnsManagementErrorWhenTextMissing(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/tools/qrcode", nil)
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/tools/qrcode without text status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	if contentType := resp.Result().Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("GET /api/tools/qrcode without text content type = %q, want application/json", contentType)
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"code\":\"qrcode_text_required\"") || !strings.Contains(body, "\"message\":\"missing text\"") {
		t.Fatalf("GET /api/tools/qrcode without text should return typed error payload, got %s", body)
	}
}
