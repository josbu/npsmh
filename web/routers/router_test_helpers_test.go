package routers

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gorilla/sessions"
)

func writeTestConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(closeAllNodeRuntimeStateWriters)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	_, callerFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(callerFile), "..", ".."))
}

func resetTestDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(closeAllNodeRuntimeStateWriters)
	if err := os.MkdirAll(filepath.Join(root, "conf"), 0o755); err != nil {
		t.Fatalf("create temp conf dir: %v", err)
	}
	oldDb := file.Db
	oldIndexes := file.SnapshotRuntimeIndexes()
	db := &file.DbUtils{JsonDb: file.NewJsonDb(root)}
	db.JsonDb.Global = &file.Glob{}
	file.ReplaceDb(db)
	file.ReplaceRuntimeIndexes(file.NewRuntimeIndexes())
	t.Cleanup(func() {
		file.ReplaceDb(oldDb)
		file.ReplaceRuntimeIndexes(oldIndexes)
	})
	return root
}

func createTestUser(t *testing.T, id int, username, password string) *file.User {
	t.Helper()
	user := &file.User{
		Id:         id,
		Username:   username,
		Password:   password,
		Kind:       "local",
		Status:     1,
		TotalFlow:  &file.Flow{},
		MaxClients: 8,
	}
	if err := file.GetDb().NewUser(user); err != nil {
		t.Fatalf("NewUser(%d) error = %v", id, err)
	}
	return user
}

func createOwnedTestClient(t *testing.T, id int, ownerUserID int, remark string) *file.Client {
	t.Helper()
	client := &file.Client{
		Id:        id,
		Status:    true,
		VerifyKey: fmt.Sprintf("vk-%d", id),
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
		Remark:    remark,
	}
	client.SetOwnerUserID(ownerUserID)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient(%d) error = %v", id, err)
	}
	return client
}

func createTestTunnel(t *testing.T, id int, client *file.Client, port int) *file.Tunnel {
	t.Helper()
	tunnel := &file.Tunnel{
		Id:         id,
		Port:       port,
		Mode:       "tcp",
		Status:     true,
		Client:     client,
		TargetType: common.CONN_TCP,
		Target: &file.Target{
			TargetStr: "127.0.0.1:80",
		},
	}
	if err := file.GetDb().NewTask(tunnel); err != nil {
		t.Fatalf("NewTask(%d) error = %v", id, err)
	}
	return tunnel
}

func createTestHost(t *testing.T, id int, client *file.Client, host string) *file.Host {
	t.Helper()
	record := &file.Host{
		Id:     id,
		Host:   host,
		Client: client,
		Target: &file.Target{
			TargetStr: "127.0.0.1:80",
		},
	}
	if err := file.GetDb().NewHost(record); err != nil {
		t.Fatalf("NewHost(%d) error = %v", id, err)
	}
	return record
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func sessionCookiesFromIdentity(t *testing.T, cfg *servercfg.Snapshot, identity *webservice.SessionIdentity) []*http.Cookie {
	t.Helper()
	encoded, err := webservice.MarshalSessionIdentity(identity)
	if err != nil {
		t.Fatalf("MarshalSessionIdentity() error = %v", err)
	}
	authKey, encKey := deriveTestSessionKeys(cfg)
	store := sessions.NewCookieStore(authKey, encKey)
	store.Options = &sessions.Options{
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp := httptest.NewRecorder()
	session, err := store.Get(req, "nps_session")
	if err != nil {
		t.Fatalf("CookieStore.Get() error = %v", err)
	}
	session.Values[webservice.SessionIdentityKey] = encoded
	if err := session.Save(req, resp); err != nil {
		t.Fatalf("session.Save() error = %v", err)
	}
	return resp.Result().Cookies()
}

func discoveryActionPaths(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	actions := discoveryActions(t, body)
	paths := make(map[string]bool, len(actions))
	for _, action := range actions {
		if action.Path == "" {
			continue
		}
		paths[action.Path] = true
	}
	return paths
}

func discoveryActions(t *testing.T, body []byte) []publishedAction {
	t.Helper()
	var payload struct {
		Actions []publishedAction `json:"actions"`
	}
	decodeManagementData(t, body, &payload)
	return payload.Actions
}

func discoveryRoutes(t *testing.T, body []byte) webapi.ManagementRoutes {
	t.Helper()
	var payload struct {
		Routes webapi.ManagementRoutes `json:"routes"`
	}
	decodeManagementData(t, body, &payload)
	return payload.Routes
}

type publishedAction struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Method   string `json:"method"`
	Path     string `json:"path"`
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, ok := range values {
		if ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func deriveTestSessionKeys(cfg *servercfg.Snapshot) ([]byte, []byte) {
	seed := fmt.Sprintf("%s|%s|%s|%s", cfg.Web.Username, cfg.Web.Password, cfg.Auth.Key, cfg.App.Name)
	authSum := sha256.Sum256([]byte("auth:" + seed))
	encSum := sha256.Sum256([]byte("enc:" + seed))
	return authSum[:], encSum[:]
}

type managementDataEnvelope struct {
	Data json.RawMessage               `json:"data"`
	Meta webapi.ManagementResponseMeta `json:"meta"`
}

func decodeManagementData(t *testing.T, body []byte, target interface{}) webapi.ManagementResponseMeta {
	t.Helper()
	var envelope managementDataEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("json.Unmarshal(management envelope) error = %v body=%s", err, string(body))
	}
	if target != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			t.Fatalf("json.Unmarshal(management data) error = %v body=%s data=%s", err, string(body), string(envelope.Data))
		}
	}
	return envelope.Meta
}

func decodeManagementFrameData(t *testing.T, frame nodeWSFrame, target interface{}) webapi.ManagementResponseMeta {
	t.Helper()
	return decodeManagementData(t, frame.Body, target)
}
