package routers

import (
	"bytes"
	"io"
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

func TestNodeRegistrationEndpointExposesClusterSnapshot(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nnode_changes_window=2048\nnode_batch_max_items=80\nnode_idempotency_ttl_seconds=600\nweb_username=admin\nweb_password=secret\n")
	if err := file.GetDb().NewUser(&file.User{Id: 1, Username: "tenant", Password: "secret", Status: 1, TotalFlow: &file.Flow{}}); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{Id: 1, UserId: 1, VerifyKey: "registration-client", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	req := httptest.NewRequest(http.MethodGet, "/api/system/registration", nil)
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/registration status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"store_mode\":\"local\"") ||
		!strings.Contains(body, "\"api_base\":\"/api\"") ||
		!strings.Contains(body, "\"health\":") ||
		!strings.Contains(body, "\"protocol\":") ||
		!strings.Contains(body, "\"counts\":") {
		t.Fatalf("GET /api/system/registration body = %s", body)
	}
}

func TestNodeOverviewEndpointCombinesRegistrationAndUsageSnapshot(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	user := &file.User{Id: 1, Username: "overview-user", Password: "secret", Status: 1, TotalFlow: &file.Flow{InletFlow: 10, ExportFlow: 20}}
	if err := file.GetDb().NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	client := &file.Client{Id: 1, UserId: user.Id, VerifyKey: "overview-client", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{InletFlow: 10, ExportFlow: 20}}
	client.SetOwnerUserID(user.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	overviewPath := webapi.NodeDirectRouteCatalog("").Overview
	req := httptest.NewRequest(http.MethodGet, overviewPath, nil)
	req.Header.Set("X-Node-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200 body=%s", overviewPath, resp.Code, resp.Body.String())
	}
	body := resp.Body.String()
	if !strings.Contains(body, "\"registration\":") ||
		!strings.Contains(body, "\"usage_snapshot\":") ||
		!strings.Contains(body, "\"verify_key\":\"overview-client\"") {
		t.Fatalf("GET %s body = %s", overviewPath, body)
	}
}

func TestNodeConfigImportResetsEpochAndReplacesSnapshot(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	if err := file.GetDb().NewUser(&file.User{Id: 1, Username: "old-user", Password: "secret", Status: 1, TotalFlow: &file.Flow{}}); err != nil {
		t.Fatalf("NewUser(old) error = %v", err)
	}
	if err := file.GetDb().NewClient(&file.Client{Id: 1, UserId: 1, VerifyKey: "old-client", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}); err != nil {
		t.Fatalf("NewClient(old) error = %v", err)
	}
	swapRecoveredNodeStore(t, file.NewLocalStore())

	gin.SetMode(gin.TestMode)
	handler := Init()

	statusReq := httptest.NewRequest(http.MethodGet, "/api/system/status", nil)
	statusReq.Header.Set("X-Node-Token", "secret")
	statusResp := httptest.NewRecorder()
	handler.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/status before import status = %d, want 200 body=%s", statusResp.Code, statusResp.Body.String())
	}
	beforeEpoch := recoveredNodeConfigEpoch(t, statusResp.Body.Bytes())

	importReq := httptest.NewRequest(http.MethodPost, "/api/system/import", strings.NewReader(string(recoveredNodeImportSnapshot(t))))
	importReq.Header.Set("Content-Type", "application/json")
	importReq.Header.Set("X-Node-Token", "secret")
	importResp := httptest.NewRecorder()
	handler.ServeHTTP(importResp, importReq)
	if importResp.Code != http.StatusOK {
		t.Fatalf("POST /api/system/import status = %d, want 200 body=%s", importResp.Code, importResp.Body.String())
	}
	var importPayload nodeConfigImportResponse
	decodeManagementData(t, importResp.Body.Bytes(), &importPayload)
	if strings.TrimSpace(importPayload.ConfigEpoch) == "" || importPayload.ConfigEpoch == beforeEpoch {
		t.Fatalf("config epoch after import = %q, before=%q", importPayload.ConfigEpoch, beforeEpoch)
	}

	configReq := httptest.NewRequest(http.MethodGet, "/api/system/export", nil)
	configReq.Header.Set("X-Node-Token", "secret")
	configResp := httptest.NewRecorder()
	handler.ServeHTTP(configResp, configReq)
	if configResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/export after import status = %d, want 200 body=%s", configResp.Code, configResp.Body.String())
	}
	body := configResp.Body.String()
	if !strings.Contains(body, "\"import-user\"") ||
		!strings.Contains(body, "\"import-client\"") ||
		strings.Contains(body, "\"old-user\"") ||
		strings.Contains(body, "\"old-client\"") {
		t.Fatalf("GET /api/system/export after import body = %s", body)
	}
}

func TestNodeConfigImportInvalidatesRealtimeSessions(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nnode_token=secret\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

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

	importReq, err := http.NewRequest(http.MethodPost, srv.URL+"/api/system/import", bytes.NewReader(recoveredNodeImportSnapshot(t)))
	if err != nil {
		t.Fatalf("http.NewRequest(config import) error = %v", err)
	}
	importReq.Header.Set("Content-Type", "application/json")
	importReq.Header.Set("X-Node-Token", "secret")
	importResp, err := srv.Client().Do(importReq)
	if err != nil {
		t.Fatalf("srv.Client().Do(config import) error = %v", err)
	}
	defer func() { _ = importResp.Body.Close() }()
	importBody, err := io.ReadAll(importResp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll(config import response) error = %v", err)
	}
	if importResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/system/import status = %d, want 200 body=%s", importResp.StatusCode, string(importBody))
	}
	var importPayload nodeConfigImportResponse
	decodeManagementData(t, importBody, &importPayload)

	epochChanged := readNodeWSFrameUntil(t, conn, 5*time.Second, func(frame nodeWSFrame) bool {
		return frame.Type == "epoch_changed"
	})
	if !strings.Contains(string(epochChanged.Body), "\"config_epoch\":\""+importPayload.ConfigEpoch+"\"") ||
		!strings.Contains(string(epochChanged.Body), "\"reason\":\"config_import\"") {
		t.Fatalf("unexpected epoch_changed frame: %+v body=%s", epochChanged, string(epochChanged.Body))
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var closed nodeWSFrame
	if err := conn.ReadJSON(&closed); err == nil {
		t.Fatalf("expected realtime websocket to close after config import, got %+v", closed)
	}
}
