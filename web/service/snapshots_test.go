package service

import (
	"testing"

	"github.com/djylb/nps/lib/file"
	"github.com/djylb/nps/lib/rate"
)

func TestCloneClientSnapshotRedactsAttachedOwnerSecrets(t *testing.T) {
	owner := &file.User{
		Id:             7,
		Username:       "tenant",
		Password:       "secret-password",
		TOTPSecret:     "SECRET123",
		ExpireAt:       12345,
		FlowLimit:      67890,
		TotalFlow:      &file.Flow{InletFlow: 11, ExportFlow: 22},
		TotalTraffic:   &file.TrafficStats{},
		MaxClients:     1,
		MaxTunnels:     2,
		MaxHosts:       3,
		MaxConnections: 4,
	}
	owner.EnsureRuntimeTraffic()
	owner.TotalTraffic.Add(33, 44)

	client := &file.Client{
		Id:          9,
		OwnerUserID: owner.Id,
		VerifyKey:   "vk-9",
		Status:      true,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	client.BindOwnerUser(owner)

	cloned := CloneClientSnapshot(client)
	if cloned == nil || cloned.OwnerUser() == nil {
		t.Fatal("CloneClientSnapshot() owner snapshot = nil, want attached owner")
	}
	if cloned.OwnerUser() == owner {
		t.Fatal("CloneClientSnapshot() owner snapshot should be detached from the live user")
	}
	if cloned.OwnerUser().Password != "" || cloned.OwnerUser().TOTPSecret != "" {
		t.Fatalf("CloneClientSnapshot() owner secrets = password:%q totp:%q, want redacted", cloned.OwnerUser().Password, cloned.OwnerUser().TOTPSecret)
	}
	if cloned.EffectiveExpireAt() != owner.ExpireAt {
		t.Fatalf("EffectiveExpireAt() = %d, want %d", cloned.EffectiveExpireAt(), owner.ExpireAt)
	}
	if cloned.EffectiveFlowLimitBytes() != owner.FlowLimit {
		t.Fatalf("EffectiveFlowLimitBytes() = %d, want %d", cloned.EffectiveFlowLimitBytes(), owner.FlowLimit)
	}
	cloned.OwnerUser().ExpireAt++
	if owner.ExpireAt != 12345 {
		t.Fatalf("live owner mutated = %d, want 12345", owner.ExpireAt)
	}
}

func TestCloneClientSnapshotDetachesRuntimeLimitersAndMeters(t *testing.T) {
	owner := &file.User{
		Id:           17,
		Username:     "tenant",
		RateLimit:    64,
		TotalFlow:    &file.Flow{},
		TotalTraffic: &file.TrafficStats{},
	}
	file.InitializeUserRuntime(owner)

	client := &file.Client{
		Id:          19,
		OwnerUserID: owner.Id,
		VerifyKey:   "vk-19",
		Status:      true,
		RateLimit:   32,
		Cnf:         &file.Config{},
		Flow:        &file.Flow{},
	}
	file.InitializeClientRuntime(client)
	client.Rate = rate.NewRate(32 * 1024)
	client.Rate.Start()
	client.BindOwnerUser(owner)

	cloned := CloneClientSnapshot(client)
	if cloned == nil {
		t.Fatal("CloneClientSnapshot() = nil, want detached snapshot")
	}
	if cloned.Rate == client.Rate {
		t.Fatal("CloneClientSnapshot() should detach client rate limiter")
	}
	if cloned.BridgeMeter == client.BridgeMeter || cloned.ServiceMeter == client.ServiceMeter || cloned.TotalMeter == client.TotalMeter {
		t.Fatal("CloneClientSnapshot() should detach client runtime meters")
	}
	if cloned.OwnerUser() == nil {
		t.Fatal("CloneClientSnapshot() owner snapshot = nil, want detached owner")
	}
	if cloned.OwnerUser().Rate == owner.Rate || cloned.OwnerUser().TotalMeter == owner.TotalMeter {
		t.Fatal("CloneClientSnapshot() should detach attached owner runtime pointers")
	}

	cloned.Rate.ResetLimit(0)
	if got := client.Rate.Limit(); got != 32*1024 {
		t.Fatalf("live client rate limit after clone mutation = %d, want %d", got, 32*1024)
	}
	cloned.OwnerUser().Rate.ResetLimit(0)
	if got := owner.Rate.Limit(); got != 64*1024 {
		t.Fatalf("live owner rate limit after clone mutation = %d, want %d", got, 64*1024)
	}
}
