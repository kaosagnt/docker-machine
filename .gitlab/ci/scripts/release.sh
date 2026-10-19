#!/bin/sh
# release.sh <tag> <ref> [--assets]: creates the GitLab release for tag when
# it doesn't exist yet, and with it the tag itself at ref when that doesn't
# exist yet either. The notes are the tag's CHANGELOG.md section. With
# --assets, it then adds whichever links to the binaries the tag pipeline
# uploads to S3 the release doesn't have yet, so it's only given after the
# upload.
#
# Runs in the glab image with GLAB_ENABLE_CI_AUTOLOGIN set, so the release,
# and with it the tag, is created with the job token, as the user who started
# the job. The job token can only create a tag by creating its release, and
# a protected tag needs a user allowed to create it.

set -eu

tag=$1
ref=$2
assets=${3:-}

if glab release view "${tag}" -R "${CI_PROJECT_PATH}" >/dev/null 2>&1; then
	echo "The ${tag} release exists"
else
	notes=$(mktemp)
	.gitlab/ci/scripts/changelog.sh notes "${tag}" >"${notes}"
	if [ ! -s "${notes}" ]; then
		echo "New release of GitLab's fork of Docker Machine is out!" >"${notes}"
	fi

	glab release create "${tag}" -R "${CI_PROJECT_PATH}" \
		--ref "${ref}" \
		--name "${tag}" \
		--tag-message "Version ${tag#v}" \
		--notes-file "${notes}" \
		--no-update
fi

if [ "${assets}" != "--assets" ]; then
	exit 0
fi

have=$(glab release view "${tag}" -R "${CI_PROJECT_PATH}" --output json | jq -c '[(.assets.links // [])[].name]')
links=$(jq -cn --arg base "${S3_DOWNLOADS_URL}/${tag}" --argjson have "${have}" '[
	["binary: macOS amd64", "docker-machine-Darwin-x86_64"],
	["binary: Linux amd64", "docker-machine-Linux-x86_64"],
	["binary: Linux arm", "docker-machine-Linux-armhf"],
	["binary: Linux arm64", "docker-machine-Linux-aarch64"],
	["binary: OpenBSD amd64", "docker-machine-OpenBSD-x86_64"],
	["binary: Windows amd64", "docker-machine-Windows-x86_64.exe"],
	["checksums", "release.sha256"],
	["checksums GPG signature", "release.sha256.asc"],
	["other release artifacts", "index.html"]
] | map(select(.[0] as $name | any($have[]; . == $name) | not))
  | map({name: .[0], url: "\($base)/\(.[1])", direct_asset_path: "/\(.[1])", link_type: "other"})')

if [ "${links}" = "[]" ]; then
	echo "The ${tag} release has all its asset links"
	exit 0
fi

glab release upload "${tag}" -R "${CI_PROJECT_PATH}" --assets-links "${links}"
