#!/usr/bin/env bash
# Block commits that contain gitignored paths, including ones force-added
# with "git add -f" (--no-index makes check-ignore disregard the index).
set -euo pipefail

ignored=$(git diff --cached --name-only --diff-filter=ACMR -z \
  | git check-ignore --no-index --stdin -z \
  | tr '\0' '\n') || true

if [ -n "$ignored" ]; then
  echo "commit blocked: staged paths are gitignored" >&2
  while IFS= read -r path; do
    [ -n "$path" ] && echo "  $path" >&2
  done <<<"$ignored"
  echo "unstage them (git rm --cached -- <path>) or drop the ignore rule" >&2
  exit 1
fi
