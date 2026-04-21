package routers

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
)

func TestDeliverNodeCallbackDrainsSmallResponseBodyForConnectionReuse(t *testing.T) {
	var accepted int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 96<<10)))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&accepted, 1)
		}
	}
	server.Start()
	defer server.Close()

	state := NewStateWithApp(webapi.New(nil))
	defer state.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()

	platform := servercfg.ManagementPlatformConfig{
		PlatformID:  "platform-a",
		Token:       "callback-secret",
		CallbackURL: server.URL,
	}
	payload := buildNodeCallbackEnvelope(state, platform.PlatformID, webapi.Event{
		Name:     "client.created",
		Resource: "client",
		Fields:   map[string]interface{}{"id": 1},
	})

	for attempt := 0; attempt < 2; attempt++ {
		statusCode, err := deliverNodeCallback(context.Background(), client, state, platform, payload)
		if statusCode != http.StatusBadGateway {
			t.Fatalf("deliverNodeCallback() status = %d, want %d", statusCode, http.StatusBadGateway)
		}
		if err == nil {
			t.Fatal("deliverNodeCallback() error = nil, want callback http error")
		}
	}

	if got := atomic.LoadInt32(&accepted); got != 1 {
		t.Fatalf("accepted connections = %d, want 1", got)
	}
}

func TestDeliverNodeWebhookDrainsSmallResponseBodyForConnectionReuse(t *testing.T) {
	var accepted int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", 96<<10)))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt32(&accepted, 1)
		}
	}
	server.Start()
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()

	config := nodeWebhookConfig{
		ID:             7,
		URL:            server.URL,
		TimeoutSeconds: 5,
	}
	payload := nodeRenderedEventSinkPayload{
		ContentType: "application/json",
		Body:        []byte(`{"event":"client.created"}`),
	}

	for attempt := 0; attempt < 2; attempt++ {
		statusCode, err := deliverNodeWebhook(context.Background(), client, config, payload)
		if statusCode != http.StatusOK {
			t.Fatalf("deliverNodeWebhook() status = %d, want %d", statusCode, http.StatusOK)
		}
		if err != nil {
			t.Fatalf("deliverNodeWebhook() error = %v, want nil", err)
		}
	}

	if got := atomic.LoadInt32(&accepted); got != 1 {
		t.Fatalf("accepted connections = %d, want 1", got)
	}
}
