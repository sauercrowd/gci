# Docker Swarm Configuration

GCI currently deploys via the `driver_docker_swarm` section.

## Required Table

```toml
[driver_docker_swarm]
```

## `app_network`

- Type: `string`
- Optional, default behavior is `"auto"`.
- `"auto"` (or empty) resolves to deterministic `gci_net_<app_name>`.
- Any other value is used as the exact network name.

## `log_services`

- Type: `array[inline table]`
- Optional.
- Controls default services shown by `gci logs`.

Each item:

- `name` (required)
- `stack` (optional, but must match a defined stack when set)

Example:

```toml
log_services = [
  { stack = "app", name = "web" },
  { stack = "app", name = "worker" },
]
```

## `force_restart_services`

- Type: `bool`
- Optional, default `true`.
- After each successful `docker stack deploy`, runs `docker service update --force` for every service in stacks with `mode = "services"`.
- Use this to guarantee a rollout even when the image tag and rendered service spec have not changed.

## `stacks`

- Type: `[[driver_docker_swarm.stacks]]`
- Required, at least one entry.

Each stack supports:

- `name` (required, must be unique)
- `compose_file` (required)
- `mode` (optional): `"services"` or `"job"`
- `wait_timeout_seconds` (optional): integer `>= 0`

Example:

```toml
[[driver_docker_swarm.stacks]]
name = "app"
compose_file = "docker-compose.prod.yaml"
mode = "services"
wait_timeout_seconds = 300
```

## `prune_images`

- Type: `bool`
- Optional, default `true`.
- Runs `docker image prune -f` after successful deploy when enabled.

## `prune_containers_after`

- Type: `string`
- Optional, default `"24h"`.
- Removes stopped containers older than this age (per stack) after deploy.

Allowed values:

- Go duration literals, for example `"30m"` or `"24h"`
- `"none"` to disable container pruning

Validation:

- Duration must be greater than `0` unless set to `"none"`.
