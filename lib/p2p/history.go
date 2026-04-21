package p2p

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/logs"
)

const (
	predictionHistoryTTL        = 30 * time.Minute
	predictionHistoryMaxEntries = 256
	predictionHistoryMaxOffsets = 6
	adaptiveProfileHistoryTTL   = 2 * time.Hour
	adaptiveProfileMaxEntries   = 512
	p2pTelemetrySchemaVersion   = 1
)

type predictionHistoryEntry struct {
	updatedAt time.Time
	offsets   map[int]predictionOffsetStat
}

type predictionOffsetStat struct {
	count     int
	updatedAt time.Time
}

type predictionHistoryStore struct {
	mu      sync.Mutex
	entries map[string]*predictionHistoryEntry
}

var globalPredictionHistory = &predictionHistoryStore{
	entries: make(map[string]*predictionHistoryEntry),
}

type adaptiveProfileEntry struct {
	updatedAt      time.Time
	successCount   int
	timeoutCount   int
	lastSuccessAt  time.Time
	lastTimeoutAt  time.Time
	transportModes map[string]int
}

type adaptiveProfileStore struct {
	mu      sync.Mutex
	entries map[string]*adaptiveProfileEntry
}

var globalAdaptiveProfileHistory = &adaptiveProfileStore{
	entries: make(map[string]*adaptiveProfileEntry),
}

type P2PStageTelemetrySnapshot struct {
	FirstAtMs int64  `json:"first_at_ms,omitempty"`
	LastAtMs  int64  `json:"last_at_ms,omitempty"`
	Count     int    `json:"count,omitempty"`
	Status    string `json:"status,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Family    string `json:"family,omitempty"`
}

type P2PProbeTelemetrySnapshot struct {
	PublicIP             string `json:"public_ip,omitempty"`
	NATType              string `json:"nat_type,omitempty"`
	MappingBehavior      string `json:"mapping_behavior,omitempty"`
	FilteringBehavior    string `json:"filtering_behavior,omitempty"`
	ClassificationLevel  string `json:"classification_level,omitempty"`
	ProbeEndpointCount   int    `json:"probe_endpoint_count,omitempty"`
	ProbeProviderCount   int    `json:"probe_provider_count,omitempty"`
	ObservedBasePort     int    `json:"observed_base_port,omitempty"`
	ObservedInterval     int    `json:"observed_interval,omitempty"`
	ProbePortRestricted  bool   `json:"probe_port_restricted,omitempty"`
	MappingConfidenceLow bool   `json:"mapping_confidence_low,omitempty"`
	FilteringTested      bool   `json:"filtering_tested,omitempty"`
}

type P2PPortMappingTelemetrySnapshot struct {
	Method       string `json:"method,omitempty"`
	ExternalAddr string `json:"external_addr,omitempty"`
	InternalAddr string `json:"internal_addr,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type P2PRoleTelemetrySnapshot struct {
	LastStage            string                                     `json:"last_stage,omitempty"`
	LastStatus           string                                     `json:"last_status,omitempty"`
	LastDetail           string                                     `json:"last_detail,omitempty"`
	LastLocalAddr        string                                     `json:"last_local_addr,omitempty"`
	LastRemoteAddr       string                                     `json:"last_remote_addr,omitempty"`
	LastFamily           string                                     `json:"last_family,omitempty"`
	TransportMode        string                                     `json:"transport_mode,omitempty"`
	TransportEstablished bool                                       `json:"transport_established,omitempty"`
	Counters             map[string]int                             `json:"counters,omitempty"`
	Meta                 map[string]string                          `json:"meta,omitempty"`
	Stages               map[string]P2PStageTelemetrySnapshot       `json:"stages,omitempty"`
	ProbeFamilies        map[string]P2PProbeTelemetrySnapshot       `json:"probe_families,omitempty"`
	PortMappings         map[string]P2PPortMappingTelemetrySnapshot `json:"port_mappings,omitempty"`
}

type P2PSessionTelemetrySnapshot struct {
	CreatedAtMs  int64                               `json:"created_at_ms,omitempty"`
	SummaryAtMs  int64                               `json:"summary_at_ms,omitempty"`
	GoAtMs       int64                               `json:"go_at_ms,omitempty"`
	OutcomeAtMs  int64                               `json:"outcome_at_ms,omitempty"`
	Outcome      string                              `json:"outcome,omitempty"`
	Reason       string                              `json:"reason,omitempty"`
	SummaryHints *P2PSummaryHints                    `json:"summary_hints,omitempty"`
	Roles        map[string]P2PRoleTelemetrySnapshot `json:"roles,omitempty"`
}

type P2PSessionTelemetryRecord struct {
	SchemaVersion int                         `json:"schema_version"`
	ExportedAtMs  int64                       `json:"exported_at_ms"`
	SessionID     string                      `json:"session_id"`
	Snapshot      P2PSessionTelemetrySnapshot `json:"snapshot"`
}

type PredictionOffsetSnapshot struct {
	Offset      int   `json:"offset"`
	Count       int   `json:"count"`
	UpdatedAtMs int64 `json:"updated_at_ms"`
}

type PredictionHistorySnapshot struct {
	Key         string                     `json:"key"`
	UpdatedAtMs int64                      `json:"updated_at_ms"`
	Offsets     []PredictionOffsetSnapshot `json:"offsets,omitempty"`
}

type AdaptiveProfileSnapshot struct {
	Key             string         `json:"key"`
	UpdatedAtMs     int64          `json:"updated_at_ms"`
	SuccessCount    int            `json:"success_count"`
	TimeoutCount    int            `json:"timeout_count"`
	LastSuccessAtMs int64          `json:"last_success_at_ms,omitempty"`
	LastTimeoutAtMs int64          `json:"last_timeout_at_ms,omitempty"`
	TransportModes  map[string]int `json:"transport_modes,omitempty"`
}

type P2PDiagnosticsSnapshot struct {
	SchemaVersion     int                         `json:"schema_version"`
	ExportedAtMs      int64                       `json:"exported_at_ms"`
	Sessions          []P2PSessionTelemetryRecord `json:"sessions,omitempty"`
	PredictionHistory []PredictionHistorySnapshot `json:"prediction_history,omitempty"`
	AdaptiveProfiles  []AdaptiveProfileSnapshot   `json:"adaptive_profiles,omitempty"`
}

type PredictionHistoryStore interface {
	RecordSuccess(obs NatObservation, remoteAddr string)
	Offsets(obs NatObservation) []int
	Snapshot() []PredictionHistorySnapshot
}

type AdaptiveProfileHistoryStore interface {
	RecordSuccess(summary P2PProbeSummary, transportMode string)
	RecordTimeout(summary P2PProbeSummary)
	Score(summary P2PProbeSummary) int
	Snapshot() []AdaptiveProfileSnapshot
}

type P2PTelemetrySink interface {
	EmitSessionTelemetry(record P2PSessionTelemetryRecord)
}

type predictionHistoryStoreAdapter struct {
	store *predictionHistoryStore
}

type adaptiveProfileHistoryStoreAdapter struct {
	store *adaptiveProfileStore
}

type logP2PTelemetrySink struct{}

type predictionHistoryStoreHolder struct {
	store PredictionHistoryStore
}

type adaptiveProfileHistoryStoreHolder struct {
	store AdaptiveProfileHistoryStore
}

type p2pTelemetrySinkHolder struct {
	sink P2PTelemetrySink
}

var globalPredictionHistoryStoreValue atomic.Value
var globalAdaptiveProfileHistoryStoreValue atomic.Value
var globalP2PTelemetrySinkValue atomic.Value

func init() {
	globalPredictionHistoryStoreValue.Store(predictionHistoryStoreHolder{store: defaultPredictionHistoryStore()})
	globalAdaptiveProfileHistoryStoreValue.Store(adaptiveProfileHistoryStoreHolder{store: defaultAdaptiveProfileHistoryStore()})
	globalP2PTelemetrySinkValue.Store(p2pTelemetrySinkHolder{sink: defaultP2PTelemetrySink()})
}

func defaultPredictionHistoryStore() PredictionHistoryStore {
	return predictionHistoryStoreAdapter{store: globalPredictionHistory}
}

func defaultAdaptiveProfileHistoryStore() AdaptiveProfileHistoryStore {
	return adaptiveProfileHistoryStoreAdapter{store: globalAdaptiveProfileHistory}
}

func defaultP2PTelemetrySink() P2PTelemetrySink {
	return logP2PTelemetrySink{}
}

func currentPredictionHistoryStore() PredictionHistoryStore {
	if holder, ok := globalPredictionHistoryStoreValue.Load().(predictionHistoryStoreHolder); ok && holder.store != nil {
		return holder.store
	}
	return defaultPredictionHistoryStore()
}

func currentAdaptiveProfileHistoryStore() AdaptiveProfileHistoryStore {
	if holder, ok := globalAdaptiveProfileHistoryStoreValue.Load().(adaptiveProfileHistoryStoreHolder); ok && holder.store != nil {
		return holder.store
	}
	return defaultAdaptiveProfileHistoryStore()
}

func currentP2PTelemetrySink() P2PTelemetrySink {
	if holder, ok := globalP2PTelemetrySinkValue.Load().(p2pTelemetrySinkHolder); ok && holder.sink != nil {
		return holder.sink
	}
	return defaultP2PTelemetrySink()
}

func recordPredictionSuccess(obs NatObservation, remoteAddr string) {
	currentPredictionHistoryStore().RecordSuccess(obs, remoteAddr)
}

func predictionHistoryOffsets(obs NatObservation) []int {
	return currentPredictionHistoryStore().Offsets(obs)
}

func predictionHistoryKey(obs NatObservation) string {
	if obs.PublicIP == "" {
		return ""
	}
	return obs.PublicIP + "|" + obs.NATType + "|" + obs.MappingBehavior + "|" + strconv.Itoa(obs.ObservedInterval)
}

func recordAdaptiveProfileSuccess(summary P2PProbeSummary, transportMode string) {
	currentAdaptiveProfileHistoryStore().RecordSuccess(summary, transportMode)
}

func recordAdaptiveProfileTimeout(summary P2PProbeSummary) {
	currentAdaptiveProfileHistoryStore().RecordTimeout(summary)
}

func adaptiveProfileScore(summary P2PProbeSummary) int {
	return currentAdaptiveProfileHistoryStore().Score(summary)
}

func adaptiveProfileKey(summary P2PProbeSummary) string {
	family := inferPeerInfoFamily(summary.Self)
	if family == udpFamilyAny {
		if infos := NormalizePeerFamilyInfos(summary.Self); len(infos) > 0 {
			family = parseUDPAddrFamily(infos[0].Family)
		}
	}
	if family == udpFamilyAny {
		family = inferPeerInfoFamily(summary.Peer)
	}
	if family == udpFamilyAny {
		if infos := NormalizePeerFamilyInfos(summary.Peer); len(infos) > 0 {
			family = parseUDPAddrFamily(infos[0].Family)
		}
	}
	if family == udpFamilyAny {
		return ""
	}
	return strings.Join([]string{
		family.String(),
		adaptiveNatFingerprint(summary.Self.Nat),
		adaptiveNatFingerprint(summary.Peer.Nat),
	}, "||")
}

func adaptiveNatFingerprint(obs NatObservation) string {
	flags := make([]string, 0, 4)
	if obs.ProbePortRestricted {
		flags = append(flags, "probe_port_restricted")
	}
	if obs.MappingConfidenceLow {
		flags = append(flags, "mapping_conf_low")
	}
	if obs.FilteringTested {
		flags = append(flags, "filter_tested")
	}
	if obs.PortMapping != nil && obs.PortMapping.Method != "" {
		flags = append(flags, "portmap_"+obs.PortMapping.Method)
	}
	return strings.Join([]string{
		nonEmptyOr(obs.NATType, NATTypeUnknown),
		nonEmptyOr(obs.MappingBehavior, NATMappingUnknown),
		nonEmptyOr(obs.FilteringBehavior, NATFilteringUnknown),
		nonEmptyOr(obs.ClassificationLevel, ClassificationConfidenceLow),
		"providers=" + strconv.Itoa(natProbeProviderCount(obs)),
		"endpoints=" + strconv.Itoa(bucketAdaptiveInt(obs.ProbeEndpointCount)),
		"interval=" + strconv.Itoa(bucketAdaptiveInterval(obs.ObservedInterval)),
		strings.Join(flags, ","),
	}, "|")
}

func bucketAdaptiveInt(value int) int {
	switch {
	case value <= 0:
		return 0
	case value == 1:
		return 1
	case value <= 3:
		return 3
	default:
		return 4
	}
}

func bucketAdaptiveInterval(value int) int {
	switch {
	case value <= 0:
		return 0
	case value == 1:
		return 1
	case value <= 4:
		return 4
	case value <= 16:
		return 16
	default:
		return 32
	}
}

func nonEmptyOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func SetPredictionHistoryStore(store PredictionHistoryStore) func() {
	previous := currentPredictionHistoryStore()
	if store == nil {
		store = defaultPredictionHistoryStore()
	}
	globalPredictionHistoryStoreValue.Store(predictionHistoryStoreHolder{store: store})
	return func() {
		globalPredictionHistoryStoreValue.Store(predictionHistoryStoreHolder{store: previous})
	}
}

func SetAdaptiveProfileHistoryStore(store AdaptiveProfileHistoryStore) func() {
	previous := currentAdaptiveProfileHistoryStore()
	if store == nil {
		store = defaultAdaptiveProfileHistoryStore()
	}
	globalAdaptiveProfileHistoryStoreValue.Store(adaptiveProfileHistoryStoreHolder{store: store})
	return func() {
		globalAdaptiveProfileHistoryStoreValue.Store(adaptiveProfileHistoryStoreHolder{store: previous})
	}
}

func SetP2PTelemetrySink(sink P2PTelemetrySink) func() {
	previous := currentP2PTelemetrySink()
	if sink == nil {
		sink = defaultP2PTelemetrySink()
	}
	globalP2PTelemetrySinkValue.Store(p2pTelemetrySinkHolder{sink: sink})
	return func() {
		globalP2PTelemetrySinkValue.Store(p2pTelemetrySinkHolder{sink: previous})
	}
}

func SnapshotPredictionHistory() []PredictionHistorySnapshot {
	return currentPredictionHistoryStore().Snapshot()
}

func SnapshotAdaptiveProfileHistory() []AdaptiveProfileSnapshot {
	return currentAdaptiveProfileHistoryStore().Snapshot()
}

func EmitP2PSessionTelemetry(sessionID string, snapshot P2PSessionTelemetrySnapshot) {
	sink := currentP2PTelemetrySink()
	if sink == nil {
		return
	}
	sink.EmitSessionTelemetry(newP2PSessionTelemetryRecordAt(sessionID, snapshot, time.Now()))
}

func SnapshotP2PDiagnostics(sessionRecords []P2PSessionTelemetryRecord) P2PDiagnosticsSnapshot {
	return snapshotP2PDiagnosticsAt(sessionRecords, time.Now())
}

func MarshalP2PSessionTelemetryRecord(record P2PSessionTelemetryRecord) ([]byte, error) {
	return json.Marshal(record)
}

func MarshalP2PDiagnosticsSnapshot(snapshot P2PDiagnosticsSnapshot) ([]byte, error) {
	return json.Marshal(snapshot)
}

func CloneP2PSummaryHints(hints *P2PSummaryHints) *P2PSummaryHints {
	if hints == nil {
		return nil
	}
	out := *hints
	if len(hints.SharedFamilies) > 0 {
		out.SharedFamilies = append([]string(nil), hints.SharedFamilies...)
	}
	if len(hints.SelfFamilyDetails) > 0 {
		out.SelfFamilyDetails = make(map[string]P2PFamilyHintDetail, len(hints.SelfFamilyDetails))
		for key, value := range hints.SelfFamilyDetails {
			out.SelfFamilyDetails[key] = value
		}
	}
	if len(hints.PeerFamilyDetails) > 0 {
		out.PeerFamilyDetails = make(map[string]P2PFamilyHintDetail, len(hints.PeerFamilyDetails))
		for key, value := range hints.PeerFamilyDetails {
			out.PeerFamilyDetails[key] = value
		}
	}
	return &out
}

func CloneP2PSessionTelemetrySnapshot(snapshot P2PSessionTelemetrySnapshot) P2PSessionTelemetrySnapshot {
	out := snapshot
	out.SummaryHints = CloneP2PSummaryHints(snapshot.SummaryHints)
	if len(snapshot.Roles) == 0 {
		out.Roles = nil
		return out
	}
	out.Roles = make(map[string]P2PRoleTelemetrySnapshot, len(snapshot.Roles))
	for role, roleSnapshot := range snapshot.Roles {
		roleCopy := roleSnapshot
		roleCopy.Counters = cloneHistoryIntMap(roleSnapshot.Counters)
		roleCopy.Meta = cloneStringMap(roleSnapshot.Meta)
		roleCopy.ProbeFamilies = cloneP2PProbeTelemetryMap(roleSnapshot.ProbeFamilies)
		roleCopy.PortMappings = cloneP2PPortMappingTelemetryMap(roleSnapshot.PortMappings)
		if len(roleSnapshot.Stages) > 0 {
			roleCopy.Stages = make(map[string]P2PStageTelemetrySnapshot, len(roleSnapshot.Stages))
			for stage, stageSnapshot := range roleSnapshot.Stages {
				roleCopy.Stages[stage] = stageSnapshot
			}
		} else {
			roleCopy.Stages = nil
		}
		out.Roles[role] = roleCopy
	}
	return out
}

func cloneP2PProbeTelemetryMap(values map[string]P2PProbeTelemetrySnapshot) map[string]P2PProbeTelemetrySnapshot {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]P2PProbeTelemetrySnapshot, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneP2PPortMappingTelemetryMap(values map[string]P2PPortMappingTelemetrySnapshot) map[string]P2PPortMappingTelemetrySnapshot {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]P2PPortMappingTelemetrySnapshot, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func newP2PSessionTelemetryRecordAt(sessionID string, snapshot P2PSessionTelemetrySnapshot, at time.Time) P2PSessionTelemetryRecord {
	return P2PSessionTelemetryRecord{
		SchemaVersion: p2pTelemetrySchemaVersion,
		ExportedAtMs:  at.UnixMilli(),
		SessionID:     sessionID,
		Snapshot:      CloneP2PSessionTelemetrySnapshot(snapshot),
	}
}

func snapshotP2PDiagnosticsAt(sessionRecords []P2PSessionTelemetryRecord, at time.Time) P2PDiagnosticsSnapshot {
	sessions := make([]P2PSessionTelemetryRecord, 0, len(sessionRecords))
	for _, record := range sessionRecords {
		sessions = append(sessions, newP2PSessionTelemetryRecordAt(record.SessionID, record.Snapshot, time.UnixMilli(record.ExportedAtMs)))
	}
	return P2PDiagnosticsSnapshot{
		SchemaVersion:     p2pTelemetrySchemaVersion,
		ExportedAtMs:      at.UnixMilli(),
		Sessions:          sessions,
		PredictionHistory: SnapshotPredictionHistory(),
		AdaptiveProfiles:  SnapshotAdaptiveProfileHistory(),
	}
}

func (logP2PTelemetrySink) EmitSessionTelemetry(record P2PSessionTelemetryRecord) {
	raw, err := MarshalP2PSessionTelemetryRecord(record)
	if err != nil {
		logs.Info("[P2P] session=%s telemetry marshal failed: %v", record.SessionID, err)
		return
	}
	logs.Info("[P2P] session=%s telemetry=%s", record.SessionID, raw)
}

func (s predictionHistoryStoreAdapter) RecordSuccess(obs NatObservation, remoteAddr string) {
	basePort := obs.ObservedBasePort
	remotePort := common.GetPortByAddr(remoteAddr)
	if basePort <= 0 || remotePort <= 0 {
		return
	}
	key := predictionHistoryKey(obs)
	if key == "" || s.store == nil {
		return
	}
	s.store.record(key, remotePort-basePort)
}

func (s predictionHistoryStoreAdapter) Offsets(obs NatObservation) []int {
	if s.store == nil {
		return nil
	}
	return s.store.offsets(predictionHistoryKey(obs))
}

func (s predictionHistoryStoreAdapter) Snapshot() []PredictionHistorySnapshot {
	if s.store == nil {
		return nil
	}
	return s.store.snapshot()
}

func (s adaptiveProfileHistoryStoreAdapter) RecordSuccess(summary P2PProbeSummary, transportMode string) {
	key := adaptiveProfileKey(summary)
	if key == "" || s.store == nil {
		return
	}
	s.store.recordSuccess(key, transportMode)
}

func (s adaptiveProfileHistoryStoreAdapter) RecordTimeout(summary P2PProbeSummary) {
	key := adaptiveProfileKey(summary)
	if key == "" || s.store == nil {
		return
	}
	s.store.recordTimeout(key)
}

func (s adaptiveProfileHistoryStoreAdapter) Score(summary P2PProbeSummary) int {
	if s.store == nil {
		return 0
	}
	return s.store.score(adaptiveProfileKey(summary))
}

func (s adaptiveProfileHistoryStoreAdapter) Snapshot() []AdaptiveProfileSnapshot {
	if s.store == nil {
		return nil
	}
	return s.store.snapshot()
}

func (s *predictionHistoryStore) record(key string, offset int) {
	if key == "" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	entry, ok := s.entries[key]
	if !ok {
		entry = &predictionHistoryEntry{offsets: make(map[int]predictionOffsetStat)}
		s.entries[key] = entry
	}
	entry.updatedAt = now
	stat := entry.offsets[offset]
	stat.count++
	stat.updatedAt = now
	entry.offsets[offset] = stat
}

func (s *predictionHistoryStore) offsets(key string) []int {
	if key == "" {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	entry, ok := s.entries[key]
	if !ok || entry == nil || len(entry.offsets) == 0 {
		return nil
	}
	type scoredOffset struct {
		offset int
		score  int
		count  int
		fresh  time.Time
	}
	items := make([]scoredOffset, 0, len(entry.offsets))
	for offset, stat := range entry.offsets {
		items = append(items, scoredOffset{
			offset: offset,
			score:  predictionOffsetScore(stat, now),
			count:  stat.count,
			fresh:  stat.updatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		if !items[i].fresh.Equal(items[j].fresh) {
			return items[i].fresh.After(items[j].fresh)
		}
		if absInt(items[i].offset) != absInt(items[j].offset) {
			return absInt(items[i].offset) < absInt(items[j].offset)
		}
		return items[i].offset < items[j].offset
	})
	limit := predictionHistoryMaxOffsets
	if len(items) < limit {
		limit = len(items)
	}
	out := make([]int, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, items[i].offset)
	}
	return out
}

func (s *predictionHistoryStore) pruneLocked(now time.Time) {
	for key, entry := range s.entries {
		if entry == nil {
			delete(s.entries, key)
			continue
		}
		for offset, stat := range entry.offsets {
			if now.Sub(stat.updatedAt) > predictionHistoryTTL {
				delete(entry.offsets, offset)
			}
		}
		if len(entry.offsets) == 0 || now.Sub(entry.updatedAt) > predictionHistoryTTL {
			delete(s.entries, key)
		}
	}
	if len(s.entries) <= predictionHistoryMaxEntries {
		return
	}
	type entryAge struct {
		key       string
		updatedAt time.Time
	}
	ages := make([]entryAge, 0, len(s.entries))
	for key, entry := range s.entries {
		ages = append(ages, entryAge{key: key, updatedAt: entry.updatedAt})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].updatedAt.Before(ages[j].updatedAt) })
	for len(s.entries) > predictionHistoryMaxEntries && len(ages) > 0 {
		delete(s.entries, ages[0].key)
		ages = ages[1:]
	}
}

func (s *predictionHistoryStore) snapshot() []PredictionHistorySnapshot {
	if s == nil {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	keys := make([]string, 0, len(s.entries))
	for key := range s.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]PredictionHistorySnapshot, 0, len(keys))
	for _, key := range keys {
		entry := s.entries[key]
		if entry == nil {
			continue
		}
		offsets := make([]PredictionOffsetSnapshot, 0, len(entry.offsets))
		for offset, stat := range entry.offsets {
			offsets = append(offsets, PredictionOffsetSnapshot{
				Offset:      offset,
				Count:       stat.count,
				UpdatedAtMs: stat.updatedAt.UnixMilli(),
			})
		}
		sort.SliceStable(offsets, func(i, j int) bool {
			if offsets[i].Count != offsets[j].Count {
				return offsets[i].Count > offsets[j].Count
			}
			if offsets[i].UpdatedAtMs != offsets[j].UpdatedAtMs {
				return offsets[i].UpdatedAtMs > offsets[j].UpdatedAtMs
			}
			return offsets[i].Offset < offsets[j].Offset
		})
		out = append(out, PredictionHistorySnapshot{
			Key:         key,
			UpdatedAtMs: entry.updatedAt.UnixMilli(),
			Offsets:     offsets,
		})
	}
	return out
}

func (s *adaptiveProfileStore) recordSuccess(key, transportMode string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	entry := s.ensureEntryLocked(key, now)
	entry.successCount++
	entry.lastSuccessAt = now
	entry.updatedAt = now
	if transportMode != "" {
		if entry.transportModes == nil {
			entry.transportModes = make(map[string]int)
		}
		entry.transportModes[transportMode]++
	}
}

func (s *adaptiveProfileStore) recordTimeout(key string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	entry := s.ensureEntryLocked(key, now)
	entry.timeoutCount++
	entry.lastTimeoutAt = now
	entry.updatedAt = now
}

func (s *adaptiveProfileStore) score(key string) int {
	if key == "" {
		return 0
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	entry := s.entries[key]
	if entry == nil {
		return 0
	}
	return adaptiveProfileScoreEntry(entry, now)
}

func (s *adaptiveProfileStore) ensureEntryLocked(key string, now time.Time) *adaptiveProfileEntry {
	entry := s.entries[key]
	if entry != nil {
		return entry
	}
	entry = &adaptiveProfileEntry{updatedAt: now, transportModes: make(map[string]int)}
	s.entries[key] = entry
	return entry
}

func (s *adaptiveProfileStore) pruneLocked(now time.Time) {
	for key, entry := range s.entries {
		if entry == nil || now.Sub(entry.updatedAt) > adaptiveProfileHistoryTTL {
			delete(s.entries, key)
		}
	}
	if len(s.entries) <= adaptiveProfileMaxEntries {
		return
	}
	type entryAge struct {
		key       string
		updatedAt time.Time
	}
	ages := make([]entryAge, 0, len(s.entries))
	for key, entry := range s.entries {
		ages = append(ages, entryAge{key: key, updatedAt: entry.updatedAt})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].updatedAt.Before(ages[j].updatedAt) })
	for len(s.entries) > adaptiveProfileMaxEntries && len(ages) > 0 {
		delete(s.entries, ages[0].key)
		ages = ages[1:]
	}
}

func (s *adaptiveProfileStore) snapshot() []AdaptiveProfileSnapshot {
	if s == nil {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	keys := make([]string, 0, len(s.entries))
	for key := range s.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]AdaptiveProfileSnapshot, 0, len(keys))
	for _, key := range keys {
		entry := s.entries[key]
		if entry == nil {
			continue
		}
		out = append(out, AdaptiveProfileSnapshot{
			Key:             key,
			UpdatedAtMs:     entry.updatedAt.UnixMilli(),
			SuccessCount:    entry.successCount,
			TimeoutCount:    entry.timeoutCount,
			LastSuccessAtMs: entry.lastSuccessAt.UnixMilli(),
			LastTimeoutAtMs: entry.lastTimeoutAt.UnixMilli(),
			TransportModes:  cloneHistoryIntMap(entry.transportModes),
		})
	}
	return out
}

func adaptiveProfileScoreEntry(entry *adaptiveProfileEntry, now time.Time) int {
	if entry == nil {
		return 0
	}
	score := entry.successCount*24 - entry.timeoutCount*16
	switch {
	case !entry.lastSuccessAt.IsZero() && now.Sub(entry.lastSuccessAt) <= 10*time.Minute:
		score += 10
	case !entry.lastSuccessAt.IsZero() && now.Sub(entry.lastSuccessAt) <= adaptiveProfileHistoryTTL/2:
		score += 4
	}
	switch {
	case !entry.lastTimeoutAt.IsZero() && now.Sub(entry.lastTimeoutAt) <= 10*time.Minute:
		score -= 6
	case !entry.lastTimeoutAt.IsZero() && now.Sub(entry.lastTimeoutAt) <= adaptiveProfileHistoryTTL/2:
		score -= 2
	}
	return score
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func predictionOffsetScore(stat predictionOffsetStat, now time.Time) int {
	score := stat.count * 16
	age := now.Sub(stat.updatedAt)
	switch {
	case age <= 2*time.Minute:
		score += 8
	case age <= 10*time.Minute:
		score += 4
	case age <= predictionHistoryTTL/2:
		score += 2
	}
	return score
}

func cloneHistoryIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]int, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
