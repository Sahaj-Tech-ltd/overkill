#!/usr/bin/env bash
# Fresh, uniquely named Compose stack; cleanup cannot affect other projects.
set -euo pipefail
cd "$(dirname "$0")/.."
export COMPOSE_PROJECT_NAME="overkill-smoke-$$"
export OVERKILL_POSTGRES_PORT=0
export OVERKILL_POSTGRES_PASSWORD
OVERKILL_POSTGRES_PASSWORD=$(openssl rand -hex 24)
export OVERKILL_API_TOKEN
OVERKILL_API_TOKEN=$(openssl rand -hex 32)
container="${COMPOSE_PROJECT_NAME}-api"
cleanup() {
    docker rm -f "$container" >/dev/null 2>&1 || true
    docker compose down --volumes --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker compose config --quiet
docker compose build overkill
docker compose up -d --wait postgres
docker compose run --rm --no-deps overkill --version
docker compose run --rm --no-deps --entrypoint sh overkill -ec '
    test "$(id -u)" != 0
    node --version
    node --input-type=module -e "await import(\"/usr/local/share/overkill/tui/dist/app.js\")"
    python -c "import server, overkill_pb2, overkill_pb2_grpc, grpc_health, psycopg2, pgvector"
    overkill config init
    overkill config validate
'
docker compose run --rm --no-deps --entrypoint python overkill \
    /usr/local/share/overkill/verify-packaged-tui.py --binary /usr/local/bin/overkill
docker compose run -d --no-deps --name "$container" -e OVERKILL_API_TOKEN overkill serve --addr 127.0.0.1:7777
ready=false
for attempt in {1..30}; do
    if docker exec "$container" python -c 'import os, urllib.request; request=urllib.request.Request("http://127.0.0.1:7777/health", headers={"Authorization": "Bearer " + os.environ["OVERKILL_API_TOKEN"]}); assert urllib.request.urlopen(request, timeout=1).status == 200' >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep 1
done
if ! "$ready"; then docker logs "$container"; echo 'Docker backend failed readiness' >&2; exit 1; fi
docker exec "$container" python -c 'import urllib.request, urllib.error
try:
    urllib.request.urlopen("http://127.0.0.1:7777/health", timeout=2)
    raise AssertionError("Unauthenticated health request was accepted")
except urllib.error.HTTPError as error:
    assert error.code == 401'
printf 'Docker image and fresh PostgreSQL-backed startup PASS\n'
