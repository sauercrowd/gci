# Using Migrations

Run schema migrations as a job stack, separate from long-running app services.

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
log_services = [
  { stack = "app", name = "app" },
  { stack = "migrations", name = "migrate" },
]

[[driver_docker_swarm.stacks]]
name = "migrations"
compose_file = "docker-compose.migrations.yaml"
mode = "job"
wait_timeout_seconds = 600

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
wait_timeout_seconds = 300
```

`docker-compose.migrations.yaml`:

```yaml
version: "3.9"

services:
  migrate:
    image: 127.0.0.1:41114/my_app
    command: ["./bin/migrate", "up"]
    networks:
      - app_net
    deploy:
      replicas: 1
      restart_policy:
        condition: none

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "${GCI_APP_NETWORK}"
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
