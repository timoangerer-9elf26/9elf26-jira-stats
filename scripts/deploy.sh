#!/usr/bin/env bash
# Single-step redeploy for jira-stats on the internal-tooling EC2 instance.
#
# Builds the arm64 release binary, ships it to the instance via S3 + a
# presigned URL (no SSH — delivery runs over SSM), and restarts the systemd
# unit. The SQLite store is a rebuildable projection, so the app re-syncs from
# Jira on restart; there is no data migration step.
#
# Prereqs: run from the repo root with AWS credentials for the Org *management*
# account (the script assumes OrganizationAccountAccessRole into the tooling
# account). Requires: go, aws CLI v2, base64.
#
# Usage:  ./scripts/deploy.sh
set -euo pipefail

# --- deployment constants (internal-tooling account) ---
ACCOUNT_ID=214519213070
REGION=eu-central-1
INSTANCE_ID=i-0220fc1a6bee863d6
BUCKET=jira-stats-artifacts-214519213070
ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/OrganizationAccountAccessRole"
BINARY=bin/jira-stats-linux-arm64

cd "$(dirname "$0")/.."

echo "==> building arm64 release binary"
make build-arm64

echo "==> assuming role into tooling account (${ACCOUNT_ID})"
creds=$(aws sts assume-role --role-arn "$ROLE_ARN" --role-session-name deploy \
  --query 'Credentials.[AccessKeyId,SecretAccessKey,SessionToken]' --output text)
export AWS_ACCESS_KEY_ID=$(echo "$creds" | cut -f1)
export AWS_SECRET_ACCESS_KEY=$(echo "$creds" | cut -f2)
export AWS_SESSION_TOKEN=$(echo "$creds" | cut -f3)
export AWS_DEFAULT_REGION=$REGION

echo "==> uploading binary to s3://${BUCKET}"
aws s3 cp "$BINARY" "s3://${BUCKET}/jira-stats-linux-arm64" >/dev/null
url=$(aws s3 presign "s3://${BUCKET}/jira-stats-linux-arm64" --expires-in 600)

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
  --comment "redeploy jira-stats" \
  --parameters "commands=[\"echo ${b64} | base64 -d | bash\"]" \
  --query 'Command.CommandId' --output text)

for _ in $(seq 1 30); do
  st=$(aws ssm get-command-invocation --command-id "$cmd" --instance-id "$INSTANCE_ID" \
    --query 'Status' --output text 2>/dev/null || true)
  [ "$st" = "Success" ] || [ "$st" = "Failed" ] && break
  sleep 3
done

echo "==> result: $st"
aws ssm get-command-invocation --command-id "$cmd" --instance-id "$INSTANCE_ID" \
  --query 'StandardOutputContent' --output text
[ "$st" = "Success" ] || { echo "DEPLOY FAILED"; exit 1; }
echo "==> deployed. https://jira-stats.in.9elf26.ai"
