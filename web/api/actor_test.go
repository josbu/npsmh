package api

import (
	"testing"

	webservice "github.com/djylb/nps/web/service"
)

func TestActorCanAccessClient(t *testing.T) {
	actor := UserActor("demo", []int{3, 7})
	if !ActorCanAccessClient(actor, 3) {
		t.Fatal("expected actor to access client 3")
	}
	if !ActorCanAccessClient(actor, 7) {
		t.Fatal("expected actor to access client 7")
	}
	if ActorCanAccessClient(actor, 9) {
		t.Fatal("expected actor to reject client 9")
	}
	if len(actor.Permissions) == 0 {
		t.Fatal("expected default user permissions to be populated")
	}
}

func TestActorPrimaryClientID(t *testing.T) {
	actor := UserActor("demo", []int{0, 5, 8})
	clientID, ok := ActorPrimaryClientID(actor)
	if !ok || clientID != 5 {
		t.Fatalf("ActorPrimaryClientID() = %d, %v, want 5, true", clientID, ok)
	}
}

func TestActorSingleClientID(t *testing.T) {
	if clientID, ok := ActorSingleClientID(UserActor("demo", []int{0, 5})); !ok || clientID != 5 {
		t.Fatalf("ActorSingleClientID(single) = %d, %v, want 5, true", clientID, ok)
	}
	if clientID, ok := ActorSingleClientID(UserActor("demo", []int{5, 8})); ok || clientID != 0 {
		t.Fatalf("ActorSingleClientID(multi) = %d, %v, want 0, false", clientID, ok)
	}
}

func TestActorFromSessionIdentity(t *testing.T) {
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "user",
		SubjectID:     "user:demo",
		Username:      "demo",
		ClientIDs:     []int{7, 7, 2},
		Roles:         []string{webservice.RoleUser},
		Attributes:    map[string]string{"scope": "management"},
	}).Normalize()

	actor := ActorFromSessionIdentity(identity)
	if actor.SubjectID != "user:demo" {
		t.Fatalf("SubjectID = %q, want user:demo", actor.SubjectID)
	}
	if len(actor.ClientIDs) != 2 || actor.ClientIDs[0] != 7 || actor.ClientIDs[1] != 2 {
		t.Fatalf("ClientIDs = %v, want [7 2]", actor.ClientIDs)
	}
	if actor.Attributes["scope"] != "management" {
		t.Fatalf("Attributes = %v, want scope=management", actor.Attributes)
	}
	if len(actor.Permissions) == 0 {
		t.Fatal("expected permissions to round-trip from session identity")
	}
}

func TestAdminActorWithFallback(t *testing.T) {
	actor := AdminActorWithFallback("", "configured-admin")
	if actor.Username != "configured-admin" {
		t.Fatalf("Username = %q, want configured-admin", actor.Username)
	}
	if !actor.IsAdmin || actor.SubjectID != "admin:configured-admin" {
		t.Fatalf("unexpected admin actor: %+v", actor)
	}
}

func TestActorFromSessionIdentityWithFallback(t *testing.T) {
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "admin",
		IsAdmin:       true,
	}).Normalize()

	actor := ActorFromSessionIdentityWithFallback(identity, "configured-admin")
	if actor.Username != "configured-admin" {
		t.Fatalf("Username = %q, want configured-admin", actor.Username)
	}
	if actor.SubjectID != "admin:configured-admin" {
		t.Fatalf("SubjectID = %q, want admin:configured-admin", actor.SubjectID)
	}
}

func TestActorFromSessionIdentityPreservesClientKind(t *testing.T) {
	identity := (&webservice.SessionIdentity{
		Authenticated: true,
		Kind:          "client",
		SubjectID:     "client:vkey:3",
		Username:      "vkey-client-3",
		ClientIDs:     []int{3},
		Roles:         []string{webservice.RoleClient},
	}).Normalize()

	actor := ActorFromSessionIdentity(identity)
	if actor.Kind != "client" {
		t.Fatalf("Kind = %q, want client", actor.Kind)
	}
	if len(actor.ClientIDs) != 1 || actor.ClientIDs[0] != 3 {
		t.Fatalf("ClientIDs = %v, want [3]", actor.ClientIDs)
	}
}

func TestNodeClientVisibilityNormalizesActorClientIDsWithoutRepositoryLookup(t *testing.T) {
	services := webservice.New()
	services.Backend.Repository = stubVisibilityRepository{
		Repository: services.Backend.Repository,
		getManagedClientIDs: func(userID int) ([]int, error) {
			t.Fatalf("GetManagedClientIDsByUserID(%d) should not be called when actor already carries client ids", userID)
			return nil, nil
		},
	}
	app := &App{Services: webservice.BindDefaultServices(services, nil)}
	actor := &Actor{
		Kind:      "user",
		ClientIDs: []int{9, 0, 3, 9},
		Attributes: map[string]string{
			"user_id": "8",
		},
	}

	visibility, ok, err := app.nodeClientVisibility(actor, webservice.NodeAccessScope{})
	if err != nil {
		t.Fatalf("nodeClientVisibility() error = %v, want nil", err)
	}
	if !ok {
		t.Fatal("nodeClientVisibility() ok = false, want true")
	}
	if visibility.PrimaryClientID != 3 {
		t.Fatalf("visibility.PrimaryClientID = %d, want 3", visibility.PrimaryClientID)
	}
	if len(visibility.ClientIDs) != 2 || visibility.ClientIDs[0] != 3 || visibility.ClientIDs[1] != 9 {
		t.Fatalf("visibility.ClientIDs = %v, want [3 9]", visibility.ClientIDs)
	}
}

func TestNodeClientVisibilityUsesManagedClientLookupForUserActor(t *testing.T) {
	services := webservice.New()
	services.Backend.Repository = stubVisibilityRepository{
		Repository:              services.Backend.Repository,
		authoritativeManagedIDs: true,
		getManagedClientIDs: func(userID int) ([]int, error) {
			if userID != 8 {
				t.Fatalf("GetManagedClientIDsByUserID(%d), want 8", userID)
			}
			return []int{5, 2, 5, 0}, nil
		},
	}
	app := &App{Services: webservice.BindDefaultServices(services, nil)}
	actor := &Actor{
		Kind: "user",
		Attributes: map[string]string{
			"user_id": "8",
		},
	}

	visibility, ok, err := app.nodeClientVisibility(actor, webservice.NodeAccessScope{})
	if err != nil {
		t.Fatalf("nodeClientVisibility() error = %v, want nil", err)
	}
	if !ok {
		t.Fatal("nodeClientVisibility() ok = false, want true")
	}
	if visibility.PrimaryClientID != 2 {
		t.Fatalf("visibility.PrimaryClientID = %d, want 2", visibility.PrimaryClientID)
	}
	if len(visibility.ClientIDs) != 2 || visibility.ClientIDs[0] != 2 || visibility.ClientIDs[1] != 5 {
		t.Fatalf("visibility.ClientIDs = %v, want [2 5]", visibility.ClientIDs)
	}
}
