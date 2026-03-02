# GCI - fly.io like deployments for any server

Fly.io like deployments any VM, Raspberry Pi, or any SSH server.


It's just a CLI, only requires docker swarm and ssh on the remote host.

## Quickstart


It comes with a `gci agents.md` command that will describe your favourite coding tool how to set it up and use it.


<details>
<summary>Setup</summary>

#### Server side setup
Your server needs to have ssh set up and docker installed ([Docker setup guide](https://docs.docker.com/engine/install/))

After installing,  initialize a new swarm setup with `docker swarm init` (this can be joined by other machines later if you like, but is not required)

It's recommended to setup a docker registry (non-public, bound to localhost), which you can do with
```
docker run -d --name local-registry --restart always -p 127.0.0.1:41114:5000 registry:3
```

GCI takes care of proxying docker requests at the right time to the registry, so you dont have to worry about that.

#### Client side setup

On Linux/macOS, install the latest release binary with:

```bash
curl -fsSL https://raw.githubusercontent.com/sauercrowd/gci/main/install.sh | sh
```

Pin a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/sauercrowd/gci/main/install.sh | sh -s -- --version v0.1.0
```

On Windows (PowerShell):

```powershell
iwr https://raw.githubusercontent.com/sauercrowd/gci/main/install.ps1 -UseBasicParsing | iex
```

Or install from source with Go:

```bash
go install github.com/sauercrowd/gci@latest
```

You dont need any other dependencies

</details>


1. Register a server - a single server can be used for as many apps as you like. it just acts as an alias.

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa \
```

2. Initialize a GCI app (can include/manage many different containers)

```bash
gci init my_platform
```

This creates `gci.toml` in the current directory.

Example shape:

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

# local TCP forwards active during local build (SSH -L style)
build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]

[driver_docker_swarm]
app_network = "auto"
log_services = ["app"]

[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
```


3. Deploy

```bash
gci deploy
```

Deploy does:
1. Execute your build step (+ proxy the ports specified)
2. sync your docker compose file
3. (re)deploy your services, monitoring for their success

### Other commands
Show the status of all containers of your app

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


## Advanced commands
To make it easier to update containers at the right time, GCI includes a templating tool
that injects a few variables into build_step but can also be triggered independently.
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

## Releasing binaries

GitHub Actions builds release binaries for:
- linux amd64/arm64
- darwin amd64/arm64
- windows amd64/arm64

To publish a new release, push a version tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

This runs `.github/workflows/release.yml` and publishes archives to GitHub Releases.
