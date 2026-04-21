package routers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestInitNodeKickRequiresToken(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nnode_changes_window=2048\nnode_batch_max_items=80\nnode_idempotency_ttl_seconds=600\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = &fakeNodeStore{}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	pageReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	pageResp := httptest.NewRecorder()
	handler.ServeHTTP(pageResp, pageReq)
	if pageResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery in node mode status = %d, want 200", pageResp.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/clients/actions/kick", strings.NewReader(`{"verify_key":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/clients/actions/kick status = %d, want 401", resp.Code)
	}
	if body := resp.Body.String(); !strings.Contains(body, "unauthorized") && !strings.Contains(body, "invalid node token") {
		t.Fatalf("POST /api/clients/actions/kick body = %s, want unauthorized or invalid node token", body)
	}
}

func TestInitNodeKickByVKey(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nnode_changes_window=2048\nnode_batch_max_items=80\nnode_idempotency_ttl_seconds=600\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = &fakeNodeStore{
		clientsByID: map[int]*file.Client{
			7: {Id: 7, VerifyKey: "demo", Status: true},
		},
		clientsByVKey: map[string]*file.Client{
			"demo": {Id: 7, VerifyKey: "demo", Status: true},
		},
	}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/clients/actions/kick", strings.NewReader(`{"verify_key":"demo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/clients/actions/kick status = %d, want 200", resp.Code)
	}
	var payload struct {
		Message  string `json:"message"`
		ClientID int    `json:"client_id"`
	}
	decodeManagementData(t, resp.Body.Bytes(), &payload)
	if payload.Message != "kick success" || payload.ClientID != 7 {
		t.Fatalf("POST /api/clients/actions/kick payload = %+v", payload)
	}
	if strings.Contains(resp.Body.String(), `"verify_key":"demo"`) {
		t.Fatalf("POST /api/clients/actions/kick should not echo verify_key, got %s", resp.Body.String())
	}
	if client := file.GlobalStore.(*fakeNodeStore).clientsByVKey["demo"]; client == nil || client.Status {
		t.Fatalf("POST /api/clients/actions/kick should disable client in memory, got %+v", client)
	}

	changesReq := httptest.NewRequest(http.MethodGet, "/api/system/changes?after=0&limit=10", nil)
	changesReq.Header.Set("X-Node-Token", "secret")
	changesResp := httptest.NewRecorder()
	handler.ServeHTTP(changesResp, changesReq)
	if changesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/changes status = %d body=%s", changesResp.Code, changesResp.Body.String())
	}
	if !strings.Contains(changesResp.Body.String(), `"name":"node.client.kicked"`) {
		t.Fatalf("GET /api/system/changes should include node.client.kicked, got %s", changesResp.Body.String())
	}
	if strings.Contains(changesResp.Body.String(), `"verify_key":"demo"`) {
		t.Fatalf("GET /api/system/changes should not leak kicked client verify_key, got %s", changesResp.Body.String())
	}
}

func TestInitNodeSyncWithBearerToken(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nnode_changes_window=2048\nnode_batch_max_items=80\nnode_idempotency_ttl_seconds=600\nweb_username=admin\nweb_password=secret\n")
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
	handler := Init()

	req := httptest.NewRequest(http.MethodPost, "/api/system/actions/sync", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("POST /api/system/actions/sync status = %d, want 200", resp.Code)
	}
	if !fakeStore.flushCalled {
		t.Fatalf("POST /api/system/actions/sync should flush node-local state")
	}
	if fakeStore.syncCalled {
		t.Fatalf("POST /api/system/actions/sync should not call SyncNow()")
	}
	if !strings.Contains(resp.Body.String(), "\"message\":\"sync success\"") {
		t.Fatalf("POST /api/system/actions/sync body = %s", resp.Body.String())
	}
}

func TestNodeSyncIdempotencyDoesNotCacheServerErrors(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	fakeStore := &fakeNodeStore{flushErr: errors.New("boom")}
	oldStore := file.GlobalStore
	file.GlobalStore = fakeStore
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	makeReq := func(requestID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/system/actions/sync", nil)
		req.Header.Set("X-Node-Token", "secret")
		req.Header.Set("Idempotency-Key", "sync-1")
		req.Header.Set("X-Request-ID", requestID)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}

	resp1 := makeReq("sync-err-1")
	if resp1.Code != http.StatusInternalServerError {
		t.Fatalf("first POST /api/system/actions/sync status = %d, want 500 body=%s", resp1.Code, resp1.Body.String())
	}
	resp2 := makeReq("sync-err-2")
	if resp2.Code != http.StatusInternalServerError {
		t.Fatalf("second POST /api/system/actions/sync status = %d, want 500 body=%s", resp2.Code, resp2.Body.String())
	}
	if replay := resp2.Result().Header.Get("X-Idempotent-Replay"); replay != "" {
		t.Fatalf("second POST /api/system/actions/sync should not be served from idempotency cache on 5xx, got replay header %q", replay)
	}
	if fakeStore.flushCalls != 2 {
		t.Fatalf("POST /api/system/actions/sync with 5xx should execute twice, got %d", fakeStore.flushCalls)
	}
}

func TestInitNodeStatusRejectsQueryToken(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = &fakeNodeStore{}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/system/status?token=secret", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/system/status?token=secret status = %d, want 401 body=%s", resp.Code, resp.Body.String())
	}
}

func TestNodeTrafficIdempotencyKeyPreventsReplay(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	client := &file.Client{Id: 7, VerifyKey: "demo", Status: true, Flow: new(file.Flow)}
	fakeStore := &fakeNodeStore{
		clientsByID: map[int]*file.Client{
			7: client,
		},
		clientsByVKey: map[string]*file.Client{
			"demo": client,
		},
	}
	oldStore := file.GlobalStore
	file.GlobalStore = fakeStore
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	makeReq := func(body, requestID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Node-Token", "secret")
		req.Header.Set("Idempotency-Key", "traffic-1")
		req.Header.Set("X-Request-ID", requestID)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		return resp
	}

	resp1 := makeReq(`{"verify_key":"demo","in":10,"out":5}`, "traffic-req-1")
	if resp1.Code != http.StatusOK {
		t.Fatalf("first POST /api/traffic status = %d body=%s", resp1.Code, resp1.Body.String())
	}
	storedClient, err := fakeStore.GetClientByID(7)
	if err != nil {
		t.Fatalf("GetClientByID(7) after first POST /api/traffic error = %v", err)
	}
	if storedClient.Flow == nil || storedClient.Flow.InletFlow != 10 || storedClient.Flow.ExportFlow != 5 {
		t.Fatalf("first POST /api/traffic flow = %+v, want in:10 out:5", storedClient.Flow)
	}

	resp2 := makeReq(`{"verify_key":"demo","in":10,"out":5}`, "traffic-req-2")
	if resp2.Code != http.StatusOK {
		t.Fatalf("replayed POST /api/traffic status = %d body=%s", resp2.Code, resp2.Body.String())
	}
	if resp2.Result().Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("replayed POST /api/traffic should mark replay header, got %q", resp2.Result().Header.Get("X-Idempotent-Replay"))
	}
	if resp2.Result().Header.Get("X-Request-ID") != "traffic-req-2" {
		t.Fatalf("replayed POST /api/traffic should echo current request id, got %q", resp2.Result().Header.Get("X-Request-ID"))
	}
	storedClient, err = fakeStore.GetClientByID(7)
	if err != nil {
		t.Fatalf("GetClientByID(7) after replayed POST /api/traffic error = %v", err)
	}
	if storedClient.Flow == nil || storedClient.Flow.InletFlow != 10 || storedClient.Flow.ExportFlow != 5 {
		t.Fatalf("replayed POST /api/traffic should not double count, got %+v", storedClient.Flow)
	}

	resp3 := makeReq(`{"verify_key":"demo","in":10,"out":6}`, "traffic-req-3")
	if resp3.Code != http.StatusConflict {
		t.Fatalf("conflicting POST /api/traffic status = %d, want 409 body=%s", resp3.Code, resp3.Body.String())
	}
	if body := resp3.Body.String(); !strings.Contains(body, "\"code\":\"idempotency_conflict\"") {
		t.Fatalf("conflicting POST /api/traffic should use formal management error payload, got %s", body)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "secret")
	statusResp := httptest.NewRecorder()
	handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status after idempotency operations status = %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	if body := statusResp.Body.String(); !strings.Contains(body, "\"replay_hits\":1") || !strings.Contains(body, "\"conflicts\":1") || !strings.Contains(body, "\"ttl_seconds\":300") {
		t.Fatalf("GET /api/system/status should expose idempotency metrics, got %s", body)
	}
}

func TestNodeIdempotencyAcquireDoesNotEvictInflightEntryOnReplayTTL(t *testing.T) {
	idemStore := newNodeIdempotencyStore(40*time.Millisecond, "")
	first := idemStore.acquire("scope", "key", "fingerprint")
	if first.entry == nil {
		t.Fatal("first acquire should create inflight entry")
	}

	time.Sleep(idemStore.TTL() + 40*time.Millisecond)

	resultCh := make(chan nodeIdempotencyAcquireResult, 1)
	go func() {
		resultCh <- idemStore.acquire("scope", "key", "fingerprint")
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("second acquire should keep waiting for inflight completion after ttl expiry, got %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	idemStore.completeHTTP("scope", "key", first.entry, &nodeIdempotencyHTTPResponse{
		Status: http.StatusOK,
		Body:   []byte(`{"ok":true}`),
	})

	select {
	case result := <-resultCh:
		if result.entry != nil || result.httpResp == nil || result.conflict || result.err != nil {
			t.Fatalf("second acquire after completion = %+v, want cached replay", result)
		}
	case <-time.After(time.Second):
		t.Fatal("second acquire did not unblock after inflight completion")
	}
}

func TestNodeModeRetainsLocalWebAndNodeRoutes(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"master_url=https://master.internal",
		"node_token=secret",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = &fakeNodeStore{}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	loginReq := httptest.NewRequest(http.MethodGet, "/api/system/discovery", nil)
	loginResp := httptest.NewRecorder()
	handler.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/discovery in node mode with management platform status = %d, want 200", loginResp.Code)
	}

	configReq := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	configReq.Header.Set("X-Node-Token", "secret")
	configResp := httptest.NewRecorder()
	handler.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/export in node mode with management platform status = %d, want 200 body=%s", configResp.Code, configResp.Body.String())
	}

	syncReq := httptest.NewRequest(http.MethodPost, "/api/system/actions/sync", nil)
	syncReq.Header.Set("X-Node-Token", "secret")
	syncResp := httptest.NewRecorder()
	handler.ServeHTTP(syncResp, syncReq)
	if syncResp.Code != http.StatusOK {
		t.Fatalf("POST /api/system/actions/sync in node mode with management platform status = %d, want 200 body=%s", syncResp.Code, syncResp.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "secret")
	statusResp := httptest.NewRecorder()
	handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status in node mode with management platform status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
	}
	if body := statusResp.Body.String(); !strings.Contains(body, "\"platform_id\":\"legacy-master\"") || !strings.Contains(body, "\"master_url\":\"https://master.internal\"") || !strings.Contains(body, "\"node.api.batch\"") || !strings.Contains(body, "\"node.api.idempotency\"") || !strings.Contains(body, "\"api_base\":\"/api\"") {
		t.Fatalf("GET /api/system/status should advertise management platform and capabilities, got %s", body)
	}
	if body := statusResp.Body.String(); !strings.Contains(body, "\"live_only_events\":") || !strings.Contains(body, "\"node.traffic.report\"") {
		t.Fatalf("GET /api/system/status should advertise live-only events, got %s", body)
	}
}

func TestNodeWebSocketRejectsCrossOriginBrowserHandshake(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = &fakeNodeStore{}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(Init())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	headers := http.Header{}
	headers.Set("X-Node-Token", "secret")
	headers.Set("Origin", "http://evil.example")
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		t.Fatal("Dial() error = nil, want cross-origin websocket handshake rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("cross-origin websocket status = %d, want 403", status)
	}
}

func TestAllowNodeWebSocketOriginNormalizesDefaultPortAndForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal/ws", nil)
	req.Host = "example.com:80"
	req.Header.Set("Origin", "http://example.com")
	if !allowNodeWebSocketOrigin(req) {
		t.Fatal("allowNodeWebSocketOrigin() should allow same-origin default http port")
	}

	req = httptest.NewRequest(http.MethodGet, "http://internal/ws", nil)
	req.Host = "internal:8080"
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "example.com")
	if !allowNodeWebSocketOrigin(req) {
		t.Fatal("allowNodeWebSocketOrigin() should honor forwarded host/proto")
	}
}

func TestAllowNodeWebSocketOriginAllowsConfiguredStandaloneOrigin(t *testing.T) {
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_standalone_allowed_origins=https://console.example.com",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://internal/ws", nil)
	req.Host = "example.com:80"
	req.Header.Set("Origin", "https://console.example.com")
	if !allowNodeWebSocketOrigin(req) {
		t.Fatal("allowNodeWebSocketOrigin() should allow configured standalone origin")
	}
}

func TestAllowNodeWebSocketOriginWithExplicitConfigAllowsConfiguredStandaloneOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://internal/ws", nil)
	req.Host = "example.com:80"
	req.Header.Set("Origin", "https://console.example.com")

	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			StandaloneAllowedOrigins: "https://console.example.com",
		},
	}
	if !allowNodeWebSocketOriginWithConfig(cfg, req) {
		t.Fatal("allowNodeWebSocketOriginWithConfig() should allow origin from explicit state config")
	}
}

func TestNodeWSInitialSyncFieldsFollowActorScope(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{
			BaseURL: "/base",
		},
	}

	full := nodeWSInitialSyncFields(cfg, &webapi.Actor{Kind: "platform_admin", IsAdmin: true})
	if full["initial_sync_scope"] != "full" || full["initial_sync_complete_config"] != true {
		t.Fatalf("full initial sync fields = %#v", full)
	}
	nodeRoutes := webapi.NodeDirectRouteCatalog(cfg.Web.BaseURL)
	fullRoutes, _ := full["initial_sync_routes"].([]string)
	if !reflect.DeepEqual(fullRoutes, []string{nodeRoutes.OverviewURL(true)}) {
		t.Fatalf("full initial sync routes = %v", fullRoutes)
	}

	account := nodeWSInitialSyncFields(cfg, &webapi.Actor{Kind: "platform_admin"})
	if account["initial_sync_scope"] != "account" || account["initial_sync_complete_config"] != false {
		t.Fatalf("account initial sync fields = %#v", account)
	}
	accountRoutes, _ := account["initial_sync_routes"].([]string)
	if !reflect.DeepEqual(accountRoutes, []string{nodeRoutes.OverviewURL(false)}) {
		t.Fatalf("account initial sync routes = %v", accountRoutes)
	}

	user := nodeWSInitialSyncFields(cfg, &webapi.Actor{Kind: "platform_user"})
	if user["initial_sync_scope"] != "user" || user["initial_sync_complete_config"] != false {
		t.Fatalf("user initial sync fields = %#v", user)
	}
	userRoutes, _ := user["initial_sync_routes"].([]string)
	if !reflect.DeepEqual(userRoutes, []string{nodeRoutes.OverviewURL(false)}) {
		t.Fatalf("user initial sync routes = %v", userRoutes)
	}
}

func TestReverseOnlyManagementPlatformRejectsDirectNodeHTTP(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-reverse",
		"platform_tokens=reverse-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_reverse",
		"platform_connect_modes=reverse",
		"platform_reverse_ws_urls=ws://127.0.0.1:65535/reverse/ws",
		"platform_reverse_enabled=true",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = &fakeNodeStore{}
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	req.Header.Set("X-Node-Token", "reverse-secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/system/status for reverse-only platform status = %d, want 403 body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "direct node management is disabled") {
		t.Fatalf("GET /api/system/status for reverse-only platform body = %s", resp.Body.String())
	}
}
