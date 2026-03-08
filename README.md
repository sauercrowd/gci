# GCI

Fly.io style deployments for any VM, Raspberry Pi, or SSH server.

Full documentation: https://gci.jonas.foo

## What You Get
- SSH-based deployments, powered by Docker Swarm (no additional software on the server)
- Local and remote build
- Deploy status monitoring
- Configurable deployment with gci.toml

## Quickstart

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

## Advanced commands

To make it easier to update containers at the right time, GCI includes a templating tool
that injects a few variables into build step but can also be triggered independently.

Template rendering with git/deploy context:

```bash
# stdout
gci template render deploy.yaml.tmpl

# write to file
gci template render deploy.yaml.tmpl deploy.yaml

# in-place
gci template render -i deploy.yaml.tmpl
```

Template variables/functions:

- `{{ .GitSHA }}`
- `{{ .GitShortSHA }}`
- `{{ .AppNetwork }}` (when `gci.toml` is found)
- `{{ .ServiceName }}` (when `gci.toml` is found)
- `{{ git_sha }}`
- `{{ git_short_sha }}`
- `{{ app_network }}`

The same template values/functions are also rendered in `build_local` and `build_remote` during `gci deploy`.

