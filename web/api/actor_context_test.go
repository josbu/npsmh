package api

import "testing"

func TestNodeActorHelpers(t *testing.T) {
	actor := &Actor{
		Kind:      "platform_user",
		SubjectID: "platform:acct:user-1",
		Username:  "demo-user",
		Attributes: map[string]string{
			"platform_id":              "acct-1",
			"platform_actor_id":        "actor-1",
			"platform_service_user_id": "17",
			"user_id":                  "23",
		},
	}

	if got := ActorAttribute(actor, "platform_id"); got != "acct-1" {
		t.Fatalf("ActorAttribute(platform_id) = %q, want acct-1", got)
	}
	if got, ok := ActorAttributeInt(actor, "platform_service_user_id"); !ok || got != 17 {
		t.Fatalf("ActorAttributeInt(platform_service_user_id) = %d, %v, want 17, true", got, ok)
	}
	if got := NodeActorSourceType(actor); got != "platform_user" {
		t.Fatalf("NodeActorSourceType() = %q, want platform_user", got)
	}
	if got := NodeActorPlatformID(actor); got != "acct-1" {
		t.Fatalf("NodeActorPlatformID() = %q, want acct-1", got)
	}
	if got := NodeActorID(actor); got != "actor-1" {
		t.Fatalf("NodeActorID() = %q, want actor-1", got)
	}
	if got := NodeActorSubjectID(actor); got != "platform:acct:user-1" {
		t.Fatalf("NodeActorSubjectID() = %q, want platform:acct:user-1", got)
	}
	if got := NodeActorServiceUserID(actor); got != 17 {
		t.Fatalf("NodeActorServiceUserID() = %d, want 17", got)
	}
	if got := NodeActorUserID(actor); got != 23 {
		t.Fatalf("NodeActorUserID() = %d, want 23", got)
	}
	if got := NodeOperationScope(actor); got != "user" {
		t.Fatalf("NodeOperationScope(platform_user) = %q, want user", got)
	}

	payload := NodeOperationActorPayload(actor)
	if payload.PlatformID != "acct-1" || payload.SubjectID != "platform:acct:user-1" {
		t.Fatalf("NodeOperationActorPayload() = %+v, want platform_id acct-1 and subject", payload)
	}
}

func TestNodeActorHelpersFallbacks(t *testing.T) {
	admin := &Actor{Kind: "admin", Username: "root", IsAdmin: true}
	if got := NodeActorSourceType(admin); got != "node_admin" {
		t.Fatalf("NodeActorSourceType(admin) = %q, want node_admin", got)
	}
	if got := NodeActorID(admin); got != "root" {
		t.Fatalf("NodeActorID(admin) = %q, want root", got)
	}
	if got := NodeOperationScope(admin); got != "full" {
		t.Fatalf("NodeOperationScope(admin) = %q, want full", got)
	}
}
