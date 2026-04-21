package routers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/gin-gonic/gin"
)

func createNodeTrafficCallbackClient(t *testing.T) *file.Client {
	t.Helper()
	user := createTestUser(t, 1, "tenant", "secret")
	client := &file.Client{
		Id:        1,
		UserId:    user.Id,
		VerifyKey: "callback-traffic-client",
		Status:    true,
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		FlowLimit: 1024,
	}
	client.SetOwnerUserID(user.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient(%d) error = %v", client.Id, err)
	}
	return client
}

func TestNodeTrafficReportsThresholdEventViaCallbackAndAliasFields(t *testing.T) {
	resetTestDB(t)
	callbackBodies := make(chan []byte, 2)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		callbackBodies <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callbackSrv.Close()

	loadRecoveredNodeConfig(t, strings.Join([]string{
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
		"node_traffic_report_interval_seconds=1",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	createNodeTrafficCallbackClient(t)
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	trafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(`{"client_id":1,"in":6,"out":5}`))
	trafficReq.Header.Set("Content-Type", "application/json")
	trafficReq.Header.Set("X-Node-Token", "callback-secret")
	trafficResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(trafficResp, trafficReq)
	if trafficResp.Code != http.StatusOK || !strings.Contains(trafficResp.Body.String(), "\"message\":\"traffic accepted\"") {
		t.Fatalf("POST /api/traffic status = %d, want 200 body=%s", trafficResp.Code, trafficResp.Body.String())
	}

	var callbackBody []byte
	select {
	case callbackBody = <-callbackBodies:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback body")
	}
	body := string(callbackBody)
	if !strings.Contains(body, "\"name\":\"client.traffic.reported\"") ||
		!strings.Contains(body, "\"resource\":\"client\"") ||
		!strings.Contains(body, "\"id\":1") ||
		!strings.Contains(body, "\"client_id\":1") ||
		!strings.Contains(body, "\"traffic_in\":6") ||
		!strings.Contains(body, "\"traffic_out\":5") ||
		!strings.Contains(body, "\"traffic_total_in\":6") ||
		!strings.Contains(body, "\"traffic_total_out\":5") ||
		!strings.Contains(body, "\"traffic_trigger\":\"initial\"") {
		t.Fatalf("unexpected callback body: %s", body)
	}

	select {
	case extra := <-callbackBodies:
		t.Fatalf("expected one callback attempt for traffic threshold event, got extra body=%s", string(extra))
	case <-time.After(1500 * time.Millisecond):
	}
}

func TestNodeTrafficReportedCallbackFailureDoesNotQueue(t *testing.T) {
	resetTestDB(t)
	attempts := make(chan []byte, 4)
	callbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		attempts <- body
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer callbackSrv.Close()

	loadRecoveredNodeConfig(t, strings.Join([]string{
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
		"node_traffic_report_interval_seconds=1",
		"web_username=admin",
		"web_password=secret",
	}, "\n")+"\n")
	createNodeTrafficCallbackClient(t)
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	runtime := NewManagedRuntime(nil)
	defer runtime.Stop()

	trafficReq := httptest.NewRequest(http.MethodPost, "/api/traffic", strings.NewReader(`{"client_id":1,"in":6,"out":5}`))
	trafficReq.Header.Set("Content-Type", "application/json")
	trafficReq.Header.Set("X-Node-Token", "callback-secret")
	trafficResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(trafficResp, trafficReq)
	if trafficResp.Code != http.StatusOK || !strings.Contains(trafficResp.Body.String(), "\"message\":\"traffic accepted\"") {
		t.Fatalf("POST /api/traffic status = %d, want 200 body=%s", trafficResp.Code, trafficResp.Body.String())
	}

	select {
	case body := <-attempts:
		if !strings.Contains(string(body), "\"name\":\"client.traffic.reported\"") {
			t.Fatalf("unexpected callback failure body: %s", string(body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback failure attempt")
	}

	select {
	case body := <-attempts:
		t.Fatalf("expected one callback failure attempt with retry_max=0, got extra body=%s", string(body))
	case <-time.After(1500 * time.Millisecond):
	}

	queueReq := httptest.NewRequest(http.MethodGet, "/api/callbacks/queue?limit=10", nil)
	queueReq.Header.Set("X-Node-Token", "callback-secret")
	queueResp := httptest.NewRecorder()
	runtime.Handler.ServeHTTP(queueResp, queueReq)
	if queueResp.Code != http.StatusOK {
		t.Fatalf("GET /api/callbacks/queue status = %d body=%s", queueResp.Code, queueResp.Body.String())
	}
	if body := queueResp.Body.String(); !strings.Contains(body, "\"callback_queue_size\":0") || strings.Contains(body, "client.traffic.reported") {
		t.Fatalf("GET /api/callbacks/queue should stay empty for ephemeral traffic event, got %s", body)
	}

	deadline := time.Now().Add(5 * time.Second)
	statusBody := ""
	for {
		statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
		statusReq.Header.Set("X-Node-Token", "callback-secret")
		statusResp := httptest.NewRecorder()
		runtime.Handler.ServeHTTP(statusResp, statusReq)
		if statusResp.Code != http.StatusOK {
			t.Fatalf("GET /api/system/status status = %d body=%s", statusResp.Code, statusResp.Body.String())
		}
		statusBody = statusResp.Body.String()
		if strings.Contains(statusBody, "\"callback_failures\":1") && strings.Contains(statusBody, "\"callback_queue_size\":0") {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(statusBody, "\"callback_failures\":1") || !strings.Contains(statusBody, "\"callback_queue_size\":0") {
		t.Fatalf("GET /api/system/status should expose failed-but-not-queued traffic callback, got %s", statusBody)
	}
}
