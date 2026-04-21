package routers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWebhookMutationStatusTreatsUnknownErrorsAsInternalServerError(t *testing.T) {
	if got := webhookMutationStatus(errors.New("storage unavailable")); got != http.StatusInternalServerError {
		t.Fatalf("webhookMutationStatus(unknown) = %d, want 500", got)
	}
	if got := webhookMutationStatus(errors.New("invalid header_templates for X-Test")); got != http.StatusBadRequest {
		t.Fatalf("webhookMutationStatus(invalid header_templates) = %d, want 400", got)
	}
}

func TestNodeWebhookMutationAbortTreatsUnknownErrorsAsInternalServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	nodeWebhookMutationAbort(ctx, errors.New("storage unavailable"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("nodeWebhookMutationAbort() status = %d, want 500 body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"code":"request_failed"`) || !strings.Contains(body, `"message":"storage unavailable"`) {
		t.Fatalf("nodeWebhookMutationAbort() body = %s, want request_failed with original message", body)
	}
}

func TestStampNodeWebhookResponseDataTimestamp(t *testing.T) {
	listStamped, ok := stampNodeWebhookResponseDataTimestamp(nodeWebhookListPayload{
		Items: []nodeWebhookPayload{{ID: 3}},
	}, 55).(nodeWebhookListPayload)
	if !ok {
		t.Fatal("stampNodeWebhookResponseDataTimestamp(list) type assertion failed")
	}
	if listStamped.Timestamp != 55 || len(listStamped.Items) != 1 || listStamped.Items[0].ID != 3 {
		t.Fatalf("unexpected stamped list payload: %+v", listStamped)
	}

	mutationStamped, ok := stampNodeWebhookResponseDataTimestamp(nodeWebhookMutationPayload{
		Item: &nodeWebhookPayload{ID: 7},
	}, 66).(nodeWebhookMutationPayload)
	if !ok {
		t.Fatal("stampNodeWebhookResponseDataTimestamp(mutation) type assertion failed")
	}
	if mutationStamped.Timestamp != 66 || mutationStamped.Item == nil || mutationStamped.Item.ID != 7 {
		t.Fatalf("unexpected stamped mutation payload: %+v", mutationStamped)
	}
}

func TestWebhookEventFieldsIncludeFullSnapshot(t *testing.T) {
	payload := nodeWebhookPayload{
		ID:                  7,
		Name:                "hook",
		URL:                 "https://example.invalid/hook",
		Method:              "POST",
		Enabled:             false,
		TimeoutSeconds:      0,
		EventNames:          nil,
		Resources:           []string{"client"},
		Actions:             nil,
		UserIDs:             nil,
		ClientIDs:           []int{3},
		TunnelIDs:           nil,
		HostIDs:             nil,
		ContentMode:         "",
		ContentType:         "",
		BodyTemplate:        "",
		HeaderTemplates:     nil,
		Owner:               nodeWebhookOwnerPayload{},
		CreatedAt:           10,
		UpdatedAt:           20,
		LastDeliveredAt:     0,
		LastError:           "",
		LastErrorAt:         0,
		LastStatusCode:      0,
		Deliveries:          0,
		Failures:            0,
		ConsecutiveFailures: 0,
		LastDisabledReason:  "",
		LastDisabledAt:      0,
	}

	fields := webhookEventFields(payload, "webhook.selector_scrubbed")

	if got, ok := fields["enabled"].(bool); !ok || got {
		t.Fatalf("enabled field = %#v, want false", fields["enabled"])
	}
	if got, ok := fields["timeout_seconds"].(int); !ok || got != 0 {
		t.Fatalf("timeout_seconds = %#v, want 0", fields["timeout_seconds"])
	}
	if got, ok := fields["event_names"].([]string); !ok || len(got) != 0 {
		t.Fatalf("event_names = %#v, want empty []string", fields["event_names"])
	}
	if got, ok := fields["client_ids"].([]int); !ok || len(got) != 1 || got[0] != 3 {
		t.Fatalf("client_ids = %#v, want [3]", fields["client_ids"])
	}
	if got, ok := fields["header_templates"].(map[string]string); !ok || len(got) != 0 {
		t.Fatalf("header_templates = %#v, want empty map[string]string", fields["header_templates"])
	}
	owner, ok := fields["owner"].(map[string]interface{})
	if !ok {
		t.Fatalf("owner = %#v, want map[string]interface{}", fields["owner"])
	}
	if got, ok := owner["username"].(string); !ok || got != "" {
		t.Fatalf("owner.username = %#v, want empty string", owner["username"])
	}
	if got, ok := fields["last_error"].(string); !ok || got != "" {
		t.Fatalf("last_error = %#v, want empty string", fields["last_error"])
	}
	if got, ok := fields["last_status_code"].(int); !ok || got != 0 {
		t.Fatalf("last_status_code = %#v, want 0", fields["last_status_code"])
	}
	if got, ok := fields["cause_event"].(string); !ok || got != "webhook.selector_scrubbed" {
		t.Fatalf("cause_event = %#v, want webhook.selector_scrubbed", fields["cause_event"])
	}
}
