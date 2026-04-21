package api

import (
	"context"
	"testing"

	webservice "github.com/djylb/nps/web/service"
)

type sessionIdentityTestContext struct {
	session map[string]interface{}
	actor   *Actor
}

type sessionIdentityResolver struct {
	normalizeIdentity func(*webservice.SessionIdentity) *webservice.SessionIdentity
}

func (c *sessionIdentityTestContext) BaseContext() context.Context        { return context.Background() }
func (c *sessionIdentityTestContext) String(string) string                { return "" }
func (c *sessionIdentityTestContext) LookupString(string) (string, bool)  { return "", false }
func (c *sessionIdentityTestContext) Int(string, ...int) int              { return 0 }
func (c *sessionIdentityTestContext) Bool(string, ...bool) bool           { return false }
func (c *sessionIdentityTestContext) Method() string                      { return "" }
func (c *sessionIdentityTestContext) Host() string                        { return "" }
func (c *sessionIdentityTestContext) RemoteAddr() string                  { return "" }
func (c *sessionIdentityTestContext) ClientIP() string                    { return "" }
func (c *sessionIdentityTestContext) RequestHeader(string) string         { return "" }
func (c *sessionIdentityTestContext) SessionValue(key string) interface{} { return c.session[key] }
func (c *sessionIdentityTestContext) SetSessionValue(key string, value interface{}) {
	c.session[key] = value
}
func (c *sessionIdentityTestContext) DeleteSessionValue(key string)    { delete(c.session, key) }
func (c *sessionIdentityTestContext) SetParam(string, string)          {}
func (c *sessionIdentityTestContext) RespondJSON(int, interface{})     {}
func (c *sessionIdentityTestContext) RespondString(int, string)        {}
func (c *sessionIdentityTestContext) RespondData(int, string, []byte)  {}
func (c *sessionIdentityTestContext) Redirect(int, string)             {}
func (c *sessionIdentityTestContext) SetResponseHeader(string, string) {}
func (c *sessionIdentityTestContext) IsWritten() bool                  { return false }
func (c *sessionIdentityTestContext) Actor() *Actor                    { return c.actor }
func (c *sessionIdentityTestContext) SetActor(actor *Actor)            { c.actor = actor }
func (c *sessionIdentityTestContext) Metadata() RequestMetadata        { return RequestMetadata{} }

func (r sessionIdentityResolver) NormalizePrincipal(principal webservice.Principal) webservice.Principal {
	return webservice.DefaultPermissionResolver().NormalizePrincipal(principal)
}

func (r sessionIdentityResolver) NormalizeIdentity(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
	if r.normalizeIdentity != nil {
		if identity = r.normalizeIdentity(identity); identity == nil {
			return nil
		}
	}
	return webservice.DefaultPermissionResolver().NormalizeIdentity(identity)
}

func (r sessionIdentityResolver) KnownRoles() []string {
	return webservice.DefaultPermissionResolver().KnownRoles()
}

func (r sessionIdentityResolver) KnownPermissions() []string {
	return webservice.DefaultPermissionResolver().KnownPermissions()
}

func (r sessionIdentityResolver) PermissionCatalog() map[string][]string {
	return webservice.DefaultPermissionResolver().PermissionCatalog()
}

func TestCurrentSessionIdentityWithResolverIgnoresLegacySessionFields(t *testing.T) {
	ctx := &sessionIdentityTestContext{
		session: map[string]interface{}{
			"auth":      true,
			"isAdmin":   false,
			"username":  "tenant",
			"clientIds": []int{4, 8},
		},
	}

	if identity := currentSessionIdentityWithResolver(ctx, webservice.DefaultPermissionResolver()); identity != nil {
		t.Fatalf("currentSessionIdentityWithResolver() = %+v, want nil without encoded session identity", identity)
	}
}

func TestApplySessionIdentityPersistsEncodedSessionIdentityOnly(t *testing.T) {
	ctx := &sessionIdentityTestContext{
		session: map[string]interface{}{},
	}
	identity := (&webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:alice",
		Username:      "alice",
		ClientIDs:     []int{7, 9},
		Roles:         []string{webservice.RoleUser},
	}).Normalize()

	ApplySessionIdentity(ctx, identity)

	raw, _ := ctx.session[webservice.SessionIdentityKey].(string)
	if raw == "" {
		t.Fatal("ApplySessionIdentity() should persist encoded session_identity")
	}
	if ctx.session["auth"] != nil || ctx.session["isAdmin"] != nil || ctx.session["username"] != nil ||
		ctx.session["clientId"] != nil || ctx.session["clientIds"] != nil {
		t.Fatalf("ApplySessionIdentity() should not persist legacy session fields, got %#v", ctx.session)
	}
	if ctx.actor == nil || ctx.actor.Username != "alice" {
		t.Fatalf("ApplySessionIdentity() actor = %+v, want alice actor", ctx.actor)
	}
}

func TestApplySessionIdentityWithResolverPersistsResolverNormalizedIdentity(t *testing.T) {
	ctx := &sessionIdentityTestContext{
		session: map[string]interface{}{},
	}
	resolver := sessionIdentityResolver{
		normalizeIdentity: func(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
			if identity == nil {
				return nil
			}
			cloned := *identity
			if cloned.Username == "mapped" {
				cloned.ClientIDs = []int{55}
				cloned.Permissions = append([]string(nil), webservice.PermissionHostsUpdate)
				cloned.Attributes = map[string]string{"mapped": "true"}
			}
			return &cloned
		},
	}

	ApplySessionIdentityWithResolver(ctx, &webservice.SessionIdentity{
		Version:       webservice.SessionIdentityVersion,
		Authenticated: true,
		Kind:          "user",
		Provider:      "test",
		SubjectID:     "user:mapped",
		Username:      "mapped",
		Roles:         []string{webservice.RoleUser},
	}, resolver)

	raw, _ := ctx.session[webservice.SessionIdentityKey].(string)
	if raw == "" {
		t.Fatal("ApplySessionIdentityWithResolver() should persist encoded session_identity")
	}
	parsed, err := webservice.ParseSessionIdentityWithResolver(raw, webservice.DefaultPermissionResolver())
	if err != nil {
		t.Fatalf("ParseSessionIdentityWithResolver() error = %v", err)
	}
	if parsed == nil || len(parsed.ClientIDs) != 1 || parsed.ClientIDs[0] != 55 {
		t.Fatalf("ApplySessionIdentityWithResolver() parsed identity = %+v, want resolver-shaped client scope", parsed)
	}
	if ctx.actor == nil || len(ctx.actor.ClientIDs) != 1 || ctx.actor.ClientIDs[0] != 55 {
		t.Fatalf("ApplySessionIdentityWithResolver() actor = %+v, want resolver-shaped actor scope", ctx.actor)
	}
}

func TestApplyActorSessionMapPersistsEncodedSessionIdentityOnly(t *testing.T) {
	session := map[string]interface{}{
		"auth":      true,
		"isAdmin":   false,
		"username":  "legacy",
		"clientId":  4,
		"clientIds": []int{4, 8},
	}

	ApplyActorSessionMap(session, UserActor("alice", []int{7, 9}), "ws")

	raw, _ := session[webservice.SessionIdentityKey].(string)
	if raw == "" {
		t.Fatal("ApplyActorSessionMap() should persist encoded session_identity")
	}
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, webservice.DefaultPermissionResolver())
	if err != nil {
		t.Fatalf("ParseSessionIdentityWithResolver() error = %v", err)
	}
	if identity == nil || !identity.Authenticated || identity.Username != "alice" || identity.Provider != "ws" {
		t.Fatalf("ApplyActorSessionMap() parsed identity = %+v, want authenticated ws identity for alice", identity)
	}
	for _, key := range []string{"auth", "isAdmin", "username", "clientId", "clientIds"} {
		if got := session[key]; got != nil {
			t.Fatalf("ApplyActorSessionMap() %s = %#v, want nil", key, got)
		}
	}
}

func TestApplyActorSessionMapWithResolverPersistsResolverNormalizedIdentity(t *testing.T) {
	session := map[string]interface{}{}
	resolver := sessionIdentityResolver{
		normalizeIdentity: func(identity *webservice.SessionIdentity) *webservice.SessionIdentity {
			if identity == nil {
				return nil
			}
			cloned := *identity
			if cloned.Username == "mapped" {
				cloned.ClientIDs = []int{88}
				cloned.Permissions = []string{webservice.PermissionHostsUpdate}
				cloned.Attributes = map[string]string{"mapped": "true"}
			}
			return &cloned
		},
	}

	ApplyActorSessionMapWithResolver(session, UserActor("mapped", []int{7, 9}), "ws", resolver)

	raw, _ := session[webservice.SessionIdentityKey].(string)
	if raw == "" {
		t.Fatal("ApplyActorSessionMapWithResolver() should persist encoded session_identity")
	}
	identity, err := webservice.ParseSessionIdentityWithResolver(raw, webservice.DefaultPermissionResolver())
	if err != nil {
		t.Fatalf("ParseSessionIdentityWithResolver() error = %v", err)
	}
	if identity == nil || len(identity.ClientIDs) != 1 || identity.ClientIDs[0] != 88 {
		t.Fatalf("ApplyActorSessionMapWithResolver() parsed identity = %+v, want resolver-shaped client scope", identity)
	}
	if got := identity.Attributes["mapped"]; got != "true" {
		t.Fatalf("ApplyActorSessionMapWithResolver() mapped attribute = %q, want true", got)
	}
}

func TestApplyActorSessionMapClearsAnonymousSessionIdentity(t *testing.T) {
	session := map[string]interface{}{
		webservice.SessionIdentityKey: "stale",
		"auth":                        true,
		"clientIds":                   []int{4, 8},
	}

	ApplyActorSessionMap(session, AnonymousActor(), "ws")

	if got := session[webservice.SessionIdentityKey]; got != nil {
		t.Fatalf("ApplyActorSessionMap() session_identity = %#v, want nil", got)
	}
	for _, key := range []string{"auth", "isAdmin", "username", "clientId", "clientIds"} {
		if got := session[key]; got != nil {
			t.Fatalf("ApplyActorSessionMap() %s = %#v, want nil", key, got)
		}
	}
}
