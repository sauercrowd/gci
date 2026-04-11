# gci.toml Overview

Use this page as the index for all configuration keys.

## Core Keys

- `name` (required): service/app identifier.
- `server` (optional): default target server alias.

## Detailed Sections

- [Build Configuration](/configuration/build)
- [Sync Configuration](/configuration/sync)
- [Docker Swarm Configuration](/configuration/docker-swarm)

## Full Example

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

build_remote = """
docker compose -f docker-compose.prod.yaml config >/dev/null
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

sync_paths = ["./ops", "./scripts"]
exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
app_network = "auto"
force_restart_services = true
prune_images = true
prune_containers_after = "24h"
log_services = [
  { stack = "app", name = "app" },
]

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
wait_timeout_seconds = 300
```
