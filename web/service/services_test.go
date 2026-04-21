package service

import (
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type customSystemService struct{}

func (*customSystemService) Info() SystemInfo { return SystemInfo{} }

func (*customSystemService) BridgeDisplay(*servercfg.Snapshot, string) BridgeDisplay {
	return BridgeDisplay{}
}

func (*customSystemService) RegisterManagementAccess(string) {}

type taggedPermissionResolver struct {
	tag string
}

func (r taggedPermissionResolver) NormalizePrincipal(principal Principal) Principal {
	principal.Authenticated = true
	principal.Kind = "user"
	principal.Permissions = []string{"resolver:" + r.tag}
	return DefaultPermissionResolver().NormalizePrincipal(principal)
}

func (r taggedPermissionResolver) NormalizeIdentity(identity *SessionIdentity) *SessionIdentity {
	if identity == nil {
		return nil
	}
	normalized := *identity
	normalized.Authenticated = true
	normalized.Kind = "user"
	normalized.Permissions = []string{"resolver:" + r.tag}
	return DefaultPermissionResolver().NormalizeIdentity(&normalized)
}

func (taggedPermissionResolver) KnownRoles() []string {
	return DefaultPermissionResolver().KnownRoles()
}

func (taggedPermissionResolver) KnownPermissions() []string {
	return DefaultPermissionResolver().KnownPermissions()
}

func (taggedPermissionResolver) PermissionCatalog() map[string][]string {
	return DefaultPermissionResolver().PermissionCatalog()
}

func TestBindDefaultServicesPropagatesBackendAndConfigProvider(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Web: servercfg.WebConfig{Username: "cfg-admin"},
	}
	repo := &stubRepository{}
	runtime := &stubRuntime{}
	customBackend := Backend{
		Repository: repo,
		Runtime:    runtime,
	}
	bound := BindDefaultServices(MergeServices(New(), Services{
		Backend: customBackend,
	}), func() *servercfg.Snapshot {
		return cfg
	})

	auth, ok := bound.Auth.(DefaultAuthService)
	if !ok {
		t.Fatalf("Auth type = %T, want DefaultAuthService", bound.Auth)
	}
	if auth.Backend.Repository != customBackend.Repository || auth.Backend.Runtime != customBackend.Runtime {
		t.Fatalf("Auth backend = %+v, want custom backend %+v", auth.Backend, customBackend)
	}
	if got := auth.ConfigProvider(); got != cfg {
		t.Fatalf("Auth ConfigProvider() = %p, want %p", got, cfg)
	}

	clients, ok := bound.Clients.(DefaultClientService)
	if !ok {
		t.Fatalf("Clients type = %T, want DefaultClientService", bound.Clients)
	}
	if clients.Backend.Repository != customBackend.Repository || clients.Backend.Runtime != customBackend.Runtime {
		t.Fatalf("Clients backend = %+v, want custom backend %+v", clients.Backend, customBackend)
	}
	if got := clients.ConfigProvider(); got != cfg {
		t.Fatalf("Clients ConfigProvider() = %p, want %p", got, cfg)
	}

	users, ok := bound.Users.(DefaultUserService)
	if !ok {
		t.Fatalf("Users type = %T, want DefaultUserService", bound.Users)
	}
	if users.Backend.Repository != customBackend.Repository || users.Backend.Runtime != customBackend.Runtime {
		t.Fatalf("Users backend = %+v, want custom backend %+v", users.Backend, customBackend)
	}
	if got := users.ConfigProvider(); got != cfg {
		t.Fatalf("Users ConfigProvider() = %p, want %p", got, cfg)
	}

	if _, ok := bound.LoginPolicy.(*DefaultLoginPolicy); !ok {
		t.Fatalf("LoginPolicy type = %T, want *DefaultLoginPolicy", bound.LoginPolicy)
	}

	authz, ok := bound.Authz.(DefaultAuthorizationService)
	if !ok {
		t.Fatalf("Authz type = %T, want DefaultAuthorizationService", bound.Authz)
	}
	if authz.Backend.Repository != customBackend.Repository || authz.Backend.Runtime != customBackend.Runtime {
		t.Fatalf("Authz backend = %+v, want custom backend %+v", authz.Backend, customBackend)
	}

	control, ok := bound.NodeControl.(DefaultNodeControlService)
	if !ok {
		t.Fatalf("NodeControl type = %T, want DefaultNodeControlService", bound.NodeControl)
	}
	if control.Backend.Repository != customBackend.Repository || control.Backend.Runtime != customBackend.Runtime {
		t.Fatalf("NodeControl backend = %+v, want custom backend %+v", control.Backend, customBackend)
	}

	platforms, ok := bound.ManagementPlatforms.(DefaultManagementPlatformStore)
	if !ok {
		t.Fatalf("ManagementPlatforms type = %T, want DefaultManagementPlatformStore", bound.ManagementPlatforms)
	}
	if platforms.Backend.Repository != customBackend.Repository || platforms.Backend.Runtime != customBackend.Runtime {
		t.Fatalf("ManagementPlatforms backend = %+v, want custom backend %+v", platforms.Backend, customBackend)
	}

}

func TestBindDefaultServicesRebindsDefaultAuthResolversToCurrentPermissions(t *testing.T) {
	resolver := taggedPermissionResolver{tag: "current"}
	bound := BindDefaultServices(MergeServices(New(), Services{
		Permissions: resolver,
	}), func() *servercfg.Snapshot {
		return &servercfg.Snapshot{}
	})

	auth, ok := bound.Auth.(DefaultAuthService)
	if !ok {
		t.Fatalf("Auth type = %T, want DefaultAuthService", bound.Auth)
	}
	principal := auth.resolver().NormalizePrincipal(Principal{Username: "resolver-user"})
	if !permissionSetAllows(principal.Permissions, "resolver:current") {
		t.Fatalf("Auth resolver permissions = %v, want resolver:current", principal.Permissions)
	}

	authz, ok := bound.Authz.(DefaultAuthorizationService)
	if !ok {
		t.Fatalf("Authz type = %T, want DefaultAuthorizationService", bound.Authz)
	}
	principal = authz.resolver().NormalizePrincipal(Principal{Username: "resolver-user"})
	if !permissionSetAllows(principal.Permissions, "resolver:current") {
		t.Fatalf("Authz resolver permissions = %v, want resolver:current", principal.Permissions)
	}
}

func TestBindDefaultServicesReplacesTypedNilServices(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	var loginPolicy *DefaultLoginPolicy
	var runtimeIdentity *StaticNodeRuntimeIdentity
	var runtimeStatus *InMemoryNodeRuntimeStatusStore
	var operations *InMemoryNodeOperationStore
	var reporter *DefaultNodeTrafficReporter
	var platformStatus *InMemoryManagementPlatformRuntimeStatusStore

	bound := BindDefaultServices(Services{
		LoginPolicy:                     loginPolicy,
		NodeRuntimeIdentity:             runtimeIdentity,
		NodeRuntimeStatus:               runtimeStatus,
		NodeOperations:                  operations,
		NodeTrafficReporter:             reporter,
		ManagementPlatformRuntimeStatus: platformStatus,
	}, func() *servercfg.Snapshot {
		return cfg
	})

	login, ok := bound.LoginPolicy.(*DefaultLoginPolicy)
	if !ok || login == nil {
		t.Fatalf("LoginPolicy = %T, want non-nil *DefaultLoginPolicy", bound.LoginPolicy)
	}
	if got := login.ConfigProvider(); got != cfg {
		t.Fatalf("LoginPolicy ConfigProvider() = %p, want %p", got, cfg)
	}

	if identity, ok := bound.NodeRuntimeIdentity.(*StaticNodeRuntimeIdentity); !ok || identity == nil {
		t.Fatalf("NodeRuntimeIdentity = %T, want non-nil *StaticNodeRuntimeIdentity", bound.NodeRuntimeIdentity)
	}
	if status, ok := bound.NodeRuntimeStatus.(*InMemoryNodeRuntimeStatusStore); !ok || status == nil {
		t.Fatalf("NodeRuntimeStatus = %T, want non-nil *InMemoryNodeRuntimeStatusStore", bound.NodeRuntimeStatus)
	}
	if store, ok := bound.NodeOperations.(*InMemoryNodeOperationStore); !ok || store == nil {
		t.Fatalf("NodeOperations = %T, want non-nil *InMemoryNodeOperationStore", bound.NodeOperations)
	}

	bound.NodeRuntimeStatus.NoteOperation("sync", nil)
	if got := bound.NodeRuntimeStatus.Operations().LastSyncAt; got == 0 {
		t.Fatalf("NodeRuntimeStatus.Operations().LastSyncAt = %d, want recorded operation", got)
	}
	bound.NodeOperations.Record(NodeOperationRecordPayload{OperationID: "op-1", Kind: "sync"})
	if got := len(bound.NodeOperations.Query(NodeOperationQueryInput{Scope: ResolveNodeAccessScope(Principal{Authenticated: true, IsAdmin: true})}).Items); got != 1 {
		t.Fatalf("NodeOperations.Query() items = %d, want 1", got)
	}

	trafficReporter, ok := bound.NodeTrafficReporter.(*DefaultNodeTrafficReporter)
	if !ok || trafficReporter == nil {
		t.Fatalf("NodeTrafficReporter = %T, want non-nil *DefaultNodeTrafficReporter", bound.NodeTrafficReporter)
	}
	if got := trafficReporter.ConfigProvider(); got != cfg {
		t.Fatalf("NodeTrafficReporter ConfigProvider() = %p, want %p", got, cfg)
	}
	if report, ok := bound.NodeTrafficReporter.Observe(&file.Client{Id: 1}, 1, 1); ok || report != nil {
		t.Fatalf("NodeTrafficReporter.Observe() = %#v, %v, want disabled default reporter", report, ok)
	}

	platformRuntime, ok := bound.ManagementPlatformRuntimeStatus.(*InMemoryManagementPlatformRuntimeStatusStore)
	if !ok || platformRuntime == nil {
		t.Fatalf("ManagementPlatformRuntimeStatus = %T, want non-nil *InMemoryManagementPlatformRuntimeStatusStore", bound.ManagementPlatformRuntimeStatus)
	}
	platformRuntime.NoteConfigured("master-a", "dual", "wss://master-a.example/node/ws", true)
	if got := platformRuntime.Status("master-a").ReverseWSURL; got != "wss://master-a.example/node/ws" {
		t.Fatalf("ManagementPlatformRuntimeStatus.Status() = %q, want configured ws url", got)
	}
}

func TestBindDefaultServicesReplacesTypedNilBackendDependencies(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	var repo *stubRepository
	var runtime *stubRuntime

	bound := BindDefaultServices(Services{
		Backend: Backend{
			Repository: repo,
			Runtime:    runtime,
		},
	}, func() *servercfg.Snapshot {
		return cfg
	})

	if isNilServiceValue(bound.Backend.Repository) {
		t.Fatalf("BindDefaultServices() backend repository = %T, want non-nil default repository", bound.Backend.Repository)
	}
	if isNilServiceValue(bound.Backend.Runtime) {
		t.Fatalf("BindDefaultServices() backend runtime = %T, want non-nil default runtime", bound.Backend.Runtime)
	}

	auth, ok := bound.Auth.(DefaultAuthService)
	if !ok {
		t.Fatalf("Auth type = %T, want DefaultAuthService", bound.Auth)
	}
	if isNilServiceValue(auth.Backend.Repository) || isNilServiceValue(auth.Backend.Runtime) {
		t.Fatalf("Auth backend kept typed nil dependencies: %+v", auth.Backend)
	}
}

func TestBindDefaultServicesFillsPointerDefaultImplementations(t *testing.T) {
	cfg := &servercfg.Snapshot{}
	repo := &stubRepository{}
	runtime := &stubRuntime{}
	services := BindDefaultServices(Services{
		Backend:             Backend{Repository: repo, Runtime: runtime},
		Permissions:         DefaultPermissionResolver(),
		System:              DefaultSystemService{},
		LoginPolicy:         &DefaultLoginPolicy{},
		Auth:                &DefaultAuthService{},
		Authz:               &DefaultAuthorizationService{},
		Clients:             &DefaultClientService{},
		Users:               &DefaultUserService{},
		Globals:             &DefaultGlobalService{},
		Index:               &DefaultIndexService{},
		ManagementPlatforms: &DefaultManagementPlatformStore{},
		NodeControl:         &DefaultNodeControlService{},
	}, func() *servercfg.Snapshot {
		return cfg
	})

	login, ok := services.LoginPolicy.(*DefaultLoginPolicy)
	if !ok || login == nil {
		t.Fatalf("LoginPolicy = %T, want non-nil *DefaultLoginPolicy", services.LoginPolicy)
	}
	if got := login.ConfigProvider(); got != cfg {
		t.Fatalf("LoginPolicy ConfigProvider() = %p, want %p", got, cfg)
	}

	auth, ok := services.Auth.(*DefaultAuthService)
	if !ok || auth == nil {
		t.Fatalf("Auth = %T, want non-nil *DefaultAuthService", services.Auth)
	}
	if auth.Resolver == nil || auth.Repo != repo || auth.Backend.Repository != repo || auth.Backend.Runtime != runtime {
		t.Fatalf("Auth not fully bound: %+v", auth)
	}
	if got := auth.ConfigProvider(); got != cfg {
		t.Fatalf("Auth ConfigProvider() = %p, want %p", got, cfg)
	}

	authz, ok := services.Authz.(*DefaultAuthorizationService)
	if !ok || authz == nil {
		t.Fatalf("Authz = %T, want non-nil *DefaultAuthorizationService", services.Authz)
	}
	if authz.Resolver == nil || authz.Repo != repo || authz.Backend.Repository != repo || authz.Backend.Runtime != runtime {
		t.Fatalf("Authz not fully bound: %+v", authz)
	}

	clients, ok := services.Clients.(*DefaultClientService)
	if !ok || clients == nil {
		t.Fatalf("Clients = %T, want non-nil *DefaultClientService", services.Clients)
	}
	if clients.Repo != repo || clients.Runtime != runtime || clients.Backend.Repository != repo || clients.Backend.Runtime != runtime {
		t.Fatalf("Clients not fully bound: %+v", clients)
	}
	if got := clients.ConfigProvider(); got != cfg {
		t.Fatalf("Clients ConfigProvider() = %p, want %p", got, cfg)
	}

	control, ok := services.NodeControl.(*DefaultNodeControlService)
	if !ok || control == nil {
		t.Fatalf("NodeControl = %T, want non-nil *DefaultNodeControlService", services.NodeControl)
	}
	if control.System == nil || control.Authz == nil || control.Repo != repo || control.Runtime != runtime || control.Backend.Repository != repo || control.Backend.Runtime != runtime {
		t.Fatalf("NodeControl not fully bound: %+v", control)
	}
}

func TestMergeServicesIgnoresTypedNilOverrides(t *testing.T) {
	base := New()
	var loginPolicy *DefaultLoginPolicy
	var reporter *DefaultNodeTrafficReporter

	merged := MergeServices(base, Services{
		LoginPolicy:         loginPolicy,
		NodeTrafficReporter: reporter,
	})

	login, ok := merged.LoginPolicy.(*DefaultLoginPolicy)
	if !ok || login == nil {
		t.Fatalf("MergeServices() replaced LoginPolicy with typed nil override: %T", merged.LoginPolicy)
	}
	trafficReporter, ok := merged.NodeTrafficReporter.(*DefaultNodeTrafficReporter)
	if !ok || trafficReporter == nil {
		t.Fatalf("MergeServices() replaced NodeTrafficReporter with typed nil override: %T", merged.NodeTrafficReporter)
	}
}

func TestMergeServicesIgnoresTypedNilBackendOverrides(t *testing.T) {
	base := New()
	var repo *stubRepository
	var runtime *stubRuntime

	merged := MergeServices(base, Services{
		Backend: Backend{
			Repository: repo,
			Runtime:    runtime,
		},
	})

	if isNilServiceValue(merged.Backend.Repository) {
		t.Fatalf("MergeServices() replaced backend repository with typed nil override: %T", merged.Backend.Repository)
	}
	if isNilServiceValue(merged.Backend.Runtime) {
		t.Fatalf("MergeServices() replaced backend runtime with typed nil override: %T", merged.Backend.Runtime)
	}
}

func TestDefaultServiceHelpersIgnoreTypedNilDependencies(t *testing.T) {
	var repo *stubRepository
	var runtime *stubRuntime
	var resolver *stubPermissionResolver
	var authz *DefaultAuthorizationService
	var loginPolicy *DefaultLoginPolicy
	var quotaStore *DefaultQuotaStore

	backend := Backend{Repository: repo, Runtime: runtime}
	if isNilServiceValue(backend.repo()) {
		t.Fatalf("Backend.repo() kept typed nil repository")
	}
	if isNilServiceValue(backend.runtime()) {
		t.Fatalf("Backend.runtime() kept typed nil runtime")
	}

	authService := DefaultAuthService{Resolver: resolver, Repo: repo, Backend: backend}
	if isNilServiceValue(authService.resolver()) {
		t.Fatalf("DefaultAuthService.resolver() kept typed nil resolver")
	}
	if isNilServiceValue(authService.repo()) {
		t.Fatalf("DefaultAuthService.repo() kept typed nil repository")
	}

	authzService := DefaultAuthorizationService{Resolver: resolver, Repo: repo, Backend: backend}
	if isNilServiceValue(authzService.resolver()) {
		t.Fatalf("DefaultAuthorizationService.resolver() kept typed nil resolver")
	}
	if isNilServiceValue(authzService.repo()) {
		t.Fatalf("DefaultAuthorizationService.repo() kept typed nil repository")
	}

	clientService := DefaultClientService{Repo: repo, Runtime: runtime, Backend: backend}
	if isNilServiceValue(clientService.repo()) {
		t.Fatalf("DefaultClientService.repo() kept typed nil repository")
	}
	if isNilServiceValue(clientService.runtime()) {
		t.Fatalf("DefaultClientService.runtime() kept typed nil runtime")
	}

	userService := DefaultUserService{Repo: repo, Runtime: runtime, Backend: backend}
	if isNilServiceValue(userService.repo()) {
		t.Fatalf("DefaultUserService.repo() kept typed nil repository")
	}
	if isNilServiceValue(userService.runtime()) {
		t.Fatalf("DefaultUserService.runtime() kept typed nil runtime")
	}

	indexService := DefaultIndexService{Repo: repo, Runtime: runtime, Backend: backend, QuotaStore: quotaStore}
	if isNilServiceValue(indexService.repo()) {
		t.Fatalf("DefaultIndexService.repo() kept typed nil repository")
	}
	if isNilServiceValue(indexService.runtime()) {
		t.Fatalf("DefaultIndexService.runtime() kept typed nil runtime")
	}
	if isNilServiceValue(indexService.quotaStore()) {
		t.Fatalf("DefaultIndexService.quotaStore() kept typed nil quota store")
	}

	controlService := DefaultNodeControlService{Repo: repo, Runtime: runtime, Authz: authz, Backend: backend}
	if isNilServiceValue(controlService.repo()) {
		t.Fatalf("DefaultNodeControlService.repo() kept typed nil repository")
	}
	if isNilServiceValue(controlService.runtime()) {
		t.Fatalf("DefaultNodeControlService.runtime() kept typed nil runtime")
	}
	if isNilServiceValue(controlService.authz()) {
		t.Fatalf("DefaultNodeControlService.authz() kept typed nil authz")
	}

	globalService := DefaultGlobalService{LoginPolicy: loginPolicy, Repo: repo, Backend: backend}
	if isNilServiceValue(globalService.loginPolicy()) {
		t.Fatalf("DefaultGlobalService.loginPolicy() kept typed nil login policy")
	}
	if isNilServiceValue(globalService.repo()) {
		t.Fatalf("DefaultGlobalService.repo() kept typed nil repository")
	}

	platformStore := DefaultManagementPlatformStore{Repo: repo, Backend: backend}
	if isNilServiceValue(platformStore.repo()) {
		t.Fatalf("DefaultManagementPlatformStore.repo() kept typed nil repository")
	}
}

func TestBindDefaultServicesReplacesCustomTypedNilSystemService(t *testing.T) {
	var system *customSystemService

	bound := BindDefaultServices(Services{System: system}, servercfg.Current)

	if _, ok := bound.System.(DefaultSystemService); !ok {
		t.Fatalf("System = %T, want DefaultSystemService for custom typed nil override", bound.System)
	}
}

func TestMergeServicesIgnoresCustomTypedNilSystemOverride(t *testing.T) {
	base := New()
	var system *customSystemService

	merged := MergeServices(base, Services{System: system})

	if _, ok := merged.System.(DefaultSystemService); !ok {
		t.Fatalf("System = %T, want DefaultSystemService after typed nil custom override", merged.System)
	}
}
