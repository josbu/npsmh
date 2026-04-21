package file

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/rate"
)

type groupIDIndex struct {
	data  sync.Map // map[int]*idSet
	ready atomic.Bool
}

type idSet struct {
	mu  sync.RWMutex
	ids map[int]struct{}
}

func (idx *groupIDIndex) markReady() {
	if idx == nil {
		return
	}
	idx.ready.Store(true)
}

func (idx *groupIDIndex) isReady() bool {
	if idx == nil {
		return false
	}
	return idx.ready.Load()
}

func (idx *groupIDIndex) clear() {
	if idx == nil {
		return
	}
	idx.data.Range(func(key, _ interface{}) bool {
		idx.data.Delete(key)
		return true
	})
	idx.ready.Store(false)
}

func (idx *groupIDIndex) add(groupID, id int) {
	if idx == nil || groupID <= 0 || id <= 0 {
		return
	}
	raw, _ := idx.data.LoadOrStore(groupID, &idSet{
		ids: make(map[int]struct{}, 1),
	})
	set, ok := raw.(*idSet)
	if !ok || set == nil {
		return
	}
	set.add(id)
}

func (idx *groupIDIndex) remove(groupID, id int) {
	if idx == nil || groupID <= 0 || id <= 0 {
		return
	}
	raw, ok := idx.data.Load(groupID)
	if !ok {
		return
	}
	set, ok := raw.(*idSet)
	if !ok || set == nil {
		return
	}
	if set.remove(id) {
		idx.data.CompareAndDelete(groupID, raw)
	}
}

func (idx *groupIDIndex) removeID(id int) {
	if idx == nil || id <= 0 {
		return
	}
	idx.data.Range(func(key, value interface{}) bool {
		groupID, ok := key.(int)
		if !ok || groupID <= 0 {
			idx.data.CompareAndDelete(key, value)
			return true
		}
		set, ok := value.(*idSet)
		if !ok || set == nil {
			idx.data.CompareAndDelete(key, value)
			return true
		}
		if set.remove(id) {
			idx.data.CompareAndDelete(groupID, value)
		}
		return true
	})
}

func (idx *groupIDIndex) snapshot(groupID int) []int {
	if idx == nil || groupID <= 0 {
		return nil
	}
	raw, ok := idx.data.Load(groupID)
	if !ok {
		return nil
	}
	set, ok := raw.(*idSet)
	if !ok || set == nil {
		return nil
	}
	return set.snapshot()
}

func (idx *groupIDIndex) keys() []int {
	if idx == nil {
		return nil
	}
	keys := make([]int, 0)
	idx.data.Range(func(key, value interface{}) bool {
		groupID, ok := key.(int)
		if !ok || groupID <= 0 {
			idx.data.CompareAndDelete(key, value)
			return true
		}
		if _, ok := value.(*idSet); !ok {
			idx.data.CompareAndDelete(key, value)
			return true
		}
		keys = append(keys, groupID)
		return true
	})
	sort.Ints(keys)
	return keys
}

func (s *idSet) add(id int) {
	if s == nil || id <= 0 {
		return
	}
	s.mu.Lock()
	if s.ids == nil {
		s.ids = make(map[int]struct{}, 1)
	}
	s.ids[id] = struct{}{}
	s.mu.Unlock()
}

func (s *idSet) remove(id int) bool {
	if s == nil || id <= 0 {
		return false
	}
	s.mu.Lock()
	delete(s.ids, id)
	empty := len(s.ids) == 0
	s.mu.Unlock()
	return empty
}

func (s *idSet) snapshot() []int {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ids := make([]int, 0, len(s.ids))
	for id := range s.ids {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	sort.Ints(ids)
	return ids
}

func (s *DbUtils) GetClientList(start, length int, search, sort, order string, clientId int) ([]*Client, int) {
	list := make([]*Client, 0)
	var cnt int
	originLength := length
	id := common.GetIntNoErrByStr(search)
	keys := GetMapKeys(&s.JsonDb.Clients, true, sort, order)
	for _, key := range keys {
		v, ok := loadClientEntry(&s.JsonDb.Clients, key)
		if !ok {
			continue
		}
		if v.NoDisplay {
			continue
		}
		if clientId != 0 && clientId != v.Id {
			continue
		}
		if search != "" && v.Id != id && !common.ContainsFold(v.VerifyKey, search) && !common.ContainsFold(v.Remark, search) {
			continue
		}
		cnt++
		if start--; start < 0 {
			if originLength == 0 {
				list = append(list, v)
			} else if length--; length >= 0 {
				list = append(list, v)
			}
		}
	}
	return list, cnt
}

func (s *DbUtils) GetClientByVerifyKey(vKey string) (c *Client, err error) {
	vKey = strings.TrimSpace(vKey)
	if vKey == "" {
		return nil, errors.New("empty verify key")
	}
	hash := crypt.Blake2b(vKey)
	if current, ok := s.getClientByVerifyKeyHash(hash, vKey); ok {
		return current, nil
	}
	s.RangeClients(func(current *Client) bool {
		if c == nil && current.VerifyKey == vKey {
			c = current
			return false
		}
		return true
	})
	if c != nil {
		runtimeBlake2bVkeyIndex().Add(hash, c.Id)
		return c, nil
	}
	return nil, ErrClientNotFound
}

func (s *DbUtils) getClientByVerifyKeyHash(hash, verifyKey string) (*Client, bool) {
	if s == nil || hash == "" {
		return nil, false
	}
	id, err := s.GetClientIdByBlake2bVkey(hash)
	if err != nil || id <= 0 {
		return nil, false
	}
	current, err := s.GetClient(id)
	if err != nil || current == nil {
		runtimeBlake2bVkeyIndex().Remove(hash)
		return nil, false
	}
	if current.VerifyKey != verifyKey {
		runtimeBlake2bVkeyIndex().Remove(hash)
		return nil, false
	}
	return current, true
}

func (s *DbUtils) GetIdByVerifyKey(vKey, addr, localAddr string, hashFunc func(string) string) (id int, err error) {
	var exist bool
	s.RangeClients(func(v *Client) bool {
		if !exist && hashFunc(v.VerifyKey) == vKey && v.Status && v.Id > 0 {
			v.Addr = common.GetIpByAddr(addr)
			v.LocalAddr = common.GetIpByAddr(localAddr)
			id = v.Id
			exist = true
			return false
		}
		return true
	})
	if exist {
		return
	}
	return 0, errors.New("not found")
}

func (s *DbUtils) GetClientIdByBlake2bVkey(vkey string) (id int, err error) {
	var exist bool
	id, exist = runtimeBlake2bVkeyIndex().Get(vkey)
	if exist {
		return
	}
	err = ErrClientNotFound
	return
}

func (s *DbUtils) GetClientIdByMd5Vkey(vkey string) (id int, err error) {
	var exist bool
	s.RangeClients(func(v *Client) bool {
		if !exist && crypt.Md5(v.VerifyKey) == vkey {
			exist = true
			id = v.Id
			return false
		}
		return true
	})
	if exist {
		return
	}
	err = ErrClientNotFound
	return
}

func (s *DbUtils) DelClient(id int) error {
	current, ok := loadClientEntry(&s.JsonDb.Clients, id)
	if !ok {
		return ErrClientNotFound
	}
	if s.clientOwnsPersistedResources(id) {
		return errors.New("client has resources")
	}
	s.cleanupInvalidClientEntries()
	runtimeBlake2bVkeyIndex().Remove(crypt.Blake2b(current.VerifyKey))
	s.JsonDb.removeClientUserIndexesByID(current.Id)
	if current.Rate != nil {
		current.Rate.Stop()
	}
	s.JsonDb.Clients.Delete(id)
	s.JsonDb.markClientUserIndexesReady()
	s.JsonDb.StoreClients()
	return nil
}

func (s *DbUtils) cleanupInvalidClientEntries() {
	s.RangeClients(func(*Client) bool {
		return true
	})
}

func (s *DbUtils) RangeTunnelsByClientID(clientID int, fn func(*Tunnel) bool) {
	s.rangeClientTunnels(clientID, fn)
}

func (s *DbUtils) RangeHostsByClientID(clientID int, fn func(*Host) bool) {
	s.rangeClientHosts(clientID, fn)
}

func (s *DbUtils) CountResourcesByClientID(clientID int) int {
	if s == nil || s.JsonDb == nil || clientID <= 0 {
		return 0
	}
	return s.countClientTunnels(clientID) + s.countClientHosts(clientID)
}

func (s *DbUtils) countClientTunnels(clientID int) int {
	if s == nil || s.JsonDb == nil || clientID <= 0 {
		return 0
	}
	count := 0
	s.rangeClientTunnels(clientID, func(*Tunnel) bool {
		count++
		return true
	})
	return count
}

func (s *DbUtils) countClientHosts(clientID int) int {
	if s == nil || s.JsonDb == nil || clientID <= 0 {
		return 0
	}
	count := 0
	s.rangeClientHosts(clientID, func(*Host) bool {
		count++
		return true
	})
	return count
}

func (s *DbUtils) ensureTaskClientIndexReady() {
	if s == nil || s.JsonDb == nil || s.JsonDb.taskClientIndex.isReady() {
		return
	}
	s.rebuildTaskClientIndex()
}

func (s *DbUtils) ensureHostClientIndexReady() {
	if s == nil || s.JsonDb == nil || s.JsonDb.hostClientIndex.isReady() {
		return
	}
	s.rebuildHostClientIndex()
}

func (s *DbUtils) rebuildTaskClientIndex() {
	if s == nil || s.JsonDb == nil {
		return
	}
	s.JsonDb.taskClientIndex.clear()
	s.JsonDb.Tasks.Range(func(key, value interface{}) bool {
		tunnel, ok := value.(*Tunnel)
		if !ok || tunnel == nil {
			s.JsonDb.Tasks.CompareAndDelete(key, value)
			return true
		}
		id, keyOK := key.(int)
		if !keyOK || id != tunnel.Id {
			s.JsonDb.Tasks.CompareAndDelete(key, value)
			return true
		}
		clientID, ok := s.syncIndexedTunnelClient(tunnel)
		if !ok {
			return true
		}
		s.JsonDb.taskClientIndex.add(clientID, tunnel.Id)
		return true
	})
	s.JsonDb.taskClientIndex.markReady()
}

func (s *DbUtils) rebuildHostClientIndex() {
	if s == nil || s.JsonDb == nil {
		return
	}
	s.JsonDb.hostClientIndex.clear()
	s.JsonDb.Hosts.Range(func(key, value interface{}) bool {
		host, ok := value.(*Host)
		if !ok || host == nil {
			s.JsonDb.Hosts.CompareAndDelete(key, value)
			return true
		}
		id, keyOK := key.(int)
		if !keyOK || id != host.Id {
			s.JsonDb.Hosts.CompareAndDelete(key, value)
			return true
		}
		clientID, ok := s.syncIndexedHostClient(host)
		if !ok {
			return true
		}
		s.JsonDb.hostClientIndex.add(clientID, host.Id)
		return true
	})
	s.JsonDb.hostClientIndex.markReady()
}

func (s *DbUtils) syncIndexedTunnelClient(tunnel *Tunnel) (int, bool) {
	if s == nil || s.JsonDb == nil || tunnel == nil || tunnel.Id <= 0 {
		return 0, false
	}
	clientID := 0
	if tunnel.Client != nil {
		clientID = tunnel.Client.Id
	}
	if clientID <= 0 {
		s.JsonDb.dropLoadedTunnel(tunnel)
		return 0, false
	}
	client, ok := loadClientEntry(&s.JsonDb.Clients, clientID)
	if !ok || client == nil {
		s.JsonDb.dropLoadedTunnel(tunnel)
		return 0, false
	}
	if tunnel.Client != client {
		tunnel.Client = client
	}
	return clientID, true
}

func (s *DbUtils) syncIndexedHostClient(host *Host) (int, bool) {
	if s == nil || s.JsonDb == nil || host == nil || host.Id <= 0 {
		return 0, false
	}
	clientID := 0
	if host.Client != nil {
		clientID = host.Client.Id
	}
	if clientID <= 0 {
		s.JsonDb.dropLoadedHost(host)
		return 0, false
	}
	client, ok := loadClientEntry(&s.JsonDb.Clients, clientID)
	if !ok || client == nil {
		s.JsonDb.dropLoadedHost(host)
		return 0, false
	}
	if host.Client != client {
		host.Client = client
	}
	return clientID, true
}

func (s *DbUtils) rangeClientTunnels(clientID int, fn func(*Tunnel) bool) {
	if s == nil || s.JsonDb == nil || clientID <= 0 || fn == nil {
		return
	}
	s.ensureTaskClientIndexReady()
	for _, tunnelID := range s.JsonDb.taskClientIndex.snapshot(clientID) {
		tunnel, ok := loadTaskEntry(&s.JsonDb.Tasks, tunnelID)
		if !ok {
			s.JsonDb.taskClientIndex.remove(clientID, tunnelID)
			continue
		}
		actualClientID, ok := s.syncIndexedTunnelClient(tunnel)
		if !ok {
			s.JsonDb.taskClientIndex.remove(clientID, tunnelID)
			continue
		}
		if actualClientID != clientID {
			s.JsonDb.taskClientIndex.remove(clientID, tunnelID)
			s.JsonDb.taskClientIndex.add(actualClientID, tunnelID)
			continue
		}
		if !fn(tunnel) {
			return
		}
	}
}

func (s *DbUtils) rangeClientHosts(clientID int, fn func(*Host) bool) {
	if s == nil || s.JsonDb == nil || clientID <= 0 || fn == nil {
		return
	}
	s.ensureHostClientIndexReady()
	for _, hostID := range s.JsonDb.hostClientIndex.snapshot(clientID) {
		host, ok := loadHostEntry(&s.JsonDb.Hosts, hostID)
		if !ok {
			s.JsonDb.hostClientIndex.remove(clientID, hostID)
			continue
		}
		actualClientID, ok := s.syncIndexedHostClient(host)
		if !ok {
			s.JsonDb.hostClientIndex.remove(clientID, hostID)
			continue
		}
		if actualClientID != clientID {
			s.JsonDb.hostClientIndex.remove(clientID, hostID)
			s.JsonDb.hostClientIndex.add(actualClientID, hostID)
			continue
		}
		if !fn(host) {
			return
		}
	}
}

func (s *DbUtils) clientOwnsPersistedResources(id int) bool {
	if s == nil || s.JsonDb == nil || id <= 0 {
		return false
	}
	hasRefs := false
	s.rangeClientTunnels(id, func(*Tunnel) bool {
		hasRefs = true
		return false
	})
	if hasRefs {
		return true
	}
	s.rangeClientHosts(id, func(*Host) bool {
		hasRefs = true
		return false
	})
	return hasRefs
}

func (c *Client) HasTunnel(tunnel *Tunnel) (current *Tunnel, exist bool) {
	if c == nil || tunnel == nil {
		return nil, false
	}
	db := GetDb()
	if db == nil {
		return nil, false
	}
	db.rangeClientTunnels(c.Id, func(candidate *Tunnel) bool {
		if ((candidate.Port == tunnel.Port && tunnel.Port != 0) ||
			(candidate.Password == tunnel.Password && tunnel.Password != "")) ||
			FileTunnelIdentityEqual(candidate, tunnel) {
			current = candidate
			exist = true
			return false
		}
		return true
	})
	return current, exist
}

func (c *Client) GetTunnelNum() (num int) {
	if c == nil {
		return 0
	}
	db := GetDb()
	if db == nil {
		return 0
	}
	db.rangeClientTunnels(c.Id, func(*Tunnel) bool {
		num++
		return true
	})
	db.rangeClientHosts(c.Id, func(*Host) bool {
		num++
		return true
	})
	return num
}

func (c *Client) HasHost(host *Host) (current *Host, exist bool) {
	if c == nil || host == nil {
		return nil, false
	}
	db := GetDb()
	if db == nil {
		return nil, false
	}
	db.rangeClientHosts(c.Id, func(candidate *Host) bool {
		if candidate.Host == host.Host && candidate.Location == host.Location {
			current = candidate
			exist = true
			return false
		}
		return true
	})
	return current, exist
}

func (s *DbUtils) NewClient(c *Client) error {
	if c == nil {
		return errors.New("client is nil")
	}
	c.VerifyKey = strings.TrimSpace(c.VerifyKey)
	var isNotSet bool
reset:
	if c.VerifyKey == "" || isNotSet {
		isNotSet = true
		c.VerifyKey = crypt.GetRandomString(16, c.Id)
	}
	if !s.VerifyVkey(c.VerifyKey, c.Id) {
		if isNotSet {
			goto reset
		}
		return errors.New("vkey duplicate, please reset")
	}
	if c.RateLimit == 0 {
		c.Rate = rate.NewRate(0)
	} else if c.Rate == nil {
		c.Rate = rate.NewRate(int64(c.RateLimit) * 1024)
	}
	c.Rate.Start()
	if c.Id == 0 {
		c.Id = int(s.JsonDb.GetClientId())
	} else if c.Id > int(s.JsonDb.ClientIncreaseId) {
		s.JsonDb.ClientIncreaseId = int32(c.Id)
	}
	if c.Flow == nil {
		c.Flow = new(Flow)
	}
	applyClientSetupHook(c)
	s.bindClientOwnerUser(c)
	s.JsonDb.Clients.Store(c.Id, c)
	s.JsonDb.removeClientUserIndexesByID(c.Id)
	s.JsonDb.addClientUserIndexes(c)
	s.JsonDb.markClientUserIndexesReady()
	runtimeBlake2bVkeyIndex().Add(crypt.Blake2b(c.VerifyKey), c.Id)
	s.JsonDb.StoreClients()
	return nil
}

func (s *DbUtils) VerifyVkey(vkey string, id int) (res bool) {
	res = true
	s.RangeClients(func(v *Client) bool {
		if v.VerifyKey == vkey && v.Id != id {
			res = false
			return false
		}
		return true
	})
	return res
}

func (s *DbUtils) UpdateClient(t *Client) error {
	if t == nil {
		return errors.New("client is nil")
	}
	t.VerifyKey = strings.TrimSpace(t.VerifyKey)
	current, ok := loadClientEntry(&s.JsonDb.Clients, t.Id)
	if !ok {
		return ErrClientNotFound
	}
	if t.ExpectedRevision > 0 {
		if current.Revision != t.ExpectedRevision {
			return ErrRevisionConflict
		}
	}
	if strings.TrimSpace(t.VerifyKey) == "" {
		return errors.New("empty verify key")
	}
	if !s.VerifyVkey(t.VerifyKey, t.Id) {
		return errors.New("vkey duplicate, please reset")
	}
	runtimeBlake2bVkeyIndex().Remove(crypt.Blake2b(current.VerifyKey))
	if t.Rate == nil {
		t.Rate = current.Rate
	}

	applyClientSetupHook(t)
	t.ExpectedRevision = 0
	s.bindClientOwnerUser(t)
	s.JsonDb.Clients.Store(t.Id, t)
	s.JsonDb.removeClientUserIndexesByID(t.Id)
	s.JsonDb.addClientUserIndexes(t)
	s.JsonDb.markClientUserIndexesReady()
	s.relinkClientReferences(t)
	runtimeBlake2bVkeyIndex().Add(crypt.Blake2b(t.VerifyKey), t.Id)
	var limit int64
	if t.RateLimit > 0 {
		limit = int64(t.RateLimit) * 1024
	} else {
		limit = 0
	}
	if t.Rate != nil {
		if t.Rate.Limit() != limit {
			t.Rate.ResetLimit(limit)
		} else {
			t.Rate.Start()
		}
	} else {
		t.Rate = rate.NewRate(limit)
		t.Rate.Start()
	}
	s.JsonDb.StoreClients()
	return nil
}

func (s *DbUtils) bindClientOwnerUser(client *Client) {
	if s == nil || s.JsonDb == nil || client == nil {
		return
	}
	ownerID := client.OwnerID()
	if ownerID <= 0 {
		client.BindOwnerUser(nil)
		return
	}
	if user, ok := loadUserEntry(&s.JsonDb.Users, ownerID); ok {
		client.BindOwnerUser(user)
		return
	}
	client.BindOwnerUser(nil)
}

func (s *DbUtils) relinkClientReferences(client *Client) {
	if s == nil || s.JsonDb == nil || client == nil {
		return
	}
	s.rangeClientTunnels(client.Id, func(tunnel *Tunnel) bool {
		tunnel.Client = client
		return true
	})
	s.rangeClientHosts(client.Id, func(host *Host) bool {
		host.Client = client
		return true
	})
}

func (s *DbUtils) IsPubClient(id int) bool {
	client, err := s.GetClient(id)
	if err == nil {
		return client.NoDisplay
	}
	return false
}

func (s *DbUtils) GetClient(id int) (c *Client, err error) {
	if c, ok := loadClientEntry(&s.JsonDb.Clients, id); ok {
		return c, nil
	}
	err = ErrClientNotFound
	return
}
