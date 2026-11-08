# gci

Deploy services to a Linux host over SSH. Nix provides runtime packages,
Incus runs isolated system containers, and Envoy routes traffic to healthy
services.

## Quickstart

```bash
go install github.com/sauercrowd/gci@latest
gci server add prod --host prod.example.com -u ubuntu -i ~/.ssh/id_ed25519
gci bootstrap
cd examples/platform # or run `gci init` in your project
gci secret set postgres-password
gci secret set database-url
gci deploy
gci status
```

A project defines reusable images and services that reference them:

```toml
app = "shop"

[images.web]
packages = ["nodejs_22"]
source = "."
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
routes = [{ host = "shop.example.com", prefix = "/" }]
```

See [`examples/platform`](examples/platform) and
[`docs/configuration.md`](docs/configuration.md) for configuration details.

## How deployment works

The client uploads changed image sources. The host realizes each referenced
image's Nix environment and prepares that image once, before changing any
services. Every service sharing an image receives its own copy-on-write
container. Build steps run in order as the image user, with image environment
variables; service environment overrides them at runtime.

Images without `snapshot_step` start from a fresh base on every build. A named
snapshot lets later builds start from the saved state after that step, but all
steps rerun. A snapshot on the final step carries generated output forward and
may leave stale artifacts. Use an earlier dependency step when outputs should
be recreated. `gci deploy --clean` or `GCI_CLEAN=1` forces a fresh base.

Services can use `migrate` before activation. Stateless migrations run in a
temporary copy while the old version serves; stateful migrations run with the
volume attached after the old service stops. Envoy switches to a healthy
replacement before the old stateless container is removed. Secrets are injected
at runtime and are never part of image builds.

Each image keeps its newest three releases plus releases still used by a
service. Build cache volumes are per image; application volumes and secrets
persist across deployments.

For Node.js projects use `npm install --no-audit --no-fund` in a dependency
step, not `npm ci`.
