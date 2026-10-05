#!/usr/bin/env bash
# Point the Homebrew tap's formula (KevinTCoughlin/homebrew-lazydeck,
# lazydeck.rb) at a new release: rewrites the `version` line, every release
# asset `url`, and the `sha256` that follows each url, taking checksums from
# that release's checksums.txt. The rest of the hand-maintained formula
# (install layout, service block, tests) is left untouched.
#
#   scripts/bump-homebrew-formula.sh FORMULA VERSION CHECKSUMS
set -euo pipefail

formula="${1:?usage: bump-homebrew-formula.sh FORMULA VERSION CHECKSUMS}"
version="${2:?usage: bump-homebrew-formula.sh FORMULA VERSION CHECKSUMS}"
checksums="${3:?usage: bump-homebrew-formula.sh FORMULA VERSION CHECKSUMS}"
version="${version#v}"

if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "bump-homebrew-formula: refusing non-stable version ${version@Q}" >&2
  exit 1
fi

tmp="$(mktemp)"
trap 'rm -f -- "${tmp}"' EXIT

awk -v version="${version}" -v checksums="${checksums}" '
  BEGIN {
    while ((getline line < checksums) > 0) {
      split(line, field, /[[:space:]]+\*?/)
      sum[field[2]] = field[1]
    }
  }
  /^  version "/ {
    sub(/"[^"]*"/, "\"" version "\"")
    print
    next
  }
  /^ *url "https:\/\/github.com\/[^"]*\/releases\/download\// {
    sub(/\/download\/v[^\/]+\//, "/download/v" version "/")
    sub(/lazydeck_[^_]+_/, "lazydeck_" version "_")
    match($0, /lazydeck_[^"\/]+\.tar\.gz/)
    asset = substr($0, RSTART, RLENGTH)
    if (!(asset in sum)) {
      printf "bump-homebrew-formula: %s is absent from checksums\n", asset > "/dev/stderr"
      failed = 1
      exit 1
    }
    expect_sha = sum[asset]
    print
    next
  }
  expect_sha != "" && /^ *sha256 "/ {
    sub(/"[0-9a-f]*"/, "\"" expect_sha "\"")
    expect_sha = ""
    rewritten++
    print
    next
  }
  { print }
  END {
    if (failed) exit 1
    if (rewritten == 0) {
      print "bump-homebrew-formula: no release url/sha256 pairs found" > "/dev/stderr"
      exit 1
    }
  }
' "${formula}" > "${tmp}"

cat -- "${tmp}" > "${formula}"
