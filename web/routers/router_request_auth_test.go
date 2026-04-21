package routers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

type recordingRouterSystemService struct {
	registered []string
}

type authFailureRouterRepo struct {
	webservice.Repository
	getUserByUsername func(string) (*file.User, error)
	getUser           func(int) (*file.User, error)
}

type routerStubPermissionResolver struct {
	normalizeIdentity func(*webservice.SessionIdentity) *webservice.SessionIdentity
}

func (s *recordingRouterSystemService) Info() webservice.SystemInfo {
	return webservice.SystemInfo{}
}

func (s *recordingRouterSystemService) BridgeDisplay(*servercfg.Snapshot, string) webservice.BridgeDisplay {
	return webservice.BridgeDisplay{}
}

func (s *recordingRouterSystemService) RegisterManagementAccess(remoteAddr string) {
	s.registered = append(s.registered, remoteAddr)
}

func (r authFailureRouterRepo) GetUserByUsername(username string) (*file.User, error) {
	if r.getUserByUsername != nil {
		return r.getUserByUsername(username)
	}
	return r.Repository.GetUserByUsername(username)
}

func (r authFailureRouterRepo) GetUser(id int) (*file.User, error) {
	if r.getUser != nil {
		return r.getUser(id)
	}
	return r.Repository.GetUser(id)
}

func (r routerStubPermissionResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	return principal
}

func (r routerStubPermissionResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	if r.normalizeIdentity != nil {
		return r.normalizeIdentity(identity)
	}
	return identity
}

func (routerStubPermissionResolver) KnownRoles() []string {
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (routerStubPermissionResolver) KnownPermissions() []string {
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (routerStubPermissionResolver) PermissionCatalog() map[string][]string {
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

func TestProtectedRouteRejectsStandaloneTokenQueryParameter(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			Password:                  "secret",
			BaseURL:                   "/base",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
	}
	handler := buildEngine(NewStateWithApp(webapi.New(cfg)))

	tokenReq := httptest.NewRequest(http.MethodPost, "/base/api/auth/token", strings.NewReader(`{"username":"admin","password":"secret"}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp := httptest.NewRecorder()
	handler.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("POST /base/api/auth/token status = %d, want 200 body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenPayload webapi.ManagementAuthTokenPayload
	decodeManagementData(t, tokenResp.Body.Bytes(), &tokenPayload)
	if tokenPayload.AccessToken == "" {
		t.Fatalf("token payload = %+v, want issued token", tokenPayload)
	}

	req := httptest.NewRequest(http.MethodGet, "/base/api/system/status?access_token="+tokenPayload.AccessToken, nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /base/api/system/status?access_token=... status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("unexpected unauthorized body = %s", body)
	}
}

func TestProtectedRouteRejectsStandaloneTokenInNodeTokenHeader(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			Password:                  "secret",
			BaseURL:                   "/base",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
	}
	handler := buildEngine(NewStateWithApp(webapi.New(cfg)))

	tokenReq := httptest.NewRequest(http.MethodPost, "/base/api/auth/token", strings.NewReader(`{"username":"admin","password":"secret"}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp := httptest.NewRecorder()
	handler.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("POST /base/api/auth/token status = %d, want 200 body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenPayload webapi.ManagementAuthTokenPayload
	decodeManagementData(t, tokenResp.Body.Bytes(), &tokenPayload)
	if tokenPayload.AccessToken == "" {
		t.Fatalf("token payload = %+v, want issued token", tokenPayload)
	}

	req := httptest.NewRequest(http.MethodGet, "/base/api/system/status", nil)
	req.Header.Set("X-Node-Token", tokenPayload.AccessToken)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /base/api/system/status with X-Node-Token standalone token status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"code":"node_management_unavailable"`) && !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("unexpected standalone token rejection body = %s", body)
	}
}

func TestPrefixedProtectedRouteReturnsManagementErrorForInvalidOrUnsupportedAuth(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			Password:                  "secret",
			BaseURL:                   "/base",
			StandaloneTokenSecret:     "standalone-secret",
			StandaloneTokenTTLSeconds: 600,
		},
	}
	handler := buildEngine(NewStateWithApp(webapi.New(cfg)))

	invalidTokenReq := httptest.NewRequest(http.MethodGet, "/base/api/system/status", nil)
	invalidTokenReq.Header.Set("Authorization", "Bearer nps_st_invalid")
	invalidTokenResp := httptest.NewRecorder()
	handler.ServeHTTP(invalidTokenResp, invalidTokenReq)
	if invalidTokenResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /base/api/system/status with invalid standalone token status = %d, want 401 body=%s", invalidTokenResp.Code, invalidTokenResp.Body.String())
	}
	if body := invalidTokenResp.Body.String(); !strings.Contains(body, `"code":"standalone_token_invalid"`) || strings.Contains(body, `"status":0`) {
		t.Fatalf("invalid standalone token should return formal management error body, got %s", body)
	}

	unsupportedBasicReq := httptest.NewRequest(http.MethodGet, "/base/api/system/discovery", nil)
	unsupportedBasicReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	unsupportedBasicResp := httptest.NewRecorder()
	handler.ServeHTTP(unsupportedBasicResp, unsupportedBasicReq)
	if unsupportedBasicResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /base/api/system/discovery with basic auth status = %d, want 401 body=%s", unsupportedBasicResp.Code, unsupportedBasicResp.Body.String())
	}
	if body := unsupportedBasicResp.Body.String(); !strings.Contains(body, `"code":"unsupported_auth_scheme"`) || strings.Contains(body, `"status":0`) {
		t.Fatalf("basic auth should return formal management error body, got %s", body)
	}
}

func TestPrefixedProtectedRouteReturnsInternalErrorForStandaloneTokenRefreshFailure(t *testing.T) {
	backendErr := errors.New("token refresh lookup failed")
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			BaseURL:                   "/base",
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
					getUser: func(int) (*file.User, error) {
						return nil, backendErr
					},
				},
			},
		},
	})
	handler := buildEngine(NewStateWithApp(app))

	tokenReq := httptest.NewRequest(http.MethodPost, "/base/api/auth/token", strings.NewReader(`{"username":"tenant","password":"secret"}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp := httptest.NewRecorder()
	handler.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("POST /base/api/auth/token status = %d, want 200 body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenPayload webapi.ManagementAuthTokenPayload
	decodeManagementData(t, tokenResp.Body.Bytes(), &tokenPayload)
	if tokenPayload.AccessToken == "" {
		t.Fatalf("token payload = %+v, want issued token", tokenPayload)
	}

	req := httptest.NewRequest(http.MethodGet, "/base/api/system/status", nil)
	req.Header.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("GET /base/api/system/status with broken standalone refresh status = %d, want 500 body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"code":"request_failed"`) || !strings.Contains(body, backendErr.Error()) {
		t.Fatalf("unexpected standalone refresh failure body = %s", body)
	}
}

func TestPrefixedProtectedRouteReturnsUnauthorizedForStandaloneTokenRefreshMissingUser(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username:                  "admin",
			BaseURL:                   "/base",
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
					getUser: func(int) (*file.User, error) {
						return nil, file.ErrUserNotFound
					},
				},
			},
		},
	})
	handler := buildEngine(NewStateWithApp(app))

	tokenReq := httptest.NewRequest(http.MethodPost, "/base/api/auth/token", strings.NewReader(`{"username":"tenant","password":"secret"}`))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenResp := httptest.NewRecorder()
	handler.ServeHTTP(tokenResp, tokenReq)
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("POST /base/api/auth/token status = %d, want 200 body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	var tokenPayload webapi.ManagementAuthTokenPayload
	decodeManagementData(t, tokenResp.Body.Bytes(), &tokenPayload)
	if tokenPayload.AccessToken == "" {
		t.Fatalf("token payload = %+v, want issued token", tokenPayload)
	}

	req := httptest.NewRequest(http.MethodGet, "/base/api/system/status", nil)
	req.Header.Set("Authorization", "Bearer "+tokenPayload.AccessToken)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /base/api/system/status with missing standalone refresh subject status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, `"code":"unauthorized"`) {
		t.Fatalf("unexpected standalone refresh missing-user body = %s", body)
	}
}

func TestManagementAccessRejectsBasicAuthWithoutRenewal(t *testing.T) {
	system := &recordingRouterSystemService{}
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username: "admin",
			Password: "secret",
		},
		Auth: servercfg.AuthConfig{
			AllowXRealIP:    true,
			TrustedProxyIPs: "127.0.0.1",
		},
	}
	handler := buildEngine(NewStateWithApp(webapi.NewWithOptions(cfg, webapi.Options{
		Services: &webservice.Services{System: system},
	})))

	basicReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	basicReq.RemoteAddr = "127.0.0.1:3456"
	basicReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	basicReq.Header.Set("X-Forwarded-For", "198.51.100.77, 127.0.0.1")
	basicReq.Header.Set("X-Real-IP", "203.0.113.9")
	basicResp := httptest.NewRecorder()
	handler.ServeHTTP(basicResp, basicReq)
	if basicResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/system/discovery with basic auth status = %d, want 401 body=%s", basicResp.Code, basicResp.Body.String())
	}
	if body := basicResp.Body.String(); !strings.Contains(body, `"code":"unsupported_auth_scheme"`) {
		t.Fatalf("GET /api/system/discovery with basic auth body = %s, want unsupported_auth_scheme", basicResp.Body.String())
	}
	if len(system.registered) != 0 {
		t.Fatalf("basic RegisterManagementAccess() calls = %v, want none", system.registered)
	}
}

func TestManagementAccessRenewalUsesTrustedForwardedIPForSessionAuth(t *testing.T) {
	system := &recordingRouterSystemService{}
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			Username: "admin",
			Password: "secret",
		},
		Auth: servercfg.AuthConfig{
			AllowXRealIP:    true,
			TrustedProxyIPs: "127.0.0.1",
		},
	}
	handler := buildEngine(NewStateWithApp(webapi.NewWithOptions(cfg, webapi.Options{
		Services: &webservice.Services{System: system},
	})))

	sessionIdentity := (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "admin",
		Provider:      "local",
		SubjectID:     "admin:admin",
		Username:      "admin",
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
		Permissions:   []string{webservice.PermissionAll},
	}).Normalize()
	sessionReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	sessionReq.RemoteAddr = "127.0.0.1:4567"
	sessionReq.Header.Set("X-Forwarded-For", "198.51.100.88, 127.0.0.1")
	sessionReq.Header.Set("X-Real-IP", "203.0.113.10")
	for _, cookie := range sessionCookiesFromIdentity(t, cfg, sessionIdentity) {
		sessionReq.AddCookie(cookie)
	}
	sessionResp := httptest.NewRecorder()
	handler.ServeHTTP(sessionResp, sessionReq)
	if sessionResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery with session auth status = %d, want 200 body=%s", sessionResp.Code, sessionResp.Body.String())
	}
	if len(system.registered) != 1 || system.registered[0] != "198.51.100.88" {
		t.Fatalf("session RegisterManagementAccess() calls = %v, want [198.51.100.88]", system.registered)
	}
}

func TestPlatformActorFromLookupSkipsOwnedClientLookupForFullControlAdmin(t *testing.T) {
	calls := 0
	actor, err := platformActorFromLookup(servercfg.ManagementPlatformConfig{
		PlatformID:   "platform-a",
		ControlScope: "full",
	}, 17, func() ([]int, error) {
		calls++
		return []int{3, 5}, nil
	}, func(string) string {
		return ""
	})
	if err != nil {
		t.Fatalf("platformActorFromLookup() error = %v, want nil", err)
	}

	if calls != 0 {
		t.Fatalf("owned client lookup calls = %d, want 0", calls)
	}
	if actor == nil || !actor.IsAdmin {
		t.Fatalf("actor = %#v, want full-control admin actor", actor)
	}
	if len(actor.ClientIDs) != 0 {
		t.Fatalf("actor.ClientIDs = %v, want nil", actor.ClientIDs)
	}
}

func TestPlatformActorFromLookupSkipsOwnedClientLookupForUserWithoutRequestedClients(t *testing.T) {
	calls := 0
	actor, err := platformActorFromLookup(servercfg.ManagementPlatformConfig{
		PlatformID:   "platform-a",
		ControlScope: "account",
	}, 17, func() ([]int, error) {
		calls++
		return []int{3, 5}, nil
	}, func(key string) string {
		if key == "X-Platform-Role" {
			return "user"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("platformActorFromLookup() error = %v, want nil", err)
	}

	if calls != 0 {
		t.Fatalf("owned client lookup calls = %d, want 0", calls)
	}
	if actor == nil || actor.IsAdmin {
		t.Fatalf("actor = %#v, want non-admin user actor", actor)
	}
	if len(actor.ClientIDs) != 0 {
		t.Fatalf("actor.ClientIDs = %v, want nil", actor.ClientIDs)
	}
}

func TestPlatformActorFromLookupFiltersRequestedClientIDsForUser(t *testing.T) {
	calls := 0
	actor, err := platformActorFromLookup(servercfg.ManagementPlatformConfig{
		PlatformID:   "platform-a",
		ControlScope: "account",
	}, 17, func() ([]int, error) {
		calls++
		return []int{5, 3, 9}, nil
	}, func(key string) string {
		switch key {
		case "X-Platform-Role":
			return "user"
		case "X-Platform-Client-IDs":
			return "7,3,7,5"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("platformActorFromLookup() error = %v, want nil", err)
	}

	if calls != 1 {
		t.Fatalf("owned client lookup calls = %d, want 1", calls)
	}
	if actor == nil || actor.IsAdmin {
		t.Fatalf("actor = %#v, want delegated user actor", actor)
	}
	if !reflect.DeepEqual(actor.ClientIDs, []int{3, 5}) {
		t.Fatalf("actor.ClientIDs = %v, want [3 5]", actor.ClientIDs)
	}
}

func TestFixedPlatformAdminActorSkipsOwnedClientLookupForFullControl(t *testing.T) {
	calls := 0
	actor, err := fixedPlatformAdminActor(servercfg.ManagementPlatformConfig{
		PlatformID:   "platform-a",
		ControlScope: "full",
	}, 17, "callback", "callback:platform-a", func() ([]int, error) {
		calls++
		return []int{3, 5}, nil
	})
	if err != nil {
		t.Fatalf("fixedPlatformAdminActor() error = %v, want nil", err)
	}

	if calls != 0 {
		t.Fatalf("owned client lookup calls = %d, want 0", calls)
	}
	if actor == nil || !actor.IsAdmin {
		t.Fatalf("actor = %#v, want full-control fixed admin actor", actor)
	}
	if len(actor.ClientIDs) != 0 {
		t.Fatalf("actor.ClientIDs = %v, want nil", actor.ClientIDs)
	}
}

func TestFixedPlatformAdminActorUsesOwnedClientIDsForAccountScope(t *testing.T) {
	calls := 0
	actor, err := fixedPlatformAdminActor(servercfg.ManagementPlatformConfig{
		PlatformID:   "platform-a",
		ControlScope: "account",
	}, 17, "reverse", "reverse:platform-a", func() ([]int, error) {
		calls++
		return []int{3, 5}, nil
	})
	if err != nil {
		t.Fatalf("fixedPlatformAdminActor() error = %v, want nil", err)
	}

	if calls != 1 {
		t.Fatalf("owned client lookup calls = %d, want 1", calls)
	}
	if actor == nil || actor.IsAdmin {
		t.Fatalf("actor = %#v, want account-scope platform admin actor", actor)
	}
	if !reflect.DeepEqual(actor.ClientIDs, []int{3, 5}) {
		t.Fatalf("actor.ClientIDs = %v, want [3 5]", actor.ClientIDs)
	}
}

func TestPlatformActorFromLookupPropagatesOwnedClientLookupError(t *testing.T) {
	backendErr := errors.New("owned client lookup failed")

	actor, err := platformActorFromLookup(servercfg.ManagementPlatformConfig{
		PlatformID:   "platform-a",
		ControlScope: "account",
	}, 17, func() ([]int, error) {
		return nil, backendErr
	}, func(string) string {
		return ""
	})

	if !errors.Is(err, backendErr) {
		t.Fatalf("platformActorFromLookup() error = %v, want %v", err, backendErr)
	}
	if actor != nil {
		t.Fatalf("platformActorFromLookup() actor = %#v, want nil on lookup error", actor)
	}
}

func TestNodePlatformLookupReadsExplicitQueryParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/api/system/discovery?platform_role=user&platform_username=tenant&platform_actor_id=actor-q&platform_client_ids=1,2", nil)
	ctx.Request = req

	lookup := nodePlatformLookup(ctx)
	if got := lookup("platform_role"); got != "user" {
		t.Fatalf("lookup(platform_role) = %q, want user", got)
	}
	if got := lookup("platform_username"); got != "tenant" {
		t.Fatalf("lookup(platform_username) = %q, want tenant", got)
	}
	if got := lookup("platform_actor_id"); got != "actor-q" {
		t.Fatalf("lookup(platform_actor_id) = %q, want actor-q", got)
	}
	if got := lookup("platform_client_ids"); got != "1,2" {
		t.Fatalf("lookup(platform_client_ids) = %q, want 1,2", got)
	}
}
