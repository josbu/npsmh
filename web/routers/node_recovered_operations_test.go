package routers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestNodeSyncRecordsOperationSummary(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, &fakeNodeStore{})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/system/actions/sync", nil)
	req.Header.Set("X-Node-Token", "secret")
	req.Header.Set("X-Operation-ID", "sync-op-1")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/system/actions/sync status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if got := resp.Result().Header.Get("X-Operation-ID"); got != "sync-op-1" {
		t.Fatalf("POST /api/system/actions/sync X-Operation-ID = %q, want sync-op-1", got)
	}

	operationsReq := httptest.NewRequest(http.MethodGet, "/api/system/operations?operation_id=sync-op-1", nil)
	operationsReq.Header.Set("X-Node-Token", "secret")
	operationsResp := httptest.NewRecorder()
	handler.ServeHTTP(operationsResp, operationsReq)
	if operationsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/operations status = %d, want 200 body=%s", operationsResp.Code, operationsResp.Body.String())
	}
	body := operationsResp.Body.String()
	if !strings.Contains(body, "\"operation_id\":\"sync-op-1\"") ||
		!strings.Contains(body, "\"kind\":\"sync\"") ||
		!strings.Contains(body, "\"paths\":[\"/api/system/actions/sync\"]") {
		t.Fatalf("GET /api/system/operations body = %s", body)
	}
}

func TestNodeManageRecordsOperationSummary(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"manage-op-client","remark":"tracked by operations"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "secret")
	req.Header.Set("X-Operation-ID", "manage-op-1")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	operationsReq := httptest.NewRequest(http.MethodGet, "/api/system/operations?operation_id=manage-op-1", nil)
	operationsReq.Header.Set("X-Node-Token", "secret")
	operationsResp := httptest.NewRecorder()
	handler.ServeHTTP(operationsResp, operationsReq)
	if operationsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/operations status = %d, want 200 body=%s", operationsResp.Code, operationsResp.Body.String())
	}
	body := operationsResp.Body.String()
	if !strings.Contains(body, "\"operation_id\":\"manage-op-1\"") ||
		!strings.Contains(body, "\"kind\":\"resource\"") ||
		!strings.Contains(body, "\"paths\":[\"/api/clients\"]") {
		t.Fatalf("GET /api/system/operations body = %s", body)
	}
}

func TestInitNodeGlobalRouteReturnsFormalPayload(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := file.GetDb().SaveGlobal(&file.Glob{EntryAclMode: file.AclBlacklist, EntryAclRules: "10.0.0.1\n10.0.0.2"}); err != nil {
		t.Fatalf("SaveGlobal() error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/settings/global", nil)
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/settings/global status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"entry_acl_mode\":2") ||
		!strings.Contains(body, "\"entry_acl_rules\":\"10.0.0.1\\n10.0.0.2\"") ||
		!strings.Contains(body, "\"config_epoch\":\"") {
		t.Fatalf("GET /api/settings/global body = %s", body)
	}
}

func TestInitNodeUpdateGlobalRouteSupportsFormalACLPayload(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/settings/global/actions/update", strings.NewReader(`{"entry_acl_mode":1,"entry_acl_rules":"10.0.0.0/8"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/settings/global/actions/update status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	global := file.GetDb().GetGlobal()
	if global.EntryAclMode != file.AclWhitelist || global.EntryAclRules != "10.0.0.0/8" {
		t.Fatalf("global ACL = mode:%d rules:%q, want 1 and 10.0.0.0/8", global.EntryAclMode, global.EntryAclRules)
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"resource\":\"global\"") ||
		!strings.Contains(body, "\"action\":\"update\"") ||
		!strings.Contains(body, "\"entry_acl_mode\":1") {
		t.Fatalf("POST /api/settings/global/actions/update body = %s", body)
	}
}

func TestInitNodeBanListRouteReturnsFormalPayload(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	webservice.SharedLoginPolicy().RemoveAllBans()
	t.Cleanup(webservice.SharedLoginPolicy().RemoveAllBans)
	webservice.SharedLoginPolicy().RecordFailure("192.0.2.10", true)
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/security/bans", nil)
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/security/bans status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"total\":1") ||
		!strings.Contains(body, "\"key\":\"192.0.2.10\"") ||
		!strings.Contains(body, "\"ban_type\":\"ip\"") {
		t.Fatalf("GET /api/security/bans body = %s", body)
	}
}

func TestInitNodeClearClientsRouteClearsVisibleClientFlow(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	client := &file.Client{
		Id:        7,
		VerifyKey: "clear-flow-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 5},
	}
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()
	adminIdentity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
	}).Normalize()
	adminCookies := sessionCookiesFromIdentity(t, servercfg.Current(), adminIdentity)

	req := httptest.NewRequest(http.MethodPost, "/api/clients/actions/clear", strings.NewReader(`{"mode":"flow","client_ids":[7]}`))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range adminCookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/actions/clear status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	updated, err := file.GetDb().GetClient(7)
	if err != nil {
		t.Fatalf("GetClient(7) error = %v", err)
	}
	if updated.Flow == nil || updated.Flow.InletFlow != 0 || updated.Flow.ExportFlow != 0 {
		t.Fatalf("client flow after clear = %+v, want zeros", updated.Flow)
	}
}

func TestInitNodeGlobalRouteRejectsAccountScopePlatform(t *testing.T) {
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
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/settings/global", nil)
	req.Header.Set("X-Node-Token", "token-a")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/settings/global status = %d, want 403 body=%s", resp.Code, resp.Body.String())
	}
}
