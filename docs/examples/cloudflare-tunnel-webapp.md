# Cloudflare Tunnel Webapp

Expose your internal web service without opening inbound ports on your server.

## `gci.toml`

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_web ./web
docker push 127.0.0.1:41114/my_web
docker build -t 127.0.0.1:41114/my_tunnel ./tunnel
docker push 127.0.0.1:41114/my_tunnel
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"
log_services = [
  { stack = "app", name = "web" },
  { stack = "app", name = "cloudflared" },
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
  web:
    image: 127.0.0.1:41114/my_web
    networks:
      - app_net
    deploy:
      replicas: 2

  cloudflared:
    image: cloudflare/cloudflared:latest
    command: tunnel --no-autoupdate run
    environment:
      - TUNNEL_METRICS=0.0.0.0:60123
      - TUNNEL_TOKEN=MY_TOKEN
    healthcheck:
      test: ["CMD", "cloudflared", "tunnel", "--metrics", "localhost:60123", "ready"]
      interval: 30s
      timeout: 30s
      retries: 3
    networks:
      - app_net

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "${GCI_APP_NETWORK}"
```

Use `TUNNEL_TOKEN` from your secret manager and keep it out of source control.
