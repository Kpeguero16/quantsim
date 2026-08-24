#!/usr/bin/env bash
#
# QuantSim -- build here, run there.
#
#   infra/deploy/deploy.sh ec2-user@quantsim.khalilpeguero.me
#
# Images are built on this machine and shipped as a tarball over ssh. They are
# NOT built on the instance: a t3.micro has 1GB of RAM and the Go toolchain
# compiling six services will meet it. They do not go through a registry
# either, because that needs a token to create and store for one box with no
# CI. See SPEC.md Step 26 §2.9 -- ghcr.io is the upgrade the moment CI exists.
#
# Nothing here is idempotent by accident: it rebuilds, re-ships and restarts
# every time. That is the right shape for one instance and a handful of
# deploys, and the wrong shape for anything more frequent.
set -euo pipefail

TARGET="${1:-}"
if [ -z "$TARGET" ]; then
    echo "usage: $0 user@host" >&2
    exit 2
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REMOTE_DIR="${REMOTE_DIR:-/opt/quantsim}"
BUNDLE="$(mktemp -t quantsim-images-XXXXXX).tar"

cleanup() { rm -f "$BUNDLE"; }
trap cleanup EXIT

cd "$REPO_ROOT"

echo "==> building images"
docker compose --profile app build

echo "==> saving images to $BUNDLE"
# Names, not ids, so `docker load` restores the tags compose looks for.
docker save -o "$BUNDLE" \
    quantsim-auth quantsim-market-data quantsim-trading-engine \
    quantsim-backtesting quantsim-ai-insights quantsim-gateway \
    quantsim-frontend

echo "==> shipping configuration"
ssh "$TARGET" "mkdir -p $REMOTE_DIR/infra"
# The migrations are a bind mount for the migrate one-shot, so they have to
# exist on the instance. The Dockerfiles do not: `up --no-build` below uses the
# images loaded from the tarball and never looks at a build context.
scp -q docker-compose.yml docker-compose.prod.yml "$TARGET:$REMOTE_DIR/"
rsync -az --delete infra/migrations "$TARGET:$REMOTE_DIR/infra/"

echo "==> loading images on the instance (this is the slow part)"
ssh "$TARGET" "docker load" < "$BUNDLE"

echo "==> starting"
# --no-build is load-bearing. Without it compose tries to build on the
# instance, which is what shipping a tarball exists to avoid.
ssh "$TARGET" "cd $REMOTE_DIR && docker compose -f docker-compose.yml -f docker-compose.prod.yml --profile app up -d --no-build"

echo "==> state"
ssh "$TARGET" "cd $REMOTE_DIR && docker compose -f docker-compose.yml -f docker-compose.prod.yml --profile app ps"

echo
echo "Done. If this was the first deploy, Caddy issues a certificate on the"
echo "first request to the domain -- give it a few seconds, then check:"
echo "  curl -sI https://\$DOMAIN/ | head -1"
