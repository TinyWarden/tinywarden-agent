# Publishing agent releases

Each stable release ships one complete platform. Update `internal/platform/version.go`
for every executable or trusted runtime change, and regenerate the runtime artifact
when SDK files change. Skills have their own independent package versions.

## Prepare and verify

Run the agent phase gate and app protocol tests before signing. The release builder
requires the exact committed source to match the staged tree and working files.
Use a new output directory and a protected Ed25519 private key outside the repository:

```sh
python3 scripts/release.py --output /absolute/release-output \
  --signing-key /protected/release-key.pem --source-commit FULL_COMMIT_HASH \
  --source-min 0.0.8 --source-max 0.0.11
```

Choose the supported source range deliberately. It declares state compatibility;
it is not a marketing version range. The builder checks the key against the pinned
public key, builds a static linux-amd64 executable, checks its version, packages all
trusted runtime files and units, and signs the exact UTF-8 manifest bytes. Never
edit `release.json` after signing. Keep all five outputs together: archive,
`release.json`, `release.json.sig`, `install.sh`, and `SHA256SUMS`.

## GitHub publication

The manual `release.yml` workflow builds the selected version tag in Debian 13.
Use the protected GitHub environment `agent-release` with required maintainers.
Its secret `AGENT_RELEASE_SIGNING_KEY` contains the private PEM key. The key is
written only to a protected temporary file and removed after signing. Do not expose
it to pull-request workflows, logs, repository files or skill authors.

Enable immutable releases in `TinyWarden/tinywarden-agent` before the first stable
publication. The script creates a **draft**, uploads all five assets, and leaves
final publication to the maintainer. Verify the tag, exact commit, checksums and
signed identity before publishing the draft. Do not replace stable release assets;
fixes require a new version. See [GitHub release management](https://docs.github.com/en/repositories/releasing-projects-on-github/managing-releases-in-a-repository).

The app discovers numeric stable versions, verifies their signatures and caches
compatibility metadata every six hours. GitHub's `latest` flag is used only to locate
the initial bootstrap; it does not decide installed update ordering or trust.

## Key custody and failure

Back up the private key in a protected offline location. The same public key must
remain pinned in the agent verifier, bootstrap and app verifier. Changing a key
requires a separately trusted software distribution; an untrusted manifest cannot
replace it. If authenticity or compatibility checks fail, preserve the installation
and its state. Disable automatic updates while investigating a compromised key.

Release publication cannot prove runtime health. The root updater waits for a
fresh report from the actual non-root monitoring process with matching executable,
runtime and descriptor hashes. Unconfirmed updates remain visible for recovery.
