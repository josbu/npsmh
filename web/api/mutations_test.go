package api

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

type nodeMutationTestContext struct {
	actor       *Actor
	values      map[string]string
	rawBody     []byte
	status      int
	jsonPayload interface{}
}

func (c *nodeMutationTestContext) BaseContext() context.Context { return context.Background() }
func (c *nodeMutationTestContext) String(key string) string {
	if c == nil || c.values == nil {
		return ""
	}
	return c.values[key]
}
func (c *nodeMutationTestContext) LookupString(key string) (string, bool) {
	if c == nil || c.values == nil {
		return "", false
	}
	value, ok := c.values[key]
	return value, ok
}
func (c *nodeMutationTestContext) Int(key string, def ...int) int {
	if value, ok := c.LookupString(key); ok {
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	if len(def) > 0 {
		return def[0]
	}
	return 0
}
func (c *nodeMutationTestContext) Bool(string, ...bool) bool { return false }
func (c *nodeMutationTestContext) Method() string            { return "" }
func (c *nodeMutationTestContext) Host() string              { return "" }
func (c *nodeMutationTestContext) RemoteAddr() string        { return "" }
func (c *nodeMutationTestContext) ClientIP() string          { return "" }
func (c *nodeMutationTestContext) RequestHeader(string) string {
	return ""
}
func (c *nodeMutationTestContext) RawBody() []byte {
	if c == nil {
		return nil
	}
	return append([]byte(nil), c.rawBody...)
}
func (c *nodeMutationTestContext) SessionValue(string) interface{}     { return nil }
func (c *nodeMutationTestContext) SetSessionValue(string, interface{}) {}
func (c *nodeMutationTestContext) DeleteSessionValue(string)           {}
func (c *nodeMutationTestContext) SetParam(key, value string) {
	if c.values == nil {
		c.values = map[string]string{}
	}
	c.values[key] = value
}
func (c *nodeMutationTestContext) RespondJSON(status int, payload interface{}) {
	c.status = status
	c.jsonPayload = payload
}
func (c *nodeMutationTestContext) RespondString(int, string)        {}
func (c *nodeMutationTestContext) RespondData(int, string, []byte)  {}
func (c *nodeMutationTestContext) Redirect(int, string)             {}
func (c *nodeMutationTestContext) SetResponseHeader(string, string) {}
func (c *nodeMutationTestContext) IsWritten() bool                  { return c.status != 0 }
func (c *nodeMutationTestContext) Actor() *Actor                    { return c.actor }
func (c *nodeMutationTestContext) SetActor(actor *Actor)            { c.actor = actor }
func (c *nodeMutationTestContext) Metadata() RequestMetadata        { return RequestMetadata{} }

type stubNodeMutationAuthorizationService struct {
	normalizePrincipal func(webservice.Principal) webservice.Principal
	clientScope        func(webservice.Principal) webservice.ClientScope
	permissions        func(webservice.Principal) []string
	allows             func(webservice.Principal, string) bool
	resolveClient      func(webservice.Principal, int) (int, error)
	requirePermission  func(webservice.Principal, string) error
	requireAdmin       func(webservice.Principal) error
	requireClient      func(webservice.Principal, int) error
	requireTunnel      func(webservice.Principal, int) error
	requireHost        func(webservice.Principal, int) error
}

func (s stubNodeMutationAuthorizationService) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	if s.normalizePrincipal != nil {
		return s.normalizePrincipal(principal)
	}
	return principal
}

func (s stubNodeMutationAuthorizationService) ClientScope(principal webservice.Principal) webservice.ClientScope {
	if s.clientScope != nil {
		return s.clientScope(principal)
	}
	return webservice.ClientScope{}
}

func (s stubNodeMutationAuthorizationService) Permissions(principal webservice.Principal) []string {
	if s.permissions != nil {
		return s.permissions(principal)
	}
	return append([]string(nil), principal.Permissions...)
}

func (s stubNodeMutationAuthorizationService) Allows(principal webservice.Principal, permission string) bool {
	if s.allows != nil {
		return s.allows(principal, permission)
	}
	return true
}

func (s stubNodeMutationAuthorizationService) ResolveClient(principal webservice.Principal, requestedClientID int) (int, error) {
	if s.resolveClient != nil {
		return s.resolveClient(principal, requestedClientID)
	}
	return requestedClientID, nil
}

func (s stubNodeMutationAuthorizationService) RequirePermission(principal webservice.Principal, permission string) error {
	if s.requirePermission != nil {
		return s.requirePermission(principal, permission)
	}
	return nil
}

func (s stubNodeMutationAuthorizationService) RequireAdmin(principal webservice.Principal) error {
	if s.requireAdmin != nil {
		return s.requireAdmin(principal)
	}
	return nil
}

func (s stubNodeMutationAuthorizationService) RequireClient(principal webservice.Principal, id int) error {
	if s.requireClient != nil {
		return s.requireClient(principal, id)
	}
	return nil
}

func (s stubNodeMutationAuthorizationService) RequireTunnel(principal webservice.Principal, id int) error {
	if s.requireTunnel != nil {
		return s.requireTunnel(principal, id)
	}
	return nil
}

func (s stubNodeMutationAuthorizationService) RequireHost(principal webservice.Principal, id int) error {
	if s.requireHost != nil {
		return s.requireHost(principal, id)
	}
	return nil
}

type stubNodeMutationClientService struct {
	add            func(webservice.AddClientInput) (int, error)
	get            func(int) (*file.Client, error)
	edit           func(webservice.EditClientInput) error
	clear          func(int, string, bool) error
	set            func(int, bool) error
	delete         func(int) error
	deleteMutation func(int) (webservice.ClientMutation, error)
	ping           func(int, string) (int, error)
}

func (s stubNodeMutationClientService) List(webservice.ListClientsInput) webservice.ListClientsResult {
	return webservice.ListClientsResult{}
}
func (s stubNodeMutationClientService) Add(input webservice.AddClientInput) (webservice.ClientMutation, error) {
	if s.add != nil {
		id, err := s.add(input)
		if err != nil {
			return webservice.ClientMutation{}, err
		}
		return s.clientMutation(id), nil
	}
	return webservice.ClientMutation{}, nil
}
func (s stubNodeMutationClientService) Ping(id int, remoteAddr string) (int, error) {
	if s.ping != nil {
		return s.ping(id, remoteAddr)
	}
	return 0, nil
}
func (s stubNodeMutationClientService) Get(id int) (*file.Client, error) {
	if s.get != nil {
		return s.get(id)
	}
	return nil, webservice.ErrClientNotFound
}
func (s stubNodeMutationClientService) Edit(input webservice.EditClientInput) (webservice.ClientMutation, error) {
	if s.edit != nil {
		if err := s.edit(input); err != nil {
			return webservice.ClientMutation{}, err
		}
	}
	return s.clientMutation(input.ID), nil
}
func (s stubNodeMutationClientService) Clear(id int, mode string, isAdmin bool) (webservice.ClientMutation, error) {
	if s.clear != nil {
		if err := s.clear(id, mode, isAdmin); err != nil {
			return webservice.ClientMutation{}, err
		}
	}
	return s.clientMutation(id), nil
}
func (s stubNodeMutationClientService) ChangeStatus(id int, status bool) (webservice.ClientMutation, error) {
	if s.set != nil {
		if err := s.set(id, status); err != nil {
			return webservice.ClientMutation{}, err
		}
	}
	return s.clientMutation(id), nil
}
func (s stubNodeMutationClientService) Delete(id int) (webservice.ClientMutation, error) {
	if s.deleteMutation != nil {
		return s.deleteMutation(id)
	}
	if s.delete != nil {
		if err := s.delete(id); err != nil {
			return webservice.ClientMutation{}, err
		}
	}
	return s.clientMutation(id), nil
}
func (s stubNodeMutationClientService) BuildQRCode(webservice.ClientQRCodeInput) ([]byte, error) {
	return nil, nil
}

func (s stubNodeMutationClientService) clientMutation(id int) webservice.ClientMutation {
	client, _ := s.Get(id)
	return webservice.ClientMutation{ID: id, Client: client}
}

type stubNodeMutationUserService struct {
	get            func(int) (*file.User, error)
	add            func(webservice.AddUserInput) (int, error)
	edit           func(webservice.EditUserInput) error
	editMutation   func(webservice.EditUserInput) (webservice.UserMutation, error)
	set            func(int, bool) error
	delete         func(int) error
	deleteMutation func(int) (webservice.UserMutation, error)
}

func (stubNodeMutationUserService) List(webservice.ListUsersInput) webservice.ListUsersResult {
	return webservice.ListUsersResult{}
}
func (s stubNodeMutationUserService) Get(id int) (*file.User, error) {
	if s.get != nil {
		return s.get(id)
	}
	return nil, webservice.ErrUserNotFound
}
func (s stubNodeMutationUserService) Add(input webservice.AddUserInput) (webservice.UserMutation, error) {
	if s.add != nil {
		id, err := s.add(input)
		if err != nil {
			return webservice.UserMutation{}, err
		}
		return s.userMutation(id), nil
	}
	return webservice.UserMutation{}, nil
}
func (s stubNodeMutationUserService) Edit(input webservice.EditUserInput) (webservice.UserMutation, error) {
	if s.editMutation != nil {
		return s.editMutation(input)
	}
	if s.edit != nil {
		if err := s.edit(input); err != nil {
			return webservice.UserMutation{}, err
		}
	}
	return s.userMutation(input.ID), nil
}
func (s stubNodeMutationUserService) ChangeStatus(id int, status bool) (webservice.UserMutation, error) {
	if s.set != nil {
		if err := s.set(id, status); err != nil {
			return webservice.UserMutation{}, err
		}
	}
	return s.userMutation(id), nil
}
func (s stubNodeMutationUserService) Delete(id int) (webservice.UserMutation, error) {
	if s.deleteMutation != nil {
		return s.deleteMutation(id)
	}
	if s.delete != nil {
		if err := s.delete(id); err != nil {
			return webservice.UserMutation{}, err
		}
	}
	return s.userMutation(id), nil
}

func (s stubNodeMutationUserService) userMutation(id int) webservice.UserMutation {
	user, _ := s.Get(id)
	return webservice.UserMutation{ID: id, User: user}
}

type stubNodeMutationGlobalService struct {
	banList  func() []webservice.LoginBanRecord
	unban    func(string) bool
	unbanAll func()
	clean    func()
}

func (stubNodeMutationGlobalService) Save(webservice.SaveGlobalInput) error { return nil }
func (stubNodeMutationGlobalService) Get() *file.Glob                       { return nil }
func (s stubNodeMutationGlobalService) BanList() []webservice.LoginBanRecord {
	if s.banList != nil {
		return s.banList()
	}
	return nil
}
func (s stubNodeMutationGlobalService) Unban(key string) bool {
	if s.unban != nil {
		return s.unban(key)
	}
	return false
}
func (s stubNodeMutationGlobalService) UnbanAll() {
	if s.unbanAll != nil {
		s.unbanAll()
	}
}
func (s stubNodeMutationGlobalService) CleanBans() {
	if s.clean != nil {
		s.clean()
	}
}

type stubNodeMutationIndexService struct {
	getHost              func(int) (*file.Host, error)
	getTunnel            func(int) (*file.Tunnel, error)
	listHosts            func(webservice.HostListInput) ([]*file.Host, int)
	addHost              func(webservice.AddHostInput) (webservice.HostMutation, error)
	addTunnel            func(webservice.AddTunnelInput) (webservice.TunnelMutation, error)
	editHost             func(webservice.EditHostInput) (webservice.HostMutation, error)
	editTunnel           func(webservice.EditTunnelInput) (webservice.TunnelMutation, error)
	deleteHost           func(int) error
	deleteTunnel         func(int) error
	deleteHostMutation   func(int) (webservice.HostMutation, error)
	deleteTunnelMutation func(int) (webservice.TunnelMutation, error)
	startHost            func(int, string) error
	stopHost             func(int, string) error
	clearHost            func(int, string) error
	startTunnel          func(int, string) error
	stopTunnel           func(int, string) error
	clearTunnel          func(int, string) error
}

func (s stubNodeMutationIndexService) DashboardData(bool) map[string]interface{} { return nil }
func (s stubNodeMutationIndexService) ListTunnels(webservice.TunnelListInput) ([]*file.Tunnel, int) {
	return nil, 0
}
func (s stubNodeMutationIndexService) AddTunnel(input webservice.AddTunnelInput) (webservice.TunnelMutation, error) {
	if s.addTunnel != nil {
		return s.addTunnel(input)
	}
	return webservice.TunnelMutation{}, nil
}
func (s stubNodeMutationIndexService) GetTunnel(id int) (*file.Tunnel, error) {
	if s.getTunnel != nil {
		return s.getTunnel(id)
	}
	return nil, webservice.ErrTunnelNotFound
}
func (s stubNodeMutationIndexService) EditTunnel(input webservice.EditTunnelInput) (webservice.TunnelMutation, error) {
	if s.editTunnel != nil {
		return s.editTunnel(input)
	}
	return webservice.TunnelMutation{}, nil
}
func (s stubNodeMutationIndexService) StopTunnel(id int, mode string) (webservice.TunnelMutation, error) {
	if s.stopTunnel != nil {
		if err := s.stopTunnel(id, mode); err != nil {
			return webservice.TunnelMutation{}, err
		}
	}
	return s.tunnelMutation(id), nil
}
func (s stubNodeMutationIndexService) DeleteTunnel(id int) (webservice.TunnelMutation, error) {
	if s.deleteTunnelMutation != nil {
		return s.deleteTunnelMutation(id)
	}
	if s.deleteTunnel != nil {
		if err := s.deleteTunnel(id); err != nil {
			return webservice.TunnelMutation{}, err
		}
	}
	return s.tunnelMutation(id), nil
}
func (s stubNodeMutationIndexService) StartTunnel(id int, mode string) (webservice.TunnelMutation, error) {
	if s.startTunnel != nil {
		if err := s.startTunnel(id, mode); err != nil {
			return webservice.TunnelMutation{}, err
		}
	}
	return s.tunnelMutation(id), nil
}
func (s stubNodeMutationIndexService) ClearTunnel(id int, mode string) (webservice.TunnelMutation, error) {
	if s.clearTunnel != nil {
		if err := s.clearTunnel(id, mode); err != nil {
			return webservice.TunnelMutation{}, err
		}
	}
	return s.tunnelMutation(id), nil
}
func (s stubNodeMutationIndexService) ListHosts(input webservice.HostListInput) ([]*file.Host, int) {
	if s.listHosts != nil {
		return s.listHosts(input)
	}
	return nil, 0
}
func (s stubNodeMutationIndexService) GetHost(id int) (*file.Host, error) {
	if s.getHost != nil {
		return s.getHost(id)
	}
	return nil, webservice.ErrHostNotFound
}
func (s stubNodeMutationIndexService) DeleteHost(id int) (webservice.HostMutation, error) {
	if s.deleteHostMutation != nil {
		return s.deleteHostMutation(id)
	}
	if s.deleteHost != nil {
		if err := s.deleteHost(id); err != nil {
			return webservice.HostMutation{}, err
		}
	}
	return s.hostMutation(id), nil
}
func (s stubNodeMutationIndexService) StartHost(id int, mode string) (webservice.HostMutation, error) {
	if s.startHost != nil {
		if err := s.startHost(id, mode); err != nil {
			return webservice.HostMutation{}, err
		}
	}
	return s.hostMutation(id), nil
}
func (s stubNodeMutationIndexService) StopHost(id int, mode string) (webservice.HostMutation, error) {
	if s.stopHost != nil {
		if err := s.stopHost(id, mode); err != nil {
			return webservice.HostMutation{}, err
		}
	}
	return s.hostMutation(id), nil
}
func (s stubNodeMutationIndexService) ClearHost(id int, mode string) (webservice.HostMutation, error) {
	if s.clearHost != nil {
		if err := s.clearHost(id, mode); err != nil {
			return webservice.HostMutation{}, err
		}
	}
	return s.hostMutation(id), nil
}
func (s stubNodeMutationIndexService) AddHost(input webservice.AddHostInput) (webservice.HostMutation, error) {
	if s.addHost != nil {
		return s.addHost(input)
	}
	return webservice.HostMutation{}, nil
}
func (s stubNodeMutationIndexService) EditHost(input webservice.EditHostInput) (webservice.HostMutation, error) {
	if s.editHost != nil {
		return s.editHost(input)
	}
	return webservice.HostMutation{}, nil
}

func (s stubNodeMutationIndexService) tunnelMutation(id int) webservice.TunnelMutation {
	tunnel, _ := s.GetTunnel(id)
	result := webservice.TunnelMutation{ID: id, Tunnel: tunnel}
	if tunnel != nil {
		result.Mode = tunnel.Mode
		if tunnel.Client != nil {
			result.ClientID = tunnel.Client.Id
		}
	}
	return result
}

func (s stubNodeMutationIndexService) hostMutation(id int) webservice.HostMutation {
	host, _ := s.GetHost(id)
	result := webservice.HostMutation{ID: id, Host: host}
	if host != nil && host.Client != nil {
		result.ClientID = host.Client.Id
	}
	return result
}

func TestAuthorizeNodeProtectedActionPropagatesResolveClientError(t *testing.T) {
	lookupErr := errors.New("resolve client failed")
	services := webservice.New()
	services.Authz = stubNodeMutationAuthorizationService{
		resolveClient: func(principal webservice.Principal, requestedClientID int) (int, error) {
			if requestedClientID != 1 {
				t.Fatalf("ResolveClient() requestedClientID = %d, want 1", requestedClientID)
			}
			return 0, lookupErr
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{actor: UserActor("demo", []int{1})}

	_, err := authorizeNodeProtectedAction(app, ctx, webservice.ProtectedActionAccessInput{
		Principal:         PrincipalFromActor(ctx.Actor()),
		Permission:        webservice.PermissionTunnelsUpdate,
		ClientScope:       true,
		RequestedClientID: 1,
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("authorizeNodeProtectedAction() error = %v, want %v", err, lookupErr)
	}
}

func TestAuthorizeNodeProtectedActionPropagatesRequireTunnelErrorWithoutResourceReload(t *testing.T) {
	lookupErr := errors.New("require tunnel failed")
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(int) (*file.Client, error) {
			t.Fatal("authorizeNodeProtectedAction() should not reload client state")
			return nil, nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getTunnel: func(int) (*file.Tunnel, error) {
			t.Fatal("authorizeNodeProtectedAction() should not reload tunnel state")
			return nil, nil
		},
	}
	services.Authz = stubNodeMutationAuthorizationService{
		requireTunnel: func(principal webservice.Principal, id int) error {
			if id != 12 {
				t.Fatalf("RequireTunnel() id = %d, want 12", id)
			}
			return lookupErr
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{actor: UserActor("demo", []int{1})}

	_, err := authorizeNodeProtectedAction(app, ctx, webservice.ProtectedActionAccessInput{
		Principal:  PrincipalFromActor(ctx.Actor()),
		Permission: webservice.PermissionTunnelsUpdate,
		Ownership:  "tunnel",
		ResourceID: 12,
	})
	if !errors.Is(err, lookupErr) {
		t.Fatalf("authorizeNodeProtectedAction() error = %v, want %v", err, lookupErr)
	}
}

func TestAuthorizeNodeProtectedActionUsesPermissionResolverFallback(t *testing.T) {
	services := webservice.New()
	services.Permissions = mappedPermissionResolver{
		normalizePrincipal: func(principal webservice.Principal) webservice.Principal {
			if principal.Username == "resolver-user" {
				principal.Authenticated = true
				principal.Kind = "user"
				principal.ClientIDs = []int{77}
				principal.Permissions = append(principal.Permissions, webservice.PermissionClientsRead)
			}
			return principal
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{actor: &Actor{Username: "resolver-user"}}

	clientID, err := authorizeNodeProtectedAction(app, ctx, webservice.ProtectedActionAccessInput{
		Principal:         PrincipalFromActor(ctx.Actor()),
		Permission:        webservice.PermissionClientsRead,
		ClientScope:       true,
		RequestedClientID: 77,
	})
	if err != nil {
		t.Fatalf("authorizeNodeProtectedAction() error = %v, want nil", err)
	}
	if clientID != 77 {
		t.Fatalf("authorizeNodeProtectedAction() clientID = %d, want 77", clientID)
	}
}

func TestNodeUpdateHostRejectsClientReassignmentOutsideScope(t *testing.T) {
	editCalled := false
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(int) (*file.Client, error) {
			t.Fatal("NodeUpdateHost() should reject unauthorized reassignment without reloading requested client")
			return nil, nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getHost: func(id int) (*file.Host, error) {
			return &file.Host{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
			}, nil
		},
		editHost: func(webservice.EditHostInput) (webservice.HostMutation, error) {
			editCalled = true
			return webservice.HostMutation{}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		values:  map[string]string{"id": "11"},
		rawBody: []byte(`{"client_id":2}`),
	}

	app.NodeUpdateHost(ctx)

	if editCalled {
		t.Fatal("NodeUpdateHost() should reject unauthorized client reassignment before edit")
	}
	if ctx.status != 403 {
		t.Fatalf("NodeUpdateHost() status = %d, want 403", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUpdateHost() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Message != webservice.ErrForbidden.Error() {
		t.Fatalf("NodeUpdateHost() message = %q, want %q", response.Error.Message, webservice.ErrForbidden.Error())
	}
}

func TestNodeUpdateTunnelRejectsClientReassignmentOutsideScope(t *testing.T) {
	editCalled := false
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(int) (*file.Client, error) {
			t.Fatal("NodeUpdateTunnel() should reject unauthorized reassignment without reloading requested client")
			return nil, nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getTunnel: func(id int) (*file.Tunnel, error) {
			return &file.Tunnel{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
			}, nil
		},
		editTunnel: func(webservice.EditTunnelInput) (webservice.TunnelMutation, error) {
			editCalled = true
			return webservice.TunnelMutation{}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		values:  map[string]string{"id": "12"},
		rawBody: []byte(`{"client_id":2}`),
	}

	app.NodeUpdateTunnel(ctx)

	if editCalled {
		t.Fatal("NodeUpdateTunnel() should reject unauthorized client reassignment before edit")
	}
	if ctx.status != 403 {
		t.Fatalf("NodeUpdateTunnel() status = %d, want 403", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUpdateTunnel() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Message != webservice.ErrForbidden.Error() {
		t.Fatalf("NodeUpdateTunnel() message = %q, want %q", response.Error.Message, webservice.ErrForbidden.Error())
	}
}

func TestNodeStartTunnelReturnsNotFoundForMissingTunnel(t *testing.T) {
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		startTunnel: func(id int, mode string) error {
			if id != 12 {
				t.Fatalf("StartTunnel(%d), want 12", id)
			}
			if mode != "" {
				t.Fatalf("StartTunnel() mode = %q, want empty", mode)
			}
			return webservice.ErrTunnelNotFound
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:  AdminActor("root"),
		values: map[string]string{"id": "12"},
	}

	app.NodeStartTunnel(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeStartTunnel() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeStartTunnel() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "tunnel_not_found" {
		t.Fatalf("NodeStartTunnel() code = %q, want tunnel_not_found", response.Error.Code)
	}
}

func TestNodeClearHostReturnsNotFoundForMissingHost(t *testing.T) {
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		clearHost: func(id int, mode string) error {
			if id != 21 {
				t.Fatalf("ClearHost(%d), want 21", id)
			}
			if mode != "auto_ssl" {
				t.Fatalf("ClearHost() mode = %q, want auto_ssl", mode)
			}
			return webservice.ErrHostNotFound
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("root"),
		values:  map[string]string{"id": "21"},
		rawBody: []byte(`{"mode":"auto_ssl"}`),
	}

	app.NodeClearHost(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeClearHost() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClearHost() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "host_not_found" {
		t.Fatalf("NodeClearHost() code = %q, want host_not_found", response.Error.Code)
	}
}

func TestNodeCreateTunnelUsesResolvedScopedClientWhenRequestOmitsClientID(t *testing.T) {
	var addInput webservice.AddTunnelInput
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		addTunnel: func(input webservice.AddTunnelInput) (webservice.TunnelMutation, error) {
			addInput = input
			return webservice.TunnelMutation{ID: 21, ClientID: input.ClientID, Mode: input.Mode}, nil
		},
		getTunnel: func(id int) (*file.Tunnel, error) {
			return &file.Tunnel{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
				Mode:   "tcp",
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		rawBody: []byte(`{"mode":"tcp","port":1234,"target":"127.0.0.1:8080"}`),
	}

	app.NodeCreateTunnel(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeCreateTunnel() status = %d, want 200", ctx.status)
	}
	if addInput.ClientID != 1 {
		t.Fatalf("NodeCreateTunnel() client_id = %d, want 1", addInput.ClientID)
	}
}

func TestNodeCreateTunnelRequiresClientIDWhenScopeHasMultipleClients(t *testing.T) {
	addCalled := false
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		addTunnel: func(input webservice.AddTunnelInput) (webservice.TunnelMutation, error) {
			addCalled = true
			return webservice.TunnelMutation{}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1, 2}),
		rawBody: []byte(`{"mode":"tcp","port":1234,"target":"127.0.0.1:8080"}`),
	}

	app.NodeCreateTunnel(ctx)

	if addCalled {
		t.Fatal("NodeCreateTunnel() should reject ambiguous scoped client before add")
	}
	if ctx.status != 400 {
		t.Fatalf("NodeCreateTunnel() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeCreateTunnel() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "client_id_required" {
		t.Fatalf("NodeCreateTunnel() code = %q, want client_id_required", response.Error.Code)
	}
}

func TestNodeUpdateTunnelLeavesClientIDUnsetWhenRequestOmitsClientID(t *testing.T) {
	var editInput webservice.EditTunnelInput
	getCalls := 0
	services := webservice.New()
	services.Authz = stubNodeMutationAuthorizationService{
		requireTunnel: func(principal webservice.Principal, id int) error {
			if id != 12 {
				t.Fatalf("RequireTunnel() id = %d, want 12", id)
			}
			return nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getTunnel: func(id int) (*file.Tunnel, error) {
			getCalls++
			t.Fatal("NodeUpdateTunnel() should not reload tunnel state when authz already owns tunnel access checks")
			return nil, nil
		},
		editTunnel: func(input webservice.EditTunnelInput) (webservice.TunnelMutation, error) {
			editInput = input
			return webservice.TunnelMutation{
				ID:       input.ID,
				ClientID: 1,
				Mode:     "tcp",
				Tunnel: &file.Tunnel{
					Id:     input.ID,
					Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}},
					Flow:   &file.Flow{},
					Target: &file.Target{},
					Mode:   "tcp",
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		values:  map[string]string{"id": "12"},
		rawBody: []byte(`{"mode":"tcp","port":1234,"target":"127.0.0.1:8080"}`),
	}

	app.NodeUpdateTunnel(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateTunnel() status = %d, want 200", ctx.status)
	}
	if getCalls != 0 {
		t.Fatalf("NodeUpdateTunnel() getTunnel calls = %d, want 0 with authz-owned ownership checks", getCalls)
	}
	if editInput.ClientID != 0 {
		t.Fatalf("NodeUpdateTunnel() client_id = %d, want omitted 0 for service-side fallback", editInput.ClientID)
	}
}

func TestNodeCreateHostUsesResolvedScopedClientWhenRequestOmitsClientID(t *testing.T) {
	var addInput webservice.AddHostInput
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		addHost: func(input webservice.AddHostInput) (webservice.HostMutation, error) {
			addInput = input
			return webservice.HostMutation{ID: 31, ClientID: input.ClientID}, nil
		},
		getHost: func(id int) (*file.Host, error) {
			return &file.Host{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		rawBody: []byte(`{"host":"demo.example.com","target":"127.0.0.1:8080"}`),
	}

	app.NodeCreateHost(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeCreateHost() status = %d, want 200", ctx.status)
	}
	if addInput.ClientID != 1 {
		t.Fatalf("NodeCreateHost() client_id = %d, want 1", addInput.ClientID)
	}
}

func TestNodeCreateHostRequiresClientIDWhenScopeHasMultipleClients(t *testing.T) {
	addCalled := false
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		addHost: func(input webservice.AddHostInput) (webservice.HostMutation, error) {
			addCalled = true
			return webservice.HostMutation{}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1, 2}),
		rawBody: []byte(`{"host":"demo.example.com","target":"127.0.0.1:8080"}`),
	}

	app.NodeCreateHost(ctx)

	if addCalled {
		t.Fatal("NodeCreateHost() should reject ambiguous scoped client before add")
	}
	if ctx.status != 400 {
		t.Fatalf("NodeCreateHost() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeCreateHost() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "client_id_required" {
		t.Fatalf("NodeCreateHost() code = %q, want client_id_required", response.Error.Code)
	}
}

func TestNodeUpdateHostLeavesClientIDUnsetWhenRequestOmitsClientID(t *testing.T) {
	var editInput webservice.EditHostInput
	getCalls := 0
	services := webservice.New()
	services.Authz = stubNodeMutationAuthorizationService{
		requireHost: func(principal webservice.Principal, id int) error {
			if id != 11 {
				t.Fatalf("RequireHost() id = %d, want 11", id)
			}
			return nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getHost: func(id int) (*file.Host, error) {
			getCalls++
			t.Fatal("NodeUpdateHost() should not reload host state when authz already owns host access checks")
			return nil, nil
		},
		editHost: func(input webservice.EditHostInput) (webservice.HostMutation, error) {
			editInput = input
			return webservice.HostMutation{
				ID:       input.ID,
				ClientID: 1,
				Host: &file.Host{
					Id:     input.ID,
					Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}},
					Flow:   &file.Flow{},
					Target: &file.Target{},
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		values:  map[string]string{"id": "11"},
		rawBody: []byte(`{"host":"demo.example.com","target":"127.0.0.1:8080"}`),
	}

	app.NodeUpdateHost(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateHost() status = %d, want 200", ctx.status)
	}
	if getCalls != 0 {
		t.Fatalf("NodeUpdateHost() getHost calls = %d, want 0 with authz-owned ownership checks", getCalls)
	}
	if editInput.ClientID != 0 {
		t.Fatalf("NodeUpdateHost() client_id = %d, want omitted 0 for service-side fallback", editInput.ClientID)
	}
}

func TestNodeUpdateHostSkipsSyncHostLookupWhenCertSyncDisabled(t *testing.T) {
	var (
		editInput       webservice.EditHostInput
		listHostsCalled bool
	)
	services := webservice.New()
	services.Authz = stubNodeMutationAuthorizationService{
		requireHost: func(principal webservice.Principal, id int) error {
			if id != 11 {
				t.Fatalf("RequireHost() id = %d, want 11", id)
			}
			return nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getHost: func(id int) (*file.Host, error) {
			return &file.Host{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
			}, nil
		},
		listHosts: func(webservice.HostListInput) ([]*file.Host, int) {
			listHostsCalled = true
			return nil, 0
		},
		editHost: func(input webservice.EditHostInput) (webservice.HostMutation, error) {
			editInput = input
			return webservice.HostMutation{ID: input.ID, ClientID: input.ClientID}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   UserActor("demo", []int{1}),
		values:  map[string]string{"id": "11"},
		rawBody: []byte(`{"host":"demo.example.com","target":"127.0.0.1:8080","sync_cert_to_matching_hosts":false}`),
	}

	app.NodeUpdateHost(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateHost() status = %d, want 200", ctx.status)
	}
	if listHostsCalled {
		t.Fatal("NodeUpdateHost() should not list hosts when cert sync is disabled")
	}
	if editInput.SyncCertToMatchingHosts {
		t.Fatalf("NodeUpdateHost() sync_cert_to_matching_hosts = true, want false")
	}
	if len(editInput.SyncHostIDs) != 0 {
		t.Fatalf("NodeUpdateHost() sync_host_ids = %v, want none when cert sync is disabled", editInput.SyncHostIDs)
	}
}

func TestNodeUpdateHostBuildsSyncHostIDsOnlyWhenCertSyncEnabled(t *testing.T) {
	var (
		editInput       webservice.EditHostInput
		listHostsCalled int
	)
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id}, nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getHost: func(id int) (*file.Host, error) {
			return &file.Host{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
			}, nil
		},
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			listHostsCalled++
			return []*file.Host{
				{Id: 31, Client: &file.Client{Id: 1}, Flow: &file.Flow{}, Target: &file.Target{}},
				{Id: 32, Client: &file.Client{Id: 2}, Flow: &file.Flow{}, Target: &file.Target{}},
			}, 2
		},
		editHost: func(input webservice.EditHostInput) (webservice.HostMutation, error) {
			editInput = input
			return webservice.HostMutation{ID: input.ID, ClientID: input.ClientID}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "11"},
		rawBody: []byte(`{"host":"demo.example.com","target":"127.0.0.1:8080","sync_cert_to_matching_hosts":true}`),
	}

	app.NodeUpdateHost(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateHost() status = %d, want 200", ctx.status)
	}
	if listHostsCalled != 1 {
		t.Fatalf("NodeUpdateHost() listed hosts %d times, want 1", listHostsCalled)
	}
	if !editInput.SyncCertToMatchingHosts {
		t.Fatalf("NodeUpdateHost() sync_cert_to_matching_hosts = false, want true")
	}
	if len(editInput.SyncHostIDs) != 2 || editInput.SyncHostIDs[0] != 31 || editInput.SyncHostIDs[1] != 32 {
		t.Fatalf("NodeUpdateHost() sync_host_ids = %v, want [31 32]", editInput.SyncHostIDs)
	}
}

func TestNodeUpdateHostDeduplicatesSyncHostIDs(t *testing.T) {
	var editInput webservice.EditHostInput
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id}, nil
		},
	}
	services.Index = stubNodeMutationIndexService{
		getHost: func(id int) (*file.Host, error) {
			return &file.Host{
				Id:     id,
				Client: &file.Client{Id: 1},
				Flow:   &file.Flow{},
				Target: &file.Target{},
			}, nil
		},
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			return []*file.Host{
				{Id: 31, Client: &file.Client{Id: 1}, Flow: &file.Flow{}, Target: &file.Target{}},
				nil,
				{Id: 31, Client: &file.Client{Id: 1}, Flow: &file.Flow{}, Target: &file.Target{}},
				{Id: 0, Client: &file.Client{Id: 1}, Flow: &file.Flow{}, Target: &file.Target{}},
				{Id: 32, Client: &file.Client{Id: 2}, Flow: &file.Flow{}, Target: &file.Target{}},
				{Id: 32, Client: &file.Client{Id: 2}, Flow: &file.Flow{}, Target: &file.Target{}},
			}, 6
		},
		editHost: func(input webservice.EditHostInput) (webservice.HostMutation, error) {
			editInput = input
			return webservice.HostMutation{ID: input.ID, ClientID: input.ClientID}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "11"},
		rawBody: []byte(`{"host":"demo.example.com","target":"127.0.0.1:8080","sync_cert_to_matching_hosts":true}`),
	}

	app.NodeUpdateHost(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateHost() status = %d, want 200", ctx.status)
	}
	if len(editInput.SyncHostIDs) != 2 || editInput.SyncHostIDs[0] != 31 || editInput.SyncHostIDs[1] != 32 {
		t.Fatalf("NodeUpdateHost() sync_host_ids = %v, want deduplicated [31 32]", editInput.SyncHostIDs)
	}
}

func TestNodeUpdateClientRejectsLegacyAliasField(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	ctx := &nodeMutationTestContext{
		values:  map[string]string{"id": "1"},
		rawBody: []byte(`{"vkey":"legacy-client"}`),
	}

	app.NodeUpdateClient(ctx)

	if ctx.status != 400 {
		t.Fatalf("NodeUpdateClient() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUpdateClient() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "invalid_json_body" {
		t.Fatalf("NodeUpdateClient() code = %q, want invalid_json_body", response.Error.Code)
	}
}

func TestNodeUpdateClientPreservesExplicitZeroOwnerUserID(t *testing.T) {
	var editInput webservice.EditClientInput
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id, UserId: 7, VerifyKey: "vk-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}, nil
		},
		edit: func(input webservice.EditClientInput) error {
			editInput = input
			return nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"owner_user_id":0,"verify_key":"vk-7"}`),
	}

	app.NodeUpdateClient(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateClient() status = %d, want 200", ctx.status)
	}
	if !editInput.OwnerSpecified || editInput.UserID != 0 {
		t.Fatalf("NodeUpdateClient() edit input = %+v, want explicit owner clear", editInput)
	}
}

func TestNodeUpdateClientOmitsPasswordWhenBodyLeavesItAbsent(t *testing.T) {
	var editInput webservice.EditClientInput
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id, VerifyKey: "vk-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}, nil
		},
		edit: func(input webservice.EditClientInput) error {
			editInput = input
			return nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"verify_key":"vk-7","remark":"updated"}`),
	}

	app.NodeUpdateClient(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateClient() status = %d, want 200", ctx.status)
	}
	if editInput.PasswordProvided || editInput.Password != "" {
		t.Fatalf("NodeUpdateClient() edit input = %+v, want password omitted", editInput)
	}
}

func TestNodeUpdateClientPreservesExplicitEmptyPasswordField(t *testing.T) {
	var editInput webservice.EditClientInput
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id, VerifyKey: "vk-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}, nil
		},
		edit: func(input webservice.EditClientInput) error {
			editInput = input
			return nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"verify_key":"vk-7","password":""}`),
	}

	app.NodeUpdateClient(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateClient() status = %d, want 200", ctx.status)
	}
	if !editInput.PasswordProvided || editInput.Password != "" {
		t.Fatalf("NodeUpdateClient() edit input = %+v, want explicit empty password preserved", editInput)
	}
}

func TestNodeClearClientRequiresJSONBody(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	ctx := &nodeMutationTestContext{
		values: map[string]string{
			"id":   "1",
			"mode": "flow",
		},
	}

	app.NodeClearClient(ctx)

	if ctx.status != 400 {
		t.Fatalf("NodeClearClient() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClearClient() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "json_body_required" {
		t.Fatalf("NodeClearClient() code = %q, want json_body_required", response.Error.Code)
	}
}

func TestNodeClearClientReturnsNotFoundForMissingClient(t *testing.T) {
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		clear: func(id int, mode string, isAdmin bool) error {
			if id != 7 || mode != "flow" || !isAdmin {
				t.Fatalf("Clear(%d, %q, %v), want 7, flow, true", id, mode, isAdmin)
			}
			return webservice.ErrClientNotFound
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"mode":"flow"}`),
	}

	app.NodeClearClient(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeClearClient() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClearClient() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "client_not_found" {
		t.Fatalf("NodeClearClient() code = %q, want client_not_found", response.Error.Code)
	}
}

func TestNodeClearClientReturnsLeanMutationPayloadAndMeta(t *testing.T) {
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		clear: func(id int, mode string, isAdmin bool) error {
			if id != 7 || mode != "flow" || !isAdmin {
				t.Fatalf("Clear(%d, %q, %v), want 7, flow, true", id, mode, isAdmin)
			}
			return nil
		},
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id, VerifyKey: "vk-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"mode":"flow"}`),
	}

	app.NodeClearClient(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeClearClient() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeClearClient() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	if response.Meta.GeneratedAt <= 0 {
		t.Fatalf("NodeClearClient() meta.generated_at = %d, want > 0", response.Meta.GeneratedAt)
	}
	if response.Meta.ConfigEpoch == "" {
		t.Fatal("NodeClearClient() meta.config_epoch = empty, want runtime epoch")
	}
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("json.Marshal(data) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}
	if payload["resource"] != "client" || payload["action"] != "clear" || payload["id"] != float64(7) {
		t.Fatalf("NodeClearClient() data = %#v, want resource/action/id", payload)
	}
	if _, exists := payload["generated_at"]; exists {
		t.Fatalf("NodeClearClient() data should not include generated_at: %#v", payload)
	}
	if _, exists := payload["config_epoch"]; exists {
		t.Fatalf("NodeClearClient() data should not include config_epoch: %#v", payload)
	}
}

func TestNodePingClientReturnsLeanPayloadAndMeta(t *testing.T) {
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(id int) (*file.Client, error) {
			return &file.Client{Id: id, VerifyKey: "vk-7", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}}, nil
		},
		ping: func(id int, remoteAddr string) (int, error) {
			if id != 7 {
				t.Fatalf("Ping(%d, %q), want id 7", id, remoteAddr)
			}
			return 37, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:  AdminActor("admin"),
		values: map[string]string{"id": "7"},
	}

	app.NodePingClient(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodePingClient() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodePingClient() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	if response.Meta.GeneratedAt <= 0 {
		t.Fatalf("NodePingClient() meta.generated_at = %d, want > 0", response.Meta.GeneratedAt)
	}
	if response.Meta.ConfigEpoch == "" {
		t.Fatal("NodePingClient() meta.config_epoch = empty, want runtime epoch")
	}
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("json.Marshal(data) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("NodePingClient() data = %#v, want only id/rtt", payload)
	}
	if payload["id"] != float64(7) || payload["rtt"] != float64(37) {
		t.Fatalf("NodePingClient() data = %#v, want id=7 rtt=37", payload)
	}
	if _, exists := payload["generated_at"]; exists {
		t.Fatalf("NodePingClient() data should not include generated_at: %#v", payload)
	}
	if _, exists := payload["config_epoch"]; exists {
		t.Fatalf("NodePingClient() data should not include config_epoch: %#v", payload)
	}
}

func TestNodeUnbanRequiresJSONBody(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	ctx := &nodeMutationTestContext{
		values: map[string]string{
			"key": "1.2.3.4",
		},
	}

	app.NodeUnban(ctx)

	if ctx.status != 400 {
		t.Fatalf("NodeUnban() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUnban() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "json_body_required" {
		t.Fatalf("NodeUnban() code = %q, want json_body_required", response.Error.Code)
	}
}

func TestNodeUnbanRequiresKeyField(t *testing.T) {
	app := NewWithOptions(nil, Options{})
	ctx := &nodeMutationTestContext{
		rawBody: []byte(`{}`),
	}

	app.NodeUnban(ctx)

	if ctx.status != 400 {
		t.Fatalf("NodeUnban() status = %d, want 400", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUnban() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "key_required" {
		t.Fatalf("NodeUnban() code = %q, want key_required", response.Error.Code)
	}
}

func TestNodeUnbanReturnsLeanPayloadAndMeta(t *testing.T) {
	services := webservice.New()
	services.Globals = stubNodeMutationGlobalService{
		unban: func(key string) bool {
			return key == "198.51.100.7"
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:   AdminActor("admin"),
		rawBody: []byte(`{"key":"198.51.100.7"}`),
	}

	app.NodeUnban(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUnban() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeUnban() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	if response.Meta.GeneratedAt <= 0 {
		t.Fatalf("NodeUnban() meta.generated_at = %d, want > 0", response.Meta.GeneratedAt)
	}
	if response.Meta.ConfigEpoch == "" {
		t.Fatal("NodeUnban() meta.config_epoch = empty, want runtime epoch")
	}
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("json.Marshal(data) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("NodeUnban() data = %#v, want only action/key", payload)
	}
	if payload["action"] != "unban" || payload["key"] != "198.51.100.7" {
		t.Fatalf("NodeUnban() data = %#v, want action=unban key=198.51.100.7", payload)
	}
	if _, exists := payload["generated_at"]; exists {
		t.Fatalf("NodeUnban() data should not include generated_at: %#v", payload)
	}
	if _, exists := payload["config_epoch"]; exists {
		t.Fatalf("NodeUnban() data should not include config_epoch: %#v", payload)
	}
}

func TestNodeBanCleanReturnsRemovedKeysAndTotal(t *testing.T) {
	records := []webservice.LoginBanRecord{
		{Key: "198.51.100.7", IsBanned: true, BanType: "ip"},
		{Key: "alice", IsBanned: true, BanType: "username"},
		{Key: "bob", IsBanned: false, BanType: "username"},
	}
	services := webservice.New()
	services.Globals = stubNodeMutationGlobalService{
		banList: func() []webservice.LoginBanRecord {
			return append([]webservice.LoginBanRecord(nil), records...)
		},
		clean: func() {
			records = []webservice.LoginBanRecord{
				{Key: "bob", IsBanned: false, BanType: "username"},
			}
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{actor: AdminActor("admin")}

	app.NodeBanClean(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeBanClean() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeBanClean() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	if response.Meta.GeneratedAt <= 0 {
		t.Fatalf("NodeBanClean() meta.generated_at = %d, want > 0", response.Meta.GeneratedAt)
	}
	if response.Meta.ConfigEpoch == "" {
		t.Fatal("NodeBanClean() meta.config_epoch = empty, want runtime epoch")
	}
	data, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("json.Marshal(data) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(data) error = %v", err)
	}
	if payload["action"] != "clean" {
		t.Fatalf("NodeBanClean() action = %#v, want clean", payload["action"])
	}
	if payload["total"] != float64(1) {
		t.Fatalf("NodeBanClean() total = %#v, want 1", payload["total"])
	}
	removed, ok := payload["removed_keys"].([]any)
	if !ok {
		t.Fatalf("NodeBanClean() removed_keys = %#v, want array", payload["removed_keys"])
	}
	if len(removed) != 2 || removed[0] != "198.51.100.7" || removed[1] != "alice" {
		t.Fatalf("NodeBanClean() removed_keys = %#v, want [198.51.100.7 alice]", removed)
	}
	if _, exists := payload["generated_at"]; exists {
		t.Fatalf("NodeBanClean() data should not include generated_at: %#v", payload)
	}
	if _, exists := payload["config_epoch"]; exists {
		t.Fatalf("NodeBanClean() data should not include config_epoch: %#v", payload)
	}
}

func TestNodeCreateUserPreservesExplicitEmptyPasswordForTOTPOnlyUser(t *testing.T) {
	var addInput webservice.AddUserInput
	services := webservice.New()
	services.Users = stubNodeMutationUserService{
		add: func(input webservice.AddUserInput) (int, error) {
			addInput = input
			return 7, nil
		},
		get: func(id int) (*file.User, error) {
			return &file.User{Id: id, Username: "tenant", TOTPSecret: "SECRET123", TotalFlow: &file.Flow{}}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		rawBody: []byte(`{"username":"tenant","password":"","totp_secret":"SECRET123","status":true}`),
	}

	app.NodeCreateUser(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeCreateUser() status = %d, want 200", ctx.status)
	}
	if addInput.Password != "" || addInput.TOTPSecret != "SECRET123" {
		t.Fatalf("NodeCreateUser() add input = %+v, want empty password and totp secret preserved", addInput)
	}
}

func TestNodeUpdateUserOmitsCredentialFieldsWhenBodyLeavesThemAbsent(t *testing.T) {
	var editInput webservice.EditUserInput
	services := webservice.New()
	services.Users = stubNodeMutationUserService{
		get: func(int) (*file.User, error) {
			t.Fatal("NodeUpdateUser() should not reload current user when optional fields are omitted")
			return nil, nil
		},
		editMutation: func(input webservice.EditUserInput) (webservice.UserMutation, error) {
			editInput = input
			return webservice.UserMutation{
				ID: 7,
				User: &file.User{
					Id:        7,
					Username:  "tenant",
					Status:    1,
					TotalFlow: &file.Flow{},
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"username":"tenant"}`),
	}

	app.NodeUpdateUser(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateUser() status = %d, want 200", ctx.status)
	}
	if editInput.PasswordProvided || editInput.TOTPSecretProvided || editInput.StatusProvided {
		t.Fatalf("NodeUpdateUser() edit input = %+v, want optional fields omitted", editInput)
	}
}

func TestNodeUpdateUserPreservesExplicitEmptyPasswordField(t *testing.T) {
	var editInput webservice.EditUserInput
	services := webservice.New()
	services.Users = stubNodeMutationUserService{
		get: func(id int) (*file.User, error) {
			return &file.User{Id: id, Username: "tenant", Status: 1, TotalFlow: &file.Flow{}}, nil
		},
		edit: func(input webservice.EditUserInput) error {
			editInput = input
			return nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		values:  map[string]string{"id": "7"},
		rawBody: []byte(`{"username":"tenant","password":"","status":true}`),
	}

	app.NodeUpdateUser(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeUpdateUser() status = %d, want 200", ctx.status)
	}
	if !editInput.PasswordProvided || editInput.Password != "" || !editInput.StatusProvided || !editInput.Status {
		t.Fatalf("NodeUpdateUser() edit input = %+v, want explicit empty password and status to be preserved", editInput)
	}
}

func TestNodeDeleteClientUsesDeleteMutationSnapshotWithoutReload(t *testing.T) {
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(int) (*file.Client, error) {
			t.Fatal("NodeDeleteClient() should not reload client state after delete")
			return nil, nil
		},
		deleteMutation: func(id int) (webservice.ClientMutation, error) {
			return webservice.ClientMutation{
				ID: id,
				Client: &file.Client{
					Id:        id,
					VerifyKey: "vk-delete",
					Remark:    "deleted-snapshot",
					Cnf:       &file.Config{},
					Flow:      &file.Flow{},
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:  AdminActor("root"),
		values: map[string]string{"id": "9"},
	}

	app.NodeDeleteClient(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeDeleteClient() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeDeleteClient() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	payload, ok := response.Data.(nodeResourceMutationPayload)
	if !ok {
		t.Fatalf("NodeDeleteClient() data type = %T, want nodeResourceMutationPayload", response.Data)
	}
	item, ok := payload.Item.(nodeClientResourcePayload)
	if !ok {
		t.Fatalf("NodeDeleteClient() item type = %T, want nodeClientResourcePayload", payload.Item)
	}
	if item.Remark != "deleted-snapshot" {
		t.Fatalf("NodeDeleteClient() item.remark = %q, want deleted-snapshot", item.Remark)
	}
}

func TestNodeDeleteTunnelUsesDeleteMutationSnapshotWithoutReload(t *testing.T) {
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		getTunnel: func(int) (*file.Tunnel, error) {
			t.Fatal("NodeDeleteTunnel() should not reload tunnel state after delete")
			return nil, nil
		},
		deleteTunnelMutation: func(id int) (webservice.TunnelMutation, error) {
			return webservice.TunnelMutation{
				ID: id,
				Tunnel: &file.Tunnel{
					Id:     id,
					Remark: "deleted-tunnel",
					Mode:   "tcp",
					Flow:   &file.Flow{},
					Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}},
					Target: &file.Target{TargetStr: "127.0.0.1:8080"},
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:  AdminActor("root"),
		values: map[string]string{"id": "12"},
	}

	app.NodeDeleteTunnel(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeDeleteTunnel() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeDeleteTunnel() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	payload, ok := response.Data.(nodeResourceMutationPayload)
	if !ok {
		t.Fatalf("NodeDeleteTunnel() data type = %T, want nodeResourceMutationPayload", response.Data)
	}
	item, ok := payload.Item.(nodeTunnelResourcePayload)
	if !ok {
		t.Fatalf("NodeDeleteTunnel() item type = %T, want nodeTunnelResourcePayload", payload.Item)
	}
	if item.Remark != "deleted-tunnel" {
		t.Fatalf("NodeDeleteTunnel() item.remark = %q, want deleted-tunnel", item.Remark)
	}
}

func TestNodeDeleteHostUsesDeleteMutationSnapshotWithoutReload(t *testing.T) {
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		getHost: func(int) (*file.Host, error) {
			t.Fatal("NodeDeleteHost() should not reload host state after delete")
			return nil, nil
		},
		deleteHostMutation: func(id int) (webservice.HostMutation, error) {
			return webservice.HostMutation{
				ID: id,
				Host: &file.Host{
					Id:     id,
					Host:   "deleted.example.com",
					Remark: "deleted-host",
					Flow:   &file.Flow{},
					Client: &file.Client{Id: 1, Cnf: &file.Config{}, Flow: &file.Flow{}},
					Target: &file.Target{TargetStr: "127.0.0.1:8080"},
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		actor:  AdminActor("root"),
		values: map[string]string{"id": "21"},
	}

	app.NodeDeleteHost(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeDeleteHost() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeDeleteHost() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	payload, ok := response.Data.(nodeResourceMutationPayload)
	if !ok {
		t.Fatalf("NodeDeleteHost() data type = %T, want nodeResourceMutationPayload", response.Data)
	}
	item, ok := payload.Item.(nodeHostResourcePayload)
	if !ok {
		t.Fatalf("NodeDeleteHost() item type = %T, want nodeHostResourcePayload", payload.Item)
	}
	if item.Host != "deleted.example.com" || item.Remark != "deleted-host" {
		t.Fatalf("NodeDeleteHost() item = %+v, want delete mutation snapshot", item)
	}
}

func TestNodeDeleteUserUsesDeleteMutationSnapshotWithoutReload(t *testing.T) {
	services := webservice.New()
	services.Users = stubNodeMutationUserService{
		get: func(int) (*file.User, error) {
			t.Fatal("NodeDeleteUser() should not reload user state after delete")
			return nil, nil
		},
		deleteMutation: func(id int) (webservice.UserMutation, error) {
			return webservice.UserMutation{
				ID: id,
				User: &file.User{
					Id:        id,
					Username:  "deleted-user",
					Status:    1,
					TotalFlow: &file.Flow{},
				},
			}, nil
		},
	}
	app := NewWithOptions(nil, Options{Services: &services})
	ctx := &nodeMutationTestContext{
		values: map[string]string{"id": "7"},
	}

	app.NodeDeleteUser(ctx)

	if ctx.status != 200 {
		t.Fatalf("NodeDeleteUser() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeDeleteUser() payload type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	payload, ok := response.Data.(nodeResourceMutationPayload)
	if !ok {
		t.Fatalf("NodeDeleteUser() data type = %T, want nodeResourceMutationPayload", response.Data)
	}
	item, ok := payload.Item.(nodeUserResourcePayload)
	if !ok {
		t.Fatalf("NodeDeleteUser() item type = %T, want nodeUserResourcePayload", payload.Item)
	}
	if item.Username != "deleted-user" {
		t.Fatalf("NodeDeleteUser() item.username = %q, want deleted-user", item.Username)
	}
}
