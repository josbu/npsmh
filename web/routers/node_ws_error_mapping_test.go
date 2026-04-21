package routers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	webservice "github.com/djylb/nps/web/service"
)

func TestWebhookMutationErrorDetailUsesCanonicalCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{
			name:    "unknown",
			err:     errors.New("storage unavailable"),
			status:  http.StatusInternalServerError,
			code:    "request_failed",
			message: "storage unavailable",
		},
		{
			name:    "unauthorized",
			err:     webservice.ErrUnauthenticated,
			status:  http.StatusUnauthorized,
			code:    "unauthorized",
			message: "unauthenticated",
		},
		{
			name:    "enabled_required",
			err:     errors.New("missing enabled"),
			status:  http.StatusBadRequest,
			code:    "enabled_required",
			message: "missing enabled",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, code, message := webhookMutationErrorDetail(tc.err)
			if status != tc.status || code != tc.code || message != tc.message {
				t.Fatalf("webhookMutationErrorDetail(%q) = (%d, %q, %q), want (%d, %q, %q)", tc.err.Error(), status, code, message, tc.status, tc.code, tc.message)
			}
		})
	}
}

func TestWriteWSWebhookMutationErrorUsesCanonicalCodes(t *testing.T) {
	t.Parallel()

	ctx := &wsAPIContext{responseHeader: map[string]string{}}
	writeWSWebhookMutationError(ctx, errors.New("storage unavailable"))
	if ctx.status != http.StatusInternalServerError {
		t.Fatalf("writeWSWebhookMutationError(unknown) status = %d, want 500", ctx.status)
	}
	body := string(ctx.responseBody)
	if !strings.Contains(body, `"code":"request_failed"`) || !strings.Contains(body, `"message":"storage unavailable"`) {
		t.Fatalf("writeWSWebhookMutationError(unknown) body = %s, want request_failed with original message", body)
	}

	ctx = &wsAPIContext{responseHeader: map[string]string{}}
	writeWSWebhookMutationError(ctx, errors.New("missing enabled"))
	if ctx.status != http.StatusBadRequest {
		t.Fatalf("writeWSWebhookMutationError(missing enabled) status = %d, want 400", ctx.status)
	}
	body = string(ctx.responseBody)
	if !strings.Contains(body, `"code":"enabled_required"`) || !strings.Contains(body, `"message":"missing enabled"`) {
		t.Fatalf("writeWSWebhookMutationError(missing enabled) body = %s, want enabled_required with original message", body)
	}

	ctx = &wsAPIContext{responseHeader: map[string]string{}}
	writeWSWebhookMutationError(ctx, webservice.ErrUnauthenticated)
	if ctx.status != http.StatusUnauthorized {
		t.Fatalf("writeWSWebhookMutationError(unauthorized) status = %d, want 401", ctx.status)
	}
	body = string(ctx.responseBody)
	if !strings.Contains(body, `"code":"unauthorized"`) || !strings.Contains(body, `"message":"unauthenticated"`) {
		t.Fatalf("writeWSWebhookMutationError(unauthorized) body = %s, want unauthorized with original message", body)
	}
}

func TestNodeWSSubscriptionErrorDetailUsesCanonicalCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{
			name:    "not_found",
			err:     errNodeWSSubscriptionNotFound,
			status:  http.StatusNotFound,
			code:    "subscription_not_found",
			message: "subscription not found",
		},
		{
			name:    "enabled_required",
			err:     errNodeWSSubscriptionEnabledReq,
			status:  http.StatusBadRequest,
			code:    "enabled_required",
			message: "missing enabled",
		},
		{
			name:    "invalid_content_mode",
			err:     errNodeWSSubscriptionInvalidMode,
			status:  http.StatusBadRequest,
			code:    "invalid_content_mode",
			message: "invalid content_mode",
		},
		{
			name:    "unavailable",
			err:     errNodeWSSubscriptionsUnavailable,
			status:  http.StatusBadRequest,
			code:    "realtime_subscriptions_unavailable",
			message: "websocket subscriptions are unavailable",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, code, message := nodeWSSubscriptionErrorDetail(tc.err)
			if status != tc.status || code != tc.code || message != tc.message {
				t.Fatalf("nodeWSSubscriptionErrorDetail(%q) = (%d, %q, %q), want (%d, %q, %q)", tc.err.Error(), status, code, message, tc.status, tc.code, tc.message)
			}
		})
	}
}

func TestWriteWSSubscriptionErrorUsesCanonicalCodes(t *testing.T) {
	t.Parallel()

	ctx := &wsAPIContext{responseHeader: map[string]string{}}
	writeWSSubscriptionError(ctx, errNodeWSSubscriptionNotFound)
	if ctx.status != http.StatusNotFound {
		t.Fatalf("writeWSSubscriptionError(not_found) status = %d, want 404", ctx.status)
	}
	body := string(ctx.responseBody)
	if !strings.Contains(body, `"code":"subscription_not_found"`) || !strings.Contains(body, `"message":"subscription not found"`) {
		t.Fatalf("writeWSSubscriptionError(not_found) body = %s, want subscription_not_found", body)
	}

	ctx = &wsAPIContext{responseHeader: map[string]string{}}
	writeWSSubscriptionError(ctx, errNodeWSSubscriptionEnabledReq)
	if ctx.status != http.StatusBadRequest {
		t.Fatalf("writeWSSubscriptionError(enabled_required) status = %d, want 400", ctx.status)
	}
	body = string(ctx.responseBody)
	if !strings.Contains(body, `"code":"enabled_required"`) || !strings.Contains(body, `"message":"missing enabled"`) {
		t.Fatalf("writeWSSubscriptionError(enabled_required) body = %s, want enabled_required", body)
	}
}

func TestNodeCallbackQueueMutationStatusTreatsUnknownErrorsAsInternalServerError(t *testing.T) {
	t.Parallel()

	if got := nodeCallbackQueueMutationStatus(errors.New("storage unavailable")); got != http.StatusInternalServerError {
		t.Fatalf("nodeCallbackQueueMutationStatus(unknown) = %d, want 500", got)
	}
	if got := nodeCallbackQueueMutationStatus(webservice.ErrInvalidCallbackQueueAction); got != http.StatusBadRequest {
		t.Fatalf("nodeCallbackQueueMutationStatus(invalid action) = %d, want 400", got)
	}
}

func TestWriteWSManagementErrorResponseUsesCanonicalCodes(t *testing.T) {
	t.Parallel()

	ctx := &wsAPIContext{responseHeader: map[string]string{}}
	writeWSManagementErrorResponse(ctx, nodeCallbackQueueMutationStatus(webservice.ErrInvalidCallbackQueueAction), webservice.ErrInvalidCallbackQueueAction)
	if ctx.status != http.StatusBadRequest {
		t.Fatalf("writeWSManagementErrorResponse(invalid action) status = %d, want 400", ctx.status)
	}
	body := string(ctx.responseBody)
	if !strings.Contains(body, `"code":"invalid_callback_queue_action"`) || !strings.Contains(body, `"message":"invalid callback queue action"`) {
		t.Fatalf("writeWSManagementErrorResponse(invalid action) body = %s, want invalid_callback_queue_action with original message", body)
	}
}
