package routers

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type closeIdleTrackingTransport struct {
	closeIdleCalls int
}

func (t *closeIdleTrackingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func (t *closeIdleTrackingTransport) CloseIdleConnections() {
	if t != nil {
		t.closeIdleCalls++
	}
}

func TestNodeCallbackWorkerStopClosesIdleHTTPConnections(t *testing.T) {
	transport := &closeIdleTrackingTransport{}
	worker := &nodeCallbackWorker{
		client: &http.Client{Transport: transport},
	}

	worker.stop()

	if transport.closeIdleCalls != 1 {
		t.Fatalf("CloseIdleConnections() calls = %d, want 1", transport.closeIdleCalls)
	}
}

func TestNodeWebhookWorkerStopClosesIdleHTTPConnections(t *testing.T) {
	transport := &closeIdleTrackingTransport{}
	worker := &nodeWebhookWorker{
		client: &http.Client{Transport: transport},
	}

	worker.stop()

	if transport.closeIdleCalls != 1 {
		t.Fatalf("CloseIdleConnections() calls = %d, want 1", transport.closeIdleCalls)
	}
}
