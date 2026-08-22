#!/usr/bin/env bash
#
# Push helper for the @claude workflow.
#
# The workflow's allowlist deliberately does NOT contain `git push`. Prefix rules
# cannot express "force-push this branch but never main": `-f` evades a
# `--force` rule, `+HEAD:main` force-pushes with no flag at all, and
# `--force-with-lease` has to stay permitted for the rebase case. Allowlisting
# this script instead means the push target comes from the checked-out branch
# rather than from anything the model composed.
#
# Backstop, not the only control: branch protection on `main` rejects a force
# push server-side regardless of what reaches the wire.
set -euo pipefail

branch="$(git rev-parse --abbrev-ref HEAD)"

# Detached HEAD resolves to the literal "HEAD" - refuse rather than guess a target.
case "$branch" in
main | master | HEAD | "")
	echo "claude-push: refusing to push to '${branch}'" >&2
	exit 1
	;;
esac

echo "claude-push: pushing HEAD to origin/${branch}" >&2
git push --force-with-lease origin "HEAD:refs/heads/${branch}"
