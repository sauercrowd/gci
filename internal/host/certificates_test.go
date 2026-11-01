package host

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestSelfSignedCertificate(t *testing.T) {
	cert, err := makeSelfSignedCertificate("example.com", "self-signed", "self-signed")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := firstCertificate(cert.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "example.com" {
		t.Fatalf("DNS SANs = %v", leaf.DNSNames)
	}
	if leaf.NotAfter.Before(time.Now().AddDate(0, 11, 0)) || leaf.NotAfter.After(time.Now().AddDate(1, 0, 1)) {
		t.Fatalf("unexpected certificate validity ending at %s", leaf.NotAfter)
	}
	if _, ok := leaf.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Fatalf("public key type = %T, want ECDSA", leaf.PublicKey)
	}
	if got := leaf.PublicKey.(*ecdsa.PublicKey).Curve; got != elliptic.P256() {
		t.Fatalf("curve = %v, want P-256", got)
	}
	if err := leaf.VerifyHostname("example.com"); err != nil {
		t.Fatal(err)
	}

	defaultCert, err := makeSelfSignedCertificate("_default", "self-signed", "self-signed")
	if err != nil {
		t.Fatal(err)
	}
	defaultLeaf, err := firstCertificate(defaultCert.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if defaultLeaf.Subject.CommonName != "gci default" || len(defaultLeaf.DNSNames) != 0 || len(defaultLeaf.IPAddresses) != 0 {
		t.Fatalf("default certificate subject/SANs = %q, %v, %v", defaultLeaf.Subject.CommonName, defaultLeaf.DNSNames, defaultLeaf.IPAddresses)
	}
}

func TestCustomCertificateValidation(t *testing.T) {
	certPEM, keyPEM, otherKeyPEM := testCertificatePEM(t, "example.com")
	if _, err := validateCustomCertificate("example.com", certPEM, keyPEM); err != nil {
		t.Fatalf("valid material rejected: %v", err)
	}
	if _, err := validateCustomCertificate("example.com", certPEM, otherKeyPEM); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched key error = %v", err)
	}
	if _, err := validateCustomCertificate("wrong.example.com", certPEM, keyPEM); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("wrong host error = %v", err)
	}
}

func testCertificatePEM(t *testing.T, host string) ([]byte, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkixName("test issuer"),
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	otherKeyDER, err := x509.MarshalPKCS8PrivateKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: otherKeyDER})
}
