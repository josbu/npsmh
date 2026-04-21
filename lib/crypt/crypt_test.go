package crypt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
)

func TestPKCS5UnPaddingRejectsEmptyAndInvalidPadding(t *testing.T) {
	if _, err := PKCS5UnPadding(nil); err == nil {
		t.Fatal("PKCS5UnPadding(nil) error = nil, want non-nil")
	}
	if _, err := PKCS5UnPadding([]byte{1, 2, 3, 0}); err == nil {
		t.Fatal("PKCS5UnPadding(zero padding) error = nil, want non-nil")
	}
	if _, err := PKCS5UnPadding([]byte{1, 2, 3, 2, 1, 2}); err == nil {
		t.Fatal("PKCS5UnPadding(invalid padding bytes) error = nil, want non-nil")
	}
}

func TestEncryptBytesRoundTripAndShortCiphertext(t *testing.T) {
	plain := []byte("hello crypt")
	key := "secret-key"

	encrypted, err := EncryptBytes(plain, key)
	if err != nil {
		t.Fatalf("EncryptBytes() error = %v", err)
	}
	if string(encrypted) == string(plain) {
		t.Fatal("EncryptBytes() should not return plaintext when key is set")
	}

	decrypted, err := DecryptBytes(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptBytes() error = %v", err)
	}
	if string(decrypted) != string(plain) {
		t.Fatalf("DecryptBytes() = %q, want %q", decrypted, plain)
	}

	if _, err := DecryptBytes([]byte("short"), key); err == nil {
		t.Fatal("DecryptBytes(short ciphertext) error = nil, want non-nil")
	}
}

func TestParseLoginPayloadRoundTrip(t *testing.T) {
	InitTls(tls.Certificate{})

	publicKeyPEM, err := GetRSAPublicKeyPEM()
	if err != nil {
		t.Fatalf("GetRSAPublicKeyPEM() error = %v", err)
	}
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		t.Fatal("pem.Decode(publicKeyPEM) = nil")
	}
	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey() error = %v", err)
	}
	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T, want *rsa.PublicKey", publicKey)
	}

	payload := LoginPayload{
		Nonce:     "nonce-1",
		Timestamp: 1700000000,
		Password:  "p@ss=word",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	cipherBytes, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, rsaPublicKey, raw, nil)
	if err != nil {
		t.Fatalf("EncryptOAEP() error = %v", err)
	}

	decoded, err := ParseLoginPayload(base64.StdEncoding.EncodeToString(cipherBytes))
	if err != nil {
		t.Fatalf("ParseLoginPayload() error = %v", err)
	}
	if *decoded != payload {
		t.Fatalf("ParseLoginPayload() = %+v, want %+v", *decoded, payload)
	}
}
