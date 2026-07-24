# GitHub OIDC deploy identity (one-time AWS setup)

Source-of-truth for the AWS side of the release/deploy pipeline (ADR 0006, #153).
GitHub Actions on this repo obtains **short-lived** AWS credentials in the
internal-tooling account (`214519213070`) via **GitHub OIDC** — no long-lived
secrets stored in the repo. This is a **one-time** human setup; the JSON here is
what gets applied, and `.github/workflows/oidc-verify.yml` proves it works.

## What gets created

| Resource | Value |
|---|---|
| Account | `9elf26-internal-tooling` — `214519213070` |
| Region | `eu-central-1` |
| OIDC provider | `token.actions.githubusercontent.com` (audience `sts.amazonaws.com`) |
| Role | `jira-stats-github-deploy` |
| Trust | this repo on `main` only — `oidc-trust-policy.json` |
| Permissions | S3 artifact read/write + list/delete (retention pruning, #156) + SSM SendCommand/GetCommandInvocation to the one instance — `deploy-role-policy.json` |

The role is deliberately **narrower** than the human deploy path (which assumes
the broad `OrganizationAccountAccessRole`). CI never touches the management
account.

## One-time setup

Run from the **management account** with credentials that can assume into the
tooling account. All commands below run **inside the tooling account**; see the
"reaching the tooling account" note at the bottom for how to get a shell there.

```sh
ACCOUNT=214519213070
REGION=eu-central-1

# 1. IAM OIDC identity provider for GitHub Actions (idempotent — skip if it
#    already exists; check with: aws iam list-open-id-connect-providers).
#    GitHub's OIDC endpoint uses a well-known thumbprint; modern IAM validates
#    the cert chain and the thumbprint is not security-critical, but the API
#    still requires one.
aws iam create-open-id-connect-provider \
  --url https://token.actions.githubusercontent.com \
  --client-id-list sts.amazonaws.com \
  --thumbprint-list 6938fd4d98bab03faadb97b34396831e3780aea1

# 2. The deploy role, trusting only this repo on main (files are in this dir).
aws iam create-role \
  --role-name jira-stats-github-deploy \
  --assume-role-policy-document file://oidc-trust-policy.json \
  --description "GitHub Actions OIDC deploy role for jira-stats (ADR 0006, #153)"

# 3. Its least-privilege permissions (inline policy).
aws iam put-role-policy \
  --role-name jira-stats-github-deploy \
  --policy-name jira-stats-deploy \
  --policy-document file://deploy-role-policy.json
```

The resulting role ARN is
`arn:aws:iam::214519213070:role/jira-stats-github-deploy` — already hard-wired
into `.github/workflows/oidc-verify.yml` (and reused by the deploy workflow in
#155).

## Verify

After the role exists, run the verification workflow **from `main`** (the trust
policy only federates on `main`):

```sh
gh workflow run "OIDC verify" --ref main
gh run watch   # or: gh run list --workflow "OIDC verify"
```

Green means: `sts:GetCallerIdentity` federated (no stored keys), the artifact
bucket is read/write **and** list/delete under `releases/` (the retention-pruning
permissions, #156), `ssm:SendCommand` + `GetCommandInvocation` reach the
instance, **and** an out-of-scope call (`ec2:DescribeInstances`) is correctly
denied — i.e. the role is neither too narrow nor too broad.

## Reaching the tooling account from the management account

```sh
creds=$(aws sts assume-role \
  --role-arn arn:aws:iam::214519213070:role/OrganizationAccountAccessRole \
  --role-session-name oidc-setup --query Credentials --output json)
export AWS_ACCESS_KEY_ID=$(echo "$creds" | jq -r .AccessKeyId)
export AWS_SECRET_ACCESS_KEY=$(echo "$creds" | jq -r .SecretAccessKey)
export AWS_SESSION_TOKEN=$(echo "$creds" | jq -r .SessionToken)
export AWS_DEFAULT_REGION=eu-central-1
# ...run the setup commands above, then `unset` these when done.
```

(Or log in to the tooling account via Identity Center — Identity Center lives in
`eu-north-1` — and use a console/CloudShell session there.)

## Revocation

If this repo is ever compromised, delete the role to cut all CI access at once:
`aws iam delete-role-policy --role-name jira-stats-github-deploy --policy-name
jira-stats-deploy && aws iam delete-role --role-name jira-stats-github-deploy`.
