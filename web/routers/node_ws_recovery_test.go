package routers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeWebSocketRequestForwardingSupportsFormalClientMutationRoute(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	user := createTestUser(t, 1, "tenant", "secret")
	client := createOwnedTestClient(t, 1, user.Id, "original remark")

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "secret")
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
		ID:     "update-client-1",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/clients/1/actions/update",
		Body:   json.RawMessage(`{"verify_key":"` + client.VerifyKey + `","remark":"edited via ws formal route","manager_user_ids":[2,3]}`),
	}); err != nil {
		t.Fatalf("WriteJSON(update request) error = %v", err)
	}

	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "update-client-1"
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("unexpected ws update response: %+v body=%s", resp, string(resp.Body))
	}

	savedClient, err := file.GetDb().GetClient(1)
	if err != nil {
		t.Fatalf("GetClient(1) error = %v", err)
	}
	if savedClient.Remark != "edited via ws formal route" {
		t.Fatalf("client remark = %q, want edited via ws formal route", savedClient.Remark)
	}
	if len(savedClient.ManagerUserIDs) != 2 || savedClient.ManagerUserIDs[0] != 2 || savedClient.ManagerUserIDs[1] != 3 {
		t.Fatalf("client manager_user_ids = %v, want [2 3]", savedClient.ManagerUserIDs)
	}
}

func TestNodeReverseHelloUsesDurableReplayHistory(t *testing.T) {
	resetTestDB(t)
	platformConn := make(chan *websocket.Conn, 1)
	platformSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		platformConn <- conn
	}))
	defer platformSrv.Close()

	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-reverse",
		"platform_tokens=reverse-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_reverse",
		"platform_connect_modes=dual",
		"platform_reverse_ws_urls=ws" + strings.TrimPrefix(platformSrv.URL, "http") + "/reverse/ws",
		"platform_reverse_enabled=true",
		"platform_reverse_heartbeat_seconds=30",
		"node_changes_window=100",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	runtime := NewRuntime(nil)
	defer runtime.Stop()
	stopReverse := StartNodeReverseConnectors(runtime.State)
	defer stopReverse()

	var conn *websocket.Conn
	select {
	case conn = <-platformConn:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reverse websocket connection")
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(reverse hello) error = %v", err)
	}

	bootID := strings.TrimSpace(runtime.State.RuntimeIdentity().BootID())
	if bootID == "" {
		t.Fatal("runtime boot id is empty")
	}
	for id := 1; id <= 110; id++ {
		runtime.State.NodeEventLog.Record(webapi.Event{
			Name:     "client.created",
			Resource: "client",
			Action:   "create",
			Fields: map[string]interface{}{
				"id": id,
			},
		})
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:   "reverse-hello-durable",
		Type: "hello",
		Body: json.RawMessage(`{"last_boot_id":"` + bootID + `","changes_after":0,"changes_limit":200}`),
	}); err != nil {
		t.Fatalf("WriteJSON(reverse durable hello) error = %v", err)
	}

	ackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "hello_ack" && frame.ID == "reverse-hello-durable"
	})
	var ack nodeReverseHelloAck
	if err := json.Unmarshal(ackFrame.Body, &ack); err != nil {
		t.Fatalf("json.Unmarshal(reverse hello ack) error = %v body=%s", err, string(ackFrame.Body))
	}
	if ack.ResyncRequired {
		t.Fatalf("reverse hello ack resync_required = true, want false body=%s", string(ackFrame.Body))
	}
	if ack.Replay.Count != 110 || len(ack.Replay.Items) != 110 || ack.Replay.OldestCursor != 1 {
		t.Fatalf("unexpected reverse durable replay payload: %+v", ack.Replay)
	}
	if ack.Replay.Items[0].Sequence != 1 {
		t.Fatalf("reverse durable replay first sequence = %d, want 1", ack.Replay.Items[0].Sequence)
	}
}

func TestNodeReverseHelloStillWorksAfterGraceWithoutRealtimeTraffic(t *testing.T) {
	resetTestDB(t)
	platformConn := make(chan *websocket.Conn, 1)
	platformSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		platformConn <- conn
	}))
	defer platformSrv.Close()

	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-reverse",
		"platform_tokens=reverse-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_reverse",
		"platform_connect_modes=dual",
		"platform_reverse_ws_urls=ws" + strings.TrimPrefix(platformSrv.URL, "http") + "/reverse/ws",
		"platform_reverse_enabled=true",
		"platform_reverse_heartbeat_seconds=30",
		"node_changes_window=32",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	runtime := NewRuntime(nil)
	defer runtime.Stop()
	stopReverse := StartNodeReverseConnectors(runtime.State)
	defer stopReverse()

	var conn *websocket.Conn
	select {
	case conn = <-platformConn:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reverse websocket connection")
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(reverse hello) error = %v", err)
	}

	time.Sleep(nodeReverseHelloGracePeriod + 200*time.Millisecond)

	bootID := strings.TrimSpace(runtime.State.RuntimeIdentity().BootID())
	if bootID == "" {
		t.Fatal("runtime boot id is empty")
	}
	if err := conn.WriteJSON(nodeWSFrame{
		ID:   "reverse-hello-after-grace",
		Type: "hello",
		Body: json.RawMessage(`{"last_boot_id":"` + bootID + `","changes_after":0,"changes_limit":10}`),
	}); err != nil {
		t.Fatalf("WriteJSON(reverse hello after grace) error = %v", err)
	}

	ackFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "hello_ack" && frame.ID == "reverse-hello-after-grace"
	})
	var ack nodeReverseHelloAck
	if err := json.Unmarshal(ackFrame.Body, &ack); err != nil {
		t.Fatalf("json.Unmarshal(reverse hello after grace ack) error = %v body=%s", err, string(ackFrame.Body))
	}
	if ack.ResyncRequired {
		t.Fatalf("reverse hello after grace resync_required = true, want false body=%s", string(ackFrame.Body))
	}
}

func TestConfigImportInvalidatesReverseRealtimeSessions(t *testing.T) {
	resetTestDB(t)
	platformConn := make(chan *websocket.Conn, 1)
	platformSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := nodeWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		platformConn <- conn
	}))
	defer platformSrv.Close()

	loadRecoveredNodeConfig(t, strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-reverse",
		"platform_tokens=reverse-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_reverse",
		"platform_connect_modes=dual",
		"platform_reverse_ws_urls=ws" + strings.TrimPrefix(platformSrv.URL, "http") + "/reverse/ws",
		"platform_reverse_enabled=true",
		"platform_reverse_heartbeat_seconds=30",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	runtime := NewRuntime(nil)
	defer runtime.Stop()
	stopReverse := StartNodeReverseConnectors(runtime.State)
	defer stopReverse()

	var conn *websocket.Conn
	select {
	case conn = <-platformConn:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reverse websocket connection")
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(reverse hello) error = %v", err)
	}

	importReq := httptest.NewRequest(http.MethodPost, "/api/system/import", bytes.NewReader(recoveredNodeImportSnapshot(t)))
	importReq.Header.Set("Content-Type", "application/json")
	importReq.Header.Set("X-Node-Token", "reverse-secret")
	importResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(importResp, importReq)
	if importResp.Code != http.StatusOK {
		t.Fatalf("POST /api/system/import status = %d, want 200 body=%s", importResp.Code, importResp.Body.String())
	}
	var importPayload nodeConfigImportResponse
	decodeManagementData(t, importResp.Body.Bytes(), &importPayload)

	epochChanged := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "epoch_changed"
	})
	if !strings.Contains(string(epochChanged.Body), "\"config_epoch\":\""+importPayload.ConfigEpoch+"\"") ||
		!strings.Contains(string(epochChanged.Body), "\"reason\":\"config_import\"") {
		t.Fatalf("unexpected reverse epoch_changed frame: %+v body=%s", epochChanged, string(epochChanged.Body))
	}

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var closed nodeWSFrame
	if err := conn.ReadJSON(&closed); err == nil {
		t.Fatalf("expected reverse realtime websocket to close after config import, got %+v", closed)
	}
}
