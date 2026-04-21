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

func TestNodeBatchHTTPDispatchesMixedRequests(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-batch",
		"platform_tokens=batch-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_batch",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	payload := `{"items":[{"id":"status-1","method":"GET","path":"/api/system/status"},{"id":"add-1","method":"POST","path":"/api/clients","body":{"verify_key":"batch-http-client","remark":"created via batch"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "batch-secret")
	req.Header.Set("X-Platform-Role", "admin")
	req.Header.Set("X-Platform-Username", "platform-admin")
	req.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/batch status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	var batchResp nodeBatchResponsePayload
	decodeManagementData(t, resp.Body.Bytes(), &batchResp)
	if batchResp.Count != 2 || len(batchResp.Items) != 2 {
		t.Fatalf("unexpected batch response payload: %+v", batchResp)
	}
	if batchResp.Items[0].ID != "status-1" || batchResp.Items[0].Status != http.StatusOK {
		t.Fatalf("unexpected status item: %+v", batchResp.Items[0])
	}
	if batchResp.Items[1].ID != "add-1" || batchResp.Items[1].Status != http.StatusOK {
		t.Fatalf("unexpected add item: %+v body=%s", batchResp.Items[1], string(batchResp.Items[1].Body))
	}

	var addPayload struct {
		ID int `json:"id"`
	}
	decodeManagementData(t, batchResp.Items[1].Body, &addPayload)
	if addPayload.ID <= 0 {
		t.Fatalf("unexpected add payload: %+v", addPayload)
	}
	createdClient, err := file.GetDb().GetClient(addPayload.ID)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", addPayload.ID, err)
	}
	if createdClient == nil || createdClient.VerifyKey != "batch-http-client" {
		t.Fatalf("unexpected created client: %+v", createdClient)
	}
}

func TestNodeBatchHTTPSupportsTopLevelJSONArrayBody(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-batch-array",
		"platform_tokens=batch-array-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_batch_array",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	payload := `[{"id":"status-1","method":"GET","path":"/api/system/status"}]`
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "batch-array-secret")
	req.Header.Set("X-Platform-Role", "admin")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/batch with top-level array status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "\"count\":1") || !strings.Contains(resp.Body.String(), "\"id\":\"status-1\"") {
		t.Fatalf("unexpected batch response for top-level array body: %s", resp.Body.String())
	}
}

func TestNodeBatchHTTPRespectsConfiguredLimit(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-batch-limit",
		"platform_tokens=batch-limit-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_batch_limit",
		"node_batch_max_items=1",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	payload := `{"items":[{"id":"status-1","method":"GET","path":"/api/system/status"},{"id":"status-2","method":"GET","path":"/api/system/status"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "batch-limit-secret")
	req.Header.Set("X-Platform-Role", "admin")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/batch with configured limit status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "batch items exceed limit") {
		t.Fatalf("POST /api/batch with configured limit body = %s", resp.Body.String())
	}
}

func TestNodeBatchHTTPRejectsLegacyFormItemsPayload(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-batch-form",
		"platform_tokens=batch-form-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_batch_form",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader("items=%5B%7B%22id%22%3A%22status-1%22%2C%22method%22%3A%22GET%22%2C%22path%22%3A%22%2Fapi%2Fsystem%2Fstatus%22%7D%5D"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Node-Token", "batch-form-secret")
	req.Header.Set("X-Platform-Role", "admin")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/batch with legacy form items status = %d, want 400 body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "\"code\":\"invalid_json_body\"") {
		t.Fatalf("POST /api/batch with legacy form items should use formal management error, got %s", resp.Body.String())
	}
}

func TestNodeBatchHTTPRejectsMissingRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(`{"items":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	nodeBatchHTTPHandler(nil)(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("nodeBatchHTTPHandler(nil state) status = %d, want 500 body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "\"code\":\"node_state_unavailable\"") || !strings.Contains(body, "\"message\":\"node state is unavailable\"") {
		t.Fatalf("nodeBatchHTTPHandler(nil state) body = %s, want node_state_unavailable", body)
	}
}

func TestNodeBatchHTTPRejectsUnsupportedItemMethod(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-batch-method",
		"platform_tokens=batch-method-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_batch_method",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	payload := `{"items":[{"id":"status-1","method":"PATCH","path":"/api/system/status"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "batch-method-secret")
	req.Header.Set("X-Platform-Role", "admin")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/batch with unsupported item method status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	var batchResp nodeBatchResponsePayload
	decodeManagementData(t, resp.Body.Bytes(), &batchResp)
	if batchResp.Count != 1 || len(batchResp.Items) != 1 {
		t.Fatalf("unexpected batch response payload: %+v", batchResp)
	}
	if batchResp.Items[0].Status != http.StatusBadRequest || batchResp.Items[0].Error != "unsupported request method" {
		t.Fatalf("unexpected batch item response: %+v", batchResp.Items[0])
	}
}

func TestNodeBatchHTTPRejectsRealtimeSubscriptionPaths(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-batch-realtime",
		"platform_tokens=batch-realtime-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_batch_realtime",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	payload := `{"items":[{"id":"subs-1","method":"GET","path":"/api/realtime/subscriptions"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "batch-realtime-secret")
	req.Header.Set("X-Platform-Role", "admin")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/batch with realtime path status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	var batchResp nodeBatchResponsePayload
	decodeManagementData(t, resp.Body.Bytes(), &batchResp)
	if batchResp.Count != 1 || len(batchResp.Items) != 1 {
		t.Fatalf("unexpected batch response payload: %+v", batchResp)
	}
	if batchResp.Items[0].Status != http.StatusBadRequest || batchResp.Items[0].Error != "nested batch or websocket dispatch is not supported" {
		t.Fatalf("unexpected batch realtime item response: %+v", batchResp.Items[0])
	}
}
