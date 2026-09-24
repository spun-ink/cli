# Installs spun, the spun.ink command line, from its GitHub releases:
#
#   irm https://raw.githubusercontent.com/spun-ink/cli/main/scripts/install.ps1 | iex
#
# SPUN_VERSION picks a release (default: the latest), SPUN_INSTALL_DIR the directory
# (default: %LOCALAPPDATA%\Programs\spun). The archive is checked against the release's
# checksums.txt before anything is installed.
$ErrorActionPreference = 'Stop'

$repo = 'https://github.com/spun-ink/cli'
$installDir = if ($env:SPUN_INSTALL_DIR) { $env:SPUN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\spun' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "spun install: unsupported architecture $env:PROCESSOR_ARCHITECTURE" }
}

if ($env:SPUN_VERSION) {
  $tag = 'v' + $env:SPUN_VERSION.TrimStart('v')
} else {
  $latest = Invoke-RestMethod -Uri 'https://api.github.com/repos/spun-ink/cli/releases/latest' -Headers @{ 'User-Agent' = 'spun-install' }
  $tag = $latest.tag_name
}
$version = $tag.TrimStart('v')

$archive = "spun_${version}_windows_${arch}.zip"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading spun $version for windows/$arch"
  Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$tag/$archive" -OutFile (Join-Path $tmp $archive)
  Invoke-WebRequest -UseBasicParsing -Uri "$repo/releases/download/$tag/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $archive }
  if (-not $line) { throw "spun install: $archive is not listed in checksums.txt" }
  $expected = ($line -split '\s+')[0]
  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
  if ($expected -ne $actual) { throw "spun install: checksum mismatch for $archive - nothing was installed" }

  Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force
  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  Move-Item -Force (Join-Path $tmp 'spun.exe') (Join-Path $installDir 'spun.exe')
} finally {
  Remove-Item -Recurse -Force $tmp
}

$exe = Join-Path $installDir 'spun.exe'
Write-Host "Installed $(& $exe --version) to $exe"
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($userPath -split ';') -contains $installDir)) {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$installDir", 'User')
  Write-Host "Added $installDir to your user PATH - open a new terminal to use it."
}
Write-Host 'Next: spun signup (a new account) or spun login (an existing one).'
