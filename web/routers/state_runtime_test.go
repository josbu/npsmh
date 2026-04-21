package routers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

func TestStateConcurrentLazyRuntimeServicesReuseSingleInstances(t *testing.T) {
	state := &State{App: &webapi.App{}}
	tests := []struct {
		name   string
		getter func() interface{}
	}{
		{
			name: "management platform runtime status",
			getter: func() interface{} {
				return state.ManagementPlatformRuntimeStatus()
			},
		},
		{
			name: "runtime status",
			getter: func() interface{} {
				return state.RuntimeStatus()
			},
		},
		{
			name: "node operations",
			getter: func() interface{} {
				return state.NodeOperations()
			},
		},
		{
			name: "runtime identity",
			getter: func() interface{} {
				return state.RuntimeIdentity()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const workers = 32
			start := make(chan struct{})
			results := make(chan uintptr, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					results <- routerInterfacePointer(tt.getter())
				}()
			}
			close(start)
			wg.Wait()
			close(results)

			var first uintptr
			for ptr := range results {
				if ptr == 0 {
					t.Fatal("getter returned nil runtime service")
				}
				if first == 0 {
					first = ptr
					continue
				}
				if ptr != first {
					t.Fatalf("getter returned multiple runtime service instances: first=%#x current=%#x", first, ptr)
				}
			}
		})
	}
}

func TestWrapManagedRuntimeStopCallsEachStopOnceInOrder(t *testing.T) {
	order := make([]string, 0, 4)
	stop := wrapManagedRuntimeStop(
		func() { order = append(order, "webhooks") },
		func() { order = append(order, "callbacks") },
		func() { order = append(order, "reverse") },
		func() { order = append(order, "base") },
	)

	stop()
	stop()

	want := []string{"webhooks", "callbacks", "reverse", "base"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("stop order = %v, want %v", order, want)
	}
}

func routerInterfacePointer(value interface{}) uintptr {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return 0
	}
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return rv.Pointer()
	default:
		return 0
	}
}

type scopedClientPermissionResolver struct{}

type platformScopePermissionResolver struct{}

func (scopedClientPermissionResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	normalized := webservice.DefaultPermissionResolver().NormalizePrincipal(principal)
	if principal.Username == "scoped-user" {
		normalized.Authenticated = true
		normalized.Kind = "user"
		normalized.ClientIDs = []int{11}
	}
	return normalized
}

func (scopedClientPermissionResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	return webservice.DefaultPermissionResolver().NormalizeIdentity(identity)
}

func (scopedClientPermissionResolver) KnownRoles() []string {
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (scopedClientPermissionResolver) KnownPermissions() []string {
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (scopedClientPermissionResolver) PermissionCatalog() map[string][]string {
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

func (platformScopePermissionResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	if principal.Username == "mapped-platform" {
		principal.Authenticated = true
		principal.Kind = "platform_admin"
		principal.Attributes = map[string]string{
			"platform_id":              "platform-a",
			"platform_service_user_id": "9001",
		}
	}
	return webservice.DefaultPermissionResolver().NormalizePrincipal(principal)
}

func (platformScopePermissionResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	return webservice.DefaultPermissionResolver().NormalizeIdentity(identity)
}

func (platformScopePermissionResolver) KnownRoles() []string {
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (platformScopePermissionResolver) KnownPermissions() []string {
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (platformScopePermissionResolver) PermissionCatalog() map[string][]string {
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

func TestStateGettersIgnoreTypedNilServices(t *testing.T) {
	var (
		permissions    *webservice.StaticPermissionResolver
		authz          *webservice.DefaultAuthorizationService
		loginPolicy    *webservice.DefaultLoginPolicy
		nodeControl    *webservice.DefaultNodeControlService
		nodeStorage    *webservice.DefaultNodeStorage
		platforms      *webservice.DefaultManagementPlatformStore
		platformStatus *webservice.InMemoryManagementPlatformRuntimeStatusStore
		runtimeStatus  *webservice.InMemoryNodeRuntimeStatusStore
		nodeOperations *nodeOperationRuntimeStore
		runtimeID      *webservice.StaticNodeRuntimeIdentity
		system         *webservice.DefaultSystemService
	)

	state := &State{
		App: &webapi.App{
			Services: webservice.Services{
				Permissions:                     permissions,
				Authz:                           authz,
				LoginPolicy:                     loginPolicy,
				NodeControl:                     nodeControl,
				NodeStorage:                     nodeStorage,
				ManagementPlatforms:             platforms,
				ManagementPlatformRuntimeStatus: platformStatus,
				NodeRuntimeStatus:               runtimeStatus,
				NodeOperations:                  nodeOperations,
				NodeRuntimeIdentity:             runtimeID,
				System:                          system,
			},
		},
	}

	if isNilRouterServiceValue(state.PermissionResolver()) {
		t.Fatal("PermissionResolver() kept typed nil service")
	}
	if isNilRouterServiceValue(state.Authorization()) {
		t.Fatal("Authorization() kept typed nil service")
	}
	if isNilRouterServiceValue(state.LoginPolicy()) {
		t.Fatal("LoginPolicy() kept typed nil service")
	}
	if isNilRouterServiceValue(state.NodeControl()) {
		t.Fatal("NodeControl() kept typed nil service")
	}
	if isNilRouterServiceValue(state.NodeStorage()) {
		t.Fatal("NodeStorage() kept typed nil service")
	}
	if isNilRouterServiceValue(state.ManagementPlatforms()) {
		t.Fatal("ManagementPlatforms() kept typed nil service")
	}
	if isNilRouterServiceValue(state.ManagementPlatformRuntimeStatus()) {
		t.Fatal("ManagementPlatformRuntimeStatus() kept typed nil service")
	}
	if isNilRouterServiceValue(state.RuntimeStatus()) {
		t.Fatal("RuntimeStatus() kept typed nil service")
	}
	if isNilRouterServiceValue(state.NodeOperations()) {
		t.Fatal("NodeOperations() kept typed nil service")
	}
	if isNilRouterServiceValue(state.RuntimeIdentity()) {
		t.Fatal("RuntimeIdentity() kept typed nil service")
	}
	if isNilRouterServiceValue(state.System()) {
		t.Fatal("System() kept typed nil service")
	}
	backend := state.backend()
	if isNilRouterServiceValue(backend.Repository) || isNilRouterServiceValue(backend.Runtime) {
		t.Fatalf("backend() = %+v, want initialized default backend", backend)
	}
}

func TestNewStateWithAppIgnoresTypedNilRuntimeServices(t *testing.T) {
	var (
		authz          *webservice.DefaultAuthorizationService
		platformStatus *webservice.InMemoryManagementPlatformRuntimeStatusStore
		runtimeStatus  *webservice.InMemoryNodeRuntimeStatusStore
		nodeOperations *nodeOperationRuntimeStore
		runtimeID      *webservice.StaticNodeRuntimeIdentity
	)

	state := NewStateWithApp(webapi.NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "typed-nil-router"},
	}, webapi.Options{
		Services: &webservice.Services{
			Authz:                           authz,
			ManagementPlatformRuntimeStatus: platformStatus,
			NodeRuntimeStatus:               runtimeStatus,
			NodeOperations:                  nodeOperations,
			NodeRuntimeIdentity:             runtimeID,
		},
	}))
	defer state.Close()

	if isNilRouterServiceValue(state.NodeEvents.authz) {
		t.Fatal("NewStateWithApp() kept typed nil authz in node event hub")
	}
	if isNilRouterServiceValue(state.ManagementPlatformRuntimeStatus()) {
		t.Fatal("NewStateWithApp() kept typed nil management platform runtime status")
	}
	if isNilRouterServiceValue(state.RuntimeStatus()) {
		t.Fatal("NewStateWithApp() kept typed nil runtime status")
	}
	if isNilRouterServiceValue(state.NodeOperations()) {
		t.Fatal("NewStateWithApp() kept typed nil node operations")
	}
	if isNilRouterServiceValue(state.RuntimeIdentity()) {
		t.Fatal("NewStateWithApp() kept typed nil runtime identity")
	}
}

func TestNewStateWithAppUsesPermissionResolverForEventHubFallback(t *testing.T) {
	state := NewStateWithApp(webapi.NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "resolver-node"},
	}, webapi.Options{
		Services: &webservice.Services{
			Permissions: scopedClientPermissionResolver{},
		},
	}))
	defer state.Close()

	actor := webapi.UserActor("scoped-user", nil)
	event := webapi.Event{
		Resource: "client",
		Fields: map[string]interface{}{
			"id": 11,
		},
	}
	if !state.NodeEvents.allowsEvent(actor, event) {
		t.Fatal("node event hub should honor router permission resolver fallback when authz is not injected")
	}
}

func TestActorNodeScopeWithAuthzUsesResolver(t *testing.T) {
	authz := webservice.DefaultAuthorizationService{Resolver: platformScopePermissionResolver{}}
	scope := actorNodeScopeWithAuthz(authz, &webapi.Actor{Username: "mapped-platform"})
	if !scope.CanViewStatus() || !scope.CanViewCallbackQueue() {
		t.Fatalf("actorNodeScopeWithAuthz() = %+v, want platform-admin scope from resolver", scope)
	}
}

func TestNodeRuntimeStateWriterCloseFlushesWithoutWaitingForDelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writer := &nodeRuntimeStateWriter{
		path:   path,
		delay:  2 * time.Second,
		stopCh: make(chan struct{}),
	}

	writer.Store(map[string]string{"status": "ok"})

	start := time.Now()
	writer.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close() took %v, want under %v", elapsed, 500*time.Millisecond)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", string(data), err)
	}
	if decoded["status"] != "ok" {
		t.Fatalf("decoded status = %q, want ok", decoded["status"])
	}
}

func TestReadNodeRuntimeStateFlushesPendingWriterBeforeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	writer := &nodeRuntimeStateWriter{
		path:   path,
		delay:  2 * time.Second,
		stopCh: make(chan struct{}),
	}
	registerNodeRuntimeStateWriter(writer)
	t.Cleanup(writer.Close)

	writer.Store(map[string]string{"status": "ok"})

	var decoded map[string]string
	if err := readNodeRuntimeState(path, &decoded); err != nil {
		t.Fatalf("readNodeRuntimeState(%q) error = %v", path, err)
	}
	if decoded["status"] != "ok" {
		t.Fatalf("decoded status = %q, want ok", decoded["status"])
	}
}

func TestNodeRuntimeAppendWriterCloseFlushesWithoutWaitingForDelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	writer := &nodeRuntimeAppendWriter{
		path:   path,
		delay:  2 * time.Second,
		stopCh: make(chan struct{}),
	}

	writer.Store([]byte("first\n"))
	writer.Store([]byte("second\n"))

	start := time.Now()
	writer.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close() took %v, want under %v", elapsed, 500*time.Millisecond)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("append data = %q, want %q", string(data), "first\nsecond\n")
	}
}

func TestNodeRuntimeWritersCloseHandleNilStopChannel(t *testing.T) {
	stateWriter := &nodeRuntimeStateWriter{}
	appendWriter := &nodeRuntimeAppendWriter{}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Close() should not panic on nil stopCh, got %v", r)
		}
	}()

	stateWriter.Close()
	appendWriter.Close()
}

func TestNodeEventHubAllowsClientEventFromOwnershipMetadata(t *testing.T) {
	hub := newNodeEventHub(webservice.DefaultAuthorizationService{})
	actor := webapi.UserActor("owner", nil)
	actor.Attributes = map[string]string{
		"user_id": "7",
	}
	event := webapi.Event{
		Resource: "client",
		Fields: map[string]interface{}{
			"id":            11,
			"owner_user_id": 7,
		},
	}
	if !hub.allowsEvent(actor, event) {
		t.Fatal("allowsEvent(client ownership metadata) = false, want true")
	}
}

func TestNodeEventHubAllowsClientEventFromManagerMetadata(t *testing.T) {
	hub := newNodeEventHub(webservice.DefaultAuthorizationService{})
	actor := webapi.UserActor("manager", nil)
	actor.Attributes = map[string]string{
		"user_id": "9",
	}
	event := webapi.Event{
		Resource: "client",
		Fields: map[string]interface{}{
			"id":               12,
			"owner_user_id":    7,
			"manager_user_ids": []int{9},
		},
	}
	if !hub.allowsEvent(actor, event) {
		t.Fatal("allowsEvent(client manager metadata) = false, want true")
	}
}

func TestNodeEventHubAllowsTunnelAndHostEventsFromClientMetadata(t *testing.T) {
	resetTestDB(t)
	hub := newNodeEventHub(webservice.DefaultAuthorizationService{})
	actor := webapi.UserActor("scoped-user", []int{11})
	cases := []webapi.Event{
		{
			Resource: "tunnel",
			Fields: map[string]interface{}{
				"id":         21,
				"client_id":  11,
				"updated_at": 1,
			},
		},
		{
			Resource: "host",
			Fields: map[string]interface{}{
				"id":         31,
				"client_id":  11,
				"updated_at": 1,
			},
		},
	}
	for _, event := range cases {
		if !hub.allowsEvent(actor, event) {
			t.Fatalf("allowsEvent(%s client metadata) = false, want true", event.Resource)
		}
	}
}

func TestNodeEventSubscriptionOverflowSignalsSequenceAndStopsDelivery(t *testing.T) {
	hub := newNodeEventHub(webservice.DefaultAuthorizationService{})
	sub := hub.Subscribe(webapi.AdminActor("admin"))
	if sub == nil {
		t.Fatal("Subscribe() = nil")
	}
	defer sub.Close()

	for i := 1; i <= 33; i++ {
		hub.Publish(webapi.Event{
			Sequence: int64(i),
			Resource: "node",
		})
	}

	select {
	case <-sub.Done():
	default:
		t.Fatal("subscription did not signal overflow closure")
	}
	overflowSequence, overflowed := sub.OverflowSequence()
	if !overflowed || overflowSequence != 33 {
		t.Fatalf("OverflowSequence() = (%d, %v), want (33, true)", overflowSequence, overflowed)
	}

	count := 0
	drainNodeSubscriptionEvents(sub, func(event webapi.Event) {
		if event.Sequence <= 0 {
			t.Fatalf("drained event sequence = %d, want > 0", event.Sequence)
		}
		count++
	})
	if count != 32 {
		t.Fatalf("drained buffered event count = %d, want 32", count)
	}
}

func TestNodeEventHubAllowsOperationsEventsForStatusReaders(t *testing.T) {
	hub := newNodeEventHub(webservice.DefaultAuthorizationService{})
	event := webapi.Event{
		Resource: "operations",
		Fields: map[string]interface{}{
			"operation_id": "op-1",
			"platform_id":  "master-a",
		},
	}
	adminActor := webapi.PlatformActor("master-a", "admin", "platform-admin", "platform-admin-1", false, nil, 1)
	if !hub.allowsEvent(adminActor, event) {
		t.Fatal("allowsEvent(platform_admin operations) = false, want true")
	}
	userActor := webapi.PlatformActor("master-a", "user", "platform-user", "platform-user-1", false, []int{1}, 1)
	if !hub.allowsEvent(userActor, event) {
		t.Fatal("allowsEvent(platform_user operations) = false, want true")
	}
	localUser := webapi.UserActor("node-user", nil)
	if hub.allowsEvent(localUser, event) {
		t.Fatal("allowsEvent(local user operations) = true, want false")
	}
}

func TestNodeEventHubAllowsManagementPlatformEventsForStatusReaders(t *testing.T) {
	hub := newNodeEventHub(webservice.DefaultAuthorizationService{})
	event := webapi.Event{
		Resource: "management_platforms",
		Fields: map[string]interface{}{
			"platform_id": "master-a",
			"cause":       "callback_delivered",
		},
	}
	adminActor := webapi.PlatformActor("master-a", "admin", "platform-admin", "platform-admin-1", false, nil, 1)
	if !hub.allowsEvent(adminActor, event) {
		t.Fatal("allowsEvent(platform_admin management_platforms) = false, want true")
	}
	userActor := webapi.PlatformActor("master-a", "user", "platform-user", "platform-user-1", false, []int{1}, 1)
	if !hub.allowsEvent(userActor, event) {
		t.Fatal("allowsEvent(platform_user management_platforms) = false, want true")
	}
	localUser := webapi.UserActor("node-user", nil)
	if hub.allowsEvent(localUser, event) {
		t.Fatal("allowsEvent(local user management_platforms) = true, want false")
	}
}
