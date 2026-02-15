# GCI - fly.io like deployments for any server

![](./logo.png)

Fly.io like deployments any VM, Raspberry Pi, or any SSH server.

Just a CLI, only requires docker swarm and ssh on the remote host.

## Quickstart

<details>
<summary>Prerequisites</summary>

- any server with ssh and docker swarm enabled+initialized
- a docker registry. you can just start one on the remote server with 
```
docker run --name local-registry --restart always -p 127.0.0.1:41114:127.0.0.1:5000 registry:3
```

</details>
<details>
<summary>Installation</summary>

Only needed on the client, there is no additional software needed on the server

```
go install https://github.com/sauercrowd/gci
```

</details>

1. Register a server (the remote VM, needs to have docker swarm installed and initalized)

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa \
```

2. Initialize a stack

```bash
gci init my_platform
```

This creates `gci.toml` in the current directory.

Example shape:

```toml
name = "my_platform"
server = "prod"

## run locally (optional)
build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

# run remotely after sync (optional)
# build_remote = """
# docker build -t 127.0.0.1:41114/my_service .
# docker push 127.0.0.1:41114/my_service
# """

# local TCP forwards active during build (SSH -L style)
build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

# make sure to sync the docker compose file
sync_paths = ["docker-compose.prod.yaml"]

[driver_docker_swarm]
stack_name = "my_platform"
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
1. run local `build_local`
2. sync `sync_paths` to `<service_dir>/<service_name>` over SSH (`tar.gz` stream)
3. run remote `build_remote` in the synced remote service directory
4. run driver deploy

4. Observe Runtime

Service status:

```bash
gci status
```

Logs:

```bash
gci logs
```

LLM-oriented project and config reference:

```bash
gci docs
```
