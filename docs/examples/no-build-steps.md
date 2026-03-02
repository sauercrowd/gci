# No Build Steps

Use this when images are already built and tagged in your registry pipeline.

GCI will skip `build_local` and `build_remote` and log a warning, then continue with sync + deploy.

```toml
name = "my_platform"
server = "prod"

[driver_docker_swarm]
app_network = "auto"

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
    image: ghcr.io/your-org/my_app:2026-03-02
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
