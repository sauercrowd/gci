# GCI - fly.io like deployments for any server

![](./logo.png)

Fly.io like deployments for VMs, Raspberry Pis, or any SSH server.

## Core Flow

The intended workflow is:

1. Register a server.
2. Initialize service config.
3. Validate with doctor.
4. Deploy.
5. Inspect status/logs.
6. Remove when needed.

## 1) Register a Server

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa \
  --service-dir ./gci-deployments
```

Notes:
- `--user` defaults to your current user.
- `--service-dir` defaults to `./gci-deployments`.
- `--skip-check` skips the SSH connectivity check during add.
- SSH host key verification is strict; target hosts must exist in your `known_hosts`.

List and inspect servers:

```bash
gci server ls
gci server status
```

## 2) Initialize Service Config

```bash
gci init bucceo --server prod
```

This creates `gci.toml` in the current directory.

Example shape:

```toml
name = "bucceo"
server = "prod"
build_command = "go build ./..."

# Optional: local TCP forwards active during build (SSH -L style)
# build_forwards = [
#   "127.0.0.1:5433:127.0.0.1:5432",
# ]

sync_paths = ["."]
exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
stack_name = "bucceo"
log_services = ["app", "migrate"]
compose_file = "docker-compose.prod.yaml"
# migration_service = "migrate"
# migration_strict = true
prune_images = true
```

## 3) Validate Before Deploy

```bash
gci doctor
```

Checks:
- local config validity
- server config validity
- SSH connectivity
- remote driver readiness (Docker Swarm availability)

## 4) Deploy

```bash
gci deploy
```

Deploy does:
1. run local `build_command`
2. sync `sync_paths` to `<service_dir>/<service_name>` over SSH (`tar.gz` stream)
3. run driver deploy

Docker Swarm deploy:
- `docker stack deploy -c <compose_file> <stack_name>`
- optional migration trigger via `migration_service`
- poll every 2s until stack is stable

## 5) Observe Runtime

Service status:

```bash
gci status
```

Logs:

```bash
gci logs
gci logs --lines 300
gci logs --follow
gci logs --service app
```

## 6) Remove Service

```bash
gci rm
```

By default, this asks for confirmation, then:
- removes stack via driver
- deletes remote service directory

Skip prompt:

```bash
gci rm --yes
```

## Config Selection

By default, root commands (`deploy`, `doctor`, `status`, `logs`, `rm`) read `gci.toml` in the current directory.

You can override with an explicit path:

```bash
gci deploy path/to/other.toml
```
