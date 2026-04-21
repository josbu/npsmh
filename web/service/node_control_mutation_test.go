package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

func TestDefaultNodeControlServiceCallbackQueueScopeAndMutation(t *testing.T) {
	service := DefaultNodeControlService{}
	sizeByPlatform := map[string]int{
		"master-a": 2,
		"master-b": 4,
	}

	payload, err := service.QueryCallbackQueue(NodeCallbackQueueQueryInput{
		Scope:               testNodePlatformAdminScope("master-a", 10),
		Config:              testNodeControlConfig(),
		RequestedPlatformID: "",
		Limit:               50,
		ListItems: func(platformID string, limit int) []NodeCallbackQueueItemPayload {
			if platformID != "master-a" {
				return nil
			}
			return []NodeCallbackQueueItemPayload{{ID: 1, EventName: "node.config.sync"}}
		},
		QueueSize: func(platformID string) int {
			return sizeByPlatform[platformID]
		},
		ReverseStatus: func(platformID string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{CallbackDropped: int64(sizeByPlatform[platformID])}
		},
	})
	if err != nil {
		t.Fatalf("QueryCallbackQueue() error = %v", err)
	}
	if len(payload.Platforms) != 1 || payload.Platforms[0].PlatformID != "master-a" {
		t.Fatalf("Platforms = %+v, want only master-a", payload.Platforms)
	}
	if payload.Platforms[0].CallbackQueueSize != 2 || len(payload.Platforms[0].Items) != 1 {
		t.Fatalf("Platform payload = %+v, want queue size 2 and 1 item", payload.Platforms[0])
	}

	mutation, err := service.MutateCallbackQueue(NodeCallbackQueueMutationInput{
		Scope:               testNodePlatformAdminScope("master-a", 10),
		Config:              testNodeControlConfig(),
		Action:              "clear",
		RequestedPlatformID: "",
		QueueSize: func(platformID string) int {
			return sizeByPlatform[platformID]
		},
		Clear: func(platformID string) int {
			cleared := sizeByPlatform[platformID]
			sizeByPlatform[platformID] = 0
			return cleared
		},
	})
	if err != nil {
		t.Fatalf("MutateCallbackQueue() error = %v", err)
	}
	if len(mutation.Platforms) != 1 || mutation.Platforms[0].Cleared != 2 || mutation.Platforms[0].CallbackQueueSize != 0 {
		t.Fatalf("Mutation payload = %+v, want cleared master-a queue", mutation.Platforms)
	}

	_, err = service.QueryCallbackQueue(NodeCallbackQueueQueryInput{
		Scope:  ResolveNodeAccessScope(Principal{Authenticated: true, Kind: "user", Roles: []string{RoleUser}, Attributes: map[string]string{"user_id": "20"}}),
		Config: testNodeControlConfig(),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("QueryCallbackQueue() error = %v, want %v", err, ErrForbidden)
	}

	_, err = service.QueryCallbackQueue(NodeCallbackQueueQueryInput{
		Scope:               testNodeFullAdminScope(),
		Config:              testNodeControlConfig(),
		RequestedPlatformID: "missing-platform",
	})
	if !errors.Is(err, ErrManagementPlatformNotFound) {
		t.Fatalf("QueryCallbackQueue() invalid platform error = %v, want %v", err, ErrManagementPlatformNotFound)
	}

	_, err = service.MutateCallbackQueue(NodeCallbackQueueMutationInput{
		Scope:               testNodeFullAdminScope(),
		Config:              testNodeControlConfig(),
		Action:              "clear",
		RequestedPlatformID: "missing-platform",
	})
	if !errors.Is(err, ErrManagementPlatformNotFound) {
		t.Fatalf("MutateCallbackQueue() invalid platform error = %v, want %v", err, ErrManagementPlatformNotFound)
	}

	clearCalls := 0
	replayCalls := 0
	_, err = service.MutateCallbackQueue(NodeCallbackQueueMutationInput{
		Scope:  testNodeFullAdminScope(),
		Config: testNodeControlConfig(),
		Action: "purge",
		Clear: func(string) int {
			clearCalls++
			return 1
		},
		NotifyReplay: func(string) int {
			replayCalls++
			return 1
		},
	})
	if !errors.Is(err, ErrInvalidCallbackQueueAction) {
		t.Fatalf("MutateCallbackQueue() invalid action error = %v, want %v", err, ErrInvalidCallbackQueueAction)
	}
	if clearCalls != 0 || replayCalls != 0 {
		t.Fatalf("MutateCallbackQueue() side effects = clear:%d replay:%d, want 0/0 on invalid action", clearCalls, replayCalls)
	}
}

func TestDefaultNodeControlServiceQueryCallbackQueueUsesReverseStatusQueueSizeFallback(t *testing.T) {
	service := DefaultNodeControlService{}

	payload, err := service.QueryCallbackQueue(NodeCallbackQueueQueryInput{
		Scope:  testNodePlatformAdminScope("master-a", 10),
		Config: testNodeControlConfig(),
		ReverseStatus: func(platformID string) ManagementPlatformReverseRuntimeStatus {
			if platformID != "master-a" {
				return ManagementPlatformReverseRuntimeStatus{}
			}
			return ManagementPlatformReverseRuntimeStatus{
				CallbackQueueSize:      7,
				CallbackDropped:        2,
				LastCallbackQueuedAt:   11,
				LastCallbackReplayAt:   13,
				LastCallbackStatusCode: 202,
			}
		},
	})
	if err != nil {
		t.Fatalf("QueryCallbackQueue() error = %v", err)
	}
	if len(payload.Platforms) != 1 {
		t.Fatalf("Platforms = %+v, want one visible platform", payload.Platforms)
	}
	if payload.Platforms[0].CallbackQueueSize != 7 {
		t.Fatalf("CallbackQueueSize = %d, want 7 from reverse status fallback", payload.Platforms[0].CallbackQueueSize)
	}
	if payload.Platforms[0].CallbackDropped != 2 || payload.Platforms[0].LastCallbackQueuedAt != 11 || payload.Platforms[0].LastCallbackReplayAt != 13 {
		t.Fatalf("Platform payload = %+v, want reverse status fields propagated", payload.Platforms[0])
	}
}

func TestDefaultNodeControlServiceQueryCallbackQueueSkipsInvalidCallbackPlatforms(t *testing.T) {
	service := DefaultNodeControlService{}
	cfg := &servercfg.Snapshot{
		Runtime: servercfg.RuntimeConfig{
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{PlatformID: "valid", Token: "token-valid", Enabled: true, CallbackEnabled: true, CallbackURL: "https://valid.example/callback", CallbackQueueMax: 4},
				{PlatformID: "missing-token", Token: "   ", Enabled: true, CallbackEnabled: true, CallbackURL: "https://missing-token.example/callback", CallbackQueueMax: 4},
				{PlatformID: "disabled", Token: "token-disabled", Enabled: false, CallbackEnabled: true, CallbackURL: "https://disabled.example/callback", CallbackQueueMax: 4},
				{PlatformID: "no-callback", Token: "token-no-callback", Enabled: true, CallbackEnabled: false, CallbackURL: "https://no-callback.example/callback", CallbackQueueMax: 4},
			},
		},
	}

	payload, err := service.QueryCallbackQueue(NodeCallbackQueueQueryInput{
		Scope:  testNodeFullAdminScope(),
		Config: cfg,
	})
	if err != nil {
		t.Fatalf("QueryCallbackQueue() error = %v", err)
	}
	if len(payload.Platforms) != 1 || payload.Platforms[0].PlatformID != "valid" {
		t.Fatalf("Platforms = %+v, want only valid callback platform", payload.Platforms)
	}
}

func TestDefaultNodeControlServiceMutateCallbackQueueClearReadsQueueSizeAfterClearOnly(t *testing.T) {
	service := DefaultNodeControlService{}
	queueSizeCalls := 0
	queueSize := 3

	payload, err := service.MutateCallbackQueue(NodeCallbackQueueMutationInput{
		Scope:  testNodePlatformAdminScope("master-a", 10),
		Config: testNodeControlConfig(),
		Action: "clear",
		QueueSize: func(platformID string) int {
			queueSizeCalls++
			return queueSize
		},
		Clear: func(platformID string) int {
			cleared := queueSize
			queueSize = 0
			return cleared
		},
	})
	if err != nil {
		t.Fatalf("MutateCallbackQueue() error = %v", err)
	}
	if len(payload.Platforms) != 1 {
		t.Fatalf("Platforms = %+v, want one visible platform", payload.Platforms)
	}
	if payload.Platforms[0].Cleared != 3 || payload.Platforms[0].CallbackQueueSize != 0 {
		t.Fatalf("Mutation payload = %+v, want cleared=3 queue=0", payload.Platforms[0])
	}
	if queueSizeCalls != 1 {
		t.Fatalf("QueueSize() call count = %d, want 1 after clear", queueSizeCalls)
	}
}

func TestDefaultNodeControlServiceKickAppliesMutationAndFlushes(t *testing.T) {
	client := &file.Client{Id: 1, VerifyKey: "vk-1", OwnerUserID: 7, Status: true, IsConnect: true}
	disconnected := 0
	flushed := 0
	var saved *file.Client
	service := DefaultNodeControlService{
		Authz: DefaultAuthorizationService{},
	}

	result, err := service.Kick(NodeKickInput{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Roles:         []string{RoleUser},
			Permissions:   []string{PermissionClientsStatus},
			ClientIDs:     []int{1},
			Attributes:    map[string]string{"user_id": "7"},
		},
		Target:           NodeClientTarget{ClientID: 1},
		SourceType:       "node_user",
		SourcePlatformID: "master-a",
		SourceActorID:    "user:7",
		ResolveClient:    func(NodeClientTarget) (*file.Client, error) { return client, nil },
		SaveClient: func(updated *file.Client) error {
			saved = updated
			return nil
		},
		DisconnectClient: func(id int) { disconnected = id },
		FlushStore:       func() error { flushed++; return nil },
	})
	if err != nil {
		t.Fatalf("Kick() error = %v", err)
	}
	if result.Client == client {
		t.Fatal("Kick() should return committed working copy, not original live client")
	}
	if saved == nil || saved != result.Client {
		t.Fatalf("Kick() saved client = %#v, result = %#v, want same committed client", saved, result.Client)
	}
	if result.Client.Status || result.Client.IsConnect {
		t.Fatalf("result client after Kick() = %+v, want disconnected and disabled", result.Client)
	}
	if !client.Status || !client.IsConnect {
		t.Fatalf("original client after Kick() = %+v, want unchanged live object", client)
	}
	if disconnected != 1 || flushed != 1 {
		t.Fatalf("disconnect/flushed = %d/%d, want 1/1", disconnected, flushed)
	}
	if result.Client.SourceType != "node_user" || result.Client.SourcePlatformID != "master-a" || result.Client.SourceActorID != "user:7" {
		t.Fatalf("result client source meta = %q/%q/%q", result.Client.SourceType, result.Client.SourcePlatformID, result.Client.SourceActorID)
	}
}

func TestDefaultNodeControlServiceKickWithoutSaveClientDoesNotMutateResolvedClient(t *testing.T) {
	client := &file.Client{Id: 1, VerifyKey: "vk-1", Status: true, IsConnect: true, Cnf: &file.Config{}, Flow: &file.Flow{}}
	disconnected := 0
	service := DefaultNodeControlService{
		Authz: DefaultAuthorizationService{},
	}

	result, err := service.Kick(NodeKickInput{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Roles:         []string{RoleUser},
			Permissions:   []string{PermissionClientsStatus},
			ClientIDs:     []int{1},
			Attributes:    map[string]string{"user_id": "7"},
		},
		Target:           NodeClientTarget{ClientID: 1},
		ResolveClient:    func(NodeClientTarget) (*file.Client, error) { return client, nil },
		DisconnectClient: func(id int) { disconnected = id },
	})
	if err != nil {
		t.Fatalf("Kick() error = %v", err)
	}
	if result.Client == client {
		t.Fatal("Kick() should return working copy even when SaveClient is nil")
	}
	if result.Client.Status || result.Client.IsConnect {
		t.Fatalf("result client = %+v, want disabled disconnected copy", result.Client)
	}
	if !client.Status || !client.IsConnect {
		t.Fatalf("original client mutated = %+v", client)
	}
	if disconnected != 1 {
		t.Fatalf("disconnectClient(%d), want 1", disconnected)
	}
}

func TestDefaultNodeControlServiceKickDoesNotDisconnectWhenFlushFails(t *testing.T) {
	client := &file.Client{Id: 1, VerifyKey: "vk-1", Status: true, IsConnect: true}
	disconnected := 0
	saved := 0
	service := DefaultNodeControlService{
		Authz: DefaultAuthorizationService{},
	}

	_, err := service.Kick(NodeKickInput{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Roles:         []string{RoleUser},
			Permissions:   []string{PermissionClientsStatus},
			ClientIDs:     []int{1},
			Attributes:    map[string]string{"user_id": "7"},
		},
		Target:        NodeClientTarget{ClientID: 1},
		ResolveClient: func(NodeClientTarget) (*file.Client, error) { return client, nil },
		SaveClient: func(updated *file.Client) error {
			saved++
			return nil
		},
		FlushStore: func() error {
			return errors.New("flush failed")
		},
		DisconnectClient: func(id int) { disconnected = id },
	})
	if err == nil || err.Error() != "flush failed" {
		t.Fatalf("Kick() error = %v, want flush failed", err)
	}
	if saved != 1 {
		t.Fatalf("SaveClient calls = %d, want 1", saved)
	}
	if disconnected != 0 {
		t.Fatalf("disconnectClient(%d), want 0 when flush fails", disconnected)
	}
}

func TestDefaultNodeControlServiceKickMapsClientNotFoundOnSave(t *testing.T) {
	service := DefaultNodeControlService{
		Authz: DefaultAuthorizationService{},
	}

	_, err := service.Kick(NodeKickInput{
		Principal: Principal{
			Authenticated: true,
			Kind:          "user",
			Roles:         []string{RoleUser},
			Permissions:   []string{PermissionClientsStatus},
			ClientIDs:     []int{1},
			Attributes:    map[string]string{"user_id": "7"},
		},
		Target: NodeClientTarget{ClientID: 1},
		ResolveClient: func(NodeClientTarget) (*file.Client, error) {
			return &file.Client{Id: 1, Status: true, IsConnect: true}, nil
		},
		SaveClient: func(updated *file.Client) error {
			return file.ErrClientNotFound
		},
	})
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("Kick() error = %v, want %v", err, ErrClientNotFound)
	}
}

func TestDefaultNodeControlServiceSyncRestartsRuntimeOnFlushError(t *testing.T) {
	service := DefaultNodeControlService{}
	steps := make([]string, 0)

	err := service.Sync(NodeSyncInput{
		Scope: testNodeFullAdminScope(),
		StopRuntime: func() {
			steps = append(steps, "stop")
		},
		FlushLocal: func() error {
			steps = append(steps, "flush-local")
			return nil
		},
		FlushStore: func() error {
			steps = append(steps, "flush-store")
			return errors.New("flush failed")
		},
		StartRuntime: func() {
			steps = append(steps, "start")
		},
	})
	if err == nil || err.Error() != "flush failed" {
		t.Fatalf("Sync() error = %v, want flush failed", err)
	}
	if got := strings.Join(steps, ","); got != "stop,flush-local,flush-store,start" {
		t.Fatalf("Sync() steps = %s, want stop,flush-local,flush-store,start", got)
	}
}

func TestDefaultNodeControlServiceSyncRestartsRuntimeOnLocalFlushError(t *testing.T) {
	service := DefaultNodeControlService{}
	steps := make([]string, 0)

	err := service.Sync(NodeSyncInput{
		Scope: testNodeFullAdminScope(),
		StopRuntime: func() {
			steps = append(steps, "stop")
		},
		FlushLocal: func() error {
			steps = append(steps, "flush-local")
			return errors.New("flush local failed")
		},
		FlushStore: func() error {
			steps = append(steps, "flush-store")
			return nil
		},
		ClearProxyCache: func() {
			steps = append(steps, "clear-cache")
		},
		DisconnectOrphans: func() {
			steps = append(steps, "disconnect-orphans")
		},
		StartRuntime: func() {
			steps = append(steps, "start")
		},
	})
	if err == nil || err.Error() != "flush local failed" {
		t.Fatalf("Sync() error = %v, want flush local failed", err)
	}
	if got := strings.Join(steps, ","); got != "stop,flush-local,start" {
		t.Fatalf("Sync() steps = %s, want stop,flush-local,start", got)
	}
}

func TestDecodeNodeTrafficItemsAndApplyTraffic(t *testing.T) {
	items, err := DecodeNodeTrafficItems(`[{"client_id":1,"in":3,"out":4}]`, 0, "", 0, 0)
	if err != nil {
		t.Fatalf("DecodeNodeTrafficItems() error = %v", err)
	}
	if len(items) != 1 || items[0].ClientID != 1 || items[0].In != 3 || items[0].Out != 4 {
		t.Fatalf("DecodeNodeTrafficItems() = %+v", items)
	}

	client := &file.Client{Id: 1, Flow: &file.Flow{}}
	flushed := 0
	var saved *file.Client
	service := DefaultNodeControlService{}
	result, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: items,
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			if item.ClientID != 1 {
				t.Fatalf("ResolveClient item = %+v, want client 1", item)
			}
			return client, nil
		},
		SaveClient: func(updated *file.Client) error {
			saved = updated
			return nil
		},
		FlushStore: func() error {
			flushed++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ApplyTraffic() error = %v", err)
	}
	if result.ItemCount != 1 {
		t.Fatalf("ApplyTraffic() ItemCount = %d, want 1", result.ItemCount)
	}
	if len(result.Clients) != 1 || result.Clients[0].Client == nil || result.Clients[0].Client.Id != 1 || result.Clients[0].InletDelta != 3 || result.Clients[0].ExportDelta != 4 {
		t.Fatalf("ApplyTraffic() Clients = %+v, want committed delta for client 1", result.Clients)
	}
	if saved == nil {
		t.Fatal("ApplyTraffic() did not save committed client")
	}
	if saved == client {
		t.Fatal("ApplyTraffic() saved original live client pointer, want working copy")
	}
	if saved.Flow.InletFlow != 3 || saved.Flow.ExportFlow != 4 {
		t.Fatalf("saved client flow = %d/%d, want 3/4", saved.Flow.InletFlow, saved.Flow.ExportFlow)
	}
	if in, out, total := saved.ServiceTrafficTotals(); in != 3 || out != 4 || total != 7 {
		t.Fatalf("saved client service traffic = %d/%d/%d, want 3/4/7", in, out, total)
	}
	if client.Flow.InletFlow != 0 || client.Flow.ExportFlow != 0 {
		t.Fatalf("original client flow = %d/%d, want unchanged live object", client.Flow.InletFlow, client.Flow.ExportFlow)
	}
	if flushed != 1 {
		t.Fatalf("FlushStore calls = %d, want 1", flushed)
	}
}

func TestDefaultNodeControlServiceApplyTrafficDoesNotSaveUserTotalsBeforeClientSave(t *testing.T) {
	user := &file.User{Id: 7, Username: "tenant", TotalFlow: &file.Flow{}}
	client := &file.Client{Id: 1, OwnerUserID: user.Id, UserId: user.Id, Flow: &file.Flow{}}
	service := DefaultNodeControlService{}
	savedUsers := 0

	errWant := errors.New("save failed")
	_, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: []file.TrafficDelta{{ClientID: 1, In: 3, Out: 4}},
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			return client, nil
		},
		ResolveUser: func(userID int) (*file.User, error) {
			if userID != user.Id {
				t.Fatalf("ResolveUser(%d) want %d", userID, user.Id)
			}
			return user, nil
		},
		SaveClient: func(updated *file.Client) error {
			if updated == client {
				t.Fatal("ApplyTraffic() SaveClient received original live client pointer")
			}
			return errWant
		},
		SaveUser: func(updated *file.User) error {
			savedUsers++
			return nil
		},
	})
	if !errors.Is(err, errWant) {
		t.Fatalf("ApplyTraffic() error = %v, want %v", err, errWant)
	}
	if savedUsers != 0 {
		t.Fatalf("SaveUser call count = %d, want 0 when SaveClient fails", savedUsers)
	}
	if client.Flow.InletFlow != 0 || client.Flow.ExportFlow != 0 {
		t.Fatalf("original client flow = %d/%d, want unchanged live object", client.Flow.InletFlow, client.Flow.ExportFlow)
	}
}

func TestDefaultNodeControlServiceApplyTrafficMapsClientNotFoundOnSave(t *testing.T) {
	service := DefaultNodeControlService{}

	_, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: []file.TrafficDelta{{ClientID: 1, In: 3, Out: 4}},
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			return &file.Client{Id: 1, Flow: &file.Flow{}}, nil
		},
		SaveClient: func(updated *file.Client) error {
			return file.ErrClientNotFound
		},
	})
	if !errors.Is(err, ErrClientNotFound) {
		t.Fatalf("ApplyTraffic() error = %v, want %v", err, ErrClientNotFound)
	}
}

func TestDefaultNodeControlServiceApplyTrafficMapsUserNotFoundOnSave(t *testing.T) {
	service := DefaultNodeControlService{}

	_, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: []file.TrafficDelta{{ClientID: 1, In: 3, Out: 4}},
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			return &file.Client{Id: 1, OwnerUserID: 7, UserId: 7, Flow: &file.Flow{}}, nil
		},
		ResolveUser: func(userID int) (*file.User, error) {
			return &file.User{Id: userID, TotalFlow: &file.Flow{}}, nil
		},
		SaveClient: func(updated *file.Client) error {
			return nil
		},
		SaveUser: func(updated *file.User) error {
			return file.ErrUserNotFound
		},
	})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ApplyTraffic() error = %v, want %v", err, ErrUserNotFound)
	}
}

func TestDefaultNodeControlServiceApplyTrafficPropagatesUnexpectedUserLookupError(t *testing.T) {
	service := DefaultNodeControlService{}
	lookupErr := errors.New("user lookup failed")
	savedClients := 0
	savedUsers := 0

	_, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: []file.TrafficDelta{{ClientID: 1, In: 3, Out: 4}},
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			return &file.Client{Id: 1, OwnerUserID: 7, UserId: 7, Flow: &file.Flow{}}, nil
		},
		ResolveUser: func(userID int) (*file.User, error) {
			return nil, lookupErr
		},
		SaveClient: func(updated *file.Client) error {
			savedClients++
			return nil
		},
		SaveUser: func(updated *file.User) error {
			savedUsers++
			return nil
		},
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("ApplyTraffic() error = %v, want %v", err, lookupErr)
	}
	if savedClients != 0 || savedUsers != 0 {
		t.Fatalf("save calls = client:%d user:%d, want 0/0 on lookup error", savedClients, savedUsers)
	}
}

func TestDefaultNodeControlServiceApplyTrafficWithoutSaveHooksKeepsResolvedObjectsImmutable(t *testing.T) {
	user := &file.User{Id: 7, Username: "tenant", TotalFlow: &file.Flow{}}
	client := &file.Client{
		Id:          1,
		OwnerUserID: user.Id,
		UserId:      user.Id,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	service := DefaultNodeControlService{}

	result, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: []file.TrafficDelta{{ClientID: 1, In: 3, Out: 4}},
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			return client, nil
		},
		ResolveUser: func(userID int) (*file.User, error) {
			if userID != user.Id {
				t.Fatalf("ResolveUser(%d) want %d", userID, user.Id)
			}
			return user, nil
		},
	})
	if err != nil {
		t.Fatalf("ApplyTraffic() error = %v", err)
	}
	if len(result.Clients) != 1 || result.Clients[0].Client == nil {
		t.Fatalf("ApplyTraffic() Clients = %+v, want one committed client", result.Clients)
	}
	if result.Clients[0].Client == client {
		t.Fatal("ApplyTraffic() returned original live client pointer")
	}
	if in, out, total := result.Clients[0].Client.ServiceTrafficTotals(); in != 3 || out != 4 || total != 7 {
		t.Fatalf("result client service traffic = %d/%d/%d, want 3/4/7", in, out, total)
	}
	if client.Flow.InletFlow != 0 || client.Flow.ExportFlow != 0 {
		t.Fatalf("original client flow = %d/%d, want unchanged live object", client.Flow.InletFlow, client.Flow.ExportFlow)
	}
	if _, _, total := user.TotalTrafficTotals(); total != 0 {
		t.Fatalf("original user total traffic = %d, want unchanged live object", total)
	}
}

func TestDefaultNodeControlServiceApplyTrafficUsesBoundOwnerFastPath(t *testing.T) {
	user := &file.User{Id: 7, Username: "tenant", TotalFlow: &file.Flow{}}
	client := &file.Client{
		Id:          1,
		OwnerUserID: user.Id,
		UserId:      user.Id,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	client.BindOwnerUser(user)
	savedUsers := 0
	service := DefaultNodeControlService{}

	result, err := service.ApplyTraffic(NodeTrafficInput{
		Scope: testNodeFullAdminScope(),
		Items: []file.TrafficDelta{{ClientID: 1, In: 3, Out: 4}},
		ResolveClient: func(item file.TrafficDelta) (*file.Client, error) {
			return client, nil
		},
		ResolveUser: func(userID int) (*file.User, error) {
			t.Fatalf("ResolveUser(%d) should not be used when client already carries a bound owner snapshot", userID)
			return nil, nil
		},
		SaveUser: func(updated *file.User) error {
			savedUsers++
			if updated == user {
				t.Fatal("ApplyTraffic() SaveUser received original live user pointer")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ApplyTraffic() error = %v", err)
	}
	if savedUsers != 1 {
		t.Fatalf("SaveUser call count = %d, want 1", savedUsers)
	}
	if len(result.Clients) != 1 || result.Clients[0].Client == nil {
		t.Fatalf("ApplyTraffic() Clients = %+v, want one committed client", result.Clients)
	}
	if result.Clients[0].Client.OwnerUser() == nil || result.Clients[0].Client.OwnerUser().Id != user.Id {
		t.Fatalf("result owner = %#v, want bound owner %d", result.Clients[0].Client.OwnerUser(), user.Id)
	}
}
