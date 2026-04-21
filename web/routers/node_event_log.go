package routers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

const defaultNodeEventLogSize = 1024

type nodeEventLog struct {
	mu          sync.RWMutex
	nextSeq     int64
	maxEntries  int
	entries     []webapi.Event
	history     []webapi.Event
	historyMax  int
	trimmed     int
	persistPath string
	journalPath string
	writer      *nodeRuntimeStateWriter
	journal     *nodeRuntimeAppendWriter
}

type nodeEventLogSnapshot struct {
	ConfigEpoch  string         `json:"config_epoch,omitempty"`
	After        int64          `json:"after"`
	NextAfter    int64          `json:"next_after"`
	Cursor       int64          `json:"cursor"`
	OldestCursor int64          `json:"oldest_cursor,omitempty"`
	Limit        int            `json:"limit"`
	Count        int            `json:"count"`
	LastSequence int64          `json:"last_sequence,omitempty"`
	HasMore      bool           `json:"has_more"`
	Gap          bool           `json:"gap"`
	Items        []webapi.Event `json:"items"`
}

type nodeEventLogState struct {
	Cursor              int64 `json:"cursor"`
	OldestCursor        int64 `json:"oldest_cursor,omitempty"`
	MaxEntries          int   `json:"max_entries"`
	HistoryOldestCursor int64 `json:"history_oldest_cursor,omitempty"`
	HistoryMaxEntries   int   `json:"history_max_entries,omitempty"`
}

type nodeEventLogPersistedState struct {
	Version int            `json:"version,omitempty"`
	NextSeq int64          `json:"next_seq"`
	Entries []webapi.Event `json:"entries"`
}

func newNodeEventLog(maxEntries int, persistPath string) *nodeEventLog {
	if maxEntries <= 0 {
		maxEntries = defaultNodeEventLogSize
	}
	log := &nodeEventLog{
		maxEntries:  maxEntries,
		entries:     make([]webapi.Event, 0, maxEntries),
		historyMax:  nodeEventLogHistoryRetention(maxEntries),
		persistPath: persistPath,
		journalPath: nodeEventLogJournalPath(persistPath),
		writer:      newNodeRuntimeStateWriter(persistPath),
		journal:     newNodeRuntimeAppendWriter(nodeEventLogJournalPath(persistPath)),
	}
	log.load()
	return log
}

func (l *nodeEventLog) Record(event webapi.Event) webapi.Event {
	return l.record(event, true)
}

func (l *nodeEventLog) RecordLiveOnly(event webapi.Event) webapi.Event {
	return l.record(event, false)
}

func (l *nodeEventLog) record(event webapi.Event, persistEntry bool) webapi.Event {
	if l == nil {
		return event
	}
	var (
		persisted      nodeEventLogPersistedState
		appendJournal  []byte
		compactHistory []webapi.Event
	)
	l.mu.Lock()
	if persistEntry {
		l.nextSeq++
		event.Sequence = l.nextSeq
	} else {
		// Live-only events should not advance durable replay cursors. They mirror
		// the current durable cursor so after/next_after semantics remain stable.
		event.Sequence = l.nextSeq
	}
	cloned := cloneEvent(event)
	if persistEntry {
		l.entries = append(l.entries, cloned)
		if len(l.entries) > l.maxEntries {
			l.entries = trimNodeEventWindowInPlace(l.entries, l.maxEntries)
		}
		l.history = append(l.history, cloned)
		if len(l.history) > l.historyMax {
			overflow := len(l.history) - l.historyMax
			l.trimmed += overflow
			l.history = trimNodeEventWindowInPlace(l.history, l.historyMax)
		}
		if l.trimmed >= nodeEventLogCompactThreshold(l.historyMax, l.maxEntries) {
			compactHistory = cloneEventList(l.history)
			l.trimmed = 0
		} else {
			appendJournal = marshalNodeEventLogJournalEntry(cloned)
		}
		persisted = l.persistedStateLocked()
	}
	l.mu.Unlock()
	if persistEntry {
		l.persist(persisted)
		if len(compactHistory) > 0 {
			l.compactJournal(compactHistory)
		} else if len(appendJournal) > 0 {
			l.appendJournal(appendJournal)
		}
	}
	return cloneEvent(cloned)
}

func (l *nodeEventLog) Query(after int64, limit int, allow func(webapi.Event) bool) nodeEventLogSnapshot {
	return l.queryFromEntries(after, limit, allow)
}

func (l *nodeEventLog) Replay(after int64, limit int, allow func(webapi.Event) bool) nodeEventLogSnapshot {
	return l.query(after, limit, allow, true)
}

func (l *nodeEventLog) queryFromEntries(after int64, limit int, allow func(webapi.Event) bool) nodeEventLogSnapshot {
	return l.query(after, limit, allow, false)
}

func (l *nodeEventLog) query(after int64, limit int, allow func(webapi.Event) bool, durable bool) nodeEventLogSnapshot {
	if l == nil {
		return nodeEventLogSnapshot{
			After:     after,
			NextAfter: after,
			Limit:     normalizeNodeEventLimit(limit),
		}
	}
	limit = normalizeNodeEventLimit(limit)
	snapshot := nodeEventLogSnapshot{
		After:     after,
		NextAfter: after,
		Limit:     limit,
	}
	l.mu.RLock()
	snapshot.Cursor = l.nextSeq
	source := l.entries
	if durable {
		source = l.history
	}
	if len(source) == 0 {
		l.mu.RUnlock()
		snapshot.NextAfter = snapshot.Cursor
		return snapshot
	}
	snapshot.OldestCursor = source[0].Sequence
	if after > 0 && after < snapshot.OldestCursor-1 {
		snapshot.Gap = true
	}
	source = snapshotNodeEventQueryEntries(source, after, limit, allow == nil)
	l.mu.RUnlock()
	if len(source) == 0 {
		snapshot.NextAfter = snapshot.Cursor
		return snapshot
	}
	itemCap := limit
	if itemCap > len(source) {
		itemCap = len(source)
	}
	items := make([]webapi.Event, 0, itemCap)
	for _, entry := range source {
		if entry.Sequence <= after {
			continue
		}
		if allow != nil && !allow(entry) {
			continue
		}
		if len(items) >= limit {
			snapshot.HasMore = true
			break
		}
		items = append(items, cloneEvent(entry))
	}
	snapshot.Items = items
	snapshot.Count = len(items)
	if len(items) > 0 {
		snapshot.LastSequence = items[len(items)-1].Sequence
		if snapshot.HasMore {
			snapshot.NextAfter = snapshot.LastSequence
		} else {
			snapshot.NextAfter = snapshot.Cursor
		}
	} else if !snapshot.HasMore {
		snapshot.NextAfter = snapshot.Cursor
	}
	return snapshot
}

func snapshotNodeEventQueryEntries(source []webapi.Event, after int64, limit int, unfiltered bool) []webapi.Event {
	if len(source) == 0 {
		return nil
	}
	start := 0
	if after > 0 {
		start = sort.Search(len(source), func(i int) bool {
			return source[i].Sequence > after
		})
	}
	if start >= len(source) {
		return nil
	}
	end := len(source)
	if unfiltered && limit > 0 {
		maxEntries := limit + 1
		if remaining := end - start; remaining > maxEntries {
			end = start + maxEntries
		}
	}
	return append([]webapi.Event(nil), source[start:end]...)
}

func (l *nodeEventLog) load() {
	if l == nil || l.persistPath == "" {
		return
	}
	var persisted nodeEventLogPersistedState
	if err := readNodeRuntimeState(l.persistPath, &persisted); err != nil {
		if !isIgnorableNodeRuntimeStateError(err) {
			logs.Warn("load node change log state %s error: %v", l.persistPath, err)
		}
		return
	}
	if !nodeRuntimeStateVersionSupported(persisted.Version) {
		logs.Warn("load node change log state %s error: unsupported version=%d", l.persistPath, persisted.Version)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextSeq = persisted.NextSeq
	l.entries = l.entries[:0]
	for _, entry := range persisted.Entries {
		cloned := cloneEvent(entry)
		if cloned.Sequence > l.nextSeq {
			l.nextSeq = cloned.Sequence
		}
		l.entries = append(l.entries, cloned)
	}
	if len(l.entries) > l.maxEntries {
		l.entries = trimNodeEventWindowInPlace(l.entries, l.maxEntries)
	}
	l.history = l.loadJournalLocked()
	if len(l.history) == 0 {
		l.history = cloneEventList(l.entries)
	}
	if len(l.history) > 0 {
		if tail := tailEventList(l.history, l.maxEntries); len(tail) > 0 {
			l.entries = tail
		}
		last := l.history[len(l.history)-1].Sequence
		if last > l.nextSeq {
			l.nextSeq = last
		}
	}
}

func (l *nodeEventLog) persistedStateLocked() nodeEventLogPersistedState {
	state := nodeEventLogPersistedState{
		Version: nodeRuntimeStateVersion,
		NextSeq: l.nextSeq,
	}
	if len(l.entries) > 0 {
		state.Entries = make([]webapi.Event, 0, len(l.entries))
		for _, entry := range l.entries {
			state.Entries = append(state.Entries, cloneEvent(entry))
		}
	}
	return state
}

func (l *nodeEventLog) persist(state nodeEventLogPersistedState) {
	if l == nil || l.persistPath == "" {
		return
	}
	if l.writer != nil {
		l.writer.Store(state)
		return
	}
	writeNodeRuntimeState(l.persistPath, state)
}

func (l *nodeEventLog) Close() {
	if l == nil {
		return
	}
	l.mu.RLock()
	needsCompact := l.trimmed > 0
	history := cloneEventList(l.history)
	l.mu.RUnlock()
	if needsCompact && len(history) > 0 {
		l.compactJournal(history)
	}
	if l.writer != nil {
		l.writer.Close()
	}
	if l.journal != nil {
		l.journal.Close()
	}
}

func (l *nodeEventLog) State() nodeEventLogState {
	if l == nil {
		return nodeEventLogState{MaxEntries: defaultNodeEventLogSize}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	state := nodeEventLogState{
		Cursor:            l.nextSeq,
		MaxEntries:        l.maxEntries,
		HistoryMaxEntries: l.historyMax,
	}
	if len(l.entries) > 0 {
		state.OldestCursor = l.entries[0].Sequence
	}
	if len(l.history) > 0 {
		state.HistoryOldestCursor = l.history[0].Sequence
	}
	return state
}

func (l *nodeEventLog) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.nextSeq = 0
	l.entries = l.entries[:0]
	l.history = l.history[:0]
	l.trimmed = 0
	persisted := l.persistedStateLocked()
	l.mu.Unlock()
	l.persist(persisted)
	l.compactJournal(nil)
}

func (l *nodeEventLog) appendJournal(data []byte) {
	if l == nil || len(data) == 0 || strings.TrimSpace(l.journalPath) == "" {
		return
	}
	if l.journal != nil {
		l.journal.Store(data)
		return
	}
	appendNodeRuntimeStateBytes(l.journalPath, data)
}

func (l *nodeEventLog) compactJournal(history []webapi.Event) {
	if l == nil || strings.TrimSpace(l.journalPath) == "" {
		return
	}
	if l.journal != nil {
		l.journal.Flush()
	}
	writeNodeRuntimeStateBytes(l.journalPath, marshalNodeEventLogJournal(history))
}

func (l *nodeEventLog) loadJournalLocked() []webapi.Event {
	path := strings.TrimSpace(l.journalPath)
	flushNodeRuntimeAppendState(path)
	if path == "" || !common.FileExists(path) {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		logs.Warn("load node change log journal %s error: %v", path, err)
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	history := make([]webapi.Event, 0)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event webapi.Event
		if err := json.Unmarshal(line, &event); err != nil {
			logs.Warn("load node change log journal %s error: %v", path, err)
			return tailEventList(history, l.historyMax)
		}
		history = append(history, cloneEvent(event))
	}
	if err := scanner.Err(); err != nil {
		logs.Warn("scan node change log journal %s error: %v", path, err)
	}
	return tailEventList(history, l.historyMax)
}

func nodeEventLogHistoryRetention(maxEntries int) int {
	retention := maxEntries * 4
	if retention < 4096 {
		retention = 4096
	}
	if retention > 16384 {
		retention = 16384
	}
	if retention < maxEntries {
		retention = maxEntries
	}
	return retention
}

func nodeEventLogCompactThreshold(historyMax, maxEntries int) int {
	threshold := historyMax / 4
	if threshold < maxEntries {
		threshold = maxEntries
	}
	if threshold <= 0 {
		threshold = maxEntries
	}
	if threshold <= 0 {
		threshold = defaultNodeEventLogSize
	}
	return threshold
}

func marshalNodeEventLogJournalEntry(event webapi.Event) []byte {
	data, err := json.Marshal(event)
	if err != nil {
		logs.Warn("marshal node change log journal entry error: %v", err)
		return nil
	}
	return append(data, '\n')
}

func marshalNodeEventLogJournal(events []webapi.Event) []byte {
	if len(events) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, event := range events {
		line := marshalNodeEventLogJournalEntry(event)
		if len(line) == 0 {
			continue
		}
		_, _ = buf.Write(line)
	}
	return buf.Bytes()
}

func cloneEventList(events []webapi.Event) []webapi.Event {
	if len(events) == 0 {
		return nil
	}
	cloned := make([]webapi.Event, 0, len(events))
	for _, event := range events {
		cloned = append(cloned, cloneEvent(event))
	}
	return cloned
}

func tailEventList(events []webapi.Event, limit int) []webapi.Event {
	if len(events) == 0 {
		return nil
	}
	if limit <= 0 || len(events) <= limit {
		return cloneEventList(events)
	}
	return cloneEventList(events[len(events)-limit:])
}

func normalizeNodeEventLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func trimNodeEventWindowInPlace(events []webapi.Event, limit int) []webapi.Event {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	drop := len(events) - limit
	copy(events, events[drop:])
	return events[:limit]
}

func currentNodeHTTPOperationID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value := strings.TrimSpace(c.GetHeader("X-Operation-ID")); value != "" {
		return value
	}
	if value := strings.TrimSpace(nodeRequestBodyValue(c, "operation_id")); value != "" {
		return value
	}
	return strings.TrimSpace(currentRequestMetadata(c).RequestID)
}

func currentNodeWSOperationID(c *wsAPIContext) string {
	if c == nil {
		return ""
	}
	if value := strings.TrimSpace(c.RequestHeader("X-Operation-ID")); value != "" {
		return value
	}
	if value := strings.TrimSpace(c.String("operation_id")); value != "" {
		return value
	}
	return strings.TrimSpace(c.Metadata().RequestID)
}

func setNodeHTTPOperationHeader(c *gin.Context, operationID string) {
	if c == nil {
		return
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return
	}
	c.Header("X-Operation-ID", operationID)
}

func setNodeWSOperationHeader(c *wsAPIContext, operationID string) {
	if c == nil {
		return
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return
	}
	c.SetResponseHeader("X-Operation-ID", operationID)
}

func shouldRecordStandaloneNodeOperation(source string) bool {
	source = strings.TrimSpace(source)
	if source == "" {
		return true
	}
	return !strings.Contains(source, ":batch")
}

func recordNodeOperation(state *State, actor *webapi.Actor, metadata webapi.RequestMetadata, operationID string, kind string, startedAt time.Time, count, successCount, errorCount int, paths []string) {
	if state == nil || !shouldRecordStandaloneNodeOperation(metadata.Source) {
		return
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return
	}
	operations := state.NodeOperations()
	if operations == nil {
		return
	}
	operations.Record(webservice.NodeOperationRecordPayload{
		OperationID:  operationID,
		Kind:         strings.TrimSpace(kind),
		RequestID:    strings.TrimSpace(metadata.RequestID),
		Source:       strings.TrimSpace(metadata.Source),
		Scope:        webapi.NodeOperationScope(actor),
		Actor:        webapi.NodeOperationActorPayload(actor),
		StartedAt:    startedAt.Unix(),
		FinishedAt:   time.Now().Unix(),
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Count:        count,
		SuccessCount: successCount,
		ErrorCount:   errorCount,
		Paths:        normalizeNodeOperationPaths(paths),
	})
}

func canonicalNodeOperationPath(state *State, rawPath string) string {
	return canonicalNodeOperationPathWithParams(state, rawPath, nil)
}

func canonicalNodeOperationPathWithParams(state *State, rawPath string, params map[string]string) string {
	rawPath, params, formalPath, ok := parseNodeOperationInput(rawPath, params)
	if !ok {
		return ""
	}
	snapshotPath, formalPath := normalizeNodeOperationInput(state, rawPath, params, formalPath)
	if formalPath {
		return "/api" + snapshotPath
	}
	return snapshotPath
}

func parseNodeOperationInput(rawPath string, params map[string]string) (string, map[string]string, bool, bool) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", params, false, false
	}
	formalPath := rawPath == "/api" || strings.HasPrefix(rawPath, "/api/")
	if parsed, err := url.Parse(rawPath); err == nil {
		if parsed.Path != "" {
			rawPath = parsed.Path
		}
		if params == nil && len(parsed.Query()) > 0 {
			params = make(map[string]string, len(parsed.Query()))
			for key, values := range parsed.Query() {
				if len(values) == 0 {
					continue
				}
				params[key] = values[len(values)-1]
			}
		}
	}
	return rawPath, params, formalPath, true
}

func normalizeNodeOperationInput(state *State, rawPath string, params map[string]string, formalPath bool) (string, bool) {
	cfg := (*servercfg.Snapshot)(nil)
	if state != nil {
		cfg = state.CurrentConfig()
	}
	snapshotPath := rawPath
	if isFormalNodeWSRequestPath(cfg, rawPath) {
		formalPath = true
	}
	if path, err := normalizeNodeWSPath(cfg, rawPath); err == nil {
		snapshotPath = path
	}
	if params != nil {
		if resolved := resolveNodeOperationSnapshotPath(snapshotPath, params); resolved != "" {
			snapshotPath = resolved
		}
	}
	if !strings.HasPrefix(snapshotPath, "/") {
		snapshotPath = "/" + snapshotPath
	}
	return snapshotPath, formalPath
}

func resolveNodeOperationSnapshotPath(snapshotPath string, params map[string]string) string {
	switch snapshotPath {
	case "/clients/get":
		if value := strings.TrimSpace(params["id"]); value != "" {
			return "/clients/" + value
		}
		if value := strings.TrimSpace(params["client_id"]); value != "" {
			return "/clients/" + value
		}
		if value := strings.TrimSpace(params["verify_key"]); value != "" {
			return "/clients/" + value
		}
	case "/security/bans/delete":
		if value := strings.TrimSpace(params["key"]); value != "" {
			return "/security/bans/" + value
		}
		if value := strings.TrimSpace(params["ip"]); value != "" {
			return "/security/bans/" + value
		}
	case "/webhook/delete":
		if value := strings.TrimSpace(params["id"]); value != "" {
			return "/webhooks/" + value
		}
	}
	return ""
}

func nodeOperationSuccessCount(status int) int {
	if status == 0 || status < http.StatusBadRequest {
		return 1
	}
	return 0
}

func nodeOperationErrorCount(status int) int {
	if status == 0 || status < http.StatusBadRequest {
		return 0
	}
	return 1
}

func normalizeNodeOperationPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func nodeOperationErrorFromStatus(status int) error {
	if status < http.StatusBadRequest {
		return nil
	}
	text := strings.TrimSpace(http.StatusText(status))
	if text == "" {
		text = "request failed"
	}
	return errors.New(strings.ToLower(text))
}

type nodeOperationsPersistedState struct {
	Version int                                     `json:"version,omitempty"`
	Items   []webservice.NodeOperationRecordPayload `json:"items"`
}

type nodeOperationRuntimeStore struct {
	store   *webservice.InMemoryNodeOperationStore
	path    string
	emit    func(context.Context, webapi.Event)
	baseCtx func() context.Context
	writer  *nodeRuntimeStateWriter
}

func newNodeOperationRuntimeStore(path string, limit int, emit func(context.Context, webapi.Event), baseCtx func() context.Context) *nodeOperationRuntimeStore {
	runtimeStore := &nodeOperationRuntimeStore{
		store:   webservice.NewInMemoryNodeOperationStore(limit),
		path:    strings.TrimSpace(path),
		emit:    emit,
		baseCtx: baseCtx,
		writer:  newNodeRuntimeStateWriter(path),
	}
	runtimeStore.load()
	return runtimeStore
}

func (s *nodeOperationRuntimeStore) Record(record webservice.NodeOperationRecordPayload) {
	if s == nil || s.store == nil {
		return
	}
	record.OperationID = strings.TrimSpace(record.OperationID)
	if record.OperationID == "" {
		return
	}
	record.Kind = strings.TrimSpace(record.Kind)
	record.RequestID = strings.TrimSpace(record.RequestID)
	record.Source = strings.TrimSpace(record.Source)
	record.Scope = strings.TrimSpace(record.Scope)
	record.Actor.Kind = strings.TrimSpace(record.Actor.Kind)
	record.Actor.SubjectID = strings.TrimSpace(record.Actor.SubjectID)
	record.Actor.Username = strings.TrimSpace(record.Actor.Username)
	record.Actor.PlatformID = strings.TrimSpace(record.Actor.PlatformID)
	record.Paths = cloneOperationEventPaths(record.Paths)
	if record.FinishedAt == 0 {
		record.FinishedAt = time.Now().Unix()
	}
	s.store.Record(record)
	s.persist()
	s.emitEvent(nil, nodeOperationUpdatedEvent(record))
}

func (s *nodeOperationRuntimeStore) Query(input webservice.NodeOperationQueryInput) webservice.NodeOperationsPayload {
	if s == nil || s.store == nil {
		return webservice.NodeOperationsPayload{}
	}
	return s.store.Query(input)
}

func (s *nodeOperationRuntimeStore) load() {
	if s == nil || s.store == nil || s.path == "" {
		return
	}
	var persisted nodeOperationsPersistedState
	if err := readNodeRuntimeState(s.path, &persisted); err != nil {
		if !isIgnorableNodeRuntimeStateError(err) {
			logs.Warn("load node operations state %s error: %v", s.path, err)
		}
		return
	}
	if !nodeRuntimeStateVersionSupported(persisted.Version) {
		logs.Warn("load node operations state %s error: unsupported version=%d", s.path, persisted.Version)
		return
	}
	sort.SliceStable(persisted.Items, func(i, j int) bool {
		if persisted.Items[i].FinishedAt == persisted.Items[j].FinishedAt {
			return persisted.Items[i].OperationID < persisted.Items[j].OperationID
		}
		return persisted.Items[i].FinishedAt < persisted.Items[j].FinishedAt
	})
	s.store.Restore(persisted.Items)
}

func (s *nodeOperationRuntimeStore) persist() {
	if s == nil || s.store == nil || s.path == "" {
		return
	}
	state := nodeOperationsPersistedState{
		Version: nodeRuntimeStateVersion,
		Items:   s.store.Snapshot(),
	}
	if s.writer != nil {
		s.writer.Store(state)
		return
	}
	writeNodeRuntimeState(s.path, state)
}

func (s *nodeOperationRuntimeStore) Close() {
	if s == nil || s.writer == nil {
		return
	}
	s.writer.Close()
}

func (s *nodeOperationRuntimeStore) Reset() {
	if s == nil || s.store == nil {
		return
	}
	s.store.Restore(nil)
	s.persist()
}

func (s *nodeOperationRuntimeStore) emitEvent(ctx context.Context, event webapi.Event) {
	if s == nil || s.emit == nil {
		return
	}
	ctx = resolveNodeEmitContext(ctx, s.baseCtx)
	s.emit(ctx, event)
}

func nodeOperationUpdatedEvent(record webservice.NodeOperationRecordPayload) webapi.Event {
	return webapi.Event{
		Name:     "operations.updated",
		Resource: "operations",
		Action:   "update",
		Fields: map[string]interface{}{
			"operation_id":  strings.TrimSpace(record.OperationID),
			"request_id":    strings.TrimSpace(record.RequestID),
			"kind":          strings.TrimSpace(record.Kind),
			"source":        strings.TrimSpace(record.Source),
			"scope":         strings.TrimSpace(record.Scope),
			"finished_at":   record.FinishedAt,
			"duration_ms":   record.DurationMs,
			"count":         record.Count,
			"success_count": record.SuccessCount,
			"error_count":   record.ErrorCount,
			"paths":         cloneOperationEventPaths(record.Paths),
			"actor_kind":    strings.TrimSpace(record.Actor.Kind),
			"actor_subject": strings.TrimSpace(record.Actor.SubjectID),
			"actor_name":    strings.TrimSpace(record.Actor.Username),
			"platform_id":   strings.TrimSpace(record.Actor.PlatformID),
		},
	}
}

func cloneOperationEventPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	cloned := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cloned = append(cloned, path)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

var errNodeChangesStateUnavailable = errors.New("node state is unavailable")

func nodeChangesHTTPHandler(state *State) gin.HandlerFunc {
	return func(c *gin.Context) {
		request := resolveNodeChangesQueryRequest(c.Query("after"), c.Query("limit"), c.Query("durable"), c.Query("history"))
		payload, err := queryNodeChangesForRequest(state, currentActor(c), request)
		if err != nil {
			nodeChangesAbort(c, err)
			return
		}
		writeNodeChangesData(c, time.Now().Unix(), payload)
	}
}

type nodeChangesQueryRequest struct {
	After   int64 `json:"after"`
	Limit   int   `json:"limit"`
	Durable bool  `json:"durable,omitempty"`
}

func resolveNodeChangesQueryRequest(afterRaw, limitRaw string, durableValues ...string) nodeChangesQueryRequest {
	return nodeChangesQueryRequest{
		After:   parseNodeChangesAfter(afterRaw),
		Limit:   normalizeNodeEventLimit(parseNodeChangesLimit(limitRaw)),
		Durable: parseNodeChangesDurable(durableValues...),
	}
}

func writeNodeChangesData(c *gin.Context, timestamp int64, payload nodeEventLogSnapshot) {
	if c == nil {
		return
	}
	c.JSON(http.StatusOK, webapi.ManagementDataResponse{
		Data: payload,
		Meta: webapi.ManagementResponseMetaForRequest(currentRequestMetadata(c), timestamp, payload.ConfigEpoch),
	})
}

func buildNodeWSNodeChangesManagementDataFrame(frame nodeWSFrame, metadata webapi.RequestMetadata, timestamp int64, payload nodeEventLogSnapshot) nodeWSFrame {
	return buildNodeWSManagementDataFrame(frame, metadata, payload.ConfigEpoch, timestamp, payload)
}

func requireNodeChangesAccess(state *State, actor *webapi.Actor) error {
	if state == nil {
		return errNodeChangesStateUnavailable
	}
	if actor == nil || strings.TrimSpace(actor.Kind) == "" || strings.EqualFold(actor.Kind, webservice.RoleAnonymous) {
		return webservice.ErrUnauthenticated
	}
	return nil
}

func queryNodeChangesForRequest(state *State, actor *webapi.Actor, request nodeChangesQueryRequest) (nodeEventLogSnapshot, error) {
	if err := requireNodeChangesAccess(state, actor); err != nil {
		return nodeEventLogSnapshot{}, err
	}
	return queryNodeChangesRequest(state, actor, request), nil
}

func nodeChangesErrorDetail(err error) (int, webapi.ManagementErrorDetail) {
	switch {
	case errors.Is(err, errNodeChangesStateUnavailable):
		return http.StatusInternalServerError, webapi.ManagementErrorDetail{
			Code:    "node_state_unavailable",
			Message: "node state is unavailable",
		}
	case errors.Is(err, webservice.ErrUnauthenticated):
		return http.StatusUnauthorized, webapi.ManagementErrorDetail{
			Code:    "unauthorized",
			Message: "unauthorized",
		}
	default:
		response := webapi.ManagementErrorResponseForStatus(http.StatusInternalServerError, err)
		return http.StatusInternalServerError, response.Error
	}
}

func nodeChangesAbort(c *gin.Context, err error) {
	if c == nil {
		return
	}
	status, detail := nodeChangesErrorDetail(err)
	c.AbortWithStatusJSON(status, webapi.ManagementErrorResponse{Error: detail})
}

func writeWSNodeChangesError(ctx *wsAPIContext, err error) {
	if ctx == nil {
		return
	}
	status, detail := nodeChangesErrorDetail(err)
	writeWSManagementError(ctx, status, detail.Code, detail.Message, detail.Details)
}

func queryNodeChanges(state *State, actor *webapi.Actor, after int64, limit int) nodeEventLogSnapshot {
	return queryNodeChangesRequest(state, actor, nodeChangesQueryRequest{
		After: after,
		Limit: limit,
	})
}

func replayNodeChanges(state *State, actor *webapi.Actor, after int64, limit int) nodeEventLogSnapshot {
	return queryNodeChangesRequest(state, actor, nodeChangesQueryRequest{
		After:   after,
		Limit:   limit,
		Durable: true,
	})
}

func queryNodeChangesRequest(state *State, actor *webapi.Actor, request nodeChangesQueryRequest) nodeEventLogSnapshot {
	request.Limit = normalizeNodeEventLimit(request.Limit)
	if state == nil || state.NodeEventLog == nil {
		snapshot := nodeEventLogSnapshot{
			After:     request.After,
			NextAfter: request.After,
			Limit:     request.Limit,
		}
		if state != nil {
			snapshot.ConfigEpoch = state.RuntimeIdentity().ConfigEpoch()
		}
		return snapshot
	}
	query := state.NodeEventLog.Query
	if request.Durable {
		query = state.NodeEventLog.Replay
	}
	snapshot := query(request.After, request.Limit, func(event webapi.Event) bool {
		if state.NodeEvents == nil {
			return true
		}
		return state.NodeEvents.allowsEvent(actor, event)
	})
	snapshot.ConfigEpoch = state.RuntimeIdentity().ConfigEpoch()
	return snapshot
}

func parseNodeChangesAfter(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func parseNodeChangesLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 100
	}
	return value
}

func parseNodeChangesDurable(values ...string) bool {
	for _, raw := range values {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on", "durable", "history":
			return true
		}
	}
	return false
}

func cloneEvent(event webapi.Event) webapi.Event {
	cloned := event
	if event.Actor != nil {
		cloned.Actor = cloneActor(event.Actor)
	}
	if len(event.Fields) > 0 {
		cloned.Fields = cloneEventFieldMap(event.Fields)
	}
	return cloned
}

func cloneEventFieldMap(fields map[string]interface{}) map[string]interface{} {
	if len(fields) == 0 {
		return nil
	}
	cloned := make(map[string]interface{}, len(fields))
	for key, value := range fields {
		cloned[key] = cloneEventFieldValue(value)
	}
	return cloned
}

func cloneEventFieldValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), typed...)
	case []int:
		return append([]int(nil), typed...)
	case []int64:
		return append([]int64(nil), typed...)
	case []float64:
		return append([]float64(nil), typed...)
	case []string:
		return append([]string(nil), typed...)
	case []bool:
		return append([]bool(nil), typed...)
	case []interface{}:
		cloned := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			cloned = append(cloned, cloneEventFieldValue(item))
		}
		return cloned
	case map[string]interface{}:
		return cloneEventFieldMap(typed)
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	case map[string]int:
		cloned := make(map[string]int, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	case map[string]int64:
		cloned := make(map[string]int64, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	case map[string]bool:
		cloned := make(map[string]bool, len(typed))
		for key, item := range typed {
			cloned[key] = item
		}
		return cloned
	default:
		return value
	}
}
