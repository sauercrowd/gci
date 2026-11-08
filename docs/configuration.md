# Configuration (`gci.toml`)

Build inputs belong to reusable images. Services reference an image and define
only their runtime behavior:

```toml
app = "shop"

[images.web]
packages = ["nodejs_22"]
source = "."
env = { NODE_ENV = "production" }
carry_over = "deps"
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
| `carry_over` | Optional step name. Omitted means every build starts from a fresh base. |

Images not referenced by a service or deploy job are valid but are not built
during deployment; a non-deploy job prepares its image when run. Several
services can share an image: gci prepares it once, then gives each service its
own copy-on-write container.

## Services

| Key | Description |
| --- | --- |
| `image` | **Required.** Name of an existing image. |
| `command` | **Required.** Main process, run as the image user under systemd. |
| `restart` | `auto` or `manual`. Defaults to `manual` for services with volumes and `auto` otherwise. Manual services keep their running container when their revision changes; use `gci deploy --restart <service>` to apply pending changes. This does not restart an unchanged service. |
| `setup` | Shell script run before every start. |
| `env` | Runtime environment; overrides image environment. |
| `secrets` | `ENV_VAR = "secret-name"` mappings injected only at activation, never into image builds. |
| `port`, `health`, `health_command`, `health_timeout` | Port and deploy-time checks. HTTP/TCP/systemd health runs first; the optional command then runs as the image user with service environment and secrets. It can be set without `port` or `health`; timeout defaults to 120 seconds. |
| `volumes` | `name = "/path"` pairs. Services with volumes are stateful and default to `restart = "manual"`; when restarted, they are stopped then started on the same volume. |
| `depends_on` | Services or jobs deployed/run first. Cycles are rejected. |
| `routes` | Envoy routes; host defaults to `*`, prefix to `/`. |
| `routes[].tls` | `acme`, `self-signed`, `custom`, or `off`. Defaults to `self-signed` for `*`, wildcard hosts, localhost names, and IP literals; other hosts default to `acme`. ACME requires a public, non-wildcard DNS name. |
| `routes[].cert`, `routes[].key` | Secret names containing a custom certificate chain PEM and matching private key PEM. Both are required only when `tls = "custom"`; the certificate must be valid for the route host. |
| `cpu`, `memory` | Incus resource limits. |

## Jobs

Jobs are one-shot commands that run in an isolated Incus container from their
image's prepared release (or the base image when no release is needed).

| Key | Description |
| --- | --- |
| `image` | **Required.** Name of an existing image. |
| `run` | **Required.** Shell command to execute as the image user. |
| `env` | Job-specific environment, applied after image environment. |
| `secrets` | Explicit `ENV_VAR = "secret-name"` mappings. Service secrets are never inherited. |
| `volumes` | Optional mounts from one owning service; see volume jobs below. |
| `depends_on` | Services or jobs that must run first. |
| `cpu`, `memory` | Incus resource limits. |
| `timeout` | Maximum runtime in seconds; defaults to 600. |

A job becomes a deploy job when any service depends on it, directly or through
other services or jobs. Deploy jobs run in dependency order when their
configuration, image release, environment, or secrets change; unchanged jobs
are skipped. Other jobs run only when requested with `gci run <job>`, which
does not run dependencies. A failed deploy job aborts the deployment without
recording success, so it runs again on the next deploy. `gci run` streams output
and returns the job's exit status.

Jobs must be idempotent and backwards-compatible with the old service version,
because a successful deploy job can run before a later service health check
fails. Secrets are explicit per job and are never inherited from the service.

A volume job may use volumes owned by exactly one service. That service must
depend on the job, and no other service or job may depend on it. The job runs
during the owning service's rollout after the old container stops and before
the replacement starts. A manual-restart service and its changed volume jobs
remain pending until `gci deploy --restart <service>`.

## TLS and certificates

Routes use HTTPS by default. A normal DNS hostname defaults to `tls = "acme"` and is issued a Let's Encrypt certificate using HTTP-01 validation on host port 80. Wildcards, `localhost` names, IP addresses, and `*` default to `self-signed`; these routes still redirect HTTP to HTTPS. Set `tls = "off"` to serve that host over HTTP without a redirect. All routes for a host must use the same TLS mode and custom secret names.

For a custom certificate, store the full certificate chain and matching private key as app secrets, then refer to their names:

```toml
routes = [{ host = "app.example.com", prefix = "/", tls = "custom", cert = "origin-cert", key = "origin-key" }]
```

Create or update them with `gci secret set`. The certificate and key are validated before activation, and private keys are kept in the root-only host certificate store and the Envoy container's private SDS directory, not in its read-only config mount.

| Cloudflare SSL mode | Suitable origin certificate |
| --- | --- |
| Full | Self-signed works because Cloudflare does not validate the origin certificate. |
| Full (strict) | Use ACME or a custom certificate trusted by Cloudflare, such as a Cloudflare Origin CA certificate. |

Bootstrap defaults to host ports 80 and 443. ACME also requires the hostname to resolve to the host and inbound HTTP traffic to reach port 80. The daily `gci-renew.timer` retries ACME issuance and renews certificates before expiry.

Secret names use lowercase letters, digits, `.`, `_`, and `-`; environment
variable names follow shell identifier syntax. A key cannot appear in both
`env` and `secrets`.

Build steps and jobs always run in `/app`, which is created empty when the
image has no source. Health commands also run in `/app` when the image has
source; otherwise they run in the image user's home directory.

## Build snapshots

Every build runs all steps. With `carry_over`, the files present after that step
in the previous build are still there, so idempotent steps like `npm install` or
`go mod download` finish quickly. Steps must therefore be idempotent. With
`carry_over = "deps"`, gci can start from the newest release of the same image
whose build chain matches and has the `deps` snapshot. Steps after that boundary
do not affect the chain key; changes to packages, user, image environment, or
steps through the boundary start from a fresh base. Without `carry_over`, builds
always start fresh.

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
Envoy routes external traffic to the current container. A failed build, job,
missing secret, or health check does not promote a replacement.
