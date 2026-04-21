package api

import (
	"context"
	"testing"

	webservice "github.com/djylb/nps/web/service"
)

type managementStatusTestContext struct {
	status int
	body   any
}

type responseMetaTestContext struct {
	managementStatusTestContext
	metadata RequestMetadata
}

func (c *managementStatusTestContext) BaseContext() context.Context        { return context.Background() }
func (c *managementStatusTestContext) String(string) string                { return "" }
func (c *managementStatusTestContext) LookupString(string) (string, bool)  { return "", false }
func (c *managementStatusTestContext) Int(string, ...int) int              { return 0 }
func (c *managementStatusTestContext) Bool(string, ...bool) bool           { return false }
func (c *managementStatusTestContext) Method() string                      { return "" }
func (c *managementStatusTestContext) Host() string                        { return "" }
func (c *managementStatusTestContext) RemoteAddr() string                  { return "" }
func (c *managementStatusTestContext) ClientIP() string                    { return "" }
func (c *managementStatusTestContext) RequestHeader(string) string         { return "" }
func (c *managementStatusTestContext) SessionValue(string) interface{}     { return nil }
func (c *managementStatusTestContext) SetSessionValue(string, interface{}) {}
func (c *managementStatusTestContext) DeleteSessionValue(string)           {}
func (c *managementStatusTestContext) SetParam(string, string)             {}
func (c *managementStatusTestContext) RespondJSON(status int, body interface{}) {
	c.status, c.body = status, body
}
func (c *managementStatusTestContext) RespondString(int, string)        {}
func (c *managementStatusTestContext) RespondData(int, string, []byte)  {}
func (c *managementStatusTestContext) Redirect(int, string)             {}
func (c *managementStatusTestContext) SetResponseHeader(string, string) {}
func (c *managementStatusTestContext) IsWritten() bool                  { return c.status > 0 }
func (c *managementStatusTestContext) Actor() *Actor                    { return nil }
func (c *managementStatusTestContext) SetActor(*Actor)                  {}
func (c *managementStatusTestContext) Metadata() RequestMetadata        { return RequestMetadata{} }
func (c *responseMetaTestContext) Metadata() RequestMetadata            { return c.metadata }

func TestRespondNodeResourceDataUsesUnauthorizedForErrUnauthenticated(t *testing.T) {
	ctx := &managementStatusTestContext{}
	respondNodeResourceData(ctx, nodeResourceItemPayload{}, webservice.ErrUnauthenticated)
	if ctx.status != 401 {
		t.Fatalf("respondNodeResourceData() status = %d, want 401", ctx.status)
	}
	payload, ok := ctx.body.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("respondNodeResourceData() body = %#v, want ManagementErrorResponse", ctx.body)
	}
	if payload.Error.Code != "unauthorized" {
		t.Fatalf("respondNodeResourceData() error.code = %q, want unauthorized", payload.Error.Code)
	}
}

func TestRespondNodeControlDataUsesUnauthorizedForErrUnauthenticated(t *testing.T) {
	ctx := &managementStatusTestContext{}
	if !respondNodeControlData(ctx, nil, webservice.ErrUnauthenticated) {
		t.Fatal("respondNodeControlData() = false, want true")
	}
	if ctx.status != 401 {
		t.Fatalf("respondNodeControlData() status = %d, want 401", ctx.status)
	}
	payload, ok := ctx.body.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("respondNodeControlData() body = %#v, want ManagementErrorResponse", ctx.body)
	}
	if payload.Error.Code != "unauthorized" {
		t.Fatalf("respondNodeControlData() error.code = %q, want unauthorized", payload.Error.Code)
	}
}

func TestRespondNodeControlDataUsesUsageSnapshotConfigEpochForOverviewMeta(t *testing.T) {
	ctx := &managementStatusTestContext{}
	payload := webservice.NodeOverviewPayload{
		NodeID: "node-a",
		UsageSnapshot: &webservice.NodeUsageSnapshotPayload{
			NodeID:      "node-a",
			ConfigEpoch: "cfg-usage-1",
			GeneratedAt: 123,
		},
		Timestamp: 123,
	}
	if !respondNodeControlData(ctx, payload, nil) {
		t.Fatal("respondNodeControlData() = false, want true")
	}
	if ctx.status != 200 {
		t.Fatalf("respondNodeControlData() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.body.(ManagementDataResponse)
	if !ok {
		t.Fatalf("respondNodeControlData() body = %#v, want ManagementDataResponse", ctx.body)
	}
	if response.Meta.ConfigEpoch != "cfg-usage-1" {
		t.Fatalf("response.Meta.ConfigEpoch = %q, want cfg-usage-1", response.Meta.ConfigEpoch)
	}
}

func TestManagementResponseMetaUsesRequestIDAndTrimmedConfigEpoch(t *testing.T) {
	ctx := &responseMetaTestContext{
		metadata: RequestMetadata{RequestID: " req-1 "},
	}

	meta := managementResponseMeta(ctx, 123, " cfg-1 ")
	if meta.RequestID != "req-1" {
		t.Fatalf("meta.RequestID = %q, want req-1", meta.RequestID)
	}
	if meta.GeneratedAt != 123 {
		t.Fatalf("meta.GeneratedAt = %d, want 123", meta.GeneratedAt)
	}
	if meta.ConfigEpoch != "cfg-1" {
		t.Fatalf("meta.ConfigEpoch = %q, want cfg-1", meta.ConfigEpoch)
	}
}

func TestRespondManagementErrorMessageDefaultsCodeAndStatusText(t *testing.T) {
	ctx := &managementStatusTestContext{}
	respondManagementErrorMessage(ctx, 400, " ", " ", nil)

	if ctx.status != 400 {
		t.Fatalf("respondManagementErrorMessage() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.body.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("respondManagementErrorMessage() body = %#v, want ManagementErrorResponse", ctx.body)
	}
	if response.Error.Code != "request_failed" {
		t.Fatalf("response.Error.Code = %q, want request_failed", response.Error.Code)
	}
	if response.Error.Message != "Bad Request" {
		t.Fatalf("response.Error.Message = %q, want Bad Request", response.Error.Message)
	}
}
