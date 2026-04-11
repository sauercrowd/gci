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

## Execute a command on a server

```bash
gci server exec <server> <command> [args...]
```

If no command is given, `gci` opens an interactive login shell.
When run from a terminal, `gci` requests a remote PTY and forwards stdin/stdout/stderr so interactive programs should work.

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
