# GCI

## Quickstart

1. Register a server:

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa
```

2. Initialize an app (creates `gci.toml`):

```bash
gci init my_platform
```

3. Deploy:

```bash
gci deploy
```

Full documentation: https://gci.jonas.foo
