package routers

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/djylb/nps/lib/common"
	"github.com/djylb/nps/lib/crypt"
	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/servercfg"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
	"github.com/gin-gonic/gin"
)

func TestProtectedRouteInvalidatesDisabledUserSessionOnNextRequest(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	webservice.SharedLoginPolicy().RemoveAllBans()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	user := createTestUser(t, 1101, "tenant-disable", "tenant-secret")
	client := createOwnedTestClient(t, 601, user.Id, "tenant-disable-client")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + user.Username,
		Username:      user.Username,
		ClientIDs:     []int{client.Id},
		Attributes: map[string]string{
			"user_id":    fmt.Sprintf("%d", user.Id),
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())

	beforeReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range cookies {
		beforeReq.AddCookie(cookie)
	}
	beforeResp := httptest.NewRecorder()
	handler.ServeHTTP(beforeResp, beforeReq)
	var beforeItems []map[string]interface{}
	beforeMeta := decodeManagementData(t, beforeResp.Body.Bytes(), &beforeItems)
	if beforeResp.Code != http.StatusOK || beforeMeta.Pagination == nil || beforeMeta.Pagination.Total != 1 {
		t.Fatalf("GET /api/clients before disabling user status=%d body=%s", beforeResp.Code, beforeResp.Body.String())
	}

	savedUser, err := file.GetDb().GetUser(user.Id)
	if err != nil {
		t.Fatalf("GetUser(%d) error = %v", user.Id, err)
	}
	savedUser.Status = 0
	if err := file.GetDb().UpdateUser(savedUser); err != nil {
		t.Fatalf("UpdateUser(%d) error = %v", user.Id, err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range cookies {
		afterReq.AddCookie(cookie)
	}
	afterResp := httptest.NewRecorder()
	handler.ServeHTTP(afterResp, afterReq)
	if afterResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/clients after disabling user status=%d, want 401 body=%s", afterResp.Code, afterResp.Body.String())
	}
	if body := afterResp.Body.String(); !strings.Contains(body, "\"code\":\"unauthorized\"") {
		t.Fatalf("GET /api/clients after disabling user should clear session and return unauthorized, got %s", body)
	}
}

func TestProtectedRouteRejectsSessionWhenRefreshFails(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	webservice.SharedLoginPolicy().RemoveAllBans()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	user := createTestUser(t, 1102, "tenant-refresh-error", "tenant-secret")
	client := createOwnedTestClient(t, 611, user.Id, "tenant-refresh-error-client")
	backendErr := errors.New("session refresh lookup failed")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	app := webapi.NewWithOptions(servercfg.Current(), webapi.Options{
		Services: &webservice.Services{
			Backend: webservice.Backend{
				Repository: authFailureRouterRepo{
					Repository: webservice.DefaultBackend().Repository,
					getUser: func(id int) (*file.User, error) {
						if id != user.Id {
							t.Fatalf("GetUser(%d), want %d", id, user.Id)
						}
						return nil, backendErr
					},
				},
			},
		},
	})
	handler := buildEngine(NewStateWithApp(app))
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + user.Username,
		Username:      user.Username,
		ClientIDs:     []int{client.Id},
		Attributes: map[string]string{
			"user_id":    fmt.Sprintf("%d", user.Id),
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())

	req := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("GET /api/clients with broken session refresh status=%d, want 500 body=%s", resp.Code, resp.Body.String())
	}
	if body := resp.Body.String(); !strings.Contains(body, "\"code\":\"request_failed\"") || !strings.Contains(body, backendErr.Error()) {
		t.Fatalf("GET /api/clients with broken session refresh body=%s, want formal request_failed", body)
	}
}

func TestProtectedRouteRefreshesGrantedManagerClientScopeWithoutRelogin(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	webservice.SharedLoginPolicy().RemoveAllBans()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	managerUser := createTestUser(t, 1201, "tenant-manager", "tenant-secret")
	ownerUser := createTestUser(t, 1202, "tenant-owner", "owner-secret")
	client := createOwnedTestClient(t, 602, ownerUser.Id, "managed-later")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()
	cookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + managerUser.Username,
		Username:      managerUser.Username,
		Attributes: map[string]string{
			"user_id":    fmt.Sprintf("%d", managerUser.Id),
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())

	beforeReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range cookies {
		beforeReq.AddCookie(cookie)
	}
	beforeResp := httptest.NewRecorder()
	handler.ServeHTTP(beforeResp, beforeReq)
	var beforeItems []map[string]interface{}
	beforeMeta := decodeManagementData(t, beforeResp.Body.Bytes(), &beforeItems)
	if beforeResp.Code != http.StatusOK || beforeMeta.Pagination == nil || beforeMeta.Pagination.Total != 0 {
		t.Fatalf("GET /api/clients before manager grant status=%d body=%s", beforeResp.Code, beforeResp.Body.String())
	}

	savedClient, err := file.GetDb().GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", client.Id, err)
	}
	savedClient.ManagerUserIDs = []int{managerUser.Id}
	if err := file.GetDb().UpdateClient(savedClient); err != nil {
		t.Fatalf("UpdateClient(%d) error = %v", client.Id, err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range cookies {
		afterReq.AddCookie(cookie)
	}
	afterResp := httptest.NewRecorder()
	handler.ServeHTTP(afterResp, afterReq)
	var afterItems []map[string]interface{}
	afterMeta := decodeManagementData(t, afterResp.Body.Bytes(), &afterItems)
	if afterResp.Code != http.StatusOK || afterMeta.Pagination == nil || afterMeta.Pagination.Total != 1 {
		t.Fatalf("GET /api/clients after manager grant status=%d body=%s", afterResp.Code, afterResp.Body.String())
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/clients/"+fmt.Sprintf("%d", client.Id), nil)
	for _, cookie := range cookies {
		detailReq.AddCookie(cookie)
	}
	detailResp := httptest.NewRecorder()
	handler.ServeHTTP(detailResp, detailReq)
	var detailItem struct {
		ID int `json:"id"`
	}
	decodeManagementData(t, detailResp.Body.Bytes(), &detailItem)
	if detailResp.Code != http.StatusOK || detailItem.ID != client.Id {
		t.Fatalf("GET /api/clients/%d after manager grant status=%d body=%s", client.Id, detailResp.Code, detailResp.Body.String())
	}
}

func TestProtectedRouteRefreshesOwnerTransferForExistingSessions(t *testing.T) {
	repoRoot := testRepoRoot(t)
	oldConfPath := common.ConfPath
	common.ConfPath = repoRoot
	defer func() {
		common.ConfPath = oldConfPath
	}()

	resetTestDB(t)
	webservice.SharedLoginPolicy().RemoveAllBans()
	configPath := writeTestConfig(t, "nps.conf", strings.Join([]string{
		"web_username=admin",
		"web_password=secret",
		"web_base_url=",
		"allow_user_login=true",
		"open_captcha=false",
		"secure_mode=false",
	}, "\n")+"\n")
	if err := servercfg.Load(configPath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	oldOwner := createTestUser(t, 1301, "owner-old", "owner-old-secret")
	newOwner := createTestUser(t, 1302, "owner-new", "owner-new-secret")
	client := createOwnedTestClient(t, 603, oldOwner.Id, "owner-transfer")

	crypt.InitTls(tls.Certificate{})
	gin.SetMode(gin.TestMode)
	handler := Init()
	oldOwnerCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + oldOwner.Username,
		Username:      oldOwner.Username,
		ClientIDs:     []int{client.Id},
		Attributes: map[string]string{
			"user_id":    fmt.Sprintf("%d", oldOwner.Id),
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())
	newOwnerCookies := sessionCookiesFromIdentity(t, servercfg.Current(), (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "local",
		SubjectID:     "user:" + newOwner.Username,
		Username:      newOwner.Username,
		Attributes: map[string]string{
			"user_id":    fmt.Sprintf("%d", newOwner.Id),
			"login_mode": "password",
		},
		Roles: []string{webservice.RoleUser},
	}).Normalize())

	oldOwnerBeforeReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range oldOwnerCookies {
		oldOwnerBeforeReq.AddCookie(cookie)
	}
	oldOwnerBefore := httptest.NewRecorder()
	handler.ServeHTTP(oldOwnerBefore, oldOwnerBeforeReq)
	var oldOwnerBeforeItems []map[string]interface{}
	oldOwnerBeforeMeta := decodeManagementData(t, oldOwnerBefore.Body.Bytes(), &oldOwnerBeforeItems)
	if oldOwnerBefore.Code != http.StatusOK || oldOwnerBeforeMeta.Pagination == nil || oldOwnerBeforeMeta.Pagination.Total != 1 {
		t.Fatalf("GET /api/clients for old owner before transfer status=%d body=%s", oldOwnerBefore.Code, oldOwnerBefore.Body.String())
	}
	newOwnerBeforeReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range newOwnerCookies {
		newOwnerBeforeReq.AddCookie(cookie)
	}
	newOwnerBefore := httptest.NewRecorder()
	handler.ServeHTTP(newOwnerBefore, newOwnerBeforeReq)
	var newOwnerBeforeItems []map[string]interface{}
	newOwnerBeforeMeta := decodeManagementData(t, newOwnerBefore.Body.Bytes(), &newOwnerBeforeItems)
	if newOwnerBefore.Code != http.StatusOK || newOwnerBeforeMeta.Pagination == nil || newOwnerBeforeMeta.Pagination.Total != 0 {
		t.Fatalf("GET /api/clients for new owner before transfer status=%d body=%s", newOwnerBefore.Code, newOwnerBefore.Body.String())
	}

	savedClient, err := file.GetDb().GetClient(client.Id)
	if err != nil {
		t.Fatalf("GetClient(%d) error = %v", client.Id, err)
	}
	savedClient.SetOwnerUserID(newOwner.Id)
	savedClient.ManagerUserIDs = nil
	if err := file.GetDb().UpdateClient(savedClient); err != nil {
		t.Fatalf("UpdateClient(%d) error = %v", client.Id, err)
	}

	oldOwnerAfterReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range oldOwnerCookies {
		oldOwnerAfterReq.AddCookie(cookie)
	}
	oldOwnerAfter := httptest.NewRecorder()
	handler.ServeHTTP(oldOwnerAfter, oldOwnerAfterReq)
	var oldOwnerAfterItems []map[string]interface{}
	oldOwnerAfterMeta := decodeManagementData(t, oldOwnerAfter.Body.Bytes(), &oldOwnerAfterItems)
	if oldOwnerAfter.Code != http.StatusOK || oldOwnerAfterMeta.Pagination == nil || oldOwnerAfterMeta.Pagination.Total != 0 {
		t.Fatalf("GET /api/clients for old owner after transfer status=%d body=%s", oldOwnerAfter.Code, oldOwnerAfter.Body.String())
	}
	newOwnerAfterReq := httptest.NewRequest(http.MethodGet, "/api/clients?offset=0&limit=0", nil)
	for _, cookie := range newOwnerCookies {
		newOwnerAfterReq.AddCookie(cookie)
	}
	newOwnerAfter := httptest.NewRecorder()
	handler.ServeHTTP(newOwnerAfter, newOwnerAfterReq)
	var newOwnerAfterItems []map[string]interface{}
	newOwnerAfterMeta := decodeManagementData(t, newOwnerAfter.Body.Bytes(), &newOwnerAfterItems)
	if newOwnerAfter.Code != http.StatusOK || newOwnerAfterMeta.Pagination == nil || newOwnerAfterMeta.Pagination.Total != 1 {
		t.Fatalf("GET /api/clients for new owner after transfer status=%d body=%s", newOwnerAfter.Code, newOwnerAfter.Body.String())
	}

	oldOwnerDetailReq := httptest.NewRequest(http.MethodGet, "/api/clients/"+fmt.Sprintf("%d", client.Id), nil)
	for _, cookie := range oldOwnerCookies {
		oldOwnerDetailReq.AddCookie(cookie)
	}
	oldOwnerDetail := httptest.NewRecorder()
	handler.ServeHTTP(oldOwnerDetail, oldOwnerDetailReq)
	if oldOwnerDetail.Code != http.StatusForbidden {
		t.Fatalf("GET /api/clients/%d for old owner after transfer status=%d, want 403 body=%s", client.Id, oldOwnerDetail.Code, oldOwnerDetail.Body.String())
	}
	newOwnerDetailReq := httptest.NewRequest(http.MethodGet, "/api/clients/"+fmt.Sprintf("%d", client.Id), nil)
	for _, cookie := range newOwnerCookies {
		newOwnerDetailReq.AddCookie(cookie)
	}
	newOwnerDetail := httptest.NewRecorder()
	handler.ServeHTTP(newOwnerDetail, newOwnerDetailReq)
	var newOwnerDetailItem struct {
		ID int `json:"id"`
	}
	decodeManagementData(t, newOwnerDetail.Body.Bytes(), &newOwnerDetailItem)
	if newOwnerDetail.Code != http.StatusOK || newOwnerDetailItem.ID != client.Id {
		t.Fatalf("GET /api/clients/%d for new owner after transfer status=%d body=%s", client.Id, newOwnerDetail.Code, newOwnerDetail.Body.String())
	}
}
