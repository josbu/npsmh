package file

import (
	"errors"
	"strings"
	"sync"

	"github.com/djylb/nps/lib/common"
)

type runtimeOwnerPool[T any] struct {
	mu      sync.RWMutex
	order   []string
	entries map[string]T
	next    int
}

func newRuntimeOwnerPool[T any]() *runtimeOwnerPool[T] {
	return &runtimeOwnerPool[T]{
		entries: make(map[string]T),
	}
}

func (p *runtimeOwnerPool[T]) clone() *runtimeOwnerPool[T] {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	cloned := &runtimeOwnerPool[T]{
		order:   append([]string(nil), p.order...),
		entries: make(map[string]T, len(p.entries)),
		next:    p.next,
	}
	for key, value := range p.entries {
		cloned.entries[key] = value
	}
	return cloned
}

func (p *runtimeOwnerPool[T]) count() int {
	if p == nil {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.order)
}

func (p *runtimeOwnerPool[T]) set(uuid string, value T) {
	if p == nil || uuid == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = make(map[string]T)
	}
	if _, exists := p.entries[uuid]; !exists {
		p.order = append(p.order, uuid)
	}
	p.entries[uuid] = value
	if p.next < 0 || p.next >= len(p.order) {
		p.next = 0
	}
}

func (p *runtimeOwnerPool[T]) remove(uuid string) (int, bool) {
	if p == nil || uuid == "" {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.entries[uuid]; !exists {
		return len(p.order), false
	}
	delete(p.entries, uuid)
	for index, current := range p.order {
		if current != uuid {
			continue
		}
		p.order = append(p.order[:index], p.order[index+1:]...)
		if len(p.order) == 0 {
			p.next = 0
			return 0, true
		}
		if p.next > index {
			p.next--
		} else if p.next >= len(p.order) {
			p.next = 0
		}
		return len(p.order), true
	}
	if len(p.order) == 0 {
		p.next = 0
	}
	return len(p.order), true
}

func (p *runtimeOwnerPool[T]) first() (string, T, bool) {
	var zero T
	if p == nil {
		return "", zero, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, uuid := range p.order {
		value, ok := p.entries[uuid]
		if ok {
			return uuid, value, true
		}
	}
	return "", zero, false
}

func (p *runtimeOwnerPool[T]) selectNext() (string, T, bool) {
	var zero T
	if p == nil {
		return "", zero, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.order) == 0 {
		return "", zero, false
	}
	if p.next < 0 || p.next >= len(p.order) {
		p.next = 0
	}
	start := p.next
	for i := 0; i < len(p.order); i++ {
		index := (start + i) % len(p.order)
		uuid := p.order[index]
		value, ok := p.entries[uuid]
		if !ok {
			continue
		}
		p.next = (index + 1) % len(p.order)
		return uuid, value, true
	}
	return "", zero, false
}

func (p *runtimeOwnerPool[T]) get(uuid string) (T, bool) {
	var zero T
	if p == nil || uuid == "" {
		return zero, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	value, ok := p.entries[uuid]
	return value, ok
}

func (p *runtimeOwnerPool[T]) uuids() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.order) == 0 {
		return nil
	}
	items := make([]string, 0, len(p.order))
	for _, uuid := range p.order {
		if _, ok := p.entries[uuid]; ok {
			items = append(items, uuid)
		}
	}
	return items
}

type tunnelRuntimeBindingSnapshot struct {
	routeUUID string
	owners    *runtimeOwnerPool[*Tunnel]
}

type hostRuntimeBindingSnapshot struct {
	routeUUID string
	owners    *runtimeOwnerPool[*Host]
}

func (t *Tunnel) RuntimeRouteUUID() string {
	if t == nil {
		return ""
	}
	t.RLock()
	defer t.RUnlock()
	return strings.TrimSpace(t.runtimeRouteUUID)
}

func (t *Tunnel) RuntimeOwnerUUIDs() []string {
	if t == nil {
		return nil
	}
	t.RLock()
	owners := t.runtimeOwners
	t.RUnlock()
	return owners.uuids()
}

func (t *Tunnel) RuntimeOwnerCount() int {
	if t == nil {
		return 0
	}
	t.RLock()
	owners := t.runtimeOwners
	t.RUnlock()
	return owners.count()
}

func (t *Tunnel) RuntimeOwnerLoadBalanced() bool {
	return t.RuntimeOwnerCount() > 1
}

func (t *Tunnel) BackendLoadBalanced() bool {
	if t == nil || t.Target == nil {
		return false
	}
	return t.Target.TargetCount() > 1
}

func (t *Tunnel) RequiresPerRequestBackend() bool {
	return t != nil && (t.RuntimeOwnerLoadBalanced() || t.BackendLoadBalanced())
}

func (t *Tunnel) BindRuntimeOwner(uuid string, snapshot *Tunnel) {
	if t == nil || snapshot == nil {
		return
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return
	}
	update := snapshot.SnapshotForUpdate()
	if update == nil {
		return
	}
	normalizeRuntimeTunnelOwnerSnapshot(update)

	t.Lock()
	defer t.Unlock()

	prevCount := 0
	if t.runtimeOwners != nil {
		prevCount = t.runtimeOwners.count()
	}
	if t.runtimeOwners == nil {
		t.runtimeOwners = newRuntimeOwnerPool[*Tunnel]()
	}
	t.runtimeOwners.set(uuid, update)
	if prevCount == 0 || t.runtimeRouteUUID == uuid {
		t.runtimeRouteUUID = uuid
		t.Update(update)
	}
}

func (t *Tunnel) SnapshotRuntimeBinding() tunnelRuntimeBindingSnapshot {
	if t == nil {
		return tunnelRuntimeBindingSnapshot{}
	}
	t.RLock()
	defer t.RUnlock()
	return tunnelRuntimeBindingSnapshot{
		routeUUID: t.runtimeRouteUUID,
		owners:    t.runtimeOwners.clone(),
	}
}

func (t *Tunnel) RestoreRuntimeBinding(snapshot tunnelRuntimeBindingSnapshot) {
	if t == nil {
		return
	}
	t.runtimeRouteUUID = snapshot.routeUUID
	t.runtimeOwners = snapshot.owners
}

func (t *Tunnel) RemoveRuntimeOwner(uuid string) int {
	if t == nil {
		return 0
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return t.RuntimeOwnerCount()
	}

	t.Lock()
	defer t.Unlock()

	if t.runtimeOwners == nil {
		if t.runtimeRouteUUID == uuid {
			t.runtimeRouteUUID = ""
		}
		return 0
	}
	remaining, removed := t.runtimeOwners.remove(uuid)
	if !removed {
		return remaining
	}
	if remaining == 0 {
		t.runtimeOwners = nil
		t.runtimeRouteUUID = ""
		return 0
	}
	if t.runtimeRouteUUID == uuid || strings.TrimSpace(t.runtimeRouteUUID) == "" {
		nextUUID, snapshot, ok := t.runtimeOwners.first()
		if ok {
			t.runtimeRouteUUID = nextUUID
			if snapshot != nil {
				snapshot.RLock()
				t.Update(snapshot)
				snapshot.RUnlock()
			}
		} else {
			t.runtimeRouteUUID = ""
		}
	}
	return remaining
}

func (t *Tunnel) SelectRuntimeRoute() *Tunnel {
	if t == nil {
		return nil
	}
	t.RLock()
	view := t.runtimeRouteViewLocked()
	owners := t.runtimeOwners
	t.RUnlock()
	if owners == nil {
		return view
	}
	uuid, snapshot, ok := owners.selectNext()
	if !ok {
		return view
	}
	view.runtimeRouteUUID = uuid
	if snapshot != nil {
		snapshot.RLock()
		applyRuntimeTunnelBackend(view, snapshot)
		snapshot.RUnlock()
	}
	return view
}

func (t *Tunnel) SelectRuntimeRouteByUUID(uuid string) *Tunnel {
	if t == nil {
		return nil
	}
	uuid = strings.TrimSpace(uuid)
	t.RLock()
	view := t.runtimeRouteViewLocked()
	owners := t.runtimeOwners
	t.RUnlock()
	if owners == nil || uuid == "" {
		return view
	}
	snapshot, ok := owners.get(uuid)
	if !ok {
		return view
	}
	view.runtimeRouteUUID = uuid
	if snapshot != nil {
		snapshot.RLock()
		applyRuntimeTunnelBackend(view, snapshot)
		snapshot.RUnlock()
	}
	return view
}

func (t *Tunnel) runtimeRouteViewLocked() *Tunnel {
	if t == nil {
		return nil
	}
	view := &Tunnel{
		Id:                 t.Id,
		Revision:           t.Revision,
		UpdatedAt:          t.UpdatedAt,
		Port:               t.Port,
		ServerIp:           t.ServerIp,
		Mode:               t.Mode,
		Status:             t.Status,
		RunStatus:          t.RunStatus,
		Client:             t.Client,
		Ports:              t.Ports,
		ExpireAt:           t.ExpireAt,
		FlowLimit:          t.FlowLimit,
		RateLimit:          t.RateLimit,
		Flow:               t.Flow,
		Rate:               t.Rate,
		ServiceTraffic:     t.ServiceTraffic,
		ServiceMeter:       t.ServiceMeter,
		MaxConn:            t.MaxConn,
		NowConn:            t.runtimeConnValue(),
		Password:           t.Password,
		Remark:             t.Remark,
		TargetAddr:         t.TargetAddr,
		TargetType:         t.TargetType,
		EntryAclMode:       t.EntryAclMode,
		EntryAclRules:      t.EntryAclRules,
		entryPolicy:        t.entryPolicy,
		DestAclMode:        t.DestAclMode,
		DestAclRules:       t.DestAclRules,
		DestAclSet:         t.DestAclSet,
		destIPPolicy:       t.destIPPolicy,
		destPolicy:         t.destPolicy,
		NoStore:            t.NoStore,
		IsHttp:             t.IsHttp,
		HttpProxy:          t.HttpProxy,
		Socks5Proxy:        t.Socks5Proxy,
		LocalPath:          t.LocalPath,
		StripPre:           t.StripPre,
		ReadOnly:           t.ReadOnly,
		Target:             t.Target,
		UserAuth:           t.UserAuth,
		MultiAccount:       t.MultiAccount,
		runtimeRouteUUID:   t.runtimeRouteUUID,
		runtimeOwners:      t.runtimeOwners,
		runtimeConnCounter: t.runtimeConnCounterRef(),
	}
	copyRuntimeHealth(&view.Health, &t.Health)
	return view
}

func (h *Host) RuntimeRouteUUID() string {
	if h == nil {
		return ""
	}
	h.RLock()
	defer h.RUnlock()
	return strings.TrimSpace(h.runtimeRouteUUID)
}

func (h *Host) RuntimeOwnerUUIDs() []string {
	if h == nil {
		return nil
	}
	h.RLock()
	owners := h.runtimeOwners
	h.RUnlock()
	return owners.uuids()
}

func (h *Host) RuntimeOwnerCount() int {
	if h == nil {
		return 0
	}
	h.RLock()
	owners := h.runtimeOwners
	h.RUnlock()
	return owners.count()
}

func (h *Host) RuntimeOwnerLoadBalanced() bool {
	return h.RuntimeOwnerCount() > 1
}

func (h *Host) BackendLoadBalanced() bool {
	if h == nil || h.Target == nil {
		return false
	}
	return h.Target.TargetCount() > 1
}

func (h *Host) RequiresPerRequestBackend() bool {
	return h != nil && (h.RuntimeOwnerLoadBalanced() || h.BackendLoadBalanced())
}

func (h *Host) BindRuntimeOwner(uuid string, snapshot *Host) {
	if h == nil || snapshot == nil {
		return
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return
	}
	update := snapshot.SnapshotForUpdate()
	if update == nil {
		return
	}
	normalizeRuntimeHostOwnerSnapshot(update)

	h.Lock()
	defer h.Unlock()

	prevCount := 0
	if h.runtimeOwners != nil {
		prevCount = h.runtimeOwners.count()
	}
	if h.runtimeOwners == nil {
		h.runtimeOwners = newRuntimeOwnerPool[*Host]()
	}
	h.runtimeOwners.set(uuid, update)
	if prevCount == 0 || h.runtimeRouteUUID == uuid {
		h.runtimeRouteUUID = uuid
		h.Update(update)
	}
}

func (h *Host) SnapshotRuntimeBinding() hostRuntimeBindingSnapshot {
	if h == nil {
		return hostRuntimeBindingSnapshot{}
	}
	h.RLock()
	defer h.RUnlock()
	return hostRuntimeBindingSnapshot{
		routeUUID: h.runtimeRouteUUID,
		owners:    h.runtimeOwners.clone(),
	}
}

func (h *Host) RestoreRuntimeBinding(snapshot hostRuntimeBindingSnapshot) {
	if h == nil {
		return
	}
	h.runtimeRouteUUID = snapshot.routeUUID
	h.runtimeOwners = snapshot.owners
}

func (h *Host) RemoveRuntimeOwner(uuid string) int {
	if h == nil {
		return 0
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return h.RuntimeOwnerCount()
	}

	h.Lock()
	defer h.Unlock()

	if h.runtimeOwners == nil {
		if h.runtimeRouteUUID == uuid {
			h.runtimeRouteUUID = ""
		}
		return 0
	}
	remaining, removed := h.runtimeOwners.remove(uuid)
	if !removed {
		return remaining
	}
	if remaining == 0 {
		h.runtimeOwners = nil
		h.runtimeRouteUUID = ""
		return 0
	}
	if h.runtimeRouteUUID == uuid || strings.TrimSpace(h.runtimeRouteUUID) == "" {
		nextUUID, snapshot, ok := h.runtimeOwners.first()
		if ok {
			h.runtimeRouteUUID = nextUUID
			if snapshot != nil {
				snapshot.RLock()
				h.Update(snapshot)
				snapshot.RUnlock()
			}
		} else {
			h.runtimeRouteUUID = ""
		}
	}
	return remaining
}

func (h *Host) SelectRuntimeRoute() *Host {
	if h == nil {
		return nil
	}
	h.RLock()
	view := h.runtimeRouteViewLocked()
	owners := h.runtimeOwners
	h.RUnlock()
	if owners == nil {
		return view
	}
	uuid, snapshot, ok := owners.selectNext()
	if !ok {
		return view
	}
	view.runtimeRouteUUID = uuid
	if snapshot != nil {
		snapshot.RLock()
		applyRuntimeHostBackend(view, snapshot)
		snapshot.RUnlock()
	}
	return view
}

func (h *Host) runtimeRouteViewLocked() *Host {
	if h == nil {
		return nil
	}
	view := &Host{
		Id:                 h.Id,
		Revision:           h.Revision,
		UpdatedAt:          h.UpdatedAt,
		Host:               h.Host,
		HeaderChange:       h.HeaderChange,
		RespHeaderChange:   h.RespHeaderChange,
		HostChange:         h.HostChange,
		Location:           h.Location,
		PathRewrite:        h.PathRewrite,
		Remark:             h.Remark,
		Scheme:             h.Scheme,
		RedirectURL:        h.RedirectURL,
		HttpsJustProxy:     h.HttpsJustProxy,
		TlsOffload:         h.TlsOffload,
		AutoSSL:            h.AutoSSL,
		CertType:           h.CertType,
		CertHash:           h.CertHash,
		CertFile:           h.CertFile,
		KeyFile:            h.KeyFile,
		NoStore:            h.NoStore,
		IsClose:            h.IsClose,
		AutoHttps:          h.AutoHttps,
		AutoCORS:           h.AutoCORS,
		CompatMode:         h.CompatMode,
		ExpireAt:           h.ExpireAt,
		FlowLimit:          h.FlowLimit,
		RateLimit:          h.RateLimit,
		Flow:               h.Flow,
		Rate:               h.Rate,
		ServiceTraffic:     h.ServiceTraffic,
		ServiceMeter:       h.ServiceMeter,
		MaxConn:            h.MaxConn,
		NowConn:            h.runtimeConnValue(),
		Client:             h.Client,
		EntryAclMode:       h.EntryAclMode,
		EntryAclRules:      h.EntryAclRules,
		entryPolicy:        h.entryPolicy,
		TargetIsHttps:      h.TargetIsHttps,
		Target:             h.Target,
		UserAuth:           h.UserAuth,
		MultiAccount:       h.MultiAccount,
		runtimeRouteUUID:   h.runtimeRouteUUID,
		runtimeOwners:      h.runtimeOwners,
		runtimeConnCounter: h.runtimeConnCounterRef(),
	}
	copyRuntimeHealth(&view.Health, &h.Health)
	return view
}

func copyRuntimeHealth(dst *Health, src *Health) {
	if dst == nil {
		return
	}
	*dst = Health{}
	if src == nil {
		return
	}
	dst.HealthCheckTimeout = src.HealthCheckTimeout
	dst.HealthMaxFail = src.HealthMaxFail
	dst.HealthCheckInterval = src.HealthCheckInterval
	dst.HealthNextTime = src.HealthNextTime
	dst.HttpHealthUrl = src.HttpHealthUrl
	dst.HealthCheckType = src.HealthCheckType
	dst.HealthCheckTarget = src.HealthCheckTarget
	if len(src.HealthRemoveArr) > 0 {
		dst.HealthRemoveArr = append([]string(nil), src.HealthRemoveArr...)
	}
	if len(src.HealthMap) > 0 {
		dst.HealthMap = make(map[string]int, len(src.HealthMap))
		for key, value := range src.HealthMap {
			dst.HealthMap[key] = value
		}
	}
}

func applyRuntimeTargetHealthLocked(target *Target, healthRemoveArr *[]string, info string, healthy bool) bool {
	if target == nil {
		return false
	}
	info = strings.TrimSpace(info)
	if info == "" {
		return false
	}
	target.Lock()
	defer target.Unlock()
	configured := configuredTargetEntries(target)
	if healthy {
		if !common.IsArrContains(*healthRemoveArr, info) {
			return false
		}
		if !common.IsArrContains(target.TargetArr, info) {
			target.TargetArr = append(target.TargetArr, info)
		}
		*healthRemoveArr = common.RemoveArrVal(*healthRemoveArr, info)
		return true
	}
	if target.TargetArr == nil || (len(target.TargetArr) == 0 && len(*healthRemoveArr) == 0) {
		target.TargetArr = append([]string(nil), configured...)
		target.targetArrSource = normalizedTargetSource(target.TargetStr)
		target.nowIndex = -1
	}
	if !common.IsArrContains(target.TargetArr, info) && !common.IsArrContains(configured, info) {
		return false
	}
	target.TargetArr = common.RemoveArrVal(target.TargetArr, info)
	if !common.IsArrContains(*healthRemoveArr, info) {
		*healthRemoveArr = append(*healthRemoveArr, info)
	}
	return true
}

func (t *Tunnel) UpdateRuntimeTargetHealth(uuid, info string, healthy bool) bool {
	if t == nil {
		return false
	}
	uuid = strings.TrimSpace(uuid)
	t.Lock()
	defer t.Unlock()
	if t.runtimeOwners == nil || uuid == "" {
		return applyRuntimeTargetHealthLocked(t.Target, &t.HealthRemoveArr, info, healthy)
	}
	updated := false
	if snapshot, ok := t.runtimeOwners.get(uuid); ok && snapshot != nil {
		snapshot.Lock()
		updated = applyRuntimeTargetHealthLocked(snapshot.Target, &snapshot.HealthRemoveArr, info, healthy) || updated
		snapshot.Unlock()
	}
	if strings.TrimSpace(t.runtimeRouteUUID) == uuid {
		updated = applyRuntimeTargetHealthLocked(t.Target, &t.HealthRemoveArr, info, healthy) || updated
	}
	return updated
}

func applyRuntimeTunnelBackend(dst *Tunnel, src *Tunnel) {
	if dst == nil || src == nil {
		return
	}
	dst.Target = src.Target
	copyRuntimeHealth(&dst.Health, &src.Health)
}

func applyRuntimeHostBackend(dst *Host, src *Host) {
	if dst == nil || src == nil {
		return
	}
	dst.TargetIsHttps = src.TargetIsHttps
	dst.Target = src.Target
	copyRuntimeHealth(&dst.Health, &src.Health)
}

func configuredTargetEntries(target *Target) []string {
	if target == nil {
		return nil
	}
	targetStr := normalizedTargetSource(target.TargetStr)
	return parseNormalizedTargetEntries(targetStr)
}

func normalizedTargetSource(targetStr string) string {
	targetStr = strings.ReplaceAll(targetStr, "：", ":")
	return strings.ReplaceAll(targetStr, "\r\n", "\n")
}

func parseNormalizedTargetEntries(normalized string) []string {
	return common.TrimArr(strings.Split(normalized, "\n"))
}

func normalizeRuntimeTunnelOwnerSnapshot(update *Tunnel) {
	if update == nil {
		return
	}
	switch update.Mode {
	case "socks5":
		update.Mode = "mixProxy"
		update.HttpProxy = false
		update.Socks5Proxy = true
	case "httpProxy":
		update.Mode = "mixProxy"
		update.HttpProxy = true
		update.Socks5Proxy = false
	}
	if update.TargetType != common.CONN_TCP && update.TargetType != common.CONN_UDP {
		update.TargetType = common.CONN_ALL
	}
	update.CompileEntryACL()
	update.CompileDestACL()
}

func normalizeRuntimeHostOwnerSnapshot(update *Host) {
	if update == nil {
		return
	}
	normalizeHostRoutingFields(update)
	update.CompileEntryACL()
}

func (h *Host) UpdateRuntimeTargetHealth(uuid, info string, healthy bool) bool {
	if h == nil {
		return false
	}
	uuid = strings.TrimSpace(uuid)
	h.Lock()
	defer h.Unlock()
	if h.runtimeOwners == nil || uuid == "" {
		return applyRuntimeTargetHealthLocked(h.Target, &h.HealthRemoveArr, info, healthy)
	}
	updated := false
	if snapshot, ok := h.runtimeOwners.get(uuid); ok && snapshot != nil {
		snapshot.Lock()
		updated = applyRuntimeTargetHealthLocked(snapshot.Target, &snapshot.HealthRemoveArr, info, healthy) || updated
		snapshot.Unlock()
	}
	if strings.TrimSpace(h.runtimeRouteUUID) == uuid {
		updated = applyRuntimeTargetHealthLocked(h.Target, &h.HealthRemoveArr, info, healthy) || updated
	}
	return updated
}

func (s *Target) TargetCount() int {
	if s == nil {
		return 0
	}
	s.Lock()
	defer s.Unlock()
	s.ensureTargetArrLocked()
	return len(s.TargetArr)
}

func (s *Target) ensureTargetArrLocked() {
	if s == nil || s.TargetArr != nil {
		return
	}
	s.TargetStr = strings.ReplaceAll(s.TargetStr, "：", ":")
	normalized := normalizedTargetSource(s.TargetStr)
	s.targetArrSource = normalized
	s.TargetArr = parseNormalizedTargetEntries(normalized)
	s.nowIndex = -1
}

func (s *Target) refreshConfiguredTargetsLocked() {
	if s == nil {
		return
	}
	s.TargetStr = strings.ReplaceAll(s.TargetStr, "：", ":")
	normalized := normalizedTargetSource(s.TargetStr)
	if s.TargetArr != nil && s.targetArrSource == normalized {
		return
	}
	s.targetArrSource = normalized
	s.TargetArr = parseNormalizedTargetEntries(normalized)
	s.nowIndex = -1
}

func (s *Target) GetRandomTarget() (string, error) {
	return s.getNextTarget("")
}

func (s *Target) GetRouteTarget(routeKey string) (string, error) {
	return s.getNextTarget(routeKey)
}

func (s *Target) getNextTarget(routeKey string) (string, error) {
	if s == nil {
		return "", errors.New("all inward-bending targets are offline")
	}
	s.Lock()
	defer s.Unlock()

	s.refreshConfiguredTargetsLocked()

	if len(s.TargetArr) == 1 {
		return s.TargetArr[0], nil
	}
	if len(s.TargetArr) == 0 {
		return "", errors.New("all inward-bending targets are offline")
	}

	if s.nowIndex < 0 {
		s.nowIndex = routeTargetOffset(routeKey, len(s.TargetArr)) - 1
	}
	if s.nowIndex >= len(s.TargetArr)-1 {
		s.nowIndex = -1
	}
	s.nowIndex++
	return s.TargetArr[s.nowIndex], nil
}

func routeTargetOffset(routeKey string, size int) int {
	if size <= 1 {
		return 0
	}
	routeKey = strings.TrimSpace(routeKey)
	if routeKey == "" {
		return 0
	}
	sum := 0
	for i := 0; i < len(routeKey); i++ {
		sum += int(routeKey[i])
	}
	if sum < 0 {
		sum = -sum
	}
	return sum % size
}

func CloneTargetSnapshot(target *Target) *Target {
	return cloneTargetSnapshot(target)
}

func cloneTargetSnapshot(target *Target) *Target {
	if target == nil {
		return nil
	}
	target.RLock()
	defer target.RUnlock()
	return &Target{
		nowIndex:        target.nowIndex,
		TargetStr:       target.TargetStr,
		TargetArr:       append([]string(nil), target.TargetArr...),
		LocalProxy:      target.LocalProxy,
		ProxyProtocol:   target.ProxyProtocol,
		targetArrSource: target.targetArrSource,
	}
}
