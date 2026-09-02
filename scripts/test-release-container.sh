#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
# Use the production image and architecture, without installing test runtimes in it.
image="$(awk '/id: deploy$/ { deploy = 1; next } deploy && /name:/ { print $2; exit }' cloudbuild.release.yaml)"
if [[ -z "$image" ]]; then
  echo "cloudbuild.release.yaml must define the deploy image." >&2
  exit 1
fi
temporary_directory="$(mktemp -d "$root/.release-container.XXXXXX")"
trap 'rm -rf "$temporary_directory"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./deploy -o "$temporary_directory/deploy.test"

# No credentials, Docker socket, or network enter the container. Only gcloud and
# curl are substituted by the release tests; the deployment runtime is real.
docker run --rm --platform linux/amd64 --network none --read-only \
  --tmpfs /tmp:exec \
  --volume "$root/deploy:$root/deploy:ro" \
  --volume "$temporary_directory:/tests:ro" \
  --workdir "$root" \
  --entrypoint /bin/bash \
  "$image" \
  -ec 'gcloud version; curl --version; exec /tests/deploy.test "$@"' -- \
  -test.v -test.run '^Test(ProductionRelease|ProductionRuntimeVerifier|Destructive)' -test.count=1
