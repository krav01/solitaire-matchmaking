#!/usr/bin/env bash
set -euo pipefail

module="github.com/krav01/solitaire-matchmaking"

check_domain_package() {
  local pkg="$1"
  local deps
  deps="$(go list -deps -f '{{.ImportPath}}' "${pkg}/...")"

  if grep -Eq "^${module}/internal(/|$)" <<<"${deps}"; then
    echo "architecture violation: ${pkg} depends on internal packages" >&2
    return 1
  fi

  if grep -Eq '^(net/http|database/sql|github\.com/jackc/pgx)' <<<"${deps}"; then
    echo "architecture violation: ${pkg} depends on transport or persistence packages" >&2
    return 1
  fi
}

check_domain_package "${module}/pkg/rating"
check_domain_package "${module}/pkg/matchmaking"

echo "architecture checks passed"
