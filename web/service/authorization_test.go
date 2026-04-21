package service

import (
	"errors"
	"testing"

	"github.com/djylb/nps/lib/file"
)

type stubPermissionResolver struct {
	normalizePrincipal func(Principal) Principal
	normalizeIdentity  func(*SessionIdentity) *SessionIdentity
}

func (s stubPermissionResolver) NormalizePrincipal(principal Principal) Principal {
	if s.normalizePrincipal != nil {
		return s.normalizePrincipal(principal)
	}
	return principal
}

func (s stubPermissionResolver) NormalizeIdentity(identity *SessionIdentity) *SessionIdentity {
	if s.normalizeIdentity != nil {
		return s.normalizeIdentity(identity)
	}
	return identity
}

func (stubPermissionResolver) KnownRoles() []string {
	return []string{"stub"}
}

func (stubPermissionResolver) KnownPermissions() []string {
	return []string{"stub:access"}
}

func (stubPermissionResolver) PermissionCatalog() map[string][]string {
	return map[string][]string{"stub": {"stub:access"}}
}

func TestNormalizePrincipalDerivesDefaultPermissions(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := authz.NormalizePrincipal(Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{7, 7, 2},
		Roles:         []string{RoleUser},
	})

	if len(principal.ClientIDs) != 2 || principal.ClientIDs[0] != 7 || principal.ClientIDs[1] != 2 {
		t.Fatalf("ClientIDs = %v, want [7 2]", principal.ClientIDs)
	}
	if !authz.Allows(principal, PermissionClientsRead) {
		t.Fatalf("user principal should allow %q", PermissionClientsRead)
	}
	if authz.Allows(principal, PermissionManagementAdmin) {
		t.Fatalf("user principal should not allow %q", PermissionManagementAdmin)
	}
}

func TestRequireAdminAcceptsExplicitManagementPermission(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := Principal{
		Authenticated: true,
		Kind:          "service",
		SubjectID:     "service:console",
		Permissions:   []string{PermissionManagementAdmin},
	}

	if err := authz.RequireAdmin(principal); err != nil {
		t.Fatalf("RequireAdmin() error = %v, want nil", err)
	}
}

func TestAllowsSupportsWildcardPermissions(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := Principal{
		Authenticated: true,
		Kind:          "service",
		SubjectID:     "service:automation",
		Roles:         []string{"service"},
		Permissions:   []string{"clients:*"},
	}

	if !authz.Allows(principal, PermissionClientsDelete) {
		t.Fatalf("wildcard permission should allow %q", PermissionClientsDelete)
	}
	if authz.Allows(principal, PermissionHostsRead) {
		t.Fatalf("clients wildcard should not allow %q", PermissionHostsRead)
	}
}

func TestAuthorizationServiceUsesInjectedResolver(t *testing.T) {
	authz := DefaultAuthorizationService{
		Resolver: stubPermissionResolver{
			normalizePrincipal: func(principal Principal) Principal {
				principal.Authenticated = true
				principal.Permissions = []string{"stub:access"}
				return principal
			},
		},
	}

	if err := authz.RequirePermission(Principal{}, "stub:access"); err != nil {
		t.Fatalf("RequirePermission() error = %v, want nil", err)
	}
	if err := authz.RequirePermission(Principal{}, PermissionClientsRead); err == nil {
		t.Fatal("RequirePermission() should reject permissions not granted by injected resolver")
	}
}

func TestResolveClientDefaultsToAllVisibleClientsForMultiClientUser(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{7, 8},
		Roles:         []string{RoleUser},
	}

	clientID, err := authz.ResolveClient(principal, 0)
	if err != nil {
		t.Fatalf("ResolveClient() error = %v, want nil", err)
	}
	if clientID != 0 {
		t.Fatalf("ResolveClient() = %d, want 0 for multi-client user default scope", clientID)
	}
}

func TestResolveClientDefaultsToPrimaryForSingleClientUser(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{7},
		Roles:         []string{RoleUser},
	}

	clientID, err := authz.ResolveClient(principal, 0)
	if err != nil {
		t.Fatalf("ResolveClient() error = %v, want nil", err)
	}
	if clientID != 7 {
		t.Fatalf("ResolveClient() = %d, want 7 for single-client user", clientID)
	}
}

func TestNormalizePrincipalDerivesClientRolePermissions(t *testing.T) {
	authz := DefaultAuthorizationService{}
	principal := authz.NormalizePrincipal(Principal{
		Authenticated: true,
		Kind:          "client",
		SubjectID:     "client:vkey:7",
		Username:      "vkey-client-7",
		ClientIDs:     []int{7},
		Roles:         []string{RoleClient},
	})

	if !authz.Allows(principal, PermissionClientsRead) {
		t.Fatalf("client principal should allow %q", PermissionClientsRead)
	}
	if !authz.Allows(principal, PermissionTunnelsUpdate) {
		t.Fatalf("client principal should allow %q", PermissionTunnelsUpdate)
	}
	if authz.Allows(principal, PermissionClientsUpdate) {
		t.Fatalf("client principal should not allow %q", PermissionClientsUpdate)
	}
	if authz.Allows(principal, PermissionClientsCreate) {
		t.Fatalf("client principal should not allow %q", PermissionClientsCreate)
	}
}

func TestResolvePrincipalContext(t *testing.T) {
	context := resolvePrincipalContext(Principal{
		Authenticated: true,
		Kind:          "platform_user",
		SubjectID:     "platform:master-a:user-1",
		Username:      "delegated-user",
		Attributes: map[string]string{
			"platform_id":              "master-a",
			"platform_actor_id":        "acct-user",
			"platform_service_user_id": "7",
			"user_id":                  "11",
		},
	})

	if context.Kind != "platform_user" {
		t.Fatalf("Kind = %q, want platform_user", context.Kind)
	}
	if context.PlatformID != "master-a" || context.PlatformActorID != "acct-user" {
		t.Fatalf("platform meta = %q/%q, want master-a/acct-user", context.PlatformID, context.PlatformActorID)
	}
	if context.ServiceUserID != 7 || context.UserID != 11 {
		t.Fatalf("user ids = %d/%d, want 7/11", context.ServiceUserID, context.UserID)
	}
	if context.SourceType != "platform_user" {
		t.Fatalf("SourceType = %q, want platform_user", context.SourceType)
	}
}

func TestResolvePrincipalContextSourceTypeFallbacks(t *testing.T) {
	if got := resolvePrincipalContext(Principal{Authenticated: true, Kind: "admin"}).SourceType; got != "node_admin" {
		t.Fatalf("admin SourceType = %q, want node_admin", got)
	}
	if got := resolvePrincipalContext(Principal{Authenticated: true, Kind: "client"}).SourceType; got != "node_client" {
		t.Fatalf("client SourceType = %q, want node_client", got)
	}
	if got := resolvePrincipalContext(Principal{Authenticated: true, Kind: "user"}).SourceType; got != "node_user" {
		t.Fatalf("user SourceType = %q, want node_user", got)
	}
}

func TestResolveNodeAccessScopePlatformAdminAccount(t *testing.T) {
	scope := ResolveNodeAccessScope(Principal{
		Authenticated: true,
		Kind:          "platform_admin",
		SubjectID:     "platform:master-a:admin-1",
		Username:      "platform-admin",
		Permissions: []string{
			PermissionClientsCreate,
			PermissionClientsRead,
			PermissionClientsUpdate,
			PermissionClientsDelete,
			PermissionClientsStatus,
		},
		Attributes: map[string]string{
			"platform_id":              "master-a",
			"platform_service_user_id": "9",
		},
	})

	if !scope.CanViewStatus() || !scope.CanViewUsage() {
		t.Fatalf("platform account admin should view status and usage")
	}
	if !scope.CanViewCallbackQueue() || !scope.CanManageCallbackQueue() {
		t.Fatalf("platform account admin should manage callback queue")
	}
	if scope.CanExportConfig() || scope.CanSync() || scope.CanWriteTraffic() {
		t.Fatalf("platform account admin should not get full-control node mutations")
	}
	if !scope.AllowsPlatform("master-a") || scope.AllowsPlatform("master-b") {
		t.Fatalf("platform account admin platform scope mismatch")
	}
	if !scope.AllowsUser(&file.User{Id: 9}) || scope.AllowsUser(&file.User{Id: 10}) {
		t.Fatalf("platform account admin should only see its service user")
	}
	if !scope.AllowsClient(&file.Client{Id: 1, UserId: 9, OwnerUserID: 9}) {
		t.Fatalf("platform account admin should manage owned service-user clients")
	}
	if scope.AllowsClient(&file.Client{Id: 2, UserId: 10, OwnerUserID: 10}) {
		t.Fatalf("platform account admin should not manage foreign clients")
	}
}

func TestResolveNodeAccessScopePlatformUserDelegatedClients(t *testing.T) {
	scope := ResolveNodeAccessScope(Principal{
		Authenticated: true,
		Kind:          "platform_user",
		SubjectID:     "platform:master-a:user-1",
		Username:      "delegated-user",
		ClientIDs:     []int{3, 5},
		Permissions:   []string{PermissionClientsRead},
		Attributes: map[string]string{
			"platform_id":              "master-a",
			"platform_service_user_id": "9",
		},
	})

	if !scope.CanViewStatus() || !scope.CanViewUsage() {
		t.Fatalf("platform user should view status and usage")
	}
	if scope.CanViewCallbackQueue() || scope.CanManageCallbackQueue() {
		t.Fatalf("platform user should not access callback queue admin endpoints")
	}
	if !scope.AllowsPlatform("master-a") || scope.AllowsPlatform("master-b") {
		t.Fatalf("platform user platform scope mismatch")
	}
	if !scope.AllowsClient(&file.Client{Id: 3, UserId: 9, OwnerUserID: 9}) {
		t.Fatalf("platform user should manage delegated owned client")
	}
	if scope.AllowsClient(&file.Client{Id: 4, UserId: 9, OwnerUserID: 9}) {
		t.Fatalf("platform user should not manage undelegated client")
	}
	if scope.AllowsClient(&file.Client{Id: 3, UserId: 10, OwnerUserID: 10}) {
		t.Fatalf("platform user should not manage foreign-owner client even if ids match")
	}
}

func TestResolveNodeAccessScopeLocalUserUsesOwnerAndManagerBinding(t *testing.T) {
	scope := ResolveNodeAccessScope(Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:tenant",
		Username:      "tenant",
		Permissions:   []string{PermissionClientsRead},
		Attributes: map[string]string{
			"user_id": "7",
		},
	})

	if scope.CanViewStatus() {
		t.Fatalf("local user should not view node status")
	}
	if !scope.CanViewUsage() {
		t.Fatalf("local user should view scoped usage")
	}
	if !scope.AllowsUser(&file.User{Id: 7}) || scope.AllowsUser(&file.User{Id: 8}) {
		t.Fatalf("local user should only see itself")
	}
	if !scope.AllowsClient(&file.Client{Id: 1, UserId: 7, OwnerUserID: 7}) {
		t.Fatalf("local user should manage owned client")
	}
	if !scope.AllowsClient(&file.Client{Id: 2, UserId: 9, OwnerUserID: 9, ManagerUserIDs: []int{7}}) {
		t.Fatalf("local user should manage delegated client")
	}
	if scope.AllowsClient(&file.Client{Id: 3, UserId: 9, OwnerUserID: 9, ManagerUserIDs: []int{8}}) {
		t.Fatalf("local user should not manage unrelated client")
	}
}

func TestResolveNodeAccessScopeFullAdmin(t *testing.T) {
	scope := ResolveNodeAccessScope(Principal{
		Authenticated: true,
		Kind:          "admin",
		SubjectID:     "admin:root",
		Username:      "root",
		IsAdmin:       true,
		Permissions:   []string{PermissionAll},
	})

	if !scope.IsFullAccess() || !scope.CanExportConfig() || !scope.CanSync() || !scope.CanWriteTraffic() {
		t.Fatalf("full admin should keep full node-control access")
	}
	if !scope.CanViewCallbackQueue() || !scope.CanManageCallbackQueue() {
		t.Fatalf("full admin should manage callback queue")
	}
	if !scope.AllowsPlatform("master-a") {
		t.Fatalf("full admin should see all platforms")
	}
}

func TestResolveNodeAccessScopeClientPrincipalIsSingleClientScoped(t *testing.T) {
	scope := ResolveNodeAccessScope(Principal{
		Authenticated: true,
		Kind:          "client",
		SubjectID:     "client:vkey:3",
		Username:      "vkey-client-3",
		ClientIDs:     []int{3},
		Roles:         []string{RoleClient},
		Permissions:   []string{PermissionClientsRead, PermissionTunnelsRead, PermissionHostsRead},
		Attributes: map[string]string{
			"client_id":  "3",
			"login_mode": "client_vkey",
		},
	})

	if scope.CanViewStatus() {
		t.Fatalf("client principal should not view node status")
	}
	if !scope.CanViewUsage() {
		t.Fatalf("client principal should view scoped usage")
	}
	if !scope.AllowsClient(&file.Client{Id: 3, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}) {
		t.Fatalf("client principal should manage its own client scope")
	}
	if scope.AllowsClient(&file.Client{Id: 4, Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}) {
		t.Fatalf("client principal should not manage foreign client scope")
	}
	if scope.AllowsUser(&file.User{Id: 3}) {
		t.Fatalf("client principal should not expose user scope")
	}
}

func TestResolveNodeAccessScopeWithAuthorizationUsesInjectedResolver(t *testing.T) {
	scope := ResolveNodeAccessScopeWithAuthorization(DefaultAuthorizationService{
		Resolver: stubPermissionResolver{
			normalizePrincipal: func(principal Principal) Principal {
				if principal.Username == "mapped-platform" {
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
	}, Principal{Username: "mapped-platform"})

	if !scope.CanViewStatus() || !scope.CanViewCallbackQueue() {
		t.Fatalf("ResolveNodeAccessScopeWithAuthorization() = %+v, want platform-admin scope from injected resolver", scope)
	}
	if !scope.AllowsPlatform("platform-a") || scope.AllowsPlatform("platform-b") {
		t.Fatalf("ResolveNodeAccessScopeWithAuthorization() platform scope = %+v, want only platform-a", scope)
	}
}

type stubProtectedActionAuthz struct {
	requirePermission func(Principal, string) error
	resolveClient     func(Principal, int) (int, error)
	requireClient     func(Principal, int) error
	requireTunnel     func(Principal, int) error
	requireHost       func(Principal, int) error
}

func (s stubProtectedActionAuthz) NormalizePrincipal(principal Principal) Principal {
	return principal
}

func (s stubProtectedActionAuthz) ClientScope(principal Principal) ClientScope {
	return ClientScope{}
}

func (s stubProtectedActionAuthz) Permissions(principal Principal) []string {
	return append([]string(nil), principal.Permissions...)
}

func (s stubProtectedActionAuthz) Allows(principal Principal, permission string) bool {
	return true
}

func (s stubProtectedActionAuthz) ResolveClient(principal Principal, requestedClientID int) (int, error) {
	if s.resolveClient != nil {
		return s.resolveClient(principal, requestedClientID)
	}
	return requestedClientID, nil
}

func (s stubProtectedActionAuthz) RequirePermission(principal Principal, permission string) error {
	if s.requirePermission != nil {
		return s.requirePermission(principal, permission)
	}
	return nil
}

func (s stubProtectedActionAuthz) RequireAdmin(Principal) error {
	return nil
}

func (s stubProtectedActionAuthz) RequireClient(principal Principal, id int) error {
	if s.requireClient != nil {
		return s.requireClient(principal, id)
	}
	return nil
}

func (s stubProtectedActionAuthz) RequireTunnel(principal Principal, id int) error {
	if s.requireTunnel != nil {
		return s.requireTunnel(principal, id)
	}
	return nil
}

func (s stubProtectedActionAuthz) RequireHost(principal Principal, id int) error {
	if s.requireHost != nil {
		return s.requireHost(principal, id)
	}
	return nil
}

func TestResolveProtectedActionAccessResolvesClientAndOwnership(t *testing.T) {
	calledPermission := ""
	calledRequestedClientID := 0
	calledTunnelID := 0
	result, err := ResolveProtectedActionAccess(stubProtectedActionAuthz{
		requirePermission: func(principal Principal, permission string) error {
			calledPermission = permission
			return nil
		},
		resolveClient: func(principal Principal, requestedClientID int) (int, error) {
			calledRequestedClientID = requestedClientID
			return 7, nil
		},
		requireTunnel: func(principal Principal, id int) error {
			calledTunnelID = id
			return nil
		},
	}, ProtectedActionAccessInput{
		Principal:         Principal{Authenticated: true, Username: "tester"},
		Permission:        PermissionTunnelsUpdate,
		ClientScope:       true,
		Ownership:         "tunnel",
		RequestedClientID: 5,
		ResourceID:        12,
	})
	if err != nil {
		t.Fatalf("ResolveProtectedActionAccess() error = %v", err)
	}
	if result.ResolvedClientID != 7 {
		t.Fatalf("ResolvedClientID = %d, want 7", result.ResolvedClientID)
	}
	if calledPermission != PermissionTunnelsUpdate {
		t.Fatalf("RequirePermission called with %q, want %q", calledPermission, PermissionTunnelsUpdate)
	}
	if calledRequestedClientID != 5 {
		t.Fatalf("ResolveClient called with %d, want 5", calledRequestedClientID)
	}
	if calledTunnelID != 12 {
		t.Fatalf("RequireTunnel called with %d, want %d", calledTunnelID, 12)
	}
}

func TestResolveProtectedActionAccessReturnsPermissionError(t *testing.T) {
	_, err := ResolveProtectedActionAccess(stubProtectedActionAuthz{
		requirePermission: func(Principal, string) error {
			return ErrForbidden
		},
		resolveClient: func(Principal, int) (int, error) {
			t.Fatal("ResolveClient should not be called after permission failure")
			return 0, nil
		},
	}, ProtectedActionAccessInput{
		Principal:   Principal{Authenticated: true},
		Permission:  PermissionClientsRead,
		ClientScope: true,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResolveProtectedActionAccess() error = %v, want %v", err, ErrForbidden)
	}
}
