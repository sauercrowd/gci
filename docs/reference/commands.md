# Commands

Global flags: `-c/--config` (default `gci.toml`) and `-s/--server` (default: `server` from `gci.toml`, or the only registered server).

| Command | Description |
| --- | --- |
| `gci server add <name> --host h[:port] [-u user] [-i key]` | Register an SSH host in `~/.gci/servers.toml`. Use `--local` to target this machine directly. |
| `gci server ls` / `gci server rm <name>` | List or remove registered hosts. |
| `gci bootstrap [--http-port 80] [--subnet 10.77.0.1/24] [--vip-range 10.77.1.0/24]` | Prepare a host once (see [Installation](/installation/)). |
| `gci deploy` | Upload changed sources, build Nix envs, and roll changed services. |
| `gci status` | Show each service's container state, unit state, IP, port, and revision. |
| `gci logs <service> [-n 100] [-f]` | Show a service's journal. |
| `gci rm [--volumes]` | Remove all of the app's containers and routes. Volumes are kept unless `--volumes` is passed. |

The first time a command runs against a host, gci uploads itself to `/usr/local/bin/gci` (verified by SHA-256) and runs host-side work through hidden `gci agent ...` subcommands with `sudo -n`.
