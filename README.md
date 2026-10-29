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
gci bootstrap            # once per host: Incus, Nix, gci0 bridge, base image, Envoy on :80
cd examples/platform
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

[services.web]
packages   = ["python3.withPackages (p: [ p.psycopg ])"]
source     = "./app"            # shipped to the host, mounted read-only at /app
command    = "python3 server.py"
port       = 8080
health     = "/healthz"
depends_on = ["postgres"]
routes     = [{ prefix = "/" }]
env        = { DATABASE_URL = "postgresql://postgres@postgres:5432/postgres" }
```

See [`examples/platform`](examples/platform) for the full example, and [docs](docs/) for the configuration and command reference.

## How a deploy works

1. The client packs each `source` directory into a deterministic tarball and uploads it only if its hash is new on the host.
2. The host agent (the same `gci` binary, installed to `/usr/local/bin/gci`) builds a Nix `buildEnv` per service from `packages`.
3. Each service gets a revision hash of its definition, Nix env, and source. Unchanged services are left alone.
4. For a changed **stateless** service, gci starts a new container, waits for it to be healthy, switches Envoy and `/etc/hosts`, then deletes the old container, so there's no downtime.
5. For a changed **stateful** service (one with `volumes`), gci stops the old container, starts the new one on the same volume, and restarts the old one if the new one fails its health check.
