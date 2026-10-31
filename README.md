# gci

Deploy services to any Linux host over SSH. No image builds and no registry.

- **Nix** pins and provides every runtime and package. A deploy only realises store paths the host is missing.
- **Incus** system containers run each service with its own IP, cgroup limits, and persistent volumes. The host `/nix/store` is mounted read-only into every container.
- **Envoy** runs once per host as the front door and routes traffic to services.

Today gci targets a single host. Cross-host networking (WireGuard between per-host Envoys) is planned.

## Quickstart

```bash
go install github.com/sauercrowd/gci@latest

gci server add prod --host prod.example.com -u ubuntu -i ~/.ssh/id_ed25519
gci bootstrap            # once per host: Incus, Nix, gci btrfs pool, bridge, base image, Envoy
cd examples/platform    # or: gci init in your own project for a starter gci.toml
gci secret set postgres-password
gci secret set database-url
gci deploy               # Postgres + ClickHouse + Python app behind Envoy
gci status
gci logs web -f
```

`gci.toml`:

```toml
app = "platform"

[services.postgres]
packages = ["postgresql_16"]
volumes  = { pgdata = "/var/lib/postgresql" }
command  = "postgres -D /var/lib/postgresql/16 -c listen_addresses=*"
port     = 5432
secrets  = { POSTGRES_PASSWORD = "postgres-password" }

[services.web]
packages   = ["python3.withPackages (p: [ p.psycopg ])"]
source     = "./app"            # shipped to the host and snapshotted at /app
command    = "python3 server.py"
port       = 8080
health     = "/healthz"
depends_on = ["postgres"]
routes     = [{ prefix = "/" }]
env        = { CLICKHOUSE_URL = "http://clickhouse.platform.internal:8123" }
secrets    = { DATABASE_URL = "database-url" }
```

See [`examples/platform`](examples/platform) for the full example, and [docs](docs/) for the configuration and command reference.

Routed hosts use HTTPS by default: public DNS names use Let's Encrypt, while `*`, wildcard, localhost, and IP hosts use self-signed certificates. HTTP redirects to HTTPS unless the route sets `tls = "off"`. Bootstrap publishes HTTPS on port 443 by default; see the [TLS configuration guide](docs/configuration.md#tls-and-certificates) for custom certificates and ACME options.

## How a deploy works

1. The client packs each `source` directory into a deterministic tarball and uploads it only if its hash is new on the host.
2. Before changing running services, the host checks secrets, realizes each Nix `buildEnv`, installs dependencies into a stopped `deps` snapshot keyed by the lockfile inputs, and creates a stopped `release` snapshot keyed by the source and build.
3. Activation copies a release into a fresh service container, writes runtime configuration and secrets, starts it, and waits for the configured probe and optional deploy-time `health_command`. Secrets are never present in dependency or release snapshots.
4. Envoy switches to a healthy replacement before the old stateless service container is deleted. Stateful services stop the old container and reuse its persistent volume.
5. Each service retains its current release and the two newest others. Release roots keep the corresponding Nix closures available for reuse.

`gci secret set <name>` reads a value from stdin; secret values are not command-line arguments. Secrets are stored on the host with mode `0600`, injected only into `/etc/gci/service.env` at activation, and kept by `gci rm`.

Bootstrap creates a btrfs-backed Incus pool named `gci` (default 50 GiB; set `--storage-size` to change it). New service, dependency, and release containers use that pool; existing named volumes in the legacy `default` pool keep their data. Dependency and build commands share a persistent `gcicache-<app>-<service>` volume at `/var/cache/gci`. `gci rm --volumes` removes application volumes from both pools and these caches; without it, they are kept.
