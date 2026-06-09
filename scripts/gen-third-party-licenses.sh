#!/usr/bin/env bash
# Regenerate THIRD_PARTY_LICENSES from the module build graph.
#
# Run inside `nix develop` (needs go + go-licenses). It reproduces, in full, the
# license of every third-party Go module linked into the classify-bash binary —
# what MIT/BSD require when redistributing compiled binaries. Our own module is
# excluded (its license is LICENSE).
set -euo pipefail
cd "$(dirname "$0")/.."

self=$(go list -m)
out=THIRD_PARTY_LICENSES

{
  echo "Third-party licenses"
  echo "===================="
  echo
  echo "classify-bash links the Go modules below into its binary. Their licenses"
  echo "are reproduced in full, as required when redistributing compiled binaries."
  echo "This file is generated — regenerate with scripts/gen-third-party-licenses.sh."
  echo
} >"$out"

go-licenses report ./... 2>/dev/null | sort | while IFS=, read -r module _url license; do
  [ "$module" = "$self" ] && continue
  dir=$(go list -m -f '{{.Dir}}' "$module" 2>/dev/null) || continue
  lf=$(find "$dir" -maxdepth 1 -iname 'LICENSE*' | head -1)
  if [ -z "$lf" ]; then
    echo "WARNING: no LICENSE file found for $module" >&2
    continue
  fi
  {
    echo "================================================================================"
    echo "$module  ($license)"
    echo "================================================================================"
    echo
    cat "$lf"
    echo
  } >>"$out"
done

echo "wrote $out"
