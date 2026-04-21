package routers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
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
	nodeIdempotencyTTL             = 5 * time.Minute
	nodeIdempotencyInflightMinWait = 150 * time.Millisecond
)

type nodeIdempotencyStore struct {
	mu            sync.Mutex
	ttl           time.Duration
	entries       map[string]*nodeIdempotencyEntry
	path          string
	runtimeStatus webservice.NodeRuntimeStatusStore
	writer        *nodeRuntimeStateWriter
}

type nodeIdempotencyEntry struct {
	fingerprint string
	expiresAt   time.Time
	ready       chan struct{}
	httpResp    *nodeIdempotencyHTTPResponse
	wsResp      *nodeWSFrame
}

type nodeIdempotencyHTTPHeader http.Header

type nodeIdempotencyHTTPResponse struct {
	Status int
	Header nodeIdempotencyHTTPHeader
	Body   []byte
}

type nodeIdempotencyAcquireResult struct {
	entry    *nodeIdempotencyEntry
	httpResp *nodeIdempotencyHTTPResponse
	wsResp   *nodeWSFrame
	conflict bool
	err      error
}

type nodeIdempotencyCaptureWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

type nodeIdempotencyPersistedStore struct {
	Version int                             `json:"version,omitempty"`
	Entries []nodeIdempotencyPersistedEntry `json:"entries"`
}

type nodeIdempotencyPersistedEntry struct {
	Key         string                       `json:"key"`
	Fingerprint string                       `json:"fingerprint"`
	ExpiresAt   int64                        `json:"expires_at"`
	HTTPResp    *nodeIdempotencyHTTPResponse `json:"http_response,omitempty"`
	WSResp      *nodeWSFrame                 `json:"ws_response,omitempty"`
}

type nodeIdempotencyPersistSnapshot struct {
	Key         string
	Fingerprint string
	ExpiresAt   int64
	HTTPResp    *nodeIdempotencyHTTPResponse
	WSResp      *nodeWSFrame
}

func (h nodeIdempotencyHTTPHeader) Clone() nodeIdempotencyHTTPHeader {
	if len(h) == 0 {
		return nil
	}
	return normalizeNodeIdempotencyHTTPHeader(nodeIdempotencyHTTPHeader(http.Header(h).Clone()))
}

func (h nodeIdempotencyHTTPHeader) WriteTo(dst http.Header) {
	if len(h) == 0 || dst == nil {
		return
	}
	for key, values := range h {
		key = strings.TrimSpace(key)
		if key == "" || len(values) == 0 {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
}

func (h *nodeIdempotencyHTTPHeader) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*h = nil
		return nil
	}
	var multi http.Header
	if err := json.Unmarshal(data, &multi); err == nil {
		*h = normalizeNodeIdempotencyHTTPHeader(nodeIdempotencyHTTPHeader(multi))
		return nil
	}
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err == nil {
		headers := make(nodeIdempotencyHTTPHeader, len(legacy))
		for key, value := range legacy {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			headers[key] = []string{value}
		}
		*h = normalizeNodeIdempotencyHTTPHeader(headers)
		return nil
	}
	return json.Unmarshal(data, &multi)
}

func normalizeNodeIdempotencyHTTPHeader(header nodeIdempotencyHTTPHeader) nodeIdempotencyHTTPHeader {
	if len(header) == 0 {
		return nil
	}
	normalized := make(nodeIdempotencyHTTPHeader, len(header))
	for key, values := range header {
		key = strings.TrimSpace(key)
		if key == "" || len(values) == 0 {
			continue
		}
		normalized[key] = append([]string(nil), values...)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func newNodeIdempotencyStore(ttl time.Duration, path string) *nodeIdempotencyStore {
	if ttl <= 0 {
		ttl = nodeIdempotencyTTL
	}
	store := &nodeIdempotencyStore{
		ttl:     ttl,
		entries: make(map[string]*nodeIdempotencyEntry),
		path:    path,
		writer:  newNodeRuntimeStateWriter(path),
	}
	store.load()
	store.BindRuntimeStatus(webservice.NewInMemoryNodeRuntimeStatusStore())
	return store
}

func (s *nodeIdempotencyStore) TTL() time.Duration {
	if s == nil || s.ttl <= 0 {
		return nodeIdempotencyTTL
	}
	return s.ttl
}

func (s *nodeIdempotencyStore) BindRuntimeStatus(status webservice.NodeRuntimeStatusStore) {
	if s == nil {
		return
	}
	if status == nil {
		status = webservice.NewInMemoryNodeRuntimeStatusStore()
	}
	s.mu.Lock()
	s.runtimeStatus = status
	if s.runtimeStatus != nil {
		s.runtimeStatus.ResetIdempotency(s.ttl)
		s.updateRuntimeLocked()
	}
	s.mu.Unlock()
}

func nodeIdempotencyMiddleware(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		if state == nil || state.Idempotency == nil || !nodeHTTPWriteRequest(c) {
			c.Next()
			return
		}
		key := nodeHTTPRequestIdempotencyKey(c)
		if key == "" {
			c.Next()
			return
		}
		scope := nodeHTTPIdempotencyScope(c, currentActor(c))
		fingerprint := nodeHTTPIdempotencyFingerprint(c)
		result := state.Idempotency.acquireContext(c.Request.Context(), scope, key, fingerprint)
		switch {
		case result.err != nil:
			status, code, message := nodeIdempotencyAcquireError(result.err)
			c.AbortWithStatusJSON(status, webapi.ManagementErrorResponse{
				Error: webapi.ManagementErrorDetail{
					Code:    code,
					Message: message,
				},
			})
			return
		case result.conflict:
			c.AbortWithStatusJSON(http.StatusConflict, webapi.ManagementErrorResponse{
				Error: webapi.ManagementErrorDetail{
					Code:    "idempotency_conflict",
					Message: "idempotency key already used with a different request",
				},
			})
			return
		case result.httpResp != nil:
			writeCachedHTTPIdempotencyResponse(c, result.httpResp)
			return
		case result.entry == nil:
			c.Next()
			return
		}

		writer := &nodeIdempotencyCaptureWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()

		state.Idempotency.completeHTTP(scope, key, result.entry, &nodeIdempotencyHTTPResponse{
			Status: writer.Status(),
			Header: cloneNodeIdempotencyHTTPHeader(writer.Header()),
			Body:   writer.body.Bytes(),
		})
	}
}

func (w *nodeIdempotencyCaptureWriter) Write(data []byte) (int, error) {
	if w == nil || w.ResponseWriter == nil {
		return 0, net.ErrClosed
	}
	if len(data) > 0 {
		_, _ = w.body.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *nodeIdempotencyCaptureWriter) WriteString(value string) (int, error) {
	if w == nil || w.ResponseWriter == nil {
		return 0, net.ErrClosed
	}
	if value != "" {
		_, _ = w.body.WriteString(value)
	}
	return w.ResponseWriter.WriteString(value)
}

func (s *nodeIdempotencyStore) acquire(scope, key, fingerprint string) nodeIdempotencyAcquireResult {
	return s.acquireContext(nil, scope, key, fingerprint)
}

func (s *nodeIdempotencyStore) acquireContext(ctx context.Context, scope, key, fingerprint string) nodeIdempotencyAcquireResult {
	if s == nil || strings.TrimSpace(scope) == "" || strings.TrimSpace(key) == "" {
		return nodeIdempotencyAcquireResult{}
	}
	compound := scope + "\x00" + strings.TrimSpace(key)
	for {
		if err := nodeIdempotencyAcquireContextErr(ctx); err != nil {
			return nodeIdempotencyAcquireResult{err: err}
		}
		now := time.Now()
		changed := false
		s.mu.Lock()
		changed = s.gcLocked(now)
		if entry, ok := s.entries[compound]; ok {
			if entry.fingerprint != fingerprint {
				if s.runtimeStatus != nil {
					s.runtimeStatus.NoteIdempotencyConflict()
				}
				s.mu.Unlock()
				if changed {
					s.persist()
				}
				return nodeIdempotencyAcquireResult{conflict: true}
			}
			if entry.ready == nil {
				if s.runtimeStatus != nil {
					s.runtimeStatus.NoteIdempotencyReplay()
				}
				resp := nodeIdempotencyAcquireResult{
					httpResp: cloneNodeIdempotencyHTTPResponse(entry.httpResp),
					wsResp:   cloneNodeWSFrame(entry.wsResp),
				}
				s.mu.Unlock()
				if changed {
					s.persist()
				}
				return resp
			}
			wait := entry.ready
			s.mu.Unlock()
			if changed {
				s.persist()
			}
			timedOut, err := waitNodeIdempotencyReady(ctx, wait, s.TTL())
			if err != nil {
				return nodeIdempotencyAcquireResult{err: err}
			}
			if timedOut {
				s.expireInflightEntry(compound, entry)
			}
			continue
		}
		if err := nodeIdempotencyAcquireContextErr(ctx); err != nil {
			s.mu.Unlock()
			if changed {
				s.persist()
			}
			return nodeIdempotencyAcquireResult{err: err}
		}
		entry := &nodeIdempotencyEntry{
			fingerprint: fingerprint,
			ready:       make(chan struct{}),
		}
		s.entries[compound] = entry
		s.updateRuntimeLocked()
		s.mu.Unlock()
		if changed {
			s.persist()
		}
		return nodeIdempotencyAcquireResult{entry: entry}
	}
}

func nodeIdempotencyAcquireContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func waitNodeIdempotencyReady(ctx context.Context, ready <-chan struct{}, ttl time.Duration) (bool, error) {
	if ready == nil {
		return false, nil
	}
	if err := nodeIdempotencyAcquireContextErr(ctx); err != nil {
		return false, err
	}
	ttl = nodeIdempotencyInflightWaitTimeout(ttl)
	timer := time.NewTimer(ttl)
	defer timer.Stop()
	if ctx == nil {
		select {
		case <-ready:
			return false, nil
		case <-timer.C:
			return true, nil
		}
	}
	select {
	case <-ready:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		return true, nil
	}
}

func nodeIdempotencyInflightWaitTimeout(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return nodeIdempotencyTTL
	}
	if ttl < nodeIdempotencyInflightMinWait {
		return nodeIdempotencyInflightMinWait
	}
	return ttl
}

func nodeIdempotencyAcquireError(err error) (int, string, string) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusRequestTimeout, "idempotency_wait_timeout", "request timed out while waiting for the in-flight idempotent request"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "idempotency_wait_canceled", "request canceled while waiting for the in-flight idempotent request"
	default:
		return http.StatusRequestTimeout, "idempotency_wait_canceled", "request canceled while waiting for the in-flight idempotent request"
	}
}

func (s *nodeIdempotencyStore) completeHTTP(scope, key string, entry *nodeIdempotencyEntry, resp *nodeIdempotencyHTTPResponse) {
	if s == nil {
		return
	}
	s.complete(scope, key, entry, resp != nil && resp.Status > 0 && resp.Status < http.StatusInternalServerError, func(current *nodeIdempotencyEntry) {
		current.httpResp = cloneNodeIdempotencyHTTPResponse(resp)
	})
}

func (s *nodeIdempotencyStore) completeWS(scope, key string, entry *nodeIdempotencyEntry, resp *nodeWSFrame) {
	if s == nil {
		return
	}
	s.complete(scope, key, entry, resp != nil && resp.Status > 0 && resp.Status < http.StatusInternalServerError, func(current *nodeIdempotencyEntry) {
		current.wsResp = cloneNodeWSFrame(resp)
	})
}

func (s *nodeIdempotencyStore) complete(scope, key string, entry *nodeIdempotencyEntry, cacheable bool, update func(*nodeIdempotencyEntry)) {
	if s == nil || entry == nil || strings.TrimSpace(scope) == "" || strings.TrimSpace(key) == "" || update == nil {
		return
	}
	compound := scope + "\x00" + strings.TrimSpace(key)
	persist := false
	s.mu.Lock()
	current, ok := s.entries[compound]
	if !ok || current != entry {
		s.mu.Unlock()
		return
	}
	if !cacheable {
		releaseNodeIdempotencyEntry(current)
		delete(s.entries, compound)
		s.updateRuntimeLocked()
		persist = true
		s.mu.Unlock()
		if persist {
			s.persist()
		}
		return
	}
	current.expiresAt = time.Now().Add(s.TTL())
	update(current)
	releaseNodeIdempotencyEntry(current)
	s.updateRuntimeLocked()
	persist = true
	s.mu.Unlock()
	if persist {
		s.persist()
	}
}

func (s *nodeIdempotencyStore) gcLocked(now time.Time) bool {
	changed := false
	for key, entry := range s.entries {
		switch {
		case entry == nil:
			delete(s.entries, key)
			changed = true
		case entry.ready != nil:
			continue
		case entry.expiresAt.IsZero(), now.After(entry.expiresAt):
			releaseNodeIdempotencyEntry(entry)
			delete(s.entries, key)
			changed = true
		}
	}
	if changed {
		s.updateRuntimeLocked()
	}
	return changed
}

func (s *nodeIdempotencyStore) expireInflightEntry(compound string, entry *nodeIdempotencyEntry) {
	if s == nil || entry == nil || strings.TrimSpace(compound) == "" {
		return
	}
	s.mu.Lock()
	current, ok := s.entries[compound]
	if !ok || current != entry || current.ready == nil {
		s.mu.Unlock()
		return
	}
	releaseNodeIdempotencyEntry(current)
	delete(s.entries, compound)
	s.updateRuntimeLocked()
	s.mu.Unlock()
	s.persist()
}

func (s *nodeIdempotencyStore) updateRuntimeLocked() {
	if s == nil {
		return
	}
	entries, inflight := s.entryCountsLocked()
	if s.runtimeStatus != nil {
		s.runtimeStatus.UpdateIdempotency(entries, inflight, s.ttl)
	}
}

func (s *nodeIdempotencyStore) entryCountsLocked() (entries, inflight int) {
	if s == nil {
		return 0, 0
	}
	for _, entry := range s.entries {
		if entry == nil {
			continue
		}
		entries++
		if entry.ready != nil {
			inflight++
		}
	}
	return entries, inflight
}

func releaseNodeIdempotencyEntry(entry *nodeIdempotencyEntry) bool {
	if entry == nil || entry.ready == nil {
		return false
	}
	close(entry.ready)
	entry.ready = nil
	return true
}

func (s *nodeIdempotencyStore) load() {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return
	}
	var persisted nodeIdempotencyPersistedStore
	if err := readNodeRuntimeState(s.path, &persisted); err != nil {
		if !isIgnorableNodeRuntimeStateError(err) {
			logs.Warn("load node idempotency state %s error: %v", s.path, err)
		}
		return
	}
	if !nodeRuntimeStateVersionSupported(persisted.Version) {
		logs.Warn("load node idempotency state %s error: unsupported version=%d", s.path, persisted.Version)
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range persisted.Entries {
		if strings.TrimSpace(item.Key) == "" || strings.TrimSpace(item.Fingerprint) == "" || item.ExpiresAt <= now.Unix() {
			continue
		}
		if item.HTTPResp == nil && item.WSResp == nil {
			continue
		}
		entry := &nodeIdempotencyEntry{
			fingerprint: item.Fingerprint,
			expiresAt:   time.Unix(item.ExpiresAt, 0),
			httpResp:    cloneNodeIdempotencyHTTPResponse(item.HTTPResp),
			wsResp:      cloneNodeWSFrame(item.WSResp),
		}
		s.entries[item.Key] = entry
	}
	s.updateRuntimeLocked()
}

func (s *nodeIdempotencyStore) persist() {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return
	}
	persisted := nodeIdempotencyPersistedStore{
		Version: nodeRuntimeStateVersion,
	}
	persisted.Entries = s.snapshotPersistedEntries(time.Now())
	if s.writer != nil {
		s.writer.Store(persisted)
		return
	}
	writeNodeRuntimeState(s.path, persisted)
}

func (s *nodeIdempotencyStore) snapshotPersistedEntries(now time.Time) []nodeIdempotencyPersistedEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	snapshots := make([]nodeIdempotencyPersistSnapshot, 0, len(s.entries))
	for key, entry := range s.entries {
		if entry == nil || entry.ready != nil || entry.expiresAt.IsZero() || now.After(entry.expiresAt) {
			continue
		}
		if entry.httpResp == nil && entry.wsResp == nil {
			continue
		}
		snapshots = append(snapshots, nodeIdempotencyPersistSnapshot{
			Key:         key,
			Fingerprint: entry.fingerprint,
			ExpiresAt:   entry.expiresAt.Unix(),
			HTTPResp:    entry.httpResp,
			WSResp:      entry.wsResp,
		})
	}
	s.mu.Unlock()
	if len(snapshots) == 0 {
		return nil
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Key < snapshots[j].Key
	})
	entries := make([]nodeIdempotencyPersistedEntry, 0, len(snapshots))
	for _, snapshot := range snapshots {
		entries = append(entries, nodeIdempotencyPersistedEntry{
			Key:         snapshot.Key,
			Fingerprint: snapshot.Fingerprint,
			ExpiresAt:   snapshot.ExpiresAt,
			HTTPResp:    cloneNodeIdempotencyHTTPResponse(snapshot.HTTPResp),
			WSResp:      cloneNodeWSFrame(snapshot.WSResp),
		})
	}
	return entries
}

func (s *nodeIdempotencyStore) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := false
	for key, entry := range s.entries {
		if !releaseNodeIdempotencyEntry(entry) {
			continue
		}
		delete(s.entries, key)
		changed = true
	}
	if changed {
		s.updateRuntimeLocked()
	}
	s.mu.Unlock()
	if s.writer != nil {
		s.writer.Close()
	}
}

func (s *nodeIdempotencyStore) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	for _, entry := range s.entries {
		releaseNodeIdempotencyEntry(entry)
	}
	s.entries = make(map[string]*nodeIdempotencyEntry)
	if s.runtimeStatus != nil {
		s.runtimeStatus.ResetIdempotency(s.ttl)
	}
	s.mu.Unlock()
	s.persist()
}

func nodeHTTPWriteRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(c.Request.Method), http.MethodPost)
}

func nodeHTTPRequestIdempotencyKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return firstNonEmpty(
		c.GetHeader("Idempotency-Key"),
		c.GetHeader("X-Idempotency-Key"),
	)
}

func nodeWSRequestIdempotencyKey(frame nodeWSFrame) string {
	return firstNonEmpty(
		frameHeaderValue(frame.Headers, "Idempotency-Key"),
		frameHeaderValue(frame.Headers, "X-Idempotency-Key"),
	)
}

func nodeHTTPIdempotencyScope(c *gin.Context, actor *webapi.Actor) string {
	if c == nil || c.Request == nil {
		return ""
	}
	return "http|" + nodeActorScopeKey(actor) + "|" + strings.ToUpper(strings.TrimSpace(c.Request.Method)) + "|" + nodeHTTPIdempotencyTarget(c)
}

func nodeWSIdempotencyScope(actor *webapi.Actor, method, path, query string) string {
	return "ws|" + nodeActorScopeKey(actor) + "|" + strings.ToUpper(strings.TrimSpace(method)) + "|" + nodeWSIdempotencyTarget(path, query)
}

func nodeHTTPIdempotencyFingerprint(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	method := strings.ToUpper(strings.TrimSpace(c.Request.Method))
	target := nodeHTTPIdempotencyTarget(c)
	body := nodeHTTPRequestFingerprintBody(c)
	return nodeIdempotencyFingerprint(method, target, body)
}

func nodeWSIdempotencyFingerprint(method, path, query string, body []byte) string {
	return nodeIdempotencyFingerprint(method, nodeWSIdempotencyTarget(path, query), body)
}

func nodeIdempotencyFingerprint(method, target string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.ToUpper(strings.TrimSpace(method)) + "\n" + strings.TrimSpace(target) + "\n" + hex.EncodeToString(sum[:])
}

func nodeHTTPIdempotencyTarget(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return nodeWSIdempotencyTarget(c.Request.URL.Path, c.Request.URL.Query().Encode())
}

func nodeWSIdempotencyTarget(path, query string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	query = normalizeIdempotencyQuery(query)
	if query == "" {
		return path
	}
	return path + "?" + query
}

func normalizeIdempotencyQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	return values.Encode()
}

func nodeHTTPRequestFingerprintBody(c *gin.Context) []byte {
	if c == nil || c.Request == nil {
		return nil
	}
	if raw := framework.RequestRawBodyView(c); len(raw) > 0 {
		return raw
	}
	return nil
}

func nodeWSWriteRequest(method string) bool {
	return strings.EqualFold(strings.TrimSpace(method), http.MethodPost)
}

func nodeActorScopeKey(actor *webapi.Actor) string {
	if actor == nil {
		return "anonymous"
	}
	if subject := strings.TrimSpace(actor.SubjectID); subject != "" {
		return subject
	}
	if username := strings.TrimSpace(actor.Username); username != "" {
		return strings.TrimSpace(actor.Kind) + ":" + username
	}
	if len(actor.ClientIDs) == 0 {
		return strings.TrimSpace(actor.Kind)
	}
	clientIDs := append([]int(nil), actor.ClientIDs...)
	sort.Ints(clientIDs)
	parts := make([]string, 0, len(clientIDs))
	for _, clientID := range clientIDs {
		if clientID > 0 {
			parts = append(parts, strconvFormatInt(int64(clientID)))
		}
	}
	return strings.TrimSpace(actor.Kind) + ":" + strings.Join(parts, ",")
}

func writeCachedHTTPIdempotencyResponse(c *gin.Context, resp *nodeIdempotencyHTTPResponse) {
	if c == nil || resp == nil {
		return
	}
	resp.Header.WriteTo(c.Writer.Header())
	if requestID := currentIdempotencyReplayRequestID(c); requestID != "" {
		c.Writer.Header().Set("X-Request-ID", requestID)
	}
	c.Writer.Header().Set("X-Idempotent-Replay", "true")
	c.Status(resp.Status)
	if len(resp.Body) > 0 {
		_, _ = c.Writer.Write(resp.Body)
	}
	c.Abort()
}

func currentIdempotencyReplayRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if metadata := currentRequestMetadata(c); strings.TrimSpace(metadata.RequestID) != "" {
		return metadata.RequestID
	}
	return firstNonEmpty(
		c.GetHeader("X-Request-ID"),
		c.GetHeader("X-Correlation-ID"),
		c.Query("request_id"),
		c.Query("requestId"),
	)
}

func cloneNodeIdempotencyHTTPHeader(header http.Header) nodeIdempotencyHTTPHeader {
	if len(header) == 0 {
		return nil
	}
	return nodeIdempotencyHTTPHeader(header.Clone())
}

func cloneNodeIdempotencyHTTPResponse(resp *nodeIdempotencyHTTPResponse) *nodeIdempotencyHTTPResponse {
	if resp == nil {
		return nil
	}
	cloned := &nodeIdempotencyHTTPResponse{
		Status: resp.Status,
		Body:   append([]byte(nil), resp.Body...),
		Header: resp.Header.Clone(),
	}
	return cloned
}
