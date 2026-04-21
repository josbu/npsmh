package api

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

func TestVisibleActionEntriesRespectScopeSpecificManagementActions(t *testing.T) {
	specs := ProtectedActionCatalog(&App{})
	authz := webservice.DefaultAuthorizationService{}
	callbackCfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{PlatformID: "platform-a", Token: "token-a", Enabled: true, CallbackEnabled: true, CallbackURL: "https://example.com/callback"},
			},
		},
	}

	userEntries := VisibleActionEntries(callbackCfg, "", UserActor("operator", []int{101}), authz, specs)
	userActions := actionEntryIndex(userEntries)
	if !userActions["system/overview"] || !userActions["system/usage_snapshot"] {
		t.Fatalf("user discovery should expose overview and usage snapshot, got %v", userActions)
	}
	if userActions["system/dashboard"] || userActions["system/status"] || userActions["system/sync"] {
		t.Fatalf("user discovery should not expose full-access system actions, got %v", userActions)
	}
	if !userActions["clients/kick"] || !userActions["clients/clear_all"] {
		t.Fatalf("user discovery should expose permitted client operations, got %v", userActions)
	}
	if !userActions["clients/connections"] {
		t.Fatalf("user discovery should expose client connections read route, got %v", userActions)
	}
	if userActions["callbacks_queue/list"] || userActions["webhooks/list"] {
		t.Fatalf("user discovery should not expose callback queue or webhook admin actions, got %v", userActions)
	}

	clientEntries := VisibleActionEntries(callbackCfg, "", &Actor{
		Kind:      "client",
		SubjectID: "client:vkey:101",
		Username:  "vkey-client-101",
		ClientIDs: []int{101},
		Roles:     []string{webservice.RoleClient},
	}, authz, specs)
	clientActions := actionEntryIndex(clientEntries)
	if !clientActions["system/overview"] || !clientActions["system/usage_snapshot"] {
		t.Fatalf("client discovery should expose overview and usage snapshot, got %v", clientActions)
	}
	if clientActions["clients/kick"] || clientActions["clients/clear_all"] || clientActions["system/status"] {
		t.Fatalf("client discovery should not expose operator-only actions, got %v", clientActions)
	}
	if !clientActions["clients/connections"] {
		t.Fatalf("client discovery should expose client connections read route, got %v", clientActions)
	}

	platformEntries := VisibleActionEntries(callbackCfg, "", PlatformActor("platform-a", "admin", "platform-admin", "actor-1", false, []int{201}, 9001), authz, specs)
	platformActions := actionEntryIndex(platformEntries)
	if !platformActions["system/status"] || !platformActions["callbacks_queue/list"] || !platformActions["callbacks_queue/replay"] || !platformActions["webhooks/list"] || !platformActions["webhooks/create"] {
		t.Fatalf("platform admin discovery should expose account-wide management actions, got %v", platformActions)
	}
	if platformActions["system/dashboard"] || platformActions["system/sync"] || platformActions["system/export"] || platformActions["traffic/write"] {
		t.Fatalf("platform admin discovery should not expose full-only actions, got %v", platformActions)
	}

	noCallbackEntries := VisibleActionEntries(&servercfg.Snapshot{}, "", PlatformActor("platform-a", "admin", "platform-admin", "actor-1", false, []int{201}, 9001), authz, specs)
	noCallbackActions := actionEntryIndex(noCallbackEntries)
	if noCallbackActions["callbacks_queue/list"] || noCallbackActions["callbacks_queue/replay"] || noCallbackActions["callbacks_queue/clear"] {
		t.Fatalf("platform admin discovery should hide callback queue actions when no callback-enabled platform is visible, got %v", noCallbackActions)
	}
}

func TestVisibleActionEntriesUseNormalizedActorForVisibility(t *testing.T) {
	specs := ProtectedActionCatalog(&App{})
	authz := webservice.DefaultAuthorizationService{
		Resolver: mappedPermissionResolver{
			normalizePrincipal: func(principal webservice.Principal) webservice.Principal {
				if principal.Username == "mapped-platform" {
					principal.Authenticated = true
					principal.Kind = "platform_admin"
					principal.ClientIDs = []int{201}
					principal.Attributes = map[string]string{
						"platform_id":              "platform-a",
						"platform_service_user_id": "9001",
					}
				}
				return principal
			},
		},
	}
	callbackCfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{PlatformID: "platform-a", Token: "token-a", Enabled: true, CallbackEnabled: true, CallbackURL: "https://example.com/callback"},
			},
		},
	}

	entries := VisibleActionEntries(callbackCfg, "", &Actor{
		Kind:     "user",
		Username: "mapped-platform",
	}, authz, specs)
	actions := actionEntryIndex(entries)
	if !actions["callbacks_queue/list"] || !actions["webhooks/list"] {
		t.Fatalf("VisibleActionEntries() should use normalized actor visibility, got %v", actions)
	}
}

func actionEntryIndex(entries []ActionEntry) map[string]bool {
	index := make(map[string]bool, len(entries))
	for _, entry := range entries {
		index[entry.Resource+"/"+entry.Action] = true
	}
	return index
}
