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
    Builds the Windows Tether desktop release files.

.DESCRIPTION
    The Wails WebView2 bootstrapper is embedded in both GUI executables. On a
    Windows machine that does not already have a compatible WebView2 Runtime,
    Tether prompts the user and runs Microsoft's embedded bootstrapper before
    the GUI starts. The runtime itself remains Microsoft's Evergreen runtime;
    its installation may download the current payload on first launch.
#>
[CmdletBinding()]
param(
    [string]$OutputDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $repositoryRoot 'bin'
}
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

Push-Location $repositoryRoot
try {
    # production selects Wails' runnable production application. wv2runtime.embed
    # selects its embedded Microsoft WebView2 bootstrapper rather than the
    # download-only strategy. -H windowsgui keeps desktop applications from
    # opening a console window for the end user.
    & go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o (Join-Path $OutputDirectory 'tether.exe') ./cmd/tether
    if ($LASTEXITCODE -ne 0) { throw 'Building tether.exe failed.' }

    & go build -tags 'production,wv2runtime.embed' -ldflags '-H windowsgui' -o (Join-Path $OutputDirectory 'tether-agent.exe') ./cmd/tether-agent
    if ($LASTEXITCODE -ne 0) { throw 'Building tether-agent.exe failed.' }

    # The OpenAI gateway is not a WebView application, so it does not need the
    # runtime tag. Keep its console output available for operator diagnostics.
    & go build -o (Join-Path $OutputDirectory 'tether-api.exe') ./cmd/tether-api
    if ($LASTEXITCODE -ne 0) { throw 'Building tether-api.exe failed.' }

    & go build -o (Join-Path $OutputDirectory 'tether-dashboard.exe') ./cmd/tether-dashboard
    if ($LASTEXITCODE -ne 0) { throw 'Building tether-dashboard.exe failed.' }
} finally {
    Pop-Location
}

Write-Host "Windows release files written to $OutputDirectory" -ForegroundColor Green
Write-Host 'Send tether-agent.exe to a GPU-node friend. Keep tether.exe, tether-api.exe, and tether-dashboard.exe together on the Orchestrator.' -ForegroundColor Cyan
