param(
    [string]$RemoteHost = $(if ($env:INTEL_DEPLOY_HOST) { $env:INTEL_DEPLOY_HOST } else { "primary-server" }),
    [switch]$MigrateHermes,
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$ServerDir = Join-Path $Root "server"
$DistDir = Join-Path $Root "dist"
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

Push-Location $ServerDir
try {
    if (-not $SkipTests) {
        Write-Host "[1/4] Running Go unit tests..." -ForegroundColor Cyan
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw "Go tests failed" }
    }

    Write-Host "[2/4] Cross-compiling static Linux amd64 binary (CGO_ENABLED=0)..." -ForegroundColor Cyan
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $BinPath = Join-Path $DistDir "intel-hub-linux-amd64"
    go build -trimpath -ldflags="-s -w" -o $BinPath .\cmd\intel-hub
    if ($LASTEXITCODE -ne 0) { throw "Go build failed" }
} finally {
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Pop-Location
}

Write-Host "[3/4] Uploading binary, systemd unit, and Hermes skill to $RemoteHost..." -ForegroundColor Cyan
ssh $RemoteHost "mkdir -p ~/.local/bin ~/.hermes/intel/backups ~/.hermes/skills/intel-hub ~/.config/systemd/user"
scp (Join-Path $DistDir "intel-hub-linux-amd64") "${RemoteHost}:~/.local/bin/intel-hub.new"
scp (Join-Path $Root "deploy\intel-hub.service") "${RemoteHost}:~/.config/systemd/user/intel-hub.service"
scp (Join-Path $Root "deploy\cloudflared.service") "${RemoteHost}:~/.config/systemd/user/cloudflared.service"
scp (Join-Path $Root "configs\hermes-skill\SKILL.md") "${RemoteHost}:~/.hermes/skills/intel-hub/SKILL.md"
scp (Join-Path $Root "configs\config.example.yaml") "${RemoteHost}:~/.hermes/intel/config.example.yaml"

Write-Host "[4/4] Activating binary and restarting intel-hub.service..." -ForegroundColor Cyan
$MigrateCmd = ""
if ($MigrateHermes) {
    $MigrateCmd = "~/.local/bin/intel-hub migrate-hermes; "
}
ssh $RemoteHost "chmod +x ~/.local/bin/intel-hub.new && mv ~/.local/bin/intel-hub.new ~/.local/bin/intel-hub && if [ ! -f ~/.hermes/intel/config.yaml ]; then cp ~/.hermes/intel/config.example.yaml ~/.hermes/intel/config.yaml; fi && $MigrateCmd systemctl --user daemon-reload && systemctl --user enable --now intel-hub.service && systemctl --user restart intel-hub.service && sleep 1 && ~/.local/bin/intel-hub query stats"

Write-Host "Deployment to $RemoteHost complete!" -ForegroundColor Green
