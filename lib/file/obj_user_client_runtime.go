package file

import (
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/rate"
)

func (u *User) EnsureTotalFlow() {
	if u != nil && u.TotalFlow == nil {
		u.TotalFlow = new(Flow)
	}
}

func (u *User) EnsureRuntimeTraffic() {
	if u == nil {
		return
	}
	u.RLock()
	ready := u.TotalTraffic != nil && u.TotalMeter != nil
	u.RUnlock()
	if ready {
		return
	}
	u.Lock()
	defer u.Unlock()
	if u.TotalTraffic == nil {
		u.TotalTraffic = &TrafficStats{}
		if in, out := flowSnapshot(u.TotalFlow); in != 0 || out != 0 {
			u.TotalTraffic.Add(in, out)
		}
	}
	if u.TotalMeter == nil {
		u.TotalMeter = rate.NewMeter()
	}
}

func (u *User) EnsureRuntimeRate() {
	if u == nil {
		return
	}
	limit := int64(u.RateLimit) * 1024
	if u.RateLimit <= 0 {
		limit = 0
	}
	if u.Rate == nil {
		u.Rate = rate.NewRate(limit)
		u.Rate.Start()
		return
	}
	if u.Rate.Limit() != limit {
		u.Rate.ResetLimit(limit)
		return
	}
	u.Rate.Start()
}

func InitializeUserRuntime(user *User) {
	if user == nil {
		return
	}
	user.Lock()
	user.MaxConnections = normalizeNonNegativeConnectionLimit(user.MaxConnections)
	user.normalizeIdentityLocked()
	user.Unlock()
	user.EnsureTotalFlow()
	user.EnsureRuntimeTraffic()
	user.EnsureRuntimeRate()
	user.CompileSourcePolicy()
	user.CompileDestACL()
}

func (u *User) TotalTrafficTotals() (int64, int64, int64) {
	if u == nil {
		return 0, 0, 0
	}
	if u.TotalTraffic != nil {
		return u.TotalTraffic.Snapshot()
	}
	if u.TotalFlow != nil {
		return u.TotalFlow.InletFlow, u.TotalFlow.ExportFlow, u.TotalFlow.InletFlow + u.TotalFlow.ExportFlow
	}
	return 0, 0, 0
}

func (u *User) ObserveTotalTraffic(in, out int64) error {
	if u == nil || (in == 0 && out == 0) {
		return nil
	}
	u.EnsureRuntimeTraffic()
	_, _, total := u.TotalTraffic.Add(in, out)
	u.TotalMeter.Add(in, out)
	u.EnsureTotalFlow()
	u.TotalFlow.Add(in, out)
	if expireAt := u.ExpireAt; expireAt > 0 && time.Now().Unix() >= expireAt {
		return errors.New("User: time limit exceeded")
	}
	if u.FlowLimit > 0 {
		if total >= u.FlowLimit {
			return errors.New("User: flow limit exceeded")
		}
	}
	return nil
}

func (u *User) TotalRateTotals() (int64, int64, int64) {
	if u == nil {
		return 0, 0, 0
	}
	u.EnsureRuntimeTraffic()
	return rateSnapshot(u.TotalMeter)
}

func (u *User) ResetTotalTraffic() {
	if u == nil {
		return
	}
	u.EnsureRuntimeTraffic()
	u.TotalTraffic.Reset()
	u.TotalMeter.Reset()
	if u.TotalFlow != nil {
		u.TotalFlow.InletFlow = 0
		u.TotalFlow.ExportFlow = 0
	}
}

func (u *User) AddConn() {
	addRuntimeConn(&u.NowConn)
}

func (u *User) CutConn() {
	cutRuntimeConn(&u.NowConn)
}

func (u *User) GetConn() bool {
	if u == nil {
		return false
	}
	return acquireRuntimeConn(&u.NowConn, u.MaxConnections)
}

func addRuntimeConn(counter *int32) {
	if counter == nil {
		return
	}
	atomic.AddInt32(counter, 1)
}

func cutRuntimeConn(counter *int32) {
	if counter == nil {
		return
	}
	for {
		current := atomic.LoadInt32(counter)
		if current <= 0 {
			if atomic.CompareAndSwapInt32(counter, current, 0) {
				return
			}
			continue
		}
		if atomic.CompareAndSwapInt32(counter, current, current-1) {
			return
		}
	}
}

func acquireRuntimeConn(counter *int32, limit int) bool {
	if counter == nil {
		return false
	}
	for {
		current := atomic.LoadInt32(counter)
		if current < 0 {
			if !atomic.CompareAndSwapInt32(counter, current, 0) {
				continue
			}
			current = 0
		}
		if limit != 0 && int(current) >= limit {
			return false
		}
		if atomic.CompareAndSwapInt32(counter, current, current+1) {
			return true
		}
	}
}

func (u *User) NormalizeIdentity() {
	if u == nil {
		return
	}
	u.Lock()
	defer u.Unlock()
	u.normalizeIdentityLocked()
}

func (u *User) normalizeIdentityLocked() {
	if u == nil {
		return
	}
	u.TOTPSecret = strings.ToUpper(strings.TrimSpace(u.TOTPSecret))
	u.Kind = strings.TrimSpace(u.Kind)
	u.ExternalPlatformID = strings.TrimSpace(u.ExternalPlatformID)
	switch u.Kind {
	case "", "local":
		u.Kind = "local"
		u.ExternalPlatformID = ""
		u.Hidden = false
	case "platform_service":
		u.Hidden = true
	default:
		u.Kind = "local"
		u.ExternalPlatformID = ""
		u.Hidden = false
	}
}

func (u *User) TouchMeta() {
	if u == nil {
		return
	}
	u.Lock()
	defer u.Unlock()
	u.Revision++
	u.UpdatedAt = time.Now().Unix()
}

func NewClient(vKey string, noStore bool, noDisplay bool) *Client {
	client := &Client{
		Cnf:       new(Config),
		VerifyKey: vKey,
		Status:    true,
		Flow:      new(Flow),
		NoStore:   noStore,
		NoDisplay: noDisplay,
	}
	applyClientSetupHook(client)
	return client
}

func SetClientSetupHook(fn func(*Client)) {
	clientSetupHookMu.Lock()
	clientSetupHook = fn
	clientSetupHookMu.Unlock()
}

func applyClientSetupHook(client *Client) {
	if client == nil {
		return
	}
	client.Lock()
	if client.Flow == nil {
		client.Flow = new(Flow)
	}
	client.normalizeOwnershipLocked()
	client.normalizeLifecycleFieldsLocked()
	client.Unlock()
	client.EnsureRuntimeTraffic()
	client.CompileSourcePolicy()
	clientSetupHookMu.RLock()
	fn := clientSetupHook
	clientSetupHookMu.RUnlock()
	if fn != nil {
		fn(client)
	}
}

func InitializeClientRuntime(client *Client) {
	applyClientSetupHook(client)
}

func (s *Client) AddConn() {
	if s == nil {
		return
	}
	addRuntimeConn(&s.NowConn)
}

func (s *Client) CutConn() {
	if s == nil {
		return
	}
	cutRuntimeConn(&s.NowConn)
}

func (s *Client) GetConn() bool {
	if s == nil {
		return false
	}
	return acquireRuntimeConn(&s.NowConn, s.MaxConn)
}

func (s *Client) EnsureRuntimeTraffic() {
	if s == nil {
		return
	}
	s.RLock()
	ready := s.BridgeTraffic != nil && s.ServiceTraffic != nil && s.BridgeMeter != nil && s.ServiceMeter != nil && s.TotalMeter != nil
	s.RUnlock()
	if ready {
		return
	}
	s.Lock()
	defer s.Unlock()
	if s.BridgeTraffic == nil {
		s.BridgeTraffic = &TrafficStats{}
		if in, out := flowSnapshot(s.Flow); in != 0 || out != 0 {
			s.BridgeTraffic.Add(in, out)
		}
	}
	if s.ServiceTraffic == nil {
		s.ServiceTraffic = &TrafficStats{}
	}
	if s.BridgeMeter == nil {
		s.BridgeMeter = rate.NewMeter()
	}
	if s.ServiceMeter == nil {
		s.ServiceMeter = rate.NewMeter()
	}
	if s.TotalMeter == nil {
		s.TotalMeter = rate.NewMeter()
	}
}

func (s *Client) NormalizeLifecycleFields() {
	if s == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	s.normalizeLifecycleFieldsLocked()
}

func (s *Client) normalizeLifecycleFieldsLocked() {
	if s == nil {
		return
	}
	s.MaxConn = normalizeNonNegativeConnectionLimit(s.MaxConn)
	if s.Flow == nil {
		s.Flow = new(Flow)
	}
	if s.ExpireAt <= 0 && !s.Flow.TimeLimit.IsZero() {
		s.ExpireAt = s.Flow.TimeLimit.Unix()
	} else if s.ExpireAt > 0 && s.Flow.TimeLimit.IsZero() {
		s.Flow.TimeLimit = time.Unix(s.ExpireAt, 0)
	}
	if s.FlowLimit <= 0 && s.Flow.FlowLimit > 0 {
		s.FlowLimit = s.Flow.FlowLimit << 20
	} else if s.FlowLimit > 0 && s.Flow.FlowLimit == 0 {
		s.Flow.FlowLimit = legacyFlowLimitMegabytes(s.FlowLimit)
	}
}

func (s *Client) NormalizeOwnership() {
	if s == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	s.normalizeOwnershipLocked()
}

func (s *Client) normalizeOwnershipLocked() {
	if s == nil {
		return
	}
	if s.OwnerUserID > 0 || s.UserId > 0 {
		if s.OwnerUserID > 0 {
			s.UserId = s.OwnerUserID
		} else {
			s.OwnerUserID = s.UserId
		}
	} else {
		s.OwnerUserID = 0
		s.UserId = 0
	}
	normalized := make([]int, 0, len(s.ManagerUserIDs))
	for _, userID := range s.ManagerUserIDs {
		if userID <= 0 || userID == s.OwnerUserID {
			continue
		}
		duplicate := false
		for _, current := range normalized {
			if current == userID {
				duplicate = true
				break
			}
		}
		if !duplicate {
			normalized = append(normalized, userID)
		}
	}
	s.ManagerUserIDs = normalized
	if s.ownerUser != nil && s.ownerUser.Id != s.OwnerUserID {
		s.ownerUser = nil
	}
}

func (s *Client) SetOwnerUserID(userID int) {
	if s == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	if userID < 0 {
		userID = 0
	}
	s.OwnerUserID = userID
	s.UserId = userID
	s.normalizeOwnershipLocked()
	if s.ownerUser != nil && s.ownerUser.Id != userID {
		s.ownerUser = nil
	}
}

func (s *Client) OwnerID() int {
	if s == nil {
		return 0
	}
	if s.OwnerUserID > 0 {
		return s.OwnerUserID
	}
	return s.UserId
}

func (s *Client) BindOwnerUser(user *User) {
	if s == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	if user == nil || user.Id != s.OwnerID() {
		s.ownerUser = nil
		return
	}
	s.ownerUser = user
}

func (s *Client) OwnerUser() *User {
	if s == nil {
		return nil
	}
	s.RLock()
	defer s.RUnlock()
	return s.ownerUser
}

func (s *Client) CanBeManagedByUser(userID int) bool {
	if s == nil || userID <= 0 {
		return false
	}
	if s.OwnerID() == userID {
		return true
	}
	for _, current := range s.ManagerUserIDs {
		if current == userID {
			return true
		}
	}
	return false
}

func (s *Client) TouchMeta(sourceType, platformID, actorID string) {
	if s == nil {
		return
	}
	s.Lock()
	defer s.Unlock()
	if strings.TrimSpace(sourceType) != "" {
		s.SourceType = strings.TrimSpace(sourceType)
	}
	if strings.TrimSpace(platformID) != "" {
		s.SourcePlatformID = strings.TrimSpace(platformID)
	}
	if strings.TrimSpace(actorID) != "" {
		s.SourceActorID = strings.TrimSpace(actorID)
	}
	s.Revision++
	s.UpdatedAt = time.Now().Unix()
}

func (s *Client) EffectiveExpireAt() int64 {
	if s == nil {
		return 0
	}
	if s.ExpireAt > 0 {
		return s.ExpireAt
	}
	if s.Flow != nil && !s.Flow.TimeLimit.IsZero() {
		return s.Flow.TimeLimit.Unix()
	}
	if owner := s.OwnerUser(); owner != nil {
		return owner.ExpireAt
	}
	return 0
}

func (s *Client) EffectiveFlowLimitBytes() int64 {
	if s == nil {
		return 0
	}
	if s.FlowLimit > 0 {
		return s.FlowLimit
	}
	if s.Flow != nil && s.Flow.FlowLimit > 0 {
		return s.Flow.FlowLimit << 20
	}
	if owner := s.OwnerUser(); owner != nil {
		return owner.FlowLimit
	}
	return 0
}

func (s *Client) TotalTrafficTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.EnsureRuntimeTraffic()
	bridgeIn, bridgeOut, _ := s.BridgeTraffic.Snapshot()
	serviceIn, serviceOut, _ := s.ServiceTraffic.Snapshot()
	totalIn := bridgeIn + serviceIn
	totalOut := bridgeOut + serviceOut
	return totalIn, totalOut, totalIn + totalOut
}

func (s *Client) BridgeTrafficTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.EnsureRuntimeTraffic()
	return s.BridgeTraffic.Snapshot()
}

func (s *Client) ServiceTrafficTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.EnsureRuntimeTraffic()
	return s.ServiceTraffic.Snapshot()
}

func (s *Client) observeTraffic(bridge bool, in, out int64) error {
	if s == nil || (in == 0 && out == 0) {
		return nil
	}
	s.EnsureRuntimeTraffic()
	target := s.ServiceTraffic
	meter := s.ServiceMeter
	if bridge {
		target = s.BridgeTraffic
		meter = s.BridgeMeter
	}
	currentTotal := int64(0)
	if target != nil {
		_, _, currentTotal = target.Add(in, out)
	}
	if meter != nil {
		meter.Add(in, out)
	}
	s.TotalMeter.Add(in, out)
	if expireAt := s.EffectiveExpireAt(); expireAt > 0 && time.Now().Unix() >= expireAt {
		return errors.New("Client: time limit exceeded")
	}
	if flowLimit := s.EffectiveFlowLimitBytes(); flowLimit > 0 {
		other := s.BridgeTraffic
		if bridge {
			other = s.ServiceTraffic
		}
		_, _, otherTotal := other.Snapshot()
		total := currentTotal + otherTotal
		if total >= flowLimit {
			return errors.New("Client: flow limit exceeded")
		}
	}
	if owner := s.OwnerUser(); owner != nil {
		return owner.ObserveTotalTraffic(in, out)
	}
	return nil
}

func (s *Client) ObserveBridgeTraffic(in, out int64) error {
	return s.observeTraffic(true, in, out)
}

func (s *Client) ObserveServiceTraffic(in, out int64) error {
	return s.observeTraffic(false, in, out)
}

func (s *Client) BridgeRateTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.EnsureRuntimeTraffic()
	return rateSnapshot(s.BridgeMeter)
}

func (s *Client) ServiceRateTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.EnsureRuntimeTraffic()
	return rateSnapshot(s.ServiceMeter)
}

func (s *Client) TotalRateTotals() (int64, int64, int64) {
	if s == nil {
		return 0, 0, 0
	}
	s.EnsureRuntimeTraffic()
	return rateSnapshot(s.TotalMeter)
}

func (s *Client) ResetTraffic() {
	if s == nil {
		return
	}
	s.EnsureRuntimeTraffic()
	s.BridgeTraffic.Reset()
	s.ServiceTraffic.Reset()
	s.BridgeMeter.Reset()
	s.ServiceMeter.Reset()
	s.TotalMeter.Reset()
	if s.Flow != nil {
		s.Flow.InletFlow = 0
		s.Flow.ExportFlow = 0
	}
	s.InletFlow = 0
	s.ExportFlow = 0
}

func (s *Client) EffectiveExpireTime() time.Time {
	if expireAt := s.EffectiveExpireAt(); expireAt > 0 {
		return time.Unix(expireAt, 0)
	}
	return time.Time{}
}

func (s *Client) LegacyFlowLimitMegabytes() int64 {
	if s == nil {
		return 0
	}
	if s.Flow != nil && s.Flow.FlowLimit > 0 {
		return s.Flow.FlowLimit
	}
	return legacyFlowLimitMegabytes(s.EffectiveFlowLimitBytes())
}

func (s *Client) SetExpireAt(value int64) {
	if s == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	s.ExpireAt = value
	if s.Flow == nil {
		s.Flow = new(Flow)
	}
	if value > 0 {
		s.Flow.TimeLimit = time.Unix(value, 0)
		return
	}
	s.Flow.TimeLimit = time.Time{}
}

func (s *Client) SetFlowLimitBytes(value int64) {
	if s == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	s.FlowLimit = value
	if s.Flow == nil {
		s.Flow = new(Flow)
	}
	s.Flow.FlowLimit = legacyFlowLimitMegabytes(value)
}

func legacyFlowLimitMegabytes(value int64) int64 {
	if value <= 0 {
		return 0
	}
	const mb = int64(1024 * 1024)
	if value%mb == 0 {
		return value / mb
	}
	return (value + mb - 1) / mb
}
