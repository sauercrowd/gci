# Configuration (`gci.toml`)

Build inputs belong to reusable images. Services reference an image and define
only their runtime behavior:

```toml
app = "shop"

[images.web]
packages = ["nodejs_22"]
source = "."
env = { NODE_ENV = "production" }
snapshot_step = "deps"
steps = [
  { name = "deps", run = "npm install --no-audit --no-fund" },
  { name = "build", run = "npm run build" },
]

[services.web]
image = "web"
command = "node server.js"
port = 8080
health = "/healthz"
health_command = "node check.js"
routes = [{ host = "shop.example.com", prefix = "/" }]
```

## Images

| Key | Description |
| --- | --- |
| `packages` | Nix expressions evaluated with `nixpkgs` in scope and combined into the image's runtime environment. |
| `user` | User for build steps and service commands. Defaults to `gci`. |
| `source` | Optional local directory, relative to `gci.toml`, uploaded to `/app`. `.git`, `node_modules`, and `__pycache__` are skipped. |
| `env` | Environment inherited by build steps and services using the image. Service `env` overrides it. |
| `steps` | Ordered `{ name?, run }` commands executed in `/app` (created empty when no source is configured). Names are optional and unique per image. |
| `snapshot_step` | Optional step name that marks the reusable build boundary. Omitted means every build starts from a fresh base. |

Images not referenced by a service are valid but are not built. Several services
can share an image: gci prepares it once, then gives each service its own
copy-on-write container.

## Services

| Key | Description |
| --- | --- |
| `image` | **Required.** Name of an existing image. |
| `command` | **Required.** Main process, run as the image user under systemd. |
| `setup` | Shell script run before every start. |
| `migrate` | Optional migration command. Stateless services migrate in a temporary copy; services with volumes migrate after the old container stops and before the new one starts. Migrations have a 10-minute timeout and should be idempotent and backwards-compatible. |
| `env` | Runtime environment; overrides image environment. |
| `secrets` | `ENV_VAR = "secret-name"` mappings injected only at activation, never into image builds. |
| `port`, `health`, `health_command`, `health_timeout` | Port and deploy-time checks. HTTP/TCP/systemd health runs first; the optional command then runs as the image user. Timeout defaults to 120 seconds. |
| `volumes` | `name = "/path"` pairs. Services with volumes are stateful and are replaced stop-then-start. |
| `depends_on` | Services deployed first. Cycles are rejected. |
| `routes` | Envoy routes; host defaults to `*`, prefix to `/`. |
| `cpu`, `memory` | Incus resource limits. |

Secret names use lowercase letters, digits, `.`, `_`, and `-`; environment
variable names follow shell identifier syntax. A key cannot appear in both
`env` and `secrets`.

Build steps always run in `/app`, which is created empty when the image has no
source. Health and migration commands run in `/app` when the image has source;
otherwise they run in the image user's home directory.

## Build snapshots

All steps run on every deploy, in order. With `snapshot_step = "deps"`, gci can
start from the newest release of the same image whose build chain matches and
has the `deps` snapshot. Steps after that boundary do not affect the chain key;
changes to packages, user, image environment, or steps through the boundary
start from a fresh base. Without `snapshot_step`, builds always start fresh.

The snapshot is taken immediately after the named step, with the build cache
device detached. A snapshot on the final step instead starts from the previous
release itself, carrying its output and caches forward; this can leave stale
artifacts (for example, JavaScript emitted by `tsc` for a deleted source file).
Choose an earlier dependency step when generated output should be recreated.
Source files are synchronized with a manifest, so removed source files are
deleted without sweeping files created by build steps.

`gci deploy --clean` or `GCI_CLEAN=1` (also `true` or `yes`) forces fresh-base
builds. Image releases are retained per image: the newest three plus any release
currently used by a service. Image environment changes affect release keys;
service environment changes do not.

Node projects should use `npm install --no-audit --no-fund` in a dependency step,
not `npm ci`, so the snapshot can be reused while the command still resolves
dependencies.

## Storage and service discovery

Build steps share a persistent per-image `gcicache-<app>-<image>` volume mounted
at `/var/cache/gci`. `gci rm --volumes` removes application volumes and caches.
Host secrets remain under `/var/lib/gci/secrets/<app>` until explicitly removed.

Services with ports receive stable VIPs and `<service>.<app>.internal` names.
Envoy routes external traffic to the current container. A failed build,
migration, missing secret, or health check does not promote a replacement.
