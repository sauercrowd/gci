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
7. starts `gci-envoy`, published on the host's `--http-port` (default 80)

Host state lives in `/var/lib/gci`.
