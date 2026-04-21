package service

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
)

type NodeControlService interface {
	Dashboard(NodeDashboardInput) (NodeDashboardPayload, error)
	Overview(NodeOverviewInput) (NodeOverviewPayload, error)
	Registration(NodeRegistrationInput) (NodeRegistrationPayload, error)
	Operations(NodeOperationsInput) (NodeOperationsPayload, error)
	Status(NodeStatusInput) (NodeStatusPayload, error)
	UsageSnapshot(NodeUsageSnapshotInput) (NodeUsageSnapshotPayload, error)
	ExportConfig(NodeConfigExportInput) (interface{}, error)
	Kick(NodeKickInput) (NodeKickResult, error)
	Sync(NodeSyncInput) error
	ApplyTraffic(NodeTrafficInput) (NodeTrafficResult, error)
	QueryCallbackQueue(NodeCallbackQueueQueryInput) (NodeCallbackQueuePayload, error)
	MutateCallbackQueue(NodeCallbackQueueMutationInput) (NodeCallbackQueueMutationPayload, error)
}

type DefaultNodeControlService struct {
	System  SystemService
	Authz   AuthorizationService
	Repo    NodeControlRepository
	Runtime NodeControlRuntime
	Backend Backend
}

type NodeDashboardInput struct {
	Scope       NodeAccessScope
	ConfigEpoch string
}

type NodeClientTarget struct {
	ClientID  int
	VerifyKey string
}

type NodeRegistrationInput struct {
	NodeID                 string
	Host                   string
	Scope                  NodeAccessScope
	Config                 *servercfg.Snapshot
	BootID                 string
	RuntimeStartedAt       int64
	ConfigEpoch            string
	Operations             NodeOperationPayload
	Idempotency            NodeIdempotencyPayload
	LiveOnlyEvents         []string
	ReverseStatus          func(string) ManagementPlatformReverseRuntimeStatus
	ResolveServiceUsername func(string, string) string
}

type NodeOverviewInput struct {
	NodeID                 string
	Host                   string
	Principal              Principal
	Scope                  NodeAccessScope
	Config                 *servercfg.Snapshot
	BootID                 string
	RuntimeStartedAt       int64
	ConfigEpoch            string
	Operations             NodeOperationPayload
	Idempotency            NodeIdempotencyPayload
	LiveOnlyEvents         []string
	ReverseStatus          func(string) ManagementPlatformReverseRuntimeStatus
	ResolveServiceUsername func(string, string) string
	IncludeConfig          bool
	Snapshot               func() (interface{}, error)
}

type NodeOperationsInput struct {
	Scope       NodeAccessScope
	SubjectID   string
	OperationID string
	Limit       int
	Store       NodeOperationStore
}

type NodeStatusInput struct {
	NodeID                 string
	Host                   string
	Scope                  NodeAccessScope
	Config                 *servercfg.Snapshot
	BootID                 string
	RuntimeStartedAt       int64
	ConfigEpoch            string
	Operations             NodeOperationPayload
	Idempotency            NodeIdempotencyPayload
	LiveOnlyEvents         []string
	ReverseStatus          func(string) ManagementPlatformReverseRuntimeStatus
	ResolveServiceUsername func(string, string) string
}

type NodeUsageSnapshotInput struct {
	NodeID      string
	Principal   Principal
	Scope       NodeAccessScope
	Config      *servercfg.Snapshot
	ConfigEpoch string
}

type NodeCallbackQueueQueryInput struct {
	Scope               NodeAccessScope
	Config              *servercfg.Snapshot
	RequestedPlatformID string
	Limit               int
	ListItems           func(string, int) []NodeCallbackQueueItemPayload
	QueueSize           func(string) int
	ReverseStatus       func(string) ManagementPlatformReverseRuntimeStatus
}

type NodeCallbackQueueMutationInput struct {
	Scope               NodeAccessScope
	Config              *servercfg.Snapshot
	RequestedPlatformID string
	Action              string
	QueueSize           func(string) int
	Clear               func(string) int
	NotifyReplay        func(string) int
}

type NodeConfigExportInput struct {
	Scope    NodeAccessScope
	Snapshot func() (interface{}, error)
}

type NodeKickInput struct {
	Principal        Principal
	Target           NodeClientTarget
	SourceType       string
	SourcePlatformID string
	SourceActorID    string
	ResolveClient    func(NodeClientTarget) (*file.Client, error)
	SaveClient       func(*file.Client) error
	DisconnectClient func(int)
	FlushStore       func() error
}

type NodeKickResult struct {
	Client *file.Client
}

type NodeSyncInput struct {
	Scope             NodeAccessScope
	StopRuntime       func()
	FlushLocal        func() error
	FlushStore        func() error
	ClearProxyCache   func()
	DisconnectOrphans func()
	StartRuntime      func()
}

type NodeTrafficInput struct {
	Scope         NodeAccessScope
	Items         []file.TrafficDelta
	ResolveClient func(file.TrafficDelta) (*file.Client, error)
	ResolveUser   func(int) (*file.User, error)
	SaveUser      func(*file.User) error
	SaveClient    func(*file.Client) error
	FlushStore    func() error
}

type NodeTrafficResult struct {
	ItemCount int
	Clients   []NodeTrafficClientDelta
}

type NodeTrafficClientDelta struct {
	Client      *file.Client
	InletDelta  int64
	ExportDelta int64
}

type nodeReadView struct {
	Descriptor          NodeDescriptor
	StoreMode           string
	Version             string
	Display             NodeDisplayPayload
	ManagementPlatforms []NodePlatformStatusPayload
	Counts              NodeCountsPayload
	Revisions           NodeRevisionSummaryPayload
	Operations          NodeOperationPayload
	Idempotency         NodeIdempotencyPayload
	Timestamp           int64
}

type nodeScopeSnapshot struct {
	Users              []*file.User
	Clients            []*file.Client
	ClientIDs          map[int]struct{}
	ClientTunnelCounts map[int]int
	ClientHostCounts   map[int]int
	Counts             NodeCountsPayload
	Revisions          NodeRevisionSummaryPayload
}

type nodeScopeSnapshotMode uint8

const (
	nodeScopeSnapshotCountsOnly nodeScopeSnapshotMode = iota
	nodeScopeSnapshotWithDetails
)

type nodeScopeUserLookupRepository interface {
	GetUser(int) (*file.User, error)
}

func (s DefaultNodeControlService) repo() NodeControlRepository {
	if !isNilServiceValue(s.Repo) {
		return s.Repo
	}
	return s.Backend.repo()
}

func (s DefaultNodeControlService) runtime() NodeControlRuntime {
	if !isNilServiceValue(s.Runtime) {
		return s.Runtime
	}
	return s.Backend.runtime()
}

func (s DefaultNodeControlService) authz() AuthorizationService {
	if !isNilServiceValue(s.Authz) {
		return s.Authz
	}
	return DefaultAuthorizationService{Repo: s.Backend.repo(), Backend: s.Backend}
}

func (s DefaultNodeControlService) Dashboard(input NodeDashboardInput) (NodeDashboardPayload, error) {
	if !input.Scope.IsFullAccess() {
		return NodeDashboardPayload{}, ErrForbidden
	}
	stats := s.runtime().DashboardData(false)
	if stats == nil {
		stats = map[string]interface{}{}
	}
	return NodeDashboardPayload{
		ConfigEpoch: input.ConfigEpoch,
		GeneratedAt: time.Now().Unix(),
		Stats:       stats,
	}, nil
}

func (s DefaultNodeControlService) ExportConfig(input NodeConfigExportInput) (interface{}, error) {
	if !input.Scope.CanExportConfig() {
		return nil, ErrForbidden
	}
	if input.Snapshot == nil {
		return nil, ErrSnapshotExportUnsupported
	}
	return input.Snapshot()
}

func (s DefaultNodeControlService) Operations(input NodeOperationsInput) (NodeOperationsPayload, error) {
	if !input.Scope.CanViewStatus() {
		return NodeOperationsPayload{}, ErrForbidden
	}
	if input.Store == nil {
		return NodeOperationsPayload{Timestamp: time.Now().Unix()}, nil
	}
	return input.Store.Query(NodeOperationQueryInput{
		Scope:       input.Scope,
		SubjectID:   input.SubjectID,
		OperationID: input.OperationID,
		Limit:       input.Limit,
	}), nil
}

func (s DefaultNodeControlService) QueryCallbackQueue(input NodeCallbackQueueQueryInput) (NodeCallbackQueuePayload, error) {
	if !input.Scope.CanViewCallbackQueue() {
		return NodeCallbackQueuePayload{}, ErrForbidden
	}
	platforms, err := visibleNodeCallbackPlatforms(input.Scope, input.Config, input.RequestedPlatformID)
	if err != nil {
		return NodeCallbackQueuePayload{}, err
	}
	reverseStatus := input.ReverseStatus
	if reverseStatus == nil {
		reverseStatus = func(string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{}
		}
	}
	limit := normalizeNodeCallbackQueueLimit(input.Limit)
	payload := NodeCallbackQueuePayload{
		Timestamp: time.Now().Unix(),
		Platforms: make([]NodeCallbackQueuePlatformPayload, 0, len(platforms)),
	}
	for _, platform := range platforms {
		reverse := reverseStatus(platform.PlatformID)
		var items []NodeCallbackQueueItemPayload
		if input.ListItems != nil {
			items = input.ListItems(platform.PlatformID, limit)
		}
		queueSize := reverse.CallbackQueueSize
		if input.QueueSize != nil {
			queueSize = input.QueueSize(platform.PlatformID)
		}
		payload.Platforms = append(payload.Platforms, NodeCallbackQueuePlatformPayload{
			PlatformID:             platform.PlatformID,
			CallbackURL:            platform.CallbackURL,
			CallbackQueueMax:       platform.CallbackQueueMax,
			CallbackQueueSize:      queueSize,
			CallbackDropped:        reverse.CallbackDropped,
			LastCallbackQueuedAt:   reverse.LastCallbackQueuedAt,
			LastCallbackReplayAt:   reverse.LastCallbackReplayAt,
			LastCallbackError:      reverse.LastCallbackError,
			LastCallbackErrorAt:    reverse.LastCallbackErrorAt,
			LastCallbackStatusCode: reverse.LastCallbackStatusCode,
			Items:                  items,
		})
	}
	return payload, nil
}

func (s DefaultNodeControlService) MutateCallbackQueue(input NodeCallbackQueueMutationInput) (NodeCallbackQueueMutationPayload, error) {
	if !input.Scope.CanManageCallbackQueue() {
		return NodeCallbackQueueMutationPayload{}, ErrForbidden
	}
	action, err := normalizeNodeCallbackQueueAction(input.Action)
	if err != nil {
		return NodeCallbackQueueMutationPayload{}, err
	}
	platforms, err := visibleNodeCallbackPlatforms(input.Scope, input.Config, input.RequestedPlatformID)
	if err != nil {
		return NodeCallbackQueueMutationPayload{}, err
	}
	payload := NodeCallbackQueueMutationPayload{
		Platforms: make([]NodeCallbackQueueMutationPlatformPayload, 0, len(platforms)),
		Timestamp: time.Now().Unix(),
	}
	for _, platform := range platforms {
		item := NodeCallbackQueueMutationPlatformPayload{
			PlatformID:       platform.PlatformID,
			CallbackQueueMax: platform.CallbackQueueMax,
		}
		if input.QueueSize != nil && action != "clear" {
			item.CallbackQueueSize = input.QueueSize(platform.PlatformID)
		}
		switch action {
		case "replay":
			if input.NotifyReplay != nil {
				item.ReplayNotified = input.NotifyReplay(platform.PlatformID)
				item.ReplayTriggered = item.ReplayNotified > 0
			}
		case "clear":
			if input.Clear != nil {
				item.Cleared = input.Clear(platform.PlatformID)
			}
			if input.QueueSize != nil {
				item.CallbackQueueSize = input.QueueSize(platform.PlatformID)
			}
		}
		payload.Platforms = append(payload.Platforms, item)
	}
	return payload, nil
}

func (s DefaultNodeControlService) visibleNodePlatforms(scope NodeAccessScope, cfg *servercfg.Snapshot, reverseStatus func(string) ManagementPlatformReverseRuntimeStatus, resolveServiceUsername func(string, string) string) []NodePlatformStatusPayload {
	return BuildNodePlatformStatusPayloads(cfg, reverseStatus, resolveServiceUsername, scope.AllowsPlatform)
}

func (s DefaultNodeControlService) collectNodeScopeSnapshot(scope NodeAccessScope) (nodeScopeSnapshot, error) {
	return s.collectNodeScopeSnapshotMode(scope, nodeScopeSnapshotWithDetails)
}

func (s DefaultNodeControlService) collectNodeScopeCounts(scope NodeAccessScope) (nodeScopeSnapshot, error) {
	return s.collectNodeScopeSnapshotMode(scope, nodeScopeSnapshotCountsOnly)
}

func (s DefaultNodeControlService) collectNodeScopeSnapshotMode(scope NodeAccessScope, mode nodeScopeSnapshotMode) (nodeScopeSnapshot, error) {
	repo := s.repo()
	if scope.IsFullAccess() {
		return collectRangedNodeScopeSnapshot(repo, scope, mode)
	}
	switch scope.actorKind {
	case "platform_admin":
		return collectPlatformNodeScopeSnapshot(repo, scope, mode)
	case "platform_user":
		return collectPlatformUserNodeScopeSnapshot(repo, scope, mode)
	case "client":
		return collectClientNodeScopeSnapshot(repo, scope, mode)
	case "user":
		return collectUserNodeScopeSnapshot(repo, scope, mode)
	default:
		return collectRangedNodeScopeSnapshot(repo, scope, mode)
	}
}

func newNodeScopeSnapshot(mode nodeScopeSnapshotMode) nodeScopeSnapshot {
	snapshot := nodeScopeSnapshot{
		ClientIDs: make(map[int]struct{}),
	}
	if mode == nodeScopeSnapshotWithDetails {
		snapshot.Users = make([]*file.User, 0)
		snapshot.Clients = make([]*file.Client, 0)
		snapshot.ClientTunnelCounts = make(map[int]int)
		snapshot.ClientHostCounts = make(map[int]int)
	}
	return snapshot
}

func (s *nodeScopeSnapshot) wantsDetails() bool {
	return s != nil && s.Users != nil && s.Clients != nil
}

func (s *nodeScopeSnapshot) noteUser(user *file.User) {
	if s == nil || user == nil {
		return
	}
	if s.wantsDetails() {
		s.Users = append(s.Users, user)
	}
	s.Counts.Users++
	if user.Hidden {
		s.Counts.HiddenUsers++
	}
	if strings.EqualFold(strings.TrimSpace(user.Kind), "platform_service") {
		s.Counts.PlatformUsers++
	}
	updateNodeRevisionSummary(&s.Revisions, user.Revision, user.UpdatedAt, "user")
}

func (s *nodeScopeSnapshot) noteClient(client *file.Client) {
	if s == nil || client == nil {
		return
	}
	if client.Id > 0 {
		if _, exists := s.ClientIDs[client.Id]; exists {
			return
		}
		s.ClientIDs[client.Id] = struct{}{}
	}
	if s.wantsDetails() {
		s.Clients = append(s.Clients, client)
	}
	s.Counts.Clients++
	if client.OwnerID() > 0 {
		s.Counts.OwnedClients++
	} else {
		s.Counts.UnownedClients++
	}
	if len(client.ManagerUserIDs) > 0 {
		s.Counts.DelegatedClients++
	}
	if client.IsConnect {
		s.Counts.OnlineClients++
	}
	updateNodeRevisionSummary(&s.Revisions, client.Revision, client.UpdatedAt, "client")
}

func (s *nodeScopeSnapshot) noteTunnel(tunnel *file.Tunnel) {
	if s == nil || tunnel == nil || tunnel.Client == nil {
		return
	}
	if _, ok := s.ClientIDs[tunnel.Client.Id]; !ok {
		return
	}
	s.Counts.Tunnels++
	if s.wantsDetails() {
		s.ClientTunnelCounts[tunnel.Client.Id]++
	}
	updateNodeRevisionSummary(&s.Revisions, tunnel.Revision, tunnel.UpdatedAt, "tunnel")
}

func (s *nodeScopeSnapshot) noteHost(host *file.Host) {
	if s == nil || host == nil || host.Client == nil {
		return
	}
	if _, ok := s.ClientIDs[host.Client.Id]; !ok {
		return
	}
	s.Counts.Hosts++
	if s.wantsDetails() {
		s.ClientHostCounts[host.Client.Id]++
	}
	updateNodeRevisionSummary(&s.Revisions, host.Revision, host.UpdatedAt, "host")
}

func (s *nodeScopeSnapshot) sortDetails() {
	if s == nil || !s.wantsDetails() {
		return
	}
	if len(s.Users) > 1 {
		sort.Slice(s.Users, func(i, j int) bool {
			return s.Users[i].Id < s.Users[j].Id
		})
	}
	if len(s.Clients) > 1 {
		sort.Slice(s.Clients, func(i, j int) bool {
			return s.Clients[i].Id < s.Clients[j].Id
		})
	}
}

func collectRangedNodeScopeSnapshot(repo NodeControlRepository, scope NodeAccessScope, mode nodeScopeSnapshotMode) (nodeScopeSnapshot, error) {
	snapshot := newNodeScopeSnapshot(mode)
	if repo == nil {
		return snapshot, nil
	}
	repo.RangeUsers(func(user *file.User) bool {
		if scope.AllowsUser(user) {
			snapshot.noteUser(user)
		}
		return true
	})
	repo.RangeClients(func(client *file.Client) bool {
		if scope.AllowsClient(client) {
			snapshot.noteClient(client)
		}
		return true
	})
	return finishNodeScopeSnapshot(repo, snapshot)
}

func collectPlatformNodeScopeSnapshot(repo NodeControlRepository, scope NodeAccessScope, mode nodeScopeSnapshotMode) (nodeScopeSnapshot, error) {
	snapshot := newNodeScopeSnapshot(mode)
	if err := collectNodeScopeUserByID(repo, scope.serviceUserID, &snapshot); err != nil {
		return nodeScopeSnapshot{}, err
	}
	handled, err := collectNodeScopeOwnedClients(repo, scope.serviceUserID, scope, &snapshot)
	if err != nil {
		return nodeScopeSnapshot{}, err
	}
	if !handled {
		collectNodeScopeClientsByRange(repo, scope, &snapshot)
	}
	return finishNodeScopeSnapshot(repo, snapshot)
}

func collectPlatformUserNodeScopeSnapshot(repo NodeControlRepository, scope NodeAccessScope, mode nodeScopeSnapshotMode) (nodeScopeSnapshot, error) {
	snapshot := newNodeScopeSnapshot(mode)
	if err := collectNodeScopeUserByID(repo, scope.serviceUserID, &snapshot); err != nil {
		return nodeScopeSnapshot{}, err
	}
	if err := collectNodeScopeClientsByIDs(repo, sortedNodeScopeClientIDs(scope.allowedClientIDs), scope.AllowsClient, &snapshot); err != nil {
		return nodeScopeSnapshot{}, err
	}
	return finishNodeScopeSnapshot(repo, snapshot)
}

func collectClientNodeScopeSnapshot(repo NodeControlRepository, scope NodeAccessScope, mode nodeScopeSnapshotMode) (nodeScopeSnapshot, error) {
	snapshot := newNodeScopeSnapshot(mode)
	if err := collectNodeScopeClientsByIDs(repo, sortedNodeScopeClientIDs(scope.allowedClientIDs), scope.AllowsClient, &snapshot); err != nil {
		return nodeScopeSnapshot{}, err
	}
	return finishNodeScopeSnapshot(repo, snapshot)
}

func collectUserNodeScopeSnapshot(repo NodeControlRepository, scope NodeAccessScope, mode nodeScopeSnapshotMode) (nodeScopeSnapshot, error) {
	snapshot := newNodeScopeSnapshot(mode)
	if err := collectNodeScopeUserByID(repo, scope.userID, &snapshot); err != nil {
		return nodeScopeSnapshot{}, err
	}
	if scope.userID > 0 {
		handled, err := collectNodeScopeManagedClients(repo, scope.userID, scope, &snapshot)
		if err != nil {
			return nodeScopeSnapshot{}, err
		}
		if !handled {
			handled, err = collectNodeScopeOwnedClients(repo, scope.userID, scope, &snapshot)
			if err != nil {
				return nodeScopeSnapshot{}, err
			}
			if !handled {
				collectNodeScopeClientsByRange(repo, scope, &snapshot)
			}
		}
	} else {
		collectNodeScopeClientsByRange(repo, scope, &snapshot)
	}
	return finishNodeScopeSnapshot(repo, snapshot)
}

func finishNodeScopeSnapshot(repo NodeControlRepository, snapshot nodeScopeSnapshot) (nodeScopeSnapshot, error) {
	collectNodeScopeResources(repo, &snapshot)
	snapshot.sortDetails()
	return snapshot, nil
}

func collectNodeScopeUserByID(repo NodeControlRepository, userID int, snapshot *nodeScopeSnapshot) error {
	if repo == nil || snapshot == nil || userID <= 0 {
		return nil
	}
	if lookupRepo, ok := repo.(nodeScopeUserLookupRepository); ok {
		user, err := lookupRepo.GetUser(userID)
		switch {
		case err == nil && user != nil:
			snapshot.noteUser(user)
			return nil
		case err != nil && !errors.Is(err, file.ErrUserNotFound):
			return err
		}
	}
	repo.RangeUsers(func(user *file.User) bool {
		if user == nil || user.Id != userID {
			return true
		}
		snapshot.noteUser(user)
		return false
	})
	return nil
}

func collectNodeScopeOwnedClients(repo NodeControlRepository, userID int, scope NodeAccessScope, snapshot *nodeScopeSnapshot) (bool, error) {
	if repo == nil || snapshot == nil || userID <= 0 {
		return false, nil
	}
	if clientsRepo, ok := repo.(clientsByUserLookupRepository); ok && clientsRepo.SupportsGetClientsByUserID() {
		clients, err := clientsRepo.GetClientsByUserID(userID)
		if err != nil {
			return false, err
		}
		for _, client := range clients {
			if client != nil && client.OwnerID() == userID && scope.AllowsClient(client) {
				snapshot.noteClient(client)
			}
		}
		return true, nil
	}
	if idsRepo, ok := repo.(clientIDsByUserLookupRepository); ok && idsRepo.SupportsGetClientIDsByUserID() {
		clientIDs, err := idsRepo.GetClientIDsByUserID(userID)
		if err != nil {
			return false, err
		}
		if err := collectNodeScopeClientsByIDs(repo, normalizeSortedUniqueClientIDs(clientIDs), func(client *file.Client) bool {
			return client != nil && client.OwnerID() == userID && scope.AllowsClient(client)
		}, snapshot); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func collectNodeScopeManagedClients(repo NodeControlRepository, userID int, scope NodeAccessScope, snapshot *nodeScopeSnapshot) (bool, error) {
	if repo == nil || snapshot == nil || userID <= 0 {
		return false, nil
	}
	clientIDs, err := AllManagedClientIDsByUser(repo, userID)
	if err != nil {
		return false, err
	}
	if err := collectNodeScopeClientsByIDs(repo, clientIDs, scope.AllowsClient, snapshot); err != nil {
		return false, err
	}
	return true, nil
}

func collectNodeScopeClientsByIDs(repo NodeControlRepository, clientIDs []int, allow func(*file.Client) bool, snapshot *nodeScopeSnapshot) error {
	if repo == nil || snapshot == nil || len(clientIDs) == 0 {
		return nil
	}
	return rangeClientsByIDs(repo, clientIDs, func(client *file.Client) bool {
		if allow == nil || allow(client) {
			snapshot.noteClient(client)
		}
		return true
	})
}

func collectNodeScopeClientsByRange(repo NodeControlRepository, scope NodeAccessScope, snapshot *nodeScopeSnapshot) {
	if repo == nil || snapshot == nil {
		return
	}
	repo.RangeClients(func(client *file.Client) bool {
		if scope.AllowsClient(client) {
			snapshot.noteClient(client)
		}
		return true
	})
}

func collectNodeScopeResources(repo NodeControlRepository, snapshot *nodeScopeSnapshot) {
	if repo == nil || snapshot == nil || len(snapshot.ClientIDs) == 0 {
		return
	}
	clientIDs := sortedNodeScopeClientIDs(snapshot.ClientIDs)
	if scopedRepo, ok := repo.(clientScopedResourceRangeRepository); ok && scopedRepo.SupportsRangeTunnelsByClientID() {
		for _, clientID := range clientIDs {
			scopedRepo.RangeTunnelsByClientID(clientID, func(tunnel *file.Tunnel) bool {
				snapshot.noteTunnel(tunnel)
				return true
			})
		}
	} else {
		repo.RangeTunnels(func(tunnel *file.Tunnel) bool {
			snapshot.noteTunnel(tunnel)
			return true
		})
	}
	if scopedRepo, ok := repo.(clientScopedResourceRangeRepository); ok && scopedRepo.SupportsRangeHostsByClientID() {
		for _, clientID := range clientIDs {
			scopedRepo.RangeHostsByClientID(clientID, func(host *file.Host) bool {
				snapshot.noteHost(host)
				return true
			})
		}
		return
	}
	repo.RangeHosts(func(host *file.Host) bool {
		snapshot.noteHost(host)
		return true
	})
}

func sortedNodeScopeClientIDs(clientIDs map[int]struct{}) []int {
	if len(clientIDs) == 0 {
		return nil
	}
	sorted := make([]int, 0, len(clientIDs))
	for clientID := range clientIDs {
		if clientID > 0 {
			sorted = append(sorted, clientID)
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.Ints(sorted)
	return sorted
}

func updateNodeRevisionSummary(summary *NodeRevisionSummaryPayload, revision, updatedAt int64, kind string) {
	if summary == nil {
		return
	}
	switch kind {
	case "user":
		if revision > summary.Users {
			summary.Users = revision
		}
	case "client":
		if revision > summary.Clients {
			summary.Clients = revision
		}
	case "tunnel":
		if revision > summary.Tunnels {
			summary.Tunnels = revision
		}
	case "host":
		if revision > summary.Hosts {
			summary.Hosts = revision
		}
	}
	if revision > summary.Max {
		summary.Max = revision
	}
	if updatedAt > summary.LatestUpdatedAt {
		summary.LatestUpdatedAt = updatedAt
	}
}

func buildNodeHealthPayload(platforms []NodePlatformStatusPayload, operations NodeOperationPayload, idempotency NodeIdempotencyPayload) NodeHealthPayload {
	payload := NodeHealthPayload{
		LastSyncAt:               operations.LastSyncAt,
		LastTrafficAt:            operations.LastTrafficAt,
		LastKickAt:               operations.LastKickAt,
		LastMutationAt:           operations.LastMutationAt,
		LastErrorAt:              operations.LastErrorAt,
		LastError:                operations.LastError,
		IdempotencyCachedEntries: idempotency.CachedEntries,
		IdempotencyInflight:      idempotency.Inflight,
		IdempotencyReplayHits:    idempotency.ReplayHits,
		IdempotencyConflicts:     idempotency.Conflicts,
	}
	for _, platform := range platforms {
		if platform.DirectEnabled {
			payload.DirectEnabledPlatforms++
		}
		if platform.ReverseEnabled {
			payload.ReverseEnabledPlatforms++
		}
		if platform.ReverseConnected {
			payload.ReverseConnectedPlatforms++
		}
		if platform.CallbackEnabled {
			payload.CallbackEnabledPlatforms++
		}
		payload.CallbackQueueBacklog += platform.CallbackQueueSize
	}
	return payload
}

func visibleNodeCallbackPlatforms(scope NodeAccessScope, cfg *servercfg.Snapshot, requestedPlatformID string) ([]servercfg.ManagementPlatformConfig, error) {
	requestedPlatformID = strings.TrimSpace(requestedPlatformID)
	if cfg == nil {
		if requestedPlatformID != "" {
			return nil, ErrManagementPlatformNotFound
		}
		return nil, nil
	}
	items := make([]servercfg.ManagementPlatformConfig, 0)
	for _, platform := range cfg.Runtime.ManagementPlatforms {
		if !isRuntimeManagementPlatformEnabled(platform) || !platform.SupportsCallback() {
			continue
		}
		if requestedPlatformID != "" && platform.PlatformID != requestedPlatformID {
			continue
		}
		if !scope.AllowsPlatform(platform.PlatformID) {
			continue
		}
		items = append(items, platform)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].PlatformID < items[j].PlatformID
	})
	if requestedPlatformID != "" && len(items) == 0 {
		return nil, ErrManagementPlatformNotFound
	}
	return items, nil
}

func normalizeNodeCallbackQueueLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func normalizeNodeCallbackQueueAction(action string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(action)); normalized {
	case "replay", "clear":
		return normalized, nil
	default:
		return "", ErrInvalidCallbackQueueAction
	}
}

func buildNodeDisplayPayload(system SystemService, cfg *servercfg.Snapshot, host string) NodeDisplayPayload {
	if system == nil {
		system = DefaultSystemService{}
	}
	cfg = servercfg.Resolve(cfg)
	bridge := system.BridgeDisplay(cfg, host)
	payload := NodeDisplayPayload{
		VisitorVKey:    cfg.Runtime.VisitorVKey,
		HTTPProxyPort:  cfg.Network.HTTPProxyPort,
		HTTPSProxyPort: cfg.Network.HTTPSProxyPort,
		Bridge: NodeDisplayBridgePayload{
			Primary: NodeDisplayEndpointPayload{
				Type: bridge.Primary.Type,
				IP:   bridge.Primary.IP,
				Port: bridge.Primary.Port,
				Addr: bridge.Primary.Addr,
			},
			Path: bridge.Path,
			TCP:  buildNodeDisplayTransportPayload(bridge.TCP),
			KCP:  buildNodeDisplayTransportPayload(bridge.KCP),
			TLS:  buildNodeDisplayTransportPayload(bridge.TLS),
			QUIC: buildNodeDisplayTransportPayload(bridge.QUIC),
			WS:   buildNodeDisplayTransportPayload(bridge.WS),
			WSS:  buildNodeDisplayTransportPayload(bridge.WSS),
		},
	}
	if common.IsWindows() {
		payload.ClientBinarySuffix = ".exe"
	}
	return payload
}

func buildNodeDisplayTransportPayload(transport BridgeTransport) NodeDisplayTransportPayload {
	return NodeDisplayTransportPayload(transport)
}

func joinNodeBase(base, suffix string) string {
	base = strings.TrimSpace(base)
	suffix = strings.TrimSpace(suffix)
	if base == "" {
		if suffix == "" {
			return ""
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

type NodeOperationPayload struct {
	LastSyncAt     int64  `json:"last_sync_at,omitempty"`
	LastTrafficAt  int64  `json:"last_traffic_at,omitempty"`
	LastKickAt     int64  `json:"last_kick_at,omitempty"`
	LastMutationAt int64  `json:"last_mutation_at,omitempty"`
	LastErrorAt    int64  `json:"last_error_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type NodeHealthPayload struct {
	DirectEnabledPlatforms    int    `json:"direct_enabled_platforms"`
	ReverseEnabledPlatforms   int    `json:"reverse_enabled_platforms"`
	ReverseConnectedPlatforms int    `json:"reverse_connected_platforms"`
	CallbackEnabledPlatforms  int    `json:"callback_enabled_platforms"`
	CallbackQueueBacklog      int    `json:"callback_queue_backlog"`
	LastSyncAt                int64  `json:"last_sync_at,omitempty"`
	LastTrafficAt             int64  `json:"last_traffic_at,omitempty"`
	LastKickAt                int64  `json:"last_kick_at,omitempty"`
	LastMutationAt            int64  `json:"last_mutation_at,omitempty"`
	LastErrorAt               int64  `json:"last_error_at,omitempty"`
	LastError                 string `json:"last_error,omitempty"`
	IdempotencyCachedEntries  int    `json:"idempotency_cached_entries"`
	IdempotencyInflight       int    `json:"idempotency_inflight"`
	IdempotencyReplayHits     int64  `json:"idempotency_replay_hits"`
	IdempotencyConflicts      int64  `json:"idempotency_conflicts"`
}

type NodeIdempotencyPayload struct {
	TTLSeconds    int64 `json:"ttl_seconds"`
	CachedEntries int   `json:"cached_entries"`
	Inflight      int   `json:"inflight"`
	ReplayHits    int64 `json:"replay_hits"`
	Conflicts     int64 `json:"conflicts"`
}

type NodeRegistrationPayload struct {
	NodeID              string                      `json:"node_id"`
	BootID              string                      `json:"boot_id,omitempty"`
	RuntimeStartedAt    int64                       `json:"runtime_started_at,omitempty"`
	ConfigEpoch         string                      `json:"config_epoch,omitempty"`
	ResyncOnBootChange  bool                        `json:"resync_on_boot_change"`
	RunMode             string                      `json:"run_mode"`
	StoreMode           string                      `json:"store_mode"`
	Version             string                      `json:"version"`
	SchemaVersion       int                         `json:"schema_version"`
	EventsEnabled       bool                        `json:"events_enabled"`
	CallbacksReady      bool                        `json:"callbacks_ready"`
	APIBase             string                      `json:"api_base,omitempty"`
	Capabilities        []string                    `json:"capabilities,omitempty"`
	Protocol            NodeProtocolPayload         `json:"protocol"`
	Display             NodeDisplayPayload          `json:"display"`
	ManagementPlatforms []NodePlatformStatusPayload `json:"management_platforms"`
	Counts              NodeCountsPayload           `json:"counts"`
	Revisions           NodeRevisionSummaryPayload  `json:"revisions"`
	Health              NodeHealthPayload           `json:"health"`
	Operations          NodeOperationPayload        `json:"operations"`
	Idempotency         NodeIdempotencyPayload      `json:"idempotency"`
	Timestamp           int64                       `json:"timestamp"`
}

type NodeStatusPayload struct {
	NodeID              string                      `json:"node_id"`
	BootID              string                      `json:"boot_id,omitempty"`
	RuntimeStartedAt    int64                       `json:"runtime_started_at,omitempty"`
	ConfigEpoch         string                      `json:"config_epoch,omitempty"`
	ResyncOnBootChange  bool                        `json:"resync_on_boot_change"`
	RunMode             string                      `json:"run_mode"`
	StoreMode           string                      `json:"store_mode"`
	Version             string                      `json:"version"`
	SchemaVersion       int                         `json:"schema_version"`
	APIBase             string                      `json:"api_base,omitempty"`
	Capabilities        []string                    `json:"capabilities,omitempty"`
	Protocol            NodeProtocolPayload         `json:"protocol"`
	Display             NodeDisplayPayload          `json:"display"`
	ManagementPlatforms []NodePlatformStatusPayload `json:"management_platforms"`
	Counts              NodeCountsPayload           `json:"counts"`
	Revisions           NodeRevisionSummaryPayload  `json:"revisions"`
	Operations          NodeOperationPayload        `json:"operations"`
	Idempotency         NodeIdempotencyPayload      `json:"idempotency"`
	Timestamp           int64                       `json:"timestamp"`
}

type NodeOverviewPayload struct {
	NodeID        string                    `json:"node_id,omitempty"`
	Registration  *NodeRegistrationPayload  `json:"registration,omitempty"`
	UsageSnapshot *NodeUsageSnapshotPayload `json:"usage_snapshot,omitempty"`
	Config        interface{}               `json:"config,omitempty"`
	Timestamp     int64                     `json:"timestamp"`
}

type NodeDisplayEndpointPayload struct {
	Type string `json:"type,omitempty"`
	IP   string `json:"ip,omitempty"`
	Port string `json:"port,omitempty"`
	Addr string `json:"addr,omitempty"`
}

type NodeDisplayTransportPayload struct {
	Enabled bool   `json:"enabled,omitempty"`
	Type    string `json:"type,omitempty"`
	IP      string `json:"ip,omitempty"`
	Port    string `json:"port,omitempty"`
	Addr    string `json:"addr,omitempty"`
	ALPN    string `json:"alpn,omitempty"`
}

type NodeDisplayBridgePayload struct {
	Primary NodeDisplayEndpointPayload  `json:"primary"`
	Path    string                      `json:"path,omitempty"`
	TCP     NodeDisplayTransportPayload `json:"tcp,omitempty"`
	KCP     NodeDisplayTransportPayload `json:"kcp,omitempty"`
	TLS     NodeDisplayTransportPayload `json:"tls,omitempty"`
	QUIC    NodeDisplayTransportPayload `json:"quic,omitempty"`
	WS      NodeDisplayTransportPayload `json:"ws,omitempty"`
	WSS     NodeDisplayTransportPayload `json:"wss,omitempty"`
}

type NodeDisplayPayload struct {
	ClientBinarySuffix string                   `json:"client_binary_suffix,omitempty"`
	VisitorVKey        string                   `json:"visitor_vkey,omitempty"`
	HTTPProxyPort      int                      `json:"http_proxy_port,omitempty"`
	HTTPSProxyPort     int                      `json:"https_proxy_port,omitempty"`
	Bridge             NodeDisplayBridgePayload `json:"bridge"`
}

type NodeDashboardPayload struct {
	ConfigEpoch string                 `json:"config_epoch,omitempty"`
	GeneratedAt int64                  `json:"generated_at"`
	Stats       map[string]interface{} `json:"stats"`
}

type NodeProtocolPayload struct {
	ChangesWindow                int      `json:"changes_window"`
	ChangesDurable               bool     `json:"changes_durable,omitempty"`
	ChangesHistoryWindow         int      `json:"changes_history_window,omitempty"`
	BatchMaxItems                int      `json:"batch_max_items"`
	IdempotencyTTLSeconds        int      `json:"idempotency_ttl_seconds"`
	TrafficReportIntervalSeconds int      `json:"traffic_report_interval_seconds,omitempty"`
	TrafficReportStepBytes       int64    `json:"traffic_report_step_bytes,omitempty"`
	LiveOnlyEvents               []string `json:"live_only_events,omitempty"`
}

type NodePlatformStatusPayload struct {
	PlatformID              string `json:"platform_id"`
	MasterURL               string `json:"master_url,omitempty"`
	ControlScope            string `json:"control_scope"`
	ServiceUsername         string `json:"service_username,omitempty"`
	Enabled                 bool   `json:"enabled"`
	ConnectMode             string `json:"connect_mode,omitempty"`
	DirectEnabled           bool   `json:"direct_enabled,omitempty"`
	ReverseEnabled          bool   `json:"reverse_enabled,omitempty"`
	ReverseHeartbeatSeconds int    `json:"reverse_heartbeat_seconds,omitempty"`
	ReverseWSURL            string `json:"reverse_ws_url,omitempty"`
	CallbackEnabled         bool   `json:"callback_enabled,omitempty"`
	CallbackURL             string `json:"callback_url,omitempty"`
	CallbackTimeoutSeconds  int    `json:"callback_timeout_seconds,omitempty"`
	CallbackRetryMax        int    `json:"callback_retry_max,omitempty"`
	CallbackRetryBackoffSec int    `json:"callback_retry_backoff_seconds,omitempty"`
	CallbackQueueMax        int    `json:"callback_queue_max"`
	CallbackQueueSize       int    `json:"callback_queue_size"`
	CallbackDropped         int64  `json:"callback_dropped,omitempty"`
	CallbackSigningEnabled  bool   `json:"callback_signing_enabled,omitempty"`
	ReverseConnected        bool   `json:"reverse_connected,omitempty"`
	LastReverseConnectedAt  int64  `json:"last_reverse_connected_at,omitempty"`
	LastReverseHelloAt      int64  `json:"last_reverse_hello_at,omitempty"`
	LastReverseEventAt      int64  `json:"last_reverse_event_at,omitempty"`
	LastReversePingAt       int64  `json:"last_reverse_ping_at,omitempty"`
	LastReversePongAt       int64  `json:"last_reverse_pong_at,omitempty"`
	LastCallbackAt          int64  `json:"last_callback_at,omitempty"`
	LastCallbackSuccessAt   int64  `json:"last_callback_success_at,omitempty"`
	LastCallbackQueuedAt    int64  `json:"last_callback_queued_at,omitempty"`
	LastCallbackReplayAt    int64  `json:"last_callback_replay_at,omitempty"`
	LastCallbackStatusCode  int    `json:"last_callback_status_code,omitempty"`
	CallbackDeliveries      int64  `json:"callback_deliveries,omitempty"`
	CallbackFailures        int64  `json:"callback_failures,omitempty"`
	CallbackConsecutiveFail int64  `json:"callback_consecutive_failures,omitempty"`
	LastReverseError        string `json:"last_reverse_error,omitempty"`
	LastReverseErrorAt      int64  `json:"last_reverse_error_at,omitempty"`
	LastCallbackError       string `json:"last_callback_error,omitempty"`
	LastCallbackErrorAt     int64  `json:"last_callback_error_at,omitempty"`
	LastReverseDisconnectAt int64  `json:"last_reverse_disconnect_at,omitempty"`
}

type NodeCountsPayload struct {
	Users            int `json:"users"`
	HiddenUsers      int `json:"hidden_users"`
	PlatformUsers    int `json:"platform_users"`
	Clients          int `json:"clients"`
	OwnedClients     int `json:"owned_clients"`
	UnownedClients   int `json:"unowned_clients"`
	DelegatedClients int `json:"delegated_clients"`
	OnlineClients    int `json:"online_clients"`
	Tunnels          int `json:"tunnels"`
	Hosts            int `json:"hosts"`
}

type NodeUsageSnapshotPayload struct {
	NodeID        string                     `json:"node_id"`
	SchemaVersion int                        `json:"schema_version"`
	APIBase       string                     `json:"api_base,omitempty"`
	ConfigEpoch   string                     `json:"config_epoch,omitempty"`
	GeneratedAt   int64                      `json:"generated_at"`
	Summary       NodeUsageSummaryPayload    `json:"summary"`
	Revisions     NodeRevisionSummaryPayload `json:"revisions"`
	Users         []NodeUsageUserPayload     `json:"users"`
	Clients       []NodeUsageClientPayload   `json:"clients"`
}

type NodeRevisionSummaryPayload struct {
	Users           int64 `json:"users"`
	Clients         int64 `json:"clients"`
	Tunnels         int64 `json:"tunnels"`
	Hosts           int64 `json:"hosts"`
	Max             int64 `json:"max"`
	LatestUpdatedAt int64 `json:"latest_updated_at"`
}

type NodeUsageSummaryPayload struct {
	Users          int   `json:"users"`
	HiddenUsers    int   `json:"hidden_users"`
	PlatformUsers  int   `json:"platform_users"`
	Clients        int   `json:"clients"`
	OwnedClients   int   `json:"owned_clients"`
	UnownedClients int   `json:"unowned_clients"`
	Tunnels        int   `json:"tunnels"`
	Hosts          int   `json:"hosts"`
	TotalInBytes   int64 `json:"total_in_bytes"`
	TotalOutBytes  int64 `json:"total_out_bytes"`
}

type NodeUsageUserPayload struct {
	ID                  int    `json:"id"`
	Username            string `json:"username"`
	Kind                string `json:"kind"`
	ExternalPlatformID  string `json:"external_platform_id,omitempty"`
	Hidden              bool   `json:"hidden"`
	Status              int    `json:"status"`
	ExpireAt            int64  `json:"expire_at"`
	FlowLimitTotalBytes int64  `json:"flow_limit_total_bytes"`
	MaxClients          int    `json:"max_clients"`
	MaxTunnels          int    `json:"max_tunnels"`
	MaxHosts            int    `json:"max_hosts"`
	MaxConnections      int    `json:"max_connections"`
	RateLimitTotalBps   int    `json:"rate_limit_total_bps"`
	Revision            int64  `json:"revision"`
	UpdatedAt           int64  `json:"updated_at"`
	TotalInBytes        int64  `json:"total_in_bytes"`
	TotalOutBytes       int64  `json:"total_out_bytes"`
	ClientCount         int    `json:"client_count"`
	TunnelCount         int    `json:"tunnel_count"`
	HostCount           int    `json:"host_count"`
}

type NodeUsageClientPayload struct {
	ID                  int    `json:"id"`
	VerifyKey           string `json:"verify_key"`
	Remark              string `json:"remark"`
	OwnerUserID         int    `json:"owner_user_id"`
	ManagerUserIDs      []int  `json:"manager_user_ids,omitempty"`
	SourceType          string `json:"source_type,omitempty"`
	SourcePlatformID    string `json:"source_platform_id,omitempty"`
	SourceActorID       string `json:"source_actor_id,omitempty"`
	Status              bool   `json:"status"`
	IsConnect           bool   `json:"is_connect"`
	ExpireAt            int64  `json:"expire_at"`
	FlowLimitTotalBytes int64  `json:"flow_limit_total_bytes"`
	RateLimitTotalBps   int    `json:"rate_limit_total_bps"`
	MaxConnections      int    `json:"max_connections"`
	MaxTunnelNum        int    `json:"max_tunnel_num"`
	ConfigConnAllow     bool   `json:"config_conn_allow"`
	Revision            int64  `json:"revision"`
	UpdatedAt           int64  `json:"updated_at"`
	BridgeInBytes       int64  `json:"bridge_in_bytes"`
	BridgeOutBytes      int64  `json:"bridge_out_bytes"`
	BridgeTotalBytes    int64  `json:"bridge_total_bytes"`
	ServiceInBytes      int64  `json:"service_in_bytes"`
	ServiceOutBytes     int64  `json:"service_out_bytes"`
	ServiceTotalBytes   int64  `json:"service_total_bytes"`
	TotalInBytes        int64  `json:"total_in_bytes"`
	TotalOutBytes       int64  `json:"total_out_bytes"`
	TotalBytes          int64  `json:"total_bytes"`
	TunnelCount         int    `json:"tunnel_count"`
	HostCount           int    `json:"host_count"`
	EntryACLRuleCount   int    `json:"entry_acl_rule_count"`
	CreateTime          string `json:"create_time,omitempty"`
	LastOnlineTime      string `json:"last_online_time,omitempty"`
}

type NodeCallbackQueueItemPayload struct {
	ID            int64  `json:"id"`
	EventName     string `json:"event_name,omitempty"`
	EventResource string `json:"event_resource,omitempty"`
	EventAction   string `json:"event_action,omitempty"`
	EventSequence int64  `json:"event_sequence,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
	EnqueuedAt    int64  `json:"enqueued_at,omitempty"`
	LastAttemptAt int64  `json:"last_attempt_at,omitempty"`
	Attempts      int    `json:"attempts,omitempty"`
}

type NodeCallbackQueuePlatformPayload struct {
	PlatformID             string                         `json:"platform_id"`
	CallbackURL            string                         `json:"callback_url,omitempty"`
	CallbackQueueMax       int                            `json:"callback_queue_max"`
	CallbackQueueSize      int                            `json:"callback_queue_size"`
	CallbackDropped        int64                          `json:"callback_dropped,omitempty"`
	LastCallbackQueuedAt   int64                          `json:"last_callback_queued_at,omitempty"`
	LastCallbackReplayAt   int64                          `json:"last_callback_replay_at,omitempty"`
	LastCallbackError      string                         `json:"last_callback_error,omitempty"`
	LastCallbackErrorAt    int64                          `json:"last_callback_error_at,omitempty"`
	LastCallbackStatusCode int                            `json:"last_callback_status_code,omitempty"`
	Items                  []NodeCallbackQueueItemPayload `json:"items,omitempty"`
}

type NodeCallbackQueuePayload struct {
	Timestamp int64                              `json:"timestamp"`
	Platforms []NodeCallbackQueuePlatformPayload `json:"platforms"`
}

type NodeCallbackQueueMutationPayload struct {
	Platforms []NodeCallbackQueueMutationPlatformPayload `json:"platforms"`
	Timestamp int64                                      `json:"timestamp"`
}

type NodeCallbackQueueMutationPlatformPayload struct {
	PlatformID        string `json:"platform_id"`
	CallbackQueueSize int    `json:"callback_queue_size"`
	CallbackQueueMax  int    `json:"callback_queue_max"`
	Cleared           int    `json:"cleared,omitempty"`
	ReplayTriggered   bool   `json:"replay_triggered,omitempty"`
	ReplayNotified    int    `json:"replay_notified,omitempty"`
}

const managementRateLimitUnitBytesPerSecond = 1024

func ManagementRateLimitToBps(limit int) int {
	if limit <= 0 {
		return 0
	}
	return limit * managementRateLimitUnitBytesPerSecond
}

func ManagementRateLimitFromBps(limit int) int {
	if limit <= 0 {
		return 0
	}
	return (limit + managementRateLimitUnitBytesPerSecond - 1) / managementRateLimitUnitBytesPerSecond
}

func (s DefaultNodeControlService) Overview(input NodeOverviewInput) (NodeOverviewPayload, error) {
	payload := NodeOverviewPayload{}
	cfg := servercfg.Resolve(input.Config)

	if !input.Scope.CanViewStatus() && !input.Scope.CanViewUsage() {
		return NodeOverviewPayload{}, ErrForbidden
	}

	scopeSnapshot, err := s.collectNodeScopeSnapshot(input.Scope)
	if err != nil {
		return NodeOverviewPayload{}, err
	}

	if input.Scope.CanViewStatus() {
		registration := s.buildNodeReadViewWithSnapshot(NodeStatusInput{
			NodeID:                 input.NodeID,
			Host:                   input.Host,
			Scope:                  input.Scope,
			Config:                 cfg,
			BootID:                 input.BootID,
			RuntimeStartedAt:       input.RuntimeStartedAt,
			ConfigEpoch:            input.ConfigEpoch,
			Operations:             input.Operations,
			Idempotency:            input.Idempotency,
			LiveOnlyEvents:         input.LiveOnlyEvents,
			ReverseStatus:          input.ReverseStatus,
			ResolveServiceUsername: input.ResolveServiceUsername,
		}, scopeSnapshot).registrationPayload()
		registrationCopy := registration
		payload.NodeID = registration.NodeID
		payload.Registration = &registrationCopy
		payload.Timestamp = registration.Timestamp
	}

	if input.Scope.CanViewUsage() {
		usage := s.buildUsageSnapshotPayload(NodeUsageSnapshotInput{
			NodeID:      input.NodeID,
			Principal:   input.Principal,
			Scope:       input.Scope,
			Config:      cfg,
			ConfigEpoch: input.ConfigEpoch,
		}, cfg, scopeSnapshot)
		usageCopy := usage
		if payload.NodeID == "" {
			payload.NodeID = usage.NodeID
		}
		if payload.Timestamp == 0 {
			payload.Timestamp = usage.GeneratedAt
		}
		payload.UsageSnapshot = &usageCopy
	}

	if input.IncludeConfig && input.Scope.CanExportConfig() {
		config, err := s.ExportConfig(NodeConfigExportInput{
			Scope:    input.Scope,
			Snapshot: input.Snapshot,
		})
		if err != nil {
			return NodeOverviewPayload{}, err
		}
		payload.Config = config
	}

	if payload.Timestamp == 0 {
		payload.Timestamp = time.Now().Unix()
	}
	return payload, nil
}

func (s DefaultNodeControlService) Status(input NodeStatusInput) (NodeStatusPayload, error) {
	view, err := s.buildNodeReadView(input)
	if err != nil {
		return NodeStatusPayload{}, err
	}
	return view.statusPayload(), nil
}

func (s DefaultNodeControlService) Registration(input NodeRegistrationInput) (NodeRegistrationPayload, error) {
	view, err := s.buildNodeReadView(NodeStatusInput{
		NodeID:                 input.NodeID,
		Host:                   input.Host,
		Scope:                  input.Scope,
		Config:                 input.Config,
		BootID:                 input.BootID,
		RuntimeStartedAt:       input.RuntimeStartedAt,
		ConfigEpoch:            input.ConfigEpoch,
		Operations:             input.Operations,
		Idempotency:            input.Idempotency,
		LiveOnlyEvents:         input.LiveOnlyEvents,
		ReverseStatus:          input.ReverseStatus,
		ResolveServiceUsername: input.ResolveServiceUsername,
	})
	if err != nil {
		return NodeRegistrationPayload{}, err
	}
	return view.registrationPayload(), nil
}

func (s DefaultNodeControlService) buildNodeReadView(input NodeStatusInput) (nodeReadView, error) {
	if !input.Scope.CanViewStatus() {
		return nodeReadView{}, ErrForbidden
	}
	snapshot, err := s.collectNodeScopeCounts(input.Scope)
	if err != nil {
		return nodeReadView{}, err
	}
	return s.buildNodeReadViewWithSnapshot(input, snapshot), nil
}

func (s DefaultNodeControlService) buildNodeReadViewWithSnapshot(input NodeStatusInput, snapshot nodeScopeSnapshot) nodeReadView {
	cfg := servercfg.Resolve(input.Config)
	system := s.System
	if system == nil {
		system = DefaultSystemService{}
	}
	reverseStatus := input.ReverseStatus
	if reverseStatus == nil {
		reverseStatus = func(string) ManagementPlatformReverseRuntimeStatus {
			return ManagementPlatformReverseRuntimeStatus{}
		}
	}
	resolveServiceUsername := input.ResolveServiceUsername
	if resolveServiceUsername == nil {
		resolveServiceUsername = func(platformID, configured string) string {
			return strings.TrimSpace(configured)
		}
	}
	descriptor := BuildNodeDescriptor(NodeDescriptorInput{
		NodeID:           input.NodeID,
		Config:           cfg,
		BootID:           input.BootID,
		RuntimeStartedAt: input.RuntimeStartedAt,
		ConfigEpoch:      input.ConfigEpoch,
		LiveOnlyEvents:   input.LiveOnlyEvents,
	})
	return nodeReadView{
		Descriptor:          descriptor,
		StoreMode:           "local",
		Version:             system.Info().Version,
		Display:             buildNodeDisplayPayload(system, cfg, input.Host),
		ManagementPlatforms: s.visibleNodePlatforms(input.Scope, cfg, reverseStatus, resolveServiceUsername),
		Counts:              snapshot.Counts,
		Revisions:           snapshot.Revisions,
		Operations:          input.Operations,
		Idempotency:         input.Idempotency,
		Timestamp:           time.Now().Unix(),
	}
}

func (v nodeReadView) statusPayload() NodeStatusPayload {
	return NodeStatusPayload{
		NodeID:              v.Descriptor.NodeID,
		BootID:              v.Descriptor.BootID,
		RuntimeStartedAt:    v.Descriptor.RuntimeStartedAt,
		ConfigEpoch:         v.Descriptor.ConfigEpoch,
		ResyncOnBootChange:  v.Descriptor.ResyncOnBootChange,
		RunMode:             v.Descriptor.RunMode,
		StoreMode:           v.StoreMode,
		Version:             v.Version,
		SchemaVersion:       v.Descriptor.SchemaVersion,
		APIBase:             v.Descriptor.NodeAPIBase,
		Capabilities:        v.Descriptor.Capabilities,
		Protocol:            v.Descriptor.Protocol,
		Display:             v.Display,
		ManagementPlatforms: v.ManagementPlatforms,
		Counts:              v.Counts,
		Revisions:           v.Revisions,
		Operations:          v.Operations,
		Idempotency:         v.Idempotency,
		Timestamp:           v.Timestamp,
	}
}

func (v nodeReadView) registrationPayload() NodeRegistrationPayload {
	return NodeRegistrationPayload{
		NodeID:              v.Descriptor.NodeID,
		BootID:              v.Descriptor.BootID,
		RuntimeStartedAt:    v.Descriptor.RuntimeStartedAt,
		ConfigEpoch:         v.Descriptor.ConfigEpoch,
		ResyncOnBootChange:  v.Descriptor.ResyncOnBootChange,
		RunMode:             v.Descriptor.RunMode,
		StoreMode:           v.StoreMode,
		Version:             v.Version,
		SchemaVersion:       v.Descriptor.SchemaVersion,
		EventsEnabled:       v.Descriptor.EventsEnabled,
		CallbacksReady:      v.Descriptor.CallbacksReady,
		APIBase:             v.Descriptor.NodeAPIBase,
		Capabilities:        v.Descriptor.Capabilities,
		Protocol:            v.Descriptor.Protocol,
		Display:             v.Display,
		ManagementPlatforms: v.ManagementPlatforms,
		Counts:              v.Counts,
		Revisions:           v.Revisions,
		Health:              buildNodeHealthPayload(v.ManagementPlatforms, v.Operations, v.Idempotency),
		Operations:          v.Operations,
		Idempotency:         v.Idempotency,
		Timestamp:           v.Timestamp,
	}
}

func (s DefaultNodeControlService) UsageSnapshot(input NodeUsageSnapshotInput) (NodeUsageSnapshotPayload, error) {
	if !input.Scope.CanViewUsage() {
		return NodeUsageSnapshotPayload{}, ErrForbidden
	}
	cfg := servercfg.Resolve(input.Config)
	snapshot, err := s.collectNodeScopeSnapshot(input.Scope)
	if err != nil {
		return NodeUsageSnapshotPayload{}, err
	}
	return s.buildUsageSnapshotPayload(input, cfg, snapshot), nil
}

func (s DefaultNodeControlService) usageSnapshotAllowsClientSecrets(input NodeUsageSnapshotInput) bool {
	if input.Scope.IsFullAccess() {
		return true
	}
	authz := s.authz()
	principal := authz.NormalizePrincipal(input.Principal)
	if !principal.Authenticated {
		return false
	}
	return authz.Allows(principal, PermissionClientsUpdate)
}

func (s DefaultNodeControlService) buildUsageSnapshotPayload(input NodeUsageSnapshotInput, cfg *servercfg.Snapshot, snapshot nodeScopeSnapshot) NodeUsageSnapshotPayload {
	type usageTotals struct {
		clients int
		tunnels int
		hosts   int
	}

	summary := NodeUsageSummaryPayload{
		Users:          snapshot.Counts.Users,
		HiddenUsers:    snapshot.Counts.HiddenUsers,
		PlatformUsers:  snapshot.Counts.PlatformUsers,
		Clients:        snapshot.Counts.Clients,
		OwnedClients:   snapshot.Counts.OwnedClients,
		UnownedClients: snapshot.Counts.UnownedClients,
		Tunnels:        snapshot.Counts.Tunnels,
		Hosts:          snapshot.Counts.Hosts,
	}
	allowClientSecrets := s.usageSnapshotAllowsClientSecrets(input)
	userTotals := make(map[int]*usageTotals, len(snapshot.Users))
	clients := make([]NodeUsageClientPayload, 0, len(snapshot.Clients))
	for _, client := range snapshot.Clients {
		tunnelCount := snapshot.ClientTunnelCounts[client.Id]
		hostCount := snapshot.ClientHostCounts[client.Id]
		ownerID := client.OwnerID()
		if ownerID > 0 {
			if userTotals[ownerID] == nil {
				userTotals[ownerID] = &usageTotals{}
			}
			userTotals[ownerID].clients++
			userTotals[ownerID].tunnels += tunnelCount
			userTotals[ownerID].hosts += hostCount
		}
		payload := NodeUsageClientPayload{
			ID:                  client.Id,
			Remark:              client.Remark,
			OwnerUserID:         ownerID,
			SourceType:          client.SourceType,
			SourcePlatformID:    client.SourcePlatformID,
			SourceActorID:       client.SourceActorID,
			Status:              client.Status,
			IsConnect:           client.IsConnect,
			ExpireAt:            client.EffectiveExpireAt(),
			FlowLimitTotalBytes: client.EffectiveFlowLimitBytes(),
			RateLimitTotalBps:   ManagementRateLimitToBps(client.RateLimit),
			MaxConnections:      client.MaxConn,
			MaxTunnelNum:        client.MaxTunnelNum,
			ConfigConnAllow:     client.ConfigConnAllow,
			Revision:            client.Revision,
			UpdatedAt:           client.UpdatedAt,
			TunnelCount:         tunnelCount,
			HostCount:           hostCount,
			EntryACLRuleCount:   countNormalizedRuleLines(client.EntryAclRules),
			CreateTime:          client.CreateTime,
			LastOnlineTime:      client.LastOnlineTime,
		}
		payload.BridgeInBytes, payload.BridgeOutBytes, payload.BridgeTotalBytes = client.BridgeTrafficTotals()
		payload.ServiceInBytes, payload.ServiceOutBytes, payload.ServiceTotalBytes = client.ServiceTrafficTotals()
		payload.TotalInBytes, payload.TotalOutBytes, payload.TotalBytes = client.TotalTrafficTotals()
		summary.TotalInBytes += payload.TotalInBytes
		summary.TotalOutBytes += payload.TotalOutBytes
		if allowClientSecrets {
			payload.VerifyKey = client.VerifyKey
		}
		payload.ManagerUserIDs = append([]int(nil), client.ManagerUserIDs...)
		clients = append(clients, payload)
	}

	users := make([]NodeUsageUserPayload, 0, len(snapshot.Users))
	for _, user := range snapshot.Users {
		totals := userTotals[user.Id]
		if totals == nil {
			totals = &usageTotals{}
		}
		totalIn, totalOut, _ := user.TotalTrafficTotals()
		users = append(users, NodeUsageUserPayload{
			ID:                  user.Id,
			Username:            user.Username,
			Kind:                user.Kind,
			ExternalPlatformID:  user.ExternalPlatformID,
			Hidden:              user.Hidden,
			Status:              user.Status,
			ExpireAt:            user.ExpireAt,
			FlowLimitTotalBytes: user.FlowLimit,
			MaxClients:          user.MaxClients,
			MaxTunnels:          user.MaxTunnels,
			MaxHosts:            user.MaxHosts,
			MaxConnections:      user.MaxConnections,
			RateLimitTotalBps:   ManagementRateLimitToBps(user.RateLimit),
			Revision:            user.Revision,
			UpdatedAt:           user.UpdatedAt,
			TotalInBytes:        totalIn,
			TotalOutBytes:       totalOut,
			ClientCount:         totals.clients,
			TunnelCount:         totals.tunnels,
			HostCount:           totals.hosts,
		})
	}

	return NodeUsageSnapshotPayload{
		NodeID:        input.NodeID,
		SchemaVersion: NodeSchemaVersion,
		APIBase:       joinNodeBase(cfg.Web.BaseURL, "/api"),
		ConfigEpoch:   input.ConfigEpoch,
		GeneratedAt:   time.Now().Unix(),
		Summary:       summary,
		Revisions:     snapshot.Revisions,
		Users:         users,
		Clients:       clients,
	}
}

func (s DefaultNodeControlService) Kick(input NodeKickInput) (NodeKickResult, error) {
	if err := s.authz().RequirePermission(input.Principal, PermissionClientsStatus); err != nil {
		return NodeKickResult{}, err
	}
	if input.ResolveClient == nil {
		return NodeKickResult{}, ErrStoreNotInitialized
	}
	client, err := input.ResolveClient(input.Target)
	if err != nil {
		return NodeKickResult{}, err
	}
	if client == nil {
		return NodeKickResult{}, ErrClientNotFound
	}
	if err := s.authz().RequireClient(input.Principal, client.Id); err != nil {
		return NodeKickResult{}, err
	}
	working := cloneClientSnapshotForMutation(client)
	working.Status = false
	working.IsConnect = false
	working.TouchMeta(input.SourceType, input.SourcePlatformID, input.SourceActorID)
	if input.SaveClient != nil {
		if err := input.SaveClient(working); err != nil {
			return NodeKickResult{}, mapClientServiceError(err)
		}
	}
	if input.FlushStore != nil {
		if err := input.FlushStore(); err != nil {
			return NodeKickResult{}, err
		}
	}
	if input.DisconnectClient != nil {
		input.DisconnectClient(working.Id)
	}
	return NodeKickResult{Client: working}, nil
}

func (s DefaultNodeControlService) Sync(input NodeSyncInput) error {
	if !input.Scope.CanSync() {
		return ErrForbidden
	}
	if input.StopRuntime != nil {
		input.StopRuntime()
	}
	if input.FlushLocal != nil {
		if err := input.FlushLocal(); err != nil {
			if input.StartRuntime != nil {
				input.StartRuntime()
			}
			return err
		}
	}
	if input.FlushStore != nil {
		if err := input.FlushStore(); err != nil {
			if input.StartRuntime != nil {
				input.StartRuntime()
			}
			return err
		}
	}
	if input.ClearProxyCache != nil {
		input.ClearProxyCache()
	}
	if input.DisconnectOrphans != nil {
		input.DisconnectOrphans()
	}
	if input.StartRuntime != nil {
		input.StartRuntime()
	}
	return nil
}

func (s DefaultNodeControlService) ApplyTraffic(input NodeTrafficInput) (NodeTrafficResult, error) {
	if !input.Scope.CanWriteTraffic() {
		return NodeTrafficResult{}, ErrForbidden
	}
	if len(input.Items) == 0 {
		return NodeTrafficResult{}, ErrTrafficItemsEmpty
	}
	if input.ResolveClient == nil {
		return NodeTrafficResult{}, ErrStoreNotInitialized
	}
	workingByID := make(map[int]*file.Client)
	workingUsersByID := make(map[int]*file.User)
	type trafficTotals struct {
		in  int64
		out int64
	}
	recordByID := make(map[int]trafficTotals)
	for _, item := range input.Items {
		client, err := input.ResolveClient(item)
		if err != nil {
			return NodeTrafficResult{}, err
		}
		if client == nil {
			return NodeTrafficResult{}, ErrClientNotFound
		}
		working := workingByID[client.Id]
		if working == nil {
			working = cloneClientSnapshotForMutation(client)
			workingByID[client.Id] = working
		}
		if ownerID := working.OwnerID(); ownerID > 0 && input.ResolveUser != nil {
			if owner := workingUsersByID[ownerID]; owner != nil {
				working.BindOwnerUser(owner)
			} else if owner := working.OwnerUser(); owner != nil && owner.Id == ownerID {
				owner = cloneUserForMutation(owner)
				workingUsersByID[ownerID] = owner
				working.BindOwnerUser(owner)
			} else {
				user, err := input.ResolveUser(ownerID)
				if err != nil {
					if errors.Is(err, ErrUserNotFound) {
						working.BindOwnerUser(nil)
					} else {
						return NodeTrafficResult{}, mapUserServiceError(err)
					}
				} else if user != nil {
					user = cloneUserForMutation(user)
					workingUsersByID[ownerID] = user
					working.BindOwnerUser(user)
				} else {
					working.BindOwnerUser(nil)
				}
			}
		}
		recordImportedClientTraffic(working, item.In, item.Out)
		totals := recordByID[working.Id]
		totals.in += item.In
		totals.out += item.Out
		recordByID[working.Id] = totals
	}
	if input.SaveClient != nil {
		for _, client := range workingByID {
			if err := input.SaveClient(client); err != nil {
				return NodeTrafficResult{}, mapClientServiceError(err)
			}
		}
	}
	if input.SaveUser != nil {
		for _, user := range workingUsersByID {
			if err := input.SaveUser(user); err != nil {
				return NodeTrafficResult{}, mapUserServiceError(err)
			}
		}
	}
	if input.FlushStore != nil {
		if err := input.FlushStore(); err != nil {
			return NodeTrafficResult{}, err
		}
	}
	clients := make([]NodeTrafficClientDelta, 0, len(recordByID))
	for clientID, totals := range recordByID {
		client := workingByID[clientID]
		if client == nil {
			continue
		}
		clients = append(clients, NodeTrafficClientDelta{
			Client:      client,
			InletDelta:  totals.in,
			ExportDelta: totals.out,
		})
	}
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].Client.Id < clients[j].Client.Id
	})
	return NodeTrafficResult{ItemCount: len(input.Items), Clients: clients}, nil
}

func recordImportedClientTraffic(client *file.Client, in, out int64) {
	if client == nil || (in == 0 && out == 0) {
		return
	}
	client.EnsureRuntimeTraffic()
	client.ServiceTraffic.Add(in, out)
	client.ServiceMeter.Add(in, out)
	client.TotalMeter.Add(in, out)
	if client.Flow == nil {
		client.Flow = new(file.Flow)
	}
	client.Flow.Add(in, out)
	if owner := client.OwnerUser(); owner != nil {
		recordImportedUserTraffic(owner, in, out)
	}
}

func recordImportedUserTraffic(user *file.User, in, out int64) {
	if user == nil || (in == 0 && out == 0) {
		return
	}
	user.EnsureRuntimeTraffic()
	user.TotalTraffic.Add(in, out)
	user.TotalMeter.Add(in, out)
	user.EnsureTotalFlow()
	user.TotalFlow.Add(in, out)
}

func DecodeNodeTrafficItems(raw string, clientID int, verifyKey string, in, out int64) ([]file.TrafficDelta, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		var payload []file.TrafficDelta
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, ErrInvalidTrafficItems
		}
		if len(payload) == 0 {
			return nil, ErrTrafficItemsEmpty
		}
		return payload, nil
	}
	item := file.TrafficDelta{
		ClientID:  clientID,
		VerifyKey: strings.TrimSpace(verifyKey),
		In:        in,
		Out:       out,
	}
	if item.ClientID == 0 && item.VerifyKey == "" {
		return nil, ErrTrafficClientRequired
	}
	return []file.TrafficDelta{item}, nil
}
