#!/usr/bin/env bash
# Re-apply the source-controlled deploy-role policy to the live IAM role, so
# reconciling drift is one step instead of copy-pasting the put-role-policy
# invocation out of deploy/aws/README.md (issue #174, docs/adr/0006).
#
# The recurring failure this fixes is pure state drift: deploy-role-policy.json
# on `main` is correct, but the live jira-stats-github-deploy role isn't
# re-applied after edits. Applying still needs admin IAM creds in the tooling
# account (by design CI cannot touch IAM), so a human runs this; it just removes
# the copy-paste toil and the validation gap.
#
# Before ANY AWS call, a local guard validates the policy JSON parses and carries
# every expected Sid — so a malformed or truncated edit is caught offline (no AWS
# credentials required). Run just the guard with --validate-only.
#
# Requires: jq always; awscli + tooling-account IAM creds only for the apply.
set -euo pipefail

ROLE_NAME=jira-stats-github-deploy
POLICY_NAME=jira-stats-deploy
REGION=eu-central-1

# Resolve the policy file relative to this script so it works from any cwd.
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
POLICY_FILE="$SCRIPT_DIR/../deploy/aws/deploy-role-policy.json"

# The Sids the deploy role must grant (kept in lockstep with the workflow the
# role serves: S3 artifact read/write + list/delete retention pruning, #156, and
# SSM SendCommand + GetCommandInvocation to the one instance). A drift or a fat-
# fingered edit that drops one is caught here, offline, before we touch IAM.
EXPECTED_SIDS=(
  ArtifactBucketReadWrite
  ArtifactBucketRetentionDelete
  ArtifactBucketListForRetention
  SendDeployCommandToTheOneInstance
  ReadCommandResults
)

command -v jq >/dev/null 2>&1 || { echo "error: jq is required" >&2; exit 1; }

validate_policy() {
  [ -f "$POLICY_FILE" ] || { echo "error: policy file not found: $POLICY_FILE" >&2; exit 1; }

  # Must be well-formed JSON (catches a malformed / truncated edit).
  if ! jq empty "$POLICY_FILE" 2>/dev/null; then
    echo "error: $POLICY_FILE is not valid JSON" >&2
    exit 1
  fi

  # Every expected Sid must be present.
  local present missing=()
  present=$(jq -r '[.Statement[].Sid] | @tsv' "$POLICY_FILE")
  for sid in "${EXPECTED_SIDS[@]}"; do
    printf '%s\n' "$present" | tr '\t' '\n' | grep -qxF "$sid" || missing+=("$sid")
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    echo "error: $POLICY_FILE is missing expected Sid(s): ${missing[*]}" >&2
    exit 1
  fi

  echo "guard: $POLICY_FILE parses and carries all ${#EXPECTED_SIDS[@]} expected Sids"
}

validate_policy

if [ "${1:-}" = "--validate-only" ]; then
  exit 0
fi

echo "==> applying $POLICY_FILE to role $ROLE_NAME (inline policy $POLICY_NAME) in $REGION"
aws iam put-role-policy \
  --role-name "$ROLE_NAME" \
  --policy-name "$POLICY_NAME" \
  --policy-document "file://$POLICY_FILE" \
  --region "$REGION"
echo "==> applied. Verify with: gh workflow run \"OIDC verify\" --ref main"
