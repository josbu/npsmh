package api

import (
	"testing"

	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

func testAdminActor() *Actor {
	return AdminActor("admin")
}

func TestDiscoveryPayloadFiltersManagementPlatformsByActorScope(t *testing.T) {
	customServices := webservice.New()
	customServices.ManagementPlatforms = stubAppManagementPlatformStore{}
	customServices.ManagementPlatformRuntimeStatus.NoteConfigured("master-a", "dual", "wss://master-a.example/node/ws", true)
	customServices.ManagementPlatformRuntimeStatus.NoteConfigured("master-b", "dual", "wss://master-b.example/node/ws", true)
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{PlatformID: "master-a", Token: "secret-a", ControlScope: "account", ServiceUsername: "__platform_master-a", Enabled: true},
				{PlatformID: "master-b", Token: "secret-b", ControlScope: "account", ServiceUsername: "__platform_master-b", Enabled: true},
			},
		},
	}, Options{
		Services: &customServices,
	})

	if payload := app.managementDiscoveryPayload(stubAppContext{actor: AnonymousActor()}); len(payload.Extensions.Cluster.ManagementPlatforms) != 0 {
		t.Fatalf("anonymous discovery management platforms = %#v, want none", payload.Extensions.Cluster.ManagementPlatforms)
	}
	if payload := app.managementDiscoveryPayload(stubAppContext{actor: UserActor("operator", []int{101})}); len(payload.Extensions.Cluster.ManagementPlatforms) != 0 {
		t.Fatalf("user discovery management platforms = %#v, want none", payload.Extensions.Cluster.ManagementPlatforms)
	}

	platformActor := PlatformActor("master-a", "admin", "master-a-admin", "actor-a", false, nil, 0)
	payload := app.managementDiscoveryPayload(stubAppContext{actor: platformActor})
	if len(payload.Extensions.Cluster.ManagementPlatforms) != 1 {
		t.Fatalf("platform discovery management platforms = %#v, want one item", payload.Extensions.Cluster.ManagementPlatforms)
	}
	if payload.Extensions.Cluster.ManagementPlatforms[0].PlatformID != "master-a" {
		t.Fatalf("platform discovery platform_id = %q, want master-a", payload.Extensions.Cluster.ManagementPlatforms[0].PlatformID)
	}
}

func TestDiscoveryPayloadRoutesRespectVisibleActionsWhileKeepingSharedRealtimeRoutes(t *testing.T) {
	app := NewWithOptions(&servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
	}, Options{})

	payload := app.managementDiscoveryPayload(stubAppContext{actor: UserActor("operator", []int{101})})
	if payload.Routes.Overview == "" || payload.Routes.Clients == "" || payload.Routes.ClientsConnections == "" || payload.Routes.ClientsClear == "" || payload.Routes.UsageSnapshot == "" {
		t.Fatalf("user discovery routes = %#v, want overview/client scoped routes and usage snapshot", payload.Routes)
	}
	if payload.Routes.Dashboard != "" || payload.Routes.Status != "" || payload.Routes.SystemSync != "" || payload.Routes.SystemExport != "" {
		t.Fatalf("user discovery routes = %#v, want full-access system routes hidden", payload.Routes)
	}
	if payload.Routes.Changes == "" || payload.Routes.Batch == "" || payload.Routes.WebSocket == "" || payload.Routes.RealtimeSubscriptions == "" {
		t.Fatalf("user discovery routes = %#v, want shared realtime routes still present", payload.Routes)
	}
}
