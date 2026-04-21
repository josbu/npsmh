package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

type nodeUserWriteRequest struct {
	Username            string  `json:"username"`
	Password            *string `json:"password,omitempty"`
	TOTPSecret          *string `json:"totp_secret,omitempty"`
	ExpectedRevision    int64   `json:"expected_revision,omitempty"`
	Status              *bool   `json:"status,omitempty"`
	ExpireAt            string  `json:"expire_at"`
	FlowLimitTotalBytes int64   `json:"flow_limit_total_bytes"`
	MaxClients          int     `json:"max_clients"`
	MaxTunnels          int     `json:"max_tunnels"`
	MaxHosts            int     `json:"max_hosts"`
	MaxConnections      int     `json:"max_connections"`
	RateLimitTotalBps   int     `json:"rate_limit_total_bps"`
	ResetFlow           bool    `json:"reset_flow"`
	EntryACLMode        int     `json:"entry_acl_mode"`
	EntryACLRules       string  `json:"entry_acl_rules"`
	DestACLMode         int     `json:"dest_acl_mode"`
	DestACLRules        string  `json:"dest_acl_rules"`
}

type nodeClientWriteRequest struct {
	OwnerUserID         *int    `json:"owner_user_id,omitempty"`
	ManagerUserIDs      *[]int  `json:"manager_user_ids,omitempty"`
	VerifyKey           string  `json:"verify_key"`
	ExpectedRevision    int64   `json:"expected_revision,omitempty"`
	Remark              string  `json:"remark"`
	Username            string  `json:"username"`
	Password            *string `json:"password,omitempty"`
	Compress            bool    `json:"compress"`
	Crypt               bool    `json:"crypt"`
	ConfigConnAllow     bool    `json:"config_conn_allow"`
	RateLimitTotalBps   int     `json:"rate_limit_total_bps"`
	MaxConnections      int     `json:"max_connections"`
	MaxTunnelNum        int     `json:"max_tunnel_num"`
	FlowLimitTotalBytes int64   `json:"flow_limit_total_bytes"`
	ExpireAt            string  `json:"expire_at"`
	EntryACLMode        int     `json:"entry_acl_mode"`
	EntryACLRules       string  `json:"entry_acl_rules"`
	ResetFlow           bool    `json:"reset_flow"`
}

type nodeStatusActionRequest struct {
	Status *bool `json:"status,omitempty"`
}

type nodeKeyActionRequest struct {
	Key string `json:"key"`
}

type nodeModeActionRequest struct {
	Mode string `json:"mode"`
}

type nodeClearClientsRequest struct {
	Mode      string `json:"mode"`
	ClientIDs *[]int `json:"client_ids,omitempty"`
}

type nodeTunnelWriteRequest struct {
	ClientID            int    `json:"client_id"`
	ExpectedRevision    int64  `json:"expected_revision,omitempty"`
	Port                int    `json:"port"`
	ServerIP            string `json:"server_ip"`
	Mode                string `json:"mode"`
	TargetType          string `json:"target_type"`
	Target              string `json:"target"`
	ProxyProtocol       int    `json:"proxy_protocol"`
	LocalProxy          bool   `json:"local_proxy"`
	Auth                string `json:"auth"`
	Remark              string `json:"remark"`
	Password            string `json:"password"`
	LocalPath           string `json:"local_path"`
	StripPre            string `json:"strip_pre"`
	EnableHTTP          bool   `json:"enable_http"`
	EnableSocks5        bool   `json:"enable_socks5"`
	EntryACLMode        int    `json:"entry_acl_mode"`
	EntryACLRules       string `json:"entry_acl_rules"`
	DestACLMode         int    `json:"dest_acl_mode"`
	DestACLRules        string `json:"dest_acl_rules"`
	FlowLimitTotalBytes int64  `json:"flow_limit_total_bytes"`
	ExpireAt            string `json:"expire_at"`
	RateLimitTotalBps   int    `json:"rate_limit_total_bps"`
	MaxConnections      int    `json:"max_connections"`
	ResetFlow           bool   `json:"reset_flow"`
}

type nodeHostWriteRequest struct {
	ClientID                int    `json:"client_id"`
	ExpectedRevision        int64  `json:"expected_revision,omitempty"`
	Host                    string `json:"host"`
	Target                  string `json:"target"`
	ProxyProtocol           int    `json:"proxy_protocol"`
	LocalProxy              bool   `json:"local_proxy"`
	Auth                    string `json:"auth"`
	Header                  string `json:"header"`
	RespHeader              string `json:"resp_header"`
	HostChange              string `json:"host_change"`
	Remark                  string `json:"remark"`
	Location                string `json:"location"`
	PathRewrite             string `json:"path_rewrite"`
	RedirectURL             string `json:"redirect_url"`
	FlowLimitTotalBytes     int64  `json:"flow_limit_total_bytes"`
	ExpireAt                string `json:"expire_at"`
	RateLimitTotalBps       int    `json:"rate_limit_total_bps"`
	MaxConnections          int    `json:"max_connections"`
	ResetFlow               bool   `json:"reset_flow"`
	EntryACLMode            int    `json:"entry_acl_mode"`
	EntryACLRules           string `json:"entry_acl_rules"`
	Scheme                  string `json:"scheme"`
	HTTPSJustProxy          bool   `json:"https_just_proxy"`
	TLSOffload              bool   `json:"tls_offload"`
	AutoSSL                 bool   `json:"auto_ssl"`
	KeyFile                 string `json:"key_file"`
	CertFile                string `json:"cert_file"`
	AutoHTTPS               bool   `json:"auto_https"`
	AutoCORS                bool   `json:"auto_cors"`
	CompatMode              bool   `json:"compat_mode"`
	TargetIsHTTPS           bool   `json:"target_is_https"`
	SyncCertToMatchingHosts bool   `json:"sync_cert_to_matching_hosts"`
}

type nodeGlobalUpdateRequest struct {
	EntryACLMode  int    `json:"entry_acl_mode"`
	EntryACLRules string `json:"entry_acl_rules"`
}

type nodeKickRequest struct {
	ClientID  int    `json:"client_id"`
	VerifyKey string `json:"verify_key"`
}

type nodeTrafficRequest struct {
	Items     []nodeTrafficItemRequest `json:"items,omitempty"`
	ClientID  int                      `json:"client_id"`
	VerifyKey string                   `json:"verify_key"`
	In        int64                    `json:"in"`
	Out       int64                    `json:"out"`
}

type nodeTrafficItemRequest struct {
	ClientID  int    `json:"client_id"`
	VerifyKey string `json:"verify_key"`
	In        int64  `json:"in"`
	Out       int64  `json:"out"`
}

type nodeResourceMutationPayload struct {
	ConfigEpoch string `json:"-"`
	GeneratedAt int64  `json:"-"`
	Resource    string `json:"resource"`
	Action      string `json:"action"`
	ID          int    `json:"id,omitempty"`
	Item        any    `json:"item,omitempty"`
}

func nodeMutationStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nodeResourceMutationFields(id int, resourceFields, overrides map[string]interface{}) map[string]interface{} {
	fields := map[string]interface{}{"id": id}
	mergeEventFieldMap(fields, resourceFields)
	mergeEventFieldMap(fields, overrides)
	return fields
}

func nodeResourceStateEventName(resource, action string) string {
	resource = strings.TrimSpace(resource)
	switch strings.TrimSpace(action) {
	case "start":
		return resource + ".started"
	case "clear":
		return resource + ".cleared"
	default:
		return resource + ".stopped"
	}
}

func (a *App) emitNodeResourceMutationEvent(c Context, eventName, resource, action string, fields map[string]interface{}) {
	if a == nil {
		return
	}
	a.Emit(c, Event{
		Name:     strings.TrimSpace(eventName),
		Resource: strings.TrimSpace(resource),
		Action:   strings.TrimSpace(action),
		Fields:   fields,
	})
}

func (a *App) respondCompletedNodeMutation(c Context, resource, action string, id int, item any) {
	respondNodeMutationData(c, nodeResourceMutationPayload{
		ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
		GeneratedAt: time.Now().Unix(),
		Resource:    strings.TrimSpace(resource),
		Action:      strings.TrimSpace(action),
		ID:          id,
		Item:        item,
	}, nil)
}

func authorizeNodeProtectedAction(a *App, c Context, input webservice.ProtectedActionAccessInput) (int, error) {
	authz := a.authorization()
	input.Principal = normalizePrincipalWithAuthorization(input.Principal, authz)
	result, err := webservice.ResolveProtectedActionAccess(authz, input)
	if err != nil {
		return 0, err
	}
	return result.ResolvedClientID, nil
}

func respondNodeMutationData(c Context, payload nodeResourceMutationPayload, err error) {
	if err != nil {
		respondManagementError(c, nodeMutationErrorStatus(err), err)
		return
	}
	respondManagementData(c, http.StatusOK, payload, managementResponseMeta(c, payload.GeneratedAt, payload.ConfigEpoch))
}

func nodeMutationErrorStatus(err error) int {
	switch {
	case errors.Is(err, webservice.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, webservice.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, webservice.ErrUserNotFound),
		errors.Is(err, webservice.ErrClientNotFound),
		errors.Is(err, webservice.ErrTunnelNotFound),
		errors.Is(err, webservice.ErrHostNotFound):
		return http.StatusNotFound
	case errors.Is(err, webservice.ErrClientVKeyDuplicate),
		errors.Is(err, webservice.ErrClientLimitExceeded),
		errors.Is(err, webservice.ErrClientRateLimitExceeded),
		errors.Is(err, webservice.ErrClientConnLimitExceeded),
		errors.Is(err, webservice.ErrRevisionConflict),
		errors.Is(err, webservice.ErrHostExists),
		errors.Is(err, webservice.ErrPortUnavailable),
		errors.Is(err, webservice.ErrTunnelLimitExceeded),
		errors.Is(err, webservice.ErrHostLimitExceeded),
		errors.Is(err, webservice.ErrClientResourceLimitExceeded):
		return http.StatusConflict
	case errors.Is(err, webservice.ErrReservedUsername),
		errors.Is(err, webservice.ErrUserUsernameRequired),
		errors.Is(err, webservice.ErrUserPasswordRequired),
		errors.Is(err, webservice.ErrClientModifyFailed),
		errors.Is(err, webservice.ErrModeRequired):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func authorizeRequestedNodeClientWithAccess(a *App, c Context, access nodeActorAccess, requestedClientID int) error {
	if requestedClientID <= 0 {
		return nil
	}
	_, err := access.authorize(a, c, webservice.ProtectedActionAccessInput{
		ClientScope:       true,
		RequestedClientID: requestedClientID,
	})
	return err
}

func editableNodeHostIDs(a *App, access nodeActorAccess) ([]int, error) {
	if a == nil {
		return nil, nil
	}
	rows, err := a.scopedNodeHostsWithAccess(access)
	if err != nil {
		return nil, err
	}
	return nodeHostIDs(rows), nil
}

func nodeHostIDs(hosts []*file.Host) []int {
	if len(hosts) == 0 {
		return nil
	}
	ids := make([]int, 0, len(hosts))
	for _, host := range hosts {
		if host == nil || host.Client == nil {
			continue
		}
		ids = append(ids, host.Id)
	}
	return orderedUniquePositiveIDs(ids)
}

func orderedUniquePositiveIDs(ids []int) []int {
	if len(ids) == 0 {
		return nil
	}
	unique := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return nil
	}
	return unique
}

func userEventFields(user *file.User) map[string]interface{} {
	if user == nil {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"id":                   user.Id,
		"kind":                 user.Kind,
		"external_platform_id": user.ExternalPlatformID,
		"hidden":               user.Hidden,
		"revision":             user.Revision,
		"updated_at":           user.UpdatedAt,
	}
}

func clientEventFields(client *file.Client) map[string]interface{} {
	if client == nil {
		return map[string]interface{}{}
	}
	fields := map[string]interface{}{
		"id":                 client.Id,
		"owner_user_id":      client.OwnerID(),
		"source_type":        client.SourceType,
		"source_platform_id": client.SourcePlatformID,
		"source_actor_id":    client.SourceActorID,
		"revision":           client.Revision,
		"updated_at":         client.UpdatedAt,
	}
	if len(client.ManagerUserIDs) > 0 {
		fields["manager_user_ids"] = append([]int(nil), client.ManagerUserIDs...)
	}
	return fields
}

func tunnelEventFields(tunnel *file.Tunnel) map[string]interface{} {
	if tunnel == nil {
		return map[string]interface{}{}
	}
	fields := map[string]interface{}{
		"id":         tunnel.Id,
		"revision":   tunnel.Revision,
		"updated_at": tunnel.UpdatedAt,
	}
	if tunnel.Client != nil {
		fields["client_id"] = tunnel.Client.Id
		mergeEventFieldMap(fields, clientEventOwnershipFields(tunnel.Client))
	}
	if tunnel.Mode != "" {
		fields["mode"] = tunnel.Mode
	}
	return fields
}

func hostEventFields(host *file.Host) map[string]interface{} {
	if host == nil {
		return map[string]interface{}{}
	}
	fields := map[string]interface{}{
		"id":         host.Id,
		"revision":   host.Revision,
		"updated_at": host.UpdatedAt,
	}
	if host.Client != nil {
		fields["client_id"] = host.Client.Id
		mergeEventFieldMap(fields, clientEventOwnershipFields(host.Client))
	}
	return fields
}

func clientEventOwnershipFields(client *file.Client) map[string]interface{} {
	if client == nil {
		return map[string]interface{}{}
	}
	fields := map[string]interface{}{
		"owner_user_id":      client.OwnerID(),
		"source_type":        client.SourceType,
		"source_platform_id": client.SourcePlatformID,
		"source_actor_id":    client.SourceActorID,
	}
	if len(client.ManagerUserIDs) > 0 {
		fields["manager_user_ids"] = append([]int(nil), client.ManagerUserIDs...)
	}
	return fields
}

func mergeEventFieldMap(base map[string]interface{}, extras map[string]interface{}) map[string]interface{} {
	if base == nil {
		base = make(map[string]interface{})
	}
	for key, value := range extras {
		if value == nil {
			continue
		}
		base[key] = value
	}
	return base
}

var (
	nodeLiveOnlyEvents = []string{
		"node.traffic.report",
		"management_platforms.updated",
		"operations.updated",
		"callbacks_queue.updated",
		"webhook.delivery_succeeded",
		"webhook.delivery_failed",
	}
	nodeEphemeralEvents = []string{
		"client.traffic.reported",
	}
	nodeSinkSuppressedEvents = []string{
		"node.traffic.report",
		"management_platforms.updated",
		"operations.updated",
		"callbacks_queue.updated",
		"webhook.delivery_succeeded",
		"webhook.delivery_failed",
	}
)

func NodeLiveOnlyEvents() []string {
	return append([]string(nil), nodeLiveOnlyEvents...)
}

func NodeEphemeralEvents() []string {
	return append([]string(nil), nodeEphemeralEvents...)
}

func ShouldPersistNodeEventName(name string) bool {
	name = normalizeNodeEventName(name)
	if name == "" {
		return false
	}
	return !containsNodeEventName(nodeLiveOnlyEvents, name) && !containsNodeEventName(nodeEphemeralEvents, name)
}

func ShouldDeliverNodeCallbackEventName(name string) bool {
	return ShouldDeliverNodeSinkEventName(name)
}

func ShouldDeliverNodeSinkEventName(name string) bool {
	name = normalizeNodeEventName(name)
	if name == "" {
		return false
	}
	return !containsNodeEventName(nodeSinkSuppressedEvents, name)
}

func normalizeNodeEventName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func containsNodeEventName(values []string, target string) bool {
	target = normalizeNodeEventName(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if normalizeNodeEventName(value) == target {
			return true
		}
	}
	return false
}
