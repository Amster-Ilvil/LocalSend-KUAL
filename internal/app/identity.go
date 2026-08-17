package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Identity struct {
	Cert        tls.Certificate
	CertDER     []byte
	Fingerprint string
	CertPath    string
	KeyPath     string
}

func LoadOrCreateIdentity(root string) (*Identity, error) {
	stateDir := filepath.Join(root, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	certPath := filepath.Join(stateDir, "device.crt")
	keyPath := filepath.Join(stateDir, "device.key")
	if _, err := os.Stat(certPath); err == nil {
		if _, err2 := os.Stat(keyPath); err2 == nil {
			return loadIdentity(certPath, keyPath)
		}
	}
	return createIdentity(certPath, keyPath)
}

func createIdentity(certPath, keyPath string) (*Identity, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "LocalSend User"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Date(4095, 12, 31, 23, 59, 59, 0, time.UTC),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	return loadIdentity(certPath, keyPath)
}

func loadIdentity(certPath, keyPath string) (*Identity, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("certificate has no DER leaf")
	}
	der := pair.Certificate[0]
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	pair.Leaf = cert
	h := sha256.Sum256(der)
	return &Identity{
		Cert:        pair,
		CertDER:     der,
		Fingerprint: strings.ToUpper(hex.EncodeToString(h[:])),
		CertPath:    certPath,
		KeyPath:     keyPath,
	}, nil
}

func verifySelfSignedRaw(rawCerts [][]byte) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("peer did not provide a certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return fmt.Errorf("peer certificate is outside validity period")
	}
	if err := cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature); err != nil {
		return fmt.Errorf("invalid peer certificate signature: %w", err)
	}
	return nil
}

func certFingerprint(raw []byte) string {
	h := sha256.Sum256(raw)
	return strings.ToUpper(hex.EncodeToString(h[:]))
}
