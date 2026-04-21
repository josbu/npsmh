package api

import (
	"sort"
	"strconv"
	"strings"

	webservice "github.com/djylb/nps/web/service"
)

type Actor struct {
	Kind        string            `json:"kind"`
	SubjectID   string            `json:"subject_id,omitempty"`
	Username    string            `json:"username,omitempty"`
	IsAdmin     bool              `json:"is_admin"`
	ClientIDs   []int             `json:"client_ids,omitempty"`
	Roles       []string          `json:"roles,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

func AnonymousActor() *Actor {
	return &Actor{
		Kind:  webservice.RoleAnonymous,
		Roles: []string{webservice.RoleAnonymous},
	}
}

func AdminActor(username string) *Actor {
	return AdminActorWithFallback(username, "")
}

func AdminActorWithFallback(username, fallbackAdminUsername string) *Actor {
	username = strings.TrimSpace(username)
	if username == "" {
		username = strings.TrimSpace(fallbackAdminUsername)
	}
	if username == "" {
		username = "admin"
	}
	principal := webservice.DefaultAuthorizationService{}.NormalizePrincipal(webservice.Principal{
		Authenticated: true,
		Kind:          "admin",
		SubjectID:     "admin:" + username,
		Username:      username,
		IsAdmin:       true,
		Roles:         []string{webservice.RoleAdmin},
		Permissions:   []string{webservice.PermissionAll},
	})
	return ActorFromPrincipal(principal)
}

func UserActor(username string, clientIDs []int) *Actor {
	filtered := make([]int, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		if clientID > 0 {
			filtered = append(filtered, clientID)
		}
	}
	principal := webservice.DefaultAuthorizationService{}.NormalizePrincipal(webservice.Principal{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     strings.TrimSpace(username),
		Username:      strings.TrimSpace(username),
		ClientIDs:     filtered,
		Roles:         []string{webservice.RoleUser},
	})
	return ActorFromPrincipal(principal)
}

func PlatformActor(platformID, role, username, actorID string, fullControl bool, clientIDs []int, serviceUserID int) *Actor {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = "admin"
	}
	kind := "platform_user"
	isAdmin := false
	permissions := []string{
		webservice.PermissionClientsCreate,
		webservice.PermissionClientsRead,
		webservice.PermissionClientsUpdate,
		webservice.PermissionClientsDelete,
		webservice.PermissionClientsStatus,
		webservice.PermissionTunnelsCreate,
		webservice.PermissionTunnelsRead,
		webservice.PermissionTunnelsUpdate,
		webservice.PermissionTunnelsDelete,
		webservice.PermissionTunnelsControl,
		webservice.PermissionHostsCreate,
		webservice.PermissionHostsRead,
		webservice.PermissionHostsUpdate,
		webservice.PermissionHostsDelete,
		webservice.PermissionHostsControl,
	}
	if role == "admin" {
		kind = "platform_admin"
	}
	if fullControl {
		isAdmin = true
		permissions = []string{webservice.PermissionAll}
	}
	principal := webservice.DefaultAuthorizationService{}.NormalizePrincipal(webservice.Principal{
		Authenticated: true,
		Kind:          kind,
		SubjectID:     "platform:" + strings.TrimSpace(platformID) + ":" + strings.TrimSpace(actorID),
		Username:      strings.TrimSpace(username),
		IsAdmin:       isAdmin,
		ClientIDs:     append([]int(nil), clientIDs...),
		Roles:         []string{webservice.RoleUser},
		Permissions:   permissions,
		Attributes: map[string]string{
			"platform_id":              strings.TrimSpace(platformID),
			"platform_role":            role,
			"platform_actor_id":        strings.TrimSpace(actorID),
			"platform_control_scope":   map[bool]string{true: "full", false: "account"}[fullControl],
			"platform_service_user_id": strconv.Itoa(serviceUserID),
		},
	})
	return ActorFromPrincipal(principal)
}

func ActorFromSessionIdentity(identity *webservice.SessionIdentity) *Actor {
	return ActorFromSessionIdentityWithFallback(identity, "")
}

func ActorFromSessionIdentityWithFallback(identity *webservice.SessionIdentity, fallbackAdminUsername string) *Actor {
	if identity == nil || !identity.Authenticated {
		return AnonymousActor()
	}
	actor := &Actor{
		Kind:        strings.TrimSpace(identity.Kind),
		SubjectID:   strings.TrimSpace(identity.SubjectID),
		Username:    strings.TrimSpace(identity.Username),
		IsAdmin:     identity.IsAdmin,
		ClientIDs:   append([]int(nil), identity.ClientIDs...),
		Roles:       append([]string(nil), identity.Roles...),
		Permissions: append([]string(nil), identity.Permissions...),
	}
	if len(identity.Attributes) > 0 {
		actor.Attributes = make(map[string]string, len(identity.Attributes))
		for key, value := range identity.Attributes {
			actor.Attributes[key] = value
		}
	}
	if actor.Kind == "" {
		if actor.IsAdmin {
			actor.Kind = "admin"
		} else if hasActorRole(actor.Roles, webservice.RoleClient) {
			actor.Kind = "client"
		} else {
			actor.Kind = "user"
		}
	}
	if actor.Kind == "admin" && actor.SubjectID == "" {
		if actor.Username == "" {
			return AdminActorWithFallback("", fallbackAdminUsername)
		}
		return AdminActorWithFallback(actor.Username, fallbackAdminUsername)
	}
	return actor
}

func PrincipalFromActor(actor *Actor) webservice.Principal {
	if actor == nil {
		return webservice.Principal{}
	}
	principal := webservice.Principal{
		Authenticated: actor.Kind != "" && actor.Kind != "anonymous",
		Kind:          actor.Kind,
		SubjectID:     actor.SubjectID,
		Username:      actor.Username,
		IsAdmin:       actor.IsAdmin,
		ClientIDs:     append([]int(nil), actor.ClientIDs...),
		Roles:         append([]string(nil), actor.Roles...),
		Permissions:   append([]string(nil), actor.Permissions...),
	}
	if len(actor.Attributes) > 0 {
		principal.Attributes = make(map[string]string, len(actor.Attributes))
		for key, value := range actor.Attributes {
			principal.Attributes[key] = value
		}
	}
	return principal
}

func normalizePrincipalWithAuthorization(principal webservice.Principal, authz webservice.AuthorizationService) webservice.Principal {
	if isNilAppServiceValue(authz) {
		authz = webservice.DefaultAuthorizationService{}
	}
	return authz.NormalizePrincipal(principal)
}

func normalizeActorWithAuthorization(actor *Actor, authz webservice.AuthorizationService) (*Actor, webservice.Principal) {
	principal := normalizePrincipalWithAuthorization(PrincipalFromActor(actor), authz)
	normalized := ActorFromPrincipal(principal)
	if normalized == nil {
		normalized = AnonymousActor()
		principal = PrincipalFromActor(normalized)
	}
	return normalized, principal
}

func ActorFromPrincipal(principal webservice.Principal) *Actor {
	if !principal.Authenticated {
		return AnonymousActor()
	}
	actor := &Actor{
		Kind:        strings.TrimSpace(principal.Kind),
		SubjectID:   strings.TrimSpace(principal.SubjectID),
		Username:    strings.TrimSpace(principal.Username),
		IsAdmin:     principal.IsAdmin,
		ClientIDs:   append([]int(nil), principal.ClientIDs...),
		Roles:       append([]string(nil), principal.Roles...),
		Permissions: append([]string(nil), principal.Permissions...),
	}
	if len(principal.Attributes) > 0 {
		actor.Attributes = make(map[string]string, len(principal.Attributes))
		for key, value := range principal.Attributes {
			actor.Attributes[key] = value
		}
	}
	if actor.Kind == "" {
		if actor.IsAdmin {
			actor.Kind = "admin"
		} else if hasActorRole(actor.Roles, webservice.RoleClient) {
			actor.Kind = "client"
		} else {
			actor.Kind = "user"
		}
	}
	return actor
}

func ActorCanAccessClient(actor *Actor, clientID int) bool {
	if actor == nil || clientID <= 0 {
		return false
	}
	if actor.IsAdmin {
		return true
	}
	for _, current := range actor.ClientIDs {
		if current == clientID {
			return true
		}
	}
	return false
}

func ActorPrimaryClientID(actor *Actor) (int, bool) {
	if actor == nil {
		return 0, false
	}
	for _, clientID := range actor.ClientIDs {
		if clientID > 0 {
			return clientID, true
		}
	}
	return 0, false
}

func hasActorRole(roles []string, target string) bool {
	for _, role := range roles {
		if strings.TrimSpace(role) == target {
			return true
		}
	}
	return false
}

func ActorAttribute(actor *Actor, key string) string {
	if actor == nil || len(actor.Attributes) == 0 {
		return ""
	}
	return strings.TrimSpace(actor.Attributes[strings.TrimSpace(key)])
}

func ActorAttributeInt(actor *Actor, key string) (int, bool) {
	value := ActorAttribute(actor, key)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

func NodeActorSourceType(actor *Actor) string {
	if actor == nil {
		return ""
	}
	switch strings.TrimSpace(actor.Kind) {
	case "platform_admin", "platform_user":
		return strings.TrimSpace(actor.Kind)
	case "admin":
		return "node_admin"
	case "user":
		return "node_user"
	default:
		return strings.TrimSpace(actor.Kind)
	}
}

func NodeActorPlatformID(actor *Actor) string {
	return ActorAttribute(actor, "platform_id")
}

func NodeActorID(actor *Actor) string {
	if actor == nil {
		return ""
	}
	if actorID := ActorAttribute(actor, "platform_actor_id"); actorID != "" {
		return actorID
	}
	return strings.TrimSpace(actor.Username)
}

func NodeActorSubjectID(actor *Actor) string {
	if actor == nil {
		return ""
	}
	return strings.TrimSpace(actor.SubjectID)
}

func NodeActorUserID(actor *Actor) int {
	value, _ := ActorAttributeInt(actor, "user_id")
	return value
}

func NodeActorServiceUserID(actor *Actor) int {
	value, _ := ActorAttributeInt(actor, "platform_service_user_id")
	return value
}

func NodeOperationActorPayload(actor *Actor) webservice.NodeOperationActorPayload {
	if actor == nil {
		return webservice.NodeOperationActorPayload{}
	}
	return webservice.NodeOperationActorPayload{
		Kind:       strings.TrimSpace(actor.Kind),
		SubjectID:  strings.TrimSpace(actor.SubjectID),
		Username:   strings.TrimSpace(actor.Username),
		PlatformID: NodeActorPlatformID(actor),
	}
}

func NodeOperationScope(actor *Actor) string {
	if actor == nil {
		return ""
	}
	if actor.IsAdmin {
		return "full"
	}
	switch strings.TrimSpace(actor.Kind) {
	case "platform_admin":
		return "account"
	case "platform_user", "user":
		return "user"
	default:
		return ""
	}
}

func (a *App) nodeClientVisibility(actor *Actor, scope webservice.NodeAccessScope) (webservice.ClientVisibility, bool, error) {
	if scope.IsFullAccess() {
		return webservice.ClientVisibility{IsAdmin: true}, true, nil
	}
	if clientIDs := normalizedActorClientIDs(actor); len(clientIDs) > 0 {
		return nodeClientVisibilityFromIDs(clientIDs), true, nil
	}
	switch strings.TrimSpace(actorKind(actor)) {
	case "platform_admin":
		serviceUserID := NodeActorServiceUserID(actor)
		if serviceUserID <= 0 {
			return webservice.ClientVisibility{}, false, nil
		}
		clientIDs, err := a.managementPlatforms().OwnedClientIDs(serviceUserID)
		if err != nil {
			return webservice.ClientVisibility{}, false, err
		}
		return nodeClientVisibilityFromIDs(clientIDs), true, nil
	case "user":
		userID := NodeActorUserID(actor)
		if userID <= 0 {
			return webservice.ClientVisibility{}, false, nil
		}
		clientIDs, err := webservice.ManagedClientIDsByUser(a.backend().Repository, userID)
		if err != nil {
			return webservice.ClientVisibility{}, false, err
		}
		return nodeClientVisibilityFromIDs(clientIDs), true, nil
	default:
		return webservice.ClientVisibility{}, false, nil
	}
}

func actorKind(actor *Actor) string {
	if actor == nil {
		return ""
	}
	return actor.Kind
}

func nodeClientVisibilityFromIDs(clientIDs []int) webservice.ClientVisibility {
	clientIDs = normalizeSortedClientIDs(clientIDs)
	if len(clientIDs) == 0 {
		return webservice.ClientVisibility{}
	}
	return webservice.ClientVisibility{
		PrimaryClientID: clientIDs[0],
		ClientIDs:       clientIDs,
	}
}

func normalizeSortedClientIDs(clientIDs []int) []int {
	switch len(clientIDs) {
	case 0:
		return nil
	case 1:
		if clientIDs[0] <= 0 {
			return nil
		}
		return []int{clientIDs[0]}
	}
	seen := make(map[int]struct{}, len(clientIDs))
	normalized := make([]int, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		if clientID <= 0 {
			continue
		}
		if _, ok := seen[clientID]; ok {
			continue
		}
		seen[clientID] = struct{}{}
		normalized = append(normalized, clientID)
	}
	if len(normalized) == 0 {
		return nil
	}
	sort.Ints(normalized)
	return normalized
}

func normalizedActorClientIDs(actor *Actor) []int {
	if actor == nil || len(actor.ClientIDs) == 0 {
		return nil
	}
	return normalizeSortedClientIDs(actor.ClientIDs)
}

func (a *App) nodeActorAllows(actor *Actor, permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return true
	}
	authz := a.authorization()
	_, principal := normalizeActorWithAuthorization(actor, authz)
	return authz.Allows(principal, permission)
}

func ActorSingleClientID(actor *Actor) (int, bool) {
	if actor == nil {
		return 0, false
	}
	clientID := 0
	count := 0
	for _, current := range actor.ClientIDs {
		if current <= 0 {
			continue
		}
		if clientID == 0 {
			clientID = current
			count = 1
			continue
		}
		if current != clientID {
			return 0, false
		}
	}
	if count == 1 {
		return clientID, true
	}
	return 0, false
}
