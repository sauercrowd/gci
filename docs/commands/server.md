# `gci server`

Manage server entries used by app configs.

## Add a server

```bash
gci server add <name> \
  --host <host> \
  --user <user> \
  --private-key <path>
```

Useful flags:

- `--service-dir <path>`: remote base directory for services.
- `--skip-check`: skip SSH connectivity validation.

## List servers

```bash
gci server ls
```

## Remove a server

```bash
gci server rm <name>
```

## Check SSH reachability

```bash
gci server status
```

## Manage Docker node labels

```bash
gci server add-label <server> <key=value>
gci server remove-label <server> <key>
```
