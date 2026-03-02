# Sync Configuration

Sync settings control what gets uploaded to the server before deploy actions run.

## `sync_paths`

- Type: `array[string]`
- Optional.
- Extra files/directories (relative to `gci.toml`) to sync into the remote service directory.

Example:

```toml
sync_paths = ["./ops", "./scripts"]
```

Behavior:

- Compose files from `driver_docker_swarm.stacks[].compose_file` are synced automatically, even if not listed in `sync_paths`.
- Stack compose files are template-rendered automatically during deploy before they are synced.

## `exclude_patterns`

- Type: `array[string]`
- Optional.
- Excludes matching files/directories from sync.

Common default-style values:

```toml
exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]
```

Matching behavior:

- Full relative-path glob matching.
- Basename/segment matching for simple patterns without `/`.
- Directory-prefix matching when pattern ends with `/`.
