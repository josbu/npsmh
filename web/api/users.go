package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

type nodeResourceListPayload struct {
	ConfigEpoch string `json:"config_epoch,omitempty"`
	GeneratedAt int64  `json:"generated_at"`
	Offset      int    `json:"offset,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Returned    int    `json:"returned,omitempty"`
	Total       int    `json:"total"`
	HasMore     bool   `json:"has_more,omitempty"`
	Items       any    `json:"items"`
}

type nodeResourceItemPayload struct {
	ConfigEpoch string `json:"config_epoch,omitempty"`
	GeneratedAt int64  `json:"generated_at"`
	Item        any    `json:"item,omitempty"`
}

type nodeUserResourcePayload struct {
	ID                  int    `json:"id"`
	Username            string `json:"username"`
	Kind                string `json:"kind"`
	ExternalPlatformID  string `json:"external_platform_id,omitempty"`
	Hidden              bool   `json:"hidden"`
	Status              int    `json:"status"`
	ExpireAt            int64  `json:"expire_at"`
	FlowLimitTotalBytes int64  `json:"flow_limit_total_bytes"`
	TotalInBytes        int64  `json:"total_in_bytes"`
	TotalOutBytes       int64  `json:"total_out_bytes"`
	TotalBytes          int64  `json:"total_bytes"`
	NowRateInBps        int64  `json:"now_rate_in_bps"`
	NowRateOutBps       int64  `json:"now_rate_out_bps"`
	NowRateTotalBps     int64  `json:"now_rate_total_bps"`
	MaxClients          int    `json:"max_clients"`
	MaxTunnels          int    `json:"max_tunnels"`
	MaxHosts            int    `json:"max_hosts"`
	MaxConnections      int    `json:"max_connections"`
	RateLimitTotalBps   int    `json:"rate_limit_total_bps"`
	EntryACLMode        int    `json:"entry_acl_mode"`
	EntryACLRules       string `json:"entry_acl_rules,omitempty"`
	DestACLMode         int    `json:"dest_acl_mode"`
	DestACLRules        string `json:"dest_acl_rules,omitempty"`
	Revision            int64  `json:"revision"`
	UpdatedAt           int64  `json:"updated_at"`
	ClientCount         int    `json:"client_count,omitempty"`
	TunnelCount         int    `json:"tunnel_count,omitempty"`
	HostCount           int    `json:"host_count,omitempty"`
	ExpireAtText        string `json:"expire_at_text,omitempty"`
}

type nodeListQuery struct {
	Offset   int
	Limit    int
	ClientID int
	Search   string
	Sort     string
	Order    string
	Mode     string
	Host     string
}

func (a *App) NodeUsers(c Context) {
	access := a.nodeActorAccessFromContext(c)
	offset := requestIntValue(c, "offset")
	limit := requestIntValue(c, "limit")
	if !access.scope.IsFullAccess() {
		if items, handled, err := a.scopedNodeUserItemsWithAccess(access, requestString(c, "search")); err != nil {
			respondNodeResourceData(c, nodeResourceListPayload{}, err)
			return
		} else if handled {
			paged, total := paginateNodeItems(items, offset, limit)
			offset, limit, returned, hasMore := nodeListPagination(offset, limit, len(paged), total)
			respondNodeResourceData(c, nodeResourceListPayload{
				ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
				GeneratedAt: time.Now().Unix(),
				Offset:      offset,
				Limit:       limit,
				Returned:    returned,
				Total:       total,
				HasMore:     hasMore,
				Items:       paged,
			}, nil)
			return
		}
	}
	listInput := webservice.ListUsersInput{
		Offset: 0,
		Limit:  0,
		Search: requestString(c, "search"),
		Sort:   requestString(c, "sort"),
		Order:  requestString(c, "order"),
	}
	if access.scope.IsFullAccess() {
		listInput.Offset = offset
		listInput.Limit = limit
	}
	result := a.Services.Users.List(listInput)
	payloadItems := make([]nodeUserResourcePayload, 0, len(result.Rows))
	for _, row := range result.Rows {
		if row == nil || row.User == nil || !access.scope.AllowsUser(row.User) {
			continue
		}
		payloadItems = append(payloadItems, newNodeUserResourceFromRow(row))
	}
	if access.scope.IsFullAccess() {
		total := adjustedNodeListTotal(result.Total, len(result.Rows), len(payloadItems))
		offset, limit, returned, hasMore := nodeListPagination(offset, limit, len(payloadItems), total)
		respondNodeResourceData(c, nodeResourceListPayload{
			ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
			GeneratedAt: time.Now().Unix(),
			Offset:      offset,
			Limit:       limit,
			Returned:    returned,
			Total:       total,
			HasMore:     hasMore,
			Items:       payloadItems,
		}, nil)
		return
	}
	paged, total := paginateNodeItems(payloadItems, offset, limit)
	offset, limit, returned, hasMore := nodeListPagination(offset, limit, len(paged), total)
	respondNodeResourceData(c, nodeResourceListPayload{
		ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
		GeneratedAt: time.Now().Unix(),
		Offset:      offset,
		Limit:       limit,
		Returned:    returned,
		Total:       total,
		HasMore:     hasMore,
		Items:       paged,
	}, nil)
}

func (a *App) NodeUser(c Context) {
	access := a.nodeActorAccessFromContext(c)
	user, err := a.Services.Users.Get(requestIntValue(c, "id"))
	item := nodeUserResourcePayload{}
	if err == nil {
		switch {
		case user == nil:
			err = webservice.ErrUserNotFound
		case !access.scope.AllowsUser(user):
			err = webservice.ErrForbidden
		default:
			item = newNodeUserResource(user)
		}
	}
	respondNodeResourceData(c, nodeResourceItemPayload{
		ConfigEpoch: a.runtimeIdentity().ConfigEpoch(),
		GeneratedAt: time.Now().Unix(),
		Item:        item,
	}, err)
}

func (a *App) scopedNodeUserItems(actor *Actor, scope webservice.NodeAccessScope, search string) ([]nodeUserResourcePayload, bool, error) {
	access := a.nodeActorAccess(actor)
	access.scope = scope
	return a.scopedNodeUserItemsWithAccess(access, search)
}

func (a *App) scopedNodeUserItemsWithAccess(access nodeActorAccess, search string) ([]nodeUserResourcePayload, bool, error) {
	userID, ok := scopedNodeUserID(access.actor)
	if !ok {
		return nil, true, nil
	}
	user, err := a.Services.Users.Get(userID)
	switch {
	case err == nil:
	case errors.Is(err, webservice.ErrForbidden):
		return nil, true, nil
	case errors.Is(err, webservice.ErrUserNotFound):
		return nil, false, nil
	default:
		return nil, true, err
	}
	if user == nil || !access.scope.AllowsUser(user) || !matchesScopedNodeUserSearch(user, search) {
		return nil, true, nil
	}
	payload := newNodeUserResource(user)
	payload.ClientCount, payload.TunnelCount, payload.HostCount = a.nodeOwnedUserResourceCounts(user.Id)
	payload.ExpireAtText = formatNodeUserExpireAt(user.ExpireAt)
	return []nodeUserResourcePayload{payload}, true, nil
}

func scopedNodeUserID(actor *Actor) (int, bool) {
	if userID := NodeActorUserID(actor); userID > 0 {
		return userID, true
	}
	if userID := NodeActorServiceUserID(actor); userID > 0 {
		return userID, true
	}
	return 0, false
}

func matchesScopedNodeUserSearch(user *file.User, search string) bool {
	if user == nil {
		return false
	}
	search = strings.TrimSpace(search)
	if search == "" {
		return true
	}
	searchID := common.GetIntNoErrByStr(search)
	return user.Id == searchID || common.ContainsFold(user.Username, search)
}

func formatNodeUserExpireAt(expireAt int64) string {
	if expireAt <= 0 {
		return ""
	}
	return time.Unix(expireAt, 0).Format("2006-01-02 15:04:05")
}

func (a *App) nodeOwnedUserResourceCounts(userID int) (int, int, int) {
	if a == nil || userID <= 0 {
		return 0, 0, 0
	}
	return webservice.OwnedResourceCountsByUser(a.backend().Repository, userID)
}

func respondNodeResourceData(c Context, payload interface{}, err error) {
	switch {
	case errors.Is(err, webservice.ErrForbidden), errors.Is(err, webservice.ErrUnauthenticated):
		respondManagementError(c, nodeAccessErrorStatus(err), err)
	case errors.Is(err, webservice.ErrUserNotFound), errors.Is(err, webservice.ErrClientNotFound), errors.Is(err, webservice.ErrTunnelNotFound), errors.Is(err, webservice.ErrHostNotFound):
		respondManagementError(c, http.StatusNotFound, err)
	case err != nil:
		respondManagementError(c, http.StatusInternalServerError, err)
	default:
		switch value := payload.(type) {
		case nodeResourceListPayload:
			meta := managementResponseMeta(c, value.GeneratedAt, value.ConfigEpoch)
			meta.Pagination = &ManagementPagination{
				Offset:   value.Offset,
				Limit:    value.Limit,
				Returned: value.Returned,
				Total:    value.Total,
				HasMore:  value.HasMore,
			}
			respondManagementData(c, http.StatusOK, value.Items, meta)
		case nodeResourceItemPayload:
			respondManagementData(c, http.StatusOK, value.Item, managementResponseMeta(c, value.GeneratedAt, value.ConfigEpoch))
		default:
			respondManagementData(c, http.StatusOK, payload, managementResponseMeta(c, 0, ""))
		}
	}
}

func newNodeUserResourceFromRow(row *webservice.UserListRow) nodeUserResourcePayload {
	payload := newNodeUserResource(row.User)
	payload.ClientCount = row.ClientCount
	payload.TunnelCount = row.TunnelCount
	payload.HostCount = row.HostCount
	payload.ExpireAtText = row.ExpireAtText
	return payload
}

func newNodeUserResource(user *file.User) nodeUserResourcePayload {
	payload := nodeUserResourcePayload{}
	if user == nil {
		return payload
	}
	totalIn, totalOut, totalBytes := user.TotalTrafficTotals()
	nowRateIn, nowRateOut, nowRateTotal := user.TotalRateTotals()
	return nodeUserResourcePayload{
		ID:                  user.Id,
		Username:            user.Username,
		Kind:                user.Kind,
		ExternalPlatformID:  user.ExternalPlatformID,
		Hidden:              user.Hidden,
		Status:              user.Status,
		ExpireAt:            user.ExpireAt,
		FlowLimitTotalBytes: user.FlowLimit,
		TotalInBytes:        totalIn,
		TotalOutBytes:       totalOut,
		TotalBytes:          totalBytes,
		NowRateInBps:        nowRateIn,
		NowRateOutBps:       nowRateOut,
		NowRateTotalBps:     nowRateTotal,
		MaxClients:          user.MaxClients,
		MaxTunnels:          user.MaxTunnels,
		MaxHosts:            user.MaxHosts,
		MaxConnections:      user.MaxConnections,
		RateLimitTotalBps:   webservice.ManagementRateLimitToBps(user.RateLimit),
		EntryACLMode:        user.EntryAclMode,
		EntryACLRules:       user.EntryAclRules,
		DestACLMode:         user.DestAclMode,
		DestACLRules:        user.DestAclRules,
		Revision:            user.Revision,
		UpdatedAt:           user.UpdatedAt,
	}
}

func paginateNodeItems[T any](items []T, offset, limit int) ([]T, int) {
	total := len(items)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = total
	}
	if offset >= total {
		return []T{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total
}

func nodeListPagination(offset, limit, returned, total int) (int, int, int, bool) {
	if offset < 0 {
		offset = 0
	}
	if limit < 0 {
		limit = 0
	}
	if returned < 0 {
		returned = 0
	}
	if total < 0 {
		total = 0
	}
	hasMore := false
	if total > 0 {
		hasMore = offset+returned < total
	}
	return offset, limit, returned, hasMore
}

func adjustedNodeListTotal(total, pageRows, returned int) int {
	if total < 0 {
		total = 0
	}
	if pageRows < returned {
		pageRows = returned
	}
	dropped := pageRows - returned
	if dropped <= 0 {
		return total
	}
	if total < dropped {
		return returned
	}
	return total - dropped
}

func nodeListQueryFromContext(c Context) nodeListQuery {
	if c == nil {
		return nodeListQuery{}
	}
	return nodeListQuery{
		Offset:   requestIntValue(c, "offset"),
		Limit:    requestIntValue(c, "limit"),
		ClientID: requestIntValue(c, "client_id"),
		Search:   requestString(c, "search"),
		Sort:     requestString(c, "sort"),
		Order:    requestString(c, "order"),
		Mode:     requestString(c, "mode"),
		Host:     c.Host(),
	}
}

func (q nodeListQuery) clientsInput(visibility webservice.ClientVisibility) webservice.ListClientsInput {
	return webservice.ListClientsInput{
		Offset:     q.Offset,
		Limit:      q.Limit,
		Search:     q.Search,
		Sort:       q.Sort,
		Order:      q.Order,
		Host:       q.Host,
		Visibility: visibility,
	}
}

func (q nodeListQuery) hostsInput(visibility webservice.ClientVisibility) webservice.HostListInput {
	return webservice.HostListInput{
		Offset:     q.Offset,
		Limit:      q.Limit,
		ClientID:   q.ClientID,
		Search:     q.Search,
		Sort:       q.Sort,
		Order:      q.Order,
		Visibility: visibility,
	}
}

func (q nodeListQuery) tunnelsInput(visibility webservice.ClientVisibility) webservice.TunnelListInput {
	return webservice.TunnelListInput{
		Offset:     q.Offset,
		Limit:      q.Limit,
		Type:       q.Mode,
		ClientID:   q.ClientID,
		Search:     q.Search,
		Sort:       q.Sort,
		Order:      q.Order,
		Visibility: visibility,
	}
}

func (a *App) finishNodeUserMutation(c Context, action, eventName string, id int, user *file.User, overrides map[string]interface{}) {
	a.emitNodeResourceMutationEvent(c, eventName, "user", action, nodeResourceMutationFields(id, userEventFields(user), overrides))
	a.respondCompletedNodeMutation(c, "user", action, id, newNodeUserResource(user))
}

func (a *App) NodeCreateUser(c Context) {
	var body nodeUserWriteRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}
	status := true
	if body.Status != nil {
		status = *body.Status
	}
	result, err := a.Services.Users.Add(webservice.AddUserInput{
		ReservedAdminUsername: a.currentConfig().Web.Username,
		Username:              body.Username,
		Password:              nodeMutationStringValue(body.Password),
		TOTPSecret:            nodeMutationStringValue(body.TOTPSecret),
		Status:                status,
		ExpireAt:              body.ExpireAt,
		FlowLimit:             body.FlowLimitTotalBytes,
		MaxClients:            body.MaxClients,
		MaxTunnels:            body.MaxTunnels,
		MaxHosts:              body.MaxHosts,
		MaxConnections:        body.MaxConnections,
		RateLimit:             webservice.ManagementRateLimitFromBps(body.RateLimitTotalBps),
		EntryACLMode:          body.EntryACLMode,
		EntryACLRules:         body.EntryACLRules,
		DestACLMode:           body.DestACLMode,
		DestACLRules:          body.DestACLRules,
	})
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	a.finishNodeUserMutation(c, "create", "user.created", result.ID, result.User, nil)
}

func (a *App) NodeUpdateUser(c Context) {
	id := requestIntValue(c, "id")
	var body nodeUserWriteRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}
	status := false
	if body.Status != nil {
		status = *body.Status
	}
	result, err := a.Services.Users.Edit(webservice.EditUserInput{
		ID:                    id,
		ReservedAdminUsername: a.currentConfig().Web.Username,
		ExpectedRevision:      body.ExpectedRevision,
		Username:              body.Username,
		Password:              nodeMutationStringValue(body.Password),
		PasswordProvided:      body.Password != nil,
		TOTPSecret:            nodeMutationStringValue(body.TOTPSecret),
		TOTPSecretProvided:    body.TOTPSecret != nil,
		Status:                status,
		StatusProvided:        body.Status != nil,
		ExpireAt:              body.ExpireAt,
		FlowLimit:             body.FlowLimitTotalBytes,
		MaxClients:            body.MaxClients,
		MaxTunnels:            body.MaxTunnels,
		MaxHosts:              body.MaxHosts,
		MaxConnections:        body.MaxConnections,
		RateLimit:             webservice.ManagementRateLimitFromBps(body.RateLimitTotalBps),
		ResetFlow:             body.ResetFlow,
		EntryACLMode:          body.EntryACLMode,
		EntryACLRules:         body.EntryACLRules,
		DestACLMode:           body.DestACLMode,
		DestACLRules:          body.DestACLRules,
	})
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	a.finishNodeUserMutation(c, "update", "user.updated", result.ID, result.User, nil)
}

func (a *App) NodeSetUserStatus(c Context) {
	id := requestIntValue(c, "id")
	var body nodeStatusActionRequest
	if !decodeCanonicalJSONObject(c, &body) {
		return
	}
	if body.Status == nil {
		respondMissingRequestField(c, "status")
		return
	}
	status := *body.Status
	result, err := a.Services.Users.ChangeStatus(id, status)
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	user := result.User
	statusFields := map[string]interface{}{"status": status}
	if user != nil {
		statusFields["status"] = user.Status
	}
	a.finishNodeUserMutation(c, "status", "user.status_changed", result.ID, user, statusFields)
}

func (a *App) NodeDeleteUser(c Context) {
	id := requestIntValue(c, "id")
	result, err := a.Services.Users.Delete(id)
	if err != nil {
		respondNodeMutationData(c, nodeResourceMutationPayload{}, err)
		return
	}
	a.finishNodeUserMutation(c, "delete", "user.deleted", result.ID, result.User, nil)
}
