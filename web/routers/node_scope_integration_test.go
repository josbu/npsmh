package routers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestLocalNodeMirrorRoutesRespectSessionScope(t *testing.T) {
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
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	user := &file.User{
		Id:        1,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{InletFlow: 11, ExportFlow: 22},
	}
	if err := file.GetDb().NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &file.Client{
		Id:        1,
		UserId:    user.Id,
		VerifyKey: "tenant-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 22},
	}
	client.SetOwnerUserID(user.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	cfg := servercfg.Current()

	adminIdentity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
	}).Normalize()
	adminCookies := sessionCookiesFromIdentity(t, cfg, adminIdentity)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	for _, cookie := range adminCookies {
		statusReq.AddCookie(cookie)
	}
	statusResp := httptest.NewRecorder()
	handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
	}
	if body := statusResp.Body.String(); !strings.Contains(body, "\"store_mode\":\"local\"") || !strings.Contains(body, "\"clients\":1") {
		t.Fatalf("GET /api/system/status should expose local mirror status, got %s", body)
	}
	if got := statusResp.Result().Header.Get("X-Request-ID"); strings.TrimSpace(got) == "" {
		t.Fatalf("GET /api/system/status should generate request id response header")
	}

	manageReq := httptest.NewRequest(http.MethodGet, "/api/clients?limit=10", nil)
	for _, cookie := range adminCookies {
		manageReq.AddCookie(cookie)
	}
	manageResp := httptest.NewRecorder()
	handler.ServeHTTP(manageResp, manageReq)
	if manageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/clients status = %d, want 200 body=%s", manageResp.Code, manageResp.Body.String())
	}
	if !strings.Contains(manageResp.Body.String(), "\"verify_key\":\"tenant-client\"") {
		t.Fatalf("GET /api/clients should return resource list payload, got %s", manageResp.Body.String())
	}

	batchReq := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(`{"items":[{"id":"status-1","method":"GET","path":"/api/system/status"}]}`))
	batchReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range adminCookies {
		batchReq.AddCookie(cookie)
	}
	batchResp := httptest.NewRecorder()
	handler.ServeHTTP(batchResp, batchReq)
	if batchResp.Code != http.StatusOK || !strings.Contains(batchResp.Body.String(), "\"count\":1") || !strings.Contains(batchResp.Body.String(), "\"id\":\"status-1\"") {
		t.Fatalf("POST /api/batch as admin status = %d body=%s", batchResp.Code, batchResp.Body.String())
	}

	changesReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=10", nil)
	for _, cookie := range adminCookies {
		changesReq.AddCookie(cookie)
	}
	changesResp := httptest.NewRecorder()
	handler.ServeHTTP(changesResp, changesReq)
	if changesResp.Code != http.StatusOK || !strings.Contains(changesResp.Body.String(), "\"count\":0") {
		t.Fatalf("GET /api/system/changes as admin status = %d body=%s", changesResp.Code, changesResp.Body.String())
	}

	trafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(`{"client_id":1,"in":5,"out":7}`))
	trafficReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range adminCookies {
		trafficReq.AddCookie(cookie)
	}
	trafficResp := httptest.NewRecorder()
	handler.ServeHTTP(trafficResp, trafficReq)
	if trafficResp.Code != http.StatusOK {
		t.Fatalf("POST /api/traffic as admin status = %d, want 200 body=%s", trafficResp.Code, trafficResp.Body.String())
	}

	userIdentity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		ClientIDs:     []int{client.Id},
		Roles:         []string{webservice.RoleUser},
		Attributes: map[string]string{
			"user_id": "1",
		},
	}).Normalize()
	userCookies := sessionCookiesFromIdentity(t, cfg, userIdentity)

	userStatusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	for _, cookie := range userCookies {
		userStatusReq.AddCookie(cookie)
	}
	userStatusResp := httptest.NewRecorder()
	handler.ServeHTTP(userStatusResp, userStatusReq)
	if userStatusResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/status as node user status = %d, want 403 body=%s", userStatusResp.Code, userStatusResp.Body.String())
	}

	userUsageReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	for _, cookie := range userCookies {
		userUsageReq.AddCookie(cookie)
	}
	userUsageResp := httptest.NewRecorder()
	handler.ServeHTTP(userUsageResp, userUsageReq)
	if userUsageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/usage-snapshot as node user status = %d, want 200 body=%s", userUsageResp.Code, userUsageResp.Body.String())
	}
	if body := userUsageResp.Body.String(); !strings.Contains(body, "\"verify_key\":\"tenant-client\"") || strings.Contains(body, "\"manager_user_ids\"") {
		t.Fatalf("GET /api/system/usage-snapshot should expose scoped client without admin-only manager ids, got %s", body)
	}

	userTrafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(`{"client_id":1,"in":1,"out":1}`))
	userTrafficReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range userCookies {
		userTrafficReq.AddCookie(cookie)
	}
	userTrafficResp := httptest.NewRecorder()
	handler.ServeHTTP(userTrafficResp, userTrafficReq)
	if userTrafficResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/traffic as node user status = %d, want 403 body=%s", userTrafficResp.Code, userTrafficResp.Body.String())
	}
}

func TestRemoteNodeScopeFiltersStatusAndUsageSnapshot(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a,master-b",
		"platform_tokens=token-a,token-b",
		"platform_scopes=account,full",
		"platform_enabled=true,true",
		"platform_service_users=svc_master_a,svc_master_b",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{PlatformID: "master-a", ServiceUsername: "svc_master_a", Enabled: true},
		{PlatformID: "master-b", ServiceUsername: "svc_master_b", Enabled: true},
	})
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-a) error = %v", err)
	}
	serviceUser.TotalFlow = &file.Flow{InletFlow: 500, ExportFlow: 600}
	if err := file.GetDb().UpdateUser(serviceUser); err != nil {
		t.Fatalf("UpdateUser(service user) error = %v", err)
	}
	localUser := &file.User{
		Id:        20,
		Username:  "tenant-b",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{InletFlow: 30, ExportFlow: 40},
	}
	if err := file.GetDb().NewUser(localUser); err != nil {
		t.Fatalf("NewUser(local user) error = %v", err)
	}

	serviceClient := &file.Client{
		Id:        1,
		UserId:    serviceUser.Id,
		VerifyKey: "svc-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{InletFlow: 10, ExportFlow: 20},
	}
	serviceClient.SetOwnerUserID(serviceUser.Id)
	if err := file.GetDb().NewClient(serviceClient); err != nil {
		t.Fatalf("NewClient(service client) error = %v", err)
	}
	localClient := &file.Client{
		Id:        2,
		UserId:    localUser.Id,
		VerifyKey: "local-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{InletFlow: 30, ExportFlow: 40},
	}
	localClient.SetOwnerUserID(localUser.Id)
	if err := file.GetDb().NewClient(localClient); err != nil {
		t.Fatalf("NewClient(local client) error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "token-a")
	statusReq.Header.Set("X-Platform-Role", "user")
	statusReq.Header.Set("X-Platform-Actor-ID", "platform-user-1")
	statusReq.Header.Set("X-Platform-Client-IDs", "1")
	statusResp := httptest.NewRecorder()
	handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status scoped status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
	}
	statusBody := statusResp.Body.String()
	if !strings.Contains(statusBody, "\"platform_id\":\"master-a\"") || strings.Contains(statusBody, "\"platform_id\":\"master-b\"") || !strings.Contains(statusBody, "\"clients\":1") {
		t.Fatalf("GET /api/system/status should scope visible platforms and counts, got %s", statusBody)
	}

	usageReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	usageReq.Header.Set("X-Node-Token", "token-a")
	usageReq.Header.Set("X-Platform-Role", "user")
	usageReq.Header.Set("X-Platform-Actor-ID", "platform-user-1")
	usageReq.Header.Set("X-Platform-Client-IDs", "1")
	usageResp := httptest.NewRecorder()
	handler.ServeHTTP(usageResp, usageReq)
	if usageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/usage-snapshot scoped status = %d, want 200 body=%s", usageResp.Code, usageResp.Body.String())
	}
	usageBody := usageResp.Body.String()
	if !strings.Contains(usageBody, "\"verify_key\":\"svc-client\"") ||
		strings.Contains(usageBody, "\"verify_key\":\"local-client\"") ||
		!strings.Contains(usageBody, "\"total_in_bytes\":10") ||
		strings.Contains(usageBody, "\"manager_user_ids\"") {
		t.Fatalf("GET /api/system/usage-snapshot should filter to scoped account data, got %s", usageBody)
	}

	configReq := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	configReq.Header.Set("X-Node-Token", "token-a")
	configReq.Header.Set("X-Platform-Role", "user")
	configReq.Header.Set("X-Platform-Actor-ID", "platform-user-1")
	configReq.Header.Set("X-Platform-Client-IDs", "1")
	configResp := httptest.NewRecorder()
	handler.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/export scoped platform status = %d, want 403 body=%s", configResp.Code, configResp.Body.String())
	}
}

func TestRemoteNodeDelegatedPlatformRequestWithoutRoleDefaultsToUserScope(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=token-a",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	file.EnsureManagementPlatformUsers([]file.ManagementPlatformBinding{
		{PlatformID: "master-a", ServiceUsername: "svc_master_a", Enabled: true},
	})
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-a")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-a) error = %v", err)
	}
	client := &file.Client{
		Id:        1,
		UserId:    serviceUser.Id,
		VerifyKey: "svc-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{InletFlow: 10, ExportFlow: 20},
	}
	client.SetOwnerUserID(serviceUser.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient(service client) error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	configReq := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	configReq.Header.Set("X-Node-Token", "token-a")
	configReq.Header.Set("X-Platform-Actor-ID", "delegated-user-1")
	configReq.Header.Set("X-Platform-Client-IDs", "1")
	configResp := httptest.NewRecorder()
	handler.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/export without explicit role should default delegated request to user scope, status = %d body=%s", configResp.Code, configResp.Body.String())
	}

	usageReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	usageReq.Header.Set("X-Node-Token", "token-a")
	usageReq.Header.Set("X-Platform-Actor-ID", "delegated-user-1")
	usageReq.Header.Set("X-Platform-Client-IDs", "1")
	usageResp := httptest.NewRecorder()
	handler.ServeHTTP(usageResp, usageReq)
	if usageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/usage-snapshot without explicit role status = %d body=%s", usageResp.Code, usageResp.Body.String())
	}
	if body := usageResp.Body.String(); !strings.Contains(body, "\"verify_key\":\"svc-client\"") {
		t.Fatalf("GET /api/system/usage-snapshot without explicit role should remain scoped to delegated client, got %s", body)
	}

	configReqNoRole := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	configReqNoRole.Header.Set("X-Node-Token", "token-a")
	configRespNoRole := httptest.NewRecorder()
	handler.ServeHTTP(configRespNoRole, configReqNoRole)
	if configRespNoRole.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/export without explicit role or delegated context should not default to admin, status = %d body=%s", configRespNoRole.Code, configRespNoRole.Body.String())
	}
}
