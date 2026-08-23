# `gci logs`

Fetch service logs from the remote server.

For non-streaming output, services are printed by running-container start time:
the oldest appears first and the most recently started appears last.

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
