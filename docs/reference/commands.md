# Commands

## Core

### `gci init`

Initialize a new app configuration in the current directory.

### `gci deploy`

Run build hooks, sync stack files, and deploy to the configured server.

### `gci status`

Show app container/service status.

### `gci logs`

Show service logs.

## Server Management

### `gci server add`

Register a server alias with SSH details.

### `gci server ls`

List configured servers.

### `gci server rm`

Remove a configured server.

## Templating

### `gci template render`

Render templates with git and app context.

Examples:

```bash
gci template render deploy.yaml.tmpl
gci template render deploy.yaml.tmpl deploy.yaml
gci template render -i deploy.yaml.tmpl
```

Template values:

```text
{{ .GitSHA }}
{{ .GitShortSHA }}
{{ .AppNetwork }}
{{ .ServiceName }}
{{ git_sha }}
{{ git_short_sha }}
{{ app_network }}
```

## LLM Docs

### `gci agents.md`

Print project and configuration guidance intended for coding agents and LLM workflows.
