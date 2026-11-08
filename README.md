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
routes = [{ host = "shop.example.com", prefix = "/" }]
```

See [`examples/platform`](examples/platform) and
[`docs/configuration.md`](docs/configuration.md) for configuration details.

Routed hosts use HTTPS by default: public DNS names use Let's Encrypt, while `*`, wildcard, localhost, and IP hosts use self-signed certificates. HTTP redirects to HTTPS unless the route sets `tls = "off"`. Bootstrap publishes HTTPS on port 443 by default; see the [TLS configuration guide](docs/configuration.md#tls-and-certificates) for custom certificates and ACME options.

## How a deploy works

The client uploads changed image sources. The host realizes each referenced
image's Nix environment and prepares that image once, before changing any
services. Every service sharing an image receives its own copy-on-write
container. Build steps run in order as the image user, with image environment
variables; service environment overrides them at runtime.

Every build runs all steps. With `carry_over`, the files present after that step
in the previous build are still there, so idempotent steps like `npm install` or
`go mod download` finish quickly. Steps must therefore be idempotent. Without
`carry_over`, each build starts from a fresh base. Setting it on the final step
carries generated output forward and may leave stale artifacts; use an earlier
dependency step when outputs should be recreated. `gci deploy --clean` or
`GCI_CLEAN=1` forces a fresh base.

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
