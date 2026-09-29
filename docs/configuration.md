# Configuration

Tether has two intentionally separate configuration surfaces:

- `node_allowlist.yaml` is an Orchestrator-side, human-maintained list of
  Tailnet nodes Tether is allowed to discover and contact.
- `agent_config.yaml` is local state on each GPU node. It tells that Agent
  which `ggml-rpc-server` executable it may start. Its bind address is fixed
  to IPv4 loopback and cannot be changed by the Orchestrator.

## Node allowlist

By default, `tether-api` and `tether-dashboard` look for
`node_allowlist.yaml` in their working directory. Pass `-allowlist /path/to/file`
to select another file.

```yaml
nodes:
  - hostname: node-alpha
    role: rpc-node
    agent_port: 7420
    rpc_port: 50052
```

Each entry requires a unique `hostname`, a non-empty `role`, and distinct,
valid `agent_port` and `rpc_port` values. The hostname must match the name
reported by Tailscale. Discovery only considers a machine when it is both
online in the Tailnet and present in this allowlist.

`agent_port` is the Tether Agent's certificate-pinned mTLS control and tunnel
port. `rpc_port` is used only by the local `127.0.0.1` llama.cpp process. Never
expose the RPC port to the Tailnet, LAN, or internet.

## Registry data model

The allowlist is merged with live Tailscale data to create a registry node.
The following simplified Go view is useful when integrating with the registry;
the allowlist itself contains only `hostname`, `role`, and the two ports.

```go
type Node struct {
	Hostname     string
	TailscaleIP  string
	Role         string
	AgentPort    int
	RPCPort      int
	Status       NodeStatus
	LastSeen     time.Time
	Capabilities NodeCapabilities
}

type NodeCapabilities struct {
	CPUModel  string
	GPUModel  string
	VRAMTotal int64 // bytes
	RAMTotal  int64 // bytes
}
```

## Agent configuration

The Agent reads its local file from the platform user configuration directory:

- Windows: `%AppData%\tether\agent_config.yaml`
- Linux: `~/.config/tether/agent_config.yaml` (or `$XDG_CONFIG_HOME` when set)

```yaml
rpc_server_path: C:\llama.cpp\build-rpc-cuda\bin\Release\ggml-rpc-server.exe
rpc_listen_host: 127.0.0.1
```

On Linux, `rpc_server_path` can instead be a path such as
`/opt/llama.cpp/build-rpc/bin/ggml-rpc-server`. `rpc_listen_host` is retained
as a migration field but must be exactly `127.0.0.1`. The process launcher
also hard-codes loopback, so configuration cannot widen the bind address. The
path must exist and be a file before the Agent accepts the configuration.

Changing the Agent configuration is a local operator action. Pairing only
establishes identity and trust; it does not give the Orchestrator authority to
choose an executable or bind address.

## Network policy

`ggml-rpc-server` has no authentication and remains bound to `127.0.0.1`.
Remote RPC is transported only through the Agent's HTTP/1.1 CONNECT endpoint,
which uses the exact certificates established during pairing. Allow only the
specific Orchestrator to reach Agent TCP 7420. Certificate rotation fails
closed until the node is explicitly re-paired.

For example, assign role tags to the two machine types and grant the
Orchestrator access only to the Agent control and tunnel port:

```jsonc
{
  "tagOwners": {
    "tag:tether-orchestrator": ["autogroup:admin"],
    "tag:tether-agent": ["autogroup:admin"]
  },
  "grants": [
    {
      "src": ["tag:tether-orchestrator"],
      "dst": ["tag:tether-agent"],
      "ip": ["tcp:7420"]
    }
  ]
}
```

Use equivalent selectors for named devices or groups if tags do not fit your
Tailnet. Keep the host firewall aligned with this policy. Do not add an inbound
rule for `rpc_port`; Tether's Windows setup removes its obsolete RPC rule.

Paired nodes are trusted peers: mTLS prevents arbitrary network clients from
reaching RPC, but it does not sanitize RPC messages, sandbox llama.cpp, or
protect against malicious authenticated peers or hostile localhost processes.
