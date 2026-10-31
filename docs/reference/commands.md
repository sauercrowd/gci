# Commands

Global flags: `-c/--config` (default `gci.toml`) and `-s/--server` (default: `server` from `gci.toml`, or the only registered server).

| Command | Description |
| --- | --- |
| `gci init [--app name]` | Write a commented starter `gci.toml` (or the `-c` path) locally. The app name defaults to the directory name; an existing file is never overwritten. |
| `gci server add <name> --host h[:port] [-u user] [-i key]` | Register an SSH host in `~/.gci/servers.toml`. Use `--local` to target this machine directly. |
| `gci server ls` / `gci server rm <name>` | List or remove registered hosts. |
| `gci bootstrap [--http-port 80] [--subnet 10.77.0.1/24] [--vip-range 10.77.1.0/24] [--storage-size 50GiB]` | Prepare a host once, including the btrfs-backed `gci` storage pool (see [Installation](/installation/)). |
| `gci deploy [--clean]` | Upload changed sources, build Nix envs, and roll changed services. `--clean` forces source services to rebuild from a fresh base; the equivalent environment setting is `GCI_CLEAN=1`, `true`, or `yes`. |
| `gci status` | Show each service's container state, unit state, IP, port, and revision. |
| `gci logs <service> [-n 100] [-f]` | Show a service's journal. |
| `gci rm [--volumes]` | Remove all of the app's containers and routes. Volumes and caches are kept unless `--volumes` is passed; host secrets are kept. |
| `gci secret set <name>` | Read a secret value from stdin and store it for this app. |
| `gci secret ls` | List this app's secret names (never values). |
| `gci secret rm <name>` | Remove one of this app's secrets. |

The first time a command runs against a host, gci uploads itself to `/usr/local/bin/gci` (verified by SHA-256) and runs host-side work through hidden `gci agent ...` subcommands with `sudo -n`.

When `gci secret set` reads from a terminal, enter the value and finish with Ctrl-D. For non-interactive use, pipe the value on stdin; it is never passed as a command-line argument.

### Clean builds in GitHub Actions

Set `GCI_CLEAN` to request a fresh-base build manually or by including `[clean]` in the commit message:

```yaml
on:
  push:
  workflow_dispatch:
    inputs:
      clean: { type: boolean, default: false }
# ...
- run: gci deploy
  env:
    GCI_CLEAN: ${{ inputs.clean || contains(github.event.head_commit.message, '[clean]') }}
```
