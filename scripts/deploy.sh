#!/usr/bin/env bash
set -euo pipefail

# Deploy a PREBUILT, released jira-stats artifact to the production instance and
# restart the service (docs/adr/0006). This script is BUILD-ONCE: it never
# compiles — it ships the exact bytes it is handed. It powers two paths:
#
#   1. CI deploy job (.github/workflows/ci.yml): AWS credentials are already in
#      the environment (minted via GitHub OIDC), so the assume-role step is
#      skipped. The workflow downloads the just-released Release asset and passes
#      it here by path + tag.
#   2. Manual break-glass: run it from a laptop with no AWS creds in the env; it
#      assumes OrganizationAccountAccessRole (the broad human/manual path) and
#      deploys the artifact you hand it.
#
# Usage:
#   scripts/deploy.sh <tag> [binary-path]
#
#   <tag>          CalVer release tag, e.g. v2026.07.23.160. Used to key the S3
#                  object (releases/<tag>/...) so a prior tag can be redeployed
#                  for rollback (#156). The OIDC policy allows any key under the
#                  bucket.
#   [binary-path]  Path to the prebuilt linux/arm64 binary. Defaults to
#                  bin/jira-stats-linux-arm64. For a manual deploy of a tag you
#                  don't have locally, download it first:
#                    gh release download <tag> --pattern jira-stats-linux-arm64 \
#                      -O bin/jira-stats-linux-arm64
#
# The post-deploy /version health check lives in the CI workflow (it compares
# the public /version to the exact stamped version string, tag+sha). This script
# confirms the unit is active after restart; the workflow proves the new bytes
# are actually serving.

ACCOUNT_ID=214519213070
REGION=eu-central-1
INSTANCE_ID=i-0220fc1a6bee863d6
BUCKET=jira-stats-artifacts-214519213070
ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/OrganizationAccountAccessRole"

TAG="${1:-}"
BINARY="${2:-bin/jira-stats-linux-arm64}"

if [ -z "$TAG" ]; then
  echo "usage: $0 <tag> [binary-path]" >&2
  exit 2
fi

cd "$(dirname "$0")/.."

if [ ! -f "$BINARY" ]; then
  echo "artifact not found: $BINARY" >&2
  echo "(build-once: this script does not compile — supply a prebuilt binary)" >&2
  exit 1
fi

# Assume the broad OrganizationAccountAccessRole ONLY for the manual path, i.e.
# when no AWS credentials are already present. In CI the OIDC step has already
# exported short-lived creds for the narrowly-scoped deploy role, and this
# management-account role is neither available nor wanted there.
if [ -z "${AWS_ACCESS_KEY_ID:-}" ] && [ -z "${AWS_SESSION_TOKEN:-}" ]; then
  echo "==> no AWS creds in env — assuming role into tooling account (${ACCOUNT_ID})"
  creds=$(aws sts assume-role --role-arn "$ROLE_ARN" --role-session-name deploy \
    --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)
  AWS_ACCESS_KEY_ID=$(echo "$creds" | cut -f1)
  AWS_SECRET_ACCESS_KEY=$(echo "$creds" | cut -f2)
  AWS_SESSION_TOKEN=$(echo "$creds" | cut -f3)
  export AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
  export AWS_DEFAULT_REGION=$REGION
else
  echo "==> AWS creds already in env — skipping assume-role (CI/OIDC path)"
  export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-$REGION}"
fi

# Key the object by tag so #156 can redeploy a prior tag byte-for-byte.
S3_KEY="releases/${TAG}/jira-stats-linux-arm64"

echo "==> uploading ${BINARY} to s3://${BUCKET}/${S3_KEY}"
aws s3 cp "$BINARY" "s3://${BUCKET}/${S3_KEY}" >/dev/null
url=$(aws s3 presign "s3://${BUCKET}/${S3_KEY}" --expires-in 600)

echo "==> installing on ${INSTANCE_ID} + restarting unit (via SSM)"
remote='set -euo pipefail
curl -fsSL "'"$url"'" -o /opt/jira-stats/jira-stats.new
chmod 0755 /opt/jira-stats/jira-stats.new
mv -f /opt/jira-stats/jira-stats.new /opt/jira-stats/jira-stats
systemctl restart jira-stats
sleep 3
systemctl is-active jira-stats'
b64=$(printf '%s' "$remote" | base64 | tr -d '\n')

cmd=$(aws ssm send-command --instance-ids "$INSTANCE_ID" --document-name AWS-RunShellScript \
  --comment "deploy jira-stats ${TAG}" \
  --parameters "commands=[\"echo ${b64} | base64 -d | bash\"]" \
  --query 'Command.CommandId' --output text)

st=""
for _ in $(seq 1 30); do
  st=$(aws ssm get-command-invocation --command-id "$cmd" --instance-id "$INSTANCE_ID" \
    --query 'Status' --output text 2>/dev/null || true)
  { [ "$st" = "Success" ] || [ "$st" = "Failed" ]; } && break
  sleep 3
done

echo "==> result: $st"
aws ssm get-command-invocation --command-id "$cmd" --instance-id "$INSTANCE_ID" \
  --query 'StandardOutputContent' --output text
[ "$st" = "Success" ] || { echo "DEPLOY FAILED"; exit 1; }
echo "==> deployed ${TAG}. https://jira-stats.in.9elf26.ai"
