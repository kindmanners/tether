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
    Audits or provisions a Windows NVIDIA GPU host for Tether.

.DESCRIPTION
    This configures only machine-local state: CUDA/build prerequisites, the
    pinned llama.cpp RPC build, Tailnet-scoped firewall rules, and the Agent's
    local config. It never pairs an Agent automatically; pairing needs the
    one-time local code required by Tether's pinned-mTLS design.
#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [switch]$Provision,
    [switch]$InstallMissing,
    [switch]$ReplaceAgentConfig,
    [switch]$SkipRPCBuild,
    [string]$TetherRoot,
    [string]$AgentExecutablePath,
    [string]$LlamaCppPath,
    [string]$ProgressPath,
    [string]$CancelPath,
    [int]$AgentPort = 7420,
    [int]$RPCPort = 50053,
    [string]$TailnetCIDR = '100.64.0.0/10',
    [string]$MinimumCudaVersion = '13.4',
    [string]$LlamaCppRevision = '3057bb66c86c46d5781e50e85462a760ba7d1feb'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$script:missingRequirements = [System.Collections.Generic.List[string]]::new()
$script:installationOccurred = $false

if ([string]::IsNullOrWhiteSpace($TetherRoot) -and [string]::IsNullOrWhiteSpace($AgentExecutablePath)) {
    $TetherRoot = Split-Path -Parent $PSScriptRoot
}

function Write-Step {
    param([string]$Message, [string]$StepId)
    Write-Host "`n==> $Message" -ForegroundColor Cyan
    if ($StepId -and $ProgressPath) {
        $progress = [ordered]@{ step = $StepId; detail = $Message }
        [System.IO.File]::WriteAllText($ProgressPath, ($progress | ConvertTo-Json -Compress), [System.Text.UTF8Encoding]::new($false))
    }
}

function Write-SetupProgress {
    param([string]$StepId, [string]$Detail)
    if (-not $ProgressPath) { return }
    $progress = [ordered]@{ step = $StepId; detail = $Detail }
    [System.IO.File]::WriteAllText($ProgressPath, ($progress | ConvertTo-Json -Compress), [System.Text.UTF8Encoding]::new($false))
}

function Assert-SetupNotCancelled {
    if ($CancelPath -and (Test-Path $CancelPath)) {
        Write-SetupProgress 'cancelled' 'Setup was cancelled. No Agent configuration was written; run setup again when ready.'
        throw [System.OperationCanceledException]::new('Setup was cancelled by closing Tether Agent.')
    }
}

trap {
    if ($_.Exception -is [System.OperationCanceledException]) {
        Write-SetupProgress 'cancelled' $_.Exception.Message
    } else { Write-SetupProgress 'failed' $_.Exception.Message }
    throw
}

function Write-Check {
    param([string]$Message, [bool]$Passed)
    $prefix = if ($Passed) { '[ok] ' } else { '[!!] ' }
    $colour = if ($Passed) { 'Green' } else { 'Yellow' }
    Write-Host "$prefix$Message" -ForegroundColor $colour
}

function Require-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Provisioning changes packages, firewall rules, and local configuration. Re-run PowerShell as Administrator.'
    }
}

function Get-CommandPath {
    param([string]$Name)
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($null -eq $command) { return $null }
    return $command.Source
}

function ConvertTo-Version {
    param([string]$Value)
    $match = [regex]::Match($Value, '\d+(?:\.\d+){1,3}')
    if (-not $match.Success) { return $null }
    try { return [version]$match.Value } catch { return $null }
}

function Get-CudaToolkit {
    $candidates = @()
    if ($env:CUDA_PATH -and (Test-Path (Join-Path $env:CUDA_PATH 'bin\nvcc.exe'))) {
        $candidates += $env:CUDA_PATH
    }
    $base = Join-Path $env:ProgramFiles 'NVIDIA GPU Computing Toolkit\CUDA'
    if (Test-Path $base) {
        $candidates += Get-ChildItem -Path $base -Directory |
            Where-Object { Test-Path (Join-Path $_.FullName 'bin\nvcc.exe') } |
            ForEach-Object { $_.FullName }
    }
    $candidates = @($candidates | Select-Object -Unique)
    if ($candidates.Count -eq 0) { return $null }
    $selected = $candidates |
        Sort-Object { ConvertTo-Version (Split-Path $_ -Leaf) } -Descending |
        Select-Object -First 1
    [pscustomobject]@{ Path = $selected; Version = ConvertTo-Version (Split-Path $selected -Leaf) }
}

function Get-NvidiaGPUInfo {
    $fallback = @(Get-CimInstance Win32_VideoController | Where-Object { $_.PNPDeviceID -match 'VEN_10DE' } |
        Select-Object @{ Name = 'name'; Expression = { $_.Name } },
            @{ Name = 'driverVersion'; Expression = { $_.DriverVersion } },
            @{ Name = 'vramBytes'; Expression = { [int64]$_.AdapterRAM } })
    $nvidiaSmi = Get-CommandPath 'nvidia-smi'
    if (-not $nvidiaSmi) { return $fallback }

    $lines = @(& $nvidiaSmi --query-gpu=name,memory.total,memory.free,driver_version --format=csv,noheader,nounits 2>$null)
    if ($LASTEXITCODE -ne 0 -or $lines.Count -eq 0) { return $fallback }
    $gpus = @()
    foreach ($line in $lines) {
        $values = @($line -split ',' | ForEach-Object { $_.Trim() })
        if ($values.Count -ne 4) { continue }
        try {
            $gpus += [pscustomobject]@{
                name = $values[0]
                driverVersion = $values[3]
                vramBytes = [int64]$values[1] * 1MB
                vramFreeBytes = [int64]$values[2] * 1MB
            }
        } catch { continue }
    }
    if ($gpus.Count -gt 0) { return $gpus }
    return $fallback
}

function Install-WingetPackage {
    param([string]$Id, [string]$Name)
    if (-not (Get-CommandPath 'winget')) {
        throw "WinGet is required to install $Name. Install Microsoft App Installer, then re-run this script."
    }
    Write-Step "Installing $Name"
    & winget install --id $Id --exact --source winget --accept-package-agreements --accept-source-agreements --silent
    if ($LASTEXITCODE -ne 0) { throw "WinGet could not install $Name (package $Id; exit code $LASTEXITCODE)." }
}

function Ensure-Tool {
    param([string]$Command, [string]$PackageId, [string]$Name)
    if (Get-CommandPath $Command) {
        Write-Check "$Name is available" $true
        return
    }
    Write-Check "$Name is not available" $false
    $script:missingRequirements.Add($Name)
    if ($InstallMissing) {
        Install-WingetPackage -Id $PackageId -Name $Name
        $script:installationOccurred = $true
    }
    return $false
}

function Ensure-TailscaleTool {
    if (Get-CommandPath 'tailscale') {
        Write-Check 'Tailscale is available' $true
        return
    }
    Write-Check 'Tailscale is not available' $false
    if (-not $InstallMissing) {
        $script:missingRequirements.Add('Tailscale')
        return
    }

    Install-WingetPackage -Id 'Tailscale.Tailscale' -Name 'Tailscale'
    $script:installationOccurred = $true
    # Tailscale installs a standalone CLI in this conventional directory.
    # Refresh just this process before checking again, so a node that only
    # lacked Tailscale can continue directly to its intentional browser login
    # instead of asking the user to repeat setup for a stale PATH.
    $tailscaleDirectory = Join-Path $env:ProgramFiles 'Tailscale'
    if (Test-Path (Join-Path $tailscaleDirectory 'tailscale.exe')) {
        $env:Path = "$tailscaleDirectory;$env:Path"
    }
    if (Get-CommandPath 'tailscale') {
        Write-Check 'Tailscale is available after installation' $true
        return
    }
    $script:missingRequirements.Add('Tailscale')
}

function Ensure-TailscaleConnection {
    if (-not (Get-CommandPath 'tailscale')) {
        throw 'Tailscale is unavailable after installation. Re-open Tether Agent and run setup again.'
    }

    # A successful status query with a DNS name proves both the daemon and a
    # Tailnet identity are ready. Do not call `tailscale up` unnecessarily:
    # it can otherwise alter an already-working node's preferences.
    try {
        $status = & tailscale status --json 2>$null | ConvertFrom-Json
        if ($LASTEXITCODE -eq 0 -and $status.Self.DNSName) {
            Write-Check 'Tailscale is connected to a Tailnet' $true
            return
        }
    } catch { }

    Write-Step 'Connecting Tailscale'
    Write-Host 'Tailscale needs a Tailnet login. A browser window should open; sign in or join the shared Tailnet, then return here.' -ForegroundColor Yellow
    & tailscale up
    if ($LASTEXITCODE -ne 0) {
        throw "Tailscale sign-in did not complete (exit code $LASTEXITCODE). A browser window should have opened; complete login, then run setup again."
    }
    try {
        $status = & tailscale status --json 2>$null | ConvertFrom-Json
        if ($LASTEXITCODE -eq 0 -and $status.Self.DNSName) {
            Write-Check 'Tailscale is connected to a Tailnet' $true
            return
        }
    } catch { }
    throw 'Tailscale did not report a Tailnet identity after sign-in. Complete the browser login, then run setup again.'
}

function Install-VisualStudioBuildTools {
    if (-not (Get-CommandPath 'winget')) {
        throw 'WinGet is required to install Visual Studio Build Tools. Install Microsoft App Installer, then re-run this script.'
    }
    Write-Step 'Installing Visual Studio 2022 Build Tools with the C++ workload'
    & winget install --id Microsoft.VisualStudio.2022.BuildTools --exact --source winget `
        --accept-package-agreements --accept-source-agreements `
        --override '--wait --passive --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended'
    if ($LASTEXITCODE -ne 0) { throw "WinGet could not install Visual Studio Build Tools (exit code $LASTEXITCODE)." }
}

function Get-PortOwners {
    param([int]$Port)
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue)
    foreach ($listener in $listeners) {
        $process = Get-Process -Id $listener.OwningProcess -ErrorAction SilentlyContinue
        [pscustomobject]@{
            Port = $Port; Address = $listener.LocalAddress; PID = $listener.OwningProcess
            Process = if ($process) { $process.ProcessName } else { 'unknown' }
        }
    }
}

function Assert-PortIsFree {
    param([int]$Port, [string]$Purpose)
    $owners = @(Get-PortOwners -Port $Port)
    if ($owners.Count -eq 0) {
        Write-Check "TCP $Port is free for $Purpose" $true
        return
    }
    $description = ($owners | ForEach-Object { "$($_.Process) (PID $($_.PID), $($_.Address))" }) -join ', '
    throw "TCP $Port is already listening and cannot be used for ${Purpose}: $description"
}

function Ensure-FirewallRule {
    param([string]$Name, [int]$Port)
    if (Get-NetFirewallRule -DisplayName $Name -ErrorAction SilentlyContinue) {
        Write-Check "Firewall rule '$Name' already exists" $true
        return
    }
    if ($PSCmdlet.ShouldProcess("TCP $Port", "allow inbound Tether traffic from $TailnetCIDR")) {
        New-NetFirewallRule -DisplayName $Name -Direction Inbound -Action Allow -Protocol TCP `
            -LocalPort $Port -RemoteAddress $TailnetCIDR -Profile Any | Out-Null
    }
}

function Get-TailscaleIPv4 {
    if (-not (Get-CommandPath 'tailscale')) { return $null }
    $addresses = @(& tailscale ip -4 2>$null | Where-Object { $_ -match '^100\.' })
    if ($LASTEXITCODE -ne 0 -or $addresses.Count -eq 0) { return $null }
    return $addresses[0].Trim()
}

function Get-TailscaleHostname {
    if (-not (Get-CommandPath 'tailscale')) { return $null }
    try {
        $status = & tailscale status --json 2>$null | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or -not $status.Self.DNSName) { return $null }
        return ($status.Self.DNSName.TrimEnd('.') -split '\.')[0]
    } catch { return $null }
}

function Set-CudaEnvironment {
    param([string]$CudaPath)
    $env:CUDA_PATH = $CudaPath
    $env:CudaToolkitDir = "$CudaPath\"
    $env:Path = "$CudaPath\bin;$CudaPath\bin\x64;$env:Path"
}

function Ensure-CudaRuntimePath {
    param([string]$CudaPath)
    $cudaBin = Join-Path $CudaPath 'bin'
    $cudaBinX64 = Join-Path $cudaBin 'x64'
    $runtimePaths = @($cudaBin)
    if (Test-Path $cudaBinX64) { $runtimePaths += $cudaBinX64 }
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    $missingPaths = @($runtimePaths | Where-Object { $entries -notcontains $_ })
    if ($missingPaths.Count -eq 0) {
        Write-Check "CUDA runtime path is present in the user PATH" $true
    } elseif ($PSCmdlet.ShouldProcess('User PATH', "add CUDA runtime paths for the Tether Agent")) {
        [Environment]::SetEnvironmentVariable('Path', (($entries + $missingPaths) -join ';'), 'User')
        Write-Check "Added CUDA runtime path to the user PATH for future Agent launches" $true
    }
    Set-CudaEnvironment -CudaPath $CudaPath
}

function Get-MSBuild {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    if (-not (Test-Path $vswhere)) { return $null }
    $path = & $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 `
        -find 'MSBuild\**\Bin\MSBuild.exe' 2>$null | Select-Object -First 1
    if ($LASTEXITCODE -ne 0 -or -not $path) { return $null }
    return $path.Trim()
}

function Get-CMakeCompileUnitCount {
    param([string]$BuildPath)
    $replyDir = Join-Path $BuildPath '.cmake\api\v1\reply'
    $index = Get-ChildItem -Path $replyDir -Filter 'index-*.json' -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $index) { return 0 }
    try {
        $indexData = Get-Content -Raw $index.FullName | ConvertFrom-Json
        $modelFile = $indexData.reply.'codemodel-v2'.jsonFile
        if (-not $modelFile) { return 0 }
        $model = Get-Content -Raw (Join-Path $replyDir $modelFile) | ConvertFrom-Json
        $configuration = $model.configurations | Select-Object -First 1
        $targets = @{}
        foreach ($target in $configuration.targets) { $targets[$target.id] = $target.jsonFile }
        $root = $configuration.targets | Where-Object { $_.name -eq 'ggml-rpc-server' } | Select-Object -First 1
        if (-not $root) { return 0 }
        $visited = [System.Collections.Generic.HashSet[string]]::new()
        $compileCounter = [ref]0
        function Visit-CMakeTarget([string]$TargetID) {
            if (-not $visited.Add($TargetID) -or -not $targets.ContainsKey($TargetID)) { return }
            $targetData = Get-Content -Raw (Join-Path $replyDir $targets[$TargetID]) | ConvertFrom-Json
            foreach ($source in @($targetData.sources)) {
                if ($null -ne $source.PSObject.Properties['compileGroupIndex']) { $compileCounter.Value++ }
            }
            foreach ($dependency in @($targetData.dependencies)) { Visit-CMakeTarget $dependency.id }
        }
        Visit-CMakeTarget $root.id
        return $compileCounter.Value
    } catch { return 0 }
}

function Invoke-CancellableLlamaBuild {
    param([string]$BuildPath, [int]$CompileUnits)
    $stdout = Join-Path ([IO.Path]::GetTempPath()) ("tether-cmake-{0}.out" -f [guid]::NewGuid())
    $stderr = Join-Path ([IO.Path]::GetTempPath()) ("tether-cmake-{0}.err" -f [guid]::NewGuid())
    try {
        $build = Start-Process -FilePath 'cmake' -ArgumentList @('--build', $BuildPath, '--config', 'Release', '--target', 'ggml-rpc-server', '--parallel', '4') `
            -PassThru -NoNewWindow -RedirectStandardOutput $stdout -RedirectStandardError $stderr
        $lastDetail = ''
        while (-not $build.HasExited) {
            if ($CancelPath -and (Test-Path $CancelPath)) {
                & taskkill.exe /PID $build.Id /T /F 2>$null | Out-Null
                Write-SetupProgress 'cancelled' 'Stopping the CMake/MSBuild compilation safely. Run setup again to resume.'
                throw [System.OperationCanceledException]::new('Setup was cancelled while compiling llama.cpp.')
            }
            $build.Refresh()
            $text = ((Get-Content -Raw $stdout -ErrorAction SilentlyContinue) + "`n" + (Get-Content -Raw $stderr -ErrorAction SilentlyContinue))
            $matches = [regex]::Matches($text, '\[(\d+)\s*/\s*(\d+)\]')
            if ($matches.Count -gt 0) {
                $match = $matches[$matches.Count - 1]
                $done = [int]$match.Groups[1].Value; $total = [int]$match.Groups[2].Value
                $detail = "Compiling llama.cpp CUDA RPC server: $done of $total build steps finished; $($total - $done) remaining."
            } elseif ($CompileUnits -gt 0) {
                $compiled = [regex]::Matches($text, '(?im)^.*\.(?:c|cc|cpp|cxx|cu)(?::|\s|$)').Count
                $detail = "Compiling llama.cpp CUDA RPC server: $compiled of $CompileUnits source units reported; about $([Math]::Max(0, $CompileUnits - $compiled)) remaining."
            } else {
                $detail = 'Compiling llama.cpp CUDA RPC server. CMake is checking and building the required source units.'
            }
            if ($detail -ne $lastDetail) { Write-SetupProgress 'rpc-server' $detail; $lastDetail = $detail }
            Start-Sleep -Milliseconds 400
        }
        $build.WaitForExit()
        $build.Refresh()
        # Start-Process on some Windows builds does not populate ExitCode
        # after a redirected, asynchronously monitored child exits. The
        # caller independently verifies the expected RPC executable, so only
        # reject a concrete non-zero code here; a missing property is not a
        # build failure.
        if ($null -ne $build.ExitCode -and $build.ExitCode -ne 0) {
            throw "Building ggml-rpc-server failed (exit code $($build.ExitCode))."
        }
    } finally {
        Remove-Item -LiteralPath $stdout, $stderr -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-LlamaCppBuild {
    param([string]$SourcePath, [string]$CudaPath)
    if (-not (Test-Path (Join-Path $SourcePath '.git'))) {
        if ($PSCmdlet.ShouldProcess($SourcePath, 'clone llama.cpp')) {
            # Keep native tool output visible in the elevated setup window,
            # but do not let it enter this function's success-output stream.
            # The caller captures this function's one return value as the RPC
            # executable path, so any CMake/Git text here would corrupt YAML.
            & git clone https://github.com/ggml-org/llama.cpp.git $SourcePath 2>&1 | Out-Host
            if ($LASTEXITCODE -ne 0) { throw "Could not clone llama.cpp (exit code $LASTEXITCODE)." }
        }
    }
    Push-Location $SourcePath
    try {
        Assert-SetupNotCancelled
        # Fetch the configured object itself. `git fetch --tags` only updates
        # tag refs and can leave an otherwise valid, untagged pinned commit
        # absent from a fresh clone; checkout would then fail much later with
        # a misleading unknown-revision error.
        & git fetch --no-tags origin $LlamaCppRevision 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "Could not fetch llama.cpp revision $LlamaCppRevision." }
        & git cat-file -e "$LlamaCppRevision^{commit}" 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "Fetched llama.cpp revision $LlamaCppRevision is not a commit object." }
        & git checkout --detach $LlamaCppRevision 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "Could not check out llama.cpp revision $LlamaCppRevision." }
        Set-CudaEnvironment -CudaPath $CudaPath
        $buildPath = Join-Path $SourcePath 'build-rpc-cuda'
        $queryDir = Join-Path $buildPath '.cmake\api\v1\query'
        New-Item -ItemType Directory -Force -Path $queryDir | Out-Null
        New-Item -ItemType File -Force -Path (Join-Path $queryDir 'codemodel-v2') | Out-Null
        & cmake -S . -B $buildPath -G 'Visual Studio 17 2022' -A x64 -DGGML_CUDA=ON -DGGML_RPC=ON 2>&1 | Out-Host
        if ($LASTEXITCODE -ne 0) { throw 'CMake configuration of the CUDA RPC server failed.' }
        Assert-SetupNotCancelled
        $compileUnits = Get-CMakeCompileUnitCount -BuildPath $buildPath
        if ($compileUnits -eq 0) {
            # CMake's File API records a query during one configure pass and
            # can publish its reply only on the next one. The second pass is
            # inexpensive and gives the UI a real total before compilation.
            Write-SetupProgress 'rpc-server' 'Calculating the exact CMake CUDA build plan…'
            & cmake -S . -B $buildPath 2>&1 | Out-Host
            if ($LASTEXITCODE -ne 0) { throw 'CMake could not prepare the CUDA build plan.' }
            $compileUnits = Get-CMakeCompileUnitCount -BuildPath $buildPath
        }
        if ($compileUnits -gt 0) { Write-SetupProgress 'rpc-server' "CMake prepared $compileUnits source units for the CUDA RPC build." }
        Invoke-CancellableLlamaBuild -BuildPath $buildPath -CompileUnits $compileUnits
    } finally { Pop-Location }
    $rpcServer = Join-Path $SourcePath 'build-rpc-cuda\bin\Release\ggml-rpc-server.exe'
    if (-not (Test-Path $rpcServer)) { throw "Build completed without $rpcServer." }
    return $rpcServer
}

function Write-AgentConfig {
    param([string]$RPCServerPath, [string]$ListenHost)
    $configDir = Join-Path $env:APPDATA 'tether'
    $configPath = Join-Path $configDir 'agent_config.yaml'
    if ((Test-Path $configPath) -and -not $ReplaceAgentConfig) {
        $existing = Get-Content -Raw -Path $configPath -ErrorAction SilentlyContinue
        $pathMatch = [regex]::Match($existing, "(?m)^rpc_server_path:\\s*'(?<path>(?:[^']|'')*)'\\s*$")
        $existingRPCServer = if ($pathMatch.Success) { $pathMatch.Groups['path'].Value.Replace("''", "'") } else { $null }
        if ($existingRPCServer -and (Test-Path -LiteralPath $existingRPCServer -PathType Leaf)) {
            Write-Check "Keeping existing Agent configuration at $configPath" $true
            return $configPath
        }
        Write-Check "Replacing unusable Agent configuration at $configPath" $false
    }
    if ($PSCmdlet.ShouldProcess($configPath, 'write local Tether Agent configuration')) {
        New-Item -ItemType Directory -Force -Path $configDir | Out-Null
        $escapedPath = $RPCServerPath.Replace("'", "''")
        @("rpc_server_path: '$escapedPath'", "rpc_listen_host: '$ListenHost'") |
            Set-Content -Path $configPath -Encoding UTF8
    }
    return $configPath
}

function Write-Report {
    param($GpuInfo, $Cuda, [string]$TailscaleIP, [string]$TailscaleHostname, [string]$AgentConfigPath)
    $reportDir = Join-Path $env:APPDATA 'tether'
    $reportPath = Join-Path $reportDir 'bootstrap-report.json'
    $report = [ordered]@{
        observedAt = (Get-Date).ToUniversalTime().ToString('o'); hostname = $TailscaleHostname
        tailscaleIP = $TailscaleIP; agentPort = $AgentPort; rpcPort = $RPCPort
        cudaVersion = if ($Cuda) { $Cuda.Version.ToString() } else { $null }
        gpus = @($GpuInfo); agentConfigPath = $AgentConfigPath
    }
    New-Item -ItemType Directory -Force -Path $reportDir | Out-Null
    $reportJSON = $report | ConvertTo-Json -Depth 5
    [System.IO.File]::WriteAllText($reportPath, $reportJSON, [System.Text.UTF8Encoding]::new($false))
    return $reportPath
}

if ($AgentPort -lt 1 -or $AgentPort -gt 65535 -or $RPCPort -lt 1 -or $RPCPort -gt 65535) {
    throw 'AgentPort and RPCPort must be between 1 and 65535.'
}
if ($AgentPort -eq $RPCPort) { throw 'AgentPort and RPCPort must differ.' }
if ($InstallMissing -and -not $Provision) {
    throw '-InstallMissing changes this machine and requires -Provision.'
}
if ($WhatIfPreference -and $InstallMissing) {
    throw '-WhatIf cannot be combined with -InstallMissing because package installation is intentionally not simulated.'
}
if ($AgentExecutablePath) {
    if (-not (Test-Path $AgentExecutablePath) -or (Get-Item $AgentExecutablePath).PSIsContainer) {
        throw "AgentExecutablePath must point to tether-agent.exe: $AgentExecutablePath"
    }
    $AgentExecutablePath = (Resolve-Path $AgentExecutablePath).Path
} elseif (-not (Test-Path $TetherRoot)) {
    throw "Tether root does not exist: $TetherRoot"
}
if (-not $LlamaCppPath) {
    if ($TetherRoot) {
        $LlamaCppPath = Join-Path $TetherRoot 'llama.cpp'
    } else {
        $LlamaCppPath = Join-Path (Split-Path -Parent $AgentExecutablePath) 'llama.cpp'
    }
}

Write-Step 'Checking Windows GPU node prerequisites' 'requirements'
if ($Provision) { Require-Administrator }
$gpuInfo = @(Get-NvidiaGPUInfo)
Write-Check 'An NVIDIA GPU was detected' ($gpuInfo.Count -gt 0)
if ($gpuInfo.Count -eq 0) { throw 'No NVIDIA GPU was detected; this node cannot build a CUDA RPC server.' }

Ensure-Tool -Command 'git' -PackageId 'Git.Git' -Name 'Git'
Ensure-Tool -Command 'cmake' -PackageId 'Kitware.CMake' -Name 'CMake'
if (-not $AgentExecutablePath) {
    Ensure-Tool -Command 'go' -PackageId 'GoLang.Go' -Name 'Go'
}
Ensure-TailscaleTool
$msbuild = Get-MSBuild
Write-Check 'Visual Studio C++ Build Tools are available' ($null -ne $msbuild)
if (-not $msbuild) {
    if ($InstallMissing) {
        Install-VisualStudioBuildTools
        $script:installationOccurred = $true
    }
    $script:missingRequirements.Add('Visual Studio 2022 Build Tools with the C++ workload')
}
$cuda = Get-CudaToolkit
if (-not $cuda) {
    Write-Check 'CUDA Toolkit is available' $false
    if ($InstallMissing) {
        Install-WingetPackage -Id 'Nvidia.CUDA' -Name 'NVIDIA CUDA Toolkit'
        $script:installationOccurred = $true
        $cuda = Get-CudaToolkit
    }
}
if (-not $cuda) {
    $script:missingRequirements.Add('CUDA Toolkit')
}
$minimumVersion = ConvertTo-Version $MinimumCudaVersion
if ($cuda -and ($null -eq $minimumVersion -or $cuda.Version -lt $minimumVersion)) {
    throw "CUDA $MinimumCudaVersion or newer is required; found $($cuda.Version)."
}
if ($cuda) { Write-Check "CUDA $($cuda.Version) is available at $($cuda.Path)" $true }

Write-Step 'Verifying local ports'
Assert-PortIsFree -Port $AgentPort -Purpose 'the Tether Agent'
Assert-PortIsFree -Port $RPCPort -Purpose 'llama.cpp RPC'
if ($RPCPort -eq 50052) {
    Write-Warning 'TCP 50052 was occupied by Incredibuild LicenseService on Mathesis. Prefer 50053 unless 50052 is verified safe on this host.'
}
if ($script:missingRequirements.Count -gt 0) {
    $missing = $script:missingRequirements -join ', '
    if ($script:installationOccurred) {
        throw "Dependencies were installed, but this shell may still have a stale PATH. Re-open elevated PowerShell and re-run the script. Remaining checks: $missing"
    }
    throw "Audit failed. Resolve these requirements, then re-run: $missing"
}
if ($Provision) {
    Write-Step 'Connecting this PC to Tailscale' 'tailscale'
    Ensure-TailscaleConnection
}
$tailscaleIP = Get-TailscaleIPv4
Write-Check 'A Tailscale IPv4 address is available' ($null -ne $tailscaleIP)
if (-not $tailscaleIP) { $script:missingRequirements.Add('an active Tailscale IPv4 connection') }
else { Write-Host "Tailscale IPv4: $tailscaleIP" }
$tailscaleHostname = Get-TailscaleHostname
Write-Check 'A Tailscale hostname is available' ($null -ne $tailscaleHostname)
if (-not $tailscaleHostname) { $script:missingRequirements.Add('a Tailscale hostname') }
else { Write-Host "Tailscale hostname: $tailscaleHostname" }
if ($script:missingRequirements.Count -gt 0) {
    $missing = $script:missingRequirements -join ', '
    throw "Audit failed. Resolve these requirements, then re-run: $missing"
}
if (-not $Provision) {
    Write-Host "`nAudit complete. To make changes, re-run with -Provision. Add -InstallMissing to let WinGet install missing tools." -ForegroundColor Cyan
    exit 0
}
if ($WhatIfPreference) {
    Write-Host "`nWhatIf: would build the Agent and CUDA RPC server, add Tailnet-only firewall rules, update the user CUDA runtime PATH, and write the local Agent config/report." -ForegroundColor Cyan
    exit 0
}

Ensure-CudaRuntimePath -CudaPath $cuda.Path
if ($AgentExecutablePath) {
    $agentOutput = $AgentExecutablePath
    Write-Check "Using supplied Tether Agent at $agentOutput" $true
} else {
    Write-Step 'Building local Tether Agent'
    Push-Location $TetherRoot
    try {
        $agentOutput = Join-Path $TetherRoot 'bin\tether-agent.exe'
        if ($PSCmdlet.ShouldProcess($agentOutput, 'build Tether Agent')) {
            New-Item -ItemType Directory -Force -Path (Split-Path $agentOutput) | Out-Null
            # A Wails desktop executable must be built in production mode.
            # Without this tag it compiles, but displays Wails' build-tags
            # error dialog immediately when launched.
            & go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o $agentOutput ./cmd/tether-agent
            if ($LASTEXITCODE -ne 0) { throw 'Building tether-agent failed.' }
        }
    } finally { Pop-Location }
}
$rpcServer = Join-Path $LlamaCppPath 'build-rpc-cuda\bin\Release\ggml-rpc-server.exe'
if (-not $SkipRPCBuild) {
Write-Step 'Building pinned llama.cpp CUDA RPC server' 'rpc-server'
    $rpcServer = Invoke-LlamaCppBuild -SourcePath $LlamaCppPath -CudaPath $cuda.Path
} elseif (-not (Test-Path $rpcServer)) { throw "-SkipRPCBuild was specified but no RPC server exists at $rpcServer." }

Write-Step 'Creating Tailnet-only firewall rules' 'configuration'
Ensure-FirewallRule -Name "Tether Agent TCP $AgentPort" -Port $AgentPort
Ensure-FirewallRule -Name "Tether llama.cpp RPC TCP $RPCPort" -Port $RPCPort
Write-Step 'Writing local Agent configuration' 'configuration'
$configPath = Write-AgentConfig -RPCServerPath $rpcServer -ListenHost $tailscaleIP
$reportPath = Write-Report -GpuInfo $gpuInfo -Cuda $cuda -TailscaleIP $tailscaleIP -TailscaleHostname $tailscaleHostname -AgentConfigPath $configPath
Write-Host "`nProvisioning complete." -ForegroundColor Green
if ($ProgressPath) {
    $progress = [ordered]@{ step = 'complete'; detail = 'Local setup completed successfully.' }
    [System.IO.File]::WriteAllText($ProgressPath, ($progress | ConvertTo-Json -Compress), [System.Text.UTF8Encoding]::new($false))
}
Write-Host "Agent: $(Join-Path $TetherRoot 'bin\tether-agent.exe')"
Write-Host "RPC endpoint: $tailscaleIP`:$RPCPort"
Write-Host "Capability report: $reportPath"
Write-Host "`nAdd this node to the Orchestrator allowlist:"
Write-Host "  - hostname: $tailscaleHostname"
Write-Host "    role: rpc-node"
Write-Host "    agent_port: $AgentPort"
Write-Host "    rpc_port: $RPCPort"
Write-Host "Next: run '.\bin\tether-agent.exe'. Use '-pair' only for a new node or deliberate certificate rotation." -ForegroundColor Cyan
