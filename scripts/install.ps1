param(
  [string]$Version = $env:GCI_VERSION,
  [string]$InstallDir = $env:GCI_INSTALL_DIR
)

$ErrorActionPreference = "Stop"

if (-not $Version -or $Version -eq "") {
  $Version = "latest"
}

if (-not $InstallDir -or $InstallDir -eq "") {
  $InstallDir = Join-Path $HOME "AppData\\Local\\gci\\bin"
}

$repo = "sauercrowd/gci"

if ($Version -eq "latest") {
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
  $Version = $release.tag_name
}

if (-not $Version) {
  throw "Unable to resolve release version"
}

$archName = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
switch ($archName) {
  "x64" { $arch = "amd64" }
  "arm64" { $arch = "arm64" }
  default { throw "Unsupported architecture: $archName" }
}

$versionNoV = $Version.TrimStart('v')
$archive = "gci_${versionNoV}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$Version/$archive"

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("gci-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmpDir | Out-Null

try {
  $archivePath = Join-Path $tmpDir $archive
  Write-Host "Downloading $url"
  Invoke-WebRequest -Uri $url -OutFile $archivePath

  Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Copy-Item -Path (Join-Path $tmpDir "gci.exe") -Destination (Join-Path $InstallDir "gci.exe") -Force

  Write-Host "Installed gci.exe to $InstallDir"
  Write-Host "Make sure '$InstallDir' is in your PATH."
} finally {
  Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
