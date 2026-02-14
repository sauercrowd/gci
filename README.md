# GCI - fly.io like deployments for any server

![](./logo.png)

Fly.io like deployments for VMs, Raspberry Pis, or any SSH server, powered by docker swarm (which adds benefits even with a single node).

## Quickstart

Prerequisites:
- any server with ssh and docker swarm enabled+initialized
- a docker registry, e.g. start one with `docker run --name local-registry -p 127.0.0.1:41114:127.0.0.1:5000 registry:2`

1. Register a server (our VM, needs to have docker swarm installed and initalized)

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa \
```

2. Initialize a stack

```bash
gci init my_website
```

This creates `gci.toml` in the current directory.

Example shape:

```toml
name = "bucceo"
server = "prod"
build_command = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

# local TCP forwards active during build (SSH -L style)
build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

# make sure to sync the docker compose file
sync_paths = ["docker-compose.prod.yaml"]

[driver_docker_swarm]
stack_name = "bucceo"
log_services = ["app", "migrate"]
compose_file = "docker-compose.prod.yaml"
# migration_service = "migrate"
# migration_strict = true
prune_images = true
```

3. Deploy

```bash
gci deploy
```

Deploy does:
1. run local `build_command`
2. sync `sync_paths` to `<service_dir>/<service_name>` over SSH (`tar.gz` stream)
3. run driver deploy

4. Observe Runtime

Service status:

```bash
gci status
```

Logs:

```bash
gci logs
```

