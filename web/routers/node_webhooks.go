package routers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/logs"
	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

const (
	nodeWebhookDefaultTimeoutSeconds = 10
	nodeWebhookMaxTimeoutSeconds     = 60
	nodeWebhookDisableConfigUpdate   = "config_update"
)

var (
	errNodeWebhookStateUnavailable   = errors.New("node state is unavailable")
	errNodeWebhookNotFound           = errors.New("webhook not found")
	errNodeWebhookInvalidID          = errors.New("invalid webhook id")
	errNodeWebhookEnabledRequired    = errors.New("missing enabled")
	errNodeWebhookJSONBodyRequired   = errors.New("json body is required")
	errNodeWebhookJSONObjectRequired = errors.New("json object body is required")
	errNodeWebhookInvalidJSONBody    = errors.New("invalid json body")
	errNodeWebhookURLRequired        = errors.New("url is required")
	errNodeWebhookInvalidURL         = errors.New("invalid webhook url")
	errNodeWebhookInvalidMethod      = errors.New("webhook method must be POST")
	errNodeWebhookInvalidContentMode = errors.New("invalid content_mode")
	errNodeWebhookInvalidBodyTmpl    = errors.New("invalid body_template")
)

type nodeWebhookConfig struct {
	ID             int64         `json:"id"`
	URL            string        `json:"url"`
	Method         string        `json:"method,omitempty"`
	TimeoutSeconds int           `json:"timeout_seconds,omitempty"`
	Owner          *webapi.Actor `json:"owner,omitempty"`
	CreatedAt      int64         `json:"created_at,omitempty"`
	UpdatedAt      int64         `json:"updated_at,omitempty"`
	nodeEventSinkConfig
}

type nodeWebhookPersistedState struct {
	Version int64               `json:"version,omitempty"`
	NextID  int64               `json:"next_id"`
	Items   []nodeWebhookConfig `json:"items"`
}

type nodeWebhookRuntimeState struct {
	LastDeliveredAt        int64  `json:"last_delivered_at,omitempty"`
	LastError              string `json:"last_error,omitempty"`
	LastErrorAt            int64  `json:"last_error_at,omitempty"`
	LastStatusCode         int    `json:"last_status_code,omitempty"`
	Deliveries             int64  `json:"deliveries,omitempty"`
	Failures               int64  `json:"failures,omitempty"`
	ConsecutiveFailures    int64  `json:"consecutive_failures,omitempty"`
	LastDisabledReason     string `json:"last_disabled_reason,omitempty"`
	LastDisabledAt         int64  `json:"last_disabled_at,omitempty"`
	LastSelectorScrubbedAt int64  `json:"last_selector_scrubbed_at,omitempty"`
}

type nodeWebhookStore struct {
	mu        sync.RWMutex
	path      string
	nextID    int64
	items     map[int64]nodeWebhookConfig
	runtime   map[int64]nodeWebhookRuntimeState
	writer    *nodeRuntimeStateWriter
	lookup    func() nodeEventResourceLookup
	baseCtx   func() context.Context
	emit      func(context.Context, webapi.Event)
	changeCh  chan struct{}
	closeOnce sync.Once
}

type nodeWebhookScrubCandidate struct {
	id   int64
	item nodeWebhookConfig
}

type nodeWebhookScrubPlan struct {
	id              int64
	selector        nodeEventSelector
	owner           *webapi.Actor
	scrubbed        nodeEventSelector
	selectorChanged bool
	disableSelector bool
	disableOwner    bool
}

type nodeWebhookWriteRequest struct {
	Name            string            `json:"name"`
	URL             string            `json:"url"`
	Method          string            `json:"method,omitempty"`
	Enabled         *bool             `json:"enabled,omitempty"`
	TimeoutSeconds  *int              `json:"timeout_seconds,omitempty"`
	EventNames      []string          `json:"event_names,omitempty"`
	Resources       []string          `json:"resources,omitempty"`
	Actions         []string          `json:"actions,omitempty"`
	UserIDs         []int             `json:"user_ids,omitempty"`
	ClientIDs       []int             `json:"client_ids,omitempty"`
	TunnelIDs       []int             `json:"tunnel_ids,omitempty"`
	HostIDs         []int             `json:"host_ids,omitempty"`
	ContentMode     string            `json:"content_mode,omitempty"`
	ContentType     string            `json:"content_type,omitempty"`
	BodyTemplate    string            `json:"body_template,omitempty"`
	HeaderTemplates map[string]string `json:"header_templates,omitempty"`
}

type nodeWebhookStatusRequest struct {
	Enabled *bool `json:"enabled"`
}

type nodeWebhookOwnerPayload struct {
	Kind       string `json:"kind,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	Username   string `json:"username,omitempty"`
	PlatformID string `json:"platform_id,omitempty"`
}

type nodeWebhookPayload struct {
	ID                  int64                   `json:"id"`
	Name                string                  `json:"name,omitempty"`
	URL                 string                  `json:"url"`
	Method              string                  `json:"method,omitempty"`
	Enabled             bool                    `json:"enabled"`
	TimeoutSeconds      int                     `json:"timeout_seconds,omitempty"`
	EventNames          []string                `json:"event_names,omitempty"`
	Resources           []string                `json:"resources,omitempty"`
	Actions             []string                `json:"actions,omitempty"`
	UserIDs             []int                   `json:"user_ids,omitempty"`
	ClientIDs           []int                   `json:"client_ids,omitempty"`
	TunnelIDs           []int                   `json:"tunnel_ids,omitempty"`
	HostIDs             []int                   `json:"host_ids,omitempty"`
	ContentMode         string                  `json:"content_mode,omitempty"`
	ContentType         string                  `json:"content_type,omitempty"`
	BodyTemplate        string                  `json:"body_template,omitempty"`
	HeaderTemplates     map[string]string       `json:"header_templates,omitempty"`
	Owner               nodeWebhookOwnerPayload `json:"owner,omitempty"`
	CreatedAt           int64                   `json:"created_at,omitempty"`
	UpdatedAt           int64                   `json:"updated_at,omitempty"`
	LastDeliveredAt     int64                   `json:"last_delivered_at,omitempty"`
	LastError           string                  `json:"last_error,omitempty"`
	LastErrorAt         int64                   `json:"last_error_at,omitempty"`
	LastStatusCode      int                     `json:"last_status_code,omitempty"`
	Deliveries          int64                   `json:"deliveries,omitempty"`
	Failures            int64                   `json:"failures,omitempty"`
	ConsecutiveFailures int64                   `json:"consecutive_failures,omitempty"`
	LastDisabledReason  string                  `json:"last_disabled_reason,omitempty"`
	LastDisabledAt      int64                   `json:"last_disabled_at,omitempty"`
}

type nodeWebhookListPayload struct {
	Timestamp int64                `json:"timestamp"`
	Items     []nodeWebhookPayload `json:"items"`
}

type nodeWebhookMutationPayload struct {
	Timestamp int64               `json:"timestamp"`
	Item      *nodeWebhookPayload `json:"item,omitempty"`
}

func nodeWebhookListHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		store, actor, ok := requireNodeWebhookActor(state, c)
		if !ok {
			return
		}
		timestamp := time.Now().Unix()
		writeNodeWebhookData(c, state, timestamp, nodeWebhookListPayload{
			Items: store.ListVisible(actor),
		})
	}
}

func nodeWebhookGetHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		store, actor, ok := requireNodeWebhookActor(state, c)
		if !ok {
			return
		}
		id, ok := parseNodeWebhookID(c)
		if !ok {
			return
		}
		item, ok := store.GetVisible(actor, id)
		if !ok {
			nodeWebhookAbort(c, errNodeWebhookNotFound)
			return
		}
		timestamp := time.Now().Unix()
		writeNodeWebhookData(c, state, timestamp, nodeWebhookMutationPayload{
			Item: &item,
		})
	}
}

func nodeWebhookCreateHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		store, actor, ok := requireNodeWebhookActor(state, c)
		if !ok {
			return
		}
		var request nodeWebhookWriteRequest
		if !decodeCanonicalNodeHTTPBody(c, &request) {
			return
		}
		item, err := store.Create(c.Request.Context(), actor, currentRequestMetadata(c), request)
		if err != nil {
			nodeWebhookMutationAbort(c, err)
			return
		}
		timestamp := time.Now().Unix()
		writeNodeWebhookData(c, state, timestamp, nodeWebhookMutationPayload{
			Item: &item,
		})
	}
}

func nodeWebhookUpdateHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		store, actor, ok := requireNodeWebhookActor(state, c)
		if !ok {
			return
		}
		id, ok := parseNodeWebhookID(c)
		if !ok {
			return
		}
		var request nodeWebhookWriteRequest
		if !decodeCanonicalNodeHTTPBody(c, &request) {
			return
		}
		item, err := store.Update(c.Request.Context(), actor, currentRequestMetadata(c), id, request)
		if err != nil {
			nodeWebhookMutationAbort(c, err)
			return
		}
		timestamp := time.Now().Unix()
		writeNodeWebhookData(c, state, timestamp, nodeWebhookMutationPayload{
			Item: &item,
		})
	}
}

func nodeWebhookStatusHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		store, actor, ok := requireNodeWebhookActor(state, c)
		if !ok {
			return
		}
		id, ok := parseNodeWebhookID(c)
		if !ok {
			return
		}
		var request nodeWebhookStatusRequest
		if !decodeCanonicalNodeHTTPBody(c, &request) {
			return
		}
		if request.Enabled == nil {
			nodeWebhookAbort(c, errNodeWebhookEnabledRequired)
			return
		}
		item, err := store.SetEnabled(c.Request.Context(), actor, currentRequestMetadata(c), id, *request.Enabled, "manual_status")
		if err != nil {
			nodeWebhookMutationAbort(c, err)
			return
		}
		timestamp := time.Now().Unix()
		writeNodeWebhookData(c, state, timestamp, nodeWebhookMutationPayload{
			Item: &item,
		})
	}
}

func nodeWebhookDeleteHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		store, actor, ok := requireNodeWebhookActor(state, c)
		if !ok {
			return
		}
		id, ok := parseNodeWebhookID(c)
		if !ok {
			return
		}
		if !store.Delete(c.Request.Context(), actor, currentRequestMetadata(c), id) {
			nodeWebhookAbort(c, errNodeWebhookNotFound)
			return
		}
		timestamp := time.Now().Unix()
		writeNodeWebhookData(c, state, timestamp, nodeWebhookMutationPayload{})
	}
}

func requireNodeWebhookStoreAccess(state *State, actor *webapi.Actor) (*nodeWebhookStore, error) {
	if state == nil || state.NodeWebhooks == nil {
		return nil, errNodeWebhookStateUnavailable
	}
	if nodeWebhookAnonymous(actor) {
		return nil, webservice.ErrUnauthenticated
	}
	if !nodeWebhookManageableByActor(actor) {
		return nil, webservice.ErrForbidden
	}
	return state.NodeWebhooks, nil
}

func requireNodeWebhookActor(state *State, c *gin.Context) (*nodeWebhookStore, *webapi.Actor, bool) {
	actor := currentActor(c)
	store, err := requireNodeWebhookStoreAccess(state, actor)
	if err != nil {
		nodeWebhookAbort(c, err)
		return nil, nil, false
	}
	return store, actor, true
}

func writeNodeWebhookData(c *gin.Context, state *State, timestamp int64, data interface{}) {
	if c == nil || state == nil {
		return
	}
	if timestamp <= 0 {
		timestamp = time.Now().Unix()
	}
	c.JSON(http.StatusOK, webapi.ManagementDataResponse{
		Data: stampNodeWebhookResponseDataTimestamp(data, timestamp),
		Meta: webapi.ManagementResponseMetaForRequest(currentRequestMetadata(c), timestamp, state.RuntimeIdentity().ConfigEpoch()),
	})
}

func parseNodeWebhookID(c *gin.Context) (int64, bool) {
	if c == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		nodeWebhookAbort(c, errNodeWebhookInvalidID)
		return 0, false
	}
	return id, true
}

func decodeCanonicalNodeHTTPBody(c *gin.Context, dest interface{}) bool {
	if c == nil || dest == nil {
		return false
	}
	raw := bytes.TrimSpace(framework.RequestRawBodyView(c))
	if len(raw) == 0 {
		nodeWebhookAbort(c, errNodeWebhookJSONBodyRequired)
		return false
	}
	if raw[0] != '{' {
		nodeWebhookAbort(c, errNodeWebhookJSONObjectRequired)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		nodeWebhookAbort(c, errNodeWebhookInvalidJSONBody)
		return false
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != nil {
		if err == io.EOF {
			return true
		}
		nodeWebhookAbort(c, errNodeWebhookInvalidJSONBody)
		return false
	}
	nodeWebhookAbort(c, errNodeWebhookInvalidJSONBody)
	return false
}

func nodeWebhookAbort(c *gin.Context, err error) {
	if c == nil {
		return
	}
	status, code, message := webhookMutationErrorDetail(err)
	c.AbortWithStatusJSON(status, webapi.ManagementErrorResponse{
		Error: webapi.ManagementErrorDetail{
			Code:    strings.TrimSpace(code),
			Message: strings.TrimSpace(message),
		},
	})
}

func nodeWebhookMutationAbort(c *gin.Context, err error) {
	nodeWebhookAbort(c, err)
}

func webhookMutationStatus(err error) int {
	status, _, _ := webhookMutationErrorDetail(err)
	return status
}

func webhookMutationErrorDetail(err error) (int, string, string) {
	if err == nil {
		return http.StatusOK, "", ""
	}
	message := strings.TrimSpace(err.Error())
	switch {
	case errors.Is(err, errNodeWebhookStateUnavailable):
		return http.StatusInternalServerError, "node_state_unavailable", errNodeWebhookStateUnavailable.Error()
	case errors.Is(err, webservice.ErrUnauthenticated) || message == webservice.ErrUnauthenticated.Error():
		return http.StatusUnauthorized, "unauthorized", webservice.ErrUnauthenticated.Error()
	case errors.Is(err, webservice.ErrForbidden) || message == webservice.ErrForbidden.Error():
		return http.StatusForbidden, "forbidden", webservice.ErrForbidden.Error()
	case errors.Is(err, errNodeWebhookNotFound) || message == errNodeWebhookNotFound.Error():
		return http.StatusNotFound, "webhook_not_found", errNodeWebhookNotFound.Error()
	case errors.Is(err, errNodeWebhookInvalidID) || message == errNodeWebhookInvalidID.Error():
		return http.StatusBadRequest, "invalid_webhook_id", errNodeWebhookInvalidID.Error()
	case errors.Is(err, errNodeWebhookJSONBodyRequired) || message == errNodeWebhookJSONBodyRequired.Error():
		return http.StatusBadRequest, "json_body_required", errNodeWebhookJSONBodyRequired.Error()
	case errors.Is(err, errNodeWebhookJSONObjectRequired) || message == errNodeWebhookJSONObjectRequired.Error():
		return http.StatusBadRequest, "invalid_json_body", errNodeWebhookJSONObjectRequired.Error()
	case errors.Is(err, errNodeWebhookInvalidJSONBody) || message == errNodeWebhookInvalidJSONBody.Error():
		return http.StatusBadRequest, "invalid_json_body", errNodeWebhookInvalidJSONBody.Error()
	case errors.Is(err, errNodeWebhookEnabledRequired) || message == errNodeWebhookEnabledRequired.Error():
		return http.StatusBadRequest, "enabled_required", errNodeWebhookEnabledRequired.Error()
	case errors.Is(err, errNodeWebhookURLRequired) || message == errNodeWebhookURLRequired.Error():
		return http.StatusBadRequest, "url_required", errNodeWebhookURLRequired.Error()
	case errors.Is(err, errNodeWebhookInvalidURL) || message == errNodeWebhookInvalidURL.Error():
		return http.StatusBadRequest, "invalid_webhook_url", errNodeWebhookInvalidURL.Error()
	case errors.Is(err, errNodeWebhookInvalidMethod) || message == errNodeWebhookInvalidMethod.Error():
		return http.StatusBadRequest, "invalid_webhook_method", errNodeWebhookInvalidMethod.Error()
	case errors.Is(err, errNodeWebhookInvalidContentMode) || message == errNodeWebhookInvalidContentMode.Error():
		return http.StatusBadRequest, "invalid_content_mode", errNodeWebhookInvalidContentMode.Error()
	case errors.Is(err, errNodeWebhookInvalidBodyTmpl) || message == errNodeWebhookInvalidBodyTmpl.Error():
		return http.StatusBadRequest, "invalid_body_template", errNodeWebhookInvalidBodyTmpl.Error()
	case strings.HasPrefix(message, "invalid header_templates"):
		return http.StatusBadRequest, "invalid_header_templates", message
	default:
		return http.StatusInternalServerError, "request_failed", message
	}
}

func writeWSWebhookError(ctx *wsAPIContext, err error) {
	status, code, message := webhookMutationErrorDetail(err)
	writeWSManagementError(ctx, status, code, message, nil)
}

func writeWSWebhookMutationError(ctx *wsAPIContext, err error) {
	writeWSWebhookError(ctx, err)
}

func stampNodeWebhookResponseDataTimestamp(data interface{}, timestamp int64) interface{} {
	switch typed := data.(type) {
	case nodeWebhookListPayload:
		typed.Timestamp = timestamp
		return typed
	case *nodeWebhookListPayload:
		if typed == nil {
			return typed
		}
		cloned := *typed
		cloned.Timestamp = timestamp
		return &cloned
	case nodeWebhookMutationPayload:
		typed.Timestamp = timestamp
		return typed
	case *nodeWebhookMutationPayload:
		if typed == nil {
			return typed
		}
		cloned := *typed
		cloned.Timestamp = timestamp
		return &cloned
	default:
		return data
	}
}

func buildNodeWSWebhookManagementDataFrame(frame nodeWSFrame, state *State, metadata webapi.RequestMetadata, timestamp int64, data interface{}) nodeWSFrame {
	configEpoch := ""
	if state != nil {
		configEpoch = state.RuntimeIdentity().ConfigEpoch()
	}
	return buildNodeWSManagementDataFrame(frame, metadata, configEpoch, timestamp, stampNodeWebhookResponseDataTimestamp(data, timestamp))
}

func dispatchNodeWSWebhookCollectionRequest(state *State, ctx *wsAPIContext, actor *webapi.Actor, metadata webapi.RequestMetadata, method string, response nodeWSFrame) nodeWSFrame {
	store, err := requireNodeWebhookStoreAccess(state, actor)
	if err != nil {
		writeWSWebhookError(ctx, err)
		return buildNodeWSResponseFrame(response, ctx)
	}
	handler, matched := resolveNodeWSManagedCollectionMethod(method, []nodeWSManagedCollectionMethodSpec{
		{
			method: http.MethodGet,
			handler: func() nodeWSFrame {
				timestamp := time.Now().Unix()
				return buildNodeWSWebhookManagementDataFrame(response, state, metadata, timestamp, nodeWebhookListPayload{
					Items: store.ListVisible(actor),
				})
			},
		},
		{
			method: http.MethodPost,
			handler: func() nodeWSFrame {
				var request nodeWebhookWriteRequest
				if !decodeCanonicalNodeWSBody(ctx, &request) {
					return buildNodeWSResponseFrame(response, ctx)
				}
				item, err := store.Create(ctx.BaseContext(), actor, metadata, request)
				if err != nil {
					writeWSWebhookMutationError(ctx, err)
					return buildNodeWSResponseFrame(response, ctx)
				}
				timestamp := time.Now().Unix()
				return buildNodeWSWebhookManagementDataFrame(response, state, metadata, timestamp, nodeWebhookMutationPayload{
					Item: &item,
				})
			},
		},
	})
	if !matched {
		return buildNodeWSMethodNotAllowedManagementFrame(response, ctx)
	}
	return handler()
}

func dispatchNodeWSWebhookItemRequest(state *State, ctx *wsAPIContext, actor *webapi.Actor, metadata webapi.RequestMetadata, method, path string, response nodeWSFrame) nodeWSFrame {
	store, err := requireNodeWebhookStoreAccess(state, actor)
	if err != nil {
		writeWSWebhookError(ctx, err)
		return buildNodeWSResponseFrame(response, ctx)
	}
	request, err := resolveNodeWSManagedItemRequest(path, "/webhooks/")
	if err != nil {
		return buildNodeWSManagedItemLookupErrorFrame(response, ctx, err, "invalid_webhook_id", "invalid webhook id")
	}
	handler, pathMatched, methodMatched := resolveNodeWSManagedItemAction(request.Action, method, []nodeWSManagedItemActionSpec{
		{
			action: "",
			method: http.MethodGet,
			handler: func(request nodeWSManagedItemRequest) nodeWSFrame {
				item, ok := store.GetVisible(actor, request.ID)
				if !ok {
					writeWSWebhookError(ctx, errNodeWebhookNotFound)
					return buildNodeWSResponseFrame(response, ctx)
				}
				timestamp := time.Now().Unix()
				return buildNodeWSWebhookManagementDataFrame(response, state, metadata, timestamp, nodeWebhookMutationPayload{
					Item: &item,
				})
			},
		},
		{
			action: "update",
			method: http.MethodPost,
			handler: func(request nodeWSManagedItemRequest) nodeWSFrame {
				var update nodeWebhookWriteRequest
				if !decodeCanonicalNodeWSBody(ctx, &update) {
					return buildNodeWSResponseFrame(response, ctx)
				}
				item, err := store.Update(ctx.BaseContext(), actor, metadata, request.ID, update)
				if err != nil {
					writeWSWebhookMutationError(ctx, err)
					return buildNodeWSResponseFrame(response, ctx)
				}
				timestamp := time.Now().Unix()
				return buildNodeWSWebhookManagementDataFrame(response, state, metadata, timestamp, nodeWebhookMutationPayload{
					Item: &item,
				})
			},
		},
		{
			action: "status",
			method: http.MethodPost,
			handler: func(request nodeWSManagedItemRequest) nodeWSFrame {
				var statusRequest nodeWebhookStatusRequest
				if !decodeCanonicalNodeWSBody(ctx, &statusRequest) {
					return buildNodeWSResponseFrame(response, ctx)
				}
				if statusRequest.Enabled == nil {
					writeWSWebhookError(ctx, errNodeWebhookEnabledRequired)
					return buildNodeWSResponseFrame(response, ctx)
				}
				item, err := store.SetEnabled(ctx.BaseContext(), actor, metadata, request.ID, *statusRequest.Enabled, "manual_status")
				if err != nil {
					writeWSWebhookMutationError(ctx, err)
					return buildNodeWSResponseFrame(response, ctx)
				}
				timestamp := time.Now().Unix()
				return buildNodeWSWebhookManagementDataFrame(response, state, metadata, timestamp, nodeWebhookMutationPayload{
					Item: &item,
				})
			},
		},
		{
			action: "delete",
			method: http.MethodPost,
			handler: func(request nodeWSManagedItemRequest) nodeWSFrame {
				if !store.Delete(ctx.BaseContext(), actor, metadata, request.ID) {
					writeWSWebhookError(ctx, errNodeWebhookNotFound)
					return buildNodeWSResponseFrame(response, ctx)
				}
				timestamp := time.Now().Unix()
				return buildNodeWSWebhookManagementDataFrame(response, state, metadata, timestamp, nodeWebhookMutationPayload{})
			},
		},
	})
	switch {
	case !pathMatched:
		return buildNodeWSUnknownPathManagementFrame(response, ctx)
	case !methodMatched:
		return buildNodeWSMethodNotAllowedManagementFrame(response, ctx)
	default:
		return handler(request)
	}
}

func newNodeWebhookStore(path string, lookup func() nodeEventResourceLookup, emit func(context.Context, webapi.Event), baseCtx func() context.Context) *nodeWebhookStore {
	store := &nodeWebhookStore{
		path:     strings.TrimSpace(path),
		items:    make(map[int64]nodeWebhookConfig),
		runtime:  make(map[int64]nodeWebhookRuntimeState),
		writer:   newNodeRuntimeStateWriter(path),
		lookup:   lookup,
		baseCtx:  baseCtx,
		emit:     emit,
		changeCh: make(chan struct{}, 1),
	}
	store.load()
	return store
}

func (s *nodeWebhookStore) Changed() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.changeCh
}

func (s *nodeWebhookStore) notifyChanged() {
	if s == nil {
		return
	}
	select {
	case s.changeCh <- struct{}{}:
	default:
	}
}

func (s *nodeWebhookStore) emitEvent(ctx context.Context, event webapi.Event) {
	if s == nil || s.emit == nil {
		return
	}
	s.emit(resolveNodeEmitContext(ctx, s.baseCtx), event)
}

func (s *nodeWebhookStore) emitWebhookEvent(
	ctx context.Context,
	name string,
	action string,
	actor *webapi.Actor,
	metadata webapi.RequestMetadata,
	payload nodeWebhookPayload,
	cause string,
) {
	if s == nil || strings.TrimSpace(name) == "" {
		return
	}
	s.emitEvent(ctx, webapi.Event{
		Name:     strings.TrimSpace(name),
		Resource: "webhook",
		Action:   strings.TrimSpace(action),
		Actor:    cloneActor(actor),
		Metadata: metadata,
		Fields:   webhookEventFields(payload, cause),
	})
}

func (s *nodeWebhookStore) completeWebhookMutation(
	ctx context.Context,
	name string,
	action string,
	actor *webapi.Actor,
	metadata webapi.RequestMetadata,
	payload nodeWebhookPayload,
	cause string,
) {
	if s == nil {
		return
	}
	s.notifyChanged()
	s.emitWebhookEvent(ctx, name, action, actor, metadata, payload, cause)
}

func (s *nodeWebhookStore) load() {
	if s == nil || s.path == "" {
		return
	}
	var persisted nodeWebhookPersistedState
	if err := readNodeRuntimeState(s.path, &persisted); err != nil {
		if !isIgnorableNodeRuntimeStateError(err) {
			logs.Warn("load node webhook state %s error: %v", s.path, err)
		}
		return
	}
	if !nodeRuntimeStateVersionSupported(int(persisted.Version)) {
		logs.Warn("load node webhook state %s error: unsupported version=%d", s.path, persisted.Version)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID = persisted.NextID
	for _, item := range persisted.Items {
		cloned := cloneNodeWebhookConfig(item)
		if cloned.ID <= 0 {
			continue
		}
		cloned.nodeEventSinkConfig = normalizeNodeEventSinkConfig(cloned.nodeEventSinkConfig)
		if !nodeWebhookOwnerSupported(cloned.Owner) {
			cloned.Enabled = false
		}
		s.items[cloned.ID] = cloned
		if cloned.ID > s.nextID {
			s.nextID = cloned.ID
		}
	}
}

func (s *nodeWebhookStore) persistLocked() {
	if s == nil || s.path == "" {
		return
	}
	state := nodeWebhookPersistedState{
		Version: nodeRuntimeStateVersion,
		NextID:  s.nextID,
	}
	ids := make([]int64, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		state.Items = append(state.Items, cloneNodeWebhookConfig(s.items[id]))
	}
	if s.writer != nil {
		s.writer.Store(state)
		return
	}
	writeNodeRuntimeState(s.path, state)
}

func (s *nodeWebhookStore) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		if s.writer != nil {
			s.writer.Close()
		}
	})
}

func (s *nodeWebhookStore) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.nextID = 0
	s.items = make(map[int64]nodeWebhookConfig)
	s.runtime = make(map[int64]nodeWebhookRuntimeState)
	s.persistLocked()
	s.mu.Unlock()
	s.notifyChanged()
}

func (s *nodeWebhookStore) Active() []nodeWebhookConfig {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]nodeWebhookConfig, 0, len(s.items))
	for _, item := range s.items {
		if !item.Enabled || !nodeWebhookOwnerSupported(item.Owner) {
			continue
		}
		items = append(items, cloneNodeWebhookConfig(item))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (s *nodeWebhookStore) activeConfigMatches(expected nodeWebhookConfig) bool {
	if s == nil || expected.ID <= 0 {
		return false
	}
	s.mu.RLock()
	current, ok := s.items[expected.ID]
	s.mu.RUnlock()
	return ok && current.Enabled && nodeWebhookOwnerSupported(current.Owner) && nodeWebhookConfigEqual(current, expected)
}

func (s *nodeWebhookStore) ListVisible(actor *webapi.Actor) []nodeWebhookPayload {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]nodeWebhookPayload, 0, len(s.items))
	for _, item := range s.items {
		if !nodeWebhookVisibleToActor(actor, item.Owner) {
			continue
		}
		items = append(items, buildNodeWebhookPayload(item, s.runtime[item.ID]))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (s *nodeWebhookStore) GetVisible(actor *webapi.Actor, id int64) (nodeWebhookPayload, bool) {
	if s == nil || id <= 0 {
		return nodeWebhookPayload{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok || !nodeWebhookVisibleToActor(actor, item.Owner) {
		return nodeWebhookPayload{}, false
	}
	return buildNodeWebhookPayload(item, s.runtime[id]), true
}

func (s *nodeWebhookStore) Create(ctx context.Context, actor *webapi.Actor, metadata webapi.RequestMetadata, request nodeWebhookWriteRequest) (nodeWebhookPayload, error) {
	switch {
	case nodeWebhookAnonymous(actor):
		return nodeWebhookPayload{}, webservice.ErrUnauthenticated
	case !nodeWebhookManageableByActor(actor):
		return nodeWebhookPayload{}, webservice.ErrForbidden
	}
	config, err := buildNodeWebhookConfig(actor, 0, nil, request)
	if err != nil {
		return nodeWebhookPayload{}, err
	}
	s.mu.Lock()
	s.nextID++
	config.ID = s.nextID
	now := time.Now().Unix()
	config.CreatedAt = now
	config.UpdatedAt = now
	s.items[config.ID] = cloneNodeWebhookConfig(config)
	s.persistLocked()
	payload := buildNodeWebhookPayload(config, s.runtime[config.ID])
	s.mu.Unlock()
	s.completeWebhookMutation(ctx, "webhook.created", "create", actor, metadata, payload, "")
	return payload, nil
}

func (s *nodeWebhookStore) Update(ctx context.Context, actor *webapi.Actor, metadata webapi.RequestMetadata, id int64, request nodeWebhookWriteRequest) (nodeWebhookPayload, error) {
	if s == nil || id <= 0 {
		return nodeWebhookPayload{}, errNodeWebhookNotFound
	}
	s.mu.Lock()
	current, ok := s.items[id]
	if !ok || !nodeWebhookMutableByActor(actor, current.Owner) {
		s.mu.Unlock()
		return nodeWebhookPayload{}, errNodeWebhookNotFound
	}
	config, err := buildNodeWebhookConfig(current.Owner, id, &current, request)
	if err != nil {
		s.mu.Unlock()
		return nodeWebhookPayload{}, err
	}
	config.CreatedAt = current.CreatedAt
	runtime := s.runtime[id]
	configChanged := !nodeWebhookConfigEqual(current, config)
	if !configChanged {
		payload := buildNodeWebhookPayload(current, runtime)
		s.mu.Unlock()
		return payload, nil
	}
	now := time.Now().Unix()
	config.UpdatedAt = now
	applyNodeWebhookEnabledTransition(&runtime, current.Enabled, config.Enabled, nodeWebhookDisableConfigUpdate, now)
	s.items[id] = cloneNodeWebhookConfig(config)
	s.runtime[id] = runtime
	s.persistLocked()
	payload := buildNodeWebhookPayload(config, runtime)
	s.mu.Unlock()
	s.completeWebhookMutation(ctx, "webhook.updated", "update", actor, metadata, payload, "")
	return payload, nil
}

func (s *nodeWebhookStore) SetEnabled(ctx context.Context, actor *webapi.Actor, metadata webapi.RequestMetadata, id int64, enabled bool, reason string) (nodeWebhookPayload, error) {
	if s == nil || id <= 0 {
		return nodeWebhookPayload{}, errNodeWebhookNotFound
	}
	s.mu.Lock()
	current, ok := s.items[id]
	if !ok || !nodeWebhookMutableByActor(actor, current.Owner) {
		s.mu.Unlock()
		return nodeWebhookPayload{}, errNodeWebhookNotFound
	}
	state := s.runtime[id]
	if current.Enabled == enabled {
		payload := buildNodeWebhookPayload(current, state)
		s.mu.Unlock()
		return payload, nil
	}
	now := time.Now().Unix()
	previousEnabled := current.Enabled
	current.Enabled = enabled
	current.UpdatedAt = now
	s.items[id] = current
	applyNodeWebhookEnabledTransition(&state, previousEnabled, enabled, reason, now)
	s.runtime[id] = state
	s.persistLocked()
	payload := buildNodeWebhookPayload(current, state)
	s.mu.Unlock()
	s.completeWebhookMutation(ctx, "webhook.status_changed", "status", actor, metadata, payload, strings.TrimSpace(reason))
	return payload, nil
}

func applyNodeWebhookEnabledTransition(runtime *nodeWebhookRuntimeState, previousEnabled bool, nextEnabled bool, reason string, now int64) {
	if runtime == nil || previousEnabled == nextEnabled {
		return
	}
	if nextEnabled {
		runtime.LastDisabledReason = ""
		runtime.LastDisabledAt = 0
		return
	}
	runtime.LastDisabledReason = strings.TrimSpace(reason)
	runtime.LastDisabledAt = now
}

func (s *nodeWebhookStore) Delete(ctx context.Context, actor *webapi.Actor, metadata webapi.RequestMetadata, id int64) bool {
	if s == nil || id <= 0 {
		return false
	}
	s.mu.Lock()
	current, ok := s.items[id]
	if !ok || !nodeWebhookMutableByActor(actor, current.Owner) {
		s.mu.Unlock()
		return false
	}
	payload := buildNodeWebhookPayload(current, s.runtime[id])
	delete(s.items, id)
	delete(s.runtime, id)
	s.persistLocked()
	s.mu.Unlock()
	s.completeWebhookMutation(ctx, "webhook.deleted", "delete", actor, metadata, payload, "")
	return true
}

func (s *nodeWebhookStore) mutateWebhookRuntime(id int64, mutate func(*nodeWebhookRuntimeState, int64)) (nodeWebhookPayload, bool) {
	if s == nil || id <= 0 {
		return nodeWebhookPayload{}, false
	}
	s.mu.Lock()
	current, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return nodeWebhookPayload{}, false
	}
	state := s.runtime[id]
	if mutate != nil {
		mutate(&state, time.Now().Unix())
	}
	s.runtime[id] = state
	payload := buildNodeWebhookPayload(current, state)
	s.mu.Unlock()
	return payload, true
}

func (s *nodeWebhookStore) emitWebhookRuntimeEvent(ctx context.Context, name string, payload nodeWebhookPayload) {
	s.emitWebhookEvent(ctx, name, "delivery", nil, webapi.RequestMetadata{}, payload, "")
}

func updateNodeWebhookDeliveredRuntime(state *nodeWebhookRuntimeState, statusCode int, now int64) {
	if state == nil {
		return
	}
	state.LastDeliveredAt = now
	state.LastStatusCode = statusCode
	state.Deliveries++
	state.ConsecutiveFailures = 0
	state.LastError = ""
	state.LastErrorAt = 0
}

func updateNodeWebhookFailedRuntime(state *nodeWebhookRuntimeState, err error, statusCode int, now int64) {
	if state == nil {
		return
	}
	state.LastStatusCode = statusCode
	state.Failures++
	state.ConsecutiveFailures++
	if err != nil {
		state.LastError = strings.TrimSpace(err.Error())
		state.LastErrorAt = now
		return
	}
	state.LastError = ""
	state.LastErrorAt = 0
}

func (s *nodeWebhookStore) NoteDelivered(ctx context.Context, id int64, statusCode int) {
	payload, ok := s.mutateWebhookRuntime(id, func(state *nodeWebhookRuntimeState, now int64) {
		updateNodeWebhookDeliveredRuntime(state, statusCode, now)
	})
	if !ok {
		return
	}
	s.emitWebhookRuntimeEvent(ctx, "webhook.delivery_succeeded", payload)
}

func (s *nodeWebhookStore) NoteFailed(ctx context.Context, id int64, err error, statusCode int) {
	payload, ok := s.mutateWebhookRuntime(id, func(state *nodeWebhookRuntimeState, now int64) {
		updateNodeWebhookFailedRuntime(state, err, statusCode, now)
	})
	if !ok {
		return
	}
	s.emitWebhookRuntimeEvent(ctx, "webhook.delivery_failed", payload)
}

func (s *nodeWebhookStore) ScrubEvent(ctx context.Context, event webapi.Event) {
	if s == nil {
		return
	}
	resource := strings.ToLower(strings.TrimSpace(event.Resource))
	if resource == "" {
		return
	}
	lookup := nodeEventResourceLookup{}
	if s.lookup != nil {
		lookup = s.lookup()
	}
	candidates := s.snapshotScrubCandidates()
	plans := buildNodeWebhookScrubPlans(candidates, lookup)
	if len(plans) == 0 {
		return
	}
	s.mu.Lock()
	changed := false
	emitted := make([]webapi.Event, 0, len(plans))
	for _, plan := range plans {
		item, runtime, updated, ok := s.applyScrubPlanLocked(plan)
		if !ok || !updated {
			continue
		}
		changed = true
		payload := buildNodeWebhookPayload(item, runtime)
		emitted = append(emitted, webapi.Event{
			Name:     "webhook.selector_scrubbed",
			Resource: "webhook",
			Action:   "update",
			Actor:    cloneActor(event.Actor),
			Metadata: event.Metadata,
			Fields:   webhookEventFields(payload, event.Name),
		})
	}
	if changed {
		s.persistLocked()
	}
	s.mu.Unlock()
	if changed {
		s.notifyChanged()
	}
	for _, emittedEvent := range emitted {
		s.emitEvent(ctx, emittedEvent)
	}
}

func (s *nodeWebhookStore) snapshotScrubCandidates() []nodeWebhookScrubCandidate {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidates := make([]nodeWebhookScrubCandidate, 0, len(s.items))
	for id, item := range s.items {
		candidates = append(candidates, nodeWebhookScrubCandidate{
			id:   id,
			item: cloneNodeWebhookConfig(item),
		})
	}
	return candidates
}

func buildNodeWebhookScrubPlans(candidates []nodeWebhookScrubCandidate, lookup nodeEventResourceLookup) []nodeWebhookScrubPlan {
	if len(candidates) == 0 {
		return nil
	}
	lookup = memoizeNodeEventResourceLookup(lookup)
	plans := make([]nodeWebhookScrubPlan, 0, len(candidates))
	for _, candidate := range candidates {
		scrubbed, disableSelector := scrubNodeEventSelector(candidate.item.Selector, lookup)
		disableOwner := nodeWebhookOwnerInvalid(candidate.item.Owner, lookup)
		selectorChanged := !nodeEventSelectorEqual(candidate.item.Selector, scrubbed)
		if !selectorChanged && !disableSelector && !disableOwner {
			continue
		}
		plans = append(plans, nodeWebhookScrubPlan{
			id:              candidate.id,
			selector:        candidate.item.Selector,
			owner:           cloneActor(candidate.item.Owner),
			scrubbed:        scrubbed,
			selectorChanged: selectorChanged,
			disableSelector: disableSelector,
			disableOwner:    disableOwner,
		})
	}
	return plans
}

func (s *nodeWebhookStore) applyScrubPlanLocked(plan nodeWebhookScrubPlan) (nodeWebhookConfig, nodeWebhookRuntimeState, bool, bool) {
	if s == nil {
		return nodeWebhookConfig{}, nodeWebhookRuntimeState{}, false, false
	}
	current, ok := s.items[plan.id]
	if !ok {
		return nodeWebhookConfig{}, nodeWebhookRuntimeState{}, false, false
	}
	runtime := s.runtime[plan.id]
	updated := false
	selectorMatchesSnapshot := nodeEventSelectorEqual(current.Selector, plan.selector)
	ownerMatchesSnapshot := equalActors(current.Owner, plan.owner)
	if plan.selectorChanged && selectorMatchesSnapshot {
		current.Selector = plan.scrubbed
		updated = true
	}
	if current.Enabled {
		switch {
		case plan.disableOwner && ownerMatchesSnapshot:
			current.Enabled = false
			current.UpdatedAt = time.Now().Unix()
			runtime.LastDisabledAt = current.UpdatedAt
			runtime.LastDisabledReason = "owner_invalid"
			updated = true
		case plan.disableSelector && selectorMatchesSnapshot:
			current.Enabled = false
			current.UpdatedAt = time.Now().Unix()
			runtime.LastDisabledAt = current.UpdatedAt
			runtime.LastDisabledReason = "selector_invalid"
			runtime.LastSelectorScrubbedAt = current.UpdatedAt
			updated = true
		}
	}
	if !updated {
		return nodeWebhookConfig{}, nodeWebhookRuntimeState{}, false, true
	}
	if current.UpdatedAt == 0 {
		current.UpdatedAt = time.Now().Unix()
	}
	s.items[plan.id] = current
	s.runtime[plan.id] = runtime
	return current, runtime, true, true
}

func buildNodeWebhookConfig(owner *webapi.Actor, id int64, current *nodeWebhookConfig, request nodeWebhookWriteRequest) (nodeWebhookConfig, error) {
	if nodeWebhookAnonymous(owner) {
		return nodeWebhookConfig{}, webservice.ErrUnauthenticated
	}
	if !nodeWebhookOwnerSupported(owner) {
		return nodeWebhookConfig{}, webservice.ErrForbidden
	}
	targetURL := strings.TrimSpace(request.URL)
	if targetURL == "" {
		return nodeWebhookConfig{}, errNodeWebhookURLRequired
	}
	parsed, err := url.Parse(targetURL)
	if err != nil || parsed == nil || !parsed.IsAbs() {
		return nodeWebhookConfig{}, errNodeWebhookInvalidURL
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https":
	default:
		return nodeWebhookConfig{}, errNodeWebhookInvalidURL
	}
	method := "POST"
	if current != nil && strings.TrimSpace(current.Method) != "" {
		method = current.Method
	}
	if strings.TrimSpace(request.Method) != "" {
		method = strings.ToUpper(strings.TrimSpace(request.Method))
	}
	if method != http.MethodPost {
		return nodeWebhookConfig{}, errNodeWebhookInvalidMethod
	}
	timeoutSeconds := nodeWebhookDefaultTimeoutSeconds
	if current != nil && current.TimeoutSeconds > 0 {
		timeoutSeconds = current.TimeoutSeconds
	}
	if request.TimeoutSeconds != nil {
		timeoutSeconds = *request.TimeoutSeconds
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = nodeWebhookDefaultTimeoutSeconds
	}
	if timeoutSeconds > nodeWebhookMaxTimeoutSeconds {
		timeoutSeconds = nodeWebhookMaxTimeoutSeconds
	}
	config := nodeWebhookConfig{
		ID:             id,
		URL:            targetURL,
		Method:         method,
		TimeoutSeconds: timeoutSeconds,
		Owner:          cloneActor(owner),
		nodeEventSinkConfig: nodeEventSinkConfig{
			Name: request.Name,
			Selector: nodeEventSelector{
				EventNames: request.EventNames,
				Resources:  request.Resources,
				Actions:    request.Actions,
				UserIDs:    request.UserIDs,
				ClientIDs:  request.ClientIDs,
				TunnelIDs:  request.TunnelIDs,
				HostIDs:    request.HostIDs,
			},
			ContentMode:     request.ContentMode,
			ContentType:     request.ContentType,
			BodyTemplate:    request.BodyTemplate,
			HeaderTemplates: cloneNodeHeaderTemplates(request.HeaderTemplates),
		},
	}
	if current != nil {
		config.Enabled = current.Enabled
		config.CreatedAt = current.CreatedAt
	}
	if request.Enabled != nil {
		config.Enabled = *request.Enabled
	}
	if current == nil && request.Enabled == nil {
		config.Enabled = true
	}
	config.nodeEventSinkConfig, err = prepareNodeEventSinkConfig(config.nodeEventSinkConfig)
	if err != nil {
		return nodeWebhookConfig{}, err
	}
	if config.ContentMode == "" {
		return nodeWebhookConfig{}, errNodeWebhookInvalidContentMode
	}
	return config, nil
}

func nodeWebhookAnonymous(actor *webapi.Actor) bool {
	return actor == nil || strings.TrimSpace(actor.Kind) == "" || strings.EqualFold(actor.Kind, "anonymous")
}

func nodeWebhookManageableByActor(actor *webapi.Actor) bool {
	if nodeWebhookAnonymous(actor) {
		return false
	}
	if actor.IsAdmin {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(actor.Kind), "platform_admin")
}

func nodeWebhookOwnerSupported(owner *webapi.Actor) bool {
	return nodeWebhookManageableByActor(owner)
}

func nodeWebhookVisibleToActor(actor *webapi.Actor, owner *webapi.Actor) bool {
	if !nodeWebhookManageableByActor(actor) || owner == nil || !nodeWebhookOwnerSupported(owner) {
		return false
	}
	if actor.IsAdmin {
		return true
	}
	if strings.TrimSpace(actor.SubjectID) != "" && strings.TrimSpace(actor.SubjectID) == strings.TrimSpace(owner.SubjectID) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(actor.Kind), "platform_admin") {
		return strings.TrimSpace(webapi.NodeActorPlatformID(actor)) != "" && strings.TrimSpace(webapi.NodeActorPlatformID(actor)) == strings.TrimSpace(webapi.NodeActorPlatformID(owner))
	}
	return false
}

func nodeWebhookMutableByActor(actor *webapi.Actor, owner *webapi.Actor) bool {
	return nodeWebhookVisibleToActor(actor, owner)
}

func nodeWebhookOwnerInvalid(owner *webapi.Actor, lookup nodeEventResourceLookup) bool {
	if owner == nil {
		return true
	}
	if !nodeWebhookOwnerSupported(owner) {
		return true
	}
	if owner.IsAdmin {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(owner.Kind)) {
	case "platform_admin", "platform_user":
		platformID := strings.TrimSpace(webapi.NodeActorPlatformID(owner))
		if platformID == "" || (lookup.PlatformExists != nil && !lookup.PlatformExists(platformID)) {
			return true
		}
		serviceUserID := webapi.NodeActorServiceUserID(owner)
		return serviceUserID > 0 && lookup.UserExists != nil && !lookup.UserExists(serviceUserID)
	}
	return false
}

func buildNodeWebhookPayload(config nodeWebhookConfig, runtime nodeWebhookRuntimeState) nodeWebhookPayload {
	payload := nodeWebhookPayload{
		ID:                  config.ID,
		Name:                config.Name,
		URL:                 config.URL,
		Method:              config.Method,
		Enabled:             config.Enabled,
		TimeoutSeconds:      config.TimeoutSeconds,
		EventNames:          append([]string(nil), config.Selector.EventNames...),
		Resources:           append([]string(nil), config.Selector.Resources...),
		Actions:             append([]string(nil), config.Selector.Actions...),
		UserIDs:             append([]int(nil), config.Selector.UserIDs...),
		ClientIDs:           append([]int(nil), config.Selector.ClientIDs...),
		TunnelIDs:           append([]int(nil), config.Selector.TunnelIDs...),
		HostIDs:             append([]int(nil), config.Selector.HostIDs...),
		ContentMode:         config.ContentMode,
		ContentType:         config.ContentType,
		BodyTemplate:        config.BodyTemplate,
		HeaderTemplates:     cloneNodeHeaderTemplates(config.HeaderTemplates),
		CreatedAt:           config.CreatedAt,
		UpdatedAt:           config.UpdatedAt,
		LastDeliveredAt:     runtime.LastDeliveredAt,
		LastError:           runtime.LastError,
		LastErrorAt:         runtime.LastErrorAt,
		LastStatusCode:      runtime.LastStatusCode,
		Deliveries:          runtime.Deliveries,
		Failures:            runtime.Failures,
		ConsecutiveFailures: runtime.ConsecutiveFailures,
		LastDisabledReason:  runtime.LastDisabledReason,
		LastDisabledAt:      runtime.LastDisabledAt,
	}
	if config.Owner != nil {
		payload.Owner = nodeWebhookOwnerPayload{
			Kind:       config.Owner.Kind,
			SubjectID:  config.Owner.SubjectID,
			Username:   config.Owner.Username,
			PlatformID: webapi.NodeActorPlatformID(config.Owner),
		}
	}
	return payload
}

func webhookEventFields(payload nodeWebhookPayload, cause string) map[string]interface{} {
	headerTemplates := map[string]string{}
	for key, value := range payload.HeaderTemplates {
		headerTemplates[key] = value
	}
	fields := map[string]interface{}{
		"id":               payload.ID,
		"name":             payload.Name,
		"url":              payload.URL,
		"method":           payload.Method,
		"enabled":          payload.Enabled,
		"timeout_seconds":  payload.TimeoutSeconds,
		"event_names":      append([]string{}, payload.EventNames...),
		"resources":        append([]string{}, payload.Resources...),
		"actions":          append([]string{}, payload.Actions...),
		"user_ids":         append([]int{}, payload.UserIDs...),
		"client_ids":       append([]int{}, payload.ClientIDs...),
		"tunnel_ids":       append([]int{}, payload.TunnelIDs...),
		"host_ids":         append([]int{}, payload.HostIDs...),
		"content_mode":     payload.ContentMode,
		"content_type":     payload.ContentType,
		"body_template":    payload.BodyTemplate,
		"header_templates": headerTemplates,
		"owner": map[string]interface{}{
			"kind":        payload.Owner.Kind,
			"subject_id":  payload.Owner.SubjectID,
			"username":    payload.Owner.Username,
			"platform_id": payload.Owner.PlatformID,
		},
		"created_at":           payload.CreatedAt,
		"updated_at":           payload.UpdatedAt,
		"last_delivered_at":    payload.LastDeliveredAt,
		"last_error":           payload.LastError,
		"last_error_at":        payload.LastErrorAt,
		"last_status_code":     payload.LastStatusCode,
		"deliveries":           payload.Deliveries,
		"failures":             payload.Failures,
		"consecutive_failures": payload.ConsecutiveFailures,
		"last_disabled_reason": payload.LastDisabledReason,
		"last_disabled_at":     payload.LastDisabledAt,
	}
	if cause = strings.TrimSpace(cause); cause != "" {
		fields["cause_event"] = cause
	}
	return fields
}

func cloneNodeWebhookConfig(config nodeWebhookConfig) nodeWebhookConfig {
	cloned := config
	cloned.Owner = cloneActor(config.Owner)
	cloned.nodeEventSinkConfig = cloneNodeEventSinkConfig(config.nodeEventSinkConfig)
	return cloned
}

func cloneNodeHeaderTemplates(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func nodeEventSelectorEqual(left, right nodeEventSelector) bool {
	return equalIntSlices(left.UserIDs, right.UserIDs) &&
		equalIntSlices(left.ClientIDs, right.ClientIDs) &&
		equalIntSlices(left.TunnelIDs, right.TunnelIDs) &&
		equalIntSlices(left.HostIDs, right.HostIDs) &&
		equalStringSlices(left.EventNames, right.EventNames) &&
		equalStringSlices(left.Resources, right.Resources) &&
		equalStringSlices(left.Actions, right.Actions)
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalIntSlices(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func equalActors(left, right *webapi.Actor) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Kind != right.Kind || left.SubjectID != right.SubjectID || left.Username != right.Username || left.IsAdmin != right.IsAdmin {
		return false
	}
	if !equalIntSlices(left.ClientIDs, right.ClientIDs) || !equalStringSlices(left.Roles, right.Roles) || !equalStringSlices(left.Permissions, right.Permissions) {
		return false
	}
	return equalStringMap(left.Attributes, right.Attributes)
}

func buildNodeWebhookResourceLookup(state *State) func() nodeEventResourceLookup {
	return func() nodeEventResourceLookup {
		lookup := nodeEventResourceLookup{}
		if state == nil {
			return lookup
		}
		repo := state.backend().Repository
		lookup.UserExists = func(id int) bool {
			if id <= 0 || repo == nil {
				return false
			}
			item, err := repo.GetUser(id)
			return err == nil && item != nil
		}
		lookup.ClientExists = func(id int) bool {
			if id <= 0 || repo == nil {
				return false
			}
			item, err := repo.GetClient(id)
			return err == nil && item != nil
		}
		lookup.TunnelExists = func(id int) bool {
			if id <= 0 || repo == nil {
				return false
			}
			item, err := repo.GetTunnel(id)
			return err == nil && item != nil
		}
		lookup.HostExists = func(id int) bool {
			if id <= 0 || repo == nil {
				return false
			}
			item, err := repo.GetHost(id)
			return err == nil && item != nil
		}
		lookup.PlatformExists = func(platformID string) bool {
			platformID = strings.TrimSpace(platformID)
			if platformID == "" {
				return false
			}
			cfg := state.CurrentConfig()
			if cfg == nil {
				return false
			}
			for _, platform := range cfg.Runtime.EnabledManagementPlatforms() {
				if strings.TrimSpace(platform.PlatformID) == platformID {
					return true
				}
			}
			return false
		}
		return lookup
	}
}

type nodeWebhookDispatcherManager struct {
	state   *State
	store   *nodeWebhookStore
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	workers map[int64]*nodeWebhookWorker
}

type nodeWebhookWorker struct {
	config nodeWebhookConfig
	ctx    context.Context
	cancel context.CancelFunc
	sub    *nodeEventSubscription
	client *http.Client
}

func newNodeWebhookDispatcherManager(state *State) *nodeWebhookDispatcherManager {
	parent := context.Background()
	if state != nil {
		parent = state.BaseContext()
	}
	ctx, cancel := context.WithCancel(parent)
	return &nodeWebhookDispatcherManager{
		state:   state,
		ctx:     ctx,
		cancel:  cancel,
		workers: make(map[int64]*nodeWebhookWorker),
	}
}

func StartNodeWebhookDispatchers(state *State) func() {
	if state == nil || state.NodeEvents == nil || state.NodeWebhooks == nil {
		return func() {}
	}
	manager := newNodeWebhookDispatcherManager(state)
	manager.store = state.NodeWebhooks
	manager.syncWorkers()
	manager.wg.Add(1)
	go manager.watch()
	return func() {
		manager.cancel()
		manager.wg.Wait()
		manager.stopAll()
	}
}

func (m *nodeWebhookDispatcherManager) watch() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case _, ok := <-m.store.Changed():
			if !ok {
				return
			}
			m.syncWorkers()
		}
	}
}

func (m *nodeWebhookDispatcherManager) syncWorkers() {
	if m == nil || m.store == nil || m.state == nil || m.state.NodeEvents == nil {
		return
	}
	active := m.store.Active()
	activeByID := make(map[int64]nodeWebhookConfig, len(active))
	for _, item := range active {
		activeByID[item.ID] = item
	}
	toStop, toStart := m.planWorkerSync(activeByID)
	stopNodeWebhookWorkers(toStop)
	for _, item := range toStart {
		if m.ctx != nil && m.ctx.Err() != nil {
			return
		}
		worker := m.newWorker(item)
		if worker == nil {
			continue
		}
		if !m.installWorker(worker) {
			worker.stop()
			continue
		}
		go m.runWorker(worker)
	}
}

func (m *nodeWebhookDispatcherManager) stopAll() {
	for _, worker := range m.takeAllWorkers() {
		if worker != nil {
			worker.stop()
		}
	}
}

func (m *nodeWebhookDispatcherManager) planWorkerSync(activeByID map[int64]nodeWebhookConfig) ([]*nodeWebhookWorker, []nodeWebhookConfig) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	toStop := make([]*nodeWebhookWorker, 0)
	toStart := make([]nodeWebhookConfig, 0)
	for id, worker := range m.workers {
		next, ok := activeByID[id]
		if ok && nodeWebhookConfigEqual(worker.config, next) {
			continue
		}
		toStop = append(toStop, worker)
		delete(m.workers, id)
	}
	for id, item := range activeByID {
		if _, ok := m.workers[id]; ok {
			continue
		}
		toStart = append(toStart, item)
	}
	return toStop, toStart
}

func (m *nodeWebhookDispatcherManager) installWorker(worker *nodeWebhookWorker) bool {
	if m == nil || worker == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx != nil && m.ctx.Err() != nil {
		return false
	}
	if m.workers == nil {
		m.workers = make(map[int64]*nodeWebhookWorker)
	}
	if _, ok := m.workers[worker.config.ID]; ok {
		return false
	}
	m.workers[worker.config.ID] = worker
	m.wg.Add(1)
	return true
}

func (m *nodeWebhookDispatcherManager) takeAllWorkers() []*nodeWebhookWorker {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	workers := make([]*nodeWebhookWorker, 0, len(m.workers))
	for id, worker := range m.workers {
		workers = append(workers, worker)
		delete(m.workers, id)
	}
	return workers
}

func stopNodeWebhookWorkers(workers []*nodeWebhookWorker) {
	for _, worker := range workers {
		worker.stop()
	}
}

func (m *nodeWebhookDispatcherManager) newWorker(config nodeWebhookConfig) *nodeWebhookWorker {
	ctx, cancel := context.WithCancel(m.ctx)
	worker := &nodeWebhookWorker{
		config: config,
		ctx:    ctx,
		cancel: cancel,
		client: &http.Client{Timeout: time.Duration(config.TimeoutSeconds) * time.Second},
	}
	worker.sub = m.subscribeWorker(worker)
	if worker.sub == nil {
		cancel()
		return nil
	}
	return worker
}

func (m *nodeWebhookDispatcherManager) runWorker(worker *nodeWebhookWorker) {
	defer m.wg.Done()
	if worker == nil || worker.sub == nil {
		return
	}
	defer worker.stop()
	var lastSequence int64
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-worker.ctx.Done():
			return
		case event := <-worker.sub.Events():
			m.processWebhookEvent(worker, event, &lastSequence, false)
		case <-worker.sub.Done():
			if !m.recoverWebhookWorker(worker, &lastSequence) {
				return
			}
		}
	}
}

func (m *nodeWebhookDispatcherManager) workerActive(worker *nodeWebhookWorker) bool {
	if m == nil || worker == nil {
		return false
	}
	if m.ctx != nil && m.ctx.Err() != nil {
		return false
	}
	if worker.ctx != nil && worker.ctx.Err() != nil {
		return false
	}
	if m.store != nil && !m.store.activeConfigMatches(worker.config) {
		return false
	}
	return true
}

func (m *nodeWebhookDispatcherManager) recoverWebhookWorker(worker *nodeWebhookWorker, lastSequence *int64) bool {
	if m == nil || worker == nil || worker.sub == nil || m.state == nil || m.state.NodeEvents == nil {
		return false
	}
	if !m.workerActive(worker) {
		return false
	}
	currentSub := worker.sub
	drainNodeSubscriptionEvents(currentSub, func(event webapi.Event) {
		m.processWebhookEvent(worker, event, lastSequence, true)
	})
	if overflowSequence, overflowed := currentSub.OverflowSequence(); overflowed {
		m.replayWebhookWorker(worker, lastSequence, overflowSequence)
	}
	if m.ctx.Err() != nil || worker.ctx.Err() != nil {
		return false
	}
	worker.sub = m.subscribeWorker(worker)
	return worker.sub != nil
}

func (m *nodeWebhookDispatcherManager) replayWebhookWorker(worker *nodeWebhookWorker, lastSequence *int64, overflowSequence int64) {
	if m == nil || worker == nil || m.state == nil || m.state.NodeEventLog == nil {
		return
	}
	after := int64(0)
	if lastSequence != nil {
		after = *lastSequence
	}
	for {
		if m.ctx.Err() != nil || worker.ctx.Err() != nil {
			return
		}
		snapshot := replayNodeChanges(m.state, worker.config.Owner, after, 100)
		if snapshot.Gap {
			logs.Warn("node webhook replay for webhook %d observed event-log gap after=%d overflow_sequence=%d oldest=%d cursor=%d", worker.config.ID, after, overflowSequence, snapshot.OldestCursor, snapshot.Cursor)
		}
		if len(snapshot.Items) == 0 {
			return
		}
		for _, event := range snapshot.Items {
			if m.ctx.Err() != nil || worker.ctx.Err() != nil {
				return
			}
			after = maxInt64(after, event.Sequence)
			m.processWebhookEvent(worker, event, lastSequence, true)
		}
		if !snapshot.HasMore {
			return
		}
	}
}

func (m *nodeWebhookDispatcherManager) subscribeWorker(worker *nodeWebhookWorker) *nodeEventSubscription {
	if m == nil || worker == nil || m.state == nil || m.state.NodeEvents == nil {
		return nil
	}
	return m.state.NodeEvents.SubscribeWithOptions(worker.config.Owner, worker.matchesEvent, 128)
}

func (m *nodeWebhookDispatcherManager) processWebhookEvent(worker *nodeWebhookWorker, event webapi.Event, lastSequence *int64, requireSelectorMatch bool) bool {
	if !m.workerActive(worker) {
		return false
	}
	if lastSequence != nil && event.Sequence > *lastSequence {
		*lastSequence = event.Sequence
	}
	if !shouldDeliverNodeWebhook(event) {
		return false
	}
	if requireSelectorMatch && !worker.matchesEvent(event) {
		return false
	}
	m.deliverWebhookEvent(worker, event)
	return true
}

func (m *nodeWebhookDispatcherManager) deliverWebhookEvent(worker *nodeWebhookWorker, event webapi.Event) {
	if m == nil || worker == nil || m.store == nil || !m.workerActive(worker) {
		return
	}
	payload, err := renderNodeEventSinkPayload(m.state, strconv.FormatInt(worker.config.ID, 10), "webhook", worker.config.nodeEventSinkConfig, event)
	if err != nil {
		if nodeDeliveryCanceled(worker.ctx, err) {
			return
		}
		m.store.NoteFailed(worker.ctx, worker.config.ID, err, 0)
		return
	}
	statusCode, err := deliverNodeWebhook(worker.ctx, worker.client, worker.config, payload)
	if err != nil {
		if nodeDeliveryCanceled(worker.ctx, err) {
			return
		}
		m.store.NoteFailed(worker.ctx, worker.config.ID, err, statusCode)
		logs.Warn("deliver node webhook %d error: %v", worker.config.ID, err)
		return
	}
	m.store.NoteDelivered(worker.ctx, worker.config.ID, statusCode)
}

func shouldDeliverNodeWebhook(event webapi.Event) bool {
	return webapi.ShouldDeliverNodeSinkEventName(event.Name)
}

func (w *nodeWebhookWorker) matchesEvent(event webapi.Event) bool {
	if w == nil {
		return false
	}
	return matchesNodeEventSelector(w.config.Selector, event)
}

func deliverNodeWebhook(ctx context.Context, client *http.Client, config nodeWebhookConfig, payload nodeRenderedEventSinkPayload) (int, error) {
	if client == nil {
		client = &http.Client{Timeout: time.Duration(nodeWebhookDefaultTimeoutSeconds) * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(payload.Body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", payload.ContentType)
	req.Header.Set("X-Webhook-ID", strconv.FormatInt(config.ID, 10))
	if strings.TrimSpace(config.Name) != "" {
		req.Header.Set("X-Webhook-Name", config.Name)
	}
	for key, value := range payload.Headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer closeNodeHTTPResponse(resp)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, errors.New("unexpected webhook status")
}

func (w *nodeWebhookWorker) stop() {
	if w == nil {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	if w.sub != nil {
		w.sub.Close()
	}
	closeNodeHTTPClientIdleConnections(w.client)
}

func nodeWebhookConfigEqual(left, right nodeWebhookConfig) bool {
	return left.ID == right.ID &&
		left.URL == right.URL &&
		left.Method == right.Method &&
		left.TimeoutSeconds == right.TimeoutSeconds &&
		left.Enabled == right.Enabled &&
		left.Name == right.Name &&
		left.ContentMode == right.ContentMode &&
		left.ContentType == right.ContentType &&
		left.BodyTemplate == right.BodyTemplate &&
		nodeEventSelectorEqual(left.Selector, right.Selector) &&
		equalStringMap(left.HeaderTemplates, right.HeaderTemplates) &&
		equalActors(left.Owner, right.Owner)
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
