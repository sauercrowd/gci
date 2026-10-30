# Configuration (`gci.toml`)

```toml
app     = "platform"        # ^[a-z][a-z0-9-]{0,23}$
server  = "prod"            # optional: default server for this app
nixpkgs = "github:NixOS/nixpkgs/<rev>"   # optional: pinned by default

[services.<name>]
packages       = ["postgresql_16", "python3.withPackages (p: [ p.psycopg ])"]
source         = "./app"
install        = "npm ci"
install_inputs = ["package.json", "package-lock.json"]
build          = "npm run build"
command        = "python3 server.py"
setup          = "..."
user           = "gci"
env            = { KEY = "value" }
secrets        = { API_TOKEN = "api-token" }
port           = 8080
health         = "/healthz"
health_timeout = 120
volumes        = { data = "/var/lib/data" }
depends_on     = ["postgres"]
routes         = [{ host = "*", prefix = "/" }]
cpu            = "2"
memory         = "1GiB"
```

| Key | Description |
| --- | --- |
| `packages` | Nix expressions evaluated with `nixpkgs` in scope. gci combines them with `cacert`, `coreutils`, and `bashInteractive` into one `buildEnv` on `PATH`. |
| `source` | A local directory, relative to `gci.toml`, shipped to a release snapshot and used as the writable `/app` working directory. `.git`, `node_modules`, and `__pycache__` are skipped. |
| `install` | Optional dependency-install command, such as `npm ci`. Requires `source`; runs once in a stopped dependency snapshot with `sh -e -c` and never receives service `env` or secrets. |
| `install_inputs` | Source-relative files/directories used to key the dependency snapshot. Paths cannot be absolute, contain `..`, or use globs. Defaults to the known Node.js and Python manifest/lockfile names present at the source root. If none exist, set this explicitly. |
| `build` | Optional build command run after the full source is copied into a stopped release builder with `sh -e -c`. Requires `source`; receives `env` but not secrets. |
| `command` | **Required.** The main process, run under systemd as `gci-<name>.service` with `Restart=always`. |
| `setup` | A shell script run before every start (`ExecStartPre`), for example first-boot initialisation. |
| `user` | The user to run as. Default `gci`. It is created in the container, and volumes are owned by it. |
| `env` | Environment variables. |
| `secrets` | `ENV_VAR = "secret-name"` mappings. Names use lowercase letters, digits, `.`, `_`, and `-`; environment variable names follow shell identifier syntax. A key cannot appear in both `env` and `secrets`. Values are read from `gci secret set` and injected only when the service container is activated. |
| `port` | The port the service listens on. Required for `routes`. |
| `health` | An HTTP path that must return 2xx/3xx. If only `port` is set, the check is a TCP connect. With neither, the check is that the systemd unit is active. |
| `health_command` | Optional shell command run during deployment after the HTTP, TCP, or systemd-active probe passes. It runs as the service user, with the service environment (including secrets); it may be set without `port` or `health`. Only non-whitespace values are required. |
| `health_timeout` | Seconds to wait for health. Default 120. A unit that restarts 5 times fails the deploy early. |
| `volumes` | `name = "/path"` pairs. Each is an Incus custom volume named `gci-<app>-<name>`, and it survives redeploys and `gci rm`. A service with volumes is **stateful**: it is stopped, then started, rather than rolled. |
| `depends_on` | Services that are deployed first. Cycles are rejected. |
| `routes` | Envoy routes. `host` defaults to `*` and `prefix` to `/`. |
| `cpu`, `memory` | Incus `limits.cpu` and `limits.memory`. |

## Health checks

The HTTP, TCP, or systemd-active probe runs first. If `health_command` is set, gci then runs it inside the new container before promoting traffic, retrying once per second until it succeeds or the shared `health_timeout` expires. Each command attempt is capped at 10 seconds. The command runs only during deployment; Envoy does not run it, and continuous health-command checks are not supported.

## Release snapshots

Services with `source` are prepared before any running service is activated. When `install` is set, gci copies only `install_inputs` into a dependency builder and caches the completed dependency tree by the base image, Nix environment, user, install command, and input contents. Source and `build` are then combined in a release snapshot. A source-only change can reuse the dependency snapshot; a dependency-input change installs dependencies again.

Deploy activation is a copy-on-write copy of the stopped release, followed by runtime configuration, start, health checking, and the Envoy switch. A failed install/build or a missing secret does not replace a running container. Secrets are not available to install/build commands and do not enter release snapshots.

The host retains the current release and the two newest prior releases per service. Each retained release has its own Nix GC root; older release containers and no-longer-referenced dependency snapshots are removed after a successful apply.

## Storage and build cache

Bootstrap creates a loop-backed Incus btrfs pool named `gci`; adjust its capacity with `gci bootstrap --storage-size 50GiB`. New service, dependency, and release containers use this pool for copy-on-write snapshots. The existing `gci` profile root device is not changed, and the Envoy container stays on its existing pool. If a named application volume already exists in the legacy `default` pool, gci keeps using it, so existing database data survives the migration. New volumes use `gci`.

Dependency and build commands share a persistent per-service `gcicache-<app>-<service>` volume mounted at `/var/cache/gci`. `gci rm --volumes` deletes application volumes from both pools and these cache volumes; without `--volumes`, volumes and caches remain.

`gci rm` also keeps host secrets in `/var/lib/gci/secrets/<app>`; remove them explicitly with `gci secret rm <name>`.

Every service with a `port` gets a stable virtual IP (VIP) from the host's VIP range (`10.77.1.0/24` by default) and the name `<service>.<app>.internal`, e.g. `postgres.platform.internal:5432`. The VIP belongs to Envoy, not to a container: Envoy proxies it to whichever container is current, so the address never changes across deploys. Services with a `health` path are proxied as HTTP (request draining, access logs); everything else as plain TCP. Containers have `<app>.internal` as a search domain, so a bare `postgres` also works within an app.

`*.internal` lookups are forwarded from the bridge's dnsmasq to Envoy's DNS listener, which answers from the same config it routes with, so deploys never rewrite DNS or `/etc/hosts`. Existing TCP connections are not migrated when a backend is replaced; clients reconnect to the new one through the same VIP.

## Revisions

A service is redeployed only when its revision changes. The revision includes the service definition (excluding `source` and `routes`), its Nix environment, release key, and a digest of that service's resolved secret values. Changing a secret replaces the service container while reusing its release; changing only `routes` updates Envoy without restarting anything.
