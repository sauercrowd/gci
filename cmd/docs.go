package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Print LLM-friendly documentation for this tool",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprint(cmd.OutOrStdout(), llmDocs)
	},
}

func init() {
	rootCmd.AddCommand(docsCmd)
}

const llmDocs = `# GCI Tool Docs (LLM-Oriented)

This command prints a compact, implementation-aligned explanation of how GCI works and how ` + "`gci.toml`" + ` is structured.

## What GCI Is

GCI is a deployment CLI for SSH-accessible servers running Docker Swarm.
It is designed to feel similar to "deploy and observe" workflows:

- Build artifacts or images locally and/or remotely.
- Sync project files to a remote service directory over SSH.
- Run a driver-specific deploy step (currently Docker Swarm).
- Inspect runtime state and logs.

No GCI agent runs on the server. The client performs SSH operations directly.

## High-Level Deploy Flow

When running ` + "`gci deploy`" + `:

1. Optional local build: run ` + "`build_local`" + ` on the client machine.
2. Sync files: upload ` + "`sync_paths`" + ` (minus ` + "`exclude_patterns`" + `) to:
   - remote path: ` + "`<server.service_dir>/<service.name>`" + `
3. Optional remote build: run ` + "`build_remote`" + ` on the server in that synced directory.
4. Driver deploy: execute driver logic (Docker Swarm stack deploy flow).

Requested sequence is explicitly supported:

` + "`build_local -> sync -> build_remote -> deploy`" + `

## Service Config: gci.toml

` + "```toml" + `
name = "my_platform"
server = "prod"

# Optional local build command (multiline supported)
build_local = """
docker build -t 127.0.0.1:41114/my_service .
docker push 127.0.0.1:41114/my_service
"""

# Optional remote build command, executed after sync
# in the remote service directory
# build_remote = """
# docker build -t 127.0.0.1:41114/my_service .
# docker push 127.0.0.1:41114/my_service
# """

# Optional SSH local forwards active during build_local (SSH -L style)
# build_forwards = [
#   "127.0.0.1:41114:127.0.0.1:41114",
# ]

# Required: list of local paths (relative to gci.toml) to sync
sync_paths = ["docker-compose.prod.yaml"]

# Optional path/glob excludes during sync
exclude_patterns = [".git", "node_modules", "__pycache__", "*.pyc"]

[driver_docker_swarm]
stack_name = "my_platform"
log_services = ["app", "migrate"]
compose_file = "docker-compose.prod.yaml"
# migration_service = "migrate"
# migration_strict = true
prune_images = true
` + "```" + `

## Config Semantics

- ` + "`name`" + `: service identifier; used in remote directory naming.
- ` + "`server`" + `: optional default server alias; can be overridden by CLI flag.
- ` + "`build_local`" + `: optional command run on the local machine before sync.
- ` + "`build_remote`" + `: optional command run remotely after sync.
- At least one of ` + "`build_local`" + ` or ` + "`build_remote`" + ` must be present.
- ` + "`build_forwards`" + `: optional local TCP forwards during local build.
- ` + "`sync_paths`" + `: required list of files/dirs to transfer.
- ` + "`exclude_patterns`" + `: optional skip rules for sync.

### Docker Swarm Driver Block

- ` + "`stack_name`" + `: target swarm stack name.
- ` + "`log_services`" + `: services shown by default in ` + "`gci logs`" + `.
- ` + "`compose_file`" + `: compose file path relative to synced service directory.
- ` + "`migration_service`" + `: optional one-shot migration service.
- ` + "`migration_strict`" + `: if true, fail deploy when migration fails.
- ` + "`prune_images`" + `: prune unused images post-deploy (default true when omitted).

## Related Commands

- ` + "`gci init <service_name>`" + `: scaffold ` + "`gci.toml`" + ` and example compose file.
- ` + "`gci doctor`" + `: validate local config + remote prerequisites.
- ` + "`gci status`" + `: show remote service/stack status.
- ` + "`gci logs`" + `: stream service logs.
`
