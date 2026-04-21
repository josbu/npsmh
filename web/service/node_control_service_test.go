package service

import (
	"strconv"
	"testing"

	"github.com/djylb/nps/lib/servercfg"
)

func testNodeControlConfig() *servercfg.Snapshot {
	return &servercfg.Snapshot{
		App: servercfg.AppConfig{Name: "node-a"},
		Web: servercfg.WebConfig{BaseURL: "/base"},
		Runtime: servercfg.RuntimeConfig{
			NodeBatchMaxItems:         16,
			NodeEventLogSize:          64,
			NodeTrafficReportInterval: 1,
			NodeTrafficReportStep:     10 * 1024 * 1024,
			ManagementPlatforms: []servercfg.ManagementPlatformConfig{
				{
					PlatformID:       "master-a",
					Token:            "token-a",
					Enabled:          true,
					ControlScope:     "account",
					ServiceUsername:  "svc-a",
					MasterURL:        "https://master-a.example",
					ConnectMode:      "dual",
					CallbackEnabled:  true,
					CallbackURL:      "https://master-a.example/callback",
					CallbackQueueMax: 8,
				},
				{
					PlatformID:       "master-b",
					Token:            "token-b",
					Enabled:          true,
					ControlScope:     "account",
					ServiceUsername:  "svc-b",
					MasterURL:        "https://master-b.example",
					ConnectMode:      "direct",
					CallbackEnabled:  true,
					CallbackURL:      "https://master-b.example/callback",
					CallbackQueueMax: 8,
				},
			},
		},
	}
}

func testNodePlatformAdminScope(platformID string, serviceUserID int) NodeAccessScope {
	return ResolveNodeAccessScope(testNodePlatformAdminPrincipal(platformID, serviceUserID))
}

func testNodePlatformAdminPrincipal(platformID string, serviceUserID int) Principal {
	return Principal{
		Authenticated: true,
		Kind:          "platform_admin",
		Roles:         []string{RoleUser},
		Permissions: []string{
			PermissionClientsRead,
			PermissionClientsUpdate,
		},
		Attributes: map[string]string{
			"platform_id":              platformID,
			"platform_service_user_id": strconv.Itoa(serviceUserID),
		},
	}
}

func testNodeFullAdminScope() NodeAccessScope {
	return ResolveNodeAccessScope(testNodeFullAdminPrincipal())
}

func testNodeFullAdminPrincipal() Principal {
	return Principal{
		Authenticated: true,
		Kind:          "admin",
		IsAdmin:       true,
		Roles:         []string{RoleAdmin},
		Permissions:   []string{PermissionAll},
	}
}

func TestNodeUsageSnapshotAllowsClientSecretsUsesServiceAuthorizationFallback(t *testing.T) {
	service := DefaultNodeControlService{
		Authz: DefaultAuthorizationService{
			Resolver: stubPermissionResolver{
				normalizePrincipal: func(principal Principal) Principal {
					if principal.Username == "resolver-user" {
						principal.Authenticated = true
						principal.Kind = "user"
						principal.Permissions = []string{PermissionClientsUpdate}
					}
					return principal
				},
			},
		},
	}

	if !service.usageSnapshotAllowsClientSecrets(NodeUsageSnapshotInput{
		Principal: Principal{Username: "resolver-user"},
		Scope: NodeAccessScope{
			actorKind: "user",
		},
	}) {
		t.Fatal("usageSnapshotAllowsClientSecrets() should honor the service authorization fallback")
	}
}
