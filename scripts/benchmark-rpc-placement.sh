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

# Compare a whole-model local run with the current RPC mesh path before using
# placement results to tune policy.  It retains both llama.cpp logs for review.
set -euo pipefail

usage() {
  printf '%s\n' "Usage: $0 --model PATH --rpc HOST:PORT [--llama-cli PATH] [--prompt TEXT] [--context N] [--tokens N]"
}

model=""
rpc=""
llama_cli="llama.cpp/build-rpc/bin/llama-cli"
prompt="Reply with exactly: Tether RPC works."
context=1024
tokens=12
while [ "$#" -gt 0 ]; do
  case "$1" in
    --model) model=${2:?}; shift 2 ;;
    --rpc) rpc=${2:?}; shift 2 ;;
    --llama-cli) llama_cli=${2:?}; shift 2 ;;
    --prompt) prompt=${2:?}; shift 2 ;;
    --context) context=${2:?}; shift 2 ;;
    --tokens) tokens=${2:?}; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

if [ -z "$model" ] || [ -z "$rpc" ]; then usage >&2; exit 2; fi
if [ ! -f "$model" ]; then printf 'Model not found: %s\n' "$model" >&2; exit 2; fi
if [ ! -x "$llama_cli" ]; then printf 'llama-cli is not executable: %s\n' "$llama_cli" >&2; exit 2; fi

results_dir=$(mktemp -d "${TMPDIR:-/tmp}/tether-placement-benchmark.XXXXXX")
printf 'Writing benchmark logs to %s\n' "$results_dir"

printf '\n== Solo: local GPU only ==\n'
"$llama_cli" -m "$model" -ngl 99 -c "$context" -n "$tokens" --single-turn -p "$prompt" 2>&1 | tee "$results_dir/solo.log"
printf '\n== Mesh: RPC endpoint(s) ==\n'
"$llama_cli" -m "$model" -ngl 99 -c "$context" -n "$tokens" --single-turn --rpc "$rpc" -p "$prompt" 2>&1 | tee "$results_dir/rpc.log"

printf '\n== Throughput lines (compare eval/decode tokens per second) ==\n'
grep -E 'eval time|prompt eval time|decode|tok/s|\[ Prompt:' "$results_dir/solo.log" || true
grep -E 'eval time|prompt eval time|decode|tok/s|\[ Prompt:' "$results_dir/rpc.log" || true
printf '\nKeep these logs with the model name, context, and node list when deciding whether to change placement policy.\n'
