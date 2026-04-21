package routers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNodeWebSocketHelloAndPing(t *testing.T) {
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

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			t.Fatalf("Dial(%s) error = %v status=%d", wsURL, err, resp.StatusCode)
		}
		t.Fatalf("Dial(%s) error = %v", wsURL, err)
	}
	defer func() { _ = conn.Close() }()

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}
	fullOverviewPath := webapi.NodeDirectRouteCatalog("").OverviewURL(true)
	if hello.Type != "hello" || hello.NodeID == "" || !strings.Contains(string(hello.Body), "node.api.ws") ||
		!strings.Contains(string(hello.Body), "\"boot_id\":\"") ||
		!strings.Contains(string(hello.Body), "\"runtime_started_at\":") ||
		!strings.Contains(string(hello.Body), "\"resync_on_boot_change\":true") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_required\":true") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_scope\":\"full\"") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_complete_config\":true") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_routes\":[\""+fullOverviewPath+"\"]") ||
		!strings.Contains(string(hello.Body), "\"changes_window\":2048") ||
		!strings.Contains(string(hello.Body), "\"batch_max_items\":80") ||
		!strings.Contains(string(hello.Body), "\"idempotency_ttl_seconds\":600") ||
		!strings.Contains(string(hello.Body), "\"live_only_events\":") ||
		!strings.Contains(string(hello.Body), "\"node.traffic.report\"") {
		t.Fatalf("unexpected hello frame: %+v body=%s", hello, string(hello.Body))
	}

	if err := conn.WriteJSON(nodeWSFrame{ID: "ping-1", Type: "ping"}); err != nil {
		t.Fatalf("WriteJSON(ping) error = %v", err)
	}
	var pong nodeWSFrame
	if err := conn.ReadJSON(&pong); err != nil {
		t.Fatalf("ReadJSON(pong) error = %v", err)
	}
	if pong.Type != "pong" || pong.ID != "ping-1" {
		t.Fatalf("unexpected pong frame: %+v", pong)
	}
}

func TestNodeWebSocketRequestForwardingSupportsJSONAndBinary(t *testing.T) {
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

	user := &file.User{
		Id:        1,
		Username:  "tenant",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}
	if err := file.GetDb().NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &file.Client{
		Id:        1,
		UserId:    user.Id,
		VerifyKey: "tenant-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	client.SetOwnerUserID(user.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	oldStore := file.GlobalStore
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

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
		ID:     "req-status",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/status",
		Headers: map[string]string{
			"X-Request-ID": "ws-request-1",
		},
	}); err != nil {
		t.Fatalf("WriteJSON(status request) error = %v", err)
	}
	statusResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "req-status"
	})
	if statusResp.Type != "response" || statusResp.ID != "req-status" || statusResp.Status != http.StatusOK {
		t.Fatalf("unexpected status response frame: %+v", statusResp)
	}
	if statusResp.Headers["X-Request-ID"] != "ws-request-1" {
		t.Fatalf("unexpected ws response request id header: %+v", statusResp.Headers)
	}
	if body := string(statusResp.Body); !strings.Contains(body, "\"store_mode\":\"local\"") || !strings.Contains(body, "\"clients\":1") {
		t.Fatalf("status response body = %s, want local counts", body)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "req-list",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients?limit=10",
	}); err != nil {
		t.Fatalf("WriteJSON(client list request) error = %v", err)
	}
	listResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "req-list"
	})
	if listResp.Status != http.StatusOK || !strings.Contains(string(listResp.Body), "\"verify_key\":\"tenant-client\"") {
		t.Fatalf("unexpected list response: %+v body=%s", listResp, string(listResp.Body))
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "req-qr",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/tools/qrcode?text=nps-ws-demo",
	}); err != nil {
		t.Fatalf("WriteJSON(qr request) error = %v", err)
	}
	qrResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "req-qr"
	})
	if qrResp.Status != http.StatusOK || qrResp.Encoding != "base64" || qrResp.Headers["Content-Type"] != "image/png" {
		t.Fatalf("unexpected qr response: %+v body=%s", qrResp, string(qrResp.Body))
	}
	if qrResp.Headers["Cache-Control"] != "no-store" || qrResp.Headers["Pragma"] != "no-cache" {
		t.Fatalf("unexpected qr cache headers: %+v", qrResp.Headers)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "req-qr-post",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/tools/qrcode",
		Body:   []byte(`{"account":"tenant@example.com","secret":"JBSWY3DPEHPK3PXP"}`),
	}); err != nil {
		t.Fatalf("WriteJSON(qr post request) error = %v", err)
	}
	qrPostResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "req-qr-post"
	})
	if qrPostResp.Status != http.StatusOK || qrPostResp.Encoding != "base64" || qrPostResp.Headers["Content-Type"] != "image/png" {
		t.Fatalf("unexpected qr post response: %+v body=%s", qrPostResp, string(qrPostResp.Body))
	}
	if qrPostResp.Headers["Cache-Control"] != "no-store" || qrPostResp.Headers["Pragma"] != "no-cache" {
		t.Fatalf("unexpected qr post cache headers: %+v", qrPostResp.Headers)
	}
}

func TestNodeWebSocketRejectsUnsupportedRequestMethod(t *testing.T) {
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
	file.GlobalStore = file.NewLocalStore()
	t.Cleanup(func() {
		file.GlobalStore = oldStore
	})

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
		ID:     "req-invalid-method",
		Type:   "request",
		Method: http.MethodPatch,
		Path:   "/api/system/status",
	}); err != nil {
		t.Fatalf("WriteJSON(invalid method request) error = %v", err)
	}
	resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "req-invalid-method"
	})
	if resp.Status != http.StatusBadRequest || resp.Error != "unsupported request method" {
		t.Fatalf("unexpected invalid method response: %+v", resp)
	}
}

func TestNodeWSRealtimeSubscriptionDetailMutationsIncludeConfigEpochMeta(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-subscription-meta",
		"platform_tokens=ws-subscription-meta-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_subscription_meta",
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
	srv := httptest.NewServer(Init())
	defer srv.Close()

	conn := dialNodeWSTestConnection(t, srv.URL, "ws-subscription-meta-secret")
	defer closeNodeWSTestConnection(t, conn)

	createBody, _ := json.Marshal(map[string]interface{}{
		"name":        "subscription-meta",
		"event_names": []string{"client.created"},
		"resources":   []string{"client"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "subscription-create-meta",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/realtime/subscriptions",
		Body:   createBody,
	}); err != nil {
		t.Fatalf("WriteJSON(subscription create) error = %v", err)
	}

	createResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "subscription-create-meta"
	})
	if createResp.Status != http.StatusOK {
		t.Fatalf("unexpected subscription create response: %+v body=%s", createResp, string(createResp.Body))
	}
	var createPayload struct {
		Timestamp int64                     `json:"timestamp"`
		Item      nodeWSSubscriptionPayload `json:"item"`
	}
	createMeta := decodeManagementFrameData(t, createResp, &createPayload)
	if createPayload.Item.ID <= 0 {
		t.Fatalf("create payload = %+v, want item id", createPayload)
	}
	if strings.TrimSpace(createMeta.ConfigEpoch) == "" {
		t.Fatalf("create meta.config_epoch = %q, want non-empty", createMeta.ConfigEpoch)
	}
	if createMeta.GeneratedAt != createPayload.Timestamp {
		t.Fatalf("create meta.generated_at = %d, want %d", createMeta.GeneratedAt, createPayload.Timestamp)
	}

	subscriptionID := createPayload.Item.ID
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "subscription-list-meta",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/realtime/subscriptions",
	}); err != nil {
		t.Fatalf("WriteJSON(subscription list) error = %v", err)
	}
	listResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "subscription-list-meta"
	})
	if listResp.Status != http.StatusOK {
		t.Fatalf("unexpected subscription list response: %+v body=%s", listResp, string(listResp.Body))
	}
	var listPayload nodeWSSubscriptionListPayload
	listMeta := decodeManagementFrameData(t, listResp, &listPayload)
	if len(listPayload.Items) == 0 || listPayload.Items[0].ID <= 0 {
		t.Fatalf("subscription list payload = %+v, want non-empty items", listPayload)
	}
	if strings.TrimSpace(listMeta.ConfigEpoch) == "" {
		t.Fatalf("list meta.config_epoch = %q, want non-empty", listMeta.ConfigEpoch)
	}
	if listMeta.GeneratedAt != listPayload.Timestamp {
		t.Fatalf("list meta.generated_at = %d, want %d", listMeta.GeneratedAt, listPayload.Timestamp)
	}

	assertConfigEpochMeta := func(requestID, method, path string, body []byte) {
		t.Helper()
		frame := nodeWSFrame{
			ID:     requestID,
			Type:   "request",
			Method: method,
			Path:   path,
			Body:   body,
		}
		if err := conn.WriteJSON(frame); err != nil {
			t.Fatalf("WriteJSON(%s %s) error = %v", method, path, err)
		}
		resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
			return frame.Type == "response" && frame.ID == requestID
		})
		if resp.Status != http.StatusOK {
			t.Fatalf("%s %s response = %+v body=%s, want 200", method, path, resp, string(resp.Body))
		}
		var payload struct {
			Timestamp int64 `json:"timestamp"`
		}
		meta := decodeManagementFrameData(t, resp, &payload)
		if strings.TrimSpace(meta.ConfigEpoch) == "" {
			t.Fatalf("%s %s meta.config_epoch = %q, want non-empty", method, path, meta.ConfigEpoch)
		}
		if meta.GeneratedAt != payload.Timestamp {
			t.Fatalf("%s %s meta.generated_at = %d, want %d", method, path, meta.GeneratedAt, payload.Timestamp)
		}
	}

	subscriptionPath := "/api/realtime/subscriptions/" + strconv.FormatInt(subscriptionID, 10)
	assertConfigEpochMeta("subscription-get-meta", http.MethodGet, subscriptionPath, nil)

	updateBody, _ := json.Marshal(map[string]interface{}{
		"name":        "subscription-meta-updated",
		"event_names": []string{"client.updated"},
		"resources":   []string{"client"},
	})
	assertConfigEpochMeta("subscription-update-meta", http.MethodPost, subscriptionPath+"/actions/update", updateBody)

	statusBody, _ := json.Marshal(map[string]interface{}{"enabled": false})
	assertConfigEpochMeta("subscription-status-meta", http.MethodPost, subscriptionPath+"/actions/status", statusBody)

	assertConfigEpochMeta("subscription-delete-meta", http.MethodPost, subscriptionPath+"/actions/delete", nil)
}

func TestNodeWSWebhookDetailMutationsIncludeConfigEpochMeta(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-webhook-meta",
		"platform_tokens=ws-webhook-meta-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_webhook_meta",
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
	srv := httptest.NewServer(Init())
	defer srv.Close()

	conn := dialNodeWSTestConnection(t, srv.URL, "ws-webhook-meta-secret")
	defer closeNodeWSTestConnection(t, conn)

	createBody, _ := json.Marshal(map[string]interface{}{
		"name":        "webhook-meta",
		"url":         "https://example.invalid/hook",
		"event_names": []string{"client.created"},
		"resources":   []string{"client"},
	})
	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "webhook-create-meta",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/webhooks",
		Body:   createBody,
	}); err != nil {
		t.Fatalf("WriteJSON(webhook create) error = %v", err)
	}

	createResp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "webhook-create-meta"
	})
	if createResp.Status != http.StatusOK {
		t.Fatalf("unexpected webhook create response: %+v body=%s", createResp, string(createResp.Body))
	}
	var createPayload nodeWebhookMutationPayload
	createMeta := decodeManagementFrameData(t, createResp, &createPayload)
	if createPayload.Item == nil || createPayload.Item.ID <= 0 {
		t.Fatalf("create webhook payload = %+v, want item id", createPayload)
	}
	if strings.TrimSpace(createMeta.ConfigEpoch) == "" {
		t.Fatalf("create webhook meta.config_epoch = %q, want non-empty", createMeta.ConfigEpoch)
	}
	if createMeta.GeneratedAt != createPayload.Timestamp {
		t.Fatalf("create webhook meta.generated_at = %d, want %d", createMeta.GeneratedAt, createPayload.Timestamp)
	}

	webhookID := createPayload.Item.ID
	assertConfigEpochMeta := func(requestID, method, path string, body []byte) {
		t.Helper()
		frame := nodeWSFrame{
			ID:     requestID,
			Type:   "request",
			Method: method,
			Path:   path,
			Body:   body,
		}
		if err := conn.WriteJSON(frame); err != nil {
			t.Fatalf("WriteJSON(%s %s) error = %v", method, path, err)
		}
		resp := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
			return frame.Type == "response" && frame.ID == requestID
		})
		if resp.Status != http.StatusOK {
			t.Fatalf("%s %s response = %+v body=%s, want 200", method, path, resp, string(resp.Body))
		}
		var payload nodeWebhookMutationPayload
		meta := decodeManagementFrameData(t, resp, &payload)
		if strings.TrimSpace(meta.ConfigEpoch) == "" {
			t.Fatalf("%s %s meta.config_epoch = %q, want non-empty", method, path, meta.ConfigEpoch)
		}
		if meta.GeneratedAt != payload.Timestamp {
			t.Fatalf("%s %s meta.generated_at = %d, want %d", method, path, meta.GeneratedAt, payload.Timestamp)
		}
	}

	webhookPath := "/api/webhooks/" + strconv.FormatInt(webhookID, 10)
	assertConfigEpochMeta("webhook-get-meta", http.MethodGet, webhookPath, nil)

	updateBody, _ := json.Marshal(map[string]interface{}{
		"name":        "webhook-meta-updated",
		"url":         "https://example.invalid/hook-updated",
		"event_names": []string{"client.updated"},
		"resources":   []string{"client"},
	})
	assertConfigEpochMeta("webhook-update-meta", http.MethodPost, webhookPath+"/actions/update", updateBody)

	statusBody, _ := json.Marshal(map[string]interface{}{"enabled": false})
	assertConfigEpochMeta("webhook-status-meta", http.MethodPost, webhookPath+"/actions/status", statusBody)

	assertConfigEpochMeta("webhook-delete-meta", http.MethodPost, webhookPath+"/actions/delete", nil)
}

func TestNodeWSSubscriptionScrubWithLookupDoesNotHoldRegistryLockDuringLookup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	registry := &nodeWSSubscriptionRegistry{
		items: map[int64]nodeWSSubscription{
			1: {
				ID: 1,
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
					Selector: nodeEventSelector{
						HostIDs: []int{1},
					},
				},
			},
		},
	}

	done := make(chan struct{})
	go func() {
		registry.scrubWithLookup(nodeEventResourceLookup{
			HostExists: func(id int) bool {
				startedOnce.Do(func() { close(started) })
				<-release
				return false
			},
		})
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scrub lookup to start")
	}

	readDone := make(chan error, 1)
	go func() {
		payload, ok := registry.Get(1)
		if !ok {
			readDone <- context.DeadlineExceeded
			return
		}
		if payload.ID != 1 {
			readDone <- context.Canceled
			return
		}
		readDone <- nil
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("Get() during scrub returned unexpected error marker: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Get() blocked behind scrub lookup")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scrubWithLookup() did not finish after lookup release")
	}

	if _, ok := registry.Get(1); ok {
		t.Fatal("Get() after scrub = true, want deleted subscription")
	}
}

func TestNodeWSSubscriptionScrubWithLookupSkipsStaleConcurrentSelectorChanges(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	registry := &nodeWSSubscriptionRegistry{
		items: map[int64]nodeWSSubscription{
			1: {
				ID: 1,
				nodeEventSinkConfig: nodeEventSinkConfig{
					Enabled: true,
					Name:    "original",
					Selector: nodeEventSelector{
						HostIDs: []int{1},
					},
				},
			},
		},
	}

	done := make(chan struct{})
	go func() {
		registry.scrubWithLookup(nodeEventResourceLookup{
			HostExists: func(id int) bool {
				startedOnce.Do(func() { close(started) })
				<-release
				return false
			},
		})
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scrub lookup to start")
	}

	registry.mu.Lock()
	item := registry.items[1]
	item.Name = "updated"
	item.Selector = nodeEventSelector{HostIDs: []int{2}}
	registry.items[1] = item
	registry.mu.Unlock()

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scrubWithLookup() did not finish after lookup release")
	}

	payload, ok := registry.Get(1)
	if !ok {
		t.Fatal("Get() after concurrent update = false, want payload")
	}
	if !payload.Enabled {
		t.Fatalf("stale scrub should not disable concurrently updated subscription, got %+v", payload)
	}
	if payload.Name != "updated" {
		t.Fatalf("payload.Name = %q, want updated", payload.Name)
	}
	if len(payload.HostIDs) != 1 || payload.HostIDs[0] != 2 {
		t.Fatalf("payload.HostIDs = %v, want [2]", payload.HostIDs)
	}
}

func TestNodeReverseWebSocketConnectsDispatchesRequestsAndTracksStatus(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	platformReqHeaders := make(chan http.Header, 1)
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
		platformReqHeaders <- r.Header.Clone()
		platformConn <- conn
	}))
	defer platformSrv.Close()

	reverseWSURL := "ws" + strings.TrimPrefix(platformSrv.URL, "http") + "/reverse/ws"
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-reverse",
		"platform_tokens=reverse-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_reverse",
		"platform_connect_modes=dual",
		"platform_reverse_ws_urls=" + reverseWSURL,
		"platform_reverse_enabled=true",
		"platform_reverse_heartbeat_seconds=30",
		"node_changes_window=2048",
		"node_batch_max_items=80",
		"node_idempotency_ttl_seconds=600",
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
	runtime := NewRuntime(nil)
	stopReverse := StartNodeReverseConnectors(runtime.State)
	defer stopReverse()
	handshakeDeadline := time.Now().Add(30 * time.Second)

	var headers http.Header
	select {
	case headers = <-platformReqHeaders:
	case <-time.After(time.Until(handshakeDeadline)):
		t.Fatal("timed out waiting for reverse websocket handshake")
	}
	if headers.Get("X-Node-Token") != "reverse-secret" ||
		headers.Get("X-Platform-ID") != "master-reverse" ||
		headers.Get("X-Node-Connect-Mode") != "reverse" {
		t.Fatalf("unexpected reverse handshake headers: %+v", headers)
	}

	var conn *websocket.Conn
	select {
	case conn = <-platformConn:
	case <-time.After(time.Until(handshakeDeadline)):
		t.Fatal("timed out waiting for reverse websocket connection")
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}

	var hello nodeWSFrame
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("ReadJSON(hello) error = %v", err)
	}
	fullOverviewPath := webapi.NodeDirectRouteCatalog("").OverviewURL(true)
	if hello.Type != "hello" || hello.NodeID == "" || !strings.Contains(string(hello.Body), "\"connect_mode\":\"reverse\"") ||
		!strings.Contains(string(hello.Body), "\"boot_id\":\"") ||
		!strings.Contains(string(hello.Body), "\"runtime_started_at\":") ||
		!strings.Contains(string(hello.Body), "\"resync_on_boot_change\":true") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_required\":true") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_scope\":\"full\"") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_complete_config\":true") ||
		!strings.Contains(string(hello.Body), "\"initial_sync_routes\":[\""+fullOverviewPath+"\"]") ||
		!strings.Contains(string(hello.Body), "\"changes_window\":2048") ||
		!strings.Contains(string(hello.Body), "\"batch_max_items\":80") ||
		!strings.Contains(string(hello.Body), "\"idempotency_ttl_seconds\":600") ||
		!strings.Contains(string(hello.Body), "\"live_only_events\":") ||
		!strings.Contains(string(hello.Body), "\"node.traffic.report\"") {
		t.Fatalf("unexpected reverse hello frame: %+v body=%s", hello, string(hello.Body))
	}
	var helloBody map[string]interface{}
	if err := json.Unmarshal(hello.Body, &helloBody); err != nil {
		t.Fatalf("json.Unmarshal(reverse hello body) error = %v body=%s", err, string(hello.Body))
	}
	bootID, _ := helloBody["boot_id"].(string)
	if strings.TrimSpace(bootID) == "" {
		t.Fatalf("reverse hello missing boot_id: %s", string(hello.Body))
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:   "reverse-hello-mismatch",
		Type: "hello",
		Body: json.RawMessage(`{"last_boot_id":"stale-boot","changes_after":0}`),
	}); err != nil {
		t.Fatalf("WriteJSON(reverse hello mismatch) error = %v", err)
	}
	mismatchFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "hello_ack" && frame.ID == "reverse-hello-mismatch"
	})
	if !strings.Contains(string(mismatchFrame.Body), "\"resync_required\":true") || !strings.Contains(string(mismatchFrame.Body), "\"reason\":\"boot_changed\"") {
		t.Fatalf("unexpected reverse hello mismatch ack: %+v body=%s", mismatchFrame, string(mismatchFrame.Body))
	}

	preReplayAddReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(fmt.Sprintf(`{"verify_key":"reverse-pre-replay-client-%d","remark":"created before reverse replay"}`, time.Now().UnixNano())))
	preReplayAddReq.Header.Set("Content-Type", "application/json")
	preReplayAddReq.Header.Set("X-Node-Token", "reverse-secret")
	preReplayAddReq.Header.Set("X-Platform-Role", "user")
	preReplayAddReq.Header.Set("X-Platform-Username", "platform-user")
	preReplayAddReq.Header.Set("X-Platform-Actor-ID", "platform-user-pre-replay")
	preReplayAddResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(preReplayAddResp, preReplayAddReq)
	if preReplayAddResp.Code != http.StatusOK ||
		!strings.Contains(preReplayAddResp.Body.String(), "\"resource\":\"client\"") ||
		!strings.Contains(preReplayAddResp.Body.String(), "\"action\":\"create\"") {
		t.Fatalf("POST /api/clients before reverse replay status = %d body=%s", preReplayAddResp.Code, preReplayAddResp.Body.String())
	}

	replayHelloBody := `{"last_boot_id":"` + bootID + `","changes_after":0,"changes_limit":50}`
	if err := conn.WriteJSON(nodeWSFrame{
		ID:   "reverse-hello-replay",
		Type: "hello",
		Body: json.RawMessage(replayHelloBody),
	}); err != nil {
		t.Fatalf("WriteJSON(reverse hello replay) error = %v", err)
	}
	replayAck := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "hello_ack" && frame.ID == "reverse-hello-replay"
	})
	if !strings.Contains(string(replayAck.Body), "\"resync_required\":false") ||
		!strings.Contains(string(replayAck.Body), "\"initial_sync_required\":true") ||
		!strings.Contains(string(replayAck.Body), "\"initial_sync_scope\":\"full\"") ||
		!strings.Contains(string(replayAck.Body), "\"initial_sync_complete_config\":true") ||
		!strings.Contains(string(replayAck.Body), "\"initial_sync_routes\":[\""+fullOverviewPath+"\"]") ||
		!strings.Contains(string(replayAck.Body), "\"name\":\"client.created\"") ||
		!strings.Contains(string(replayAck.Body), "\"last_sequence\":") ||
		!strings.Contains(string(replayAck.Body), "\"next_after\":") {
		t.Fatalf("unexpected reverse hello replay ack: %+v body=%s", replayAck, string(replayAck.Body))
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "reverse-secret")
	statusResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status over direct HTTP status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
	}
	if body := statusResp.Body.String(); !strings.Contains(body, "\"platform_id\":\"master-reverse\"") ||
		!strings.Contains(body, "\"boot_id\":\"") ||
		!strings.Contains(body, "\"runtime_started_at\":") ||
		!strings.Contains(body, "\"resync_on_boot_change\":true") ||
		!strings.Contains(body, "\"connect_mode\":\"dual\"") ||
		!strings.Contains(body, "\"direct_enabled\":true") ||
		!strings.Contains(body, "\"reverse_connected\":true") ||
		!strings.Contains(body, "\"reverse_heartbeat_seconds\":30") ||
		!strings.Contains(body, "\"changes_window\":2048") ||
		!strings.Contains(body, "\"batch_max_items\":80") ||
		!strings.Contains(body, "\"idempotency_ttl_seconds\":600") ||
		!strings.Contains(body, "\"live_only_events\":") ||
		!strings.Contains(body, "\"node.traffic.report\"") ||
		!strings.Contains(body, "\"reverse_ws_url\":\""+reverseWSURL+"\"") {
		t.Fatalf("GET /api/system/status should expose reverse runtime status, got %s", body)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "reverse-status",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/status",
	}); err != nil {
		t.Fatalf("WriteJSON(status request) error = %v", err)
	}
	statusFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "reverse-status"
	})
	if statusFrame.Status != http.StatusOK || !strings.Contains(string(statusFrame.Body), "\"reverse_connected\":true") {
		t.Fatalf("unexpected reverse status response: %+v body=%s", statusFrame, string(statusFrame.Body))
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "reverse-add",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/clients",
		Headers: map[string]string{
			"X-Platform-Role":     "user",
			"X-Platform-Username": "platform-user",
			"X-Platform-Actor-ID": "platform-user-1",
		},
		Body: json.RawMessage(fmt.Sprintf(`{"verify_key":"reverse-platform-client-%d","remark":"created through reverse ws"}`, time.Now().UnixNano())),
	}); err != nil {
		t.Fatalf("WriteJSON(add request) error = %v", err)
	}
	addFrame := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "response" && frame.ID == "reverse-add"
	})
	if addFrame.Status != http.StatusOK ||
		!strings.Contains(string(addFrame.Body), "\"resource\":\"client\"") ||
		!strings.Contains(string(addFrame.Body), "\"action\":\"create\"") {
		t.Fatalf("unexpected reverse add response: %+v body=%s", addFrame, string(addFrame.Body))
	}
	var addPayload struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(addFrame.Body, &addPayload); err != nil {
		t.Fatalf("json.Unmarshal(add response) error = %v body=%s", err, string(addFrame.Body))
	}
	if addPayload.Data.ID <= 0 {
		t.Fatalf("unexpected add payload: %+v body=%s", addPayload, string(addFrame.Body))
	}
	serviceUser, err := file.GetDb().GetUserByExternalPlatformID("master-reverse")
	if err != nil {
		t.Fatalf("GetUserByExternalPlatformID(master-reverse) error = %v", err)
	}
	createdClient, err := file.GetDb().GetClient(addPayload.Data.ID)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", addPayload.Data.ID, err)
	}
	if createdClient.OwnerID() != serviceUser.Id ||
		createdClient.SourceType != "platform_user" ||
		createdClient.SourcePlatformID != "master-reverse" ||
		createdClient.SourceActorID != "platform-user-1" {
		t.Fatalf("reverse-created client = %+v, serviceUser=%+v", createdClient, serviceUser)
	}

	if err := conn.WriteJSON(nodeWSFrame{
		ID:     "reverse-sync",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/system/actions/sync",
	}); err != nil {
		t.Fatalf("WriteJSON(sync request) error = %v", err)
	}
	var sawSyncResponse bool
	var sawSyncEvent bool
	deadline := time.Now().Add(5 * time.Second)
	for !sawSyncResponse || !sawSyncEvent {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("SetReadDeadline(sync loop) error = %v", err)
		}
		var frame nodeWSFrame
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatalf("ReadJSON(sync loop) error = %v", err)
		}
		switch {
		case frame.Type == "response" && frame.ID == "reverse-sync":
			if frame.Status != http.StatusOK {
				t.Fatalf("unexpected reverse sync response: %+v body=%s", frame, string(frame.Body))
			}
			sawSyncResponse = true
		case frame.Type == "event":
			body := string(frame.Body)
			if strings.Contains(body, "\"name\":\"node.config.sync\"") && strings.Contains(body, "\"resource\":\"node\"") {
				sawSyncEvent = true
			}
		}
	}
}
