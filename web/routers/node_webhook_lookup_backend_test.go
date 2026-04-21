package routers

import (
	"testing"

	"github.com/djylb/nps/lib/file"
	webapi "github.com/djylb/nps/web/api"
	webservice "github.com/djylb/nps/web/service"
)

type webhookLookupTestRepository struct {
	getUser   func(int) (*file.User, error)
	getClient func(int) (*file.Client, error)
	getTunnel func(int) (*file.Tunnel, error)
	getHost   func(int) (*file.Host, error)
}

func (r webhookLookupTestRepository) RangeClients(func(*file.Client) bool) {}
func (r webhookLookupTestRepository) RangeUsers(func(*file.User) bool)     {}
func (r webhookLookupTestRepository) RangeTunnels(func(*file.Tunnel) bool) {}
func (r webhookLookupTestRepository) RangeHosts(func(*file.Host) bool)     {}
func (r webhookLookupTestRepository) ListVisibleClients(webservice.ListClientsInput) ([]*file.Client, int) {
	return nil, 0
}
func (r webhookLookupTestRepository) NextUserID() int             { return 0 }
func (r webhookLookupTestRepository) NextClientID() int           { return 0 }
func (r webhookLookupTestRepository) NextTunnelID() int           { return 0 }
func (r webhookLookupTestRepository) NextHostID() int             { return 0 }
func (r webhookLookupTestRepository) CreateUser(*file.User) error { return nil }
func (r webhookLookupTestRepository) GetUser(id int) (*file.User, error) {
	return callWebhookLookupUser(r.getUser, id)
}
func (r webhookLookupTestRepository) GetUserByUsername(string) (*file.User, error) { return nil, nil }
func (r webhookLookupTestRepository) SaveUser(*file.User) error                    { return nil }
func (r webhookLookupTestRepository) DeleteUser(int) error                         { return nil }
func (r webhookLookupTestRepository) CreateClient(*file.Client) error              { return nil }
func (r webhookLookupTestRepository) GetClient(id int) (*file.Client, error) {
	return callWebhookLookupClient(r.getClient, id)
}
func (r webhookLookupTestRepository) SaveClient(*file.Client) error              { return nil }
func (r webhookLookupTestRepository) PersistClients() error                      { return nil }
func (r webhookLookupTestRepository) DeleteClient(int) error                     { return nil }
func (r webhookLookupTestRepository) VerifyUserName(string, int) bool            { return true }
func (r webhookLookupTestRepository) VerifyVKey(string, int) bool                { return true }
func (r webhookLookupTestRepository) ReplaceClientVKeyIndex(string, string, int) {}
func (r webhookLookupTestRepository) CreateTunnel(*file.Tunnel) error            { return nil }
func (r webhookLookupTestRepository) GetTunnel(id int) (*file.Tunnel, error) {
	return callWebhookLookupTunnel(r.getTunnel, id)
}
func (r webhookLookupTestRepository) SaveTunnel(*file.Tunnel) error { return nil }
func (r webhookLookupTestRepository) DeleteTunnelRecord(int) error  { return nil }
func (r webhookLookupTestRepository) CreateHost(*file.Host) error   { return nil }
func (r webhookLookupTestRepository) GetHost(id int) (*file.Host, error) {
	return callWebhookLookupHost(r.getHost, id)
}
func (r webhookLookupTestRepository) SaveHost(*file.Host, string) error { return nil }
func (r webhookLookupTestRepository) PersistHosts() error               { return nil }
func (r webhookLookupTestRepository) DeleteHostRecord(int) error        { return nil }
func (r webhookLookupTestRepository) HostExists(*file.Host) bool        { return false }
func (r webhookLookupTestRepository) GetGlobal() *file.Glob             { return nil }
func (r webhookLookupTestRepository) SaveGlobal(*file.Glob) error       { return nil }
func (r webhookLookupTestRepository) ClientOwnsTunnel(int, int) bool    { return false }
func (r webhookLookupTestRepository) ClientOwnsHost(int, int) bool      { return false }

func callWebhookLookupUser(fn func(int) (*file.User, error), id int) (*file.User, error) {
	if fn == nil {
		return nil, nil
	}
	return fn(id)
}

func callWebhookLookupClient(fn func(int) (*file.Client, error), id int) (*file.Client, error) {
	if fn == nil {
		return nil, nil
	}
	return fn(id)
}

func callWebhookLookupTunnel(fn func(int) (*file.Tunnel, error), id int) (*file.Tunnel, error) {
	if fn == nil {
		return nil, nil
	}
	return fn(id)
}

func callWebhookLookupHost(fn func(int) (*file.Host, error), id int) (*file.Host, error) {
	if fn == nil {
		return nil, nil
	}
	return fn(id)
}

func TestBuildNodeWebhookResourceLookupUsesStateBackendRepository(t *testing.T) {
	var userLookups, clientLookups, tunnelLookups, hostLookups int
	repo := webhookLookupTestRepository{
		getUser: func(id int) (*file.User, error) {
			userLookups++
			if id != 101 {
				t.Fatalf("GetUser(%d), want 101", id)
			}
			return &file.User{Id: id, Username: "repo-user"}, nil
		},
		getClient: func(id int) (*file.Client, error) {
			clientLookups++
			if id != 202 {
				t.Fatalf("GetClient(%d), want 202", id)
			}
			return &file.Client{Id: id, VerifyKey: "repo-client", Flow: &file.Flow{}, Cnf: &file.Config{}}, nil
		},
		getTunnel: func(id int) (*file.Tunnel, error) {
			tunnelLookups++
			if id != 303 {
				t.Fatalf("GetTunnel(%d), want 303", id)
			}
			return &file.Tunnel{Id: id}, nil
		},
		getHost: func(id int) (*file.Host, error) {
			hostLookups++
			if id != 404 {
				t.Fatalf("GetHost(%d), want 404", id)
			}
			return &file.Host{Id: id}, nil
		},
	}

	app := webapi.NewWithOptions(nil, webapi.Options{
		ConfigureServices: func(services *webservice.Services) {
			services.Backend.Repository = repo
		},
	})
	state := NewStateWithApp(app)
	defer state.Close()

	lookup := buildNodeWebhookResourceLookup(state)()
	if !lookup.UserExists(101) {
		t.Fatal("UserExists(101) = false, want true from backend repository")
	}
	if !lookup.ClientExists(202) {
		t.Fatal("ClientExists(202) = false, want true from backend repository")
	}
	if !lookup.TunnelExists(303) {
		t.Fatal("TunnelExists(303) = false, want true from backend repository")
	}
	if !lookup.HostExists(404) {
		t.Fatal("HostExists(404) = false, want true from backend repository")
	}
	if userLookups != 1 || clientLookups != 1 || tunnelLookups != 1 || hostLookups != 1 {
		t.Fatalf("lookup counts = user:%d client:%d tunnel:%d host:%d, want 1 each", userLookups, clientLookups, tunnelLookups, hostLookups)
	}
}
