package api

import (
	"errors"
	"reflect"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
)

type stubQueryClientService struct {
	list  func(webservice.ListClientsInput) webservice.ListClientsResult
	clear func(int, string, bool) error
}

func (s stubQueryClientService) List(input webservice.ListClientsInput) webservice.ListClientsResult {
	if s.list != nil {
		return s.list(input)
	}
	return webservice.ListClientsResult{}
}
func (stubQueryClientService) Add(webservice.AddClientInput) (webservice.ClientMutation, error) {
	return webservice.ClientMutation{}, nil
}
func (stubQueryClientService) Ping(int, string) (int, error) { return 0, nil }
func (stubQueryClientService) Get(int) (*file.Client, error) {
	return nil, webservice.ErrClientNotFound
}
func (stubQueryClientService) Edit(webservice.EditClientInput) (webservice.ClientMutation, error) {
	return webservice.ClientMutation{}, nil
}
func (s stubQueryClientService) Clear(id int, mode string, isAdmin bool) (webservice.ClientMutation, error) {
	if s.clear != nil {
		if err := s.clear(id, mode, isAdmin); err != nil {
			return webservice.ClientMutation{}, err
		}
	}
	return webservice.ClientMutation{ID: id}, nil
}
func (stubQueryClientService) ChangeStatus(int, bool) (webservice.ClientMutation, error) {
	return webservice.ClientMutation{}, nil
}
func (stubQueryClientService) Delete(int) (webservice.ClientMutation, error) {
	return webservice.ClientMutation{}, nil
}
func (stubQueryClientService) BuildQRCode(webservice.ClientQRCodeInput) ([]byte, error) {
	return nil, nil
}

type stubQueryUserService struct {
	list func(webservice.ListUsersInput) webservice.ListUsersResult
	get  func(int) (*file.User, error)
}

func (s stubQueryUserService) List(input webservice.ListUsersInput) webservice.ListUsersResult {
	if s.list != nil {
		return s.list(input)
	}
	return webservice.ListUsersResult{}
}
func (s stubQueryUserService) Get(id int) (*file.User, error) {
	if s.get != nil {
		return s.get(id)
	}
	return nil, webservice.ErrUserNotFound
}
func (stubQueryUserService) Add(webservice.AddUserInput) (webservice.UserMutation, error) {
	return webservice.UserMutation{}, nil
}
func (stubQueryUserService) Edit(webservice.EditUserInput) (webservice.UserMutation, error) {
	return webservice.UserMutation{}, nil
}
func (stubQueryUserService) ChangeStatus(int, bool) (webservice.UserMutation, error) {
	return webservice.UserMutation{}, nil
}
func (stubQueryUserService) Delete(int) (webservice.UserMutation, error) {
	return webservice.UserMutation{}, nil
}

type stubQueryIndexService struct {
	listTunnels func(webservice.TunnelListInput) ([]*file.Tunnel, int)
	listHosts   func(webservice.HostListInput) ([]*file.Host, int)
}

type stubQueryManagementPlatformStore struct {
	ownedClientIDs    func(int) []int
	ownedClientIDsErr error
}

func (s stubQueryManagementPlatformStore) EnsureServiceUser(servercfg.ManagementPlatformConfig) (*file.User, error) {
	return nil, nil
}

func (s stubQueryManagementPlatformStore) ServiceUsername(string, string) string {
	return ""
}

func (s stubQueryManagementPlatformStore) OwnedClientIDs(serviceUserID int) ([]int, error) {
	if s.ownedClientIDsErr != nil {
		return nil, s.ownedClientIDsErr
	}
	if s.ownedClientIDs != nil {
		return s.ownedClientIDs(serviceUserID), nil
	}
	return nil, nil
}

type stubVisibilityRepository struct {
	webservice.Repository
	getManagedClientIDs     func(int) ([]int, error)
	authoritativeManagedIDs bool
	countOwnedResourcesByID func(int) (int, int, int, error)
	collectOwnedCounts      func() (map[int]int, map[int]int, map[int]int, error)
}

func (s stubVisibilityRepository) SupportsGetManagedClientIDsByUserID() bool {
	return s.getManagedClientIDs != nil
}

func (s stubVisibilityRepository) GetManagedClientIDsByUserID(userID int) ([]int, error) {
	if s.getManagedClientIDs != nil {
		return s.getManagedClientIDs(userID)
	}
	return nil, nil
}

func (s stubVisibilityRepository) SupportsAuthoritativeManagedClientIDsByUserID() bool {
	return s.authoritativeManagedIDs
}

func (s stubVisibilityRepository) SupportsCountOwnedResourcesByUserID() bool {
	return s.countOwnedResourcesByID != nil
}

func (s stubVisibilityRepository) CountOwnedResourcesByUserID(userID int) (int, int, int, error) {
	if s.countOwnedResourcesByID != nil {
		return s.countOwnedResourcesByID(userID)
	}
	return 0, 0, 0, nil
}

func (s stubVisibilityRepository) SupportsCollectOwnedResourceCounts() bool {
	return s.collectOwnedCounts != nil
}

func (s stubVisibilityRepository) CollectOwnedResourceCounts() (map[int]int, map[int]int, map[int]int, error) {
	if s.collectOwnedCounts != nil {
		return s.collectOwnedCounts()
	}
	return nil, nil, nil, nil
}

type stubRangeOnlyVisibilityRepository struct {
	webservice.Repository
	rangeClients func(func(*file.Client) bool)
	rangeTunnels func(func(*file.Tunnel) bool)
	rangeHosts   func(func(*file.Host) bool)
}

func (s stubRangeOnlyVisibilityRepository) RangeClients(fn func(*file.Client) bool) {
	if s.rangeClients != nil {
		s.rangeClients(fn)
	}
}

func (s stubRangeOnlyVisibilityRepository) RangeTunnels(fn func(*file.Tunnel) bool) {
	if s.rangeTunnels != nil {
		s.rangeTunnels(fn)
	}
}

func (s stubRangeOnlyVisibilityRepository) RangeHosts(fn func(*file.Host) bool) {
	if s.rangeHosts != nil {
		s.rangeHosts(fn)
	}
}

func (stubQueryIndexService) DashboardData(bool) map[string]interface{} { return nil }
func (s stubQueryIndexService) ListTunnels(input webservice.TunnelListInput) ([]*file.Tunnel, int) {
	if s.listTunnels != nil {
		return s.listTunnels(input)
	}
	return nil, 0
}
func (stubQueryIndexService) AddTunnel(webservice.AddTunnelInput) (webservice.TunnelMutation, error) {
	return webservice.TunnelMutation{}, nil
}
func (stubQueryIndexService) GetTunnel(int) (*file.Tunnel, error) {
	return nil, webservice.ErrTunnelNotFound
}
func (stubQueryIndexService) EditTunnel(webservice.EditTunnelInput) (webservice.TunnelMutation, error) {
	return webservice.TunnelMutation{}, nil
}
func (stubQueryIndexService) StopTunnel(int, string) (webservice.TunnelMutation, error) {
	return webservice.TunnelMutation{}, nil
}
func (stubQueryIndexService) DeleteTunnel(int) (webservice.TunnelMutation, error) {
	return webservice.TunnelMutation{}, nil
}
func (stubQueryIndexService) StartTunnel(int, string) (webservice.TunnelMutation, error) {
	return webservice.TunnelMutation{}, nil
}
func (stubQueryIndexService) ClearTunnel(int, string) (webservice.TunnelMutation, error) {
	return webservice.TunnelMutation{}, nil
}
func (s stubQueryIndexService) ListHosts(input webservice.HostListInput) ([]*file.Host, int) {
	if s.listHosts != nil {
		return s.listHosts(input)
	}
	return nil, 0
}
func (stubQueryIndexService) GetHost(int) (*file.Host, error) { return nil, webservice.ErrHostNotFound }
func (stubQueryIndexService) DeleteHost(int) (webservice.HostMutation, error) {
	return webservice.HostMutation{}, nil
}
func (stubQueryIndexService) StartHost(int, string) (webservice.HostMutation, error) {
	return webservice.HostMutation{}, nil
}
func (stubQueryIndexService) StopHost(int, string) (webservice.HostMutation, error) {
	return webservice.HostMutation{}, nil
}
func (stubQueryIndexService) ClearHost(int, string) (webservice.HostMutation, error) {
	return webservice.HostMutation{}, nil
}
func (stubQueryIndexService) AddHost(webservice.AddHostInput) (webservice.HostMutation, error) {
	return webservice.HostMutation{}, nil
}
func (stubQueryIndexService) EditHost(webservice.EditHostInput) (webservice.HostMutation, error) {
	return webservice.HostMutation{}, nil
}

func TestManagementListHandlersPreserveRawQueryValues(t *testing.T) {
	actor := &Actor{Kind: "admin", IsAdmin: true}
	const rawSearch = `<demo>&"quoted"`
	const rawSort = `updated_at`
	const rawOrder = `desc`
	const rawMode = `tcp<raw>`

	var clientListInput webservice.ListClientsInput
	var userListInput webservice.ListUsersInput
	var tunnelListInput webservice.TunnelListInput
	var hostListInput webservice.HostListInput

	services := webservice.New()
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			clientListInput = input
			return webservice.ListClientsResult{}
		},
	}
	services.Users = stubQueryUserService{
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			userListInput = input
			return webservice.ListUsersResult{}
		},
	}
	services.Index = stubQueryIndexService{
		listTunnels: func(input webservice.TunnelListInput) ([]*file.Tunnel, int) {
			tunnelListInput = input
			return nil, 0
		},
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			hostListInput = input
			return nil, 0
		},
	}
	app := &App{Services: services}

	clientsCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"search": rawSearch, "sort": rawSort, "order": rawOrder},
	}
	app.NodeClients(clientsCtx)
	if clientsCtx.status != 200 {
		t.Fatalf("NodeClients() status = %d, want 200", clientsCtx.status)
	}
	if clientListInput.Search != rawSearch || clientListInput.Sort != rawSort || clientListInput.Order != rawOrder {
		t.Fatalf("NodeClients() list input = %+v, want raw values preserved", clientListInput)
	}

	usersCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"search": rawSearch, "sort": rawSort, "order": rawOrder},
	}
	app.NodeUsers(usersCtx)
	if usersCtx.status != 200 {
		t.Fatalf("NodeUsers() status = %d, want 200", usersCtx.status)
	}
	if userListInput.Search != rawSearch || userListInput.Sort != rawSort || userListInput.Order != rawOrder {
		t.Fatalf("NodeUsers() list input = %+v, want raw values preserved", userListInput)
	}

	tunnelsCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"mode": rawMode, "search": rawSearch, "sort": rawSort, "order": rawOrder},
	}
	app.NodeTunnels(tunnelsCtx)
	if tunnelsCtx.status != 200 {
		t.Fatalf("NodeTunnels() status = %d, want 200", tunnelsCtx.status)
	}
	if tunnelListInput.Type != rawMode || tunnelListInput.Search != rawSearch || tunnelListInput.Sort != rawSort || tunnelListInput.Order != rawOrder {
		t.Fatalf("NodeTunnels() list input = %+v, want raw values preserved", tunnelListInput)
	}

	hostsCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"search": rawSearch, "sort": rawSort, "order": rawOrder},
	}
	app.NodeHosts(hostsCtx)
	if hostsCtx.status != 200 {
		t.Fatalf("NodeHosts() status = %d, want 200", hostsCtx.status)
	}
	if hostListInput.Search != rawSearch || hostListInput.Sort != rawSort || hostListInput.Order != rawOrder {
		t.Fatalf("NodeHosts() list input = %+v, want raw values preserved", hostListInput)
	}
}

func TestNodeUsersPassesPaginationThroughForFullAccess(t *testing.T) {
	actor := &Actor{Kind: "admin", IsAdmin: true}
	var userListInput webservice.ListUsersInput

	services := webservice.New()
	services.Users = stubQueryUserService{
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			userListInput = input
			return webservice.ListUsersResult{
				Rows: []*webservice.UserListRow{
					{
						User:         &file.User{Id: 2, Username: "demo", TotalFlow: &file.Flow{}},
						ExpireAtText: "never",
					},
				},
				Total: 3,
			}
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "1", "limit": "1"},
	}
	app.NodeUsers(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeUsers() status = %d, want 200", ctx.status)
	}
	if userListInput.Offset != 1 || userListInput.Limit != 1 {
		t.Fatalf("NodeUsers() list input = %+v, want offset/limit passed through", userListInput)
	}

	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeUsers() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeUserResourcePayload)
	if !ok {
		t.Fatalf("NodeUsers() data type = %T, want []nodeUserResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 2 {
		t.Fatalf("NodeUsers() items = %+v, want single user id 2", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 3 || !response.Meta.Pagination.HasMore {
		t.Fatalf("NodeUsers() pagination = %+v, want total=3 has_more=true", response.Meta.Pagination)
	}
}

func TestNodeUsersPassesOffsetThroughForFullAccessUnlimitedListing(t *testing.T) {
	actor := &Actor{Kind: "admin", IsAdmin: true}
	var userListInput webservice.ListUsersInput

	services := webservice.New()
	services.Users = stubQueryUserService{
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			userListInput = input
			return webservice.ListUsersResult{
				Rows: []*webservice.UserListRow{
					{
						User:         &file.User{Id: 3, Username: "charlie", TotalFlow: &file.Flow{}},
						ExpireAtText: "never",
					},
					{
						User:         &file.User{Id: 4, Username: "delta", TotalFlow: &file.Flow{}},
						ExpireAtText: "never",
					},
				},
				Total: 4,
			}
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "2", "limit": "0"},
	}
	app.NodeUsers(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeUsers() status = %d, want 200", ctx.status)
	}
	if userListInput.Offset != 2 || userListInput.Limit != 0 {
		t.Fatalf("NodeUsers() list input = %+v, want offset passthrough with unlimited limit", userListInput)
	}

	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeUsers() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeUserResourcePayload)
	if !ok {
		t.Fatalf("NodeUsers() data type = %T, want []nodeUserResourcePayload", response.Data)
	}
	if len(items) != 2 || items[0].ID != 3 || items[1].ID != 4 {
		t.Fatalf("NodeUsers() items = %+v, want ids [3 4]", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Offset != 2 || response.Meta.Pagination.Total != 4 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeUsers() pagination = %+v, want offset=2 total=4 has_more=false", response.Meta.Pagination)
	}
}

func TestNodeUsersScopedAccessStillFiltersAndPaginatesAfterListing(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}
	var userListInput webservice.ListUsersInput

	services := webservice.New()
	services.Users = stubQueryUserService{
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			userListInput = input
			return webservice.ListUsersResult{
				Rows: []*webservice.UserListRow{
					{User: &file.User{Id: 7, Username: "other", TotalFlow: &file.Flow{}}},
					{User: &file.User{Id: 8, Username: "demo", TotalFlow: &file.Flow{}}},
				},
				Total: 2,
			}
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "0", "limit": "1"},
	}
	app.NodeUsers(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeUsers() status = %d, want 200", ctx.status)
	}
	if userListInput.Offset != 0 || userListInput.Limit != 0 {
		t.Fatalf("NodeUsers() scoped list input = %+v, want full fetch for scope filtering", userListInput)
	}

	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeUsers() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeUserResourcePayload)
	if !ok {
		t.Fatalf("NodeUsers() data type = %T, want []nodeUserResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 8 {
		t.Fatalf("NodeUsers() items = %+v, want single visible user id 8", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 1 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeUsers() pagination = %+v, want total=1 has_more=false", response.Meta.Pagination)
	}
}

func TestScopedNodeResourceListsPushVisibilityDown(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		ClientIDs: []int{7, 8},
	}
	var clientListInput webservice.ListClientsInput
	var tunnelListInput webservice.TunnelListInput
	var hostListInput webservice.HostListInput

	services := webservice.New()
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			clientListInput = input
			return webservice.ListClientsResult{}
		},
	}
	services.Index = stubQueryIndexService{
		listTunnels: func(input webservice.TunnelListInput) ([]*file.Tunnel, int) {
			tunnelListInput = input
			return nil, 0
		},
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			hostListInput = input
			return nil, 0
		},
	}
	app := &App{Services: services}

	clientsCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "1", "limit": "2"},
	}
	app.NodeClients(clientsCtx)
	if clientsCtx.status != 200 {
		t.Fatalf("NodeClients() status = %d, want 200", clientsCtx.status)
	}
	assertScopedClientVisibility(t, clientListInput.Visibility, []int{7, 8})
	if clientListInput.Offset != 1 || clientListInput.Limit != 2 {
		t.Fatalf("NodeClients() list input = %+v, want offset=1 limit=2", clientListInput)
	}

	tunnelsCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "3", "limit": "4"},
	}
	app.NodeTunnels(tunnelsCtx)
	if tunnelsCtx.status != 200 {
		t.Fatalf("NodeTunnels() status = %d, want 200", tunnelsCtx.status)
	}
	assertScopedClientVisibility(t, tunnelListInput.Visibility, []int{7, 8})
	if tunnelListInput.Offset != 3 || tunnelListInput.Limit != 4 {
		t.Fatalf("NodeTunnels() list input = %+v, want offset=3 limit=4", tunnelListInput)
	}

	hostsCtx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "5", "limit": "6"},
	}
	app.NodeHosts(hostsCtx)
	if hostsCtx.status != 200 {
		t.Fatalf("NodeHosts() status = %d, want 200", hostsCtx.status)
	}
	assertScopedClientVisibility(t, hostListInput.Visibility, []int{7, 8})
	if hostListInput.Offset != 5 || hostListInput.Limit != 6 {
		t.Fatalf("NodeHosts() list input = %+v, want offset=5 limit=6", hostListInput)
	}
}

func TestScopedNodeResourceListsDeriveVisibilityFromUserScopeWhenActorClientIDsEmpty(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}
	var clientListInput webservice.ListClientsInput

	services := webservice.New()
	services.Backend.Repository = stubVisibilityRepository{
		Repository:              services.Backend.Repository,
		authoritativeManagedIDs: true,
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 8 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 8", userID)
			}
			return []int{12, 11, 12}, nil
		},
	}
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			clientListInput = input
			return webservice.ListClientsResult{}
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{actor: actor}
	app.NodeClients(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeClients() status = %d, want 200", ctx.status)
	}
	assertScopedClientVisibility(t, clientListInput.Visibility, []int{11, 12})
}

func TestScopedNodeResourceListsFallBackToRepositoryRangeWhenManagedLookupUnsupported(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}
	var clientListInput webservice.ListClientsInput

	services := webservice.New()
	services.Backend.Repository = stubRangeOnlyVisibilityRepository{
		rangeClients: func(fn func(*file.Client) bool) {
			for _, client := range []*file.Client{
				{Id: 12, OwnerUserID: 8, Status: true, Flow: &file.Flow{}},
				{Id: 11, OwnerUserID: 8, Status: true, Flow: &file.Flow{}},
				{Id: 15, OwnerUserID: 9, Status: true, Flow: &file.Flow{}},
			} {
				if !fn(client) {
					return
				}
			}
		},
	}
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			clientListInput = input
			return webservice.ListClientsResult{}
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{actor: actor}
	app.NodeClients(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeClients() status = %d, want 200", ctx.status)
	}
	assertScopedClientVisibility(t, clientListInput.Visibility, []int{11, 12})
}

func TestScopedNodeResourceListsDeriveVisibilityFromPlatformServiceUserWhenActorClientIDsEmpty(t *testing.T) {
	actor := PlatformActor("platform-a", "admin", "platform-admin", "actor-1", false, nil, 77)
	var hostListInput webservice.HostListInput

	services := webservice.New()
	services.ManagementPlatforms = stubQueryManagementPlatformStore{
		ownedClientIDs: func(serviceUserID int) []int {
			if serviceUserID != 77 {
				t.Fatalf("OwnedClientIDs(%d), want 77", serviceUserID)
			}
			return []int{22, 21, 21}
		},
	}
	services.Index = stubQueryIndexService{
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			hostListInput = input
			return nil, 0
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{actor: actor}
	app.NodeHosts(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeHosts() status = %d, want 200", ctx.status)
	}
	assertScopedClientVisibility(t, hostListInput.Visibility, []int{21, 22})
}

func TestScopedNodeResourceListsReturnInternalServerErrorWhenPlatformOwnershipLookupFails(t *testing.T) {
	actor := PlatformActor("platform-a", "admin", "platform-admin", "actor-1", false, nil, 77)
	lookupErr := errors.New("platform ownership lookup failed")

	services := webservice.New()
	services.ManagementPlatforms = stubQueryManagementPlatformStore{
		ownedClientIDsErr: lookupErr,
	}
	services.Index = stubQueryIndexService{
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			t.Fatalf("ListHosts(%+v) should not be called when platform ownership lookup fails", input)
			return nil, 0
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{actor: actor}
	app.NodeHosts(ctx)

	if ctx.status != 500 {
		t.Fatalf("NodeHosts() status = %d, want 500", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeHosts() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != lookupErr.Error() {
		t.Fatalf("NodeHosts() error = %+v, want request_failed/%q", response.Error, lookupErr.Error())
	}
}

func TestScopedNodeHostsUsesScopedVisibility(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		ClientIDs: []int{7, 8},
	}
	var hostListInput webservice.HostListInput

	services := webservice.New()
	services.Index = stubQueryIndexService{
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			hostListInput = input
			return nil, 0
		},
	}
	app := &App{Services: services}

	hosts, err := app.scopedNodeHosts(actor)
	if err != nil {
		t.Fatalf("scopedNodeHosts() error = %v, want nil", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("scopedNodeHosts() = %+v, want empty slice", hosts)
	}
	assertScopedClientVisibility(t, hostListInput.Visibility, []int{7, 8})
	if hostListInput.Offset != 0 || hostListInput.Limit != 0 {
		t.Fatalf("scopedNodeHosts() list input = %+v, want offset=0 limit=0", hostListInput)
	}
}

func TestNodeClearClientsUsesScopedVisibilityClientIDsWithoutListing(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		ClientIDs: []int{7, 8},
		Permissions: []string{
			webservice.PermissionClientsStatus,
			webservice.PermissionClientsRead,
		},
		Roles: []string{webservice.RoleUser},
	}
	cleared := make([]int, 0, 2)

	services := webservice.New()
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			t.Fatalf("List(%+v) should not be called when scoped visibility already carries client ids", input)
			return webservice.ListClientsResult{}
		},
		clear: func(id int, mode string, isAdmin bool) error {
			if mode != "flow" || !isAdmin {
				t.Fatalf("Clear(%d, %q, %v), want mode=flow isAdmin=true", id, mode, isAdmin)
			}
			cleared = append(cleared, id)
			return nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:   actor,
		rawBody: []byte(`{"mode":"flow"}`),
	}

	app.NodeClearClients(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeClearClients() status = %d, want 200", ctx.status)
	}
	if !reflect.DeepEqual(cleared, []int{7, 8}) {
		t.Fatalf("NodeClearClients() cleared = %v, want [7 8] directly from scoped visibility", cleared)
	}
}

func TestNodeClearClientsDeduplicatesAdminEnumeratedTargets(t *testing.T) {
	actor := AdminActor("admin")
	cleared := make([]int, 0, 2)
	var clientListInput webservice.ListClientsInput

	services := webservice.New()
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			clientListInput = input
			return webservice.ListClientsResult{
				Rows: []*file.Client{
					{Id: 7, Status: true, VerifyKey: "vk-7", Cnf: &file.Config{}, Flow: &file.Flow{}},
					{Id: 7, Status: true, VerifyKey: "vk-7-dup", Cnf: &file.Config{}, Flow: &file.Flow{}},
					{Id: 8, Status: true, VerifyKey: "vk-8", Cnf: &file.Config{}, Flow: &file.Flow{}},
				},
				Total: 3,
			}
		},
		clear: func(id int, mode string, isAdmin bool) error {
			if mode != "flow" || !isAdmin {
				t.Fatalf("Clear(%d, %q, %v), want mode=flow isAdmin=true", id, mode, isAdmin)
			}
			cleared = append(cleared, id)
			return nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:   actor,
		rawBody: []byte(`{"mode":"flow"}`),
	}

	app.NodeClearClients(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeClearClients() status = %d, want 200", ctx.status)
	}
	if !clientListInput.Visibility.IsAdmin || clientListInput.Offset != 0 || clientListInput.Limit != 0 {
		t.Fatalf("NodeClearClients() list input = %+v, want admin full enumeration", clientListInput)
	}
	if !reflect.DeepEqual(cleared, []int{7, 8}) {
		t.Fatalf("NodeClearClients() cleared = %v, want [7 8]", cleared)
	}
}

func TestNodeClearClientsDeduplicatesExplicitTargetsBeforeClear(t *testing.T) {
	actor := AdminActor("admin")
	cleared := make([]int, 0, 2)

	services := webservice.New()
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			t.Fatalf("List(%+v) should not be called when explicit client ids are provided", input)
			return webservice.ListClientsResult{}
		},
		clear: func(id int, mode string, isAdmin bool) error {
			if mode != "flow" || !isAdmin {
				t.Fatalf("Clear(%d, %q, %v), want mode=flow isAdmin=true", id, mode, isAdmin)
			}
			cleared = append(cleared, id)
			return nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:   actor,
		rawBody: []byte(`{"mode":"flow","client_ids":[7,7,8,0,8]}`),
	}

	app.NodeClearClients(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeClearClients() status = %d, want 200", ctx.status)
	}
	if !reflect.DeepEqual(cleared, []int{7, 8}) {
		t.Fatalf("NodeClearClients() cleared = %v, want deduplicated explicit [7 8]", cleared)
	}
}

func TestScopedNodeClientsReturnInternalServerErrorWhenVisibilityLookupFails(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}
	lookupErr := errors.New("managed lookup failed")

	services := webservice.New()
	services.Backend.Repository = stubVisibilityRepository{
		Repository: services.Backend.Repository,
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 8 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 8", userID)
			}
			return nil, lookupErr
		},
	}
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			t.Fatalf("List(%+v) should not be called when visibility lookup fails", input)
			return webservice.ListClientsResult{}
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{actor: actor}

	app.NodeClients(ctx)

	if ctx.status != 500 {
		t.Fatalf("NodeClients() status = %d, want 500", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClients() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != lookupErr.Error() {
		t.Fatalf("NodeClients() error = %+v, want request_failed/%q", response.Error, lookupErr.Error())
	}
}

func TestNodeClearClientsReturnsInternalServerErrorWhenVisibilityLookupFails(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
		Roles: []string{webservice.RoleUser},
		Permissions: []string{
			webservice.PermissionClientsStatus,
			webservice.PermissionClientsRead,
		},
	}
	lookupErr := errors.New("managed lookup failed")

	services := webservice.New()
	services.Backend.Repository = stubVisibilityRepository{
		Repository: services.Backend.Repository,
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 8 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 8", userID)
			}
			return nil, lookupErr
		},
	}
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			t.Fatalf("List(%+v) should not be called when visibility lookup fails", input)
			return webservice.ListClientsResult{}
		},
		clear: func(id int, mode string, isAdmin bool) error {
			t.Fatalf("Clear(%d, %q, %v) should not be called when visibility lookup fails", id, mode, isAdmin)
			return nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:   actor,
		rawBody: []byte(`{"mode":"flow"}`),
	}

	app.NodeClearClients(ctx)

	if ctx.status != 500 {
		t.Fatalf("NodeClearClients() status = %d, want 500", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClearClients() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != lookupErr.Error() {
		t.Fatalf("NodeClearClients() error = %+v, want request_failed/%q", response.Error, lookupErr.Error())
	}
}

func assertScopedClientVisibility(t *testing.T, visibility webservice.ClientVisibility, clientIDs []int) {
	t.Helper()
	if visibility.IsAdmin {
		t.Fatalf("visibility = %+v, want scoped visibility", visibility)
	}
	if visibility.PrimaryClientID != clientIDs[0] {
		t.Fatalf("visibility.PrimaryClientID = %d, want %d", visibility.PrimaryClientID, clientIDs[0])
	}
	if !reflect.DeepEqual(visibility.ClientIDs, clientIDs) {
		t.Fatalf("visibility.ClientIDs = %v, want %v", visibility.ClientIDs, clientIDs)
	}
}

func TestNodeUsersScopedAccessUsesDirectLookupFastPath(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}

	services := webservice.New()
	services.Users = stubQueryUserService{
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			t.Fatalf("List(%+v) should not be called for scoped direct lookup path", input)
			return webservice.ListUsersResult{}
		},
		get: func(id int) (*file.User, error) {
			if id != 8 {
				t.Fatalf("Get(%d), want 8", id)
			}
			return &file.User{Id: 8, Username: "demo", Status: 1, TotalFlow: &file.Flow{}}, nil
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "0", "limit": "1", "search": " demo "},
	}
	app.NodeUsers(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeUsers() status = %d, want 200", ctx.status)
	}

	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeUsers() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeUserResourcePayload)
	if !ok {
		t.Fatalf("NodeUsers() data type = %T, want []nodeUserResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 8 {
		t.Fatalf("NodeUsers() items = %+v, want single visible user id 8", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 1 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeUsers() pagination = %+v, want total=1 has_more=false", response.Meta.Pagination)
	}
}

func TestNodeOwnedUserResourceCountsUsesSingleUserFastPath(t *testing.T) {
	app := &App{
		Services: webservice.Services{
			Backend: webservice.Backend{
				Repository: stubVisibilityRepository{
					countOwnedResourcesByID: func(userID int) (int, int, int, error) {
						if userID != 8 {
							t.Fatalf("CountOwnedResourcesByUserID(%d), want 8", userID)
						}
						return 2, 3, 4, nil
					},
				},
			},
		},
	}

	clientCount, tunnelCount, hostCount := app.nodeOwnedUserResourceCounts(8)
	if clientCount != 2 || tunnelCount != 3 || hostCount != 4 {
		t.Fatalf("nodeOwnedUserResourceCounts(8) = (%d,%d,%d), want (2,3,4)", clientCount, tunnelCount, hostCount)
	}
}

func TestNodeOwnedUserResourceCountsFallsBackToAggregateFastPathAfterSingleUserError(t *testing.T) {
	app := &App{
		Services: webservice.Services{
			Backend: webservice.Backend{
				Repository: stubVisibilityRepository{
					countOwnedResourcesByID: func(userID int) (int, int, int, error) {
						if userID != 8 {
							t.Fatalf("CountOwnedResourcesByUserID(%d), want 8", userID)
						}
						return 0, 0, 0, errors.New("single-user count failed")
					},
					collectOwnedCounts: func() (map[int]int, map[int]int, map[int]int, error) {
						return map[int]int{8: 5}, map[int]int{8: 6}, map[int]int{8: 7}, nil
					},
				},
			},
		},
	}

	clientCount, tunnelCount, hostCount := app.nodeOwnedUserResourceCounts(8)
	if clientCount != 5 || tunnelCount != 6 || hostCount != 7 {
		t.Fatalf("nodeOwnedUserResourceCounts(8) = (%d,%d,%d), want aggregate fallback (5,6,7)", clientCount, tunnelCount, hostCount)
	}
}

func TestNodeOwnedUserResourceCountsFallsBackToRangesAfterFastPathErrors(t *testing.T) {
	app := &App{
		Services: webservice.Services{
			Backend: webservice.Backend{
				Repository: stubRangeOnlyVisibilityRepository{
					Repository: stubVisibilityRepository{
						countOwnedResourcesByID: func(userID int) (int, int, int, error) {
							if userID != 8 {
								t.Fatalf("CountOwnedResourcesByUserID(%d), want 8", userID)
							}
							return 0, 0, 0, errors.New("single-user count failed")
						},
						collectOwnedCounts: func() (map[int]int, map[int]int, map[int]int, error) {
							return nil, nil, nil, errors.New("aggregate count failed")
						},
					},
					rangeClients: func(fn func(*file.Client) bool) {
						fn(&file.Client{Id: 11, OwnerUserID: 8})
						fn(&file.Client{Id: 12, OwnerUserID: 9})
					},
					rangeTunnels: func(fn func(*file.Tunnel) bool) {
						fn(&file.Tunnel{Id: 21, Client: &file.Client{Id: 11, OwnerUserID: 8}})
						fn(&file.Tunnel{Id: 22, Client: &file.Client{Id: 12, OwnerUserID: 9}})
					},
					rangeHosts: func(fn func(*file.Host) bool) {
						fn(&file.Host{Id: 31, Client: &file.Client{Id: 11, OwnerUserID: 8}})
						fn(&file.Host{Id: 32, Client: &file.Client{Id: 12, OwnerUserID: 9}})
					},
				},
			},
		},
	}

	clientCount, tunnelCount, hostCount := app.nodeOwnedUserResourceCounts(8)
	if clientCount != 1 || tunnelCount != 1 || hostCount != 1 {
		t.Fatalf("nodeOwnedUserResourceCounts(8) = (%d,%d,%d), want range fallback (1,1,1)", clientCount, tunnelCount, hostCount)
	}
}

func TestNodeUsersScopedAccessFailsClosedWhenDirectLookupErrors(t *testing.T) {
	actor := &Actor{
		Kind:      "user",
		Username:  "demo",
		SubjectID: "demo",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}
	var userListInput webservice.ListUsersInput
	backendErr := errors.New("user lookup unavailable")

	services := webservice.New()
	services.Users = stubQueryUserService{
		get: func(id int) (*file.User, error) {
			if id != 8 {
				t.Fatalf("Get(%d), want 8", id)
			}
			return nil, backendErr
		},
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			userListInput = input
			t.Fatalf("List(%+v) should not be called after scoped direct lookup error", input)
			return webservice.ListUsersResult{}
		},
	}
	app := &App{Services: services}

	ctx := &nodeMutationTestContext{
		actor:  actor,
		values: map[string]string{"offset": "0", "limit": "1"},
	}
	app.NodeUsers(ctx)
	if ctx.status != 500 {
		t.Fatalf("NodeUsers() status = %d, want 500", ctx.status)
	}
	if userListInput != (webservice.ListUsersInput{}) {
		t.Fatalf("NodeUsers() fallback list input = %+v, want no fallback list call", userListInput)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUsers() response type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "request_failed" || response.Error.Message != backendErr.Error() {
		t.Fatalf("NodeUsers() error = %+v, want request_failed/%q", response.Error, backendErr.Error())
	}
}

func TestNodeUsersSkipsNilRowsForFullAccess(t *testing.T) {
	services := webservice.New()
	services.Users = stubQueryUserService{
		list: func(input webservice.ListUsersInput) webservice.ListUsersResult {
			return webservice.ListUsersResult{
				Rows: []*webservice.UserListRow{
					nil,
					{User: nil},
					{User: &file.User{Id: 9, Username: "echo", TotalFlow: &file.Flow{}}},
				},
				Total: 3,
			}
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{actor: &Actor{Kind: "admin", IsAdmin: true}}

	app.NodeUsers(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeUsers() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeUsers() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeUserResourcePayload)
	if !ok {
		t.Fatalf("NodeUsers() data type = %T, want []nodeUserResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 9 {
		t.Fatalf("NodeUsers() items = %+v, want single user id 9", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 1 || response.Meta.Pagination.Returned != 1 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeUsers() pagination = %+v, want total=1 returned=1 has_more=false", response.Meta.Pagination)
	}
}

func TestNodeClientsSkipsNilRowsForFullAccess(t *testing.T) {
	services := webservice.New()
	services.Clients = stubQueryClientService{
		list: func(input webservice.ListClientsInput) webservice.ListClientsResult {
			return webservice.ListClientsResult{
				Rows: []*file.Client{
					nil,
					{Id: 11, VerifyKey: "vk-11", Status: true, Cnf: &file.Config{}, Flow: &file.Flow{}},
				},
				Total: 2,
			}
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{actor: &Actor{Kind: "admin", IsAdmin: true}}

	app.NodeClients(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeClients() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeClients() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeClientResourcePayload)
	if !ok {
		t.Fatalf("NodeClients() data type = %T, want []nodeClientResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 11 {
		t.Fatalf("NodeClients() items = %+v, want single client id 11", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 1 || response.Meta.Pagination.Returned != 1 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeClients() pagination = %+v, want total=1 returned=1 has_more=false", response.Meta.Pagination)
	}
}

func TestNodeTunnelsSkipsNilRowsForFullAccess(t *testing.T) {
	services := webservice.New()
	services.Index = stubQueryIndexService{
		listTunnels: func(input webservice.TunnelListInput) ([]*file.Tunnel, int) {
			return []*file.Tunnel{
				nil,
				{Id: 21, Status: true, Flow: &file.Flow{}, Target: &file.Target{TargetStr: "127.0.0.1:80"}},
			}, 2
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{actor: &Actor{Kind: "admin", IsAdmin: true}}

	app.NodeTunnels(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeTunnels() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeTunnels() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeTunnelResourcePayload)
	if !ok {
		t.Fatalf("NodeTunnels() data type = %T, want []nodeTunnelResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 21 {
		t.Fatalf("NodeTunnels() items = %+v, want single tunnel id 21", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 1 || response.Meta.Pagination.Returned != 1 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeTunnels() pagination = %+v, want total=1 returned=1 has_more=false", response.Meta.Pagination)
	}
}

func TestNodeHostsSkipsNilRowsForFullAccess(t *testing.T) {
	services := webservice.New()
	services.Index = stubQueryIndexService{
		listHosts: func(input webservice.HostListInput) ([]*file.Host, int) {
			return []*file.Host{
				nil,
				{Id: 31, Host: "demo.example.com", Flow: &file.Flow{}, Target: &file.Target{TargetStr: "127.0.0.1:80"}},
			}, 2
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{actor: &Actor{Kind: "admin", IsAdmin: true}}

	app.NodeHosts(ctx)
	if ctx.status != 200 {
		t.Fatalf("NodeHosts() status = %d, want 200", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementDataResponse)
	if !ok {
		t.Fatalf("NodeHosts() response type = %T, want ManagementDataResponse", ctx.jsonPayload)
	}
	items, ok := response.Data.([]nodeHostResourcePayload)
	if !ok {
		t.Fatalf("NodeHosts() data type = %T, want []nodeHostResourcePayload", response.Data)
	}
	if len(items) != 1 || items[0].ID != 31 {
		t.Fatalf("NodeHosts() items = %+v, want single host id 31", items)
	}
	if response.Meta.Pagination == nil || response.Meta.Pagination.Total != 1 || response.Meta.Pagination.Returned != 1 || response.Meta.Pagination.HasMore {
		t.Fatalf("NodeHosts() pagination = %+v, want total=1 returned=1 has_more=false", response.Meta.Pagination)
	}
}

func TestNodeClientReturnsNotFoundWhenServiceReturnsNilWithoutError(t *testing.T) {
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(int) (*file.Client, error) {
			return nil, nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:  &Actor{Kind: "admin", IsAdmin: true},
		values: map[string]string{"id": "11"},
	}

	app.NodeClient(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeClient() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClient() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "client_not_found" {
		t.Fatalf("NodeClient() code = %q, want client_not_found", response.Error.Code)
	}
}

func TestNodeClientConnectionsReturnsNotFoundWhenServiceReturnsNilWithoutError(t *testing.T) {
	services := webservice.New()
	services.Clients = stubNodeMutationClientService{
		get: func(int) (*file.Client, error) {
			return nil, nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:  &Actor{Kind: "admin", IsAdmin: true},
		values: map[string]string{"id": "12"},
	}

	app.NodeClientConnections(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeClientConnections() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeClientConnections() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "client_not_found" {
		t.Fatalf("NodeClientConnections() code = %q, want client_not_found", response.Error.Code)
	}
}

func TestNodeUserReturnsNotFoundWhenServiceReturnsNilWithoutError(t *testing.T) {
	services := webservice.New()
	services.Users = stubNodeMutationUserService{
		get: func(int) (*file.User, error) {
			return nil, nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:  &Actor{Kind: "admin", IsAdmin: true},
		values: map[string]string{"id": "13"},
	}

	app.NodeUser(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeUser() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeUser() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "user_not_found" {
		t.Fatalf("NodeUser() code = %q, want user_not_found", response.Error.Code)
	}
}

func TestNodeTunnelReturnsNotFoundWhenServiceReturnsNilWithoutError(t *testing.T) {
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		getTunnel: func(int) (*file.Tunnel, error) {
			return nil, nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:  &Actor{Kind: "admin", IsAdmin: true},
		values: map[string]string{"id": "14"},
	}

	app.NodeTunnel(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeTunnel() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeTunnel() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "tunnel_not_found" {
		t.Fatalf("NodeTunnel() code = %q, want tunnel_not_found", response.Error.Code)
	}
}

func TestNodeHostReturnsNotFoundWhenServiceReturnsNilWithoutError(t *testing.T) {
	services := webservice.New()
	services.Index = stubNodeMutationIndexService{
		getHost: func(int) (*file.Host, error) {
			return nil, nil
		},
	}
	app := &App{Services: services}
	ctx := &nodeMutationTestContext{
		actor:  &Actor{Kind: "admin", IsAdmin: true},
		values: map[string]string{"id": "15"},
	}

	app.NodeHost(ctx)

	if ctx.status != 404 {
		t.Fatalf("NodeHost() status = %d, want 404", ctx.status)
	}
	response, ok := ctx.jsonPayload.(ManagementErrorResponse)
	if !ok {
		t.Fatalf("NodeHost() payload type = %T, want ManagementErrorResponse", ctx.jsonPayload)
	}
	if response.Error.Code != "host_not_found" {
		t.Fatalf("NodeHost() code = %q, want host_not_found", response.Error.Code)
	}
}
