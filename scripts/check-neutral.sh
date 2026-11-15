#!/bin/sh
# This script fails when a file name, staged text, or a commit message since the base ref names a private project.
# It skips itself and binary files, and a missing base ref fails only when CI is set.
set -eu

word="kny""ta"
# The base ref defaults to origin/main.
base="${1:-origin/main}"
self="scripts/check-neutral.sh"
hint="factory is a general tool. Describe the general need instead of a private project."
fails=0

# die MESSAGE stops the check, because a failed git command must never read as a pass.
die() {
	echo "check-neutral: $1" >&2
	exit 1
}

top=$(git rev-parse --show-toplevel) || die "git rev-parse failed, so nothing was checked"
cd "$top"

# report KIND LIST prints one error for each non-empty line of LIST.
report() {
	while IFS= read -r item; do
		[ -n "$item" ] || continue
		echo "check-neutral: $1 $item names a private project. $hint" >&2
		fails=$((fails + 1))
	done <<EOF2
$2
EOF2
}

listing=$(git ls-files --cached) || die "git ls-files failed, so nothing was checked"
names=$(printf '%s\n' "$listing" | grep -i -e "$word" || true)
report "the file name" "$names"

# git grep exits 1 when nothing matches, which is a pass. Any higher code is a failure.
status=0
raw=$(git grep --cached -I -i -l -z -e "$word" -- . ":!$self") || status=$?
[ "$status" -le 1 ] || die "git grep failed with exit $status, so nothing was checked"
contents=$(printf '%s' "$raw" | tr '\0' '\n')
report "the file" "$contents"

if git rev-parse --verify --quiet "$base" >/dev/null; then
	commits=$(git rev-list "$base..HEAD") || die "git rev-list failed, so the commit messages were not checked"
	for commit in $commits; do
		if git log -1 --format=%B "$commit" | grep -qi -e "$word"; then
			report "the commit" "$commit"
		fi
	done
elif [ -n "${CI:-}" ]; then
	echo "check-neutral: base ref $base not found, so CI cannot check the commit messages. Fetch the full history." >&2
	exit 1
else
	echo "check-neutral: warning: base ref $base not found, so the commit messages were not checked" >&2
fi

if [ "$fails" -ne 0 ]; then
	exit 1
fi
