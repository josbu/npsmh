package routers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/djylb/nps/server"
	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

const nodeBatchMaxItems = webservice.NodeBatchDefaultMaxItems

type nodeBatchRequest = webservice.NodeBatchRequest

type nodeBatchRequestItem = webservice.NodeBatchRequestItem

type nodeBatchResponsePayload struct {
	OperationID  string        `json:"operation_id,omitempty"`
	Count        int           `json:"count"`
	SuccessCount int           `json:"success_count"`
	ErrorCount   int           `json:"error_count"`
	Items        []nodeWSFrame `json:"items"`
}

type nodeConfigImportResponse struct {
	Status      int    `json:"status"`
	Msg         string `json:"msg"`
	ConfigEpoch string `json:"config_epoch,omitempty"`
	ImportedAt  int64  `json:"imported_at,omitempty"`
}

type nodeConfigImportEnvelope struct {
	Status int             `json:"status"`
	Msg    string          `json:"msg"`
	Data   json.RawMessage `json:"data"`
}

var errNodeBatchStateUnavailable = errors.New("node state is unavailable")

type nodeWSDispatchRequest struct {
	state    *State
	base     nodeWSDispatchBase
	actor    *webapi.Actor
	frame    nodeWSFrame
	response nodeWSFrame
	ctx      *wsAPIContext
	metadata webapi.RequestMetadata
	path     string
	method   string
	query    string
}

type nodeWSManagedRouteKind int

const (
	nodeWSRouteUnknown nodeWSManagedRouteKind = iota
	nodeWSRouteChanges
	nodeWSRouteCallbackQueue
	nodeWSRouteConfigImport
	nodeWSRouteBatch
	nodeWSRouteCallbackQueueReplay
	nodeWSRouteCallbackQueueClear
	nodeWSRouteWebhookCollection
	nodeWSRouteWebhookItem
	nodeWSRouteRealtimeSubscriptionCollection
	nodeWSRouteRealtimeSubscriptionItem
)

type nodeWSPrefixRequestRoute struct {
	prefix string
	kind   nodeWSManagedRouteKind
}

var nodeWSExactRequestRoutes = map[string]nodeWSManagedRouteKind{
	"/system/changes":                 nodeWSRouteChanges,
	"/callbacks/queue":                nodeWSRouteCallbackQueue,
	"/system/import":                  nodeWSRouteConfigImport,
	"/batch":                          nodeWSRouteBatch,
	"/callbacks/queue/actions/replay": nodeWSRouteCallbackQueueReplay,
	"/callbacks/queue/actions/clear":  nodeWSRouteCallbackQueueClear,
	"/webhooks":                       nodeWSRouteWebhookCollection,
	"/realtime/subscriptions":         nodeWSRouteRealtimeSubscriptionCollection,
}

var nodeWSPrefixRequestRoutes = []nodeWSPrefixRequestRoute{
	{prefix: "/webhooks/", kind: nodeWSRouteWebhookItem},
	{prefix: "/realtime/subscriptions/", kind: nodeWSRouteRealtimeSubscriptionItem},
}

func dispatchNodeWSRequestWithBase(state *State, base nodeWSDispatchBase, actor *webapi.Actor, frame nodeWSFrame) nodeWSFrame {
	response := nodeWSFrame{
		ID:        frame.ID,
		Type:      "response",
		Timestamp: time.Now().Unix(),
	}
	if state == nil || state.App == nil {
		response.Status = http.StatusInternalServerError
		response.Error = "node state is unavailable"
		return response
	}
	if isNilRouterServiceValue(base.Resolver) {
		base.Resolver = state.PermissionResolver()
	}
	metadata := base.Metadata
	if strings.TrimSpace(metadata.Source) == "" {
		metadata.Source = "node-ws"
	} else {
		metadata.Source = strings.TrimSpace(metadata.Source) + ":ws"
	}
	metadata.RequestID = resolveRequestID(
		frameHeaderValue(frame.Headers, "X-Request-ID"),
		frameHeaderValue(frame.Headers, "X-Correlation-ID"),
		frame.ID,
		metadata.RequestID,
	)
	base.Metadata = metadata
	ctx, err := newWSAPIContextFromBase(base, actor, frame)
	if err != nil {
		response.Status = http.StatusBadRequest
		response.Error = "invalid request body"
		return response
	}
	if ctx.method == "" {
		ctx.method = http.MethodGet
	}
	resolved, err := resolveNodeWSRequest(state.CurrentConfig(), ctx.Method(), frame.Path)
	if err != nil {
		response.Status = http.StatusBadRequest
		response.Error = err.Error()
		return response
	}
	if strings.TrimSpace(resolved.CanonicalPath) == "" {
		response.Status = http.StatusNotFound
		response.Error = "unknown ws request path"
		return response
	}
	path := resolved.CanonicalPath
	query := resolved.CanonicalQuery
	effectiveMethod := resolved.CanonicalMethod
	if effectiveMethod == "" {
		effectiveMethod = ctx.Method()
	}
	request := &nodeWSDispatchRequest{
		state:    state,
		base:     base,
		actor:    actor,
		frame:    frame,
		response: response,
		ctx:      ctx,
		metadata: metadata,
		path:     path,
		method:   effectiveMethod,
		query:    query,
	}
	idempotencyKey := nodeWSRequestIdempotencyKey(frame)
	idempotency := nodeIdempotencyAcquireResult{}
	if state.Idempotency != nil && idempotencyKey != "" && nodeWSWriteRequest(effectiveMethod) {
		idempotency = state.Idempotency.acquireContext(
			ctx.BaseContext(),
			nodeWSIdempotencyScope(actor, effectiveMethod, path, query),
			idempotencyKey,
			nodeWSIdempotencyFingerprint(effectiveMethod, path, query, frame.Body),
		)
		switch {
		case idempotency.err != nil:
			status, _, message := nodeIdempotencyAcquireError(idempotency.err)
			response.Status = status
			response.Error = message
			response.Headers = requestMetadataResponseHeaders(webapi.RequestMetadata{RequestID: metadata.RequestID})
			return response
		case idempotency.conflict:
			response.Status = http.StatusConflict
			response.Error = "idempotency key already used with a different request"
			response.Headers = requestMetadataResponseHeaders(webapi.RequestMetadata{RequestID: metadata.RequestID})
			return response
		case idempotency.wsResp != nil:
			cached := cloneNodeWSFrame(idempotency.wsResp)
			if cached == nil {
				break
			}
			cached.ID = frame.ID
			if cached.Headers == nil {
				cached.Headers = map[string]string{}
			}
			if strings.TrimSpace(metadata.RequestID) != "" {
				cached.Headers["X-Request-ID"] = metadata.RequestID
			}
			cached.Headers["X-Idempotent-Replay"] = "true"
			return *cached
		}
	}
	finalize := func(frame nodeWSFrame) nodeWSFrame {
		if idempotency.entry != nil {
			state.Idempotency.completeWS(
				nodeWSIdempotencyScope(actor, effectiveMethod, path, query),
				idempotencyKey,
				idempotency.entry,
				&frame,
			)
		}
		return frame
	}

	if directResponse, handled := dispatchNodeWSDirectRoute(request); handled {
		return finalize(directResponse)
	}

	if routedResponse, handled := dispatchNodeWSManagedRoute(request); handled {
		return finalize(routedResponse)
	}

	response.Status = http.StatusNotFound
	response.Error = "unknown ws request path"
	return finalize(response)
}

type nodeWSResolvedRequest struct {
	CanonicalPath   string
	CanonicalMethod string
	CanonicalQuery  string
}

type nodeWSDirectRouteSpec struct {
	action    webapi.ActionSpec
	operation string
}

func requestMetadataResponseHeaders(metadata webapi.RequestMetadata) map[string]string {
	headers := make(map[string]string)
	if requestID := strings.TrimSpace(metadata.RequestID); requestID != "" {
		headers["X-Request-ID"] = requestID
	}
	return headers
}

func buildNodeWSManagementDataFrame(frame nodeWSFrame, metadata webapi.RequestMetadata, configEpoch string, timestamp int64, data interface{}) nodeWSFrame {
	body, _ := json.Marshal(webapi.ManagementDataResponse{
		Data: data,
		Meta: webapi.ManagementResponseMetaForRequest(metadata, timestamp, configEpoch),
	})
	frame.Status = http.StatusOK
	frame.Error = ""
	frame.Body = body
	frame.Headers = requestMetadataResponseHeaders(metadata)
	return frame
}

func setNodeWSFrameHeader(frame *nodeWSFrame, key, value string) {
	if frame == nil {
		return
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return
	}
	if frame.Headers == nil {
		frame.Headers = make(map[string]string)
	}
	frame.Headers[key] = value
}

var (
	errNodeWSManagedItemUnknownPath = errors.New("unknown ws request path")
	errNodeWSManagedItemInvalidID   = errors.New("invalid managed item id")
	errNodeWSHandlerUnavailable     = errors.New("node handler is unavailable")
)

type nodeWSManagedItemRequest struct {
	ID     int64
	Action string
}

type nodeWSManagedCollectionMethodSpec struct {
	method  string
	handler func() nodeWSFrame
}

type nodeWSManagedItemActionSpec struct {
	action  string
	method  string
	handler func(nodeWSManagedItemRequest) nodeWSFrame
}

func resolveNodeWSManagedItemRequest(path, prefix string) (nodeWSManagedItemRequest, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(path), strings.TrimSpace(prefix))
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return nodeWSManagedItemRequest{}, errNodeWSManagedItemUnknownPath
	}
	id, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || id <= 0 {
		return nodeWSManagedItemRequest{}, errNodeWSManagedItemInvalidID
	}
	request := nodeWSManagedItemRequest{ID: id}
	switch {
	case len(parts) == 1:
		return request, nil
	case len(parts) == 3 && parts[1] == "actions" && strings.TrimSpace(parts[2]) != "":
		request.Action = strings.TrimSpace(parts[2])
		return request, nil
	default:
		return nodeWSManagedItemRequest{}, errNodeWSManagedItemUnknownPath
	}
}

func resolveNodeWSManagedCollectionMethod(method string, specs []nodeWSManagedCollectionMethodSpec) (func() nodeWSFrame, bool) {
	method = strings.ToUpper(strings.TrimSpace(method))
	for _, spec := range specs {
		if spec.method == method {
			return spec.handler, true
		}
	}
	return nil, false
}

func resolveNodeWSManagedItemAction(action, method string, specs []nodeWSManagedItemActionSpec) (func(nodeWSManagedItemRequest) nodeWSFrame, bool, bool) {
	action = strings.TrimSpace(action)
	method = strings.ToUpper(strings.TrimSpace(method))
	pathMatched := false
	for _, spec := range specs {
		if spec.action != action {
			continue
		}
		pathMatched = true
		if spec.method == method {
			return spec.handler, true, true
		}
	}
	return nil, pathMatched, false
}

func buildNodeWSMethodNotAllowedManagementFrame(frame nodeWSFrame, ctx *wsAPIContext) nodeWSFrame {
	return buildNodeWSManagementErrorFrame(frame, ctx, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}

func buildNodeWSUnknownPathManagementFrame(frame nodeWSFrame, ctx *wsAPIContext) nodeWSFrame {
	return buildNodeWSManagementErrorFrame(frame, ctx, http.StatusNotFound, "unknown_ws_request_path", "unknown ws request path", nil)
}

func buildNodeWSManagedItemLookupErrorFrame(frame nodeWSFrame, ctx *wsAPIContext, err error, invalidCode, invalidMessage string) nodeWSFrame {
	if errors.Is(err, errNodeWSManagedItemInvalidID) {
		return buildNodeWSManagementErrorFrame(frame, ctx, http.StatusBadRequest, invalidCode, invalidMessage, nil)
	}
	return buildNodeWSUnknownPathManagementFrame(frame, ctx)
}

func nodeWSManagedRouteKindForProtectedAction(spec webapi.ActionSpec) (nodeWSManagedRouteKind, bool) {
	switch nodeProtectedActionKey(spec) {
	case "system/changes":
		return nodeWSRouteChanges, true
	case "system/import":
		return nodeWSRouteConfigImport, true
	case "callbacks_queue/list":
		return nodeWSRouteCallbackQueue, true
	case "callbacks_queue/replay":
		return nodeWSRouteCallbackQueueReplay, true
	case "callbacks_queue/clear":
		return nodeWSRouteCallbackQueueClear, true
	case "webhooks/list", "webhooks/create":
		return nodeWSRouteWebhookCollection, true
	case "webhooks/read", "webhooks/update", "webhooks/status", "webhooks/delete":
		return nodeWSRouteWebhookItem, true
	default:
		return nodeWSRouteUnknown, false
	}
}

func nodeWSDirectRouteSpecFromAction(spec webapi.ActionSpec) (nodeWSDirectRouteSpec, bool) {
	if _, managed := nodeWSManagedRouteKindForProtectedAction(spec); managed {
		return nodeWSDirectRouteSpec{}, false
	}
	if _, ok := nodeWSResourceRouteSpecFromAction(spec); ok {
		return nodeWSDirectRouteSpec{}, false
	}
	if strings.TrimSpace(nodeProtectedActionRelativePath(spec.Path)) == "" {
		return nodeWSDirectRouteSpec{}, false
	}
	return nodeWSDirectRouteSpec{
		action:    spec,
		operation: nodeProtectedActionAuditKind(spec),
	}, true
}

func resolveNodeWSDirectRoute(state *State, path, method string) (nodeWSDirectRouteSpec, bool, bool) {
	if state == nil || state.App == nil {
		return nodeWSDirectRouteSpec{}, false, false
	}
	path = strings.TrimSpace(path)
	method = strings.ToUpper(strings.TrimSpace(method))
	pathMatched := false
	for _, action := range state.ProtectedActions {
		spec, ok := nodeWSDirectRouteSpecFromAction(action)
		if !ok || nodeProtectedActionRelativePath(spec.action.Path) != path {
			continue
		}
		pathMatched = true
		if strings.EqualFold(strings.TrimSpace(spec.action.Method), method) {
			return spec, true, true
		}
	}
	return nodeWSDirectRouteSpec{}, pathMatched, false
}

func dispatchNodeWSManagedRoute(request *nodeWSDispatchRequest) (nodeWSFrame, bool) {
	if request == nil {
		return nodeWSFrame{}, false
	}
	if kind, ok := nodeWSExactRequestRoutes[strings.TrimSpace(request.path)]; ok {
		return dispatchNodeWSManagedRouteKind(request, kind), true
	}
	for _, route := range nodeWSPrefixRequestRoutes {
		if strings.HasPrefix(request.path, route.prefix) {
			return dispatchNodeWSManagedRouteKind(request, route.kind), true
		}
	}
	if nodeWSResourceRequestPath(request.path) {
		return dispatchNodeWSResourceRequest(request), true
	}
	return nodeWSFrame{}, false
}

func dispatchNodeWSManagedRouteKind(request *nodeWSDispatchRequest, kind nodeWSManagedRouteKind) nodeWSFrame {
	switch kind {
	case nodeWSRouteChanges:
		return dispatchNodeWSChangesRoute(request)
	case nodeWSRouteCallbackQueue:
		return dispatchNodeWSCallbackQueueRoute(request)
	case nodeWSRouteConfigImport:
		return dispatchNodeWSConfigImportRoute(request)
	case nodeWSRouteBatch:
		return dispatchNodeWSBatchRoute(request)
	case nodeWSRouteCallbackQueueReplay:
		return dispatchNodeWSCallbackQueueReplayRoute(request)
	case nodeWSRouteCallbackQueueClear:
		return dispatchNodeWSCallbackQueueClearRoute(request)
	case nodeWSRouteWebhookCollection:
		return dispatchNodeWSWebhookCollectionRoute(request)
	case nodeWSRouteWebhookItem:
		return dispatchNodeWSWebhookItemRoute(request)
	case nodeWSRouteRealtimeSubscriptionCollection:
		return dispatchNodeWSRealtimeSubscriptionCollectionRoute(request)
	case nodeWSRouteRealtimeSubscriptionItem:
		return dispatchNodeWSRealtimeSubscriptionItemRoute(request)
	default:
		return request.response
	}
}

func dispatchNodeWSDirectRoute(request *nodeWSDispatchRequest) (nodeWSFrame, bool) {
	if request == nil {
		return nodeWSFrame{}, false
	}
	spec, pathMatched, methodMatched := resolveNodeWSDirectRoute(request.state, request.path, request.method)
	if !pathMatched {
		return request.response, false
	}
	if !methodMatched {
		return buildNodeWSMethodNotAllowedManagementFrame(request.response, request.ctx), true
	}
	if request.state == nil || request.state.App == nil {
		return buildNodeWSManagementErrorFrame(request.response, request.ctx, http.StatusInternalServerError, "node_state_unavailable", "node state is unavailable", nil), true
	}
	return executeNodeWSRouteExecution(request, nodeWSRouteExecution{
		operation:  spec.operation,
		actionSpec: &spec.action,
		handler:    spec.action.Handler,
	}), true
}

type nodeWSOperation struct {
	state       *State
	ctx         *wsAPIContext
	kind        string
	operationID string
	startedAt   time.Time
	paths       []string
}

func startNodeWSOperation(state *State, ctx *wsAPIContext, kind string, paths []string) nodeWSOperation {
	operation := nodeWSOperation{
		state: state,
		ctx:   ctx,
		kind:  strings.TrimSpace(kind),
		paths: append([]string(nil), paths...),
	}
	if operation.kind == "" || ctx == nil {
		return operation
	}
	operation.startedAt = time.Now()
	operation.operationID = currentNodeWSOperationID(ctx)
	setNodeWSOperationHeader(ctx, operation.operationID)
	return operation
}

func (o nodeWSOperation) record(status int, err error) {
	if o.kind == "" {
		return
	}
	recordNodeWSOperationStatus(o.state, o.ctx, o.operationID, o.kind, o.startedAt, status, err, o.paths)
}

func (o nodeWSOperation) setFrameHeader(frame *nodeWSFrame) {
	if frame == nil {
		return
	}
	setNodeWSFrameHeader(frame, "X-Operation-ID", o.operationID)
}

type nodeWSRouteExecution struct {
	operation  string
	actionSpec *webapi.ActionSpec
	handler    webapi.Handler
}

func recordNodeWSOperationStatus(state *State, ctx *wsAPIContext, operationID, kind string, startedAt time.Time, status int, err error, paths []string) {
	if state == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return
	}
	if err == nil {
		err = nodeOperationErrorFromStatus(status)
	}
	state.RuntimeStatus().NoteOperation(kind, err)
	if ctx == nil {
		return
	}
	recordNodeOperation(
		state,
		ctx.Actor(),
		ctx.Metadata(),
		operationID,
		kind,
		startedAt,
		1,
		nodeOperationSuccessCount(status),
		nodeOperationErrorCount(status),
		paths,
	)
}

func executeNodeWSRouteExecution(request *nodeWSDispatchRequest, execution nodeWSRouteExecution) nodeWSFrame {
	operation := startNodeWSOperation(request.state, request.ctx, execution.operation, []string{canonicalNodeOperationPath(request.state, request.frame.Path)})
	if err := requireNodeWSRouteExecutionAccess(request, execution); err != nil {
		operation.record(permissionStatus(err), err)
		writeWSError(request.ctx, permissionStatus(err), accessErrorMessage(err))
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	if execution.handler == nil {
		operation.record(http.StatusInternalServerError, errNodeWSHandlerUnavailable)
		return buildNodeWSManagementErrorFrame(request.response, request.ctx, http.StatusInternalServerError, "node_handler_unavailable", "node handler is unavailable", nil)
	}
	execution.handler(request.ctx)
	operation.record(request.ctx.status, nil)
	return buildNodeWSResponseFrame(request.response, request.ctx)
}

func requireNodeWSRouteExecutionAccess(request *nodeWSDispatchRequest, execution nodeWSRouteExecution) error {
	if execution.actionSpec == nil {
		return nil
	}
	spec := *execution.actionSpec
	if scopeAllowed := nodeProtectedActionScopeAllowed(spec); scopeAllowed != nil && !scopeAllowed(actorNodeScopeWithAuthz(request.state.Authorization(), request.actor)) {
		return webservice.ErrForbidden
	}
	authz := request.state.Authorization()
	result, err := webservice.ResolveProtectedActionAccess(authz, webservice.ProtectedActionAccessInput{
		Principal:         webapi.PrincipalFromActor(request.ctx.Actor()),
		Permission:        spec.Permission,
		ClientScope:       spec.ClientScope,
		Ownership:         string(spec.Ownership),
		RequestedClientID: wsRequestInt(request.ctx, "client_id"),
		ResourceID:        wsRequestInt(request.ctx, "id"),
	})
	if err != nil {
		return err
	}
	if result.ResolvedClientID > 0 {
		request.ctx.SetParam("client_id", strconv.Itoa(result.ResolvedClientID))
	}
	return nil
}

func wsRequestInt(ctx *wsAPIContext, key string) int {
	if ctx == nil {
		return 0
	}
	value := strings.TrimSpace(ctx.String(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func dispatchNodeWSChangesRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	query := resolveNodeChangesQueryRequest(request.ctx.String("after"), request.ctx.String("limit"), request.ctx.String("durable"), request.ctx.String("history"))
	payload, err := queryNodeChangesForRequest(request.state, request.actor, query)
	if err != nil {
		writeWSNodeChangesError(request.ctx, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	return buildNodeWSNodeChangesManagementDataFrame(request.response, request.metadata, time.Now().Unix(), payload)
}

func dispatchNodeWSCallbackQueueRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	scope := actorNodeScopeWithAuthz(request.state.Authorization(), request.actor)
	if err := requireNodeCallbackQueueAccess(request.state, request.actor, scope, false); err != nil {
		writeWSCallbackQueueError(request.ctx, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	payload, err := queryNodeCallbackQueue(
		request.state,
		scope,
		request.ctx.String("platform_id"),
		parseNodeCallbackQueueLimit(request.ctx.String("limit")),
	)
	if err != nil {
		writeWSCallbackQueueError(request.ctx, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	timestamp := time.Now().Unix()
	return buildNodeWSCallbackQueueManagementDataFrame(request.response, request.state, request.metadata, timestamp, payload)
}

func dispatchNodeWSConfigImportRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSConfigImport(request)
}

func dispatchNodeWSBatchRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	batch, err := decodeNodeBatchRequestBytes(nodeBatchRequestLimit(request.state), request.frame.Body)
	if err != nil {
		writeWSBatchError(request.ctx, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	payload := executeNodeBatchRequest(request.state, nodeWSDispatchBase{
		Context:    request.ctx.BaseContext(),
		Host:       request.ctx.Host(),
		RemoteAddr: request.ctx.RemoteAddr(),
		ClientIP:   request.ctx.ClientIP(),
		Metadata: webapi.RequestMetadata{
			NodeID:    request.metadata.NodeID,
			RequestID: request.metadata.RequestID,
			Source:    appendBatchSource(request.metadata.Source),
		},
	}, request.actor, batch)
	response := buildNodeWSManagementDataFrame(request.response, request.metadata, request.state.RuntimeIdentity().ConfigEpoch(), time.Now().Unix(), payload)
	setNodeWSFrameHeader(&response, "X-Operation-ID", payload.OperationID)
	return response
}

func dispatchNodeWSCallbackQueueReplayRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSCallbackQueueMutation(request, "replay")
}

func dispatchNodeWSCallbackQueueClearRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSCallbackQueueMutation(request, "clear")
}

func dispatchNodeWSWebhookCollectionRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSWebhookCollectionRequest(request.state, request.ctx, request.actor, request.metadata, request.method, request.response)
}

func dispatchNodeWSWebhookItemRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSWebhookItemRequest(request.state, request.ctx, request.actor, request.metadata, request.method, request.path, request.response)
}

func dispatchNodeWSRealtimeSubscriptionCollectionRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSRealtimeSubscriptionCollectionRequest(
		request.base.Subscriptions,
		request.ctx,
		request.metadata,
		request.state.RuntimeIdentity().ConfigEpoch(),
		request.method,
		request.response,
	)
}

func dispatchNodeWSRealtimeSubscriptionItemRoute(request *nodeWSDispatchRequest) nodeWSFrame {
	return dispatchNodeWSRealtimeSubscriptionItemRequest(
		request.base.Subscriptions,
		request.ctx,
		request.metadata,
		request.state.RuntimeIdentity().ConfigEpoch(),
		request.method,
		request.path,
		request.response,
	)
}

func nodeWSResourceRequestPath(path string) bool {
	switch {
	case path == "/users", strings.HasPrefix(path, "/users/"):
		return true
	case path == "/clients", strings.HasPrefix(path, "/clients/"):
		return true
	case path == "/tunnels", strings.HasPrefix(path, "/tunnels/"):
		return true
	case path == "/hosts", strings.HasPrefix(path, "/hosts/"):
		return true
	default:
		return false
	}
}

func dispatchNodeWSConfigImport(request *nodeWSDispatchRequest) nodeWSFrame {
	operation := startNodeWSOperation(request.state, request.ctx, "config_import", []string{canonicalNodeOperationPath(request.state, request.frame.Path)})
	payload, status, err := executeNodeConfigImport(request.ctx.BaseContext(), request.state, request.actor, request.metadata, request.frame.Body)
	operation.record(status, err)
	if err != nil {
		writeWSManagementErrorResponse(request.ctx, status, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	response := buildNodeWSManagementDataFrame(request.response, request.metadata, payload.ConfigEpoch, time.Now().Unix(), payload)
	operation.setFrameHeader(&response)
	return response
}

func dispatchNodeWSCallbackQueueMutation(request *nodeWSDispatchRequest, action string) nodeWSFrame {
	scope := actorNodeScopeWithAuthz(request.state.Authorization(), request.actor)
	operationName := "callback_queue_" + strings.TrimSpace(action)
	operationPath := canonicalNodeOperationPath(request.state, "/api/callbacks/queue/actions/"+strings.TrimSpace(action))
	operation := startNodeWSOperation(request.state, request.ctx, operationName, []string{operationPath})
	if err := requireNodeCallbackQueueAccess(request.state, request.actor, scope, true); err != nil {
		recordNodeCallbackQueueMutationOutcome(request.state, request.actor, request.ctx.Metadata(), operation.operationID, operationName, operation.startedAt, operation.paths, err, nil)
		writeWSCallbackQueueError(request.ctx, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	result, err := executeAuditedNodeCallbackQueueMutation(request.state, request.actor, request.ctx.Metadata(), operation.operationID, operationName, operation.startedAt, operation.paths, scope, nodeCallbackQueueMutationRequest{
		PlatformID: strings.TrimSpace(request.ctx.String("platform_id")),
		Action:     action,
	})
	if err != nil {
		writeWSCallbackQueueError(request.ctx, err)
		return buildNodeWSResponseFrame(request.response, request.ctx)
	}
	timestamp := time.Now().Unix()
	response := buildNodeWSCallbackQueueManagementDataFrame(request.response, request.state, request.metadata, timestamp, result.Payload)
	operation.setFrameHeader(&response)
	return response
}

func buildNodeWSCallbackQueueManagementDataFrame(frame nodeWSFrame, state *State, metadata webapi.RequestMetadata, timestamp int64, data interface{}) nodeWSFrame {
	configEpoch := ""
	if state != nil {
		configEpoch = state.RuntimeIdentity().ConfigEpoch()
	}
	return buildNodeWSManagementDataFrame(frame, metadata, configEpoch, timestamp, stampNodeCallbackQueueResponseDataTimestamp(data, timestamp))
}

func frameHeaderValue(headers map[string]string, key string) string {
	if len(headers) == 0 {
		return ""
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if value, ok := headers[key]; ok {
		return value
	}
	return headers[strings.ToLower(key)]
}

func isFormalNodeWSRequestPath(cfg *servercfg.Snapshot, rawPath string) bool {
	_, matched, err := normalizePrefixedNodeWSPath(cfg, rawPath)
	return err == nil && matched
}

func normalizeNodeWSPath(cfg *servercfg.Snapshot, rawPath string) (string, error) {
	path, _, err := normalizePrefixedNodeWSPath(cfg, rawPath)
	return path, err
}

func resolveNodeWSRequest(cfg *servercfg.Snapshot, rawMethod, rawPath string) (nodeWSResolvedRequest, error) {
	method := strings.ToUpper(strings.TrimSpace(rawMethod))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return nodeWSResolvedRequest{}, errors.New("unsupported request method")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawPath))
	if err != nil {
		return nodeWSResolvedRequest{}, err
	}
	path, matched, err := normalizePrefixedNodeWSPath(cfg, rawPath)
	if err != nil {
		return nodeWSResolvedRequest{}, err
	}
	if !matched {
		return nodeWSResolvedRequest{}, nil
	}
	return nodeWSResolvedRequest{
		CanonicalPath:   path,
		CanonicalMethod: method,
		CanonicalQuery:  parsed.Query().Encode(),
	}, nil
}

func normalizePrefixedNodeWSPath(cfg *servercfg.Snapshot, rawPath string) (string, bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawPath))
	if err != nil {
		return "", false, err
	}
	path := strings.TrimSpace(parsed.Path)
	if path == "" {
		path = "/"
	}
	baseURL := ""
	if cfg != nil {
		baseURL = cfg.Web.BaseURL
	}
	matched := false
	for _, prefix := range nodeWSRoutePrefixes(baseURL) {
		if path == prefix {
			path = "/"
			matched = true
			break
		}
		if strings.HasPrefix(path, prefix+"/") {
			path = strings.TrimPrefix(path, prefix)
			matched = true
			break
		}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path, matched, nil
}

func nodeWSRoutePrefixes(baseURL string) []string {
	return appendDedupedRoutePrefixes(webapi.NodeRoutePrefixes(baseURL))
}

func appendDedupedRoutePrefixes(groups ...[]string) []string {
	size := 0
	for _, group := range groups {
		size += len(group)
	}
	prefixes := make([]string, 0, size)
	seen := make(map[string]struct{}, size)
	for _, group := range groups {
		for _, prefix := range group {
			prefix = strings.TrimRight(strings.TrimSpace(prefix), "/")
			if prefix == "" {
				continue
			}
			if _, ok := seen[prefix]; ok {
				continue
			}
			seen[prefix] = struct{}{}
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

func isWSJSONContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(contentType, "application/json")
}

func isWSTextContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.HasPrefix(contentType, "text/")
}

func marshalNodeWSTextFrameBody(body []byte) json.RawMessage {
	data, _ := json.Marshal(string(body))
	return data
}

func marshalNodeWSBinaryFrameBody(body []byte) json.RawMessage {
	data, _ := json.Marshal(base64.StdEncoding.EncodeToString(body))
	return data
}

func buildNodeWSResponseFrame(frame nodeWSFrame, ctx *wsAPIContext) nodeWSFrame {
	if ctx == nil {
		frame.Status = http.StatusInternalServerError
		frame.Error = "ws context is unavailable"
		return frame
	}
	frame.Status = ctx.status
	if frame.Status == 0 {
		frame.Status = http.StatusOK
	}
	if len(ctx.responseHeader) > 0 {
		frame.Headers = make(map[string]string, len(ctx.responseHeader))
		for key, value := range ctx.responseHeader {
			frame.Headers[key] = value
		}
	}
	if len(ctx.responseBody) == 0 {
		return frame
	}
	switch ctx.responseMode {
	case wsResponseBodyModeJSON:
		frame.Body = ctx.responseBody
		return frame
	case wsResponseBodyModeText:
		frame.Body = marshalNodeWSTextFrameBody(ctx.responseBody)
		frame.Encoding = "text"
		return frame
	case wsResponseBodyModeBinary:
		frame.Body = marshalNodeWSBinaryFrameBody(ctx.responseBody)
		frame.Encoding = "base64"
		return frame
	}
	if isWSJSONContentType(ctx.contentType) && json.Valid(ctx.responseBody) {
		frame.Body = ctx.responseBody
		return frame
	}
	if isWSTextContentType(ctx.contentType) {
		frame.Body = marshalNodeWSTextFrameBody(ctx.responseBody)
		frame.Encoding = "text"
		return frame
	}
	frame.Body = marshalNodeWSBinaryFrameBody(ctx.responseBody)
	frame.Encoding = "base64"
	return frame
}

func writeWSError(ctx *wsAPIContext, status int, msg string) {
	writeWSManagementError(ctx, status, strings.ReplaceAll(strings.ToLower(strings.TrimSpace(msg)), " ", "_"), msg, nil)
}

func writeWSManagementError(ctx *wsAPIContext, status int, code, message string, details map[string]any) {
	if ctx == nil {
		return
	}
	webapi.RespondManagementErrorMessage(ctx, status, code, message, details)
}

func writeWSManagementErrorResponse(ctx *wsAPIContext, status int, err error) {
	if ctx == nil {
		return
	}
	response := webapi.ManagementErrorResponseForStatus(status, err)
	writeWSManagementError(ctx, status, response.Error.Code, response.Error.Message, response.Error.Details)
}

func buildNodeWSManagementErrorFrame(frame nodeWSFrame, ctx *wsAPIContext, status int, code, message string, details map[string]any) nodeWSFrame {
	frame.Error = ""
	writeWSManagementError(ctx, status, code, message, details)
	return buildNodeWSResponseFrame(frame, ctx)
}

func permissionStatus(err error) int {
	switch err {
	case nil:
		return http.StatusOK
	case webservice.ErrUnauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusForbidden
	}
}

func accessErrorMessage(err error) string {
	switch err {
	case nil:
		return ""
	case webservice.ErrUnauthenticated:
		return "unauthorized"
	default:
		return "forbidden"
	}
}

type nodeWSResourceDispatch struct {
	action  webapi.ActionSpec
	handler webapi.Handler
}

type nodeWSResourceRouteSpec struct {
	action      webapi.ActionSpec
	resource    string
	requiresID  bool
	subresource string
	routeAction string
	method      string
}

func (spec nodeWSResourceRouteSpec) pathMatches(resource string, resourceID int, subresource, action string) bool {
	if spec.resource != resource {
		return false
	}
	if spec.requiresID != (resourceID > 0) {
		return false
	}
	if spec.subresource != strings.TrimSpace(subresource) {
		return false
	}
	return spec.routeAction == strings.TrimSpace(action)
}

func nodeWSResourceRouteSpecFromAction(spec webapi.ActionSpec) (nodeWSResourceRouteSpec, bool) {
	if _, managed := nodeWSManagedRouteKindForProtectedAction(spec); managed {
		return nodeWSResourceRouteSpec{}, false
	}
	relative := strings.Trim(strings.TrimSpace(nodeProtectedActionRelativePath(spec.Path)), "/")
	if relative == "" {
		return nodeWSResourceRouteSpec{}, false
	}
	parts := strings.Split(relative, "/")
	switch parts[0] {
	case "users", "clients", "tunnels", "hosts":
	default:
		return nodeWSResourceRouteSpec{}, false
	}
	route := nodeWSResourceRouteSpec{
		action:   spec,
		resource: parts[0],
		method:   strings.ToUpper(strings.TrimSpace(spec.Method)),
	}
	switch len(parts) {
	case 1:
		return route, true
	case 2:
		if parts[1] == "{id}" {
			route.requiresID = true
			return route, true
		}
		route.subresource = parts[1]
		return route, true
	case 3:
		if parts[1] == "{id}" {
			route.requiresID = true
			route.subresource = parts[2]
			return route, true
		}
		route.subresource = parts[1]
		route.routeAction = parts[2]
		return route, true
	case 4:
		if parts[1] != "{id}" {
			return nodeWSResourceRouteSpec{}, false
		}
		route.requiresID = true
		route.subresource = parts[2]
		route.routeAction = parts[3]
		return route, true
	default:
		return nodeWSResourceRouteSpec{}, false
	}
}

func resolveNodeWSResourceDispatch(state *State, resource string, resourceID int, subresource, action, method string) (nodeWSResourceDispatch, bool, bool) {
	if state == nil || state.App == nil {
		return nodeWSResourceDispatch{}, false, false
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	pathMatched := false
	for _, protected := range state.ProtectedActions {
		spec, ok := nodeWSResourceRouteSpecFromAction(protected)
		if !ok {
			continue
		}
		if !spec.pathMatches(resource, resourceID, subresource, action) {
			continue
		}
		pathMatched = true
		if spec.method != method {
			continue
		}
		return nodeWSResourceDispatch{
			action:  spec.action,
			handler: spec.action.Handler,
		}, true, true
	}
	return nodeWSResourceDispatch{}, pathMatched, false
}

func dispatchNodeWSResourceRequest(request *nodeWSDispatchRequest) nodeWSFrame {
	operation := startNodeWSOperation(request.state, request.ctx, "resource", []string{canonicalNodeOperationPath(request.state, request.frame.Path)})
	resource, resourceID, subresource, action, ok := splitNodeResourcePath(request.path)
	if !ok {
		operation.record(http.StatusNotFound, nil)
		return buildNodeWSUnknownPathManagementFrame(request.response, request.ctx)
	}
	if request.state == nil || request.state.App == nil {
		operation.record(http.StatusInternalServerError, errors.New("node state is unavailable"))
		return buildNodeWSManagementErrorFrame(request.response, request.ctx, http.StatusInternalServerError, "node_state_unavailable", "node state is unavailable", nil)
	}
	if resourceID > 0 {
		request.ctx.SetParam("id", strconv.Itoa(resourceID))
	}
	dispatch, pathMatched, methodMatched := resolveNodeWSResourceDispatch(request.state, resource, resourceID, subresource, action, request.method)
	switch {
	case !pathMatched:
		operation.record(http.StatusNotFound, nil)
		return buildNodeWSUnknownPathManagementFrame(request.response, request.ctx)
	case !methodMatched:
		operation.record(http.StatusMethodNotAllowed, nil)
		return buildNodeWSMethodNotAllowedManagementFrame(request.response, request.ctx)
	}
	return executeNodeWSRouteExecution(request, nodeWSRouteExecution{
		operation:  "resource",
		actionSpec: &dispatch.action,
		handler:    dispatch.handler,
	})
}

func splitNodeResourcePath(path string) (resource string, id int, subresource string, action string, ok bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(path), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", 0, "", "", false
	}
	resource = parts[0]
	switch len(parts) {
	case 1:
		return resource, 0, "", "", true
	case 2:
		parsedID, err := strconv.Atoi(parts[1])
		if err != nil || parsedID <= 0 {
			return resource, 0, parts[1], "", true
		}
		return resource, parsedID, "", "", true
	case 3:
		if parts[1] == "actions" {
			return resource, 0, "actions", parts[2], true
		}
		parsedID, err := strconv.Atoi(parts[1])
		if err != nil || parsedID <= 0 {
			return "", 0, "", "", false
		}
		return resource, parsedID, parts[2], "", true
	case 4:
		parsedID, err := strconv.Atoi(parts[1])
		if err != nil || parsedID <= 0 || parts[2] != "actions" {
			return "", 0, "", "", false
		}
		return resource, parsedID, "actions", parts[3], true
	default:
		return "", 0, "", "", false
	}
}

func nodeBatchHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		if state == nil || state.App == nil {
			nodeBatchAbort(c, errNodeBatchStateUnavailable)
			return
		}
		request, err := decodeNodeBatchRequestFromHTTP(state, c)
		if err != nil {
			nodeBatchAbort(c, err)
			return
		}
		metadata := currentRequestMetadata(c)
		base := nodeWSDispatchBase{
			Context:    c.Request.Context(),
			Host:       c.Request.Host,
			RemoteAddr: c.Request.RemoteAddr,
			ClientIP:   resolvedManagementClientIP(requestAPIContext(c), state),
			Metadata:   metadata,
		}
		base.Metadata.Source = appendBatchSource(base.Metadata.Source)
		payload := executeNodeBatchRequest(state, base, currentActor(c), request)
		if payload.OperationID != "" {
			c.Header("X-Operation-ID", payload.OperationID)
		}
		c.JSON(http.StatusOK, webapi.ManagementDataResponse{
			Data: payload,
			Meta: webapi.ManagementResponseMetaForRequest(metadata, time.Now().Unix(), state.RuntimeIdentity().ConfigEpoch()),
		})
	}
}

func decodeNodeBatchRequestFromHTTP(state *State, c *gin.Context) (nodeBatchRequest, error) {
	if c == nil {
		return nodeBatchRequest{}, errors.New("batch request is empty")
	}
	raw := bytes.TrimSpace(framework.RequestRawBodyView(c))
	if len(raw) == 0 && c.Request != nil && c.Request.ContentLength > 0 {
		return nodeBatchRequest{}, errors.New("invalid batch request")
	}
	return decodeNodeBatchRequestBytes(nodeBatchRequestLimit(state), raw)
}

func decodeNodeBatchRequestBytes(limit int, raw []byte) (nodeBatchRequest, error) {
	return webservice.DecodeNodeBatchRequest(limit, raw)
}

func nodeBatchErrorDetail(err error) (int, webapi.ManagementErrorDetail) {
	switch {
	case errors.Is(err, errNodeBatchStateUnavailable):
		return http.StatusInternalServerError, webapi.ManagementErrorDetail{
			Code:    "node_state_unavailable",
			Message: errNodeBatchStateUnavailable.Error(),
		}
	case isNodeBatchInvalidJSONError(err):
		return http.StatusBadRequest, webapi.ManagementErrorDetail{
			Code:    "invalid_json_body",
			Message: "invalid json body",
		}
	default:
		response := webapi.ManagementErrorResponseForStatus(http.StatusBadRequest, err)
		return http.StatusBadRequest, response.Error
	}
}

func isNodeBatchInvalidJSONError(err error) bool {
	message := ""
	if err != nil {
		message = strings.TrimSpace(err.Error())
	}
	return message == "invalid batch request" || message == "invalid batch items"
}

func nodeBatchAbort(c *gin.Context, err error) {
	if c == nil {
		return
	}
	status, detail := nodeBatchErrorDetail(err)
	c.AbortWithStatusJSON(status, webapi.ManagementErrorResponse{Error: detail})
}

func writeWSBatchError(ctx *wsAPIContext, err error) {
	if ctx == nil {
		return
	}
	status, detail := nodeBatchErrorDetail(err)
	writeWSManagementError(ctx, status, detail.Code, detail.Message, detail.Details)
}

func nodeBatchRequestLimit(state *State) int {
	if state == nil || state.NodeBatchMaxItems <= 0 {
		return nodeBatchMaxItems
	}
	return state.NodeBatchMaxItems
}

func executeNodeBatchRequest(state *State, base nodeWSDispatchBase, actor *webapi.Actor, request nodeBatchRequest) nodeBatchResponsePayload {
	startedAt := time.Now()
	result := webservice.ExecuteNodeBatch(webservice.NodeBatchExecuteInput{
		Request:            request,
		DefaultOperationID: strings.TrimSpace(base.Metadata.RequestID),
		DefaultItemID:      nodeBatchDefaultItemID,
		AllowPath: func(path string) bool {
			return nodeBatchPathAllowed(state, path)
		},
		RejectedHeaders:      requestMetadataResponseHeaders(base.Metadata),
		RejectedStatus:       http.StatusBadRequest,
		RejectedErrorMessage: "nested batch or websocket dispatch is not supported",
		RejectedTimestamp:    nodeBatchTimestamp,
		Dispatch: func(item webservice.NodeBatchDispatchItem) webservice.NodeBatchResponseItem {
			frame := nodeWSFrame{
				ID:      item.Item.ID,
				Type:    "request",
				Method:  item.Item.Method,
				Path:    item.Item.Path,
				Headers: cloneBatchHeaders(item.Item.Headers),
				Body:    item.Item.Body,
			}
			itemBase := base
			itemBase.Metadata = base.Metadata
			itemBase.Metadata.RequestID = resolveRequestID(
				frameHeaderValue(frame.Headers, "X-Request-ID"),
				frameHeaderValue(frame.Headers, "X-Correlation-ID"),
				frame.ID,
				base.Metadata.RequestID,
			)
			response := dispatchNodeWSRequestWithBase(state, itemBase, actor, frame)
			return webservice.NodeBatchResponseItem{
				ID:        response.ID,
				Status:    response.Status,
				Error:     response.Error,
				Headers:   cloneBatchHeaders(response.Headers),
				Body:      response.Body,
				Encoding:  response.Encoding,
				Timestamp: response.Timestamp,
			}
		},
	})
	payload := nodeBatchResponsePayload{
		OperationID:  result.OperationID,
		Count:        result.Count,
		SuccessCount: result.SuccessCount,
		ErrorCount:   result.ErrorCount,
		Items:        make([]nodeWSFrame, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		payload.Items = append(payload.Items, nodeWSFrame{
			ID:        item.ID,
			Type:      "response",
			Status:    item.Status,
			Error:     item.Error,
			Headers:   item.Headers,
			Body:      item.Body,
			Encoding:  item.Encoding,
			Timestamp: item.Timestamp,
		})
	}
	recordNodeBatchOperation(state, actor, base, request, payload, startedAt)
	return payload
}

func nodeBatchPathAllowed(state *State, rawPath string) bool {
	if state == nil {
		return false
	}
	path, err := normalizeNodeWSPath(state.CurrentConfig(), rawPath)
	if err != nil {
		return false
	}
	switch path {
	case "/batch", "/ws":
		return false
	case "/realtime/subscriptions":
		return false
	default:
		return !strings.HasPrefix(path, "/realtime/subscriptions/")
	}
}

func appendBatchSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "node-batch"
	}
	if strings.Contains(source, ":batch") {
		return source
	}
	return source + ":batch"
}

func cloneBatchHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func nodeBatchDefaultItemID(index int) string {
	return "batch-" + strconvFormatInt(int64(index+1))
}

func nodeBatchTimestamp() int64 {
	return time.Now().Unix()
}

func recordNodeBatchOperation(state *State, actor *webapi.Actor, base nodeWSDispatchBase, request nodeBatchRequest, payload nodeBatchResponsePayload, startedAt time.Time) {
	if state == nil {
		return
	}
	operations := state.NodeOperations()
	if operations == nil {
		return
	}
	operationID := strings.TrimSpace(payload.OperationID)
	if operationID == "" {
		return
	}
	operations.Record(webservice.NodeOperationRecordPayload{
		OperationID:  operationID,
		Kind:         "batch",
		RequestID:    strings.TrimSpace(base.Metadata.RequestID),
		Source:       strings.TrimSpace(base.Metadata.Source),
		Scope:        webapi.NodeOperationScope(actor),
		Actor:        webapi.NodeOperationActorPayload(actor),
		StartedAt:    startedAt.Unix(),
		FinishedAt:   time.Now().Unix(),
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Count:        payload.Count,
		SuccessCount: payload.SuccessCount,
		ErrorCount:   payload.ErrorCount,
		Paths:        normalizeNodeOperationPaths(nodeBatchCanonicalPaths(state, request.Items)),
	})
}

func nodeBatchPaths(items []nodeBatchRequestItem) []string {
	if len(items) == 0 {
		return nil
	}
	paths := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func nodeBatchCanonicalPaths(state *State, items []nodeBatchRequestItem) []string {
	paths := nodeBatchPaths(items)
	if len(paths) == 0 {
		return nil
	}
	canonical := make([]string, 0, len(paths))
	for _, item := range items {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			continue
		}
		params, err := decodeWSParams(path, item.Body)
		if err != nil {
			params = nil
		}
		canonical = append(canonical, canonicalNodeOperationPathWithParams(state, path, params))
	}
	sort.Strings(canonical)
	return normalizeNodeOperationPaths(canonical)
}

func nodeConfigImportHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		operationID := currentNodeHTTPOperationID(c)
		setNodeHTTPOperationHeader(c, operationID)
		payload, status, err := executeNodeConfigImport(
			c.Request.Context(),
			state,
			currentActor(c),
			currentRequestMetadata(c),
			framework.RequestRawBodyView(c),
		)
		recordNodeOperation(
			state,
			currentActor(c),
			currentRequestMetadata(c),
			operationID,
			"config_import",
			startedAt,
			1,
			nodeOperationSuccessCount(status),
			nodeOperationErrorCount(status),
			[]string{canonicalNodeOperationPath(state, c.Request.URL.Path)},
		)
		if err != nil {
			state.RuntimeStatus().NoteOperation("config_import", err)
			c.JSON(status, webapi.ManagementErrorResponseForStatus(status, err))
			return
		}
		state.RuntimeStatus().NoteOperation("config_import", nil)
		c.JSON(status, webapi.ManagementDataResponse{
			Data: payload,
			Meta: webapi.ManagementResponseMetaForRequest(currentRequestMetadata(c), time.Now().Unix(), payload.ConfigEpoch),
		})
		maybeInvalidateRealtimeSessions(state, payload.ConfigEpoch)
	}
}

func executeNodeConfigImport(ctx context.Context, state *State, actor *webapi.Actor, metadata webapi.RequestMetadata, raw []byte) (nodeConfigImportResponse, int, error) {
	if state == nil || state.App == nil {
		return nodeConfigImportResponse{}, http.StatusInternalServerError, errors.New("node runtime is unavailable")
	}
	scope := actorNodeScopeWithAuthz(state.Authorization(), actor)
	if !scope.CanExportConfig() {
		return nodeConfigImportResponse{}, http.StatusForbidden, webservice.ErrForbidden
	}
	snapshot, err := decodeNodeConfigSnapshot(raw)
	if err != nil {
		return nodeConfigImportResponse{}, http.StatusBadRequest, err
	}
	storage := state.NodeStorage()
	rollback, rollbackErr := currentNodeConfigSnapshot(storage)
	if rollbackErr != nil {
		return nodeConfigImportResponse{}, nodeConfigImportErrorStatus(rollbackErr), rollbackErr
	}

	stopRuntimePreserveStatus()
	importErr := storage.ImportSnapshot(snapshot)
	if importErr == nil {
		file.EnsureManagementPlatformUsers(currentManagementPlatformBindings(state.CurrentConfig()))
		importErr = flushImportedConfigStorage(storage)
	}
	if importErr != nil {
		if rollback != nil {
			_ = storage.ImportSnapshot(rollback)
			_ = flushImportedConfigStorage(storage)
		}
		startRuntimeFromDB()
		return nodeConfigImportResponse{}, nodeConfigImportErrorStatus(importErr), importErr
	}

	state.ResetProtocolState()
	configEpoch := state.RuntimeIdentity().RotateConfigEpoch()
	clearRuntimeCaches()
	startRuntimeFromDB()
	recordImportedConfigEvent(ctx, state, actor, metadata, configEpoch)

	return nodeConfigImportResponse{
		Status:      1,
		Msg:         "config import success",
		ConfigEpoch: configEpoch,
		ImportedAt:  time.Now().Unix(),
	}, http.StatusOK, nil
}

func maybeInvalidateRealtimeSessionsAfterConfigImport(state *State, request nodeWSFrame, response nodeWSFrame) {
	if !isNodeConfigImportWSPath(state, request.Path) || response.Status != http.StatusOK {
		return
	}
	maybeInvalidateRealtimeSessions(state, extractNodeConfigImportEpoch(response.Body))
}

func maybeInvalidateRealtimeSessions(state *State, configEpoch string) {
	if state == nil {
		return
	}
	configEpoch = strings.TrimSpace(configEpoch)
	if configEpoch == "" {
		return
	}
	state.InvalidateRealtimeSessions("config_import", configEpoch)
}

func isNodeConfigImportWSPath(state *State, rawPath string) bool {
	if strings.TrimSpace(rawPath) == "" {
		return false
	}
	cfg := (*servercfg.Snapshot)(nil)
	if state != nil {
		cfg = state.CurrentConfig()
	}
	path, err := normalizeNodeWSPath(cfg, rawPath)
	if err != nil {
		return false
	}
	return path == "/system/import"
}

func extractNodeConfigImportEpoch(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	var payload nodeConfigImportResponse
	if err := json.Unmarshal(body, &payload); err == nil {
		return strings.TrimSpace(payload.ConfigEpoch)
	}
	var envelope nodeConfigImportEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	if len(bytes.TrimSpace(envelope.Data)) == 0 {
		return ""
	}
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ConfigEpoch)
}

func decodeNodeConfigSnapshot(raw []byte) (*file.ConfigSnapshot, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("config snapshot body is empty")
	}
	var snapshot file.ConfigSnapshot
	if err := json.Unmarshal(raw, &snapshot); err == nil {
		return &snapshot, nil
	}
	var envelope nodeConfigImportEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(envelope.Data)) == 0 {
		return nil, errors.New("config snapshot payload is empty")
	}
	if envelope.Status == 0 && strings.TrimSpace(envelope.Msg) != "" {
		return nil, errors.New(strings.TrimSpace(envelope.Msg))
	}
	if err := json.Unmarshal(envelope.Data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func currentNodeConfigSnapshot(storage webservice.NodeStorage) (*file.ConfigSnapshot, error) {
	if storage == nil {
		return nil, nil
	}
	current, err := storage.Snapshot()
	if err != nil {
		return nil, err
	}
	snapshot, ok := current.(*file.ConfigSnapshot)
	if !ok || snapshot == nil {
		return nil, nil
	}
	return snapshot, nil
}

func nodeConfigImportErrorStatus(err error) int {
	switch {
	case errors.Is(err, webservice.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, webservice.ErrSnapshotImportUnsupported):
		return http.StatusNotImplemented
	case errors.Is(err, webservice.ErrSnapshotExportUnsupported):
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func currentManagementPlatformBindings(cfg *servercfg.Snapshot) []file.ManagementPlatformBinding {
	if cfg == nil {
		return nil
	}
	bindings := make([]file.ManagementPlatformBinding, 0, len(cfg.Runtime.ManagementPlatforms))
	for _, platform := range cfg.Runtime.EnabledManagementPlatforms() {
		bindings = append(bindings, file.ManagementPlatformBinding{
			PlatformID:      platform.PlatformID,
			ServiceUsername: platform.ServiceUsername,
			Enabled:         platform.Enabled,
		})
	}
	return bindings
}

func stopRuntimePreserveStatus() {
	server.StopManagedTasksPreserveStatus()
}

func startRuntimeFromDB() {
	server.StartManagedTasksFromDB()
}

func clearRuntimeCaches() {
	server.ClearProxyCache()
	server.DisconnectOrphanClients()
}

func recordImportedConfigEvent(ctx context.Context, state *State, actor *webapi.Actor, metadata webapi.RequestMetadata, configEpoch string) {
	if state == nil || state.App == nil || state.App.Hooks == nil {
		return
	}
	_ = state.App.Hooks.OnManagementEvent(resolveNodeEmitContext(ctx, func() context.Context {
		if state != nil {
			return state.BaseContext()
		}
		return context.Background()
	}), webapi.Event{
		Name:     "node.config.imported",
		Resource: "node",
		Action:   "import",
		Actor:    cloneActor(actor),
		Metadata: metadata,
		Fields: map[string]interface{}{
			"config_epoch": configEpoch,
		},
	})
}

func flushImportedConfigStorage(storage webservice.NodeStorage) error {
	if storage == nil {
		return nil
	}
	if err := storage.FlushLocal(); err != nil {
		return err
	}
	return storage.FlushRuntime()
}

func decodeCanonicalNodeWSBody(ctx *wsAPIContext, dest interface{}) bool {
	if ctx == nil || dest == nil {
		return false
	}
	raw := bytes.TrimSpace(ctx.RawBodyView())
	if len(raw) == 0 {
		webapi.RespondManagementErrorMessage(ctx, http.StatusBadRequest, "json_body_required", "json body is required", nil)
		return false
	}
	if raw[0] != '{' {
		webapi.RespondManagementErrorMessage(ctx, http.StatusBadRequest, "invalid_json_body", "json object body is required", nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		webapi.RespondManagementErrorMessage(ctx, http.StatusBadRequest, "invalid_json_body", "invalid json body", map[string]any{
			"detail": strings.TrimSpace(err.Error()),
		})
		return false
	}
	if err := ensureNodeWSJSONEOF(decoder); err != nil {
		webapi.RespondManagementErrorMessage(ctx, http.StatusBadRequest, "invalid_json_body", "invalid json body", map[string]any{
			"detail": strings.TrimSpace(err.Error()),
		})
		return false
	}
	return true
}

func ensureNodeWSJSONEOF(decoder *json.Decoder) error {
	if decoder == nil {
		return nil
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return errors.New("json body must contain a single object")
}
