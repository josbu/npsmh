package routers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeBatchHTTPAcceptsJSONBodyTokenAndDelegatedActorContext(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-a",
		"platform_tokens=token-a",
		"platform_scopes=account",
		"platform_enabled=true",
		"platform_service_users=svc_master_a",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	vkey := "batch-json-client-" + strconvFormatInt(time.Now().UnixNano())
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(`{"items":[{"id":"add-1","method":"POST","path":"/api/clients","body":{"verify_key":"`+vkey+`","remark":"created by delegated batch"}},{"id":"config-1","method":"GET","path":"/api/system/export"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "token-a")
	req.Header.Set("X-Platform-Actor-ID", "delegated-user-1")
	req.Header.Set("X-Platform-Username", "delegated-user")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/batch status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}

	var payload nodeBatchResponsePayload
	decodeManagementData(t, resp.Body.Bytes(), &payload)
	if payload.Count != 2 || len(payload.Items) != 2 {
		t.Fatalf("unexpected batch payload: %+v", payload)
	}
	if payload.Items[0].ID != "add-1" || payload.Items[0].Status != http.StatusOK {
		t.Fatalf("unexpected add item: %+v body=%s", payload.Items[0], string(payload.Items[0].Body))
	}
	if payload.Items[1].ID != "config-1" || payload.Items[1].Status != http.StatusForbidden {
		t.Fatalf("unexpected config item: %+v body=%s", payload.Items[1], string(payload.Items[1].Body))
	}

	var addPayload struct {
		ID int `json:"id"`
	}
	decodeManagementData(t, payload.Items[0].Body, &addPayload)
	createdClient, err := file.GetDb().GetClient(addPayload.ID)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", addPayload.ID, err)
	}
	if createdClient == nil || createdClient.VerifyKey != vkey {
		t.Fatalf("unexpected created client: %+v", createdClient)
	}
	if createdClient.SourceType != "platform_user" || createdClient.SourcePlatformID != "master-a" || createdClient.SourceActorID != "delegated-user-1" {
		t.Fatalf("created client source meta = %q/%q/%q, want platform_user/master-a/delegated-user-1", createdClient.SourceType, createdClient.SourcePlatformID, createdClient.SourceActorID)
	}
}

func TestNodeChangesDurableQueryExtendsReplayWindowOverHTTPAndWS(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-durable",
		"platform_tokens=durable-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_durable",
		"node_changes_window=100",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	for id := 1; id <= 110; id++ {
		req := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"durable-client-`+strconvFormatInt(int64(id))+`","remark":"durable event"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Token", "durable-secret")
		req.Header.Set("X-Platform-Role", "admin")
		req.Header.Set("X-Platform-Username", "platform-admin")
		req.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("POST /api/clients #%d status = %d body=%s", id, resp.Code, resp.Body.String())
		}
	}

	httpReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=200", nil)
	httpReq.Header.Set("X-Node-Token", "durable-secret")
	httpReq.Header.Set("X-Platform-Role", "admin")
	httpReq.Header.Set("X-Platform-Username", "platform-admin")
	httpReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	httpResp := httptest.NewRecorder()
	handler.ServeHTTP(httpResp, httpReq)
	if httpResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes status = %d body=%s", httpResp.Code, httpResp.Body.String())
	}
	var livePayload nodeEventLogSnapshot
	decodeManagementData(t, httpResp.Body.Bytes(), &livePayload)
	if livePayload.Count != 100 || len(livePayload.Items) != 100 || livePayload.OldestCursor != 11 {
		t.Fatalf("unexpected live changes payload: %+v", livePayload)
	}

	durableReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=200&durable=1", nil)
	durableReq.Header.Set("X-Node-Token", "durable-secret")
	durableReq.Header.Set("X-Platform-Role", "admin")
	durableReq.Header.Set("X-Platform-Username", "platform-admin")
	durableReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	durableResp := httptest.NewRecorder()
	handler.ServeHTTP(durableResp, durableReq)
	if durableResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes durable=1 status = %d body=%s", durableResp.Code, durableResp.Body.String())
	}
	var durablePayload nodeEventLogSnapshot
	decodeManagementData(t, durableResp.Body.Bytes(), &durablePayload)
	if durablePayload.Count != 110 || len(durablePayload.Items) != 110 || durablePayload.OldestCursor != 1 {
		t.Fatalf("unexpected durable changes payload: %+v", durablePayload)
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "durable-secret")
	headers.Set("X-Platform-Role", "admin")
	headers.Set("X-Platform-Username", "platform-admin")
	headers.Set("X-Platform-Actor-ID", "platform-admin-1")
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
		ID:     "history-1",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/changes?after=0&limit=200&history=1",
	}); err != nil {
		t.Fatalf("WriteJSON(history request) error = %v", err)
	}
	wsResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "history-1"
	})
	if wsResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws durable replay response: %+v body=%s", wsResp, string(wsResp.Body))
	}
	var wsPayload nodeEventLogSnapshot
	decodeManagementFrameData(t, wsResp, &wsPayload)
	if wsPayload.Count != 110 || len(wsPayload.Items) != 110 || wsPayload.OldestCursor != 1 {
		t.Fatalf("unexpected ws durable replay payload: %+v", wsPayload)
	}
}
