# GCI

A CLI for Fly.io style deployments for any VM, Raspberry Pi, or SSH server.
Powered by docker swarm.

Full documentation: https://gci.jonas.foo

## What You Get
- SSH-based deployments, powered by Docker Swarm (no additional software on the server)
- Local and remote build
- Deploy status monitoring
- Configurable deployment with gci.toml

## Install

Install with Go:

```bash
go install github.com/sauercrowd/gci@latest
```

Or use the release install scripts:

MacOS/Linux

```bash
curl -fsSL https://raw.githubusercontent.com/sauercrowd/gci/main/scripts/install.sh | sh
```

Windows

```powershell
irm https://raw.githubusercontent.com/sauercrowd/gci/main/scripts/install.ps1 | iex
```

For manual binary downloads and platform-specific details, see <https://gci.jonas.foo/installation/client-side>.

## Quickstart

1. Register a server - a single server can be used for as many apps as you like. it just acts as an alias.

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa
```

2. Initialize a GCI app (can include/manage many different containers). This creates `gci.toml` in the current directory.

```bash
gci init my_platform
```

Minimal config example:

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

# local TCP forwards active during local build (SSH -L style)
build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
force_restart_services = true

# define a stack
[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
```

3. Deploy

```bash
gci deploy
```

Deploy does:
1. Execute your build step (+ proxy the ports specified)
2. sync your docker compose file
3. (re)deploy your services, monitoring for their success

### Other commands

Show the status of all containers of your app:

```bash
gci status
```

Logs:

```bash
gci logs
```

LLM-oriented project and config reference:

```bash
gci agents.md
```

Full documentation: https://gci.jonas.foo
