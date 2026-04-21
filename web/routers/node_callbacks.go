package routers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	"github.com/djylb/nps/web/framework"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

type nodeCallbackEnvelope struct {
	Type             string       `json:"type"`
	NodeID           string       `json:"node_id"`
	PlatformID       string       `json:"platform_id"`
	SchemaVersion    int          `json:"schema_version"`
	BootID           string       `json:"boot_id"`
	RuntimeStartedAt int64        `json:"runtime_started_at"`
	ConfigEpoch      string       `json:"config_epoch,omitempty"`
	Timestamp        int64        `json:"timestamp"`
	Event            webapi.Event `json:"event"`
}

type nodeCallbackManager struct {
	state  *State
	queue  *nodeCallbackQueueStore
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var nodeCallbackReplayRegistry = struct {
	mu         sync.RWMutex
	byPlatform map[string][]chan struct{}
}{
	byPlatform: make(map[string][]chan struct{}),
}

type nodeCallbackWorkerConfig struct {
	PlatformID             string
	Platform               servercfg.ManagementPlatformConfig
	ServiceUserID          int
	CallbackQueueMax       int
	CallbackReplayInterval time.Duration
}

type nodeCallbackWorker struct {
	config     nodeCallbackWorkerConfig
	actor      *webapi.Actor
	sub        *nodeEventSubscription
	client     *http.Client
	replayTick *time.Ticker
	replayWake chan struct{}
}

func newNodeCallbackManager(state *State) *nodeCallbackManager {
	parent := context.Background()
	if state != nil {
		parent = state.BaseContext()
	}
	ctx, cancel := context.WithCancel(parent)
	return &nodeCallbackManager{
		state:  state,
		ctx:    ctx,
		cancel: cancel,
	}
}

func StartNodeCallbackDispatchers(state *State) func() {
	if state == nil {
		return func() {}
	}
	platforms := state.CurrentConfig().Runtime.EnabledCallbackManagementPlatforms()
	if len(platforms) == 0 {
		return func() {}
	}
	manager := newNodeCallbackManager(state)
	runtimeStatus := ensureNodeManagementPlatformRuntimeStore(state)
	configs := make([]nodeCallbackWorkerConfig, 0, len(platforms))
	for _, platform := range platforms {
		serviceUser, err := ensurePlatformServiceUser(state, platform)
		if err != nil {
			runtimeStatus.NoteCallbackConfigured(
				platform.PlatformID,
				platform.CallbackURL,
				platform.CallbackEnabled,
				platform.CallbackTimeoutSeconds,
				platform.CallbackRetryMax,
				platform.CallbackRetryBackoffSec,
				platform.CallbackQueueMax,
				0,
			)
			runtimeStatus.NoteCallbackFailed(platform.PlatformID, err, 0)
			logs.Warn("node callback init for platform %s error: %v", platform.PlatformID, err)
			continue
		}
		configs = append(configs, buildNodeCallbackWorkerConfig(platform, serviceUser))
	}
	manager.queue = state.NodeCallbackQueue
	if manager.queue == nil {
		manager.queue = newNodeCallbackQueueStore(
			nodeCallbackPersistencePath(),
			configs,
			runtimeStatus,
			func(ctx context.Context, event webapi.Event) {
				emitNodeManagementEvent(state, ctx, event)
			},
			func() context.Context {
				return state.BaseContext()
			},
		)
	} else {
		manager.queue.runtimeStatus = runtimeStatus
		manager.queue.emit = func(ctx context.Context, event webapi.Event) {
			emitNodeManagementEvent(state, ctx, event)
		}
		manager.queue.baseCtx = func() context.Context {
			return state.BaseContext()
		}
		manager.queue.Configure(configs)
	}
	for _, config := range configs {
		runtimeStatus.NoteCallbackConfigured(
			config.PlatformID,
			config.Platform.CallbackURL,
			config.Platform.CallbackEnabled,
			config.Platform.CallbackTimeoutSeconds,
			config.Platform.CallbackRetryMax,
			config.Platform.CallbackRetryBackoffSec,
			config.CallbackQueueMax,
			manager.queue.Size(config.PlatformID),
		)
		worker, err := manager.prepareWorker(config)
		if err != nil {
			runtimeStatus.NoteCallbackFailed(config.PlatformID, err, 0)
			logs.Warn("node callback init for platform %s error: %v", config.PlatformID, err)
			continue
		}
		if worker == nil {
			continue
		}
		manager.wg.Add(1)
		go manager.runWorker(worker)
		if manager.queue != nil && manager.queue.Size(config.PlatformID) > 0 {
			worker.notifyReplay()
		}
	}
	return func() {
		manager.cancel()
		manager.wg.Wait()
	}
}

func buildNodeCallbackWorkerConfig(platform servercfg.ManagementPlatformConfig, serviceUser *file.User) nodeCallbackWorkerConfig {
	serviceUserID := 0
	if serviceUser != nil {
		serviceUserID = serviceUser.Id
	}
	interval := time.Duration(platform.CallbackRetryBackoffSec) * time.Second
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return nodeCallbackWorkerConfig{
		PlatformID:             platform.PlatformID,
		Platform:               platform,
		ServiceUserID:          serviceUserID,
		CallbackQueueMax:       platform.CallbackQueueMax,
		CallbackReplayInterval: interval,
	}
}

func (m *nodeCallbackManager) prepareWorker(config nodeCallbackWorkerConfig) (*nodeCallbackWorker, error) {
	if config.ServiceUserID <= 0 || m.state == nil || m.state.NodeEvents == nil {
		return nil, nil
	}
	actor, err := fixedPlatformAdminActor(config.Platform, config.ServiceUserID, "callback", "callback:"+config.PlatformID, func() ([]int, error) {
		return platformOwnedClientIDs(m.state, config.ServiceUserID)
	})
	if err != nil {
		return nil, err
	}
	timeoutSeconds := config.Platform.CallbackTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	worker := &nodeCallbackWorker{
		config: config,
		actor:  actor,
		client: &http.Client{
			Timeout: time.Duration(timeoutSeconds) * time.Second,
		},
		replayWake: make(chan struct{}, 1),
	}
	sub := m.subscribeWorker(worker)
	if sub == nil {
		return nil, nil
	}
	worker.sub = sub
	return worker, nil
}

func (m *nodeCallbackManager) runWorker(worker *nodeCallbackWorker) {
	defer m.wg.Done()
	if worker == nil || worker.sub == nil {
		return
	}
	defer worker.stop()
	unregisterReplay := registerNodeCallbackReplay(worker.config.PlatformID, worker.replayWake)
	defer unregisterReplay()
	if worker.config.CallbackReplayInterval > 0 {
		worker.replayTick = time.NewTicker(worker.config.CallbackReplayInterval)
		defer worker.replayTick.Stop()
	}
	var lastSequence int64
	for {
		select {
		case <-m.ctx.Done():
			return
		case event := <-worker.sub.Events():
			m.processCallbackEvent(worker, event, &lastSequence)
		case <-worker.subscriptionDone():
			if !m.recoverWorkerSubscription(worker, &lastSequence) {
				return
			}
		case <-worker.replayWake:
			m.replayQueuedCallback(worker)
		case <-worker.replayTicker():
			m.replayQueuedCallback(worker)
		}
	}
}

func (m *nodeCallbackManager) active() bool {
	return m != nil && (m.ctx == nil || m.ctx.Err() == nil)
}

func registerNodeCallbackReplay(platformID string, wake chan struct{}) func() {
	platformID = strings.TrimSpace(platformID)
	if platformID == "" || wake == nil {
		return func() {}
	}
	nodeCallbackReplayRegistry.mu.Lock()
	nodeCallbackReplayRegistry.byPlatform[platformID] = append(nodeCallbackReplayRegistry.byPlatform[platformID], wake)
	nodeCallbackReplayRegistry.mu.Unlock()
	return func() {
		nodeCallbackReplayRegistry.mu.Lock()
		defer nodeCallbackReplayRegistry.mu.Unlock()
		current := nodeCallbackReplayRegistry.byPlatform[platformID]
		filtered := current[:0]
		for _, ch := range current {
			if ch != nil && ch != wake {
				filtered = append(filtered, ch)
			}
		}
		if len(filtered) == 0 {
			delete(nodeCallbackReplayRegistry.byPlatform, platformID)
			return
		}
		nodeCallbackReplayRegistry.byPlatform[platformID] = filtered
	}
}

func notifyNodeCallbackReplay(platformID string) int {
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return 0
	}
	nodeCallbackReplayRegistry.mu.RLock()
	channels := append([]chan struct{}(nil), nodeCallbackReplayRegistry.byPlatform[platformID]...)
	nodeCallbackReplayRegistry.mu.RUnlock()
	notified := 0
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		select {
		case ch <- struct{}{}:
			notified++
		default:
			notified++
		}
	}
	return notified
}

func (w *nodeCallbackWorker) notifyReplay() {
	if w == nil || w.replayWake == nil {
		return
	}
	select {
	case w.replayWake <- struct{}{}:
	default:
	}
}

func (w *nodeCallbackWorker) replayTicker() <-chan time.Time {
	if w == nil || w.replayTick == nil {
		return nil
	}
	return w.replayTick.C
}

func (w *nodeCallbackWorker) subscriptionDone() <-chan struct{} {
	if w == nil || w.sub == nil {
		return nil
	}
	return w.sub.Done()
}

func (w *nodeCallbackWorker) stop() {
	if w == nil {
		return
	}
	if w.sub != nil {
		w.sub.Close()
	}
	closeNodeHTTPClientIdleConnections(w.client)
}

func (m *nodeCallbackManager) deliverCallbackEvent(worker *nodeCallbackWorker, event webapi.Event) {
	if m == nil || worker == nil || !m.active() {
		return
	}
	payload := buildNodeCallbackEnvelope(m.state, worker.config.PlatformID, event)
	statusCode, err := deliverNodeCallbackWithRetry(m.ctx, worker.client, m.state, worker.config.Platform, payload)
	if err != nil {
		if nodeDeliveryCanceled(m.ctx, err) {
			return
		}
		if m.queue != nil && shouldPersistNodeEvent(event) {
			queueSize, dropped := m.queue.Enqueue(worker.config.PlatformID, payload)
			if queueSize > 0 {
				worker.notifyReplay()
			}
			if dropped {
				logs.Warn("node callback queue for platform %s dropped oldest event after reaching max=%d", worker.config.PlatformID, worker.config.CallbackQueueMax)
			}
		} else if m.queue != nil {
			m.queue.EmitRuntimeEvent(m.ctx, worker.config.PlatformID, "delivery_failed", nil)
		}
		logs.Warn("deliver node callback to %s error: %v", worker.config.PlatformID, err)
		return
	}
	m.runtimeStatus().NoteCallbackDelivered(worker.config.PlatformID, statusCode)
	if m.queue != nil {
		m.queue.EmitRuntimeEvent(m.ctx, worker.config.PlatformID, "delivered", nil)
	}
}

func (m *nodeCallbackManager) recoverWorkerSubscription(worker *nodeCallbackWorker, lastSequence *int64) bool {
	if m == nil || worker == nil || worker.sub == nil {
		return false
	}
	if !m.active() {
		return false
	}
	currentSub := worker.sub
	drainNodeSubscriptionEvents(currentSub, func(event webapi.Event) {
		m.processCallbackEvent(worker, event, lastSequence)
	})
	if overflowSequence, overflowed := currentSub.OverflowSequence(); overflowed {
		m.replayWorkerEventLog(worker, lastSequence, overflowSequence)
	}
	if m.ctx.Err() != nil || m.state == nil || m.state.NodeEvents == nil {
		return false
	}
	worker.sub = m.subscribeWorker(worker)
	return worker.sub != nil
}

func (m *nodeCallbackManager) replayWorkerEventLog(worker *nodeCallbackWorker, lastSequence *int64, overflowSequence int64) {
	if m == nil || worker == nil || m.state == nil || m.state.NodeEventLog == nil {
		return
	}
	after := int64(0)
	if lastSequence != nil {
		after = *lastSequence
	}
	for {
		if m.ctx.Err() != nil {
			return
		}
		snapshot := replayNodeChanges(m.state, worker.actor, after, 100)
		if snapshot.Gap {
			logs.Warn("node callback replay for platform %s observed event-log gap after=%d overflow_sequence=%d oldest=%d cursor=%d", worker.config.PlatformID, after, overflowSequence, snapshot.OldestCursor, snapshot.Cursor)
		}
		if len(snapshot.Items) == 0 {
			return
		}
		for _, event := range snapshot.Items {
			if m.ctx.Err() != nil {
				return
			}
			if event.Sequence > after {
				after = event.Sequence
			}
			m.processCallbackEvent(worker, event, lastSequence)
		}
		if !snapshot.HasMore {
			return
		}
	}
}

func (m *nodeCallbackManager) subscribeWorker(worker *nodeCallbackWorker) *nodeEventSubscription {
	if m == nil || worker == nil || m.state == nil || m.state.NodeEvents == nil {
		return nil
	}
	return m.state.NodeEvents.Subscribe(worker.actor)
}

func (m *nodeCallbackManager) processCallbackEvent(worker *nodeCallbackWorker, event webapi.Event, lastSequence *int64) bool {
	if m == nil || worker == nil || !m.active() {
		return false
	}
	if lastSequence != nil && event.Sequence > *lastSequence {
		*lastSequence = event.Sequence
	}
	if !shouldDeliverNodeCallback(event) {
		return false
	}
	m.deliverCallbackEvent(worker, event)
	return true
}

func (m *nodeCallbackManager) replayQueuedCallback(worker *nodeCallbackWorker) {
	if m == nil || worker == nil || m.queue == nil || !m.active() {
		return
	}
	item, ok := m.queue.Peek(worker.config.PlatformID)
	if !ok {
		return
	}
	statusCode, err := deliverNodeCallbackWithRetry(m.ctx, worker.client, m.state, worker.config.Platform, item.Payload)
	if err != nil {
		if nodeDeliveryCanceled(m.ctx, err) {
			return
		}
		m.queue.MarkAttempt(worker.config.PlatformID, item.ID)
		logs.Warn("replay node callback to %s error: %v", worker.config.PlatformID, err)
		return
	}
	queueSize := m.queue.Remove(worker.config.PlatformID, item.ID)
	m.runtimeStatus().NoteCallbackReplayDelivered(worker.config.PlatformID, statusCode, queueSize)
	m.queue.EmitRuntimeEvent(m.ctx, worker.config.PlatformID, "replayed", &item)
	if queueSize > 0 {
		worker.notifyReplay()
	}
}

func shouldDeliverNodeCallback(event webapi.Event) bool {
	return webapi.ShouldDeliverNodeSinkEventName(event.Name)
}

func deliverNodeCallbackWithRetry(ctx context.Context, client *http.Client, state *State, platform servercfg.ManagementPlatformConfig, payload nodeCallbackEnvelope) (int, error) {
	retryMax := platform.CallbackRetryMax
	if retryMax < 0 {
		retryMax = 0
	}
	backoff := time.Duration(platform.CallbackRetryBackoffSec) * time.Second
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	var lastErr error
	var lastStatusCode int
	for attempt := 0; attempt <= retryMax; attempt++ {
		lastStatusCode, lastErr = deliverNodeCallback(ctx, client, state, platform, payload)
		if lastErr == nil {
			return lastStatusCode, nil
		}
		if nodeDeliveryCanceled(ctx, lastErr) {
			return lastStatusCode, lastErr
		}
		if state != nil {
			ensureNodeManagementPlatformRuntimeStore(state).NoteCallbackFailed(platform.PlatformID, lastErr, lastStatusCode)
		}
		if attempt == retryMax {
			break
		}
		if !waitNodeDelay(ctx, backoff) {
			return lastStatusCode, ctx.Err()
		}
	}
	return lastStatusCode, lastErr
}

func buildNodeCallbackEnvelope(state *State, platformID string, event webapi.Event) nodeCallbackEnvelope {
	if state == nil || state.App == nil {
		return nodeCallbackEnvelope{Event: cloneEvent(event)}
	}
	descriptor := webservice.BuildNodeDescriptor(webservice.NodeDescriptorInput{
		NodeID:           state.App.NodeID,
		Config:           state.CurrentConfig(),
		BootID:           state.RuntimeIdentity().BootID(),
		RuntimeStartedAt: state.RuntimeIdentity().StartedAt(),
		ConfigEpoch:      state.RuntimeIdentity().ConfigEpoch(),
	})
	return nodeCallbackEnvelope{
		Type:             "event",
		NodeID:           descriptor.NodeID,
		PlatformID:       strings.TrimSpace(platformID),
		SchemaVersion:    descriptor.SchemaVersion,
		BootID:           descriptor.BootID,
		RuntimeStartedAt: descriptor.RuntimeStartedAt,
		ConfigEpoch:      descriptor.ConfigEpoch,
		Timestamp:        time.Now().Unix(),
		Event:            cloneEvent(event),
	}
}

func deliverNodeCallback(ctx context.Context, client *http.Client, state *State, platform servercfg.ManagementPlatformConfig, payload nodeCallbackEnvelope) (int, error) {
	if state == nil || state.App == nil {
		return 0, nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, platform.CallbackURL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+platform.Token)
	req.Header.Set("X-Node-Token", platform.Token)
	req.Header.Set("X-Node-ID", state.App.NodeID)
	req.Header.Set("X-Platform-ID", platform.PlatformID)
	req.Header.Set("X-Node-Schema-Version", strconv.Itoa(webservice.NodeSchemaVersion))
	req.Header.Set("X-Node-Boot-ID", state.RuntimeIdentity().BootID())
	req.Header.Set("X-Node-Config-Epoch", state.RuntimeIdentity().ConfigEpoch())
	if signingKey := strings.TrimSpace(platform.CallbackSigningKey); signingKey != "" {
		signatureTimestamp := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("X-Node-Signature-Alg", "hmac-sha256")
		req.Header.Set("X-Node-Signature-Timestamp", signatureTimestamp)
		req.Header.Set("X-Node-Signature", signNodeCallbackPayload(signingKey, signatureTimestamp, body))
	}
	if requestID := strings.TrimSpace(payload.Event.Metadata.RequestID); requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer closeNodeHTTPResponse(resp)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, &nodeCallbackHTTPError{StatusCode: resp.StatusCode}
	}
	return resp.StatusCode, nil
}

type nodeCallbackHTTPError struct {
	StatusCode int
}

func (e *nodeCallbackHTTPError) Error() string {
	if e == nil || e.StatusCode == 0 {
		return "callback request failed"
	}
	return "callback request failed with status " + strconv.Itoa(e.StatusCode) + " " + http.StatusText(e.StatusCode)
}

func signNodeCallbackPayload(signingKey, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(signingKey))
	_, _ = mac.Write([]byte(strings.TrimSpace(timestamp)))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (m *nodeCallbackManager) runtimeStatus() webservice.ManagementPlatformRuntimeStatusStore {
	if m != nil && m.state != nil {
		return m.state.ManagementPlatformRuntimeStatus()
	}
	return webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
}

func waitNodeDelay(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type nodeCallbackQueuePayload = webservice.NodeCallbackQueuePayload
type nodeCallbackQueueMutationPayload = webservice.NodeCallbackQueueMutationPayload

var (
	errNodeCallbackQueueStateUnavailable = errors.New("node state is unavailable")
	errNodeCallbackQueueJSONBodyRequired = errors.New("json object body is required")
	errNodeCallbackQueueInvalidJSONBody  = errors.New("invalid json body")
)

type nodeCallbackQueueMutationBody struct {
	PlatformID string `json:"platform_id"`
}

type nodeCallbackQueueMutationRequest struct {
	PlatformID string
	Action     string
}

type nodeCallbackQueueMutationExecution struct {
	Payload      nodeCallbackQueueMutationPayload
	Count        int
	SuccessCount int
	ErrorCount   int
}

func nodeCallbackQueueHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := resolveNodeCallbackQueueScope(state, c, false)
		if err != nil {
			nodeCallbackQueueAbort(c, err)
			return
		}
		payload, err := queryNodeCallbackQueue(
			state,
			scope,
			strings.TrimSpace(c.Query("platform_id")),
			parseNodeCallbackQueueLimit(c.Query("limit")),
		)
		if err != nil {
			nodeCallbackQueueAbort(c, err)
			return
		}
		writeNodeCallbackQueueData(c, state, payload)
	}
}

func nodeCallbackQueueReplayHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		executeNodeCallbackQueueMutationHTTP(state, c, "callback_queue_replay", "replay")
	}
}

func nodeCallbackQueueClearHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		executeNodeCallbackQueueMutationHTTP(state, c, "callback_queue_clear", "clear")
	}
}

func resolveNodeCallbackQueueScope(state *State, c *gin.Context, manage bool) (webservice.NodeAccessScope, error) {
	scope := actorNodeScopeWithAuthz(state.Authorization(), currentActor(c))
	if err := requireNodeCallbackQueueAccess(state, currentActor(c), scope, manage); err != nil {
		return webservice.NodeAccessScope{}, err
	}
	return scope, nil
}

func requireNodeCallbackQueueAccess(state *State, actor *webapi.Actor, scope webservice.NodeAccessScope, manage bool) error {
	if state == nil {
		return errNodeCallbackQueueStateUnavailable
	}
	if nodeWebhookAnonymous(actor) {
		return webservice.ErrUnauthenticated
	}
	switch {
	case manage && !scope.CanManageCallbackQueue():
		return webservice.ErrForbidden
	case !manage && !scope.CanViewCallbackQueue():
		return webservice.ErrForbidden
	default:
		return nil
	}
}

func executeNodeCallbackQueueMutationHTTP(state *State, c *gin.Context, operationName, action string) {
	startedAt := time.Now()
	operationID := currentNodeHTTPOperationID(c)
	setNodeHTTPOperationHeader(c, operationID)
	paths := []string{canonicalNodeOperationPath(state, c.Request.URL.Path)}
	actor := currentActor(c)
	metadata := currentRequestMetadata(c)
	scope, err := resolveNodeCallbackQueueScope(state, c, true)
	if err != nil {
		recordNodeCallbackQueueMutationOutcome(state, actor, metadata, operationID, operationName, startedAt, paths, err, nil)
		nodeCallbackQueueAbort(c, err)
		return
	}
	body, err := decodeOptionalNodeCallbackQueueMutationBody(c)
	if err != nil {
		recordNodeCallbackQueueMutationOutcome(state, actor, metadata, operationID, operationName, startedAt, paths, err, nil)
		nodeCallbackQueueAbort(c, err)
		return
	}
	result, err := executeAuditedNodeCallbackQueueMutation(state, actor, metadata, operationID, operationName, startedAt, paths, scope, nodeCallbackQueueMutationRequest{
		PlatformID: strings.TrimSpace(body.PlatformID),
		Action:     action,
	})
	if err != nil {
		nodeCallbackQueueAbort(c, err)
		return
	}
	writeNodeCallbackQueueData(c, state, result.Payload)
}

func executeAuditedNodeCallbackQueueMutation(
	state *State,
	actor *webapi.Actor,
	metadata webapi.RequestMetadata,
	operationID,
	operationName string,
	startedAt time.Time,
	paths []string,
	scope webservice.NodeAccessScope,
	request nodeCallbackQueueMutationRequest,
) (nodeCallbackQueueMutationExecution, error) {
	result, err := executeNodeCallbackQueueMutation(state, scope, request)
	resultRef := &result
	if err != nil {
		resultRef = nil
	}
	recordNodeCallbackQueueMutationOutcome(state, actor, metadata, operationID, operationName, startedAt, paths, err, resultRef)
	return result, err
}

func recordNodeCallbackQueueMutationOutcome(
	state *State,
	actor *webapi.Actor,
	metadata webapi.RequestMetadata,
	operationID,
	operationName string,
	startedAt time.Time,
	paths []string,
	err error,
	result *nodeCallbackQueueMutationExecution,
) {
	if state == nil {
		return
	}
	total, success, failed := 1, 0, 1
	if err == nil {
		total, success, failed = 0, 0, 0
	}
	if result != nil {
		total = result.Count
		success = result.SuccessCount
		failed = result.ErrorCount
	}
	state.RuntimeStatus().NoteOperation(operationName, err)
	recordNodeOperation(
		state,
		actor,
		metadata,
		operationID,
		operationName,
		startedAt,
		total,
		success,
		failed,
		paths,
	)
}

func writeNodeCallbackQueueData(c *gin.Context, state *State, data interface{}) {
	if c == nil || state == nil {
		return
	}
	timestamp := time.Now().Unix()
	c.JSON(http.StatusOK, webapi.ManagementDataResponse{
		Data: stampNodeCallbackQueueResponseDataTimestamp(data, timestamp),
		Meta: webapi.ManagementResponseMetaForRequest(currentRequestMetadata(c), timestamp, state.RuntimeIdentity().ConfigEpoch()),
	})
}

func queryNodeCallbackQueue(state *State, scope webservice.NodeAccessScope, requestedPlatformID string, limit int) (nodeCallbackQueuePayload, error) {
	if state == nil {
		return nodeCallbackQueuePayload{}, errNodeCallbackQueueStateUnavailable
	}
	input := resolveNodeCallbackQueueControlInput(state)
	payload, err := state.NodeControl().QueryCallbackQueue(webservice.NodeCallbackQueueQueryInput{
		Scope:               scope,
		Config:              state.CurrentConfig(),
		RequestedPlatformID: requestedPlatformID,
		Limit:               limit,
		ListItems:           input.ListItems,
		QueueSize:           input.QueueSize,
		ReverseStatus:       input.ReverseStatus,
	})
	if err != nil {
		return nodeCallbackQueuePayload{}, err
	}
	return payload, nil
}

func mutateNodeCallbackQueue(state *State, scope webservice.NodeAccessScope, request nodeCallbackQueueMutationRequest) (nodeCallbackQueueMutationPayload, error) {
	if state == nil {
		return nodeCallbackQueueMutationPayload{}, errNodeCallbackQueueStateUnavailable
	}
	input := resolveNodeCallbackQueueControlInput(state)
	payload, err := state.NodeControl().MutateCallbackQueue(webservice.NodeCallbackQueueMutationInput{
		Scope:               scope,
		Config:              state.CurrentConfig(),
		RequestedPlatformID: strings.TrimSpace(request.PlatformID),
		Action:              request.Action,
		QueueSize:           input.QueueSize,
		Clear:               input.Clear,
		NotifyReplay:        notifyNodeCallbackReplay,
	})
	if err != nil {
		return nodeCallbackQueueMutationPayload{}, err
	}
	return payload, nil
}

func executeNodeCallbackQueueMutation(state *State, scope webservice.NodeAccessScope, request nodeCallbackQueueMutationRequest) (nodeCallbackQueueMutationExecution, error) {
	payload, err := mutateNodeCallbackQueue(state, scope, request)
	if err != nil {
		return nodeCallbackQueueMutationExecution{}, err
	}
	count := len(payload.Platforms)
	return nodeCallbackQueueMutationExecution{
		Payload:      payload,
		Count:        count,
		SuccessCount: count,
	}, nil
}

type nodeCallbackQueueControlInput struct {
	ListItems     func(string, int) []webservice.NodeCallbackQueueItemPayload
	QueueSize     func(string) int
	Clear         func(string) int
	ReverseStatus func(string) webservice.ManagementPlatformReverseRuntimeStatus
}

func resolveNodeCallbackQueueControlInput(state *State) nodeCallbackQueueControlInput {
	input := nodeCallbackQueueControlInput{
		ReverseStatus: func(string) webservice.ManagementPlatformReverseRuntimeStatus {
			return webservice.ManagementPlatformReverseRuntimeStatus{}
		},
	}
	if state == nil {
		return input
	}
	if runtimeStatus := state.ManagementPlatformRuntimeStatus(); runtimeStatus != nil {
		input.ReverseStatus = runtimeStatus.Status
	}
	if state.NodeCallbackQueue == nil {
		return input
	}
	input.ListItems = func(platformID string, limit int) []webservice.NodeCallbackQueueItemPayload {
		return nodeCallbackQueueItemPayloads(state.NodeCallbackQueue.List(platformID, limit))
	}
	input.QueueSize = state.NodeCallbackQueue.Size
	input.Clear = state.NodeCallbackQueue.Clear
	return input
}

func nodeCallbackQueueItemPayloads(items []nodeCallbackQueuedItem) []webservice.NodeCallbackQueueItemPayload {
	if len(items) == 0 {
		return nil
	}
	payloads := make([]webservice.NodeCallbackQueueItemPayload, len(items))
	for index, item := range items {
		payloads[index] = webservice.NodeCallbackQueueItemPayload{
			ID:            item.ID,
			EventName:     item.Payload.Event.Name,
			EventResource: item.Payload.Event.Resource,
			EventAction:   item.Payload.Event.Action,
			EventSequence: item.Payload.Event.Sequence,
			RequestID:     item.Payload.Event.Metadata.RequestID,
			EnqueuedAt:    item.EnqueuedAt,
			LastAttemptAt: item.LastAttemptAt,
			Attempts:      item.Attempts,
		}
	}
	return payloads
}

func stampNodeCallbackQueueResponseDataTimestamp(data interface{}, timestamp int64) interface{} {
	switch payload := data.(type) {
	case nodeCallbackQueuePayload:
		payload.Timestamp = timestamp
		return payload
	case nodeCallbackQueueMutationPayload:
		payload.Timestamp = timestamp
		return payload
	case *nodeCallbackQueuePayload:
		if payload != nil {
			payload.Timestamp = timestamp
		}
		return payload
	case *nodeCallbackQueueMutationPayload:
		if payload != nil {
			payload.Timestamp = timestamp
		}
		return payload
	default:
		return data
	}
}

func parseNodeCallbackQueueLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 20
	}
	if value > 200 {
		return 200
	}
	return value
}

func decodeOptionalNodeCallbackQueueMutationBody(c *gin.Context) (nodeCallbackQueueMutationBody, error) {
	if c == nil {
		return nodeCallbackQueueMutationBody{}, errNodeCallbackQueueInvalidJSONBody
	}
	raw := bytes.TrimSpace(framework.RequestRawBodyView(c))
	if len(raw) == 0 {
		if c.Request != nil && c.Request.ContentLength > 0 {
			return nodeCallbackQueueMutationBody{}, errNodeCallbackQueueJSONBodyRequired
		}
		return nodeCallbackQueueMutationBody{}, nil
	}
	if raw[0] != '{' {
		return nodeCallbackQueueMutationBody{}, errNodeCallbackQueueJSONBodyRequired
	}
	var body nodeCallbackQueueMutationBody
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return nodeCallbackQueueMutationBody{}, errNodeCallbackQueueInvalidJSONBody
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != nil {
		if err == io.EOF {
			return body, nil
		}
		return nodeCallbackQueueMutationBody{}, errNodeCallbackQueueInvalidJSONBody
	}
	return nodeCallbackQueueMutationBody{}, errNodeCallbackQueueInvalidJSONBody
}

func nodeCallbackQueueMutationDetail(err error) (int, webapi.ManagementErrorDetail) {
	if err == nil {
		return http.StatusOK, webapi.ManagementErrorDetail{}
	}
	switch {
	case errors.Is(err, errNodeCallbackQueueStateUnavailable):
		return http.StatusInternalServerError, webapi.ManagementErrorDetail{
			Code:    "node_state_unavailable",
			Message: "node state is unavailable",
		}
	case errors.Is(err, webservice.ErrUnauthenticated):
		return http.StatusUnauthorized, webapi.ManagementErrorDetail{
			Code:    "unauthorized",
			Message: "unauthorized",
		}
	case errors.Is(err, webservice.ErrForbidden):
		return http.StatusForbidden, webapi.ManagementErrorDetail{
			Code:    "forbidden",
			Message: "forbidden",
		}
	case errors.Is(err, webservice.ErrManagementPlatformNotFound):
		return http.StatusNotFound, webapi.ManagementErrorResponseForStatus(http.StatusNotFound, err).Error
	case errors.Is(err, webservice.ErrInvalidCallbackQueueAction):
		return http.StatusBadRequest, webapi.ManagementErrorResponseForStatus(http.StatusBadRequest, err).Error
	case errors.Is(err, errNodeCallbackQueueJSONBodyRequired),
		errors.Is(err, errNodeCallbackQueueInvalidJSONBody),
		strings.EqualFold(strings.TrimSpace(err.Error()), "json object body is required"),
		strings.EqualFold(strings.TrimSpace(err.Error()), "invalid json body"):
		message := "invalid json body"
		if errors.Is(err, errNodeCallbackQueueJSONBodyRequired) || strings.EqualFold(strings.TrimSpace(err.Error()), "json object body is required") {
			message = "json object body is required"
		}
		return http.StatusBadRequest, webapi.ManagementErrorDetail{
			Code:    "invalid_json_body",
			Message: message,
		}
	default:
		return http.StatusInternalServerError, webapi.ManagementErrorResponseForStatus(http.StatusInternalServerError, err).Error
	}
}

func nodeCallbackQueueMutationStatus(err error) int {
	status, _ := nodeCallbackQueueMutationDetail(err)
	return status
}

func writeWSCallbackQueueError(ctx *wsAPIContext, err error) {
	if ctx == nil {
		return
	}
	status, detail := nodeCallbackQueueMutationDetail(err)
	writeWSManagementError(ctx, status, detail.Code, detail.Message, detail.Details)
}

func nodeCallbackQueueAbort(c *gin.Context, err error) {
	if c == nil {
		return
	}
	status, detail := nodeCallbackQueueMutationDetail(err)
	c.AbortWithStatusJSON(status, webapi.ManagementErrorResponse{Error: detail})
}

// Callback queue persistence and platform runtime status are owned by the
// callback/reverse platform runtime path, so they stay with the callback owner.

type nodeCallbackQueuedItem struct {
	ID            int64                `json:"id"`
	PlatformID    string               `json:"platform_id"`
	EnqueuedAt    int64                `json:"enqueued_at"`
	LastAttemptAt int64                `json:"last_attempt_at,omitempty"`
	Attempts      int                  `json:"attempts,omitempty"`
	Payload       nodeCallbackEnvelope `json:"payload"`
}

type nodeCallbackQueuePersistedState struct {
	Version int64                    `json:"version,omitempty"`
	NextID  int64                    `json:"next_id"`
	Items   []nodeCallbackQueuedItem `json:"items"`
}

type nodeCallbackQueueStore struct {
	mu            sync.RWMutex
	persistMu     sync.Mutex
	path          string
	nextID        int64
	persistSeq    int64
	maxByID       map[string]int
	items         map[string][]nodeCallbackQueuedItem
	runtimeStatus webservice.ManagementPlatformRuntimeStatusStore
	emit          func(context.Context, webapi.Event)
	baseCtx       func() context.Context
	writer        *nodeRuntimeStateWriter
}

func newNodeCallbackQueueStore(path string, platforms []nodeCallbackWorkerConfig, runtimeStatus webservice.ManagementPlatformRuntimeStatusStore, emit func(context.Context, webapi.Event), baseCtx func() context.Context) *nodeCallbackQueueStore {
	store := &nodeCallbackQueueStore{
		path:          strings.TrimSpace(path),
		maxByID:       make(map[string]int),
		items:         make(map[string][]nodeCallbackQueuedItem),
		runtimeStatus: runtimeStatus,
		emit:          emit,
		baseCtx:       baseCtx,
		writer:        newNodeRuntimeStateWriter(path),
	}
	for _, platform := range platforms {
		store.maxByID[strings.TrimSpace(platform.PlatformID)] = platform.CallbackQueueMax
	}
	store.load()
	store.trimAndPersist()
	return store
}

func (s *nodeCallbackQueueStore) Configure(platforms []nodeCallbackWorkerConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.maxByID = make(map[string]int, len(platforms))
	for _, platform := range platforms {
		platformID := strings.TrimSpace(platform.PlatformID)
		if platformID == "" {
			continue
		}
		s.maxByID[platformID] = platform.CallbackQueueMax
	}
	s.mu.Unlock()
	s.trimAndPersist()
}

func (s *nodeCallbackQueueStore) emitEvent(ctx context.Context, event webapi.Event) {
	if s == nil || s.emit == nil {
		return
	}
	ctx = resolveNodeEmitContext(ctx, s.baseCtx)
	s.emit(ctx, event)
}

type nodeCallbackQueueStatusUpdate struct {
	PlatformID string
	QueueSize  int
	Queued     bool
	Dropped    bool
}

type nodeCallbackQueueAppliedStatus struct {
	PlatformID string
	Status     webservice.ManagementPlatformReverseRuntimeStatus
}

func (s *nodeCallbackQueueStore) applyStatusUpdates(updates []nodeCallbackQueueStatusUpdate) []nodeCallbackQueueAppliedStatus {
	if s == nil || len(updates) == 0 {
		return nil
	}
	runtimeStatus := s.managementPlatformRuntimeStatus()
	applied := make([]nodeCallbackQueueAppliedStatus, 0, len(updates))
	for _, update := range updates {
		platformID := strings.TrimSpace(update.PlatformID)
		if platformID == "" {
			continue
		}
		if update.Queued {
			runtimeStatus.NoteCallbackQueued(platformID, update.QueueSize, update.Dropped)
		} else {
			runtimeStatus.NoteCallbackQueueSize(platformID, update.QueueSize)
		}
		applied = append(applied, nodeCallbackQueueAppliedStatus{
			PlatformID: platformID,
			Status:     runtimeStatus.Status(platformID),
		})
	}
	return applied
}

func (s *nodeCallbackQueueStore) emitQueueUpdated(
	ctx context.Context,
	platformID string,
	queueSize int,
	queueMax int,
	status webservice.ManagementPlatformReverseRuntimeStatus,
	hasStatus bool,
	cause string,
	item *nodeCallbackQueuedItem,
	dropped bool,
	cleared int,
) {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" || strings.TrimSpace(cause) == "" {
		return
	}
	if !hasStatus {
		status = s.managementPlatformRuntimeStatus().Status(platformID)
	}
	s.emitEvent(ctx, callbackQueueUpdatedEvent(platformID, queueSize, queueMax, status, cause, item, dropped, cleared))
}

func trimNodeCallbackQueueItemsInPlace(queue []nodeCallbackQueuedItem, keep int) ([]nodeCallbackQueuedItem, bool) {
	if keep <= 0 {
		zeroNodeCallbackQueuedItems(queue)
		return queue[:0], len(queue) > 0
	}
	if len(queue) <= keep {
		return queue, false
	}
	drop := len(queue) - keep
	copy(queue, queue[drop:])
	zeroNodeCallbackQueuedItems(queue[keep:])
	return queue[:keep], true
}

func removeNodeCallbackQueueItemInPlace(queue []nodeCallbackQueuedItem, index int) []nodeCallbackQueuedItem {
	if index < 0 || index >= len(queue) {
		return queue
	}
	copy(queue[index:], queue[index+1:])
	zeroNodeCallbackQueuedItems(queue[len(queue)-1:])
	return queue[:len(queue)-1]
}

func zeroNodeCallbackQueuedItems(items []nodeCallbackQueuedItem) {
	var zero nodeCallbackQueuedItem
	for index := range items {
		items[index] = zero
	}
}

func (s *nodeCallbackQueueStore) Enqueue(platformID string, payload nodeCallbackEnvelope) (queueSize int, dropped bool) {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return 0, false
	}
	s.mu.Lock()
	queueMax := s.maxByID[platformID]
	if queueMax <= 0 {
		queueSize = len(s.items[platformID])
		s.mu.Unlock()
		return queueSize, false
	}
	s.nextID++
	item := nodeCallbackQueuedItem{
		ID:         s.nextID,
		PlatformID: platformID,
		EnqueuedAt: time.Now().Unix(),
		Payload:    cloneNodeCallbackEnvelope(payload),
	}
	queue := append(s.items[platformID], item)
	queue, dropped = trimNodeCallbackQueueItemsInPlace(queue, queueMax)
	s.items[platformID] = queue
	queueSize = len(queue)
	emitted := cloneNodeCallbackQueuedItem(item)
	persistSeq, persisted := s.nextPersistedStateLocked()
	s.mu.Unlock()
	s.persistSnapshot(persistSeq, persisted)
	statuses := s.applyStatusUpdates([]nodeCallbackQueueStatusUpdate{{
		PlatformID: platformID,
		QueueSize:  queueSize,
		Queued:     true,
		Dropped:    dropped,
	}})
	status, hasStatus := nodeCallbackQueueAppliedStatusFor(statuses, platformID)
	s.emitQueueUpdated(nil, platformID, queueSize, queueMax, status, hasStatus, "queued", &emitted, dropped, 0)
	return queueSize, dropped
}

func (s *nodeCallbackQueueStore) Peek(platformID string) (nodeCallbackQueuedItem, bool) {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return nodeCallbackQueuedItem{}, false
	}
	s.mu.RLock()
	queue := s.items[platformID]
	if len(queue) == 0 {
		s.mu.RUnlock()
		return nodeCallbackQueuedItem{}, false
	}
	item := queue[0]
	s.mu.RUnlock()
	return cloneNodeCallbackQueuedItem(item), true
}

func (s *nodeCallbackQueueStore) MarkAttempt(platformID string, itemID int64) int {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" || itemID <= 0 {
		return 0
	}
	s.mu.Lock()
	queue := s.items[platformID]
	for index := range queue {
		if queue[index].ID != itemID {
			continue
		}
		queue[index].Attempts++
		queue[index].LastAttemptAt = time.Now().Unix()
		s.items[platformID] = queue
		queueSize := len(queue)
		queueMax := s.maxByID[platformID]
		emitted := cloneNodeCallbackQueuedItem(queue[index])
		persistSeq, persisted := s.nextPersistedStateLocked()
		s.mu.Unlock()
		s.persistSnapshot(persistSeq, persisted)
		s.emitQueueUpdated(nil, platformID, queueSize, queueMax, webservice.ManagementPlatformReverseRuntimeStatus{}, false, "attempted", &emitted, false, 0)
		return queueSize
	}
	queueSize := len(queue)
	s.mu.Unlock()
	return queueSize
}

func (s *nodeCallbackQueueStore) Remove(platformID string, itemID int64) int {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" || itemID <= 0 {
		return 0
	}
	s.mu.Lock()
	queue := s.items[platformID]
	for index := range queue {
		if queue[index].ID != itemID {
			continue
		}
		queue = removeNodeCallbackQueueItemInPlace(queue, index)
		s.items[platformID] = queue
		queueSize := len(queue)
		persistSeq, persisted := s.nextPersistedStateLocked()
		s.mu.Unlock()
		s.persistSnapshot(persistSeq, persisted)
		s.applyStatusUpdates([]nodeCallbackQueueStatusUpdate{{
			PlatformID: platformID,
			QueueSize:  queueSize,
		}})
		return queueSize
	}
	queueSize := len(queue)
	s.mu.Unlock()
	return queueSize
}

func (s *nodeCallbackQueueStore) Size(platformID string) int {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items[platformID])
}

func (s *nodeCallbackQueueStore) List(platformID string, limit int) []nodeCallbackQueuedItem {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.RLock()
	queue := s.items[platformID]
	if len(queue) == 0 {
		s.mu.RUnlock()
		return nil
	}
	if len(queue) > limit {
		queue = queue[:limit]
	}
	snapshot := append([]nodeCallbackQueuedItem(nil), queue...)
	s.mu.RUnlock()
	items := make([]nodeCallbackQueuedItem, 0, len(snapshot))
	for _, item := range snapshot {
		items = append(items, cloneNodeCallbackQueuedItem(item))
	}
	return items
}

func (s *nodeCallbackQueueStore) Clear(platformID string) int {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return 0
	}
	s.mu.Lock()
	queue := s.items[platformID]
	size := len(queue)
	zeroNodeCallbackQueuedItems(queue)
	delete(s.items, platformID)
	queueMax := s.maxByID[platformID]
	persistSeq, persisted := s.nextPersistedStateLocked()
	s.mu.Unlock()
	s.persistSnapshot(persistSeq, persisted)
	statuses := s.applyStatusUpdates([]nodeCallbackQueueStatusUpdate{{
		PlatformID: platformID,
		QueueSize:  0,
	}})
	status, hasStatus := nodeCallbackQueueAppliedStatusFor(statuses, platformID)
	s.emitQueueUpdated(nil, platformID, 0, queueMax, status, hasStatus, "cleared", nil, false, size)
	return size
}

func (s *nodeCallbackQueueStore) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.nextID = 0
	s.items = make(map[string][]nodeCallbackQueuedItem)
	updates := make([]nodeCallbackQueueStatusUpdate, 0, len(s.maxByID))
	for platformID := range s.maxByID {
		updates = append(updates, nodeCallbackQueueStatusUpdate{PlatformID: platformID, QueueSize: 0})
	}
	persistSeq, persisted := s.nextPersistedStateLocked()
	s.mu.Unlock()
	s.persistSnapshot(persistSeq, persisted)
	s.applyStatusUpdates(updates)
}

func (s *nodeCallbackQueueStore) load() {
	if s == nil || s.path == "" {
		return
	}
	var persisted nodeCallbackQueuePersistedState
	if err := readNodeRuntimeState(s.path, &persisted); err != nil {
		if !isIgnorableNodeRuntimeStateError(err) {
			logs.Warn("load node callback state %s error: %v", s.path, err)
		}
		return
	}
	if !nodeRuntimeStateVersionSupported(int(persisted.Version)) {
		logs.Warn("load node callback state %s error: unsupported version=%d", s.path, persisted.Version)
		return
	}
	s.mu.RLock()
	maxByID := make(map[string]int, len(s.maxByID))
	for platformID, max := range s.maxByID {
		maxByID[platformID] = max
	}
	s.mu.RUnlock()
	items := make(map[string][]nodeCallbackQueuedItem, len(maxByID))
	updates := make([]nodeCallbackQueueStatusUpdate, 0, len(maxByID))
	nextID := persisted.NextID
	for _, item := range persisted.Items {
		platformID := strings.TrimSpace(item.PlatformID)
		if platformID == "" {
			continue
		}
		if _, ok := maxByID[platformID]; !ok {
			continue
		}
		cloned := cloneNodeCallbackQueuedItem(item)
		items[platformID] = append(items[platformID], cloned)
		if cloned.ID > nextID {
			nextID = cloned.ID
		}
	}
	for platformID, queue := range items {
		sort.Slice(queue, func(i, j int) bool {
			return queue[i].ID < queue[j].ID
		})
		items[platformID] = queue
		updates = append(updates, nodeCallbackQueueStatusUpdate{PlatformID: platformID, QueueSize: len(queue)})
	}
	s.mu.Lock()
	s.nextID = nextID
	s.items = items
	s.mu.Unlock()
	s.applyStatusUpdates(updates)
}

func (s *nodeCallbackQueueStore) trimAndPersist() {
	if s == nil {
		return
	}
	s.mu.Lock()
	updates := make([]nodeCallbackQueueStatusUpdate, 0, len(s.items))
	for platformID, queue := range s.items {
		max := s.maxByID[platformID]
		if max <= 0 {
			zeroNodeCallbackQueuedItems(queue)
			delete(s.items, platformID)
			updates = append(updates, nodeCallbackQueueStatusUpdate{PlatformID: platformID, QueueSize: 0})
			continue
		}
		queue, _ = trimNodeCallbackQueueItemsInPlace(queue, max)
		s.items[platformID] = queue
		updates = append(updates, nodeCallbackQueueStatusUpdate{PlatformID: platformID, QueueSize: len(queue)})
	}
	persistSeq, persisted := s.nextPersistedStateLocked()
	s.mu.Unlock()
	s.persistSnapshot(persistSeq, persisted)
	s.applyStatusUpdates(updates)
}

func (s *nodeCallbackQueueStore) managementPlatformRuntimeStatus() webservice.ManagementPlatformRuntimeStatusStore {
	if s != nil && s.runtimeStatus != nil {
		return s.runtimeStatus
	}
	return webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
}

func (s *nodeCallbackQueueStore) nextPersistedStateLocked() (int64, nodeCallbackQueuePersistedState) {
	if s == nil {
		return 0, nodeCallbackQueuePersistedState{}
	}
	s.persistSeq++
	return s.persistSeq, s.snapshotPersistedStateLocked()
}

func (s *nodeCallbackQueueStore) snapshotPersistedStateLocked() nodeCallbackQueuePersistedState {
	state := nodeCallbackQueuePersistedState{
		Version: nodeRuntimeStateVersion,
		NextID:  s.nextID,
	}
	total := 0
	for _, queue := range s.items {
		total += len(queue)
	}
	if total == 0 {
		return state
	}
	state.Items = make([]nodeCallbackQueuedItem, 0, total)
	for platformID, queue := range s.items {
		for _, item := range queue {
			snapshot := item
			snapshot.PlatformID = platformID
			state.Items = append(state.Items, snapshot)
		}
	}
	return state
}

func (s *nodeCallbackQueueStore) latestPersistSeq() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.persistSeq
}

func (s *nodeCallbackQueueStore) persistSnapshot(seq int64, state nodeCallbackQueuePersistedState) {
	if s == nil || s.path == "" || seq <= 0 {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if seq < s.latestPersistSeq() {
		return
	}
	state = cloneNodeCallbackQueuePersistedState(state)
	if s.writer != nil {
		s.writer.Store(state)
		return
	}
	writeNodeRuntimeState(s.path, state)
}

func (s *nodeCallbackQueueStore) Close() {
	if s == nil || s.writer == nil {
		return
	}
	s.writer.Close()
}

func cloneNodeCallbackEnvelope(payload nodeCallbackEnvelope) nodeCallbackEnvelope {
	cloned := payload
	cloned.Event = cloneEvent(payload.Event)
	return cloned
}

func cloneNodeCallbackQueuedItem(item nodeCallbackQueuedItem) nodeCallbackQueuedItem {
	cloned := item
	cloned.Payload = cloneNodeCallbackEnvelope(item.Payload)
	return cloned
}

func cloneNodeCallbackQueuePersistedState(state nodeCallbackQueuePersistedState) nodeCallbackQueuePersistedState {
	cloned := nodeCallbackQueuePersistedState{
		Version: state.Version,
		NextID:  state.NextID,
	}
	if len(state.Items) == 0 {
		return cloned
	}
	cloned.Items = make([]nodeCallbackQueuedItem, 0, len(state.Items))
	for _, item := range state.Items {
		cloned.Items = append(cloned.Items, cloneNodeCallbackQueuedItem(item))
	}
	sort.Slice(cloned.Items, func(i, j int) bool {
		if cloned.Items[i].PlatformID == cloned.Items[j].PlatformID {
			return cloned.Items[i].ID < cloned.Items[j].ID
		}
		return cloned.Items[i].PlatformID < cloned.Items[j].PlatformID
	})
	return cloned
}

func nodeCallbackQueueAppliedStatusFor(statuses []nodeCallbackQueueAppliedStatus, platformID string) (webservice.ManagementPlatformReverseRuntimeStatus, bool) {
	platformID = strings.TrimSpace(platformID)
	for _, applied := range statuses {
		if applied.PlatformID == platformID {
			return applied.Status, true
		}
	}
	return webservice.ManagementPlatformReverseRuntimeStatus{}, false
}

func (s *nodeCallbackQueueStore) EmitRuntimeEvent(ctx context.Context, platformID, cause string, item *nodeCallbackQueuedItem) {
	platformID = strings.TrimSpace(platformID)
	if s == nil || platformID == "" {
		return
	}
	s.mu.RLock()
	queueSize := len(s.items[platformID])
	queueMax := s.maxByID[platformID]
	s.mu.RUnlock()
	s.emitQueueUpdated(ctx, platformID, queueSize, queueMax, webservice.ManagementPlatformReverseRuntimeStatus{}, false, cause, item, false, 0)
}

func callbackQueueUpdatedEvent(
	platformID string,
	queueSize int,
	queueMax int,
	status webservice.ManagementPlatformReverseRuntimeStatus,
	cause string,
	item *nodeCallbackQueuedItem,
	dropped bool,
	cleared int,
) webapi.Event {
	return webapi.Event{
		Name:     "callbacks_queue.updated",
		Resource: "callbacks_queue",
		Action:   "update",
		Fields:   callbackQueueEventFields(platformID, queueSize, queueMax, status, cause, item, dropped, cleared),
	}
}

func callbackQueueEventFields(
	platformID string,
	queueSize int,
	queueMax int,
	status webservice.ManagementPlatformReverseRuntimeStatus,
	cause string,
	item *nodeCallbackQueuedItem,
	dropped bool,
	cleared int,
) map[string]interface{} {
	fields := map[string]interface{}{
		"platform_id": strings.TrimSpace(platformID),
	}
	appendNodeCallbackQueueRuntimeFields(fields, status, queueSize, queueMax)
	if cause = strings.TrimSpace(cause); cause != "" {
		fields["cause"] = cause
	}
	if dropped {
		fields["dropped"] = true
	}
	if cleared > 0 {
		fields["cleared"] = cleared
	}
	if item != nil {
		fields["id"] = item.ID
		fields["request_id"] = item.Payload.Event.Metadata.RequestID
		fields["event_name"] = item.Payload.Event.Name
		fields["event_resource"] = item.Payload.Event.Resource
		fields["event_action"] = item.Payload.Event.Action
		fields["event_sequence"] = item.Payload.Event.Sequence
		fields["enqueued_at"] = item.EnqueuedAt
		if item.LastAttemptAt > 0 {
			fields["last_attempt_at"] = item.LastAttemptAt
		}
		if item.Attempts > 0 {
			fields["attempts"] = item.Attempts
		}
	}
	return fields
}

func appendNodeCallbackQueueRuntimeFields(
	fields map[string]interface{},
	status webservice.ManagementPlatformReverseRuntimeStatus,
	queueSize int,
	queueMax int,
) {
	if fields == nil {
		return
	}
	if queueSize < 0 {
		queueSize = status.CallbackQueueSize
	}
	if queueMax < 0 {
		queueMax = status.CallbackQueueMax
	}
	fields["callback_queue_size"] = queueSize
	fields["callback_queue_max"] = queueMax
	fields["callback_dropped"] = status.CallbackDropped
	fields["last_callback_queued_at"] = status.LastCallbackQueuedAt
	fields["last_callback_replay_at"] = status.LastCallbackReplayAt
	fields["last_callback_status_code"] = status.LastCallbackStatusCode
	appendNodeCallbackErrorFields(fields, status)
}

func appendNodeCallbackErrorFields(fields map[string]interface{}, status webservice.ManagementPlatformReverseRuntimeStatus) {
	if fields == nil {
		return
	}
	if status.LastCallbackError != "" {
		fields["last_callback_error"] = status.LastCallbackError
	}
	if status.LastCallbackErrorAt > 0 {
		fields["last_callback_error_at"] = status.LastCallbackErrorAt
	}
}

type nodeManagementPlatformRuntimeStore struct {
	base    webservice.ManagementPlatformRuntimeStatusStore
	emit    func(context.Context, webapi.Event)
	baseCtx func() context.Context
}

func wrapNodeManagementPlatformRuntimeStore(
	base webservice.ManagementPlatformRuntimeStatusStore,
	emit func(context.Context, webapi.Event),
	baseCtx func() context.Context,
) *nodeManagementPlatformRuntimeStore {
	if base == nil {
		base = webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
	}
	return &nodeManagementPlatformRuntimeStore{
		base:    base,
		emit:    emit,
		baseCtx: baseCtx,
	}
}

func ensureNodeManagementPlatformRuntimeStore(state *State) webservice.ManagementPlatformRuntimeStatusStore {
	if state == nil || state.App == nil {
		return webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
	}
	state.servicesMu.Lock()
	defer state.servicesMu.Unlock()
	emit := func(ctx context.Context, event webapi.Event) {
		emitNodeManagementEvent(state, ctx, event)
	}
	baseCtx := func() context.Context {
		return state.BaseContext()
	}
	switch current := state.App.Services.ManagementPlatformRuntimeStatus.(type) {
	case nil:
		wrapped := wrapNodeManagementPlatformRuntimeStore(nil, emit, baseCtx)
		state.App.Services.ManagementPlatformRuntimeStatus = wrapped
		return wrapped
	case *nodeManagementPlatformRuntimeStore:
		current.emit = emit
		current.baseCtx = baseCtx
		if current.base == nil {
			current.base = webservice.NewInMemoryManagementPlatformRuntimeStatusStore()
		}
		return current
	default:
		wrapped := wrapNodeManagementPlatformRuntimeStore(current, emit, baseCtx)
		state.App.Services.ManagementPlatformRuntimeStatus = wrapped
		return wrapped
	}
}

func (s *nodeManagementPlatformRuntimeStore) Reset() {
	if s == nil || s.base == nil {
		return
	}
	s.base.Reset()
}

func (s *nodeManagementPlatformRuntimeStore) Status(platformID string) webservice.ManagementPlatformReverseRuntimeStatus {
	if s == nil || s.base == nil {
		return webservice.ManagementPlatformReverseRuntimeStatus{}
	}
	return s.base.Status(platformID)
}

func (s *nodeManagementPlatformRuntimeStore) NoteConfigured(platformID, connectMode, reverseWSURL string, reverseEnabled bool) {
	s.noteStatusUpdateIfChanged(platformID, "configured", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteConfigured(platformID, connectMode, reverseWSURL, reverseEnabled)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteCallbackConfigured(
	platformID,
	callbackURL string,
	callbackEnabled bool,
	callbackTimeoutSeconds int,
	callbackRetryMax int,
	callbackRetryBackoffSec int,
	callbackQueueMax int,
	callbackQueueSize int,
) {
	s.noteStatusUpdateIfChanged(platformID, "callback_configured", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteCallbackConfigured(
			platformID,
			callbackURL,
			callbackEnabled,
			callbackTimeoutSeconds,
			callbackRetryMax,
			callbackRetryBackoffSec,
			callbackQueueMax,
			callbackQueueSize,
		)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteReverseConnected(platformID string) {
	s.noteStatusUpdate(platformID, "reverse_connected", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteReverseConnected(platformID)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteReverseDisconnected(platformID string, err error) {
	s.noteStatusUpdate(platformID, "reverse_disconnected", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteReverseDisconnected(platformID, err)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteReverseHello(platformID string) {
	s.note(platformID, func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteReverseHello(platformID)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteReversePing(platformID string) {
	s.note(platformID, func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteReversePing(platformID)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteReversePong(platformID string) {
	s.note(platformID, func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteReversePong(platformID)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteReverseEvent(platformID string) {
	s.note(platformID, func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteReverseEvent(platformID)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteCallbackDelivered(platformID string, statusCode int) {
	s.noteStatusUpdate(platformID, "callback_delivered", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteCallbackDelivered(platformID, statusCode)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteCallbackQueued(platformID string, queueSize int, dropped bool) {
	s.noteStatusUpdate(platformID, "callback_queued", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteCallbackQueued(platformID, queueSize, dropped)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteCallbackReplayDelivered(platformID string, statusCode int, queueSize int) {
	s.noteStatusUpdate(platformID, "callback_replayed", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteCallbackReplayDelivered(platformID, statusCode, queueSize)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteCallbackQueueSize(platformID string, queueSize int) {
	s.noteStatusUpdateIfChanged(platformID, "callback_queue_size", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteCallbackQueueSize(platformID, queueSize)
	})
}

func (s *nodeManagementPlatformRuntimeStore) NoteCallbackFailed(platformID string, err error, statusCode int) {
	s.noteStatusUpdate(platformID, "callback_failed", func(base webservice.ManagementPlatformRuntimeStatusStore, platformID string) {
		base.NoteCallbackFailed(platformID, err, statusCode)
	})
}

func (s *nodeManagementPlatformRuntimeStore) note(platformID string, note func(webservice.ManagementPlatformRuntimeStatusStore, string)) bool {
	if s == nil || s.base == nil || note == nil {
		return false
	}
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return false
	}
	note(s.base, platformID)
	return true
}

func (s *nodeManagementPlatformRuntimeStore) noteStatusUpdate(
	platformID,
	cause string,
	note func(webservice.ManagementPlatformRuntimeStatusStore, string),
) {
	if !s.note(platformID, note) {
		return
	}
	s.emitStatusUpdate(nil, platformID, cause)
}

func (s *nodeManagementPlatformRuntimeStore) noteStatusUpdateIfChanged(
	platformID,
	cause string,
	note func(webservice.ManagementPlatformRuntimeStatusStore, string),
) {
	if s == nil || s.base == nil || note == nil {
		return
	}
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return
	}
	before := s.base.Status(platformID)
	note(s.base, platformID)
	after := s.base.Status(platformID)
	if after == before {
		return
	}
	s.emitStatusSnapshot(nil, platformID, cause, after)
}

func (s *nodeManagementPlatformRuntimeStore) emitStatusSnapshot(
	ctx context.Context,
	platformID,
	cause string,
	status webservice.ManagementPlatformReverseRuntimeStatus,
) {
	if s == nil || s.base == nil || s.emit == nil {
		return
	}
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return
	}
	ctx = resolveNodeEmitContext(ctx, s.baseCtx)
	s.emit(ctx, managementPlatformStatusUpdatedEvent(platformID, status, cause))
}

func (s *nodeManagementPlatformRuntimeStore) emitStatusUpdate(ctx context.Context, platformID, cause string) {
	if s == nil || s.emit == nil {
		return
	}
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return
	}
	s.emitStatusSnapshot(ctx, platformID, cause, s.Status(platformID))
}

func managementPlatformStatusUpdatedEvent(
	platformID string,
	status webservice.ManagementPlatformReverseRuntimeStatus,
	cause string,
) webapi.Event {
	fields := map[string]interface{}{
		"platform_id":                    strings.TrimSpace(platformID),
		"connect_mode":                   status.ConnectMode,
		"reverse_ws_url":                 status.ReverseWSURL,
		"reverse_enabled":                status.ReverseEnabled,
		"reverse_connected":              status.ReverseConnected,
		"callback_url":                   status.CallbackURL,
		"callback_enabled":               status.CallbackEnabled,
		"callback_timeout_seconds":       status.CallbackTimeoutSeconds,
		"callback_retry_max":             status.CallbackRetryMax,
		"callback_retry_backoff_seconds": status.CallbackRetryBackoffSec,
		"last_connected_at":              status.LastConnectedAt,
		"last_disconnected_at":           status.LastDisconnectedAt,
		"last_callback_at":               status.LastCallbackAt,
		"last_callback_success_at":       status.LastCallbackSuccessAt,
		"callback_deliveries":            status.CallbackDeliveries,
		"callback_failures":              status.CallbackFailures,
		"callback_consecutive_failures":  status.CallbackConsecutiveFailures,
	}
	appendNodeCallbackQueueRuntimeFields(fields, status, status.CallbackQueueSize, status.CallbackQueueMax)
	if cause = strings.TrimSpace(cause); cause != "" {
		fields["cause"] = cause
	}
	if status.LastReverseError != "" {
		fields["last_reverse_error"] = status.LastReverseError
	}
	if status.LastReverseErrorAt > 0 {
		fields["last_reverse_error_at"] = status.LastReverseErrorAt
	}
	return webapi.Event{
		Name:     "management_platforms.updated",
		Resource: "management_platforms",
		Action:   "update",
		Fields:   fields,
	}
}
