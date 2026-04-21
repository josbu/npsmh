package file

import "testing"

func TestNegativeConnectionLimitsNormalizeToUnlimited(t *testing.T) {
	t.Run("client", func(t *testing.T) {
		client := &Client{MaxConn: -1, Flow: &Flow{}}
		InitializeClientRuntime(client)
		if client.MaxConn != 0 {
			t.Fatalf("client.MaxConn = %d, want 0", client.MaxConn)
		}
		if !client.GetConn() {
			t.Fatal("client.GetConn() = false, want unlimited after normalization")
		}
	})

	t.Run("tunnel", func(t *testing.T) {
		tunnel := &Tunnel{MaxConn: -1, Flow: &Flow{}}
		InitializeTunnelRuntime(tunnel)
		if tunnel.MaxConn != 0 {
			t.Fatalf("tunnel.MaxConn = %d, want 0", tunnel.MaxConn)
		}
		if !tunnel.GetConn() {
			t.Fatal("tunnel.GetConn() = false, want unlimited after normalization")
		}
	})

	t.Run("host", func(t *testing.T) {
		host := &Host{MaxConn: -1, Flow: &Flow{}}
		InitializeHostRuntime(host)
		if host.MaxConn != 0 {
			t.Fatalf("host.MaxConn = %d, want 0", host.MaxConn)
		}
		if !host.GetConn() {
			t.Fatal("host.GetConn() = false, want unlimited after normalization")
		}
	})
}

func TestNegativeUserConnectionLimitNormalizesToUnlimited(t *testing.T) {
	user := &User{MaxConnections: -1, TotalFlow: &Flow{}}
	InitializeUserRuntime(user)
	if user.MaxConnections != 0 {
		t.Fatalf("user.MaxConnections = %d, want 0", user.MaxConnections)
	}
	if !user.GetConn() {
		t.Fatal("user.GetConn() = false, want unlimited after normalization")
	}
}

func TestBoundClientEffectiveLifecycleFallsBackToOwnerUser(t *testing.T) {
	owner := &User{
		Id:        9,
		ExpireAt:  1_900_000_000,
		FlowLimit: 8 * 1024 * 1024,
		TotalFlow: &Flow{},
	}
	InitializeUserRuntime(owner)
	client := &Client{
		OwnerUserID: owner.Id,
		Flow:        &Flow{},
	}
	InitializeClientRuntime(client)
	client.BindOwnerUser(owner)

	if got := client.EffectiveExpireAt(); got != owner.ExpireAt {
		t.Fatalf("client.EffectiveExpireAt() = %d, want owner expire_at %d", got, owner.ExpireAt)
	}
	if got := client.EffectiveFlowLimitBytes(); got != owner.FlowLimit {
		t.Fatalf("client.EffectiveFlowLimitBytes() = %d, want owner flow_limit %d", got, owner.FlowLimit)
	}

	client.SetExpireAt(owner.ExpireAt + 60)
	client.SetFlowLimitBytes(owner.FlowLimit / 2)
	if got := client.EffectiveExpireAt(); got != owner.ExpireAt+60 {
		t.Fatalf("client.EffectiveExpireAt() with direct limit = %d, want %d", got, owner.ExpireAt+60)
	}
	if got := client.EffectiveFlowLimitBytes(); got != owner.FlowLimit/2 {
		t.Fatalf("client.EffectiveFlowLimitBytes() with direct limit = %d, want %d", got, owner.FlowLimit/2)
	}
}
