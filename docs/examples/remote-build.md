# Remote Build

Sync source to the server and build there.

```toml
name = "my_platform"
server = "prod"

build_remote = """
docker build -t local-image .
"""

exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
app_network = "auto"

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
wait_timeout_seconds = 300
```

`docker-compose.prod.yaml`:

```yaml
version: "3.9"

services:
  app:
    image: local-image
    ports:
      - "8080:8080"
    networks:
      - app_net
    deploy:
      replicas: 2

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "${GCI_APP_NETWORK}"
```

For multi-node Swarm, use a shared registry instead of a local-only image tag.
