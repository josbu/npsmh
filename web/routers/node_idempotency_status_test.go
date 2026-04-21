package routers

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	webservice "github.com/djylb/nps/web/service"
)

func TestNodeIdempotencyBindRuntimeStatusSyncsLoadedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node_idempotency_state.json")
	writeNodeRuntimeState(path, nodeIdempotencyPersistedStore{
		Version: nodeRuntimeStateVersion,
		Entries: []nodeIdempotencyPersistedEntry{
			{
				Key:         "scope\x00cached",
				Fingerprint: "fingerprint",
				ExpiresAt:   time.Now().Add(time.Minute).Unix(),
				HTTPResp: &nodeIdempotencyHTTPResponse{
					Status: http.StatusOK,
					Body:   []byte(`{"ok":true}`),
				},
			},
			{
				Key:         "scope\x00broken",
				Fingerprint: "fingerprint",
				ExpiresAt:   time.Now().Add(time.Minute).Unix(),
			},
		},
	})

	store := newNodeIdempotencyStore(time.Minute, path)
	status := webservice.NewInMemoryNodeRuntimeStatusStore()
	store.BindRuntimeStatus(status)

	payload := status.Idempotency()
	if payload.CachedEntries != 1 || payload.Inflight != 0 || payload.TTLSeconds != 60 {
		t.Fatalf("BindRuntimeStatus() payload = %+v, want cached=1 inflight=0 ttl=60", payload)
	}

	cached := store.acquire("scope", "cached", "fingerprint")
	if cached.httpResp == nil || cached.entry != nil || cached.conflict || cached.err != nil {
		t.Fatalf("cached persisted acquire = %+v, want cached replay", cached)
	}

	broken := store.acquire("scope", "broken", "fingerprint")
	if broken.entry == nil || broken.httpResp != nil || broken.conflict || broken.err != nil {
		t.Fatalf("broken persisted entry should be ignored and replaced with a fresh inflight entry, got %+v", broken)
	}
}

func TestNodeIdempotencyLoadAcceptsLegacyFlatHeaderState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node_idempotency_state.json")
	writeNodeRuntimeStateBytes(path, []byte(`{
  "version": 1,
  "entries": [
    {
      "key": "scope\u0000cached",
      "fingerprint": "fingerprint",
      "expires_at": 4102444800,
      "http_response": {
        "Status": 200,
        "Header": {
          "Set-Cookie": "session=a; Path=/",
          "Cache-Control": "no-cache"
        },
        "Body": "eyJvayI6dHJ1ZX0="
      }
    }
  ]
}`))

	store := newNodeIdempotencyStore(time.Minute, path)
	result := store.acquire("scope", "cached", "fingerprint")
	if result.httpResp == nil || result.entry != nil || result.conflict || result.err != nil {
		t.Fatalf("legacy persisted acquire = %+v, want cached replay", result)
	}
	if got := result.httpResp.Header["Set-Cookie"]; len(got) != 1 || got[0] != "session=a; Path=/" {
		t.Fatalf("legacy Set-Cookie header = %v, want single preserved value", got)
	}
	if got := result.httpResp.Header["Cache-Control"]; len(got) != 1 || got[0] != "no-cache" {
		t.Fatalf("legacy Cache-Control header = %v, want single preserved value", got)
	}
}
