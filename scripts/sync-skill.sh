#!/bin/sh
# Vendors the spun skill from the pinned tag of github.com/spun-ink/plugins into the file the
# binary embeds. The skill is edited there, never here. To ship a new skill, bump tag in
# skill.pin and run this script.
#
#   scripts/sync-skill.sh           download the pinned file over cmd/spun/skill/SKILL.md
#   scripts/sync-skill.sh --check   fail if the embedded file differs from the pin; changes nothing
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
pin="$root/skill.pin"
target="$root/cmd/spun/skill/SKILL.md"

pin_value() {
	sed -n "s/^$1=//p" "$pin"
}

repo=$(pin_value repo)
tag=$(pin_value tag)
path=$(pin_value path)
if [ -z "$repo" ] || [ -z "$tag" ] || [ -z "$path" ]; then
	echo "skill.pin must set repo, tag and path" >&2
	exit 2
fi
url="https://raw.githubusercontent.com/$repo/$tag/$path"

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -fsSL "$url" -o "$tmp"

case "${1:-}" in
"")
	cp "$tmp" "$target"
	echo "cmd/spun/skill/SKILL.md now equals $repo@$tag:$path"
	;;
--check)
	if ! cmp -s "$tmp" "$target"; then
		echo "cmd/spun/skill/SKILL.md differs from the pin in skill.pin ($repo@$tag:$path): run scripts/sync-skill.sh" >&2
		exit 1
	fi
	;;
*)
	echo "usage: scripts/sync-skill.sh [--check]" >&2
	exit 2
	;;
esac
