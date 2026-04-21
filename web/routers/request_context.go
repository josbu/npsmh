package routers

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

const (
	apiContextContextKey             = "nps.api.context"
	actorContextKey                  = "nps.api.actor"
	metadataContextKey               = "nps.api.metadata"
	requestIdentityContextKey        = "nps.api.request_identity"
	requestIdentityAppliedContextKey = "nps.api.request_identity_applied"
	sessionInvalidatedContextKey     = "nps.api.session_invalidated"
)

type ginAPIContext struct {
	gin *gin.Context
}

type resolvedActorIdentity struct {
	identity *webservice.SessionIdentity
	actor    *webapi.Actor
}

type requestAuthFailure struct {
	status  int
	code    string
	message string
}

var errManagementBasicAuthUnsupported = errors.New("basic authorization is not supported")

func newAPIContext(g *gin.Context) webapi.Context {
	return requestAPIContext(g)
}

func requestAPIContext(g *gin.Context) *ginAPIContext {
	if g == nil {
		return &ginAPIContext{}
	}
	if existing, ok := g.Get(apiContextContextKey); ok {
		if ctx, ok := existing.(*ginAPIContext); ok && ctx != nil {
			return ctx
		}
	}
	ctx := &ginAPIContext{gin: g}
	g.Set(apiContextContextKey, ctx)
	return ctx
}

func (c *ginAPIContext) BaseContext() context.Context {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return context.Background()
	}
	return c.gin.Request.Context()
}

func (c *ginAPIContext) String(key string) string {
	if c == nil || c.gin == nil {
		return ""
	}
	if value, ok := framework.RequestParam(c.gin, key); ok {
		return value
	}
	if value := c.gin.Param(key); value != "" {
		return value
	}
	if c.gin.Request == nil || c.gin.Request.URL == nil {
		return ""
	}
	values, ok := c.gin.Request.URL.Query()[key]
	if !ok || len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func (c *ginAPIContext) LookupString(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" || c == nil || c.gin == nil {
		return "", false
	}
	if value, ok := framework.RequestParam(c.gin, key); ok {
		return value, true
	}
	if value := c.gin.Param(key); value != "" {
		return value, true
	}
	if c.gin.Request == nil || c.gin.Request.URL == nil {
		return "", false
	}
	if values, ok := c.gin.Request.URL.Query()[key]; ok {
		if len(values) == 0 {
			return "", true
		}
		return values[len(values)-1], true
	}
	return "", false
}

func (c *ginAPIContext) Int(key string, def ...int) int {
	value := strings.TrimSpace(c.String(key))
	if value == "" {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func (c *ginAPIContext) Bool(key string, def ...bool) bool {
	value := strings.TrimSpace(c.String(key))
	if value == "" {
		if len(def) > 0 {
			return def[0]
		}
		return false
	}
	parsed, _ := strconv.ParseBool(value)
	return parsed
}

func (c *ginAPIContext) Method() string {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return http.MethodGet
	}
	return c.gin.Request.Method
}

func (c *ginAPIContext) Host() string {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return ""
	}
	return c.gin.Request.Host
}

func (c *ginAPIContext) RemoteAddr() string {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return ""
	}
	return c.gin.Request.RemoteAddr
}

func (c *ginAPIContext) ClientIP() string {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return ""
	}
	return c.gin.ClientIP()
}

func (c *ginAPIContext) RequestHeader(key string) string {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return ""
	}
	return c.gin.GetHeader(key)
}

func (c *ginAPIContext) RawBody() []byte {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return nil
	}
	return framework.RequestRawBody(c.gin)
}

func (c *ginAPIContext) RawBodyView() []byte {
	if c == nil || c.gin == nil || c.gin.Request == nil {
		return nil
	}
	return framework.RequestRawBodyView(c.gin)
}

func (c *ginAPIContext) SessionValue(key string) interface{} {
	if c == nil || c.gin == nil {
		return nil
	}
	return framework.SessionValue(c.gin, key)
}

func (c *ginAPIContext) SetSessionValue(key string, value interface{}) {
	if c == nil || c.gin == nil {
		return
	}
	_ = framework.SetSessionValue(c.gin, key, value)
}

func (c *ginAPIContext) DeleteSessionValue(key string) {
	if c == nil || c.gin == nil {
		return
	}
	_ = framework.DeleteSessionValue(c.gin, key)
}

func (c *ginAPIContext) MutateSession(fn func(webapi.SessionEditor)) {
	if c == nil || c.gin == nil || fn == nil {
		return
	}
	_ = framework.MutateSession(c.gin, func(editor framework.SessionEditor) {
		fn(editor)
	})
}

func (c *ginAPIContext) SetParam(key, value string) {
	if c == nil || c.gin == nil {
		return
	}
	framework.SetRequestParam(c.gin, key, value)
	if strings.EqualFold(strings.TrimSpace(key), "client_id") {
		if clientID, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && clientID > 0 {
			framework.SetRequestData(c.gin, "client_id", clientID)
		}
	}
}

func (c *ginAPIContext) RespondJSON(status int, value interface{}) {
	if c == nil || c.gin == nil {
		return
	}
	c.gin.JSON(status, value)
}

func (c *ginAPIContext) RespondString(status int, body string) {
	if c == nil || c.gin == nil {
		return
	}
	c.gin.String(status, body)
}

func (c *ginAPIContext) RespondData(status int, contentType string, data []byte) {
	if c == nil || c.gin == nil {
		return
	}
	c.gin.Data(status, contentType, data)
}

func (c *ginAPIContext) Redirect(status int, location string) {
	if c == nil || c.gin == nil {
		return
	}
	c.gin.Redirect(status, location)
}

func (c *ginAPIContext) SetResponseHeader(key, value string) {
	if c == nil || c.gin == nil || c.gin.Writer == nil {
		return
	}
	c.gin.Writer.Header().Set(key, value)
}

func (c *ginAPIContext) IsWritten() bool {
	if c == nil || c.gin == nil || c.gin.Writer == nil {
		return false
	}
	return c.gin.Writer.Written()
}

func (c *ginAPIContext) Actor() *webapi.Actor {
	if c == nil || c.gin == nil {
		return nil
	}
	return currentActor(c.gin)
}

func (c *ginAPIContext) SetActor(actor *webapi.Actor) {
	if c == nil || c.gin == nil {
		return
	}
	setActor(c.gin, actor)
}

func (c *ginAPIContext) Metadata() webapi.RequestMetadata {
	if c == nil || c.gin == nil {
		return webapi.RequestMetadata{}
	}
	return currentRequestMetadata(c.gin)
}

func attachRequestMetadata(c *gin.Context, app *webapi.App) {
	if app == nil {
		return
	}
	requestID := resolveRequestID(
		c.GetHeader("X-Request-ID"),
		c.GetHeader("X-Correlation-ID"),
		c.Query("request_id"),
		c.Query("requestId"),
	)
	source := strings.TrimSpace(c.GetHeader("X-NPS-Source"))
	if source == "" {
		source = strings.TrimSpace(c.GetHeader("User-Agent"))
	}
	setRequestMetadata(c, webapi.RequestMetadata{
		NodeID:    app.NodeID,
		RequestID: requestID,
		Source:    source,
	})
}

func setRequestMetadata(c *gin.Context, metadata webapi.RequestMetadata) {
	c.Set(metadataContextKey, metadata)
	if metadata.RequestID != "" {
		c.Writer.Header().Set("X-Request-ID", metadata.RequestID)
	}
}

func currentRequestMetadata(c *gin.Context) webapi.RequestMetadata {
	if v, ok := c.Get(metadataContextKey); ok {
		if metadata, ok := v.(webapi.RequestMetadata); ok {
			return metadata
		}
	}
	return webapi.RequestMetadata{}
}

func resolveRequestID(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return crypt.GetUUID().String()
}

func actorFromSession(c *gin.Context, resolver webservice.PermissionResolver, fallbackAdminUsername string) *webapi.Actor {
	if identity := sessionIdentityFromSession(c, resolver); identity != nil && identity.Authenticated {
		return webapi.ActorFromSessionIdentityWithFallback(identity, fallbackAdminUsername)
	}
	return webapi.AnonymousActor()
}

func sessionIdentityFromSession(c *gin.Context, resolver webservice.PermissionResolver) *webservice.SessionIdentity {
	raw, _ := framework.SessionValue(c, webservice.SessionIdentityKey).(string)
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, resolver)
	if err == nil && identity != nil {
		return identity
	}
	return nil
}

func actorIdentityFromSessionIdentity(state *State, identity *webservice.SessionIdentity) *resolvedActorIdentity {
	if identity == nil || !identity.Authenticated {
		return nil
	}
	return &resolvedActorIdentity{
		identity: identity,
		actor:    webapi.ActorFromSessionIdentityWithFallback(identity, state.AdminUsername()),
	}
}

func currentResolvedRequestIdentity(c *gin.Context, state *State) *resolvedActorIdentity {
	return actorIdentityFromSessionIdentity(state, currentRequestIdentity(c))
}

func resolveStandaloneTokenActorIdentity(state *State, token string, now time.Time) (*resolvedActorIdentity, error) {
	identity, err := webservice.ResolveStandaloneTokenIdentity(
		state.CurrentConfig(),
		state.PermissionResolver(),
		state.backend().Repository,
		token,
		now,
	)
	if err != nil {
		return nil, err
	}
	return actorIdentityFromSessionIdentity(state, identity), nil
}

func refreshSessionActorIdentity(state *State, identity *webservice.SessionIdentity, now time.Time) (*resolvedActorIdentity, error) {
	if identity == nil || !identity.Authenticated {
		return nil, nil
	}
	refreshed, err := webservice.RefreshSessionIdentity(identity, state.PermissionResolver(), state.backend().Repository, now)
	if err != nil {
		return nil, err
	}
	return actorIdentityFromSessionIdentity(state, refreshed), nil
}

func registerManagementAccessForResolvedActor(c *gin.Context, state *State, resolved *resolvedActorIdentity) {
	if c == nil || state == nil || resolved == nil || resolved.identity == nil || !resolved.identity.Authenticated {
		return
	}
	state.System().RegisterManagementAccess(resolvedManagementClientIP(requestAPIContext(c), state))
}

func setActor(c *gin.Context, actor *webapi.Actor) {
	if actor == nil {
		actor = webapi.AnonymousActor()
	}
	c.Set(actorContextKey, actor)
}

func setRequestIdentity(c *gin.Context, identity *webservice.SessionIdentity) {
	if c == nil {
		return
	}
	c.Set(requestIdentityAppliedContextKey, true)
	if identity == nil {
		c.Set(requestIdentityContextKey, (*webservice.SessionIdentity)(nil))
		return
	}
	c.Set(requestIdentityContextKey, identity.Normalize())
}

func currentRequestIdentity(c *gin.Context) *webservice.SessionIdentity {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(requestIdentityContextKey); ok {
		if identity, ok := v.(*webservice.SessionIdentity); ok {
			return identity
		}
	}
	return nil
}

func hasRequestIdentity(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(requestIdentityAppliedContextKey); ok {
		if applied, ok := v.(bool); ok {
			return applied
		}
	}
	return false
}

func currentActor(c *gin.Context) *webapi.Actor {
	if v, ok := c.Get(actorContextKey); ok {
		if actor, ok := v.(*webapi.Actor); ok && actor != nil {
			return actor
		}
	}
	return webapi.AnonymousActor()
}

func platformOwnedClientIDs(state *State, serviceUserID int) ([]int, error) {
	if state == nil {
		return nil, nil
	}
	return state.ManagementPlatforms().OwnedClientIDs(serviceUserID)
}

func filterClientIDs(requested, allowed []int) []int {
	if len(requested) == 0 || len(allowed) == 0 {
		return nil
	}
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, clientID := range allowed {
		if clientID > 0 {
			allowedSet[clientID] = struct{}{}
		}
	}
	filtered := make([]int, 0, len(requested))
	seen := make(map[int]struct{}, len(requested))
	for _, clientID := range requested {
		if clientID <= 0 {
			continue
		}
		if _, ok := allowedSet[clientID]; !ok {
			continue
		}
		if _, ok := seen[clientID]; ok {
			continue
		}
		seen[clientID] = struct{}{}
		filtered = append(filtered, clientID)
	}
	sort.Ints(filtered)
	return filtered
}

func platformActorFromLookup(platform servercfg.ManagementPlatformConfig, serviceUserID int, ownedClientIDs func() ([]int, error), lookup func(string) string) (*webapi.Actor, error) {
	role, explicitRole := platformRoleFromLookup(lookup)
	switch {
	case !explicitRole && platformLookupHasDelegatedContext(lookup):
		role = "user"
	case !explicitRole:
		role = defaultPlatformActorRole(platform)
	}
	username := platformUsernameFromLookup(lookup)
	actorID := platformActorIDFromLookup(lookup, username)
	fullControl := strings.EqualFold(strings.TrimSpace(platform.ControlScope), "full")
	requestedClientIDs := platformClientIDsFromLookup(lookup)
	clientIDs, err := resolvePlatformActorClientIDs(platform, role, requestedClientIDs, ownedClientIDs)
	if err != nil {
		return nil, err
	}
	if role == "admin" {
		if fullControl {
			return webapi.PlatformActor(platform.PlatformID, role, username, actorID, true, nil, serviceUserID), nil
		}
		return webapi.PlatformActor(platform.PlatformID, role, username, actorID, false, clientIDs, serviceUserID), nil
	}
	return webapi.PlatformActor(platform.PlatformID, "user", username, actorID, false, clientIDs, serviceUserID), nil
}

func fixedPlatformAdminActor(platform servercfg.ManagementPlatformConfig, serviceUserID int, username, actorID string, ownedClientIDs func() ([]int, error)) (*webapi.Actor, error) {
	fullControl := strings.EqualFold(strings.TrimSpace(platform.ControlScope), "full")
	clientIDs, err := resolvePlatformActorClientIDs(platform, "admin", nil, ownedClientIDs)
	if err != nil {
		return nil, err
	}
	if fullControl {
		clientIDs = nil
	}
	return webapi.PlatformActor(platform.PlatformID, "admin", username, actorID, fullControl, clientIDs, serviceUserID), nil
}

func resolvePlatformActorClientIDs(platform servercfg.ManagementPlatformConfig, role string, requestedClientIDs []int, ownedClientIDs func() ([]int, error)) ([]int, error) {
	role = normalizePlatformRole(role)
	if role == "admin" {
		if strings.EqualFold(strings.TrimSpace(platform.ControlScope), "full") {
			return nil, nil
		}
		if ownedClientIDs == nil {
			return nil, nil
		}
		return ownedClientIDs()
	}
	if len(requestedClientIDs) == 0 || ownedClientIDs == nil {
		return nil, nil
	}
	allowed, err := ownedClientIDs()
	if err != nil {
		return nil, err
	}
	return filterClientIDs(requestedClientIDs, allowed), nil
}

func nodePlatformLookup(c *gin.Context) func(string) string {
	if c == nil {
		return func(string) string { return "" }
	}
	return func(key string) string {
		switch key {
		case "X-Platform-Role", "X-Master-Role", "X-Platform-Username", "X-Master-Username", "X-Platform-Actor-ID", "X-Master-Actor-ID", "X-Platform-Client-IDs", "X-Master-Client-IDs":
			return c.GetHeader(key)
		case "platform_role", "role", "platform_username", "username", "platform_actor_id", "actor_id", "platform_client_ids", "client_ids":
			return nodeRequestValue(c, key)
		default:
			return ""
		}
	}
}

var nodePlatformLookupKeys = []string{
	"X-Platform-Role",
	"X-Master-Role",
	"X-Platform-Username",
	"X-Master-Username",
	"X-Platform-Actor-ID",
	"X-Master-Actor-ID",
	"X-Platform-Client-IDs",
	"X-Master-Client-IDs",
	"platform_role",
	"role",
	"platform_username",
	"username",
	"platform_actor_id",
	"actor_id",
	"platform_client_ids",
	"client_ids",
}

func snapshotNodePlatformLookup(c *gin.Context) func(string) string {
	if c == nil {
		return func(string) string { return "" }
	}
	lookup := nodePlatformLookup(c)
	values := make(map[string]string, len(nodePlatformLookupKeys))
	for _, key := range nodePlatformLookupKeys {
		if value := strings.TrimSpace(lookup(key)); value != "" {
			values[key] = value
		}
	}
	return func(key string) string {
		return values[strings.TrimSpace(key)]
	}
}

func platformRoleFromLookup(lookup func(string) string) (string, bool) {
	if lookup == nil {
		return normalizePlatformRole(""), false
	}
	raw := firstNonEmpty(
		lookup("X-Platform-Role"),
		lookup("X-Master-Role"),
		lookup("platform_role"),
		lookup("role"),
	)
	return normalizePlatformRole(raw), strings.TrimSpace(raw) != ""
}

func platformLookupHasDelegatedContext(lookup func(string) string) bool {
	if lookup == nil {
		return false
	}
	return strings.TrimSpace(firstNonEmpty(
		lookup("X-Platform-Actor-ID"),
		lookup("X-Master-Actor-ID"),
		lookup("platform_actor_id"),
		lookup("actor_id"),
		lookup("X-Platform-Client-IDs"),
		lookup("X-Master-Client-IDs"),
		lookup("platform_client_ids"),
		lookup("client_ids"),
	)) != ""
}

func normalizePlatformRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return "user"
	}
	if role != "admin" {
		return "user"
	}
	return role
}

func defaultPlatformActorRole(platform servercfg.ManagementPlatformConfig) string {
	return "admin"
}

func platformUsernameFromLookup(lookup func(string) string) string {
	if lookup == nil {
		return normalizePlatformUsername("")
	}
	return normalizePlatformUsername(firstNonEmpty(
		lookup("X-Platform-Username"),
		lookup("X-Master-Username"),
		lookup("platform_username"),
		lookup("username"),
	))
}

func normalizePlatformUsername(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return "platform"
	}
	return username
}

func platformActorIDFromLookup(lookup func(string) string, fallbackUsername string) string {
	if lookup == nil {
		return normalizePlatformActorID("", fallbackUsername)
	}
	return normalizePlatformActorID(firstNonEmpty(
		lookup("X-Platform-Actor-ID"),
		lookup("X-Master-Actor-ID"),
		lookup("platform_actor_id"),
		lookup("actor_id"),
	), fallbackUsername)
}

func normalizePlatformActorID(actorID, fallbackUsername string) string {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return strings.TrimSpace(fallbackUsername)
	}
	return actorID
}

func platformClientIDsFromLookup(lookup func(string) string) []int {
	if lookup == nil {
		return nil
	}
	raw := strings.TrimSpace(firstNonEmpty(
		lookup("X-Platform-Client-IDs"),
		lookup("X-Master-Client-IDs"),
		lookup("platform_client_ids"),
		lookup("client_ids"),
	))
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';' || r == ' ' || r == '[' || r == ']'
	})
	clientIDs := make([]int, 0, len(parts))
	seen := map[int]struct{}{}
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		clientID, err := strconv.Atoi(part)
		if err != nil || clientID <= 0 {
			continue
		}
		if _, ok := seen[clientID]; ok {
			continue
		}
		seen[clientID] = struct{}{}
		clientIDs = append(clientIDs, clientID)
	}
	sort.Ints(clientIDs)
	return clientIDs
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func markSessionInvalidated(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(sessionInvalidatedContextKey, true)
}

func sessionInvalidated(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(sessionInvalidatedContextKey); ok {
		if invalidated, ok := v.(bool); ok {
			return invalidated
		}
	}
	return false
}

func requestRemoteIP(c interface {
	RemoteAddr() string
	RequestHeader(string) string
}, allowXRealIP bool, trustedProxyIPs string, httpOnlyPass string) string {
	if c == nil {
		return ""
	}
	ip := splitRemoteHost(c.RemoteAddr())
	if ip == "" {
		return ""
	}
	if (allowXRealIP && common.IsTrustedProxy(trustedProxyIPs, ip)) ||
		(strings.TrimSpace(httpOnlyPass) != "" && c.RequestHeader("X-NPS-Http-Only") == httpOnlyPass) {
		if forwarded := firstTrustedForwardedIP(
			c.RequestHeader("X-Forwarded-For"),
			c.RequestHeader("X-Real-IP"),
		); forwarded != "" {
			return forwarded
		}
	}
	return ip
}

func resolvedManagementClientIP(c interface {
	RemoteAddr() string
	RequestHeader(string) string
}, state *State) string {
	if state == nil {
		return splitRemoteHost(c.RemoteAddr())
	}
	cfg := state.CurrentConfig()
	if cfg == nil {
		return splitRemoteHost(c.RemoteAddr())
	}
	return requestRemoteIP(c, cfg.Auth.AllowXRealIP, cfg.Auth.TrustedProxyIPs, cfg.Auth.HTTPOnlyPass)
}

func authenticateRequest(c *gin.Context, state *State) bool {
	if c == nil || state == nil {
		return false
	}
	authHeader := c.GetHeader("Authorization")
	if token := standaloneTokenFromRequest(c); token != "" {
		resolved, err := resolveStandaloneTokenActorIdentity(state, token, common.TimeNow())
		if err != nil {
			abortRequestAuthError(c, err)
			return true
		}
		applyRequestIdentity(c, state, resolved)
		return false
	}
	if webservice.HasAuthorizationScheme(authHeader, "basic") {
		abortRequestAuthError(c, errManagementBasicAuthUnsupported)
		return true
	}
	return false
}

func applyRequestIdentity(c *gin.Context, state *State, resolved *resolvedActorIdentity) {
	if c == nil || resolved == nil || resolved.identity == nil {
		return
	}
	setRequestIdentity(c, resolved.identity)
	setActor(c, resolved.actor)
	registerManagementAccessForResolvedActor(c, state, resolved)
}

func standaloneTokenFromRequest(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if token, ok := webservice.ParseBearerAuthorizationHeader(c.GetHeader("Authorization")); ok && webservice.IsStandaloneToken(token) {
		return token
	}
	if token := standaloneTokenFromWebSocketProtocol(c.Request); token != "" {
		return token
	}
	return ""
}

func standaloneTokenFromWebSocketProtocol(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, raw := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(raw, ",") {
			token := strings.TrimSpace(part)
			if webservice.IsStandaloneToken(token) {
				return token
			}
		}
	}
	return ""
}

func abortRequestAuthError(c *gin.Context, err error) {
	if c == nil {
		return
	}
	detail := requestAuthFailureDetail(err)
	if isFormalManagementAPIRequest(c.Request.URL.Path) {
		c.AbortWithStatusJSON(detail.status, webapi.ManagementErrorResponse{
			Error: webapi.ManagementErrorDetail{
				Code:    detail.code,
				Message: detail.message,
			},
		})
		return
	}
	c.AbortWithStatus(detail.status)
}

func requestAuthFailureDetail(err error) requestAuthFailure {
	detail := requestAuthFailure{
		status:  http.StatusInternalServerError,
		code:    "request_failed",
		message: "internal server error",
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		detail.message = strings.TrimSpace(err.Error())
	}
	switch {
	case errors.Is(err, errManagementBasicAuthUnsupported):
		detail.status = http.StatusUnauthorized
		detail.code = "unsupported_auth_scheme"
		detail.message = err.Error()
	case errors.Is(err, webservice.ErrInvalidCredentials):
		detail.status = http.StatusUnauthorized
		detail.code = "invalid_credentials"
		detail.message = "invalid credentials"
	case errors.Is(err, webservice.ErrForbidden):
		detail.status = http.StatusForbidden
		detail.code = "forbidden"
		detail.message = "forbidden"
	case errors.Is(err, webservice.ErrUnauthenticated):
		detail.status = http.StatusUnauthorized
		detail.code = "unauthorized"
		detail.message = "unauthorized"
	case errors.Is(err, webservice.ErrStandaloneTokenExpired):
		detail.status = http.StatusUnauthorized
		detail.code = "standalone_token_expired"
		detail.message = err.Error()
	case errors.Is(err, webservice.ErrStandaloneTokenInvalid):
		detail.status = http.StatusUnauthorized
		detail.code = "standalone_token_invalid"
		detail.message = err.Error()
	case errors.Is(err, webservice.ErrStandaloneTokenUnavailable):
		detail.status = http.StatusServiceUnavailable
		detail.code = "standalone_token_unavailable"
		detail.message = err.Error()
	}
	return detail
}

func splitRemoteHost(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func firstTrustedForwardedIP(values ...string) string {
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			if ip := common.GetIpByAddr(strings.TrimSpace(part)); ip != "" {
				return ip
			}
		}
	}
	return ""
}
