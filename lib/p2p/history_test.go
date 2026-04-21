package p2p

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/common"
)

func TestReplayWindowRejectsExpiredFutureAndDuplicatePackets(t *testing.T) {
	window := NewReplayWindow(100 * time.Millisecond)
	now := time.Now().UnixMilli()
	if window.Accept(now-500, "expired") {
		t.Fatal("expired packet should be rejected")
	}
	if window.Accept(now+500, "future") {
		t.Fatal("future packet should be rejected")
	}
	if !window.Accept(now, "ok") {
		t.Fatal("fresh packet should be accepted")
	}
	if window.Accept(now, "ok") {
		t.Fatal("duplicate nonce should be rejected")
	}
}

func TestReplayWindowCapsObservedEntries(t *testing.T) {
	window := newReplayWindow(5*time.Second, 3)
	now := time.Now().UnixMilli()
	for i := 0; i < 6; i++ {
		if !window.Accept(now+int64(i), strconv.Itoa(i)) {
			t.Fatalf("nonce %d should be accepted", i)
		}
	}
	window.mu.Lock()
	size := len(window.observed)
	_, oldestPresent := window.observed["0"]
	window.mu.Unlock()
	if size != 3 {
		t.Fatalf("replay window size = %d, want capped at 3", size)
	}
	if oldestPresent {
		t.Fatal("oldest replay nonce should be evicted when the window reaches capacity")
	}
}

func TestBuildHistoricalPredictionPortsOrdersByFrequency(t *testing.T) {
	resetPredictionHistoryForTest()
	t.Cleanup(resetPredictionHistoryForTest)

	obs := NatObservation{
		PublicIP:         "1.1.1.1",
		ObservedBasePort: 5000,
		ObservedInterval: 2,
		NATType:          NATTypeSymmetric,
		MappingBehavior:  NATMappingEndpointDependent,
	}
	recordPredictionSuccess(obs, "1.1.1.1:5006")
	recordPredictionSuccess(obs, "1.1.1.1:5006")
	recordPredictionSuccess(obs, "1.1.1.1:4998")
	recordPredictionSuccess(obs, "1.1.1.1:5004")

	ports := buildHistoricalPredictionPorts(obs, 4)
	if len(ports) < 3 {
		t.Fatalf("expected historical prediction ports, got %#v", ports)
	}
	if ports[0] != 5006 {
		t.Fatalf("ports[0] = %d, want 5006", ports[0])
	}
}

func TestPredictionHistoryOffsetsPreferRecentWhenFrequencyMatches(t *testing.T) {
	resetPredictionHistoryForTest()
	t.Cleanup(resetPredictionHistoryForTest)

	key := predictionHistoryKey(NatObservation{
		PublicIP:         "1.1.1.1",
		ObservedBasePort: 5000,
		ObservedInterval: 2,
		NATType:          NATTypeSymmetric,
		MappingBehavior:  NATMappingEndpointDependent,
	})
	now := time.Now()
	globalPredictionHistory.mu.Lock()
	globalPredictionHistory.entries[key] = &predictionHistoryEntry{
		updatedAt: now,
		offsets: map[int]predictionOffsetStat{
			6: {count: 2, updatedAt: now.Add(-20 * time.Minute)},
			8: {count: 2, updatedAt: now.Add(-1 * time.Minute)},
		},
	}
	globalPredictionHistory.mu.Unlock()

	offsets := predictionHistoryOffsets(NatObservation{
		PublicIP:         "1.1.1.1",
		ObservedBasePort: 5000,
		ObservedInterval: 2,
		NATType:          NATTypeSymmetric,
		MappingBehavior:  NATMappingEndpointDependent,
	})
	if len(offsets) < 2 {
		t.Fatalf("expected recent offsets, got %#v", offsets)
	}
	if offsets[0] != 8 {
		t.Fatalf("offsets[0] = %d, want 8", offsets[0])
	}
}

func TestBuildPredictedPortsAddsHistoryNeighborsBeforeWideSweep(t *testing.T) {
	resetPredictionHistoryForTest()
	t.Cleanup(resetPredictionHistoryForTest)

	obs := NatObservation{
		PublicIP:         "1.1.1.1",
		ObservedBasePort: 5000,
		ObservedInterval: 2,
		NATType:          NATTypeSymmetric,
		MappingBehavior:  NATMappingEndpointDependent,
	}
	recordPredictionSuccess(obs, "1.1.1.1:5006")
	recordPredictionSuccess(obs, "1.1.1.1:5006")

	ports := BuildPredictedPorts(obs, []int{2}, 6)
	if len(ports) < 4 {
		t.Fatalf("expected predicted ports, got %#v", ports)
	}
	if ports[0] != 5006 {
		t.Fatalf("ports[0] = %d, want 5006", ports[0])
	}
	if ports[1] != 5005 || ports[2] != 5007 {
		t.Fatalf("neighbor ports should follow exact history hit, got %#v", ports)
	}
}

type stubPredictionHistoryStore struct {
	recordedObs    NatObservation
	recordedRemote string
	offsets        []int
	snapshots      []PredictionHistorySnapshot
}

func (s *stubPredictionHistoryStore) RecordSuccess(obs NatObservation, remoteAddr string) {
	s.recordedObs = obs
	s.recordedRemote = remoteAddr
}

func (s *stubPredictionHistoryStore) Offsets(obs NatObservation) []int {
	s.recordedObs = obs
	return append([]int(nil), s.offsets...)
}

func (s *stubPredictionHistoryStore) Snapshot() []PredictionHistorySnapshot {
	return append([]PredictionHistorySnapshot(nil), s.snapshots...)
}

type stubAdaptiveProfileHistoryStore struct {
	recordedSummary P2PProbeSummary
	recordedMode    string
	timeoutSummary  P2PProbeSummary
	scoreValue      int
	snapshots       []AdaptiveProfileSnapshot
}

type stubP2PTelemetrySink struct {
	records []P2PSessionTelemetryRecord
}

func (s *stubAdaptiveProfileHistoryStore) RecordSuccess(summary P2PProbeSummary, transportMode string) {
	s.recordedSummary = summary
	s.recordedMode = transportMode
}

func (s *stubAdaptiveProfileHistoryStore) RecordTimeout(summary P2PProbeSummary) {
	s.timeoutSummary = summary
}

func (s *stubAdaptiveProfileHistoryStore) Score(summary P2PProbeSummary) int {
	s.recordedSummary = summary
	return s.scoreValue
}

func (s *stubAdaptiveProfileHistoryStore) Snapshot() []AdaptiveProfileSnapshot {
	return append([]AdaptiveProfileSnapshot(nil), s.snapshots...)
}

func (s *stubP2PTelemetrySink) EmitSessionTelemetry(record P2PSessionTelemetryRecord) {
	s.records = append(s.records, record)
}

func TestPredictionHistoryStoreCanBeReplaced(t *testing.T) {
	stub := &stubPredictionHistoryStore{
		offsets:   []int{8, 6},
		snapshots: []PredictionHistorySnapshot{{Key: "custom"}},
	}
	restore := SetPredictionHistoryStore(stub)
	t.Cleanup(restore)

	obs := NatObservation{PublicIP: "1.1.1.1", ObservedBasePort: 5000}
	recordPredictionSuccess(obs, "1.1.1.1:5008")
	if stub.recordedRemote != "1.1.1.1:5008" {
		t.Fatalf("recordedRemote = %q, want 1.1.1.1:5008", stub.recordedRemote)
	}
	offsets := predictionHistoryOffsets(obs)
	if !reflect.DeepEqual(offsets, []int{8, 6}) {
		t.Fatalf("predictionHistoryOffsets() = %#v, want [8 6]", offsets)
	}
	snapshot := SnapshotPredictionHistory()
	if len(snapshot) != 1 || snapshot[0].Key != "custom" {
		t.Fatalf("SnapshotPredictionHistory() = %#v", snapshot)
	}
}

func TestAdaptiveProfileHistoryStoreCanBeReplaced(t *testing.T) {
	stub := &stubAdaptiveProfileHistoryStore{
		scoreValue: 7,
		snapshots:  []AdaptiveProfileSnapshot{{Key: "adaptive"}},
	}
	restore := SetAdaptiveProfileHistoryStore(stub)
	t.Cleanup(restore)

	summary := P2PProbeSummary{SessionID: "session-adaptive"}
	recordAdaptiveProfileSuccess(summary, common.CONN_KCP)
	if stub.recordedMode != common.CONN_KCP {
		t.Fatalf("recordedMode = %q, want %q", stub.recordedMode, common.CONN_KCP)
	}
	recordAdaptiveProfileTimeout(summary)
	if stub.timeoutSummary.SessionID != "session-adaptive" {
		t.Fatalf("timeoutSummary = %#v", stub.timeoutSummary)
	}
	if score := adaptiveProfileScore(summary); score != 7 {
		t.Fatalf("adaptiveProfileScore() = %d, want 7", score)
	}
	snapshot := SnapshotAdaptiveProfileHistory()
	if len(snapshot) != 1 || snapshot[0].Key != "adaptive" {
		t.Fatalf("SnapshotAdaptiveProfileHistory() = %#v", snapshot)
	}
}

func TestP2PTelemetrySinkCanBeReplaced(t *testing.T) {
	sink := &stubP2PTelemetrySink{}
	restore := SetP2PTelemetrySink(sink)
	t.Cleanup(restore)

	snapshot := P2PSessionTelemetrySnapshot{
		Outcome: "transport_established",
		Roles: map[string]P2PRoleTelemetrySnapshot{
			common.WORK_P2P_VISITOR: {
				LastStage: "handover_begin",
				Meta:      map[string]string{"confirmed_remote": "1.2.3.4:9000"},
				ProbeFamilies: map[string]P2PProbeTelemetrySnapshot{
					"udp4": {PublicIP: "198.51.100.10", NATType: NATTypeSymmetric},
				},
				PortMappings: map[string]P2PPortMappingTelemetrySnapshot{
					"udp4": {Method: "pcp", ExternalAddr: "198.51.100.10:45000"},
				},
			},
		},
	}
	EmitP2PSessionTelemetry("session-telemetry", snapshot)
	snapshot.Roles[common.WORK_P2P_VISITOR] = P2PRoleTelemetrySnapshot{}

	if len(sink.records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(sink.records))
	}
	if sink.records[0].SessionID != "session-telemetry" {
		t.Fatalf("session id = %q, want session-telemetry", sink.records[0].SessionID)
	}
	if sink.records[0].SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", sink.records[0].SchemaVersion)
	}
	if sink.records[0].Snapshot.Outcome != "transport_established" {
		t.Fatalf("snapshot outcome = %q, want transport_established", sink.records[0].Snapshot.Outcome)
	}
	if sink.records[0].ExportedAtMs == 0 {
		t.Fatal("exported_at_ms should be populated")
	}
	if sink.records[0].Snapshot.Roles[common.WORK_P2P_VISITOR].Meta["confirmed_remote"] != "1.2.3.4:9000" {
		t.Fatalf("sink record should keep deep-copied snapshot, got %#v", sink.records[0].Snapshot.Roles)
	}
	if sink.records[0].Snapshot.Roles[common.WORK_P2P_VISITOR].ProbeFamilies["udp4"].PublicIP != "198.51.100.10" {
		t.Fatalf("probe families should be deep copied, got %#v", sink.records[0].Snapshot.Roles[common.WORK_P2P_VISITOR].ProbeFamilies)
	}
	if sink.records[0].Snapshot.Roles[common.WORK_P2P_VISITOR].PortMappings["udp4"].Method != "pcp" {
		t.Fatalf("port mappings should be deep copied, got %#v", sink.records[0].Snapshot.Roles[common.WORK_P2P_VISITOR].PortMappings)
	}
}

func TestSnapshotP2PDiagnosticsBuildsUnifiedExport(t *testing.T) {
	predictionStore := &stubPredictionHistoryStore{
		snapshots: []PredictionHistorySnapshot{{Key: "pred"}},
	}
	adaptiveStore := &stubAdaptiveProfileHistoryStore{
		snapshots: []AdaptiveProfileSnapshot{{Key: "adaptive"}},
	}
	restorePrediction := SetPredictionHistoryStore(predictionStore)
	t.Cleanup(restorePrediction)
	restoreAdaptive := SetAdaptiveProfileHistoryStore(adaptiveStore)
	t.Cleanup(restoreAdaptive)

	diag := snapshotP2PDiagnosticsAt([]P2PSessionTelemetryRecord{
		{
			SessionID:    "session-export",
			ExportedAtMs: 456,
			Snapshot: P2PSessionTelemetrySnapshot{
				Outcome: "aborted",
			},
		},
	}, time.UnixMilli(789))

	if diag.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", diag.SchemaVersion)
	}
	if diag.ExportedAtMs != 789 {
		t.Fatalf("exported_at_ms = %d, want 789", diag.ExportedAtMs)
	}
	if len(diag.Sessions) != 1 || diag.Sessions[0].SessionID != "session-export" {
		t.Fatalf("sessions = %#v", diag.Sessions)
	}
	if diag.Sessions[0].ExportedAtMs != 456 {
		t.Fatalf("session exported_at_ms = %d, want 456", diag.Sessions[0].ExportedAtMs)
	}
	if len(diag.PredictionHistory) != 1 || diag.PredictionHistory[0].Key != "pred" {
		t.Fatalf("prediction history = %#v", diag.PredictionHistory)
	}
	if len(diag.AdaptiveProfiles) != 1 || diag.AdaptiveProfiles[0].Key != "adaptive" {
		t.Fatalf("adaptive profiles = %#v", diag.AdaptiveProfiles)
	}
	raw, err := MarshalP2PDiagnosticsSnapshot(diag)
	if err != nil {
		t.Fatalf("MarshalP2PDiagnosticsSnapshot() error = %v", err)
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if roundTrip["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %#v, want 1", roundTrip["schema_version"])
	}
}

func TestBuildPredictedPortsPrefersLikelyNextAfterSequentialSamples(t *testing.T) {
	obs := NatObservation{
		PublicIP:         "1.1.1.1",
		ObservedBasePort: 5000,
		ObservedInterval: 2,
		NATType:          NATTypeSymmetric,
		MappingBehavior:  NATMappingEndpointDependent,
		Samples: []ProbeSample{
			{ObservedAddr: "1.1.1.1:5000"},
			{ObservedAddr: "1.1.1.1:5002"},
			{ObservedAddr: "1.1.1.1:5004"},
		},
	}

	ports := BuildPredictedPorts(obs, []int{2}, 6)
	if len(ports) == 0 {
		t.Fatalf("expected predicted ports, got %#v", ports)
	}
	if ports[0] != 5006 {
		t.Fatalf("ports[0] = %d, want likely next 5006", ports[0])
	}
}
