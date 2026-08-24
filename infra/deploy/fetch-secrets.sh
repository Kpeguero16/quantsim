#!/usr/bin/env bash
#
# QuantSim -- write .env on the instance from SSM Parameter Store.
#
# Run ON the instance, from $REMOTE_DIR:
#   sudo infra/deploy/fetch-secrets.sh
#
# The file this writes is a CACHE, not the source of truth. Every run rewrites
# it from SSM, so editing it by hand is a change that survives exactly until
# the next deploy. Put it in SSM instead.
#
# Standard parameters are free and SecureString uses the default KMS key, which
# is also free. Secrets Manager is the same thing at $0.40 per secret per
# month, which is more than this instance costs to run.
#
# The instance reads these through an IAM role, so there are no AWS keys on the
# box. That is the actual reason for using SSM rather than scp'ing a file: not
# encryption at rest, but that nothing long-lived has to sit on a machine
# exposed to the internet.
set -euo pipefail

PREFIX="${SSM_PREFIX:-/quantsim}"
OUT="${OUT:-/opt/quantsim/.env}"

command -v aws >/dev/null || { echo "aws CLI is not installed on this instance" >&2; exit 1; }

echo "==> reading $PREFIX from SSM"
# get-parameters-by-path paginates at 10 by default; --recursive picks up
# anything nested, and --with-decryption is what turns SecureString into a
# value rather than ciphertext.
JSON="$(aws ssm get-parameters-by-path \
    --path "$PREFIX" \
    --recursive \
    --with-decryption \
    --output json)"

COUNT="$(printf '%s' "$JSON" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["Parameters"]))')"
if [ "$COUNT" -eq 0 ]; then
    # Refuse rather than write an empty file. An empty .env starts a stack
    # that fails on the first missing variable, which is a confusing way to
    # learn that a parameter path was wrong.
    echo "no parameters under $PREFIX -- refusing to write an empty $OUT" >&2
    exit 1
fi

TMP="$(mktemp)"
chmod 600 "$TMP"
{
    echo "# Written by infra/deploy/fetch-secrets.sh from SSM $PREFIX."
    echo "# Do not edit: the next deploy overwrites this file."
    printf '%s' "$JSON" | python3 -c '
import json, sys
for p in sorted(json.load(sys.stdin)["Parameters"], key=lambda p: p["Name"]):
    name = p["Name"].rsplit("/", 1)[-1]
    value = p["Value"]
    # No quoting. Compose reads this file itself and treats quotes as part of
    # the value, so a quoted password becomes a wrong password with no error.
    print(f"{name}={value}")
'
} > "$TMP"

install -m 600 -o root -g root "$TMP" "$OUT"
rm -f "$TMP"
echo "==> wrote $COUNT parameters to $OUT (mode 600, root)"
