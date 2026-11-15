#!/bin/sh
# This script tests check-neutral.sh.
# Each case builds a small git repository and checks the exit code and the output.
set -eu

script="$(cd "$(dirname "$0")" && pwd)/check-neutral.sh"
word="kny""ta"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fails=0
out=""

# new_repo DIR creates a repository with one clean commit and a base branch.
new_repo() {
	git init -q "$1"
	git -C "$1" config user.name tester
	git -C "$1" config user.email tester@example.com
	git -C "$1" config commit.gpgsign false
	echo "clean" >"$1/a.txt"
	git -C "$1" add a.txt
	git -C "$1" commit -q -m "init"
	git -C "$1" branch base
}

# run DIR [BASE] sets code and out from the script. CI is unset unless the case sets it.
run() {
	if out=$(cd "$1" && env -u CI ${CASE_ENV:-} sh "$script" "${2:-base}" 2>&1); then
		code=0
	else
		code=1
	fi
}

fail() {
	echo "check-neutral-test: FAIL: $1" >&2
	fails=$((fails + 1))
}

# expect DESC WANT_CODE [WANT_TEXT] checks the last run.
expect() {
	if [ "$code" -ne "$2" ]; then
		fail "$1 (exit $code, want $2)"
	fi
	if [ -n "${3:-}" ] && ! printf '%s\n' "$out" | grep -qF -e "$3"; then
		fail "$1 (output lacks \"$3\": $out)"
	fi
}

test_clean_tree_passes() {
	new_repo "$tmp/clean"
	run "$tmp/clean"
	expect "a clean tree passes" 0
}

test_staged_content_fails() {
	new_repo "$tmp/content"
	echo "see $word for details" >"$tmp/content/b.txt"
	git -C "$tmp/content" add b.txt
	run "$tmp/content"
	expect "a staged file with the word fails" 1 "b.txt"
}

test_working_tree_only_is_ignored() {
	new_repo "$tmp/worktree"
	echo "see $word" >>"$tmp/worktree/a.txt"
	run "$tmp/worktree"
	expect "text only in the working tree passes" 0
}

test_upper_case_fails() {
	new_repo "$tmp/upper"
	echo "see $(echo "$word" | tr '[:lower:]' '[:upper:]')" >"$tmp/upper/b.txt"
	git -C "$tmp/upper" add b.txt
	run "$tmp/upper"
	expect "the word in capitals fails" 1 "b.txt"
}

test_file_name_fails() {
	new_repo "$tmp/name"
	echo "clean" >"$tmp/name/notes-$word.txt"
	git -C "$tmp/name" add .
	run "$tmp/name"
	expect "a file name with the word fails" 1 "notes-$word.txt"
}

test_file_name_with_spaces_counts_once() {
	new_repo "$tmp/spaces"
	echo "see $word" >"$tmp/spaces/my notes.txt"
	git -C "$tmp/spaces" add .
	run "$tmp/spaces"
	expect "a file name with spaces fails" 1 "my notes.txt"
	count=$(printf '%s\n' "$out" | grep -c "names a private project" || true)
	if [ "$count" -ne 1 ]; then
		fail "a file name with spaces counts once (got $count errors: $out)"
	fi
}

test_script_may_name_the_word() {
	new_repo "$tmp/self"
	mkdir "$tmp/self/scripts"
	echo "word=$word" >"$tmp/self/scripts/check-neutral.sh"
	git -C "$tmp/self" add scripts
	run "$tmp/self"
	expect "the script itself may name the word" 0
}

test_bad_commit_fails() {
	new_repo "$tmp/commit"
	echo "more" >>"$tmp/commit/a.txt"
	git -C "$tmp/commit" commit -q -am "fix: handle $word"
	run "$tmp/commit"
	expect "a commit message with the word fails" 1 "$(git -C "$tmp/commit" rev-parse HEAD)"
}

test_clean_commit_passes() {
	new_repo "$tmp/okcommit"
	echo "more" >>"$tmp/okcommit/a.txt"
	git -C "$tmp/okcommit" commit -q -am "fix: handle the general case"
	run "$tmp/okcommit"
	expect "a clean commit message passes" 0
}

test_older_bad_commit_in_range_fails() {
	new_repo "$tmp/range"
	echo "one" >>"$tmp/range/a.txt"
	git -C "$tmp/range" commit -q -am "fix: handle $word"
	old=$(git -C "$tmp/range" rev-parse HEAD)
	echo "two" >>"$tmp/range/a.txt"
	git -C "$tmp/range" commit -q -am "fix: handle the second case"
	echo "three" >>"$tmp/range/a.txt"
	git -C "$tmp/range" commit -q -am "fix: handle the third case"
	run "$tmp/range"
	expect "only the older bad commit fails" 1 "$old"
	if [ "$(printf '%s\n' "$out" | grep -c "names a private project" || true)" -ne 1 ]; then
		fail "only the older bad commit is named ($out)"
	fi
}

test_missing_base_fails_in_ci() {
	new_repo "$tmp/ci"
	CASE_ENV="CI=1" run "$tmp/ci" missing-ref
	expect "a missing base ref with CI set fails" 1 "missing-ref"
}

test_missing_base_warns_locally() {
	new_repo "$tmp/local"
	run "$tmp/local" missing-ref
	expect "a missing base ref without CI passes with a warning" 0 "warning"
}

test_git_failure_fails_closed() {
	new_repo "$tmp/broken"
	CASE_ENV="GIT_DIR=$tmp/nonexistent" run "$tmp/broken"
	expect "a broken GIT_DIR fails" 1 "nothing was checked"
}

test_outside_a_repo_fails_closed() {
	mkdir "$tmp/plain"
	run "$tmp/plain"
	expect "a directory outside a repo fails" 1 "nothing was checked"
}

for t in $(sed -n 's/^\(test_[a-z_]*\)() {$/\1/p' "$0"); do
	"$t"
done

if [ "$fails" -ne 0 ]; then
	exit 1
fi
echo "check-neutral-test: ok"
