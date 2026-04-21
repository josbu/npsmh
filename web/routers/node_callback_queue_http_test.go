package routers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestNodeCallbackQueueEndpointsInspectReplayAndClear(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	var mu sync.Mutex
	acceptCallbacks := false
	attempts := make(chan int, 8)
	successBodies := make(chan []byte, 2)
	requestCount := 0
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		requestCount++
		currentAttempt := requestCount
		accepted := acceptCallbacks
		mu.Unlock()
		attempts <- currentAttempt
		if !accepted {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		successBodies <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackSrv.Close()

	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-callback",
		"platform_tokens=callback-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_callback",
		"platform_callback_urls=" + callbackSrv.URL + "/node/callback",
		"platform_callback_enabled=true",
		"platform_callback_timeout_seconds=5",
		"platform_callback_retry_max=0",
		"platform_callback_retry_backoff_seconds=20",
		"platform_callback_queue_max=4",
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
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	createClient := func(vkey string) {
		addReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"`+vkey+`","remark":"queued callback test"}`))
		addReq.Header.Set("Content-Type", "application/json")
		addReq.Header.Set("X-Node-Token", "callback-secret")
		addReq.Header.Set("X-Platform-Role", "admin")
		addReq.Header.Set("X-Platform-Username", "platform-admin")
		addReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
		addResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(addResp, addReq)
		if addResp.Code != http.StatusOK {
			t.Fatalf("POST /api/clients status = %d body=%s", addResp.Code, addResp.Body.String())
		}
	}

	createClient("callback-queue-client-1")

	select {
	case got := <-attempts:
		if got != 1 {
			t.Fatalf("initial callback attempt = %d, want 1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initial callback attempt")
	}

	queueReq := httptest.NewRequest(http.MethodGet, "/api/callbacks/queue?limit=10", nil)
	queueReq.Header.Set("X-Node-Token", "callback-secret")
	deadline := time.Now().Add(5 * time.Second)
	queueBody := ""
	for {
		queueResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(queueResp, queueReq)
		if queueResp.Code != http.StatusOK {
			t.Fatalf("GET /api/callbacks/queue status = %d body=%s", queueResp.Code, queueResp.Body.String())
		}
		queueBody = queueResp.Body.String()
		if strings.Contains(queueBody, "\"callback_queue_size\":1") &&
			strings.Contains(queueBody, "\"event_name\":\"client.created\"") {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(queueBody, "\"platform_id\":\"master-callback\"") ||
		!strings.Contains(queueBody, "\"callback_queue_size\":1") ||
		!strings.Contains(queueBody, "\"callback_queue_max\":4") ||
		!strings.Contains(queueBody, "\"event_name\":\"client.created\"") {
		t.Fatalf("GET /api/callbacks/queue should expose queued callback item, got %s", queueBody)
	}

	missingQueueReq := httptest.NewRequest(http.MethodGet, "/api/callbacks/queue?platform_id=missing-platform", nil)
	missingQueueReq.Header.Set("X-Node-Token", "callback-secret")
	missingQueueResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(missingQueueResp, missingQueueReq)
	if missingQueueResp.Code != http.StatusNotFound || !strings.Contains(missingQueueResp.Body.String(), "\"code\":\"management_platform_not_found\"") {
		t.Fatalf("GET /api/callbacks/queue unknown platform status = %d body=%s, want 404 management_platform_not_found", missingQueueResp.Code, missingQueueResp.Body.String())
	}

	formReplayReq := httptest.NewRequest(http.MethodPost, "/api/callbacks/queue/actions/replay", strings.NewReader("platform_id=master-callback"))
	formReplayReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formReplayReq.Header.Set("X-Node-Token", "callback-secret")
	formReplayResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(formReplayResp, formReplayReq)
	if formReplayResp.Code != http.StatusBadRequest || !strings.Contains(formReplayResp.Body.String(), "\"code\":\"invalid_json_body\"") {
		t.Fatalf("POST /api/callbacks/queue/actions/replay with form body status = %d body=%s, want 400 invalid_json_body", formReplayResp.Code, formReplayResp.Body.String())
	}
	operations := runtime.State.NodeOperations().Query(webservice.NodeOperationQueryInput{
		Scope: webservice.ResolveNodeAccessScope(webservice.Principal{
			Authenticated: true,
			Kind:          "admin",
			IsAdmin:       true,
		}),
		Limit: 20,
	})
	foundReplayError := false
	for _, item := range operations.Items {
		if item.Kind != "callback_queue_replay" {
			continue
		}
		if len(item.Paths) != 1 || item.Paths[0] != "/api/callbacks/queue/actions/replay" {
			t.Fatalf("callback queue replay paths = %+v, want [/api/callbacks/queue/actions/replay]", item.Paths)
		}
		if item.Count != 1 || item.SuccessCount != 0 || item.ErrorCount != 1 {
			t.Fatalf("callback queue replay operation counts = %+v, want count=1 success=0 error=1", item)
		}
		foundReplayError = true
		break
	}
	if !foundReplayError {
		t.Fatal("invalid callback queue replay request did not record callback_queue_replay operation")
	}

	multiReplayReq := httptest.NewRequest(http.MethodPost, "/api/callbacks/queue/actions/replay", strings.NewReader(`{"platform_id":"master-callback"}{"platform_id":"master-callback"}`))
	multiReplayReq.Header.Set("Content-Type", "application/json")
	multiReplayReq.Header.Set("X-Node-Token", "callback-secret")
	multiReplayResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(multiReplayResp, multiReplayReq)
	if multiReplayResp.Code != http.StatusBadRequest || !strings.Contains(multiReplayResp.Body.String(), "\"code\":\"invalid_json_body\"") {
		t.Fatalf("POST /api/callbacks/queue/actions/replay with trailing json status = %d body=%s, want 400 invalid_json_body", multiReplayResp.Code, multiReplayResp.Body.String())
	}

	missingReplayReq := httptest.NewRequest(http.MethodPost, "/api/callbacks/queue/actions/replay", strings.NewReader(`{"platform_id":"missing-platform"}`))
	missingReplayReq.Header.Set("Content-Type", "application/json")
	missingReplayReq.Header.Set("X-Node-Token", "callback-secret")
	missingReplayResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(missingReplayResp, missingReplayReq)
	if missingReplayResp.Code != http.StatusNotFound || !strings.Contains(missingReplayResp.Body.String(), "\"code\":\"management_platform_not_found\"") {
		t.Fatalf("POST /api/callbacks/queue/actions/replay unknown platform status = %d body=%s, want 404 management_platform_not_found", missingReplayResp.Code, missingReplayResp.Body.String())
	}

	mu.Lock()
	acceptCallbacks = true
	mu.Unlock()

	replayReq := httptest.NewRequest(http.MethodPost, "/api/callbacks/queue/actions/replay", nil)
	replayReq.Header.Set("X-Node-Token", "callback-secret")
	replayResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(replayResp, replayReq)
	if replayResp.Code != http.StatusOK || !strings.Contains(replayResp.Body.String(), "\"replay_triggered\":true") {
		t.Fatalf("POST /api/callbacks/queue/actions/replay status = %d body=%s", replayResp.Code, replayResp.Body.String())
	}

	select {
	case body := <-successBodies:
		if !strings.Contains(string(body), "\"name\":\"client.created\"") {
			t.Fatalf("unexpected replay callback body: %s", string(body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replayed callback")
	}

	queueReq = httptest.NewRequest(http.MethodGet, "/api/callbacks/queue?limit=10", nil)
	queueReq.Header.Set("X-Node-Token", "callback-secret")
	deadline = time.Now().Add(5 * time.Second)
	for {
		queueResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(queueResp, queueReq)
		if queueResp.Code != http.StatusOK {
			t.Fatalf("GET /api/callbacks/queue after replay status = %d body=%s", queueResp.Code, queueResp.Body.String())
		}
		if strings.Contains(queueResp.Body.String(), "\"callback_queue_size\":0") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("callback queue should be empty after replay, got %s", queueResp.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	acceptCallbacks = false
	mu.Unlock()
	createClient("callback-queue-client-2")

	select {
	case <-attempts:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second callback attempt")
	}

	deadline = time.Now().Add(5 * time.Second)
	for {
		queueReq = httptest.NewRequest(http.MethodGet, "/api/callbacks/queue?limit=10", nil)
		queueReq.Header.Set("X-Node-Token", "callback-secret")
		queueResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(queueResp, queueReq)
		if queueResp.Code != http.StatusOK {
			t.Fatalf("GET /api/callbacks/queue before clear status = %d body=%s", queueResp.Code, queueResp.Body.String())
		}
		if strings.Contains(queueResp.Body.String(), "\"callback_queue_size\":1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for callback queue before clear, got %s", queueResp.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	clearReq := httptest.NewRequest(http.MethodPost, "/api/callbacks/queue/actions/clear", nil)
	clearReq.Header.Set("X-Node-Token", "callback-secret")
	clearResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(clearResp, clearReq)
	if clearResp.Code != http.StatusOK || !strings.Contains(clearResp.Body.String(), "\"cleared\":1") {
		t.Fatalf("POST /api/callbacks/queue/actions/clear status = %d body=%s", clearResp.Code, clearResp.Body.String())
	}
}

func TestNodeCallbackQueueEndpointsRequireAuthentication(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-callback",
		"platform_tokens=callback-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_callback",
		"platform_callback_urls=https://example.invalid/node/callback",
		"platform_callback_enabled=true",
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
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	queueReq := httptest.NewRequest(http.MethodGet, "/api/callbacks/queue", nil)
	queueResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(queueResp, queueReq)
	if queueResp.Code != http.StatusUnauthorized || !strings.Contains(queueResp.Body.String(), "\"code\":\"unauthorized\"") {
		t.Fatalf("GET /api/callbacks/queue without auth status = %d body=%s, want 401 unauthorized", queueResp.Code, queueResp.Body.String())
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/callbacks/queue/actions/replay", nil)
	replayResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(replayResp, replayReq)
	if replayResp.Code != http.StatusUnauthorized || !strings.Contains(replayResp.Body.String(), "\"code\":\"unauthorized\"") {
		t.Fatalf("POST /api/callbacks/queue/actions/replay without auth status = %d body=%s, want 401 unauthorized", replayResp.Code, replayResp.Body.String())
	}
}
