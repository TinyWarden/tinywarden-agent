# Instructions for LLM assistants

Read [CONTRIBUTING.md](CONTRIBUTING.md), the
[working guide](docs/agents/README.md) and the owning implementation/tests.
Human documentation starts at the
[TinyWarden documentation index](https://github.com/TinyWarden/tinywarden/blob/main/docs/README.md).

- Keep the host agent independent of app and skill source checkouts. Generic SDK
  operations serve multiple skills; avoid service/package-specific host branches.
- Preserve durable enrollment identity, replay, counters, queues, deadlines and
  compatibility negotiation. Recover uncertain operations without deleting state.
- Enforce bounded, read-only declared observation access. Do not weaken sandbox,
  process cleanup, resource budgets or runtime integrity checks.
- SDK assets are pinned and consumed by other repositories. Review runtime asset
  manifests, fixtures and affected consumers together; never silently change pins.
- Follow the package ownership in the working guide. Shared config, transport,
  state, release and observation code must not import daemon or installer owners.
  Official monitoring packages stay in the separate skills repository.
- Keep CLI copy in `internal/cli/en.json` and author-tool copy in
  `sdk/python/authoring/en.json`.
- Maintain source roles and the generated file map. Run focused affected tests and
  the required release gate before publication.
- Update human references with behavior changes and link public LLM guidance to the
  same facts. Keep secrets, local identities and private working memory out of Git.
