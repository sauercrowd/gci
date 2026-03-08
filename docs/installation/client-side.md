# Client Side Installation

## Install GCI

On your local machine:

```bash
go install https://github.com/sauercrowd/gci
```

Or use the release installer script, which defaults to a user-owned install directory:

```bash
curl -fsSL https://raw.githubusercontent.com/sauercrowd/gci/main/scripts/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/sauercrowd/gci/main/scripts/install.ps1 | iex
```

Default install locations:

- Unix: `~/.local/bin`
- Windows: `%USERPROFILE%\\AppData\\Local\\gci\\bin`

You can override the destination with `GCI_INSTALL_DIR`, `--bin-dir`, or `-InstallDir`.

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
