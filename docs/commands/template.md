# `gci template`

Render template files with git and app metadata.

## Usage

```bash
gci template render <input_file> [output_file]
```

## Useful flags

- `-i, --inplace`: overwrite input file in place.

## Examples

```bash
gci template render deploy.yaml.tmpl
gci template render deploy.yaml.tmpl deploy.yaml
gci template render -i docker-compose.prod.yaml.tmpl
```

## Available template values

```text
{{ .GitSHA }}
{{ .GitShortSHA }}
{{ .AppNetwork }}
{{ .ServiceName }}
{{ git_sha }}
{{ git_short_sha }}
{{ app_network }}
```
