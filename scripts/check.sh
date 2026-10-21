#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
if [[ $# -gt 1 || ( $# -eq 1 && "$1" != --browser ) ]]; then
  echo "Usage: scripts/check.sh [--browser]" >&2
  exit 2
fi
# Explicit project/file selection keeps development containers and volumes out
# of every lifecycle command. Stopping this test DB discards only its tmpfs data.
compose_test=(docker compose --project-name multiworks-stage9 --file "$repo_dir/docker-compose.test.yml")
cleanup() { "${compose_test[@]}" --profile browser stop test-frontend test-api test-db >/dev/null 2>&1 || true; }
trap cleanup EXIT
"${compose_test[@]}" config --quiet
"${compose_test[@]}" up -d --wait test-db
stage9_port="${TEST_DB_PORT:-55432}"
if [[ ! "$stage9_port" =~ ^[0-9]+$ ]] || (( stage9_port < 1024 || stage9_port > 65535 )); then
  echo "TEST_DB_PORT must be between 1024 and 65535" >&2; exit 2
fi
export OPS_TEST_DATABASE_URL="postgres://multiworks_test:multiworks_test_password@127.0.0.1:${stage9_port}/multiworks_test?sslmode=disable"
export REQUIRE_INTEGRATION_TESTS=1
cd "$repo_dir/backend"
go run ./tests/testsupport/dbsetup
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...
cd "$repo_dir/frontend"
npm test
cd "$repo_dir"
docker compose config --quiet
if [[ "${1:-}" == --browser ]]; then
  COMPOSE_BAKE=false "${compose_test[@]}" --profile browser up --build -d --wait
  node scripts/browser-stage9.mjs
fi
