# Operating Agents

Use the Tether desktop applications to pair and manage GPU-node Agents. The
Orchestrator does not use a `status`, `start`, `stop`, or `quit` command-line
workflow; those actions are available in the GUI.

## Pair a node

Before pairing, add the GPU node's Tailscale hostname and ports to the
Orchestrator-side [`node_allowlist.yaml`](configuration.md#node-allowlist).
The node must be online in the Tailnet and appear in that allowlist.

1. Start `tether-agent` on the GPU node. Complete its local preflight or setup
   if needed.
2. When the Agent is not paired, it displays a one-time pairing code. The code
   expires after about two minutes and is never sent over the network.
3. Open `tether` on the Orchestrator. Select **Add Tailnet node**, choose the
   online allowlisted node, and enter that code.
4. After the certificate exchange succeeds, the Orchestrator pins the Agent's
   certificate and the Agent pins the Orchestrator's certificate. Later
   control traffic uses mTLS.

## Monitor and control the RPC server

The Orchestrator checks paired Agents when it opens and every five minutes.
Each node card shows Tailnet reachability, the Agent/RPC-server status, the
latest check time, and round-trip latency.

For an online paired node, select **Start RPC** to start its local
`ggml-rpc-server`, or **Stop RPC** to stop it. **Start RPC** is unavailable
while that server is already running. The RPC server exposes GPU capacity; it
does not load a model. Load models through the Orchestrator or `tether-api`.

## Re-pair an Agent

Use **Re-pair Orchestrator** in `tether-agent` when intentionally rotating
trust or recovering from a pairing problem. After confirmation, the Agent
removes its prior key, certificate, and pinned Orchestrator certificate, then
opens a new pairing window. Pair it again from the Orchestrator using the new
one-time code.

## Local Agent configuration

The GPU-node operator, not the Orchestrator, chooses the RPC executable and
bind address. See [Configuration](configuration.md#agent-configuration) for
the local `agent_config.yaml` format and [Network policy](configuration.md#network-policy)
for the required Tailnet restrictions.
