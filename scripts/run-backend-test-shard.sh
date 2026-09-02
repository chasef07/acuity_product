#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

case "${1:-}" in
  calling)
    packages=(
      ./backend/internal/humancalling
    )
    ;;
  domain)
    packages=(
      ./backend/internal/access
      ./backend/internal/httpapi
      ./backend/internal/migrations
      ./backend/internal/realtime
    )
    ;;
  support)
    packages=(
      ./backend/cmd/acuity
      ./backend/cmd/backlog-recovery
      ./backend/cmd/receipt-audit
      ./backend/internal/api
      ./backend/internal/app
      ./backend/internal/authn
      ./backend/internal/interaction
      ./backend/internal/messaging
      ./backend/internal/observability
      ./backend/internal/postgres
      ./backend/internal/testaccess
      ./backend/internal/testdb
      ./backend/internal/work
      ./backend/internal/worker
      ./backend/internal/workspace
      ./deploy
    )
    ;;
  *)
    echo "usage: $0 {calling|domain|support} [--list]" >&2
    exit 2
    ;;
esac

case "${2:-}" in
  "")
    go test -p 1 "${packages[@]}" -count=1
    ;;
  --list)
    go list "${packages[@]}"
    ;;
  *)
    echo "usage: $0 {calling|domain|support} [--list]" >&2
    exit 2
    ;;
esac
