package common

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ReadAllFromFile Read file content by file path
func ReadAllFromFile(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

func GetPath(filePath string) string {
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	path, err := filepath.Abs(filePath)
	if err != nil {
		return filePath
	}
	return path
}

func GetCertContent(filePath, header string) (string, error) {
	if filePath == "" || strings.Contains(filePath, header) {
		return filePath, nil
	}
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(GetRunPath(), filePath)
	}
	content, err := ReadAllFromFile(filePath)
	if err != nil {
		return "", err
	}
	if !strings.Contains(string(content), header) {
		return "", fmt.Errorf("content at %s does not contain %s", filePath, header)
	}
	return string(content), nil
}

func LoadCertPair(certFile, keyFile string) (certContent, keyContent string, ok bool) {
	var wg sync.WaitGroup
	var certErr, keyErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		certContent, certErr = GetCertContent(certFile, "CERTIFICATE")
	}()
	go func() {
		defer wg.Done()
		keyContent, keyErr = GetCertContent(keyFile, "PRIVATE")
	}()
	wg.Wait()
	if certErr != nil || keyErr != nil || certContent == "" || keyContent == "" {
		return "", "", false
	}
	return certContent, keyContent, true
}

func LoadCert(certFile, keyFile string) (tls.Certificate, bool) {
	certContent, keyContent, ok := LoadCertPair(certFile, keyFile)
	if ok {
		certificate, err := tls.X509KeyPair([]byte(certContent), []byte(keyContent))
		if err == nil {
			return certificate, true
		}
	}
	return tls.Certificate{}, false
}

func LoadCertLeaf(certFile, keyFile string) (*x509.Certificate, error) {
	certificate, ok := LoadCert(certFile, keyFile)
	if !ok {
		return nil, errors.New("invalid certificate pair")
	}
	if len(certificate.Certificate) == 0 {
		return nil, errors.New("empty certificate")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, err
	}
	return leaf, nil
}

func LoadCertExpireAt(certFile, keyFile string) (time.Time, error) {
	leaf, err := LoadCertLeaf(certFile, keyFile)
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}

func LoadCertDomains(certFile, keyFile string) ([]string, error) {
	leaf, err := LoadCertLeaf(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	add := func(value string, domains *[]string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		*domains = append(*domains, value)
	}

	domains := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses)+1)
	for _, name := range leaf.DNSNames {
		add(name, &domains)
	}
	for _, ip := range leaf.IPAddresses {
		add(ip.String(), &domains)
	}
	if len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0 {
		add(leaf.Subject.CommonName, &domains)
	}
	return domains, nil
}

func GetCertType(s string) string {
	if s == "" {
		return "empty"
	}
	if strings.Contains(s, "-----BEGIN ") || strings.Contains(s, "\n") {
		return "text"
	}
	if _, err := os.Stat(s); err == nil {
		return "file"
	}
	return "invalid"
}

// FileExists reports whether the named file or directory exists.
func FileExists(name string) bool {
	if _, err := os.Stat(name); err != nil && os.IsNotExist(err) {
		return false
	}
	return true
}

func GetExtFromPath(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	if base == "" {
		return ""
	}
	ext := filepath.Ext(base)
	if ext == "" || ext == base {
		return ""
	}
	return strings.TrimPrefix(ext, ".")
}
