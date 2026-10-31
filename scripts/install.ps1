# wandersort installer for Windows (PowerShell 5+).
#   irm https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.ps1 | iex
# Downloads the bare .exe from the GitHub release and refuses to install it
# unless it matches the release's checksums.txt.
# Env overrides:
#   $env:WS_VERSION = "v0.1.0"   install a specific tag (default: latest)
#   $env:WS_BINDIR  = "C:\tools" install location (default: %LOCALAPPDATA%\Programs\wandersort)

# A script block keeps $ErrorActionPreference out of the caller's session under iex
& {
  $ErrorActionPreference = "Stop"

  $repo = "jammutkarsh/wandersort"
  $bin  = "wandersort"

  # --- detect arch ------------------------------------------------------------
  $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "unsupported architecture '$($env:PROCESSOR_ARCHITECTURE)'" }
  }

  # --- resolve version --------------------------------------------------------
  $version = $env:WS_VERSION
  if (-not $version) {
    $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $version = $rel.tag_name
  }
  $num = $version.TrimStart("v")

  $asset = "${bin}_${num}_windows_${arch}.exe"
  $base  = "https://github.com/$repo/releases/download/$version"

  # --- download + verify ------------------------------------------------------
  $tmp = Join-Path $env:TEMP ("wandersort-" + [guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "Downloading $asset ($version)..."
    $download = Join-Path $tmp "$bin.exe"
    Invoke-WebRequest "$base/$asset" -OutFile $download -UseBasicParsing

    $sums = Join-Path $tmp "checksums.txt"
    Invoke-WebRequest "$base/checksums.txt" -OutFile $sums -UseBasicParsing

    $line = Get-Content $sums | Where-Object { ($_ -split '\s+')[1] -eq $asset } | Select-Object -First 1
    if (-not $line) { throw "$asset is not listed in checksums.txt" }
    $want = ($line -split '\s+')[0].ToLower()
    $got  = (Get-FileHash $download -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "checksum mismatch for $asset" }

    # --- install --------------------------------------------------------------
    $bindir = $env:WS_BINDIR
    if (-not $bindir) { $bindir = Join-Path $env:LOCALAPPDATA "Programs\wandersort" }
    New-Item -ItemType Directory -Force -Path $bindir | Out-Null
    $exe = Join-Path $bindir "$bin.exe"
    Move-Item -Force $download $exe

    # add to user PATH if missing
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$bindir*") {
      [Environment]::SetEnvironmentVariable("Path", "$userPath;$bindir", "User")
      Write-Host "Added $bindir to your user PATH (restart your shell to pick it up)."
    }

    Write-Host "Installed $bin -> $exe"
    & $exe --version
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }
}
