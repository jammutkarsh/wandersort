# wandersort installer for Windows (PowerShell 5+).
#   irm https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.ps1 | iex
# Env overrides:
#   $env:WS_VERSION = "v0.1.0"   install a specific tag (default: latest)
#   $env:WS_BINDIR  = "C:\tools" install location (default: %LOCALAPPDATA%\Programs\wandersort)
$ErrorActionPreference = "Stop"

$repo = "jammutkarsh/wandersort"
$bin  = "wandersort"

# --- detect arch --------------------------------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'" }
}

# --- resolve version ----------------------------------------------------------
$version = $env:WS_VERSION
if (-not $version) {
  $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
  $version = $rel.tag_name
}
$num = $version.TrimStart("v")

$asset = "${bin}_${num}_windows_${arch}.zip"
$base  = "https://github.com/$repo/releases/download/$version"

# --- download + verify --------------------------------------------------------
$tmp = Join-Path $env:TEMP ("wandersort-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading $asset ($version)..."
  $zip = Join-Path $tmp $asset
  Invoke-WebRequest "$base/$asset" -OutFile $zip

  $sums = Join-Path $tmp "checksums.txt"
  try { Invoke-WebRequest "$base/checksums.txt" -OutFile $sums } catch {}
  if (Test-Path $sums) {
    $want = (Select-String -Path $sums -Pattern ([regex]::Escape($asset)) | Select-Object -First 1).Line
    if ($want) {
      $want = $want.Split(" ")[0].ToLower()
      $got  = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
      if ($want -ne $got) { throw "checksum mismatch for $asset" }
    }
  }

  # --- install ----------------------------------------------------------------
  $bindir = $env:WS_BINDIR
  if (-not $bindir) { $bindir = Join-Path $env:LOCALAPPDATA "Programs\wandersort" }
  New-Item -ItemType Directory -Force -Path $bindir | Out-Null
  Expand-Archive -Path $zip -DestinationPath $bindir -Force

  # add to user PATH if missing
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$bindir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$bindir", "User")
    Write-Host "Added $bindir to your user PATH (restart your shell to pick it up)."
  }

  $exe = Join-Path $bindir "$bin.exe"
  Write-Host "Installed $bin -> $exe"
  & $exe --version
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
