#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

set -euo pipefail

before_diff="$(mktemp)"
after_diff="$(mktemp)"
before_untracked="$(mktemp)"
after_untracked="$(mktemp)"
trap 'rm -f "$before_diff" "$after_diff" "$before_untracked" "$after_untracked"' EXIT

git diff --binary -- . ':!docs' >"$before_diff"
git ls-files --others --exclude-standard -- ':!docs' | sort >"$before_untracked"

go generate ./...
${PNPM:-pnpm} --filter @openbox/web generate >/dev/null

git diff --binary -- . ':!docs' >"$after_diff"
git ls-files --others --exclude-standard -- ':!docs' | sort >"$after_untracked"

if ! cmp -s "$before_diff" "$after_diff" || ! cmp -s "$before_untracked" "$after_untracked"; then
  echo "generated files are stale; regenerate them and include the result" >&2
  exit 1
fi
