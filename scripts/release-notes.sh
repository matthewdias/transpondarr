#!/bin/sh
# Print the CHANGELOG.md section for a version (accepts "v0.1.0" or "0.1.0").
# Fails if the section is missing or empty, so it doubles as the release gate's
# tag-matches-changelog check.
set -eu

ver="${1#v}"

notes="$(awk -v ver="$ver" '
  $0 ~ "^## \\[" ver "\\]" { in_section = 1; next }
  /^## /                   { in_section = 0 }
  /^\[[^]]*\]: /           { next }  # link-reference definitions
  in_section               { print }
' CHANGELOG.md)"

if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
  echo "error: CHANGELOG.md has no (non-empty) section for version $ver" >&2
  echo "add a \"## [$ver]\" section before tagging v$ver" >&2
  exit 1
fi

printf '%s\n' "$notes"
