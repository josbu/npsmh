package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/djylb/nps/lib/servercfg"
)

const NodeBatchDefaultMaxItems = 50
const NodeSchemaVersion = 1

type NodeBatchRequest struct {
	OperationID string                 `json:"operation_id,omitempty"`
	Items       []NodeBatchRequestItem `json:"items"`
}

type NodeBatchRequestItem struct {
	ID      string            `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

type NodeBatchResponsePayload struct {
	OperationID  string                  `json:"operation_id,omitempty"`
	Count        int                     `json:"count"`
	SuccessCount int                     `json:"success_count"`
	ErrorCount   int                     `json:"error_count"`
	Items        []NodeBatchResponseItem `json:"items"`
}

type NodeBatchResponseItem struct {
	ID        string            `json:"id,omitempty"`
	Status    int               `json:"status,omitempty"`
	Error     string            `json:"error,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
	Encoding  string            `json:"encoding,omitempty"`
	Timestamp int64             `json:"timestamp,omitempty"`
}

type NodeBatchDispatchItem struct {
	OperationID string
	Index       int
	Item        NodeBatchRequestItem
}

type NodeBatchExecuteInput struct {
	Request              NodeBatchRequest
	DefaultOperationID   string
	DefaultItemID        func(int) string
	AllowPath            func(string) bool
	Dispatch             func(NodeBatchDispatchItem) NodeBatchResponseItem
	RejectedHeaders      map[string]string
	RejectedStatus       int
	RejectedErrorMessage string
	RejectedTimestamp    func() int64
}

func DecodeNodeBatchRequest(limit int, raw []byte) (NodeBatchRequest, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return NodeBatchRequest{}, errors.New("batch items are empty")
	}
	request := NodeBatchRequest{}
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := json.Unmarshal(trimmed, &request.Items); err != nil {
			return NodeBatchRequest{}, errors.New("invalid batch items")
		}
	} else {
		if err := json.Unmarshal(trimmed, &request); err != nil {
			return NodeBatchRequest{}, errors.New("invalid batch request")
		}
	}
	request.OperationID = strings.TrimSpace(request.OperationID)
	for index := range request.Items {
		request.Items[index] = normalizeNodeBatchRequestItem(request.Items[index])
	}
	if len(request.Items) == 0 {
		return NodeBatchRequest{}, errors.New("batch items are empty")
	}
	if limit <= 0 {
		limit = NodeBatchDefaultMaxItems
	}
	if len(request.Items) > limit {
		return NodeBatchRequest{}, errors.New("batch items exceed limit")
	}
	return request, nil
}

func ExecuteNodeBatch(input NodeBatchExecuteInput) NodeBatchResponsePayload {
	request := input.Request
	request.OperationID = strings.TrimSpace(request.OperationID)
	if request.OperationID == "" {
		request.OperationID = strings.TrimSpace(input.DefaultOperationID)
	}
	items := make([]NodeBatchRequestItem, 0, len(request.Items))
	for index, item := range request.Items {
		item = normalizeNodeBatchRequestItem(item)
		if item.ID == "" && input.DefaultItemID != nil {
			item.ID = strings.TrimSpace(input.DefaultItemID(index))
		}
		items = append(items, item)
	}
	payload := NodeBatchResponsePayload{
		OperationID: request.OperationID,
		Count:       len(items),
		Items:       make([]NodeBatchResponseItem, 0, len(items)),
	}
	for index, item := range items {
		var result NodeBatchResponseItem
		switch {
		case input.AllowPath != nil && !input.AllowPath(item.Path):
			result = NodeBatchResponseItem{
				ID:      item.ID,
				Status:  input.rejectedStatus(),
				Error:   defaultString(input.RejectedErrorMessage, "nested batch or websocket dispatch is not supported"),
				Headers: cloneStringMap(input.RejectedHeaders),
			}
			if input.RejectedTimestamp != nil {
				result.Timestamp = input.RejectedTimestamp()
			}
		case input.Dispatch != nil:
			result = input.Dispatch(NodeBatchDispatchItem{
				OperationID: request.OperationID,
				Index:       index,
				Item:        item,
			})
		default:
			result = NodeBatchResponseItem{ID: item.ID, Status: http.StatusInternalServerError, Error: "batch dispatch is unavailable"}
		}
		if result.ID == "" {
			result.ID = item.ID
		}
		if nodeBatchItemSucceeded(result) {
			payload.SuccessCount++
		} else {
			payload.ErrorCount++
		}
		payload.Items = append(payload.Items, normalizeNodeBatchResponseItem(result))
	}
	return payload
}

func (i NodeBatchExecuteInput) rejectedStatus() int {
	if i.RejectedStatus > 0 {
		return i.RejectedStatus
	}
	return http.StatusBadRequest
}

func nodeBatchItemSucceeded(item NodeBatchResponseItem) bool {
	return item.Error == "" && item.Status >= 200 && item.Status < 400
}

func normalizeNodeBatchRequestItem(item NodeBatchRequestItem) NodeBatchRequestItem {
	item.ID = strings.TrimSpace(item.ID)
	item.Method = strings.TrimSpace(item.Method)
	item.Path = strings.TrimSpace(item.Path)
	item.Headers = cloneStringMap(item.Headers)
	item.Body = append(json.RawMessage(nil), item.Body...)
	return item
}

func normalizeNodeBatchResponseItem(item NodeBatchResponseItem) NodeBatchResponseItem {
	item.ID = strings.TrimSpace(item.ID)
	item.Error = strings.TrimSpace(item.Error)
	item.Headers = cloneStringMap(item.Headers)
	item.Body = append(json.RawMessage(nil), item.Body...)
	item.Encoding = strings.TrimSpace(item.Encoding)
	return item
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

type NodeDescriptorInput struct {
	NodeID                       string
	Config                       *servercfg.Snapshot
	BootID                       string
	RuntimeStartedAt             int64
	ConfigEpoch                  string
	ChangesCursor                int64
	ChangesOldestCursor          int64
	ChangesHistoryOldestCursor   int64
	ChangesWindow                int
	ChangesHistoryWindow         int
	BatchMaxItems                int
	IdempotencyTTLSeconds        int
	TrafficReportIntervalSeconds int
	TrafficReportStepBytes       int64
	LiveOnlyEvents               []string
}

type NodeDescriptor struct {
	NodeID                     string
	BootID                     string
	RuntimeStartedAt           int64
	ConfigEpoch                string
	ResyncOnBootChange         bool
	RunMode                    string
	EventsEnabled              bool
	CallbacksReady             bool
	SchemaVersion              int
	NodeAPIBase                string
	Capabilities               []string
	ChangesCursor              int64
	ChangesOldestCursor        int64
	ChangesHistoryOldestCursor int64
	Protocol                   NodeProtocolPayload
}

func BuildNodeDescriptor(input NodeDescriptorInput) NodeDescriptor {
	cfg := servercfg.Resolve(input.Config)
	capabilities := servercfg.NodeCapabilities(cfg)
	changesWindow := input.ChangesWindow
	if changesWindow <= 0 {
		changesWindow = cfg.Runtime.NodeEventLogSizeValue()
	}
	changesHistoryWindow := input.ChangesHistoryWindow
	if changesHistoryWindow <= 0 && changesWindow > 0 {
		changesHistoryWindow = nodeChangesHistoryWindow(changesWindow)
	}
	batchMaxItems := input.BatchMaxItems
	if batchMaxItems <= 0 {
		batchMaxItems = cfg.Runtime.NodeBatchMaxItemsValue()
	}
	idempotencyTTLSeconds := input.IdempotencyTTLSeconds
	if idempotencyTTLSeconds <= 0 {
		idempotencyTTLSeconds = cfg.Runtime.NodeIdempotencyTTLSeconds()
	}
	trafficReportIntervalSeconds := input.TrafficReportIntervalSeconds
	if trafficReportIntervalSeconds < 0 {
		trafficReportIntervalSeconds = 0
	}
	if trafficReportIntervalSeconds == 0 {
		trafficReportIntervalSeconds = cfg.Runtime.NodeTrafficReportIntervalSeconds()
	}
	trafficReportStepBytes := input.TrafficReportStepBytes
	if trafficReportStepBytes < 0 {
		trafficReportStepBytes = 0
	}
	if trafficReportStepBytes == 0 {
		trafficReportStepBytes = cfg.Runtime.NodeTrafficReportStepBytes()
	}
	return NodeDescriptor{
		NodeID:                     strings.TrimSpace(input.NodeID),
		BootID:                     strings.TrimSpace(input.BootID),
		RuntimeStartedAt:           input.RuntimeStartedAt,
		ConfigEpoch:                strings.TrimSpace(input.ConfigEpoch),
		ResyncOnBootChange:         true,
		RunMode:                    strings.TrimSpace(cfg.Runtime.RunMode),
		EventsEnabled:              true,
		CallbacksReady:             hasNodeCapability(capabilities, "node.api.callbacks"),
		SchemaVersion:              NodeSchemaVersion,
		NodeAPIBase:                joinNodeBase(cfg.Web.BaseURL, "/api"),
		Capabilities:               capabilities,
		ChangesCursor:              input.ChangesCursor,
		ChangesOldestCursor:        input.ChangesOldestCursor,
		ChangesHistoryOldestCursor: input.ChangesHistoryOldestCursor,
		Protocol: NodeProtocolPayload{
			ChangesWindow:                changesWindow,
			ChangesDurable:               changesHistoryWindow > 0,
			ChangesHistoryWindow:         changesHistoryWindow,
			BatchMaxItems:                batchMaxItems,
			IdempotencyTTLSeconds:        idempotencyTTLSeconds,
			TrafficReportIntervalSeconds: trafficReportIntervalSeconds,
			TrafficReportStepBytes:       trafficReportStepBytes,
			LiveOnlyEvents:               append([]string(nil), input.LiveOnlyEvents...),
		},
	}
}

func hasNodeCapability(capabilities []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" || len(capabilities) == 0 {
		return false
	}
	index := sort.SearchStrings(capabilities, target)
	return index < len(capabilities) && capabilities[index] == target
}

func isRuntimeManagementPlatformEnabled(platform servercfg.ManagementPlatformConfig) bool {
	return platform.Enabled && strings.TrimSpace(platform.PlatformID) != "" && strings.TrimSpace(platform.Token) != ""
}

func (d NodeDescriptor) HelloBody(extra map[string]interface{}) map[string]interface{} {
	body := map[string]interface{}{
		"capabilities":          append([]string(nil), d.Capabilities...),
		"boot_id":               d.BootID,
		"runtime_started_at":    d.RuntimeStartedAt,
		"config_epoch":          d.ConfigEpoch,
		"resync_on_boot_change": d.ResyncOnBootChange,
	}
	if d.ChangesCursor > 0 || d.ChangesOldestCursor > 0 || d.Protocol.ChangesWindow > 0 {
		body["changes_cursor"] = d.ChangesCursor
		body["changes_window"] = d.Protocol.ChangesWindow
		if d.ChangesOldestCursor > 0 {
			body["changes_oldest_cursor"] = d.ChangesOldestCursor
		}
	}
	if d.Protocol.ChangesDurable {
		body["changes_durable"] = true
	}
	if d.Protocol.ChangesHistoryWindow > 0 {
		body["changes_history_window"] = d.Protocol.ChangesHistoryWindow
	}
	if d.ChangesHistoryOldestCursor > 0 {
		body["changes_history_oldest_cursor"] = d.ChangesHistoryOldestCursor
	}
	if d.Protocol.BatchMaxItems > 0 {
		body["batch_max_items"] = d.Protocol.BatchMaxItems
	}
	if d.Protocol.IdempotencyTTLSeconds > 0 {
		body["idempotency_ttl_seconds"] = int64(d.Protocol.IdempotencyTTLSeconds)
	}
	if d.Protocol.TrafficReportIntervalSeconds > 0 {
		body["traffic_report_interval_seconds"] = int64(d.Protocol.TrafficReportIntervalSeconds)
	}
	if d.Protocol.TrafficReportStepBytes > 0 {
		body["traffic_report_step_bytes"] = d.Protocol.TrafficReportStepBytes
	}
	if len(d.Protocol.LiveOnlyEvents) > 0 {
		body["live_only_events"] = append([]string(nil), d.Protocol.LiveOnlyEvents...)
	}
	for key, value := range extra {
		body[key] = value
	}
	return body
}

func nodeChangesHistoryWindow(changesWindow int) int {
	retention := changesWindow * 4
	if retention < 4096 {
		retention = 4096
	}
	if retention > 16384 {
		retention = 16384
	}
	if retention < changesWindow {
		retention = changesWindow
	}
	return retention
}

func BuildNodePlatformStatusPayloads(
	cfg *servercfg.Snapshot,
	reverseStatus func(string) ManagementPlatformReverseRuntimeStatus,
	resolveServiceUsername func(string, string) string,
	allow func(string) bool,
) []NodePlatformStatusPayload {
	if cfg == nil {
		return nil
	}
	if reverseStatus == nil {
		reverseStatus = func(string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{}
		}
	}
	if resolveServiceUsername == nil {
		resolveServiceUsername = func(platformID, configured string) string {
			return strings.TrimSpace(configured)
		}
	}
	if allow == nil {
		allow = func(string) bool { return true }
	}
	platforms := make([]NodePlatformStatusPayload, 0, len(cfg.Runtime.ManagementPlatforms))
	for _, platform := range cfg.Runtime.ManagementPlatforms {
		if !isRuntimeManagementPlatformEnabled(platform) {
			continue
		}
		if !allow(platform.PlatformID) {
			continue
		}
		reverse := reverseStatus(platform.PlatformID)
		platforms = append(platforms, NodePlatformStatusPayload{
			PlatformID:              platform.PlatformID,
			MasterURL:               platform.MasterURL,
			ControlScope:            platform.ControlScope,
			ServiceUsername:         resolveServiceUsername(platform.PlatformID, platform.ServiceUsername),
			Enabled:                 platform.Enabled,
			ConnectMode:             platform.ConnectMode,
			DirectEnabled:           platform.SupportsDirect(),
			ReverseEnabled:          platform.ReverseEnabled,
			ReverseHeartbeatSeconds: platform.ReverseHeartbeatSeconds,
			ReverseWSURL:            platform.ReverseWSURL,
			CallbackEnabled:         platform.CallbackEnabled,
			CallbackURL:             platform.CallbackURL,
			CallbackTimeoutSeconds:  platform.CallbackTimeoutSeconds,
			CallbackRetryMax:        platform.CallbackRetryMax,
			CallbackRetryBackoffSec: platform.CallbackRetryBackoffSec,
			CallbackQueueMax:        platform.CallbackQueueMax,
			CallbackQueueSize:       reverse.CallbackQueueSize,
			CallbackDropped:         reverse.CallbackDropped,
			CallbackSigningEnabled:  strings.TrimSpace(platform.CallbackSigningKey) != "",
			ReverseConnected:        reverse.ReverseConnected,
			LastReverseConnectedAt:  reverse.LastConnectedAt,
			LastReverseHelloAt:      reverse.LastHelloAt,
			LastReverseEventAt:      reverse.LastEventAt,
			LastReversePingAt:       reverse.LastPingAt,
			LastReversePongAt:       reverse.LastPongAt,
			LastCallbackAt:          reverse.LastCallbackAt,
			LastCallbackSuccessAt:   reverse.LastCallbackSuccessAt,
			LastCallbackQueuedAt:    reverse.LastCallbackQueuedAt,
			LastCallbackReplayAt:    reverse.LastCallbackReplayAt,
			LastCallbackStatusCode:  reverse.LastCallbackStatusCode,
			CallbackDeliveries:      reverse.CallbackDeliveries,
			CallbackFailures:        reverse.CallbackFailures,
			CallbackConsecutiveFail: reverse.CallbackConsecutiveFailures,
			LastReverseError:        reverse.LastReverseError,
			LastReverseErrorAt:      reverse.LastReverseErrorAt,
			LastCallbackError:       reverse.LastCallbackError,
			LastCallbackErrorAt:     reverse.LastCallbackErrorAt,
			LastReverseDisconnectAt: reverse.LastDisconnectedAt,
		})
	}
	if len(platforms) > 1 {
		sort.Slice(platforms, func(i, j int) bool {
			return platforms[i].PlatformID < platforms[j].PlatformID
		})
	}
	return platforms
}
