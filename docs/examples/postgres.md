# Postgres

Example with app + postgres services in the same stack.
This variant pins Postgres to a labeled node so its local volume stays on one machine.

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
  { stack = "app", name = "postgres" },
]

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
```

Typical compose service names in this setup are `app` and `postgres`.

## Pin Postgres to one node

Label the node where Postgres should always run:

```bash
gci server add-label prod db=true
```

Then add a placement constraint on the Postgres service.
This prevents Swarm from moving it to a different node unless that node has the same label.

`docker-compose.prod.yaml`:

```yaml
version: "3.9"

services:
  app:
    image: 127.0.0.1:41114/my_app
    environment:
      DATABASE_URL: postgres://app:secret@postgres:5432/app?sslmode=disable
    ports:
      - "8080:8080"
    networks:
      - app_net
    deploy:
      replicas: 2

  postgres:
    image: postgres:16
    environment:
      POSTGRES_DB: app
      POSTGRES_USER: app
      POSTGRES_PASSWORD: secret
    volumes:
      - pgdata:/var/lib/postgresql/data
    networks:
      - app_net
    deploy:
      replicas: 1
      placement:
        constraints:
          - node.labels.db == true

volumes:
  pgdata:

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "${GCI_APP_NETWORK}"
```
