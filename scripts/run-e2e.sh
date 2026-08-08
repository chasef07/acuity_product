#!/usr/bin/env sh
set -eu

: "${E2E_DATABASE_URL:?required and must name a disposable database ending in _e2e}"

database_name="$(psql "$E2E_DATABASE_URL" -Atqc 'select current_database()')"
case "$database_name" in
  *_e2e) ;;
  *)
    echo "refusing to reset non-E2E database: $database_name" >&2
    exit 1
    ;;
esac

root="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
runtime_dir="$(mktemp -d)"
replacement_realtime_pid_file="$runtime_dir/realtime-replacement.pid"
web_pid=""
portal_pid=""
realtime_pid=""
provider_pid=""
worker_pid=""
telnyx_pid=""
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    for log in portal realtime provider worker web telnyx; do
      if [ -f "$runtime_dir/$log.log" ]; then
        echo "----- $log.log -----" >&2
        tail -80 "$runtime_dir/$log.log" >&2
      fi
    done
  fi
  if [ -f "$replacement_realtime_pid_file" ]; then
    replacement_realtime_pid="$(cat "$replacement_realtime_pid_file")"
    case "$replacement_realtime_pid" in
      *[!0-9]*|"") ;;
      *) kill "$replacement_realtime_pid" 2>/dev/null || true ;;
    esac
  fi
  for pid in "$worker_pid" "$provider_pid" "$realtime_pid" "$portal_pid" "$web_pid" "$telnyx_pid"; do
    if [ -n "$pid" ]; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  rm -rf "$runtime_dir"
}
trap cleanup EXIT INT TERM

psql "$E2E_DATABASE_URL" -v ON_ERROR_STOP=1 -q <<'SQL'
DROP SCHEMA IF EXISTS auth CASCADE;
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
DO $$
DECLARE
  role_name text;
BEGIN
  FOREACH role_name IN ARRAY ARRAY[
    'acuity_auth',
    'acuity_migrate',
    'acuity_portal',
    'acuity_provider',
    'acuity_realtime',
    'acuity_worker'
  ]
  LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name) THEN
      EXECUTE format('CREATE ROLE %I NOLOGIN', role_name);
    END IF;
  END LOOP;
END
$$;
SQL

cd "$root"
go build -o "$runtime_dir/acuity" ./backend/cmd/acuity
ACUITY_RUNTIME_ROLE=migrate \
DATABASE_URL="$E2E_DATABASE_URL" \
DATABASE_POOL_MAX=2 \
DATABASE_ACQUIRE_TIMEOUT_MS=5000 \
PROVISIONING_INPUT="$root/config/development-provisioning.json" \
PROVISIONING_OUTPUT="$runtime_dir/provisioned.json" \
"$runtime_dir/acuity"
practice_id="$(psql "$E2E_DATABASE_URL" -Atqc \
  "select id from access_practices where provisioning_key = 'abita-eye-group'")"

TELNYX_FIXTURE_PUBLIC_KEY_OUTPUT="$runtime_dir/telnyx-public-key" \
node "$root/scripts/telnyx-api-fixture.mjs" >"$runtime_dir/telnyx.log" 2>&1 &
telnyx_pid=$!
attempts=0
until [ -s "$runtime_dir/telnyx-public-key" ]; do
  attempts=$((attempts + 1))
  if [ "$attempts" -ge 50 ]; then
    echo "Telnyx fixture did not publish its webhook key" >&2
    exit 1
  fi
  sleep 0.1
done

cd "$root/web"
NEXT_PUBLIC_PORTAL_API_URL=http://127.0.0.1:18080 \
NEXT_PUBLIC_REALTIME_URL=http://127.0.0.1:18081 \
pnpm build

PORT=13000 \
HOSTNAME=127.0.0.1 \
AUTH_DATABASE_URL="$E2E_DATABASE_URL" \
AUTH_DB_POOL_MAX=3 \
AUTH_DB_ACQUIRE_TIMEOUT_MS=1500 \
BETTER_AUTH_URL=http://127.0.0.1:13000 \
BETTER_AUTH_SECRET=local-e2e-secret-that-is-at-least-32-characters \
BETTER_AUTH_TRUSTED_ORIGINS=http://127.0.0.1:13000 \
PORTAL_API_INTERNAL_URL=http://127.0.0.1:18080 \
PORTAL_API_AUDIENCE=http://127.0.0.1:18080 \
AUTH_EMAIL_MODE=test \
AUTH_ALLOW_TEST_EMAIL=true \
GOOGLE_CLIENT_ID=e2e-google-client \
GOOGLE_CLIENT_SECRET=e2e-google-secret \
NEXT_PUBLIC_PORTAL_API_URL=http://127.0.0.1:18080 \
NEXT_PUBLIC_REALTIME_URL=http://127.0.0.1:18081 \
pnpm start >"$runtime_dir/web.log" 2>&1 &
web_pid=$!

cd "$root"
ACUITY_RUNTIME_ROLE=portal-api \
DATABASE_URL="$E2E_DATABASE_URL" \
DATABASE_POOL_MAX=4 \
DATABASE_ACQUIRE_TIMEOUT_MS=1500 \
HTTP_PORT=18080 \
BROWSER_ORIGIN=http://127.0.0.1:13000 \
BETTER_AUTH_JWKS_URL=http://127.0.0.1:13000/api/auth/jwks \
BETTER_AUTH_ISSUER=http://127.0.0.1:13000 \
PORTAL_API_AUDIENCE=http://127.0.0.1:18080 \
HUMAN_CALLING_SIP_DOMAIN=synthetic.sip.telnyx.com \
HUMAN_CALLING_STAFF_SIP_DOMAIN=sip.telnyx.com \
HUMAN_CALLING_HANDOFF_TOKEN_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= \
HUMAN_CALLING_PLAYBACK_SIGNING_KEY=YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODk= \
HUMAN_CALLING_RING_WINDOW_SECONDS=20 \
HUMAN_CALLING_LEASE_SECONDS=30 \
HUMAN_CALLING_READINESS_GRACE_SECONDS=15 \
HANDOFF_SERVICE_TOKEN=synthetic-service-token \
HANDOFF_SERVICE_SUBJECT=abita-synthetic \
HANDOFF_SERVICE_PRACTICE_ID="$practice_id" \
TELNYX_API_KEY=KEY_e2e \
TELNYX_API_BASE_URL=http://127.0.0.1:19000/v2 \
TELNYX_CALL_CONTROL_ID=fixture-call-control \
TELNYX_CREDENTIAL_CONNECTION_ID=fixture-credential-connection \
TELNYX_FROM_NUMBER=+15555550100 \
TELNYX_RINGBACK_URL=https://assets.example.test/ringback.wav \
MESSAGING_WEBHOOK_BASE_URL=https://messaging.e2e.invalid/v1/provider/telnyx/messaging-webhooks \
MESSAGING_ATTACHMENT_DIRECTORY="$runtime_dir/messaging-attachments" \
"$runtime_dir/acuity" >"$runtime_dir/portal.log" 2>&1 &
portal_pid=$!

ACUITY_RUNTIME_ROLE=realtime \
DATABASE_URL="$E2E_DATABASE_URL" \
DATABASE_POOL_MAX=3 \
DATABASE_ACQUIRE_TIMEOUT_MS=1500 \
HTTP_PORT=18081 \
BROWSER_ORIGIN=http://127.0.0.1:13000 \
BETTER_AUTH_JWKS_URL=http://127.0.0.1:13000/api/auth/jwks \
BETTER_AUTH_ISSUER=http://127.0.0.1:13000 \
PORTAL_API_AUDIENCE=http://127.0.0.1:18080 \
REALTIME_HEARTBEAT_SECONDS=2 \
REALTIME_STREAM_SECONDS=30 \
REALTIME_STREAM_JITTER_SECONDS=5 \
REALTIME_REVALIDATE_SECONDS=2 \
REALTIME_RECONNECT_MIN_MS=100 \
REALTIME_RECONNECT_MAX_SECONDS=2 \
"$runtime_dir/acuity" >"$runtime_dir/realtime.log" 2>&1 &
realtime_pid=$!

ACUITY_RUNTIME_ROLE=provider-ingress \
DATABASE_URL="$E2E_DATABASE_URL" \
DATABASE_POOL_MAX=2 \
DATABASE_ACQUIRE_TIMEOUT_MS=1500 \
HTTP_PORT=18082 \
TELNYX_WEBHOOK_PUBLIC_KEY="$(cat "$runtime_dir/telnyx-public-key")" \
MESSAGING_ATTACHMENT_DIRECTORY="$runtime_dir/messaging-attachments" \
MESSAGING_MEDIA_SIGNING_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= \
"$runtime_dir/acuity" >"$runtime_dir/provider.log" 2>&1 &
provider_pid=$!

ACUITY_RUNTIME_ROLE=worker \
DATABASE_URL="$E2E_DATABASE_URL" \
DATABASE_POOL_MAX=2 \
DATABASE_ACQUIRE_TIMEOUT_MS=1500 \
HUMAN_CALLING_SIP_DOMAIN=synthetic.sip.telnyx.com \
HUMAN_CALLING_STAFF_SIP_DOMAIN=sip.telnyx.com \
HUMAN_CALLING_HANDOFF_TOKEN_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= \
HUMAN_CALLING_PLAYBACK_SIGNING_KEY=YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODk= \
TELNYX_API_KEY=KEY_e2e \
TELNYX_API_BASE_URL=http://127.0.0.1:19000/v2 \
TELNYX_CALL_CONTROL_ID=fixture-call-control \
TELNYX_CREDENTIAL_CONNECTION_ID=fixture-credential-connection \
TELNYX_FROM_NUMBER=+15555550100 \
TELNYX_RINGBACK_URL=https://assets.example.test/ringback.wav \
MESSAGING_WEBHOOK_BASE_URL=https://messaging.e2e.invalid/v1/provider/telnyx/messaging-webhooks \
MESSAGING_ATTACHMENT_DIRECTORY="$runtime_dir/messaging-attachments" \
MESSAGING_MEDIA_PUBLIC_BASE_URL=https://media.e2e.invalid/v1/provider/messaging-media \
MESSAGING_MEDIA_SIGNING_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY= \
HUMAN_CALLING_RING_WINDOW_SECONDS=20 \
HUMAN_CALLING_LEASE_SECONDS=30 \
HUMAN_CALLING_READINESS_GRACE_SECONDS=15 \
"$runtime_dir/acuity" >"$runtime_dir/worker.log" 2>&1 &
worker_pid=$!

wait_for() {
  url="$1"
  attempts=0
  until curl -fsS "$url" >/dev/null; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 50 ]; then
      echo "runtime did not become ready: $url" >&2
      return 1
    fi
    sleep 0.2
  done
}

wait_for http://127.0.0.1:13000/sign-in
wait_for http://127.0.0.1:19000/health
wait_for http://127.0.0.1:18080/health/ready
wait_for http://127.0.0.1:18081/health/ready
wait_for http://127.0.0.1:18082/health/ready
kill -0 "$worker_pid"

cd "$root/web"
E2E_PROVISIONING_OUTPUT="$runtime_dir/provisioned.json" \
E2E_BASE_URL=http://127.0.0.1:13000 \
E2E_PORTAL_API_URL=http://127.0.0.1:18080 \
E2E_REALTIME_URL=http://127.0.0.1:18081 \
E2E_REALTIME_PID="$realtime_pid" \
E2E_REALTIME_REPLACEMENT_PID_FILE="$replacement_realtime_pid_file" \
E2E_RUNTIME_BINARY="$runtime_dir/acuity" \
E2E_DATABASE_URL="$E2E_DATABASE_URL" \
E2E_TELNYX_FIXTURE_URL=http://127.0.0.1:19000 \
pnpm exec playwright test --project=chromium "$@"
