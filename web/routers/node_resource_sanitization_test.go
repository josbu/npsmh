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

func TestTunnelAndHostResponsesRedactNestedClientSecrets(t *testing.T) {
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

	client := createOwnedTestClient(t, 1, 0, "tenant-client")
	client.VerifyKey = "secret-vk"
	client.Cnf.U = "cfg-user"
	client.Cnf.P = "cfg-pass"
	createTestTunnel(t, 1, client, 10080)
	createTestHost(t, 1, client, "demo.example.com")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	for _, path := range []string{"/api/tunnels/1", "/api/hosts/1"} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 body=%s", path, resp.Code, resp.Body.String())
		}
		body := resp.Body.String()
		if !strings.Contains(body, `"client":{"id":1`) && !strings.Contains(body, `"client":{"id":1,`) {
			t.Fatalf("GET %s should include nested client summary, got %s", path, body)
		}
		if strings.Contains(body, `"verify_key":"secret-vk"`) {
			t.Fatalf("GET %s should redact nested client verify_key, got %s", path, body)
		}
		if strings.Contains(body, `"user":"cfg-user"`) || strings.Contains(body, `"password":"cfg-pass"`) {
			t.Fatalf("GET %s should redact nested client config credentials, got %s", path, body)
		}
	}
}

func TestClientResponsesRedactBasicPassword(t *testing.T) {
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

	client := createOwnedTestClient(t, 1, 0, "tenant-client")
	client.Cnf.U = "cfg-user"
	client.Cnf.P = "cfg-pass"

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	for _, path := range []string{"/api/clients", "/api/clients/1"} {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 body=%s", path, resp.Code, resp.Body.String())
		}
		body := resp.Body.String()
		if strings.Contains(body, `"password":"cfg-pass"`) {
			t.Fatalf("GET %s should redact client config password, got %s", path, body)
		}
		if !strings.Contains(body, `"user":"cfg-user"`) {
			t.Fatalf("GET %s should keep client config user, got %s", path, body)
		}
	}

	updateResp := httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPost, "/api/clients/1/actions/update", strings.NewReader(`{"verify_key":"tenant-client","remark":"updated"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/1/actions/update status = %d, want 200 body=%s", updateResp.Code, updateResp.Body.String())
	}
	if strings.Contains(updateResp.Body.String(), `"password":"cfg-pass"`) {
		t.Fatalf("POST /api/clients/1/actions/update should redact client config password, got %s", updateResp.Body.String())
	}
}

func TestReadOnlyScopedUserRedactsVerifyKeyAcrossResourceAndUsageViews(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"allow_user_login=true",
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"open_captcha=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	user := createTestUser(t, 1, "tenant", "secret")
	client := createOwnedTestClient(t, 1, user.Id, "tenant-client")
	client.VerifyKey = "secret-vk"
	client.ManagerUserIDs = []int{2, 3}
	if err := file.GetDb().UpdateClient(client); err != nil {
		t.Fatalf("UpdateClient() error = %v", err)
	}

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		ClientIDs:     []int{client.Id},
		Roles:         []string{"restricted"},
		Permissions:   []string{webservice.PermissionClientsRead},
		Attributes: map[string]string{
			"user_id": "1",
		},
	}).Normalize())

	for _, path := range []string{"/api/clients/1", "/api/system/usage-snapshot", "/api/system/overview"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200 body=%s", path, resp.Code, resp.Body.String())
		}
		body := resp.Body.String()
		if strings.Contains(body, `"verify_key":"secret-vk"`) {
			t.Fatalf("GET %s should redact verify_key for read-only scoped user, got %s", path, body)
		}
		if !strings.Contains(body, `"id":1`) {
			t.Fatalf("GET %s should still expose scoped client payload, got %s", path, body)
		}
	}
}
