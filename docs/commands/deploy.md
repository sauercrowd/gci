# `gci deploy`

Deploy the current project to the configured server.

## Usage

```bash
gci deploy [config_file]
```

## Useful flags

- `--server <name>`: override server from `gci.toml`.

## What it does

1. Runs `build_local` if configured.
2. Syncs files to remote service directory.
3. Runs `build_remote` if configured.
4. Renders stack compose files automatically and syncs them.
5. Runs driver deploy actions (Docker Swarm).

## Example

```bash
gci deploy
gci deploy ./gci.toml --server staging
```
