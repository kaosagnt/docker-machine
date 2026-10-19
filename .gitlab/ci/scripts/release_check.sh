#!/usr/bin/env bash
# Checks that CHANGELOG.md's newest release and version/version.go agree,
# and works out whether that release is still to be made: its tag doesn't
# exist yet. Writes release/tag, empty when there's nothing to release, for
# the release job.

set -eo pipefail

# vX.Y.Z-gitlab.N
release_pattern='^v[0-9]+\.[0-9]+\.[0-9]+-gitlab\.[0-9]+$'

tag=$(.gitlab/ci/scripts/changelog.sh version)
if [[ -z "${tag}" ]]; then
  echo "CHANGELOG.md has no '## v<version>' release heading"
  exit 1
fi
if ! grep -qE "${release_pattern}" <<<"${tag}"; then
  echo "CHANGELOG.md's newest release heading is '## ${tag}', but it must be '## vX.Y.Z-gitlab.N' with nothing after the version. Release candidates are tagged by hand and have no section."
  exit 1
fi

version=$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)"[[:space:]]*$/\1/p' version/version.go)
if [[ "${tag}" != "v${version}" ]]; then
  echo "CHANGELOG.md's newest release is ${tag}, but version/version.go has Version = \"${version}\"; they must match"
  exit 1
fi

notes=$(.gitlab/ci/scripts/changelog.sh notes "${tag}")
if [[ -z "${notes}" ]]; then
  echo "CHANGELOG.md's ${tag} section is empty"
  exit 1
fi

mkdir -p release
tags=$(git ls-remote --tags --refs origin 'v*' | sed 's|.*refs/tags/||')
if grep -qxF "${tag}" <<<"${tags}"; then
  # A change added to the section after it was tagged would ship in no
  # release. Releases tagged before CHANGELOG.md described them have no
  # section to compare with.
  git fetch --quiet --no-tags --depth=1 origin "refs/tags/${tag}"
  released=$({ git show FETCH_HEAD:CHANGELOG.md 2>/dev/null || true; } | .gitlab/ci/scripts/changelog.sh notes "${tag}" -)
  if [[ -n "${released}" && "${notes}" != "${released}" ]]; then
    echo "CHANGELOG.md's ${tag} section has changed since ${tag} was released, so the change isn't in any release:"
    diff <(echo "${released}") <(echo "${notes}") || true
    echo "Put it in a new section at the top instead, and set Version in version/version.go to match."
    exit 1
  fi

  echo "${tag} is already released"
  : >release/tag
  exit 0
fi

newest=$({ grep -E "${release_pattern}" <<<"${tags}" || true; } |
  sed 's/^v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)-gitlab\.\([0-9]*\)$/\1 \2 \3 \4 &/' |
  sort -n -k1,1 -k2,2 -k3,3 -k4,4 | tail -n 1 | cut -d' ' -f5)
if [[ -n "${newest}" ]]; then
  later=$(printf '%s\n%s\n' "${newest}" "${tag}" |
    sed 's/^v\([0-9]*\)\.\([0-9]*\)\.\([0-9]*\)-gitlab\.\([0-9]*\)$/\1 \2 \3 \4 &/' |
    sort -n -k1,1 -k2,2 -k3,3 -k4,4 | tail -n 1 | cut -d' ' -f5)
  if [[ "${later}" != "${tag}" ]]; then
    echo "CHANGELOG.md's newest release, ${tag}, isn't newer than the newest tag, ${newest}"
    exit 1
  fi
fi

echo "${tag}" >release/tag
echo "${tag} isn't tagged yet: the next scheduled pipeline on ${CI_DEFAULT_BRANCH:-the default branch} releases it"
