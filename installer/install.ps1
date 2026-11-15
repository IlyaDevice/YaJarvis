# Jarvis for Yandex Station - one-line install on Windows (PowerShell):
#   irm https://github.com/__REPO__/releases/latest/download/install.ps1 | iex
# Downloads the installer and runs it: a page opens in the browser, finds the speaker
# on the local network and installs Jarvis. (ASCII only: irm may decode the script as Latin-1.)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$repo = if ($env:JARVIS_REPO) { $env:JARVIS_REPO } else { '__REPO__' }
if (-not [Environment]::Is64BitOperatingSystem) { Write-Error 'Jarvis installer needs 64-bit Windows'; return }
$name = 'JarvisSetup-windows-amd64.exe'
$base = "https://github.com/$repo/releases/latest/download"
$dir = Join-Path $env:TEMP 'JarvisSetup'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir $name
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Write-Host "Downloading Jarvis installer from $repo ..."
Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile $exe
$sumsFile = Join-Path $dir 'SHA256SUMS'
try { Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sumsFile } catch { $sumsFile = $null }
if ($sumsFile) {
    $line = Get-Content $sumsFile | Where-Object { $_ -match (' ' + [regex]::Escape($name) + '\s*$') } | Select-Object -First 1
    if ($line) {
        $want = ($line -split '\s+')[0].ToLower()
        $got = (Get-FileHash -Algorithm SHA256 $exe).Hash.ToLower()
        if ($want -ne $got) { Write-Error 'Checksum mismatch - the download is damaged, please try again'; return }
    }
}
Write-Host 'Starting the installer - a page will open in your browser.'
Start-Process -FilePath $exe
