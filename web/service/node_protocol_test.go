package service

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func TestBuildNodeDescriptorUsesRuntimeConfigAndHelloBody(t *testing.T) {
	cfg := testNodeControlConfig()
	descriptor := BuildNodeDescriptor(NodeDescriptorInput{
		NodeID:                     "node-a",
		Config:                     cfg,
		BootID:                     "boot-1",
		RuntimeStartedAt:           123,
		ChangesCursor:              77,
		ChangesOldestCursor:        41,
		ChangesHistoryOldestCursor: 11,
		LiveOnlyEvents:             []string{"node.traffic.report"},
	})

	if descriptor.NodeID != "node-a" || descriptor.BootID != "boot-1" || descriptor.RuntimeStartedAt != 123 {
		t.Fatalf("descriptor identity = %+v, want node-a/boot-1/123", descriptor)
	}
	if !descriptor.ResyncOnBootChange || !descriptor.EventsEnabled || !descriptor.CallbacksReady {
		t.Fatalf("descriptor flags = %+v, want resync/events/callbacks enabled", descriptor)
	}
	if descriptor.SchemaVersion != NodeSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", descriptor.SchemaVersion, NodeSchemaVersion)
	}
	if descriptor.NodeAPIBase != "/base/api" {
		t.Fatalf("NodeAPIBase = %q, want /base/api", descriptor.NodeAPIBase)
	}
	if descriptor.Protocol.ChangesWindow != cfg.Runtime.NodeEventLogSizeValue() ||
		!descriptor.Protocol.ChangesDurable ||
		descriptor.Protocol.ChangesHistoryWindow != nodeChangesHistoryWindow(cfg.Runtime.NodeEventLogSizeValue()) ||
		descriptor.Protocol.BatchMaxItems != cfg.Runtime.NodeBatchMaxItemsValue() ||
		descriptor.Protocol.IdempotencyTTLSeconds != cfg.Runtime.NodeIdempotencyTTLSeconds() {
		t.Fatalf("descriptor protocol = %+v, want changes=%d durable=%t history=%d batch=%d ttl=%d",
			descriptor.Protocol,
			cfg.Runtime.NodeEventLogSizeValue(),
			true,
			nodeChangesHistoryWindow(cfg.Runtime.NodeEventLogSizeValue()),
			cfg.Runtime.NodeBatchMaxItemsValue(),
			cfg.Runtime.NodeIdempotencyTTLSeconds(),
		)
	}
	if !reflect.DeepEqual(descriptor.Protocol.LiveOnlyEvents, []string{"node.traffic.report"}) {
		t.Fatalf("LiveOnlyEvents = %#v, want [node.traffic.report]", descriptor.Protocol.LiveOnlyEvents)
	}

	body := descriptor.HelloBody(map[string]interface{}{"connect_mode": "reverse"})
	if body["boot_id"] != "boot-1" || body["runtime_started_at"] != int64(123) {
		t.Fatalf("hello body identity = %#v", body)
	}
	if body["changes_cursor"] != int64(77) ||
		body["changes_oldest_cursor"] != int64(41) ||
		body["changes_window"] != cfg.Runtime.NodeEventLogSizeValue() ||
		body["changes_durable"] != true ||
		body["changes_history_window"] != nodeChangesHistoryWindow(cfg.Runtime.NodeEventLogSizeValue()) ||
		body["changes_history_oldest_cursor"] != int64(11) {
		t.Fatalf("hello body change state = %#v", body)
	}
	if body["batch_max_items"] != cfg.Runtime.NodeBatchMaxItemsValue() ||
		body["idempotency_ttl_seconds"] != int64(cfg.Runtime.NodeIdempotencyTTLSeconds()) {
		t.Fatalf("hello body protocol = %#v", body)
	}
	if got, ok := body["live_only_events"].([]string); !ok || !reflect.DeepEqual(got, []string{"node.traffic.report"}) {
		t.Fatalf("hello body live_only_events = %#v", body["live_only_events"])
	}
	if body["connect_mode"] != "reverse" {
		t.Fatalf("hello body extra fields = %#v, want connect_mode=reverse", body)
	}
}

func TestBuildNodeDescriptorCallbacksReadyRequiresCallbackCapability(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{
					PlatformID:     "reverse-only",
					Token:          "token-reverse",
					Enabled:        true,
					ConnectMode:    "reverse",
					ReverseEnabled: true,
					ReverseWSURL:   "wss://reverse.example/ws",
				},
				{
					PlatformID:      "disabled-callback",
					Token:           "token-disabled",
					Enabled:         false,
					CallbackEnabled: true,
					CallbackURL:     "https://disabled.example/callback",
				},
			},
		},
	}

	descriptor := BuildNodeDescriptor(NodeDescriptorInput{
		NodeID: "node-a",
		Config: cfg,
	})

	if descriptor.CallbacksReady {
		t.Fatalf("CallbacksReady = true, want false when callback capability is absent: %+v", descriptor)
	}
	if !containsString(descriptor.Capabilities, "node.api.ws_reverse") {
		t.Fatalf("Capabilities = %v, want reverse capability retained", descriptor.Capabilities)
	}
	if containsString(descriptor.Capabilities, "node.api.callbacks") {
		t.Fatalf("Capabilities = %v, want callback capability omitted for invalid callback platform", descriptor.Capabilities)
	}
}

func TestBuildNodePlatformStatusPayloadsFiltersSortsAndResolvesServiceUsername(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{
					PlatformID:       "master-b",
					Token:            "token-b",
					Enabled:          true,
					ControlScope:     "account",
					ServiceUsername:  "svc-b",
					MasterURL:        "https://master-b.example",
					ConnectMode:      "direct",
					CallbackEnabled:  true,
					CallbackURL:      "https://master-b.example/callback",
					CallbackQueueMax: 8,
				},
				{
					PlatformID:       "master-a",
					Token:            "token-a",
					Enabled:          true,
					ControlScope:     "full",
					ServiceUsername:  "svc-a",
					MasterURL:        "https://master-a.example",
					ConnectMode:      "reverse",
					ReverseEnabled:   true,
					ReverseWSURL:     "wss://master-a.example/node/ws",
					CallbackEnabled:  true,
					CallbackURL:      "https://master-a.example/callback",
					CallbackQueueMax: 4,
				},
			},
		},
	}
	statuses := BuildNodePlatformStatusPayloads(
		cfg,
		func(platformID string) ManagementPlatformReverseRuntimeStatus {
			if platformID == "master-a" {
				return ManagementPlatformReverseRuntimeStatus{ReverseConnected: true, CallbackQueueSize: 3}
			}
			return ManagementPlatformReverseRuntimeStatus{CallbackQueueSize: 1}
		},
		func(platformID, configured string) string {
			return "resolved-" + platformID
		},
		func(platformID string) bool {
			return platformID != "master-b"
		},
	)

	if len(statuses) != 1 {
		t.Fatalf("statuses len = %d, want 1 (%+v)", len(statuses), statuses)
	}
	if statuses[0].PlatformID != "master-a" || statuses[0].ServiceUsername != "resolved-master-a" {
		t.Fatalf("statuses[0] = %+v, want resolved master-a", statuses[0])
	}
	if !statuses[0].ReverseConnected || statuses[0].CallbackQueueSize != 3 {
		t.Fatalf("statuses[0] runtime = %+v, want reverse connected and queue size 3", statuses[0])
	}

	allStatuses := BuildNodePlatformStatusPayloads(cfg, nil, nil, nil)
	if len(allStatuses) != 2 || allStatuses[0].PlatformID != "master-a" || allStatuses[1].PlatformID != "master-b" {
		t.Fatalf("all statuses order = %+v, want sorted [master-a master-b]", allStatuses)
	}
	if allStatuses[0].ServiceUsername != "svc-a" || allStatuses[1].ServiceUsername != "svc-b" {
		t.Fatalf("default service usernames = %+v, want configured usernames", allStatuses)
	}
}

func TestBuildNodePlatformStatusPayloadsSkipsInvalidManagementPlatformEntries(t *testing.T) {
	cfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{PlatformID: "missing-token", Enabled: true, Token: "   ", ControlScope: "full"},
				{PlatformID: " ", Enabled: true, Token: "token-empty-id", ControlScope: "account"},
				{PlatformID: "disabled", Enabled: false, Token: "token-disabled", ControlScope: "account"},
				{PlatformID: "valid-b", Enabled: true, Token: "token-b", ControlScope: "account"},
				{PlatformID: "valid-a", Enabled: true, Token: "token-a", ControlScope: "full"},
			},
		},
	}

	statuses := BuildNodePlatformStatusPayloads(cfg, nil, nil, nil)
	if len(statuses) != 2 {
		t.Fatalf("statuses len = %d, want 2 (%+v)", len(statuses), statuses)
	}
	if statuses[0].PlatformID != "valid-a" || statuses[1].PlatformID != "valid-b" {
		t.Fatalf("statuses order = %+v, want [valid-a valid-b]", statuses)
	}
}

func TestDecodeNodeBatchRequestSupportsObjectAndArrayPayloads(t *testing.T) {
	objectReq, err := DecodeNodeBatchRequest(2, []byte(`{"operation_id":"op-1","items":[{"id":"status-1","method":"GET","path":"/api/system/status"}]}`))
	if err != nil {
		t.Fatalf("DecodeNodeBatchRequest(object) error = %v", err)
	}
	if objectReq.OperationID != "op-1" || len(objectReq.Items) != 1 || objectReq.Items[0].ID != "status-1" {
		t.Fatalf("DecodeNodeBatchRequest(object) = %+v", objectReq)
	}

	arrayReq, err := DecodeNodeBatchRequest(2, []byte(`[{"id":"status-1","method":"GET","path":"/api/system/status"}]`))
	if err != nil {
		t.Fatalf("DecodeNodeBatchRequest(array) error = %v", err)
	}
	if arrayReq.OperationID != "" || len(arrayReq.Items) != 1 || arrayReq.Items[0].Path != "/api/system/status" {
		t.Fatalf("DecodeNodeBatchRequest(array) = %+v", arrayReq)
	}
}

func TestDecodeNodeBatchRequestRejectsEmptyAndLimitOverflow(t *testing.T) {
	if _, err := DecodeNodeBatchRequest(1, nil); err == nil || err.Error() != "batch items are empty" {
		t.Fatalf("DecodeNodeBatchRequest(empty) error = %v, want batch items are empty", err)
	}
	if _, err := DecodeNodeBatchRequest(1, []byte(`{"items":[{"path":"/a"},{"path":"/b"}]}`)); err == nil || err.Error() != "batch items exceed limit" {
		t.Fatalf("DecodeNodeBatchRequest(limit) error = %v, want batch items exceed limit", err)
	}
}

func TestExecuteNodeBatchAddsDefaultsAndSummarizesResults(t *testing.T) {
	calls := 0
	payload := ExecuteNodeBatch(NodeBatchExecuteInput{
		Request: NodeBatchRequest{
			Items: []NodeBatchRequestItem{
				{Path: "/api/system/status"},
				{ID: "nested", Path: "/api/batch"},
				{ID: "sync", Path: "/api/system/actions/sync"},
			},
		},
		DefaultOperationID: "req-123",
		DefaultItemID: func(index int) string {
			return "batch-item-" + string(rune('1'+index))
		},
		AllowPath: func(path string) bool {
			return path != "/api/batch"
		},
		Dispatch: func(item NodeBatchDispatchItem) NodeBatchResponseItem {
			calls++
			if item.OperationID != "req-123" {
				t.Fatalf("OperationID = %q, want req-123", item.OperationID)
			}
			if item.Item.ID == "" {
				t.Fatal("dispatch item should have default ID assigned")
			}
			status := http.StatusOK
			if item.Item.Path == "/api/system/actions/sync" {
				status = http.StatusInternalServerError
			}
			body, _ := json.Marshal(map[string]string{"path": item.Item.Path})
			return NodeBatchResponseItem{
				ID:      item.Item.ID,
				Status:  status,
				Body:    body,
				Headers: map[string]string{"X-Request-ID": item.Item.ID},
			}
		},
		RejectedHeaders: map[string]string{"X-Request-ID": "req-123"},
	})

	if payload.OperationID != "req-123" || payload.Count != 3 || payload.SuccessCount != 1 || payload.ErrorCount != 2 {
		t.Fatalf("ExecuteNodeBatch() payload = %+v, want op=req-123 count=3 success=1 error=2", payload)
	}
	if calls != 2 {
		t.Fatalf("dispatch calls = %d, want 2", calls)
	}
	if payload.Items[0].ID == "" || payload.Items[0].Status != http.StatusOK {
		t.Fatalf("first item = %+v, want default id and 200", payload.Items[0])
	}
	if payload.Items[1].ID != "nested" || payload.Items[1].Status != http.StatusBadRequest || payload.Items[1].Error == "" {
		t.Fatalf("second item = %+v, want rejected nested batch item", payload.Items[1])
	}
	if payload.Items[2].ID != "sync" || payload.Items[2].Status != http.StatusInternalServerError {
		t.Fatalf("third item = %+v, want sync failure", payload.Items[2])
	}
	if !reflect.DeepEqual(payload.Items[1].Headers, map[string]string{"X-Request-ID": "req-123"}) {
		t.Fatalf("rejected headers = %#v, want X-Request-ID passthrough", payload.Items[1].Headers)
	}
}

func TestExecuteNodeBatchDetachesDispatchResponseBody(t *testing.T) {
	body := json.RawMessage(`{"ok":true}`)
	payload := ExecuteNodeBatch(NodeBatchExecuteInput{
		Request: NodeBatchRequest{
			Items: []NodeBatchRequestItem{{ID: "item-1", Path: "/api/system/status"}},
		},
		Dispatch: func(item NodeBatchDispatchItem) NodeBatchResponseItem {
			return NodeBatchResponseItem{
				ID:     item.Item.ID,
				Status: http.StatusOK,
				Body:   body,
			}
		},
	})

	body[0] = '['
	if got := string(payload.Items[0].Body); got != `{"ok":true}` {
		t.Fatalf("payload body = %s, want detached response body", got)
	}
}
