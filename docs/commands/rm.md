# `gci rm`

Remove the deployed service from the remote server.

## Usage

```bash
gci rm [config_file]
```

## Useful flags

- `--server <name>`: override server from config.
- `-y, --yes`: skip confirmation prompt.

## Example

```bash
gci rm
gci rm --server prod --yes
```
