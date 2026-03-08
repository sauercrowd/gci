# Rolling Releases

Docker Swarm supports rolling-update strategies through `deploy.update_config` and `deploy.rollback_config`.
These settings control how many tasks update at once, whether old tasks stop before or after new ones start, and what happens on failure.

## Strategy: Start-First Rolling Update

Use this when you want minimal downtime during updates.

```yaml
services:
  app:
    image: 127.0.0.1:41114/my_app:latest
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        delay: 10s
        order: start-first
        failure_action: rollback
        monitor: 30s
      rollback_config:
        parallelism: 1
        delay: 10s
        order: stop-first
```

Behavior:

- Starts new tasks before stopping old tasks.
- Good for web APIs and frontends where overlap is acceptable.

## Strategy: Stop-First Rolling Update

Use this when your service cannot run old and new versions at the same time.

```yaml
services:
  app:
    image: 127.0.0.1:41114/my_app:latest
    deploy:
      replicas: 3
      update_config:
        parallelism: 1
        delay: 10s
        order: stop-first
        failure_action: pause
        monitor: 30s
```

Behavior:

- Stops each old task before starting its replacement.
- Uses less peak capacity but may introduce brief downtime.

## Core Swarm Rollout Knobs

- `parallelism`: number of tasks updated simultaneously.
- `delay`: wait time between update batches.
- `order`: `start-first` or `stop-first`.
- `failure_action`: typically `pause` or `rollback`.
- `monitor`: observation window for task failure after update.

## Rollback Behavior

Use `deploy.rollback_config` to control how Swarm reverts a failed rollout.

```yaml
deploy:
  rollback_config:
    parallelism: 1
    delay: 10s
    order: stop-first
```

## Practical Defaults

- Stateless web services: `start-first`, `parallelism: 1`, `failure_action: rollback`.
- Stateful/single-writer workloads: `stop-first`, conservative `parallelism`.
