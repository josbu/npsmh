package proxy

import (
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/conn"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/logs"
)

var errProxyAccessDenied = errors.New("proxy access denied")
var errProxyBridgeUnavailable = errors.New("proxy bridge runtime unavailable")
var errProxyUnauthorized = errors.New("401 Unauthorized")

type BridgeLinkOpener interface {
	SendLinkInfo(clientId int, link *conn.Link, t *file.Tunnel) (target net.Conn, err error)
}

type BridgeServerRole interface {
	IsServer() bool
}

type BridgeClientProcessor interface {
	CliProcess(c *conn.Conn, tunnelType string)
}

type Service interface {
	Start() error
	Close() error
}

type NetBridge interface {
	BridgeLinkOpener
	BridgeServerRole
	BridgeClientProcessor
}

type proxyLocker interface {
	Lock()
	Unlock()
}

// BaseServer struct
type BaseServer struct {
	Id              int
	linkOpener      BridgeLinkOpener
	serverRole      BridgeServerRole
	clientProcessor BridgeClientProcessor
	sources         proxyRuntimeSources
	Task            *file.Tunnel
	ErrorContent    []byte
	sync.Mutex
}

func NewBaseServer(bridge NetBridge, task *file.Tunnel) *BaseServer {
	return newBaseServerWithRuntimeSources(currentProxyRuntimeSources(), bridge, task)
}

func NewBaseServerWithRuntime(runtime proxyRuntimeContext, bridge NetBridge, task *file.Tunnel) *BaseServer {
	return newBaseServerWithRuntimeSources(injectedProxyRuntimeSources(runtime), bridge, task)
}

func NewBaseServerWithRuntimeRoot(runtimeRoot func() proxyRuntimeContext, bridge NetBridge, task *file.Tunnel) *BaseServer {
	return NewBaseServerWithRuntimeRootAndLocalProxyAllowed(runtimeRoot, currentProxyLocalProxyAllowed, bridge, task)
}

func NewBaseServerWithRuntimeRootAndLocalProxyAllowed(runtimeRoot func() proxyRuntimeContext, localProxyRoot func() bool, bridge NetBridge, task *file.Tunnel) *BaseServer {
	return newBaseServerWithRuntimeSources(proxyRuntimeSources{
		runtimeRoot:       runtimeRoot,
		localProxyAllowed: localProxyRoot,
	}, bridge, task)
}

func newBaseServerWithRuntimeSources(sources proxyRuntimeSources, bridge NetBridge, task *file.Tunnel) *BaseServer {
	var linkOpener BridgeLinkOpener
	var serverRole BridgeServerRole
	var clientProcessor BridgeClientProcessor
	if !isNilProxyRuntimeValue(bridge) {
		linkOpener = bridge
		serverRole = bridge
		clientProcessor = bridge
	}
	return &BaseServer{
		linkOpener:      linkOpener,
		serverRole:      serverRole,
		clientProcessor: clientProcessor,
		sources:         sources,
		Task:            task,
		ErrorContent:    nil,
		Mutex:           sync.Mutex{},
	}
}

func (s *BaseServer) CurrentTask() *file.Tunnel {
	if s == nil {
		return nil
	}
	return s.Task
}

func (s *BaseServer) SetTask(task *file.Tunnel) {
	if s == nil {
		return
	}
	s.Task = task
}

func (s *BaseServer) CurrentErrorContent() []byte {
	if s == nil {
		return nil
	}
	return s.ErrorContent
}

func (s *BaseServer) SetErrorContent(content []byte) {
	if s == nil {
		return
	}
	s.ErrorContent = content
}

func (s *BaseServer) IsGlobalBlackIP(ipPort string) bool {
	return s.runtimeContext().accessPolicy.IsGlobalBlackIP(ipPort)
}

func (s *BaseServer) HasActiveDestinationACL(client *file.Client, task *file.Tunnel) bool {
	return s.runtimeContext().accessPolicy.HasActiveDestinationACL(client, task)
}

func (s *BaseServer) IsClientDestinationAccessDenied(client *file.Client, task *file.Tunnel, addr string) bool {
	return s.runtimeContext().accessPolicy.IsClientDestinationAccessDenied(client, task, addr)
}

func (s *BaseServer) IsClientSourceAccessDenied(client *file.Client, task *file.Tunnel, ipPort string) bool {
	return s.runtimeContext().accessPolicy.IsClientSourceAccessDenied(client, task, ipPort)
}

func (s *BaseServer) IsHostSourceAccessDenied(host *file.Host, ipPort string) bool {
	return s.runtimeContext().accessPolicy.IsHostSourceAccessDenied(host, ipPort)
}

func (s *BaseServer) OpenBridgeLink(clientID int, link *conn.Link, task *file.Tunnel) (net.Conn, error) {
	if s == nil || isNilProxyRuntimeValue(s.linkOpener) {
		return nil, errProxyBridgeUnavailable
	}
	inheritLinkRouteUUID(link, task)
	return s.linkOpener.SendLinkInfo(clientID, link, task)
}

func (s *BaseServer) BridgeIsServer() bool {
	return s != nil && !isNilProxyRuntimeValue(s.serverRole) && s.serverRole.IsServer()
}

func (s *BaseServer) ProcessBridgeClient(c *conn.Conn, tunnelType string) {
	if s == nil || isNilProxyRuntimeValue(s.clientProcessor) || c == nil {
		return
	}
	s.clientProcessor.CliProcess(c, tunnelType)
}

func (s *BaseServer) LinkOpener() BridgeLinkOpener {
	if s == nil {
		return nil
	}
	return s.linkOpener
}

func (s *BaseServer) ServerRole() BridgeServerRole {
	if s == nil {
		return nil
	}
	return s.serverRole
}

type proxyBaseDependencies struct {
	linkOpener        BridgeLinkOpener
	serverRole        BridgeServerRole
	clientProcessor   BridgeClientProcessor
	locker            proxyLocker
	task              *file.Tunnel
	errorContent      []byte
	localProxyAllowed func() bool
}

func newProxyBaseDependencies(base *BaseServer) proxyBaseDependencies {
	if base == nil {
		return proxyBaseDependencies{}
	}
	return proxyBaseDependencies{
		linkOpener:        normalizeProxyBridgeLinkOpener(base.linkOpener),
		serverRole:        normalizeProxyBridgeServerRole(base.serverRole),
		clientProcessor:   normalizeProxyBridgeClientProcessor(base.clientProcessor),
		locker:            &base.Mutex,
		task:              base.Task,
		errorContent:      base.ErrorContent,
		localProxyAllowed: base.LocalProxyAllowed,
	}
}

func (d proxyBaseDependencies) OpenBridgeLink(clientID int, link *conn.Link, task *file.Tunnel) (net.Conn, error) {
	if isNilProxyRuntimeValue(d.linkOpener) {
		return nil, errProxyBridgeUnavailable
	}
	inheritLinkRouteUUID(link, task)
	return d.linkOpener.SendLinkInfo(clientID, link, task)
}

func (d proxyBaseDependencies) ProcessBridgeClient(c *conn.Conn, tunnelType string) {
	if isNilProxyRuntimeValue(d.clientProcessor) || c == nil {
		return
	}
	d.clientProcessor.CliProcess(c, tunnelType)
}

func (d proxyBaseDependencies) BridgeIsServer() bool {
	return !isNilProxyRuntimeValue(d.serverRole) && d.serverRole.IsServer()
}

func (d proxyBaseDependencies) LocalProxyAllowed() bool {
	return d.localProxyAllowed != nil && d.localProxyAllowed()
}

type proxyAuthRuntime struct {
	errorContent []byte
}

func normalizeProxyBridgeLinkOpener(opener BridgeLinkOpener) BridgeLinkOpener {
	if isNilProxyRuntimeValue(opener) {
		return nil
	}
	return opener
}

func normalizeProxyBridgeServerRole(role BridgeServerRole) BridgeServerRole {
	if isNilProxyRuntimeValue(role) {
		return nil
	}
	return role
}

func normalizeProxyBridgeClientProcessor(processor BridgeClientProcessor) BridgeClientProcessor {
	if isNilProxyRuntimeValue(processor) {
		return nil
	}
	return processor
}

func (s *BaseServer) writeConnFail(c net.Conn) {
	proxyAuthRuntime{errorContent: s.CurrentErrorContent()}.WriteConnFail(c)
}

func (s *BaseServer) Auth(r *http.Request, c *conn.Conn, u, p string, multiAccount, userAuth *file.MultiAccount) error {
	return proxyAuthRuntime{errorContent: s.CurrentErrorContent()}.Check(r, c, u, p, multiAccount, userAuth)
}

func (r proxyAuthRuntime) WriteConnFail(c net.Conn) {
	if c == nil {
		return
	}
	_, _ = c.Write([]byte(common.ConnectionFailBytes))
	_, _ = c.Write(r.errorContent)
}

func (r proxyAuthRuntime) Check(req *http.Request, c *conn.Conn, u, p string, multiAccount, userAuth *file.MultiAccount) error {
	if !newProxyAuthPolicy(u, p, multiAccount, userAuth).CheckRequest(req) {
		writeProxyUnauthorized(c)
		return errProxyUnauthorized
	}
	return nil
}

func writeProxyUnauthorized(c *conn.Conn) {
	if c == nil {
		return
	}
	_, _ = c.Write([]byte(common.UnauthorizedBytes))
	_ = c.Close()
}

type proxyFlowRuntime struct {
	locker proxyLocker
	task   *file.Tunnel
}

func (s *BaseServer) FlowAdd(in, out int64) {
	if s == nil {
		return
	}
	proxyFlowRuntime{locker: &s.Mutex, task: s.Task}.AddTaskFlow(in, out)
}

func (s *BaseServer) FlowAddHost(host *file.Host, in, out int64) {
	if s == nil {
		return
	}
	proxyFlowRuntime{locker: &s.Mutex, task: s.Task}.AddHostFlow(host, in, out)
}

func (r proxyFlowRuntime) AddTaskFlow(in, out int64) {
	if r.locker == nil || r.task == nil || r.task.Flow == nil {
		return
	}
	r.locker.Lock()
	defer r.locker.Unlock()
	r.task.Flow.ExportFlow += out
	r.task.Flow.InletFlow += in
}

func (r proxyFlowRuntime) AddHostFlow(host *file.Host, in, out int64) {
	if r.locker == nil || host == nil || host.Flow == nil {
		return
	}
	r.locker.Lock()
	defer r.locker.Unlock()
	host.Flow.ExportFlow += out
	host.Flow.InletFlow += in
}

type proxyServiceContext struct {
	auth      proxyAuthRuntime
	flow      proxyFlowRuntime
	limit     proxyLimitRuntime
	transport proxyTransportRuntime
}

func newProxyServiceContext(base *BaseServer) proxyServiceContext {
	if base == nil {
		return proxyServiceContext{}
	}
	runtime := base.runtimeContext()
	limit := newProxyLimitRuntime(runtime)
	return newProxyServiceContextWith(newProxyBaseDependencies(base), limit, runtime.accessPolicy)
}

func newProxyServiceContextWith(deps proxyBaseDependencies, limit proxyLimitRuntime, policy proxyTransportPolicy) proxyServiceContext {
	if deps.locker == nil && deps.linkOpener == nil && deps.task == nil && deps.errorContent == nil {
		return proxyServiceContext{}
	}
	return proxyServiceContext{
		auth: proxyAuthRuntime{
			errorContent: deps.errorContent,
		},
		flow: proxyFlowRuntime{
			locker: deps.locker,
			task:   deps.task,
		},
		limit:     limit,
		transport: newProxyTransportRuntime(deps, limit, policy),
	}
}

func (s *BaseServer) runtimeContext() proxyRuntimeContext {
	if s != nil {
		return s.sources.resolvedRuntimeRoot()()
	}
	return currentProxyRuntimeRoot()
}

type proxyAuthPolicy struct {
	user       string
	passwd     string
	accountMap map[string]string
	authMap    map[string]string
}

func newProxyAuthPolicy(user, passwd string, multiAccount, userAuth *file.MultiAccount) proxyAuthPolicy {
	return proxyAuthPolicy{
		user:       user,
		passwd:     passwd,
		accountMap: file.GetAccountMap(multiAccount),
		authMap:    file.GetAccountMap(userAuth),
	}
}

func tunnelProxyAuthPolicy(task *file.Tunnel) proxyAuthPolicy {
	if task == nil || task.Client == nil || task.Client.Cnf == nil {
		return proxyAuthPolicy{}
	}
	return newProxyAuthPolicy(task.Client.Cnf.U, task.Client.Cnf.P, task.MultiAccount, task.UserAuth)
}

func hostProxyAuthPolicy(host *file.Host) proxyAuthPolicy {
	if host == nil || host.Client == nil || host.Client.Cnf == nil {
		return proxyAuthPolicy{}
	}
	return newProxyAuthPolicy(host.Client.Cnf.U, host.Client.Cnf.P, host.MultiAccount, host.UserAuth)
}

func (p proxyAuthPolicy) RequiresAuth() bool {
	return p.user != "" || p.passwd != "" || len(p.accountMap) > 0 || len(p.authMap) > 0
}

func (p proxyAuthPolicy) CheckCredentials(user, passwd string) bool {
	return common.CheckAuthWithAccountMap(user, passwd, p.user, p.passwd, p.accountMap, p.authMap)
}

func (p proxyAuthPolicy) CheckRequest(r *http.Request) bool {
	return common.CheckAuth(r, p.user, p.passwd, p.accountMap, p.authMap)
}

func in(target string, strArray []string) bool {
	for _, current := range strArray {
		if current == target {
			return true
		}
	}
	return false
}

func IsGlobalBlackIp(ipPort string) bool {
	if !currentProxyRuntimeRoot().accessPolicy.IsGlobalBlackIP(ipPort) {
		return false
	}
	logGlobalSourceAccessDenied(ipPort)
	return true
}

func logGlobalSourceAccessDenied(ipPort string) {
	ip := common.GetIpByAddr(ipPort)
	if ip == "" {
		ip = ipPort
	}
	logs.Warn("IP address [%s] is blocked by global source access rules", ip)
}

func IsClientBlackIp(client *file.Client, ipPort string) bool {
	if client == nil || client.AllowsSourceAddr(ipPort) {
		return false
	}
	ip := common.GetIpByAddr(ipPort)
	if ip == "" {
		ip = ipPort
	}
	logs.Warn("IP [%s] is blocked for client [%s]", ip, client.VerifyKey)
	return true
}

func allowsDestinationByMode(user *file.User, task *file.Tunnel, addr string, allowDomain bool) (bool, string) {
	if user != nil && user.DestAclMode != file.AclOff {
		if allowDomain {
			if !user.AllowsDestination(addr) {
				return false, "user"
			}
		} else if !user.AllowsDestinationIP(addr) {
			return false, "user"
		}
	}
	if task != nil && task.DestAclMode != file.AclOff {
		if allowDomain {
			if !task.AllowsDestination(addr) {
				return false, "tunnel"
			}
		} else if !task.AllowsDestinationIP(addr) {
			return false, "tunnel"
		}
	}
	return true, ""
}

func HasActiveDestinationACL(client *file.Client, task *file.Tunnel) bool {
	return currentProxyRuntimeRoot().accessPolicy.HasActiveDestinationACL(client, task)
}

func IsClientDestinationAccessDenied(client *file.Client, task *file.Tunnel, addr string) bool {
	return currentProxyRuntimeRoot().accessPolicy.IsClientDestinationAccessDenied(client, task, addr)
}

func IsUserSourceDenied(user *file.User, ipPort string) bool {
	if user == nil || user.AllowsSourceAddr(ipPort) {
		return false
	}
	ip := common.GetIpByAddr(ipPort)
	if ip == "" {
		ip = ipPort
	}
	logs.Warn("IP [%s] is blocked for user [%d]", ip, user.Id)
	return true
}

func IsTunnelSourceDenied(task *file.Tunnel, ipPort string) bool {
	if task == nil || task.AllowsSourceAddr(ipPort) {
		return false
	}
	ip := common.GetIpByAddr(ipPort)
	if ip == "" {
		ip = ipPort
	}
	logs.Warn("IP [%s] is blocked for tunnel [%d]", ip, task.Id)
	return true
}

func IsHostSourceDenied(host *file.Host, ipPort string) bool {
	if host == nil || host.AllowsSourceAddr(ipPort) {
		return false
	}
	ip := common.GetIpByAddr(ipPort)
	if ip == "" {
		ip = ipPort
	}
	logs.Warn("IP [%s] is blocked for host [%d] (%s)", ip, host.Id, host.Host)
	return true
}

func IsHostSourceAccessDenied(host *file.Host, ipPort string) bool {
	return currentProxyRuntimeRoot().accessPolicy.IsHostSourceAccessDenied(host, ipPort)
}

func isClientSourceAccessDenied(client *file.Client, task *file.Tunnel, ipPort string) bool {
	return currentProxyRuntimeRoot().accessPolicy.IsClientSourceAccessDenied(client, task, ipPort)
}

func logDestinationAccessDenied(client *file.Client, task *file.Tunnel, user *file.User, addr, deniedBy string) {
	dest := common.ExtractHost(addr)
	switch deniedBy {
	case "user":
		if user != nil {
			logs.Warn("user dest acl deny: client=%d user=%d dest=%s", client.Id, user.Id, dest)
		}
	case "tunnel":
		if task != nil {
			logs.Warn("tunnel dest acl deny: client=%d task=%d dest=%s", client.Id, task.Id, dest)
		}
	}
}
