#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: bin/start-local.sh

Start the local new-api service from the repository root.

Environment:
  GO_VERSION   Optional gvm Go version, for example: go1.24.6
  GOCACHE      Optional Go build cache path. Defaults to ./.gocache

Notes:
  - The Go application loads .env itself.
  - If .env is missing, the service starts with built-in defaults.
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
cd "${repo_root}"

if [[ -f "${HOME}/.gvm/scripts/gvm" ]]; then
  # gvm scripts reference optional shell variables such as ZSH_VERSION and
  # GVM_DEBUG directly, and may return non-zero under errexit in some shells.
  set +eu
  # shellcheck source=/dev/null
  source "${HOME}/.gvm/scripts/gvm"
  if [[ -n "${GO_VERSION:-}" ]]; then
    gvm use "${GO_VERSION}" >/dev/null
  fi
  set -euo pipefail
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go command not found. Install Go or set GO_VERSION after installing gvm." >&2
  exit 1
fi

if [[ ! -f ".env" ]]; then
  echo "warning: .env not found; using application defaults" >&2
fi

export GOCACHE="${GOCACHE:-${repo_root}/.gocache}"
mkdir -p "${GOCACHE}"

echo "Starting new-api from ${repo_root}"
echo "Go: $(go version)"
echo "GOCACHE: ${GOCACHE}"
echo "Config: ${repo_root}/.env"

exec go run main.go
