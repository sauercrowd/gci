# Installation

## Client

```bash
go install github.com/sauercrowd/gci@latest
# or: curl -fsSL https://raw.githubusercontent.com/sauercrowd/gci/main/scripts/install.sh | sh
```

Release archives for linux/darwin and amd64/arm64 are on the [releases page](https://github.com/sauercrowd/gci/releases/latest).

## Server

Requirements:

- A Linux host with systemd and cgroups v2 (Debian/Ubuntu are tested; bootstrap installs Incus from the Zabbly apt repository)
- An SSH user with passwordless `sudo`, or root

Register the host and bootstrap it:

```bash
gci server add prod --host prod.example.com -u ubuntu -i ~/.ssh/id_ed25519
gci bootstrap -s prod
```

To target the current machine directly, without SSH, use `gci server add here --local`.

Bootstrap is idempotent and safe to re-run. It:

1. installs Incus (if missing) and runs `incus admin init --minimal`
2. installs Nix (Determinate installer, if missing)
3. creates the `gci0` bridge (`--subnet`, default `10.77.0.1/24`, DNS domain `gci`)
4. installs `gci-firewall.service`, which accepts forwarding for `gci0` (Docker sets `FORWARD` to `DROP`)
5. creates the `gci` Incus profile, which mounts `/nix/store` read-only
6. builds the `gci-base` image from `images:debian/12`
7. starts `gci-envoy`, published on host ports `--http-port` (default 80) and `--https-port` (default 443)
8. installs and enables `gci-renew.timer` for daily certificate renewal and ACME retries

Host state lives in `/var/lib/gci`.

Bootstrap accepts `--acme-email` (optional), `--acme-directory` (default `https://acme-v02.api.letsencrypt.org/directory`), and `--acme-ca-bundle` (optional PEM bundle path on the host). The CA bundle is useful for a private ACME directory such as Pebble. HTTP-01 validation requires DNS to point at the host and inbound port 80 to reach Envoy.

Normal DNS hosts use ACME certificates by default. Wildcard and `*.localhost` routes use self-signed certificates, and HTTP is redirected to HTTPS unless the route sets `tls = "off"`. See [Configuration](/configuration#tls-and-certificates) for route TLS modes, custom certificates, and Cloudflare settings. `gci status <app>` shows certificate source and expiry for the app's TLS hosts.
