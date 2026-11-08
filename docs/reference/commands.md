# Commands

Global flags: `-c/--config` (default `gci.toml`) and `-s/--server` (default: `server` from `gci.toml`, or the only registered server).

| Command | Description |
| --- | --- |
| `gci init [--app name]` | Write a commented starter `gci.toml` (or the `-c` path) locally. The app name defaults to the directory name; an existing file is never overwritten. |
| `gci server add <name> --host h[:port] [-u user] [-i key]` | Register an SSH host in `~/.gci/servers.toml`. Use `--local` to target this machine directly. |
| `gci server ls` / `gci server rm <name>` | List or remove registered hosts. |
| `gci bootstrap [--http-port 80] [--https-port 443] [--subnet ADDRESS/CIDR] [--vip-range 10.77.1.0/24] [--storage-size 50GiB] [--acme-email EMAIL] [--acme-directory URL] [--acme-ca-bundle PATH]` | Prepare a host once, including the btrfs-backed `gci` storage pool, HTTPS listener, and certificate-renewal timer. An omitted subnet reuses the existing bridge address or defaults to `10.77.0.1/24`; changing an existing bridge subnet is unsupported (see [Installation](/installation/)). |
| `gci deploy [--clean] [--restart <service>]` | Upload changed image sources, realize Nix envs, run deploy jobs, and roll changed services. `--clean` forces image builds to start from a fresh base; the equivalent environment setting is `GCI_CLEAN=1`, `true`, or `yes`. Repeat `--restart` (or pass comma-separated names) to apply pending changes to manual services; unchanged services are not restarted. |
| `gci run <job> [--clean]` | Run only the named job, stream its output, and return its exit code. Jobs share their image's build cache. Dependencies are not run; volume jobs can run only during their owning service's rollout. `--clean` or `GCI_CLEAN=1` forces a fresh-base image build. |
| `gci status` | Show each service's container state, unit state, IP, port, and revision, plus TLS certificate mode, source, issuer, and expiry for the app's routed hosts. |
| `gci logs <service> [-n 100] [-f]` | Show a service's journal. |
| `gci rm [--volumes]` | Remove all of the app's containers and routes. Volumes and caches are kept unless `--volumes` is passed; host secrets are kept. |
| `gci secret set <name>` | Read a secret value from stdin and store it for this app. |
| `gci secret ls` | List this app's secret names (never values). |
| `gci secret rm <name>` | Remove one of this app's secrets. |

The first time a command runs against a host, gci uploads itself to `/usr/local/bin/gci` (verified by SHA-256) and runs host-side work through hidden `gci agent ...` subcommands with `sudo -n`.

When `gci secret set` reads from a terminal, enter the value and finish with Ctrl-D. For non-interactive use, pipe the value on stdin; it is never passed as a command-line argument.
Bootstrap defaults to Let's Encrypt's production ACME directory. `--acme-email` is optional; `--acme-directory` and `--acme-ca-bundle` support alternate ACME servers and their CA trust bundle. The host enables `gci-renew.timer`, which runs daily to retry ACME issuance and renew expiring certificates.

Run a CI job after deploying the CLI and configuring its server:

```yaml
- name: Run tests
  run: gci run test
```

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
