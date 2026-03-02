# Build Configuration

Build settings control commands run before deployment.

## `build_local`

- Type: `string`
- Optional.
- Runs on your local machine before file sync.
- Supports multiline TOML strings.
- Supports template rendering values/functions (for example git SHA and app network).

Example:

```toml
build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""
```

## `build_remote`

- Type: `string`
- Optional.
- Runs on the target server after file sync, inside the remote service directory.
- Supports multiline TOML strings.

Example:

```toml
build_remote = """
docker compose -f docker-compose.prod.yaml config >/dev/null
"""
```

## `build_forwards`

- Type: `array[string]`
- Optional.
- Local SSH forwards active only while `build_local` runs.
- Format is SSH `-L` style:
  - `"LOCAL_HOST:LOCAL_PORT:REMOTE_HOST:REMOTE_PORT"`

Example:

```toml
build_forwards = [
  "127.0.0.1:41114:127.0.0.1:41114",
]
```

Validation:

- Each entry must parse as a valid forward spec.
- If both `build_local` and `build_remote` are omitted, deploy continues and logs a warning.
