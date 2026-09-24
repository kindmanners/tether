<#
Copyright (C) 2026 kindmanners on github

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
#>

#requires -Version 5.1
<#
.SYNOPSIS
    Builds the local llama.cpp server used by the Tether Orchestrator.

.DESCRIPTION
    This is deliberately user-scoped. It builds a pinned llama-server with RPC
    support and, when requested, CUDA support. It never configures an Agent,
    opens firewall ports, or changes Tailscale state.
#>
[CmdletBinding()]
param(
    [switch]$InstallMissing,
    [switch]$LocalGPU,
    [string]$LlamaCppPath,
    [string]$ProgressPath,
    [string]$LlamaCppRevision = '3057bb66c86c46d5781e50e85462a760ba7d1feb'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-SetupProgress {
    param([string]$Step, [string]$Detail)
    Write-Host "`n==> $Detail" -ForegroundColor Cyan
    if (-not $ProgressPath) { return }
    $parent = Split-Path -Parent $ProgressPath
    if ($parent) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }
    $payload = [ordered]@{ step = $Step; detail = $Detail } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText($ProgressPath, $payload, [Text.UTF8Encoding]::new($false))
}

trap {
    if ($ProgressPath) {
        $payload = [ordered]@{ step = 'failed'; detail = $_.Exception.Message } | ConvertTo-Json -Compress
        [IO.File]::WriteAllText($ProgressPath, $payload, [Text.UTF8Encoding]::new($false))
    }
    throw
}

function Get-CommandPath([string]$Name) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($command) { return $command.Source }
    return $null
}

function Install-WingetPackage([string]$Id, [string]$Name, [string[]]$ExtraArguments = @()) {
    if (-not (Get-CommandPath 'winget')) {
        throw "WinGet is required to install $Name. Install Microsoft App Installer and retry."
    }
    Write-SetupProgress 'requirements' "Installing $Name with WinGet."
    $arguments = @('install', '--id', $Id, '--exact', '--source', 'winget', '--accept-package-agreements', '--accept-source-agreements', '--silent') + $ExtraArguments
    & winget @arguments
    if ($LASTEXITCODE -ne 0) { throw "WinGet could not install $Name (exit code $LASTEXITCODE)." }
}

function Get-MSBuild {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    if (-not (Test-Path -LiteralPath $vswhere)) { return $null }
    $result = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -find 'MSBuild\**\Bin\MSBuild.exe' 2>$null | Select-Object -First 1
    if ($LASTEXITCODE -ne 0 -or -not $result) { return $null }
    return $result.Trim()
}

function Get-CudaToolkit {
    $candidates = @()
    if ($env:CUDA_PATH -and (Test-Path -LiteralPath (Join-Path $env:CUDA_PATH 'bin\nvcc.exe'))) { $candidates += $env:CUDA_PATH }
    $base = Join-Path $env:ProgramFiles 'NVIDIA GPU Computing Toolkit\CUDA'
    if (Test-Path -LiteralPath $base) {
        $candidates += Get-ChildItem -LiteralPath $base -Directory | Where-Object { Test-Path -LiteralPath (Join-Path $_.FullName 'bin\nvcc.exe') } | ForEach-Object FullName
    }
    return $candidates | Sort-Object -Descending | Select-Object -First 1
}

function Refresh-ToolPaths {
    $paths = @(
        (Join-Path $env:ProgramFiles 'Git\cmd'),
        (Join-Path $env:ProgramFiles 'CMake\bin')
    ) | Where-Object { Test-Path -LiteralPath $_ }
    if ($paths.Count -gt 0) { $env:Path = (($paths + $env:Path) -join ';') }
}

function Enable-CudaRuntimePath([string]$CudaPath) {
    $cudaBin = Join-Path $CudaPath 'bin'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if ($entries -notcontains $cudaBin) {
        [Environment]::SetEnvironmentVariable('Path', (($entries + $cudaBin) -join ';'), 'User')
    }
    $env:Path = "$cudaBin;$env:Path"
}

if (-not $LlamaCppPath) {
    $dataRoot = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { $env:APPDATA }
    $LlamaCppPath = Join-Path $dataRoot 'tether\llama.cpp'
}

Write-SetupProgress 'requirements' 'Checking Windows build prerequisites for the local inference backend.'
if (-not (Get-CommandPath 'git') -and $InstallMissing) { Install-WingetPackage 'Git.Git' 'Git' }
if (-not (Get-CommandPath 'cmake') -and $InstallMissing) { Install-WingetPackage 'Kitware.CMake' 'CMake' }
Refresh-ToolPaths
if (-not (Get-CommandPath 'git')) { throw 'Git is required. Install Git or choose automatic setup again with WinGet available.' }
if (-not (Get-CommandPath 'cmake')) { throw 'CMake is required. Install CMake or choose automatic setup again with WinGet available.' }

if (-not (Get-MSBuild)) {
    if ($InstallMissing) {
        Install-WingetPackage 'Microsoft.VisualStudio.2022.BuildTools' 'Visual Studio 2022 Build Tools' @('--override', '--wait --passive --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended')
    }
    if (-not (Get-MSBuild)) { throw 'Visual Studio 2022 Build Tools with the C++ workload are required.' }
}

$cudaPath = $null
$buildName = 'build-rpc'
if ($LocalGPU) {
    if (-not (Get-CommandPath 'nvidia-smi')) { throw 'No NVIDIA driver was found. Use control-only mode or install a supported NVIDIA driver.' }
    & nvidia-smi -L | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'The NVIDIA driver did not report a usable GPU. Use control-only mode or repair the driver.' }
    $cudaPath = Get-CudaToolkit
    if (-not $cudaPath -and $InstallMissing) {
        Install-WingetPackage 'Nvidia.CUDA' 'NVIDIA CUDA Toolkit'
        $cudaPath = Get-CudaToolkit
    }
    if (-not $cudaPath) { throw 'The NVIDIA CUDA Toolkit is required when this Orchestrator contributes its GPU.' }
    $env:CUDA_PATH = $cudaPath
    $env:CudaToolkitDir = "$cudaPath\"
    Enable-CudaRuntimePath $cudaPath
    $buildName = 'build-rpc-cuda'
}

Write-SetupProgress 'source' 'Fetching the pinned llama.cpp revision.'
$parent = Split-Path -Parent $LlamaCppPath
New-Item -ItemType Directory -Force -Path $parent | Out-Null
if (-not (Test-Path -LiteralPath (Join-Path $LlamaCppPath '.git'))) {
    & git clone https://github.com/ggml-org/llama.cpp.git $LlamaCppPath
    if ($LASTEXITCODE -ne 0) { throw 'Could not clone llama.cpp.' }
}
& git -C $LlamaCppPath fetch --no-tags origin $LlamaCppRevision
if ($LASTEXITCODE -ne 0) { throw "Could not fetch llama.cpp revision $LlamaCppRevision." }
& git -C $LlamaCppPath cat-file -e "$LlamaCppRevision^{commit}"
if ($LASTEXITCODE -ne 0) { throw 'The pinned llama.cpp revision is unavailable.' }
& git -C $LlamaCppPath checkout --detach $LlamaCppRevision
if ($LASTEXITCODE -ne 0) { throw 'Could not check out the pinned llama.cpp revision.' }

$buildPath = Join-Path $LlamaCppPath $buildName
$arguments = @('-S', $LlamaCppPath, '-B', $buildPath, '-G', 'Visual Studio 17 2022', '-A', 'x64', '-DGGML_RPC=ON')
if ($LocalGPU) { $arguments += '-DGGML_CUDA=ON' } else { $arguments += '-DGGML_CUDA=OFF' }
Write-SetupProgress 'build' $(if ($LocalGPU) { 'Configuring the CUDA and RPC-enabled local inference backend.' } else { 'Configuring the RPC-enabled control-only inference backend.' })
& cmake @arguments
if ($LASTEXITCODE -ne 0) { throw 'CMake could not configure llama-server.' }
Write-SetupProgress 'build' 'Compiling llama-server. This can take several minutes.'
& cmake --build $buildPath --config Release --target llama-server --parallel 4
if ($LASTEXITCODE -ne 0) { throw 'Building llama-server failed.' }

$server = Join-Path $buildPath 'bin\Release\llama-server.exe'
if (-not (Test-Path -LiteralPath $server -PathType Leaf)) { throw "Build completed without the expected executable: $server" }
Write-SetupProgress 'complete' 'Local inference backend is ready for Tether.'
Write-Output $server
