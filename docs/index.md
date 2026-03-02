# GCI

Fly.io style deployments for any VM, Raspberry Pi, or SSH server.

## What You Get

- SSH-based deployments to Docker Swarm
- Local and remote build hooks
- Deploy status monitoring
- Configurable stack deployment with `gci.toml`

## Quickstart in 3 Steps

1. Register a server:

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa
```

2. Initialize your app config (this creates `gci.toml`):

```bash
gci init my_platform
```

3. Deploy:

```bash
gci deploy
```

For full setup details, start at [Installation](/installation/) and [Example Configurations](/examples/).

## Quick Links

- [Installation](/installation/)
- [Server Side Installation](/installation/server-side)
- [Client Side Installation](/installation/client-side)
- [Configuration](/configuration/)
- [gci.toml Overview](/configuration/gci-toml)
- [Build Configuration](/configuration/build)
- [Sync Configuration](/configuration/sync)
- [Docker Swarm Configuration](/configuration/docker-swarm)
- [Example Configurations](/examples/)
- [Cloudflare Deployment Setup](/installation/cloudflare-deployment)
- [Commands Overview](/commands/)
