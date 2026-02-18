# GCI - fly.io like deployments for any server

![](./logo.png)

Fly.io like deployments any VM, Raspberry Pi, or any SSH server.


## Quickstart

It's just a CLI, only requires docker swarm and ssh on the remote host.

Here's the great news: it comes with a `gci agents.md` command that will describe your favourite coding tool how to set it up and use it.


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

## run locally
build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
# or use git sha template values:
# docker build -t 127.0.0.1:41114/my_service:{{ .GitShortSHA }} .
"""

# alternatively, you can also (or only) run a command on the remote
# e.g. if you just want to sync all the code to the target machine and build images there
# this step is executed after the sync step
# build_remote = """
# docker build -t 127.0.0.1:41114/my_service .
# docker push 127.0.0.1:41114/my_service
# """

# local TCP forwards active during local build (SSH -L style)
# so we can reach the registry bound to localhost on the server
build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

# make sure to sync the docker compose file
# since we're building locally, but can also be the whole folder
sync_paths = ["docker-compose.prod.yaml"]

# currently only supports docker swarm, but might support others in the future
[driver_docker_swarm]
app_network = "auto"
log_services = ["app", "migrate"]

[[driver_docker_swarm.stacks]]
name = "my_platform_infra"
compose_file = "docker-compose.infra.yaml"
mode = "services"

[[driver_docker_swarm.stacks]]
name = "my_platform_migration"
compose_file = "docker-compose.migration.yaml"
mode = "job"

[[driver_docker_swarm.stacks]]
name = "my_platform_app"
compose_file = "docker-compose.prod.yaml"
mode = "services"

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
gci agents.md
```

Template rendering with git/deploy context:

```bash
# stdout
gci template render deploy.yaml.tmpl

# write to file
gci template render deploy.yaml.tmpl deploy.yaml

# in-place
gci template render -i deploy.yaml.tmpl
```

Template variables/functions:
- `{{ .GitSHA }}`
- `{{ .GitShortSHA }}`
- `{{ .AppNetwork }}` (when `gci.toml` is found)
- `{{ .ServiceName }}` (when `gci.toml` is found)
- `{{ git_sha }}`
- `{{ git_short_sha }}`
- `{{ app_network }}`

The same template values/functions are also rendered in `build_local` and `build_remote` during `gci deploy`.
