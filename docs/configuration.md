# Configuration

Tether has two intentionally separate configuration surfaces:

- `node_allowlist.yaml` is an Orchestrator-side, human-maintained list of
  Tailnet nodes Tether is allowed to discover and contact.
- `agent_config.yaml` is local state on each GPU node. It tells that Agent
  which `ggml-rpc-server` executable it may start and which local address it
  may bind. The Orchestrator cannot set either value remotely.

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

`agent_port` is the Tether Agent's mTLS control port. `rpc_port` is the port
the local llama.cpp RPC server will use. Do not expose either port publicly;
allow only the required Tailnet traffic.

## Agent configuration

The Agent reads its local file from the platform user configuration directory:

- Windows: `%AppData%\tether\agent_config.yaml`
- Linux: `~/.config/tether/agent_config.yaml` (or `$XDG_CONFIG_HOME` when set)

```yaml
rpc_server_path: C:\llama.cpp\build-rpc-cuda\bin\Release\ggml-rpc-server.exe
rpc_listen_host: 100.x.y.z
```

On Linux, `rpc_server_path` can instead be a path such as
`/opt/llama.cpp/build-rpc/bin/ggml-rpc-server`. `rpc_listen_host` should be the
node's Tailscale IPv4 address. The path must exist and be a file before the
Agent will accept the configuration.

Changing the Agent configuration is a local operator action. Pairing only
establishes identity and trust; it does not give the Orchestrator authority to
choose an executable or bind address.
