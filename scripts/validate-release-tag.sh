#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.[1-9][0-9]*)?$'

if [[ ! "${tag}" =~ ${pattern} ]]; then
  echo "release tag must use vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N (N >= 1)" >&2
  exit 1
fi
