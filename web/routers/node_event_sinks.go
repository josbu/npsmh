package routers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/template"
	"time"

	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

const (
	nodeEventSinkContentCanonical = "canonical"
	nodeEventSinkContentCustom    = "custom"
	nodeHTTPResponseDrainLimit    = 128 << 10
)

func nodeDeliveryCanceled(ctx context.Context, err error) bool {
	return ctx != nil && ctx.Err() != nil
}

func resolveNodeEmitContext(ctx context.Context, baseCtx func() context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if baseCtx != nil {
		if current := baseCtx(); current != nil {
			return current
		}
	}
	return context.Background()
}

func closeNodeHTTPClientIdleConnections(client *http.Client) {
	if client == nil {
		return
	}
	client.CloseIdleConnections()
}

// Drain a bounded response body before close so small callback/webhook replies
// can still keep the underlying connection reusable under churn.
func closeNodeHTTPResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.CopyN(io.Discard, resp.Body, nodeHTTPResponseDrainLimit)
	_ = resp.Body.Close()
}

type nodeEventSelector struct {
	EventNames []string `json:"event_names,omitempty"`
	Resources  []string `json:"resources,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	UserIDs    []int    `json:"user_ids,omitempty"`
	ClientIDs  []int    `json:"client_ids,omitempty"`
	TunnelIDs  []int    `json:"tunnel_ids,omitempty"`
	HostIDs    []int    `json:"host_ids,omitempty"`
}

type nodeEventSinkConfig struct {
	Name            string            `json:"name,omitempty"`
	Enabled         bool              `json:"enabled"`
	Selector        nodeEventSelector `json:"selector,omitempty"`
	ContentMode     string            `json:"content_mode,omitempty"`
	ContentType     string            `json:"content_type,omitempty"`
	BodyTemplate    string            `json:"body_template,omitempty"`
	HeaderTemplates map[string]string `json:"header_templates,omitempty"`
	compiledBody    *template.Template
	compiledHeaders map[string]*template.Template
}

type nodeEventResourceLookup struct {
	UserExists     func(int) bool
	ClientExists   func(int) bool
	TunnelExists   func(int) bool
	HostExists     func(int) bool
	PlatformExists func(string) bool
}

func memoizeNodeEventResourceLookup(lookup nodeEventResourceLookup) nodeEventResourceLookup {
	return nodeEventResourceLookup{
		UserExists:     memoizeNodeIntLookup(lookup.UserExists),
		ClientExists:   memoizeNodeIntLookup(lookup.ClientExists),
		TunnelExists:   memoizeNodeIntLookup(lookup.TunnelExists),
		HostExists:     memoizeNodeIntLookup(lookup.HostExists),
		PlatformExists: memoizeNodeStringLookup(lookup.PlatformExists),
	}
}

func memoizeNodeIntLookup(fn func(int) bool) func(int) bool {
	if fn == nil {
		return nil
	}
	cache := make(map[int]bool)
	return func(id int) bool {
		if cached, ok := cache[id]; ok {
			return cached
		}
		cached := fn(id)
		cache[id] = cached
		return cached
	}
}

func memoizeNodeStringLookup(fn func(string) bool) func(string) bool {
	if fn == nil {
		return nil
	}
	cache := make(map[string]bool)
	return func(value string) bool {
		key := strings.TrimSpace(value)
		if cached, ok := cache[key]; ok {
			return cached
		}
		cached := fn(key)
		cache[key] = cached
		return cached
	}
}

type nodeEventSinkRenderMeta struct {
	ID        string `json:"id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

type nodeEventSinkEnvelope struct {
	Type             string                  `json:"type"`
	NodeID           string                  `json:"node_id,omitempty"`
	BootID           string                  `json:"boot_id,omitempty"`
	ConfigEpoch      string                  `json:"config_epoch,omitempty"`
	SchemaVersion    int                     `json:"schema_version,omitempty"`
	RuntimeStartedAt int64                   `json:"runtime_started_at,omitempty"`
	Timestamp        int64                   `json:"timestamp"`
	Sink             nodeEventSinkRenderMeta `json:"sink"`
	Event            webapi.Event            `json:"event"`
}

type nodeEventTemplateIDs struct {
	UserID   int `json:"user_id,omitempty"`
	ClientID int `json:"client_id,omitempty"`
	TunnelID int `json:"tunnel_id,omitempty"`
	HostID   int `json:"host_id,omitempty"`
}

type nodeEventTemplateData struct {
	Timestamp int64                   `json:"timestamp"`
	NodeID    string                  `json:"node_id,omitempty"`
	BootID    string                  `json:"boot_id,omitempty"`
	Config    string                  `json:"config_epoch,omitempty"`
	Sink      nodeEventSinkRenderMeta `json:"sink"`
	Event     webapi.Event            `json:"event"`
	IDs       nodeEventTemplateIDs    `json:"ids"`
}

type nodeRenderedEventSinkPayload struct {
	ContentType string
	Headers     map[string]string
	Body        []byte
	BodyMode    wsResponseBodyMode
}

func normalizeNodeEventSinkConfig(config nodeEventSinkConfig) nodeEventSinkConfig {
	config.Name = strings.TrimSpace(config.Name)
	config.ContentMode = normalizeNodeEventSinkContentMode(config.ContentMode)
	config.ContentType = normalizeNodeEventSinkContentType(config.ContentMode, config.ContentType)
	config.BodyTemplate = strings.TrimSpace(config.BodyTemplate)
	config.Selector = normalizeNodeEventSelector(config.Selector)
	if len(config.HeaderTemplates) == 0 {
		config.HeaderTemplates = nil
	} else {
		normalized := make(map[string]string, len(config.HeaderTemplates))
		for key, value := range config.HeaderTemplates {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key == "" || value == "" {
				continue
			}
			normalized[key] = value
		}
		if len(normalized) == 0 {
			config.HeaderTemplates = nil
		} else {
			config.HeaderTemplates = normalized
		}
	}
	return config
}

func normalizeNodeEventSelector(selector nodeEventSelector) nodeEventSelector {
	selector.EventNames = normalizeStringList(selector.EventNames, true)
	selector.Resources = normalizeStringList(selector.Resources, true)
	selector.Actions = normalizeStringList(selector.Actions, true)
	selector.UserIDs = normalizeIntList(selector.UserIDs)
	selector.ClientIDs = normalizeIntList(selector.ClientIDs)
	selector.TunnelIDs = normalizeIntList(selector.TunnelIDs)
	selector.HostIDs = normalizeIntList(selector.HostIDs)
	return selector
}

func normalizeNodeEventSinkContentMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", nodeEventSinkContentCanonical:
		return nodeEventSinkContentCanonical
	case nodeEventSinkContentCustom:
		return nodeEventSinkContentCustom
	default:
		return ""
	}
}

func normalizeNodeEventSinkContentType(mode, contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if strings.EqualFold(mode, nodeEventSinkContentCanonical) {
		return "application/json"
	}
	if contentType == "" {
		return "application/json"
	}
	return contentType
}

func normalizeStringList(values []string, lower bool) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeIntList(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	normalized := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func matchesNodeEventSelector(selector nodeEventSelector, event webapi.Event) bool {
	selector = normalizeNodeEventSelector(selector)
	if !matchesNodeStringFilter(selector.EventNames, event.Name) {
		return false
	}
	if !matchesNodeStringFilter(selector.Resources, event.Resource) {
		return false
	}
	if !matchesNodeStringFilter(selector.Actions, event.Action) {
		return false
	}
	ids := extractNodeEventTemplateIDs(event)
	if !matchesNodeIntFilter(selector.UserIDs, ids.UserID) {
		return false
	}
	if !matchesNodeIntFilter(selector.ClientIDs, ids.ClientID) {
		return false
	}
	if !matchesNodeIntFilter(selector.TunnelIDs, ids.TunnelID) {
		return false
	}
	if !matchesNodeIntFilter(selector.HostIDs, ids.HostID) {
		return false
	}
	return true
}

func matchesNodeStringFilter(values []string, target string) bool {
	if len(values) == 0 {
		return true
	}
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func matchesNodeIntFilter(values []int, target int) bool {
	if len(values) == 0 {
		return true
	}
	if target <= 0 {
		return false
	}
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func extractNodeEventTemplateIDs(event webapi.Event) nodeEventTemplateIDs {
	ids := nodeEventTemplateIDs{
		UserID:   eventFieldInt(event.Fields, "user_id"),
		ClientID: eventFieldInt(event.Fields, "client_id"),
		TunnelID: eventFieldInt(event.Fields, "tunnel_id"),
		HostID:   eventFieldInt(event.Fields, "host_id"),
	}
	switch strings.ToLower(strings.TrimSpace(event.Resource)) {
	case "user":
		if ids.UserID == 0 {
			ids.UserID = eventFieldInt(event.Fields, "id")
		}
	case "client":
		if ids.ClientID == 0 {
			ids.ClientID = eventFieldInt(event.Fields, "id")
		}
		if ids.UserID == 0 {
			ids.UserID = eventFieldInt(event.Fields, "owner_user_id")
		}
	case "tunnel":
		if ids.TunnelID == 0 {
			ids.TunnelID = eventFieldInt(event.Fields, "id")
		}
		if ids.UserID == 0 {
			ids.UserID = eventFieldInt(event.Fields, "owner_user_id")
		}
	case "host":
		if ids.HostID == 0 {
			ids.HostID = eventFieldInt(event.Fields, "id")
		}
		if ids.UserID == 0 {
			ids.UserID = eventFieldInt(event.Fields, "owner_user_id")
		}
	}
	return ids
}

func scrubNodeEventSelector(selector nodeEventSelector, lookup nodeEventResourceLookup) (nodeEventSelector, bool) {
	scrubbed := normalizeNodeEventSelector(selector)
	disable := false
	scrubbed.UserIDs, disable = scrubNodeIntSelector(scrubbed.UserIDs, lookup.UserExists)
	if disable {
		return scrubbed, true
	}
	scrubbed.ClientIDs, disable = scrubNodeIntSelector(scrubbed.ClientIDs, lookup.ClientExists)
	if disable {
		return scrubbed, true
	}
	scrubbed.TunnelIDs, disable = scrubNodeIntSelector(scrubbed.TunnelIDs, lookup.TunnelExists)
	if disable {
		return scrubbed, true
	}
	scrubbed.HostIDs, disable = scrubNodeIntSelector(scrubbed.HostIDs, lookup.HostExists)
	return scrubbed, disable
}

func scrubNodeIntSelector(values []int, exists func(int) bool) ([]int, bool) {
	if len(values) == 0 {
		return nil, false
	}
	if exists == nil {
		return append([]int(nil), values...), false
	}
	filtered := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if exists(value) {
			filtered = append(filtered, value)
		}
	}
	if len(filtered) == 0 {
		return nil, true
	}
	return filtered, false
}

func renderNodeEventSinkPayload(state *State, sinkID string, sinkKind string, config nodeEventSinkConfig, event webapi.Event) (nodeRenderedEventSinkPayload, error) {
	var err error
	config, err = prepareNodeEventSinkConfig(config)
	if err != nil {
		return nodeRenderedEventSinkPayload{}, err
	}
	if !config.Enabled {
		return nodeRenderedEventSinkPayload{}, fmt.Errorf("sink is disabled")
	}
	data := buildNodeEventTemplateData(state, sinkID, sinkKind, config.Name, event)
	headers := make(map[string]string)
	for key, value := range config.HeaderTemplates {
		rendered, err := renderNodeEventTemplate(config.compiledHeaders[key], value, data)
		if err != nil {
			return nodeRenderedEventSinkPayload{}, err
		}
		rendered = strings.TrimSpace(rendered)
		if rendered == "" {
			continue
		}
		headers[key] = rendered
	}
	if strings.EqualFold(config.ContentMode, nodeEventSinkContentCustom) {
		rendered, err := renderNodeEventTemplate(config.compiledBody, config.BodyTemplate, data)
		if err != nil {
			return nodeRenderedEventSinkPayload{}, err
		}
		return nodeRenderedEventSinkPayload{
			ContentType: config.ContentType,
			Headers:     headers,
			Body:        []byte(rendered),
			BodyMode:    nodeRenderedEventSinkPayloadMode(config.ContentType),
		}, nil
	}
	envelope := buildNodeEventSinkEnvelope(state, sinkID, sinkKind, config.Name, event)
	body, err := json.Marshal(envelope)
	if err != nil {
		return nodeRenderedEventSinkPayload{}, err
	}
	return nodeRenderedEventSinkPayload{
		ContentType: "application/json",
		Headers:     headers,
		Body:        body,
		BodyMode:    wsResponseBodyModeJSON,
	}, nil
}

func nodeRenderedEventSinkPayloadMode(contentType string) wsResponseBodyMode {
	if isWSJSONContentType(contentType) {
		return wsResponseBodyModeUnknown
	}
	return wsResponseBodyModeText
}

func buildNodeEventSinkEnvelope(state *State, sinkID string, sinkKind string, name string, event webapi.Event) nodeEventSinkEnvelope {
	timestamp := time.Now().Unix()
	envelope := nodeEventSinkEnvelope{
		Type:      "event_callback",
		Timestamp: timestamp,
		Sink: nodeEventSinkRenderMeta{
			ID:        strings.TrimSpace(sinkID),
			Kind:      strings.TrimSpace(sinkKind),
			Name:      strings.TrimSpace(name),
			Timestamp: timestamp,
		},
		Event: cloneEvent(event),
	}
	if state == nil || state.App == nil {
		return envelope
	}
	descriptor := webservice.BuildNodeDescriptor(webservice.NodeDescriptorInput{
		NodeID:           state.App.NodeID,
		Config:           state.CurrentConfig(),
		BootID:           state.RuntimeIdentity().BootID(),
		RuntimeStartedAt: state.RuntimeIdentity().StartedAt(),
		ConfigEpoch:      state.RuntimeIdentity().ConfigEpoch(),
	})
	envelope.NodeID = descriptor.NodeID
	envelope.BootID = descriptor.BootID
	envelope.ConfigEpoch = descriptor.ConfigEpoch
	envelope.SchemaVersion = descriptor.SchemaVersion
	envelope.RuntimeStartedAt = descriptor.RuntimeStartedAt
	return envelope
}

func prepareNodeEventSinkConfig(config nodeEventSinkConfig) (nodeEventSinkConfig, error) {
	config = normalizeNodeEventSinkConfig(config)
	if !strings.EqualFold(config.ContentMode, nodeEventSinkContentCustom) {
		config.compiledBody = nil
		config.compiledHeaders = nil
		return config, nil
	}
	if config.compiledBody == nil {
		compiled, err := compileNodeEventTemplate(config.BodyTemplate)
		if err != nil {
			return nodeEventSinkConfig{}, errors.New("invalid body_template")
		}
		config.compiledBody = compiled
	}
	if len(config.HeaderTemplates) == 0 {
		config.compiledHeaders = nil
		return config, nil
	}
	if len(config.compiledHeaders) != len(config.HeaderTemplates) {
		config.compiledHeaders = nil
	}
	compiledHeaders := make(map[string]*template.Template, len(config.HeaderTemplates))
	for key, value := range config.HeaderTemplates {
		compiled := config.compiledHeaders[key]
		if compiled == nil {
			var err error
			compiled, err = compileNodeEventTemplate(value)
			if err != nil {
				return nodeEventSinkConfig{}, errors.New("invalid header_templates for " + key)
			}
		}
		compiledHeaders[key] = compiled
	}
	config.compiledHeaders = compiledHeaders
	return config, nil
}

func cloneNodeEventSinkConfig(config nodeEventSinkConfig) nodeEventSinkConfig {
	cloned := normalizeNodeEventSinkConfig(config)
	cloned.HeaderTemplates = cloneNodeHeaderTemplates(cloned.HeaderTemplates)
	cloned.compiledBody = config.compiledBody
	if len(config.compiledHeaders) != 0 {
		cloned.compiledHeaders = make(map[string]*template.Template, len(config.compiledHeaders))
		for key, compiled := range config.compiledHeaders {
			cloned.compiledHeaders[key] = compiled
		}
	}
	prepared, err := prepareNodeEventSinkConfig(cloned)
	if err == nil {
		return prepared
	}
	return cloned
}

var nodeEventTemplateFuncMap = template.FuncMap{
	"json": func(value interface{}) string {
		body, _ := json.Marshal(value)
		return string(body)
	},
	"quote": strconv.Quote,
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	"trim":  strings.TrimSpace,
}

func buildNodeEventTemplateData(state *State, sinkID string, sinkKind string, name string, event webapi.Event) nodeEventTemplateData {
	envelope := buildNodeEventSinkEnvelope(state, sinkID, sinkKind, name, event)
	return nodeEventTemplateData{
		Timestamp: envelope.Timestamp,
		NodeID:    envelope.NodeID,
		BootID:    envelope.BootID,
		Config:    envelope.ConfigEpoch,
		Sink:      envelope.Sink,
		Event:     envelope.Event,
		IDs:       extractNodeEventTemplateIDs(event),
	}
}

func renderNodeEventTemplateString(source string, data nodeEventTemplateData) (string, error) {
	return renderNodeEventTemplate(nil, source, data)
}

func compileNodeEventTemplate(source string) (*template.Template, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, nil
	}
	return template.New("node_event_sink").Funcs(nodeEventTemplateFuncMap).Option("missingkey=zero").Parse(source)
}

func renderNodeEventTemplate(compiled *template.Template, source string, data nodeEventTemplateData) (string, error) {
	if compiled == nil {
		var err error
		compiled, err = compileNodeEventTemplate(source)
		if err != nil {
			return "", err
		}
	}
	if compiled == nil {
		return "", nil
	}
	var buffer bytes.Buffer
	if err := compiled.Execute(&buffer, data); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func nodeEventMayInvalidateSelectors(event webapi.Event) bool {
	switch strings.ToLower(strings.TrimSpace(event.Resource)) {
	case "user", "client", "tunnel", "host":
	default:
		return false
	}
	action := strings.ToLower(strings.TrimSpace(event.Action))
	name := strings.ToLower(strings.TrimSpace(event.Name))
	return action == "delete" || strings.HasSuffix(name, ".deleted")
}
