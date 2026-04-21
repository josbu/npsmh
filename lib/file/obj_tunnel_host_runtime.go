package file

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/rate"
)

func InitializeTunnelRuntime(tunnel *Tunnel) {
	if tunnel == nil {
		return
	}
	tunnel.Lock()
	tunnel.normalizeLifecycleFieldsLocked()
	tunnel.Unlock()
	tunnel.EnsureRuntimeTraffic()
	tunnel.EnsureRuntimeRate()
}

func (t *Tunnel) EnsureRuntimeTraffic() {
	if t == nil {
		return
	}
	t.RLock()
	ready := t.ServiceTraffic != nil && t.ServiceMeter != nil
	t.RUnlock()
	if ready {
		return
	}
	t.Lock()
	defer t.Unlock()
	if t.ServiceTraffic == nil {
		t.ServiceTraffic = &TrafficStats{}
		if in, out := flowSnapshot(t.Flow); in != 0 || out != 0 {
			t.ServiceTraffic.Add(in, out)
		}
	}
	if t.ServiceMeter == nil {
		t.ServiceMeter = rate.NewMeter()
	}
}

func (t *Tunnel) NormalizeLifecycleFields() {
	if t == nil {
		return
	}
	t.Lock()
	defer t.Unlock()
	t.normalizeLifecycleFieldsLocked()
}

func (t *Tunnel) normalizeLifecycleFieldsLocked() {
	if t == nil {
		return
	}
	t.MaxConn = normalizeNonNegativeConnectionLimit(t.MaxConn)
	if t.Flow == nil {
		t.Flow = new(Flow)
	}
	if t.ExpireAt <= 0 && !t.Flow.TimeLimit.IsZero() {
		t.ExpireAt = t.Flow.TimeLimit.Unix()
	} else if t.ExpireAt > 0 && t.Flow.TimeLimit.IsZero() {
		t.Flow.TimeLimit = time.Unix(t.ExpireAt, 0)
	}
	if t.FlowLimit <= 0 && t.Flow.FlowLimit > 0 {
		t.FlowLimit = t.Flow.FlowLimit << 20
	} else if t.FlowLimit > 0 && t.Flow.FlowLimit == 0 {
		t.Flow.FlowLimit = legacyFlowLimitMegabytes(t.FlowLimit)
	}
}

func (t *Tunnel) EnsureRuntimeRate() {
	if t == nil {
		return
	}
	limit := int64(t.RateLimit) * 1024
	if t.RateLimit <= 0 {
		limit = 0
	}
	if t.Rate == nil {
		t.Rate = rate.NewRate(limit)
		t.Rate.Start()
		return
	}
	if t.Rate.Limit() != limit {
		t.Rate.ResetLimit(limit)
		return
	}
	t.Rate.Start()
}

func (t *Tunnel) ServiceTrafficTotals() (int64, int64, int64) {
	if t == nil {
		return 0, 0, 0
	}
	t.EnsureRuntimeTraffic()
	return t.ServiceTraffic.Snapshot()
}

func (t *Tunnel) ObserveServiceTraffic(in, out int64) error {
	if t == nil || (in == 0 && out == 0) {
		return nil
	}
	t.EnsureRuntimeTraffic()
	_, _, total := t.ServiceTraffic.Add(in, out)
	t.ServiceMeter.Add(in, out)
	if expireAt := t.EffectiveExpireAt(); expireAt > 0 && time.Now().Unix() >= expireAt {
		return errors.New("Task: time limit exceeded")
	}
	if flowLimit := t.EffectiveFlowLimitBytes(); flowLimit > 0 && total >= flowLimit {
		return errors.New("Task: flow limit exceeded")
	}
	return nil
}

func (t *Tunnel) ServiceRateTotals() (int64, int64, int64) {
	if t == nil {
		return 0, 0, 0
	}
	t.EnsureRuntimeTraffic()
	return rateSnapshot(t.ServiceMeter)
}

func (t *Tunnel) ResetServiceTraffic() {
	if t == nil {
		return
	}
	t.EnsureRuntimeTraffic()
	t.ServiceTraffic.Reset()
	t.ServiceMeter.Reset()
	if t.Flow != nil {
		t.Flow.InletFlow = 0
		t.Flow.ExportFlow = 0
	}
}

func (t *Tunnel) EffectiveExpireAt() int64 {
	if t == nil {
		return 0
	}
	if t.ExpireAt > 0 {
		return t.ExpireAt
	}
	if t.Flow != nil && !t.Flow.TimeLimit.IsZero() {
		return t.Flow.TimeLimit.Unix()
	}
	return 0
}

func (t *Tunnel) EffectiveFlowLimitBytes() int64 {
	if t == nil {
		return 0
	}
	if t.FlowLimit > 0 {
		return t.FlowLimit
	}
	if t.Flow != nil && t.Flow.FlowLimit > 0 {
		return t.Flow.FlowLimit << 20
	}
	return 0
}

func (t *Tunnel) SetExpireAt(value int64) {
	if t == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	t.ExpireAt = value
	if t.Flow == nil {
		t.Flow = new(Flow)
	}
	if value > 0 {
		t.Flow.TimeLimit = time.Unix(value, 0)
		return
	}
	t.Flow.TimeLimit = time.Time{}
}

func (t *Tunnel) SetFlowLimitBytes(value int64) {
	if t == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	t.FlowLimit = value
	if t.Flow == nil {
		t.Flow = new(Flow)
	}
	t.Flow.FlowLimit = legacyFlowLimitMegabytes(value)
}

func NewTunnelByHost(host *Host, port int) *Tunnel {
	host.EnsureRuntimeTraffic()
	return &Tunnel{
		ServerIp:         "0.0.0.0",
		Port:             port,
		Mode:             "tcp",
		Status:           !host.IsClose,
		RunStatus:        !host.IsClose,
		Client:           host.Client,
		Flow:             host.Flow,
		ExpireAt:         host.ExpireAt,
		FlowLimit:        host.FlowLimit,
		RateLimit:        host.RateLimit,
		Rate:             host.Rate,
		MaxConn:          host.MaxConn,
		NoStore:          true,
		Target:           host.Target,
		ServiceTraffic:   host.ServiceTraffic,
		ServiceMeter:     host.ServiceMeter,
		UserAuth:         host.UserAuth,
		MultiAccount:     host.MultiAccount,
		runtimeRouteUUID: host.RuntimeRouteUUID(),
	}
}

func FileTunnelRuntimeKey(verifyKey string, t *Tunnel) string {
	if t == nil {
		return crypt.GenerateUUID(strings.TrimSpace(verifyKey), "file").String()
	}
	return crypt.GenerateUUID(
		strings.TrimSpace(verifyKey),
		"file",
		strings.TrimSpace(t.ServerIp),
		strconv.Itoa(t.Port),
		strings.TrimSpace(t.LocalPath),
		strings.TrimSpace(t.StripPre),
		strconv.FormatBool(t.ReadOnly),
		fileTunnelAccountsSignature(t.MultiAccount),
		fileTunnelAccountsSignature(t.UserAuth),
		fileTunnelClientAuthSignature(t.Client),
	).String()
}

func FileTunnelIdentityEqual(a, b *Tunnel) bool {
	if a == nil || b == nil {
		return false
	}
	if strings.TrimSpace(a.Mode) != "file" || strings.TrimSpace(b.Mode) != "file" {
		return false
	}
	return a.Port == b.Port &&
		strings.TrimSpace(a.ServerIp) == strings.TrimSpace(b.ServerIp) &&
		strings.TrimSpace(a.LocalPath) == strings.TrimSpace(b.LocalPath) &&
		strings.TrimSpace(a.StripPre) == strings.TrimSpace(b.StripPre) &&
		a.ReadOnly == b.ReadOnly &&
		fileTunnelAccountsSignature(a.MultiAccount) == fileTunnelAccountsSignature(b.MultiAccount) &&
		fileTunnelAccountsSignature(a.UserAuth) == fileTunnelAccountsSignature(b.UserAuth) &&
		fileTunnelClientAuthSignature(a.Client) == fileTunnelClientAuthSignature(b.Client)
}

func fileTunnelAccountsSignature(accounts *MultiAccount) string {
	accountMap := GetAccountMap(accounts)
	if len(accountMap) > 0 {
		keys := make([]string, 0, len(accountMap))
		for user := range accountMap {
			keys = append(keys, user)
		}
		sort.Strings(keys)

		var builder strings.Builder
		for i, user := range keys {
			if i > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(strings.TrimSpace(user))
			builder.WriteByte('=')
			builder.WriteString(accountMap[user])
		}
		return builder.String()
	}
	if accounts == nil {
		return ""
	}
	return strings.TrimSpace(accounts.Content)
}

func fileTunnelClientAuthSignature(client *Client) string {
	if client == nil || client.Cnf == nil {
		return ""
	}
	user := strings.TrimSpace(client.Cnf.U)
	if user == "" || client.Cnf.P == "" {
		return ""
	}
	return user + "=" + client.Cnf.P
}

func (t *Tunnel) AddConn() {
	addRuntimeConn(t.runtimeConnCounterRef())
}

func (t *Tunnel) CutConn() {
	cutRuntimeConn(t.runtimeConnCounterRef())
}

func (t *Tunnel) GetConn() bool {
	if t == nil {
		return false
	}
	return acquireRuntimeConn(t.runtimeConnCounterRef(), t.MaxConn)
}

func (t *Tunnel) runtimeConnCounterRef() *int32 {
	if t == nil {
		return nil
	}
	if t.runtimeConnCounter != nil {
		return t.runtimeConnCounter
	}
	return &t.NowConn
}

func (t *Tunnel) runtimeConnValue() int32 {
	counter := t.runtimeConnCounterRef()
	if counter == nil {
		return 0
	}
	return atomic.LoadInt32(counter)
}

func (t *Tunnel) TouchMeta() {
	if t == nil {
		return
	}
	t.Lock()
	defer t.Unlock()
	t.Revision++
	t.UpdatedAt = time.Now().Unix()
}

func (t *Tunnel) SnapshotForUpdate() *Tunnel {
	if t == nil {
		return nil
	}
	snapshot := &Tunnel{
		ServerIp:      t.ServerIp,
		Mode:          t.Mode,
		Password:      t.Password,
		Remark:        t.Remark,
		TargetType:    t.TargetType,
		EntryAclMode:  t.EntryAclMode,
		EntryAclRules: t.EntryAclRules,
		entryPolicy:   t.entryPolicy,
		HttpProxy:     t.HttpProxy,
		Socks5Proxy:   t.Socks5Proxy,
		DestAclMode:   t.DestAclMode,
		DestAclRules:  t.DestAclRules,
		DestAclSet:    t.DestAclSet,
		destIPPolicy:  t.destIPPolicy,
		destPolicy:    t.destPolicy,
		LocalPath:     t.LocalPath,
		StripPre:      t.StripPre,
		ReadOnly:      t.ReadOnly,
		Target:        t.Target,
		UserAuth:      t.UserAuth,
		MultiAccount:  t.MultiAccount,
	}
	copyRuntimeHealth(&snapshot.Health, &t.Health)
	return snapshot
}

func (t *Tunnel) Update(other *Tunnel) {
	t.ServerIp = other.ServerIp
	t.Mode = other.Mode
	t.Password = other.Password
	t.Remark = other.Remark
	t.TargetType = other.TargetType
	t.EntryAclMode = other.EntryAclMode
	t.EntryAclRules = other.EntryAclRules
	t.entryPolicy = other.entryPolicy
	t.HttpProxy = other.HttpProxy
	t.Socks5Proxy = other.Socks5Proxy
	t.DestAclMode = other.DestAclMode
	t.DestAclRules = other.DestAclRules
	t.DestAclSet = other.DestAclSet
	t.destIPPolicy = other.destIPPolicy
	t.destPolicy = other.destPolicy
	t.LocalPath = other.LocalPath
	t.StripPre = other.StripPre
	t.ReadOnly = other.ReadOnly
	t.Target = other.Target
	t.UserAuth = other.UserAuth
	t.MultiAccount = other.MultiAccount
	copyRuntimeHealth(&t.Health, &other.Health)
}

func InitializeHostRuntime(host *Host) {
	if host == nil {
		return
	}
	host.Lock()
	host.normalizeLifecycleFieldsLocked()
	host.Unlock()
	host.EnsureRuntimeTraffic()
	host.EnsureRuntimeRate()
}

func (h *Host) EnsureRuntimeTraffic() {
	if h == nil {
		return
	}
	h.RLock()
	ready := h.ServiceTraffic != nil && h.ServiceMeter != nil
	h.RUnlock()
	if ready {
		return
	}
	h.Lock()
	defer h.Unlock()
	if h.ServiceTraffic == nil {
		h.ServiceTraffic = &TrafficStats{}
		if in, out := flowSnapshot(h.Flow); in != 0 || out != 0 {
			h.ServiceTraffic.Add(in, out)
		}
	}
	if h.ServiceMeter == nil {
		h.ServiceMeter = rate.NewMeter()
	}
}

func (h *Host) NormalizeLifecycleFields() {
	if h == nil {
		return
	}
	h.Lock()
	defer h.Unlock()
	h.normalizeLifecycleFieldsLocked()
}

func (h *Host) normalizeLifecycleFieldsLocked() {
	if h == nil {
		return
	}
	h.MaxConn = normalizeNonNegativeConnectionLimit(h.MaxConn)
	if h.Flow == nil {
		h.Flow = new(Flow)
	}
	if h.ExpireAt <= 0 && !h.Flow.TimeLimit.IsZero() {
		h.ExpireAt = h.Flow.TimeLimit.Unix()
	} else if h.ExpireAt > 0 && h.Flow.TimeLimit.IsZero() {
		h.Flow.TimeLimit = time.Unix(h.ExpireAt, 0)
	}
	if h.FlowLimit <= 0 && h.Flow.FlowLimit > 0 {
		h.FlowLimit = h.Flow.FlowLimit << 20
	} else if h.FlowLimit > 0 && h.Flow.FlowLimit == 0 {
		h.Flow.FlowLimit = legacyFlowLimitMegabytes(h.FlowLimit)
	}
}

func (h *Host) EnsureRuntimeRate() {
	if h == nil {
		return
	}
	limit := int64(h.RateLimit) * 1024
	if h.RateLimit <= 0 {
		limit = 0
	}
	if h.Rate == nil {
		h.Rate = rate.NewRate(limit)
		h.Rate.Start()
		return
	}
	if h.Rate.Limit() != limit {
		h.Rate.ResetLimit(limit)
		return
	}
	h.Rate.Start()
}

func (h *Host) ServiceTrafficTotals() (int64, int64, int64) {
	if h == nil {
		return 0, 0, 0
	}
	h.EnsureRuntimeTraffic()
	return h.ServiceTraffic.Snapshot()
}

func (h *Host) ObserveServiceTraffic(in, out int64) error {
	if h == nil || (in == 0 && out == 0) {
		return nil
	}
	h.EnsureRuntimeTraffic()
	_, _, total := h.ServiceTraffic.Add(in, out)
	h.ServiceMeter.Add(in, out)
	if expireAt := h.EffectiveExpireAt(); expireAt > 0 && time.Now().Unix() >= expireAt {
		return errors.New("Host: time limit exceeded")
	}
	if flowLimit := h.EffectiveFlowLimitBytes(); flowLimit > 0 && total >= flowLimit {
		return errors.New("Host: flow limit exceeded")
	}
	return nil
}

func (h *Host) ServiceRateTotals() (int64, int64, int64) {
	if h == nil {
		return 0, 0, 0
	}
	h.EnsureRuntimeTraffic()
	return rateSnapshot(h.ServiceMeter)
}

func (h *Host) ResetServiceTraffic() {
	if h == nil {
		return
	}
	h.EnsureRuntimeTraffic()
	h.ServiceTraffic.Reset()
	h.ServiceMeter.Reset()
	if h.Flow != nil {
		h.Flow.InletFlow = 0
		h.Flow.ExportFlow = 0
	}
}

func (h *Host) EffectiveExpireAt() int64 {
	if h == nil {
		return 0
	}
	if h.ExpireAt > 0 {
		return h.ExpireAt
	}
	if h.Flow != nil && !h.Flow.TimeLimit.IsZero() {
		return h.Flow.TimeLimit.Unix()
	}
	return 0
}

func (h *Host) EffectiveFlowLimitBytes() int64 {
	if h == nil {
		return 0
	}
	if h.FlowLimit > 0 {
		return h.FlowLimit
	}
	if h.Flow != nil && h.Flow.FlowLimit > 0 {
		return h.Flow.FlowLimit << 20
	}
	return 0
}

func (h *Host) SetExpireAt(value int64) {
	if h == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	h.ExpireAt = value
	if h.Flow == nil {
		h.Flow = new(Flow)
	}
	if value > 0 {
		h.Flow.TimeLimit = time.Unix(value, 0)
		return
	}
	h.Flow.TimeLimit = time.Time{}
}

func (h *Host) SetFlowLimitBytes(value int64) {
	if h == nil {
		return
	}
	if value < 0 {
		value = 0
	}
	h.FlowLimit = value
	if h.Flow == nil {
		h.Flow = new(Flow)
	}
	h.Flow.FlowLimit = legacyFlowLimitMegabytes(value)
}

func (h *Host) AddConn() {
	addRuntimeConn(h.runtimeConnCounterRef())
}

func (h *Host) CutConn() {
	cutRuntimeConn(h.runtimeConnCounterRef())
}

func (h *Host) GetConn() bool {
	if h == nil {
		return false
	}
	return acquireRuntimeConn(h.runtimeConnCounterRef(), h.MaxConn)
}

func (h *Host) runtimeConnCounterRef() *int32 {
	if h == nil {
		return nil
	}
	if h.runtimeConnCounter != nil {
		return h.runtimeConnCounter
	}
	return &h.NowConn
}

func (h *Host) runtimeConnValue() int32 {
	counter := h.runtimeConnCounterRef()
	if counter == nil {
		return 0
	}
	return atomic.LoadInt32(counter)
}

func (h *Host) TouchMeta() {
	if h == nil {
		return
	}
	h.Lock()
	defer h.Unlock()
	h.Revision++
	h.UpdatedAt = time.Now().Unix()
}

func (h *Host) SnapshotForUpdate() *Host {
	if h == nil {
		return nil
	}
	snapshot := &Host{
		HeaderChange:     h.HeaderChange,
		RespHeaderChange: h.RespHeaderChange,
		HostChange:       h.HostChange,
		PathRewrite:      h.PathRewrite,
		Remark:           h.Remark,
		RedirectURL:      h.RedirectURL,
		HttpsJustProxy:   h.HttpsJustProxy,
		TlsOffload:       h.TlsOffload,
		AutoSSL:          h.AutoSSL,
		CertType:         h.CertType,
		CertHash:         h.CertHash,
		CertFile:         h.CertFile,
		KeyFile:          h.KeyFile,
		AutoHttps:        h.AutoHttps,
		AutoCORS:         h.AutoCORS,
		CompatMode:       h.CompatMode,
		EntryAclMode:     h.EntryAclMode,
		EntryAclRules:    h.EntryAclRules,
		entryPolicy:      h.entryPolicy,
		TargetIsHttps:    h.TargetIsHttps,
		Target:           h.Target,
		UserAuth:         h.UserAuth,
		MultiAccount:     h.MultiAccount,
	}
	copyRuntimeHealth(&snapshot.Health, &h.Health)
	return snapshot
}

func (h *Host) Update(other *Host) {
	h.HeaderChange = other.HeaderChange
	h.RespHeaderChange = other.RespHeaderChange
	h.HostChange = other.HostChange
	h.PathRewrite = other.PathRewrite
	h.Remark = other.Remark
	h.RedirectURL = other.RedirectURL
	h.HttpsJustProxy = other.HttpsJustProxy
	h.TlsOffload = other.TlsOffload
	h.AutoSSL = other.AutoSSL
	h.CertType = common.GetCertType(other.CertFile)
	h.CertHash = crypt.FNV1a64(h.CertType, other.CertFile, other.KeyFile)
	h.CertFile = other.CertFile
	h.KeyFile = other.KeyFile
	h.AutoHttps = other.AutoHttps
	h.AutoCORS = other.AutoCORS
	h.CompatMode = other.CompatMode
	h.EntryAclMode = other.EntryAclMode
	h.EntryAclRules = other.EntryAclRules
	h.entryPolicy = other.entryPolicy
	h.TargetIsHttps = other.TargetIsHttps
	h.Target = other.Target
	h.UserAuth = other.UserAuth
	h.MultiAccount = other.MultiAccount
	copyRuntimeHealth(&h.Health, &other.Health)
}
