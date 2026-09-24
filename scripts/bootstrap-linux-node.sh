#!/usr/bin/env bash
# Copyright (C) 2026 kindmanners on github
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as
# published by the Free Software Foundation, either version 3 of the
# License, or (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

# Audits or provisions a Linux NVIDIA GPU host for Tether.
#
# The script is intentionally user-scoped. It never pairs the Agent, modifies
# Tailnet ACLs, opens a firewall, or writes system service configuration. The
# only Tether state it writes is the local Agent config and capability report.
set -Eeuo pipefail
umask 077

PROVISION=0
INSTALL_MISSING=0
REPLACE_AGENT_CONFIG=0
SKIP_RPC_BUILD=0
PROGRESS_PATH=""
CANCEL_PATH=""
LLAMA_CPP_PATH=""
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/tether"
AGENT_PORT=7420
RPC_PORT=50053
LLAMA_CPP_REVISION="3057bb66c86c46d5781e50e85462a760ba7d1feb"

usage() {
  printf '%s\n' 'Usage: bootstrap-linux-node.sh --provision [--install-missing] [--llama-cpp-path PATH] [--progress-path PATH] [--cancel-path PATH]'
}

while (($#)); do
  case "$1" in
    --provision) PROVISION=1 ;;
    --install-missing) INSTALL_MISSING=1 ;;
    --replace-agent-config) REPLACE_AGENT_CONFIG=1 ;;
    --skip-rpc-build) SKIP_RPC_BUILD=1 ;;
    --progress-path) PROGRESS_PATH="$2"; shift ;;
    --cancel-path) CANCEL_PATH="$2"; shift ;;
    --llama-cpp-path) LLAMA_CPP_PATH="$2"; shift ;;
    --config-dir) CONFIG_DIR="$2"; shift ;;
    --agent-port) AGENT_PORT="$2"; shift ;;
    --rpc-port) RPC_PORT="$2"; shift ;;
    --llama-cpp-revision) LLAMA_CPP_REVISION="$2"; shift ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

progress() {
  local step="$1" detail="$2"
  printf '\n==> %s\n' "$detail"
  if [[ -n "$PROGRESS_PATH" ]]; then
    mkdir -p "$(dirname "$PROGRESS_PATH")"
    # All details below are fixed strings, avoiding ambiguous shell output in
    # the small JSON record consumed by the desktop UI.
    printf '{"step":"%s","detail":"%s"}\n' "$step" "$detail" >"$PROGRESS_PATH"
  fi
}

cancelled() {
  [[ -n "$CANCEL_PATH" && -e "$CANCEL_PATH" ]]
}

check_cancel() {
  if cancelled; then
    progress cancelled 'Setup was cancelled. No Agent configuration was written.'
    exit 130
  fi
}

fail() {
  local code=$?
  if (( code != 0 )); then
    if cancelled; then
      progress cancelled 'Setup was cancelled. No Agent configuration was written.'
    else
      progress failed 'Linux setup stopped. Read the persistent setup log, correct the reported prerequisite, then run setup again.'
    fi
  fi
  exit "$code"
}
trap fail EXIT

need() { command -v "$1" >/dev/null 2>&1; }

require_port_free() {
  local port="$1" label="$2"
  if ! need ss; then
    printf 'Cannot verify %s port %s: install iproute2 (the ss command) and run setup again.\n' "$label" "$port" >&2
    return 1
  fi
  if ss -ltnH | awk -v p=":${port}" '$4 ~ (p "$") { found=1 } END { exit !found }'; then
    printf 'TCP %s is already listening; choose another port before provisioning %s.\n' "$port" "$label" >&2
    return 1
  fi
}

package_manager() {
  if need apt-get; then printf apt; return; fi
  if need dnf; then printf dnf; return; fi
  if need pacman; then printf pacman; return; fi
  return 1
}

as_root() {
  if (( EUID == 0 )); then "$@"; return; fi
  if need sudo; then sudo "$@"; return; fi
  printf 'Installing packages requires root privileges. Install sudo or run the documented distro command in a terminal, then re-run setup.\n' >&2
  return 1
}

install_build_tools() {
  local manager
  manager="$(package_manager)" || {
    printf 'Unsupported package manager. Install Git, CMake, a C/C++ compiler, NVIDIA CUDA Toolkit, and Tailscale manually, then run setup again.\n' >&2
    return 1
  }
  progress requirements 'Installing missing Git, CMake, and compiler packages with your distro package manager.'
  case "$manager" in
    apt)
      as_root apt-get update
      as_root apt-get install -y git cmake build-essential iproute2
      ;;
    dnf)
      as_root dnf install -y git cmake gcc-c++ make iproute
      ;;
    pacman)
      as_root pacman -Sy --needed --noconfirm git cmake base-devel iproute2
      ;;
  esac
}

install_cuda_toolkit() {
  local manager
  manager="$(package_manager)" || {
    printf 'Unsupported package manager. Install the NVIDIA CUDA Toolkit for this distribution manually, then run setup again.\n' >&2
    return 1
  }
  progress requirements 'Installing the CUDA Toolkit with your distro package manager.'
  case "$manager" in
    apt) as_root apt-get update; as_root apt-get install -y nvidia-cuda-toolkit ;;
    pacman) as_root pacman -Sy --needed --noconfirm cuda ;;
    dnf)
      printf 'Fedora/RHEL CUDA packages require NVIDIA\047s configured CUDA repository. Add the appropriate NVIDIA repository, install cuda-toolkit, then run setup again.\n' >&2
      return 1
      ;;
  esac
}

tailscale_hostname() {
  tailscale status --json | sed -n '/"Self": {/,/^[[:space:]]*}/ s/.*"DNSName": "\([^"]*\)".*/\1/p' | head -n1 | sed 's/\..*$//'
}

write_report() {
  local report_path="$CONFIG_DIR/bootstrap-report.json" gpu_json="" comma="" line name total free driver
  while IFS=, read -r name total free driver; do
    name="${name# }"; total="${total# }"; free="${free# }"; driver="${driver# }"
    gpu_json+="${comma}{\"name\":\"${name}\",\"driverVersion\":\"${driver}\",\"vramBytes\":$((total * 1048576)),\"vramFreeBytes\":$((free * 1048576))}"
    comma="," 
  done < <(nvidia-smi --query-gpu=name,memory.total,memory.free,driver_version --format=csv,noheader,nounits)
  [[ -n "$gpu_json" ]] || { printf 'nvidia-smi returned no GPU capabilities.\n' >&2; return 1; }
  cat >"$report_path" <<EOF
{"observedAt":"$(date -u +%Y-%m-%dT%H:%M:%SZ)","hostname":"$TAILSCALE_HOSTNAME","tailscaleIP":"$TAILSCALE_IP","agentPort":$AGENT_PORT,"rpcPort":$RPC_PORT,"cudaVersion":"$CUDA_VERSION","gpus":[$gpu_json],"agentConfigPath":"$CONFIG_DIR/agent_config.yaml"}
EOF
  printf '%s\n' "$report_path"
}

if (( AGENT_PORT < 1 || AGENT_PORT > 65535 || RPC_PORT < 1 || RPC_PORT > 65535 || AGENT_PORT == RPC_PORT )); then
  printf 'Agent and RPC ports must be distinct values between 1 and 65535.\n' >&2
  exit 2
fi
if (( INSTALL_MISSING && ! PROVISION )); then
  printf '%s\n' '--install-missing changes this machine and requires --provision.' >&2
  exit 2
fi
if [[ -z "$LLAMA_CPP_PATH" ]]; then
  LLAMA_CPP_PATH="${XDG_DATA_HOME:-$HOME/.local/share}/tether/llama.cpp"
fi

progress requirements 'Checking NVIDIA GPU, CUDA, Tailscale, build tools, and selected ports (read-only).'
if ! need nvidia-smi || ! nvidia-smi -L >/dev/null 2>&1; then
  printf 'No usable NVIDIA driver/GPU was found. Install a supported NVIDIA driver, reboot if required, then run setup again.\n' >&2
  exit 1
fi
if ! need nvcc; then
  if (( INSTALL_MISSING )); then install_cuda_toolkit; else
    printf 'CUDA Toolkit is unavailable (nvcc was not found). Install the NVIDIA CUDA Toolkit for this distribution, then run setup again.\n' >&2
    exit 1
  fi
fi
need nvcc || { printf 'CUDA Toolkit is still unavailable after installation. Re-open Tether Agent and run setup again.\n' >&2; exit 1; }
CUDA_VERSION="$(nvcc --version | sed -n 's/.*release \([0-9.]*\).*/\1/p' | tail -n1)"
[[ -n "$CUDA_VERSION" ]] || CUDA_VERSION=available

if ! need tailscale || ! tailscale status --json >/dev/null 2>&1; then
  printf 'Tailscale is not ready. Install it from https://tailscale.com/download/linux and sign in to the intended Tailnet; Tether never supplies credentials.\n' >&2
  exit 1
fi
TAILSCALE_IP="$(tailscale ip -4 2>/dev/null | head -n1 || true)"
TAILSCALE_HOSTNAME="$(tailscale_hostname)"
if [[ -z "$TAILSCALE_IP" || -z "$TAILSCALE_HOSTNAME" ]]; then
  printf 'Tailscale is running but has no usable local IPv4 address or hostname. Complete Tailscale sign-in, then run setup again.\n' >&2
  exit 1
fi

if ! need git || ! need cmake || ! need ss || (! need cc && ! need gcc && ! need clang); then
  if (( INSTALL_MISSING )); then install_build_tools; else
    printf 'Git, CMake, and a C/C++ compiler are required. Re-run with --install-missing to use apt/dnf/pacman, or install them yourself.\n' >&2
    exit 1
  fi
fi
need git && need cmake && { need cc || need gcc || need clang; } || { printf 'Build tools are still unavailable after installation. Re-open Tether Agent and run setup again.\n' >&2; exit 1; }
require_port_free "$AGENT_PORT" 'the Tether Agent'
require_port_free "$RPC_PORT" 'llama.cpp RPC'

if (( ! PROVISION )); then
  progress complete 'Read-only Linux preflight completed. Re-run with --provision to build and configure this local node.'
  exit 0
fi

check_cancel
progress tailscale 'Tailscale is connected. Keeping pairing and Tailnet authorization as an explicit local step.'
RPC_SERVER="$LLAMA_CPP_PATH/build-rpc-cuda/bin/ggml-rpc-server"
if (( SKIP_RPC_BUILD )); then
  [[ -x "$RPC_SERVER" ]] || { printf '%s\n' "--skip-rpc-build requires an existing executable at $RPC_SERVER." >&2; exit 1; }
  progress rpc-server 'Using the existing local llama.cpp CUDA RPC server.'
else
  progress rpc-server 'Fetching the pinned llama.cpp revision and preparing the CUDA RPC build. This can take several minutes.'
  mkdir -p "$(dirname "$LLAMA_CPP_PATH")"
  if [[ ! -d "$LLAMA_CPP_PATH/.git" ]]; then
    git clone https://github.com/ggml-org/llama.cpp.git "$LLAMA_CPP_PATH"
  fi
  check_cancel
  git -C "$LLAMA_CPP_PATH" fetch --no-tags origin "$LLAMA_CPP_REVISION"
  git -C "$LLAMA_CPP_PATH" cat-file -e "${LLAMA_CPP_REVISION}^{commit}"
  git -C "$LLAMA_CPP_PATH" checkout --detach "$LLAMA_CPP_REVISION"
  cmake -S "$LLAMA_CPP_PATH" -B "$LLAMA_CPP_PATH/build-rpc-cuda" -DGGML_CUDA=ON -DGGML_RPC=ON -DCMAKE_BUILD_TYPE=Release
  check_cancel
  progress rpc-server 'Compiling the pinned llama.cpp CUDA RPC server. Live compiler output is being saved to the setup log.'
  cmake --build "$LLAMA_CPP_PATH/build-rpc-cuda" --target ggml-rpc-server --parallel "$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf 4)"
  [[ -x "$RPC_SERVER" ]] || { printf 'Build completed without expected executable: %s\n' "$RPC_SERVER" >&2; exit 1; }
fi

check_cancel
progress firewall 'Reviewing narrow firewall guidance; Tether will not modify firewall rules automatically.'
if need ufw; then
  printf 'Firewall guidance (not applied): after configuring a Tailscale grant for the specific Orchestrator, optionally allow its exact Tailscale IP with: sudo ufw allow from <orchestrator-tailscale-ip> to any port %s proto tcp; sudo ufw allow from <orchestrator-tailscale-ip> to any port %s proto tcp\n' "$AGENT_PORT" "$RPC_PORT"
elif need firewall-cmd; then
  printf 'Firewall guidance (not applied): after configuring a Tailscale grant for the specific Orchestrator, use your active firewalld zone to allow TCP %s and %s only from that Orchestrator address.\n' "$AGENT_PORT" "$RPC_PORT"
else
  printf 'Firewall guidance: configure a Tailscale grant for the specific Orchestrator. If a host firewall is enabled, allow TCP %s and %s only from that Orchestrator address. No firewall changes were made.\n' "$AGENT_PORT" "$RPC_PORT"
fi

progress configuration 'Writing only the local Agent configuration and observed GPU capability report.'
mkdir -p "$CONFIG_DIR"
CONFIG_PATH="$CONFIG_DIR/agent_config.yaml"
KEEP_EXISTING=0
if [[ -f "$CONFIG_PATH" && $REPLACE_AGENT_CONFIG -eq 0 ]]; then
  EXISTING_RPC_SERVER="$(sed -n "s|^rpc_server_path:[[:space:]]*['\"]\{0,1\}\([^'\"]*\)['\"]\{0,1\}[[:space:]]*$|\1|p" "$CONFIG_PATH" | head -n1)"
  if [[ -n "$EXISTING_RPC_SERVER" && -x "$EXISTING_RPC_SERVER" ]]; then
    KEEP_EXISTING=1
  else
    printf 'Replacing unusable local Agent configuration at %s.\n' "$CONFIG_PATH"
  fi
fi
if (( ! KEEP_EXISTING )); then
  config_tmp="$(mktemp "$CONFIG_DIR/agent_config.yaml.XXXXXX")"
  printf "rpc_server_path: '%s'\nrpc_listen_host: '%s'\n" "$RPC_SERVER" "$TAILSCALE_IP" >"$config_tmp"
  mv "$config_tmp" "$CONFIG_PATH"
else
  printf 'Keeping existing local Agent configuration at %s (use --replace-agent-config to replace it).\n' "$CONFIG_PATH"
fi
REPORT_PATH="$(write_report)"
progress complete 'Local Linux setup completed. Review the firewall guidance in the persistent setup log, then return here to pair this node.'
printf '\nProvisioning complete.\nRPC endpoint: %s:%s\nCapability report: %s\n' "$TAILSCALE_IP" "$RPC_PORT" "$REPORT_PATH"
