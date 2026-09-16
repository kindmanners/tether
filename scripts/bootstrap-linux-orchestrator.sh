#!/usr/bin/env bash
# Builds the local llama.cpp server used by a Linux Tether Orchestrator.
# It is intentionally explicit and user-scoped: no Agent config, pairing,
# firewall, or system service state is touched here.
set -Eeuo pipefail
umask 077

INSTALL_MISSING=0
LOCAL_GPU=0
PROGRESS_PATH=""
LLAMA_CPP_PATH=""
LLAMA_CPP_REVISION="3057bb66c86c46d5781e50e85462a760ba7d1feb"

usage() {
  printf '%s\n' 'Usage: bootstrap-linux-orchestrator.sh [--install-missing] [--local-gpu] [--llama-cpp-path PATH] [--progress-path PATH]'
}

while (($#)); do
  case "$1" in
    --install-missing) INSTALL_MISSING=1 ;;
    --local-gpu) LOCAL_GPU=1 ;;
    --progress-path) PROGRESS_PATH="$2"; shift ;;
    --llama-cpp-path) LLAMA_CPP_PATH="$2"; shift ;;
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
    printf '{"step":"%s","detail":"%s"}\n' "$step" "$detail" >"$PROGRESS_PATH"
  fi
}

need() { command -v "$1" >/dev/null 2>&1; }

as_root() {
  if (( EUID == 0 )); then "$@"; return; fi
  if need sudo; then sudo "$@"; return; fi
  printf 'Installing prerequisites requires sudo. Install Git, CMake, and a C/C++ compiler yourself, then run this setup again.\n' >&2
  return 1
}

install_build_tools() {
  if need apt-get; then
    as_root apt-get update && as_root apt-get install -y git cmake build-essential
  elif need dnf; then
    as_root dnf install -y git cmake gcc-c++ make
  elif need pacman; then
    as_root pacman -Sy --needed --noconfirm git cmake base-devel
  else
    printf 'Unsupported package manager. Install Git, CMake, and a C/C++ compiler manually, then run setup again.\n' >&2
    return 1
  fi
}

install_cuda() {
  if need apt-get; then
    as_root apt-get update && as_root apt-get install -y nvidia-cuda-toolkit
  elif need pacman; then
    as_root pacman -Sy --needed --noconfirm cuda
  else
    printf 'Install the NVIDIA CUDA Toolkit for this distribution manually, then run setup again.\n' >&2
    return 1
  fi
}

if [[ -z "$LLAMA_CPP_PATH" ]]; then
  LLAMA_CPP_PATH="${XDG_DATA_HOME:-$HOME/.local/share}/tether/llama.cpp"
fi

progress requirements 'Checking the local llama.cpp build prerequisites.'
if ! need git || ! need cmake || (! need cc && ! need gcc && ! need clang); then
  if (( INSTALL_MISSING )); then
    progress requirements 'Installing missing Git, CMake, and compiler packages with your distro package manager.'
    install_build_tools
  else
    printf 'Git, CMake, and a C/C++ compiler are required.\n' >&2
    exit 1
  fi
fi
need git && need cmake && { need cc || need gcc || need clang; } || { printf 'Build tools are unavailable after setup.\n' >&2; exit 1; }

BUILD_DIR="$LLAMA_CPP_PATH/build-rpc"
if (( LOCAL_GPU )); then
  progress requirements 'Checking NVIDIA CUDA because this Orchestrator will contribute its GPU.'
  if ! need nvidia-smi || ! nvidia-smi -L >/dev/null 2>&1; then
    printf 'No usable NVIDIA GPU/driver was found. Choose control-only mode or install a supported NVIDIA driver.\n' >&2
    exit 1
  fi
  if ! need nvcc; then
    if (( INSTALL_MISSING )); then
      progress requirements 'Installing the NVIDIA CUDA Toolkit with your distro package manager.'
      install_cuda
    else
      printf 'The NVIDIA CUDA Toolkit is required when contributing a local GPU.\n' >&2
      exit 1
    fi
  fi
  need nvcc || { printf 'CUDA Toolkit is unavailable after setup.\n' >&2; exit 1; }
  BUILD_DIR="$LLAMA_CPP_PATH/build-rpc-cuda"
fi

progress source 'Fetching the pinned llama.cpp revision.'
mkdir -p "$(dirname "$LLAMA_CPP_PATH")"
if [[ ! -d "$LLAMA_CPP_PATH/.git" ]]; then
  git clone https://github.com/ggml-org/llama.cpp.git "$LLAMA_CPP_PATH"
fi
git -C "$LLAMA_CPP_PATH" fetch --no-tags origin "$LLAMA_CPP_REVISION"
git -C "$LLAMA_CPP_PATH" cat-file -e "${LLAMA_CPP_REVISION}^{commit}"
git -C "$LLAMA_CPP_PATH" checkout --detach "$LLAMA_CPP_REVISION"

if (( LOCAL_GPU )); then
  progress build 'Configuring the CUDA and RPC-enabled local inference backend.'
  cmake -S "$LLAMA_CPP_PATH" -B "$BUILD_DIR" -DGGML_CUDA=ON -DGGML_RPC=ON -DCMAKE_BUILD_TYPE=Release
else
  progress build 'Configuring the RPC-enabled control-only inference backend.'
  cmake -S "$LLAMA_CPP_PATH" -B "$BUILD_DIR" -DGGML_RPC=ON -DCMAKE_BUILD_TYPE=Release
fi
progress build 'Compiling llama.cpp llama-server. This can take several minutes.'
cmake --build "$BUILD_DIR" --target llama-server --parallel "$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf 4)"
SERVER="$BUILD_DIR/bin/llama-server"
[[ -x "$SERVER" ]] || { printf 'Build completed without expected executable: %s\n' "$SERVER" >&2; exit 1; }
progress complete 'Local inference backend is ready for Tether.'
printf '%s\n' "$SERVER"
