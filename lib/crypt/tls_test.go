package crypt

import (
	"bytes"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"testing"
	"time"
)

func TestGetPublicKeyPEMAndRSAPublicKeyPEM(t *testing.T) {
	oldCert, oldRSAKey, oldTLSCfg := cert, rsaKey, tlsCfg
	t.Cleanup(func() {
		cert = oldCert
		rsaKey = oldRSAKey
		tlsCfg = oldTLSCfg
	})

	cert = tls.Certificate{}
	rsaKey = nil
	tlsCfg = nil

	if _, err := GetPublicKeyPEM(); err == nil {
		t.Fatal("GetPublicKeyPEM() error = nil, want non-nil without certificate")
	}
	if _, err := GetRSAPublicKeyPEM(); err == nil {
		t.Fatal("GetRSAPublicKeyPEM() error = nil, want non-nil without RSA key")
	}

	InitTls(tls.Certificate{})

	publicKeyPEM, err := GetPublicKeyPEM()
	if err != nil {
		t.Fatalf("GetPublicKeyPEM() error = %v", err)
	}
	publicKeyBlock, _ := pem.Decode([]byte(publicKeyPEM))
	if publicKeyBlock == nil {
		t.Fatal("pem.Decode(publicKeyPEM) = nil")
	}
	if _, err := x509.ParsePKIXPublicKey(publicKeyBlock.Bytes); err != nil {
		t.Fatalf("ParsePKIXPublicKey(public key) error = %v", err)
	}

	rsaPublicKeyPEM, err := GetRSAPublicKeyPEM()
	if err != nil {
		t.Fatalf("GetRSAPublicKeyPEM() error = %v", err)
	}
	rsaBlock, _ := pem.Decode([]byte(rsaPublicKeyPEM))
	if rsaBlock == nil {
		t.Fatal("pem.Decode(rsaPublicKeyPEM) = nil")
	}
	parsed, err := x509.ParsePKIXPublicKey(rsaBlock.Bytes)
	if err != nil {
		t.Fatalf("ParsePKIXPublicKey(rsa public key) error = %v", err)
	}
	if _, ok := parsed.(*rsa.PublicKey); !ok {
		t.Fatalf("parsed RSA public key type = %T, want *rsa.PublicKey", parsed)
	}
}

func TestReadClientHelloCapturesServerName(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		tlsClient := tls.Client(clientConn, &tls.Config{
			ServerName:         "example.com",
			InsecureSkipVerify: true,
		})
		_ = tlsClient.SetDeadline(time.Now().Add(2 * time.Second))
		_ = tlsClient.Handshake()
		_ = tlsClient.Close()
	}()

	hello, raw, err := ReadClientHello(serverConn, nil)
	if err != nil {
		t.Fatalf("ReadClientHello() error = %v", err)
	}
	if hello == nil {
		t.Fatal("ReadClientHello() hello = nil")
	}
	if hello.ServerName != "example.com" {
		t.Fatalf("ReadClientHello() ServerName = %q, want example.com", hello.ServerName)
	}
	if len(raw) == 0 {
		t.Fatal("ReadClientHello() raw = empty, want captured client hello bytes")
	}

	_ = serverConn.Close()
	<-done
}

func TestNewSniffConnUsesDefaultMaxSize(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()

	conn := NewSniffConn(left, 0)
	if conn.maxSize != defaultMaxSize {
		t.Fatalf("NewSniffConn(0) maxSize = %d, want %d", conn.maxSize, defaultMaxSize)
	}
}

func TestSniffConnCapsBufferedBytesAtMaxSize(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = clientConn.Write([]byte("abcdefgh"))
		_ = clientConn.Close()
	}()

	sniff := NewSniffConn(serverConn, 4)
	buf := make([]byte, 8)
	n, err := sniff.Read(buf)
	if err != io.EOF {
		t.Fatalf("Read() error = %v, want io.EOF", err)
	}
	if n != 8 {
		t.Fatalf("Read() bytes = %d, want 8", n)
	}
	if got := sniff.Bytes(); !bytes.Equal(got, []byte("abcd")) {
		t.Fatalf("Bytes() = %q, want %q", got, []byte("abcd"))
	}

	<-done
}
