#!/usr/bin/env bash
# Retention pruning (docs/adr/0006, issue #156). After a successful deploy, keep
# only the most recent KEEP (=5) heavy release artifacts in BOTH stores:
#
#   * GitHub Release binary assets (the ~19 MB linux/arm64 binary), and
#   * the S3 artifact binaries under releases/<tag>/.
#
# Git tags are kept FOREVER — they are the cheap, complete provenance record and
# an older build can always be rebuilt from its tag. Only the heavy binaries are
# pruned; the GitHub Release object + its tag survive (we delete just the asset).
#
# Idempotent and safe to re-run: it only ever deletes assets/objects that fall
# outside the newest-KEEP window, and no-ops when a target is already gone.
#
# Env (all defaulted so it is runnable/inspectable outside CI):
#   KEEP    how many of the newest artifacts to retain           (default 5)
#   BUCKET  the S3 artifact bucket                                (default prod)
#   GITHUB_REPOSITORY  owner/name for gh                          (default repo)
#
# Requires: gh (authenticated) and awscli (credentials in env). In CI both are
# already configured by the deploy job.
set -euo pipefail

KEEP="${KEEP:-5}"
BUCKET="${BUCKET:-jira-stats-artifacts-214519213070}"
REPO="${GITHUB_REPOSITORY:-timoangerer-9elf26/9elf26-jira-stats}"
ASSET="jira-stats-linux-arm64"

echo "==> retention: keeping the newest ${KEEP} releases; git tags untouched"

# The GitHub Release list, ordered newest-first by RELEASE CREATION TIME, is the
# single source of truth for "the most recent N releases". We deliberately do NOT
# sort by tag name (CalVer run numbers are unpadded, so a lexical sort ranks
# v2026.07.23.9 above v2026.07.23.100) nor by S3 LastModified (deploy.sh
# re-uploads releases/<tag>/... on every deploy, incl. a rollback, so LastModified
# tracks last-deploy, not release recency). Keying BOTH stores off this one order
# keeps their retained windows identical — the same 5 tags in each.
ordered_tags=$(gh release list --repo "$REPO" --limit 1000 \
  --json tagName,createdAt \
  --jq "sort_by(.createdAt) | reverse | .[].tagName")

keep_tags=$(printf '%s\n' "$ordered_tags" | sed '/^$/d' | head -n "$KEEP")
stale_tags=$(printf '%s\n' "$ordered_tags" | sed '/^$/d' | tail -n "+$((KEEP + 1))")

# --- GitHub Release assets ---------------------------------------------------
# Delete just the heavy binary asset from every stale release; the Release object
# and its git tag are left intact. Guarded on the asset still being present so
# re-runs stay quiet.
for tag in $stale_tags; do
  if gh release view "$tag" --repo "$REPO" --json assets \
       --jq '.assets[].name' 2>/dev/null | grep -qx "$ASSET"; then
    echo "    prune GitHub Release asset: ${tag}/${ASSET}"
    gh release delete-asset "$tag" "$ASSET" --repo "$REPO" --yes
  fi
done

# --- S3 artifact binaries ----------------------------------------------------
# One object per tag: releases/<tag>/jira-stats-linux-arm64. Delete every object
# whose tag is NOT in the keep set, so S3 mirrors exactly the same 5 tags GitHub
# retains (independent of S3 LastModified churn from re-uploads).
s3_keys=$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix "releases/" \
  --query "Contents[].Key" --output text | tr '\t' '\n' | sed '/^$/d')

for key in $s3_keys; do
  # releases/<tag>/jira-stats-linux-arm64 -> <tag>
  tag=${key#releases/}
  tag=${tag%%/*}
  if printf '%s\n' "$keep_tags" | grep -qxF "$tag"; then
    continue
  fi
  echo "    prune S3 artifact: s3://${BUCKET}/${key}"
  aws s3api delete-object --bucket "$BUCKET" --key "$key" >/dev/null
done

echo "==> retention prune complete"
