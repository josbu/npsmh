package crypt

import (
	"bytes"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/skip2/go-qrcode"
)

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	if len(secret) != 16 {
		t.Fatalf("GenerateTOTPSecret() secret length = %d, want 16", len(secret))
	}

	if matched := regexp.MustCompile(`^[A-Z2-7]+$`).MatchString(secret); !matched {
		t.Fatalf("GenerateTOTPSecret() secret %q contains invalid base32 characters", secret)
	}
}

func TestTOTPCodeRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	code, remaining, err := GetTOTPCode(secret)
	if err != nil {
		t.Fatalf("GetTOTPCode() error = %v", err)
	}

	if len(code) != TotpLen {
		t.Fatalf("GetTOTPCode() code length = %d, want %d", len(code), TotpLen)
	}
	if remaining < 1 || remaining > 30 {
		t.Fatalf("GetTOTPCode() remaining = %d, want in [1, 30]", remaining)
	}

	ok, err := ValidateTOTPCode(secret, code)
	if err != nil {
		t.Fatalf("ValidateTOTPCode() error = %v", err)
	}
	if !ok {
		t.Fatal("ValidateTOTPCode() = false, want true for generated code")
	}
}

func TestValidateTOTPCode_InvalidInput(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}

	if ok, err := ValidateTOTPCode("not-base32", "123456"); err == nil || ok {
		t.Fatalf("ValidateTOTPCode(invalid secret) = (%v, %v), want (false, error)", ok, err)
	}

	if ok, err := ValidateTOTPCode(secret, "not-number"); err == nil || ok {
		t.Fatalf("ValidateTOTPCode(invalid code) = (%v, %v), want (false, error)", ok, err)
	}
}

func TestIsValidTOTPSecret(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	if !IsValidTOTPSecret(secret) {
		t.Fatalf("IsValidTOTPSecret(%q) = false, want true", secret)
	}
	if IsValidTOTPSecret("%%%%") {
		t.Fatal("IsValidTOTPSecret(%%%%) = true, want false")
	}
}

func TestTOTPSecretValidationAcceptsLowercaseAndWhitespace(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret() error = %v", err)
	}
	normalized := "  " + strings.ToLower(secret) + "  "

	if !IsValidTOTPSecret(normalized) {
		t.Fatalf("IsValidTOTPSecret(%q) = false, want true", normalized)
	}
	code, _, err := GetTOTPCode(normalized)
	if err != nil {
		t.Fatalf("GetTOTPCode() error = %v", err)
	}
	ok, err := ValidateTOTPCode(normalized, " "+code+" ")
	if err != nil {
		t.Fatalf("ValidateTOTPCode() error = %v", err)
	}
	if !ok {
		t.Fatal("ValidateTOTPCode() = false, want true")
	}
}

func TestBuildTotpUri(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	withIssuer := BuildTotpUri("nps team", "alice@example.com", secret)
	wantWithIssuer := "otpauth://totp/" + url.QueryEscape("nps team:alice@example.com") + "?secret=" + secret + "&issuer=" + url.QueryEscape("nps team")
	if withIssuer != wantWithIssuer {
		t.Fatalf("BuildTotpUri(with issuer) = %q, want %q", withIssuer, wantWithIssuer)
	}

	withoutIssuer := BuildTotpUri("", "alice@example.com", secret)
	wantWithoutIssuer := "otpauth://totp/" + url.QueryEscape("alice@example.com") + "?secret=" + secret
	if withoutIssuer != wantWithoutIssuer {
		t.Fatalf("BuildTotpUri(without issuer) = %q, want %q", withoutIssuer, wantWithoutIssuer)
	}
}

func TestPrintTOTPSecretDoesNotPanicOnQRCodeError(t *testing.T) {
	oldNewQRCode := newQRCode
	defer func() { newQRCode = oldNewQRCode }()
	newQRCode = func(content string, level qrcode.RecoveryLevel) (*qrcode.QRCode, error) {
		return nil, errors.New("qr-failed")
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("PrintTOTPSecret() panic = %v, want printed error", recovered)
		}
	}()

	PrintTOTPSecret()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom(stdout) error = %v", err)
	}
	if !strings.Contains(buf.String(), "Failed to generate 2FA QR code") {
		t.Fatalf("PrintTOTPSecret() output = %q, want QR generation error", buf.String())
	}
}
