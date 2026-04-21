package api

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type stubAppContext struct {
	actor *Actor
}

func (c stubAppContext) BaseContext() context.Context        { return context.Background() }
func (c stubAppContext) String(string) string                { return "" }
func (c stubAppContext) LookupString(string) (string, bool)  { return "", false }
func (c stubAppContext) Int(string, ...int) int              { return 0 }
func (c stubAppContext) Bool(string, ...bool) bool           { return false }
func (c stubAppContext) Method() string                      { return "" }
func (c stubAppContext) Host() string                        { return "" }
func (c stubAppContext) RemoteAddr() string                  { return "" }
func (c stubAppContext) ClientIP() string                    { return "" }
func (c stubAppContext) RequestHeader(string) string         { return "" }
func (c stubAppContext) RawBody() []byte                     { return nil }
func (c stubAppContext) SessionValue(string) interface{}     { return nil }
func (c stubAppContext) SetSessionValue(string, interface{}) {}
func (c stubAppContext) DeleteSessionValue(string)           {}
func (c stubAppContext) SetParam(string, string)             {}
func (c stubAppContext) RespondJSON(int, interface{})        {}
func (c stubAppContext) RespondString(int, string)           {}
func (c stubAppContext) RespondData(int, string, []byte)     {}
func (c stubAppContext) Redirect(int, string)                {}
func (c stubAppContext) SetResponseHeader(string, string)    {}
func (c stubAppContext) IsWritten() bool                     { return false }
func (c stubAppContext) Actor() *Actor                       { return c.actor }
func (c stubAppContext) SetActor(*Actor)                     {}
func (c stubAppContext) Metadata() RequestMetadata           { return RequestMetadata{} }

type nilBaseContext struct {
	stubAppContext
	metadata RequestMetadata
}

func (c nilBaseContext) BaseContext() context.Context { return nil }
func (c nilBaseContext) Metadata() RequestMetadata    { return c.metadata }

type stubAppManagementPlatformStore struct{}

type nilSensitivePermissionResolver struct{}

type mappedPermissionResolver struct {
	normalizePrincipal func(webservice.Principal) webservice.Principal
}

func (s *nilSensitivePermissionResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	if s == nil {
		panic("typed nil permission resolver should not be used")
	}
	return webservice.DefaultPermissionResolver().NormalizePrincipal(principal)
}

func (r mappedPermissionResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	if r.normalizePrincipal != nil {
		principal = r.normalizePrincipal(principal)
	}
	return webservice.DefaultPermissionResolver().NormalizePrincipal(principal)
}

func (s *nilSensitivePermissionResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	if s == nil {
		panic("typed nil permission resolver should not be used")
	}
	return webservice.DefaultPermissionResolver().NormalizeIdentity(identity)
}

func (r mappedPermissionResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	return webservice.DefaultPermissionResolver().NormalizeIdentity(identity)
}

func (s *nilSensitivePermissionResolver) KnownRoles() []string {
	if s == nil {
		panic("typed nil permission resolver should not be used")
	}
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (r mappedPermissionResolver) KnownRoles() []string {
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (s *nilSensitivePermissionResolver) KnownPermissions() []string {
	if s == nil {
		panic("typed nil permission resolver should not be used")
	}
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (r mappedPermissionResolver) KnownPermissions() []string {
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (s *nilSensitivePermissionResolver) PermissionCatalog() map[string][]string {
	if s == nil {
		panic("typed nil permission resolver should not be used")
	}
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

func (r mappedPermissionResolver) PermissionCatalog() map[string][]string {
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

type nilSensitiveAuthorizationService struct{}

func (s *nilSensitiveAuthorizationService) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.NormalizePrincipal(principal)
}

func (s *nilSensitiveAuthorizationService) ClientScope(principal webservice.Principal) webservice.ClientScope {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.ClientScope(principal)
}

func (s *nilSensitiveAuthorizationService) Permissions(principal webservice.Principal) []string {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.Permissions(principal)
}

func (s *nilSensitiveAuthorizationService) Allows(principal webservice.Principal, permission string) bool {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.Allows(principal, permission)
}

func (s *nilSensitiveAuthorizationService) ResolveClient(principal webservice.Principal, requestedClientID int) (int, error) {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.ResolveClient(principal, requestedClientID)
}

func (s *nilSensitiveAuthorizationService) RequirePermission(principal webservice.Principal, permission string) error {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.RequirePermission(principal, permission)
}

func (s *nilSensitiveAuthorizationService) RequireAdmin(principal webservice.Principal) error {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.RequireAdmin(principal)
}

func (s *nilSensitiveAuthorizationService) RequireClient(principal webservice.Principal, clientID int) error {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.RequireClient(principal, clientID)
}

func (s *nilSensitiveAuthorizationService) RequireTunnel(principal webservice.Principal, tunnelID int) error {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.RequireTunnel(principal, tunnelID)
}

func (s *nilSensitiveAuthorizationService) RequireHost(principal webservice.Principal, hostID int) error {
	if s == nil {
		panic("typed nil authorization service should not be used")
	}
	return webservice.DefaultAuthorizationService{}.RequireHost(principal, hostID)
}

type recordingHooks struct {
	events []Event
	ctxs   []context.Context
}

func (h *recordingHooks) OnManagementEvent(ctx context.Context, event Event) error {
	h.ctxs = append(h.ctxs, ctx)
	h.events = append(h.events, event)
	return nil
}

func TestAppEmitHandlesNilContextAndNilBaseContext(t *testing.T) {
	hooks := &recordingHooks{}
	app := &App{Hooks: hooks}

	app.Emit(nil, Event{Name: "manual.event"})

	if len(hooks.events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(hooks.events))
	}
	if hooks.ctxs[0] == nil {
		t.Fatal("OnManagementEvent() received nil ctx for nil Context input")
	}

	app.Emit(nilBaseContext{
		stubAppContext: stubAppContext{actor: &Actor{Username: "admin"}},
		metadata:       RequestMetadata{RequestID: "req-emit"},
	}, Event{Name: "manual.nilbase"})

	if len(hooks.events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(hooks.events))
	}
	if hooks.ctxs[1] == nil {
		t.Fatal("OnManagementEvent() received nil ctx for nil BaseContext input")
	}
	if hooks.events[1].Actor == nil || hooks.events[1].Actor.Username != "admin" {
		t.Fatalf("event.Actor = %#v, want copied actor", hooks.events[1].Actor)
	}
	if hooks.events[1].Metadata.RequestID != "req-emit" {
		t.Fatalf("event.Metadata.RequestID = %q, want req-emit", hooks.events[1].Metadata.RequestID)
	}
}

func TestAppNodeControlHandlesNilReceiver(t *testing.T) {
	var nilApp *App

	service := nilApp.nodeControl()
	if service == nil {
		t.Fatal("nodeControl() = nil, want default service")
	}
	control, ok := service.(webservice.DefaultNodeControlService)
	if !ok {
		t.Fatalf("nodeControl() type = %T, want DefaultNodeControlService", service)
	}
	if control.System == nil {
		t.Fatal("default node control system = nil, want default system service")
	}
	if control.Backend.Repository == nil || control.Backend.Runtime == nil {
		t.Fatalf("default node control backend = %+v, want initialized default backend", control.Backend)
	}
}

func TestAppGettersIgnoreTypedNilServices(t *testing.T) {
	var (
		loginPolicy     *webservice.DefaultLoginPolicy
		nodeControl     *webservice.DefaultNodeControlService
		nodeStorage     *stubTrafficNodeStorage
		platforms       *stubAppManagementPlatformStore
		platformStatus  *webservice.InMemoryManagementPlatformRuntimeStatusStore
		runtimeStatus   *webservice.InMemoryNodeRuntimeStatusStore
		nodeOperations  *webservice.InMemoryNodeOperationStore
		trafficReporter *webservice.DefaultNodeTrafficReporter
		runtimeIdentity *webservice.StaticNodeRuntimeIdentity
		system          *webservice.DefaultSystemService
	)

	app := &App{
		Services: webservice.Services{
			LoginPolicy:                     loginPolicy,
			NodeControl:                     nodeControl,
			NodeStorage:                     nodeStorage,
			ManagementPlatforms:             platforms,
			ManagementPlatformRuntimeStatus: platformStatus,
			NodeRuntimeStatus:               runtimeStatus,
			NodeOperations:                  nodeOperations,
			NodeTrafficReporter:             trafficReporter,
			NodeRuntimeIdentity:             runtimeIdentity,
			System:                          system,
		},
	}

	if isNilAppServiceValue(app.loginPolicy()) {
		t.Fatal("loginPolicy() kept typed nil service")
	}
	if isNilAppServiceValue(app.nodeControl()) {
		t.Fatal("nodeControl() kept typed nil service")
	}
	if isNilAppServiceValue(app.nodeStorage()) {
		t.Fatal("nodeStorage() kept typed nil service")
	}
	if isNilAppServiceValue(app.managementPlatforms()) {
		t.Fatal("managementPlatforms() kept typed nil service")
	}
	if isNilAppServiceValue(app.managementPlatformRuntimeStatus()) {
		t.Fatal("managementPlatformRuntimeStatus() kept typed nil service")
	}
	if isNilAppServiceValue(app.runtimeStatus()) {
		t.Fatal("runtimeStatus() kept typed nil service")
	}
	if isNilAppServiceValue(app.nodeOperations()) {
		t.Fatal("nodeOperations() kept typed nil service")
	}
	if isNilAppServiceValue(app.nodeTrafficReporter()) {
		t.Fatal("nodeTrafficReporter() kept typed nil service")
	}
	if isNilAppServiceValue(app.runtimeIdentity()) {
		t.Fatal("runtimeIdentity() kept typed nil service")
	}
	if isNilAppServiceValue(app.system()) {
		t.Fatal("system() kept typed nil service")
	}
}

func TestManagementContractStateIgnoresTypedNilAuthServices(t *testing.T) {
	var permissions *nilSensitivePermissionResolver
	var authz *nilSensitiveAuthorizationService

	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "typed-nil-auth"},
		Web: servercfg.WebConfig{BaseURL: "https://node.example.com"},
	}, Options{
		Services: &webservice.Services{
			Permissions: permissions,
			Authz:       authz,
		},
	})

	state := app.managementContractState(stubAppContext{actor: UserActor("demo", []int{1})})
	if state.app.Name != "typed-nil-auth" {
		t.Fatalf("managementContractState() app.name = %q, want typed-nil-auth", state.app.Name)
	}
	if !state.session.Authenticated {
		t.Fatal("managementContractState() should still authenticate using default auth services")
	}
}

func TestManagementContractStatePrefersSessionIdentityOverStaleContextActor(t *testing.T) {
	session := map[string]interface{}{}
	ApplyActorSessionMap(session, UserActor("alice", []int{12}), "session")
	ctx := &sessionIdentityTestContext{
		session: session,
		actor:   AnonymousActor(),
	}

	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "session-backed"},
		Web: servercfg.WebConfig{BaseURL: "https://node.example.com"},
	}, Options{})

	state := app.managementContractState(ctx)
	if !state.session.Authenticated {
		t.Fatal("managementContractState() should authenticate from session identity when context actor is stale")
	}
	if state.actor == nil || state.actor.Username != "alice" {
		t.Fatalf("managementContractState() actor = %+v, want alice from session identity", state.actor)
	}
	if len(state.session.ClientIDs) != 1 || state.session.ClientIDs[0] != 12 {
		t.Fatalf("managementContractState() client_ids = %v, want [12] from session identity", state.session.ClientIDs)
	}
	if ctx.actor == nil || ctx.actor.Username != "alice" {
		t.Fatalf("managementContractState() should refresh context actor from session identity, got %+v", ctx.actor)
	}
}

func TestAppAuthorizationUsesPermissionResolverFallback(t *testing.T) {
	app := &App{
		Services: webservice.Services{
			Permissions: mappedPermissionResolver{
				normalizePrincipal: func(principal webservice.Principal) webservice.Principal {
					if principal.Username == "resolver-user" {
						principal.Authenticated = true
						principal.Kind = "user"
						principal.ClientIDs = []int{77}
						principal.Permissions = append(principal.Permissions, webservice.PermissionHostsUpdate)
					}
					return principal
				},
			},
		},
	}

	if !app.nodeActorAllows(&Actor{Username: "resolver-user"}, webservice.PermissionHostsUpdate) {
		t.Fatal("nodeActorAllows() should honor permission resolver fallback when authz is not injected")
	}

	state := app.managementContractState(stubAppContext{actor: &Actor{Username: "resolver-user"}})
	if !state.session.Authenticated {
		t.Fatal("managementContractState() should authenticate through permission resolver fallback")
	}
	if state.session.ClientID == nil || *state.session.ClientID != 77 {
		t.Fatalf("managementContractState() client_id = %#v, want pointer to 77 from normalized principal", state.session.ClientID)
	}
	if len(state.session.ClientIDs) != 1 || state.session.ClientIDs[0] != 77 {
		t.Fatalf("managementContractState() client_ids = %v, want [77]", state.session.ClientIDs)
	}
}

func TestAppResolveNodeAccessScopeUsesAuthorizationFallback(t *testing.T) {
	app := &App{
		Services: webservice.Services{
			Permissions: mappedPermissionResolver{
				normalizePrincipal: func(principal webservice.Principal) webservice.Principal {
					if principal.Username == "platform-scope" {
						principal.Authenticated = true
						principal.Kind = "platform_admin"
						principal.Attributes = map[string]string{
							"platform_id":              "platform-a",
							"platform_service_user_id": "9001",
						}
					}
					return principal
				},
			},
		},
	}

	scope := app.resolveNodeAccessScope(&Actor{Username: "platform-scope"})
	if !scope.CanViewStatus() || !scope.CanViewCallbackQueue() {
		t.Fatalf("resolveNodeAccessScope() = %+v, want platform-admin scope from authorization fallback", scope)
	}
}

func TestManagementErrorCodeMapsInvalidCallbackQueueAction(t *testing.T) {
	response := ManagementErrorResponseForStatus(400, webservice.ErrInvalidCallbackQueueAction)
	if response.Error.Code != "invalid_callback_queue_action" {
		t.Fatalf("ManagementErrorResponseForStatus() code = %q, want invalid_callback_queue_action", response.Error.Code)
	}
	if response.Error.Message != webservice.ErrInvalidCallbackQueueAction.Error() {
		t.Fatalf("ManagementErrorResponseForStatus() message = %q, want %q", response.Error.Message, webservice.ErrInvalidCallbackQueueAction.Error())
	}
}

type stubTrafficNodeStorage struct {
	client            *file.Client
	resolveTrafficErr error
}

func (s *stubTrafficNodeStorage) FlushLocal() error   { return nil }
func (s *stubTrafficNodeStorage) FlushRuntime() error { return nil }
func (s *stubTrafficNodeStorage) Snapshot() (interface{}, error) {
	return map[string]interface{}{}, nil
}
func (s *stubTrafficNodeStorage) ImportSnapshot(*file.ConfigSnapshot) error { return nil }
func (s *stubTrafficNodeStorage) SaveUser(*file.User) error                 { return nil }
func (s *stubTrafficNodeStorage) SaveClient(client *file.Client) error {
	s.client = client
	return nil
}
func (s *stubTrafficNodeStorage) ResolveUser(int) (*file.User, error) {
	return nil, webservice.ErrUserNotFound
}
func (s *stubTrafficNodeStorage) ResolveClient(webservice.NodeClientTarget) (*file.Client, error) {
	if s.client == nil {
		return nil, webservice.ErrClientNotFound
	}
	return s.client, nil
}
func (s *stubTrafficNodeStorage) ResolveTrafficClient(item file.TrafficDelta) (*file.Client, error) {
	if s.resolveTrafficErr != nil {
		return nil, s.resolveTrafficErr
	}
	if s.client == nil {
		return nil, webservice.ErrClientNotFound
	}
	if item.ClientID > 0 && item.ClientID != s.client.Id {
		return nil, webservice.ErrClientNotFound
	}
	if item.ClientID > 0 && item.VerifyKey != "" && item.VerifyKey != s.client.VerifyKey {
		return nil, webservice.ErrClientIdentifierConflict
	}
	if item.VerifyKey != "" && item.VerifyKey != s.client.VerifyKey {
		return nil, webservice.ErrClientNotFound
	}
	return s.client, nil
}

type stubNodeTrafficContext struct {
	stubAppContext
	params      map[string]string
	rawBody     []byte
	status      int
	jsonPayload interface{}
	headers     map[string]string
	written     bool
}

func (c *stubNodeTrafficContext) String(key string) string {
	if c == nil || c.params == nil {
		return ""
	}
	return c.params[key]
}

func (c *stubNodeTrafficContext) LookupString(key string) (string, bool) {
	if c == nil || c.params == nil {
		return "", false
	}
	value, ok := c.params[key]
	return value, ok
}

func (c *stubNodeTrafficContext) RawBody() []byte {
	if c == nil {
		return nil
	}
	return append([]byte(nil), c.rawBody...)
}

func (c *stubNodeTrafficContext) Int(key string, def ...int) int {
	if value, ok := c.LookupString(key); ok {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	if len(def) > 0 {
		return def[0]
	}
	return 0
}

func (c *stubNodeTrafficContext) RespondJSON(status int, payload interface{}) {
	c.status = status
	c.jsonPayload = payload
	c.written = true
}

func (c *stubNodeTrafficContext) SetResponseHeader(key, value string) {
	if c.headers == nil {
		c.headers = make(map[string]string)
	}
	c.headers[key] = value
}

func (c *stubNodeTrafficContext) IsWritten() bool {
	return c.written
}

func (stubAppManagementPlatformStore) EnsureServiceUser(servercfg.ManagementPlatformConfig) (*file.User, error) {
	return nil, nil
}

func (stubAppManagementPlatformStore) ServiceUsername(platformID, configured string) string {
	return "resolved-" + platformID
}

func (stubAppManagementPlatformStore) OwnedClientIDs(int) ([]int, error) {
	return nil, nil
}

func TestNewWithOptionsUsesOverrides(t *testing.T) {
	providedCfg := &servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "cfg-node"},
	}
	customServices := webservice.New()
	customHooks := NoopHooks{}
	configured := false

	app := NewWithOptions(nil, Options{
		NodeID:   "custom-node",
		Hooks:    customHooks,
		Services: &customServices,
		ConfigureServices: func(services *webservice.Services) {
			configured = true
		},
		ConfigProvider: func() *servercfg.Snapshot { return providedCfg },
	})

	if app.NodeID != "custom-node" {
		t.Fatalf("NodeID = %q, want custom-node", app.NodeID)
	}
	if app.Hooks != customHooks {
		t.Fatal("Hooks override was not applied")
	}
	if app.CurrentConfig() != providedCfg {
		t.Fatal("CurrentConfig() did not use custom config provider")
	}
	if !configured {
		t.Fatal("ConfigureServices was not called")
	}
	if app.Services.LoginPolicy != customServices.LoginPolicy {
		t.Fatal("Services override was not applied")
	}
}

func TestDiscoveryPayloadUsesInjectedManagementPlatformStore(t *testing.T) {
	customServices := webservice.New()
	customServices.ManagementPlatforms = stubAppManagementPlatformStore{}
	customServices.ManagementPlatformRuntimeStatus.NoteConfigured("master-a", "dual", "wss://master-a.example/node/ws", true)
	customServices.ManagementPlatformRuntimeStatus.NoteReverseConnected("master-a")
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{PlatformID: "master-a", Token: "secret-a", ControlScope: "full", ServiceUsername: "__platform_master-a", Enabled: true},
			},
		},
	}, Options{
		Services: &customServices,
	})

	payload := app.managementDiscoveryPayload(stubAppContext{actor: testAdminActor()})
	if len(payload.Extensions.Cluster.ManagementPlatforms) != 1 {
		t.Fatalf("discovery management platforms = %#v, want one item", payload.Extensions.Cluster.ManagementPlatforms)
	}
	if payload.Extensions.Cluster.ManagementPlatforms[0].ServiceUsername != "resolved-master-a" {
		t.Fatalf("discovery service username = %q, want resolved-master-a", payload.Extensions.Cluster.ManagementPlatforms[0].ServiceUsername)
	}
	if !payload.Extensions.Cluster.ManagementPlatforms[0].ReverseConnected {
		t.Fatalf("discovery reverse status = %#v, want connected runtime state", payload.Extensions.Cluster.ManagementPlatforms[0])
	}
}

func TestNewWithOptionsMergesPartialServiceOverrides(t *testing.T) {
	partial := webservice.Services{
		ManagementPlatforms: stubAppManagementPlatformStore{},
	}
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "merge-node"},
	}, Options{
		Services: &partial,
	})

	if app.Services.ManagementPlatforms == nil {
		t.Fatal("ManagementPlatforms override should be preserved")
	}
	if app.Services.Clients == nil || app.Services.Auth == nil || app.Services.NodeControl == nil {
		t.Fatalf("partial service overrides should retain default service graph, got %+v", app.Services)
	}
	if app.Services.Backend.Repository == nil || app.Services.Backend.Runtime == nil {
		t.Fatalf("partial service overrides should retain default backend, got %+v", app.Services.Backend)
	}
}

func TestNewWithOptionsPropagatesConfigProviderToDefaultServices(t *testing.T) {
	cfg := &servercfg.Snapshot{
		App:      servercfg.AppConfig{Name: "cfg-propagation-node"},
		Web:      servercfg.WebConfig{Username: "cfg-admin"},
		Security: servercfg.SecurityConfig{LoginMaxSkew: 4321},
	}
	app := NewWithOptions(cfg, Options{})

	auth, ok := app.Services.Auth.(webservice.DefaultAuthService)
	if !ok {
		t.Fatalf("Auth type = %T, want DefaultAuthService", app.Services.Auth)
	}
	if got := auth.ConfigProvider(); got != cfg {
		t.Fatalf("Auth ConfigProvider() = %p, want %p", got, cfg)
	}

	clients, ok := app.Services.Clients.(webservice.DefaultClientService)
	if !ok {
		t.Fatalf("Clients type = %T, want DefaultClientService", app.Services.Clients)
	}
	if got := clients.ConfigProvider(); got != cfg {
		t.Fatalf("Clients ConfigProvider() = %p, want %p", got, cfg)
	}

	users, ok := app.Services.Users.(webservice.DefaultUserService)
	if !ok {
		t.Fatalf("Users type = %T, want DefaultUserService", app.Services.Users)
	}
	if got := users.ConfigProvider(); got != cfg {
		t.Fatalf("Users ConfigProvider() = %p, want %p", got, cfg)
	}

	loginPolicy, ok := app.Services.LoginPolicy.(*webservice.DefaultLoginPolicy)
	if !ok {
		t.Fatalf("LoginPolicy type = %T, want *DefaultLoginPolicy", app.Services.LoginPolicy)
	}
	if got := loginPolicy.Settings().MaxSkew; got != cfg.Security.LoginMaxSkew {
		t.Fatalf("LoginPolicy Settings().MaxSkew = %d, want %d", got, cfg.Security.LoginMaxSkew)
	}
}

func TestAppsUseIndependentRuntimeStatusStores(t *testing.T) {
	appA := New(nil)
	appB := New(nil)

	appA.runtimeStatus().NoteOperation("kick", nil)
	appA.runtimeStatus().ResetIdempotency(time.Minute)
	appA.runtimeStatus().NoteIdempotencyReplay()

	if appA.runtimeStatus().Operations().LastKickAt == 0 {
		t.Fatal("appA LastKickAt = 0, want recorded operation")
	}
	if appB.runtimeStatus().Operations().LastKickAt != 0 {
		t.Fatalf("appB LastKickAt = %d, want 0", appB.runtimeStatus().Operations().LastKickAt)
	}
	if appA.runtimeStatus().Idempotency().ReplayHits != 1 {
		t.Fatalf("appA ReplayHits = %d, want 1", appA.runtimeStatus().Idempotency().ReplayHits)
	}
	if appB.runtimeStatus().Idempotency().ReplayHits != 0 {
		t.Fatalf("appB ReplayHits = %d, want 0", appB.runtimeStatus().Idempotency().ReplayHits)
	}
}

func TestAppsUseIndependentManagementPlatformRuntimeStatusStores(t *testing.T) {
	appA := New(nil)
	appB := New(nil)

	appA.managementPlatformRuntimeStatus().NoteConfigured("master-a", "reverse", "wss://master-a.example/node/ws", true)
	appA.managementPlatformRuntimeStatus().NoteReverseConnected("master-a")
	appA.managementPlatformRuntimeStatus().NoteCallbackQueued("master-a", 3, true)

	statusA := appA.managementPlatformRuntimeStatus().Status("master-a")
	statusB := appB.managementPlatformRuntimeStatus().Status("master-a")
	if !statusA.ReverseConnected || statusA.CallbackQueueSize != 3 || statusA.CallbackDropped != 1 {
		t.Fatalf("appA management platform runtime status = %#v, want connected queued state", statusA)
	}
	if statusB.ReverseConnected || statusB.CallbackQueueSize != 0 || statusB.CallbackDropped != 0 {
		t.Fatalf("appB management platform runtime status = %#v, want zero value", statusB)
	}
}

func TestAppsUseIndependentRuntimeIdentity(t *testing.T) {
	appA := New(nil)
	appB := New(nil)

	identityA := appA.runtimeIdentity()
	identityB := appB.runtimeIdentity()
	if identityA == nil || identityB == nil {
		t.Fatal("runtime identity should be initialized")
	}
	if identityA.BootID() == "" || identityA.StartedAt() == 0 {
		t.Fatalf("appA runtime identity should have values, got boot=%q started_at=%d", identityA.BootID(), identityA.StartedAt())
	}
	if identityA.BootID() != appA.runtimeIdentity().BootID() || identityA.StartedAt() != appA.runtimeIdentity().StartedAt() {
		t.Fatal("appA runtime identity should remain stable across reads")
	}
	if identityA.BootID() == identityB.BootID() {
		t.Fatalf("app boot ids should be instance-scoped, both were %q", identityA.BootID())
	}
}

func TestAppConcurrentLazyRuntimeServicesReuseSingleInstances(t *testing.T) {
	app := &App{}
	tests := []struct {
		name   string
		getter func() interface{}
	}{
		{
			name: "management platform runtime status",
			getter: func() interface{} {
				return app.managementPlatformRuntimeStatus()
			},
		},
		{
			name: "runtime status",
			getter: func() interface{} {
				return app.runtimeStatus()
			},
		},
		{
			name: "node operations",
			getter: func() interface{} {
				return app.nodeOperations()
			},
		},
		{
			name: "node traffic reporter",
			getter: func() interface{} {
				return app.nodeTrafficReporter()
			},
		},
		{
			name: "runtime identity",
			getter: func() interface{} {
				return app.runtimeIdentity()
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
					results <- interfacePointer(tt.getter())
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

func interfacePointer(value interface{}) uintptr {
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

func TestNodeTrafficEmitsThresholdedClientTrafficEvent(t *testing.T) {
	hooks := &recordingHooks{}
	storage := &stubTrafficNodeStorage{
		client: &file.Client{
			Id:             1,
			VerifyKey:      "vk-1",
			OwnerUserID:    20,
			ManagerUserIDs: []int{21},
			FlowLimit:      1024,
			Flow:           &file.Flow{},
		},
	}
	services := webservice.New()
	services.NodeStorage = storage
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
		Runtime: servercfg.RuntimeConfig{
			NodeTrafficReportInterval: 1,
			NodeTrafficReportStep:     10 * 1024,
		},
	}, Options{
		Hooks:    hooks,
		Services: &services,
	})

	ctx := &stubNodeTrafficContext{
		stubAppContext: stubAppContext{
			actor: &Actor{Kind: "admin", IsAdmin: true},
		},
		rawBody: []byte(`{"client_id":1,"in":6,"out":5}`),
	}

	app.NodeTraffic(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeTraffic() status = %d, want 200", ctx.status)
	}
	if len(hooks.events) != 2 {
		t.Fatalf("events = %#v, want client traffic event plus node traffic summary", hooks.events)
	}
	if hooks.events[0].Name != "client.traffic.reported" || hooks.events[0].Resource != "client" {
		t.Fatalf("first event = %#v, want client.traffic.reported", hooks.events[0])
	}
	if hooks.events[0].Fields["traffic_trigger"] != "initial" {
		t.Fatalf("client traffic event trigger = %#v, want initial", hooks.events[0].Fields["traffic_trigger"])
	}
	if hooks.events[0].Fields["traffic_in"] != int64(6) || hooks.events[0].Fields["traffic_out"] != int64(5) {
		t.Fatalf("client traffic event fields = %#v, want delta 6/5", hooks.events[0].Fields)
	}
	if hooks.events[1].Name != "node.traffic.report" {
		t.Fatalf("second event = %#v, want node.traffic.report", hooks.events[1])
	}
}

func TestNodeTrafficRejectsConflictingClientIdentifiers(t *testing.T) {
	services := webservice.New()
	services.NodeStorage = &stubTrafficNodeStorage{
		client: &file.Client{
			Id:        1,
			VerifyKey: "vk-1",
			Flow:      &file.Flow{},
		},
	}
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
	}, Options{
		Services: &services,
	})

	ctx := &stubNodeTrafficContext{
		stubAppContext: stubAppContext{
			actor: &Actor{Kind: "admin", IsAdmin: true},
		},
		rawBody: []byte(`{"client_id":1,"verify_key":"other-vkey","in":6,"out":5}`),
	}

	app.NodeTraffic(ctx)
	if ctx.status != 400 {
		t.Fatalf("NodeTraffic() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeTraffic() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "client_identifier_conflict" {
		t.Fatalf("NodeTraffic() error code = %q, want client_identifier_conflict", response.Error.Code)
	}
}

func TestNodeTrafficTreatsUnexpectedBackendErrorsAsInternalServerError(t *testing.T) {
	backendErr := errors.New("traffic storage unavailable")
	services := webservice.New()
	services.NodeStorage = &stubTrafficNodeStorage{
		client: &file.Client{
			Id:        1,
			VerifyKey: "vk-1",
			Flow:      &file.Flow{},
		},
		resolveTrafficErr: backendErr,
	}
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
	}, Options{
		Services: &services,
	})

	ctx := &stubNodeTrafficContext{
		stubAppContext: stubAppContext{
			actor: &Actor{Kind: "admin", IsAdmin: true},
		},
		rawBody: []byte(`{"client_id":1,"in":6,"out":5}`),
	}

	app.NodeTraffic(ctx)
	if ctx.status != 500 {
		t.Fatalf("NodeTraffic() status = %d, want 500", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeTraffic() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != backendErr.Error() {
		t.Fatalf("NodeTraffic() error = %+v, want request_failed/%q", response.Error, backendErr.Error())
	}
}
