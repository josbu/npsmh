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
	"github.com/gin-gonic/gin"
)

func TestNodeCallbackDispatchersReplayQueuedCallbacksAfterRestart(t *testing.T) {
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
		"platform_callback_retry_backoff_seconds=1",
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

	addReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"callback-queued-client","remark":"created through callback platform"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addReq.Header.Set("X-Node-Token", "callback-secret")
	addReq.Header.Set("X-Platform-Role", "admin")
	addReq.Header.Set("X-Platform-Username", "platform-admin")
	addReq.Header.Set("X-Platform-Actor-ID", "platform-admin-1")
	addResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(addResp, addReq)
	if addResp.Code != http.StatusOK {
		runtime.Stop()
		t.Fatalf("POST /api/clients status = %d body=%s", addResp.Code, addResp.Body.String())
	}

	select {
	case got := <-attempts:
		if got != 1 {
			runtime.Stop()
			t.Fatalf("initial callback attempt = %d, want 1", got)
		}
	case <-time.After(5 * time.Second):
		runtime.Stop()
		t.Fatal("timed out waiting for initial callback attempt")
	}

	deadline := time.Now().Add(5 * time.Second)
	queuedStatusBody := ""
	for {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
		statusReq.Header.Set("X-Node-Token", "callback-secret")
		statusResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(statusResp, statusReq)
		if statusResp.Code != http.StatusOK {
			runtime.Stop()
			t.Fatalf("GET /api/system/status status = %d body=%s", statusResp.Code, statusResp.Body.String())
		}
		queuedStatusBody = statusResp.Body.String()
		if strings.Contains(queuedStatusBody, "\"callback_queue_size\":1") &&
			strings.Contains(queuedStatusBody, "\"callback_queue_max\":4") {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(queuedStatusBody, "\"callback_queue_size\":1") ||
		!strings.Contains(queuedStatusBody, "\"callback_queue_max\":4") {
		runtime.Stop()
		t.Fatalf("queued callback status not exposed, got %s", queuedStatusBody)
	}

	runtime.Stop()

	mu.Lock()
	acceptCallbacks = true
	mu.Unlock()

	runtime = NewManagedRuntime(nil)
	defer runtime.Stop()

	select {
	case got := <-attempts:
		if got < 2 {
			t.Fatalf("replayed callback attempt = %d, want >= 2", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replayed callback attempt")
	}

	select {
	case body := <-successBodies:
		if !strings.Contains(string(body), "\"name\":\"client.created\"") {
			t.Fatalf("unexpected replayed callback body: %s", string(body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replayed callback success")
	}

	deadline = time.Now().Add(5 * time.Second)
	replayedStatusBody := ""
	for {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
		statusReq.Header.Set("X-Node-Token", "callback-secret")
		statusResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(statusResp, statusReq)
		if statusResp.Code != http.StatusOK {
			t.Fatalf("GET /api/system/status status = %d body=%s", statusResp.Code, statusResp.Body.String())
		}
		replayedStatusBody = statusResp.Body.String()
		if strings.Contains(replayedStatusBody, "\"callback_queue_size\":0") &&
			strings.Contains(replayedStatusBody, "\"last_callback_replay_at\":") &&
			strings.Contains(replayedStatusBody, "\"callback_deliveries\":1") {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(replayedStatusBody, "\"callback_queue_size\":0") ||
		!strings.Contains(replayedStatusBody, "\"last_callback_replay_at\":") ||
		!strings.Contains(replayedStatusBody, "\"callback_deliveries\":1") {
		t.Fatalf("replayed callback status not exposed, got %s", replayedStatusBody)
	}
}
