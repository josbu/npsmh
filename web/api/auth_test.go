package api

import "testing"

func TestAuthCredentialInputAllowsUsernameWithStandaloneTOTP(t *testing.T) {
	input, failureKey, code, message, ok := authCredentialInput(ManagementAuthCredentialRequest{
		Username: "tenant",
		TOTP:     "123456",
	})
	if !ok {
		t.Fatal("authCredentialInput() should accept username + standalone TOTP")
	}
	if input.Username != "tenant" || input.Password != "" || input.TOTP != "123456" {
		t.Fatalf("authCredentialInput() input = %+v, want username=tenant password='' totp=123456", input)
	}
	if failureKey != "tenant" {
		t.Fatalf("authCredentialInput() failureKey = %q, want tenant", failureKey)
	}
	if code != "" || message != "" {
		t.Fatalf("authCredentialInput() code/message = %q/%q, want empty", code, message)
	}
}

func TestAuthCredentialInputRejectsUsernameWithoutPasswordOrTOTP(t *testing.T) {
	_, _, code, _, ok := authCredentialInput(ManagementAuthCredentialRequest{
		Username: "tenant",
	})
	if ok {
		t.Fatal("authCredentialInput() should reject username without password or TOTP")
	}
	if code != "credentials_required" {
		t.Fatalf("authCredentialInput() code = %q, want credentials_required", code)
	}
}

func TestAuthCredentialInputRejectsMixedVerifyKeyAndUsernameCredentials(t *testing.T) {
	_, _, code, message, ok := authCredentialInput(ManagementAuthCredentialRequest{
		Username:  "tenant",
		Password:  "secret",
		VerifyKey: "vk-1",
	})
	if ok {
		t.Fatal("authCredentialInput() should reject mixed verify_key and username credentials")
	}
	if code != "mixed_credentials" {
		t.Fatalf("authCredentialInput() code = %q, want mixed_credentials", code)
	}
	if message == "" {
		t.Fatal("authCredentialInput() message should explain mixed credentials")
	}
}
