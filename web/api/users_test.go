package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/djylb/nps/lib/file"
)

func generateNodeTestCertificatePair(t *testing.T, notAfter time.Time) (string, string) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "node-test.example.com",
		},
		NotBefore:             notAfter.Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"node-test.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	key := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}))
	return cert, key
}

func TestSuggestedReusableCertHostsSkipsExpiredCandidates(t *testing.T) {
	validCert, validKey := generateNodeTestCertificatePair(t, time.Now().Add(2*time.Hour))
	expiredCert, expiredKey := generateNodeTestCertificatePair(t, time.Now().Add(-2*time.Hour))

	hosts := []*file.Host{
		{Id: 1, Host: "*.example.com", Scheme: "https", CertType: "text", CertFile: validCert, KeyFile: validKey},
		{Id: 2, Host: "expired.example.com", Scheme: "https", CertType: "text", CertFile: expiredCert, KeyFile: expiredKey},
		{Id: 3, Host: "proxy.example.com", Scheme: "https", HttpsJustProxy: true, CertType: "text", CertFile: validCert, KeyFile: validKey},
		{Id: 4, Host: "http.example.com", Scheme: "http", CertType: "text", CertFile: validCert, KeyFile: validKey},
		{Id: 5, Host: "invalid.example.com", Scheme: "https", CertType: "text", CertFile: "broken", KeyFile: "broken"},
	}

	got := suggestedReusableCertHosts(hosts)
	if len(got) != 1 || got[0].Id != 1 {
		t.Fatalf("suggestedReusableCertHosts() = %+v, want only valid non-expired host id 1", got)
	}
}
