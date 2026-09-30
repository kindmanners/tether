# Two-node acceptance test

Run this test before tagging a Tether release candidate. It qualifies the
complete secure inference path on real hardware; it does not replace the Go
test suite or CI.

The release candidate passes only when a clean Orchestrator and two GPU
contributors complete this path:

```text
local setup -> pairing -> RPC start -> split placement -> streaming chat
-> unload -> gateway restart -> repeat chat
```

## Test boundaries

- Use the exact Tether commit intended for release on every machine.
- Use the llama.cpp revision in `scripts/llama-cpp-revision.txt`. Do not update
  that pin as part of an acceptance run.
- Pair through the desktop applications with the one-time code. Never put a
  pairing code, API key, private key, certificate, or Tailnet IP in a script,
  command history, log bundle, screenshot, or committed file.
- Use a model whose reservation does not fit on any one selected GPU but does
  fit across at least two contributors. A `whole` placement does not qualify
  the distributed path.
- Run the verifier on the Orchestrator. The gateway must remain loopback-only.
- Treat node names and model output as private operational data. The verifier
  replaces node names with counts in its evidence, but review every bundle
  before sharing it.

## Prerequisites

On the intended release commit:

```powershell
go test ./...
go build ./...
.\scripts\build-windows-release.ps1
```

For each GPU node, follow the relevant Windows or Linux bootstrap guide. Add
both nodes to `node_allowlist.yaml`, complete local Agent setup, and pair each
Agent from the Orchestrator. In Tether, start RPC on both nodes and start the
gateway.

Confirm before continuing:

- The Orchestrator shows both Agents as online and paired.
- Both managed RPC processes are running and bind only to `127.0.0.1`.
- Tailnet policy permits the Orchestrator to reach each Agent port.
- Tailnet policy and host firewalls do not expose either raw RPC port.
- The selected GGUF appears in the Orchestrator model library.

## Run the verifier

Set the API key in the process environment so it does not appear in the
command line or process listing:

```powershell
$env:TETHER_API_KEY = '<key shown by Tether>'
```

First preview and validate placement. Replace the example endpoints with the
two allowlisted Agent hostnames and their configured raw RPC ports. A
successful TCP connection to either endpoint fails the test.

```powershell
.\scripts\acceptance\orchestrator.ps1 `
  -Model '<model id from GET /v1/models>' `
  -RequireDistributed `
  -RpcEndpoint 'node-alpha:50052','node-beta:50052'
```

Then exercise the full inference path. The request uses a fixed, non-sensitive
prompt, streams at most 64 tokens, records first-event and total duration, and
unloads the model after the response finishes:

```powershell
.\scripts\acceptance\orchestrator.ps1 `
  -Model '<model id>' `
  -RequireDistributed `
  -RunInference `
  -UnloadAfter `
  -RpcEndpoint 'node-alpha:50052','node-beta:50052'
```

By default, results are written beneath `acceptance-results/`, which Git
ignores. Use `-OutputDirectory` to choose another local directory. The
verifier never writes the API key.

## Restart and recovery pass

After the first successful inference run:

1. Confirm the model worker exited and GPU memory was released on both nodes.
2. Stop and restart the gateway from the Orchestrator.
3. Run the full verifier command again.
4. During a separate run, terminate one managed RPC process. The request must
   fail clearly; after restarting RPC, a later request must be able to create
   a fresh worker.
5. Stop an Agent during placement. The unavailable node must be omitted or
   placement must fail; it must not silently become an unauthenticated raw RPC
   connection.

Do not rotate certificates merely to test failure handling on machines whose
trust state must be retained. If certificate rejection is tested, use an
isolated disposable pairing state and confirm the mismatched certificate
fails closed.

## Pass criteria

All of the following are required:

- `go test ./...` and `go build ./...` pass at the release commit.
- Unauthenticated gateway access returns HTTP 401.
- Authenticated model inventory succeeds.
- Placement mode is `split` and names at least two contributors.
- Every supplied raw RPC endpoint rejects a remote TCP connection.
- A streaming chat completion reaches its first SSE event and finishes.
- Model state identifies the worker as loaded or idle after inference.
- Explicit unload exits the worker and releases its GPU allocation.
- Gateway restart permits the same flow to succeed again.
- Agent or RPC-process loss produces a bounded, actionable failure and later
  recovery succeeds.
- No secret or private network address appears in the evidence bundle.

Any missing observation is a failed or incomplete run, not an assumed pass.

## Evidence and release record

Keep the generated summary with the release notes or private test record. Add
the following observations manually to `notes.md` beside the generated files:

- OS, GPU, driver, and total VRAM for each participant
- model filename and byte size
- approximate model-load duration
- prompt and generation throughput reported by llama.cpp
- whether GPU memory returned after unload
- recovery behavior and any operator-visible errors

The historical benchmark in `placement.md` predates the current pinned-mTLS
tunnel. Do not replace it until a run using the current release candidate has
been completed and its hardware and inference settings are recorded.
