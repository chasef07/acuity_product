#!/usr/bin/env sh
set -eu

: "${AUTH_SCHEMA_CHECK_DATABASE_URL:?required and must name a disposable database ending in _schema_check}"

database_name="$(
  psql "$AUTH_SCHEMA_CHECK_DATABASE_URL" -Atqc 'select current_database()'
)"
case "$database_name" in
  *_schema_check) ;;
  *)
    echo "refusing to reset non-schema-check database: $database_name" >&2
    exit 1
    ;;
esac

psql "$AUTH_SCHEMA_CHECK_DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
DROP SCHEMA IF EXISTS auth CASCADE;
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
CREATE SCHEMA auth;
SQL

generated="$(mktemp)"
expected="$(mktemp)"
normalized="$(mktemp)"
cleanup() {
  rm -f "$generated" "$expected" "$normalized"
}
trap cleanup EXIT

(
  cd web
  PATH="${NODE_BIN_PATH:-$PATH}" \
  AUTH_DATABASE_URL="$AUTH_SCHEMA_CHECK_DATABASE_URL" \
  AUTH_DB_POOL_MAX=2 \
  AUTH_DB_ACQUIRE_TIMEOUT_MS=1000 \
  BETTER_AUTH_URL=http://127.0.0.1:13000 \
  BETTER_AUTH_SECRET=auth-schema-check-secret-at-least-32-characters \
  BETTER_AUTH_TRUSTED_ORIGINS=http://127.0.0.1:13000 \
  PORTAL_API_INTERNAL_URL=http://127.0.0.1:18080 \
  PORTAL_API_AUDIENCE=http://127.0.0.1:18080 \
  GOOGLE_CLIENT_ID=auth-schema-google-client \
  GOOGLE_CLIENT_SECRET=auth-schema-google-secret \
  npx --yes auth@1.6.25 generate \
    --config src/lib/auth-cli.ts \
    --output "$generated" \
    --yes
)

tail -n +4 backend/internal/migrations/sql/0003_better_auth.sql > "$expected"
awk '{ print }' "$generated" > "$normalized"
diff -u "$expected" "$normalized"
