# `gci logs`

Fetch service logs from the remote server.

Fetched logs are combined across selected services and sorted by Docker timestamp,
oldest first. Each line retains its service and task identity. Application stdout
and stderr are merged remotely and printed together on GCI's stdout.

Equal timestamps retain their received order. Multiline continuations stay with
the preceding entry; output without a timestamp before the first entry appears first.
Deployment failure diagnostics use the same ordering.

Follow mode supports one service and streams timestamped output in arrival order.
It does not buffer or reorder live messages.

## Usage

```bash
gci logs [config_file]
```

## Useful flags

- `--server <name>`: override server from config.
- `--lines <n>`: number of log lines to fetch (default `100`).
- `--follow` / `-f`: stream logs continuously.
- `--service <name>`: filter to one or more services.

## Examples

```bash
gci logs
gci logs --lines 300
gci logs --follow --service app
```
