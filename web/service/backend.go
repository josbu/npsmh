package service

import (
	"errors"
	"sort"
	"strings"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
	"github.com/djylb/nps/server/tool"
)

type ClientConnection = server.ClientConnectionInfo

type AuthRepository interface {
	GetUserByUsername(string) (*file.User, error)
	GetUser(int) (*file.User, error)
	RangeClients(func(*file.Client) bool)
	NextUserID() int
	CreateUser(*file.User) error
	NextClientID() int
	CreateClient(*file.Client) error
	DeleteUser(int) error
}

type AuthorizationRepository interface {
	ClientOwnsTunnel(int, int) bool
	ClientOwnsHost(int, int) bool
}

type GlobalRepository interface {
	GetGlobal() *file.Glob
	SaveGlobal(*file.Glob) error
}

type ManagementPlatformRepository interface {
	RangeClients(func(*file.Client) bool)
	RangeUsers(func(*file.User) bool)
}

type ownedClientRangeRepository interface {
	RangeClients(func(*file.Client) bool)
}

type clientsByUserLookupRepository interface {
	SupportsGetClientsByUserID() bool
	GetClientsByUserID(int) ([]*file.Client, error)
}

type clientIDsByUserLookupRepository interface {
	SupportsGetClientIDsByUserID() bool
	GetClientIDsByUserID(int) ([]int, error)
}

type authoritativeClientIDsByUserLookupRepository interface {
	SupportsAuthoritativeClientIDsByUserID() bool
}

type managedClientIDsByUserLookupRepository interface {
	SupportsGetManagedClientIDsByUserID() bool
	GetManagedClientIDsByUserID(int) ([]int, error)
}

type allManagedClientIDsByUserLookupRepository interface {
	SupportsGetAllManagedClientIDsByUserID() bool
	GetAllManagedClientIDsByUserID(int) ([]int, error)
}

type authoritativeManagedClientIDsByUserLookupRepository interface {
	SupportsAuthoritativeManagedClientIDsByUserID() bool
}

type authoritativeAllManagedClientIDsByUserLookupRepository interface {
	SupportsAuthoritativeAllManagedClientIDsByUserID() bool
}

type clientByIDLookupRepository interface {
	GetClient(int) (*file.Client, error)
}

type clientByIDLookupCapabilityRepository interface {
	SupportsGetClientByID() bool
}

type deleteUserCascadeClientRefsRepository interface {
	SupportsDeleteUserCascadeClientRefs() bool
}

type detachedSnapshotRepository interface {
	SupportsDetachedSnapshots() bool
}

type deferredPersistenceRepository interface {
	WithDeferredPersistence(func() error) error
}

type clientByVerifyKeyLookupRepository interface {
	SupportsGetClientByVerifyKey() bool
	GetClientByVerifyKey(string) (*file.Client, error)
}

type resourceCountByClientRepository interface {
	SupportsCountResourcesByClientID() bool
	CountResourcesByClientID(int) (int, error)
}

type clientResourceRangeRepository interface {
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
}

type ownedResourceCountMapsRepository interface {
	SupportsCollectOwnedResourceCounts() bool
	CollectOwnedResourceCounts() (map[int]int, map[int]int, map[int]int, error)
}

type ownedResourceCountByUserRepository interface {
	SupportsCountOwnedResourcesByUserID() bool
	CountOwnedResourcesByUserID(int) (int, int, int, error)
}

type ownedResourceRangeRepository interface {
	RangeClients(func(*file.Client) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
}

type ClientRepository interface {
	ListVisibleClients(ListClientsInput) ([]*file.Client, int)
	NextClientID() int
	CreateClient(*file.Client) error
	GetClient(int) (*file.Client, error)
	GetUser(int) (*file.User, error)
	SaveClient(*file.Client) error
	SaveTunnel(*file.Tunnel) error
	SaveHost(*file.Host, string) error
	DeleteClient(int) error
	VerifyVKey(string, int) bool
	RangeClients(func(*file.Client) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
}

type ClientRuntime interface {
	PingClient(int, string) int
	ClientConnectionCount(int) int
	ListClientConnections(int) []ClientConnection
	DisconnectClient(int)
	DeleteClientResources(int)
}

type UserRepository interface {
	RangeClients(func(*file.Client) bool)
	RangeUsers(func(*file.User) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
	NextUserID() int
	GetUser(int) (*file.User, error)
	CreateUser(*file.User) error
	SaveUser(*file.User) error
	SaveClient(*file.Client) error
	DeleteClient(int) error
	DeleteUser(int) error
}

type UserRuntime interface {
	DisconnectClient(int)
	DeleteClientResources(int)
}

type IndexRepository interface {
	NextTunnelID() int
	GetClient(int) (*file.Client, error)
	CreateTunnel(*file.Tunnel) error
	DeleteTunnelRecord(int) error
	GetTunnel(int) (*file.Tunnel, error)
	SaveTunnel(*file.Tunnel) error
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
	NextHostID() int
	GetHost(int) (*file.Host, error)
	DeleteHostRecord(int) error
	SaveHost(*file.Host, string) error
	CreateHost(*file.Host) error
	HostExists(*file.Host) bool
}

type IndexRuntime interface {
	DashboardData(bool) map[string]interface{}
	ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int)
	ListVisibleTunnels(offset, limit int, tunnelType string, clientIDs []int, search, sort, order string) ([]*file.Tunnel, int)
	ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int)
	ListVisibleHosts(offset, limit int, clientIDs []int, search, sort, order string) ([]*file.Host, int)
	TunnelRunning(int) bool
	GenerateTunnelPort(*file.Tunnel) int
	TunnelPortAvailable(*file.Tunnel) bool
	AddTunnel(*file.Tunnel) error
	StopTunnel(int) error
	StartTunnel(int) error
	DeleteTunnel(int) error
	RemoveHostCache(int)
}

type NodeControlRepository interface {
	RangeUsers(func(*file.User) bool)
	RangeClients(func(*file.Client) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
}

type NodeControlRuntime interface {
	DashboardData(bool) map[string]interface{}
}

type Repository interface {
	RangeClients(func(*file.Client) bool)
	RangeUsers(func(*file.User) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
	ListVisibleClients(ListClientsInput) ([]*file.Client, int)
	NextUserID() int
	NextClientID() int
	NextTunnelID() int
	NextHostID() int
	CreateUser(*file.User) error
	GetUser(int) (*file.User, error)
	GetUserByUsername(string) (*file.User, error)
	SaveUser(*file.User) error
	DeleteUser(int) error
	CreateClient(*file.Client) error
	GetClient(int) (*file.Client, error)
	SaveClient(*file.Client) error
	PersistClients() error
	DeleteClient(int) error
	VerifyUserName(string, int) bool
	VerifyVKey(string, int) bool
	ReplaceClientVKeyIndex(oldKey, newKey string, clientID int)
	CreateTunnel(*file.Tunnel) error
	GetTunnel(int) (*file.Tunnel, error)
	SaveTunnel(*file.Tunnel) error
	DeleteTunnelRecord(int) error
	CreateHost(*file.Host) error
	GetHost(int) (*file.Host, error)
	SaveHost(*file.Host, string) error
	PersistHosts() error
	DeleteHostRecord(int) error
	HostExists(*file.Host) bool
	GetGlobal() *file.Glob
	SaveGlobal(*file.Glob) error
	ClientOwnsTunnel(int, int) bool
	ClientOwnsHost(int, int) bool
}

type Runtime interface {
	DashboardData(bool) map[string]interface{}
	ListClients(offset, limit int, search, sort, order string, clientID int) ([]*file.Client, int)
	ListVisibleTunnels(offset, limit int, tunnelType string, clientIDs []int, search, sort, order string) ([]*file.Tunnel, int)
	PingClient(int, string) int
	ClientConnectionCount(int) int
	ListClientConnections(int) []ClientConnection
	DisconnectClient(int)
	DeleteClientResources(int)
	GenerateTunnelPort(*file.Tunnel) int
	TunnelPortAvailable(*file.Tunnel) bool
	ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int)
	ListVisibleHosts(offset, limit int, clientIDs []int, search, sort, order string) ([]*file.Host, int)
	TunnelRunning(int) bool
	AddTunnel(*file.Tunnel) error
	StopTunnel(int) error
	StartTunnel(int) error
	DeleteTunnel(int) error
	ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int)
	RemoveHostCache(int)
}

type Backend struct {
	Repository Repository
	Runtime    Runtime
}

func DefaultBackend() Backend {
	return Backend{
		Repository: defaultRepository{},
		Runtime:    defaultRuntime{},
	}
}

func (b Backend) repo() Repository {
	if !isNilServiceValue(b.Repository) {
		return b.Repository
	}
	return DefaultBackend().Repository
}

func (b Backend) runtime() Runtime {
	if !isNilServiceValue(b.Runtime) {
		return b.Runtime
	}
	return DefaultBackend().Runtime
}

type defaultRuntime struct{}

func (defaultRuntime) DashboardData(force bool) map[string]interface{} {
	return server.GetDashboardData(force)
}

func (defaultRuntime) ListClients(offset, limit int, search, sort, order string, clientID int) ([]*file.Client, int) {
	return server.GetClientList(offset, limit, search, sort, order, clientID)
}

func (defaultRuntime) ListVisibleTunnels(offset, limit int, tunnelType string, clientIDs []int, search, sort, order string) ([]*file.Tunnel, int) {
	return server.GetTunnelByClientIDs(offset, limit, tunnelType, clientIDs, search, sort, order)
}

func (defaultRuntime) PingClient(id int, remoteAddr string) int {
	return server.PingClient(id, remoteAddr)
}

func (defaultRuntime) ClientConnectionCount(id int) int {
	return server.GetClientConnectionCount(id)
}

func (defaultRuntime) ListClientConnections(id int) []ClientConnection {
	return server.GetClientConnections(id)
}

func (defaultRuntime) DisconnectClient(id int) {
	server.DelClientConnect(id)
}

func (defaultRuntime) DeleteClientResources(id int) {
	server.DelTunnelAndHostByClientId(id, false)
}

func (defaultRuntime) GenerateTunnelPort(tunnel *file.Tunnel) int {
	return tool.GenerateTunnelPort(tunnel)
}

func (defaultRuntime) TunnelPortAvailable(tunnel *file.Tunnel) bool {
	return tool.TestTunnelPort(tunnel)
}

func (defaultRuntime) ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int) {
	return server.GetTunnel(offset, limit, tunnelType, clientID, search, sort, order)
}

func (defaultRuntime) ListVisibleHosts(offset, limit int, clientIDs []int, search, sort, order string) ([]*file.Host, int) {
	return server.GetHostListByClientIDs(offset, limit, clientIDs, search, sort, order)
}

func (defaultRuntime) TunnelRunning(id int) bool {
	return server.TaskRunning(id)
}

func (defaultRuntime) AddTunnel(tunnel *file.Tunnel) error {
	return server.AddTask(tunnel)
}

func (defaultRuntime) StopTunnel(id int) error {
	return server.StopServer(id)
}

func (defaultRuntime) StartTunnel(id int) error {
	return server.StartTask(id)
}

func (defaultRuntime) DeleteTunnel(id int) error {
	// Persistent task deletion is handled by the service/repository layer.
	return server.StopTaskRuntime(id)
}

func (defaultRuntime) ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int) {
	return server.GetHostList(offset, limit, clientID, search, sort, order)
}

func (defaultRuntime) RemoveHostCache(id int) {
	server.RemoveHostCache(id)
}

type uniqueClientIDVisitor struct {
	seen map[int]struct{}
	fn   func(int) bool
}

func newUniqueClientIDVisitor(capHint int, fn func(int) bool) uniqueClientIDVisitor {
	visitor := uniqueClientIDVisitor{fn: fn}
	if capHint > 0 {
		visitor.seen = make(map[int]struct{}, capHint)
	}
	return visitor
}

func (v *uniqueClientIDVisitor) visit(id int) bool {
	if v.fn == nil || id <= 0 {
		return true
	}
	if v.seen == nil {
		v.seen = make(map[int]struct{})
	}
	if _, exists := v.seen[id]; exists {
		return true
	}
	v.seen[id] = struct{}{}
	return v.fn(id)
}

func normalizeClientIDs(clientIDs []int) []int {
	if len(clientIDs) == 0 {
		return make([]int, 0)
	}
	normalized := make([]int, 0, len(clientIDs))
	visitor := newUniqueClientIDVisitor(len(clientIDs), func(clientID int) bool {
		normalized = append(normalized, clientID)
		return true
	})
	for _, clientID := range clientIDs {
		visitor.visit(clientID)
	}
	return normalized
}

func normalizeSortedUniqueClientIDs(clientIDs []int) []int {
	normalized := normalizeClientIDs(clientIDs)
	if len(normalized) == 0 {
		return nil
	}
	sort.Ints(normalized)
	return normalized
}

func withDeferredPersistence(owner interface{}, run func() error) error {
	if run == nil {
		return nil
	}
	if deferredOwner, ok := owner.(deferredPersistenceRepository); ok {
		return deferredOwner.WithDeferredPersistence(run)
	}
	return run()
}

func ManagedClientIDsByUser(repo interface {
	RangeClients(func(*file.Client) bool)
}, userID int) ([]int, error) {
	if repo == nil || userID <= 0 {
		return nil, nil
	}
	allow := func(client *file.Client) bool {
		return client != nil && client.Status && !client.NoDisplay && client.CanBeManagedByUser(userID)
	}
	if indexedRepo, ok := repo.(managedClientIDsByUserLookupRepository); ok && indexedRepo.SupportsGetManagedClientIDsByUserID() {
		clientIDs, err := indexedRepo.GetManagedClientIDsByUserID(userID)
		if err != nil {
			return nil, err
		}
		if authoritativeRepo, ok := repo.(authoritativeManagedClientIDsByUserLookupRepository); ok && authoritativeRepo.SupportsAuthoritativeManagedClientIDsByUserID() {
			return normalizeSortedUniqueClientIDs(clientIDs), nil
		}
		return collectManagedClientIDsByRange(repo, allow), nil
	}
	return collectManagedClientIDsByRange(repo, allow), nil
}

func AllManagedClientIDsByUser(repo interface {
	RangeClients(func(*file.Client) bool)
}, userID int) ([]int, error) {
	if repo == nil || userID <= 0 {
		return nil, nil
	}
	allow := func(client *file.Client) bool {
		return client != nil && client.CanBeManagedByUser(userID)
	}
	if indexedRepo, ok := repo.(allManagedClientIDsByUserLookupRepository); ok && indexedRepo.SupportsGetAllManagedClientIDsByUserID() {
		clientIDs, err := indexedRepo.GetAllManagedClientIDsByUserID(userID)
		if err != nil {
			return nil, err
		}
		if authoritativeRepo, ok := repo.(authoritativeAllManagedClientIDsByUserLookupRepository); ok && authoritativeRepo.SupportsAuthoritativeAllManagedClientIDsByUserID() {
			return normalizeSortedUniqueClientIDs(clientIDs), nil
		}
		return collectManagedClientIDsByRange(repo, allow), nil
	}
	return collectManagedClientIDsByRange(repo, allow), nil
}

func collectManagedClientIDsByRange(repo interface {
	RangeClients(func(*file.Client) bool)
}, allow func(*file.Client) bool) []int {
	if repo == nil || allow == nil {
		return nil
	}
	clientIDs := make([]int, 0)
	visitor := newUniqueClientIDVisitor(0, func(clientID int) bool {
		clientIDs = append(clientIDs, clientID)
		return true
	})
	repo.RangeClients(func(client *file.Client) bool {
		if allow(client) {
			return visitor.visit(client.Id)
		}
		return true
	})
	if len(clientIDs) == 0 {
		return nil
	}
	sort.Ints(clientIDs)
	return clientIDs
}

func FindClientByVerifyKey(repo interface {
	RangeClients(func(*file.Client) bool)
}, verifyKey string, allow func(*file.Client) (bool, error)) (*file.Client, error) {
	return findClientByVerifyKey(repo, verifyKey, allow, false)
}

func FindClientByVerifyKeyBestEffort(repo interface {
	RangeClients(func(*file.Client) bool)
}, verifyKey string, allow func(*file.Client) bool) *file.Client {
	client, _ := findClientByVerifyKey(repo, verifyKey, func(client *file.Client) (bool, error) {
		if allow == nil {
			return true, nil
		}
		return allow(client), nil
	}, true)
	return client
}

func findClientByVerifyKey(repo interface {
	RangeClients(func(*file.Client) bool)
}, verifyKey string, allow func(*file.Client) (bool, error), ignoreLookupError bool) (*file.Client, error) {
	verifyKey = strings.TrimSpace(verifyKey)
	if repo == nil || verifyKey == "" {
		return nil, nil
	}
	if indexedRepo, ok := repo.(clientByVerifyKeyLookupRepository); ok && indexedRepo.SupportsGetClientByVerifyKey() {
		client, err := indexedRepo.GetClientByVerifyKey(verifyKey)
		switch {
		case err == nil:
			matched, matchErr := matchedVerifyKeyClient(client, verifyKey, allow)
			if matchErr != nil {
				return nil, matchErr
			}
			if matched != nil {
				return matched, nil
			}
		case errors.Is(err, file.ErrClientNotFound):
		case ignoreLookupError:
		default:
			return nil, err
		}
	}
	var matched *file.Client
	var lookupErr error
	repo.RangeClients(func(client *file.Client) bool {
		current, err := matchedVerifyKeyClient(client, verifyKey, allow)
		if err != nil {
			lookupErr = err
			return false
		}
		if current == nil {
			return true
		}
		matched = current
		return false
	})
	return matched, lookupErr
}

func matchedVerifyKeyClient(client *file.Client, verifyKey string, allow func(*file.Client) (bool, error)) (*file.Client, error) {
	if client == nil || strings.TrimSpace(client.VerifyKey) != verifyKey {
		return nil, nil
	}
	if allow == nil {
		return client, nil
	}
	allowed, err := allow(client)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, nil
	}
	return client, nil
}

func rangeOwnedClientIDsByUser(repo ownedClientRangeRepository, userID int, fn func(int) bool) error {
	if repo == nil || userID <= 0 || fn == nil {
		return nil
	}
	if indexedRepo, ok := repo.(clientIDsByUserLookupRepository); ok && indexedRepo.SupportsGetClientIDsByUserID() {
		if authoritativeRepo, ok := repo.(authoritativeClientIDsByUserLookupRepository); ok && authoritativeRepo.SupportsAuthoritativeClientIDsByUserID() {
			return visitClientIDSlice(indexedRepo, userID, fn)
		}
		if clientsRepo, ok := repo.(clientsByUserLookupRepository); ok && clientsRepo.SupportsGetClientsByUserID() {
			return visitOwnedClientSlice(clientsRepo, userID, fn)
		}
		if lookupRepo, ok := supportedClientByIDLookup(repo); ok {
			return visitVerifiedOwnedClientIDs(indexedRepo, lookupRepo, userID, fn)
		}
	}
	return visitOwnedClientIDsByRange(repo, userID, fn)
}

func supportedClientByIDLookup(repo interface{}) (clientByIDLookupRepository, bool) {
	lookupRepo, ok := repo.(clientByIDLookupRepository)
	if !ok {
		return nil, false
	}
	if capabilityRepo, ok := repo.(clientByIDLookupCapabilityRepository); ok && !capabilityRepo.SupportsGetClientByID() {
		return nil, false
	}
	return lookupRepo, true
}

func rangeClientsByIDs(repo interface {
	RangeClients(func(*file.Client) bool)
}, clientIDs []int, fn func(*file.Client) bool) error {
	if repo == nil || fn == nil {
		return nil
	}
	clientIDs = normalizeSortedUniqueClientIDs(clientIDs)
	if len(clientIDs) == 0 {
		return nil
	}
	pending := make(map[int]struct{}, len(clientIDs))
	for _, clientID := range clientIDs {
		pending[clientID] = struct{}{}
	}
	if lookupRepo, ok := supportedClientByIDLookup(repo); ok {
		for _, clientID := range clientIDs {
			client, err := lookupRepo.GetClient(clientID)
			switch {
			case err == nil && client != nil && client.Id == clientID:
				delete(pending, clientID)
				if !fn(client) {
					return nil
				}
			case err == nil, errors.Is(err, file.ErrClientNotFound):
			default:
				return err
			}
		}
		if len(pending) == 0 {
			return nil
		}
	}
	repo.RangeClients(func(client *file.Client) bool {
		if client == nil {
			return true
		}
		if _, ok := pending[client.Id]; !ok {
			return true
		}
		delete(pending, client.Id)
		if !fn(client) {
			return false
		}
		return len(pending) > 0
	})
	return nil
}

func visitOwnedClientIDsByRange(repo ownedClientRangeRepository, userID int, fn func(int) bool) error {
	if repo == nil || userID <= 0 || fn == nil {
		return nil
	}
	if indexedRepo, ok := repo.(clientsByUserLookupRepository); ok && indexedRepo.SupportsGetClientsByUserID() {
		return visitOwnedClientSlice(indexedRepo, userID, fn)
	}
	visitor := newUniqueClientIDVisitor(0, fn)
	repo.RangeClients(func(client *file.Client) bool {
		if client == nil || client.OwnerID() != userID {
			return true
		}
		return visitor.visit(client.Id)
	})
	return nil
}

func visitClientIDSlice(repo clientIDsByUserLookupRepository, userID int, fn func(int) bool) error {
	clientIDs, err := repo.GetClientIDsByUserID(userID)
	if err != nil {
		return err
	}
	visitor := newUniqueClientIDVisitor(len(clientIDs), fn)
	for _, clientID := range clientIDs {
		if !visitor.visit(clientID) {
			break
		}
	}
	return nil
}

func visitOwnedClientSlice(repo clientsByUserLookupRepository, userID int, fn func(int) bool) error {
	clients, err := repo.GetClientsByUserID(userID)
	if err != nil {
		return err
	}
	visitor := newUniqueClientIDVisitor(len(clients), fn)
	for _, client := range clients {
		if client == nil || client.OwnerID() != userID {
			continue
		}
		if !visitor.visit(client.Id) {
			break
		}
	}
	return nil
}

func visitVerifiedOwnedClientIDs(idRepo clientIDsByUserLookupRepository, lookupRepo clientByIDLookupRepository, userID int, fn func(int) bool) error {
	clientIDs, err := idRepo.GetClientIDsByUserID(userID)
	if err != nil {
		return err
	}
	visitor := newUniqueClientIDVisitor(len(clientIDs), fn)
	for _, clientID := range clientIDs {
		if clientID <= 0 {
			continue
		}
		client, err := lookupRepo.GetClient(clientID)
		switch {
		case err == nil:
			if client == nil || client.OwnerID() != userID {
				continue
			}
		case errors.Is(err, file.ErrClientNotFound):
			continue
		default:
			return err
		}
		if !visitor.visit(client.Id) {
			break
		}
	}
	return nil
}

func countOwnedClientsByUser(repo ownedClientRangeRepository, userID, excludedClientID, stopAfter int) (int, error) {
	if repo == nil || userID <= 0 {
		return 0, nil
	}
	count := 0
	err := rangeOwnedClientIDsByUser(repo, userID, func(clientID int) bool {
		if clientID == excludedClientID {
			return true
		}
		count++
		return stopAfter <= 0 || count < stopAfter
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

func countResourcesByClient(repo clientResourceRangeRepository, clientID int) int {
	return countResourcesByClientUpTo(repo, clientID, 0)
}

func countResourcesByClientUpTo(repo clientResourceRangeRepository, clientID, stopAfter int) int {
	if repo == nil || clientID <= 0 {
		return 0
	}
	if countedRepo, ok := repo.(resourceCountByClientRepository); ok && countedRepo.SupportsCountResourcesByClientID() {
		if count, err := countedRepo.CountResourcesByClientID(clientID); err == nil {
			return count
		}
	}
	limitReached := func(count int) bool {
		return stopAfter > 0 && count >= stopAfter
	}
	if scopedRepo, ok := repo.(clientScopedResourceRangeRepository); ok {
		count := 0
		if scopedRepo.SupportsRangeTunnelsByClientID() {
			scopedRepo.RangeTunnelsByClientID(clientID, func(*file.Tunnel) bool {
				count++
				return !limitReached(count)
			})
		} else {
			repo.RangeTunnels(func(tunnel *file.Tunnel) bool {
				if tunnel != nil && tunnel.Client != nil && tunnel.Client.Id == clientID {
					count++
				}
				return !limitReached(count)
			})
		}
		if limitReached(count) {
			return count
		}
		if scopedRepo.SupportsRangeHostsByClientID() {
			scopedRepo.RangeHostsByClientID(clientID, func(*file.Host) bool {
				count++
				return !limitReached(count)
			})
		} else {
			repo.RangeHosts(func(host *file.Host) bool {
				if host != nil && host.Client != nil && host.Client.Id == clientID {
					count++
				}
				return !limitReached(count)
			})
		}
		return count
	}
	count := 0
	repo.RangeTunnels(func(tunnel *file.Tunnel) bool {
		if tunnel != nil && tunnel.Client != nil && tunnel.Client.Id == clientID {
			count++
		}
		return !limitReached(count)
	})
	if limitReached(count) {
		return count
	}
	repo.RangeHosts(func(host *file.Host) bool {
		if host != nil && host.Client != nil && host.Client.Id == clientID {
			count++
		}
		return !limitReached(count)
	})
	return count
}

func collectOwnedResourceCountMaps(repo ownedResourceRangeRepository) (map[int]int, map[int]int, map[int]int) {
	clientCounts := make(map[int]int)
	tunnelCounts := make(map[int]int)
	hostCounts := make(map[int]int)
	if repo == nil {
		return clientCounts, tunnelCounts, hostCounts
	}
	if countedRepo, ok := repo.(ownedResourceCountMapsRepository); ok && countedRepo.SupportsCollectOwnedResourceCounts() {
		if countedClients, countedTunnels, countedHosts, err := countedRepo.CollectOwnedResourceCounts(); err == nil {
			return countedClients, countedTunnels, countedHosts
		}
	}
	if countedRepo, ok := repo.(ownedResourceCountByUserRepository); ok && countedRepo.SupportsCountOwnedResourcesByUserID() {
		if usersRepo, ok := repo.(interface {
			RangeUsers(func(*file.User) bool)
		}); ok {
			if countedClients, countedTunnels, countedHosts, ok := collectOwnedResourceCountMapsByUsers(usersRepo, countedRepo); ok {
				return countedClients, countedTunnels, countedHosts
			}
		}
	}
	repo.RangeClients(func(client *file.Client) bool {
		if client != nil {
			if ownerID := client.OwnerID(); ownerID > 0 {
				clientCounts[ownerID]++
			}
		}
		return true
	})
	repo.RangeTunnels(func(tunnel *file.Tunnel) bool {
		if tunnel != nil && tunnel.Client != nil {
			if ownerID := tunnel.Client.OwnerID(); ownerID > 0 {
				tunnelCounts[ownerID]++
			}
		}
		return true
	})
	repo.RangeHosts(func(host *file.Host) bool {
		if host != nil && host.Client != nil {
			if ownerID := host.Client.OwnerID(); ownerID > 0 {
				hostCounts[ownerID]++
			}
		}
		return true
	})
	return clientCounts, tunnelCounts, hostCounts
}

func collectOwnedResourceCountMapsByUsers(repo interface {
	RangeUsers(func(*file.User) bool)
}, countedRepo ownedResourceCountByUserRepository) (map[int]int, map[int]int, map[int]int, bool) {
	clientCounts := make(map[int]int)
	tunnelCounts := make(map[int]int)
	hostCounts := make(map[int]int)
	if repo == nil || countedRepo == nil {
		return clientCounts, tunnelCounts, hostCounts, false
	}
	ok := true
	repo.RangeUsers(func(user *file.User) bool {
		if user == nil || user.Id <= 0 {
			return true
		}
		clientCount, tunnelCount, hostCount, err := countedRepo.CountOwnedResourcesByUserID(user.Id)
		if err != nil {
			ok = false
			return false
		}
		if clientCount > 0 {
			clientCounts[user.Id] = clientCount
		}
		if tunnelCount > 0 {
			tunnelCounts[user.Id] = tunnelCount
		}
		if hostCount > 0 {
			hostCounts[user.Id] = hostCount
		}
		return true
	})
	return clientCounts, tunnelCounts, hostCounts, ok
}

func OwnedResourceCountsByUser(repo interface {
	RangeClients(func(*file.Client) bool)
	RangeTunnels(func(*file.Tunnel) bool)
	RangeHosts(func(*file.Host) bool)
}, userID int) (int, int, int) {
	if repo == nil || userID <= 0 {
		return 0, 0, 0
	}
	if countedRepo, ok := repo.(ownedResourceCountByUserRepository); ok && countedRepo.SupportsCountOwnedResourcesByUserID() {
		if clientCount, tunnelCount, hostCount, err := countedRepo.CountOwnedResourcesByUserID(userID); err == nil {
			return clientCount, tunnelCount, hostCount
		}
	}
	if resourceRepo, ok := repo.(clientResourceRangeRepository); ok && canCountOwnedResourcesByOwnedClients(repo, resourceRepo) {
		if clientCount, tunnelCount, hostCount, err := countOwnedResourcesByOwnedClients(repo, resourceRepo, userID); err == nil {
			return clientCount, tunnelCount, hostCount
		}
	}
	clientCounts, tunnelCounts, hostCounts := collectOwnedResourceCountMaps(repo)
	return clientCounts[userID], tunnelCounts[userID], hostCounts[userID]
}

func canCountOwnedResourcesByOwnedClients(repo ownedClientRangeRepository, resourceRepo clientResourceRangeRepository) bool {
	if repo == nil || resourceRepo == nil {
		return false
	}
	if indexedRepo, ok := repo.(clientIDsByUserLookupRepository); ok && indexedRepo.SupportsGetClientIDsByUserID() {
		return true
	}
	if indexedRepo, ok := repo.(clientsByUserLookupRepository); ok && indexedRepo.SupportsGetClientsByUserID() {
		return true
	}
	if scopedRepo, ok := resourceRepo.(clientScopedResourceRangeRepository); ok {
		return scopedRepo.SupportsRangeTunnelsByClientID() || scopedRepo.SupportsRangeHostsByClientID()
	}
	return false
}

func countOwnedResourcesByOwnedClients(repo ownedClientRangeRepository, resourceRepo clientResourceRangeRepository, userID int) (int, int, int, error) {
	if repo == nil || resourceRepo == nil || userID <= 0 {
		return 0, 0, 0, nil
	}
	scopedRepo, _ := resourceRepo.(clientScopedResourceRangeRepository)
	useScopedTunnels := scopedRepo != nil && scopedRepo.SupportsRangeTunnelsByClientID()
	useScopedHosts := scopedRepo != nil && scopedRepo.SupportsRangeHostsByClientID()
	clientCount := 0
	tunnelCount := 0
	hostCount := 0
	clientSet := make(map[int]struct{})
	err := rangeOwnedClientIDsByUser(repo, userID, func(clientID int) bool {
		if clientID <= 0 {
			return true
		}
		clientCount++
		clientSet[clientID] = struct{}{}
		if useScopedTunnels {
			scopedRepo.RangeTunnelsByClientID(clientID, func(*file.Tunnel) bool {
				tunnelCount++
				return true
			})
		}
		if useScopedHosts {
			scopedRepo.RangeHostsByClientID(clientID, func(*file.Host) bool {
				hostCount++
				return true
			})
		}
		return true
	})
	if err != nil {
		return 0, 0, 0, err
	}
	if len(clientSet) == 0 {
		return 0, 0, 0, nil
	}
	if !useScopedTunnels {
		resourceRepo.RangeTunnels(func(tunnel *file.Tunnel) bool {
			if tunnel != nil && tunnel.Client != nil {
				if _, ok := clientSet[tunnel.Client.Id]; ok {
					tunnelCount++
				}
			}
			return true
		})
	}
	if !useScopedHosts {
		resourceRepo.RangeHosts(func(host *file.Host) bool {
			if host != nil && host.Client != nil {
				if _, ok := clientSet[host.Client.Id]; ok {
					hostCount++
				}
			}
			return true
		})
	}
	return clientCount, tunnelCount, hostCount, nil
}
