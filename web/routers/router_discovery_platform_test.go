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
	"github.com/djylb/nps/web/framework"
	"github.com/gin-gonic/gin"
)

func TestDiscoveryAdvertisesCallbackPlatformsForAuthenticatedPlatformActor(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-callback",
		"platform_tokens=callback-secret",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_callback",
		"platform_callback_urls=https://platform.example/node/callback",
		"platform_callback_enabled=true",
		"platform_callback_timeout_seconds=7",
		"platform_callback_retry_max=4",
		"platform_callback_retry_backoff_seconds=3",
		"platform_callback_queue_max=9",
		"platform_callback_signing_keys=sign-secret",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	req.Header.Set("X-Node-Token", "callback-secret")
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery status = %d body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"callbacks_ready\":true") ||
		!strings.Contains(body, "\"node.api.callbacks\"") ||
		!strings.Contains(body, "\"callback_enabled\":true") ||
		!strings.Contains(body, "\"callback_url\":\"https://platform.example/node/callback\"") ||
		!strings.Contains(body, "\"callback_timeout_seconds\":7") ||
		!strings.Contains(body, "\"callback_retry_max\":4") ||
		!strings.Contains(body, "\"callback_retry_backoff_seconds\":3") ||
		!strings.Contains(body, "\"callback_queue_max\":9") ||
		!strings.Contains(body, "\"callback_signing_enabled\":true") {
		t.Fatalf("discovery should advertise callback capability and platform config, got %s", body)
	}
}

func TestAPIInputMiddlewareSupportsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/test-api")
	api.Use(apiInputMiddleware())
	api.POST("/echo", func(c *gin.Context) {
		id, ok := requestInt(c, "id")
		if !ok || id != 42 {
			t.Fatalf("requestInt(id) = %d, %v, want 42, true", id, ok)
		}
		name, ok := framework.RequestParam(c, "name")
		if !ok || name != "demo" {
			t.Fatalf("RequestParam(name) = %q, %v, want demo, true", name, ok)
		}
		enabled, ok := framework.RequestParam(c, "enabled")
		if !ok || enabled != "true" {
			t.Fatalf("RequestParam(enabled) = %q, %v, want true, true", enabled, ok)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/test-api/echo", strings.NewReader(`{"id":42,"name":"demo","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("POST /test-api/echo status = %d, want 204", resp.Code)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/test-api/echo", strings.NewReader(`{"id":`))
	badReq.Header.Set("Content-Type", "application/json")
	badResp := httptest.NewRecorder()
	engine.ServeHTTP(badResp, badReq)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("POST /test-api/echo with invalid json status = %d, want 400", badResp.Code)
	}
	if body := badResp.Body.String(); !strings.Contains(body, "\"code\":\"invalid_json_body\"") || !strings.Contains(body, "\"message\":\"invalid json body\"") {
		t.Fatalf("POST /test-api/echo with invalid json should return typed error payload, got %s", body)
	}
}

func TestRequestIntReadsPathParamAndRequestedClientIDPrefersJSONBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/test-api")
	api.Use(apiInputMiddleware())
	api.POST("/clients/:id/actions/update", func(c *gin.Context) {
		id, ok := requestInt(c, "id")
		if !ok || id != 17 {
			t.Fatalf("requestInt(id) = %d, %v, want 17, true", id, ok)
		}
		if clientID := requestedClientID(c); clientID != 42 {
			t.Fatalf("requestedClientID() = %d, want 42", clientID)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/test-api/clients/17/actions/update?client_id=99", strings.NewReader(`{"client_id":42}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("POST /test-api/clients/17/actions/update status = %d, want 204 body=%s", resp.Code, resp.Body.String())
	}
}

func TestAPIInputMiddlewarePreservesRawJSONArrayBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/test-api")
	api.Use(apiInputMiddleware())
	api.POST("/raw", func(c *gin.Context) {
		raw := framework.RequestRawBody(c)
		if string(raw) != `[{"id":1},{"id":2}]` {
			t.Fatalf("RequestRawBody() = %q, want raw json array", string(raw))
		}
		if _, ok := framework.RequestParam(c, "items"); ok {
			t.Fatal("RequestParam(items) should be empty for top-level json array")
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/test-api/raw", strings.NewReader(`[{"id":1},{"id":2}]`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	engine.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("POST /test-api/raw status = %d, want 204 body=%s", resp.Code, resp.Body.String())
	}
}

func TestGinAPIContextLookupStringTracksJSONArrayAndExplicitEmptyValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/test-api")
	api.Use(apiInputMiddleware())
	api.POST("/json", func(c *gin.Context) {
		value, ok := newAPIContext(c).(*ginAPIContext).LookupString("manager_user_ids")
		if !ok || value != "[1,2]" {
			t.Fatalf("LookupString(json manager_user_ids) = %q, %v, want [1,2], true", value, ok)
		}
		c.Status(http.StatusNoContent)
	})
	api.POST("/query", func(c *gin.Context) {
		value, ok := newAPIContext(c).(*ginAPIContext).LookupString("manager_user_ids")
		if !ok || value != "" {
			t.Fatalf("LookupString(query manager_user_ids) = %q, %v, want empty string, true", value, ok)
		}
		c.Status(http.StatusNoContent)
	})
	api.POST("/form", func(c *gin.Context) {
		value, ok := newAPIContext(c).(*ginAPIContext).LookupString("manager_user_ids")
		if ok || value != "" {
			t.Fatalf("LookupString(form manager_user_ids) = %q, %v, want empty string, false", value, ok)
		}
		c.Status(http.StatusNoContent)
	})

	jsonReq := httptest.NewRequest(http.MethodPost, "/test-api/json", strings.NewReader(`{"manager_user_ids":[1,2]}`))
	jsonReq.Header.Set("Content-Type", "application/json")
	jsonResp := httptest.NewRecorder()
	engine.ServeHTTP(jsonResp, jsonReq)
	if jsonResp.Code != http.StatusNoContent {
		t.Fatalf("POST /test-api/json status = %d, want 204 body=%s", jsonResp.Code, jsonResp.Body.String())
	}

	queryReq := httptest.NewRequest(http.MethodPost, "/test-api/query?manager_user_ids=", nil)
	queryResp := httptest.NewRecorder()
	engine.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusNoContent {
		t.Fatalf("POST /test-api/query status = %d, want 204 body=%s", queryResp.Code, queryResp.Body.String())
	}

	formReq := httptest.NewRequest(http.MethodPost, "/test-api/form", strings.NewReader("manager_user_ids="))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formResp := httptest.NewRecorder()
	engine.ServeHTTP(formResp, formReq)
	if formResp.Code != http.StatusNoContent {
		t.Fatalf("POST /test-api/form status = %d, want 204 body=%s", formResp.Code, formResp.Body.String())
	}
}
