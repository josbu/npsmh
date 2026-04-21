package file

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (s *DbUtils) GetUser(id int) (u *User, err error) {
	if u, ok := loadUserEntry(&s.JsonDb.Users, id); ok {
		return u, nil
	}
	err = ErrUserNotFound
	return
}

func (s *DbUtils) GetUserByUsername(username string) (u *User, err error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("empty username")
	}
	if current, ok := s.getUserByUsernameIndex(username); ok {
		s.cleanupInvalidUserEntries()
		return current, nil
	}
	var found *User
	s.RangeUsers(func(current *User) bool {
		if found == nil && strings.TrimSpace(current.Username) == username {
			found = current
		}
		return true
	})
	if found != nil {
		runtimeUsernameIndex().Add(username, found.Id)
		return found, nil
	}
	return nil, ErrUserNotFound
}

func (s *DbUtils) getUserByUsernameIndex(username string) (*User, bool) {
	if s == nil || username == "" {
		return nil, false
	}
	id, ok := runtimeUsernameIndex().Get(username)
	if !ok || id <= 0 {
		return nil, false
	}
	current, err := s.GetUser(id)
	if err != nil || current == nil {
		runtimeUsernameIndex().Remove(username)
		return nil, false
	}
	if indexedUsername(current) != username {
		runtimeUsernameIndex().Remove(username)
		return nil, false
	}
	return current, true
}

func (s *DbUtils) GetUserByExternalPlatformID(platformID string) (u *User, err error) {
	platformID = strings.TrimSpace(platformID)
	if platformID == "" {
		return nil, errors.New("empty external platform id")
	}
	if current, ok := s.getUserByExternalPlatformIDIndex(platformID); ok {
		s.cleanupInvalidUserEntries()
		return current, nil
	}
	var found *User
	s.RangeUsers(func(current *User) bool {
		if current.Kind == "platform_service" && strings.TrimSpace(current.ExternalPlatformID) == platformID {
			found = current
			return false
		}
		return true
	})
	if found != nil {
		if platformID := indexedUserExternalPlatformID(found); platformID != "" {
			runtimePlatformUserIndex().Add(platformID, found.Id)
		}
		return found, nil
	}
	return nil, ErrUserNotFound
}

func (s *DbUtils) getUserByExternalPlatformIDIndex(platformID string) (*User, bool) {
	if s == nil || platformID == "" {
		return nil, false
	}
	id, ok := runtimePlatformUserIndex().Get(platformID)
	if !ok || id <= 0 {
		return nil, false
	}
	current, err := s.GetUser(id)
	if err != nil || current == nil {
		runtimePlatformUserIndex().Remove(platformID)
		return nil, false
	}
	if indexedUserExternalPlatformID(current) != platformID {
		runtimePlatformUserIndex().Remove(platformID)
		return nil, false
	}
	return current, true
}

func indexedUserExternalPlatformID(user *User) string {
	if user == nil {
		return ""
	}
	platformID := strings.TrimSpace(user.ExternalPlatformID)
	if platformID == "" || strings.TrimSpace(user.Kind) != "platform_service" {
		return ""
	}
	return platformID
}

func indexedUsername(user *User) string {
	if user == nil {
		return ""
	}
	return strings.TrimSpace(user.Username)
}

func (s *DbUtils) cleanupInvalidUserEntries() {
	s.RangeUsers(func(*User) bool {
		return true
	})
}

func (s *DbUtils) NewUser(u *User) error {
	if u == nil {
		return errors.New("user is nil")
	}
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" {
		return errors.New("user username is empty")
	}
	if !s.verifyUserName(u.Username, u.Id, 0) {
		return errors.New("web login username duplicate, please reset")
	}
	InitializeUserRuntime(u)
	if u.Id == 0 {
		u.Id = int(s.JsonDb.GetUserId())
	} else if u.Id > int(s.JsonDb.UserIncreaseId) {
		s.JsonDb.UserIncreaseId = int32(u.Id)
	}
	if u.Status == 0 {
		u.Status = 1
	}
	s.JsonDb.Users.Store(u.Id, u)
	if username := indexedUsername(u); username != "" {
		runtimeUsernameIndex().Add(username, u.Id)
	}
	if platformID := indexedUserExternalPlatformID(u); platformID != "" {
		runtimePlatformUserIndex().Add(platformID, u.Id)
	}
	s.relinkUserReferences(u)
	s.JsonDb.StoreUsers()
	return nil
}

func (s *DbUtils) UpdateUser(u *User) error {
	if u == nil {
		return errors.New("user is nil")
	}
	value, ok := s.JsonDb.Users.Load(u.Id)
	if !ok {
		return ErrUserNotFound
	}
	current, ok := value.(*User)
	if !ok || current == nil {
		s.JsonDb.Users.CompareAndDelete(u.Id, value)
		return ErrUserNotFound
	}
	if u.ExpectedRevision > 0 && current.Revision != u.ExpectedRevision {
		return ErrRevisionConflict
	}
	u.Username = strings.TrimSpace(u.Username)
	if u.Username == "" {
		return errors.New("user username is empty")
	}
	if !s.verifyUserName(u.Username, u.Id, 0) {
		return errors.New("web login username duplicate, please reset")
	}
	if username := indexedUsername(current); username != "" {
		if id, ok := runtimeUsernameIndex().Get(username); ok && id == current.Id {
			runtimeUsernameIndex().Remove(username)
		}
	}
	if platformID := indexedUserExternalPlatformID(current); platformID != "" {
		if id, ok := runtimePlatformUserIndex().Get(platformID); ok && id == current.Id {
			runtimePlatformUserIndex().Remove(platformID)
		}
	}
	u.ExpectedRevision = 0
	InitializeUserRuntime(u)
	s.JsonDb.Users.Store(u.Id, u)
	if username := indexedUsername(u); username != "" {
		runtimeUsernameIndex().Add(username, u.Id)
	}
	if platformID := indexedUserExternalPlatformID(u); platformID != "" {
		runtimePlatformUserIndex().Add(platformID, u.Id)
	}
	s.relinkUserReferences(u)
	s.JsonDb.StoreUsers()
	return nil
}

func (s *DbUtils) DelUser(id int) error {
	current, ok := loadUserEntry(&s.JsonDb.Users, id)
	if !ok {
		return ErrUserNotFound
	}
	if username := indexedUsername(current); username != "" {
		if indexedID, ok := runtimeUsernameIndex().Get(username); ok && indexedID == current.Id {
			runtimeUsernameIndex().Remove(username)
		}
	}
	if platformID := indexedUserExternalPlatformID(current); platformID != "" {
		if indexedID, ok := runtimePlatformUserIndex().Get(platformID); ok && indexedID == current.Id {
			runtimePlatformUserIndex().Remove(platformID)
		}
	}
	s.JsonDb.Users.Delete(id)
	clientRefsChanged := s.clearUserReferences(id)
	s.JsonDb.StoreUsers()
	if clientRefsChanged {
		s.JsonDb.StoreClients()
	}
	return nil
}

func (s *DbUtils) GetClientsByUserId(userId int) (clients []*Client) {
	if userId <= 0 {
		return nil
	}
	s.cleanupInvalidClientEntries()
	s.rangeOwnedClientsByUserID(userId, func(client *Client) bool {
		clients = append(clients, client)
		return true
	})
	return
}

func (s *DbUtils) GetClientIDsByUserId(userId int) (clientIDs []int) {
	if s == nil || s.JsonDb == nil || userId <= 0 {
		return nil
	}
	s.rangeOwnedClientsByUserID(userId, func(client *Client) bool {
		clientIDs = append(clientIDs, client.Id)
		return true
	})
	return
}

func (s *DbUtils) GetAllManagedClientIDsByUserId(userId int) []int {
	return s.collectManagedClientIDsByUserId(userId, nil)
}

func (s *DbUtils) GetVisibleManagedClientIDsByUserId(userId int) []int {
	return s.collectManagedClientIDsByUserId(userId, func(client *Client) bool {
		return client != nil && client.Status && !client.NoDisplay
	})
}

func (s *DbUtils) collectManagedClientIDsByUserId(userId int, allow func(*Client) bool) (clientIDs []int) {
	if s == nil || s.JsonDb == nil || userId <= 0 {
		return nil
	}
	seen := make(map[int]struct{})
	collect := func(client *Client) bool {
		if client == nil {
			return true
		}
		if allow != nil && !allow(client) {
			return true
		}
		if _, ok := seen[client.Id]; ok {
			return true
		}
		seen[client.Id] = struct{}{}
		clientIDs = append(clientIDs, client.Id)
		return true
	}
	s.rangeOwnedClientsByUserID(userId, collect)
	s.rangeManagedClientsByUserID(userId, collect)
	sort.Ints(clientIDs)
	return
}

func (s *DbUtils) GetTunnelsByUserId(userId int) (count int) {
	if userId <= 0 {
		return 0
	}
	_, count, _ = s.CountOwnedResourcesByUserID(userId)
	return count
}

func (s *DbUtils) GetHostsByUserId(userId int) (count int) {
	if userId <= 0 {
		return 0
	}
	_, _, count = s.CountOwnedResourcesByUserID(userId)
	return count
}

func (s *DbUtils) CountOwnedResourcesByUserID(userId int) (int, int, int) {
	if s == nil || s.JsonDb == nil || userId <= 0 {
		return 0, 0, 0
	}
	clientIDs := s.GetClientIDsByUserId(userId)
	if len(clientIDs) == 0 {
		return 0, 0, 0
	}
	tunnelCount := 0
	hostCount := 0
	for _, clientID := range clientIDs {
		tunnelCount += s.countClientTunnels(clientID)
		hostCount += s.countClientHosts(clientID)
	}
	return len(clientIDs), tunnelCount, hostCount
}

func (s *DbUtils) CollectOwnedResourceCounts() (map[int]int, map[int]int, map[int]int) {
	clientCounts := make(map[int]int)
	tunnelCounts := make(map[int]int)
	hostCounts := make(map[int]int)
	if s == nil || s.JsonDb == nil {
		return clientCounts, tunnelCounts, hostCounts
	}
	s.cleanupInvalidClientEntries()
	if !s.clientUserIndexesReady() {
		s.rebuildClientUserIndexes()
	}
	for _, ownerID := range s.JsonDb.ownerClientIndex.keys() {
		clientCount, tunnelCount, hostCount := s.CountOwnedResourcesByUserID(ownerID)
		if clientCount > 0 {
			clientCounts[ownerID] = clientCount
		}
		if tunnelCount > 0 {
			tunnelCounts[ownerID] = tunnelCount
		}
		if hostCount > 0 {
			hostCounts[ownerID] = hostCount
		}
	}
	return clientCounts, tunnelCounts, hostCounts
}

func (s *DbUtils) VerifyUserName(username string, id int) (res bool) {
	return s.verifyUserName(username, 0, id)
}

func (s *DbUtils) relinkUserReferences(user *User) {
	if s == nil || s.JsonDb == nil || user == nil {
		return
	}
	s.rangeOwnedClientsByUserID(user.Id, func(client *Client) bool {
		client.BindOwnerUser(user)
		return true
	})
}

func (s *DbUtils) clearUserReferences(userID int) bool {
	if s == nil || s.JsonDb == nil || userID <= 0 {
		return false
	}
	s.ensureClientUserIndexesReady()
	clientIDs := s.collectVisibleUserLinkedClientIDs(userID)
	if len(clientIDs) == 0 {
		return false
	}
	changed := false
	for _, clientID := range clientIDs {
		client, ok := loadClientEntry(&s.JsonDb.Clients, clientID)
		if !ok || client == nil {
			s.JsonDb.removeClientUserIndexesByID(clientID)
			continue
		}
		if clearClientUserReference(client, userID) {
			s.JsonDb.removeClientUserIndexesByID(client.Id)
			s.JsonDb.addClientUserIndexes(client)
			changed = true
		}
	}
	if changed {
		s.JsonDb.markClientUserIndexesReady()
	}
	return changed
}

func (s *DbUtils) ensureClientUserIndexesReady() {
	if s == nil || s.JsonDb == nil || s.clientUserIndexesReady() {
		return
	}
	s.rebuildClientUserIndexes()
}

func (s *DbUtils) clientUserIndexesReady() bool {
	return s != nil && s.JsonDb != nil && s.JsonDb.ownerClientIndex.isReady() && s.JsonDb.managerClientIndex.isReady()
}

func (s *DbUtils) rebuildClientUserIndexes() {
	if s == nil || s.JsonDb == nil {
		return
	}
	s.JsonDb.clearClientUserIndexes()
	s.RangeClients(func(client *Client) bool {
		s.JsonDb.addClientUserIndexes(client)
		return true
	})
	s.JsonDb.markClientUserIndexesReady()
}

func (s *DbUtils) rangeOwnedClientsByUserID(userID int, fn func(*Client) bool) {
	if s == nil || s.JsonDb == nil || userID <= 0 || fn == nil {
		return
	}
	s.ensureClientUserIndexesReady()
	s.rangeIndexedClientsByUserID(userID, &s.JsonDb.ownerClientIndex, func(client *Client) bool {
		return client != nil && client.OwnerID() == userID
	}, fn)
}

func (s *DbUtils) rangeManagedClientsByUserID(userID int, fn func(*Client) bool) {
	if s == nil || s.JsonDb == nil || userID <= 0 || fn == nil {
		return
	}
	s.ensureClientUserIndexesReady()
	s.rangeIndexedClientsByUserID(userID, &s.JsonDb.managerClientIndex, func(client *Client) bool {
		return clientHasManagerUserID(client, userID)
	}, fn)
}

func (s *DbUtils) rangeIndexedClientsByUserID(userID int, index *groupIDIndex, owns func(*Client) bool, fn func(*Client) bool) {
	if s == nil || s.JsonDb == nil || userID <= 0 || index == nil || owns == nil || fn == nil {
		return
	}
	for _, clientID := range index.snapshot(userID) {
		client, ok := loadClientEntry(&s.JsonDb.Clients, clientID)
		if !ok || client == nil {
			index.remove(userID, clientID)
			continue
		}
		if !owns(client) {
			index.remove(userID, clientID)
			continue
		}
		if !fn(client) {
			return
		}
	}
}

func (s *DbUtils) collectVisibleUserLinkedClientIDs(userID int) []int {
	if s == nil || s.JsonDb == nil || userID <= 0 {
		return nil
	}
	seen := make(map[int]struct{})
	clientIDs := make([]int, 0)
	collect := func(client *Client) bool {
		if client == nil {
			return true
		}
		if _, ok := seen[client.Id]; ok {
			return true
		}
		seen[client.Id] = struct{}{}
		clientIDs = append(clientIDs, client.Id)
		return true
	}
	s.rangeOwnedClientsByUserID(userID, collect)
	s.rangeManagedClientsByUserID(userID, collect)
	return clientIDs
}

func clientHasManagerUserID(client *Client, userID int) bool {
	if client == nil || userID <= 0 {
		return false
	}
	for _, current := range client.ManagerUserIDs {
		if current == userID {
			return true
		}
	}
	return false
}

func (s *DbUtils) verifyUserName(username string, exceptUserID int, exceptClientID int) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return true
	}
	if current, ok := s.getUserByUsernameIndex(username); ok {
		return current.Id == exceptUserID
	}
	res := true
	foundID := 0
	s.RangeUsers(func(v *User) bool {
		if indexedUsername(v) == username {
			if foundID == 0 {
				foundID = v.Id
			}
			if v.Id != exceptUserID {
				res = false
				return false
			}
		}
		return true
	})
	if res && foundID > 0 {
		runtimeUsernameIndex().Add(username, foundID)
	}
	if !res {
		return false
	}
	return res
}

func clearClientUserReference(client *Client, userID int) bool {
	if client == nil || userID <= 0 {
		return false
	}
	changed := false
	if client.OwnerID() == userID {
		client.SetOwnerUserID(0)
		changed = true
	}
	if len(client.ManagerUserIDs) > 0 {
		filtered := make([]int, 0, len(client.ManagerUserIDs))
		for _, current := range client.ManagerUserIDs {
			if current == userID {
				changed = true
				continue
			}
			filtered = append(filtered, current)
		}
		if changed {
			client.ManagerUserIDs = filtered
		}
	}
	if changed {
		client.NormalizeOwnership()
		client.BindOwnerUser(nil)
		client.TouchMeta("", "", "")
	}
	return changed
}

type ManagementPlatformBinding struct {
	PlatformID      string
	ServiceUsername string
	Enabled         bool
}

func EnsureManagementPlatformUsers(platforms []ManagementPlatformBinding) bool {
	if len(platforms) == 0 {
		return false
	}
	db := GetDb()
	usersByExternalPlatform := make(map[string]*User)
	usersByUsername := make(map[string]*User)
	db.RangeUsers(func(user *User) bool {
		InitializeUserRuntime(user)
		usersByUsername[user.Username] = user
		if user.Kind == "platform_service" && user.ExternalPlatformID != "" {
			usersByExternalPlatform[user.ExternalPlatformID] = user
		}
		return true
	})
	changed := false
	for _, platform := range platforms {
		if !platform.Enabled || strings.TrimSpace(platform.PlatformID) == "" {
			continue
		}
		serviceUser := usersByExternalPlatform[platform.PlatformID]
		if serviceUser == nil {
			username := strings.TrimSpace(platform.ServiceUsername)
			if username == "" {
				username = defaultPlatformServiceUsername(platform.PlatformID)
			}
			username = nextAvailablePlatformServiceUsername(username, usersByUsername)
			serviceUser = &User{
				Id:                 int(db.JsonDb.GetUserId()),
				Username:           username,
				Password:           "",
				Kind:               "platform_service",
				ExternalPlatformID: platform.PlatformID,
				Hidden:             true,
				Status:             1,
				TotalFlow:          &Flow{},
			}
			serviceUser.TouchMeta()
			InitializeUserRuntime(serviceUser)
			db.JsonDb.Users.Store(serviceUser.Id, serviceUser)
			if username := indexedUsername(serviceUser); username != "" {
				runtimeUsernameIndex().Add(username, serviceUser.Id)
			}
			runtimePlatformUserIndex().Add(serviceUser.ExternalPlatformID, serviceUser.Id)
			usersByUsername[username] = serviceUser
			changed = true
		}
		beforeKind := serviceUser.Kind
		beforePlatformID := serviceUser.ExternalPlatformID
		beforeHidden := serviceUser.Hidden
		serviceUser.Kind = "platform_service"
		serviceUser.ExternalPlatformID = platform.PlatformID
		serviceUser.Hidden = true
		serviceUser.NormalizeIdentity()
		usersByExternalPlatform[platform.PlatformID] = serviceUser
		if beforePlatformID != "" && beforePlatformID != serviceUser.ExternalPlatformID {
			if id, ok := runtimePlatformUserIndex().Get(beforePlatformID); ok && id == serviceUser.Id {
				runtimePlatformUserIndex().Remove(beforePlatformID)
			}
		}
		if serviceUser.ExternalPlatformID != "" {
			runtimePlatformUserIndex().Add(serviceUser.ExternalPlatformID, serviceUser.Id)
		}
		if beforeKind != serviceUser.Kind || beforePlatformID != serviceUser.ExternalPlatformID || beforeHidden != serviceUser.Hidden {
			serviceUser.TouchMeta()
			changed = true
		}
	}
	if changed {
		db.FlushToDisk()
	}
	return changed
}

func defaultPlatformServiceUsername(platformID string) string {
	platformID = strings.TrimSpace(strings.ToLower(platformID))
	if platformID == "" {
		platformID = "platform"
	}
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_")
	return "__platform_" + replacer.Replace(platformID)
}

func nextAvailablePlatformServiceUsername(base string, usersByUsername map[string]*User) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "__platform_service"
	}
	if _, exists := usersByUsername[base]; !exists {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if _, exists := usersByUsername[candidate]; !exists {
			return candidate
		}
	}
}
