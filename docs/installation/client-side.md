# Client Side Installation

## Install GCI

On your local machine:

```bash
go install https://github.com/sauercrowd/gci
```

If you prefer to install from release binaries without running an install script, download the archive for your platform from the GitHub releases page:

- <https://github.com/sauercrowd/gci/releases/latest>
- Supported targets are `linux`, `darwin`, and `windows`, with `amd64` and `arm64`
- Example archive names:
  - `gci_1.2.3_linux_amd64.tar.gz`
  - `gci_1.2.3_darwin_arm64.tar.gz`
  - `gci_1.2.3_windows_amd64.zip`

Then extract the binary and move it into a directory on your `PATH`, for example `~/.local/bin` on Unix or `%USERPROFILE%\\AppData\\Local\\gci\\bin` on Windows.

If you install to `~/.local/bin`, make sure `~/.local/bin` is in your `PATH`.

Example on Unix after downloading an archive:

```bash
tar -xzf gci_1.2.3_linux_amd64.tar.gz
install -m 755 gci ~/.local/bin/gci
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

If you use the default Unix location, make sure `~/.local/bin` is in your `PATH`.

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
