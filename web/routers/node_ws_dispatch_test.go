package routers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

func TestDecodeWSParamsUsesUnifiedStructuredValueEncoding(t *testing.T) {
	params, err := decodeWSParams("/api/clients/17/actions/update?search=abc", json.RawMessage(`{"manager_user_ids":[1,2],"status":true,"rate_limit_total_bps":3}`))
	if err != nil {
		t.Fatalf("decodeWSParams() error = %v", err)
	}
	if params["search"] != "abc" {
		t.Fatalf("search = %q, want abc", params["search"])
	}
	if params["manager_user_ids"] != `[1,2]` {
		t.Fatalf("manager_user_ids = %q, want [1,2]", params["manager_user_ids"])
	}
	if params["status"] != "true" {
		t.Fatalf("status = %q, want true", params["status"])
	}
	if params["rate_limit_total_bps"] != "3" {
		t.Fatalf("rate_limit_total_bps = %q, want 3", params["rate_limit_total_bps"])
	}
}

func TestResolveNodeWSManagedItemRequest(t *testing.T) {
	request, err := resolveNodeWSManagedItemRequest("/webhooks/17/actions/update", "/webhooks/")
	if err != nil {
		t.Fatalf("resolveNodeWSManagedItemRequest(valid action) error = %v", err)
	}
	if request.ID != 17 || request.Action != "update" {
		t.Fatalf("resolveNodeWSManagedItemRequest(valid action) = %+v, want id=17 action=update", request)
	}

	request, err = resolveNodeWSManagedItemRequest("/realtime/subscriptions/19", "/realtime/subscriptions/")
	if err != nil {
		t.Fatalf("resolveNodeWSManagedItemRequest(valid get) error = %v", err)
	}
	if request.ID != 19 || request.Action != "" {
		t.Fatalf("resolveNodeWSManagedItemRequest(valid get) = %+v, want id=19 action=''", request)
	}

	if _, err = resolveNodeWSManagedItemRequest("/webhooks/not-a-number", "/webhooks/"); !errors.Is(err, errNodeWSManagedItemInvalidID) {
		t.Fatalf("resolveNodeWSManagedItemRequest(invalid id) error = %v, want errNodeWSManagedItemInvalidID", err)
	}
	if _, err = resolveNodeWSManagedItemRequest("/webhooks/17/actions", "/webhooks/"); !errors.Is(err, errNodeWSManagedItemUnknownPath) {
		t.Fatalf("resolveNodeWSManagedItemRequest(unknown path) error = %v, want errNodeWSManagedItemUnknownPath", err)
	}
}

func TestStampNodeWSSubscriptionResponseDataTimestamp(t *testing.T) {
	listStamped, ok := stampNodeWSSubscriptionResponseDataTimestamp(nodeWSSubscriptionListPayload{
		Items: []nodeWSSubscriptionPayload{{ID: 1}},
	}, 77).(nodeWSSubscriptionListPayload)
	if !ok {
		t.Fatal("stampNodeWSSubscriptionResponseDataTimestamp(list) type assertion failed")
	}
	if listStamped.Timestamp != 77 || len(listStamped.Items) != 1 || listStamped.Items[0].ID != 1 {
		t.Fatalf("unexpected stamped list payload: %+v", listStamped)
	}

	mutationStamped, ok := stampNodeWSSubscriptionResponseDataTimestamp(nodeWSSubscriptionMutationPayload{
		Item: &nodeWSSubscriptionPayload{ID: 9},
	}, 88).(nodeWSSubscriptionMutationPayload)
	if !ok {
		t.Fatal("stampNodeWSSubscriptionResponseDataTimestamp(mutation) type assertion failed")
	}
	if mutationStamped.Timestamp != 88 || mutationStamped.Item == nil || mutationStamped.Item.ID != 9 {
		t.Fatalf("unexpected stamped mutation payload: %+v", mutationStamped)
	}
}

func TestDispatchNodeWSDirectRouteRejectsMethodMismatch(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-method-mismatch",
		"platform_tokens=ws-method-mismatch-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_method_mismatch",
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

	state := NewState(nil)
	defer state.Close()

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-method-mismatch-request-1",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-method-mismatch",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/settings/global/actions/update",
	})
	if response.Status != http.StatusMethodNotAllowed {
		t.Fatalf("dispatchNodeWSRequestWithBase(method mismatch) status = %d, want 405 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(method mismatch) error = %q, want empty formal error field", response.Error)
	}
	if !strings.Contains(string(response.Body), `"code":"method_not_allowed"`) || !strings.Contains(string(response.Body), `"message":"method not allowed"`) {
		t.Fatalf("dispatchNodeWSRequestWithBase(method mismatch) body = %s, want method_not_allowed management error", string(response.Body))
	}
}

func TestDispatchNodeWSGlobalUpdateRecordsOperation(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-global-op",
		"platform_tokens=ws-global-op-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_global_op",
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

	state := NewState(nil)
	defer state.Close()

	actor := webapi.AdminActorWithFallback("admin", "admin")
	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-global-op-request-1",
			Source:    "node-ws",
		},
	}, actor, nodeWSFrame{
		ID:     "ws-global-op",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/settings/global/actions/update",
		Headers: map[string]string{
			"X-Operation-ID": "ws-global-op-1",
		},
		Body: json.RawMessage(`{"entry_acl_mode":1,"entry_acl_rules":"10.0.0.0/8"}`),
	})
	if response.Status != http.StatusOK {
		t.Fatalf("dispatchNodeWSRequestWithBase(global update) status = %d body=%s", response.Status, string(response.Body))
	}
	if got := response.Headers["X-Operation-ID"]; got != "ws-global-op-1" {
		t.Fatalf("global update operation header = %q, want ws-global-op-1", got)
	}

	scope := webservice.ResolveNodeAccessScope(webapi.PrincipalFromActor(actor))
	result := state.NodeOperations().Query(webservice.NodeOperationQueryInput{
		Scope:       scope,
		OperationID: "ws-global-op-1",
		Limit:       4,
	})
	if len(result.Items) != 1 {
		t.Fatalf("operations query len = %d, want 1", len(result.Items))
	}
	if result.Items[0].Kind != "resource" {
		t.Fatalf("operation kind = %q, want resource", result.Items[0].Kind)
	}
	if len(result.Items[0].Paths) != 1 || result.Items[0].Paths[0] != "/api/settings/global/actions/update" {
		t.Fatalf("operation paths = %+v, want [/api/settings/global/actions/update]", result.Items[0].Paths)
	}
}

func TestDispatchNodeWSFormalResourceRouteRecordsOperation(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-manage",
		"platform_tokens=ws-manage-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_manage",
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

	state := NewState(nil)
	defer state.Close()

	actor := webapi.AdminActorWithFallback("admin", "admin")
	frame := nodeWSFrame{
		ID:     "ws-formal-add",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/clients",
		Headers: map[string]string{
			"X-Operation-ID": "ws-formal-op-1",
		},
		Body: json.RawMessage(`{"verify_key":"ws-formal-client","remark":"ws tracked"}`),
	}
	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-formal-request-1",
			Source:    "node-ws",
		},
	}, actor, frame)
	if response.Status != http.StatusOK {
		t.Fatalf("dispatchNodeWSRequestWithBase(formal) status = %d body=%s", response.Status, string(response.Body))
	}
	if got := response.Headers["X-Operation-ID"]; got != "ws-formal-op-1" {
		t.Fatalf("formal ws operation header = %q, want ws-formal-op-1", got)
	}

	scope := webservice.ResolveNodeAccessScope(webapi.PrincipalFromActor(actor))
	result := state.NodeOperations().Query(webservice.NodeOperationQueryInput{
		Scope:       scope,
		OperationID: "ws-formal-op-1",
		Limit:       1,
	})
	if len(result.Items) != 1 {
		t.Fatalf("operations query len = %d, want 1", len(result.Items))
	}
	if result.Items[0].Kind != "resource" || len(result.Items[0].Paths) != 1 || result.Items[0].Paths[0] != "/api/clients" {
		t.Fatalf("unexpected ws resource operation record: %+v", result.Items[0])
	}
	if state.RuntimeStatus().Operations().LastMutationAt == 0 {
		t.Fatalf("LastMutationAt = 0, want non-zero after ws formal request")
	}
}

func TestDispatchNodeWSRejectsLegacyProtectedPaths(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-legacy",
		"platform_tokens=ws-legacy-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_legacy",
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

	state := NewState(nil)
	defer state.Close()

	actor := webapi.AdminActorWithFallback("admin", "admin")
	frame := nodeWSFrame{
		ID:     "ws-legacy-add",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/client/add",
		Headers: map[string]string{
			"X-Operation-ID": "ws-legacy-op-1",
		},
		Body: json.RawMessage(`{"verify_key":"ws-legacy-client","remark":"ws legacy route"}`),
	}
	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-legacy-request-1",
			Source:    "node-ws",
		},
	}, actor, frame)
	if response.Status != http.StatusNotFound {
		t.Fatalf("dispatchNodeWSRequestWithBase(legacy) status = %d, want 404 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "unknown ws request path" {
		t.Fatalf("legacy ws error = %q, want unknown ws request path", response.Error)
	}

	frame.Path = "/status"
	frame.ID = "ws-bare-status"
	frame.Headers["X-Operation-ID"] = "ws-bare-op-1"
	response = dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-bare-request-1",
			Source:    "node-ws",
		},
	}, actor, frame)
	if response.Status != http.StatusNotFound {
		t.Fatalf("dispatchNodeWSRequestWithBase(bare internal path) status = %d, want 404 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "unknown ws request path" {
		t.Fatalf("bare ws error = %q, want unknown ws request path", response.Error)
	}
}

func TestDispatchNodeWSResourceReadsRequireDeclaredPermissions(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-read-perms",
		"platform_tokens=ws-read-perms-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_read_perms",
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

	state := NewState(nil)
	defer state.Close()

	actor := &webapi.Actor{
		Kind:      "service",
		SubjectID: "service:tenant-a",
		Username:  "tenant-a-service",
		ClientIDs: []int{1},
		Roles:     []string{"custom"},
	}
	for _, path := range []string{
		"/api/users",
		"/api/users/1",
		"/api/clients",
		"/api/clients/1",
		"/api/clients/1/connections",
		"/api/tunnels",
		"/api/tunnels/1",
		"/api/hosts",
		"/api/hosts/1",
	} {
		response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
			Metadata: webapi.RequestMetadata{
				RequestID: "ws-read-perms-" + strings.ReplaceAll(strings.Trim(path, "/"), "/", "-"),
				Source:    "node-ws",
			},
		}, actor, nodeWSFrame{
			ID:     "read-perms-" + strings.ReplaceAll(strings.Trim(path, "/"), "/", "-"),
			Type:   "request",
			Method: http.MethodGet,
			Path:   path,
		})
		if response.Status != http.StatusForbidden || !strings.Contains(string(response.Body), `"code":"forbidden"`) {
			t.Fatalf("dispatchNodeWSRequestWithBase(%s) = status %d error %q, want 403 forbidden body=%s", path, response.Status, response.Error, string(response.Body))
		}
	}
}

func TestResolveNodeWSResourceDispatchMatchesHTTPPermissions(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-resource-dispatch",
		"platform_tokens=ws-resource-dispatch-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_resource_dispatch",
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

	state := NewState(nil)
	defer state.Close()

	tests := []struct {
		name        string
		resource    string
		resourceID  int
		subresource string
		action      string
		method      string
		permission  string
		wantHandler bool
		wantPath    bool
		wantMethod  bool
	}{
		{name: "users list", resource: "users", method: http.MethodGet, permission: webservice.PermissionManagementAdmin, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "users update", resource: "users", resourceID: 7, subresource: "actions", action: "update", method: http.MethodPost, permission: webservice.PermissionManagementAdmin, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "clients create", resource: "clients", method: http.MethodPost, permission: webservice.PermissionClientsCreate, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "clients delete", resource: "clients", resourceID: 7, subresource: "actions", action: "delete", method: http.MethodPost, permission: webservice.PermissionClientsDelete, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "clients delete method mismatch", resource: "clients", resourceID: 7, subresource: "actions", action: "delete", method: http.MethodGet, wantHandler: false, wantPath: true, wantMethod: false},
		{name: "tunnels clear", resource: "tunnels", resourceID: 3, subresource: "actions", action: "clear", method: http.MethodPost, permission: webservice.PermissionTunnelsControl, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "hosts cert suggestion", resource: "hosts", subresource: "cert-suggestion", method: http.MethodGet, permission: webservice.PermissionHostsRead, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "hosts delete", resource: "hosts", resourceID: 11, subresource: "actions", action: "delete", method: http.MethodPost, permission: webservice.PermissionHostsDelete, wantHandler: true, wantPath: true, wantMethod: true},
		{name: "unknown action", resource: "clients", resourceID: 1, subresource: "actions", action: "unknown", method: http.MethodPost, wantHandler: false, wantPath: false, wantMethod: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatch, pathMatched, methodMatched := resolveNodeWSResourceDispatch(state, tt.resource, tt.resourceID, tt.subresource, tt.action, tt.method)
			if got := dispatch.handler != nil; got != tt.wantHandler {
				t.Fatalf("resolveNodeWSResourceDispatch() handler = %v, want %v", got, tt.wantHandler)
			}
			if pathMatched != tt.wantPath {
				t.Fatalf("resolveNodeWSResourceDispatch() pathMatched = %v, want %v", pathMatched, tt.wantPath)
			}
			if methodMatched != tt.wantMethod {
				t.Fatalf("resolveNodeWSResourceDispatch() methodMatched = %v, want %v", methodMatched, tt.wantMethod)
			}
			if got := strings.TrimSpace(dispatch.action.Permission); got != tt.permission {
				t.Fatalf("resolveNodeWSResourceDispatch() permission = %q, want %q", got, tt.permission)
			}
		})
	}
}

func TestDispatchNodeWSFormalResourceUnknownActionReturnsNotFound(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-resource-notfound",
		"platform_tokens=ws-resource-notfound-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_resource_notfound",
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

	state := NewState(nil)
	defer state.Close()

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-resource-notfound-request-1",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-resource-notfound",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/clients/1/actions/unknown",
	})
	if response.Status != http.StatusNotFound {
		t.Fatalf("dispatchNodeWSRequestWithBase(resource unknown action) status = %d, want 404 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(resource unknown action) error = %q, want empty formal error field", response.Error)
	}
	if !strings.Contains(string(response.Body), `"code":"unknown_ws_request_path"`) || !strings.Contains(string(response.Body), `"message":"unknown ws request path"`) {
		t.Fatalf("dispatchNodeWSRequestWithBase(resource unknown action) body = %s, want unknown ws request path management error", string(response.Body))
	}
}

func TestDispatchNodeWSFormalResourceMethodMismatchReturnsMethodNotAllowed(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-resource-method",
		"platform_tokens=ws-resource-method-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_resource_method",
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

	state := NewState(nil)
	defer state.Close()

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-resource-method-request-1",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-resource-method",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients/1/actions/delete",
	})
	if response.Status != http.StatusMethodNotAllowed {
		t.Fatalf("dispatchNodeWSRequestWithBase(resource method mismatch) status = %d, want 405 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(resource method mismatch) error = %q, want empty formal error field", response.Error)
	}
	if !strings.Contains(string(response.Body), `"code":"method_not_allowed"`) || !strings.Contains(string(response.Body), `"message":"method not allowed"`) {
		t.Fatalf("dispatchNodeWSRequestWithBase(resource method mismatch) body = %s, want method_not_allowed management error", string(response.Body))
	}
}

func TestDispatchNodeWSManagedItemRoutesDistinguishUnknownActionAndMethodMismatch(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-managed-items",
		"platform_tokens=ws-managed-items-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_managed_items",
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

	state := NewState(nil)
	defer state.Close()

	actor := webapi.AdminActorWithFallback("admin", "admin")
	tests := []struct {
		name              string
		method            string
		path              string
		withSubscriptions bool
		wantStatus        int
		wantCode          string
	}{
		{name: "webhook method mismatch", method: http.MethodGet, path: "/api/webhooks/1/actions/update", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "webhook unknown action", method: http.MethodPost, path: "/api/webhooks/1/actions/unknown", wantStatus: http.StatusNotFound, wantCode: "unknown_ws_request_path"},
		{name: "subscription method mismatch", method: http.MethodGet, path: "/api/realtime/subscriptions/1/actions/update", withSubscriptions: true, wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed"},
		{name: "subscription unknown action", method: http.MethodPost, path: "/api/realtime/subscriptions/1/actions/unknown", withSubscriptions: true, wantStatus: http.StatusNotFound, wantCode: "unknown_ws_request_path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := nodeWSDispatchBase{
				Metadata: webapi.RequestMetadata{
					RequestID: "ws-managed-item-" + strings.ReplaceAll(tt.name, " ", "-"),
					Source:    "node-ws",
				},
			}
			if tt.withSubscriptions {
				base.Subscriptions = newNodeWSSubscriptionRegistry()
			}
			response := dispatchNodeWSRequestWithBase(state, base, actor, nodeWSFrame{
				ID:     "ws-managed-item-" + strings.ReplaceAll(tt.name, " ", "-"),
				Type:   "request",
				Method: tt.method,
				Path:   tt.path,
			})
			if response.Status != tt.wantStatus {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) status = %d, want %d body=%s", tt.path, response.Status, tt.wantStatus, string(response.Body))
			}
			if response.Error != "" {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) error = %q, want empty formal error field", tt.path, response.Error)
			}
			if !strings.Contains(string(response.Body), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) body = %s, want code %s", tt.path, string(response.Body), tt.wantCode)
			}
		})
	}
}

func TestDispatchNodeWSCallbackQueueRoutesRejectAnonymousActor(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-callback-auth",
		"platform_tokens=ws-callback-auth-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_callback_auth",
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

	state := NewState(nil)
	defer state.Close()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "list", method: http.MethodGet, path: "/api/callbacks/queue"},
		{name: "replay", method: http.MethodPost, path: "/api/callbacks/queue/actions/replay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
				Metadata: webapi.RequestMetadata{
					RequestID: "ws-callback-auth-" + tt.name,
					Source:    "node-ws",
				},
			}, nil, nodeWSFrame{
				ID:     "ws-callback-auth-" + tt.name,
				Type:   "request",
				Method: tt.method,
				Path:   tt.path,
			})
			if response.Status != http.StatusUnauthorized || response.Error != "" || !strings.Contains(string(response.Body), `"code":"unauthorized"`) {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) = status %d error %q, want 401 unauthorized body=%s", tt.path, response.Status, response.Error, string(response.Body))
			}
		})
	}
}

func TestDispatchNodeWSChangesRouteRejectsAnonymousActor(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-changes-auth",
		"platform_tokens=ws-changes-auth-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_changes_auth",
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

	state := NewState(nil)
	defer state.Close()

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-changes-auth-list",
			Source:    "node-ws",
		},
	}, nil, nodeWSFrame{
		ID:     "ws-changes-auth-list",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/system/changes",
	})
	if response.Status != http.StatusUnauthorized {
		t.Fatalf("dispatchNodeWSRequestWithBase(/api/system/changes) status = %d, want 401 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(/api/system/changes) error = %q, want empty formal error field", response.Error)
	}
	if !strings.Contains(string(response.Body), `"code":"unauthorized"`) || !strings.Contains(string(response.Body), `"message":"unauthorized"`) {
		t.Fatalf("dispatchNodeWSRequestWithBase(/api/system/changes) body = %s, want unauthorized management error", string(response.Body))
	}
}

func TestDispatchNodeWSWebhookRoutesRejectAnonymousActor(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-webhook-auth",
		"platform_tokens=ws-webhook-auth-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_webhook_auth",
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

	state := NewState(nil)
	defer state.Close()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list", method: http.MethodGet, path: "/api/webhooks"},
		{name: "create", method: http.MethodPost, path: "/api/webhooks", body: `{"url":"https://example.invalid/hook"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
				Metadata: webapi.RequestMetadata{
					RequestID: "ws-webhook-auth-" + tt.name,
					Source:    "node-ws",
				},
			}, nil, nodeWSFrame{
				ID:     "ws-webhook-auth-" + tt.name,
				Type:   "request",
				Method: tt.method,
				Path:   tt.path,
				Body:   json.RawMessage(tt.body),
			})
			if response.Status != http.StatusUnauthorized {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) status = %d, want 401 body=%s", tt.path, response.Status, string(response.Body))
			}
			if response.Error != "" {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) error = %q, want empty formal error field", tt.path, response.Error)
			}
			if !strings.Contains(string(response.Body), `"code":"unauthorized"`) {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) body = %s, want unauthorized management error", tt.path, string(response.Body))
			}
		})
	}
}

func TestDispatchNodeWSRealtimeSubscriptionRoutesRequireRegistry(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-subscription-runtime",
		"platform_tokens=ws-subscription-runtime-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_subscription_runtime",
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

	state := NewState(nil)
	defer state.Close()

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-subscription-runtime-list",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-subscription-runtime-list",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/realtime/subscriptions",
	})
	if response.Status != http.StatusBadRequest {
		t.Fatalf("dispatchNodeWSRequestWithBase(/api/realtime/subscriptions) status = %d, want 400 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(/api/realtime/subscriptions) error = %q, want empty formal error field", response.Error)
	}
	if !strings.Contains(string(response.Body), `"code":"realtime_subscriptions_unavailable"`) || !strings.Contains(string(response.Body), `"message":"websocket subscriptions are unavailable"`) {
		t.Fatalf("dispatchNodeWSRequestWithBase(/api/realtime/subscriptions) body = %s, want realtime_subscriptions_unavailable", string(response.Body))
	}
}

func TestDispatchNodeWSUserMutationsRequireManagementAdmin(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-user-mutations",
		"platform_tokens=ws-user-mutations-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_user_mutations",
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

	user := &file.User{
		Id:        1,
		Username:  "tenant-user",
		Password:  "secret",
		Status:    1,
		TotalFlow: &file.Flow{},
	}
	if err := file.GetDb().NewUser(user); err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}

	state := NewState(nil)
	defer state.Close()

	actor := &webapi.Actor{
		Kind:      "service",
		SubjectID: "service:tenant-user-admin",
		Username:  "tenant-user-admin",
		Roles:     []string{"custom"},
	}
	tests := []struct {
		name   string
		path   string
		body   string
		method string
	}{
		{name: "create", path: "/api/users", method: http.MethodPost, body: `{"username":"blocked-user","password":"secret"}`},
		{name: "update", path: "/api/users/1/actions/update", method: http.MethodPost, body: `{"username":"blocked-update"}`},
		{name: "status", path: "/api/users/1/actions/status", method: http.MethodPost, body: `{"status":false}`},
		{name: "delete", path: "/api/users/1/actions/delete", method: http.MethodPost, body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
				Metadata: webapi.RequestMetadata{
					RequestID: "ws-user-mutation-" + tt.name,
					Source:    "node-ws",
				},
			}, actor, nodeWSFrame{
				ID:     "ws-user-mutation-" + tt.name,
				Type:   "request",
				Method: tt.method,
				Path:   tt.path,
				Body:   json.RawMessage(tt.body),
			})
			if response.Status != http.StatusForbidden || !strings.Contains(string(response.Body), `"code":"forbidden"`) {
				t.Fatalf("dispatchNodeWSRequestWithBase(%s) = status %d error %q, want 403 forbidden body=%s", tt.path, response.Status, response.Error, string(response.Body))
			}
		})
	}
}

func TestDispatchNodeWSClientConnectionsRouteSupportsFormalRead(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-client-connections",
		"platform_tokens=ws-client-connections-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_client_connections",
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
		Version:   "1.2.3",
		Addr:      "127.0.0.1",
	}
	client.SetOwnerUserID(user.Id)
	if err := file.GetDb().NewClient(client); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	state := NewState(nil)
	defer state.Close()
	actor := webapi.AdminActorWithFallback("admin", "admin")

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-client-connections-request-1",
			Source:    "node-ws",
		},
	}, actor, nodeWSFrame{
		ID:     "ws-client-connections",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/clients/1/connections",
	})
	if response.Status != http.StatusOK {
		t.Fatalf("dispatchNodeWSRequestWithBase(client connections) status = %d body=%s", response.Status, string(response.Body))
	}
	if !strings.Contains(string(response.Body), `"data":[`) || !strings.Contains(string(response.Body), `"pagination"`) {
		t.Fatalf("client connections body = %s, want formal list payload", string(response.Body))
	}
}

func TestDispatchNodeWSWebhookListRequiresWebhookRuntime(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-webhook-runtime",
		"platform_tokens=ws-webhook-runtime-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_webhook_runtime",
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

	state := NewState(nil)
	defer state.Close()
	state.NodeWebhooks = nil

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-webhook-list-runtime-request-1",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-webhook-list-runtime",
		Type:   "request",
		Method: http.MethodGet,
		Path:   "/api/webhooks",
	})
	if response.Status != http.StatusInternalServerError {
		t.Fatalf("dispatchNodeWSRequestWithBase(webhooks list without runtime) status = %d, want 500 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(webhooks list without runtime) error = %q, want empty formal error field", response.Error)
	}
	if !strings.Contains(string(response.Body), `"code":"node_state_unavailable"`) || !strings.Contains(string(response.Body), `"message":"node state is unavailable"`) {
		t.Fatalf("dispatchNodeWSRequestWithBase(webhooks list without runtime) body = %s, want formal node_state_unavailable error", string(response.Body))
	}
}

func TestDispatchNodeWSSystemImportErrorsUseManagementErrorBody(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-import-errors",
		"platform_tokens=ws-import-errors-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_import_errors",
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

	state := NewState(nil)
	defer state.Close()
	state.App.Services.NodeStorage = &stubConfigImportStorage{
		snapshotErr: webservice.ErrSnapshotExportUnsupported,
	}

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-system-import-error-request-1",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-system-import-error",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/system/import",
		Body:   recoveredNodeImportSnapshot(t),
	})
	if response.Status != http.StatusNotImplemented {
		t.Fatalf("dispatchNodeWSRequestWithBase(system import error) status = %d, want 501 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(system import error) error = %q, want empty formal error field", response.Error)
	}
	var payload webapi.ManagementErrorResponse
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("json.Unmarshal(system import error body) error = %v body=%s", err, string(response.Body))
	}
	if payload.Error.Code != "config_export_unsupported" {
		t.Fatalf("system import error code = %q, want config_export_unsupported", payload.Error.Code)
	}
	if payload.Error.Message != webservice.ErrSnapshotExportUnsupported.Error() {
		t.Fatalf("system import error message = %q, want %q", payload.Error.Message, webservice.ErrSnapshotExportUnsupported.Error())
	}
}

func TestDispatchNodeWSSystemImportRecordsOperationAndHeader(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-import-success",
		"platform_tokens=ws-import-success-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_import_success",
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

	state := NewState(nil)
	defer state.Close()
	state.App.Services.NodeStorage = &stubConfigImportStorage{
		snapshot: &file.ConfigSnapshot{
			Global: &file.Glob{},
		},
	}

	actor := webapi.AdminActorWithFallback("admin", "admin")
	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-system-import-success-request-1",
			Source:    "node-ws",
		},
	}, actor, nodeWSFrame{
		ID:     "ws-system-import-success",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/system/import",
		Headers: map[string]string{
			"X-Operation-ID": "ws-system-import-op-1",
		},
		Body: recoveredNodeImportSnapshot(t),
	})
	if response.Status != http.StatusOK {
		t.Fatalf("dispatchNodeWSRequestWithBase(system import success) status = %d, want 200 body=%s", response.Status, string(response.Body))
	}
	if got := response.Headers["X-Operation-ID"]; got != "ws-system-import-op-1" {
		t.Fatalf("system import operation header = %q, want ws-system-import-op-1", got)
	}

	scope := webservice.ResolveNodeAccessScope(webapi.PrincipalFromActor(actor))
	result := state.NodeOperations().Query(webservice.NodeOperationQueryInput{
		Scope:       scope,
		OperationID: "ws-system-import-op-1",
		Limit:       4,
	})
	if len(result.Items) != 1 {
		t.Fatalf("operations query len = %d, want 1", len(result.Items))
	}
	if result.Items[0].Kind != "config_import" {
		t.Fatalf("operation kind = %q, want config_import", result.Items[0].Kind)
	}
	if len(result.Items[0].Paths) != 1 || result.Items[0].Paths[0] != "/api/system/import" {
		t.Fatalf("operation paths = %+v, want [/api/system/import]", result.Items[0].Paths)
	}
}

func TestDispatchNodeWSBatchDecodeErrorsUseManagementErrorBody(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"run_mode=node",
		"platform_ids=master-ws-batch-errors",
		"platform_tokens=ws-batch-errors-secret",
		"platform_scopes=full",
		"platform_enabled=true",
		"platform_service_users=svc_master_ws_batch_errors",
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

	state := NewState(nil)
	defer state.Close()

	response := dispatchNodeWSRequestWithBase(state, nodeWSDispatchBase{
		Metadata: webapi.RequestMetadata{
			RequestID: "ws-batch-error-request-1",
			Source:    "node-ws",
		},
	}, webapi.AdminActorWithFallback("admin", "admin"), nodeWSFrame{
		ID:     "ws-batch-error",
		Type:   "request",
		Method: http.MethodPost,
		Path:   "/api/batch",
		Body:   json.RawMessage(`1`),
	})
	if response.Status != http.StatusBadRequest {
		t.Fatalf("dispatchNodeWSRequestWithBase(batch decode error) status = %d, want 400 body=%s", response.Status, string(response.Body))
	}
	if response.Error != "" {
		t.Fatalf("dispatchNodeWSRequestWithBase(batch decode error) error = %q, want empty formal error field", response.Error)
	}
	var payload webapi.ManagementErrorResponse
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatalf("json.Unmarshal(batch decode error body) error = %v body=%s", err, string(response.Body))
	}
	if payload.Error.Code != "invalid_json_body" {
		t.Fatalf("batch decode error code = %q, want invalid_json_body", payload.Error.Code)
	}
	if payload.Error.Message != "invalid json body" {
		t.Fatalf("batch decode error message = %q, want invalid json body", payload.Error.Message)
	}
}

type recordingImportHook struct {
	ctx   context.Context
	event webapi.Event
}

func (h *recordingImportHook) OnManagementEvent(ctx context.Context, event webapi.Event) error {
	h.ctx = ctx
	h.event = event
	return nil
}

type stubConfigImportStorage struct {
	snapshot          *file.ConfigSnapshot
	snapshotErr       error
	importedSnapshots []*file.ConfigSnapshot
	flushLocalCalls   int
	flushRuntimeCalls int
	flushLocalErr     error
	flushRuntimeErr   error
}

func (s *stubConfigImportStorage) FlushLocal() error {
	s.flushLocalCalls++
	return s.flushLocalErr
}

func (s *stubConfigImportStorage) FlushRuntime() error {
	s.flushRuntimeCalls++
	return s.flushRuntimeErr
}

func (s *stubConfigImportStorage) Snapshot() (interface{}, error) {
	return s.snapshot, s.snapshotErr
}

func (s *stubConfigImportStorage) ImportSnapshot(snapshot *file.ConfigSnapshot) error {
	s.importedSnapshots = append(s.importedSnapshots, snapshot)
	return nil
}

func (s *stubConfigImportStorage) SaveUser(*file.User) error { return errors.New("not implemented") }

func (s *stubConfigImportStorage) SaveClient(*file.Client) error {
	return errors.New("not implemented")
}

func (s *stubConfigImportStorage) ResolveUser(int) (*file.User, error) {
	return nil, errors.New("not implemented")
}

func (s *stubConfigImportStorage) ResolveClient(webservice.NodeClientTarget) (*file.Client, error) {
	return nil, errors.New("not implemented")
}

func (s *stubConfigImportStorage) ResolveTrafficClient(file.TrafficDelta) (*file.Client, error) {
	return nil, errors.New("not implemented")
}

func TestRecordImportedConfigEventUsesProvidedContext(t *testing.T) {
	hooks := &recordingImportHook{}
	state := NewStateWithApp(&webapi.App{
		NodeID: "node-a",
		Hooks:  hooks,
	})
	expectedCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	recordImportedConfigEvent(expectedCtx, state, &webapi.Actor{Username: "admin"}, webapi.RequestMetadata{
		NodeID:    "node-a",
		RequestID: "req-1",
		Source:    "node-ws",
	}, "epoch-1")

	if hooks.ctx != expectedCtx {
		t.Fatal("OnManagementEvent() should receive the caller context")
	}
	if hooks.event.Name != "node.config.imported" {
		t.Fatalf("event.Name = %q, want node.config.imported", hooks.event.Name)
	}
	if hooks.event.Metadata.RequestID != "req-1" {
		t.Fatalf("event.Metadata.RequestID = %q, want req-1", hooks.event.Metadata.RequestID)
	}
	if hooks.event.Fields["config_epoch"] != "epoch-1" {
		t.Fatalf("event config_epoch = %v, want epoch-1", hooks.event.Fields["config_epoch"])
	}
}

func TestRecordImportedConfigEventUsesStateBaseContextWhenNil(t *testing.T) {
	hooks := &recordingImportHook{}
	state := NewStateWithApp(&webapi.App{
		NodeID: "node-a",
		Hooks:  hooks,
	})

	recordImportedConfigEvent(nil, state, &webapi.Actor{Username: "admin"}, webapi.RequestMetadata{
		NodeID:    "node-a",
		RequestID: "req-2",
		Source:    "node-http",
	}, "epoch-2")

	if hooks.ctx == nil {
		t.Fatal("OnManagementEvent() should receive non-nil context")
	}
	state.Close()
	select {
	case <-hooks.ctx.Done():
	default:
		t.Fatal("OnManagementEvent() fallback context should be canceled when state closes")
	}
	if hooks.event.Name != "node.config.imported" {
		t.Fatalf("event.Name = %q, want node.config.imported", hooks.event.Name)
	}
	if hooks.event.Metadata.RequestID != "req-2" {
		t.Fatalf("event.Metadata.RequestID = %q, want req-2", hooks.event.Metadata.RequestID)
	}
	if hooks.event.Fields["config_epoch"] != "epoch-2" {
		t.Fatalf("event config_epoch = %v, want epoch-2", hooks.event.Fields["config_epoch"])
	}
}

func TestExecuteNodeConfigImportFlushesLocalAndRuntimeStorageOnSuccess(t *testing.T) {
	resetTestDB(t)

	state := NewState(nil)
	t.Cleanup(state.Close)

	storage := &stubConfigImportStorage{
		snapshot: &file.ConfigSnapshot{
			Global: &file.Glob{},
		},
	}
	state.App.Services.NodeStorage = storage

	payload, status, err := executeNodeConfigImport(
		context.Background(),
		state,
		webapi.AdminActor("admin"),
		webapi.RequestMetadata{RequestID: "req-success"},
		recoveredNodeImportSnapshot(t),
	)
	if err != nil {
		t.Fatalf("executeNodeConfigImport() error = %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("executeNodeConfigImport() status = %d, want 200", status)
	}
	if payload.Status != 1 || payload.Msg != "config import success" {
		t.Fatalf("unexpected payload = %+v", payload)
	}
	if payload.ConfigEpoch == "" {
		t.Fatal("config import should rotate config epoch")
	}
	if len(storage.importedSnapshots) != 1 {
		t.Fatalf("ImportSnapshot() call count = %d, want 1", len(storage.importedSnapshots))
	}
	if storage.flushLocalCalls != 1 {
		t.Fatalf("FlushLocal() call count = %d, want 1", storage.flushLocalCalls)
	}
	if storage.flushRuntimeCalls != 1 {
		t.Fatalf("FlushRuntime() call count = %d, want 1", storage.flushRuntimeCalls)
	}
}

func TestExecuteNodeConfigImportRollsBackAndFlushesRuntimeStorageOnFailure(t *testing.T) {
	resetTestDB(t)

	state := NewState(nil)
	t.Cleanup(state.Close)

	rollback := &file.ConfigSnapshot{
		Users: []*file.User{{
			Id:        1,
			Username:  "rollback-user",
			Password:  "secret",
			Status:    1,
			TotalFlow: &file.Flow{},
		}},
		Global: &file.Glob{},
	}
	storage := &stubConfigImportStorage{
		snapshot:        rollback,
		flushRuntimeErr: errors.New("flush runtime failed"),
	}
	state.App.Services.NodeStorage = storage

	_, status, err := executeNodeConfigImport(
		context.Background(),
		state,
		webapi.AdminActor("admin"),
		webapi.RequestMetadata{RequestID: "req-rollback"},
		recoveredNodeImportSnapshot(t),
	)
	if err == nil || err.Error() != "flush runtime failed" {
		t.Fatalf("executeNodeConfigImport() error = %v, want flush runtime failed", err)
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("executeNodeConfigImport() status = %d, want 500", status)
	}
	if len(storage.importedSnapshots) != 2 {
		t.Fatalf("ImportSnapshot() call count = %d, want 2", len(storage.importedSnapshots))
	}
	if storage.importedSnapshots[1] != rollback {
		t.Fatal("rollback import should restore the previously exported snapshot")
	}
	if storage.flushLocalCalls != 2 {
		t.Fatalf("FlushLocal() call count = %d, want 2", storage.flushLocalCalls)
	}
	if storage.flushRuntimeCalls != 2 {
		t.Fatalf("FlushRuntime() call count = %d, want 2", storage.flushRuntimeCalls)
	}
}

func TestExecuteNodeConfigImportRequiresRollbackSnapshot(t *testing.T) {
	resetTestDB(t)

	state := NewState(nil)
	t.Cleanup(state.Close)

	storage := &stubConfigImportStorage{
		snapshotErr: webservice.ErrSnapshotExportUnsupported,
	}
	state.App.Services.NodeStorage = storage

	_, status, err := executeNodeConfigImport(
		context.Background(),
		state,
		webapi.AdminActor("admin"),
		webapi.RequestMetadata{RequestID: "req-no-rollback"},
		recoveredNodeImportSnapshot(t),
	)
	if !errors.Is(err, webservice.ErrSnapshotExportUnsupported) {
		t.Fatalf("executeNodeConfigImport() error = %v, want ErrSnapshotExportUnsupported", err)
	}
	if status != http.StatusNotImplemented {
		t.Fatalf("executeNodeConfigImport() status = %d, want 501", status)
	}
	if len(storage.importedSnapshots) != 0 {
		t.Fatalf("ImportSnapshot() call count = %d, want 0", len(storage.importedSnapshots))
	}
	if storage.flushLocalCalls != 0 {
		t.Fatalf("FlushLocal() call count = %d, want 0", storage.flushLocalCalls)
	}
	if storage.flushRuntimeCalls != 0 {
		t.Fatalf("FlushRuntime() call count = %d, want 0", storage.flushRuntimeCalls)
	}
}
