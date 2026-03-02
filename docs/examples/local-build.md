# Local Build

Build and push images from your local machine, then deploy on the server.

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_app .
docker push 127.0.0.1:41114/my_app
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"
prune_images = true
prune_containers_after = "24h"

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
```

`docker-compose.prod.yaml`:

```yaml
version: "3.9"

services:
  app:
    image: 127.0.0.1:41114/my_app
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
