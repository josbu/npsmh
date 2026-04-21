package api

import (
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

type nodeHostResourcePayload struct {
	ID                  int                        `json:"id"`
	Revision            int64                      `json:"revision"`
	UpdatedAt           int64                      `json:"updated_at"`
	ClientID            int                        `json:"client_id,omitempty"`
	Client              *nodeClientResourcePayload `json:"client,omitempty"`
	Host                string                     `json:"host,omitempty"`
	Header              string                     `json:"header,omitempty"`
	RespHeader          string                     `json:"resp_header,omitempty"`
	HostChange          string                     `json:"host_change,omitempty"`
	Location            string                     `json:"location,omitempty"`
	PathRewrite         string                     `json:"path_rewrite,omitempty"`
	Remark              string                     `json:"remark,omitempty"`
	Scheme              string                     `json:"scheme,omitempty"`
	RedirectURL         string                     `json:"redirect_url,omitempty"`
	HttpsJustProxy      bool                       `json:"https_just_proxy"`
	TlsOffload          bool                       `json:"tls_offload"`
	AutoSSL             bool                       `json:"auto_ssl"`
	CertType            string                     `json:"cert_type,omitempty"`
	CertHash            string                     `json:"cert_hash,omitempty"`
	CertFile            string                     `json:"cert_file,omitempty"`
	KeyFile             string                     `json:"key_file,omitempty"`
	IsClose             bool                       `json:"is_close"`
	AutoHttps           bool                       `json:"auto_https"`
	AutoCORS            bool                       `json:"auto_cors"`
	CompatMode          bool                       `json:"compat_mode"`
	EntryACLMode        int                        `json:"entry_acl_mode"`
	EntryACLRules       string                     `json:"entry_acl_rules,omitempty"`
	TargetIsHttps       bool                       `json:"target_is_https"`
	Target              string                     `json:"target,omitempty"`
	LocalProxy          bool                       `json:"local_proxy"`
	ExpireAt            int64                      `json:"expire_at"`
	FlowLimitTotalBytes int64                      `json:"flow_limit_total_bytes"`
	RateLimitTotalBps   int                        `json:"rate_limit_total_bps"`
	MaxConnections      int                        `json:"max_connections"`
	NowConn             int                        `json:"now_conn"`
	NowRateInBps        int64                      `json:"now_rate_in_bps"`
	NowRateOutBps       int64                      `json:"now_rate_out_bps"`
	NowRateTotalBps     int64                      `json:"now_rate_total_bps"`
	ServiceInBytes      int64                      `json:"service_in_bytes"`
	ServiceOutBytes     int64                      `json:"service_out_bytes"`
	ServiceTotalBytes   int64                      `json:"service_total_bytes"`
	ProxyProtocol       int                        `json:"proxy_protocol"`
	Auth                string                     `json:"auth,omitempty"`
}

type nodeHostCertSuggestionPayload struct {
	SourceHostID   int    `json:"source_host_id,omitempty"`
	SourceHost     string `json:"source_host,omitempty"`
	SourceLocation string `json:"source_location,omitempty"`
	CertType       string `json:"cert_type,omitempty"`
	CertFile       string `json:"cert_file,omitempty"`
	KeyFile        string `json:"key_file,omitempty"`
	CanApplyToForm bool   `json:"can_apply_to_form"`
	IsClose        bool   `json:"is_close"`
}

func (a *App) NodeHosts(c Context) {
	access := a.nodeActorAccessFromContext(c)
	offset := requestIntValue(c, "offset")
	limit := requestIntValue(c, "limit")
	items, total, err := a.listNodeHosts(c, access)
	if err != nil {
		respondNodeResourceData(c, nodeResourceListPayload{}, err)
		return
	}
	offset, limit, returned, hasMore := nodeListPagination(offset, limit, len(items), total)
	respondNodeResourceData(c, nodeResourceListPayload{
		ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
		GeneratedAt: time.Now().Unix(),
		Offset:      offset,
		Limit:       limit,
		Returned:    returned,
		Total:       total,
		HasMore:     hasMore,
		Items:       items,
	}, nil)
}

func (a *App) NodeHost(c Context) {
	access := a.nodeActorAccessFromContext(c)
	host, err := a.Services.Index.GetHost(requestIntValue(c, "id"))
	if err == nil {
		switch {
		case host == nil:
			err = webservice.ErrHostNotFound
		case !access.scope.AllowsClient(host.Client):
			err = webservice.ErrForbidden
		default:
			host = a.sanitizeNodeHost(access.actor, access.scope, host)
		}
	}
	respondNodeResourceData(c, nodeResourceItemPayload{
		ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
		GeneratedAt: time.Now().Unix(),
		Item:        a.nodeHostResource(host),
	}, err)
}

func (a *App) NodeHostCertSuggestion(c Context) {
	access := a.nodeActorAccessFromContext(c)
	payload := nodeHostCertSuggestionPayload{}
	hostName := strings.TrimSpace(requestString(c, "host"))
	if hostName != "" {
		hosts, err := a.scopedNodeHostsWithAccess(access)
		if err != nil {
			respondNodeResourceData(c, nodeResourceItemPayload{}, err)
			return
		}
		candidates := suggestedReusableCertHosts(hosts)
		if match := file.SelectReusableCertHost(hostName, candidates, requestIntValue(c, "exclude_id")); match != nil {
			payload = nodeHostCertSuggestionPayload{
				SourceHostID:   match.Id,
				SourceHost:     match.Host,
				SourceLocation: match.Location,
				CertType:       match.CertType,
				CanApplyToForm: match.CertType == "file" && access.allows(a, webservice.PermissionHostsUpdate),
				IsClose:        match.IsClose,
			}
			if payload.CanApplyToForm {
				payload.CertFile = match.CertFile
				payload.KeyFile = match.KeyFile
			}
		}
	}
	respondNodeResourceData(c, nodeResourceItemPayload{
		ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
		GeneratedAt: time.Now().Unix(),
		Item:        payload,
	}, nil)
}

func (a *App) scopedNodeHosts(actor *Actor) ([]*file.Host, error) {
	return a.scopedNodeHostsWithAccess(a.nodeActorAccess(actor))
}

func (a *App) scopedNodeHostsWithAccess(access nodeActorAccess) ([]*file.Host, error) {
	if a == nil {
		return nil, nil
	}
	visibility, ok, err := access.visibility(a)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	rows, _ := a.Services.Index.ListHosts((nodeListQuery{}).hostsInput(visibility))
	return rows, nil
}

func hostHasSuggestedManualCert(host *file.Host) bool {
	if host == nil || host.Scheme == "http" || host.HttpsJustProxy {
		return false
	}
	if strings.TrimSpace(host.CertFile) == "" || strings.TrimSpace(host.KeyFile) == "" {
		return false
	}
	expireAt, err := common.LoadCertExpireAt(host.CertFile, host.KeyFile)
	if err != nil {
		return false
	}
	return time.Now().Before(expireAt)
}

func suggestedReusableCertHosts(hosts []*file.Host) []*file.Host {
	if len(hosts) == 0 {
		return nil
	}
	candidates := make([]*file.Host, 0, len(hosts))
	for _, host := range hosts {
		if hostHasSuggestedManualCert(host) {
			candidates = append(candidates, host)
		}
	}
	return candidates
}

func (a *App) listNodeHosts(c Context, access nodeActorAccess) ([]nodeHostResourcePayload, int, error) {
	visibility, ok, err := access.visibility(a)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, 0, nil
	}
	rows, total := a.Services.Index.ListHosts(nodeListQueryFromContext(c).hostsInput(visibility))
	items := a.sanitizeNodeHosts(access.actor, access.scope, rows)
	return items, adjustedNodeListTotal(total, len(rows), len(items)), nil
}

func (a *App) sanitizeNodeHosts(actor *Actor, scope webservice.NodeAccessScope, hosts []*file.Host) []nodeHostResourcePayload {
	if len(hosts) == 0 {
		return nil
	}
	items := make([]nodeHostResourcePayload, 0, len(hosts))
	for _, host := range hosts {
		if host == nil {
			continue
		}
		items = append(items, a.nodeHostResource(a.sanitizeNodeHost(actor, scope, host)))
	}
	return items
}

func (a *App) sanitizeNodeHost(actor *Actor, scope webservice.NodeAccessScope, host *file.Host) *file.Host {
	if host == nil {
		return nil
	}
	host = webservice.CloneHostSnapshot(host)
	if host.Client != nil {
		host.Client = sanitizeNodeClient(scope, host.Client)
	}
	if !a.nodeActorAllows(actor, webservice.PermissionHostsUpdate) {
		if host.UserAuth != nil {
			host.UserAuth.Content = ""
			host.UserAuth.AccountMap = nil
		}
		host.CertFile = ""
		host.KeyFile = ""
	}
	return host
}

func (a *App) nodeHostResource(host *file.Host) nodeHostResourcePayload {
	payload := nodeHostResourcePayload{}
	if host == nil {
		return payload
	}
	payload.ID = host.Id
	payload.Revision = host.Revision
	payload.UpdatedAt = host.UpdatedAt
	if host.Client != nil {
		payload.ClientID = host.Client.Id
		clientPayload := a.embeddedNodeClientResource(host.Client)
		payload.Client = &clientPayload
	}
	payload.Host = host.Host
	payload.Header = host.HeaderChange
	payload.RespHeader = host.RespHeaderChange
	payload.HostChange = host.HostChange
	payload.Location = host.Location
	payload.PathRewrite = host.PathRewrite
	payload.Remark = host.Remark
	payload.Scheme = host.Scheme
	payload.RedirectURL = host.RedirectURL
	payload.HttpsJustProxy = host.HttpsJustProxy
	payload.TlsOffload = host.TlsOffload
	payload.AutoSSL = host.AutoSSL
	payload.CertType = host.CertType
	payload.CertHash = host.CertHash
	payload.CertFile = host.CertFile
	payload.KeyFile = host.KeyFile
	payload.IsClose = host.IsClose
	payload.AutoHttps = host.AutoHttps
	payload.AutoCORS = host.AutoCORS
	payload.CompatMode = host.CompatMode
	payload.EntryACLMode = host.EntryAclMode
	payload.EntryACLRules = host.EntryAclRules
	payload.TargetIsHttps = host.TargetIsHttps
	payload.ExpireAt = host.EffectiveExpireAt()
	payload.FlowLimitTotalBytes = host.EffectiveFlowLimitBytes()
	payload.RateLimitTotalBps = webservice.ManagementRateLimitToBps(host.RateLimit)
	payload.MaxConnections = host.MaxConn
	payload.NowConn = int(host.NowConn)
	if host.Target != nil {
		payload.Target = host.Target.TargetStr
		payload.ProxyProtocol = host.Target.ProxyProtocol
		payload.LocalProxy = host.Target.LocalProxy
	}
	if host.UserAuth != nil {
		payload.Auth = host.UserAuth.Content
	}
	payload.ServiceInBytes, payload.ServiceOutBytes, payload.ServiceTotalBytes = host.ServiceTrafficTotals()
	payload.NowRateInBps, payload.NowRateOutBps, payload.NowRateTotalBps = host.ServiceRateTotals()
	return payload
}

func (a *App) finishNodeHostMutation(c Context, action, eventName string, id int, host *file.Host, overrides map[string]interface{}) {
	a.emitNodeResourceMutationEvent(c, eventName, "host", action, nodeResourceMutationFields(id, hostEventFields(host), overrides))
	a.respondCompletedNodeMutation(c, "host", action, id, a.nodeHostResource(host))
}

func (a *App) NodeCreateHost(c Context) {
	access := a.nodeActorAccessFromContext(c)
	var body nodeHostWriteRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}
	clientID, err := access.authorize(a, c, webservice.ProtectedActionAccessInput{
		Permission:        webservice.PermissionHostsCreate,
		ClientScope:       true,
		RequestedClientID: body.ClientID,
	})
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	if clientID > 0 {
		c.SetParam("client_id", strconv.Itoa(clientID))
	}
	effectiveClientID := body.ClientID
	if clientID > 0 {
		effectiveClientID = clientID
	}
	if effectiveClientID <= 0 {
		respondMissingRequestField(c, "client_id")
		return
	}
	result, err := a.Services.Index.AddHost(webservice.BuildAddHostInput(webservice.IndexMutationContext{
		Principal:       access.principal,
		Authz:           a.authorization(),
		AllowLocalProxy: a.currentConfig().Feature.AllowLocalProxy,
		AllowUserLocal:  a.currentConfig().Feature.AllowUserLocal,
	}, webservice.AddHostRequest{
		HostWriteRequest: webservice.HostWriteRequest{
			ClientID:       effectiveClientID,
			Host:           body.Host,
			Target:         body.Target,
			ProxyProtocol:  body.ProxyProtocol,
			LocalProxy:     body.LocalProxy,
			Auth:           body.Auth,
			Header:         body.Header,
			RespHeader:     body.RespHeader,
			HostChange:     body.HostChange,
			Remark:         body.Remark,
			Location:       body.Location,
			PathRewrite:    body.PathRewrite,
			RedirectURL:    body.RedirectURL,
			FlowLimit:      body.FlowLimitTotalBytes,
			TimeLimit:      body.ExpireAt,
			RateLimit:      webservice.ManagementRateLimitFromBps(body.RateLimitTotalBps),
			MaxConnections: body.MaxConnections,
			EntryACLMode:   body.EntryACLMode,
			EntryACLRules:  body.EntryACLRules,
			Scheme:         body.Scheme,
			HTTPSJustProxy: body.HTTPSJustProxy,
			TLSOffload:     body.TLSOffload,
			AutoSSL:        body.AutoSSL,
			KeyFile:        body.KeyFile,
			CertFile:       body.CertFile,
			AutoHTTPS:      body.AutoHTTPS,
			AutoCORS:       body.AutoCORS,
			CompatMode:     body.CompatMode,
			TargetIsHTTPS:  body.TargetIsHTTPS,
		},
	}))
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	a.finishNodeHostMutation(c, "create", "host.created", result.ID, a.sanitizeNodeHost(access.actor, access.scope, result.Host), map[string]interface{}{"client_id": result.ClientID})
}

func (a *App) NodeUpdateHost(c Context) {
	access := a.nodeActorAccessFromContext(c)
	id := requestIntValue(c, "id")
	var body nodeHostWriteRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}
	if _, err := access.authorize(a, c, webservice.ProtectedActionAccessInput{
		Permission: webservice.PermissionHostsUpdate,
		Ownership:  "host",
		ResourceID: id,
	}); err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	if body.ClientID > 0 {
		if err := authorizeRequestedNodeClientWithAccess(a, c, access, body.ClientID); err != nil {
			respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
			return
		}
	}
	var syncHostIDs []int
	if body.SyncCertToMatchingHosts {
		var syncErr error
		syncHostIDs, syncErr = editableNodeHostIDs(a, access)
		if syncErr != nil {
			respondNodeMutationData(c, nodeResourceMutationPayload{}, syncErr)
			return
		}
	}
	result, err := a.Services.Index.EditHost(webservice.BuildEditHostInput(webservice.IndexMutationContext{
		Principal:       access.principal,
		Authz:           a.authorization(),
		AllowLocalProxy: a.currentConfig().Feature.AllowLocalProxy,
		AllowUserLocal:  a.currentConfig().Feature.AllowUserLocal,
	}, webservice.EditHostRequest{
		ID:               id,
		ExpectedRevision: body.ExpectedRevision,
		HostWriteRequest: webservice.HostWriteRequest{
			ClientID:       body.ClientID,
			Host:           body.Host,
			Target:         body.Target,
			ProxyProtocol:  body.ProxyProtocol,
			LocalProxy:     body.LocalProxy,
			Auth:           body.Auth,
			Header:         body.Header,
			RespHeader:     body.RespHeader,
			HostChange:     body.HostChange,
			Remark:         body.Remark,
			Location:       body.Location,
			PathRewrite:    body.PathRewrite,
			RedirectURL:    body.RedirectURL,
			FlowLimit:      body.FlowLimitTotalBytes,
			TimeLimit:      body.ExpireAt,
			RateLimit:      webservice.ManagementRateLimitFromBps(body.RateLimitTotalBps),
			MaxConnections: body.MaxConnections,
			EntryACLMode:   body.EntryACLMode,
			EntryACLRules:  body.EntryACLRules,
			Scheme:         body.Scheme,
			HTTPSJustProxy: body.HTTPSJustProxy,
			TLSOffload:     body.TLSOffload,
			AutoSSL:        body.AutoSSL,
			KeyFile:        body.KeyFile,
			CertFile:       body.CertFile,
			AutoHTTPS:      body.AutoHTTPS,
			AutoCORS:       body.AutoCORS,
			CompatMode:     body.CompatMode,
			TargetIsHTTPS:  body.TargetIsHTTPS,
		},
		ResetFlow:               body.ResetFlow,
		SyncCertToMatchingHosts: body.SyncCertToMatchingHosts,
		SyncHostIDs:             syncHostIDs,
	}))
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	a.finishNodeHostMutation(c, "update", "host.updated", result.ID, a.sanitizeNodeHost(access.actor, access.scope, result.Host), map[string]interface{}{"client_id": result.ClientID})
}

func (a *App) NodeDeleteHost(c Context) {
	access := a.nodeActorAccessFromContext(c)
	id := requestIntValue(c, "id")
	if _, err := access.authorize(a, c, webservice.ProtectedActionAccessInput{
		Permission: webservice.PermissionHostsDelete,
		Ownership:  "host",
		ResourceID: id,
	}); err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	result, err := a.Services.Index.DeleteHost(id)
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	a.finishNodeHostMutation(c, "delete", "host.deleted", result.ID, a.sanitizeNodeHost(access.actor, access.scope, result.Host), nil)
}

func (a *App) NodeStartHost(c Context) { a.nodeHostStateChange(c, "start") }
func (a *App) NodeStopHost(c Context)  { a.nodeHostStateChange(c, "stop") }
func (a *App) NodeClearHost(c Context) { a.nodeHostStateChange(c, "clear") }

func (a *App) nodeHostStateChange(c Context, action string) {
	access := a.nodeActorAccessFromContext(c)
	id := requestIntValue(c, "id")
	var body nodeModeActionRequest
	if !decodeOptionalCanonicalJSONObject(c, &body) {
		return
	}
	if _, err := access.authorize(a, c, webservice.ProtectedActionAccessInput{
		Permission: webservice.PermissionHostsControl,
		Ownership:  "host",
		ResourceID: id,
	}); err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	mode := strings.TrimSpace(body.Mode)
	if action == "clear" && mode == "" {
		respondMissingRequestField(c, "mode")
		return
	}
	var (
		err    error
		result webservice.HostMutation
	)
	switch action {
	case "start":
		result, err = a.Services.Index.StartHost(id, mode)
	case "stop":
		result, err = a.Services.Index.StopHost(id, mode)
	default:
		result, err = a.Services.Index.ClearHost(id, mode)
	}
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	host := a.sanitizeNodeHost(access.actor, access.scope, result.Host)
	fields := map[string]interface{}{}
	if mode != "" {
		fields["mode"] = mode
	}
	a.finishNodeHostMutation(c, action, nodeResourceStateEventName("host", action), result.ID, host, fields)
}
