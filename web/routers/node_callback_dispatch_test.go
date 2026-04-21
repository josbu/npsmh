package routers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/gin-gonic/gin"
)

func TestNodeCallbackDispatchersDeliverScopedWebhookAndTrackStatus(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	callbackRequests := make(chan *http.Request, 4)
	callbackBodies := make(chan []byte, 4)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		callbackRequests <- r.Clone(r.Context())
		callbackBodies <- body
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
		"platform_callback_retry_max=2",
		"platform_callback_retry_backoff_seconds=1",
		"platform_callback_signing_keys=callback-signing-secret",
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

	addReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"callback-client","remark":"created through callback platform"}`))
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

	var callbackReq *http.Request
	var callbackBody []byte
	select {
	case callbackReq = <-callbackRequests:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback request")
	}
	select {
	case callbackBody = <-callbackBodies:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback body")
	}

	if callbackReq.Method != http.MethodPost || callbackReq.URL.Path != "/node/callback" {
		t.Fatalf("unexpected callback request: method=%s path=%s", callbackReq.Method, callbackReq.URL.Path)
	}
	if callbackReq.Header.Get("Authorization") != "Bearer callback-secret" ||
		callbackReq.Header.Get("X-Node-Token") != "callback-secret" ||
		callbackReq.Header.Get("X-Platform-ID") != "master-callback" ||
		strings.TrimSpace(callbackReq.Header.Get("X-Node-Boot-ID")) == "" {
		t.Fatalf("unexpected callback headers: %+v", callbackReq.Header)
	}
	if callbackReq.Header.Get("X-Node-Signature-Alg") != "hmac-sha256" ||
		strings.TrimSpace(callbackReq.Header.Get("X-Node-Signature-Timestamp")) == "" ||
		callbackReq.Header.Get("X-Node-Signature") != signNodeCallbackPayload("callback-signing-secret", callbackReq.Header.Get("X-Node-Signature-Timestamp"), callbackBody) {
		t.Fatalf("unexpected callback signing headers: %+v", callbackReq.Header)
	}
	if !strings.Contains(string(callbackBody), "\"type\":\"event\"") ||
		!strings.Contains(string(callbackBody), "\"platform_id\":\"master-callback\"") ||
		!strings.Contains(string(callbackBody), "\"name\":\"client.created\"") ||
		!strings.Contains(string(callbackBody), "\"boot_id\":\"") {
		t.Fatalf("unexpected callback body: %s", string(callbackBody))
	}

	var statusBody string
	deadline := time.Now().Add(5 * time.Second)
	for {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
		statusReq.Header.Set("X-Node-Token", "callback-secret")
		statusResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(statusResp, statusReq)
		if statusResp.Code != http.StatusOK {
			t.Fatalf("GET /api/system/status status = %d body=%s", statusResp.Code, statusResp.Body.String())
		}
		statusBody = statusResp.Body.String()
		if strings.Contains(statusBody, "\"last_callback_at\":") &&
			strings.Contains(statusBody, "\"last_callback_success_at\":") &&
			strings.Contains(statusBody, "\"last_callback_status_code\":204") &&
			strings.Contains(statusBody, "\"callback_deliveries\":1") {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(statusBody, "\"node.api.callbacks\"") ||
		!strings.Contains(statusBody, "\"callback_enabled\":true") ||
		!strings.Contains(statusBody, "\"callback_url\":\""+callbackSrv.URL+"/node/callback\"") ||
		!strings.Contains(statusBody, "\"callback_timeout_seconds\":5") ||
		!strings.Contains(statusBody, "\"callback_retry_max\":2") ||
		!strings.Contains(statusBody, "\"callback_retry_backoff_seconds\":1") ||
		!strings.Contains(statusBody, "\"callback_signing_enabled\":true") ||
		!strings.Contains(statusBody, "\"last_callback_at\":") ||
		!strings.Contains(statusBody, "\"last_callback_success_at\":") ||
		!strings.Contains(statusBody, "\"last_callback_status_code\":204") ||
		!strings.Contains(statusBody, "\"callback_deliveries\":1") {
		t.Fatalf("GET /api/system/status should expose callback runtime status, got %s", statusBody)
	}
}

func TestNodeCallbackDispatchersRetryAndSkipLiveOnlyEvents(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	attempts := make(chan int, 8)
	successBody := make(chan []byte, 1)
	requestCount := 0
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		attempts <- requestCount
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if requestCount < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		successBody <- body
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
		"platform_callback_retry_max=2",
		"platform_callback_retry_backoff_seconds=1",
		"platform_callback_signing_keys=callback-signing-secret",
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

	addReq := httptest.NewRequest(http.MethodPost, "/api/clients", strings.NewReader(`{"verify_key":"callback-retry-client","remark":"created through callback platform"}`))
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

	deadline := time.Now().Add(30 * time.Second)
	for expected := 1; expected <= 3; expected++ {
		select {
		case got := <-attempts:
			if got != expected {
				t.Fatalf("callback attempt = %d, want %d", got, expected)
			}
		case <-time.After(time.Until(deadline)):
			t.Fatalf("timed out waiting for callback attempt %d", expected)
		}
	}
	select {
	case body := <-successBody:
		if !strings.Contains(string(body), "\"name\":\"client.created\"") {
			t.Fatalf("unexpected callback success body: %s", string(body))
		}
	case <-time.After(time.Until(deadline)):
		t.Fatal("timed out waiting for callback success")
	}

	trafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(`{"client_id":1,"in":3,"out":4}`))
	trafficReq.Header.Set("Content-Type", "application/json")
	trafficReq.Header.Set("X-Node-Token", "callback-secret")
	trafficReq.Header.Set("X-Platform-Role", "admin")
	trafficResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(trafficResp, trafficReq)
	if trafficResp.Code != http.StatusOK || !strings.Contains(trafficResp.Body.String(), "\"message\":\"traffic accepted\"") {
		t.Fatalf("POST /api/traffic status = %d body=%s", trafficResp.Code, trafficResp.Body.String())
	}
	select {
	case got := <-attempts:
		t.Fatalf("live-only traffic event should not trigger callback, got extra attempt %d", got)
	case <-time.After(1500 * time.Millisecond):
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "callback-secret")
	statusResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status status = %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	if body := statusResp.Body.String(); !strings.Contains(body, "\"callback_deliveries\":1") ||
		!strings.Contains(body, "\"callback_failures\":2") ||
		!strings.Contains(body, "\"last_callback_status_code\":204") {
		t.Fatalf("GET /api/system/status should expose callback retry metrics, got %s", body)
	}
}
