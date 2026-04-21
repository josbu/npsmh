package cache

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCertManagerGetTextCacheHitAndModeValidation(t *testing.T) {
	certPEM, keyPEM := generateTestPEM(t)

	m := NewCertManager(10, 0, 0)
	defer m.Stop()

	if _, err := m.Get(certPEM, keyPEM, "invalid", "hash"); err == nil {
		t.Fatal("expected invalid mode to return error")
	}

	first, err := m.Get(certPEM, keyPEM, "text", "hash")
	if err != nil {
		t.Fatalf("first get failed: %v", err)
	}

	second, err := m.Get("broken cert", "broken key", "text", "hash")
	if err != nil {
		t.Fatalf("second get should hit cache and succeed: %v", err)
	}

	if first != second {
		t.Fatal("expected second get to return cached certificate pointer")
	}
}

func TestCertManagerFileNotFoundAndIdleEviction(t *testing.T) {
	m := NewCertManager(10, 0, 0)
	defer m.Stop()

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "not-exists-cert.pem")
	keyPath := filepath.Join(tmpDir, "not-exists-key.pem")
	_, err := m.Get(certPath, keyPath, "file", "missing")
	if err == nil || !strings.Contains(err.Error(), "cert file not found") {
		t.Fatalf("expected cert file not found error, got %v", err)
	}

	certPEM, keyPEM := generateTestPEM(t)
	_, err = m.Get(certPEM, keyPEM, "text", "evict-me")
	if err != nil {
		t.Fatalf("failed to seed cache: %v", err)
	}

	m.mu.Lock()
	if elem, ok := m.cache.Get("evict-me"); ok {
		e := elem.(*certEntry)
		e.lastUsed = time.Now().Add(-2 * time.Second)
	}
	m.loadMutexes["evict-me"] = &sync.Mutex{}
	m.idleTimeout = time.Second
	m.mu.Unlock()

	m.evictIdle()

	m.mu.Lock()
	_, cacheHit := m.cache.Get("evict-me")
	_, mutexExists := m.loadMutexes["evict-me"]
	m.mu.Unlock()

	if cacheHit {
		t.Fatal("expected evict-me to be removed from cache")
	}
	if mutexExists {
		t.Fatal("expected evict-me load mutex to be removed")
	}
}

func TestCertManagerFailedInitialLoadReleasesLoadMutex(t *testing.T) {
	m := NewCertManager(10, 0, 0)
	defer m.Stop()

	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "missing-cert.pem")
	keyPath := filepath.Join(tmpDir, "missing-key.pem")
	if _, err := m.Get(certPath, keyPath, "file", "missing-lock"); err == nil {
		t.Fatal("expected missing certificate files to return error")
	}

	m.mu.Lock()
	_, exists := m.loadMutexes["missing-lock"]
	m.mu.Unlock()
	if exists {
		t.Fatal("expected failed initial load to release its load mutex")
	}
}

func TestCertManagerExpiredFileReloadFailureReturnsError(t *testing.T) {
	m := NewCertManager(10, 0, 0)
	defer m.Stop()

	certPEM, keyPEM := generateTestPEM(t)
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	if err := os.WriteFile(certPath, []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write cert failed: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(keyPEM), 0o600); err != nil {
		t.Fatalf("write key failed: %v", err)
	}

	first, err := m.Get(certPath, keyPath, "file", "expired-reload")
	if err != nil {
		t.Fatalf("initial get failed: %v", err)
	}
	if first == nil {
		t.Fatal("initial get returned nil certificate")
	}

	m.mu.Lock()
	if elem, ok := m.cache.Get("expired-reload"); ok {
		elem.(*certEntry).expire = time.Now().Add(-time.Second)
	}
	m.mu.Unlock()

	if err := os.Remove(certPath); err != nil {
		t.Fatalf("remove cert failed: %v", err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key failed: %v", err)
	}

	if _, err := m.Get(certPath, keyPath, "file", "expired-reload"); err == nil {
		t.Fatal("expected expired cached certificate reload failure to return error")
	}
}

func generateTestPEM(t *testing.T) (certPEM string, keyPEM string) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key failed: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate failed: %v", err)
	}

	certBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return string(certBytes), string(keyBytes)
}
