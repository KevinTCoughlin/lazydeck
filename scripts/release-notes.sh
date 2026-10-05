#!/usr/bin/env bash
# Print the CHANGELOG.md entry for one version (without its heading), for use
# as GitHub release notes when a release is cut outside release-please (the
# manual workflow_dispatch path in .github/workflows/release.yml). Prints
# nothing, successfully, if the version has no entry -- e.g. a release
# candidate -- so GoReleaser falls back to its commit-based changelog.
#
#   scripts/release-notes.sh v0.2.0 [CHANGELOG.md]
set -euo pipefail

version="${1:?usage: release-notes.sh VERSION [CHANGELOG]}"
version="${version#v}"
changelog="${2:-CHANGELOG.md}"

awk -v version="${version}" '
  /^## \[/ {
    if (in_entry) exit
    if (index($0, "## [" version "]") == 1) { in_entry = 1; next }
  }
  in_entry { print }
' "${changelog}" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}'
