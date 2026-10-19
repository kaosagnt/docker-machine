#!/bin/sh
# Reads CHANGELOG.md, or the given file, whose "## v<version>" headings are
# the releases, newest first.
#
#   changelog.sh version [file]      the newest release's heading, without
#                                    "## "
#   changelog.sh notes <tag> [file]  that release's section, without its
#                                    heading
#
# Trailing spaces on a heading are ignored.

set -e

case "$1" in
version)
	sed -n 's/^## \(v.*[^[:space:]]\)[[:space:]]*$/\1/p' "${2:-CHANGELOG.md}" | head -n 1
	;;
notes)
	# the lines between the heading and the next heading of the same or a
	# higher level, without the blank lines around them
	awk -v heading="## $2" '
		{ line = $0; sub(/[[:space:]]+$/, "", line) }
		line == heading { on = 1; next }
		on && (/^# / || /^## /) { exit }
		on && /^[[:space:]]*$/ { if (seen) blank++; next }
		on {
			for (; blank > 0; blank--) print ""
			print
			seen = 1
		}
	' "${3:-CHANGELOG.md}"
	;;
*)
	echo "usage: $0 version [file] | notes <tag> [file]" >&2
	exit 2
	;;
esac
