<#
.SYNOPSIS
    Build the AgentsView Windows NSIS installer on a local Windows machine.

.DESCRIPTION
    Builds the frontend SPA, restores the pricing snapshot, compiles the
    Go sidecar binary, patches tauri.conf.json with the resolved version,
    and runs `tauri build --bundles nsis` to produce the .exe installer.

    Handles the common local-build quirks:
    - Adds NSIS to PATH if installed in the default location.
    - Restores the embedded LiteLLM pricing snapshot from the git artifact.
    - When the Rust toolchain is GNU (x86_64-pc-windows-gnu), also writes
      an MSVC-named sidecar copy so Tauri's x64 NSIS bundler finds it.
    - Restores tauri.conf.json on exit so version patching is non-destructive.

.PARAMETER VersionSuffix
    Optional prerelease suffix appended to the base version, e.g. "-bow"
    yields "0.43.0-bow". The base version is derived from
    AGENTSVIEW_VERSION or git describe. The suffix is inserted before
    any git-describe "-dev.N" tail.

.PARAMETER SkipFrontend
    Skip the frontend build step. Use when frontend dist is already
    built and copied into internal/web/dist.

.PARAMETER SkipSidecar
    Skip the Go sidecar build step. Use when the sidecar binary is
    already in desktop/src-tauri/binaries/.

.EXAMPLE
    .\scripts\build-windows-installer.ps1
    Build with the git-derived version (e.g. 0.43.0-dev.843).

.EXAMPLE
    .\scripts\build-windows-installer.ps1 -VersionSuffix bow
    Build with version "0.43.0-bow" (when HEAD is the 0.43.0 tag).

.EXAMPLE
    .\scripts\build-windows-installer.ps1 -VersionSuffix bow -SkipFrontend
    Rebuild only the sidecar and installer, reusing the existing frontend.
#>
param(
    [string]$VersionSuffix = "",
    [switch]$SkipFrontend,
    [switch]$SkipSidecar
)

$ErrorActionPreference = "Stop"

# --- helpers ---

function ConvertTo-Semver {
    param([string]$Raw)
    $Raw = $Raw -replace '^v', ''
    if ($Raw -match '^(\d+\.\d+\.\d+)-(\d+)-g[0-9a-f]+(-dirty)?$') {
        return "$($Matches[1])-dev.$($Matches[2])"
    }
    if ($Raw -match '^\d+\.\d+\.\d+(-[a-zA-Z0-9-]+(\.[a-zA-Z0-9-]+)*)?(-dirty)?$') {
        return ($Raw -replace '-dirty$', '')
    }
    if ($Raw -match '^\d+\.') {
        Write-Error "Malformed version tag: $Raw"
        exit 1
    }
    return ""
}

function Join-SemverSuffix {
    param([string]$Semver, [string]$Suffix)
    if (-not $Suffix) { return $Semver }
    if (-not $Semver) { return $Semver }
    # Strip leading dash from suffix for uniform joining.
    $Suffix = $Suffix -replace '^-', ''
    if (-not $Suffix) { return $Semver }
    # base is everything before the first prerelease dash.
    $base = $Semver
    $pre = ""
    if ($Semver -match '^(\d+\.\d+\.\d+)(-.*)$') {
        $base = $Matches[1]
        $pre = $Matches[2]
    }
    if ($pre) {
        return "$base-$Suffix$pre"
    }
    return "$base-$Suffix"
}

function Update-TauriVersion {
    param([string]$Version, [string]$ConfPath)
    if (-not $Version) {
        Write-Host "Skipping tauri.conf.json version patch (empty semver)" -ForegroundColor Yellow
        return
    }
    $origPath = "$ConfPath.orig"
    if (-not (Test-Path $origPath)) {
        Copy-Item $ConfPath $origPath
    }
    $content = Get-Content $ConfPath -Raw
    $content = $content -replace '"version":\s*"[^"]*"', "`"version`": `"$Version`""
    Set-Content -Path $ConfPath -Value $content -NoNewline
    Write-Host "Patched tauri.conf.json version to $Version" -ForegroundColor Green
}

function Restore-TauriVersion {
    param([string]$ConfPath)
    $origPath = "$ConfPath.orig"
    if (Test-Path $origPath) {
        Move-Item -Force $origPath $ConfPath
    }
}

function Ensure-NsisOnPath {
    if (Get-Command makensis -ErrorAction SilentlyContinue) {
        return
    }
    $candidates = @(
        "C:\Program Files\NSIS",
        "C:\Program Files (x86)\NSIS",
        "$env:LOCALAPPDATA\Programs\NSIS"
    )
    foreach ($dir in $candidates) {
        $exe = Join-Path $dir "makensis.exe"
        if (Test-Path $exe) {
            $env:PATH = "$dir;$env:PATH"
            Write-Host "Added NSIS to PATH: $dir" -ForegroundColor Green
            return
        }
    }
    Write-Warning "makensis not found on PATH or in default install locations. NSIS bundling may fail."
}

# --- locate repo root ---

$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not (Test-Path "$RepoRoot\go.mod")) {
    $RepoRoot = Split-Path -Parent $PSScriptRoot
}
if (-not (Test-Path "$RepoRoot\go.mod")) {
    Write-Error "Could not locate repository root (no go.mod found)"
    exit 1
}

$DesktopDir   = Join-Path $RepoRoot "desktop"
$FrontendDir  = Join-Path $RepoRoot "frontend"
$EmbedDir     = Join-Path $RepoRoot "internal\web\dist"
$BinDir       = Join-Path $DesktopDir "src-tauri\binaries"
$TauriConf    = Join-Path $DesktopDir "src-tauri\tauri.conf.json"
$DistDir      = Join-Path $RepoRoot "dist\desktop\windows"

# Detect Rust host triple for the sidecar binary name.
if (-not (Get-Command rustc -ErrorAction SilentlyContinue)) {
    Write-Error "rustc not found on PATH. Install Rust and retry."
    exit 1
}
$Triple = (rustc -vV | Select-String "^host:").ToString().Split(" ", 2)[1].Trim()
if (-not $Triple) {
    Write-Error "Could not detect Rust host triple."
    exit 1
}
Write-Host "Rust host triple: $Triple" -ForegroundColor Cyan

Ensure-NsisOnPath

# --- resolve version ---

$rawVersion = $env:AGENTSVIEW_VERSION
if (-not $rawVersion) {
    $rawVersion = git -C $RepoRoot describe --tags --always --dirty 2>$null
}
if (-not $rawVersion) { $rawVersion = "dev" }

$semver = ConvertTo-Semver $rawVersion
$semver = Join-SemverSuffix $semver $VersionSuffix
if (-not $semver) {
    # Fall back to a bare base if git-describe produced a non-tag hash.
    if ($VersionSuffix) {
        $semver = "0.0.0-$($VersionSuffix -replace '^-', '')"
    } else {
        $semver = "0.0.0"
    }
    Write-Host "Using fallback version: $semver" -ForegroundColor Yellow
}
Write-Host "Resolved version: $semver" -ForegroundColor Green

# --- build frontend ---

if (-not $SkipFrontend) {
    Write-Host "Building frontend..." -ForegroundColor Cyan
    Push-Location $FrontendDir
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    } finally {
        Pop-Location
    }

    if (Test-Path $EmbedDir) {
        Remove-Item -Recurse -Force $EmbedDir
    }
    Copy-Item -Recurse (Join-Path $FrontendDir "dist") $EmbedDir
    Write-Host "Frontend dist copied to $EmbedDir" -ForegroundColor Green
} else {
    Write-Host "Skipping frontend build (-SkipFrontend)" -ForegroundColor Yellow
    if (-not (Test-Path (Join-Path $EmbedDir "index.html"))) {
        Write-Error "Frontend dist missing at $EmbedDir. Run without -SkipFrontend first."
        exit 1
    }
}

# --- build Go sidecar ---

if (-not $SkipSidecar) {
    Write-Host "Building Go sidecar..." -ForegroundColor Cyan
    $commit = git -C $RepoRoot rev-parse --short HEAD 2>$null
    if (-not $commit) { $commit = "unknown" }
    $buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $ldflags = "-X main.version=$semver -X main.commit=$commit -X main.buildDate=$buildDate -s -w"

    if (-not (Test-Path $BinDir)) {
        New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    }

    $env:CGO_ENABLED = "1"
    Push-Location $RepoRoot
    try {
        Write-Host "Restoring pricing snapshot..." -ForegroundColor Cyan
        go run ./internal/pricing/cmd/litellm-snapshot -restore
        if ($LASTEXITCODE -ne 0) { throw "pricing snapshot restore failed" }

        Write-Host "Compiling sidecar ($Triple)..." -ForegroundColor Cyan
        $sidecarHost = Join-Path $BinDir "agentsview-$Triple.exe"
        go build -tags fts5 -ldflags $ldflags -trimpath -o $sidecarHost ./cmd/agentsview
        if ($LASTEXITCODE -ne 0) { throw "go build sidecar failed" }
        Write-Host "Sidecar: $sidecarHost" -ForegroundColor Green

        # Tauri's x64 NSIS bundler on Windows looks for the MSVC triple
        # regardless of the local Rust toolchain. When the host is GNU,
        # add an MSVC-named copy so the bundler finds the sidecar.
        if ($Triple -eq "x86_64-pc-windows-gnu") {
            $sidecarMsvc = Join-Path $BinDir "agentsview-x86_64-pc-windows-msvc.exe"
            Copy-Item $sidecarHost $sidecarMsvc -Force
            Write-Host "Wrote MSVC-named sidecar copy: $sidecarMsvc" -ForegroundColor Green
        }
    } finally {
        Remove-Item env:CGO_ENABLED -ErrorAction SilentlyContinue
        Pop-Location
    }
} else {
    Write-Host "Skipping Go sidecar build (-SkipSidecar)" -ForegroundColor Yellow
}

# --- patch tauri.conf.json version and build NSIS ---

Update-TauriVersion -Version $semver -ConfPath $TauriConf

try {
    Write-Host "Running tauri build --bundles nsis..." -ForegroundColor Cyan
    Push-Location $DesktopDir
    try {
        # Tauri exits non-zero when the updater signing key is absent
        # even though the NSIS installer was produced successfully.
        # Let output stream normally; success is verified below by
        # checking for the installer file rather than $LASTEXITCODE.
        npx tauri build --bundles nsis
    } finally {
        Pop-Location
    }

    # --- collect installer into dist/desktop/windows ---

    $bundleDir = Join-Path $DesktopDir "src-tauri\target\release\bundle\nsis"
    $installer = Get-ChildItem $bundleDir -Filter "*-setup.exe" -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $installer) {
        throw "tauri build did not produce an installer in $bundleDir"
    }
    if (-not (Test-Path $DistDir)) {
        New-Item -ItemType Directory -Path $DistDir -Force | Out-Null
    }
    Remove-Item -Path (Join-Path $DistDir "*.exe") -ErrorAction SilentlyContinue
    Copy-Item $installer.FullName $DistDir
    Write-Host ""
    Write-Host "Installer: $(Join-Path $DistDir $installer.Name)" -ForegroundColor Green
    Write-Host "Size: $([math]::Round($installer.Length / 1MB, 1)) MB" -ForegroundColor Green
} finally {
    Restore-TauriVersion -ConfPath $TauriConf
}
