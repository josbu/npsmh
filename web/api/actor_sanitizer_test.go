package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
	webservice "github.com/djylb/nps/web/service"
)

type hostCertSuggestionRuntimeStub struct {
	managementClientRuntimeStub
	hosts []*file.Host
}

func (s hostCertSuggestionRuntimeStub) ListHosts(offset, limit, clientID int, search, sort, order string) ([]*file.Host, int) {
	return s.hosts, len(s.hosts)
}

type managementSuggestionTestContext struct {
	managementStatusTestContext
	actor  *Actor
	params map[string]string
}

func (c *managementSuggestionTestContext) String(key string) string {
	if c == nil || c.params == nil {
		return ""
	}
	return c.params[key]
}

func (c *managementSuggestionTestContext) LookupString(key string) (string, bool) {
	if c == nil || c.params == nil {
		return "", false
	}
	value, ok := c.params[key]
	return value, ok
}

func (c *managementSuggestionTestContext) Int(key string, def ...int) int {
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

func (c *managementSuggestionTestContext) Actor() *Actor {
	if c == nil {
		return nil
	}
	return c.actor
}

func TestSanitizeNodeTunnelRedactsSecretsWithoutUpdatePermission(t *testing.T) {
	app := &App{Services: webservice.BindDefaultServices(webservice.Services{}, nil)}
	tunnel := &file.Tunnel{
		Id:       7,
		Password: "secret-pass",
		UserAuth: &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Client:   &file.Client{Id: 3, VerifyKey: "vk", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:     &file.Flow{},
	}
	actor := &Actor{
		Kind:        "custom",
		SubjectID:   "read-only",
		Roles:       []string{"restricted"},
		Permissions: []string{webservice.PermissionTunnelsRead},
	}

	sanitized := app.sanitizeNodeTunnel(actor, resolveNodeAccessScope(actor), tunnel)

	if sanitized == nil {
		t.Fatal("sanitizeNodeTunnel() = nil")
	}
	if sanitized.Password != "" {
		t.Fatalf("sanitizeNodeTunnel() password = %q, want redacted", sanitized.Password)
	}
	if sanitized.UserAuth != nil && sanitized.UserAuth.Content != "" {
		t.Fatalf("sanitizeNodeTunnel() auth = %q, want redacted", sanitized.UserAuth.Content)
	}
	if tunnel.Password != "secret-pass" || tunnel.UserAuth == nil || tunnel.UserAuth.Content != "demo:secret" {
		t.Fatal("sanitizeNodeTunnel() mutated original tunnel")
	}
}

func TestNodeClientResourceRedactsVerifyKeyWithoutUpdatePermission(t *testing.T) {
	app := &App{Services: webservice.BindDefaultServices(webservice.Services{}, nil)}
	client := &file.Client{
		Id:        12,
		VerifyKey: "vk-secret",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	actor := &Actor{
		Kind:        "user",
		SubjectID:   "read-only",
		Roles:       []string{"restricted"},
		ClientIDs:   []int{12},
		Permissions: []string{webservice.PermissionClientsRead},
	}

	payload := app.nodeClientResource(actor, client)

	if payload.VerifyKey != "" {
		t.Fatalf("nodeClientResource() verify_key = %q, want redacted", payload.VerifyKey)
	}
}

func TestNodeClientResourceKeepsVerifyKeyWithUpdatePermission(t *testing.T) {
	app := &App{Services: webservice.BindDefaultServices(webservice.Services{}, nil)}
	client := &file.Client{
		Id:        13,
		VerifyKey: "vk-secret",
		Cnf:       &file.Config{},
		Flow:      &file.Flow{},
	}
	actor := &Actor{
		Kind:        "user",
		SubjectID:   "writer",
		Roles:       []string{"restricted"},
		ClientIDs:   []int{13},
		Permissions: []string{webservice.PermissionClientsRead, webservice.PermissionClientsUpdate},
	}

	payload := app.nodeClientResource(actor, client)

	if payload.VerifyKey != "vk-secret" {
		t.Fatalf("nodeClientResource() verify_key = %q, want preserved", payload.VerifyKey)
	}
}

func TestSanitizeNodeTunnelKeepsSecretsWithUpdatePermission(t *testing.T) {
	app := &App{Services: webservice.BindDefaultServices(webservice.Services{}, nil)}
	tunnel := &file.Tunnel{
		Id:       8,
		Password: "secret-pass",
		UserAuth: &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Client:   &file.Client{Id: 4, VerifyKey: "vk", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:     &file.Flow{},
	}
	actor := &Actor{
		Kind:        "custom",
		SubjectID:   "writer",
		Roles:       []string{"restricted"},
		Permissions: []string{webservice.PermissionTunnelsRead, webservice.PermissionTunnelsUpdate},
	}

	sanitized := app.sanitizeNodeTunnel(actor, resolveNodeAccessScope(actor), tunnel)

	if sanitized.Password != "secret-pass" {
		t.Fatalf("sanitizeNodeTunnel() password = %q, want preserved", sanitized.Password)
	}
	if sanitized.UserAuth == nil || sanitized.UserAuth.Content != "demo:secret" {
		t.Fatalf("sanitizeNodeTunnel() auth = %#v, want preserved", sanitized.UserAuth)
	}
}

func TestSanitizeNodeHostRedactsSecretsWithoutUpdatePermission(t *testing.T) {
	app := &App{Services: webservice.BindDefaultServices(webservice.Services{}, nil)}
	host := &file.Host{
		Id:       9,
		CertFile: "/etc/nps/cert.pem",
		KeyFile:  "/etc/nps/key.pem",
		UserAuth: &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Client:   &file.Client{Id: 5, VerifyKey: "vk", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:     &file.Flow{},
	}
	actor := &Actor{
		Kind:        "custom",
		SubjectID:   "read-only",
		Roles:       []string{"restricted"},
		Permissions: []string{webservice.PermissionHostsRead},
	}

	sanitized := app.sanitizeNodeHost(actor, resolveNodeAccessScope(actor), host)

	if sanitized == nil {
		t.Fatal("sanitizeNodeHost() = nil")
	}
	if sanitized.CertFile != "" || sanitized.KeyFile != "" {
		t.Fatalf("sanitizeNodeHost() certs = cert=%q key=%q, want redacted", sanitized.CertFile, sanitized.KeyFile)
	}
	if sanitized.UserAuth != nil && sanitized.UserAuth.Content != "" {
		t.Fatalf("sanitizeNodeHost() auth = %q, want redacted", sanitized.UserAuth.Content)
	}
	if host.CertFile != "/etc/nps/cert.pem" || host.KeyFile != "/etc/nps/key.pem" || host.UserAuth == nil || host.UserAuth.Content != "demo:secret" {
		t.Fatal("sanitizeNodeHost() mutated original host")
	}
}

func TestSanitizeNodeHostKeepsSecretsWithUpdatePermission(t *testing.T) {
	app := &App{Services: webservice.BindDefaultServices(webservice.Services{}, nil)}
	host := &file.Host{
		Id:       10,
		CertFile: "/etc/nps/cert.pem",
		KeyFile:  "/etc/nps/key.pem",
		UserAuth: &file.MultiAccount{Content: "demo:secret", AccountMap: map[string]string{"demo": "secret"}},
		Client:   &file.Client{Id: 6, VerifyKey: "vk", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:     &file.Flow{},
	}
	actor := &Actor{
		Kind:        "custom",
		SubjectID:   "writer",
		Roles:       []string{"restricted"},
		Permissions: []string{webservice.PermissionHostsRead, webservice.PermissionHostsUpdate},
	}

	sanitized := app.sanitizeNodeHost(actor, resolveNodeAccessScope(actor), host)

	if sanitized.CertFile != "/etc/nps/cert.pem" || sanitized.KeyFile != "/etc/nps/key.pem" {
		t.Fatalf("sanitizeNodeHost() certs = cert=%q key=%q, want preserved", sanitized.CertFile, sanitized.KeyFile)
	}
	if sanitized.UserAuth == nil || sanitized.UserAuth.Content != "demo:secret" {
		t.Fatalf("sanitizeNodeHost() auth = %#v, want preserved", sanitized.UserAuth)
	}
}

func TestNodeHostCertSuggestionRequiresHostUpdatePermissionForCertMaterial(t *testing.T) {
	cert, key := generateNodeTestCertificatePair(t, time.Now().Add(2*time.Hour))
	host := &file.Host{
		Id:       11,
		Host:     "*.example.com",
		Scheme:   "https",
		CertType: "file",
		CertFile: cert,
		KeyFile:  key,
		Client:   &file.Client{Id: 3, VerifyKey: "vk", Cnf: &file.Config{}, Flow: &file.Flow{}},
		Flow:     &file.Flow{},
	}
	app := &App{
		Services: webservice.BindDefaultServices(webservice.Services{
			Backend: webservice.Backend{
				Runtime: hostCertSuggestionRuntimeStub{hosts: []*file.Host{host}},
			},
		}, nil),
	}

	readOnlyActor := &Actor{
		Kind:        "user",
		SubjectID:   "read-only",
		Roles:       []string{"restricted"},
		ClientIDs:   []int{3},
		Permissions: []string{webservice.PermissionHostsRead},
	}
	readOnlyCtx := &managementSuggestionTestContext{
		actor:  readOnlyActor,
		params: map[string]string{"host": "api.example.com"},
	}
	app.NodeHostCertSuggestion(readOnlyCtx)
	if readOnlyCtx.status != 200 {
		t.Fatalf("NodeHostCertSuggestion() read-only status = %d, want 200", readOnlyCtx.status)
	}
	readOnlyResp, ok := readOnlyCtx.body.(ManagementDataResponse)
	if !ok {
		t.Fatalf("read-only response = %#v, want ManagementDataResponse", readOnlyCtx.body)
	}
	readOnlyPayload, ok := readOnlyResp.Data.(nodeHostCertSuggestionPayload)
	if !ok {
		t.Fatalf("read-only payload = %#v, want nodeHostCertSuggestionPayload", readOnlyResp.Data)
	}
	if readOnlyPayload.CanApplyToForm {
		t.Fatalf("read-only payload = %#v, want can_apply_to_form=false", readOnlyPayload)
	}
	if readOnlyPayload.CertFile != "" || readOnlyPayload.KeyFile != "" {
		t.Fatalf("read-only payload leaked cert material: %#v", readOnlyPayload)
	}

	updateActor := &Actor{
		Kind:        "user",
		SubjectID:   "writer",
		Roles:       []string{"restricted"},
		ClientIDs:   []int{3},
		Permissions: []string{webservice.PermissionHostsRead, webservice.PermissionHostsUpdate},
	}
	updateCtx := &managementSuggestionTestContext{
		actor:  updateActor,
		params: map[string]string{"host": "api.example.com"},
	}
	app.NodeHostCertSuggestion(updateCtx)
	if updateCtx.status != 200 {
		t.Fatalf("NodeHostCertSuggestion() update status = %d, want 200", updateCtx.status)
	}
	updateResp, ok := updateCtx.body.(ManagementDataResponse)
	if !ok {
		t.Fatalf("update response = %#v, want ManagementDataResponse", updateCtx.body)
	}
	updatePayload, ok := updateResp.Data.(nodeHostCertSuggestionPayload)
	if !ok {
		t.Fatalf("update payload = %#v, want nodeHostCertSuggestionPayload", updateResp.Data)
	}
	if !updatePayload.CanApplyToForm {
		t.Fatalf("update payload = %#v, want can_apply_to_form=true", updatePayload)
	}
	if updatePayload.CertFile == "" || updatePayload.KeyFile == "" {
		t.Fatalf("update payload missing cert material: %#v", updatePayload)
	}
}
