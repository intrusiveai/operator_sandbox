# Candidate releases

Use this guide to configure release publication once, publish a rolling candidate,
and promote its existing signed packages to stable. All settings and commands
apply to `intrusiveai/operator_sandbox` unless explicitly stated otherwise.

## How the release moves through the system

1. A push to `candidate-v<major.minor.patch>` (including a merged feature PR)
   runs tests and reproducible builds for Linux/macOS on AMD64/ARM64.
2. Actions signs and uploads the four packages to the version's rolling GitHub
   **Pre-release**. Operator's local catalog job publishes the exact download
   links and hashes with `tags: ["rc"]` to the releases service.
3. Merging the reviewed candidate PR into `main` promotes those same packages
   to stable and updates the catalog label to `release`. It does not rebuild or
   re-sign them. The website shows the first two entries labeled `release`.

Operator's passing CI/signing and reviewed candidate merge are the publication
checks. Finish the testing planned for that release before merging. Publication
itself does not claim that every native runtime profile is qualified or approve
an Attack Harness image; those have their own qualification and admission paths.

The catalog publisher is committed in this repository. Routine publication needs
no private releases-repository access, infrastructure apply, or website deployment.
The one-time setup below must be completed before the first publication.

## One-time repository setup

Complete these steps with administrator access to
[`intrusiveai/operator_sandbox`](https://github.com/intrusiveai/operator_sandbox).
All repository settings below belong to this repository unless stated otherwise.
The releases service must already be deployed. Step 5 configures its existing
role and environment; step 6 configures the required GitHub identity format.

### 1. Make the release workflows available on GitHub

1. Merge or push the release-workflow commits to the GitHub default branch,
   `main`.
2. In GitHub's **Code** tab, select `main` and confirm that
   `.github/workflows/verify-release.yml`,
   `.github/workflows/publish-catalog.yml`, `scripts/publish_host_release.py`, and the
   complete `tools/release-publisher` directory exist.
3. Create candidate branches from a commit containing those files. The catalog
   workflow and local publisher must exist on both the default branch and the
   candidate's eventual `v*` tag for GitHub to dispatch it.

### 2. Allow the workflow's actions

1. Open **Settings → Actions → General** and ensure Actions is enabled.
2. If the repository or organization restricts allowed actions, allow the pinned
   `actions/checkout`, `actions/setup-go`, and `actions/upload-artifact` actions
   (or enable **Allow actions created by GitHub**). Also allow
   `aws-actions/configure-aws-credentials@e1253824e5c10ff9df46874f81ed3ec929e19cfd`.
   Use the exact pins in both workflow files if your policy permits individual
   SHAs rather than GitHub-owned actions generally. Remove the obsolete
   `intrusiveai-engineering/releases/.github/workflows/publish-catalog.yml` exception;
   this repository no longer calls it or needs access to that private repository.
3. Save any changes. If an organization policy locks these settings, have the
   organization owner update the allowlist.

GitHub creates `GITHUB_TOKEN` for each job automatically. **Do not create a secret
named `GITHUB_TOKEN`.** The checked-in workflows request Contents and Actions
write access only for the publishing jobs; the repository's default workflow
permission can remain read-only. The catalog job requests Contents and Pull
requests read access plus `id-token: write` for temporary AWS credentials.

### 3. Configure signing and release credentials

#### Install the OpenPGP signing key

Operator signs every host package. Complete this before pushing a candidate:

1. Select your publisher-controlled OpenPGP signing key (or signing subkey) and
   its full fingerprint. List available keys with `gpg --list-secret-keys
   --keyid-format long --with-subkey-fingerprint`. Use a full 40- or 64-character
   fingerprint, not the short key ID. Ensure the key is usable and unexpired.
2. Export the selected private signing material into a private file outside the
   repository. For a dedicated release key, run the following, replacing the
   fingerprint with your actual one. `--export-secret-subkeys` may be used for
   an established signing-subkey setup instead.

   ```sh
   umask 077
   gpg --armor --output /private/path/operator-release-private.asc \
     --export-secret-keys FULL_SIGNING_FINGERPRINT
   ```

   Replace `/private/path` with an existing private directory. Do not commit this
   file or place it in a shared directory.
3. Open **Settings → Secrets and variables → Actions → Secrets → New repository
   secret**. Add `RELEASE_SIGNING_KEY` containing the complete armored private
   export. Add `RELEASE_SIGNING_PASSPHRASE` with its passphrase if encrypted;
   omit that secret for an unencrypted CI signing key.
4. In the same Actions settings, select **Variables → New repository variable**.
   Add `RELEASE_SIGNING_FINGERPRINT` with the full fingerprint selected above.
   Use repository settings here: the signing job is not in `production`.
5. Export the corresponding public key with `gpg --armor --output
   operator-release-public.asc --export FULL_SIGNING_FINGERPRINT` and distribute
   it to installing administrators through your established trusted channel.
   Follow [host distribution](../../docs/HOST_DISTRIBUTION.md) for keyring setup.
   Retire the temporary private export according to your key-handling policy.

The signing job imports the key into a temporary GPG home and removes it on exit.
Passphrases are passed through stdin. A key downloaded alongside an archive is
not an independently trusted key.

#### Decide whether to add `RELEASE_TOKEN`

Start with the automatic `GITHUB_TOKEN` if candidate workflow files are already
on `main` and your tag rules permit it. No token creation is needed in that case.
Add `RELEASE_TOKEN` if a candidate will include workflow changes not yet on
`main`, GitHub rejects its tag write for missing workflow permission, or the tag
rules in step 4 require a permitted maintainer identity.

To add the optional token:

1. In your personal GitHub account, open **Settings → Developer settings →
   Personal access tokens → Fine-grained tokens → Generate new token**.
2. Name it `operator-release`, choose an expiration, and select **intrusiveai**
   as the resource owner. Under **Repository access**, choose **Only select
   repositories**, then select **operator_sandbox**.
3. Under **Repository permissions**, grant **Contents: Read and write**,
   **Workflows: Read and write**, and **Pull requests: Read-only**. Leave other
   permissions at their defaults. The token owner must have write access to the
   repository and any required tag-rule bypass.
4. Generate and copy the token. If organization approval is required, have an
   organization owner approve it before using it.
5. Return to the repository's **Settings → Secrets and variables → Actions →
   Secrets → New repository secret**. Name the secret exactly `RELEASE_TOKEN`,
   paste the token as its value, and click **Add secret**. Use a repository
   secret, since the GitHub release jobs do not run inside an environment.
6. Renew the token and update this secret before it expires. The workflow selects
   it automatically when present; no YAML edit is needed. Catalog dispatch still
   uses the automatic `GITHUB_TOKEN`, so this optional token needs no Actions
   permission.

See GitHub's [token creation instructions](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens#creating-a-fine-grained-personal-access-token).

### 4. Permit rolling candidate tags and release assets

1. Open **Settings → General → Releases**. Leave **Enable release immutability**
   unchecked; disable it if enabled. If enforced by the organization, ask the
   organization owner to exempt this repository before publishing candidates.
   Rolling candidates replace assets and move their `v*` tag on each update.
2. Open **Settings → Rules → Rulesets** and inspect the rules targeting `v*` tags.
   The publishing identity must be allowed to create and update these tags,
   including moving an existing tag to the next candidate commit.
3. If those rules restrict creation, updates, or force pushes, configure their
   **Bypass list** for the publishing identity. For a personal `RELEASE_TOKEN`,
   add an authorized maintainer team or role containing its owner and select
   **Always allow**. Token permissions alone do not bypass a ruleset. Keep the
   restrictions for other users; do not assume the automatic token can bypass them.
4. Review branch rules for `main` and `candidate-v*` so that only trusted
   maintainers can approve release-workflow changes or push candidate code.
   Preserve direct candidate pushes if you intend to use that supported path.

GitHub describes these controls in [ruleset setup](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository).

### 5. Create the `production` environment and add service values

1. Ask the releases-service administrator to confirm that the existing Operator
   role's trust policy has been migrated to the product-local workflow and custom
   subject described in step 6. It must accept only this repository's numeric
   identity, the `production` environment, `v*` tags, the exact
   `.github/workflows/publish-catalog.yml` reference, and audience
   `sts.amazonaws.com`. Keep its existing catalog-only permissions and role ARN.
   This is a one-time IAM migration, not a routine release or site deployment.
   Obtain the deployed service values from that administrator.
   If you administer its initialized production Terraform checkout, run these
   read-only commands from the `intrusiveai-engineering/releases` checkout:

   ```sh
   terraform -chdir=infra/service output -raw content_bucket_name
   terraform -chdir=infra/service output -raw cloudfront_distribution_id
   terraform -chdir=infra/service output -json catalog_publisher_role_arns
   ```

   Take the role ARN from the map's `operator_sandbox` entry. Read the bucket
   region from the deployed production configuration's `aws_region` value. If the
   role is missing or still trusts a centralized reusable workflow, the service
   administrator must complete the product-local publisher migration first.
   Do not substitute Interceptor's or Attack Harness's role.
2. In **operator_sandbox → Settings → Environments**, open `production`, or
   click **New environment**, enter exactly `production`, and click
   **Configure environment**.
3. Under **Deployment branches and tags**, choose **Selected branches and tags**.
   Click **Add deployment branch or tag rule**, select **Tag**, enter `v*`, and
   add the rule. This is a tag rule, not a `candidate-v*` branch rule. Remove any
   branch rules (including `main`) or other tag patterns from this environment
   after checking for other consumers; it should permit only protected `v*` tags.
4. Retain existing required reviewers. If your release policy requires approval,
   add its maintainers under **Required reviewers** and save. An eligible reviewer
   must approve each waiting catalog run within its 30-minute publication timeout.
5. Under **Environment variables**, click **Add variable** for each row below,
   enter the exact name and deployed value, and save it. These are variables,
   not secrets.

   | Variable | Value to enter |
   | --- | --- |
   | `AWS_REGION` | `us-west-2` |
   | `CONTENT_BUCKET` | `intrusiveai-releases` |
   | `CLOUDFRONT_DISTRIBUTION_ID` | `E1ET1MODBEJLY6` |
   | `CATALOG_PUBLISHER_ROLE_ARN` | `arn:aws:iam::389547485478:role/releases-operator_sandbox-catalog-publisher` |

   These are the reviewed production values; confirm them against the deployed
   service before saving. Do not add a record-publisher role or AWS access keys.

Only the local catalog job obtains temporary AWS credentials through GitHub
OIDC. The environment belongs to
`intrusiveai/operator_sandbox`, not `intrusiveai-engineering/releases`.
See GitHub's [environment setup instructions](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments).

### 6. Set the repository's custom OIDC subject

OIDC lets GitHub obtain temporary AWS credentials. AWS checks a subject string
identifying the repository, environment, tag, and workflow file. The default
GitHub subject does not satisfy the new trust policy.

1. Inventory workflows requesting `id-token: write` and coordinate any other
   cloud roles before changing the subject. In this checkout, run:

   ```sh
   rg -n 'id-token:|environment:' .github/workflows
   ```

   This setting affects **every OIDC job in this repository**, not just releases.
   All such jobs must declare an environment for the new subject to be issued.
2. Sign in to `gh` with repository administrator access. Run this read-only
   command and retain its output with your configuration change record:

   ```sh
   gh api repos/intrusiveai/operator_sandbox/actions/oidc/customization/sub
   ```

3. After the administrator confirms the IAM migration in step 5, run:

   ```sh
   gh api --method PUT \
     repos/intrusiveai/operator_sandbox/actions/oidc/customization/sub \
     --input - <<'JSON'
   {"use_default":false,"include_claim_keys":["repository_id","repository_owner_id","environment","ref","workflow_ref"]}
   JSON
   ```

   Keep the exact key order. Use `workflow_ref`, not `job_workflow_ref`: the
   publisher is an ordinary job, not a reusable workflow.
4. Repeat the GET command from step 2 and confirm `use_default` is `false` and
   the five keys appear in the specified order. For tag `v0.0.1`, the subject
   expected by the role is:

   ```text
   repository_id:1383999597:repository_owner_id:296295422:environment:production:ref:refs/tags/v0.0.1:workflow_ref:intrusiveai/operator_sandbox/.github/workflows/publish-catalog.yml@refs/tags/v0.0.1
   ```

The subject binds the workflow file and ref, not its contents. Protect release
tags and review publisher/workflow changes before approving production jobs.
See GitHub's [repository OIDC configuration API](https://docs.github.com/en/rest/actions/oidc#set-the-customization-template-for-an-oidc-subject-claim-for-a-repository).

### 7. Confirm setup with the first candidate

1. Push the intended `candidate-v<version>` branch and open **Actions →
   verify-host-candidates**. Wait for verification and builds to finish and
   for the publish job to dispatch the catalog run.
2. Open the resulting **publish Operator release catalog** run. If it is
   waiting for environment approval, use **Review deployments**, select
   `production`, and approve it after reviewing the candidate.
3. Confirm both runs finish successfully. Under **Releases**, verify that
   `v<version>` is marked **Pre-release** and includes all four platform archives
   and the signed inventory/checksums described below, plus `release-index-entry.json`.
4. Open the [public Operator catalog](https://releases.intrusive.ai/docs/releases/operator_sandbox.json)
   and confirm the version has `tags: ["rc"]` with the same archive URLs and
   digests. It should remain hidden from the stable download listing. A dispatch
   acknowledgement alone does not confirm successful setup.
5. Finish the planned candidate tests and review its pull request into `main`.
   Wait for the candidate publish job to pass, then merge that PR. Approve the
   stable catalog job if prompted.
   Confirm the GitHub release is no longer a pre-release, its archives and tag
   are unchanged, and its catalog entry now has `tags: ["release"]`. Reload the
   [downloads website](https://releases.intrusive.ai/): the stable version should
   appear if it is among the first two entries labeled `release`. No site build
   or deployment is needed. Confirm prior catalog history is retained. A repeat publication of the same stable entry must be idempotent.

For an existing rolling candidate such as `candidate-v0.0.1`, first bring the
updated `main` into the candidate and let its normal candidate build publish the
new commit. Rerunning an old run executes the old workflow code. Do not manually
move an immutable or stable tag to install the publisher. The updated workflow
and bundle must be in the tagged source before a catalog retry can use them.

## Publish and promote a release

After one-time setup, follow this sequence for each version. These commands use
`1.2.3` as an example; choose your intended, not-yet-stable version.

### 1. Create and push the candidate

From a clean checkout with the release tooling on `main`:

```sh
git switch main
git pull --ff-only
git switch -c candidate-v1.2.3
git push --set-upstream origin candidate-v1.2.3
```

Merge feature PRs into that candidate or push trusted changes directly. Each push
replaces its rolling prerelease after successful tests, builds, and signing. Use
exactly `candidate-v1.2.3`, not an `-rc` suffix or shortened numeric version.

### 2. Wait for candidate publication and test its packages

1. Open **Actions → verify-host-candidates** and select the latest candidate push.
   Wait for its **publish** job and the linked catalog job to succeed. Approve
   the `production` deployment when prompted, within the 30-minute wait.
2. Open **Releases → Operator Sandbox 1.2.3**. Check **Pre-release**, tag `v1.2.3`,
   the candidate source commit and the complete asset list below.
3. Download the needed platform's archive, checksum file and detached checksum
   signature. Verify/install using [host distribution](../../docs/HOST_DISTRIBUTION.md)
   and your independently trusted public keyring.
4. Complete your candidate tests and record the results on the release PR. Check
   the [public catalog](https://releases.intrusive.ai/docs/releases/operator_sandbox.json)
   for `rc` and the same archive hashes/URLs. Candidates remain hidden from the
   website's stable download list.

Use fresh installation directories for successive builds of the same candidate
version. The installer rejects changed contents under an already installed version.
Running campaigns retain their previously installed binaries and frozen inputs.

### 3. Merge the candidate PR and confirm promotion

1. Bring `main` into the candidate if necessary and wait for that new candidate's
   publication and testing to finish. The merge into `main` must preserve the
   candidate's file tree.
2. Open a same-repository PR with **base: main** and **compare: candidate-v1.2.3**.
   Review it and merge only after the latest candidate publish job succeeds.
3. Open the merged PR's **verify-host-candidates** run and follow its **promote**
   job. Approve the resulting catalog deployment if required; wait for both jobs
   to finish successfully.
4. Confirm the GitHub release is stable, its tag still identifies the candidate
   commit, and its archives and signatures have unchanged hashes. Confirm its
   catalog entry is now `release` with the same artifact identities. Reload the
   [downloads website](https://releases.intrusive.ai/) and check its listing and links.
   Only the first two `release` entries are shown; older entries remain in the catalog.

The executable version remains `1.2.3` throughout. Merge, squash and rebase merges
are supported if the resulting tree matches the published candidate. A direct
local merge pushed to `main` has no merged-PR receipt and does not promote. Ordinary
`main` pushes verify code without rebuilding release packages. A stable version
cannot be replaced by candidate pushes; start a new version for later changes.
GitHub's Latest designation follows semantic version ordering, so an older
maintenance version does not deliberately replace a newer release.

## What is published

Each release includes four platform archives (`linux-amd64.tar.gz`,
`linux-arm64.tar.gz`, `darwin-amd64.tar.gz`, `darwin-arm64.tar.gz`), each containing the
executable, contract, configuration templates, documentation, SBOM, provenance and
signed manifest. Each has `<platform>-SHA256SUMS` and `<platform>-SHA256SUMS.asc`.
Verify those named checksum files before unpacking, as described in
[host distribution](../../docs/HOST_DISTRIBUTION.md).

`candidate.json` and its detached signature bind the four packages to their version,
source commit and shared contract. Release metadata records the complete signed-package asset
inventory and GitHub-reported SHA-256 digests for promotion checks.

`release-index-entry.json` is mutable catalog metadata, outside the signed package
inventory. It is regenerated from actual GitHub asset URLs and hashes, and its
label changes on promotion while the signed packages remain unchanged.

## Catalog publication and recovery

The parent release workflow uploads `release-index-entry.json`, dispatches
`.github/workflows/publish-catalog.yml` at the published `v*` tag, correlates the
resulting run, and waits for success. This dispatch stays in **this repository**;
it runs at the release tag to satisfy the AWS trust policy. The tagged job
independently rechecks the source, release state, artifact inventory, URLs, and
digests and validates the catalog entry before acquiring AWS credentials. It
then merges the entry using the committed local publisher. A dispatch
acknowledgement, skipped run, failed run, or timeout does not count as publication.

The public `/docs/releases/operator_sandbox.json` catalog retains candidates
and history. Candidates are readable but hidden from the website; only the first
two entries labeled exactly `release` are displayed. The publisher merges one
entry and refuses to demote or replace the artifacts of a final version. Never
upload a locally reconstructed full catalog. The publisher uses conditional S3
writes with bounded retries so a concurrent publication does not overwrite
another writer's changes. It checks stored bytes and headers, invalidates only
the catalog path, and verifies the public response.

If GitHub publication succeeds but catalog publication fails:

1. Open the catalog run linked in the parent publish/promote job and inspect its
   failed step. For AWS credential failures, recheck setup steps 5 and 6; for a
   waiting job, request the environment approval. For an unavailable action,
   recheck step 2. Fix metadata/source errors before retrying.
2. Confirm any timed-out or manually dispatched older catalog run has stopped.
   Do not repair the catalog concurrently with candidate replacement/promotion.
3. Rerun the failed parent job through **Actions → run → Re-run failed jobs**.
   If only catalog repair is needed, run the command below from a reviewed
   checkout, replacing the source placeholder with the full commit behind the
   published tag. Do not rebuild or re-sign stable artifacts.
4. After success, repeat the public catalog, hash, and website checks above.

```sh
gh api repos/intrusiveai/operator_sandbox/commits/v1.2.3 --jq .sha
```

Use the returned commit in this command:

```sh
GITHUB_REPOSITORY=intrusiveai/operator_sandbox \
  python3 scripts/publish_host_release.py catalog-sync \
  --tag v1.2.3 --source FULL_CANDIDATE_COMMIT_SHA
```

The command needs an authenticated GitHub token with Contents and Actions write
and Pull requests read for stable promotion checks.
Use the tagged candidate commit, not the `main` merge commit. A stable retry
revalidates the recorded promotion pull request and cannot revert the catalog to
`rc`. Publication waits up to 30 minutes, including environment approval; a known
run is cancelled on timeout. Confirm timed-out runs have stopped before retrying.

## Failure behavior and local verification

- Failed tests, builds or signing do not publish a candidate.
- An old run cannot replace a newer candidate branch head.
- Interrupted asset replacement leaves a draft; rerun the candidate workflow.
- Missing or changed assets, a changed tag, the wrong candidate head, or a
  different merge tree blocks promotion.
- A stable version cannot be replaced by a later candidate push; use a new version.
- Closed but unmerged pull requests, fork pull requests, ordinary `main` pushes,
  and direct local merges pushed to `main` do not promote.

Run `make test` locally to exercise rolling release replacement, failure recovery,
catalog derivation, candidate-to-stable metadata continuity, and promotion against
a simulated GitHub service. It also runs the local publisher's race tests, vet,
and publication tests using simulated S3 and local HTTP delivery. The integration
test carries Operator's generated four-platform entry through candidate
replacement, promotion, and retry with that actual publisher. Local tests do not
publish or configure GitHub, AWS, the releases service, or the public website.

## Updating the local publisher

`tools/release-publisher` is the complete catalog-only export from reviewed
releases commit `c651c5fa47346f1ca02abe33791667f838b59e03`.
`publisher-source.json` records the source revision and each file's SHA-256.
The private module name is only a local Go import namespace; publication runs
with `GOWORK=off`, `GOPROXY=off`, and `GOTOOLCHAIN=local` and fetches no private code.
The export includes its upstream reference documentation and workflow example;
this file remains the repository-specific maintainer guide. The publisher is
maintenance tooling, separate from the installed Operator binaries.

1. Obtain a complete catalog-only export for `operator_sandbox` from an
   authorized releases maintainer at an explicitly reviewed commit. The
   maintainer runs `scripts/export-publisher.py --revision <full-commit-sha>
   --product operator_sandbox --output <new-directory>` in that checkout.
   Never use a `--working-tree` export for a product release.
2. Review the full diff, including removed files, and replace the old bundle
   with the complete new export and its provenance manifest. Do not edit vendor
   files in place or install only the shell script. Adapt the workflow example
   while retaining Operator's actual-release and promotion checks.
3. Run `make test`. Go 1.26.5, Bash, jq, curl, and Python 3 are required locally;
   publication additionally needs AWS CLI v2 with conditional `put-object`.
4. Review and commit the bundle, workflow, and any updated setup instructions.
   Deliver them to both `main` and the next candidate before its tag is built.
   Confirm live OIDC and public delivery using step 7; local tests alone cannot
   establish deployment readiness.
