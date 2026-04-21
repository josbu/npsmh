package routers

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestManagementDiscoveryReflectsPermissions(t *testing.T) {
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
		"allow_user_register=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	anonymousResp := httptest.NewRecorder()
	anonymousReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	handler.ServeHTTP(anonymousResp, anonymousReq)
	if anonymousResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery status = %d, want 200", anonymousResp.Code)
	}
	if paths := discoveryActionPaths(t, anonymousResp.Body.Bytes()); len(paths) != 0 {
		t.Fatalf("anonymous discovery should not expose actions, got %v", sortedKeys(paths))
	}

	userCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:operator",
		Username:      "operator",
		ClientIDs:     []int{101},
		Roles:         []string{webservice.RoleUser},
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
	userActionPaths := discoveryActionPaths(t, userResp.Body.Bytes())
	userRoutes := discoveryRoutes(t, userResp.Body.Bytes())
	if !userActionPaths["/api/tunnels"] || !userActionPaths["/api/clients"] {
		t.Fatalf("user discovery should expose permitted routes, got %v", sortedKeys(userActionPaths))
	}
	if !userActionPaths["/api/clients/{id}/connections"] {
		t.Fatalf("user discovery should expose client connections route, got %v", sortedKeys(userActionPaths))
	}
	if !userActionPaths["/api/clients/actions/kick"] || !userActionPaths["/api/clients/actions/clear"] {
		t.Fatalf("user discovery should expose scoped client control actions, got %v", sortedKeys(userActionPaths))
	}
	if userActionPaths["/api/security/bans"] {
		t.Fatalf("user discovery should not expose forbidden routes, got %v", sortedKeys(userActionPaths))
	}
	if userActionPaths["/api/system/dashboard"] || userActionPaths["/api/system/status"] || userActionPaths["/api/system/actions/sync"] {
		t.Fatalf("user discovery should not expose full-access system actions, got %v", sortedKeys(userActionPaths))
	}
	if userRoutes.Dashboard != "" || userRoutes.Status != "" || userRoutes.SystemSync != "" || userRoutes.Webhooks != "" {
		t.Fatalf("user discovery should not publish forbidden routes, got %#v", userRoutes)
	}
	if userRoutes.Overview == "" || userRoutes.Tunnels == "" || userRoutes.Clients == "" || userRoutes.ClientsConnections == "" || userRoutes.Batch == "" || userRoutes.WebSocket == "" {
		t.Fatalf("user discovery should publish accessible routes, got %#v", userRoutes)
	}

	globalManagerCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "service",
		Provider:      "test",
		SubjectID:     "service:global-manager",
		Username:      "global-manager",
		Roles:         []string{"service"},
		Permissions:   []string{webservice.PermissionGlobalManage},
	}).Normalize())
	globalResp := httptest.NewRecorder()
	globalReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	for _, cookie := range globalManagerCookies {
		globalReq.AddCookie(cookie)
	}
	handler.ServeHTTP(globalResp, globalReq)
	if globalResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery as global manager status = %d, want 200", globalResp.Code)
	}
	globalActionPaths := discoveryActionPaths(t, globalResp.Body.Bytes())
	globalRoutes := discoveryRoutes(t, globalResp.Body.Bytes())
	if !globalActionPaths["/api/security/bans"] {
		t.Fatalf("global manager discovery should expose global routes, got %v", sortedKeys(globalActionPaths))
	}
	if !globalActionPaths["/api/settings/global"] {
		t.Fatalf("global manager discovery should expose global read route, got %v", sortedKeys(globalActionPaths))
	}
	if globalActionPaths["/api/system/dashboard"] || globalActionPaths["/api/system/actions/sync"] {
		t.Fatalf("global manager discovery should not expose full-access system actions, got %v", sortedKeys(globalActionPaths))
	}
	if globalRoutes.SecurityBans == "" || globalRoutes.SettingsGlobal == "" {
		t.Fatalf("global manager discovery should publish accessible routes, got %#v", globalRoutes)
	}
	if globalRoutes.Dashboard != "" || globalRoutes.SystemSync != "" {
		t.Fatalf("global manager discovery should not publish full-access routes, got %#v", globalRoutes)
	}
}

func TestManagementDiscoveryReflectsClientPrincipalScope(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	if err := file.GetDb().NewClient(&file.Client{
		Id:        101,
		Status:    true,
		VerifyKey: "vk-101",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient(101) error = %v", err)
	}
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"allow_user_login=true",
		"allow_user_vkey_login=true",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

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

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	for _, cookie := range clientCookies {
		req.AddCookie(cookie)
	}
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery as client status = %d, want 200", resp.Code)
	}

	actions := discoveryActions(t, resp.Body.Bytes())
	routes := discoveryRoutes(t, resp.Body.Bytes())
	allowed := make(map[string]bool, len(actions))
	for _, action := range actions {
		allowed[action.Resource+":"+action.Action+":"+action.Method] = true
	}
	if !allowed["clients:list:GET"] || !allowed["clients:read:GET"] || !allowed["tunnels:update:POST"] || !allowed["hosts:update:POST"] {
		t.Fatalf("client discovery should expose own resource actions, got %v body=%s", allowed, resp.Body.String())
	}
	if !allowed["clients:connections:GET"] {
		t.Fatalf("client discovery should expose client connections action, got %v body=%s", allowed, resp.Body.String())
	}
	if allowed["clients:create:POST"] || allowed["clients:update:POST"] || allowed["clients:status:POST"] || allowed["clients:delete:POST"] {
		t.Fatalf("client discovery should not expose client self-mutation actions, got %v body=%s", allowed, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "\"allow_client_vkey_login\":true") {
		t.Fatalf("client discovery should publish allow_client_vkey_login, got %s", resp.Body.String())
	}
	if routes.Clients == "" || routes.ClientsConnections == "" || routes.Tunnels == "" || routes.Hosts == "" || routes.Batch == "" || routes.WebSocket == "" {
		t.Fatalf("client discovery should publish scoped resource routes, got %#v", routes)
	}
	if routes.Status != "" || routes.Webhooks != "" || routes.SecurityBans != "" || routes.SettingsGlobal != "" {
		t.Fatalf("client discovery should not publish forbidden routes, got %#v", routes)
	}
}
