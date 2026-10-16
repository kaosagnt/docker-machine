#!/bin/sh
# release.sh <tag> <ref>: creates the GitLab release for tag, and the tag
# itself at ref when it doesn't exist yet. Does nothing when the release
# already exists. The notes are the tag's CHANGELOG.md section, and the
# assets link to the binaries the tag pipeline uploads to S3.
#
# Runs in the glab image with GLAB_ENABLE_CI_AUTOLOGIN set, so the release,
# and with it the tag, is created with the job token, as the user who started
# the job. A protected tag needs a user allowed to create it.

set -eu

tag=$1
ref=$2

if glab release view "${tag}" -R "${CI_PROJECT_PATH}" >/dev/null 2>&1; then
	echo "The ${tag} release already exists"
	exit 0
fi

notes=$(mktemp)
.gitlab/ci/scripts/changelog.sh notes "${tag}" >"${notes}"
if [ ! -s "${notes}" ]; then
	echo "New release of GitLab's fork of Docker Machine is out!" >"${notes}"
fi

links=$(jq -cn --arg base "${S3_DOWNLOADS_URL}/${tag}" '[
	["binary: macOS amd64", "docker-machine-Darwin-x86_64"],
	["binary: Linux amd64", "docker-machine-Linux-x86_64"],
	["binary: Linux arm", "docker-machine-Linux-armhf"],
	["binary: Linux arm64", "docker-machine-Linux-aarch64"],
	["binary: OpenBSD amd64", "docker-machine-OpenBSD-x86_64"],
	["binary: Windows amd64", "docker-machine-Windows-x86_64.exe"],
	["checksums", "release.sha256"],
	["checksums GPG signature", "release.sha256.asc"],
	["other release artifacts", "index.html"]
] | map({name: .[0], url: "\($base)/\(.[1])", direct_asset_path: "/\(.[1])", link_type: "other"})')

glab release create "${tag}" -R "${CI_PROJECT_PATH}" \
	--ref "${ref}" \
	--name "${tag}" \
	--tag-message "Version ${tag#v}" \
	--notes-file "${notes}" \
	--assets-links "${links}" \
	--no-update
