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

func TestNodeResourceUpdatesRejectStaleExpectedRevision(t *testing.T) {
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

	user := createTestUser(t, 1, "tenant", "secret")
	client := createOwnedTestClient(t, 1, user.Id, "tenant-client")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()

	requests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "user",
			path: "/api/users/1/actions/update",
			body: `{"username":"tenant","expected_revision":999}`,
		},
		{
			name: "client",
			path: "/api/clients/1/actions/update",
			body: `{"verify_key":"vk-1","remark":"tenant-client","username":"","config_conn_allow":false,"compress":false,"crypt":false,"rate_limit_total_bps":0,"max_connections":0,"max_tunnel_num":0,"flow_limit_total_bytes":0,"expire_at":"","entry_acl_mode":0,"entry_acl_rules":"","expected_revision":999}`,
		},
	}

	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(resp, req)
			if resp.Code != http.StatusConflict {
				t.Fatalf("POST %s status = %d, want 409 body=%s", tc.path, resp.Code, resp.Body.String())
			}
			body := resp.Body.String()
			if !strings.Contains(body, `"code":"revision_conflict"`) {
				t.Fatalf("POST %s should expose revision_conflict, got %s", tc.path, body)
			}
		})
	}

	if user.Revision == 999 || client.Revision == 999 {
		t.Fatalf("stale revision update should not mutate stored resource revisions")
	}
}
