# Using Template Variables

Use GCI template variables to stamp images and generated files with deploy metadata.

Available values:

```text
{{ .GitSHA }}
{{ .GitShortSHA }}
{{ .AppNetwork }}
{{ .ServiceName }}
{{ git_sha }}
{{ git_short_sha }}
{{ app_network }}
```

## `gci.toml`

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_app:{{ .GitShortSHA }} .
docker push 127.0.0.1:41114/my_app:{{ .GitShortSHA }}
"""

build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml.tmpl"
mode = "services"
```

`docker-compose.prod.yaml.tmpl`:

```yaml
version: "3.9"

services:
  app:
    image: 127.0.0.1:41114/my_app:{{ .GitShortSHA }}
    labels:
      gci.service: "{{ .ServiceName }}"
      gci.commit: "{{ .GitSHA }}"
    networks:
      - app_net
    deploy:
      replicas: 2

networks:
  app_net:
    external: true
    # rendered automatically by GCI during deploy
    name: "{{ .AppNetwork }}"
```

GCI renders stack compose files automatically during deploy before syncing to the server.
