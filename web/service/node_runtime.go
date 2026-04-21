package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type NodeRuntimeIdentity interface {
	BootID() string
	StartedAt() int64
	ConfigEpoch() string
	RotateConfigEpoch() string
}

type StaticNodeRuntimeIdentity struct {
	mu          sync.RWMutex
	bootID      string
	startedAt   int64
	configEpoch string
}

func NewNodeRuntimeIdentity() *StaticNodeRuntimeIdentity {
	return &StaticNodeRuntimeIdentity{
		bootID:      initNodeRuntimeBootID(),
		startedAt:   common.GetStartTime(),
		configEpoch: initNodeRuntimeBootID(),
	}
}

func (i *StaticNodeRuntimeIdentity) BootID() string {
	if i == nil {
		return ""
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.bootID
}

func (i *StaticNodeRuntimeIdentity) StartedAt() int64 {
	if i == nil {
		return 0
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.startedAt
}

func (i *StaticNodeRuntimeIdentity) ConfigEpoch() string {
	if i == nil {
		return ""
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.configEpoch
}

func (i *StaticNodeRuntimeIdentity) RotateConfigEpoch() string {
	if i == nil {
		return ""
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.configEpoch = initNodeRuntimeBootID()
	return i.configEpoch
}

type NodeRuntimeStatusStore interface {
	NoteOperation(string, error)
	Operations() NodeOperationPayload
	ResetOperations()
	UpdateIdempotency(int, int, time.Duration)
	ResetIdempotency(time.Duration)
	NoteIdempotencyReplay()
	NoteIdempotencyConflict()
	Idempotency() NodeIdempotencyPayload
}

type InMemoryNodeRuntimeStatusStore struct {
	opsMu         sync.RWMutex
	ops           NodeOperationPayload
	idempotencyMu sync.RWMutex
	idempotency   NodeIdempotencyPayload
}

func NewInMemoryNodeRuntimeStatusStore() *InMemoryNodeRuntimeStatusStore {
	return &InMemoryNodeRuntimeStatusStore{}
}

func (s *InMemoryNodeRuntimeStatusStore) NoteOperation(kind string, err error) {
	if s == nil {
		return
	}
	kind = strings.TrimSpace(kind)
	now := time.Now().Unix()
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	switch kind {
	case "sync":
		s.ops.LastSyncAt = now
	case "traffic":
		s.ops.LastTrafficAt = now
	case "kick":
		s.ops.LastKickAt = now
	case "":
	default:
		s.ops.LastMutationAt = now
	}
	if err != nil {
		s.ops.LastError = err.Error()
		s.ops.LastErrorAt = now
	}
}

func (s *InMemoryNodeRuntimeStatusStore) Operations() NodeOperationPayload {
	if s == nil {
		return NodeOperationPayload{}
	}
	s.opsMu.RLock()
	defer s.opsMu.RUnlock()
	return s.ops
}

func (s *InMemoryNodeRuntimeStatusStore) ResetOperations() {
	if s == nil {
		return
	}
	s.opsMu.Lock()
	defer s.opsMu.Unlock()
	s.ops = NodeOperationPayload{}
}

func (s *InMemoryNodeRuntimeStatusStore) UpdateIdempotency(entries, inflight int, ttl time.Duration) {
	if s == nil {
		return
	}
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	s.idempotency.CachedEntries = entries
	s.idempotency.Inflight = inflight
	s.idempotency.TTLSeconds = int64(ttl / time.Second)
}

func (s *InMemoryNodeRuntimeStatusStore) ResetIdempotency(ttl time.Duration) {
	if s == nil {
		return
	}
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	s.idempotency = NodeIdempotencyPayload{
		TTLSeconds: int64(ttl / time.Second),
	}
}

func (s *InMemoryNodeRuntimeStatusStore) NoteIdempotencyReplay() {
	if s == nil {
		return
	}
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	s.idempotency.ReplayHits++
}

func (s *InMemoryNodeRuntimeStatusStore) NoteIdempotencyConflict() {
	if s == nil {
		return
	}
	s.idempotencyMu.Lock()
	defer s.idempotencyMu.Unlock()
	s.idempotency.Conflicts++
}

func (s *InMemoryNodeRuntimeStatusStore) Idempotency() NodeIdempotencyPayload {
	if s == nil {
		return NodeIdempotencyPayload{}
	}
	s.idempotencyMu.RLock()
	defer s.idempotencyMu.RUnlock()
	return s.idempotency
}

type NodeStorage interface {
	FlushLocal() error
	FlushRuntime() error
	Snapshot() (interface{}, error)
	ImportSnapshot(*file.ConfigSnapshot) error
	SaveUser(*file.User) error
	SaveClient(*file.Client) error
	ResolveUser(int) (*file.User, error)
	ResolveClient(NodeClientTarget) (*file.Client, error)
	ResolveTrafficClient(file.TrafficDelta) (*file.Client, error)
}

type DefaultNodeStorage struct{}

func (DefaultNodeStorage) WithDeferredPersistence(run func() error) error {
	if run == nil {
		return nil
	}
	if deferredStore, ok := file.GlobalStore.(interface {
		WithDeferredPersistence(func() error) error
	}); ok {
		return deferredStore.WithDeferredPersistence(run)
	}
	return run()
}

type NodeTrafficThresholdReport struct {
	Client          *file.Client
	DeltaIn         int64
	DeltaOut        int64
	TotalIn         int64
	TotalOut        int64
	Trigger         string
	IntervalSeconds int
	StepBytes       int64
	ReportedAt      int64
}

type NodeTrafficReporter interface {
	Observe(client *file.Client, deltaIn, deltaOut int64) (*NodeTrafficThresholdReport, bool)
}

type DefaultNodeTrafficReporter struct {
	ConfigProvider func() *servercfg.Snapshot

	mu   sync.Mutex
	byID map[int]nodeTrafficReporterState
	now  func() time.Time
}

type nodeTrafficReporterState struct {
	LastReportedAt time.Time
	PendingIn      int64
	PendingOut     int64
}

const (
	NodeOperationDefaultHistoryLimit = 200
	NodeOperationDefaultQueryLimit   = 20
	NodeOperationMaxQueryLimit       = 200
)

type NodeOperationActorPayload struct {
	Kind       string `json:"kind,omitempty"`
	SubjectID  string `json:"subject_id,omitempty"`
	Username   string `json:"username,omitempty"`
	PlatformID string `json:"platform_id,omitempty"`
}

type NodeOperationRecordPayload struct {
	OperationID  string                    `json:"operation_id"`
	Kind         string                    `json:"kind"`
	RequestID    string                    `json:"request_id,omitempty"`
	Source       string                    `json:"source,omitempty"`
	Scope        string                    `json:"scope,omitempty"`
	Actor        NodeOperationActorPayload `json:"actor,omitempty"`
	StartedAt    int64                     `json:"started_at,omitempty"`
	FinishedAt   int64                     `json:"finished_at,omitempty"`
	DurationMs   int64                     `json:"duration_ms,omitempty"`
	Count        int                       `json:"count"`
	SuccessCount int                       `json:"success_count"`
	ErrorCount   int                       `json:"error_count"`
	Paths        []string                  `json:"paths,omitempty"`
}

type NodeOperationsPayload struct {
	Timestamp int64                        `json:"timestamp"`
	Items     []NodeOperationRecordPayload `json:"items"`
}

type NodeOperationQueryInput struct {
	Scope       NodeAccessScope
	SubjectID   string
	OperationID string
	Limit       int
}

type NodeOperationStore interface {
	Record(NodeOperationRecordPayload)
	Query(NodeOperationQueryInput) NodeOperationsPayload
}

type InMemoryNodeOperationStore struct {
	mu      sync.RWMutex
	limit   int
	records []NodeOperationRecordPayload
}

func NewInMemoryNodeOperationStore(limit int) *InMemoryNodeOperationStore {
	if limit <= 0 {
		limit = NodeOperationDefaultHistoryLimit
	}
	return &InMemoryNodeOperationStore{limit: limit}
}

func mapNodeStorageClientLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, file.ErrClientNotFound) {
		return ErrClientNotFound
	}
	return err
}

func mapNodeStorageUserLookupError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, file.ErrUserNotFound) {
		return ErrUserNotFound
	}
	return err
}

func (DefaultNodeStorage) FlushLocal() error {
	file.GetDb().FlushToDisk()
	return nil
}

func (DefaultNodeStorage) FlushRuntime() error {
	if file.GlobalStore == nil {
		return nil
	}
	return file.GlobalStore.Flush()
}

func (DefaultNodeStorage) Snapshot() (interface{}, error) {
	exporter, ok := file.GlobalStore.(file.ConfigExporter)
	if !ok {
		return nil, ErrSnapshotExportUnsupported
	}
	return exporter.ExportConfigSnapshot()
}

func (DefaultNodeStorage) ImportSnapshot(snapshot *file.ConfigSnapshot) error {
	importer, ok := file.GlobalStore.(file.ConfigImporter)
	if !ok {
		return ErrSnapshotImportUnsupported
	}
	return importer.ImportConfigSnapshot(snapshot)
}

func (DefaultNodeStorage) SaveUser(user *file.User) error {
	if file.GlobalStore == nil {
		return ErrStoreNotInitialized
	}
	return file.GlobalStore.UpdateUser(user)
}

func (DefaultNodeStorage) SaveClient(client *file.Client) error {
	if file.GlobalStore == nil {
		return ErrStoreNotInitialized
	}
	return file.GlobalStore.UpdateClient(client)
}

func (DefaultNodeStorage) ResolveClient(target NodeClientTarget) (*file.Client, error) {
	return resolveStoredClient(target.ClientID, target.VerifyKey, ErrClientIdentifierRequired)
}

func (DefaultNodeStorage) ResolveUser(userID int) (*file.User, error) {
	if file.GlobalStore == nil {
		return nil, ErrStoreNotInitialized
	}
	if userID <= 0 {
		return nil, ErrUserNotFound
	}
	user, err := file.GlobalStore.GetUser(userID)
	if err != nil {
		return nil, mapNodeStorageUserLookupError(err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	return cloneUserForMutation(user), nil
}

func (DefaultNodeStorage) ResolveTrafficClient(item file.TrafficDelta) (*file.Client, error) {
	return resolveStoredClient(item.ClientID, item.VerifyKey, ErrTrafficTargetRequired)
}

func resolveStoredClient(clientID int, verifyKey string, missingVerifyErr error) (*file.Client, error) {
	if file.GlobalStore == nil {
		return nil, ErrStoreNotInitialized
	}
	verifyKey = strings.TrimSpace(verifyKey)
	if clientID > 0 {
		client, err := file.GlobalStore.GetClientByID(clientID)
		if err != nil {
			return nil, mapNodeStorageClientLookupError(err)
		}
		if client == nil || isReservedRuntimeClient(client) {
			return nil, ErrClientNotFound
		}
		if verifyKey != "" && strings.TrimSpace(client.VerifyKey) != verifyKey {
			return nil, ErrClientIdentifierConflict
		}
		return cloneClientSnapshotForMutation(client), nil
	}
	if verifyKey == "" {
		return nil, missingVerifyErr
	}
	client, err := file.GlobalStore.GetClient(verifyKey)
	if err != nil {
		return nil, mapNodeStorageClientLookupError(err)
	}
	if client == nil || isReservedRuntimeClient(client) {
		return nil, ErrClientNotFound
	}
	return cloneClientSnapshotForMutation(client), nil
}

func (r *DefaultNodeTrafficReporter) Observe(client *file.Client, deltaIn, deltaOut int64) (*NodeTrafficThresholdReport, bool) {
	if r == nil || client == nil || client.Id <= 0 {
		return nil, false
	}
	intervalSeconds, stepBytes := r.thresholds()
	if intervalSeconds <= 0 && stepBytes <= 0 {
		return nil, false
	}
	if client.EffectiveFlowLimitBytes() <= 0 {
		r.mu.Lock()
		if r.byID != nil {
			delete(r.byID, client.Id)
		}
		r.mu.Unlock()
		return nil, false
	}
	now := r.nowTime()
	trigger := ""
	reportDeltaIn := int64(0)
	reportDeltaOut := int64(0)

	r.mu.Lock()
	if r.byID == nil {
		r.byID = make(map[int]nodeTrafficReporterState)
	}
	state := r.byID[client.Id]
	state.PendingIn += deltaIn
	state.PendingOut += deltaOut

	if state.LastReportedAt.IsZero() {
		trigger = "initial"
	}
	if trigger == "" && intervalSeconds > 0 && now.Sub(state.LastReportedAt) >= time.Duration(intervalSeconds)*time.Second {
		trigger = "interval"
	}
	if stepBytes > 0 && state.PendingIn+state.PendingOut >= stepBytes {
		if trigger == "" {
			trigger = "step"
		} else if !strings.Contains(trigger, "step") {
			trigger += "+step"
		}
	}
	if trigger == "" {
		r.byID[client.Id] = state
		r.mu.Unlock()
		return nil, false
	}
	reportDeltaIn = state.PendingIn
	reportDeltaOut = state.PendingOut

	state.LastReportedAt = now
	state.PendingIn = 0
	state.PendingOut = 0
	r.byID[client.Id] = state
	r.mu.Unlock()

	report := buildNodeTrafficThresholdReport(client, reportDeltaIn, reportDeltaOut, trigger, intervalSeconds, stepBytes, now)
	if report == nil {
		return nil, false
	}
	return report, true
}

func (r *DefaultNodeTrafficReporter) thresholds() (int, int64) {
	cfg := servercfg.Current()
	if r != nil {
		cfg = servercfg.ResolveProvider(r.ConfigProvider)
	}
	return cfg.Runtime.NodeTrafficReportIntervalSeconds(), cfg.Runtime.NodeTrafficReportStepBytes()
}

func (r *DefaultNodeTrafficReporter) nowTime() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func buildNodeTrafficThresholdReport(client *file.Client, deltaIn, deltaOut int64, trigger string, intervalSeconds int, stepBytes int64, reportedAt time.Time) *NodeTrafficThresholdReport {
	snapshot := cloneClientSnapshotForMutation(client)
	if snapshot == nil {
		return nil
	}
	totalIn, totalOut, _ := snapshot.TotalTrafficTotals()
	return &NodeTrafficThresholdReport{
		Client:          snapshot,
		DeltaIn:         deltaIn,
		DeltaOut:        deltaOut,
		TotalIn:         totalIn,
		TotalOut:        totalOut,
		Trigger:         trigger,
		IntervalSeconds: intervalSeconds,
		StepBytes:       stepBytes,
		ReportedAt:      reportedAt.Unix(),
	}
}

func (s *InMemoryNodeOperationStore) Record(record NodeOperationRecordPayload) {
	if s == nil {
		return
	}
	record, ok := normalizeNodeOperationRecord(record, true)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	s.records = trimNodeOperationRecordHistory(s.records, s.limit)
}

func (s *InMemoryNodeOperationStore) Query(input NodeOperationQueryInput) NodeOperationsPayload {
	if s == nil {
		return NodeOperationsPayload{Timestamp: time.Now().Unix()}
	}
	limit := normalizeNodeOperationQueryLimit(input.Limit)
	operationID := strings.TrimSpace(input.OperationID)
	subjectID := strings.TrimSpace(input.SubjectID)
	records := s.snapshotRecords()
	items := make([]NodeOperationRecordPayload, 0, minInt(limit, len(records)))
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if operationID != "" && record.OperationID != operationID {
			continue
		}
		if !input.Scope.IsFullAccess() && !allowsNodeOperationRecord(input.Scope, subjectID, record) {
			continue
		}
		items = append(items, cloneNodeOperationRecord(record))
		if operationID != "" || len(items) >= limit {
			break
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].FinishedAt == items[j].FinishedAt {
			return items[i].OperationID > items[j].OperationID
		}
		return items[i].FinishedAt > items[j].FinishedAt
	})
	return NodeOperationsPayload{
		Timestamp: time.Now().Unix(),
		Items:     items,
	}
}

func (s *InMemoryNodeOperationStore) Snapshot() []NodeOperationRecordPayload {
	if s == nil {
		return nil
	}
	return cloneNodeOperationRecordList(s.snapshotRecords())
}

func (s *InMemoryNodeOperationStore) Restore(records []NodeOperationRecordPayload) {
	if s == nil {
		return
	}
	records = trimNodeOperationRecordHistory(normalizeNodeOperationRecordList(records, false), s.limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = records
}

func allowsNodeOperationRecord(scope NodeAccessScope, subjectID string, record NodeOperationRecordPayload) bool {
	if scope.IsFullAccess() {
		return true
	}
	switch record.Actor.Kind {
	case "platform_admin", "platform_user":
		if !scope.AllowsPlatform(record.Actor.PlatformID) {
			return false
		}
		if scope.accountWide {
			return true
		}
		return subjectID != "" && subjectID == record.Actor.SubjectID
	case "user":
		return subjectID != "" && subjectID == record.Actor.SubjectID
	default:
		return false
	}
}

func normalizeNodeOperationQueryLimit(limit int) int {
	if limit <= 0 {
		return NodeOperationDefaultQueryLimit
	}
	if limit > NodeOperationMaxQueryLimit {
		return NodeOperationMaxQueryLimit
	}
	return limit
}

func (s *InMemoryNodeOperationStore) snapshotRecords() []NodeOperationRecordPayload {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.records) == 0 {
		return nil
	}
	records := make([]NodeOperationRecordPayload, len(s.records))
	copy(records, s.records)
	return records
}

func normalizeNodeOperationRecord(record NodeOperationRecordPayload, fillFinishedAt bool) (NodeOperationRecordPayload, bool) {
	record.OperationID = strings.TrimSpace(record.OperationID)
	if record.OperationID == "" {
		return NodeOperationRecordPayload{}, false
	}
	record.Kind = strings.TrimSpace(record.Kind)
	record.RequestID = strings.TrimSpace(record.RequestID)
	record.Source = strings.TrimSpace(record.Source)
	record.Scope = strings.TrimSpace(record.Scope)
	record.Actor.Kind = strings.TrimSpace(record.Actor.Kind)
	record.Actor.SubjectID = strings.TrimSpace(record.Actor.SubjectID)
	record.Actor.Username = strings.TrimSpace(record.Actor.Username)
	record.Actor.PlatformID = strings.TrimSpace(record.Actor.PlatformID)
	record.Paths = cloneNodeOperationPaths(record.Paths)
	if fillFinishedAt && record.FinishedAt == 0 {
		record.FinishedAt = time.Now().Unix()
	}
	return record, true
}

func normalizeNodeOperationRecordList(records []NodeOperationRecordPayload, fillFinishedAt bool) []NodeOperationRecordPayload {
	if len(records) == 0 {
		return nil
	}
	normalized := make([]NodeOperationRecordPayload, 0, len(records))
	for _, record := range records {
		record, ok := normalizeNodeOperationRecord(record, fillFinishedAt)
		if !ok {
			continue
		}
		normalized = append(normalized, record)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func trimNodeOperationRecordHistory(records []NodeOperationRecordPayload, limit int) []NodeOperationRecordPayload {
	if len(records) == 0 || limit <= 0 {
		return records
	}
	if extra := len(records) - limit; extra > 0 {
		copy(records, records[extra:])
		records = records[:len(records)-extra]
	}
	return records
}

func cloneNodeOperationRecord(record NodeOperationRecordPayload) NodeOperationRecordPayload {
	record.Paths = cloneNodeOperationPaths(record.Paths)
	return record
}

func cloneNodeOperationRecordList(records []NodeOperationRecordPayload) []NodeOperationRecordPayload {
	if len(records) == 0 {
		return nil
	}
	cloned := make([]NodeOperationRecordPayload, 0, len(records))
	for _, record := range records {
		cloned = append(cloned, cloneNodeOperationRecord(record))
	}
	return cloned
}

func cloneNodeOperationPaths(paths []string) []string {
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

func initNodeRuntimeBootID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
