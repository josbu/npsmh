package service

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func TestDefaultNodeControlServiceStatusFiltersScope(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101}
	localUser := &file.User{Id: 20, Username: "local-user", Kind: "local", Revision: 5, UpdatedAt: 102}
	clientA := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true}
	clientB := &file.Client{Id: 2, OwnerUserID: 20, VerifyKey: "vk-b", Revision: 8, UpdatedAt: 202, Status: true}
	tunnelA := &file.Tunnel{Id: 1, Client: clientA, Revision: 11, UpdatedAt: 301}
	hostA := &file.Host{Id: 1, Client: clientA, Revision: 12, UpdatedAt: 302}
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(serviceUser)
					fn(localUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
					fn(clientB)
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(tunnelA)
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					fn(hostA)
				},
			},
		},
	}

	payload, err := service.Status(NodeStatusInput{
		NodeID:                 "node-a",
		Scope:                  testNodePlatformAdminScope("master-a", 10),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 1},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus: func(platformID string) ManagementPlatformReverseRuntimeStatus {
			if platformID == "master-a" {
				return ManagementPlatformReverseRuntimeStatus{CallbackQueueSize: 2}
			}
			return ManagementPlatformReverseRuntimeStatus{}
		},
	})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if payload.Version != "test-version" {
		t.Fatalf("Version = %q, want test-version", payload.Version)
	}
	if len(payload.ManagementPlatforms) != 1 || payload.ManagementPlatforms[0].PlatformID != "master-a" {
		t.Fatalf("ManagementPlatforms = %+v, want only master-a", payload.ManagementPlatforms)
	}
	if payload.ManagementPlatforms[0].ServiceUsername != "actual-master-a" {
		t.Fatalf("ServiceUsername = %q, want actual-master-a", payload.ManagementPlatforms[0].ServiceUsername)
	}
	if payload.Counts.Users != 1 || payload.Counts.PlatformUsers != 1 {
		t.Fatalf("Counts.Users = %d, PlatformUsers = %d, want 1/1", payload.Counts.Users, payload.Counts.PlatformUsers)
	}
	if payload.Counts.Clients != 1 || payload.Counts.OnlineClients != 1 {
		t.Fatalf("Counts.Clients = %d, OnlineClients = %d, want 1/1", payload.Counts.Clients, payload.Counts.OnlineClients)
	}
	if payload.Counts.Tunnels != 1 || payload.Counts.Hosts != 1 {
		t.Fatalf("Counts.Tunnels = %d, Hosts = %d, want 1/1", payload.Counts.Tunnels, payload.Counts.Hosts)
	}
	if payload.Revisions.Max != 12 {
		t.Fatalf("Revisions.Max = %d, want 12", payload.Revisions.Max)
	}
	if payload.Protocol.ChangesWindow != testNodeControlConfig().Runtime.NodeEventLogSizeValue() || !payload.Protocol.ChangesDurable {
		t.Fatalf("Protocol = %+v, want config-driven durable replay window", payload.Protocol)
	}
	if payload.Protocol.TrafficReportIntervalSeconds != 1 || payload.Protocol.TrafficReportStepBytes != 10*1024*1024 {
		t.Fatalf("Protocol traffic reporting = %+v, want 1s / 10MiB", payload.Protocol)
	}
}

func TestDefaultNodeControlServiceCollectNodeScopeCountsSkipsDetailSlices(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101}
	clientA := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true}
	tunnelA := &file.Tunnel{Id: 1, Client: clientA, Revision: 11, UpdatedAt: 301}
	hostA := &file.Host{Id: 1, Client: clientA, Revision: 12, UpdatedAt: 302}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(serviceUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(tunnelA)
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					fn(hostA)
				},
			},
		},
	}

	countsOnly, err := service.collectNodeScopeCounts(testNodePlatformAdminScope("master-a", 10))
	if err != nil {
		t.Fatalf("collectNodeScopeCounts() error = %v", err)
	}
	withDetails, err := service.collectNodeScopeSnapshot(testNodePlatformAdminScope("master-a", 10))
	if err != nil {
		t.Fatalf("collectNodeScopeSnapshot() error = %v", err)
	}

	if countsOnly.Counts != withDetails.Counts {
		t.Fatalf("countsOnly.Counts = %+v, want %+v", countsOnly.Counts, withDetails.Counts)
	}
	if countsOnly.Revisions != withDetails.Revisions {
		t.Fatalf("countsOnly.Revisions = %+v, want %+v", countsOnly.Revisions, withDetails.Revisions)
	}
	if countsOnly.Users != nil || countsOnly.Clients != nil {
		t.Fatalf("countsOnly detail slices = users:%v clients:%v, want nil", countsOnly.Users, countsOnly.Clients)
	}
	if countsOnly.ClientTunnelCounts != nil || countsOnly.ClientHostCounts != nil {
		t.Fatalf("countsOnly per-client maps = tunnels:%v hosts:%v, want nil", countsOnly.ClientTunnelCounts, countsOnly.ClientHostCounts)
	}
	if len(countsOnly.ClientIDs) != 1 {
		t.Fatalf("countsOnly.ClientIDs = %v, want one visible client id", countsOnly.ClientIDs)
	}
	if len(withDetails.Users) != 1 || len(withDetails.Clients) != 1 {
		t.Fatalf("withDetails slices = users:%d clients:%d, want 1/1", len(withDetails.Users), len(withDetails.Clients))
	}
	if withDetails.ClientTunnelCounts[clientA.Id] != 1 || withDetails.ClientHostCounts[clientA.Id] != 1 {
		t.Fatalf("withDetails per-client counts = tunnels:%v hosts:%v, want 1/1", withDetails.ClientTunnelCounts, withDetails.ClientHostCounts)
	}
}

func TestDefaultNodeControlServiceRegistrationBuildsClusterSnapshot(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101}
	clientA := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true}
	tunnelA := &file.Tunnel{Id: 1, Client: clientA, Revision: 11, UpdatedAt: 301}
	hostA := &file.Host{Id: 1, Client: clientA, Revision: 12, UpdatedAt: 302}
	cfg := testNodeControlConfig()
	cfg.Runtime.ManagementPlatforms[0].ReverseEnabled = true
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(serviceUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					fn(tunnelA)
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					fn(hostA)
				},
			},
		},
	}

	payload, err := service.Registration(NodeRegistrationInput{
		NodeID:                 "node-a",
		Scope:                  testNodePlatformAdminScope("master-a", 10),
		Config:                 cfg,
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		ConfigEpoch:            "cfg-epoch-1",
		Operations:             NodeOperationPayload{LastSyncAt: 11, LastMutationAt: 22, LastErrorAt: 33, LastError: "boom"},
		Idempotency:            NodeIdempotencyPayload{CachedEntries: 4, Inflight: 2, ReplayHits: 8, Conflicts: 1},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus: func(platformID string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{ReverseConnected: true, CallbackQueueSize: 3}
		},
	})
	if err != nil {
		t.Fatalf("Registration() error = %v", err)
	}
	if payload.NodeID != "node-a" || payload.Version != "test-version" || !payload.EventsEnabled || !payload.CallbacksReady {
		t.Fatalf("Registration payload core fields = %+v", payload)
	}
	if payload.ConfigEpoch != "cfg-epoch-1" {
		t.Fatalf("ConfigEpoch = %q, want cfg-epoch-1", payload.ConfigEpoch)
	}
	if len(payload.ManagementPlatforms) != 1 || payload.ManagementPlatforms[0].PlatformID != "master-a" {
		t.Fatalf("ManagementPlatforms = %+v, want only master-a", payload.ManagementPlatforms)
	}
	if payload.Health.DirectEnabledPlatforms != 1 || payload.Health.ReverseEnabledPlatforms != 1 || payload.Health.ReverseConnectedPlatforms != 1 {
		t.Fatalf("Health platform counters = %+v, want 1/1/1", payload.Health)
	}
	if payload.Health.CallbackEnabledPlatforms != 1 || payload.Health.CallbackQueueBacklog != 3 {
		t.Fatalf("Health callback counters = %+v, want enabled=1 backlog=3", payload.Health)
	}
	if payload.Health.LastSyncAt != 11 || payload.Health.LastMutationAt != 22 || payload.Health.LastErrorAt != 33 || payload.Health.LastError != "boom" {
		t.Fatalf("Health operation fields = %+v", payload.Health)
	}
	if payload.Health.IdempotencyCachedEntries != 4 || payload.Health.IdempotencyInflight != 2 || payload.Health.IdempotencyReplayHits != 8 || payload.Health.IdempotencyConflicts != 1 {
		t.Fatalf("Health idempotency fields = %+v", payload.Health)
	}
	if payload.Counts.Clients != 1 || payload.Revisions.Max != 12 {
		t.Fatalf("Registration summary = counts:%+v revisions:%+v", payload.Counts, payload.Revisions)
	}
	if payload.Protocol.ChangesWindow != cfg.Runtime.NodeEventLogSizeValue() || !payload.Protocol.ChangesDurable {
		t.Fatalf("Registration protocol = %+v, want config-driven durable replay window", payload.Protocol)
	}
	if payload.Protocol.TrafficReportIntervalSeconds != 1 || payload.Protocol.TrafficReportStepBytes != 10*1024*1024 {
		t.Fatalf("Registration protocol traffic reporting = %+v, want 1s / 10MiB", payload.Protocol)
	}
}

func TestDefaultNodeControlServiceStatusAndRegistrationReturnFreshPayloadSlices(t *testing.T) {
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{},
		},
	}
	input := NodeStatusInput{
		NodeID:                 "node-a",
		Scope:                  testNodePlatformAdminScope("master-a", 10),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus: func(platformID string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{CallbackQueueSize: 2}
		},
	}

	statusPayload, err := service.Status(input)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if len(statusPayload.Capabilities) == 0 || len(statusPayload.ManagementPlatforms) == 0 {
		t.Fatalf("Status() payload = %+v, want capabilities and management platform entries", statusPayload)
	}
	statusPayload.Capabilities[0] = "mutated.capability"
	statusPayload.ManagementPlatforms[0].PlatformID = "mutated-platform"

	statusPayload2, err := service.Status(input)
	if err != nil {
		t.Fatalf("Status() second call error = %v", err)
	}
	if len(statusPayload2.Capabilities) == 0 || statusPayload2.Capabilities[0] == "mutated.capability" {
		t.Fatalf("Status() second payload capabilities = %v, want fresh request-local slice", statusPayload2.Capabilities)
	}
	if len(statusPayload2.ManagementPlatforms) == 0 || statusPayload2.ManagementPlatforms[0].PlatformID != "master-a" {
		t.Fatalf("Status() second payload management platforms = %+v, want original platform data", statusPayload2.ManagementPlatforms)
	}

	registrationPayload, err := service.Registration(NodeRegistrationInput{
		NodeID:                 input.NodeID,
		Scope:                  input.Scope,
		Config:                 input.Config,
		BootID:                 input.BootID,
		RuntimeStartedAt:       input.RuntimeStartedAt,
		LiveOnlyEvents:         input.LiveOnlyEvents,
		ResolveServiceUsername: input.ResolveServiceUsername,
		ReverseStatus:          input.ReverseStatus,
	})
	if err != nil {
		t.Fatalf("Registration() error = %v", err)
	}
	if len(registrationPayload.Capabilities) == 0 || len(registrationPayload.ManagementPlatforms) == 0 {
		t.Fatalf("Registration() payload = %+v, want capabilities and management platform entries", registrationPayload)
	}
	registrationPayload.Capabilities[0] = "mutated.registration.capability"
	registrationPayload.ManagementPlatforms[0].PlatformID = "mutated-registration-platform"

	registrationPayload2, err := service.Registration(NodeRegistrationInput{
		NodeID:                 input.NodeID,
		Scope:                  input.Scope,
		Config:                 input.Config,
		BootID:                 input.BootID,
		RuntimeStartedAt:       input.RuntimeStartedAt,
		LiveOnlyEvents:         input.LiveOnlyEvents,
		ResolveServiceUsername: input.ResolveServiceUsername,
		ReverseStatus:          input.ReverseStatus,
	})
	if err != nil {
		t.Fatalf("Registration() second call error = %v", err)
	}
	if len(registrationPayload2.Capabilities) == 0 || registrationPayload2.Capabilities[0] == "mutated.registration.capability" {
		t.Fatalf("Registration() second payload capabilities = %v, want fresh request-local slice", registrationPayload2.Capabilities)
	}
	if len(registrationPayload2.ManagementPlatforms) == 0 || registrationPayload2.ManagementPlatforms[0].PlatformID != "master-a" {
		t.Fatalf("Registration() second payload management platforms = %+v, want original platform data", registrationPayload2.ManagementPlatforms)
	}
}

func TestDefaultNodeControlServiceOverviewCombinesRegistrationUsageAndOptionalConfig(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101}
	clientA := &file.Client{
		Id:          1,
		OwnerUserID: 10,
		VerifyKey:   "vk-a",
		Revision:    7,
		UpdatedAt:   201,
		Flow:        &file.Flow{InletFlow: 11, ExportFlow: 22},
	}
	cfg := testNodeControlConfig()
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(serviceUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:                 "node-a",
		Principal:              testNodeFullAdminPrincipal(),
		Scope:                  testNodeFullAdminScope(),
		Config:                 cfg,
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 11},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus: func(platformID string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{CallbackQueueSize: 2}
		},
		IncludeConfig: true,
		Snapshot: func() (interface{}, error) {
			return map[string]interface{}{"mode": "local"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.NodeID != "node-a" || payload.Registration == nil || payload.UsageSnapshot == nil {
		t.Fatalf("Overview() payload = %+v, want registration and usage snapshot", payload)
	}
	if payload.Registration.Health.CallbackEnabledPlatforms == 0 || payload.Registration.Health.CallbackQueueBacklog == 0 {
		t.Fatalf("Registration health = %+v, want callback-enabled platforms and backlog", payload.Registration.Health)
	}
	if payload.UsageSnapshot.Summary.Clients != 1 {
		t.Fatalf("UsageSnapshot summary = %+v, want clients=1", payload.UsageSnapshot.Summary)
	}
	configPayload, ok := payload.Config.(map[string]interface{})
	if !ok || configPayload["mode"] != "local" {
		t.Fatalf("Config = %#v, want snapshot payload", payload.Config)
	}
}

func TestDefaultNodeControlServiceOverviewPassesHostToStatusDisplay(t *testing.T) {
	var bridgeDisplayHost string
	service := DefaultNodeControlService{
		System: stubSystemService{
			info: SystemInfo{Version: "test-version"},
			bridgeDisplay: func(cfg *servercfg.Snapshot, host string) BridgeDisplay {
				bridgeDisplayHost = host
				return BridgeDisplay{}
			},
		},
		Backend: Backend{
			Repository: stubRepository{},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:           "node-a",
		Host:             "console.example.com",
		Principal:        testNodeFullAdminPrincipal(),
		Scope:            testNodeFullAdminScope(),
		Config:           testNodeControlConfig(),
		BootID:           "boot-1",
		RuntimeStartedAt: 123,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration == nil {
		t.Fatalf("Overview() payload = %+v, want registration", payload)
	}
	if bridgeDisplayHost != "console.example.com" {
		t.Fatalf("BridgeDisplay() host = %q, want %q", bridgeDisplayHost, "console.example.com")
	}
}

func TestDefaultNodeControlServiceOverviewAllowsUsageOnlyScope(t *testing.T) {
	localUser := &file.User{Id: 20, Username: "local-user", Kind: "local"}
	clientA := &file.Client{Id: 1, OwnerUserID: 20, VerifyKey: "vk-a", Flow: &file.Flow{InletFlow: 11, ExportFlow: 22}}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(localUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID: "node-a",
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Roles:         []string{RoleUser},
			Permissions: []string{
				PermissionClientsRead,
				PermissionClientsUpdate,
			},
			Attributes: map[string]string{
				"user_id": "20",
			},
		},
		Scope: ResolveNodeAccessScope(Principal{
			Authenticated: true,
			Kind:          "user",
			Roles:         []string{RoleUser},
			Attributes: map[string]string{
				"user_id": "20",
			},
		}),
		Config: testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration != nil {
		t.Fatalf("Registration = %+v, want omitted for usage-only scope", payload.Registration)
	}
	if payload.UsageSnapshot == nil || payload.UsageSnapshot.Summary.Clients != 1 {
		t.Fatalf("UsageSnapshot = %+v, want visible client summary", payload.UsageSnapshot)
	}
}

func TestDefaultNodeControlServiceOverviewReusesScopedSnapshot(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101}
	clientA := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Flow: &file.Flow{InletFlow: 11, ExportFlow: 22}}
	tunnelA := &file.Tunnel{Id: 1, Client: clientA, Revision: 11, UpdatedAt: 301}
	hostA := &file.Host{Id: 1, Client: clientA, Revision: 12, UpdatedAt: 302}
	rangeUsersCalls := 0
	rangeClientsCalls := 0
	rangeTunnelsCalls := 0
	rangeHostsCalls := 0
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					rangeUsersCalls++
					fn(serviceUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					rangeClientsCalls++
					fn(clientA)
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					rangeTunnelsCalls++
					fn(tunnelA)
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					rangeHostsCalls++
					fn(hostA)
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:                 "node-a",
		Principal:              testNodeFullAdminPrincipal(),
		Scope:                  testNodeFullAdminScope(),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 11},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration == nil || payload.UsageSnapshot == nil {
		t.Fatalf("Overview() payload = %+v, want registration and usage snapshot", payload)
	}
	if rangeUsersCalls != 1 || rangeClientsCalls != 1 || rangeTunnelsCalls != 1 || rangeHostsCalls != 1 {
		t.Fatalf(
			"Overview() range calls = users:%d clients:%d tunnels:%d hosts:%d, want 1/1/1/1",
			rangeUsersCalls,
			rangeClientsCalls,
			rangeTunnelsCalls,
			rangeHostsCalls,
		)
	}
}

func TestDefaultNodeControlServiceOverviewSkipsTunnelAndHostScansWithoutVisibleClients(t *testing.T) {
	rangeUsersCalls := 0
	rangeClientsCalls := 0
	rangeTunnelsCalls := 0
	rangeHostsCalls := 0
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					rangeUsersCalls++
				},
				rangeClients: func(fn func(*file.Client) bool) {
					rangeClientsCalls++
				},
				rangeTunnels: func(fn func(*file.Tunnel) bool) {
					rangeTunnelsCalls++
				},
				rangeHosts: func(fn func(*file.Host) bool) {
					rangeHostsCalls++
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:                 "node-a",
		Principal:              testNodeFullAdminPrincipal(),
		Scope:                  testNodeFullAdminScope(),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 11},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return configured },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration == nil || payload.UsageSnapshot == nil {
		t.Fatalf("Overview() payload = %+v, want registration and usage snapshot", payload)
	}
	if rangeUsersCalls != 1 || rangeClientsCalls != 1 {
		t.Fatalf("Overview() user/client range calls = %d/%d, want 1/1", rangeUsersCalls, rangeClientsCalls)
	}
	if rangeTunnelsCalls != 0 || rangeHostsCalls != 0 {
		t.Fatalf("Overview() tunnel/host range calls = %d/%d, want 0/0 when no visible clients", rangeTunnelsCalls, rangeHostsCalls)
	}
}

func TestCollectNodeScopeClientsByIDsFallsBackOnlyForUnresolvedIDs(t *testing.T) {
	clientA := &file.Client{Id: 1, VerifyKey: "vk-a", Flow: &file.Flow{}}
	clientB := &file.Client{Id: 2, VerifyKey: "vk-b", Flow: &file.Flow{}}
	clientC := &file.Client{Id: 3, VerifyKey: "vk-c", Flow: &file.Flow{}}
	rangeCalls := 0
	snapshot := newNodeScopeSnapshot(nodeScopeSnapshotWithDetails)

	err := collectNodeScopeClientsByIDs(stubRepository{
		getClient: func(id int) (*file.Client, error) {
			switch id {
			case 1:
				return clientA, nil
			case 2:
				return nil, nil
			case 3:
				return nil, file.ErrClientNotFound
			default:
				t.Fatalf("GetClient(%d), want 1/2/3", id)
				return nil, nil
			}
		},
		rangeClients: func(fn func(*file.Client) bool) {
			rangeCalls++
			fn(clientA)
			fn(clientB)
			fn(clientC)
		},
	}, []int{1, 2, 3}, nil, &snapshot)
	if err != nil {
		t.Fatalf("collectNodeScopeClientsByIDs() error = %v", err)
	}

	if rangeCalls != 1 {
		t.Fatalf("RangeClients() calls = %d, want 1 targeted fallback pass", rangeCalls)
	}
	if snapshot.Counts.Clients != 3 {
		t.Fatalf("snapshot.Counts.Clients = %d, want 3 visible clients after unresolved-id fallback", snapshot.Counts.Clients)
	}
	if len(snapshot.Clients) != 3 {
		t.Fatalf("snapshot.Clients = %+v, want clients 1, 2, and 3 from direct + unresolved-id fallback", snapshot.Clients)
	}
	if _, ok := snapshot.ClientIDs[1]; !ok {
		t.Fatalf("snapshot.ClientIDs = %v, want client 1 from direct lookup", snapshot.ClientIDs)
	}
	if _, ok := snapshot.ClientIDs[2]; !ok {
		t.Fatalf("snapshot.ClientIDs = %v, want client 2 from fallback lookup", snapshot.ClientIDs)
	}
	if _, ok := snapshot.ClientIDs[3]; !ok {
		t.Fatalf("snapshot.ClientIDs = %v, want client 3 recovered from unresolved-id fallback", snapshot.ClientIDs)
	}
}

func TestCollectNodeScopeClientsByIDsFailsClosedOnLookupError(t *testing.T) {
	backendErr := errors.New("temporary lookup failure")
	snapshot := newNodeScopeSnapshot(nodeScopeSnapshotWithDetails)

	err := collectNodeScopeClientsByIDs(stubRepository{
		getClient: func(id int) (*file.Client, error) {
			if id != 2 {
				t.Fatalf("GetClient(%d), want 2", id)
			}
			return nil, backendErr
		},
		rangeClients: func(func(*file.Client) bool) {
			t.Fatal("RangeClients() should not be used after direct lookup error")
		},
	}, []int{2}, nil, &snapshot)
	if !errors.Is(err, backendErr) {
		t.Fatalf("collectNodeScopeClientsByIDs() error = %v, want %v", err, backendErr)
	}
}

func TestDefaultNodeControlServiceOverviewPlatformAdminUsesScopedLookups(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101, TotalFlow: &file.Flow{}}
	clientA := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true, Flow: &file.Flow{InletFlow: 11, ExportFlow: 22}}
	tunnelA := &file.Tunnel{Id: 1, Client: clientA, Revision: 11, UpdatedAt: 301}
	hostA := &file.Host{Id: 1, Client: clientA, Revision: 12, UpdatedAt: 302}
	rangeTunnelsByIDCalls := 0
	rangeHostsByIDCalls := 0
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for platform-admin scoped overview when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used for platform-admin scoped overview when GetClientsByUserID is available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when RangeTunnelsByClientID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when RangeHostsByClientID is available")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 10 {
						t.Fatalf("GetUser(%d), want 10", id)
					}
					return serviceUser, nil
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 10 {
						t.Fatalf("GetClientsByUserID(%d), want 10", userID)
					}
					return []*file.Client{clientA}, nil
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					rangeTunnelsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeTunnelsByClientID(%d), want 1", clientID)
					}
					fn(tunnelA)
				},
				rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
					rangeHostsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeHostsByClientID(%d), want 1", clientID)
					}
					fn(hostA)
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:                 "node-a",
		Principal:              testNodePlatformAdminPrincipal("master-a", 10),
		Scope:                  testNodePlatformAdminScope("master-a", 10),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 11},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration == nil || payload.UsageSnapshot == nil {
		t.Fatalf("Overview() payload = %+v, want registration and usage snapshot", payload)
	}
	if payload.Registration.Counts.Clients != 1 || payload.Registration.Counts.Tunnels != 1 || payload.Registration.Counts.Hosts != 1 {
		t.Fatalf("Registration counts = %+v, want clients/tunnels/hosts = 1/1/1", payload.Registration.Counts)
	}
	if payload.UsageSnapshot.Summary.Clients != 1 || len(payload.UsageSnapshot.Users) != 1 || len(payload.UsageSnapshot.Clients) != 1 {
		t.Fatalf("UsageSnapshot = %+v, want one visible user and client", payload.UsageSnapshot)
	}
	if rangeTunnelsByIDCalls != 1 || rangeHostsByIDCalls != 1 {
		t.Fatalf("per-client resource range calls = tunnels:%d hosts:%d, want 1/1", rangeTunnelsByIDCalls, rangeHostsByIDCalls)
	}
}

func TestDefaultNodeControlServiceOverviewPlatformAdminOwnedClientLookupFiltersNonOwnedClients(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101, TotalFlow: &file.Flow{}}
	ownedClient := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true, Flow: &file.Flow{InletFlow: 11, ExportFlow: 22}}
	managedClient := &file.Client{Id: 2, OwnerUserID: 30, ManagerUserIDs: []int{10}, VerifyKey: "vk-b", Revision: 8, UpdatedAt: 202, Status: true, IsConnect: true, Flow: &file.Flow{InletFlow: 7, ExportFlow: 9}}
	rangeTunnelsByIDCalls := 0
	rangeHostsByIDCalls := 0
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for platform-admin scoped overview when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used for platform-admin scoped overview when GetClientsByUserID is available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when RangeTunnelsByClientID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when RangeHostsByClientID is available")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 10 {
						t.Fatalf("GetUser(%d), want 10", id)
					}
					return serviceUser, nil
				},
				getClientsByUserID: func(userID int) ([]*file.Client, error) {
					if userID != 10 {
						t.Fatalf("GetClientsByUserID(%d), want 10", userID)
					}
					return []*file.Client{ownedClient, managedClient}, nil
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					rangeTunnelsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeTunnelsByClientID(%d), want only owned client 1", clientID)
					}
					fn(&file.Tunnel{Id: 11, Client: ownedClient})
				},
				rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
					rangeHostsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeHostsByClientID(%d), want only owned client 1", clientID)
					}
					fn(&file.Host{Id: 21, Client: ownedClient})
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:                 "node-a",
		Principal:              testNodePlatformAdminPrincipal("master-a", 10),
		Scope:                  testNodePlatformAdminScope("master-a", 10),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 11},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration == nil || payload.UsageSnapshot == nil {
		t.Fatalf("Overview() payload = %+v, want registration and usage snapshot", payload)
	}
	if payload.Registration.Counts.Clients != 1 || payload.Registration.Counts.Tunnels != 1 || payload.Registration.Counts.Hosts != 1 {
		t.Fatalf("Registration counts = %+v, want only owned client/tunnel/host", payload.Registration.Counts)
	}
	if payload.UsageSnapshot.Summary.Clients != 1 || len(payload.UsageSnapshot.Users) != 1 || len(payload.UsageSnapshot.Clients) != 1 {
		t.Fatalf("UsageSnapshot = %+v, want one owned user and client", payload.UsageSnapshot)
	}
	if rangeTunnelsByIDCalls != 1 || rangeHostsByIDCalls != 1 {
		t.Fatalf("per-client resource range calls = tunnels:%d hosts:%d, want 1/1", rangeTunnelsByIDCalls, rangeHostsByIDCalls)
	}
}

func TestDefaultNodeControlServiceOverviewPlatformAdminOwnedClientIDLookupFiltersNonOwnedClients(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101, TotalFlow: &file.Flow{}}
	ownedClient := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true, Flow: &file.Flow{InletFlow: 11, ExportFlow: 22}}
	managedClient := &file.Client{Id: 2, OwnerUserID: 30, ManagerUserIDs: []int{10}, VerifyKey: "vk-b", Revision: 8, UpdatedAt: 202, Status: true, IsConnect: true, Flow: &file.Flow{InletFlow: 7, ExportFlow: 9}}
	rangeTunnelsByIDCalls := 0
	rangeHostsByIDCalls := 0
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for platform-admin scoped overview when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used for platform-admin scoped overview when GetClient is available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when RangeTunnelsByClientID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when RangeHostsByClientID is available")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 10 {
						t.Fatalf("GetUser(%d), want 10", id)
					}
					return serviceUser, nil
				},
				getClientIDsByUserID: func(userID int) ([]int, error) {
					if userID != 10 {
						t.Fatalf("GetClientIDsByUserID(%d), want 10", userID)
					}
					return []int{1, 2}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					switch id {
					case 1:
						return ownedClient, nil
					case 2:
						return managedClient, nil
					default:
						t.Fatalf("GetClient(%d), want 1 or 2", id)
						return nil, nil
					}
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					rangeTunnelsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeTunnelsByClientID(%d), want only owned client 1", clientID)
					}
					fn(&file.Tunnel{Id: 11, Client: ownedClient})
				},
				rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
					rangeHostsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeHostsByClientID(%d), want only owned client 1", clientID)
					}
					fn(&file.Host{Id: 21, Client: ownedClient})
				},
			},
		},
	}

	payload, err := service.Overview(NodeOverviewInput{
		NodeID:                 "node-a",
		Principal:              testNodePlatformAdminPrincipal("master-a", 10),
		Scope:                  testNodePlatformAdminScope("master-a", 10),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 11},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if payload.Registration == nil || payload.UsageSnapshot == nil {
		t.Fatalf("Overview() payload = %+v, want registration and usage snapshot", payload)
	}
	if payload.Registration.Counts.Clients != 1 || payload.Registration.Counts.Tunnels != 1 || payload.Registration.Counts.Hosts != 1 {
		t.Fatalf("Registration counts = %+v, want only owned client/tunnel/host", payload.Registration.Counts)
	}
	if payload.UsageSnapshot.Summary.Clients != 1 || len(payload.UsageSnapshot.Users) != 1 || len(payload.UsageSnapshot.Clients) != 1 {
		t.Fatalf("UsageSnapshot = %+v, want one owned user and client", payload.UsageSnapshot)
	}
	if rangeTunnelsByIDCalls != 1 || rangeHostsByIDCalls != 1 {
		t.Fatalf("per-client resource range calls = tunnels:%d hosts:%d, want 1/1", rangeTunnelsByIDCalls, rangeHostsByIDCalls)
	}
}

func TestDefaultNodeControlServiceStatusPlatformUserUsesDirectClientLookups(t *testing.T) {
	serviceUser := &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true, Revision: 3, UpdatedAt: 101}
	clientA := &file.Client{Id: 1, OwnerUserID: 10, VerifyKey: "vk-a", Revision: 7, UpdatedAt: 201, Status: true, IsConnect: true, Flow: &file.Flow{}}
	tunnelA := &file.Tunnel{Id: 1, Client: clientA, Revision: 11, UpdatedAt: 301}
	hostA := &file.Host{Id: 1, Client: clientA, Revision: 12, UpdatedAt: 302}
	rangeTunnelsByIDCalls := 0
	rangeHostsByIDCalls := 0
	principal := Principal{
		Authenticated: true,
		Kind:          "platform_user",
		Roles:         []string{RoleUser},
		Permissions:   []string{PermissionClientsRead, PermissionClientsUpdate},
		ClientIDs:     []int{1},
		Attributes: map[string]string{
			"platform_id":              "master-a",
			"platform_service_user_id": "10",
		},
	}
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for platform-user scoped status when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used for platform-user scoped status when GetClient is available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when RangeTunnelsByClientID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when RangeHostsByClientID is available")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 10 {
						t.Fatalf("GetUser(%d), want 10", id)
					}
					return serviceUser, nil
				},
				getClient: func(id int) (*file.Client, error) {
					if id != 1 {
						return nil, file.ErrClientNotFound
					}
					return clientA, nil
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					rangeTunnelsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeTunnelsByClientID(%d), want 1", clientID)
					}
					fn(tunnelA)
				},
				rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
					rangeHostsByIDCalls++
					if clientID != 1 {
						t.Fatalf("RangeHostsByClientID(%d), want 1", clientID)
					}
					fn(hostA)
				},
			},
		},
	}

	payload, err := service.Status(NodeStatusInput{
		NodeID:                 "node-a",
		Scope:                  ResolveNodeAccessScope(principal),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 1},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if payload.Counts.Users != 1 || payload.Counts.Clients != 1 || payload.Counts.Tunnels != 1 || payload.Counts.Hosts != 1 {
		t.Fatalf("Status counts = %+v, want 1 visible user/client/tunnel/host", payload.Counts)
	}
	if rangeTunnelsByIDCalls != 1 || rangeHostsByIDCalls != 1 {
		t.Fatalf("per-client resource range calls = tunnels:%d hosts:%d, want 1/1", rangeTunnelsByIDCalls, rangeHostsByIDCalls)
	}
}

func TestDefaultNodeControlServiceStatusPlatformUserFailsClosedOnDirectClientLookupError(t *testing.T) {
	backendErr := errors.New("lookup backend down")
	service := DefaultNodeControlService{
		System: stubSystemService{info: SystemInfo{Version: "test-version"}},
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used after direct user lookup succeeds")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used after direct client lookup error")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used after direct client lookup error")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used after direct client lookup error")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 10 {
						t.Fatalf("GetUser(%d), want 10", id)
					}
					return &file.User{Id: 10, Username: "svc-master-a", Kind: "platform_service", Hidden: true}, nil
				},
				getClient: func(id int) (*file.Client, error) {
					if id != 1 {
						t.Fatalf("GetClient(%d), want 1", id)
					}
					return nil, backendErr
				},
			},
		},
	}
	principal := Principal{
		Authenticated: true,
		Kind:          "platform_user",
		Roles:         []string{RoleUser},
		Permissions:   []string{PermissionClientsRead, PermissionClientsUpdate},
		ClientIDs:     []int{1},
		Attributes: map[string]string{
			"platform_id":              "master-a",
			"platform_service_user_id": "10",
		},
	}

	_, err := service.Status(NodeStatusInput{
		NodeID:                 "node-a",
		Scope:                  ResolveNodeAccessScope(principal),
		Config:                 testNodeControlConfig(),
		BootID:                 "boot-1",
		RuntimeStartedAt:       123,
		Operations:             NodeOperationPayload{LastSyncAt: 1},
		Idempotency:            NodeIdempotencyPayload{TTLSeconds: 30},
		LiveOnlyEvents:         []string{"node.traffic.report"},
		ResolveServiceUsername: func(platformID, configured string) string { return "actual-" + platformID },
		ReverseStatus:          func(string) ManagementPlatformReverseRuntimeStatus { return ManagementPlatformReverseRuntimeStatus{} },
	})
	if !errors.Is(err, backendErr) {
		t.Fatalf("Status() error = %v, want %v", err, backendErr)
	}
}

func TestDefaultNodeControlServiceOperationsUsesScopedStore(t *testing.T) {
	store := NewInMemoryNodeOperationStore(10)
	store.Record(NodeOperationRecordPayload{
		OperationID:  "op-1",
		Kind:         "batch",
		Actor:        NodeOperationActorPayload{Kind: "platform_admin", SubjectID: "platform:master-a:ops", PlatformID: "master-a"},
		Count:        1,
		SuccessCount: 1,
	})
	service := DefaultNodeControlService{}

	payload, err := service.Operations(NodeOperationsInput{
		Scope:     testNodePlatformAdminScope("master-a", 10),
		SubjectID: "platform:master-a:ops",
		Store:     store,
	})
	if err != nil {
		t.Fatalf("Operations() error = %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].OperationID != "op-1" {
		t.Fatalf("Operations() items = %+v, want op-1", payload.Items)
	}

	_, err = service.Operations(NodeOperationsInput{
		Scope: ResolveNodeAccessScope(Principal{
			Authenticated: true,
			Kind:          "anonymous",
			Roles:         []string{RoleAnonymous},
		}),
		Store: store,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Operations() error = %v, want %v", err, ErrForbidden)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotScopedActorKeepsCompleteVisibleFields(t *testing.T) {
	serviceUser := &file.User{
		Id:        10,
		Username:  "svc-master-a",
		Kind:      "platform_service",
		Hidden:    true,
		TotalFlow: &file.Flow{InletFlow: 99, ExportFlow: 77},
	}
	localUser := &file.User{Id: 20, Username: "local-user", Kind: "local"}
	clientA := &file.Client{
		Id:             1,
		OwnerUserID:    10,
		VerifyKey:      "vk-a",
		ManagerUserIDs: []int{20},
		Flow:           &file.Flow{InletFlow: 11, ExportFlow: 22},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(fn func(*file.User) bool) {
					fn(serviceUser)
					fn(localUser)
				},
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
			},
		},
	}

	payload, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: testNodePlatformAdminPrincipal("master-a", 10),
		Scope:     testNodePlatformAdminScope("master-a", 10),
		Config:    testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("UsageSnapshot() error = %v", err)
	}
	if len(payload.Users) != 1 || payload.Users[0].ID != 10 {
		t.Fatalf("Users = %+v, want only service user", payload.Users)
	}
	if len(payload.Clients) != 1 || payload.Clients[0].ID != 1 {
		t.Fatalf("Clients = %+v, want only client 1", payload.Clients)
	}
	if !reflect.DeepEqual(payload.Clients[0].ManagerUserIDs, []int{20}) {
		t.Fatalf("ManagerUserIDs = %v, want full visible client metadata", payload.Clients[0].ManagerUserIDs)
	}
	if payload.Users[0].TotalInBytes != 99 || payload.Users[0].TotalOutBytes != 77 {
		t.Fatalf("User totals = %d/%d, want stored totals 99/77", payload.Users[0].TotalInBytes, payload.Users[0].TotalOutBytes)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotUserScopeUsesDirectUserLookupAndScopedResources(t *testing.T) {
	localUser := &file.User{Id: 20, Username: "local-user", Kind: "local", Status: 1, TotalFlow: &file.Flow{}}
	ownedClient := &file.Client{Id: 1, OwnerUserID: 20, VerifyKey: "vk-a", Flow: &file.Flow{InletFlow: 11, ExportFlow: 22}}
	managedClient := &file.Client{Id: 2, OwnerUserID: 30, ManagerUserIDs: []int{20}, VerifyKey: "vk-b", Flow: &file.Flow{InletFlow: 7, ExportFlow: 9}}
	getAllManagedCalls := 0
	getClientCalls := 0
	rangeTunnelsByIDCalls := 0
	rangeHostsByIDCalls := 0
	principal := Principal{
		Authenticated: true,
		Kind:          "user",
		Roles:         []string{RoleUser},
		Permissions:   []string{PermissionClientsRead, PermissionClientsUpdate},
		Attributes: map[string]string{
			"user_id": "20",
		},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for user-scoped usage snapshot when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used for user-scoped usage snapshot when indexed managed client ids and GetClient are available")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when RangeTunnelsByClientID is available")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when RangeHostsByClientID is available")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 20 {
						t.Fatalf("GetUser(%d), want 20", id)
					}
					return localUser, nil
				},
				getAllManagedClientIDs: func(userID int) ([]int, error) {
					getAllManagedCalls++
					if userID != 20 {
						t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 20", userID)
					}
					return []int{2, 1, 2}, nil
				},
				authoritativeAllManagedClientIDs: true,
				getClient: func(id int) (*file.Client, error) {
					getClientCalls++
					switch id {
					case 1:
						return ownedClient, nil
					case 2:
						return managedClient, nil
					default:
						t.Fatalf("GetClient(%d), want 1 or 2", id)
						return nil, nil
					}
				},
				rangeTunnelsByID: func(clientID int, fn func(*file.Tunnel) bool) {
					rangeTunnelsByIDCalls++
					switch clientID {
					case 1:
						fn(&file.Tunnel{Id: 11, Client: ownedClient})
					case 2:
						fn(&file.Tunnel{Id: 12, Client: managedClient})
					default:
						t.Fatalf("RangeTunnelsByClientID(%d), want 1 or 2", clientID)
					}
				},
				rangeHostsByID: func(clientID int, fn func(*file.Host) bool) {
					rangeHostsByIDCalls++
					switch clientID {
					case 1:
						fn(&file.Host{Id: 21, Client: ownedClient})
					case 2:
						fn(&file.Host{Id: 22, Client: managedClient})
					default:
						t.Fatalf("RangeHostsByClientID(%d), want 1 or 2", clientID)
					}
				},
			},
		},
	}

	payload, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: principal,
		Scope:     ResolveNodeAccessScope(principal),
		Config:    testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("UsageSnapshot() error = %v", err)
	}
	if len(payload.Users) != 1 || payload.Users[0].ID != 20 {
		t.Fatalf("Users = %+v, want only local user 20", payload.Users)
	}
	if len(payload.Clients) != 2 {
		t.Fatalf("Clients = %+v, want owned + managed visible clients", payload.Clients)
	}
	if payload.Summary.Clients != 2 || payload.Summary.Tunnels != 2 || payload.Summary.Hosts != 2 {
		t.Fatalf("Summary = %+v, want clients/tunnels/hosts = 2/2/2", payload.Summary)
	}
	if getAllManagedCalls != 1 {
		t.Fatalf("GetAllManagedClientIDsByUserID() calls = %d, want 1", getAllManagedCalls)
	}
	if getClientCalls != 2 {
		t.Fatalf("GetClient() calls = %d, want 2 direct client loads", getClientCalls)
	}
	if rangeTunnelsByIDCalls != 2 || rangeHostsByIDCalls != 2 {
		t.Fatalf("per-client resource range calls = tunnels:%d hosts:%d, want 2/2", rangeTunnelsByIDCalls, rangeHostsByIDCalls)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotUserScopeEmptyManagedClientIndexSkipsFallbackRanges(t *testing.T) {
	localUser := &file.User{Id: 20, Username: "local-user", Kind: "local", Status: 1, TotalFlow: &file.Flow{}}
	principal := Principal{
		Authenticated: true,
		Kind:          "user",
		Roles:         []string{RoleUser},
		Permissions:   []string{PermissionClientsRead},
		Attributes: map[string]string{
			"user_id": "20",
		},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for user-scoped usage snapshot when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used when indexed managed client ids resolve an empty scope")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used when no visible clients exist")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used when no visible clients exist")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 20 {
						t.Fatalf("GetUser(%d), want 20", id)
					}
					return localUser, nil
				},
				getAllManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 20 {
						t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 20", userID)
					}
					return nil, nil
				},
				authoritativeAllManagedClientIDs: true,
			},
		},
	}

	payload, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: principal,
		Scope:     ResolveNodeAccessScope(principal),
		Config:    testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("UsageSnapshot() error = %v", err)
	}
	if len(payload.Users) != 1 || payload.Users[0].ID != 20 {
		t.Fatalf("Users = %+v, want only local user 20", payload.Users)
	}
	if payload.Summary.Clients != 0 || len(payload.Clients) != 0 || payload.Summary.Tunnels != 0 || payload.Summary.Hosts != 0 {
		t.Fatalf("Summary/Clients = %+v / %+v, want empty visible client scope", payload.Summary, payload.Clients)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotUserScopeFailsClosedOnManagedClientLookupError(t *testing.T) {
	backendErr := errors.New("managed client index unavailable")
	localUser := &file.User{Id: 20, Username: "local-user", Kind: "local", Status: 1, TotalFlow: &file.Flow{}}
	principal := Principal{
		Authenticated: true,
		Kind:          "user",
		Roles:         []string{RoleUser},
		Permissions:   []string{PermissionClientsRead},
		Attributes: map[string]string{
			"user_id": "20",
		},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeUsers: func(func(*file.User) bool) {
					t.Fatal("RangeUsers() should not be used for user-scoped usage snapshot when GetUser is available")
				},
				rangeClients: func(func(*file.Client) bool) {
					t.Fatal("RangeClients() should not be used after managed client lookup error")
				},
				rangeTunnels: func(func(*file.Tunnel) bool) {
					t.Fatal("RangeTunnels() should not be used after managed client lookup error")
				},
				rangeHosts: func(func(*file.Host) bool) {
					t.Fatal("RangeHosts() should not be used after managed client lookup error")
				},
				getUser: func(id int) (*file.User, error) {
					if id != 20 {
						t.Fatalf("GetUser(%d), want 20", id)
					}
					return localUser, nil
				},
				getAllManagedClientIDs: func(userID int) ([]int, error) {
					if userID != 20 {
						t.Fatalf("GetAllManagedClientIDsByUserID(%d), want 20", userID)
					}
					return nil, backendErr
				},
				authoritativeAllManagedClientIDs: true,
			},
		},
	}

	_, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: principal,
		Scope:     ResolveNodeAccessScope(principal),
		Config:    testNodeControlConfig(),
	})
	if !errors.Is(err, backendErr) {
		t.Fatalf("UsageSnapshot() error = %v, want %v", err, backendErr)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotRedactsVerifyKeyWithoutClientUpdatePermission(t *testing.T) {
	clientA := &file.Client{
		Id:        1,
		VerifyKey: "vk-a",
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 22},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
			},
		},
	}

	principal := Principal{
		Authenticated: true,
		Kind:          "user",
		Roles:         []string{"restricted"},
		Permissions:   []string{PermissionClientsRead},
		ClientIDs:     []int{1},
	}
	payload, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: principal,
		Scope:     ResolveNodeAccessScope(principal),
		Config:    testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("UsageSnapshot() error = %v", err)
	}
	if len(payload.Clients) != 1 {
		t.Fatalf("Clients = %+v, want one client", payload.Clients)
	}
	if payload.Clients[0].VerifyKey != "" {
		t.Fatalf("VerifyKey = %q, want redacted", payload.Clients[0].VerifyKey)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotUsesCanonicalEntryACLRuleCountField(t *testing.T) {
	clientA := &file.Client{
		Id:            1,
		VerifyKey:     "vk-a",
		EntryAclRules: "10.0.0.1\n10.0.0.2\n10.0.0.1",
		Flow:          &file.Flow{},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
				},
			},
		},
	}

	payload, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: testNodeFullAdminPrincipal(),
		Scope:     testNodeFullAdminScope(),
		Config:    testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("UsageSnapshot() error = %v", err)
	}
	if len(payload.Clients) != 1 || payload.Clients[0].EntryACLRuleCount != 2 {
		t.Fatalf("Clients = %+v, want entry_acl_rule_count=2", payload.Clients)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	jsonText := string(raw)
	if !strings.Contains(jsonText, "\"entry_acl_rule_count\":2") {
		t.Fatalf("UsageSnapshot JSON = %s, want entry_acl_rule_count field", jsonText)
	}
	if strings.Contains(jsonText, "\"black_ip_count\"") {
		t.Fatalf("UsageSnapshot JSON = %s, want old black_ip_count field removed", jsonText)
	}
}

func TestDefaultNodeControlServiceUsageSnapshotSummaryAggregatesClientTotalsOnce(t *testing.T) {
	clientA := &file.Client{
		Id:        1,
		VerifyKey: "vk-a",
		Flow:      &file.Flow{InletFlow: 11, ExportFlow: 22},
	}
	clientB := &file.Client{
		Id:        2,
		VerifyKey: "vk-b",
		Flow:      &file.Flow{InletFlow: 7, ExportFlow: 9},
	}
	service := DefaultNodeControlService{
		Backend: Backend{
			Repository: stubRepository{
				rangeClients: func(fn func(*file.Client) bool) {
					fn(clientA)
					fn(clientB)
				},
			},
		},
	}

	payload, err := service.UsageSnapshot(NodeUsageSnapshotInput{
		NodeID:    "node-a",
		Principal: testNodeFullAdminPrincipal(),
		Scope:     testNodeFullAdminScope(),
		Config:    testNodeControlConfig(),
	})
	if err != nil {
		t.Fatalf("UsageSnapshot() error = %v", err)
	}
	if payload.Summary.TotalInBytes != 18 || payload.Summary.TotalOutBytes != 31 {
		t.Fatalf("UsageSnapshot summary totals = %d/%d, want 18/31", payload.Summary.TotalInBytes, payload.Summary.TotalOutBytes)
	}
	if len(payload.Clients) != 2 {
		t.Fatalf("Clients = %+v, want 2 clients", payload.Clients)
	}
}
