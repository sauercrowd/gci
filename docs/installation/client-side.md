# Client Side Installation

## Install GCI

On your local machine:

```bash
go install https://github.com/sauercrowd/gci
```

## Register a Server

Create a server alias the app config will reference:

```bash
gci server add prod \
  --host your-server.example.com \
  --private-key ~/.ssh/id_rsa
```

## Verify Setup

```bash
gci server ls
```

Next step: [Docs Home Quickstart](/) or [Example Configurations](/examples/).
