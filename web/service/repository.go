package service

import (
	"errors"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/server"
)

type defaultRepository struct{}

func cloneClientList(clients []*file.Client) []*file.Client {
	return cloneClientWindow(clients, 0, 0)
}

func cloneClientWindow(clients []*file.Client, offset, limit int) []*file.Client {
	start, end, ok := sliceWindowBounds(len(clients), offset, limit)
	if !ok {
		return nil
	}
	clients = clients[start:end]
	if len(clients) == 0 {
		return nil
	}
	cloned := make([]*file.Client, 0, len(clients))
	for _, client := range clients {
		cloned = append(cloned, cloneClientSnapshotForMutation(client))
	}
	return cloned
}

func sliceWindowBounds(length, offset, limit int) (int, int, bool) {
	if length <= 0 {
		return 0, 0, false
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= length {
		return 0, 0, false
	}
	end := length
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return offset, end, true
}

func cloneGlobal(glob *file.Glob) *file.Glob {
	if glob == nil {
		return nil
	}
	glob.RLock()
	defer glob.RUnlock()
	return &file.Glob{
		EntryAclMode:  glob.EntryAclMode,
		EntryAclRules: glob.EntryAclRules,
	}
}

func (defaultRepository) RangeClients(fn func(*file.Client) bool) {
	if fn == nil {
		return
	}
	defaultRepository{}.rangeStoredClients(func(client *file.Client) bool {
		return fn(cloneClientSnapshotForMutation(client))
	})
}

func (defaultRepository) RangeUsers(fn func(*file.User) bool) {
	if fn == nil {
		return
	}
	defaultRepository{}.rangeStoredUsers(func(user *file.User) bool {
		return fn(cloneUserForMutation(user))
	})
}

func (defaultRepository) RangeTunnels(fn func(*file.Tunnel) bool) {
	if fn == nil {
		return
	}
	defaultRepository{}.rangeStoredTunnels(func(tunnel *file.Tunnel) bool {
		return fn(cloneTunnelSnapshot(tunnel))
	})
}

func (defaultRepository) RangeTunnelsByClientID(clientID int, fn func(*file.Tunnel) bool) {
	if clientID <= 0 || fn == nil {
		return
	}
	file.GetDb().RangeTunnelsByClientID(clientID, func(tunnel *file.Tunnel) bool {
		return fn(cloneTunnelSnapshot(tunnel))
	})
}

func (defaultRepository) SupportsRangeTunnelsByClientID() bool {
	return true
}

func (defaultRepository) RangeHosts(fn func(*file.Host) bool) {
	if fn == nil {
		return
	}
	defaultRepository{}.rangeStoredHosts(func(host *file.Host) bool {
		return fn(cloneHostSnapshot(host))
	})
}

func (defaultRepository) RangeHostsByClientID(clientID int, fn func(*file.Host) bool) {
	if clientID <= 0 || fn == nil {
		return
	}
	file.GetDb().RangeHostsByClientID(clientID, func(host *file.Host) bool {
		return fn(cloneHostSnapshot(host))
	})
}

func (defaultRepository) SupportsRangeHostsByClientID() bool {
	return true
}

func (defaultRepository) ListVisibleClients(input ListClientsInput) ([]*file.Client, int) {
	scope := visibleClientScope(input.Visibility)
	if input.Visibility.IsAdmin {
		clientID := scope.firstID()
		return server.GetClientList(input.Offset, input.Limit, input.Search, input.Sort, input.Order, clientID)
	}

	if len(scope.ids) == 0 {
		return nil, 0
	}
	server.RefreshClientDataSet(scope.set)
	list := defaultRepository{}.collectVisibleStoredClients(scope.set, input.Search)
	server.SortClientList(list, input.Sort, input.Order)
	return cloneClientWindow(list, input.Offset, input.Limit), len(list)
}

func (defaultRepository) NextClientID() int {
	return file.GetDb().NextClientID()
}

func (defaultRepository) NextUserID() int {
	return file.GetDb().NextUserID()
}

func (defaultRepository) NextTunnelID() int {
	return file.GetDb().NextTaskID()
}

func (defaultRepository) NextHostID() int {
	return file.GetDb().NextHostID()
}

func (defaultRepository) CreateUser(user *file.User) error {
	return file.GetDb().NewUser(user)
}

func (defaultRepository) GetUser(id int) (*file.User, error) {
	user, err := file.GetDb().GetUser(id)
	if err != nil {
		return nil, err
	}
	return cloneUserForMutation(user), nil
}

func (defaultRepository) GetUserByUsername(username string) (*file.User, error) {
	user, err := file.GetDb().GetUserByUsername(username)
	if err != nil {
		return nil, err
	}
	return cloneUserForMutation(user), nil
}

func (defaultRepository) GetUserByExternalPlatformID(platformID string) (*file.User, error) {
	user, err := file.GetDb().GetUserByExternalPlatformID(platformID)
	if err != nil {
		return nil, err
	}
	return cloneUserForMutation(user), nil
}

func (defaultRepository) SupportsGetUserByExternalPlatformID() bool {
	return true
}

func (defaultRepository) SaveUser(user *file.User) error {
	return file.GetDb().UpdateUser(user)
}

func (defaultRepository) DeleteUser(id int) error {
	return file.GetDb().DelUser(id)
}

func (defaultRepository) SupportsDeleteUserCascadeClientRefs() bool {
	return true
}

func (defaultRepository) SupportsDetachedSnapshots() bool {
	return true
}

func (defaultRepository) CreateClient(client *file.Client) error {
	return file.GetDb().NewClient(client)
}

func (defaultRepository) GetClient(id int) (*file.Client, error) {
	server.RefreshClientDataByID(id)
	client, err := file.GetDb().GetClient(id)
	if err != nil {
		return nil, err
	}
	return cloneClientSnapshotForMutation(client), nil
}

func (defaultRepository) GetClientByVerifyKey(vkey string) (*file.Client, error) {
	client, err := file.GetDb().GetClientByVerifyKey(vkey)
	if err != nil {
		return nil, err
	}
	server.RefreshClientDataByID(client.Id)
	return cloneClientSnapshotForMutation(client), nil
}

func (defaultRepository) SupportsGetClientByVerifyKey() bool {
	return true
}

func (defaultRepository) GetClientsByUserID(userID int) ([]*file.Client, error) {
	return cloneClientList(file.GetDb().GetClientsByUserId(userID)), nil
}

func (defaultRepository) SupportsGetClientsByUserID() bool {
	return true
}

func (defaultRepository) GetClientIDsByUserID(userID int) ([]int, error) {
	return append([]int(nil), file.GetDb().GetClientIDsByUserId(userID)...), nil
}

func (defaultRepository) SupportsGetClientIDsByUserID() bool {
	return true
}

func (defaultRepository) SupportsAuthoritativeClientIDsByUserID() bool {
	return true
}

func (defaultRepository) GetManagedClientIDsByUserID(userID int) ([]int, error) {
	if userID <= 0 {
		return make([]int, 0), nil
	}
	return append([]int(nil), file.GetDb().GetVisibleManagedClientIDsByUserId(userID)...), nil
}

func (defaultRepository) SupportsGetManagedClientIDsByUserID() bool {
	return true
}

func (defaultRepository) SupportsAuthoritativeManagedClientIDsByUserID() bool {
	return true
}

func (defaultRepository) GetAllManagedClientIDsByUserID(userID int) ([]int, error) {
	if userID <= 0 {
		return make([]int, 0), nil
	}
	return append([]int(nil), file.GetDb().GetAllManagedClientIDsByUserId(userID)...), nil
}

func (defaultRepository) SupportsGetAllManagedClientIDsByUserID() bool {
	return true
}

func (defaultRepository) SupportsAuthoritativeAllManagedClientIDsByUserID() bool {
	return true
}

func (defaultRepository) SupportsGetClientByID() bool {
	return true
}

func (defaultRepository) CountResourcesByClientID(clientID int) (int, error) {
	if clientID <= 0 {
		return 0, nil
	}
	return defaultRepository{}.countStoredResourcesByClientID(clientID), nil
}

func (defaultRepository) SupportsCountResourcesByClientID() bool {
	return true
}

func (defaultRepository) CollectOwnedResourceCounts() (map[int]int, map[int]int, map[int]int, error) {
	clientCounts, tunnelCounts, hostCounts := file.GetDb().CollectOwnedResourceCounts()
	return clientCounts, tunnelCounts, hostCounts, nil
}

func (defaultRepository) SupportsCollectOwnedResourceCounts() bool {
	return true
}

func (defaultRepository) CountOwnedResourcesByUserID(userID int) (int, int, int, error) {
	if userID <= 0 {
		return 0, 0, 0, nil
	}
	clientCount, tunnelCount, hostCount := file.GetDb().CountOwnedResourcesByUserID(userID)
	return clientCount, tunnelCount, hostCount, nil
}

func (defaultRepository) SupportsCountOwnedResourcesByUserID() bool {
	return true
}

func (defaultRepository) SaveClient(client *file.Client) error {
	if client == nil {
		return errors.New("client is nil")
	}
	return file.GetDb().UpdateClient(client)
}

func (defaultRepository) PersistClients() error {
	file.GetDb().StoreClients()
	return nil
}

func (defaultRepository) WithDeferredPersistence(run func() error) error {
	return file.GetDb().WithDeferredPersistence(run)
}

func (defaultRepository) DeleteClient(id int) error {
	return file.GetDb().DelClient(id)
}

func (defaultRepository) VerifyUserName(username string, exceptID int) bool {
	return file.GetDb().VerifyUserName(username, exceptID)
}

func (defaultRepository) VerifyVKey(vkey string, exceptID int) bool {
	return file.GetDb().VerifyVkey(vkey, exceptID)
}

func (defaultRepository) ReplaceClientVKeyIndex(oldKey, newKey string, clientID int) {
	file.CurrentBlake2bVkeyIndex().Remove(crypt.Blake2b(oldKey))
	file.CurrentBlake2bVkeyIndex().Add(crypt.Blake2b(newKey), clientID)
}

func (defaultRepository) CreateTunnel(tunnel *file.Tunnel) error {
	return file.GetDb().NewTask(tunnel)
}

func (defaultRepository) GetTunnel(id int) (*file.Tunnel, error) {
	tunnel, err := file.GetDb().GetTask(id)
	if err != nil {
		return nil, err
	}
	if tunnel.Client != nil {
		server.RefreshClientDataByID(tunnel.Client.Id)
	}
	return cloneTunnelSnapshot(tunnel), nil
}

func (defaultRepository) SaveTunnel(tunnel *file.Tunnel) error {
	file.InitializeTunnelRuntime(tunnel)
	return file.GetDb().UpdateTask(tunnel)
}

func (defaultRepository) DeleteTunnelRecord(id int) error {
	return file.GetDb().DelTask(id)
}

func (defaultRepository) CreateHost(host *file.Host) error {
	return file.GetDb().NewHost(host)
}

func (defaultRepository) GetHost(id int) (*file.Host, error) {
	host, err := file.GetDb().GetHostById(id)
	if err != nil {
		return nil, err
	}
	if host.Client != nil {
		server.RefreshClientDataByID(host.Client.Id)
	}
	return cloneHostSnapshot(host), nil
}

func (defaultRepository) SaveHost(host *file.Host, _ string) error {
	if host == nil {
		return errors.New("host is nil")
	}
	return file.GetDb().UpdateHost(host)
}

func (defaultRepository) PersistHosts() error {
	file.GetDb().StoreHosts()
	return nil
}

func (defaultRepository) DeleteHostRecord(id int) error {
	return file.GetDb().DelHost(id)
}

func (defaultRepository) HostExists(host *file.Host) bool {
	return file.GetDb().IsHostExist(host)
}

func (defaultRepository) GetGlobal() *file.Glob {
	return cloneGlobal(file.GetDb().GetGlobal())
}

func (defaultRepository) SaveGlobal(glob *file.Glob) error {
	return file.GetDb().SaveGlobal(cloneGlobal(glob))
}

func (defaultRepository) ClientOwnsTunnel(clientID, tunnelID int) bool {
	if clientID <= 0 || tunnelID <= 0 {
		return false
	}
	if tunnel, ok := (defaultRepository{}).loadStoredTunnel(tunnelID); ok {
		return tunnel.Client != nil && tunnel.Client.Id == clientID
	}
	return false
}

func (defaultRepository) ClientOwnsHost(clientID, hostID int) bool {
	if clientID <= 0 || hostID <= 0 {
		return false
	}
	if host, ok := (defaultRepository{}).loadStoredHost(hostID); ok {
		return host.Client != nil && host.Client.Id == clientID
	}
	return false
}

func (r defaultRepository) countStoredResourcesByClientID(clientID int) int {
	return file.GetDb().CountResourcesByClientID(clientID)
}

func (defaultRepository) rangeStoredClients(fn func(*file.Client) bool) {
	file.GetDb().RangeClients(fn)
}

func (defaultRepository) rangeStoredUsers(fn func(*file.User) bool) {
	file.GetDb().RangeUsers(fn)
}

func (defaultRepository) rangeStoredTunnels(fn func(*file.Tunnel) bool) {
	file.GetDb().RangeTasks(fn)
}

func (defaultRepository) rangeStoredHosts(fn func(*file.Host) bool) {
	file.GetDb().RangeHosts(fn)
}

func (defaultRepository) loadStoredTunnel(tunnelID int) (*file.Tunnel, bool) {
	tunnel, err := file.GetDb().GetTask(tunnelID)
	return tunnel, err == nil
}

func (defaultRepository) loadStoredHost(hostID int) (*file.Host, bool) {
	host, err := file.GetDb().GetHostById(hostID)
	return host, err == nil
}

func (defaultRepository) loadStoredClient(clientID int) (*file.Client, bool) {
	client, err := file.GetDb().GetClient(clientID)
	return client, err == nil
}

func (r defaultRepository) collectVisibleStoredClients(visible map[int]struct{}, search string) []*file.Client {
	if len(visible) == 0 {
		return nil
	}
	searchID := common.GetIntNoErrByStr(search)
	list := make([]*file.Client, 0, len(visible))
	for clientID := range visible {
		client, ok := r.loadStoredClient(clientID)
		if !ok || client == nil {
			continue
		}
		if client.NoDisplay {
			continue
		}
		if !matchesVisibleClientSearch(client, search, searchID) {
			continue
		}
		list = append(list, client)
	}
	return list
}

func matchesVisibleClientSearch(client *file.Client, search string, searchID int) bool {
	if search == "" {
		return true
	}
	if client == nil {
		return false
	}
	return client.Id == searchID ||
		common.ContainsFold(client.VerifyKey, search) ||
		common.ContainsFold(client.Remark, search)
}
