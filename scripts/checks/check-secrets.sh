#!/usr/bin/env bash
set -euo pipefail
# Scan exactly the staged publication snapshot, never ignored local environments.
target=${1:-.}
snapshot=$(mktemp -d)
trap 'rm -rf -- "$snapshot"' EXIT
git -C "$target" checkout-index --all --prefix="$snapshot/"
gitleaks dir --redact --no-banner "$snapshot"
