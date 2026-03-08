# React + Express + Nginx

Serve React static files from Nginx and reverse-proxy `/api` to Express.

## `gci.toml`

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_api ./backend
docker push 127.0.0.1:41114/my_api
docker build -t 127.0.0.1:41114/my_web -f Dockerfile.web .
docker push 127.0.0.1:41114/my_web
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"
log_services = [
  { stack = "app", name = "web" },
  { stack = "app", name = "api" },
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
    ports:
      - "80:80"
    networks:
      - app_net
    deploy:
      replicas: 2

  api:
    image: 127.0.0.1:41114/my_api
    environment:
      NODE_ENV: production
      PORT: 3000
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

`nginx.conf` (included in `my_web` image):

```nginx
server {
  listen 80;
  server_name _;
  root /usr/share/nginx/html;
  index index.html;

  location /api/ {
    proxy_pass http://api:3000/;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
  }

  location / {
    try_files $uri /index.html;
  }
}
```
