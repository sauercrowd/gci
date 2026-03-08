# Configuration

GCI is configured through a `gci.toml` file in your project directory.

## In This Section

- [gci.toml Overview](/configuration/gci-toml)
- [Build Configuration](/configuration/build)
- [Sync Configuration](/configuration/sync)
- [Docker Swarm Configuration](/configuration/docker-swarm)

## Minimal Example

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
app_network = "auto"
prune_images = true
prune_containers_after = "24h"

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
```
