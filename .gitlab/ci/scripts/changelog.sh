#!/bin/sh
# Reads CHANGELOG.md, whose "## v<version>" headings are the releases,
# newest first.
#
#   changelog.sh version      the newest release's tag
#   changelog.sh notes <tag>  that release's section, without its heading

set -e

file=CHANGELOG.md

case "$1" in
version)
	sed -n 's/^## \(v[^[:space:]]*\)[[:space:]]*$/\1/p' "$file" | head -n 1
	;;
notes)
	# the lines between the heading and the next heading of the same or a
	# higher level, without the blank lines around them. Trailing spaces on
	# the heading are ignored, as version ignores them.
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
	' "$file"
	;;
*)
	echo "usage: $0 version | notes <tag>" >&2
	exit 2
	;;
esac
