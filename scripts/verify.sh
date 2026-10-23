#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mode=${1:---batch}
case "$mode" in --batch|--phase-end) ;; *) printf 'Usage: %s [--batch|--phase-end]\n' "$0" >&2; exit 2;; esac
scope=${2:---all}
case "$scope" in --all|--skills-author|--sandbox) ;; *) printf 'Unknown verification scope: %s\n' "$scope" >&2; exit 2;; esac
[[ $# -le 2 ]] || exit 2
export GOTOOLCHAIN=local
export PATH="$HOME/.local/bin:$PATH"
python3 scripts/checks/source_check.py
python3.13 -B -m unittest discover -s tests/tooling -p 'test_*.py'
python3 -B scripts/checks/runtime_assets.py --check
python3.13 -B -m unittest discover -s sdk/python/tests -p 'test_*.py'
python3.13 -B -m unittest discover -s tests/integration -p test_security_boundaries.py
if [[ "$scope" == --skills-author ]]; then
  # Run this profile under delegated native cgroup limits, as documented.
  python3.13 -B -m unittest discover -s tests/integration -p test_authoring_sandbox.py
  if [[ "$mode" == --phase-end ]]; then ./scripts/checks/check-secrets.sh; fi
  printf 'Verification passed: %s %s\n' "$mode" "$scope"
  exit 0
fi
test -z "$(gofmt -l cmd internal)"
go vet ./...
go test ./...
mkdir -p bin
go build -o bin/tinywarden-agent ./cmd/tinywarden-agent
go mod verify
if [[ "$scope" == --sandbox ]]; then
  python3.13 -B -m unittest discover -s tests/integration -p test_authoring_sandbox.py
  python3.13 -B -m unittest discover -s tests/integration -p test_sandbox.py
fi
if [[ "$mode" == --phase-end ]]; then
  systemd-analyze verify deploy/systemd/manual/tinywarden-agent.service
  govulncheck ./...
  ./scripts/checks/check-secrets.sh
fi
printf 'Verification passed: %s\n' "$mode"
