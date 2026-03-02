# Controlling Dependencies

Use multiple stacks when rollout order matters.
In this pattern, we update infra containers first, run database migrations second, and only then deploy the app code.

GCI deploys stacks in the order they appear in `driver_docker_swarm.stacks`, so you can make dependencies explicit.

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_app .
docker push 127.0.0.1:41114/my_app
docker build -t 127.0.0.1:41114/my_migrate -f Dockerfile.migrate .
docker push 127.0.0.1:41114/my_migrate
docker build -t 127.0.0.1:41114/my_redis -f Dockerfile.redis .
docker push 127.0.0.1:41114/my_redis
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"
log_services = [
  { stack = "infra", name = "redis" },
  { stack = "migrations", name = "migrate" },
  { stack = "app", name = "app" },
]

[[driver_docker_swarm.stacks]]
name = "infra"
compose_file = "docker-compose.infra.yaml"
mode = "services"

[[driver_docker_swarm.stacks]]
name = "migrations"
compose_file = "docker-compose.migrations.yaml"
mode = "job"
wait_timeout_seconds = 600

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.app.yaml"
mode = "services"
wait_timeout_seconds = 300
```

`docker-compose.infra.yaml`:

```yaml
version: "3.9"

services:
  redis:
    image: 127.0.0.1:41114/my_redis
    networks:
      - app_net
    deploy:
      replicas: 1

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "${GCI_APP_NETWORK}"
```

`docker-compose.migrations.yaml`:

```yaml
version: "3.9"

services:
  migrate:
    image: 127.0.0.1:41114/my_migrate
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

`docker-compose.app.yaml`:

```yaml
version: "3.9"

services:
  app:
    image: 127.0.0.1:41114/my_app
    environment:
      REDIS_URL: redis://redis:6379
    ports:
      - "8080:8080"
    networks:
      - app_net
    deploy:
      replicas: 2
      update_config:
        parallelism: 1
        delay: 10s
        order: start-first
        failure_action: rollback

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "${GCI_APP_NETWORK}"
```
