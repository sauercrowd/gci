# Caddy Reverse Proxy

Use Caddy as a simple edge proxy in front of your app service.

## `gci.toml`

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_app ./app
docker push 127.0.0.1:41114/my_app
docker build -t 127.0.0.1:41114/my_caddy ./caddy
docker push 127.0.0.1:41114/my_caddy
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"
log_services = [
  { stack = "app", name = "caddy" },
  { stack = "app", name = "app" },
]

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
```

`docker-compose.prod.yaml`:

```yaml
version: "3.9"

services:
  caddy:
    image: 127.0.0.1:41114/my_caddy
    ports:
      - "80:80"
      - "443:443"
    networks:
      - app_net
    deploy:
      replicas: 1

  app:
    image: 127.0.0.1:41114/my_app
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

`Caddyfile` (included in `my_caddy` image):

```text
app.example.com {
  reverse_proxy app:8080
}
```
