package api

import (
	"errors"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
)

type ManagementAuthSessionPayload struct {
	Session ManagementSession `json:"session"`
	Actor   *Actor            `json:"actor,omitempty"`
}

type ManagementAuthTokenPayload struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	IssuedAt    int64  `json:"issued_at"`
	ExpiresAt   int64  `json:"expires_at"`
	ExpiresIn   int64  `json:"expires_in"`
}

type ManagementAuthRegisterPayload struct {
	SubjectID string `json:"subject_id"`
	Username  string `json:"username"`
	ClientIDs []int  `json:"client_ids,omitempty"`
}

type ManagementAuthCredentialRequest struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	TOTP          string `json:"totp,omitempty"`
	VerifyKey     string `json:"verify_key,omitempty"`
	CaptchaID     string `json:"captcha_id,omitempty"`
	CaptchaAnswer string `json:"captcha_answer,omitempty"`
	PoWNonce      string `json:"pow_nonce,omitempty"`
	PoWBits       int    `json:"pow_bits,omitempty"`
}

type ManagementAuthRegisterRequest struct {
	Username      string `json:"username"`
	Password      string `json:"password"`
	CaptchaID     string `json:"captcha_id,omitempty"`
	CaptchaAnswer string `json:"captcha_answer,omitempty"`
}

type ManagementAccessIPLimitRegisterRequest struct {
	VerifyKey string `json:"verify_key"`
}

type ManagementAccessIPLimitRegisterPayload struct {
	Registered bool `json:"registered"`
	ClientID   int  `json:"client_id,omitempty"`
}

type ManagementDiscoveryPayload struct {
	App        ManagementApp        `json:"app"`
	Session    ManagementSession    `json:"session"`
	Auth       ManagementAuth       `json:"auth"`
	Actor      *Actor               `json:"actor"`
	Actions    []ActionEntry        `json:"actions"`
	Features   ManagementFeatures   `json:"features"`
	Security   ManagementSecurity   `json:"security"`
	Routes     ManagementRoutes     `json:"routes"`
	Extensions ManagementExtensions `json:"extensions"`
}

type ManagementApp struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Year       int    `json:"year"`
	WebBaseURL string `json:"web_base_url"`
}

type ManagementSession struct {
	Authenticated bool   `json:"authenticated"`
	IsAdmin       bool   `json:"is_admin"`
	Username      string `json:"username,omitempty"`
	ClientID      *int   `json:"client_id"`
	ClientIDs     []int  `json:"client_ids,omitempty"`
	SubjectID     string `json:"subject_id,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Kind          string `json:"kind,omitempty"`
}

type ManagementAuth struct {
	LoginDelayMS         int  `json:"login_delay_ms,omitempty"`
	TOTPLen              int  `json:"totp_len,omitempty"`
	PoWEnabled           bool `json:"pow_enable"`
	AllowClientVKeyLogin bool `json:"allow_client_vkey_login"`
}

type ManagementFeatures struct {
	AllowUserLogin          bool `json:"allow_user_login"`
	AllowUserRegister       bool `json:"allow_user_register"`
	AllowFlowLimit          bool `json:"allow_flow_limit"`
	AllowRateLimit          bool `json:"allow_rate_limit"`
	AllowTimeLimit          bool `json:"allow_time_limit"`
	AllowConnectionNumLimit bool `json:"allow_connection_num_limit"`
	AllowMultiIP            bool `json:"allow_multi_ip"`
	AllowTunnelNumLimit     bool `json:"allow_tunnel_num_limit"`
	AllowLocalProxy         bool `json:"allow_local_proxy"`
	AllowUserLocal          bool `json:"allow_user_local"`
	AllowUserChangeUsername bool `json:"allow_user_change_username"`
	SystemInfoDisplay       bool `json:"system_info_display"`
	OpenCaptcha             bool `json:"open_captcha"`
}

type ManagementSecurity struct {
	SecureMode bool `json:"secure_mode"`
	ForcePoW   bool `json:"force_pow"`
	PoWBits    int  `json:"pow_bits"`
}

type ManagementRoutes struct {
	Base                  string `json:"base,omitempty"`
	APIBase               string `json:"api_base,omitempty"`
	Health                string `json:"health,omitempty"`
	Discovery             string `json:"discovery,omitempty"`
	Overview              string `json:"overview,omitempty"`
	Dashboard             string `json:"dashboard,omitempty"`
	Registration          string `json:"registration,omitempty"`
	Operations            string `json:"operations,omitempty"`
	SettingsGlobal        string `json:"settings_global,omitempty"`
	SecurityBans          string `json:"security_bans,omitempty"`
	Users                 string `json:"users,omitempty"`
	Clients               string `json:"clients,omitempty"`
	ClientsConnections    string `json:"clients_connections,omitempty"`
	ClientsQRCode         string `json:"clients_qrcode,omitempty"`
	ClientsClear          string `json:"clients_clear,omitempty"`
	Tunnels               string `json:"tunnels,omitempty"`
	Hosts                 string `json:"hosts,omitempty"`
	HostCertSuggestion    string `json:"host_cert_suggestion,omitempty"`
	SystemExport          string `json:"system_export,omitempty"`
	SystemImport          string `json:"system_import,omitempty"`
	Status                string `json:"status,omitempty"`
	Changes               string `json:"changes,omitempty"`
	CallbacksQueue        string `json:"callbacks_queue,omitempty"`
	CallbacksQueueReplay  string `json:"callbacks_queue_replay,omitempty"`
	CallbacksQueueClear   string `json:"callbacks_queue_clear,omitempty"`
	Webhooks              string `json:"webhooks,omitempty"`
	RealtimeSubscriptions string `json:"realtime_subscriptions,omitempty"`
	UsageSnapshot         string `json:"usage_snapshot,omitempty"`
	Batch                 string `json:"batch,omitempty"`
	Traffic               string `json:"traffic,omitempty"`
	ClientsKick           string `json:"clients_kick,omitempty"`
	SystemSync            string `json:"system_sync,omitempty"`
	WebSocket             string `json:"ws,omitempty"`
	Session               string `json:"session,omitempty"`
	CaptchaNew            string `json:"captcha_new,omitempty"`
	Token                 string `json:"token,omitempty"`
	Register              string `json:"register,omitempty"`
	Logout                string `json:"logout,omitempty"`
	AccessIPLimitRegister string `json:"access_ip_limit_register,omitempty"`
}

type ManagementHealthPayload struct {
	OK               bool   `json:"ok"`
	NodeID           string `json:"node_id,omitempty"`
	Version          string `json:"version,omitempty"`
	BootID           string `json:"boot_id,omitempty"`
	RuntimeStartedAt int64  `json:"runtime_started_at,omitempty"`
	ConfigEpoch      string `json:"config_epoch,omitempty"`
}

type ManagementExtensions struct {
	Authorization ManagementAuthorizationExtension `json:"authorization"`
	Cluster       ManagementClusterExtension       `json:"cluster"`
}

type ManagementAuthorizationExtension struct {
	Roles               []string            `json:"roles,omitempty"`
	Permissions         []string            `json:"permissions,omitempty"`
	ClientIDs           []int               `json:"client_ids,omitempty"`
	KnownRoles          []string            `json:"known_roles,omitempty"`
	KnownPermissions    []string            `json:"known_permissions,omitempty"`
	ManagementAdmin     string              `json:"management_admin"`
	ResourcePermissions map[string][]string `json:"resource_permissions,omitempty"`
}

type ManagementClusterExtension struct {
	NodeID              string                         `json:"node_id,omitempty"`
	BootID              string                         `json:"boot_id,omitempty"`
	RuntimeStartedAt    int64                          `json:"runtime_started_at,omitempty"`
	ConfigEpoch         string                         `json:"config_epoch,omitempty"`
	ResyncOnBootChange  bool                           `json:"resync_on_boot_change"`
	RunMode             string                         `json:"run_mode,omitempty"`
	EventsEnabled       bool                           `json:"events_enabled"`
	CallbacksReady      bool                           `json:"callbacks_ready"`
	SchemaVersion       int                            `json:"schema_version"`
	APIBase             string                         `json:"api_base,omitempty"`
	Capabilities        []string                       `json:"capabilities,omitempty"`
	Protocol            webservice.NodeProtocolPayload `json:"protocol"`
	LiveOnlyEvents      []string                       `json:"live_only_events,omitempty"`
	ManagementPlatforms []ManagementPlatformConfig     `json:"management_platforms,omitempty"`
}

type ManagementPlatformConfig struct {
	PlatformID              string `json:"platform_id"`
	MasterURL               string `json:"master_url,omitempty"`
	ControlScope            string `json:"control_scope"`
	ServiceUsername         string `json:"service_username,omitempty"`
	Enabled                 bool   `json:"enabled"`
	ConnectMode             string `json:"connect_mode,omitempty"`
	DirectEnabled           bool   `json:"direct_enabled,omitempty"`
	ReverseEnabled          bool   `json:"reverse_enabled,omitempty"`
	ReverseHeartbeatSeconds int    `json:"reverse_heartbeat_seconds,omitempty"`
	ReverseWSURL            string `json:"reverse_ws_url,omitempty"`
	CallbackEnabled         bool   `json:"callback_enabled,omitempty"`
	CallbackURL             string `json:"callback_url,omitempty"`
	CallbackTimeoutSeconds  int    `json:"callback_timeout_seconds,omitempty"`
	CallbackRetryMax        int    `json:"callback_retry_max,omitempty"`
	CallbackRetryBackoffSec int    `json:"callback_retry_backoff_seconds,omitempty"`
	CallbackQueueMax        int    `json:"callback_queue_max"`
	CallbackQueueSize       int    `json:"callback_queue_size"`
	CallbackDropped         int64  `json:"callback_dropped,omitempty"`
	CallbackSigningEnabled  bool   `json:"callback_signing_enabled,omitempty"`
	ReverseConnected        bool   `json:"reverse_connected,omitempty"`
	LastReverseConnectedAt  int64  `json:"last_reverse_connected_at,omitempty"`
	LastReverseHelloAt      int64  `json:"last_reverse_hello_at,omitempty"`
	LastReverseEventAt      int64  `json:"last_reverse_event_at,omitempty"`
	LastReversePingAt       int64  `json:"last_reverse_ping_at,omitempty"`
	LastReversePongAt       int64  `json:"last_reverse_pong_at,omitempty"`
	LastCallbackAt          int64  `json:"last_callback_at,omitempty"`
	LastCallbackSuccessAt   int64  `json:"last_callback_success_at,omitempty"`
	LastCallbackQueuedAt    int64  `json:"last_callback_queued_at,omitempty"`
	LastCallbackReplayAt    int64  `json:"last_callback_replay_at,omitempty"`
	LastCallbackStatusCode  int    `json:"last_callback_status_code,omitempty"`
	CallbackDeliveries      int64  `json:"callback_deliveries,omitempty"`
	CallbackFailures        int64  `json:"callback_failures,omitempty"`
	CallbackConsecutiveFail int64  `json:"callback_consecutive_failures,omitempty"`
	LastReverseError        string `json:"last_reverse_error,omitempty"`
	LastReverseErrorAt      int64  `json:"last_reverse_error_at,omitempty"`
	LastCallbackError       string `json:"last_callback_error,omitempty"`
	LastCallbackErrorAt     int64  `json:"last_callback_error_at,omitempty"`
	LastReverseDisconnectAt int64  `json:"last_reverse_disconnect_at,omitempty"`
}

type sessionBatchContext interface {
	MutateSession(func(SessionEditor))
}

type sessionEditorAdapter struct {
	set    func(string, interface{})
	delete func(string)
}

type sessionMapEditor struct {
	session map[string]interface{}
}

var legacySessionKeys = []string{
	"auth",
	"isAdmin",
	"username",
	"clientId",
	"clientIds",
}

func setManagementNoStoreHeaders(c Context) {
	if c == nil {
		return
	}
	c.SetResponseHeader("Cache-Control", "no-store")
	c.SetResponseHeader("Pragma", "no-cache")
}

func (a *App) SessionStatus(c Context) {
	setManagementNoStoreHeaders(c)
	state := a.managementContractState(c)
	respondManagementData(c, http.StatusOK, ManagementAuthSessionPayload{
		Session: state.session,
		Actor:   state.actor,
	}, managementResponseMeta(c, common.TimeNow().Unix(), strings.TrimSpace(a.runtimeIdentity().ConfigEpoch())))
}

func (a *App) ManagementHealth(c Context) {
	setManagementNoStoreHeaders(c)
	info := a.system().Info()
	runtimeIdentity := a.runtimeIdentity()
	payload := ManagementHealthPayload{
		OK:               true,
		NodeID:           strings.TrimSpace(a.NodeID),
		Version:          strings.TrimSpace(info.Version),
		BootID:           strings.TrimSpace(runtimeIdentity.BootID()),
		RuntimeStartedAt: runtimeIdentity.StartedAt(),
		ConfigEpoch:      strings.TrimSpace(runtimeIdentity.ConfigEpoch()),
	}
	respondManagementData(c, http.StatusOK, payload, managementResponseMeta(c, common.TimeNow().Unix(), payload.ConfigEpoch))
}

func (a *App) ManagementDiscovery(c Context) {
	setManagementNoStoreHeaders(c)
	payload := a.managementDiscoveryPayload(c)
	respondManagementData(c, 200, payload, managementResponseMeta(c, common.TimeNow().Unix(), payload.Extensions.Cluster.ConfigEpoch))
}

type managementContractState struct {
	cfg        *servercfg.Snapshot
	actor      *Actor
	authz      webservice.AuthorizationService
	app        ManagementApp
	session    ManagementSession
	actions    []ActionEntry
	features   ManagementFeatures
	security   ManagementSecurity
	extensions ManagementExtensions
}

func (a *App) managementDiscoveryPayload(c Context) ManagementDiscoveryPayload {
	state := a.managementContractState(c)
	return ManagementDiscoveryPayload{
		App:     state.app,
		Session: state.session,
		Auth: ManagementAuth{
			LoginDelayMS:         int(a.loginPolicy().Settings().LoginDelayMillis()),
			TOTPLen:              crypt.TotpLen,
			PoWEnabled:           state.cfg.Security.PoWBits > 0 && (state.cfg.Security.ForcePoW || state.cfg.Security.SecureMode),
			AllowClientVKeyLogin: state.cfg.Feature.AllowUserVkeyLogin,
		},
		Actor:      state.actor,
		Actions:    state.actions,
		Features:   state.features,
		Security:   state.security,
		Routes:     a.managementDiscoveryRoutes(state),
		Extensions: state.extensions,
	}
}

func (a *App) managementDiscoveryRoutes(state managementContractState) ManagementRoutes {
	baseURL := servercfg.NormalizeBaseURL(state.cfg.Web.BaseURL)
	routes := ManagementRoutes{
		Base:                  baseURL,
		APIBase:               joinBase(baseURL, "/api"),
		Health:                joinBase(baseURL, "/api/system/health"),
		Discovery:             joinBase(baseURL, "/api/system/discovery"),
		Session:               joinBase(baseURL, "/api/auth/session"),
		Token:                 joinBase(baseURL, "/api/auth/token"),
		Logout:                joinBase(baseURL, "/api/auth/session/logout"),
		AccessIPLimitRegister: joinBase(baseURL, "/api/access/ip-limit/actions/register"),
	}
	if state.features.OpenCaptcha {
		routes.CaptchaNew = joinBase(baseURL, "/captcha/new")
	}
	if state.features.AllowUserRegister {
		routes.Register = joinBase(baseURL, "/api/auth/register")
	}

	if !state.session.Authenticated {
		return routes
	}

	direct := NodeDirectRouteCatalog(baseURL)
	actionPaths := make(map[string]struct{}, len(state.actions))
	for _, action := range state.actions {
		actionPath := normalizeDiscoveryRoutePattern(action.Path)
		if actionPath == "" {
			continue
		}
		actionPaths[actionPath] = struct{}{}
	}
	visible := func(path string) bool {
		_, ok := actionPaths[normalizeDiscoveryRoutePattern(path)]
		return ok
	}
	applyVisibleManagementDiscoveryRoutes(&routes, direct, visible)
	return routes
}

func applyVisibleManagementDiscoveryRoutes(routes *ManagementRoutes, direct NodeRouteCatalog, visible func(string) bool) {
	if routes == nil {
		return
	}
	direct.ApplyDiscoveryRoutes(routes)
	if visible == nil {
		return
	}
	for _, binding := range []struct {
		path  string
		clear func(*ManagementRoutes)
	}{
		{path: direct.Overview, clear: func(routes *ManagementRoutes) { routes.Overview = "" }},
		{path: direct.Dashboard, clear: func(routes *ManagementRoutes) { routes.Dashboard = "" }},
		{path: direct.Registration, clear: func(routes *ManagementRoutes) { routes.Registration = "" }},
		{path: direct.Operations, clear: func(routes *ManagementRoutes) { routes.Operations = "" }},
		{path: direct.Global, clear: func(routes *ManagementRoutes) { routes.SettingsGlobal = "" }},
		{path: direct.BanList, clear: func(routes *ManagementRoutes) { routes.SecurityBans = "" }},
		{path: direct.Users, clear: func(routes *ManagementRoutes) { routes.Users = "" }},
		{path: direct.Clients, clear: func(routes *ManagementRoutes) { routes.Clients = "" }},
		{path: direct.ClientsConnections, clear: func(routes *ManagementRoutes) { routes.ClientsConnections = "" }},
		{path: direct.ClientsQR, clear: func(routes *ManagementRoutes) { routes.ClientsQRCode = "" }},
		{path: direct.ClientsClear, clear: func(routes *ManagementRoutes) { routes.ClientsClear = "" }},
		{path: direct.Tunnels, clear: func(routes *ManagementRoutes) { routes.Tunnels = "" }},
		{path: direct.Hosts, clear: func(routes *ManagementRoutes) { routes.Hosts = "" }},
		{path: direct.HostCertSuggestion, clear: func(routes *ManagementRoutes) { routes.HostCertSuggestion = "" }},
		{path: direct.Config, clear: func(routes *ManagementRoutes) { routes.SystemExport = "" }},
		{path: direct.ConfigImport, clear: func(routes *ManagementRoutes) { routes.SystemImport = "" }},
		{path: direct.Status, clear: func(routes *ManagementRoutes) { routes.Status = "" }},
		{path: direct.CallbackQueue, clear: func(routes *ManagementRoutes) { routes.CallbacksQueue = "" }},
		{path: direct.CallbackQueueReplay, clear: func(routes *ManagementRoutes) { routes.CallbacksQueueReplay = "" }},
		{path: direct.CallbackQueueClear, clear: func(routes *ManagementRoutes) { routes.CallbacksQueueClear = "" }},
		{path: direct.Webhooks, clear: func(routes *ManagementRoutes) { routes.Webhooks = "" }},
		{path: direct.UsageSnapshot, clear: func(routes *ManagementRoutes) { routes.UsageSnapshot = "" }},
		{path: direct.Traffic, clear: func(routes *ManagementRoutes) { routes.Traffic = "" }},
		{path: direct.Kick, clear: func(routes *ManagementRoutes) { routes.ClientsKick = "" }},
		{path: direct.Sync, clear: func(routes *ManagementRoutes) { routes.SystemSync = "" }},
	} {
		if visible(binding.path) {
			continue
		}
		binding.clear(routes)
	}
}

func normalizeDiscoveryRoutePattern(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	segments := strings.Split(path.Clean(raw), "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") && len(segment) > 1 {
			segments[index] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	normalized := strings.Join(segments, "/")
	if normalized == "." {
		return ""
	}
	if !strings.HasPrefix(normalized, "/") {
		return "/" + normalized
	}
	return normalized
}
func (a *App) managementContractState(c Context) managementContractState {

	cfg := a.currentConfig()
	info := a.system().Info()
	permissions := a.permissionResolver()
	authz := a.authorization()
	identity := currentSessionIdentityWithResolver(c, permissions)
	access := a.nodeActorAccessFromContext(c)
	actor := access.actor
	principal := access.principal
	authenticated := principal.Authenticated
	if !authenticated {
		actor = AnonymousActor()
		principal = PrincipalFromActor(actor)
	}

	runtimeIdentity := a.runtimeIdentity()
	nodeID := ""
	if a != nil {
		nodeID = a.NodeID
	}
	cluster := webservice.BuildNodeDescriptor(webservice.NodeDescriptorInput{
		NodeID:           nodeID,
		Config:           cfg,
		BootID:           runtimeIdentity.BootID(),
		RuntimeStartedAt: runtimeIdentity.StartedAt(),
		ConfigEpoch:      runtimeIdentity.ConfigEpoch(),
		LiveOnlyEvents:   NodeLiveOnlyEvents(),
	})

	state := managementContractState{
		cfg:   cfg,
		actor: actor,
		authz: authz,
		app: ManagementApp{
			Name:       cfg.App.Name,
			Version:    info.Version,
			Year:       info.Year,
			WebBaseURL: servercfg.NormalizeBaseURL(cfg.Web.BaseURL),
		},
		session: ManagementSession{
			Authenticated: authenticated,
			IsAdmin:       principal.IsAdmin,
			Username:      principal.Username,
			ClientID:      actorPrimaryClientID(actor),
			ClientIDs:     append([]int(nil), principal.ClientIDs...),
			SubjectID:     sessionIdentityValue(identity, actor, func(v *webservice.SessionIdentity) string { return v.SubjectID }, func(v *Actor) string { return v.SubjectID }),
			Provider:      identityValue(identity, func(v *webservice.SessionIdentity) string { return v.Provider }),
			Kind:          sessionIdentityValue(identity, actor, func(v *webservice.SessionIdentity) string { return v.Kind }, func(v *Actor) string { return v.Kind }),
		},
		actions: VisibleActionEntries(
			cfg,
			servercfg.NormalizeBaseURL(cfg.Web.BaseURL),
			actor,
			authz,
			ProtectedActionCatalog(a),
		),
		features: ManagementFeatures{
			AllowUserLogin:          cfg.Feature.AllowUserLogin,
			AllowUserRegister:       cfg.Feature.AllowUserRegister,
			AllowFlowLimit:          cfg.Feature.AllowFlowLimit,
			AllowRateLimit:          cfg.Feature.AllowRateLimit,
			AllowTimeLimit:          cfg.Feature.AllowTimeLimit,
			AllowConnectionNumLimit: cfg.Feature.AllowConnectionNumLimit,
			AllowMultiIP:            cfg.Feature.AllowMultiIP,
			AllowTunnelNumLimit:     cfg.Feature.AllowTunnelNumLimit,
			AllowLocalProxy:         cfg.Feature.AllowLocalProxy,
			AllowUserLocal:          cfg.Feature.AllowUserLocal,
			AllowUserChangeUsername: cfg.Feature.AllowUserChangeUsername,
			SystemInfoDisplay:       cfg.Feature.SystemInfoDisplay,
			OpenCaptcha:             cfg.Feature.OpenCaptcha,
		},
		security: ManagementSecurity{
			SecureMode: cfg.Security.SecureMode,
			ForcePoW:   cfg.Security.ForcePoW,
			PoWBits:    cfg.Security.PoWBits,
		},
		extensions: ManagementExtensions{
			Authorization: ManagementAuthorizationExtension{
				Roles:               append([]string(nil), actor.Roles...),
				Permissions:         append([]string(nil), actor.Permissions...),
				ClientIDs:           append([]int(nil), actor.ClientIDs...),
				KnownRoles:          permissions.KnownRoles(),
				KnownPermissions:    permissions.KnownPermissions(),
				ManagementAdmin:     webservice.PermissionManagementAdmin,
				ResourcePermissions: permissions.PermissionCatalog(),
			},
			Cluster: ManagementClusterExtension{
				NodeID:              cluster.NodeID,
				BootID:              cluster.BootID,
				RuntimeStartedAt:    cluster.RuntimeStartedAt,
				ConfigEpoch:         cluster.ConfigEpoch,
				ResyncOnBootChange:  cluster.ResyncOnBootChange,
				RunMode:             cluster.RunMode,
				EventsEnabled:       cluster.EventsEnabled,
				CallbacksReady:      cluster.CallbacksReady,
				SchemaVersion:       cluster.SchemaVersion,
				APIBase:             cluster.NodeAPIBase,
				Capabilities:        cluster.Capabilities,
				Protocol:            cluster.Protocol,
				LiveOnlyEvents:      cluster.Protocol.LiveOnlyEvents,
				ManagementPlatforms: a.managementPlatformConfigs(cfg, access),
			},
		},
	}
	return state
}

func (a *App) managementPlatformConfigs(cfg *servercfg.Snapshot, access nodeActorAccess) []ManagementPlatformConfig {
	scope := access.scope
	if !scope.CanViewStatus() {
		return nil
	}
	serviceUsername := func(platformID, configured string) (username string) {
		defer func() {
			if recover() != nil {
				username = strings.TrimSpace(configured)
				if username == "" {
					username = servercfg.DefaultPlatformServiceUsername(platformID)
				}
			}
		}()
		return a.managementPlatforms().ServiceUsername(platformID, configured)
	}
	statuses := webservice.BuildNodePlatformStatusPayloads(
		cfg,
		a.managementPlatformRuntimeStatus().Status,
		serviceUsername,
		scope.AllowsPlatform,
	)
	result := make([]ManagementPlatformConfig, 0, len(statuses))
	for _, platform := range statuses {
		result = append(result, ManagementPlatformConfig{
			PlatformID:              platform.PlatformID,
			MasterURL:               platform.MasterURL,
			ControlScope:            platform.ControlScope,
			ServiceUsername:         platform.ServiceUsername,
			Enabled:                 platform.Enabled,
			ConnectMode:             platform.ConnectMode,
			DirectEnabled:           platform.DirectEnabled,
			ReverseEnabled:          platform.ReverseEnabled,
			ReverseHeartbeatSeconds: platform.ReverseHeartbeatSeconds,
			ReverseWSURL:            platform.ReverseWSURL,
			CallbackEnabled:         platform.CallbackEnabled,
			CallbackURL:             platform.CallbackURL,
			CallbackTimeoutSeconds:  platform.CallbackTimeoutSeconds,
			CallbackRetryMax:        platform.CallbackRetryMax,
			CallbackRetryBackoffSec: platform.CallbackRetryBackoffSec,
			CallbackQueueMax:        platform.CallbackQueueMax,
			CallbackQueueSize:       platform.CallbackQueueSize,
			CallbackDropped:         platform.CallbackDropped,
			CallbackSigningEnabled:  platform.CallbackSigningEnabled,
			ReverseConnected:        platform.ReverseConnected,
			LastReverseConnectedAt:  platform.LastReverseConnectedAt,
			LastReverseHelloAt:      platform.LastReverseHelloAt,
			LastReverseEventAt:      platform.LastReverseEventAt,
			LastReversePingAt:       platform.LastReversePingAt,
			LastReversePongAt:       platform.LastReversePongAt,
			LastCallbackAt:          platform.LastCallbackAt,
			LastCallbackSuccessAt:   platform.LastCallbackSuccessAt,
			LastCallbackQueuedAt:    platform.LastCallbackQueuedAt,
			LastCallbackReplayAt:    platform.LastCallbackReplayAt,
			LastCallbackStatusCode:  platform.LastCallbackStatusCode,
			CallbackDeliveries:      platform.CallbackDeliveries,
			CallbackFailures:        platform.CallbackFailures,
			CallbackConsecutiveFail: platform.CallbackConsecutiveFail,
			LastReverseError:        platform.LastReverseError,
			LastReverseErrorAt:      platform.LastReverseErrorAt,
			LastCallbackError:       platform.LastCallbackError,
			LastCallbackErrorAt:     platform.LastCallbackErrorAt,
			LastReverseDisconnectAt: platform.LastReverseDisconnectAt,
		})
	}
	return result
}

func (a *App) Token(c Context) {
	cfg := a.currentConfig()
	setManagementNoStoreHeaders(c)
	loginPolicy := a.loginPolicy()

	var body ManagementAuthCredentialRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}

	input, _, errorCode, errorMessage, ok := authCredentialInput(body)
	if !ok {
		respondManagementErrorMessage(c, http.StatusBadRequest, errorCode, errorMessage, nil)
		return
	}
	ip := resolvedLoginClientIP(c, cfg)
	if !loginPolicy.AllowsIP(ip) {
		respondManagementErrorMessage(c, http.StatusForbidden, "login_access_denied", "login access denied", nil)
		return
	}

	identity, err := a.Services.Auth.Authenticate(input)
	if err != nil {
		if errors.Is(err, webservice.ErrInvalidCredentials) {
			respondManagementErrorMessage(c, http.StatusUnauthorized, "invalid_credentials", "invalid credentials", nil)
			return
		}
		respondManagementError(c, http.StatusInternalServerError, err)
		return
	}
	if identity != nil && identity.Authenticated {
		a.system().RegisterManagementAccess(ip)
	}

	token, issuedAt, expiresAt, err := webservice.IssueStandaloneToken(cfg, identity, common.TimeNow())
	if err != nil {
		statusCode := 500
		switch {
		case errors.Is(err, webservice.ErrUnauthenticated):
			statusCode = 401
		case errors.Is(err, webservice.ErrStandaloneTokenUnavailable):
			statusCode = 503
		}
		respondManagementError(c, statusCode, err)
		return
	}
	respondManagementData(c, http.StatusOK, ManagementAuthTokenPayload{
		AccessToken: token,
		TokenType:   "Bearer",
		IssuedAt:    issuedAt,
		ExpiresAt:   expiresAt,
		ExpiresIn:   expiresAt - issuedAt,
	}, managementResponseMeta(c, common.TimeNow().Unix(), strings.TrimSpace(a.runtimeIdentity().ConfigEpoch())))
}

func (a *App) SessionLogout(c Context) {
	setManagementNoStoreHeaders(c)
	a.Emit(c, Event{
		Name:     "session.logout",
		Resource: "session",
		Action:   "logout",
	})
	clearSessionIdentity(c)
	respondManagementData(c, http.StatusOK, ManagementAuthSessionPayload{
		Session: a.managementContractState(c).session,
		Actor:   AnonymousActor(),
	}, managementResponseMeta(c, common.TimeNow().Unix(), strings.TrimSpace(a.runtimeIdentity().ConfigEpoch())))
}

func ApplySessionIdentity(c Context, identity *webservice.SessionIdentity) {
	ApplySessionIdentityWithResolver(c, identity, webservice.DefaultPermissionResolver())
}

func ApplySessionIdentityWithResolver(c Context, identity *webservice.SessionIdentity, resolver webservice.PermissionResolver) {
	applySessionIdentityWithResolver(c, identity, resolver)
}

func ClearSessionIdentity(c Context) {
	clearSessionIdentity(c)
}

func currentSessionIdentityWithResolver(c Context, resolver webservice.PermissionResolver) *webservice.SessionIdentity {
	raw, _ := c.SessionValue(webservice.SessionIdentityKey).(string)
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, resolver)
	if err == nil && identity != nil {
		return identity
	}
	return nil
}

func applySessionIdentity(c Context, identity *webservice.SessionIdentity) {
	applySessionIdentityWithResolver(c, identity, webservice.DefaultPermissionResolver())
}

func applySessionIdentityWithResolver(c Context, identity *webservice.SessionIdentity, resolver webservice.PermissionResolver) {
	if c == nil {
		return
	}
	normalized := normalizedSessionIdentityWithResolver(identity, resolver)
	if normalized == nil {
		clearSessionIdentity(c)
		return
	}
	persisted := false
	mutateSession(c, func(editor SessionEditor) {
		persisted = writeSessionIdentityWithResolver(editor, normalized, resolver)
	})
	if !persisted {
		clearSessionIdentity(c)
		return
	}
	c.SetActor(ActorFromSessionIdentity(normalized))
}

func clearSessionIdentity(c Context) {
	if c == nil {
		return
	}
	mutateSession(c, func(editor SessionEditor) {
		writeSessionIdentity(editor, nil)
	})
	c.SetActor(AnonymousActor())
}

func ApplyActorSessionMap(session map[string]interface{}, actor *Actor, provider string) {
	ApplyActorSessionMapWithResolver(session, actor, provider, webservice.DefaultPermissionResolver())
}

func ApplyActorSessionMapWithResolver(session map[string]interface{}, actor *Actor, provider string, resolver webservice.PermissionResolver) {
	writeSessionIdentityWithResolver(sessionMapEditor{session: session}, sessionIdentityFromActorWithResolver(actor, provider, resolver), resolver)
}

func mutateSession(c Context, fn func(SessionEditor)) {
	if c == nil || fn == nil {
		return
	}
	if batch, ok := c.(sessionBatchContext); ok {
		batch.MutateSession(fn)
		return
	}
	fn(sessionEditorAdapter{
		set:    c.SetSessionValue,
		delete: c.DeleteSessionValue,
	})
}

func normalizedSessionIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	return normalizedSessionIdentityWithResolver(identity, webservice.DefaultPermissionResolver())
}

func normalizedSessionIdentityWithResolver(identity *webservice.SessionIdentity, resolver webservice.PermissionResolver) *webservice.SessionIdentity {
	if identity == nil {
		return nil
	}
	if isNilAppServiceValue(resolver) {
		resolver = webservice.DefaultPermissionResolver()
	}
	normalized := webservice.NormalizeSessionIdentityWithResolver(identity, resolver)
	if normalized == nil || !normalized.Authenticated {
		return nil
	}
	return normalized
}

func sessionIdentityFromActor(actor *Actor, provider string) *webservice.SessionIdentity {
	return sessionIdentityFromActorWithResolver(actor, provider, webservice.DefaultPermissionResolver())
}

func sessionIdentityFromActorWithResolver(actor *Actor, provider string, resolver webservice.PermissionResolver) *webservice.SessionIdentity {
	if actor == nil {
		return nil
	}
	kind := strings.TrimSpace(actor.Kind)
	if kind == "" || strings.EqualFold(kind, webservice.RoleAnonymous) {
		return nil
	}
	identity := &webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          kind,
		Provider:      strings.TrimSpace(provider),
		SubjectID:     strings.TrimSpace(actor.SubjectID),
		Username:      strings.TrimSpace(actor.Username),
		IsAdmin:       actor.IsAdmin,
		ClientIDs:     append([]int(nil), actor.ClientIDs...),
		Roles:         append([]string(nil), actor.Roles...),
		Permissions:   append([]string(nil), actor.Permissions...),
	}
	if len(actor.Attributes) > 0 {
		identity.Attributes = make(map[string]string, len(actor.Attributes))
		for key, value := range actor.Attributes {
			identity.Attributes[key] = value
		}
	}
	return normalizedSessionIdentityWithResolver(identity, resolver)
}

func writeSessionIdentity(editor SessionEditor, identity *webservice.SessionIdentity) bool {
	return writeSessionIdentityWithResolver(editor, identity, webservice.DefaultPermissionResolver())
}

func writeSessionIdentityWithResolver(editor SessionEditor, identity *webservice.SessionIdentity, resolver webservice.PermissionResolver) bool {
	if editor == nil {
		return false
	}
	clearSessionIdentityValues(editor)
	normalized := normalizedSessionIdentityWithResolver(identity, resolver)
	if normalized == nil {
		return true
	}
	encoded, err := webservice.MarshalSessionIdentityWithResolver(normalized, resolver)
	if err != nil {
		return false
	}
	editor.Set(webservice.SessionIdentityKey, encoded)
	return true
}

func clearSessionIdentityValues(editor SessionEditor) {
	if editor == nil {
		return
	}
	for _, key := range legacySessionKeys {
		editor.Delete(key)
	}
	editor.Delete(webservice.SessionIdentityKey)
}

func (a sessionEditorAdapter) Set(key string, value interface{}) {
	if a.set != nil {
		a.set(key, value)
	}
}

func (a sessionEditorAdapter) Delete(key string) {
	if a.delete != nil {
		a.delete(key)
	}
}

func (m sessionMapEditor) Set(key string, value interface{}) {
	if m.session != nil {
		m.session[key] = value
	}
}

func (m sessionMapEditor) Delete(key string) {
	if m.session != nil {
		delete(m.session, key)
	}
}

func authCredentialInput(body ManagementAuthCredentialRequest) (webservice.AuthenticateInput, string, string, string, bool) {
	verifyKey := strings.TrimSpace(body.VerifyKey)
	username := strings.TrimSpace(body.Username)
	totp := strings.TrimSpace(body.TOTP)
	if verifyKey != "" && (username != "" || body.Password != "" || totp != "") {
		return webservice.AuthenticateInput{}, "", "mixed_credentials", "verify_key cannot be combined with username, password, or totp", false
	}
	if verifyKey != "" {
		return webservice.AuthenticateInput{
			Password: verifyKey,
		}, verifyKey, "", "", true
	}
	if username == "" || (body.Password == "" && totp == "") {
		return webservice.AuthenticateInput{}, "", "credentials_required", "missing username/password or verify_key", false
	}
	return webservice.AuthenticateInput{
		Username: username,
		Password: body.Password,
		TOTP:     totp,
	}, username, "", "", true
}

func (a *App) RegisterIPLimitAccess(c Context) {
	var body ManagementAccessIPLimitRegisterRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}

	client := a.ipLimitRegisterableClient(body.VerifyKey)
	if client == nil {
		respondManagementErrorMessage(c, http.StatusUnauthorized, "invalid_verify_key", "invalid verify_key", nil)
		return
	}

	a.system().RegisterManagementAccess(resolvedLoginClientIP(c, a.currentConfig()))
	respondManagementData(c, http.StatusOK, ManagementAccessIPLimitRegisterPayload{
		Registered: true,
		ClientID:   client.Id,
	}, managementResponseMeta(c, common.TimeNow().Unix(), strings.TrimSpace(a.runtimeIdentity().ConfigEpoch())))
}

func (a *App) ipLimitRegisterableClient(verifyKey string) *file.Client {
	verifyKey = strings.TrimSpace(verifyKey)
	if verifyKey == "" || a == nil || a.backend().Repository == nil {
		return nil
	}
	nowUnix := common.TimeNow().Unix()
	return webservice.FindClientByVerifyKeyBestEffort(a.backend().Repository, verifyKey, func(client *file.Client) bool {
		return ipLimitRegisterableClientAllowed(client, nowUnix)
	})
}

func ipLimitRegisterableClientAllowed(client *file.Client, nowUnix int64) bool {
	if client == nil || client.Id <= 0 || !client.Status {
		return false
	}
	verifyKey := strings.TrimSpace(client.VerifyKey)
	remark := strings.ToLower(strings.TrimSpace(client.Remark))
	if strings.EqualFold(verifyKey, "localproxy") || remark == "localproxy" {
		return false
	}
	if expireAt := client.EffectiveExpireAt(); expireAt > 0 && nowUnix >= expireAt {
		return false
	}
	if !client.NoStore && !client.NoDisplay {
		return true
	}
	return client.NoStore && client.NoDisplay && (remark == "public_vkey" || remark == "visitor_vkey")
}

func actorPrimaryClientID(actor *Actor) *int {
	if actor == nil {
		return nil
	}
	if clientID, ok := ActorPrimaryClientID(actor); ok {
		return &clientID
	}
	return nil
}

func joinBase(base, suffix string) string {
	base = servercfg.NormalizeBaseURL(base)
	if base == "" {
		return suffix
	}
	return base + suffix
}

func identityValue(identity *webservice.SessionIdentity, getter func(*webservice.SessionIdentity) string) string {
	if identity == nil {
		return ""
	}
	return getter(identity)
}

func sessionIdentityValue(
	identity *webservice.SessionIdentity,
	actor *Actor,
	identityGetter func(*webservice.SessionIdentity) string,
	actorGetter func(*Actor) string,
) string {
	if value := strings.TrimSpace(identityValue(identity, identityGetter)); value != "" {
		return value
	}
	if actor == nil || actorGetter == nil {
		return ""
	}
	return strings.TrimSpace(actorGetter(actor))
}

func (a *App) CreateSession(c Context) {
	setManagementNoStoreHeaders(c)
	cfg := a.currentConfig()
	loginPolicy := a.loginPolicy()
	loginPolicy.Clean(false)

	var body ManagementAuthCredentialRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}

	input, failureKey, errorCode, errorMessage, ok := authCredentialInput(body)
	if !ok {
		respondManagementErrorMessage(c, 400, errorCode, errorMessage, nil)
		return
	}

	ip := resolvedLoginClientIP(c, cfg)
	if !loginPolicy.AllowsIP(ip) {
		logs.Warn("Login blocked by static source access rules from %s", ip)
		respondManagementErrorMessage(c, 403, "login_access_denied", "login access denied", nil)
		return
	}

	ipBanned := loginPolicy.IsIPBanned(ip)
	userBanned := failureKey != "" && loginPolicy.IsUserBanned(failureKey)
	if cfg.Feature.OpenCaptcha && !framework.VerifyCaptcha(strings.TrimSpace(body.CaptchaID), strings.TrimSpace(body.CaptchaAnswer)) {
		logs.Warn("Captcha failed for login from %s", ip)
		loginPolicy.RecordFailure(ip, true)
		respondManagementErrorMessage(c, 400, "invalid_captcha", "invalid captcha", nil)
		return
	}
	if sessionLoginPoWRequired(cfg, ipBanned, userBanned) {
		if !sessionLoginPoWValid(cfg, body) {
			logs.Warn("PoW failed for login from %s", ip)
			loginPolicy.RecordFailure(ip, true)
			respondManagementErrorMessage(c, 429, "pow_required", "pow verification required", map[string]any{
				"pow_bits": cfg.Security.PoWBits,
			})
			return
		}
	} else if ipBanned || userBanned {
		respondManagementErrorMessage(c, 429, "login_rate_limited", "login temporarily blocked", nil)
		return
	}

	identity, err := a.Services.Auth.Authenticate(input)
	if err != nil {
		if !errors.Is(err, webservice.ErrInvalidCredentials) {
			logs.Warn("Authentication error from %s: %v", ip, err)
			respondManagementError(c, 500, err)
			return
		}
		logs.Warn("Login failed from %s", ip)
		loginPolicy.RecordFailure(ip, true)
		if failureKey != "" {
			loginPolicy.RecordFailure(failureKey, true)
		}
		respondManagementErrorMessage(c, 401, "invalid_credentials", "invalid credentials", nil)
		return
	}

	applySessionIdentityWithResolver(c, identity, a.permissionResolver())
	if identity.Authenticated {
		a.system().RegisterManagementAccess(ip)
	}
	loginPolicy.RemoveBan(ip)
	if failureKey != "" {
		loginPolicy.RemoveBan(failureKey)
	}
	logs.Info("Login success for user %s from %s", identity.Username, ip)
	a.Emit(c, Event{
		Name:     "session.login",
		Resource: "session",
		Action:   "login",
		Fields:   map[string]interface{}{"username": identity.Username, "subject_id": identity.SubjectID},
	})
	respondManagementData(c, 200, ManagementAuthSessionPayload{
		Session: a.managementContractState(c).session,
		Actor:   c.Actor(),
	}, managementResponseMeta(c, common.TimeNow().Unix(), strings.TrimSpace(a.runtimeIdentity().ConfigEpoch())))
}

func (a *App) RegisterAuthUser(c Context) {
	setManagementNoStoreHeaders(c)
	cfg := a.currentConfig()
	if !cfg.Feature.AllowUserRegister {
		respondManagementErrorMessage(c, 403, "registration_disabled", "register is not allowed", nil)
		return
	}
	loginPolicy := a.loginPolicy()
	loginPolicy.Clean(false)
	ip := resolvedLoginClientIP(c, cfg)
	if !loginPolicy.AllowsIP(ip) {
		logs.Warn("Registration blocked by static source access rules from %s", ip)
		respondManagementErrorMessage(c, 403, "registration_access_denied", "registration access denied", nil)
		return
	}

	var body ManagementAuthRegisterRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}
	if strings.TrimSpace(body.Username) == "" {
		respondMissingRequestField(c, "username")
		return
	}
	if body.Password == "" {
		respondMissingRequestField(c, "password")
		return
	}
	if cfg.Feature.OpenCaptcha && !framework.VerifyCaptcha(strings.TrimSpace(body.CaptchaID), strings.TrimSpace(body.CaptchaAnswer)) {
		respondManagementErrorMessage(c, 400, "invalid_captcha", "invalid captcha", nil)
		return
	}

	result, err := a.Services.Auth.RegisterUser(webservice.RegisterUserInput{
		Username: strings.TrimSpace(body.Username),
		Password: body.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, webservice.ErrInvalidRegistration):
			respondManagementErrorMessage(c, 400, "invalid_registration", "please check your input", nil)
		case errors.Is(err, webservice.ErrReservedUsername):
			respondManagementErrorMessage(c, 400, "reserved_username", "please check your input", nil)
		default:
			respondManagementError(c, 500, err)
		}
		return
	}

	clientID := 0
	if len(result.ClientIDs) > 0 {
		clientID = result.ClientIDs[0]
	}
	a.Emit(c, Event{
		Name:     "client.registered",
		Resource: "client",
		Action:   "register",
		Fields:   map[string]interface{}{"client_id": clientID, "username": result.Username, "subject_id": result.SubjectID},
	})
	respondManagementData(c, 200, ManagementAuthRegisterPayload{
		SubjectID: result.SubjectID,
		Username:  result.Username,
		ClientIDs: append([]int(nil), result.ClientIDs...),
	}, managementResponseMeta(c, common.TimeNow().Unix(), strings.TrimSpace(a.runtimeIdentity().ConfigEpoch())))
}

func (a *App) doLogin(c Context, username, password, totp string, explicit bool) *webservice.SessionIdentity {
	cfg := a.currentConfig()
	loginPolicy := a.loginPolicy()
	loginPolicy.Clean(false)

	ip := resolvedLoginClientIP(c, cfg)
	if explicit && !loginPolicy.AllowsIP(ip) {
		logs.Warn("Login blocked by static source access rules from %s", ip)
		return nil
	}

	if explicit && loginPolicy.IsIPBanned(ip) {
		return nil
	}

	identity, err := a.Services.Auth.Authenticate(webservice.AuthenticateInput{
		Username: username,
		Password: password,
		TOTP:     totp,
	})
	if err != nil {
		if !errors.Is(err, webservice.ErrInvalidCredentials) {
			logs.Warn("Authentication error for user %s from %s: %v", username, ip, err)
		}
		loginPolicy.RecordFailure(ip, explicit)
		return nil
	}

	applySessionIdentityWithResolver(c, identity, a.permissionResolver())
	if identity.Authenticated {
		a.system().RegisterManagementAccess(ip)
	}

	loginPolicy.RemoveBan(ip)
	return identity
}

func requestRemoteIP(c Context) string {
	host, _, err := net.SplitHostPort(c.RemoteAddr())
	if err != nil {
		return c.RemoteAddr()
	}
	return host
}

func resolvedLoginClientIP(c Context, cfg *servercfg.Snapshot) string {
	ip := requestRemoteIP(c)
	if cfg == nil {
		return ip
	}
	httpOnlyPass := cfg.Auth.HTTPOnlyPass
	if (cfg.Auth.AllowXRealIP && common.IsTrustedProxy(cfg.Auth.TrustedProxyIPs, ip)) ||
		(httpOnlyPass != "" && c.RequestHeader("X-NPS-Http-Only") == httpOnlyPass) {
		if realIP := firstTrustedForwardedLoginIP(
			c.RequestHeader("X-Forwarded-For"),
			c.RequestHeader("X-Real-IP"),
		); realIP != "" {
			return realIP
		}
	}
	return ip
}

func firstTrustedForwardedLoginIP(values ...string) string {
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			if ip := common.GetIpByAddr(strings.TrimSpace(part)); ip != "" {
				return ip
			}
		}
	}
	return ""
}

func sessionLoginPoWRequired(cfg *servercfg.Snapshot, ipBanned, userBanned bool) bool {
	if cfg == nil || cfg.Security.PoWBits <= 0 {
		return false
	}
	if cfg.Security.ForcePoW {
		return true
	}
	if !cfg.Security.SecureMode {
		return false
	}
	return ipBanned || userBanned
}

func sessionLoginPoWValid(cfg *servercfg.Snapshot, body ManagementAuthCredentialRequest) bool {
	if cfg == nil || cfg.Security.PoWBits <= 0 {
		return false
	}
	nonce := strings.TrimSpace(body.PoWNonce)
	if nonce == "" || body.PoWBits != cfg.Security.PoWBits {
		return false
	}
	return common.ValidatePoW(cfg.Security.PoWBits, sessionLoginPoWSeed(body), nonce)
}

func sessionLoginPoWSeed(body ManagementAuthCredentialRequest) string {
	verifyKey := strings.TrimSpace(body.VerifyKey)
	if verifyKey != "" {
		return verifyKey
	}
	return strings.TrimSpace(body.Username) + "\n" + body.Password + "\n" + strings.TrimSpace(body.TOTP)
}
