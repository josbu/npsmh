package routers

import (
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
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeChangesPersistAcrossRuntimeRestart(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nnode_changes_window=256\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	runtimeA := NewRuntime(nil)

	addReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"persisted-change-client","remark":"persisted change"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addReq.Header.Set("X-Node-Token", "secret")
	addResp := httptest.NewRecorder()
	runtimeA.Handler.ServeHTTP(addResp, addReq)
	if addResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", addResp.Code, addResp.Body.String())
	}

	runtimeB := NewRuntime(nil)
	changesReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0", nil)
	changesReq.Header.Set("X-Node-Token", "secret")
	changesResp := httptest.NewRecorder()
	runtimeB.Handler.ServeHTTP(changesResp, changesReq)
	if changesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes after restart status = %d body=%s", changesResp.Code, changesResp.Body.String())
	}
	body := changesResp.Body.String()
	if !strings.Contains(body, "\"sequence\":1") || !strings.Contains(body, "\"name\":\"client.created\"") {
		t.Fatalf("GET /api/system/changes after restart should include persisted event, got %s", body)
	}
}

func TestNodeChangesIncludeSingleResourceControlEvents(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nnode_changes_window=256\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	user := createTestUser(t, 1, "tenant-a", "secret")
	client := createOwnedTestClient(t, 1, user.Id, "owned client")
	client.Flow = &file.Flow{}
	createTestTunnel(t, 1, client, 10001)
	createTestHost(t, 1, client, "owned.example.com")

	gin.SetMode(gin.TestMode)
	handler := Init()

	clientClearReq := httptest.NewRequest(http.MethodPost, "/api/clients/1/actions/clear", strings.NewReader(`{"mode":"flow"}`))
	clientClearReq.Header.Set("Content-Type", "application/json")
	clientClearReq.Header.Set("X-Node-Token", "secret")
	clientClearResp := httptest.NewRecorder()
	handler.ServeHTTP(clientClearResp, clientClearReq)
	if clientClearResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/1/actions/clear status = %d body=%s", clientClearResp.Code, clientClearResp.Body.String())
	}

	tunnelStopReq := httptest.NewRequest(http.MethodPost, "/api/tunnels/1/actions/stop", nil)
	tunnelStopReq.Header.Set("X-Node-Token", "secret")
	tunnelStopResp := httptest.NewRecorder()
	handler.ServeHTTP(tunnelStopResp, tunnelStopReq)
	if tunnelStopResp.Code != http.StatusOK {
		t.Fatalf("POST /api/tunnels/1/actions/stop status = %d body=%s", tunnelStopResp.Code, tunnelStopResp.Body.String())
	}

	hostClearReq := httptest.NewRequest(http.MethodPost, "/api/hosts/1/actions/clear", strings.NewReader(`{"mode":"flow"}`))
	hostClearReq.Header.Set("Content-Type", "application/json")
	hostClearReq.Header.Set("X-Node-Token", "secret")
	hostClearResp := httptest.NewRecorder()
	handler.ServeHTTP(hostClearResp, hostClearReq)
	if hostClearResp.Code != http.StatusOK {
		t.Fatalf("POST /api/hosts/1/actions/clear status = %d body=%s", hostClearResp.Code, hostClearResp.Body.String())
	}

	changesReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=20", nil)
	changesReq.Header.Set("X-Node-Token", "secret")
	changesResp := httptest.NewRecorder()
	handler.ServeHTTP(changesResp, changesReq)
	if changesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes status = %d body=%s", changesResp.Code, changesResp.Body.String())
	}

	var changesPayload struct {
		Count int            `json:"count"`
		Items []webapi.Event `json:"items"`
	}
	decodeManagementData(t, changesResp.Body.Bytes(), &changesPayload)
	if changesPayload.Count != 3 || len(changesPayload.Items) != 3 {
		t.Fatalf("unexpected changes payload = %+v", changesPayload)
	}

	if changesPayload.Items[0].Name != "client.cleared" || changesPayload.Items[0].Action != "clear" {
		t.Fatalf("first change = %+v, want client.cleared/clear", changesPayload.Items[0])
	}
	if got := changesPayload.Items[0].Fields["mode"]; got != "flow" {
		t.Fatalf("client.cleared mode = %#v, want flow", got)
	}
	if changesPayload.Items[1].Name != "tunnel.stopped" || changesPayload.Items[1].Action != "stop" {
		t.Fatalf("second change = %+v, want tunnel.stopped/stop", changesPayload.Items[1])
	}
	if changesPayload.Items[2].Name != "host.cleared" || changesPayload.Items[2].Action != "clear" {
		t.Fatalf("third change = %+v, want host.cleared/clear", changesPayload.Items[2])
	}
	if got := changesPayload.Items[2].Fields["mode"]; got != "flow" {
		t.Fatalf("host.cleared mode = %#v, want flow", got)
	}
}

func TestNodeIdempotencyPersistsAcrossRuntimeRestart(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nnode_idempotency_ttl_seconds=600\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	fakeStore := &fakeNodeStore{}
	oldStore := file.GlobalStore
	file.GlobalStore = fakeStore
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	runtimeA := NewRuntime(nil)
	firstReq := httptest.NewRequest(http.MethodPost, "/api/system/actions/sync", nil)
	firstReq.Header.Set("X-Node-Token", "secret")
	firstReq.Header.Set("Idempotency-Key", "persist-sync-1")
	firstReq.Header.Set("X-Request-ID", "persist-sync-request-1")
	firstResp := httptest.NewRecorder()
	runtimeA.Handler.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusOK {
		t.Fatalf("first POST /api/system/actions/sync status = %d body=%s", firstResp.Code, firstResp.Body.String())
	}
	if fakeStore.flushCalls != 1 {
		t.Fatalf("first POST /api/system/actions/sync should execute once, got %d", fakeStore.flushCalls)
	}

	runtimeB := NewRuntime(nil)
	secondReq := httptest.NewRequest(http.MethodPost, "/api/system/actions/sync", nil)
	secondReq.Header.Set("X-Node-Token", "secret")
	secondReq.Header.Set("Idempotency-Key", "persist-sync-1")
	secondReq.Header.Set("X-Request-ID", "persist-sync-request-2")
	secondResp := httptest.NewRecorder()
	runtimeB.Handler.ServeHTTP(secondResp, secondReq)
	if secondResp.Code != http.StatusOK {
		t.Fatalf("second POST /api/system/actions/sync after restart status = %d body=%s", secondResp.Code, secondResp.Body.String())
	}
	if secondResp.Result().Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("second POST /api/system/actions/sync after restart should be served from persisted idempotency cache, headers=%v", secondResp.Result().Header)
	}
	if fakeStore.flushCalls != 1 {
		t.Fatalf("persisted idempotency cache should prevent duplicate sync after restart, got %d executions", fakeStore.flushCalls)
	}
	if fakeStore.syncCalls != 0 {
		t.Fatalf("persisted idempotency replay should not require SyncNow(), got %d sync calls", fakeStore.syncCalls)
	}
}

func TestNodeChangesHTTPAndWSReplayEvents(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-changes",
		"platform_tokens=changes-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_changes",
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

	addReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"changes-client","remark":"tracked by changes"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addReq.Header.Set("X-Node-Token", "changes-secret")
	addReq.Header.Set("X-Platform-Role", "admin")
	addReq.Header.Set("X-Platform-Username", "platform-admin")
	addReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	addResp := httptest.NewRecorder()
	handler.ServeHTTP(addResp, addReq)
	if addResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients status = %d body=%s", addResp.Code, addResp.Body.String())
	}

	changesReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=10", nil)
	changesReq.Header.Set("X-Node-Token", "changes-secret")
	changesReq.Header.Set("X-Platform-Role", "admin")
	changesReq.Header.Set("X-Platform-Username", "platform-admin")
	changesReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	changesStart := time.Now().Unix()
	changesResp := httptest.NewRecorder()
	handler.ServeHTTP(changesResp, changesReq)
	if changesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes status = %d body=%s", changesResp.Code, changesResp.Body.String())
	}
	var changesPayload struct {
		Cursor       int64          `json:"cursor"`
		Count        int            `json:"count"`
		LastSequence int64          `json:"last_sequence"`
		NextAfter    int64          `json:"next_after"`
		HasMore      bool           `json:"has_more"`
		Items        []webapi.Event `json:"items"`
	}
	changesMeta := decodeManagementData(t, changesResp.Body.Bytes(), &changesPayload)
	if changesPayload.Count == 0 || len(changesPayload.Items) == 0 {
		t.Fatalf("unexpected changes payload: %+v", changesPayload)
	}
	if changesMeta.GeneratedAt < changesStart || changesMeta.GeneratedAt > time.Now().Unix()+1 {
		t.Fatalf("unexpected changes meta.generated_at=%d start=%d", changesMeta.GeneratedAt, changesStart)
	}
	first := changesPayload.Items[0]
	if first.Sequence <= 0 || first.Name != "client.created" || first.Resource != "client" {
		t.Fatalf("unexpected first change item: %+v", first)
	}
	if changesPayload.LastSequence != first.Sequence || changesPayload.NextAfter != first.Sequence {
		t.Fatalf("unexpected initial changes cursor hints: %+v", changesPayload)
	}
	cursor := changesPayload.Cursor

	changeStatusReq := httptest.NewRequest(http.MethodPost, "/api/clients/1/actions/status", strings.NewReader(`{"status":false}`))
	changeStatusReq.Header.Set("Content-Type", "application/json")
	changeStatusReq.Header.Set("X-Node-Token", "changes-secret")
	changeStatusReq.Header.Set("X-Platform-Role", "admin")
	changeStatusReq.Header.Set("X-Platform-Username", "platform-admin")
	changeStatusReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	changeStatusResp := httptest.NewRecorder()
	handler.ServeHTTP(changeStatusResp, changeStatusReq)
	if changeStatusResp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/1/actions/status status = %d body=%s", changeStatusResp.Code, changeStatusResp.Body.String())
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "changes-secret")
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
		ID:     "changes-req",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/changes?after=" + strconv.FormatInt(cursor, 10) + "&limit=10",
	}); err != nil {
		t.Fatalf("WriteJSON(changes request) error = %v", err)
	}
	wsChangesStart := time.Now().Unix()
	wsChangesResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "changes-req"
	})
	if wsChangesResp.Status != http.StatusOK {
		t.Fatalf("unexpected ws changes response: %+v body=%s", wsChangesResp, string(wsChangesResp.Body))
	}
	var wsPayload struct {
		Cursor int64          `json:"cursor"`
		Count  int            `json:"count"`
		Items  []webapi.Event `json:"items"`
	}
	wsChangesMeta := decodeManagementFrameData(t, wsChangesResp, &wsPayload)
	if wsPayload.Count == 0 || len(wsPayload.Items) == 0 {
		t.Fatalf("unexpected ws changes payload: %+v", wsPayload)
	}
	if wsChangesMeta.GeneratedAt < wsChangesStart || wsChangesMeta.GeneratedAt > time.Now().Unix()+1 {
		t.Fatalf("unexpected ws changes meta.generated_at=%d start=%d", wsChangesMeta.GeneratedAt, wsChangesStart)
	}
	last := wsPayload.Items[len(wsPayload.Items)-1]
	if last.Name != "client.status_changed" || last.Sequence <= cursor {
		t.Fatalf("unexpected ws replay event: %+v cursor=%d", last, cursor)
	}

	limitedChangesReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=1", nil)
	limitedChangesReq.Header.Set("X-Node-Token", "changes-secret")
	limitedChangesReq.Header.Set("X-Platform-Role", "admin")
	limitedChangesReq.Header.Set("X-Platform-Username", "platform-admin")
	limitedChangesReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	limitedChangesResp := httptest.NewRecorder()
	handler.ServeHTTP(limitedChangesResp, limitedChangesReq)
	if limitedChangesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes limit=1 status = %d body=%s", limitedChangesResp.Code, limitedChangesResp.Body.String())
	}
	var limitedPayload struct {
		Count        int            `json:"count"`
		LastSequence int64          `json:"last_sequence"`
		NextAfter    int64          `json:"next_after"`
		HasMore      bool           `json:"has_more"`
		Items        []webapi.Event `json:"items"`
	}
	decodeManagementData(t, limitedChangesResp.Body.Bytes(), &limitedPayload)
	if limitedPayload.Count != 1 || len(limitedPayload.Items) != 1 || !limitedPayload.HasMore {
		t.Fatalf("unexpected limited changes payload: %+v", limitedPayload)
	}
	if limitedPayload.LastSequence != limitedPayload.Items[0].Sequence || limitedPayload.NextAfter != limitedPayload.Items[0].Sequence {
		t.Fatalf("unexpected limited changes cursor hints: %+v", limitedPayload)
	}
}
