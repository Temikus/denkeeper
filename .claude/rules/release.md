---
name: release
description: Release pipeline and released-PR version stamping.
paths:
  - .github/**
  - .goreleaser.yml
---

# Release invariants

- **Released-PR stamping** (`stamp-prs` in `release.yml`): comments the version on each released PR; resolves commits→PRs via the GitHub API (`commits/{sha}/pulls`), not `(#N)` subject parsing (mixed merge strategies break parsing).
