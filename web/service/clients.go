package service

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
	"github.com/djylb/nps/lib/servercfg"
	"github.com/skip2/go-qrcode"
)

type ClientService interface {
	List(ListClientsInput) ListClientsResult
	Add(AddClientInput) (ClientMutation, error)
	Ping(id int, remoteAddr string) (int, error)
	Get(id int) (*file.Client, error)
	Edit(EditClientInput) (ClientMutation, error)
	Clear(id int, mode string, isAdmin bool) (ClientMutation, error)
	ChangeStatus(id int, status bool) (ClientMutation, error)
	Delete(id int) (ClientMutation, error)
	BuildQRCode(ClientQRCodeInput) ([]byte, error)
}

type DefaultClientService struct {
	ConfigProvider func() *servercfg.Snapshot
	Repo           ClientRepository
	Runtime        ClientRuntime
	Backend        Backend
}

type ClientVisibility struct {
	IsAdmin         bool
	PrimaryClientID int
	ClientIDs       []int
}

type ListClientsInput struct {
	Offset     int
	Limit      int
	Search     string
	Sort       string
	Order      string
	Host       string
	Visibility ClientVisibility
}

type ListClientsResult struct {
	Rows       []*file.Client
	Total      int
	BridgeType string
	BridgeAddr string
	BridgeIP   string
	BridgePort int
}

type ClientMutation struct {
	ID     int
	Client *file.Client
}

type AddClientInput struct {
	ReservedAdminUsername string
	UserID                int
	OwnerSpecified        bool
	CurrentUserID         int
	ManageUserBinding     bool
	AllowManagerUserIDs   bool
	ManagerUserIDs        []int
	VKey                  string
	Remark                string
	User                  string
	Password              string
	Compress              bool
	Crypt                 bool
	ConfigConnAllow       bool
	RateLimit             int
	MaxConn               int
	MaxTunnelNum          int
	FlowLimit             int64
	TimeLimit             string
	EntryACLMode          int
	EntryACLRules         string
	SourceType            string
	SourcePlatformID      string
	SourceActorID         string
}

type EditClientInput struct {
	ID                      int
	ExpectedRevision        int64
	IsAdmin                 bool
	AllowUserChangeUsername bool
	ReservedAdminUsername   string
	UserID                  int
	OwnerSpecified          bool
	ManageUserBinding       bool
	AllowManagerUserIDs     bool
	ManagerUserIDsSpecified bool
	ManagerUserIDs          []int
	VKey                    string
	Remark                  string
	User                    string
	Password                string
	PasswordProvided        bool
	Compress                bool
	Crypt                   bool
	ConfigConnAllow         bool
	RateLimit               int
	MaxConn                 int
	MaxTunnelNum            int
	FlowLimit               int64
	TimeLimit               string
	ResetFlow               bool
	EntryACLMode            int
	EntryACLRules           string
}

type ClientQRCodeInput struct {
	Text    string
	Account string
	Secret  string
	AppName string
}

type ClientMutationContext struct {
	Principal               Principal
	Authz                   AuthorizationService
	ReservedAdminUsername   string
	AllowUserChangeUsername bool
}

type ClientWriteRequest struct {
	UserID                  int
	OwnerSpecified          bool
	ManagerUserIDsSpecified bool
	ManagerUserIDs          []int
	VKey                    string
	Remark                  string
	User                    string
	Password                string
	PasswordProvided        bool
	Compress                bool
	Crypt                   bool
	ConfigConnAllow         bool
	RateLimit               int
	MaxConn                 int
	MaxTunnelNum            int
	FlowLimit               int64
	TimeLimit               string
	EntryACLMode            int
	EntryACLRules           string
}

type AddClientRequest struct {
	ClientWriteRequest
}

type EditClientRequest struct {
	ID               int
	ExpectedRevision int64
	ClientWriteRequest
	ResetFlow bool
}

type clientScopedResourceRangeRepository interface {
	SupportsRangeTunnelsByClientID() bool
	RangeTunnelsByClientID(int, func(*file.Tunnel) bool)
	SupportsRangeHostsByClientID() bool
	RangeHostsByClientID(int, func(*file.Host) bool)
}

type clientMutationPolicy struct {
	context principalContext
	isAdmin bool
}

type clientIDScope struct {
	set map[int]struct{}
	ids []int
}

func (s DefaultClientService) List(input ListClientsInput) ListClientsResult {
	rows, count := s.repo().ListVisibleClients(input)
	bridge := BestBridge(s.config(), input.Host)
	port, _ := strconv.Atoi(bridge.Port)
	return ListClientsResult{
		Rows:       rows,
		Total:      count,
		BridgeType: bridge.Type,
		BridgeAddr: bridge.Addr,
		BridgeIP:   bridge.IP,
		BridgePort: port,
	}
}

func (s DefaultClientService) Add(input AddClientInput) (ClientMutation, error) {
	id := s.repo().NextClientID()
	entryACLMode, entryACLRules := normalizeEntryACLInput(input.EntryACLMode, input.EntryACLRules)
	client := &file.Client{
		VerifyKey: input.VKey,
		Id:        id,
		Status:    true,
		Remark:    input.Remark,
		Cnf: &file.Config{
			U:        input.User,
			P:        input.Password,
			Compress: input.Compress,
			Crypt:    input.Crypt,
		},
		ConfigConnAllow: input.ConfigConnAllow,
		RateLimit:       input.RateLimit,
		MaxConn:         input.MaxConn,
		MaxTunnelNum:    input.MaxTunnelNum,
		Flow:            &file.Flow{ExportFlow: 0, InletFlow: 0},
		EntryAclMode:    entryACLMode,
		EntryAclRules:   entryACLRules,
		CreateTime:      time.Now().Format("2006-01-02 15:04:05"),
	}
	client.SetFlowLimitBytes(normalizeClientFlowLimit(input.FlowLimit))
	client.SetExpireAt(parseClientExpireAt(input.TimeLimit))
	if user, err := s.resolveManagedUser(input.UserID, input.OwnerSpecified, input.CurrentUserID, input.ManageUserBinding, 0); err != nil {
		return ClientMutation{}, err
	} else if user != nil {
		client.SetOwnerUserID(user.Id)
		client.SetFlowLimitBytes(0)
		client.SetExpireAt(0)
		if err := ensureClientUserCeilings(user, client); err != nil {
			return ClientMutation{}, err
		}
	} else {
		client.SetOwnerUserID(0)
	}
	if input.AllowManagerUserIDs {
		client.ManagerUserIDs = sanitizeManagerUserIDs(input.ManagerUserIDs, client.OwnerID())
	}
	client.TouchMeta(input.SourceType, input.SourcePlatformID, input.SourceActorID)
	if err := s.repo().CreateClient(client); err != nil {
		return ClientMutation{}, err
	}
	return newClientMutationResult(client), nil
}

func (s DefaultClientService) Ping(id int, remoteAddr string) (int, error) {
	client, err := s.repo().GetClient(id)
	if err != nil {
		return 0, mapClientServiceError(err)
	}
	if client == nil {
		return 0, ErrClientNotFound
	}
	if isReservedRuntimeClient(client) {
		return 0, ErrForbidden
	}
	return s.runtime().PingClient(id, remoteAddr), nil
}

func (s DefaultClientService) Get(id int) (*file.Client, error) {
	repo := s.repo()
	client, err := repo.GetClient(id)
	if err != nil {
		return nil, mapClientServiceError(err)
	}
	if client == nil {
		return nil, ErrClientNotFound
	}
	if isReservedRuntimeClient(client) {
		return nil, ErrForbidden
	}
	return ensureDetachedClientSnapshot(repo, client), nil
}

func (s DefaultClientService) Edit(input EditClientInput) (ClientMutation, error) {
	repo := s.repo()
	client, err := repo.GetClient(input.ID)
	if err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	if client == nil {
		return ClientMutation{}, ErrClientNotFound
	}
	if isReservedRuntimeClient(client) {
		return ClientMutation{}, ErrForbidden
	}
	working := ensureDetachedClientSnapshot(repo, client)

	if input.IsAdmin {
		if !repo.VerifyVKey(input.VKey, client.Id) {
			return ClientMutation{}, ErrClientVKeyDuplicate
		}
		working.VerifyKey = input.VKey
		working.SetFlowLimitBytes(normalizeClientFlowLimit(input.FlowLimit))
		working.SetExpireAt(parseClientExpireAt(input.TimeLimit))
		working.RateLimit = input.RateLimit
		working.MaxConn = input.MaxConn
		working.MaxTunnelNum = input.MaxTunnelNum
		if input.ResetFlow {
			working.Flow.ExportFlow = 0
			working.Flow.InletFlow = 0
		}
	}

	working.Remark = input.Remark
	working.Cnf.U = input.User
	if input.PasswordProvided {
		working.Cnf.P = input.Password
	}
	working.Cnf.Compress = input.Compress
	working.Cnf.Crypt = input.Crypt
	if input.ManageUserBinding && input.OwnerSpecified {
		user, err := s.resolveManagedUser(input.UserID, input.OwnerSpecified, 0, input.ManageUserBinding, working.Id)
		if err != nil {
			return ClientMutation{}, err
		}
		if user != nil {
			working.SetOwnerUserID(user.Id)
			working.SetFlowLimitBytes(0)
			working.SetExpireAt(0)
			if err := ensureClientUserCeilings(user, working); err != nil {
				return ClientMutation{}, err
			}
		} else if input.UserID == 0 {
			working.SetOwnerUserID(0)
		}
	} else if working.OwnerID() > 0 {
		// User-bound clients inherit login and lifecycle from their owner user.
		working.SetFlowLimitBytes(0)
		working.SetExpireAt(0)
		user, err := resolveClientOwnerUser(repo, working)
		if err != nil {
			return ClientMutation{}, mapUserServiceError(err)
		}
		if user == nil {
			return ClientMutation{}, ErrUserNotFound
		}
		if working.OwnerUser() != user {
			user = ensureDetachedUserSnapshot(repo, user)
			working.BindOwnerUser(user)
		}
		if err := ensureClientUserCeilings(user, working); err != nil {
			return ClientMutation{}, err
		}
	}
	working.ClearLegacyWebLoginImport()
	if input.AllowManagerUserIDs && input.ManagerUserIDsSpecified {
		working.ManagerUserIDs = sanitizeManagerUserIDs(input.ManagerUserIDs, working.OwnerID())
	}
	working.ConfigConnAllow = input.ConfigConnAllow
	working.EntryAclMode, working.EntryAclRules = normalizeEntryACLInput(input.EntryACLMode, input.EntryACLRules)
	working.TouchMeta("", "", "")
	working.ExpectedRevision = input.ExpectedRevision
	if err := repo.SaveClient(working); err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	return newClientMutationResult(working), nil
}

func (s DefaultClientService) Clear(id int, mode string, isAdmin bool) (ClientMutation, error) {
	mode = normalizeClientStatusName(mode)
	if !isAdmin || mode == "" {
		return ClientMutation{}, ErrClientModifyFailed
	}
	client, err := s.repo().GetClient(id)
	if err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	if client == nil {
		return ClientMutation{}, ErrClientNotFound
	}
	if isReservedRuntimeClient(client) {
		return ClientMutation{}, ErrForbidden
	}
	client, err = clearClientStatusByIDWithRepo(s.repo(), id, mode)
	if err != nil {
		return ClientMutation{}, err
	}
	return newClientMutationResult(client), nil
}

func (s DefaultClientService) ChangeStatus(id int, status bool) (ClientMutation, error) {
	repo := s.repo()
	client, err := repo.GetClient(id)
	if err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	if client == nil {
		return ClientMutation{}, ErrClientNotFound
	}
	if isReservedRuntimeClient(client) {
		return ClientMutation{}, ErrForbidden
	}
	working := ensureDetachedClientSnapshot(repo, client)
	working.Status = status
	working.TouchMeta("", "", "")
	if err := repo.SaveClient(working); err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	if !working.Status {
		s.runtime().DisconnectClient(working.Id)
		working.IsConnect = false
	}
	return newClientMutationResult(working), nil
}

func (s DefaultClientService) Delete(id int) (ClientMutation, error) {
	repo := s.repo()
	client, err := repo.GetClient(id)
	if err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	if client == nil {
		return ClientMutation{}, ErrClientNotFound
	}
	if isReservedRuntimeClient(client) {
		return ClientMutation{}, ErrForbidden
	}
	working := ensureDetachedClientSnapshot(repo, client)
	if err := repo.DeleteClient(id); err != nil {
		return ClientMutation{}, mapClientServiceError(err)
	}
	s.runtime().DisconnectClient(id)
	s.runtime().DeleteClientResources(id)
	working.IsConnect = false
	working.NowConn = 0
	return newClientMutationResult(working), nil
}

func (DefaultClientService) BuildQRCode(input ClientQRCodeInput) ([]byte, error) {
	text := input.Text
	if text != "" {
		if decoded, err := url.QueryUnescape(text); err == nil {
			text = decoded
		}
	} else if input.Account != "" && input.Secret != "" {
		text = crypt.BuildTotpUri(input.AppName, input.Account, input.Secret)
	}
	if text == "" {
		return nil, ErrClientQRCodeTextRequired
	}
	return qrcode.Encode(text, qrcode.Medium, 256)
}

func BuildAddClientInput(ctx ClientMutationContext, request AddClientRequest) AddClientInput {
	policy := resolveClientMutationPolicy(ctx)
	context := policy.context
	input := AddClientInput{
		ReservedAdminUsername: ctx.ReservedAdminUsername,
		UserID:                request.UserID,
		OwnerSpecified:        request.OwnerSpecified,
		ManageUserBinding:     policy.isAdmin,
		AllowManagerUserIDs:   policy.isAdmin,
		ManagerUserIDs:        append([]int(nil), request.ManagerUserIDs...),
		VKey:                  request.VKey,
		Remark:                request.Remark,
		User:                  request.User,
		Password:              request.Password,
		Compress:              request.Compress,
		Crypt:                 request.Crypt,
		ConfigConnAllow:       request.ConfigConnAllow,
		RateLimit:             request.RateLimit,
		MaxConn:               request.MaxConn,
		MaxTunnelNum:          request.MaxTunnelNum,
		FlowLimit:             request.FlowLimit,
		TimeLimit:             request.TimeLimit,
		EntryACLMode:          request.EntryACLMode,
		EntryACLRules:         request.EntryACLRules,
		SourceType:            context.SourceType,
		SourcePlatformID:      context.PlatformID,
		SourceActorID:         context.PlatformActorID,
	}
	switch context.Kind {
	case "platform_user":
		input.CurrentUserID = context.ServiceUserID
		input.ManageUserBinding = false
		input.AllowManagerUserIDs = false
	case "platform_admin":
		if policy.isAdmin {
			if !request.OwnerSpecified {
				input.CurrentUserID = context.ServiceUserID
			}
		} else {
			input.CurrentUserID = context.ServiceUserID
			input.ManageUserBinding = false
			input.AllowManagerUserIDs = false
		}
	case "user":
		if !policy.isAdmin && context.UserID > 0 {
			input.CurrentUserID = context.UserID
			input.ManageUserBinding = false
			input.AllowManagerUserIDs = false
		}
	}
	return input
}

func BuildEditClientInput(ctx ClientMutationContext, request EditClientRequest) EditClientInput {
	policy := resolveClientMutationPolicy(ctx)
	return EditClientInput{
		ID:                      request.ID,
		ExpectedRevision:        request.ExpectedRevision,
		IsAdmin:                 policy.isAdmin,
		AllowUserChangeUsername: ctx.AllowUserChangeUsername,
		ReservedAdminUsername:   ctx.ReservedAdminUsername,
		UserID:                  request.UserID,
		OwnerSpecified:          request.OwnerSpecified,
		ManageUserBinding:       policy.isAdmin,
		AllowManagerUserIDs:     policy.isAdmin,
		ManagerUserIDsSpecified: request.ManagerUserIDsSpecified,
		ManagerUserIDs:          append([]int(nil), request.ManagerUserIDs...),
		VKey:                    request.VKey,
		Remark:                  request.Remark,
		User:                    request.User,
		Password:                request.Password,
		PasswordProvided:        request.PasswordProvided,
		Compress:                request.Compress,
		Crypt:                   request.Crypt,
		ConfigConnAllow:         request.ConfigConnAllow,
		RateLimit:               request.RateLimit,
		MaxConn:                 request.MaxConn,
		MaxTunnelNum:            request.MaxTunnelNum,
		FlowLimit:               request.FlowLimit,
		TimeLimit:               request.TimeLimit,
		ResetFlow:               request.ResetFlow,
		EntryACLMode:            request.EntryACLMode,
		EntryACLRules:           request.EntryACLRules,
	}
}

func newClientMutationResult(client *file.Client) ClientMutation {
	if client == nil {
		return ClientMutation{}
	}
	return ClientMutation{
		ID:     client.Id,
		Client: cloneClientSnapshotForMutation(client),
	}
}

func resolveClientMutationPolicy(ctx ClientMutationContext) clientMutationPolicy {
	context := resolvePrincipalContextWithAuthorization(ctx.Authz, ctx.Principal)
	return clientMutationPolicy{
		context: context,
		isAdmin: context.Principal.Authenticated && permissionSetAllows(context.Principal.Permissions, PermissionManagementAdmin),
	}
}

func ClearClientStatusByID(id int, name string) error {
	_, err := clearClientStatusByIDWithRepo(DefaultBackend().repo(), id, name)
	return err
}

func ClearClientStatus(client *file.Client, name string) {
	applyClientStatusMutation(client, name)
}

func ClearClientStatusCopy(client *file.Client, name string) *file.Client {
	if client == nil {
		return nil
	}
	working := cloneClientSnapshotForMutation(client)
	applyClientStatusMutation(working, name)
	return working
}

func clearClientStatusByIDWithRepo(repo ClientRepository, id int, name string) (*file.Client, error) {
	mode := normalizeClientStatusName(name)
	if mode == "" {
		return nil, ErrClientModifyFailed
	}
	if id == 0 {
		return nil, clearAllClientStatusByMode(repo, mode)
	}

	client, err := repo.GetClient(id)
	if err != nil {
		return nil, mapClientServiceError(err)
	}
	if client == nil {
		return nil, ErrClientNotFound
	}
	if mode == "flow" {
		var cleared *file.Client
		err := withDeferredPersistence(repo, func() error {
			var clearErr error
			cleared, clearErr = clearClientStatusCommit(repo, client, mode, true)
			return clearErr
		})
		return cleared, err
	}
	return clearClientStatusCommit(repo, client, mode, true)
}

func clearAllClientStatusByMode(repo ClientRepository, mode string) error {
	if repo == nil {
		return nil
	}
	clients := collectClientStatusTargets(repo)
	if len(clients) == 0 {
		return nil
	}
	return withDeferredPersistence(repo, func() error {
		if mode == "flow" {
			if err := clearClientRelatedFlowsForClients(repo, clients); err != nil {
				return err
			}
		}
		for _, client := range clients {
			if _, err := clearClientStatusCommit(repo, client, mode, false); err != nil {
				return err
			}
		}
		return nil
	})
}

func collectClientStatusTargets(repo ClientRepository) []*file.Client {
	if repo == nil {
		return nil
	}
	clients := make([]*file.Client, 0)
	repo.RangeClients(func(client *file.Client) bool {
		if client != nil {
			clients = append(clients, client)
		}
		return true
	})
	return clients
}

func clearClientStatusCommit(repo ClientRepository, client *file.Client, mode string, clearRelatedFlows bool) (*file.Client, error) {
	if client == nil {
		return nil, nil
	}
	working := ensureDetachedClientSnapshot(repo, client)
	if !applyClientStatusMutation(working, mode) {
		return nil, ErrClientModifyFailed
	}
	if clearRelatedFlows && mode == "flow" {
		if err := clearClientRelatedFlows(repo, working.Id); err != nil {
			return nil, err
		}
	}
	working.TouchMeta("", "", "")
	if err := repo.SaveClient(working); err != nil {
		return nil, mapClientServiceError(err)
	}
	return cloneClientSnapshotForMutation(working), nil
}

func clearClientRelatedFlows(repo ClientRepository, clientID int) error {
	if repo == nil || clientID <= 0 {
		return nil
	}
	var firstErr error
	rangeClientHosts(repo, clientID, func(host *file.Host) bool {
		working := ensureDetachedHostMutation(repo, host)
		working.ResetServiceTraffic()
		working.TouchMeta()
		if err := repo.SaveHost(working, ""); err != nil {
			if errors.Is(err, file.ErrHostNotFound) {
				return true
			}
			firstErr = err
			return false
		}
		return true
	})
	if firstErr != nil {
		return firstErr
	}
	rangeClientTunnels(repo, clientID, func(tunnel *file.Tunnel) bool {
		working := ensureDetachedTunnelMutation(repo, tunnel)
		working.ResetServiceTraffic()
		working.TouchMeta()
		if err := repo.SaveTunnel(working); err != nil {
			if errors.Is(err, file.ErrTaskNotFound) {
				return true
			}
			firstErr = err
			return false
		}
		return true
	})
	return firstErr
}

func clearClientRelatedFlowsForClients(repo ClientRepository, clients []*file.Client) error {
	if repo == nil || len(clients) == 0 {
		return nil
	}
	clientSet := make(map[int]struct{}, len(clients))
	for _, client := range clients {
		if client == nil || client.Id <= 0 {
			continue
		}
		clientSet[client.Id] = struct{}{}
	}
	if len(clientSet) == 0 {
		return nil
	}
	var firstErr error
	repo.RangeHosts(func(host *file.Host) bool {
		if host == nil || host.Client == nil {
			return true
		}
		if _, ok := clientSet[host.Client.Id]; !ok {
			return true
		}
		working := ensureDetachedHostMutation(repo, host)
		working.ResetServiceTraffic()
		working.TouchMeta()
		if err := repo.SaveHost(working, ""); err != nil {
			if errors.Is(err, file.ErrHostNotFound) {
				return true
			}
			firstErr = err
			return false
		}
		return true
	})
	if firstErr != nil {
		return firstErr
	}
	repo.RangeTunnels(func(tunnel *file.Tunnel) bool {
		if tunnel == nil || tunnel.Client == nil {
			return true
		}
		if _, ok := clientSet[tunnel.Client.Id]; !ok {
			return true
		}
		working := ensureDetachedTunnelMutation(repo, tunnel)
		working.ResetServiceTraffic()
		working.TouchMeta()
		if err := repo.SaveTunnel(working); err != nil {
			if errors.Is(err, file.ErrTaskNotFound) {
				return true
			}
			firstErr = err
			return false
		}
		return true
	})
	return firstErr
}

func rangeClientHosts(repo ClientRepository, clientID int, fn func(*file.Host) bool) {
	if repo == nil || fn == nil || clientID <= 0 {
		return
	}
	if scopedRepo, ok := repo.(clientScopedResourceRangeRepository); ok && scopedRepo.SupportsRangeHostsByClientID() {
		scopedRepo.RangeHostsByClientID(clientID, fn)
		return
	}
	repo.RangeHosts(func(host *file.Host) bool {
		if host == nil || host.Client == nil || host.Client.Id != clientID {
			return true
		}
		return fn(host)
	})
}

func rangeClientTunnels(repo ClientRepository, clientID int, fn func(*file.Tunnel) bool) {
	if repo == nil || fn == nil || clientID <= 0 {
		return
	}
	if scopedRepo, ok := repo.(clientScopedResourceRangeRepository); ok && scopedRepo.SupportsRangeTunnelsByClientID() {
		scopedRepo.RangeTunnelsByClientID(clientID, fn)
		return
	}
	repo.RangeTunnels(func(tunnel *file.Tunnel) bool {
		if tunnel == nil || tunnel.Client == nil || tunnel.Client.Id != clientID {
			return true
		}
		return fn(tunnel)
	})
}

func applyClientStatusMutation(client *file.Client, name string) bool {
	if client == nil {
		return false
	}
	mode := normalizeClientStatusName(name)
	if mode == "" {
		return false
	}
	switch mode {
	case "flow":
		client.ResetTraffic()
	case "flow_limit":
		client.SetFlowLimitBytes(0)
	case "time_limit":
		client.SetExpireAt(0)
	case "rate_limit":
		client.RateLimit = 0
	case "conn_limit":
		client.MaxConn = 0
	case "tunnel_limit":
		client.MaxTunnelNum = 0
	}
	if mode == "rate_limit" || client.Rate != nil {
		syncClientRateLimiter(client)
	}
	return true
}

func normalizeClientStatusName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "flow", "flow_limit", "time_limit", "rate_limit", "conn_limit", "tunnel_limit":
		return strings.ToLower(strings.TrimSpace(name))
	default:
		return ""
	}
}

func syncClientRateLimiter(client *file.Client) {
	if client == nil {
		return
	}
	var limit int64
	if client.RateLimit > 0 {
		limit = int64(client.RateLimit) * 1024
	}
	if client.Rate == nil {
		client.Rate = rate.NewRate(limit)
		client.Rate.Start()
		return
	}
	if client.Rate.Limit() != limit {
		client.Rate.ResetLimit(limit)
		return
	}
	client.Rate.Start()
}

func visibleClientScope(visibility ClientVisibility) clientIDScope {
	capacity := len(visibility.ClientIDs)
	if visibility.PrimaryClientID > 0 {
		capacity++
	}
	if capacity == 0 {
		return clientIDScope{}
	}
	scope := clientIDScope{
		set: make(map[int]struct{}, capacity),
		ids: make([]int, 0, capacity),
	}
	scope.add(visibility.PrimaryClientID)
	for _, clientID := range visibility.ClientIDs {
		scope.add(clientID)
	}
	return scope
}

func (s *clientIDScope) add(clientID int) {
	if s == nil || clientID <= 0 {
		return
	}
	if _, ok := s.set[clientID]; ok {
		return
	}
	s.set[clientID] = struct{}{}
	s.ids = append(s.ids, clientID)
}

func (s clientIDScope) has(clientID int) bool {
	if clientID <= 0 {
		return false
	}
	_, ok := s.set[clientID]
	return ok
}

func (s clientIDScope) firstID() int {
	if len(s.ids) == 0 {
		return 0
	}
	return s.ids[0]
}

func (s DefaultClientService) config() *servercfg.Snapshot {
	return servercfg.ResolveProvider(s.ConfigProvider)
}

func (s DefaultClientService) repo() ClientRepository {
	if !isNilServiceValue(s.Repo) {
		return s.Repo
	}
	if !isNilServiceValue(s.Backend.Repository) {
		return s.Backend.Repository
	}
	return DefaultBackend().Repository
}

func (s DefaultClientService) runtime() ClientRuntime {
	if !isNilServiceValue(s.Runtime) {
		return s.Runtime
	}
	if !isNilServiceValue(s.Backend.Runtime) {
		return s.Backend.Runtime
	}
	return DefaultBackend().Runtime
}

func (s DefaultClientService) resolveManagedUser(requestedUserID int, ownerSpecified bool, currentUserID int, manageBinding bool, currentClientID int) (*file.User, error) {
	switch {
	case currentUserID > 0:
		requestedUserID = currentUserID
	case !manageBinding || !ownerSpecified:
		return nil, nil
	}
	if requestedUserID <= 0 {
		return nil, nil
	}
	user, err := s.repo().GetUser(requestedUserID)
	if err != nil {
		return nil, mapUserServiceError(err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	user.EnsureTotalFlow()
	if err := s.ensureUserCanAttachClient(user, currentClientID); err != nil {
		return nil, err
	}
	return user, nil
}

func (s DefaultClientService) ensureUserCanAttachClient(user *file.User, currentClientID int) error {
	if user == nil || user.Id <= 0 || user.MaxClients <= 0 {
		return nil
	}
	count, err := countOwnedClientsByUser(s.repo(), user.Id, currentClientID, user.MaxClients)
	if err != nil {
		return err
	}
	if count >= user.MaxClients {
		return ErrClientLimitExceeded
	}
	return nil
}

func parseClientExpireAt(value string) int64 {
	expireAt := common.GetTimeNoErrByStr(strings.TrimSpace(value))
	if expireAt.IsZero() {
		return 0
	}
	return expireAt.Unix()
}

func normalizeClientFlowLimit(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func sanitizeManagerUserIDs(userIDs []int, ownerUserID int) []int {
	normalized := make([]int, 0, len(userIDs))
	visitor := newUniqueClientIDVisitor(len(userIDs), func(userID int) bool {
		if userID == ownerUserID {
			return true
		}
		normalized = append(normalized, userID)
		return true
	})
	for _, userID := range userIDs {
		visitor.visit(userID)
	}
	return normalized
}

func isReservedRuntimeClient(client *file.Client) bool {
	return client != nil && client.NoDisplay
}

func ensureClientUserCeilings(user *file.User, client *file.Client) error {
	if user == nil || client == nil {
		return nil
	}
	if user.RateLimit > 0 && client.RateLimit > user.RateLimit {
		return ErrClientRateLimitExceeded
	}
	if user.MaxConnections > 0 && client.MaxConn > user.MaxConnections {
		return ErrClientConnLimitExceeded
	}
	return nil
}
