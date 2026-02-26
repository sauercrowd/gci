# GCI Tool Docs (LLM-Oriented)

This command prints a compact, implementation-aligned explanation of how GCI works and how `gci.toml` is structured.

## What GCI Is

GCI is a deployment CLI for SSH-accessible servers running Docker Swarm.
It is designed to feel similar to "deploy and observe" workflows:

- Build artifacts or images locally and/or remotely.
- Sync project files to a remote service directory over SSH.
- Run a driver-specific deploy step (currently Docker Swarm).
- Inspect runtime state and logs.

No GCI agent runs on the server. The client performs SSH operations directly.

Think of it like the `fly.io` experience on any ssh target.

Why docker swarm instead of compose? docker swarm already comes with a bunch of nice primitives (rolling updates).

## Servers

gci tracks a bunch of servers - basically just aliases - so that different people using the same repo can have different ssh keys to access a box.
All these live in the `server` command group.


## Creating an app
An app is create with the gci init command

It can contain a number of docker swarm stacks, so not just the app but also dependencies/infra around it.

## High-Level Deploy Flow

When running `gci deploy`:

1. Optional local build: run `build_local` on the client machine.
2. Sync files: upload all `stacks[].compose_file` plus optional `sync_paths` (minus `exclude_patterns`) to:
   - remote path: `<server.service_dir>/<service.name>`
3. Optional remote build: run `build_remote` on the server in that synced directory.
4. Driver deploy: execute driver logic (Docker Swarm stack deploy flow).

Requested sequence is explicitly supported:

`build_local -> sync -> build_remote -> deploy`

Genreally speaking the idea is to run the local build in the ci, build a few docker images, push them out to a registry, and then update the deployment.

## Service Config: gci.toml

```toml
name = "my_platform"
server = "prod"

# Optional local build command (multiline supported)
build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
# Example template usage:
# docker build -t your-registry/my_service:{{ .GitShortSHA }} .
"""

# Optional remote build command, executed after sync
# in the remote service directory
# build_remote = """
# docker build -t 127.0.0.1:41114/my_service .
# docker push 127.0.0.1:41114/my_service
# """

# Optional SSH local forwards active during build_local (SSH -L style)
# build_forwards = [
#   "127.0.0.1:41114:127.0.0.1:41114",
# ]

# Optional extra local paths (relative to gci.toml) to sync.
# Compose files in driver_docker_swarm.stacks[].compose_file are always included.
# sync_paths = ["./ops", "./scripts"]

# Optional path/glob excludes during sync
exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
app_network = "auto"
log_stack = "my_platform_app"
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

## Config Semantics

- `name`: service identifier; used in remote directory naming.
- `server`: optional default server alias; can be overridden by CLI flag.
- `build_local`: optional command run on the local machine before sync.
- `build_remote`: optional command run remotely after sync.
- At least one of `build_local` or `build_remote` must be present.
- `build_local` and `build_remote` are rendered as templates before execution.
- `build_forwards`: optional local TCP forwards during local build.
- `sync_paths`: optional extra files/dirs to transfer.
- `exclude_patterns`: optional skip rules for sync.

### Docker Swarm Driver Block

- `app_network`: shared swarm overlay network for all stacks; `auto` resolves to `gci_net_<app_name>`.
- `stacks`: ordered deployment list; each stack deploys and then waits before the next starts.
- `stacks[].name`: target swarm stack name.
- `stacks[].compose_file`: compose file path relative to synced service directory.
- `stacks[].compose_file` entries are always synced, even when `sync_paths` is omitted.
- `stacks[].mode`: wait strategy (`services` for desired replicas, `job` for one-shot completion).
- `stacks[].wait_timeout_seconds`: optional custom wait timeout per stack.
- `log_stack`: optional stack used by `gci logs` (defaults to the last stack).
- `log_services`: services shown by default in `gci logs`.
- `prune_images`: prune unused images post-deploy (default true when omitted).

## Template Rendering

Use `gci template render` to render files with deployment context values.

The idea is to use this for creating properly version docker images, so when updating stacks in sequence that really only the right components get updated at the right time.

```bash
# render to stdout
gci template render path/to/input.tmpl

# render to output file
gci template render path/to/input.tmpl path/to/output.txt

# render in-place
gci template render -i path/to/input.tmpl
```

Template values available:

- `{{ .GitSHA }}` (full SHA)
- `{{ .GitShortSHA }}` (short SHA)
- `{{ .AppNetwork }}` (resolved shared app network, when `gci.toml` is found)
- `{{ .ServiceName }}` (service name from `gci.toml`, when found)
- `{{ git_sha }}` (function form)
- `{{ git_short_sha }}` (function form)
- `{{ app_network }}` (function form)

Notes:

- Git SHA is resolved from the git repo containing the input file path.
- `-i` cannot be combined with an explicit output file.
- `AppNetwork`/`ServiceName` are resolved from nearest `gci.toml` when available.
- The same template values/functions are also available in `build_local` and `build_remote` during `gci deploy`.

## End-to-End Example (How To Use GCI)

The intended pattern is:

- Work around Docker Swarm's missing temporal dependency model by using ordered `driver_docker_swarm.stacks` (infra -> migration job -> platform).
- Use one shared `app_network` so services in different stacks can reach each other by DNS/service name (for example platform -> db/redis across stack boundaries).
- Use template rendering (`{{ .GitShortSHA }}` etc.) so built image tags are versioned by git SHA.
- Choose one image build strategy:
  - `build_remote` if you do not have a registry (build directly on the remote host).
  - `build_local` + push if you can run a registry.
- If running a registry on the remote host, you can start one with:

```bash
docker run --name local-registry --restart always -p 127.0.0.1:41114:127.0.0.1:5000 registry:3
```

- Then use `build_forwards` so local build/push can reach that remote localhost-bound registry port over SSH forwarding.

Example config shape:

```toml
name = "my_platform"
server = "prod"

build_local = """
docker build -t 127.0.0.1:41114/my_app:{{ .GitShortSHA }} .
docker push 127.0.0.1:41114/my_app:{{ .GitShortSHA }}
"""

# Alternative when skipping registry:
# build_remote = """
# docker build -t my_app:{{ .GitShortSHA }} .
# """

build_forwards = ["127.0.0.1:41114:127.0.0.1:41114"]
# Optional extra sync paths (compose_file entries are always synced):
# sync_paths = ["./ops", "./scripts"]

[driver_docker_swarm]
app_network = "auto"
log_stack = "my_platform_app"
log_services = ["app"]

[[driver_docker_swarm.stacks]]
name = "my_platform_infra"
compose_file = "docker-compose.infra.yaml"
mode = "services"

[[driver_docker_swarm.stacks]]
name = "my_platform_migration"
compose_file = "docker-compose.migration.yaml"
mode = "job"
# Best practice: this stack should only run migration jobs.

[[driver_docker_swarm.stacks]]
name = "my_platform_platform"
compose_file = "docker-compose.platform.yaml"
mode = "services"
```

## Related Commands

- `gci agents.md`: read this if you are an LLM.
- `gci init <service_name>`: scaffold `gci.toml` and example compose file.
- `gci doctor`: validate local config + remote prerequisites.
- `gci status`: show remote service/stack status.
- `gci logs`: stream service logs.
- `gci template render <input_file> [output_file]`: render templates with git/deploy context values.

## Other things to consider
When setting up github actions (or any CI), an ssh key needs to be made available somehow, and then in the github action `gci server add`  and then `gci deploy` needs to be excuted. Fairly simpe.
