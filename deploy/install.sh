#!/bin/bash
# Download completely before running this installer. It never installs prerequisites.
set -euo pipefail
umask 077
if [[ "$(id -u)" != 0 ]]; then
  echo "Run this installer as root." >&2
  exit 1
fi
missing=()
for tool in curl openssl python3.13 tar sha256sum; do
  command -v "$tool" >/dev/null || missing+=("$tool")
done
if (( ${#missing[@]} )); then
  echo "Missing prerequisites: ${missing[*]}" >&2
  echo "On Debian 13: apt install ca-certificates curl openssl python3.13 tar coreutils bubblewrap libseccomp2 systemd" >&2
  echo "Install the missing packages yourself, then rerun." >&2
  exit 1
fi
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
fetch() {
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 5 --max-time 90 --max-filesize "$3" --output "$2" "$1"
}
repo=https://github.com/TinyWarden/tinywarden-agent/releases/download
fetch 'https://api.github.com/repos/TinyWarden/tinywarden-agent/releases?per_page=100' "$work/releases.json" 2097152
version=$(python3.13 -I - "$work/releases.json" <<'PYCODE'
import json, re, sys
with open(sys.argv[1]) as handle:
    releases=json.load(handle)
if not isinstance(releases,list) or len(releases)>100:
    raise SystemExit("Invalid release catalogue")
versions=[r["tag_name"][1:] for r in releases if not r.get("draft") and not r.get("prerelease")
          and re.fullmatch(r"v(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})",r.get("tag_name",""))]
if not versions:
    raise SystemExit("No supported stable agent release is available")
print(max(versions,key=lambda v:tuple(map(int,v.split(".")))))
PYCODE
)
fetch "$repo/v$version/release.json" "$work/release.json" 16384
fetch "$repo/v$version/release.json.sig" "$work/release.json.sig" 256
cat > "$work/key.pem" <<'PUBLICKEY'
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEApYjVpkuPwkZLKjGFZ4AKNopcHgEFV2CIrvhME1Gl048=
-----END PUBLIC KEY-----
PUBLICKEY
python3.13 -I - "$work/release.json.sig" "$work/signature" <<'PYCODE'
import base64,sys
with open(sys.argv[1]) as handle:
    data=base64.b64decode(handle.read().strip(),validate=True)
if len(data)!=64:
    raise SystemExit("Invalid release signature")
with open(sys.argv[2],"wb") as handle:
    handle.write(data)
PYCODE
openssl pkeyutl -verify -pubin -inkey "$work/key.pem" -rawin -in "$work/release.json" -sigfile "$work/signature" >/dev/null
python3.13 -I - "$work/release.json" "$version" "$work" <<'PYCODE'
import json,re,sys
from pathlib import Path
m=json.loads(Path(sys.argv[1]).read_text())
version=sys.argv[2]
name="tinywarden-agent-"+version+"-linux-amd64.tar.gz"
if (m.get("repository"),m.get("tag"),m.get("version"),m.get("os"),m.get("os_version"),m.get("architecture")) != (
        "TinyWarden/tinywarden-agent","v"+version,version,"debian","13","amd64"):
    raise SystemExit("This release is not for Debian 13 x64")
a=m["asset"]
if a["name"]!=name or not isinstance(a["size"],int) or not 0<a["size"]<=67108864:
    raise SystemExit("Invalid release archive")
for key in [a["sha256"],m["binary_sha256"]]:
    if not re.fullmatch(r"[a-f0-9]{64}",key):
        raise SystemExit("Invalid release identity")
Path(sys.argv[3],"asset").write_text(name)
Path(sys.argv[3],"hash").write_text(a["sha256"])
Path(sys.argv[3],"binaryhash").write_text(m["binary_sha256"])
PYCODE
asset=$(cat "$work/asset")
fetch "$repo/v$version/$asset" "$work/bundle.tar.gz" 67108864
printf '%s  %s\n' "$(cat "$work/hash")" "$work/bundle.tar.gz" | sha256sum --check --status
# Extract only the authenticated executable. Its installer validates the full archive independently.
tar -xOzf "$work/bundle.tar.gz" tinywarden-agent > "$work/tinywarden-agent"
printf '%s  %s\n' "$(cat "$work/binaryhash")" "$work/tinywarden-agent" | sha256sum --check --status
chmod 0700 "$work/tinywarden-agent"
"$work/tinywarden-agent" install --archive "$work/bundle.tar.gz" --manifest "$work/release.json" \
  --signature "$work/release.json.sig" "$@"
