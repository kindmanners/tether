<#
Copyright (C) 2026 kindmanners on github

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
#>

#requires -Version 5.1
[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://127.0.0.1:11435',
    [string]$ApiKey = $env:TETHER_API_KEY,
    [string]$Model,
    [switch]$RequireDistributed,
    [switch]$RunInference,
    [switch]$UnloadAfter,
    [string[]]$RpcEndpoint = @(),
    [string]$OutputDirectory,
    [ValidateRange(1, 600)]
    [int]$TimeoutSeconds = 360
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
$repositoryRoot = Split-Path -Parent (Split-Path -Parent $scriptDirectory)
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
    $OutputDirectory = Join-Path $repositoryRoot "acceptance-results\$stamp"
}
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

$summary = [ordered]@{
    schemaVersion = 1
    startedAt = [DateTime]::UtcNow.ToString('o')
    finishedAt = $null
    passed = $false
    checks = [ordered]@{}
    model = $Model
    placementMode = $null
    placementNodeCount = $null
    firstEventMilliseconds = $null
    totalInferenceMilliseconds = $null
    error = $null
}

function Write-JsonFile {
    param([string]$Name, [object]$Value)
    $path = Join-Path $OutputDirectory $Name
    $json = $Value | ConvertTo-Json -Depth 8
    [IO.File]::WriteAllText($path, $json, [Text.UTF8Encoding]::new($false))
}

function Test-LoopbackUrl {
    param([Uri]$Uri)
    if ($Uri.Scheme -ne 'http') { return $false }
    if ($Uri.Host -eq 'localhost') { return $true }
    $address = $null
    if (-not [Net.IPAddress]::TryParse($Uri.Host, [ref]$address)) { return $false }
    return [Net.IPAddress]::IsLoopback($address)
}

function New-Request {
    param(
        [Net.Http.HttpMethod]$Method,
        [string]$Path,
        [string]$JsonBody,
        [switch]$Authenticated
    )
    $request = [Net.Http.HttpRequestMessage]::new($Method, ([Uri]::new($gatewayUri, $Path)))
    if ($Authenticated) {
        $request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $ApiKey)
    }
    if (-not [string]::IsNullOrEmpty($JsonBody)) {
        $request.Content = [Net.Http.StringContent]::new($JsonBody, [Text.Encoding]::UTF8, 'application/json')
    }
    return $request
}

function Invoke-JsonRequest {
    param([Net.Http.HttpMethod]$Method, [string]$Path, [string]$JsonBody = '')
    $request = New-Request -Method $Method -Path $Path -JsonBody $JsonBody -Authenticated
    $response = $null
    try {
        $response = $client.SendAsync($request).GetAwaiter().GetResult()
        $content = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            throw "$($Method.Method) $Path returned HTTP $([int]$response.StatusCode): $content"
        }
        if ([string]::IsNullOrWhiteSpace($content)) { return $null }
        return $content | ConvertFrom-Json
    } finally {
        $request.Dispose()
        if ($null -ne $response) { $response.Dispose() }
    }
}

function Test-ClosedTcpEndpoint {
    param([string]$Endpoint)
    $separator = $Endpoint.LastIndexOf(':')
    if ($separator -le 0 -or $separator -eq ($Endpoint.Length - 1)) {
        throw "RPC endpoint must use host:port syntax: $Endpoint"
    }
    $hostName = $Endpoint.Substring(0, $separator).Trim('[', ']')
    $port = 0
    if (-not [int]::TryParse($Endpoint.Substring($separator + 1), [ref]$port) -or $port -lt 1 -or $port -gt 65535) {
        throw "RPC endpoint has an invalid port: $Endpoint"
    }
    $tcp = [Net.Sockets.TcpClient]::new()
    try {
        $attempt = $tcp.BeginConnect($hostName, $port, $null, $null)
        if (-not $attempt.AsyncWaitHandle.WaitOne(2000)) { return $true }
        try {
            $tcp.EndConnect($attempt)
            return $false
        } catch {
            return $true
        }
    } finally {
        $tcp.Dispose()
    }
}

Add-Type -AssemblyName System.Net.Http
$gatewayUri = [Uri]$BaseUrl
if (-not (Test-LoopbackUrl $gatewayUri)) {
    throw 'BaseUrl must be an HTTP loopback URL so the API key is never sent over the network.'
}
if ([string]::IsNullOrWhiteSpace($ApiKey)) {
    throw 'Set TETHER_API_KEY or pass -ApiKey. Prefer the environment variable to keep the key out of process listings.'
}
if (($RequireDistributed -or $RunInference -or $UnloadAfter) -and [string]::IsNullOrWhiteSpace($Model)) {
    throw '-Model is required for placement or inference checks.'
}
if ($RequireDistributed -and $RpcEndpoint.Count -lt 2) {
    throw '-RequireDistributed requires at least two -RpcEndpoint values so raw RPC exposure is tested for every contributor.'
}
if ($UnloadAfter -and -not $RunInference) {
    throw '-UnloadAfter requires -RunInference.'
}

$handler = [Net.Http.HttpClientHandler]::new()
$client = [Net.Http.HttpClient]::new($handler)
$client.Timeout = [TimeSpan]::FromSeconds($TimeoutSeconds)

try {
    $unauthorizedRequest = New-Request -Method ([Net.Http.HttpMethod]::Get) -Path '/v1/models' -JsonBody ''
    $unauthorizedResponse = $null
    try {
        $unauthorizedResponse = $client.SendAsync($unauthorizedRequest).GetAwaiter().GetResult()
        if ([int]$unauthorizedResponse.StatusCode -ne 401) {
            throw "Unauthenticated GET /v1/models returned HTTP $([int]$unauthorizedResponse.StatusCode), want 401."
        }
        $summary.checks.unauthenticatedRequestRejected = $true
    } finally {
        $unauthorizedRequest.Dispose()
        if ($null -ne $unauthorizedResponse) { $unauthorizedResponse.Dispose() }
    }

    $models = Invoke-JsonRequest -Method ([Net.Http.HttpMethod]::Get) -Path '/v1/models'
    $modelIds = @($models.data | ForEach-Object { $_.id })
    Write-JsonFile -Name 'models.json' -Value ([ordered]@{ count = $modelIds.Count; models = $modelIds })
    $summary.checks.authenticatedModelInventory = $true
    if (-not [string]::IsNullOrWhiteSpace($Model) -and $Model -notin $modelIds) {
        throw "Model '$Model' is not present in the gateway model inventory."
    }

    $rpcResults = @()
    for ($index = 0; $index -lt $RpcEndpoint.Count; $index++) {
        $closed = Test-ClosedTcpEndpoint -Endpoint $RpcEndpoint[$index]
        $rpcResults += [ordered]@{ endpoint = "rpc-$($index + 1)"; remotelyClosed = $closed }
        if (-not $closed) { throw "Raw RPC endpoint rpc-$($index + 1) accepted a remote TCP connection." }
    }
    Write-JsonFile -Name 'rpc-exposure.json' -Value $rpcResults
    $summary.checks.rawRpcEndpointsClosed = ($RpcEndpoint.Count -gt 0)

    if (-not [string]::IsNullOrWhiteSpace($Model)) {
        $escapedModel = [Uri]::EscapeDataString($Model)
        $plan = Invoke-JsonRequest -Method ([Net.Http.HttpMethod]::Get) -Path "/api/v1/models/$escapedModel/plan"
        $planNodeCount = @($plan.nodes).Count
        $summary.placementMode = $plan.mode
        $summary.placementNodeCount = $planNodeCount
        Write-JsonFile -Name 'placement.json' -Value ([ordered]@{
            model = $plan.model
            mode = $plan.mode
            nodeCount = $planNodeCount
            sizeBytes = $plan.sizeBytes
            reserveBytes = $plan.reserveBytes
            modelReserveBytes = $plan.modelReserveBytes
            kvCacheBytes = $plan.kvCacheBytes
            observedAt = $plan.observedAt
        })
        if ($RequireDistributed -and ($plan.mode -ne 'split' -or $planNodeCount -lt 2)) {
            throw "Placement is '$($plan.mode)' across $planNodeCount contributor(s); a split placement across at least two is required."
        }
        $summary.checks.placementPreview = $true
    }

    if ($RunInference) {
        $body = [ordered]@{
            model = $Model
            messages = @(@{ role = 'user'; content = 'Reply with exactly: Tether acceptance stream received.' })
            stream = $true
            max_tokens = 64
            temperature = 0
        } | ConvertTo-Json -Depth 5 -Compress
        $request = New-Request -Method ([Net.Http.HttpMethod]::Post) -Path '/v1/chat/completions' -JsonBody $body -Authenticated
        $watch = [Diagnostics.Stopwatch]::StartNew()
        $events = [Text.StringBuilder]::new()
        $firstEvent = $null
        $response = $null
        try {
            $response = $client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
            if (-not $response.IsSuccessStatusCode) {
                $errorBody = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
                throw "Streaming inference returned HTTP $([int]$response.StatusCode): $errorBody"
            }
            $stream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
            $reader = [IO.StreamReader]::new($stream)
            try {
                while (-not $reader.EndOfStream) {
                    $line = $reader.ReadLine()
                    if ($null -eq $firstEvent -and $line.StartsWith('data:')) { $firstEvent = $watch.ElapsedMilliseconds }
                    if ($events.Length -lt 1048576) { [void]$events.AppendLine($line) }
                }
            } finally {
                $reader.Dispose()
                $stream.Dispose()
            }
        } finally {
            $watch.Stop()
            $request.Dispose()
            if ($null -ne $response) { $response.Dispose() }
        }
        if ($null -eq $firstEvent) { throw 'Streaming inference completed without an SSE data event.' }
        $summary.firstEventMilliseconds = $firstEvent
        $summary.totalInferenceMilliseconds = $watch.ElapsedMilliseconds
        $summary.checks.streamingInference = $true
        [IO.File]::WriteAllText((Join-Path $OutputDirectory 'completion.sse'), $events.ToString(), [Text.UTF8Encoding]::new($false))

        $states = Invoke-JsonRequest -Method ([Net.Http.HttpMethod]::Get) -Path '/api/v1/model-states'
        $matchingState = @($states.models | Where-Object { $_.model -eq $Model } | Select-Object -First 1)
        if ($matchingState.Count -ne 1 -or $matchingState[0].state -notin @('loaded', 'idle-countdown')) {
            throw "Model state after inference is not loaded or idle-countdown."
        }
        $idleUntil = $null
        if ($null -ne $matchingState[0].PSObject.Properties['idleUntil']) {
            $idleUntil = $matchingState[0].idleUntil
        }
        Write-JsonFile -Name 'model-state.json' -Value ([ordered]@{
            model = $matchingState[0].model
            state = $matchingState[0].state
            nodeCount = @($matchingState[0].nodes).Count
            updatedAt = $matchingState[0].updatedAt
            idleUntil = $idleUntil
        })
        $summary.checks.modelStateObserved = $true

        if ($UnloadAfter) {
            $null = Invoke-JsonRequest -Method ([Net.Http.HttpMethod]::Post) -Path "/api/v1/models/$escapedModel/unload"
            $summary.checks.unloadRequested = $true
        }
    }

    $summary.passed = $true
    Write-Host "Tether acceptance checks passed. Evidence: $OutputDirectory" -ForegroundColor Green
} catch {
    $summary.error = $_.Exception.Message
    Write-Error "Tether acceptance checks failed: $($summary.error)"
} finally {
    $summary.finishedAt = [DateTime]::UtcNow.ToString('o')
    try {
        $commit = (& git -C $repositoryRoot rev-parse HEAD 2>$null).Trim()
        $pin = (Get-Content -Raw -LiteralPath (Join-Path $repositoryRoot 'scripts\llama-cpp-revision.txt')).Trim()
        Write-JsonFile -Name 'environment.json' -Value ([ordered]@{
            tetherCommit = $commit
            llamaCppRevision = $pin
            powershellVersion = $PSVersionTable.PSVersion.ToString()
            osVersion = [Environment]::OSVersion.VersionString
        })
    } catch {
        $summary.checks.environmentCapture = $false
    }
    Write-JsonFile -Name 'summary.json' -Value $summary
    $client.Dispose()
    $handler.Dispose()
}

if (-not $summary.passed) { exit 1 }
