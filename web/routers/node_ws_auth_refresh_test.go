package routers

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeWebSocketAuthRefresherIgnoresUnsupportedBasicHeaderButStillUsesPlatformTokenWhenPresent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	req.Header.Set("X-Node-Token", "platform-token")
	ctx.Request = req

	refresher := newNodeWSAuthRefresher(&State{}, ctx, webapi.AdminActor("admin"))
	if refresher.mode != nodeWSAuthModeStatic {
		t.Fatalf("refresher.mode = %q, want %q when basic auth is unsupported", refresher.mode, nodeWSAuthModeStatic)
	}

	ctx.Set(nodePlatformContextKey, &nodePlatformContext{})
	refresher = newNodeWSAuthRefresher(&State{}, ctx, webapi.AdminActor("admin"))
	if refresher.mode != nodeWSAuthModePlatformToken {
		t.Fatalf("refresher.mode with platform context = %q, want %q", refresher.mode, nodeWSAuthModePlatformToken)
	}
}

func TestNodeWebSocketRejectsBasicAuthorizationHandshake(t *testing.T) {
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
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	headers := http.Header{}
	headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err == nil {
		t.Fatal("Dial() error = nil, want websocket handshake rejection for basic auth")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket basic auth status = %d, want 401", status)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `"code":"unsupported_auth_scheme"`) {
		t.Fatalf("websocket basic auth body = %s, want unsupported_auth_scheme", string(body))
	}
}

func TestNodeWebSocketSessionRefreshPreservesStatePermissionResolver(t *testing.T) {
	resolver := routerStubPermissionResolver{
		normalizeIdentity: func(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
			if identity == nil {
				return nil
			}
			cloned := *identity
			cloned.Roles = append([]string(nil), identity.Roles...)
			cloned.Permissions = append([]string(nil), identity.Permissions...)
			cloned.Permissions = append(cloned.Permissions, "custom:session")
			return &cloned
		},
	}
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "configured-admin"},
	}
	identity := resolver.NormalizeIdentity(&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
		Permissions:   []string{webservice.PermissionAll},
	})
	state := &State{App: webapi.NewWithOptions(cfg, webapi.Options{
		Services: &webservice.Services{Permissions: resolver},
	})}
	refresher := &nodeWSAuthRefresher{
		state:    state,
		mode:     nodeWSAuthModeSession,
		identity: identity,
		actor:    webapi.ActorFromSessionIdentityWithFallback(identity, state.AdminUsername()),
	}

	actor, err := refresher.Refresh(time.Unix(123, 0))
	if err != nil {
		t.Fatalf("Refresh() error = %v, want nil", err)
	}
	if refresher.identity == nil {
		t.Fatal("refresher.identity = nil, want refreshed session identity")
	}
	if !permissionListContains(actor.Permissions, "custom:session") {
		t.Fatalf("actor.Permissions = %v, want custom resolver permission", actor.Permissions)
	}
	if !permissionListContains(refresher.identity.Permissions, "custom:session") {
		t.Fatalf("refresher.identity.Permissions = %v, want custom resolver permission", refresher.identity.Permissions)
	}
}

func TestNodeWebSocketRefreshesOwnerTransferForExistingSessions(t *testing.T) {
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
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldOwner := createTestUser(t, 2101, "ws-owner-old", "owner-old-secret")
	newOwner := createTestUser(t, 2102, "ws-owner-new", "owner-new-secret")
	client := createOwnedTestClient(t, 701, oldOwner.Id, "ws-owner-transfer")

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	headers := http.Header{}
	headers.Set("Cookie", cookieHeaderValue(sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + oldOwner.Username,
		Username:      oldOwner.Username,
		ClientIDs:     []int{client.Id},
		Attributes: map[string]string{
			"user_id":    "2101",
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	savedClient, err := file.GetDb().GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", client.Id, err)
	}
	savedClient.SetOwnerUserID(newOwner.Id)
	savedClient.ManagerUserIDs = nil
	if err := file.GetDb().UpdateClient(savedClient); err != nil {
		t.Fatalf("UpdateClient(%d) error = %v", client.Id, err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "owner-transfer-detail",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients/701",
	}); err != nil {
		t.Fatalf("WriteJSON(detail request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "owner-transfer-detail"
	})
	if resp.Status != http.StatusForbidden {
		t.Fatalf("unexpected ws owner transfer response: %+v body=%s", resp, string(resp.Body))
	}
}

func permissionListContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestNodeWebSocketInvalidatesDisabledUserSessionOnNextRequest(t *testing.T) {
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
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	user := createTestUser(t, 2201, "ws-disable-user", "tenant-secret")
	createOwnedTestClient(t, 702, user.Id, "ws-disable-client")

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	headers := http.Header{}
	headers.Set("Cookie", cookieHeaderValue(sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + user.Username,
		Username:      user.Username,
		Attributes: map[string]string{
			"user_id":    "2201",
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	savedUser, err := file.GetDb().GetUser(user.Id)
	if err != nil {
		t.Fatalf("GetUser(%d) error = %v", user.Id, err)
	}
	savedUser.Status = 0
	if err := file.GetDb().UpdateUser(savedUser); err != nil {
		t.Fatalf("UpdateUser(%d) error = %v", user.Id, err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "disabled-user-list",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients?offset=0&limit=0",
	}); err != nil {
		t.Fatalf("WriteJSON(list request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "disabled-user-list"
	})
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("unexpected ws disabled user response: %+v body=%s", resp, string(resp.Body))
	}
	if body := string(resp.Body); !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("unexpected disabled user body = %s", body)
	}
}

func TestNodeWebSocketRechecksStandaloneTokenValidityPerRequest(t *testing.T) {
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
		"web_standalone_token_secret=standalone-secret",
		"web_standalone_token_ttl_seconds=600",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(`{"username":"admin","password":"secret"}`))
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

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	current := servercfg.Current()
	if current == nil {
		t.Fatal("Current() returned nil config")
	}
	current.Web.StandaloneTokenSecret = "rotated-standalone-secret"
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "invalid-token-status",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/status",
	}); err != nil {
		t.Fatalf("WriteJSON(status request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "invalid-token-status"
	})
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("unexpected ws invalid token response: %+v body=%s", resp, string(resp.Body))
	}
	if body := string(resp.Body); !strings.Contains(body, `"code":"standalone_token_invalid"`) {
		t.Fatalf("unexpected invalid token body = %s", body)
	}
}

func TestNodeWebSocketInvalidatesDisabledUserStandaloneTokenOnNextRequest(t *testing.T) {
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

	user := createTestUser(t, 2251, "ws-token-disabled-user", "tenant-secret")

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(`{"username":"ws-token-disabled-user","password":"tenant-secret"}`))
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

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	savedUser, err := file.GetDb().GetUser(user.Id)
	if err != nil {
		t.Fatalf("GetUser(%d) error = %v", user.Id, err)
	}
	savedUser.Status = 0
	if err := file.GetDb().UpdateUser(savedUser); err != nil {
		t.Fatalf("UpdateUser(%d) error = %v", user.Id, err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "disabled-user-token-list",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients?offset=0&limit=0",
	}); err != nil {
		t.Fatalf("WriteJSON(list request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "disabled-user-token-list"
	})
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("unexpected ws disabled user standalone token response: %+v body=%s", resp, string(resp.Body))
	}
	if body := string(resp.Body); !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("unexpected disabled user standalone token body = %s", body)
	}
}

func TestNodeWebSocketReturnsInternalErrorWhenStandaloneTokenRefreshFails(t *testing.T) {
	backendErr := errors.New("token refresh lookup failed")
	getUserCalls := 0
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
		Feature: servercfg.FeatureConfig{
			AllowUserLogin: true,
		},
	}
	app := webapi.NewWithOptions(cfg, webapi.Options{
		Services: &webservice.Services{
			Backend: webservice.Backend{
				Repository: authFailureRouterRepo{
					Repository: webservice.DefaultBackend().Repository,
					getUserByUsername: func(username string) (*file.User, error) {
						return &file.User{
							Id:        7,
							Username:  username,
							Password:  "secret",
							Kind:      "local",
							Status:    1,
							TotalFlow: &file.Flow{},
						}, nil
					},
					getUser: func(id int) (*file.User, error) {
						getUserCalls++
						if getUserCalls == 1 {
							return &file.User{
								Id:        id,
								Username:  "tenant",
								Kind:      "local",
								Status:    1,
								TotalFlow: &file.Flow{},
							}, nil
						}
						return nil, backendErr
					},
				},
			},
		},
	})

	gin.SetMode(gin.TestMode)
	handler := buildEngine(NewStateWithApp(app))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(`{"username":"tenant","password":"secret"}`))
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

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "broken-refresh-status",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/status",
	}); err != nil {
		t.Fatalf("WriteJSON(status request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "broken-refresh-status"
	})
	if resp.Status != http.StatusInternalServerError {
		t.Fatalf("unexpected ws broken standalone refresh response: %+v body=%s", resp, string(resp.Body))
	}
	if body := string(resp.Body); !strings.Contains(body, `"code":"request_failed"`) || !strings.Contains(body, backendErr.Error()) {
		t.Fatalf("unexpected ws broken standalone refresh body = %s", body)
	}
}

func TestNodeWebSocketAcceptsStandaloneTokenViaSubprotocol(t *testing.T) {
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
		"web_standalone_token_secret=standalone-secret",
		"web_standalone_token_ttl_seconds=600",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	tokenReq := httptest.NewRequest(http.MethodPost, "/api/auth/token", strings.NewReader(`{"username":"admin","password":"secret"}`))
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

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{tokenPayload.AccessToken}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "subprotocol-status",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/status",
	}); err != nil {
		t.Fatalf("WriteJSON(status request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "subprotocol-status"
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("unexpected ws subprotocol token response: %+v body=%s", resp, string(resp.Body))
	}
}

func TestNodeWebSocketRechecksPlatformTokenScopePerRequest(t *testing.T) {
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
	otherUser := createTestUser(t, 2301, "ws-platform-other", "tenant-secret")
	client := createOwnedTestClient(t, 703, serviceUser.Id, "ws-platform-scope")

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	headers := http.Header{}
	headers.Set("X-Node-Token", "token-a")
	headers.Set("X-Platform-Role", "user")
	headers.Set("X-Platform-Actor-ID", "delegated-user-1")
	headers.Set("X-Platform-Client-IDs", strconv.Itoa(client.Id))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws", headers)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	savedClient, err := file.GetDb().GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", client.Id, err)
	}
	savedClient.SetOwnerUserID(otherUser.Id)
	savedClient.ManagerUserIDs = nil
	if err := file.GetDb().UpdateClient(savedClient); err != nil {
		t.Fatalf("UpdateClient(%d) error = %v", client.Id, err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "platform-scope-detail",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients/" + strconv.Itoa(client.Id),
	}); err != nil {
		t.Fatalf("WriteJSON(detail request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "platform-scope-detail"
	})
	if resp.Status != http.StatusForbidden {
		t.Fatalf("unexpected ws platform scope response: %+v body=%s", resp, string(resp.Body))
	}
	if body := string(resp.Body); !strings.Contains(body, `"code":"forbidden"`) {
		t.Fatalf("unexpected ws platform scope body = %s", body)
	}
}

func cookieHeaderValue(cookies []*http.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	return strings.Join(values, "; ")
}
