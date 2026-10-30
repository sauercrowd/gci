# Configuration (`gci.toml`)

```toml
app     = "platform"        # ^[a-z][a-z0-9-]{0,23}$
server  = "prod"            # optional: default server for this app
nixpkgs = "github:NixOS/nixpkgs/<rev>"   # optional: pinned by default

[services.<name>]
packages       = ["postgresql_16", "python3.withPackages (p: [ p.psycopg ])"]
source         = "./app"
command        = "python3 server.py"
setup          = "..."
user           = "gci"
env            = { KEY = "value" }
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
| `source` | A local directory, relative to `gci.toml`, that is mounted read-only at `/app` and used as the working directory. `.git`, `node_modules`, and `__pycache__` are skipped. |
| `command` | **Required.** The main process, run under systemd as `gci-<name>.service` with `Restart=always`. |
| `setup` | A shell script run before every start (`ExecStartPre`), for example first-boot initialisation. |
| `user` | The user to run as. Default `gci`. It is created in the container, and volumes are owned by it. |
| `env` | Environment variables. |
| `port` | The port the service listens on. Required for `routes`. |
| `health` | An HTTP path that must return 2xx/3xx. If only `port` is set, the check is a TCP connect. With neither, the check is that the systemd unit is active. |
| `health_timeout` | Seconds to wait for health. Default 120. A unit that restarts 5 times fails the deploy early. |
| `volumes` | `name = "/path"` pairs. Each is an Incus custom volume named `gci-<app>-<name>`, and it survives redeploys and `gci rm`. A service with volumes is **stateful**: it is stopped, then started, rather than rolled. |
| `depends_on` | Services that are deployed first. Cycles are rejected. |
| `routes` | Envoy routes. `host` defaults to `*` and `prefix` to `/`. |
| `cpu`, `memory` | Incus `limits.cpu` and `limits.memory`. |

Every service with a `port` gets a stable virtual IP (VIP) from the host's VIP range (`10.77.1.0/24` by default) and the name `<service>.<app>.internal`, e.g. `postgres.platform.internal:5432`. The VIP belongs to Envoy, not to a container: Envoy proxies it to whichever container is current, so the address never changes across deploys. Services with a `health` path are proxied as HTTP (request draining, access logs); everything else as plain TCP. Containers have `<app>.internal` as a search domain, so a bare `postgres` also works within an app.

`*.internal` lookups are forwarded from the bridge's dnsmasq to Envoy's DNS listener, which answers from the same config it routes with, so deploys never rewrite DNS or `/etc/hosts`. Existing TCP connections are not migrated when a backend is replaced; clients reconnect to the new one through the same VIP.

## Revisions

A service is redeployed only when its revision changes. The revision is a hash of the service definition (excluding `routes`), its Nix environment, and its source hash. Changing only `routes` updates Envoy without restarting anything.
