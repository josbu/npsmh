package routers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func scopedNodeUserCookies(t *testing.T, cfg *servercfg.Snapshot, user *file.User, clientIDs ...int) []*http.Cookie {
	t.Helper()
	return sessionCookiesFromIdentity(t, cfg, (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + user.Username,
		Username:      user.Username,
		ClientIDs:     append([]int(nil), clientIDs...),
		Roles:         []string{webservice.RoleUser},
		Attributes: map[string]string{
			"user_id": strconv.Itoa(user.Id),
		},
	}).Normalize())
}

func TestNodeResourceRoutesRespectScopedUserAccess(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nallow_user_login=true\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	userA := createTestUser(t, 1, "tenant-a", "secret")
	userB := createTestUser(t, 2, "tenant-b", "secret")
	clientA := createOwnedTestClient(t, 1, userA.Id, "owned client")
	clientB := createOwnedTestClient(t, 2, userB.Id, "foreign client")
	createTestTunnel(t, 1, clientA, 10001)
	createTestTunnel(t, 2, clientB, 10002)
	createTestHost(t, 1, clientA, "owned.example.com")
	createTestHost(t, 2, clientB, "foreign.example.com")

	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := scopedNodeUserCookies(t, servercfg.Current(), userA, clientA.Id)

	clientsReq := httptest.NewRequest(http.MethodGet, "/api/clients?limit=10", nil)
	for _, cookie := range cookies {
		clientsReq.AddCookie(cookie)
	}
	clientsResp := httptest.NewRecorder()
	handler.ServeHTTP(clientsResp, clientsReq)
	if clientsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/clients status = %d, want 200 body=%s", clientsResp.Code, clientsResp.Body.String())
	}
	if body := clientsResp.Body.String(); !strings.Contains(body, "\"verify_key\":\"vk-1\"") || strings.Contains(body, "\"verify_key\":\"vk-2\"") {
		t.Fatalf("GET /api/clients should only expose owned client, got %s", body)
	}

	clientReq := httptest.NewRequest(http.MethodGet, "/api/clients/1", nil)
	for _, cookie := range cookies {
		clientReq.AddCookie(cookie)
	}
	clientResp := httptest.NewRecorder()
	handler.ServeHTTP(clientResp, clientReq)
	if clientResp.Code != http.StatusOK {
		t.Fatalf("GET /api/clients/1 status = %d, want 200 body=%s", clientResp.Code, clientResp.Body.String())
	}

	foreignClientReq := httptest.NewRequest(http.MethodGet, "/api/clients/2", nil)
	for _, cookie := range cookies {
		foreignClientReq.AddCookie(cookie)
	}
	foreignClientResp := httptest.NewRecorder()
	handler.ServeHTTP(foreignClientResp, foreignClientReq)
	if foreignClientResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/clients/2 status = %d, want 403 body=%s", foreignClientResp.Code, foreignClientResp.Body.String())
	}

	tunnelsReq := httptest.NewRequest(http.MethodGet, "/api/tunnels?limit=10", nil)
	for _, cookie := range cookies {
		tunnelsReq.AddCookie(cookie)
	}
	tunnelsResp := httptest.NewRecorder()
	handler.ServeHTTP(tunnelsResp, tunnelsReq)
	if tunnelsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/tunnels status = %d, want 200 body=%s", tunnelsResp.Code, tunnelsResp.Body.String())
	}
	if body := tunnelsResp.Body.String(); !strings.Contains(body, "\"port\":10001") || strings.Contains(body, "\"port\":10002") {
		t.Fatalf("GET /api/tunnels should only expose owned tunnel, got %s", body)
	}

	tunnelReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/1", nil)
	for _, cookie := range cookies {
		tunnelReq.AddCookie(cookie)
	}
	tunnelResp := httptest.NewRecorder()
	handler.ServeHTTP(tunnelResp, tunnelReq)
	if tunnelResp.Code != http.StatusOK {
		t.Fatalf("GET /api/tunnels/1 status = %d, want 200 body=%s", tunnelResp.Code, tunnelResp.Body.String())
	}

	foreignTunnelReq := httptest.NewRequest(http.MethodGet, "/api/tunnels/2", nil)
	for _, cookie := range cookies {
		foreignTunnelReq.AddCookie(cookie)
	}
	foreignTunnelResp := httptest.NewRecorder()
	handler.ServeHTTP(foreignTunnelResp, foreignTunnelReq)
	if foreignTunnelResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/tunnels/2 status = %d, want 403 body=%s", foreignTunnelResp.Code, foreignTunnelResp.Body.String())
	}

	hostsReq := httptest.NewRequest(http.MethodGet, "/api/hosts?limit=10", nil)
	for _, cookie := range cookies {
		hostsReq.AddCookie(cookie)
	}
	hostsResp := httptest.NewRecorder()
	handler.ServeHTTP(hostsResp, hostsReq)
	if hostsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/hosts status = %d, want 200 body=%s", hostsResp.Code, hostsResp.Body.String())
	}
	if body := hostsResp.Body.String(); !strings.Contains(body, "\"host\":\"owned.example.com\"") || strings.Contains(body, "\"host\":\"foreign.example.com\"") {
		t.Fatalf("GET /api/hosts should only expose owned host, got %s", body)
	}

	hostReq := httptest.NewRequest(http.MethodGet, "/api/hosts/1", nil)
	for _, cookie := range cookies {
		hostReq.AddCookie(cookie)
	}
	hostResp := httptest.NewRecorder()
	handler.ServeHTTP(hostResp, hostReq)
	if hostResp.Code != http.StatusOK {
		t.Fatalf("GET /api/hosts/1 status = %d, want 200 body=%s", hostResp.Code, hostResp.Body.String())
	}

	foreignHostReq := httptest.NewRequest(http.MethodGet, "/api/hosts/2", nil)
	for _, cookie := range cookies {
		foreignHostReq.AddCookie(cookie)
	}
	foreignHostResp := httptest.NewRecorder()
	handler.ServeHTTP(foreignHostResp, foreignHostReq)
	if foreignHostResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/hosts/2 status = %d, want 403 body=%s", foreignHostResp.Code, foreignHostResp.Body.String())
	}

	usersReq := httptest.NewRequest(http.MethodGet, "/api/users?limit=10", nil)
	for _, cookie := range cookies {
		usersReq.AddCookie(cookie)
	}
	usersResp := httptest.NewRecorder()
	handler.ServeHTTP(usersResp, usersReq)
	if usersResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/users status = %d, want 403 body=%s", usersResp.Code, usersResp.Body.String())
	}

	userReq := httptest.NewRequest(http.MethodGet, "/api/users/1", nil)
	for _, cookie := range cookies {
		userReq.AddCookie(cookie)
	}
	userResp := httptest.NewRecorder()
	handler.ServeHTTP(userResp, userReq)
	if userResp.Code != http.StatusForbidden {
		t.Fatalf("GET /api/users/1 status = %d, want 403 body=%s", userResp.Code, userResp.Body.String())
	}
}

func TestNodeLocalSessionInvalidatesDisabledUserOnNextRequest(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nallow_user_login=true\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	user := createTestUser(t, 1, "tenant", "secret")
	client := createOwnedTestClient(t, 1, user.Id, "owned client")

	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := scopedNodeUserCookies(t, servercfg.Current(), user, client.Id)

	beforeReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	for _, cookie := range cookies {
		beforeReq.AddCookie(cookie)
	}
	beforeResp := httptest.NewRecorder()
	handler.ServeHTTP(beforeResp, beforeReq)
	if beforeResp.Code != http.StatusOK {
		t.Fatalf("GET /api/system/usage-snapshot before disabling user status = %d, want 200 body=%s", beforeResp.Code, beforeResp.Body.String())
	}

	savedUser, err := file.GetDb().GetUser(user.Id)
	if err != nil {
		t.Fatalf("GetUser(%d) error = %v", user.Id, err)
	}
	savedUser.Status = 0
	if err := file.GetDb().UpdateUser(savedUser); err != nil {
		t.Fatalf("UpdateUser(%d) error = %v", user.Id, err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/api/system/usage-snapshot", nil)
	for _, cookie := range cookies {
		afterReq.AddCookie(cookie)
	}
	afterResp := httptest.NewRecorder()
	handler.ServeHTTP(afterResp, afterReq)
	if afterResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/system/usage-snapshot after disabling user status = %d, want 401 body=%s", afterResp.Code, afterResp.Body.String())
	}
	if body := afterResp.Body.String(); !strings.Contains(body, "\"code\":\"unauthorized\"") || !strings.Contains(body, "\"message\":\"unauthorized\"") {
		t.Fatalf("GET /api/system/usage-snapshot after disabling user should clear session, got %s", body)
	}
}

func TestNodeResourceMutationRoutesRespectScopedUserAccess(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nallow_user_login=true\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	userA := createTestUser(t, 1, "tenant-a", "secret")
	userB := createTestUser(t, 2, "tenant-b", "secret")
	clientA := createOwnedTestClient(t, 1, userA.Id, "owned client")
	clientB := createOwnedTestClient(t, 2, userB.Id, "foreign client")
	createTestTunnel(t, 1, clientA, 10001)
	createTestTunnel(t, 2, clientB, 10002)
	createTestHost(t, 1, clientA, "owned.example.com")
	createTestHost(t, 2, clientB, "foreign.example.com")

	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := scopedNodeUserCookies(t, servercfg.Current(), userA, clientA.Id)

	ownedClientReq := httptest.NewRequest(http.MethodPost, "/api/clients/1/actions/update", strings.NewReader(`{"verify_key":"vk-1","remark":"owned update"}`))
	ownedClientReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		ownedClientReq.AddCookie(cookie)
	}
	ownedClientResp := httptest.NewRecorder()
	handler.ServeHTTP(ownedClientResp, ownedClientReq)
	if ownedClientResp.Code != http.StatusOK || !strings.Contains(ownedClientResp.Body.String(), "\"resource\":\"client\"") || !strings.Contains(ownedClientResp.Body.String(), "\"action\":\"update\"") {
		t.Fatalf("POST /api/clients/1/actions/update status = %d, want 200 body=%s", ownedClientResp.Code, ownedClientResp.Body.String())
	}

	editedClient, err := file.GetDb().GetClient(1)
	if err != nil {
		t.Fatalf("GetClient(1) after owned edit error = %v", err)
	}
	if editedClient.Remark != "owned update" {
		t.Fatalf("client 1 remark = %q, want owned update", editedClient.Remark)
	}

	foreignClientReq := httptest.NewRequest(http.MethodPost, "/api/clients/2/actions/update", strings.NewReader(`{"verify_key":"vk-2","remark":"foreign update"}`))
	foreignClientReq.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		foreignClientReq.AddCookie(cookie)
	}
	foreignClientResp := httptest.NewRecorder()
	handler.ServeHTTP(foreignClientResp, foreignClientReq)
	if foreignClientResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/clients/2/actions/update status = %d, want 403 body=%s", foreignClientResp.Code, foreignClientResp.Body.String())
	}

	ownedTunnelReq := httptest.NewRequest(http.MethodPost, "/api/tunnels/1/actions/stop", nil)
	for _, cookie := range cookies {
		ownedTunnelReq.AddCookie(cookie)
	}
	ownedTunnelResp := httptest.NewRecorder()
	handler.ServeHTTP(ownedTunnelResp, ownedTunnelReq)
	if ownedTunnelResp.Code != http.StatusOK || !strings.Contains(ownedTunnelResp.Body.String(), "\"resource\":\"tunnel\"") || !strings.Contains(ownedTunnelResp.Body.String(), "\"action\":\"stop\"") {
		t.Fatalf("POST /api/tunnels/1/actions/stop status = %d, want 200 body=%s", ownedTunnelResp.Code, ownedTunnelResp.Body.String())
	}

	foreignTunnelReq := httptest.NewRequest(http.MethodPost, "/api/tunnels/2/actions/stop", nil)
	for _, cookie := range cookies {
		foreignTunnelReq.AddCookie(cookie)
	}
	foreignTunnelResp := httptest.NewRecorder()
	handler.ServeHTTP(foreignTunnelResp, foreignTunnelReq)
	if foreignTunnelResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/tunnels/2/actions/stop status = %d, want 403 body=%s", foreignTunnelResp.Code, foreignTunnelResp.Body.String())
	}

	ownedHostReq := httptest.NewRequest(http.MethodPost, "/api/hosts/1/actions/stop", nil)
	for _, cookie := range cookies {
		ownedHostReq.AddCookie(cookie)
	}
	ownedHostResp := httptest.NewRecorder()
	handler.ServeHTTP(ownedHostResp, ownedHostReq)
	if ownedHostResp.Code != http.StatusOK || !strings.Contains(ownedHostResp.Body.String(), "\"resource\":\"host\"") || !strings.Contains(ownedHostResp.Body.String(), "\"action\":\"stop\"") {
		t.Fatalf("POST /api/hosts/1/actions/stop status = %d, want 200 body=%s", ownedHostResp.Code, ownedHostResp.Body.String())
	}

	foreignHostReq := httptest.NewRequest(http.MethodPost, "/api/hosts/2/actions/stop", nil)
	for _, cookie := range cookies {
		foreignHostReq.AddCookie(cookie)
	}
	foreignHostResp := httptest.NewRecorder()
	handler.ServeHTTP(foreignHostResp, foreignHostReq)
	if foreignHostResp.Code != http.StatusForbidden {
		t.Fatalf("POST /api/hosts/2/actions/stop status = %d, want 403 body=%s", foreignHostResp.Code, foreignHostResp.Body.String())
	}
}

func TestNodeReadRoutesRequireDeclaredReadPermissions(t *testing.T) {
	resetTestDB(t)
	loadRecoveredNodeConfig(t, "run_mode=node\nallow_user_login=true\nweb_username=admin\nweb_password=secret\n")
	swapRecoveredNodeStore(t, file.NewLocalStore())

	user := createTestUser(t, 1, "tenant-a", "secret")
	client := createOwnedTestClient(t, 1, user.Id, "owned client")
	createTestTunnel(t, 1, client, 10001)
	createTestHost(t, 1, client, "owned.example.com")

	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "service",
		Provider:      "local",
		SubjectID:     "service:tenant-a",
		Username:      "tenant-a-service",
		ClientIDs:     []int{client.Id},
		Roles:         []string{"custom"},
	}).Normalize())

	for _, path := range []string{
		"/api/clients?limit=10",
		"/api/clients/1",
		"/api/clients/1/connections",
		"/api/tunnels?limit=10",
		"/api/tunnels/1",
		"/api/hosts?limit=10",
		"/api/hosts/1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want 403 body=%s", path, resp.Code, resp.Body.String())
		}
	}
}
