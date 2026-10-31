package host

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"github.com/sauercrowd/gci/internal/config"
)

const (
	acmeRoot                 = stateRoot + "/acme"
	acmeAttemptTimeout       = 90 * time.Second
	certificateRenewalWindow = 30 * 24 * time.Hour
)

type acmeUser struct {
	email        string
	registration *registration.Resource
	key          *ecdsa.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

type acmeAccountState struct {
	Email        string                 `json:"email"`
	Registration *registration.Resource `json:"registration,omitempty"`
}

func acmeAccountDir(directory string) string {
	sum := sha256.Sum256([]byte(directory))
	return filepath.Join(acmeRoot, hex.EncodeToString(sum[:])[:12])
}

func loadACMEUser(hc *HostConfig) (*acmeUser, string, error) {
	dir := acmeAccountDir(hc.ACMEDirectory)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(acmeRoot, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(dir, "account.key")
	keyPEM, err := os.ReadFile(keyPath)
	var key *ecdsa.PrivateKey
	if err == nil {
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			return nil, "", fmt.Errorf("ACME account key is not PEM encoded")
		}
		parsed, parseErr := parsePrivateKey(block)
		if parseErr != nil {
			return nil, "", fmt.Errorf("parse ACME account key: %w", parseErr)
		}
		var ok bool
		key, ok = parsed.(*ecdsa.PrivateKey)
		if !ok || key.Curve != elliptic.P256() {
			return nil, "", fmt.Errorf("ACME account key must be ECDSA P-256")
		}
	} else if !os.IsNotExist(err) {
		return nil, "", err
	} else {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, "", err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, "", err
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
			return nil, "", err
		}
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, "", err
	}

	user := &acmeUser{email: hc.ACMEEmail, key: key}
	registrationPath := filepath.Join(dir, "registration.json")
	raw, err := os.ReadFile(registrationPath)
	if err == nil {
		if err := os.Chmod(registrationPath, 0o600); err != nil {
			return nil, "", err
		}
		var state acmeAccountState
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, "", err
		}
		user.registration = state.Registration
	} else if !os.IsNotExist(err) {
		return nil, "", err
	}
	return user, dir, nil
}

func saveACMERegistration(user *acmeUser, dir string) error {
	raw, err := json.MarshalIndent(acmeAccountState{
		Email: user.email, Registration: user.registration,
	}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "registration.json"), raw, 0o600)
}

type contextRoundTripper struct {
	ctx  context.Context
	next http.RoundTripper
}

type contextResponseBody struct {
	io.ReadCloser
	stop   func() bool
	cancel context.CancelFunc
}

func (b *contextResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.stop()
		b.cancel()
	}
	return n, err
}

func (b *contextResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.stop()
	b.cancel()
	return err
}

func (t contextRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(t.ctx)
	stop := context.AfterFunc(request.Context(), cancel)
	response, err := t.next.RoundTrip(request.Clone(ctx))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	if response.Body == nil {
		stop()
		cancel()
	} else {
		response.Body = &contextResponseBody{ReadCloser: response.Body, stop: stop, cancel: cancel}
	}
	return response, nil
}

func acmeHTTPClient(ctx context.Context, hc *HostConfig) (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if hc.ACMECABundle != "" {
		caPEM, err := os.ReadFile(hc.ACMECABundle)
		if err != nil {
			return nil, fmt.Errorf("read ACME CA bundle: %w", err)
		}
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("ACME CA bundle contains no certificates")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.RootCAs = roots
	if tlsConfig.MinVersion == 0 {
		tlsConfig.MinVersion = tls.VersionTLS12
	}
	transport.TLSClientConfig = tlsConfig
	return &http.Client{
		Transport: contextRoundTripper{ctx: ctx, next: transport},
		Timeout:   30 * time.Second,
	}, nil
}

func issueACMECertificate(ctx context.Context, hc *HostConfig, host string) error {
	user, dir, err := loadACMEUser(hc)
	if err != nil {
		return err
	}
	httpClient, err := acmeHTTPClient(ctx, hc)
	if err != nil {
		return err
	}
	cfg := lego.NewConfig(user)
	cfg.CADirURL = hc.ACMEDirectory
	cfg.Certificate.KeyType = certcrypto.EC256
	cfg.HTTPClient = httpClient
	client, err := lego.NewClient(cfg)
	if err != nil {
		return err
	}
	gateway, err := bridgeGateway(hc)
	if err != nil {
		return err
	}
	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer(gateway, "8402")); err != nil {
		return err
	}
	if user.registration == nil {
		user.registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return err
		}
		if err := saveACMERegistration(user, dir); err != nil {
			return err
		}
	}
	cert, err := client.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{host},
		Bundle:  true,
	})
	if err != nil {
		return err
	}
	stored, err := validateCustomCertificate(host, cert.Certificate, cert.PrivateKey)
	if err != nil {
		return fmt.Errorf("validate issued certificate: %w", err)
	}
	leaf, err := firstCertificate(cert.Certificate)
	if err != nil {
		return err
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" {
		issuer = leaf.Issuer.String()
	}
	stored.Meta = CertificateMeta{
		Mode: "acme", Source: "acme", Issuer: issuer, NotAfter: leaf.NotAfter,
	}
	return storeCertificate(host, stored)
}

func storeACMEFallback(host string) error {
	cert, err := makeSelfSignedCertificate(host, "acme", "placeholder")
	if err != nil {
		return err
	}
	return storeCertificate(host, cert)
}

func bridgeGateway(hc *HostConfig) (string, error) {
	prefix, err := netip.ParsePrefix(hc.Subnet)
	if err != nil {
		return "", fmt.Errorf("host subnet: %w", err)
	}
	return prefix.Addr().String(), nil
}

func acmeHostsForConfig(cfg *config.Config) []string {
	hosts := map[string]struct{}{}
	for _, service := range cfg.Services {
		for _, route := range service.Routes {
			if tlsSettings(route).Mode == "acme" {
				hosts[route.Host] = struct{}{}
			}
		}
	}
	return sortedKeys(hosts)
}

func issueACMECertificates(ctx context.Context, w io.Writer, hc *HostConfig, cfg *config.Config) error {
	for _, host := range acmeHostsForConfig(cfg) {
		current, err := readStoredCertificate(host)
		if err == nil && current.Meta.Source == "acme" && current.Meta.NotAfter.After(time.Now().Add(certificateRenewalWindow)) {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, acmeAttemptTimeout)
		err = issueACMECertificate(attemptCtx, hc, host)
		cancel()
		if err != nil {
			if fallbackErr := storeACMEFallback(host); fallbackErr != nil {
				return fallbackErr
			}
			if fallbackErr := writeEnvoyConfig(ctx); fallbackErr != nil {
				return fallbackErr
			}
			fmt.Fprintf(w, "%s: Let's Encrypt certificate failed (%v); serving a self-signed certificate until it succeeds (retried on the next deploy and daily)\n", host, err)
			continue
		}
		if err := writeEnvoyConfig(ctx); err != nil {
			return err
		}
	}
	return nil
}

type routedTLSHost struct {
	settings RouteTLSSettings
	app      string
	route    config.Route
}

func tlsHostsFromStates(states []*AppState) map[string]routedTLSHost {
	hosts := map[string]routedTLSHost{}
	for _, state := range states {
		for _, service := range state.Services {
			for _, route := range service.Routes {
				if tlsSettings(route).Mode == "off" {
					continue
				}
				if _, ok := hosts[route.Host]; !ok {
					hosts[route.Host] = routedTLSHost{
						settings: tlsSettings(route), app: state.App, route: route,
					}
				}
			}
		}
	}
	return hosts
}

func Renew(ctx context.Context, w io.Writer) error {
	if err := preflight(ctx); err != nil {
		return err
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	hc, err := loadHostConfig()
	if err != nil {
		return err
	}
	states, err := loadAllStates()
	if err != nil {
		return err
	}
	hosts := tlsHostsFromStates(states)
	names := make([]string, 0, len(hosts))
	for host := range hosts {
		names = append(names, host)
	}
	sort.Strings(names)
	for _, host := range names {
		routed := hosts[host]
		current, certErr := readStoredCertificate(host)
		switch routed.settings.Mode {
		case "acme":
			if certErr == nil && current.Meta.Source == "acme" && current.Meta.NotAfter.After(time.Now().Add(certificateRenewalWindow)) {
				fmt.Fprintf(w, "%s: ACME certificate valid until %s\n", host, current.Meta.NotAfter.Format(time.RFC3339))
				continue
			}
			attemptCtx, cancel := context.WithTimeout(ctx, acmeAttemptTimeout)
			err := issueACMECertificate(attemptCtx, hc, host)
			cancel()
			if err != nil {
				if fallbackErr := storeACMEFallback(host); fallbackErr != nil {
					return fallbackErr
				}
				fmt.Fprintf(w, "%s: Let's Encrypt certificate failed (%v); serving a self-signed certificate until it succeeds (retried on the next deploy and daily)\n", host, err)
				continue
			}
			fmt.Fprintf(w, "%s: ACME certificate renewed\n", host)
		case "self-signed":
			if certErr == nil && current.Meta.Source == "self-signed" &&
				current.Meta.NotAfter.After(time.Now().Add(certificateRenewalWindow)) {
				fmt.Fprintf(w, "%s: self-signed certificate valid until %s\n", host, current.Meta.NotAfter.Format(time.RFC3339))
				continue
			}
			cert, err := makeSelfSignedCertificate(host, "self-signed", "self-signed")
			if err != nil {
				return err
			}
			if err := storeCertificate(host, cert); err != nil {
				return err
			}
			fmt.Fprintf(w, "%s: self-signed certificate renewed\n", host)
		case "custom":
			fmt.Fprintf(w, "%s: custom certificate unchanged\n", host)
		}
	}
	return writeEnvoyConfig(ctx)
}
