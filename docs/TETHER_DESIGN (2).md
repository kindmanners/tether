# Tether — Design & Onboarding Doc

Status: **In active development.** Not "design phase" anymore — real code
exists, is tested against real infrastructure, and is pushed to
`github.com/kindmanners/tether` (`master` branch) (team and enterprise branches exist too for later use.) This doc is written to be zero-context — if you're reading this without any prior conversation
about Tether, you should be able to understand what it is, why it exists,
and what's been decided vs. still open.

**Current implementation status:**
- **Node registry** (`internal/registry`) — allowlist parsing, live
  Tailscale peer discovery, and the two-layer merge into `Node` values.
- **Certs, pairing, and pinned trust** (`internal/certs`,
  `internal/pairing`, `internal/trust`) — per-machine self-signed
  identities; human-relayed, time-boxed pairing; and exact certificate pins
  for every later connection.
- **Agent command channel** (`internal/agent`, `internal/process`) — real
  mTLS status/start/stop endpoints backed by a managed local subprocess.
- **Desktop control experience** (`cmd/tether`, `cmd/tether-agent`) — Wails
  applications expose node discovery, explicit allowlisting, local Agent
  onboarding, pairing, and start/stop control. The Agent starts its command
  service after local setup and pairing are valid.
- **Real inference gateway** (`cmd/tether-api`) — a loopback OpenAI-compatible
  gateway serves `/v1/models` and `/v1/chat/completions`, manages one
  `llama-server` worker per loaded model, and uses current paired-Agent GPU
  reports for local-or-RPC placement admission.
- **Working llama.cpp RPC integration** — Agent-managed CUDA RPC servers have
  been exercised against a real remote GPU and a split placement; the pinned
  build and measured result are recorded in `HANDOFF.md`.
- **Cross-platform GPU-node onboarding** — Windows and Linux have explicit,
  local setup flows. Linux preflight and provisioning persist progress/logs,
  build the pinned CUDA/RPC server, and preserve the existing pairing flow.

---

## 1. What Tether Is

Tether is a desktop application for running **distributed local LLM inference**
across multiple machines on a private network, using `llama.cpp`'s built-in RPC
mode to split model layers across GPUs on different physical machines.

Concretely, for Ataraxia Productions: the initial test pairing is **Ataraxia**
(RTX 3060, 12GB) and **Mathesis** (RTX 3050) — pooling both GPUs into a single
inference backend to run larger models than either card could hold alone,
without needing a "real" GPU cluster or cloud inference. Wayfarer acts as the
control/orchestration host rather than a GPU contributor. Eudaimonia (Sand's
machine) is a planned future node once the test pairing is validated.

Tether is the control plane and local gateway for that. `llama.cpp` performs
inference; Tether discovers eligible machines, verifies their health and
capabilities, starts/stops their RPC servers, decides whether a requested model
fits locally or should use RPC, and exposes an OpenAI-compatible chat endpoint
for its desktop experience and clients such as Open WebUI.

**Stack:** Go (backend/orchestration), Wails (desktop GUI shell), running over
the existing Tailscale network that already connects Wayfarer, Ataraxia, and
Eudaimonia.

---

## 2. Why This Exists (Problem Being Solved)

- `llama.cpp` RPC mode lets you split a model's layers across multiple hosts,
  each contributing GPU memory. This is already possible manually — you can
  hand-start `rpc-server` on each machine and point a main `llama.cpp`
  instance at them.
- Doing this manually today means: SSH into each machine, remember the right
  flags, start each RPC server in a tmux session, hope you didn't typo an IP,
  and have no visibility into whether a node is actually healthy or just
  silently dead.
- Tether replaces that manual process with: a registry that knows what nodes
  exist, health visibility, one-click(ish) start/stop of remote RPC servers,
  and a chat UI that talks to the resulting distributed backend — instead of
  hand-rolling curl requests against the raw llama.cpp server API.

This is explicitly **not** a v1/MVP throwaway — the goal is a fully usable
tool, built incrementally, not a proof of concept.

---

## 3. Architecture Overview

The deployed path has four cooperating components:

```
┌─────────────────────┐         mTLS          ┌──────────────────────┐
│   Tether desktop      │ ───────────────────► │   Tether Agent        │
│   (Go + Wails)        │                       │   (lightweight Go     │
│                        │ ◄─────────────────── │   daemon, one per      │
│                        │      status/health    │   inference node)      │
└─────────────────────┘                         └──────────────────────┘
         │ starts/uses                                      │ spawns/kills
         ▼                                                  ▼
  tether-api gateway ── llama-server --rpc ── llama.cpp rpc-server
         │
         ▼
   node_allowlist.yaml (which Tailnet hosts are Tether nodes)
```

- **Orchestrator desktop**: the main Wails application. It discovers nodes,
  tracks their state, makes explicit allowlist/pairing decisions, controls
  Agents, and starts its sibling local gateway in packaged installs.
- **`tether-api` gateway**: the loopback OpenAI-compatible service. It owns
  model worker lifecycle, queries paired Agents for free VRAM, and launches
  `llama-server` locally with the chosen RPC endpoints when a split is needed.
- **Agent**: a small daemon that runs persistently on every machine that can
  contribute GPU (Wayfarer, Eudaimonia, later Ataraxia). Listens for commands
  from the Orchestrator over mTLS and starts/stops the local `llama.cpp
  rpc-server` process. Does not make decisions on its own — it's a remote
  hands, not a brain.
- **`llama.cpp rpc-server`**: not written by us. The actual inference backend,
  spawned as a subprocess by the Agent.

---

## 4. Node Discovery & Registry

### 4.1 Two-layer verification

Tailscale tells you *what's on the network*, not *what's running llama.cpp
RPC*. So discovery is two layers:

1. **Tailnet presence** — query `tailscale status --json` (or the Tailscale
   Go client library) to get the live list of peers, their MagicDNS
   hostnames, and Tailscale IPs.
2. **App-level allowlist** — a YAML config file, hand-maintained, that says
   "these specific tailnet hosts are Tether nodes." Tailscale has no native
   concept of this — ACL tags are for network policy, not app service
   discovery — so we maintain our own mapping.

A host only becomes a "Node" in Tether's registry if it appears in **both**
lists. This avoids false positives from other tailnet peers (e.g. Citadel,
your phone) that were never meant to run inference.

The third, runtime layer is now implemented: the dashboard and gateway contact
only paired Agents over pinned mTLS to obtain process status and the
machine-observed GPU capability report. Runtime reachability augments discovery;
it does not turn a random Tailnet peer into a Tether node.

### 4.2 Why YAML, not a database

The allowlist is small, human-edited, and changes rarely (you add a node when
you plug in a new machine, not automatically). That makes it config, not
data — it should be git-diffable and editable in a text editor, not something
you need a CLI subcommand to inspect.

Runtime state (last-seen timestamps, health history, job logs) is a different
concern — that's machine-written data that changes constantly, and is the
right fit for an embedded DB (BoltDB is the likely pick) **once we actually
need persistence**. We are not adding that dependency until there's a real
persistence need — the registry itself is in-memory for now, rebuilt from the
YAML + live Tailscale query on every Orchestrator start.

### 4.3 Node data model

```go
type Node struct {
    Hostname     string    // Tailscale MagicDNS name, e.g. "wayfarer"
    TailscaleIP  string    // 100.x.x.x
    Role         string    // "rpc-node" (app-level tag, from allowlist)
    AgentPort    int       // port the Agent daemon listens on
    RPCPort      int       // port llama.cpp rpc-server listens on, once running
    Status       NodeStatus // Unknown / Online / Offline / AgentUnreachable
    LastSeen     time.Time
    Capabilities NodeCapabilities // populated later — VRAM, GPU model, etc.
}

type NodeCapabilities struct {
    GPUModel  string
    VRAMTotal int64 // bytes
    // extended later once scheduling logic needs it
}
```

The bootstrap report and Agent `/capabilities` endpoint now provide observed
GPU model, driver, total VRAM, and free VRAM to runtime callers. The gateway
uses those values for admission and local-versus-RPC choice; the registry type
remains deliberately compact while scheduling policy continues to evolve.

### 4.4 `node_allowlist.yaml` (draft shape)

```yaml
nodes:
  - hostname: ataraxia
    role: rpc-node
    agent_port: 7420
    rpc_port: 50052
  - hostname: mathesis
    role: rpc-node
    agent_port: 7420
    rpc_port: 50053 # 50052 is owned by Incredibuild LicenseService
```

Wayfarer is deliberately **not** in this list — it's modeled as the
Orchestrator host, not an inference contributor. Eudaimonia is left out for
now since it's not part of the initial test pairing; add it once it's ready
to join.

---

## 5. Delivery Order and Current State

1. **Node registry** (Tailscale query + YAML allowlist merge, in-memory) is
   implemented and remains independently testable.
2. **Agent daemon and control path** (mTLS-authenticated, remote
   start/stop/status of `ggml-rpc-server` processes). The CLI uses the selected
   allowlisted node's `agent_port` for both first-contact pairing and later
   control. On first contact it pins the Agent certificate; on later runs it
   builds `PinnedTLSConfig` from that pin before issuing any command. The
   Agent starts only its locally configured binary and bind address.
   `ggml-rpc-server` exposes remote devices but does not load a model; the
   Orchestrator-side `llama-cli` or `llama-server` loads the GGUF model and
   connects to the device endpoint with `--rpc`.
3. **Inference routing and chat API** are implemented as `tether-api`: model
   inventory, OpenAI-compatible chat completions, managed `llama-server`
   workers, placement admission, and RPC fallback. The desktop automatically
   starts the local gateway when it is packaged beside it; clients can also use
   the loopback endpoint directly.
4. **Onboarding** follows the same safety boundary: the Agent presents
   read-only evidence first, then performs explicit local provisioning. It
   builds the pinned `GGML_CUDA=ON`/`GGML_RPC=ON` RPC binary, writes only local
   config/report state, and returns to the unchanged human-relayed pairing
   flow.

This sequencing made every layer independently verifiable before the next one
depended on it. It is no longer a future build list.

---

## 6. Security Model

The Agent daemon can start arbitrary processes on someone else's machine
(literally — Sand's, in our case) on command from the Orchestrator. That's
real remote code execution, even on a private tailnet, so it gets real auth:

- **mTLS between Orchestrator and every Agent.** Both sides present certs;
  neither trusts a connection based on network location (i.e. "you're on the
  tailnet" is not sufficient trust on its own) or a static shared secret.
- This was chosen deliberately over a simpler shared-token auth scheme,
  because the blast radius of a leaked token (arbitrary remote process
  execution) is high enough to justify the extra setup cost.

### 6.1 Cert provisioning — decided approach

Three options were considered:

- **(A) Manual pinned certs** — hand-generate a keypair per node with
  `openssl`, manually copy public certs between Orchestrator and each Agent.
  Simple trust model, zero code, but every step is a manual file operation a
  non-developer user would not know how to do correctly (which file is
  public vs. private, why the *other* machine needs *your* cert, no error
  recovery if a path is wrong). Fine for us; not viable if Tether is ever
  used by someone who isn't a developer.
- **(B) Self-built local CA** — Tether runs its own CA, issues certs to
  Orchestrator and Agents. Proper chain-of-trust, automatable onboarding, but
  meaningfully more code (CA logic, an issuance flow, and a bootstrapping
  problem: how does a brand-new, ungisted Agent authenticate to the CA to get
  its first cert?). Overkill for a handful of nodes added rarely.
- **(C) `tailscale cert`** — Tailscale's built-in Let's Encrypt-backed
  issuance (`tailscale cert <hostname>` after enabling HTTPS in the admin
  console). Issuance is free, but renewal and file distribution are still
  manual — Tailscale doesn't track where you moved the cert file, so it
  can't auto-renew it for you. It also publishes tailnet machine hostnames to
  a public ledger, which is an unnecessary exposure for a private cluster
  with no actual need for publicly-valid certs. Rejected: it looks automatic
  but isn't, and costs a public disclosure for no real benefit here.

**Decided: (A)'s trust model — self-signed, pinned certs, no CA — but with
the manual file-copy step automated away**, so the tool doesn't fall apart
the moment a non-developer tries to use it.

**How:** the tailnet itself is the bootstrap trust channel. Two already-
mutually-authenticated Tailscale peers don't need a human to manually
sneakernet cert files between them — the Orchestrator can reach a new
Agent's pairing endpoint directly over Tailscale and exchange public certs
programmatically. Concretely:

1. Agent generates its own keypair locally on first run. Private key never
   leaves that machine.
2. User runs "Add Node" in the Orchestrator UI → Orchestrator confirms the
   candidate host via the existing registry (tailnet presence + allowlist,
   see §4) → sends a pairing request to the Agent's pairing endpoint over
   Tailscale.
3. Agent responds with its public cert. Orchestrator responds with its own.
   Both sides pin what they received. No file copying, no terminal, no
   "which one is the .key file."
4. Since certs are self-signed and pinned (not Let's Encrypt-issued), there's
   no forced 90-day renewal cycle — expiry can be set long (e.g. 1–2 years),
   and re-pairing (re-running step 2) is the rotation mechanism if ever
   needed, rather than requiring a renewal system on day one.

This makes the Agent's v1 scope larger than plain start/stop — it now needs
a first-run pairing handshake exposed over the network — but it's the actual
gap between "a tool Star and Sand can use" and "a tool anyone could install
and pair a second machine into," which matches the stated goal of building
the real tool, not a personal shortcut.

### 6.2 Pairing authentication (first-contact problem)

The pairing handshake in §6.1 has a bootstrapping gap: the *first* request
between an unpaired Orchestrator and Agent happens before any mTLS trust
exists, so that initial request cannot itself be mTLS-authenticated. Without
something protecting it, any device on the tailnet that can reach the
Agent's pairing endpoint could trigger a pairing it shouldn't.

**Decided: human-relayed pairing code + time-boxed pairing window.**

Both pieces are required together — each covers a gap the other leaves open:

- A code with no time limit is a permanent shared secret sitting in the
  Agent's memory/logs indefinitely, which is its own liability.
- A time window with no code just means anyone on the tailnet who happens to
  hit the endpoint during that window pairs successfully — the window
  narrows the opportunity but doesn't authenticate the requester.

**Flow:**

1. On the target Agent, first launch (or `tether-agent -pair`) opens a
   short-lived pairing window and displays a random, human-typeable code.
2. In the Orchestrator, the user selects that already-registered host and
   enters the code displayed by the Agent. This is the out-of-band step —
   the code travels via the human physically present at both machines, not
   over the network connection it is meant to secure. An attacker therefore
   needs both tailnet access and the code through a separate channel.
3. The Agent's pairing endpoint accepts connections only during this
   time-boxed window, which closes after roughly two minutes or the first
   successful pairing. Outside the window there is no bootstrap endpoint,
   bounding the attack surface to an active pairing attempt.
4. Within the window, the Agent validates the code in the incoming pairing
   request. Only on a match do both sides exchange and pin public certs
   (§6.1, steps 3–4).
5. Pairing code is single-use and discarded immediately after either a
   successful match or window expiry — it is never persisted or logged.

**Two mechanisms considered and not chosen:**

- *Fingerprint confirm-on-both-ends* (à la SSH host-key verification — both
  sides display a short hash of the exchanged cert, user visually confirms
  they match) — lower friction, no typing, but weaker: it relies on the
  human noticing a mismatch rather than requiring a secret to be present at
  all before pairing can succeed. Not chosen because the goal here is
  correctness over minimal friction.
- *Pairing code with no time window* — rejected per the reasoning above;
  without a window the code is a standing secret rather than a
  narrow-purpose one-time token.

---

## 7. Open Questions / Not Yet Decided

These are explicitly unresolved — don't assume any of these unless it's
written elsewhere as decided:

- **Agent deployment/update story**: how does the Agent binary get onto a new
  node — manual copy, or does the Orchestrator push updates?
- **Scheduling refinements**: `tether-api` already makes capacity admission
  and local-versus-RPC decisions from current free VRAM. More sophisticated
  multi-node layer allocation, throughput benchmarking, and policy tuning are
  still open.
- **Desktop chat UX**: the working OpenAI-compatible gateway is the supported
  inference path today. The richer in-app chat workflow and whether it needs
  additional streaming/presentation features remain product work, not a
  missing llama.cpp integration.

---

## 8. Glossary (for zero-context readers)

| Term | Meaning |
|---|---|
| Tailscale | The mesh VPN connecting all of Star's and Sand's machines |
| Tailnet | The private network Tailscale creates |
| MagicDNS | Tailscale's hostname resolution (e.g. `wayfarer` instead of an IP) |
| `llama.cpp` | The inference engine actually running the models |
| RPC mode | `llama.cpp` feature allowing model layers to be split across networked hosts |
| Wails | Go framework for building desktop apps with a web-based UI |
| Orchestrator | The main Tether desktop app — discovery, local allowlisting, pairing, and Agent control |
| `tether-api` | Loopback OpenAI-compatible gateway that owns llama.cpp model workers and placement |
| Agent | Small daemon per inference node — executes start/stop commands locally |
| Node | A machine registered with Tether as an inference contributor |

---

## 9. Machine Reference (current known tailnet)

| Machine | Role | Tailscale IP | Status |
|---|---|---|---|
| Wayfarer | Linux (Arch), Orchestrator/control host | 100.104.147.111 |
| Ataraxia | Linux (Arch), RTX 3060 12GB — test inference node | 100.96.74.126 |
| Mathesis | Windows, RTX 3050 — test inference node | 100.114.155.22 |
| Eudaimonia | Windows, RTX 5060 8GB - test inference node | 100.73.93.27 |
| Citadel | Star's Android — not a Tether node | 100.120.207.41 | 

Status column reflects a snapshot from `tailscale status` and will go stale —
treat it as illustrative, not live.

---

## 10. Distribution / Installer (Planned, Not Yet Started)

Eventually Tether should install like a normal desktop app — a Windows
installer with the familiar "click Next a few times" wizard experience,
Start Menu shortcut, and a proper uninstall entry — rather than being a
bare `.exe` someone has to know how to run manually.

**Tentative pick: Inno Setup.** Considered against two alternatives:

- **WiX Toolset** — produces "true" MSI installers, which is what most
  established Windows software uses under the hood, but has a real
  learning curve (its own XML-based configuration language) that's more
  ceremony than a two-person team needs for v1.
- **NSIS** — similar category to Inno Setup, and notably what Wails' own
  packaging documentation tends to reference, since it also works
  reasonably for Linux packaging via cross-compilation. Worth revisiting
  if cross-platform installer tooling ends up mattering more than expected.
- **Inno Setup** — script-based (a plain `.iss` config file), compiles to
  a single installer `.exe`, and most directly produces the classic
  white-wizard experience being asked for here, with meaningfully less
  setup ceremony than WiX. Chosen as the tentative default for that reason.

**Explicitly deferred, not skipped.** This is a packaging concern — its
entire job is wrapping a finished, stable binary nicely. Building it now
would mean targeting a codebase that's still changing shape (config file
locations, default ports, first-run behavior), so installer config would
need repeated rework as the app evolves. Same build-order reasoning as
§5: package once there's something stable worth packaging.

**Right time to pick this up:** when the now-working onboarding and gateway
flows have a stable release/update policy. An installer should package the
desktop, Agent, and gateway coherently rather than reimplement their local
setup or pairing behavior.

**Note:** So after a bit of dilly dallying, it will just be a .exe and it will just launch, so it won't need a packager.

