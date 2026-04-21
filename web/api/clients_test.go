package api

import (
	"testing"

	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

type managementClientRuntimeStub struct {
	clientConnectionCount func(int) int
}

func (managementClientRuntimeStub) DashboardData(bool) map[string]interface{} { return nil }
func (managementClientRuntimeStub) ListClients(int, int, string, string, string, int) ([]*file.Client, int) {
	return nil, 0
}
func (managementClientRuntimeStub) ListVisibleTunnels(int, int, string, []int, string, string, string) ([]*file.Tunnel, int) {
	return nil, 0
}
func (managementClientRuntimeStub) PingClient(int, string) int { return 0 }
func (s managementClientRuntimeStub) ClientConnectionCount(id int) int {
	if s.clientConnectionCount != nil {
		return s.clientConnectionCount(id)
	}
	return 0
}
func (managementClientRuntimeStub) ListClientConnections(int) []webservice.ClientConnection {
	return nil
}
func (managementClientRuntimeStub) DisconnectClient(int)                  {}
func (managementClientRuntimeStub) DeleteClientResources(int)             {}
func (managementClientRuntimeStub) GenerateTunnelPort(*file.Tunnel) int   { return 0 }
func (managementClientRuntimeStub) TunnelPortAvailable(*file.Tunnel) bool { return true }
func (managementClientRuntimeStub) TunnelRunning(int) bool                { return false }
func (managementClientRuntimeStub) ListTunnels(int, int, string, int, string, string, string) ([]*file.Tunnel, int) {
	return nil, 0
}
func (managementClientRuntimeStub) ListVisibleHosts(int, int, []int, string, string, string) ([]*file.Host, int) {
	return nil, 0
}
func (managementClientRuntimeStub) AddTunnel(*file.Tunnel) error { return nil }
func (managementClientRuntimeStub) StopTunnel(int) error         { return nil }
func (managementClientRuntimeStub) StartTunnel(int) error        { return nil }
func (managementClientRuntimeStub) DeleteTunnel(int) error       { return nil }
func (managementClientRuntimeStub) ListHosts(int, int, int, string, string, string) ([]*file.Host, int) {
	return nil, 0
}
func (managementClientRuntimeStub) RemoveHostCache(int) {}

func TestNodeClientResourceUsesRuntimeConnectionCount(t *testing.T) {
	app := &App{
		Services: webservice.BindDefaultServices(webservice.Services{
			Backend: webservice.Backend{
				Runtime: managementClientRuntimeStub{
					clientConnectionCount: func(id int) int {
						if id != 17 {
							t.Fatalf("ClientConnectionCount(%d), want 17", id)
						}
						return 3
					},
				},
			},
		}, nil),
	}

	payload := app.nodeClientResource(AdminActor("root"), &file.Client{
		Id:     17,
		Status: true,
		Cnf:    &file.Config{},
		Flow:   &file.Flow{},
	})

	if payload.ConnectionCount != 3 {
		t.Fatalf("payload.ConnectionCount = %d, want 3", payload.ConnectionCount)
	}
}

func TestNodeTunnelResourceUsesEmbeddedRuntimeConnectionCount(t *testing.T) {
	app := &App{
		Services: webservice.BindDefaultServices(webservice.Services{
			Backend: webservice.Backend{
				Runtime: managementClientRuntimeStub{
					clientConnectionCount: func(id int) int {
						if id != 23 {
							t.Fatalf("ClientConnectionCount(%d), want 23", id)
						}
						return 5
					},
				},
			},
		}, nil),
	}

	payload := app.nodeTunnelResource(&file.Tunnel{
		Id:   101,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:        23,
			VerifyKey: "secret-vkey",
			Cnf:       &file.Config{U: "demo-user"},
			Flow:      &file.Flow{},
		},
	})

	if payload.Client == nil {
		t.Fatal("payload.Client = nil, want embedded client")
	}
	if payload.Client.ConnectionCount != 5 {
		t.Fatalf("payload.Client.ConnectionCount = %d, want 5", payload.Client.ConnectionCount)
	}
	if payload.Client.VerifyKey != "" {
		t.Fatalf("payload.Client.VerifyKey = %q, want empty", payload.Client.VerifyKey)
	}
	if payload.Client.Config.User != "" {
		t.Fatalf("payload.Client.Config.User = %q, want empty", payload.Client.Config.User)
	}
}

func TestNodeHostResourceUsesEmbeddedRuntimeConnectionCount(t *testing.T) {
	app := &App{
		Services: webservice.BindDefaultServices(webservice.Services{
			Backend: webservice.Backend{
				Runtime: managementClientRuntimeStub{
					clientConnectionCount: func(id int) int {
						if id != 29 {
							t.Fatalf("ClientConnectionCount(%d), want 29", id)
						}
						return 7
					},
				},
			},
		}, nil),
	}

	payload := app.nodeHostResource(&file.Host{
		Id:   202,
		Flow: &file.Flow{},
		Client: &file.Client{
			Id:        29,
			VerifyKey: "secret-vkey",
			Cnf:       &file.Config{U: "demo-user"},
			Flow:      &file.Flow{},
		},
	})

	if payload.Client == nil {
		t.Fatal("payload.Client = nil, want embedded client")
	}
	if payload.Client.ConnectionCount != 7 {
		t.Fatalf("payload.Client.ConnectionCount = %d, want 7", payload.Client.ConnectionCount)
	}
	if payload.Client.VerifyKey != "" {
		t.Fatalf("payload.Client.VerifyKey = %q, want empty", payload.Client.VerifyKey)
	}
	if payload.Client.Config.User != "" {
		t.Fatalf("payload.Client.Config.User = %q, want empty", payload.Client.Config.User)
	}
}

func TestClientConnectionHelpersIgnoreTypedNilRuntime(t *testing.T) {
	var runtime *managementClientRuntimeStub

	app := &App{
		Services: webservice.Services{
			Backend: webservice.Backend{
				Repository: webservice.DefaultBackend().Repository,
				Runtime:    runtime,
			},
		},
	}

	if got := app.clientConnectionCount(17); got != 0 {
		t.Fatalf("clientConnectionCount() = %d, want 0 for typed nil runtime", got)
	}
	if connections := app.clientConnections(17); connections != nil {
		t.Fatalf("clientConnections() = %#v, want nil for typed nil runtime", connections)
	}
}

func TestNewNodeClientConnectionsIncludesRuntimeInstanceFields(t *testing.T) {
	items := newNodeClientConnections([]webservice.ClientConnection{
		{
			UUID:                   "node-a",
			Version:                "1.2.3",
			BaseVer:                6,
			RemoteAddr:             "198.51.100.10:12345",
			LocalAddr:              "10.0.0.8:8024",
			HasSignal:              true,
			HasTunnel:              true,
			Online:                 true,
			ConnectedAt:            1_700_000_000_000_000_000,
			NowConn:                4,
			BridgeInBytes:          10,
			BridgeOutBytes:         20,
			BridgeTotalBytes:       30,
			ServiceInBytes:         40,
			ServiceOutBytes:        50,
			ServiceTotalBytes:      90,
			TotalInBytes:           50,
			TotalOutBytes:          70,
			TotalBytes:             120,
			BridgeNowRateInBps:     100,
			BridgeNowRateOutBps:    200,
			BridgeNowRateTotalBps:  300,
			ServiceNowRateInBps:    400,
			ServiceNowRateOutBps:   500,
			ServiceNowRateTotalBps: 900,
			TotalNowRateInBps:      500,
			TotalNowRateOutBps:     700,
			TotalNowRateTotalBps:   1_200,
		},
	})
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item.UUID != "node-a" || item.RemoteAddr != "198.51.100.10:12345" || item.LocalAddr != "10.0.0.8:8024" {
		t.Fatalf("runtime instance addresses = %#v, want populated uuid/remote/local fields", item)
	}
	if !item.HasSignal || !item.HasTunnel || !item.IsOnline {
		t.Fatalf("runtime instance state = %#v, want online signal+tunnel", item)
	}
	if item.NowConn != 4 || item.TotalBytes != 120 || item.TotalNowRateTotalBps != 1_200 {
		t.Fatalf("runtime instance counters = %#v, want copied totals and rates", item)
	}
	if item.ConnectedAtText == "" {
		t.Fatal("runtime instance should materialize connected_at_text")
	}
}

func TestNewNodeClientResourceUsesEffectiveOwnerLifecycle(t *testing.T) {
	owner := &file.User{
		Id:        5,
		ExpireAt:  1_900_000_000,
		FlowLimit: 32 * 1024 * 1024,
		TotalFlow: &file.Flow{},
	}
	client := &file.Client{
		Id:          17,
		OwnerUserID: owner.Id,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	client.BindOwnerUser(owner)

	payload := newNodeClientResource(webservice.CloneClientSnapshot(client))
	if payload.ExpireAt != owner.ExpireAt {
		t.Fatalf("payload.ExpireAt = %d, want owner expire_at %d", payload.ExpireAt, owner.ExpireAt)
	}
	if payload.FlowLimitTotalBytes != owner.FlowLimit {
		t.Fatalf("payload.FlowLimitTotalBytes = %d, want owner flow_limit %d", payload.FlowLimitTotalBytes, owner.FlowLimit)
	}
}
