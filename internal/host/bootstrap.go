package host

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sauercrowd/gci/internal/config"
)

// envoyListenPort is the port Envoy binds inside its container; the host
// port (default 80) is forwarded to it by an Incus proxy device.
const envoyListenPort = 10080
const DefaultACMEDirectory = "https://acme-v02.api.letsencrypt.org/directory"

type BootstrapOptions struct {
	Subnet        string // gateway CIDR for the gci bridge, e.g. 10.77.0.1/24
	VIPRange      string // service VIPs, routed to Envoy, e.g. 10.77.1.0/24
	StorageSize   string
	HTTPPort      int
	HTTPSPort     int
	ACMEEmail     string
	ACMEDirectory string
	ACMECABundle  string
	Nixpkgs       string
}

// Bootstrap prepares a host once: Incus, Nix, the gci bridge + profile, the
// base container image and the Envoy front door. It is idempotent.
func Bootstrap(ctx context.Context, w io.Writer, opts BootstrapOptions) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("bootstrap must run as root")
	}
	cur := currentSubnet(ctx)
	if opts.Subnet == "" {
		opts.Subnet = cur
	}
	if opts.Subnet == "" {
		opts.Subnet = "10.77.0.1/24"
	}
	if opts.VIPRange == "" {
		opts.VIPRange = "10.77.1.0/24"
	}
	if opts.StorageSize == "" {
		opts.StorageSize = "50GiB"
	}
	gw, err := netip.ParsePrefix(opts.Subnet)
	if err != nil {
		return fmt.Errorf("subnet: %w", err)
	}
	if err := checkSubnet(cur, gw); err != nil {
		return err
	}
	vips, err := netip.ParsePrefix(opts.VIPRange)
	if err != nil {
		return fmt.Errorf("vip range: %w", err)
	}
	if vips.Overlaps(gw.Masked()) {
		return fmt.Errorf("vip range %s overlaps subnet %s", opts.VIPRange, opts.Subnet)
	}
	if opts.HTTPPort == 0 {
		opts.HTTPPort = 80
	}
	if opts.HTTPSPort == 0 {
		opts.HTTPSPort = 443
	}
	if opts.HTTPPort < 1 || opts.HTTPPort > 65535 || opts.HTTPSPort < 1 || opts.HTTPSPort > 65535 {
		return fmt.Errorf("HTTP and HTTPS ports must be between 1 and 65535")
	}
	if opts.HTTPPort == opts.HTTPSPort {
		return fmt.Errorf("HTTP and HTTPS ports must be different")
	}
	if opts.ACMEDirectory == "" {
		opts.ACMEDirectory = DefaultACMEDirectory
	}
	if opts.ACMECABundle != "" {
		if _, err := os.ReadFile(opts.ACMECABundle); err != nil {
			return fmt.Errorf("read ACME CA bundle: %w", err)
		}
	}
	if opts.Nixpkgs == "" {
		opts.Nixpkgs = config.DefaultNixpkgs
	}
	hc := &HostConfig{
		EnvoyIP:       gw.Addr().Next().String(),
		VIPRange:      opts.VIPRange,
		StoragePool:   "gci",
		Subnet:        opts.Subnet,
		HTTPPort:      opts.HTTPPort,
		HTTPSPort:     opts.HTTPSPort,
		ACMEEmail:     opts.ACMEEmail,
		ACMEDirectory: opts.ACMEDirectory,
		ACMECABundle:  opts.ACMECABundle,
	}
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()

	steps := []struct {
		name string
		fn   func() error
	}{
		{"incus", func() error { return ensureIncus(ctx, w) }},
		{"storage gci", func() error {
			if err := ensureStoragePool(ctx, w, opts.StorageSize); err != nil {
				return err
			}
			return hc.save()
		}},
		{"default certificate", func() error { return ensureDefaultCertificate() }},
		{"nix", func() error { return ensureNix(ctx, w) }},
		{"network " + networkName, func() error { return ensureNetwork(ctx, opts.Subnet, hc) }},
		{"firewall", func() error { return ensureFirewallUnit(ctx) }},
		{"profile " + profileName, func() error { return ensureProfile(ctx) }},
		{"image " + baseImage, func() error { return ensureBaseImage(ctx, w) }},
		{"envoy", func() error { return ensureEnvoy(ctx, opts, hc) }},
		{"certificate renewal timer", func() error { return ensureRenewTimer(ctx) }},
	}
	for _, s := range steps {
		fmt.Fprintf(w, "==> %s\n", s.name)
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	fmt.Fprintf(w, "host ready; front door on :%d and :%d\n", opts.HTTPPort, opts.HTTPSPort)
	return nil
}

func currentSubnet(ctx context.Context) string {
	if !exists(ctx, "network", "show", networkName) {
		return ""
	}
	subnet, err := incus(ctx, "network", "get", networkName, "ipv4.address")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(subnet)
}

func checkSubnet(cur string, want netip.Prefix) error {
	if cur == "" {
		return nil
	}
	got, err := netip.ParsePrefix(cur)
	if err != nil {
		return fmt.Errorf("network %s has invalid subnet %q: %w", networkName, cur, err)
	}
	if got != want {
		return fmt.Errorf("network %s already uses %s; changing --subnet is not supported", networkName, cur)
	}
	return nil
}

func ensureStoragePool(ctx context.Context, w io.Writer, size string) error {
	if exists(ctx, "storage", "show", "gci") {
		raw, err := incus(ctx, "query", "/1.0/storage-pools/gci")
		if err != nil {
			return err
		}
		var pool struct {
			Driver string `json:"driver"`
		}
		if err := json.Unmarshal([]byte(raw), &pool); err != nil {
			return err
		}
		if pool.Driver == "dir" {
			fmt.Fprintln(w, "warning: existing gci storage pool uses the dir driver; release copies may not be copy-on-write")
		}
		return nil
	}
	if _, ok := lookPath("mkfs.btrfs"); !ok {
		if err := runStream(ctx, w, "sh", "-c",
			"apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq btrfs-progs"); err != nil {
			return err
		}
	}
	_, err := incus(ctx, "storage", "create", "gci", "btrfs", "size="+size)
	return err
}

func ensureIncus(ctx context.Context, w io.Writer) error {
	if _, ok := lookPath("incus"); !ok {
		codename, err := osCodename()
		if err != nil {
			return err
		}
		script := fmt.Sprintf(`set -e
mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.zabbly.com/key.asc -o /etc/apt/keyrings/zabbly.asc
echo "deb [signed-by=/etc/apt/keyrings/zabbly.asc] https://pkgs.zabbly.com/incus/stable %s main" > /etc/apt/sources.list.d/zabbly-incus-stable.list
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq incus`, codename)
		if err := runStream(ctx, w, "sh", "-c", script); err != nil {
			return err
		}
	}
	out, err := incus(ctx, "storage", "list", "--format", "csv")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		if _, err := incus(ctx, "admin", "init", "--minimal"); err != nil {
			return err
		}
	}
	return nil
}

func osCodename() (string, error) {
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "VERSION_CODENAME="); ok {
			return strings.Trim(v, `"`), nil
		}
	}
	return "", fmt.Errorf("cannot detect distro codename; install incus manually")
}

func ensureNix(ctx context.Context, w io.Writer) error {
	if _, err := nixBin(); err == nil {
		return nil
	}
	return runStream(ctx, w, "sh", "-c",
		"curl -fsSL https://install.determinate.systems/nix | sh -s -- install --no-confirm")
}

// ensureNetwork creates the gci bridge; its dnsmasq forwards *.internal to Envoy.
func ensureNetwork(ctx context.Context, subnet string, hc *HostConfig) error {
	if !exists(ctx, "network", "show", networkName) {
		if _, err := incus(ctx, "network", "create", networkName,
			"ipv4.address="+subnet, "ipv4.nat=true", "ipv6.address=none", "dns.domain=gci"); err != nil {
			return err
		}
	}
	_, err := incus(ctx, "network", "set", networkName, "raw.dnsmasq", "server=/internal/"+hc.EnvoyIP)
	return err
}

// Docker sets the FORWARD policy to DROP, which silently breaks bridged
// container traffic; a oneshot unit re-adds accept rules after docker starts.
func ensureFirewallUnit(ctx context.Context) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=gci firewall rules for %[1]s
After=network.target docker.service incus.service

[Service]
Type=oneshot
ExecStart=%[2]s agent firewall

[Install]
WantedBy=multi-user.target
`, networkName, self)
	if err := writeFileAtomic("/etc/systemd/system/gci-firewall.service", []byte(unit), 0o644); err != nil {
		return err
	}
	if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, _ = run(ctx, "systemctl", "enable", "gci-firewall.service")
	return Firewall(ctx)
}

func ensureRenewTimer(ctx context.Context) error {
	service := `[Unit]
Description=Renew gci ACME certificates

[Service]
Type=oneshot
ExecStart=/usr/local/bin/gci agent renew
`
	timer := `[Unit]
Description=Daily gci certificate renewal

[Timer]
OnCalendar=daily
RandomizedDelaySec=1h
Persistent=true
Unit=gci-renew.service

[Install]
WantedBy=timers.target
`
	if err := writeFileAtomic("/etc/systemd/system/gci-renew.service", []byte(service), 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic("/etc/systemd/system/gci-renew.timer", []byte(timer), 0o644); err != nil {
		return err
	}
	if _, err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	_, err := run(ctx, "systemctl", "enable", "--now", "gci-renew.timer")
	return err
}

// Firewall inserts ACCEPT rules for the gci bridge when Docker's chain exists.
func Firewall(ctx context.Context) error {
	if _, err := run(ctx, "iptables", "-n", "-L", "DOCKER-USER"); err != nil {
		return nil
	}
	rules := [][]string{
		{"DOCKER-USER", "-i", networkName, "-j", "ACCEPT"},
		{"DOCKER-USER", "-o", networkName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
	}
	for _, r := range rules {
		if _, err := run(ctx, "iptables", append([]string{"-C"}, r...)...); err == nil {
			continue
		}
		if _, err := run(ctx, "iptables", append([]string{"-I"}, r...)...); err != nil {
			return err
		}
	}
	return nil
}

func ensureProfile(ctx context.Context) error {
	if !exists(ctx, "profile", "show", profileName) {
		if _, err := incus(ctx, "profile", "create", profileName); err != nil {
			return err
		}
	}
	profile := fmt.Sprintf(`description: gci service containers
config:
  boot.autostart: "true"
devices:
  root:
    type: disk
    path: /
    pool: %s
  eth0:
    type: nic
    name: eth0
    network: %s
  nixstore:
    type: disk
    source: %s
    path: %s
    readonly: "true"
`, legacyPool, networkName, nixStore, nixStore)
	_, err := runInput(ctx, strings.NewReader(profile), "incus", "profile", "edit", profileName)
	return err
}

func ensureBaseImage(ctx context.Context, w io.Writer) error {
	if exists(ctx, "image", "show", baseImage) {
		return nil
	}
	tmp := "gci-base-build"
	_ = deleteInstance(ctx, tmp)
	fmt.Fprintln(w, "    building base image from images:debian/12")
	if _, err := incus(ctx, "launch", "images:debian/12", tmp, "--profile", profileName); err != nil {
		return err
	}
	defer deleteInstance(ctx, tmp)
	waitSystemd(ctx, tmp)
	script := `set -e
useradd -m -u 1000 -s /bin/bash gci
systemctl mask apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true
mkdir -p /etc/gci /app
# gci relies on curl (Envoy admin checks) and systemd-resolved (.internal search domain).
dpkg -s curl systemd-resolved >/dev/null 2>&1 || {
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl systemd-resolved
}`
	if _, err := incus(ctx, "exec", tmp, "--", "sh", "-c", script); err != nil {
		return err
	}
	if _, err := incus(ctx, "stop", tmp); err != nil {
		return err
	}
	_, err := incus(ctx, "publish", tmp, "--alias", baseImage)
	return err
}

func ensureEnvoy(ctx context.Context, opts BootstrapOptions, hc *HostConfig) error {
	env, err := buildEnv(ctx, opts.Nixpkgs, "gci-envoy", []string{"envoy-bin"}, filepath.Join(envoyDir(), "env"))
	if err != nil {
		return err
	}
	cfgDir := filepath.Join(envoyDir(), "config")
	if err := writeFileAtomic(filepath.Join(cfgDir, "bootstrap.yaml"), []byte(envoyBootstrap(9901)), 0o644); err != nil {
		return err
	}
	inst, err := getInstance(ctx, envoyName)
	if err != nil {
		return err
	}
	if inst == nil {
		if err := invalidateSDSCache(); err != nil {
			return err
		}
		if _, err := incus(ctx, "init", baseImage, envoyName, "--profile", profileName); err != nil {
			return err
		}
		if _, err := incus(ctx, "config", "device", "add", envoyName, "config", "disk",
			"source="+cfgDir, "path="+envoyConfigMount, "readonly=true"); err != nil {
			return err
		}
	}
	// Fixed address (the DNS server for *.internal) and the VIP range routed to it.
	nic := []string{"ipv4.address=" + hc.EnvoyIP, "ipv4.routes=" + hc.VIPRange}
	if _, err := incus(ctx, append([]string{"config", "device", "override", envoyName, "eth0"}, nic...)...); err != nil {
		if _, err := incus(ctx, append([]string{"config", "device", "set", envoyName, "eth0"}, nic...)...); err != nil {
			return err
		}
	}
	_, _ = incus(ctx, "config", "device", "remove", envoyName, "http")
	if _, err := incus(ctx, "config", "device", "add", envoyName, "http", "proxy",
		fmt.Sprintf("listen=tcp:0.0.0.0:%d", opts.HTTPPort),
		fmt.Sprintf("connect=tcp:127.0.0.1:%d", envoyListenPort)); err != nil {
		return err
	}
	_, _ = incus(ctx, "config", "device", "remove", envoyName, "https")
	if _, err := incus(ctx, "config", "device", "add", envoyName, "https", "proxy",
		fmt.Sprintf("listen=tcp:0.0.0.0:%d", opts.HTTPSPort),
		fmt.Sprintf("connect=tcp:127.0.0.1:%d", envoyHTTPSListenPort)); err != nil {
		return err
	}
	if inst == nil || inst.Status != "Running" {
		if _, err := incus(ctx, "start", envoyName); err != nil {
			return err
		}
	} else if inst.IPv4() != hc.EnvoyIP {
		if _, err := incus(ctx, "restart", envoyName); err != nil {
			return err
		}
	}
	waitSystemd(ctx, envoyName)
	if err := writeEnvoyConfig(ctx); err != nil {
		return err
	}
	unit := renderUnit(unitSpec{
		Description: "gci front door (envoy)",
		User:        "gci",
		EnvFile:     "/etc/gci/envoy.env",
		ExecPre: []string{
			"+/usr/sbin/ip route replace local " + hc.VIPRange + " dev lo",
			"+/usr/sbin/sysctl -qw net.ipv4.ip_unprivileged_port_start=0",
		},
		ExecStart: fmt.Sprintf("%s/bin/envoy -c %s/bootstrap.yaml --log-level warn", env, envoyConfigMount),
	})
	if err := pushFile(ctx, envoyName, "/etc/gci/envoy.env", "PATH="+env+"/bin:/usr/bin:/bin\n", "0644"); err != nil {
		return err
	}
	if err := pushFile(ctx, envoyName, "/etc/systemd/system/gci-envoy.service", unit, "0644"); err != nil {
		return err
	}
	if _, err := incus(ctx, "exec", envoyName, "--", "sh", "-c",
		"systemctl daemon-reload && systemctl enable gci-envoy >/dev/null 2>&1 && systemctl restart gci-envoy"); err != nil {
		return err
	}
	ip, err := waitIPv4(ctx, envoyName, 30*time.Second)
	if err != nil {
		return err
	}
	if err := waitTCP(ctx, fmt.Sprintf("%s:%d", ip, envoyListenPort), 30*time.Second); err != nil {
		return err
	}
	return waitTCP(ctx, fmt.Sprintf("%s:%d", ip, envoyHTTPSListenPort), 30*time.Second)
}
