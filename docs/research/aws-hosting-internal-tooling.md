# Hosting the Jira-stats app on AWS, cheaply, from a new internal-tooling account

Research date: 2026-07-22. Scope: two questions — (1) the AWS-recommended way to stand up a
**dedicated internal-tooling account** inside an org that already "uses AWS for everything", and
(2) the **cheapest realistic way to host this specific app** on AWS, with concrete $/month figures
traced to official pricing pages.

Every non-obvious claim (especially prices and free-tier limits) is followed back to the AWS page
that owns it, cited inline. Where a figure is JS-rendered on a pricing page that couldn't be read as
text, or comes from a third-party tracker, it is **flagged** as such and marked "re-verify".

> ⚠️ **AWS prices change.** All dollar figures below are us-east-1 (N. Virginia) and were current as
> researched on 2026-07-22. Treat them as estimates and re-verify against the live pricing pages
> (linked inline) before committing.

---

## 0. App profile (everything below is grounded in this)

- **Single static Go binary**, `CGO_ENABLED=0`, buildable for **arm64** (Graviton) as easily as
  x86. Trivially containerizable (scratch/distroless) *or* runnable as a bare Linux binary.
- Listens on HTTP **`:8080`**. Server-rendered HTMX; **static assets are embedded** in the binary —
  no separate CDN/asset bucket needed.
- **Data store is SQLite (one file), a rebuildable projection of Jira.** On start / on demand it
  re-syncs the whole dataset from Jira Cloud in ~15–30s. ⇒ **persistent disk is optional**; ephemeral
  storage lost on restart is acceptable. This is what unlocks the cheapest compute options.
- Runs a **long-running background sync loop** (~60s poll). ⇒ needs an **always-on process**. A pure
  scale-to-zero serverless model (Lambda) does **not** fit unless the sync is redesigned as a
  scheduled trigger (flagged in §2.6).
- Config via **env vars**; **exactly one secret** (`JIRA_API_TOKEN`).
- Traffic is tiny (a handful of internal users); CPU/RAM fit **128–256 MB** comfortably.
- It is internal tooling exposed over the public internet with **no built-in auth** ⇒ needs an
  **auth layer in front** (§2.8).

---

# Part 1 — Standing up a new internal-tooling account in an existing org

## 1.1 The recommended shape: a new *member account* in its own OU

AWS's primary guidance is the whitepaper **"Organizing Your AWS Environment Using Multiple
Accounts"** (publication date April 30, 2025). Its core recommendation:

> "We recommend using several accounts to separate your workloads, rather than relying on a single
> account… AWS charges are based on resource usage, not the number of accounts."
> — [Organizing Your AWS Environment Using Multiple Accounts](https://docs.aws.amazon.com/whitepapers/latest/organizing-your-aws-environment/organizing-your-aws-environment.html)

Each account is a hard **isolation boundary**: "By default, no access is allowed between accounts."
So a dedicated tooling account gives you a clean blast-radius and a clean cost line — for free
(accounts themselves cost nothing).

**Which OU does an internal-tooling account belong in?** The whitepaper's recommended OU set
([Recommended OUs and accounts](https://docs.aws.amazon.com/whitepapers/latest/organizing-your-aws-environment/recommended-ous-and-accounts.html)):

- **Foundational OUs** — *Security OU* and **Infrastructure OU** ("Groups AWS accounts that host and
  manage core infrastructure and networking services and resources **that are shared across the
  organization**").
- **Application OUs** — *Workloads OU* ("host the organization's business-specific workloads,
  including both production and non-production environments").
- **Experimental OUs** — *Sandbox OU*.
- plus procedural/advanced OUs (Deployments, Exceptions, etc.).

An internal Jira-stats dashboard is a **shared internal service**, not a customer-facing business
workload and not a sandbox experiment. Two defensible placements, both first-party-supported:

1. **Infrastructure OU** — if you treat it as a shared-services/internal-tooling account alongside
   other org-wide shared infra. This is the closest match to the whitepaper's "shared across the
   organization" language and is the recommendation here.
2. **Workloads OU (non-prod side)** — acceptable if your org already lumps internal apps there.

Either way it's **one new member account**, in an OU, governed by org policies. Don't run it in the
management (payer) account — AWS explicitly steers security/governance tooling and workloads *out* of
the management account.

## 1.2 Creating the account and setting up human access

- **Create the member account** from the org's management account via AWS Organizations (console,
  `aws organizations create-account`, or Control Tower's Account Factory). If the org runs
  **AWS Control Tower**, provision through **Account Factory** so the account is enrolled with the
  org's baseline guardrails automatically rather than hand-rolled.
- **Human sign-in: AWS IAM Identity Center** (formerly AWS SSO) is the recommended way to give people
  access to the new account — you assign users/groups to the account with a permission set rather
  than minting IAM users. **IAM Identity Center itself has no additional charge** (you pay only for
  what the underlying accounts use). Cite the service page: [AWS IAM Identity Center](https://aws.amazon.com/iam/identity-center/).
  In an org that "uses AWS for everything," Identity Center almost certainly already exists — you
  just add an assignment for the new account.

## 1.3 Consolidated billing — and the free-tier trap for a new *member* account

The new account rolls up to the org's **management (payer) account** under **consolidated billing**,
at no extra fee:

> "Every organization in AWS Organizations has a *management account* that pays the charges of all the
> *member accounts*." … "**No extra fee** – Consolidated billing is offered at no additional cost."
> — [Consolidating billing for AWS Organizations](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/consolidated-billing.html)

**The important nuance — free tier is pooled across the org, not per account.** Under consolidated
billing AWS treats the whole org as a single account for Free Tier purposes: it "applies the free
tier to the total usage across all accounts in an AWS organization… **AWS doesn't apply the free tier
to each account individually**"
([Consolidated billing / free tier docs](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-limits.html)).
So in an org that already uses AWS heavily, the org-wide 12-month and always-free allowances (e.g.
the 750 EC2 hours) are **likely already consumed by existing accounts** — the new tooling account
should **not** be assumed to have any usable "free tier" headroom. Plan on paying steady-state rates.

**And the newer, sharper caveat (this is the big one for a brand-new account in 2026):** AWS
overhauled the Free Tier for accounts created **on/after July 15, 2025**. New accounts no longer get
the classic 12-month trial; instead they get **USD $100 in credits at sign-up, up to $100 more from
activities, and a 6-month "free account plan"**
([AWS Free Tier update blog](https://aws.amazon.com/blogs/aws/aws-free-tier-update-new-customers-can-get-started-and-explore-aws-with-up-to-200-in-credits/);
[Choosing a plan](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)).
Crucially, that free-plan/credits path **does not survive joining an org**:

> "Free account plans will **automatically upgrade to paid plan** if you join AWS Organizations, set
> up an AWS Control Tower landing zone…"
> — [Choosing a plan](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)

> "When your account joins an AWS Organization or sets up an AWS Control Tower landing zone, your Free
> Tier credits expire immediately, and your account will be ineligible to earn more AWS Free Tier
> credits." — [AWS Free Tier FAQs](https://aws.amazon.com/free/free-tier-faqs/)

**Bottom line for Part 1 costing:** a member account created *inside* (or joined *into*) the org gets
**no signup credits and no personal free-tier headroom** — it's effectively a paid account from day
one, sharing whatever org-pooled always-free limits are left. The only free tier you can still count
on is the org-wide **always-free** allowances (Lambda 1M req + 400k GB-s/month, CloudFront 100 GB +
1M req/month, SSM standard parameters, etc.) — *if* not already used up elsewhere. **Do the Part 2
cost math at full steady-state rates**, and treat any free-tier savings as a bonus, not a plan.
(Contrast: a *standalone* brand-new account outside any org would get the $100–$200 credits + 6-month
free plan — but you're explicitly adding this to an existing org, so that path is off the table.)

## 1.4 Guardrails for a cheap tooling account

- **SCPs (Service Control Policies)** — attach an SCP to the account's OU to fence it in: e.g. deny
  regions you don't use, deny expensive service families, deny leaving the org. SCPs are an
  Organizations feature with **no additional charge**; they cap what the account *can* do regardless
  of IAM. (AWS Organizations feature — see the [Organizations User Guide](https://docs.aws.amazon.com/organizations/latest/userguide/).)
- **AWS Budgets** — set a hard **cost budget with alerts** so a tiny tooling account can't silently
  balloon. **Cost/usage budgets with notifications are free**: "You can monitor and receive
  notifications on your budgets free of charge." Only *action-enabled* budgets cost money — "the
  first two action-enabled budgets [are free]… afterwards each subsequent action-enabled budget will
  incur a $0.10 daily cost." — [AWS Budgets pricing](https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/).
  For a $5–10/month app, a single free notification budget at e.g. $20/month is plenty. Note free-tier
  *usage* alerts are opt-in and only at the management-account level, not per member account
  ([Consolidated billing docs](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/consolidated-billing.html)).
- **Control Tower** (if adopted) applies preventive/detective guardrails automatically at enrollment;
  the service has no per-account license fee (you pay only underlying resource usage such as the
  config/CloudTrail it turns on).

---

# Part 2 — Cheapest way to host *this* app

All estimates assume **730 hours/month** and **us-east-1**. Region matters modestly (App
Runner/Fargate/EC2 rates are a bit higher in some regions; Lightsail bundle prices are flat across
most regions). Because §1.3 shows a member account has ~no reliable free tier, the primary column
below is **steady-state (post-free-tier) cost**; free-tier notes are secondary.

## 2.1 AWS App Runner (container, always-on)

Rates (us-east-1): **$0.064 / vCPU-hour** (active) and **$0.007 / GB-hour** (memory, incl. idle
"provisioned" instances) — [App Runner pricing](https://aws.amazon.com/apprunner/pricing/). Smallest
size is **0.25 vCPU / 0.5 GB**. App Runner's model: you always pay for provisioned **memory** to keep
min instances warm, and pay **vCPU only while actively handling requests**.

- Idle/provisioned memory floor: `0.5 GB × $0.007 × 730 ≈ $2.56/mo`.
- If billed as continuously active: `+ 0.25 vCPU × $0.064 × 730 ≈ $11.68` ⇒ **~$14/mo**.

> ⚠️ **Background-sync gotcha.** App Runner throttles CPU on provisioned instances between requests
> (that's how the low "idle" rate works). This app's **60s background sync loop needs CPU when no HTTP
> request is in flight** — it may be starved on a throttled instance, and/or you may end up billed at
> the active vCPU rate to keep it running. Either the sync stalls (bad) or you pay ~$14/mo (not the
> ~$2.6 headline). App Runner is built for request-driven services, so this app is an **awkward fit**.
> No dedicated App Runner free tier is published.

## 2.2 ECS Fargate (with/without a load balancer)

Fargate rates, us-east-1 (per [Fargate pricing](https://aws.amazon.com/fargate/pricing/), converted
from per-second): **Linux/ARM (Graviton)** ≈ $0.0323798/vCPU-hr and $0.0035600/GB-hr; **Linux/x86** ≈
$0.0404784/vCPU-hr and $0.004446/GB-hr. Smallest task 0.25 vCPU / 0.5 GB. 20 GB ephemeral storage is
included free.

- **Compute, 0.25 vCPU / 0.5 GB, ARM, always-on:**
  `0.25 × 0.0323798 × 730 + 0.5 × 0.00356 × 730 ≈ $5.91 + $1.30 = ` **~$7.2/mo** (x86 ≈ $9.0/mo).
- **But you need ingress.** A Fargate task's public IP changes on restart, so a stable endpoint means
  an **Application Load Balancer** — and the ALB **dominates**: `$0.0225/hr × 730 ≈` **~$16.4/mo**
  hourly floor, plus LCUs ([ELB pricing](https://aws.amazon.com/elasticloadbalancing/pricing/)). Add
  a **public IPv4 charge** (~$0.005/hr ≈ ~$3.65/mo per public IPv4 address — *re-verify on the
  [VPC pricing page](https://aws.amazon.com/vpc/pricing/)*) if you go IP-direct instead of ALB.
- **Realistic Fargate total:** ~$7 compute + ~$16 ALB ≈ **~$23/mo** — the ALB makes this poor value
  for a tiny app. Fargate without an ALB (public-IP task) is ~$7 + ~$3.65 IPv4 ≈ **~$11/mo** but with
  an unstable IP.

## 2.3 EC2 / ECS-on-EC2 (single small instance)

A single Graviton burstable instance runs the bare binary directly (systemd) — ephemeral disk is
fine per the app profile.

- **t4g.nano** (2 vCPU burst, **0.5 GB** RAM): **$0.0042/hr ≈ $3.07/mo**
  *(price from third-party trackers cross-checking the JS-rendered [EC2 on-demand page](https://aws.amazon.com/ec2/pricing/on-demand/); re-verify).*
  0.5 GB is tight once you add the OS + a Jira re-sync into SQLite; workable but risky.
- **t4g.micro** (2 vCPU burst, **1 GB** RAM): **~$0.0084/hr ≈ ~$6.13/mo** — the safer pick.
- **Add-ons that bite:** a small **root EBS gp3 volume** (~8 GB ≈ ~$0.64/mo) and a **public IPv4**
  address (~$3.65/mo — chargeable since 2024; re-verify on [VPC pricing](https://aws.amazon.com/vpc/pricing/)).
- **Realistic EC2 total:** t4g.nano ≈ **~$7.3/mo**, t4g.micro ≈ **~$10.4/mo**, all-in with IPv4 + EBS.
- **Free tier:** the legacy EC2 free tier is **"750 hours … of t2.micro, and t3.micro instances"**
  for 12 months ([AWS Free Tier quotas](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-limits.html)) —
  x86, **not** the Graviton t4g, and (per §1.3) **pooled org-wide and unavailable to a joined member
  account**. So for this account, assume you pay the full ~$7–10/mo.
- ECS-on-EC2 adds orchestration overhead with no cost saving over just running the binary; skip it at
  this scale.

## 2.4 Lightsail (fixed-price instances)

Flat monthly pricing that **bundles the instance, a static IP, SSD, and a generous data-transfer
allowance** — the simplest predictable option. Linux/Unix (IPv6-only) bundles
([Lightsail pricing](https://aws.amazon.com/lightsail/pricing/)):

| Bundle | RAM | vCPU | SSD | Transfer |
| --- | --- | --- | --- | --- |
| **$3.50/mo** | 512 MB | 2 | 20 GB | 1 TB |
| **$5/mo** | 1 GB | 2 | 40 GB | 2 TB |
| **$10/mo** | 2 GB | 2 | 60 GB | 3 TB |

- **$5/mo** (1 GB) is the sweet spot — comfortable headroom for OS + Go binary + SQLite re-sync, no
  separate IPv4/bandwidth line items. **$3.50/mo** (512 MB) is viable given the 128–256 MB footprint
  but leaves little slack during a full Jira re-sync.
- **Lightsail free trial:** "90-day free trial" on selected bundles ($3.50/$5/$10 Linux IPv6)
  ([Free Lightsail](https://aws.amazon.com/free/compute/lightsail/)). But this is a *sign-up* promo —
  per §1.3, an account joined into an org loses free-tier eligibility, so **don't bank on it**.

## 2.5 Lightsail containers (if you prefer a container workflow)

Lightsail container service gives a **managed HTTPS endpoint** (TLS + a stable URL, no ALB to run):
**Nano $7/mo** (0.25 vCPU / 512 MB), **Micro $10/mo** (0.25 vCPU / 1 GB)
([Lightsail pricing](https://aws.amazon.com/lightsail/pricing/)). Slightly pricier than a plain $5
instance but you get HTTPS termination and rolling deploys built-in — a clean middle ground for a
containerized Go binary.

## 2.6 Lambda + API Gateway / Function URL — evaluated, but a **mismatch**

Lambda's always-free tier is generous and perpetual: **"one million requests and 400,000 GB-seconds
per month"**, then $0.20/1M requests and $0.0000166667/GB-second
([Lambda pricing](https://aws.amazon.com/lambda/pricing/)) — this app's traffic would sit inside free
tier. A **Function URL** avoids API Gateway cost entirely.

**But it doesn't fit the app as built**, on two counts:

1. **No always-on process.** Lambda scales to zero; the **60s background sync loop can't run**. You'd
   have to redesign the sync as an **EventBridge Scheduler** rule invoking the function on a cadence —
   a real code change, and a different operational model.
2. **Ephemeral, per-invocation filesystem.** The SQLite projection wouldn't persist across cold
   invocations, so each request could face a cold DB (a 15–30s Jira re-sync per cold start is
   unacceptable latency), or you'd move state to EFS/DynamoDB — defeating the simplicity.

Flag it and move on: Lambda is cheapest *on paper* but requires re-architecting the sync. Not
recommended unless you're willing to split sync (scheduled) from serve (request-driven).

## 2.7 The supporting pieces and their cost

- **Secret (`JIRA_API_TOKEN`) → SSM Parameter Store `SecureString` (standard) = free.** "Standard
  parameters are available at no additional charge" and standard-throughput API is "No additional
  charge" ([SSM pricing](https://aws.amazon.com/systems-manager/pricing/)). **Secrets Manager is
  paid** — "$0.40 / secret / month" + "$0.05 / 10,000 [API] call[s]"
  ([Secrets Manager pricing](https://aws.amazon.com/secrets-manager/pricing/)). For one static token,
  **use Parameter Store SecureString** and save the $0.40/mo. (On Lightsail, which has no IAM role
  integration, just inject the token as an env var from your deploy tooling.)
- **HTTPS/TLS.** **ACM public certificates are free** — *but only usable on ALB / CloudFront / API
  Gateway*, **not** installable directly on an EC2 or Lightsail instance. So: on **Fargate+ALB** or
  **CloudFront**, TLS is free via ACM; on **plain EC2/Lightsail**, terminate TLS in-process with
  **Let's Encrypt** (e.g. Caddy/autocert) at $0, or use a **Lightsail container service** (HTTPS
  endpoint included, §2.5).
- **Custom domain / DNS.** Route 53 hosted zone **"$0.50 per hosted zone per month"** + "$0.40 per
  million queries" ([Route 53 pricing](https://aws.amazon.com/route53/pricing/)); alias records to
  AWS resources have no query charge. Domain registration is billed separately by TLD. If the org
  already hosts a zone, a subdomain adds ~$0.
- **Data egress.** Negligible here (a few internal users, server-rendered HTML). Lightsail bundles
  **include** a multi-TB transfer allowance; for EC2/Fargate the AWS free allotment and low volume
  make egress effectively $0. CloudFront's always-free tier is **"100GB" out + "1M" requests/month**
  ([CloudFront pricing](https://aws.amazon.com/cloudfront/pricing/)).

## 2.8 Auth in front — cheapest way to gate access

The app has no auth, so *something* must gate it. Options, cheapest first:

- **IP allowlist (≈ $0).** Restrict ingress to the corp VPN/office CIDRs via the **Lightsail
  firewall** or an **EC2 security group** — free, and often sufficient for internal tooling. This is
  the cheapest gate that actually works.
- **Reverse-proxy / IdP auth in front of the app (≈ $0 AWS cost).** Run `oauth2-proxy` (or Caddy with
  an auth plugin) as a sidecar/front process wired to the org's existing IdP, or put **Cloudflare
  Access** (free tier for a small user count) in front. Since the org already runs an IdP, this gates
  by real identity for no AWS spend.
- **ALB + Amazon Cognito (built-in, but adds the ALB).** ALB supports native OIDC/Cognito
  authentication; Cognito's Essentials tier includes **"10,000 monthly active user (MAU) per month
  per account"** free, then $0.015/MAU ([Cognito pricing](https://aws.amazon.com/cognito/pricing/)) —
  so Cognito itself is free at this scale. **The cost is the ALB (~$16/mo).** Only worth it if you're
  already on Fargate+ALB.
- **CloudFront + Cognito/Lambda@Edge** — more moving parts; not cheaper than the reverse-proxy route
  for one tiny app.

**Recommendation:** for a handful of internal users, **IP allowlist (free) or an IdP-backed
reverse-proxy / Cloudflare Access (free)** gates access without paying for an ALB. Reserve
ALB+Cognito for when you're already running an ALB for other reasons.

---

## 3. Cost comparison (us-east-1, steady-state, ~730 h/mo)

| Option | Compute | Ingress/TLS | Realistic all-in $/mo | Fit for this app |
| --- | --- | --- | --- | --- |
| **Lightsail $5 instance** | 1 GB fixed | static IP + Let's Encrypt (in bundle, $0) | **~$5** | ✅ Best — always-on, bundled IP+bandwidth, simplest |
| **Lightsail $3.50 instance** | 512 MB fixed | in bundle | **~$3.50** | ✅ Works; tight RAM during re-sync |
| **Lightsail container (Nano)** | 0.25 vCPU/512 MB | HTTPS endpoint incl. | **~$7** | ✅ If you want a container + built-in HTTPS |
| **EC2 t4g.nano** | 0.5 GB burst | +IPv4 ~$3.65, +EBS ~$0.64, Let's Encrypt | **~$7.3** | ✅ Always-on; more ops; RAM tight |
| **EC2 t4g.micro** | 1 GB burst | +IPv4 +EBS | **~$10.4** | ✅ Safer RAM; more ops |
| **App Runner 0.25/0.5** | 0.25 vCPU/0.5 GB | HTTPS incl. | **~$2.6 idle → ~$14 active** | ⚠️ Background-sync may be CPU-throttled |
| **Fargate 0.25/0.5 + ALB** | ARM task | ALB ~$16.4 + ACM | **~$23** | ⚠️ ALB dominates; overkill |
| **Lambda + Function URL** | per-invoke | HTTPS incl. | **~$0 (free tier)** | ❌ Needs re-architecting sync + state |

*(Figures traced to the pricing pages linked in §2; EC2 hourly and the public-IPv4 charge are the
least-firm numbers — re-verify. The member-account free-tier caveats in §1.3 mean none of these
should be assumed to be discounted by free tier.)*

---

## 4. Bottom-line recommendation

**Cheapest viable setup: a single Amazon Lightsail $5/month Linux instance (1 GB), running the Go
binary directly under systemd, TLS via Let's Encrypt/Caddy, secret injected as an env var, access
gated by the free Lightsail firewall IP allowlist (or an IdP reverse-proxy).**

- **~$5/month, flat and predictable**, and that single line item **includes the instance, a static
  IP, SSD, and a multi-TB transfer allowance** — no surprise IPv4 or egress charges
  ([Lightsail pricing](https://aws.amazon.com/lightsail/pricing/)). The cost driver is simply the
  fixed bundle. The app's rebuildable-SQLite / ephemeral-disk design and always-on sync loop fit a
  plain always-on instance perfectly. Drop to the **$3.50 (512 MB)** bundle if you confirm the Jira
  re-sync fits comfortably in 512 MB.
- **Runner-up: an EC2 t4g.nano/micro (Graviton) instance, ~$7–10/month all-in.** Same always-on
  model, but you pay separately for a public IPv4 (~$3.65) and EBS, and you get full VPC/IAM
  integration (native SSM Parameter Store role access, org guardrails, ACM-via-ALB later). Choose
  this over Lightsail when you want the instance inside the org's VPC/IAM/tooling rather than
  Lightsail's semi-separate world.
- **When to step up:** move to **Fargate + ALB (~$23/mo)** or **App Runner** only when you need
  managed rolling deploys, autoscaling, native ACM TLS, or ALB+Cognito SSO — i.e. when the ~$16
  ALB/managed-platform premium buys operational value you actually need. For a handful of internal
  users it does not.
- **Auth:** gate with the **free IP allowlist** or an **IdP-backed reverse-proxy / Cloudflare Access
  (free)** — do **not** pay for an ALB just to bolt on Cognito.
- **Secret:** **SSM Parameter Store `SecureString` (free)**, not Secrets Manager (~$0.40/mo saved).

**Free-tier vs steady-state:** normally a brand-new standalone account could ride the $100–$200
credits / 6-month free plan and run this at ~$0 for months. **That does not apply here** — a member
account created inside (or joined into) the existing org **loses signup credits and personal
free-tier eligibility immediately** (§1.3), and the org-wide always-free/12-month allowances are
pooled and likely already spent. So plan on the **steady-state ~$5/month (Lightsail)** from day one;
treat any residual org free-tier as a bonus, not the plan.

---

## Sources

- [Organizing Your AWS Environment Using Multiple Accounts (whitepaper)](https://docs.aws.amazon.com/whitepapers/latest/organizing-your-aws-environment/organizing-your-aws-environment.html)
- [Recommended OUs and accounts](https://docs.aws.amazon.com/whitepapers/latest/organizing-your-aws-environment/recommended-ous-and-accounts.html)
- [AWS IAM Identity Center](https://aws.amazon.com/iam/identity-center/)
- [Consolidating billing for AWS Organizations](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/consolidated-billing.html)
- [AWS Free Tier quotas / limits](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-limits.html)
- [Choosing a plan (new Free Tier, post-July-15-2025)](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/free-tier-plans.html)
- [AWS Free Tier update blog ($200 credits / 6-month plan)](https://aws.amazon.com/blogs/aws/aws-free-tier-update-new-customers-can-get-started-and-explore-aws-with-up-to-200-in-credits/)
- [AWS Free Tier FAQs](https://aws.amazon.com/free/free-tier-faqs/)
- [AWS Organizations User Guide (SCPs, consolidated billing)](https://docs.aws.amazon.com/organizations/latest/userguide/)
- [AWS Budgets pricing](https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/)
- [AWS App Runner pricing](https://aws.amazon.com/apprunner/pricing/)
- [AWS Fargate pricing](https://aws.amazon.com/fargate/pricing/)
- [Amazon EC2 On-Demand pricing](https://aws.amazon.com/ec2/pricing/on-demand/)
- [Amazon Lightsail pricing](https://aws.amazon.com/lightsail/pricing/)
- [Free Lightsail (trial)](https://aws.amazon.com/free/compute/lightsail/)
- [AWS Lambda pricing](https://aws.amazon.com/lambda/pricing/)
- [Elastic Load Balancing pricing (ALB)](https://aws.amazon.com/elasticloadbalancing/pricing/)
- [Amazon Cognito pricing](https://aws.amazon.com/cognito/pricing/)
- [AWS Systems Manager (Parameter Store) pricing](https://aws.amazon.com/systems-manager/pricing/)
- [AWS Secrets Manager pricing](https://aws.amazon.com/secrets-manager/pricing/)
- [Amazon Route 53 pricing](https://aws.amazon.com/route53/pricing/)
- [Amazon CloudFront pricing](https://aws.amazon.com/cloudfront/pricing/)
- [Amazon VPC pricing (public IPv4 charge)](https://aws.amazon.com/vpc/pricing/)
