# Verification

One entry point: scripts/verify.sh --batch during work, --phase-end at phase
closeout. Both fail visibly. No app checkout, Node or PostgreSQL installation is
required. Go 1.27.1, Python 3 and Git are the source tooling.

For changes confined to the SDK author tools, their tests and documentation, use
the scoped profile `scripts/verify.sh --batch --skills-author`, or `--phase-end`
for its staged secret gate. It checks source/maps, unchanged runtime asset pins,
the static author-tool cases and isolated standalone author/collector cases.
Run it under a delegated native user unit (absolute checkout path required):

```sh
systemd-run --user --wait --pipe --collect \
  -p 'Delegate=cpu memory pids' -p DelegateSubgroup=supervisor \
  -p NoNewPrivileges=yes -p "WorkingDirectory=$PWD" \
  /bin/bash "$PWD/scripts/verify.sh" --phase-end --skills-author
```

Unavailable isolation fails explicitly. This profile has no Go build, web/database
work, dependency install or deployed-service restart. Reuse unchanged runtime,
dependency and installed-agent acceptance when the diff is confined to author
tools/docs. Runtime, Go/protocol, module, unit or packaging changes require their
normal relevant gates; the scoped profile cannot replace them. Default verification
also includes the static author cases. The authoring isolation cases join the
runtime conformance suite when releasing platform changes.

SDK v1 execution additionally requires Python 3.13, bubblewrap, libseccomp2 and
delegated native cgroup v2 limits. Run `tests/runtime/test_sandbox.py` under the
actual service account with the template's delegation and no-new-privileges.
The ten conformance cases fail when isolation is unavailable; do not count a
container or unsupported service configuration as installed-host acceptance.
Official-package and admission tests are included in the ordinary verification
entrypoint. For a runtime release, also verify real collection through server
interpretation and current contact under the installed app/agent units.

| Check | Command | Trigger |
| --- | --- | --- |
| Source sizes, protected paths and exhaustive ownership map | python3 scripts/source_check.py | Source additions/removals/changes. |
| Map regeneration | python3 scripts/source_check.py --write | Stage intended paths before regeneration; stage the generated map afterward. |
| Authoring metadata, archive and boundary checks | python3.13 -B -m unittest discover -s tests/runtime -p test_authoring.py | Author-tool changes; automated in both profiles. |
| Standalone SDK execution and real read-only collection | python3.13 -B -m unittest discover -s tests/runtime -p test_authoring_sandbox.py | Author-tool execution changes; automated in the delegated author profile, manual installed-host gate for runtime releases. |
| Formatting/static/tests/build | gofmt, go vet ./..., go test ./..., go build ./cmd/tinywarden-agent | Affected Go changes; complete suite at phase closeout. |
| Dependency integrity | go mod verify | Module changes and phase closeout; standard library only. |
| Service syntax | systemd-analyze verify infra/systemd/tinywarden-agent.service | Unit changes and phase closeout. |
| Vulnerability analysis | govulncheck ./... | Phase closeout only. |
| Exact staged secret scan | scripts/check-secrets.sh | Phase closeout before authorized publication. |
| Package identity | scripts/package.py with a new absolute output path; inspect adjacent hashes and archive | Packaging changes or a release candidate. |

The source-size gate applies to handwritten Go/Python/shell files: up to 300
physical lines ordinarily; over 300 requires a documented responsibility
review before acceptance; over 500 blocks material changes absent an approved
exact-path exception. Documentation/static JSON catalogs are excluded.
Authored roles live in scripts/map-roles.json; Git owns inventory.

CI uses the existing digest-pinned Debian 13 container with init, installs
native tools/scanners and runs the complete gate as an unprivileged account.
The workflow is manual only, with a verification label.
Dispatch after separately authorized publication of the exact intended commit.
Existing TLS/runner/queue/normalization tests use local synthetic fixtures.
Real disposable-host acceptance or an installed-agent upgrade remains a
separate operation; source-only documentation changes do not require either.
