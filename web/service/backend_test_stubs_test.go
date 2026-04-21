package service

import (
	"errors"

	"github.com/djylb/nps/lib/file"
)

type stubRepository struct {
	rangeUsers                       func(func(*file.User) bool)
	rangeClients                     func(func(*file.Client) bool)
	rangeTunnels                     func(func(*file.Tunnel) bool)
	rangeHosts                       func(func(*file.Host) bool)
	withDeferredPersistence          func(func() error) error
	listVisibleClient                func(ListClientsInput) ([]*file.Client, int)
	nextUserID                       func() int
	nextClientID                     func() int
	createUser                       func(*file.User) error
	createClient                     func(*file.Client) error
	nextTunnelID                     func() int
	nextHostID                       func() int
	createTunnel                     func(*file.Tunnel) error
	createHost                       func(*file.Host) error
	getUser                          func(int) (*file.User, error)
	getUserByUsername                func(string) (*file.User, error)
	getUserByPlatformID              func(string) (*file.User, error)
	getClientByVerifyKey             func(string) (*file.Client, error)
	getClientsByUserID               func(int) ([]*file.Client, error)
	getClientIDsByUserID             func(int) ([]int, error)
	getAllManagedClientIDs           func(int) ([]int, error)
	authoritativeClientIDsByUserID   bool
	authoritativeManagedClientIDs    bool
	authoritativeAllManagedClientIDs bool
	deleteUserCascadeClientRefs      bool
	detachedSnapshots                bool
	getManagedClientIDs              func(int) ([]int, error)
	countResourcesByID               func(int) (int, error)
	collectOwnedCounts               func() (map[int]int, map[int]int, map[int]int, error)
	countOwnedByUser                 func(int) (int, int, int, error)
	rangeTunnelsByID                 func(int, func(*file.Tunnel) bool)
	rangeHostsByID                   func(int, func(*file.Host) bool)
	saveUser                         func(*file.User) error
	deleteUser                       func(int) error
	getClient                        func(int) (*file.Client, error)
	saveClient                       func(*file.Client) error
	deleteClient                     func(int) error
	getTunnel                        func(int) (*file.Tunnel, error)
	saveTunnel                       func(*file.Tunnel) error
	deleteTunnelRecord               func(int) error
	getHost                          func(int) (*file.Host, error)
	saveHost                         func(*file.Host, string) error
	deleteHostRecord                 func(int) error
}

func (s stubRepository) RangeClients(fn func(*file.Client) bool) {
	if s.rangeClients != nil {
		s.rangeClients(fn)
	}
}

func (s stubRepository) RangeUsers(fn func(*file.User) bool) {
	if s.rangeUsers != nil {
		s.rangeUsers(fn)
	}
}

func (s stubRepository) RangeTunnels(fn func(*file.Tunnel) bool) {
	if s.rangeTunnels != nil {
		s.rangeTunnels(fn)
	}
}
func (s stubRepository) RangeHosts(fn func(*file.Host) bool) {
	if s.rangeHosts != nil {
		s.rangeHosts(fn)
	}
}
func (s stubRepository) ListVisibleClients(input ListClientsInput) ([]*file.Client, int) {
	if s.listVisibleClient != nil {
		return s.listVisibleClient(input)
	}
	return nil, 0
}
func (s stubRepository) NextUserID() int {
	if s.nextUserID != nil {
		return s.nextUserID()
	}
	return 0
}
func (s stubRepository) NextClientID() int {
	if s.nextClientID != nil {
		return s.nextClientID()
	}
	return 0
}
func (s stubRepository) NextTunnelID() int {
	if s.nextTunnelID != nil {
		return s.nextTunnelID()
	}
	return 0
}
func (s stubRepository) NextHostID() int {
	if s.nextHostID != nil {
		return s.nextHostID()
	}
	return 0
}
func (s stubRepository) CreateUser(user *file.User) error {
	if s.createUser != nil {
		return s.createUser(user)
	}
	return nil
}
func (s stubRepository) GetUser(id int) (*file.User, error) {
	if s.getUser != nil {
		user, err := s.getUser(id)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return user, nil
		}
		return cloneUserForMutation(user), nil
	}
	return nil, nil
}
func (s stubRepository) GetUserByUsername(username string) (*file.User, error) {
	if s.getUserByUsername != nil {
		user, err := s.getUserByUsername(username)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return user, nil
		}
		return cloneUserForMutation(user), nil
	}
	return nil, nil
}
func (s stubRepository) GetUserByExternalPlatformID(platformID string) (*file.User, error) {
	if s.getUserByPlatformID != nil {
		user, err := s.getUserByPlatformID(platformID)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return user, nil
		}
		return cloneUserForMutation(user), nil
	}
	return nil, file.ErrUserNotFound
}
func (s stubRepository) SupportsGetUserByExternalPlatformID() bool {
	return s.getUserByPlatformID != nil
}
func (s stubRepository) SaveUser(user *file.User) error {
	if s.saveUser != nil {
		return s.saveUser(user)
	}
	return nil
}
func (s stubRepository) DeleteUser(id int) error {
	if s.deleteUser != nil {
		return s.deleteUser(id)
	}
	return nil
}
func (s stubRepository) CreateClient(client *file.Client) error {
	if s.createClient != nil {
		return s.createClient(client)
	}
	return nil
}
func (s stubRepository) GetClient(id int) (*file.Client, error) {
	if s.getClient != nil {
		client, err := s.getClient(id)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return client, nil
		}
		return cloneClientForMutation(client), nil
	}
	return nil, nil
}
func (s stubRepository) GetClientByVerifyKey(vkey string) (*file.Client, error) {
	if s.getClientByVerifyKey != nil {
		client, err := s.getClientByVerifyKey(vkey)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return client, nil
		}
		return cloneClientForMutation(client), nil
	}
	return nil, file.ErrClientNotFound
}
func (s stubRepository) SupportsGetClientByVerifyKey() bool {
	return s.getClientByVerifyKey != nil
}
func (s stubRepository) GetClientsByUserID(userID int) ([]*file.Client, error) {
	if s.getClientsByUserID != nil {
		clients, err := s.getClientsByUserID(userID)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return clients, nil
		}
		return cloneClientList(clients), nil
	}
	return nil, nil
}
func (s stubRepository) SupportsGetClientsByUserID() bool {
	return s.getClientsByUserID != nil
}
func (s stubRepository) GetClientIDsByUserID(userID int) ([]int, error) {
	if s.getClientIDsByUserID != nil {
		clientIDs, err := s.getClientIDsByUserID(userID)
		if err != nil {
			return nil, err
		}
		return append([]int(nil), clientIDs...), nil
	}
	return nil, nil
}
func (s stubRepository) SupportsGetClientIDsByUserID() bool {
	return s.getClientIDsByUserID != nil
}
func (s stubRepository) SupportsAuthoritativeClientIDsByUserID() bool {
	return s.authoritativeClientIDsByUserID
}
func (s stubRepository) SupportsAuthoritativeManagedClientIDsByUserID() bool {
	return s.authoritativeManagedClientIDs
}
func (s stubRepository) SupportsAuthoritativeAllManagedClientIDsByUserID() bool {
	return s.authoritativeAllManagedClientIDs
}
func (s stubRepository) SupportsDeleteUserCascadeClientRefs() bool {
	return s.deleteUserCascadeClientRefs
}
func (s stubRepository) SupportsDetachedSnapshots() bool {
	return s.detachedSnapshots
}
func (s stubRepository) GetManagedClientIDsByUserID(userID int) ([]int, error) {
	if s.getManagedClientIDs != nil {
		clientIDs, err := s.getManagedClientIDs(userID)
		if err != nil {
			return nil, err
		}
		return append([]int(nil), clientIDs...), nil
	}
	return nil, nil
}
func (s stubRepository) SupportsGetManagedClientIDsByUserID() bool {
	return s.getManagedClientIDs != nil
}
func (s stubRepository) GetAllManagedClientIDsByUserID(userID int) ([]int, error) {
	if s.getAllManagedClientIDs != nil {
		clientIDs, err := s.getAllManagedClientIDs(userID)
		if err != nil {
			return nil, err
		}
		return append([]int(nil), clientIDs...), nil
	}
	return nil, nil
}
func (s stubRepository) SupportsGetAllManagedClientIDsByUserID() bool {
	return s.getAllManagedClientIDs != nil
}
func (s stubRepository) SupportsGetClientByID() bool {
	return s.getClient != nil
}
func (s stubRepository) CountResourcesByClientID(clientID int) (int, error) {
	if s.countResourcesByID != nil {
		return s.countResourcesByID(clientID)
	}
	return 0, nil
}
func (s stubRepository) SupportsCountResourcesByClientID() bool {
	return s.countResourcesByID != nil
}
func (s stubRepository) CollectOwnedResourceCounts() (map[int]int, map[int]int, map[int]int, error) {
	if s.collectOwnedCounts != nil {
		return s.collectOwnedCounts()
	}
	return nil, nil, nil, nil
}
func (s stubRepository) SupportsCollectOwnedResourceCounts() bool {
	return s.collectOwnedCounts != nil
}
func (s stubRepository) CountOwnedResourcesByUserID(userID int) (int, int, int, error) {
	if s.countOwnedByUser != nil {
		return s.countOwnedByUser(userID)
	}
	return 0, 0, 0, nil
}
func (s stubRepository) SupportsCountOwnedResourcesByUserID() bool {
	return s.countOwnedByUser != nil
}
func (s stubRepository) RangeTunnelsByClientID(clientID int, fn func(*file.Tunnel) bool) {
	if s.rangeTunnelsByID != nil {
		s.rangeTunnelsByID(clientID, fn)
	}
}
func (s stubRepository) SupportsRangeTunnelsByClientID() bool {
	return s.rangeTunnelsByID != nil
}
func (s stubRepository) RangeHostsByClientID(clientID int, fn func(*file.Host) bool) {
	if s.rangeHostsByID != nil {
		s.rangeHostsByID(clientID, fn)
	}
}
func (s stubRepository) SupportsRangeHostsByClientID() bool {
	return s.rangeHostsByID != nil
}
func (s stubRepository) SaveClient(client *file.Client) error {
	if s.saveClient != nil {
		return s.saveClient(client)
	}
	return nil
}
func (s stubRepository) WithDeferredPersistence(run func() error) error {
	if s.withDeferredPersistence != nil {
		return s.withDeferredPersistence(run)
	}
	if run == nil {
		return nil
	}
	return run()
}
func (stubRepository) PersistClients() error { return nil }
func (s stubRepository) DeleteClient(id int) error {
	if s.deleteClient != nil {
		return s.deleteClient(id)
	}
	return nil
}
func (stubRepository) VerifyUserName(string, int) bool            { return true }
func (stubRepository) VerifyVKey(string, int) bool                { return true }
func (stubRepository) ReplaceClientVKeyIndex(string, string, int) {}
func (s stubRepository) CreateTunnel(tunnel *file.Tunnel) error {
	if s.createTunnel != nil {
		return s.createTunnel(tunnel)
	}
	return nil
}
func (s stubRepository) GetTunnel(id int) (*file.Tunnel, error) {
	if s.getTunnel != nil {
		tunnel, err := s.getTunnel(id)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return tunnel, nil
		}
		return cloneTunnelForMutation(tunnel), nil
	}
	return nil, nil
}
func (s stubRepository) SaveTunnel(tunnel *file.Tunnel) error {
	if s.saveTunnel != nil {
		return s.saveTunnel(tunnel)
	}
	return nil
}
func (s stubRepository) DeleteTunnelRecord(id int) error {
	if s.deleteTunnelRecord != nil {
		return s.deleteTunnelRecord(id)
	}
	return nil
}
func (s stubRepository) CreateHost(host *file.Host) error {
	if s.createHost != nil {
		return s.createHost(host)
	}
	return nil
}
func (s stubRepository) GetHost(id int) (*file.Host, error) {
	if s.getHost != nil {
		host, err := s.getHost(id)
		if err != nil {
			return nil, err
		}
		if s.detachedSnapshots {
			return host, nil
		}
		return cloneHostForMutation(host), nil
	}
	return nil, nil
}
func (s stubRepository) SaveHost(host *file.Host, oldHost string) error {
	if s.saveHost != nil {
		return s.saveHost(host, oldHost)
	}
	return nil
}
func (stubRepository) PersistHosts() error { return nil }
func (s stubRepository) DeleteHostRecord(id int) error {
	if s.deleteHostRecord != nil {
		return s.deleteHostRecord(id)
	}
	return nil
}
func (stubRepository) HostExists(*file.Host) bool     { return false }
func (stubRepository) GetGlobal() *file.Glob          { return nil }
func (stubRepository) SaveGlobal(*file.Glob) error    { return nil }
func (stubRepository) ClientOwnsTunnel(int, int) bool { return false }
func (stubRepository) ClientOwnsHost(int, int) bool   { return false }

type stubRuntime struct {
	dashboardData         func(bool) map[string]interface{}
	generatePort          func(*file.Tunnel) int
	portAvailable         func(*file.Tunnel) bool
	addTunnel             func(*file.Tunnel) error
	disconnectClient      func(int)
	clientConnectionCount func(int) int
	deleteClientResources func(int)
	stopTunnel            func(int) error
	startTunnel           func(int) error
	deleteTunnel          func(int) error
	tunnelRunning         func(int) bool
	listTunnels           func(int, int, string, int, string, string, string) ([]*file.Tunnel, int)
	listVisibleTunnels    func(int, int, string, []int, string, string, string) ([]*file.Tunnel, int)
	listHosts             func(int, int, int, string, string, string) ([]*file.Host, int)
	listVisibleHosts      func(int, int, []int, string, string, string) ([]*file.Host, int)
	removeHostCache       func(int)
}

func (s stubRuntime) DashboardData(force bool) map[string]interface{} {
	if s.dashboardData != nil {
		return s.dashboardData(force)
	}
	return nil
}

func (stubRuntime) ListClients(int, int, string, string, string, int) ([]*file.Client, int) {
	return nil, 0
}
func (s stubRuntime) ListVisibleTunnels(offset, limit int, tunnelType string, clientIDs []int, search, sort, order string) ([]*file.Tunnel, int) {
	if s.listVisibleTunnels != nil {
		return s.listVisibleTunnels(offset, limit, tunnelType, clientIDs, search, sort, order)
	}
	return nil, 0
}
func (stubRuntime) PingClient(int, string) int { return 0 }
func (s stubRuntime) ClientConnectionCount(id int) int {
	if s.clientConnectionCount != nil {
		return s.clientConnectionCount(id)
	}
	return 0
}
func (stubRuntime) ListClientConnections(int) []ClientConnection {
	return nil
}
func (s stubRuntime) DisconnectClient(id int) {
	if s.disconnectClient != nil {
		s.disconnectClient(id)
	}
}
func (s stubRuntime) DeleteClientResources(id int) {
	if s.deleteClientResources != nil {
		s.deleteClientResources(id)
	}
}
func (s stubRuntime) GenerateTunnelPort(tunnel *file.Tunnel) int {
	if s.generatePort != nil {
		return s.generatePort(tunnel)
	}
	return 0
}
func (s stubRuntime) TunnelPortAvailable(tunnel *file.Tunnel) bool {
	if s.portAvailable != nil {
		return s.portAvailable(tunnel)
	}
	return true
}
func (s stubRuntime) ListTunnels(offset, limit int, tunnelType string, clientID int, search, sort, order string) ([]*file.Tunnel, int) {
	if s.listTunnels != nil {
		return s.listTunnels(offset, limit, tunnelType, clientID, search, sort, order)
	}
	return nil, 0
}
func (s stubRuntime) TunnelRunning(id int) bool {
	if s.tunnelRunning != nil {
		return s.tunnelRunning(id)
	}
	return false
}
func (s stubRuntime) AddTunnel(tunnel *file.Tunnel) error {
	if s.addTunnel != nil {
		return s.addTunnel(tunnel)
	}
	return nil
}
func (s stubRuntime) StopTunnel(id int) error {
	if s.stopTunnel != nil {
		return s.stopTunnel(id)
	}
	return nil
}
func (s stubRuntime) StartTunnel(id int) error {
	if s.startTunnel != nil {
		return s.startTunnel(id)
	}
	return nil
}
func (s stubRuntime) DeleteTunnel(id int) error {
	if s.deleteTunnel != nil {
		return s.deleteTunnel(id)
	}
	return nil
}
func (s stubRuntime) ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int) {
	if s.listHosts != nil {
		return s.listHosts(offset, limit, clientID, search, sort, order)
	}
	return nil, 0
}
func (s stubRuntime) ListVisibleHosts(offset, limit int, clientIDs []int, search, sort, order string) ([]*file.Host, int) {
	if s.listVisibleHosts != nil {
		return s.listVisibleHosts(offset, limit, clientIDs, search, sort, order)
	}
	return nil, 0
}
func (s stubRuntime) RemoveHostCache(id int) {
	if s.removeHostCache != nil {
		s.removeHostCache(id)
	}
}

type quotaStore struct {
	users        map[int]*file.User
	tunnelCounts map[int]int
	hostCounts   map[int]int
	getUser      func(int) (*file.User, error)
}

func (s quotaStore) GetUser(id int) (*file.User, error) {
	if s.getUser != nil {
		return s.getUser(id)
	}
	if user, ok := s.users[id]; ok {
		return user, nil
	}
	return nil, errors.New("not found")
}

func (quotaStore) GetUserByUsername(string) (*file.User, error) {
	return nil, errors.New("not implemented")
}
func (quotaStore) CreateUser(*file.User) error             { return errors.New("not implemented") }
func (quotaStore) UpdateUser(*file.User) error             { return errors.New("not implemented") }
func (quotaStore) GetClient(string) (*file.Client, error)  { return nil, errors.New("not implemented") }
func (quotaStore) GetClientByID(int) (*file.Client, error) { return nil, errors.New("not implemented") }
func (quotaStore) UpdateClient(*file.Client) error         { return errors.New("not implemented") }
func (quotaStore) GetAllClients() []*file.Client           { return nil }
func (quotaStore) GetClientsByUserId(int) []*file.Client   { return nil }
func (s quotaStore) OwnedResourceCountsByUserID(userID int) ownedUserResourceCounts {
	return ownedUserResourceCounts{
		Tunnels: s.tunnelCounts[userID],
		Hosts:   s.hostCounts[userID],
	}
}
func (quotaStore) AddTraffic(int, int64, int64) {}
func (quotaStore) Flush() error                 { return nil }
