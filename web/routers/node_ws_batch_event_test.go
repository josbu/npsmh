package routers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeWebSocketPublishesScopedEvents(t *testing.T) {
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
	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "token-a")
	headers.Set("X-Platform-Role", "admin")
	headers.Set("X-Platform-Actor-ID", "platform-admin-1")
	headers.Set("X-Platform-Username", "platform-admin")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	payload := `{"verify_key":"svc-client-created","remark":"from master account"}`
	req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "token-a")
	req.Header.Set("X-Platform-Role", "admin")
	req.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	req.Header.Set("X-Platform-Username", "platform-admin")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	for {
		var frame nodeWSFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON(event) error = %v", err)
		}
		if frame.Type != "event" {
			continue
		}
		body := string(frame.Body)
		if !strings.Contains(body, "\"name\":\"client.created\"") || !strings.Contains(body, "\"resource\":\"client\"") {
			t.Fatalf("unexpected event body = %s", body)
		}
		if !strings.Contains(body, "\"action\":\"create\"") {
			t.Fatalf("expected create action in event body, got %s", body)
		}
		break
	}
}

func TestNodeWebSocketBatchDispatchesRequests(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-batch",
		"platform_tokens=ws-batch-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_batch",
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
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "ws-batch-secret")
	headers.Set("X-Platform-Role", "admin")
	headers.Set("X-Platform-Actor-ID", "platform-admin-1")
	headers.Set("X-Platform-Username", "platform-admin")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "batch-req",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/batch",
		Body:   json.RawMessage(`{"items":[{"id":"status-sub","method":"GET","path":"/api/system/status"},{"id":"add-sub","method":"POST","path":"/api/clients","body":{"verify_key":"batch-ws-client","remark":"created via ws batch"}}]}`),
	}); err != nil {
		t.Fatalf("WriteJSON(batch request) error = %v", err)
	}

	batchResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "batch-req"
	})
	if batchResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws batch response: %+v body=%s", batchResp, string(batchResp.Body))
	}
	var payload nodeBatchResponsePayload
	decodeManagementFrameData(t, batchResp, &payload)
	if payload.Count != 2 || len(payload.Items) != 2 {
		t.Fatalf("unexpected ws batch payload: %+v", payload)
	}
	if payload.Items[0].ID != "status-sub" || payload.Items[0].Status != http.StatusOK {
		t.Fatalf("unexpected ws batch status item: %+v", payload.Items[0])
	}
	if payload.Items[1].ID != "add-sub" || payload.Items[1].Status != http.StatusOK {
		t.Fatalf("unexpected ws batch add item: %+v body=%s", payload.Items[1], string(payload.Items[1].Body))
	}
}

func TestNodeWebSocketBatchSupportsTopLevelJSONArrayBody(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-batch-array",
		"platform_tokens=ws-batch-array-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_batch_array",
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
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "ws-batch-array-secret")
	headers.Set("X-Platform-Role", "admin")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "batch-req-array",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/batch",
		Body:   json.RawMessage(`[{"id":"status-sub","method":"GET","path":"/api/system/status"}]`),
	}); err != nil {
		t.Fatalf("WriteJSON(batch array request) error = %v", err)
	}

	batchResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "batch-req-array"
	})
	if batchResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws batch array response: %+v body=%s", batchResp, string(batchResp.Body))
	}
	var payload nodeBatchResponsePayload
	decodeManagementFrameData(t, batchResp, &payload)
	if payload.Count != 1 || len(payload.Items) != 1 || payload.Items[0].ID != "status-sub" || payload.Items[0].Status != http.StatusOK {
		t.Fatalf("unexpected ws batch array payload: %+v", payload)
	}
}

func TestNodeWebSocketBatchRejectsRealtimeSubscriptionPaths(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-batch-realtime",
		"platform_tokens=ws-batch-realtime-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_batch_realtime",
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
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "ws-batch-realtime-secret")
	headers.Set("X-Platform-Role", "admin")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "batch-req-realtime",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/batch",
		Body:   json.RawMessage(`{"items":[{"id":"subs-sub","method":"GET","path":"/api/realtime/subscriptions"}]}`),
	}); err != nil {
		t.Fatalf("WriteJSON(batch realtime request) error = %v", err)
	}

	batchResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "batch-req-realtime"
	})
	if batchResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws batch realtime response: %+v body=%s", batchResp, string(batchResp.Body))
	}
	var payload nodeBatchResponsePayload
	decodeManagementFrameData(t, batchResp, &payload)
	if payload.Count != 1 || len(payload.Items) != 1 {
		t.Fatalf("unexpected ws batch realtime payload: %+v", payload)
	}
	if payload.Items[0].Status != http.StatusBadRequest || payload.Items[0].Error != "nested batch or websocket dispatch is not supported" {
		t.Fatalf("unexpected ws batch realtime item: %+v", payload.Items[0])
	}
}

func TestNodeWebSocketManageClientEditSupportsManagerUserIDArraysAndClear(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-edit",
		"platform_tokens=ws-edit-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_edit",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if err := file.GetDb().NewClient(&file.Client{
		Id:        1,
		VerifyKey: "ws-managers-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "ws-edit-secret")
	headers.Set("X-Platform-Role", "admin")
	headers.Set("X-Platform-Actor-ID", "platform-admin-1")
	headers.Set("X-Platform-Username", "platform-admin")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "edit-managers",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/clients/1/actions/update",
		Body:   json.RawMessage(`{"verify_key":"ws-managers-client","remark":"edited via ws","manager_user_ids":[2,3]}`),
	}); err != nil {
		t.Fatalf("WriteJSON(edit request) error = %v", err)
	}

	editResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "edit-managers"
	})
	if editResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws edit response: %+v body=%s", editResp, string(editResp.Body))
	}

	editedClient, err := file.GetDb().GetClient(1)
	if err != nil {
		t.Fatalf("GetClient(1) after manager edit error = %v", err)
	}
	if !reflect.DeepEqual(editedClient.ManagerUserIDs, []int{2, 3}) {
		t.Fatalf("manager_user_ids after ws edit = %v, want [2 3]", editedClient.ManagerUserIDs)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "clear-managers",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/clients/1/actions/update",
		Body:   json.RawMessage(`{"verify_key":"ws-managers-client","remark":"cleared via ws","manager_user_ids":[]}`),
	}); err != nil {
		t.Fatalf("WriteJSON(clear request) error = %v", err)
	}

	clearResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "clear-managers"
	})
	if clearResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws clear response: %+v body=%s", clearResp, string(clearResp.Body))
	}

	clearedClient, err := file.GetDb().GetClient(1)
	if err != nil {
		t.Fatalf("GetClient(1) after manager clear error = %v", err)
	}
	if len(clearedClient.ManagerUserIDs) != 0 {
		t.Fatalf("manager_user_ids after ws clear = %v, want empty", clearedClient.ManagerUserIDs)
	}
}

func TestNodeWebSocketPublishesScopedDeleteEventsWithFallbackFields(t *testing.T) {
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
		VerifyKey: "svc-delete-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	client.SetOwnerUserID(serviceUser.Id)
	client.ManagerUserIDs = []int{99}
	client.TouchMeta("platform_admin", "master-a", "platform-admin-1")
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	host := &file.Host{
		Id:       1,
		Host:     "svc-delete.example.com",
		Location: "/",
		Scheme:   "http",
		Client:   client,
		Flow:     &file.Flow{},
		Target: &file.Target{
			TargetStr: "127.0.0.1:8080",
		},
	}
	host.TouchMeta()
	if err := file.GetDb().NewHost(host); err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "token-a")
	headers.Set("X-Platform-Role", "admin")
	headers.Set("X-Platform-Actor-ID", "platform-admin-1")
	headers.Set("X-Platform-Username", "platform-admin")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}

	deleteHostReq := httptest.NewRequest(http.MethodPost, "/api/hosts/1/actions/delete", nil)
	deleteHostReq.Header.Set("X-Node-Token", "token-a")
	deleteHostReq.Header.Set("X-Platform-Role", "admin")
	deleteHostReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	deleteHostReq.Header.Set("X-Platform-Username", "platform-admin")
	deleteHostResp := httptest.NewRecorder()
	handler.ServeHTTP(deleteHostResp, deleteHostReq)
	if deleteHostResp.Code != http.StatusOK {
		t.Fatalf("POST /api/hosts/1/actions/delete status = %d body=%s", deleteHostResp.Code, deleteHostResp.Body.String())
	}

	hostDeleteFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "event" &&
			strings.Contains(string(frame.Body), "\"name\":\"host.deleted\"")
	})
	hostDeleteBody := string(hostDeleteFrame.Body)
	if !strings.Contains(hostDeleteBody, "\"client_id\":1") || !strings.Contains(hostDeleteBody, "\"owner_user_id\":"+strconv.Itoa(serviceUser.Id)) {
		t.Fatalf("unexpected host delete event body = %s", hostDeleteBody)
	}

	deleteClientReq := httptest.NewRequest(http.MethodPost, "/api/clients/1/actions/delete", nil)
	deleteClientReq.Header.Set("X-Node-Token", "token-a")
	deleteClientReq.Header.Set("X-Platform-Role", "admin")
	deleteClientReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	deleteClientReq.Header.Set("X-Platform-Username", "platform-admin")
	deleteClientResp := httptest.NewRecorder()
	handler.ServeHTTP(deleteClientResp, deleteClientReq)
	if deleteClientResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/1/actions/delete status = %d body=%s", deleteClientResp.Code, deleteClientResp.Body.String())
	}

	clientDeleteFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "event" &&
			strings.Contains(string(frame.Body), "\"name\":\"client.deleted\"")
	})
	clientDeleteBody := string(clientDeleteFrame.Body)
	if !strings.Contains(clientDeleteBody, "\"owner_user_id\":"+strconv.Itoa(serviceUser.Id)) ||
		!strings.Contains(clientDeleteBody, "\"source_platform_id\":\"master-a\"") ||
		!strings.Contains(clientDeleteBody, "\"manager_user_ids\":[99]") {
		t.Fatalf("unexpected client delete event body = %s", clientDeleteBody)
	}
}
