package host

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

const certificateRoot = stateRoot + "/certs"

type CertificateMeta struct {
	Mode     string    `json:"mode"`
	Source   string    `json:"source"`
	Issuer   string    `json:"issuer"`
	NotAfter time.Time `json:"not_after"`
}

type storedCertificate struct {
	Certificate []byte
	PrivateKey  []byte
	Meta        CertificateMeta
}

func certificateName(host string) string {
	if host == "*" || host == "_default" {
		return "_default"
	}
	name := strings.ReplaceAll(host, "*", "_wildcard")
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
	if name == "" || name == "." || name == ".." {
		return "_invalid"
	}
	return name
}

func certificateDir(host string) string {
	return filepath.Join(certificateRoot, certificateName(host))
}

func ensureDefaultCertificate() error {
	if _, err := readStoredCertificate("*"); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	cert, err := makeSelfSignedCertificate("_default", "self-signed", "self-signed")
	if err != nil {
		return err
	}
	return storeCertificate("*", cert)
}

func makeSelfSignedCertificate(host, mode, source string) (*storedCertificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	commonName := host
	if host == "*" || host == "_default" {
		commonName = "gci default"
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkixName(commonName),
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if host != "*" && host != "_default" {
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip != nil {
			template.IPAddresses = []net.IP{ip}
		} else {
			template.DNSNames = []string{host}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &storedCertificate{
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		Meta: CertificateMeta{
			Mode: mode, Source: source, Issuer: commonName, NotAfter: cert.NotAfter,
		},
	}, nil
}

func pkixName(commonName string) pkix.Name {
	return pkix.Name{CommonName: commonName}
}

func storeCertificate(host string, cert *storedCertificate) error {
	dir := certificateDir(host)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(certificateRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	meta, err := json.MarshalIndent(cert.Meta, "", "  ")
	if err != nil {
		return err
	}
	for path, data := range map[string][]byte{
		filepath.Join(dir, "cert.pem"):  cert.Certificate,
		filepath.Join(dir, "key.pem"):   cert.PrivateKey,
		filepath.Join(dir, "meta.json"): meta,
	} {
		if err := writeFileAtomic(path, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func readStoredCertificate(host string) (*storedCertificate, error) {
	dir := certificateDir(host)
	certPEM, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return nil, err
	}
	metaJSON, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta CertificateMeta
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return nil, err
	}
	return &storedCertificate{Certificate: certPEM, PrivateKey: keyPEM, Meta: meta}, nil
}

func certificatesForStates(states []*AppState) (map[string]*storedCertificate, error) {
	certificates := map[string]*storedCertificate{}
	type routeConfig struct {
		app   string
		route config.Route
	}
	routes := map[string]routeConfig{}
	for _, state := range states {
		for _, service := range state.Services {
			for _, route := range service.Routes {
				if tlsSettings(route).Mode == "off" {
					continue
				}
				if _, ok := routes[route.Host]; !ok {
					routes[route.Host] = routeConfig{app: state.App, route: route}
				}
			}
		}
	}
	if err := ensureDefaultCertificate(); err != nil {
		return nil, err
	}
	certificates["*"], _ = readStoredCertificate("*")
	for _, host := range sortedKeys(routes) {
		route := routes[host]
		mode := tlsSettings(route.route).Mode
		var cert *storedCertificate
		switch mode {
		case "custom":
			custom, err := readCustomCertificate(route.app, route.route)
			if err == nil {
				cert = custom
			} else if previous, previousErr := readStoredCertificate(host); previousErr == nil && previous.Meta.Source == "custom" {
				cert = previous
			} else {
				return nil, fmt.Errorf("%s: %w", host, err)
			}
			if err := storeCertificate(host, cert); err != nil {
				return nil, err
			}
		case "self-signed", "acme":
			current, err := readStoredCertificate(host)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			source := "self-signed"
			if mode == "acme" {
				source = "placeholder"
			}
			if err == nil && (mode == "self-signed" && current.Meta.Source == "self-signed" ||
				mode == "acme" && (current.Meta.Source == "acme" || current.Meta.Source == "placeholder")) {
				cert = current
			} else {
				cert, err = makeSelfSignedCertificate(host, mode, source)
				if err != nil {
					return nil, err
				}
				if err := storeCertificate(host, cert); err != nil {
					return nil, err
				}
			}
		default:
			return nil, fmt.Errorf("%s: unsupported TLS mode %q", host, mode)
		}
		certificates[host] = cert
	}
	return certificates, nil
}

func validateCustomCertificate(host string, certPEM, keyPEM []byte) (*storedCertificate, error) {
	leaf, err := firstCertificate(certPEM)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("private key is not PEM encoded")
	}
	key, err := parsePrivateKey(block)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("private key does not implement crypto.Signer")
	}
	certPublic, err := x509.MarshalPKIXPublicKey(leaf.PublicKey)
	if err != nil {
		return nil, err
	}
	keyPublic, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(certPublic, keyPublic) {
		return nil, fmt.Errorf("private key does not match certificate")
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("certificate is outside its validity period")
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return nil, fmt.Errorf("certificate is not valid for %q: %w", host, err)
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	return &storedCertificate{
		Certificate: append([]byte(nil), certPEM...),
		PrivateKey:  append([]byte(nil), keyPEM...),
		Meta: CertificateMeta{
			Mode: "custom", Source: "custom", Issuer: issuer, NotAfter: leaf.NotAfter,
		},
	}, nil
}

func firstCertificate(certPEM []byte) (*x509.Certificate, error) {
	rest := certPEM
	var leaf *x509.Certificate
	for len(rest) > 0 {
		rest = bytes.TrimSpace(rest)
		if len(rest) == 0 {
			break
		}
		block, remaining := pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("invalid PEM data in certificate chain")
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		if leaf == nil {
			leaf = cert
		}
	}
	if leaf != nil {
		return leaf, nil
	}
	return nil, fmt.Errorf("certificate chain contains no certificate")
}

func parsePrivateKey(block *pem.Block) (any, error) {
	switch block.Type {
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported private key type %q", block.Type)
	}
}

func validateCustomRoutes(app string, cfg *config.Config) ([]string, error) {
	missing := map[string]struct{}{}
	for _, service := range sortedKeys(cfg.Services) {
		seen := map[string]bool{}
		for _, route := range cfg.Services[service].Routes {
			if tlsSettings(route).Mode != "custom" || seen[route.Host] {
				continue
			}
			seen[route.Host] = true
			certPEM, certErr := os.ReadFile(secretPath(app, route.Cert))
			keyPEM, keyErr := os.ReadFile(secretPath(app, route.Key))
			if os.IsNotExist(certErr) {
				missing[service+": "+route.Cert] = struct{}{}
			} else if certErr != nil {
				return nil, fmt.Errorf("%s: read certificate secret: %w", service, certErr)
			}
			if os.IsNotExist(keyErr) {
				missing[service+": "+route.Key] = struct{}{}
			} else if keyErr != nil {
				return nil, fmt.Errorf("%s: read private key secret: %w", service, keyErr)
			}
			if certErr == nil && keyErr == nil {
				if _, err := validateCustomCertificate(route.Host, certPEM, keyPEM); err != nil {
					return nil, fmt.Errorf("%s: %w", service, err)
				}
			}
		}
	}
	out := make([]string, 0, len(missing))
	for value := range missing {
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func readCustomCertificate(app string, route config.Route) (*storedCertificate, error) {
	certPEM, err := os.ReadFile(secretPath(app, route.Cert))
	if err != nil {
		return nil, fmt.Errorf("read certificate secret: %w", err)
	}
	keyPEM, err := os.ReadFile(secretPath(app, route.Key))
	if err != nil {
		return nil, fmt.Errorf("read private key secret: %w", err)
	}
	return validateCustomCertificate(route.Host, certPEM, keyPEM)
}

func pushSDSCertificates(ctx context.Context, certificates map[string]*storedCertificate) error {
	digests := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(envoyDir(), "cert-digests.json"))
	if err == nil {
		_ = json.Unmarshal(raw, &digests)
	}
	if _, err := incus(ctx, "exec", envoyName, "--", "mkdir", "-p", "/etc/gci/certs"); err != nil {
		return err
	}
	if _, err := incus(ctx, "exec", envoyName, "--", "chown", "gci:gci", "/etc/gci/certs"); err != nil {
		return err
	}
	if _, err := incus(ctx, "exec", envoyName, "--", "chmod", "0700", "/etc/gci/certs"); err != nil {
		return err
	}
	for _, host := range sortedCertificateHosts(certificates) {
		cert := certificates[host]
		name := certificateName(host)
		hash := sha256.Sum256(append(append(append([]byte(nil), cert.Certificate...), 0), cert.PrivateKey...))
		digest := hex.EncodeToString(hash[:])
		if digests[name] == digest {
			continue
		}
		doc := map[string]any{"resources": []any{map[string]any{
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.Secret",
			"name":  name,
			"tls_certificate": map[string]any{
				"certificate_chain": map[string]string{"inline_string": string(cert.Certificate)},
				"private_key":       map[string]string{"inline_string": string(cert.PrivateKey)},
			},
		}}}
		data, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		finalPath := "/etc/gci/certs/" + name + ".json"
		tempPath := finalPath + ".tmp"
		if err := pushFile(ctx, envoyName, tempPath, string(data), "0600"); err != nil {
			return err
		}
		if _, err := incus(ctx, "exec", envoyName, "--", "chown", "gci:gci", tempPath); err != nil {
			return err
		}
		if _, err := incus(ctx, "exec", envoyName, "--", "chmod", "0600", tempPath); err != nil {
			return err
		}
		if _, err := incus(ctx, "exec", envoyName, "--", "mv", tempPath, finalPath); err != nil {
			return err
		}
		digests[name] = digest
	}
	cache, err := json.Marshal(digests)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(envoyDir(), "cert-digests.json"), cache, 0o600)
}

func invalidateSDSCache() error {
	err := os.Remove(filepath.Join(envoyDir(), "cert-digests.json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func sortedCertificateHosts(certs map[string]*storedCertificate) []string {
	hosts := make([]string, 0, len(certs))
	for host := range certs {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}
