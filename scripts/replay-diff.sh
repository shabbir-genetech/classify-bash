#!/usr/bin/env bash
# Per-record replay equality: classify every logged fall-through with the tree
# at REV and with the working copy, and list each record whose decision changed.
# Run from the repo root inside `nix develop`:
#
#   CORPUS=/path/corpus.jsonl scripts/replay-diff.sh master
#
# A refactor must change nothing; a whitelist change should change only the
# records it targets, and only towards allow. Exit 1 if any record went from
# allow to fall-through (a lost acceleration), so a regression cannot scroll by.
# REV must contain triage_test.go's replayHost (68dfcbf or later); this script
# injects TestReplayDecisions into a REV that predates it.
set -euo pipefail
rev=${1:?usage: CORPUS=... scripts/replay-diff.sh REV}
: "${CORPUS:?set CORPUS to a journalctl -t classify-bash -o cat dump}"
repo=$(pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The REV tree, exported file by file (this is a jj repo; there is no git index).
mkdir -p "$work/old"
jj file list -r "$rev" | while IFS= read -r f; do
  mkdir -p "$work/old/$(dirname "$f")"
  jj file show -r "$rev" "$f" >"$work/old/$f"
done
if ! grep -q 'func TestReplayDecisions' "$work/old/internal/engine/triage_test.go"; then
  awk '/^func TestReplayDecisions/,/^}/' internal/engine/triage_test.go \
    >>"$work/old/internal/engine/triage_test.go"
fi

for tree in "$work/old" "$repo"; do
  name=$([[ $tree == "$repo" ]] && echo new || echo old)
  (cd "$tree" && DECISIONS="$work/$name.txt" go test -count=1 -run TestReplayDecisions ./internal/engine/ >/dev/null)
done

changed=$(diff "$work/old.txt" "$work/new.txt" | grep '^>' || true)
total=$(wc -l <"$work/new.txt")
toallow=$(printf '%s\n' "$changed" | awk '$3 == 1' | grep -c . || true)
tofall=$(printf '%s\n' "$changed" | awk '$3 == 0' | grep -c . || true)
echo "records: $total   now allow: $toallow   now fall through: $tofall"
printf '%s\n' "$changed" | awk 'NF {print $2 + 1, $3}' | while read -r line dec; do
  cmd=$(sed -n "${line}p" "$CORPUS" | jq -r '.command' | tr '\n' ' ' | cut -c1-160)
  printf '%s  line %-6s %s\n' "$([[ $dec == 1 ]] && echo '+allow' || echo '-ALLOW LOST')" "$line" "$cmd"
done
[[ $tofall -eq 0 ]]
