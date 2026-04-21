package api

import "testing"

func TestProtectedActionCatalogAllHavePublishedNodeRoutes(t *testing.T) {
	specs := ProtectedActionCatalog(&App{})
	if len(specs) == 0 {
		t.Fatal("ProtectedActionCatalog() returned no specs")
	}
	for _, spec := range specs {
		if !spec.Protected {
			continue
		}
		if spec.Path == "" {
			t.Fatalf("protected action %s/%s is missing ActionSpec.Path", spec.Resource, spec.Action)
		}
		if spec.Method == "" {
			t.Fatalf("protected action %s/%s is missing ActionSpec.Method", spec.Resource, spec.Action)
		}
		path := PublishedActionPathForResourceAction("/base", spec.Resource, spec.Action)
		if path == "" {
			t.Fatalf("protected action %s/%s is missing published action path", spec.Resource, spec.Action)
		}
		method := PublishedActionMethodForResourceAction(spec.Resource, spec.Action)
		if method == "" {
			t.Fatalf("protected action %s/%s is missing published action method", spec.Resource, spec.Action)
		}
	}
}

func TestPublishedActionRequestForResourceActionMaterializesTemplates(t *testing.T) {
	path, method := PublishedActionRequestForResourceAction("/base", "clients", "update", map[string]string{
		"client_id": "17",
	})
	if path != "/base/api/clients/17/actions/update" || method != "POST" {
		t.Fatalf("PublishedActionRequestForResourceAction(clients/update) = %q, %q", path, method)
	}

	path, method = PublishedActionRequestForResourceAction("/base", "security_bans", "delete", map[string]string{
		"ip": "1.2.3.4",
	})
	if path != "/base/api/security/bans/actions/delete" || method != "POST" {
		t.Fatalf("PublishedActionRequestForResourceAction(security_bans/delete) = %q, %q", path, method)
	}

	path, method = PublishedActionRequestForResourceAction("/base", "clients", "kick", nil)
	if path != "/base/api/clients/actions/kick" || method != "POST" {
		t.Fatalf("PublishedActionRequestForResourceAction(clients/kick) = %q, %q", path, method)
	}

	path, method = PublishedActionRequestForResourceAction("/base", "clients", "qrcode_generate", nil)
	if path != "/base/api/tools/qrcode" || method != "POST" {
		t.Fatalf("PublishedActionRequestForResourceAction(clients/qrcode_generate) = %q, %q", path, method)
	}

	path, method = PublishedActionRequestForResourceAction("/base", "callbacks_queue", "replay", nil)
	if path != "/base/api/callbacks/queue/actions/replay" || method != "POST" {
		t.Fatalf("PublishedActionRequestForResourceAction(callbacks_queue/replay) = %q, %q", path, method)
	}

	path, method = PublishedActionRequestForResourceAction("/base", "clients", "update", map[string]string{})
	if path != "" || method != "" {
		t.Fatalf("PublishedActionRequestForResourceAction(clients/update missing id) = %q, %q, want empty", path, method)
	}
}

func TestHasPublishedActionForResourceActionOnlyMatchesProtectedActions(t *testing.T) {
	if !HasPublishedActionForResourceAction("clients", "update") {
		t.Fatal("HasPublishedActionForResourceAction(clients, update) = false, want true")
	}
	if !HasPublishedActionForResourceAction("webhooks", "create") {
		t.Fatal("HasPublishedActionForResourceAction(webhooks, create) = false, want true")
	}
	if HasPublishedActionForResourceAction("auth", "token") {
		t.Fatal("HasPublishedActionForResourceAction(auth, token) = true, want false")
	}
	if HasPublishedActionForResourceAction("node", "api") {
		t.Fatal("HasPublishedActionForResourceAction(node, api) = true, want false")
	}
}
