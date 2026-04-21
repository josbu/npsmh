package routers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
)

func TestNodeConfigAndTrafficEndpoints(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", "node_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	resetTestDB(t)
	user := &file.User{
		Id:        1,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		Revision:  4,
		UpdatedAt: 1700000100,
		TotalFlow: &file.Flow{},
	}
	if err := file.GetDb().NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &file.Client{
		Id:              1,
		UserId:          user.Id,
		VerifyKey:       "demo",
		Status:          true,
		Revision:        6,
		UpdatedAt:       1700000200,
		Cnf:             &file.Config{},
		Flow:            &file.Flow{},
		ManagerUserIDs:  []int{2, 3},
		MaxTunnelNum:    9,
		ConfigConnAllow: true,
	}
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

	configReq := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	configReq.Header.Set("X-Node-Token", "secret")
	configResp := httptest.NewRecorder()
	handler.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/export status = %d, want 200 body=%s", configResp.Code, configResp.Body.String())
	}
	var snapshot file.ConfigSnapshot
	decodeManagementData(t, configResp.Body.Bytes(), &snapshot)
	var foundUser bool
	for _, item := range snapshot.Users {
		if item != nil && item.Id == 1 && item.Username == "tenant" {
			foundUser = true
			break
		}
	}
	var foundClient bool
	for _, item := range snapshot.Clients {
		if item != nil && item.Id == 1 && item.VerifyKey == "demo" && item.UserId == 1 {
			foundClient = true
			break
		}
	}
	if !foundUser || !foundClient {
		t.Fatalf("expected synced user/client missing: users=%+v clients=%+v", snapshot.Users, snapshot.Clients)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "secret")
	statusResp := httptest.NewRecorder()
	handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
	}
	statusBody := statusResp.Body.String()
	if !strings.Contains(statusBody, "\"boot_id\":\"") ||
		!strings.Contains(statusBody, "\"runtime_started_at\":") ||
		!strings.Contains(statusBody, "\"resync_on_boot_change\":true") ||
		!strings.Contains(statusBody, "\"revisions\"") ||
		!strings.Contains(statusBody, "\"users\":4") ||
		!strings.Contains(statusBody, "\"clients\":6") ||
		!strings.Contains(statusBody, "\"max\":6") {
		t.Fatalf("GET /api/system/status should expose revision summary, got %s", statusBody)
	}

	trafficPayload := `{"items":[{"client_id":1,"in":10,"out":20}]}`
	trafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(trafficPayload))
	trafficReq.Header.Set("Content-Type", "application/json")
	trafficReq.Header.Set("Authorization", "Bearer secret")
	trafficResp := httptest.NewRecorder()
	handler.ServeHTTP(trafficResp, trafficReq)
	if trafficResp.Code != http.StatusOK {
		t.Fatalf("POST /api/traffic status = %d, want 200 body=%s", trafficResp.Code, trafficResp.Body.String())
	}

	userTrafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(trafficPayload))
	userTrafficReq.Header.Set("Content-Type", "application/json")
	userTrafficReq.Header.Set("X-Node-Token", "secret")
	userTrafficReq.Header.Set("X-Platform-Role", "user")
	userTrafficReq.Header.Set("X-Platform-Client-IDs", "1")
	userTrafficResp := httptest.NewRecorder()
	handler.ServeHTTP(userTrafficResp, userTrafficReq)
	if userTrafficResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/traffic as platform user status = %d, want 403 body=%s", userTrafficResp.Code, userTrafficResp.Body.String())
	}

	savedClient, err := file.GetDb().GetClient(1)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if savedClient.Flow == nil || savedClient.Flow.InletFlow != 10 || savedClient.Flow.ExportFlow != 20 {
		t.Fatalf("unexpected client flow: %+v", savedClient.Flow)
	}
	savedUser, err := file.GetDb().GetUser(1)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if savedUser.TotalFlow == nil || savedUser.TotalFlow.InletFlow != 10 || savedUser.TotalFlow.ExportFlow != 20 {
		t.Fatalf("unexpected user total flow: %+v", savedUser.TotalFlow)
	}

	usageReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	usageReq.Header.Set("X-Node-Token", "secret")
	usageResp := httptest.NewRecorder()
	handler.ServeHTTP(usageResp, usageReq)
	if usageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/usage-snapshot status = %d, want 200 body=%s", usageResp.Code, usageResp.Body.String())
	}
	usageBody := usageResp.Body.String()
	if !strings.Contains(usageBody, "\"schema_version\":1") ||
		!strings.Contains(usageBody, "\"api_base\":\"/api\"") ||
		!strings.Contains(usageBody, "\"summary\"") ||
		!strings.Contains(usageBody, "\"revisions\"") ||
		!strings.Contains(usageBody, "\"users\":4") ||
		!strings.Contains(usageBody, "\"clients\":6") ||
		!strings.Contains(usageBody, "\"owned_clients\":1") ||
		!strings.Contains(usageBody, "\"manager_user_ids\":[2,3]") ||
		!strings.Contains(usageBody, "\"max_tunnel_num\":9") {
		t.Fatalf("GET /api/system/usage-snapshot missing extended control metadata, got %s", usageBody)
	}
}
